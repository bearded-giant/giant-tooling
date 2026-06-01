// Package projection drives the artifacts projection: it mirrors live_docs into
// the artifacts table and keeps embeddings fresh. It sits above artifacts +
// search to avoid the search->artifacts import cycle (artifacts must not import
// search), so this is the one place both are wired together.
package projection

import (
	"database/sql"
	"strings"

	"github.com/bearded-giant/giant-tooling/giantmem/internal/artifacts"
	"github.com/bearded-giant/giant-tooling/giantmem/internal/search"
)

// Stats reports what one Reconcile pass changed.
type Stats struct {
	artifacts.TableStats
	Embedded     int  `json:"embedded"`
	EmbedSkipped int  `json:"embed_skipped"`
	Embeddings   bool `json:"embeddings_enabled"`
}

// Reconcile is the full incremental engine: project live_docs into the
// artifacts table (derive + upsert + delete orphans + canonical backfill), then
// embed any changed bodies. Idempotent and cheap in steady state — derive is
// string parsing and embedding is body-hash guarded, so unchanged rows cost a
// PK lookup. Safe to run concurrently with peer live_docs writes (WAL +
// busy_timeout, short txns).
//
// embedder may be nil or a stub; in either case the embedding pass is skipped so
// the daemon never writes non-semantic vectors when no real backend is set.
func Reconcile(live *sql.DB, archiveBase string, embedder search.Embedder) (Stats, error) {
	var st Stats
	ts, err := artifacts.ReconcileTable(live, archiveBase)
	if err != nil {
		return st, err
	}
	st.TableStats = ts

	if !embeddingsEnabled(embedder) {
		return st, nil
	}
	st.Embeddings = true

	emb, skip, err := embedChanged(live, embedder)
	st.Embedded = emb
	st.EmbedSkipped = skip
	return st, err
}

func embeddingsEnabled(e search.Embedder) bool {
	if e == nil {
		return false
	}
	return !strings.HasPrefix(e.ModelName(), "stub:")
}

// embedChanged walks the .giantmem/ rows in live_docs and embeds only bodies
// whose hash differs from the stored embedding meta (or that have none yet).
func embedChanged(live *sql.DB, embedder search.Embedder) (embedded, skipped int, err error) {
	rows, err := live.Query(
		`SELECT path, content FROM live_docs WHERE instr(path, ?) > 0`, "/.giantmem/")
	if err != nil {
		return 0, 0, err
	}
	type row struct{ rel, body, id string }
	var work []row
	for rows.Next() {
		var abs, content string
		if err := rows.Scan(&abs, &content); err != nil {
			rows.Close()
			return embedded, skipped, err
		}
		rel, ok := artifacts.RelFromLivePath(abs)
		if !ok {
			continue
		}
		a, ok := artifacts.DeriveFromLiveDoc(rel, content, "", "", "")
		if !ok {
			continue
		}
		_, body, _ := artifacts.ParseFrontmatter(content)
		work = append(work, row{rel: rel, body: body, id: a.ID})
	}
	if cerr := rows.Err(); cerr != nil {
		rows.Close()
		return embedded, skipped, cerr
	}
	rows.Close()

	for _, w := range work {
		meta, merr := search.LoadEmbeddingMeta(live, w.id)
		if merr != nil {
			return embedded, skipped, merr
		}
		if meta != nil && meta.BodyHash == search.BodyHash(w.body) && meta.Dim == embedder.Dim() {
			skipped++
			continue
		}
		vec, eerr := embedder.Embed(w.body)
		if eerr != nil {
			return embedded, skipped, eerr
		}
		if _, eerr := search.WriteEmbedding(live, w.id, w.body, vec, embedder.ModelName()); eerr != nil {
			return embedded, skipped, eerr
		}
		embedded++
	}
	return embedded, skipped, nil
}

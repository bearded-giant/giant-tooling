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
	Embedded         int  `json:"embedded"`
	EmbedSkipped     int  `json:"embed_skipped"`
	EmbedReused      int  `json:"embed_reused"`
	EmbeddingsPruned int  `json:"embeddings_pruned"`
	Embeddings       bool `json:"embeddings_enabled"`
}

// Reconcile is the full incremental engine: project live_docs into the
// artifacts table (derive + upsert + delete orphans + canonical backfill), then
// embed any changed bodies. Idempotent; embedding is body-hash guarded. Safe to
// run concurrently with peer live_docs writes (WAL + busy_timeout, short txns).
//
// embedder may be nil or a stub; in either case the embedding pass is skipped so
// the daemon never writes non-semantic vectors when no real backend is set.
func Reconcile(live *sql.DB, archiveBase string, embedder search.Embedder) (Stats, error) {
	return reconcileCached(live, archiveBase, embedder, &artifacts.Cache{})
}

func reconcileCached(live *sql.DB, archiveBase string, embedder search.Embedder, cache *artifacts.Cache) (Stats, error) {
	var st Stats
	ts, err := artifacts.ReconcileTableCached(live, archiveBase, cache)
	if err != nil {
		return st, err
	}
	st.TableStats = ts

	// runs even with embeddings disabled: orphans from deleted artifacts still rank in KNN
	pruned, err := search.DeleteOrphanEmbeddings(live)
	if err != nil {
		return st, err
	}
	st.EmbeddingsPruned = pruned

	if !embeddingsEnabled(embedder) {
		return st, nil
	}
	st.Embeddings = true

	emb, skip, reused, err := embedChanged(live, embedder, cache)
	st.Embedded = emb
	st.EmbedSkipped = skip
	st.EmbedReused = reused
	return st, err
}

func embeddingsEnabled(e search.Embedder) bool {
	if e == nil {
		return false
	}
	return !strings.HasPrefix(e.ModelName(), "stub:")
}

// embedChanged embeds only winners whose body hash differs from the stored
// embedding meta (or that have none yet), reading content just for those.
func embedChanged(live *sql.DB, embedder search.Embedder, cache *artifacts.Cache) (embedded, skipped, reused int, err error) {
	type row struct {
		abs, hash, id, typ string
		mtime              int64
	}
	// Collapse branch/worktree siblings (same projectedID, different body) to one
	// winner per id — newest mtime, tie-break highest path — matching
	// ReconcileTable's choice. Without this, siblings fight over the single
	// embedding slot and re-embed every pass, spinning the daemon loop.
	winners := map[string]row{}
	for abs, c := range cache.Rows() {
		if !c.OK {
			continue
		}
		r := row{abs: abs, hash: c.BodyHash, id: c.A.ID, typ: c.A.Type, mtime: c.Mtime}
		if cur, ok := winners[r.id]; !ok || r.mtime > cur.mtime || (r.mtime == cur.mtime && r.abs > cur.abs) {
			winners[r.id] = r
		}
	}

	// one query, not one per artifact: per-row lookups cost a lock round-trip each
	type head struct {
		hash string
		dim  int
	}
	heads := make(map[string]head, len(winners))
	mrows, err := live.Query(`SELECT artifact_id, body_hash, dim FROM artifact_embedding_meta WHERE ord = 0`)
	if err != nil {
		return 0, 0, 0, err
	}
	for mrows.Next() {
		var id string
		var h head
		if err := mrows.Scan(&id, &h.hash, &h.dim); err != nil {
			mrows.Close()
			return 0, 0, 0, err
		}
		heads[id] = h
	}
	if err := mrows.Err(); err != nil {
		mrows.Close()
		return 0, 0, 0, err
	}
	mrows.Close()

	for _, w := range winners {
		if h, ok := heads[w.id]; ok && h.hash == w.hash && h.dim == embedder.Dim() {
			skipped++
			continue
		}
		var content string
		if err := live.QueryRow(`SELECT content FROM live_docs WHERE path = ?`, w.abs).Scan(&content); err != nil {
			if err == sql.ErrNoRows {
				continue
			}
			return embedded, skipped, reused, err
		}
		_, body, _ := artifacts.ParseFrontmatter(content)
		chunks := search.ChunkBodyFor(w.typ, body)
		vecs, n, eerr := search.EmbedChunks(live, embedder, w.id, chunks)
		reused += n
		if eerr != nil {
			return embedded, skipped, reused, eerr
		}
		changed, eerr := search.WriteChunkEmbeddings(live, w.id, body, chunks, vecs, embedder.ModelName())
		if eerr != nil {
			return embedded, skipped, reused, eerr
		}
		if changed {
			embedded++
		} else {
			skipped++
		}
	}
	return embedded, skipped, reused, nil
}

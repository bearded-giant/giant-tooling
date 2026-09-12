package search

import (
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/bearded-giant/giant-tooling/giantmem/internal/artifacts"
	"github.com/bearded-giant/giant-tooling/giantmem/internal/db"
)

func insertLiveDoc(t *testing.T, d *sql.DB, path, content string) {
	t.Helper()
	_, err := d.Exec(
		`INSERT INTO live_docs(path, project, worktree_path, feature, dir_type,
            session_id, git_sha, mtime, ingested_at, content)
         VALUES (?,?,?,?,?,?,?,?,?,?)`,
		path, "r", "/r", "", "research", "", "", int64(1), "2026-09-01T00:00:00Z", content)
	if err != nil {
		t.Fatalf("insert %s: %v", path, err)
	}
}

// The FTS arm used to be a substring check on id/feature/name, so a prompt-length
// query never matched anything and the 0.5 weight was dead. It is now bm25 over
// live_docs_fts bodies via the worktree/.giantmem/path join.
func TestHybrid_FTSRanksByBodyBM25(t *testing.T) {
	d, err := db.Open(filepath.Join(t.TempDir(), "live.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer d.Close()

	insertLiveDoc(t, d, "/r/.giantmem/research/replica.md",
		"---\ntype: research\n---\nPostgres replica lag analysis: the read replica lags behind under bulk writes.")
	insertLiveDoc(t, d, "/r/.giantmem/research/buttons.md",
		"---\ntype: research\n---\nFrontend button color tokens and spacing scale.")

	cands := []artifacts.Artifact{
		{ID: "r:research:buttons", Type: "research", Path: "research/buttons.md", Worktree: "/r", Updated: "2026-09-01"},
		{ID: "r:research:replica", Type: "research", Path: "research/replica.md", Worktree: "/r", Updated: "2026-09-01"},
	}
	w := HybridWeights{FTS: 1}

	res, err := Hybrid(d, "why does the replica lag during bulk writes", nil, cands, w, 10)
	if err != nil {
		t.Fatalf("hybrid: %v", err)
	}
	if len(res) != 2 || res[0].Artifact.ID != "r:research:replica" {
		t.Fatalf("expected replica doc first, got %+v", res)
	}
	if res[0].FTSScore != 1.0 {
		t.Fatalf("best hit should normalize to 1.0, got %v", res[0].FTSScore)
	}
	if res[1].FTSScore != 0 {
		t.Fatalf("non-matching body should score 0, got %v", res[1].FTSScore)
	}

	// nil db: no FTS arm, no error
	res, err = Hybrid(nil, "replica lag", nil, cands, w, 10)
	if err != nil || len(res) != 2 || res[0].FTSScore != 0 {
		t.Fatalf("nil live should yield zero fts scores without error: %v %+v", err, res)
	}
}

func TestFTSBodyQuery(t *testing.T) {
	got := ftsBodyQuery("Why does the replica lag? replica LAG!")
	want := `content: ("why" OR "does" OR "the" OR "replica" OR "lag")`
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
	if ftsBodyQuery("(hub OR spoke) NOT legacy") != "(hub OR spoke) NOT legacy" {
		t.Fatal("FTS5 syntax must pass through untouched")
	}
	if ftsBodyQuery("a of") != "" {
		t.Fatal("all-short tokens should yield an empty query")
	}
}

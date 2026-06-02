package projection

import (
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/bearded-giant/giant-tooling/giantmem/internal/db"
)

type fakeEmbedder struct{ dim int }

func (f fakeEmbedder) Embed(string) ([]float32, error) {
	v := make([]float32, f.dim)
	for i := range v {
		v[i] = 0.1
	}
	return v, nil
}
func (f fakeEmbedder) Dim() int          { return f.dim }
func (f fakeEmbedder) ModelName() string { return "fake:test" } // non-stub => embeddings enabled
func (f fakeEmbedder) Close() error      { return nil }

type stubLike struct{ fakeEmbedder }

func (s stubLike) ModelName() string { return "stub:test" }

func newLive(t *testing.T) (*sql.DB, string) {
	t.Helper()
	base := t.TempDir()
	d, err := db.Open(filepath.Join(base, "live.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	return d, base
}

func insertDoc(t *testing.T, d *sql.DB, abs, content string) {
	t.Helper()
	_, err := d.Exec(
		`INSERT INTO live_docs(path, project, worktree_path, feature, dir_type,
            session_id, git_sha, mtime, ingested_at, content)
         VALUES (?,?,?,?,?,?,?,?,?,?)
         ON CONFLICT(path) DO UPDATE SET content=excluded.content`,
		abs, "repo", "/r", "", "", "", "", int64(1), "2026-06-01T00:00:00Z", content)
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
}

func TestReconcile_EmbedsChangedThenSkips(t *testing.T) {
	d, base := newLive(t)
	insertDoc(t, d, "/r/.giantmem/features/foo/proposal.md", "---\nstatus: ready\n---\nbody one")

	st, err := Reconcile(d, base, fakeEmbedder{768})
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if !st.Embeddings {
		t.Fatal("embeddings should be enabled for a non-stub embedder")
	}
	if st.Embedded != 1 {
		t.Fatalf("embedded = %d, want 1", st.Embedded)
	}
	if st.Upserted != 1 {
		t.Fatalf("upserted = %d, want 1", st.Upserted)
	}

	// unchanged body => embed skipped on the next pass.
	st2, err := Reconcile(d, base, fakeEmbedder{768})
	if err != nil {
		t.Fatalf("reconcile 2: %v", err)
	}
	if st2.Embedded != 0 || st2.EmbedSkipped != 1 {
		t.Fatalf("2nd pass embedded=%d skipped=%d, want 0/1", st2.Embedded, st2.EmbedSkipped)
	}

	// changed body => re-embedded.
	insertDoc(t, d, "/r/.giantmem/features/foo/proposal.md", "---\nstatus: ready\n---\nbody two changed")
	st3, err := Reconcile(d, base, fakeEmbedder{768})
	if err != nil {
		t.Fatalf("reconcile 3: %v", err)
	}
	if st3.Embedded != 1 {
		t.Fatalf("3rd pass embedded = %d, want 1 (body changed)", st3.Embedded)
	}
}

func TestReconcile_StubEmbedderSkipsEmbedding(t *testing.T) {
	d, base := newLive(t)
	insertDoc(t, d, "/r/.giantmem/features/foo/proposal.md", "body")

	st, err := Reconcile(d, base, stubLike{fakeEmbedder{768}})
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if st.Embeddings {
		t.Error("stub embedder must NOT enable embeddings (no junk vectors)")
	}
	if st.Embedded != 0 {
		t.Errorf("embedded = %d, want 0 for stub", st.Embedded)
	}
	if st.Upserted != 1 {
		t.Errorf("upserted = %d, want 1 (table still reconciled)", st.Upserted)
	}
}

func TestReconcile_NilEmbedderTableOnly(t *testing.T) {
	d, base := newLive(t)
	insertDoc(t, d, "/r/.giantmem/features/foo/proposal.md", "body")
	st, err := Reconcile(d, base, nil)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if st.Embeddings || st.Embedded != 0 {
		t.Error("nil embedder must skip embedding")
	}
	if st.Upserted != 1 {
		t.Errorf("upserted = %d, want 1", st.Upserted)
	}
}

func insertDocWT(t *testing.T, d *sql.DB, abs, worktree, content string, mtime int64) {
	t.Helper()
	_, err := d.Exec(
		`INSERT INTO live_docs(path, project, worktree_path, feature, dir_type,
            session_id, git_sha, mtime, ingested_at, content)
         VALUES (?,?,?,?,?,?,?,?,?,?)
         ON CONFLICT(path) DO UPDATE SET content=excluded.content, mtime=excluded.mtime`,
		abs, "repo", worktree, "", "", "", "", mtime, "2026-06-01T00:00:00Z", content)
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
}

func TestReconcile_MultiWorktreeCollisionConvergesEmbed(t *testing.T) {
	d, base := newLive(t)

	// two worktrees, same repo+rel => one projectedID, two different bodies.
	// They must not fight over the single embedding slot across passes.
	insertDocWT(t, d, "/wt-a/.giantmem/plans/current.md", "/wt-a", "body A older", 1)
	insertDocWT(t, d, "/wt-b/.giantmem/plans/current.md", "/wt-b", "body B newest", 2)

	st, err := Reconcile(d, base, fakeEmbedder{768})
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if st.Upserted != 1 {
		t.Fatalf("upserted = %d, want 1 (siblings collapse)", st.Upserted)
	}
	if st.Embedded != 1 {
		t.Fatalf("embedded = %d, want 1 (one winner embedded)", st.Embedded)
	}

	st2, err := Reconcile(d, base, fakeEmbedder{768})
	if err != nil {
		t.Fatalf("reconcile 2: %v", err)
	}
	if st2.Embedded != 0 {
		t.Errorf("2nd pass embedded = %d, want 0 (collision converged, no daemon loop)", st2.Embedded)
	}
}

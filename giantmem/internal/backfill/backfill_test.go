package backfill

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/bearded-giant/giant-tooling/giantmem/internal/db"
)

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

func writeFile(t *testing.T, p, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
}

func TestRunOnWorkspace_IndexesAllNonEmptyFiles(t *testing.T) {
	live, base := newLive(t)
	repo := filepath.Join(t.TempDir(), "myrepo")
	ws := filepath.Join(repo, ".giantmem")

	writeFile(t, filepath.Join(ws, "plans", "current.md"), "# plan body")
	writeFile(t, filepath.Join(ws, "features", "foo", "proposal.md"), "---\nstatus: ready\n---\nbody")
	writeFile(t, filepath.Join(ws, "features", "foo", "facts.md"), "facts body")
	writeFile(t, filepath.Join(ws, "domains", "core.json"), `{"domain":"core"}`)
	writeFile(t, filepath.Join(ws, "filebox", "kubectl.txt"), "kubectl get pods")

	// non-eligible
	writeFile(t, filepath.Join(ws, "features", "foo", "notes.md"), "") // empty -> skip
	writeFile(t, filepath.Join(ws, ".giantmem-index"), "ignored")
	writeFile(t, filepath.Join(ws, ".DS_Store"), "junk")
	writeFile(t, filepath.Join(ws, "research", ".hidden.md"), "hidden")

	st, err := RunOnWorkspace(live, base, ws)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if st.Upserted != 5 {
		t.Errorf("upserted = %d, want 5; stats=%+v", st.Upserted, st)
	}
	if st.Empty != 1 {
		t.Errorf("empty = %d, want 1", st.Empty)
	}

	var count int
	if err := live.QueryRow("SELECT COUNT(*) FROM live_docs").Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 5 {
		t.Errorf("live_docs count = %d, want 5", count)
	}
}

func TestRunOnWorkspace_Idempotent(t *testing.T) {
	live, base := newLive(t)
	repo := filepath.Join(t.TempDir(), "repo2")
	ws := filepath.Join(repo, ".giantmem")
	writeFile(t, filepath.Join(ws, "notes.md"), "hello")

	st1, err := RunOnWorkspace(live, base, ws)
	if err != nil {
		t.Fatalf("run1: %v", err)
	}
	if st1.Upserted != 1 {
		t.Fatalf("first pass upserted = %d, want 1", st1.Upserted)
	}

	st2, err := RunOnWorkspace(live, base, ws)
	if err != nil {
		t.Fatalf("run2: %v", err)
	}
	if st2.Upserted != 0 {
		t.Errorf("second pass upserted = %d, want 0 (idempotent)", st2.Upserted)
	}
	if st2.Skipped != 1 {
		t.Errorf("second pass skipped = %d, want 1", st2.Skipped)
	}
}

func TestRunOnWorkspace_DerivesFeatureAndDirType(t *testing.T) {
	live, base := newLive(t)
	repo := filepath.Join(t.TempDir(), "r")
	ws := filepath.Join(repo, ".giantmem")
	abs := filepath.Join(ws, "features", "alpha", "tasks.md")
	writeFile(t, abs, "tasks body")

	if _, err := RunOnWorkspace(live, base, ws); err != nil {
		t.Fatal(err)
	}

	var feature, dirType string
	if err := live.QueryRow("SELECT feature, dir_type FROM live_docs WHERE path = ?", abs).
		Scan(&feature, &dirType); err != nil {
		t.Fatalf("read: %v", err)
	}
	if feature != "alpha" {
		t.Errorf("feature = %q, want alpha", feature)
	}
	if dirType != "features" {
		t.Errorf("dir_type = %q, want features", dirType)
	}
}

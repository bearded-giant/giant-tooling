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

func TestRunOnWorkspace_ExtraRootsMarker(t *testing.T) {
	live, base := newLive(t)
	repo := filepath.Join(t.TempDir(), "docrepo")
	ws := filepath.Join(repo, ".giantmem")

	writeFile(t, filepath.Join(ws, "notes.md"), "giantmem note")
	writeFile(t, filepath.Join(repo, "README.md"), "# readme")
	writeFile(t, filepath.Join(repo, "SOURCE.md"), "# source of truth")
	writeFile(t, filepath.Join(repo, "research", "00.md"), "# research")
	writeFile(t, filepath.Join(repo, "archive", "old.md"), "# archived")
	writeFile(t, filepath.Join(repo, "main.go"), "package main")         // not .md -> skip
	writeFile(t, filepath.Join(repo, ".obsidian", "cfg.md"), "obsidian") // dotdir -> skip
	writeFile(t, filepath.Join(repo, ".git", "HEAD"), "ref: x")          // dotdir -> skip

	// no marker yet: only the .giantmem/ file indexes
	st, err := RunOnWorkspace(live, base, ws)
	if err != nil {
		t.Fatalf("run no-marker: %v", err)
	}
	if st.Upserted != 1 {
		t.Fatalf("no marker upserted = %d, want 1 (only .giantmem)", st.Upserted)
	}

	// opt in via marker, fresh db so counts are clean
	live2, base2 := newLive(t)
	writeFile(t, filepath.Join(ws, extraRootsMarker), "# index repo docs\n*.md\nresearch/\n")
	st2, err := RunOnWorkspace(live2, base2, ws)
	if err != nil {
		t.Fatalf("run marker: %v", err)
	}
	// notes.md + README + SOURCE + research/00 + archive/old = 5
	if st2.Upserted != 5 {
		t.Fatalf("marker upserted = %d, want 5; stats=%+v", st2.Upserted, st2)
	}

	var dirType string
	if err := live2.QueryRow("SELECT dir_type FROM live_docs WHERE path = ?",
		filepath.Join(repo, "README.md")).Scan(&dirType); err != nil {
		t.Fatalf("read README dir_type: %v", err)
	}
	if dirType != "repo-doc" {
		t.Errorf("README dir_type = %q, want repo-doc", dirType)
	}

	var n int
	if err := live2.QueryRow("SELECT COUNT(*) FROM live_docs WHERE path = ?",
		filepath.Join(repo, "main.go")).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("main.go indexed (%d), want 0 — not matched by *.md", n)
	}
}

func TestMatchesAny(t *testing.T) {
	pats := []string{"*.md", "research/", "docs/*.txt"}
	cases := []struct {
		rel, base string
		want      bool
	}{
		{"README.md", "README.md", true},         // basename glob
		{"deep/nested/x.md", "x.md", true},       // basename glob is repo-wide
		{"research/01.md", "01.md", true},        // dir prefix (and *.md)
		{"research/sub/02.txt", "02.txt", true},  // dir prefix matches subtree
		{"docs/note.txt", "note.txt", true},      // path glob
		{"docs/sub/note.txt", "note.txt", false}, // path glob is single-level
		{"main.go", "main.go", false},            // no match
	}
	for _, c := range cases {
		if got := matchesAny(pats, c.rel, c.base); got != c.want {
			t.Errorf("matchesAny(%q,%q) = %v, want %v", c.rel, c.base, got, c.want)
		}
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

package artifacts

import (
	"os"
	"path/filepath"
	"testing"
)

func TestBody_DiskFirst(t *testing.T) {
	wt := t.TempDir()
	rel := "features/foo/proposal.md"
	abs := filepath.Join(wt, ".giantmem", rel)
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(abs, []byte("disk body"), 0o644); err != nil {
		t.Fatal(err)
	}
	d := newLiveDB(t)
	insertLiveDoc(t, d, abs, "repo", wt, "stale db body", 1)

	got, err := Body(d, Artifact{ID: "x", Path: rel, Worktree: wt})
	if err != nil {
		t.Fatal(err)
	}
	if got != "disk body" {
		t.Errorf("disk-first: got %q want %q", got, "disk body")
	}
}

func TestBody_DBFallbackWhenWorktreeGone(t *testing.T) {
	// worktree dir never created on disk — simulates a removed worktree
	wt := filepath.Join(t.TempDir(), "removed-worktree")
	rel := "features/foo/facts.md"
	abs := filepath.Join(wt, ".giantmem", rel)
	d := newLiveDB(t)
	insertLiveDoc(t, d, abs, "repo", wt, "durable db body", 1)

	got, err := Body(d, Artifact{ID: "x", Path: rel, Worktree: wt})
	if err != nil {
		t.Fatal(err)
	}
	if got != "durable db body" {
		t.Errorf("db-fallback: got %q want %q", got, "durable db body")
	}
}

func TestBody_ErrorsWhenNeitherSource(t *testing.T) {
	wt := filepath.Join(t.TempDir(), "gone")
	d := newLiveDB(t)
	if _, err := Body(d, Artifact{ID: "x", Path: "features/foo/facts.md", Worktree: wt}); err == nil {
		t.Error("expected error when file absent from disk and live_docs")
	}
}

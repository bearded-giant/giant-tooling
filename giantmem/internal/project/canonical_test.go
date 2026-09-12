package project

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCanonicalize(t *testing.T) {
	base := t.TempDir()
	if err := os.MkdirAll(filepath.Join(base, "cc-wt"), 0o755); err != nil {
		t.Fatal(err)
	}
	canonicalCache = map[string]string{}
	cases := []struct{ in, want string }{
		{"cc", "cc-wt"},
		{"cc-wt", "cc-wt"},
		{"dev/ai/chat-orchestrator", "chat-orchestrator"},
		{"dev/python/cc", "cc-wt"},
		{"frost", "frost"},
	}
	for _, c := range cases {
		if got := Canonicalize(c.in, base); got != c.want {
			t.Errorf("Canonicalize(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestDetect(t *testing.T) {
	base := t.TempDir()
	root := t.TempDir()
	repo := filepath.Join(root, "giant-tooling")
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(repo, "giantmem")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := Detect(sub, base).Project; got != "giant-tooling" {
		t.Errorf("regular repo: got %q", got)
	}

	wt := filepath.Join(root, "cc-wt", "stage")
	if err := os.MkdirAll(wt, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wt, ".git"), []byte("gitdir: /x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := Detect(wt, base).Project; got != "cc-wt" {
		t.Errorf("bare worktree: got %q", got)
	}
}

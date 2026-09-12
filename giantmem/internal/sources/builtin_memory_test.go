package sources

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMemoryMDSource_EmitsHarnessMemoryFiles(t *testing.T) {
	home, _ := os.UserHomeDir()
	projects := t.TempDir()
	slug := strings.ReplaceAll(home, "/", "-") + "-dev-ai-hyphae"
	memDir := filepath.Join(projects, slug, "memory")
	if err := os.MkdirAll(memDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(memDir, "fork-decision.md"), []byte("---\nname: x\n---\nno fork\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// a project without a memory dir contributes nothing
	_ = os.MkdirAll(filepath.Join(projects, "-other-project"), 0o755)

	src := &memoryMDSource{cfg: SourceConfig{Name: "memory-md", Kind: "builtin", Enabled: true}}
	docCh, errCh := src.Emit(context.Background(), EmitOptions{ClaudeProjects: projects})
	var docs []Doc
	for d := range docCh {
		docs = append(docs, d)
	}
	for e := range errCh {
		if e != nil {
			t.Fatalf("emit error: %v", e)
		}
	}
	if len(docs) != 1 {
		t.Fatalf("docs = %d, want 1", len(docs))
	}
	d := docs[0]
	if d.Project != "ai-hyphae" || d.SourceType != "memory" || d.DirType != "memory" || !d.IsLatest {
		t.Fatalf("unexpected doc: %+v", d)
	}
	if len(d.Timestamp) != 15 || d.Timestamp[8] != '_' {
		t.Fatalf("timestamp should be 20060102_150405, got %q", d.Timestamp)
	}
	if !strings.Contains(d.Content, "no fork") {
		t.Fatalf("content missing body: %q", d.Content)
	}

	// project filter is a case-insensitive substring match, like sessions
	docCh, _ = src.Emit(context.Background(), EmitOptions{ClaudeProjects: projects, Project: "frost"})
	n := 0
	for range docCh {
		n++
	}
	if n != 0 {
		t.Fatalf("project filter should exclude, got %d", n)
	}
}

func TestMemoryProjectName(t *testing.T) {
	if got := MemoryProjectName("/x/projects/-foo-bar/memory/a.md"); got != "foo-bar" {
		t.Fatalf("got %q", got)
	}
	if got := MemoryProjectName("/x/projects/-/memory/a.md"); got != "memory" {
		t.Fatalf("empty slug should fall back to memory, got %q", got)
	}
}

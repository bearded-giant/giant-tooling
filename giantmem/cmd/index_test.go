package cmd

import (
	"path/filepath"
	"testing"

	"github.com/bearded-giant/giant-tooling/giantmem/internal/db"
)

func TestRenameSessionsFromCwd(t *testing.T) {
	a, err := db.Open(filepath.Join(t.TempDir(), "archives.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	ins := func(project, src, fp, cwd string) {
		if _, err := a.Exec(`INSERT INTO documents (project, canonical_project, timestamp, source_type, filepath, filename, cwd, indexed_at)
		                     VALUES (?, ?, '20261031_000000', ?, ?, 'x', ?, datetime('now'))`, project, project, src, fp, cwd); err != nil {
			t.Fatal(err)
		}
	}
	ins("dev/claude/code-config", "session", "/gone/a.jsonl", "/nowhere/dev/claude-code-config")
	ins("dev/ai/inv-wt-main", "subagent", "/gone/b.jsonl", "/nowhere/dev/ai/inv-wt/main")
	ins("dev/foo/bar", "session", "/gone/c.jsonl", "")
	ins("dev/claude/code-config", "workspace", "/ws/d.md", "/nowhere/dev/claude-code-config")

	if n, err := renameSessionsFromCwd(a, true); err != nil || n != 2 {
		t.Fatalf("dry run = %d, %v; want 2", n, err)
	}
	var p string
	a.QueryRow(`SELECT project FROM documents WHERE filepath='/gone/a.jsonl'`).Scan(&p)
	if p != "dev/claude/code-config" {
		t.Fatalf("dry run wrote: %q", p)
	}
	if n, err := renameSessionsFromCwd(a, false); err != nil || n != 2 {
		t.Fatalf("rename = %d, %v; want 2", n, err)
	}
	want := map[string][2]string{
		"/gone/a.jsonl": {"claude-code-config", "claude-code-config"},
		"/gone/b.jsonl": {"inv-wt-main", "inv-wt-main"},
		"/gone/c.jsonl": {"dev/foo/bar", "dev/foo/bar"},
		"/ws/d.md":      {"dev/claude/code-config", "dev/claude/code-config"},
	}
	for fp, w := range want {
		var proj, canon string
		a.QueryRow(`SELECT project, canonical_project FROM documents WHERE filepath=?`, fp).Scan(&proj, &canon)
		if proj != w[0] || canon != w[1] {
			t.Errorf("%s = %q/%q, want %q/%q", fp, proj, canon, w[0], w[1])
		}
	}
	if n, _ := renameSessionsFromCwd(a, false); n != 0 {
		t.Errorf("second pass renamed %d, want 0", n)
	}
}

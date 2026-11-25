package cmd

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
)

func writeProjectSession(t *testing.T, root, slug, cwd string) string {
	t.Helper()
	dir := filepath.Join(root, slug)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	line := `{"type":"user","cwd":"` + cwd + `"}` + "\n"
	if err := os.WriteFile(filepath.Join(dir, "s.jsonl"), []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func orphanNames(rows []orphanRow) []string {
	out := []string{}
	for _, r := range rows {
		out = append(out, r.Dir)
	}
	sort.Strings(out)
	return out
}

func TestFindOrphansUnder(t *testing.T) {
	root := t.TempDir()
	writeProjectSession(t, root, "live", t.TempDir())
	writeProjectSession(t, root, "wt-feat", "/gone/repo-wt/feat")
	writeProjectSession(t, root, "wt-feat-sub", "/gone/repo-wt/feat/apps/x")
	writeProjectSession(t, root, "wt-feat2", "/gone/repo-wt/feat2")
	writeProjectSession(t, root, "other", "/gone/elsewhere")
	if err := os.MkdirAll(filepath.Join(root, "no-sessions", "memory"), 0o755); err != nil {
		t.Fatal(err)
	}

	all, unknown, err := findOrphans(root, "")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := orphanNames(all), []string{"other", "wt-feat", "wt-feat-sub", "wt-feat2"}; !reflect.DeepEqual(got, want) {
		t.Errorf("unscoped orphans = %v, want %v", got, want)
	}
	if got, want := orphanNames(unknown), []string{"no-sessions"}; !reflect.DeepEqual(got, want) {
		t.Errorf("unscoped unknown = %v, want %v", got, want)
	}

	// a sibling worktree whose name shares the prefix (feat2) must stay out
	scoped, unknown, err := findOrphans(root, "/gone/repo-wt/feat/")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := orphanNames(scoped), []string{"wt-feat", "wt-feat-sub"}; !reflect.DeepEqual(got, want) {
		t.Errorf("scoped orphans = %v, want %v", got, want)
	}
	if len(unknown) != 0 {
		t.Errorf("scoped unknown = %v, want none", orphanNames(unknown))
	}
}

func TestRemoveProjectDirKeepsMemory(t *testing.T) {
	root := t.TempDir()

	withMemory := writeProjectSession(t, root, "p", "/gone/p")
	if err := os.MkdirAll(filepath.Join(withMemory, "memory"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(withMemory, "memory", "MEMORY.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(withMemory, "sid", "subagents"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := removeProjectDir(withMemory); err != nil {
		t.Fatal(err)
	}
	ents, _ := os.ReadDir(withMemory)
	if len(ents) != 1 || ents[0].Name() != "memory" {
		t.Errorf("dir with memory: left %v, want only memory", ents)
	}
	if _, err := os.Stat(filepath.Join(withMemory, "memory", "MEMORY.md")); err != nil {
		t.Errorf("memory file gone: %v", err)
	}

	plain := writeProjectSession(t, root, "q", "/gone/q")
	if err := os.MkdirAll(filepath.Join(plain, "memory"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := removeProjectDir(plain); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(plain); !os.IsNotExist(err) {
		t.Errorf("dir with empty memory should be removed, stat err = %v", err)
	}
}

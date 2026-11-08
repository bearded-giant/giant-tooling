package main

import (
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/bearded-giant/giant-tooling/giantmem/internal/db"
)

func write(t *testing.T, p, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestUploadFilesGuardAndConflicts(t *testing.T) {
	root := t.TempDir()
	dest := filepath.Join(root, "repo/.giantmem/features/x")
	outside := filepath.Join(root, "outside")
	src := filepath.Join(root, "in/tasks.md")
	write(t, src, "v1")
	write(t, filepath.Join(outside, "keep.txt"), "")
	if err := os.MkdirAll(dest, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "repo/.giantmem/escape")); err != nil {
		t.Fatal(err)
	}
	a := &App{}

	for _, bad := range []string{
		outside,
		"repo/.giantmem",
		root + "/repo/.giantmem/features/../features/x",
		filepath.Join(root, "repo/.giantmem/escape"),
		filepath.Join(root, "repo/.giantmem/missing"),
	} {
		if _, err := a.UploadFiles(bad, []string{src}, ""); err == nil {
			t.Fatalf("upload into %s should be refused", bad)
		}
	}
	if entries, _ := os.ReadDir(outside); len(entries) != 1 {
		t.Fatalf("outside dir got written: %v", entries)
	}
	if _, err := a.UploadFiles(dest, []string{filepath.Dir(src)}, ""); err == nil {
		t.Fatal("directory source should be refused")
	}
	if _, err := a.UploadFiles(dest, []string{src}, "bogus"); err == nil {
		t.Fatal("unknown conflict mode should be refused")
	}

	res, err := a.UploadFiles(dest, []string{src}, "")
	if err != nil || len(res.Written) != 1 || read(t, filepath.Join(dest, "tasks.md")) != "v1" {
		t.Fatalf("first upload: %+v %v", res, err)
	}

	write(t, src, "v2")
	res, err = a.UploadFiles(dest, []string{src}, "")
	if err != nil || len(res.Conflicts) != 1 || len(res.Written) != 0 || read(t, filepath.Join(dest, "tasks.md")) != "v1" {
		t.Fatalf("conflict must write nothing: %+v %v", res, err)
	}
	res, err = a.UploadFiles(dest, []string{src}, "skip")
	if err != nil || len(res.Skipped) != 1 || read(t, filepath.Join(dest, "tasks.md")) != "v1" {
		t.Fatalf("skip: %+v %v", res, err)
	}
	res, err = a.UploadFiles(dest, []string{src}, "keep")
	if err != nil || len(res.Written) != 1 || read(t, filepath.Join(dest, "tasks (1).md")) != "v2" {
		t.Fatalf("keep both: %+v %v", res, err)
	}
	res, err = a.UploadFiles(dest, []string{src}, "overwrite")
	if err != nil || read(t, filepath.Join(dest, "tasks.md")) != "v2" {
		t.Fatalf("overwrite: %+v %v", res, err)
	}
	entries, _ := os.ReadDir(dest)
	for _, e := range entries {
		if e.Name()[0] == '.' {
			t.Fatalf("temp file left behind: %s", e.Name())
		}
	}
}

func TestWorkspaceFilesDiskAndRemoved(t *testing.T) {
	root := t.TempDir()
	live, err := db.Open(filepath.Join(root, "live.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer live.Close()
	disk := filepath.Join(root, "repo")
	gone := filepath.Join(root, "repo-wt/old")
	write(t, filepath.Join(disk, ".giantmem/plans/current.md"), "plan")
	write(t, filepath.Join(disk, ".giantmem/research/diagram.png"), "\x89PNG")
	if err := os.MkdirAll(filepath.Join(disk, ".giantmem/reviews"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, r := range []struct{ path, sid string }{
		{filepath.Join(disk, ".giantmem/plans/current.md"), "s1"},
		{filepath.Join(gone, ".giantmem/features/f/proposal.md"), "s2"},
	} {
		if _, err := live.Exec(`INSERT INTO live_docs(path, project, worktree_path, feature, dir_type, session_id, git_sha, mtime, ingested_at, content)
		                        VALUES (?, 'p', '', '', '', ?, '', 5, '2026-01-01T00:00:00Z', 'body')`, r.path, r.sid); err != nil {
			t.Fatal(err)
		}
	}
	a := &App{live: live}
	nodes, err := a.WorkspaceFiles([]string{disk, gone})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]WsNode{}
	for _, n := range nodes {
		got[n.Ws+"|"+n.Rel] = n
	}
	if n := got[disk+"|plans/current.md"]; !n.Indexed || n.SessionID != "s1" || n.Dir {
		t.Fatalf("indexed disk file: %+v", n)
	}
	if n := got[disk+"|research/diagram.png"]; n.Indexed || n.Size != 4 {
		t.Fatalf("unindexed disk file: %+v", n)
	}
	if n := got[disk+"|reviews"]; !n.Dir {
		t.Fatalf("empty dir missing: %+v", got)
	}
	if n := got[gone+"|features/f/proposal.md"]; !n.Indexed || n.SessionID != "s2" || n.Size != 4 {
		t.Fatalf("removed workspace file: %+v", n)
	}
}

func TestFeatureStatusesShapes(t *testing.T) {
	for name, body := range map[string]string{
		"top":  `{"a": {"name": "a", "status": "complete"}, "b": {"status": "in_progress"}}`,
		"map":  `{"features": {"a": {"status": "complete"}, "b": {"name": "b", "status": "in_progress"}}}`,
		"list": `{"features": [{"name": "a", "status": "complete"}, {"name": "b", "status": "in_progress"}]}`,
	} {
		g := filepath.Join(t.TempDir(), ".giantmem")
		write(t, filepath.Join(g, "features/features.json"), body)
		st, active := featureStatuses(g)
		if st["a"] != "complete" || st["b"] != "in_progress" || active != "b" {
			t.Fatalf("%s: %v active=%q", name, st, active)
		}
	}
	g := filepath.Join(t.TempDir(), ".giantmem")
	write(t, filepath.Join(g, "features/features.json"), `{"active": "a", "features": {"a": {"status": "pending"}}}`)
	if _, active := featureStatuses(g); active != "a" {
		t.Fatalf("explicit active ignored: %q", active)
	}
}

func TestWorkspaceProjectsWithRemovedWorktree(t *testing.T) {
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("GIANTMEM_DEV_ROOTS", home)
	container := filepath.Join(home, "dev/x-wt")
	for _, d := range []string{".bare", "main/.giantmem", "feat/y/.giantmem"} {
		if err := os.MkdirAll(filepath.Join(container, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write(t, filepath.Join(home, "dev/solo/.git"), "")
	write(t, filepath.Join(home, "dev/solo/.giantmem/plans/current.md"), "x")
	live, err := db.Open(filepath.Join(t.TempDir(), "live.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer live.Close()
	if _, err := live.Exec(`INSERT INTO live_docs(path, project, worktree_path, feature, dir_type, session_id, git_sha, mtime, ingested_at, content)
	                        VALUES (?, 'x-wt', '', '', '', '', '', 9, '2026-01-01T00:00:00Z', 'b')`,
		filepath.Join(container, "gone/.giantmem/notes.md")); err != nil {
		t.Fatal(err)
	}

	ps, err := (&App{live: live}).WorkspaceProjects()
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]WsProject{}
	for _, p := range ps {
		byName[p.Name] = p
	}
	x := byName["x"]
	var labels []string
	for _, w := range x.Workspaces {
		labels = append(labels, w.Label)
	}
	if len(labels) != 3 || labels[0] != "main" || labels[2] != "gone" || !x.Workspaces[2].Dead || x.Flat {
		t.Fatalf("x = %+v", x)
	}
	solo := byName["solo"]
	if !solo.Flat || solo.Workspaces[0].Files != 1 {
		t.Fatalf("solo = %+v", solo)
	}
	names := make([]string, 0, len(byName))
	for n := range byName {
		names = append(names, n)
	}
	sort.Strings(names)
	if len(names) != 2 {
		t.Fatalf("projects = %v", names)
	}
}

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"

	"github.com/bearded-giant/giant-tooling/giantmem/internal/db"
)

func TestSessionFacetsRangeAndLast(t *testing.T) {
	d, err := db.Open(filepath.Join(t.TempDir(), "archives.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	for i, r := range []struct{ project, canonical, ts string }{
		{"dev/a", "a", "20200101_090000"},
		{"dev/a", "a", "20261015_120000"},
		{"dev/b", "", "20261010_080000"},
	} {
		var canon any
		if r.canonical != "" {
			canon = r.canonical
		}
		if _, err := d.Exec(`INSERT INTO documents (project, canonical_project, timestamp, source_type, filepath, filename, session_id, indexed_at)
		                     VALUES (?, ?, ?, 'session', ?, 'x.jsonl', ?, datetime('now'))`,
			r.project, canon, r.ts, filepath.Join("/t", r.ts), string(rune('a'+i))); err != nil {
			t.Fatal(err)
		}
	}
	a := &App{archive: d}

	all, err := a.SessionFacets("", "")
	if err != nil || all.ByProject["a"] != 2 || all.ByProject["dev/b"] != 1 {
		t.Fatalf("all = %+v, %v", all, err)
	}
	if all.ProjectLast["a"] != "20261015_120000" || all.ProjectLast["dev/b"] != "20261010_080000" {
		t.Fatalf("last = %+v", all.ProjectLast)
	}

	ranged, err := a.SessionFacets("2026-10-12", "")
	if err != nil || len(ranged.ByProject) != 1 || ranged.ByProject["a"] != 1 {
		t.Fatalf("ranged = %+v, %v", ranged, err)
	}
}

func TestSetPrefConcurrent(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	a := &App{}
	var wg sync.WaitGroup
	for i := 0; i < 40; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if err := a.SetPref(fmt.Sprintf("k%d", i), "v"); err != nil {
				t.Error(err)
			}
		}(i)
	}
	wg.Wait()
	m, err := readPrefs()
	if err != nil {
		t.Fatal(err)
	}
	if len(m) != 40 {
		t.Fatalf("lost writes: got %d of 40 keys", len(m))
	}
}

func TestGitLastVersionDeletedNote(t *testing.T) {
	root := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	note := filepath.Join(root, "areas", "ai", "note.md")
	if err := os.MkdirAll(filepath.Dir(note), 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(note, []byte("v1"), 0o644)
	run("init", "-q")
	run("add", ".")
	run("commit", "-qm", "add")
	os.WriteFile(note, []byte("v2"), 0o644)
	run("commit", "-qam", "edit")
	run("rm", "-rq", "areas")
	run("commit", "-qm", "delete")
	got, err := gitLastVersion(note)
	if err != nil || got != "v2" {
		t.Fatalf("got %q, %v; want v2", got, err)
	}
	if _, err := gitLastVersion("https://claude.ai/artifact/x"); err == nil {
		t.Fatal("url should not resolve")
	}
}

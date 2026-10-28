package main

import (
	"fmt"
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

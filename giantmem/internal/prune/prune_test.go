package prune

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bearded-giant/giant-tooling/giantmem/internal/db"
)

func seed(t *testing.T) *sql.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "live.db")
	d, err := db.Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	now := time.Now().Unix()
	ins := func(p, project, canonical string, ageDays int, body string) {
		_, err := d.Exec(`INSERT INTO live_docs
			(path, project, mtime, ingested_at, content, canonical_project)
			VALUES (?,?,?,?,?,?)`,
			p, project, now-int64(ageDays)*86400, "x", body, canonical)
		if err != nil {
			t.Fatalf("insert %s: %v", p, err)
		}
	}
	for i := 0; i < 10; i++ {
		ins(fmt.Sprintf("/big/old%d", i), "dev/py/big", "big-wt", 400, "0123456789")
	}
	for i := 0; i < 5; i++ {
		ins(fmt.Sprintf("/big/new%d", i), "dev/py/big", "big-wt", 1, "0123456789")
	}
	ins("/loose/a", "unlabeled", "", 100, "ab")
	return d
}

func TestBuckets_BandsAndFallbackLabel(t *testing.T) {
	got, err := Buckets(seed(t))
	if err != nil {
		t.Fatalf("buckets: %v", err)
	}
	byRepo := map[string]Bucket{}
	for _, b := range got {
		byRepo[b.Repo] = b
	}
	big, ok := byRepo["big-wt"]
	if !ok {
		t.Fatalf("big-wt missing from %v", byRepo)
	}
	want := map[int]int{0: 15, 30: 10, 90: 10, 180: 10, 365: 10}
	for _, band := range big.Bands {
		if band.Docs != want[band.Days] {
			t.Errorf("big-wt band %dd: docs=%d want %d", band.Days, band.Docs, want[band.Days])
		}
	}
	if big.Bands[0].Bytes != 150 {
		t.Errorf("big-wt estimated bytes = %d, want 150 (15 docs x 10 bytes)", big.Bands[0].Bytes)
	}
	if _, ok := byRepo["unlabeled"]; !ok {
		t.Errorf("row with empty canonical_project should fall back to its project name: %v", byRepo)
	}
}

func TestRun_RefusesEmptySelection(t *testing.T) {
	if err := Run("", Options{}, true, nil); err == nil {
		t.Fatal("expected refusal when no repo and no age cutoff")
	}
}

func TestRun_DryRunStreamsScriptOutput(t *testing.T) {
	base := t.TempDir()
	d, err := db.Open(filepath.Join(base, "live.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	now := time.Now().Unix()
	for i := 0; i < 3; i++ {
		if _, err := d.Exec(`INSERT INTO live_docs
			(path, project, mtime, ingested_at, content, canonical_project)
			VALUES (?,?,?,?,?,?)`,
			fmt.Sprintf("/old/%d", i), "dev/py/big", now-400*86400, "x", "body", "big-wt"); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}
	d.Close()

	var lines []string
	err = Run(base, Options{Repos: []string{"big-wt"}, OlderThan: "90d", DB: "live.db"}, true, func(l string) {
		lines = append(lines, l)
	})
	if err != nil {
		t.Fatalf("dry run: %v (log: %v)", err, lines)
	}
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "match: 3 docs") {
		t.Errorf("expected the match line in streamed output, got:\n%s", joined)
	}
	if !strings.Contains(joined, "dry run") {
		t.Errorf("dry run should say so, got:\n%s", joined)
	}
}

func TestRun_VacuumOnlySkipsSelection(t *testing.T) {
	base := t.TempDir()
	d, err := db.Open(filepath.Join(base, "live.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, err := d.Exec(`INSERT INTO live_docs
		(path, project, mtime, ingested_at, content, canonical_project)
		VALUES ('/keep','p',1,'x','body','r')`); err != nil {
		t.Fatalf("insert: %v", err)
	}
	d.Close()

	var lines []string
	// StopDaemon stays false: true would bounce the real giantmemd on this box.
	err = Run(base, Options{VacuumOnly: true, DB: "live.db"}, false, func(l string) {
		lines = append(lines, l)
	})
	if err != nil {
		t.Fatalf("vacuum-only: %v (log: %v)", err, lines)
	}
	if !strings.Contains(strings.Join(lines, "\n"), "vacuuming") {
		t.Errorf("expected vacuum log, got: %v", lines)
	}

	d2, err := db.Open(filepath.Join(base, "live.db"))
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer d2.Close()
	var n int
	if err := d2.QueryRow(`SELECT COUNT(*) FROM live_docs`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("vacuum-only must not delete rows: got %d, want 1", n)
	}
}

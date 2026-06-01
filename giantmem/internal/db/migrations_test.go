package db

import (
	"path/filepath"
	"testing"
)

func openLiveTest(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "live.db")
}

func tableExists(t *testing.T, path, name string) bool {
	t.Helper()
	d, err := Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer d.Close()
	var n int
	err = d.QueryRow(
		`SELECT COUNT(*) FROM sqlite_master WHERE type IN ('table','trigger') AND name=?`, name,
	).Scan(&n)
	if err != nil {
		t.Fatalf("query sqlite_master: %v", err)
	}
	return n > 0
}

func TestMigrateLive_FreshDBReachesHeadWithFullSchema(t *testing.T) {
	path := openLiveTest(t)
	d, err := Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer d.Close()

	v, err := SchemaVersion(d)
	if err != nil {
		t.Fatalf("schema version: %v", err)
	}
	if v != 5 {
		t.Fatalf("user_version = %d, want 5", v)
	}

	for _, name := range []string{
		"live_docs", "live_docs_fts", "live_docs_ai", "live_docs_ad", "live_docs_au",
		"active_sessions", "scopes", "artifact_access",
		"artifact_embedding_meta", "artifacts",
	} {
		if !tableExists(t, path, name) {
			t.Errorf("expected table/trigger %q to exist after fresh migrate", name)
		}
	}
}

func TestMigrateLive_V5AdditiveAndIdempotent(t *testing.T) {
	path := openLiveTest(t)
	d, err := Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}

	// a live_docs row written before re-migration must survive (v5 is additive).
	_, err = d.Exec(
		`INSERT INTO live_docs(path, project, worktree_path, feature, dir_type,
            session_id, git_sha, mtime, ingested_at, content)
         VALUES (?,?,?,?,?,?,?,?,?,?)`,
		"/r/.giantmem/features/x/proposal.md", "r", "/r", "x", "features",
		"", "", int64(1), "2026-06-01T00:00:00Z", "---\nstatus: ready\n---\nbody")
	if err != nil {
		t.Fatalf("insert live_docs: %v", err)
	}

	// the AFTER INSERT trigger must have mirrored into fts.
	var ftsN int
	if err := d.QueryRow(`SELECT COUNT(*) FROM live_docs_fts`).Scan(&ftsN); err != nil {
		t.Fatalf("count fts: %v", err)
	}
	if ftsN != 1 {
		t.Fatalf("live_docs_fts rows = %d, want 1 (trigger intact)", ftsN)
	}
	d.Close()

	// re-open => MigrateLive runs again; must stay at v5, no error, row intact.
	d2, err := Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer d2.Close()
	if err := MigrateLive(d2); err != nil {
		t.Fatalf("idempotent re-migrate: %v", err)
	}
	v, _ := SchemaVersion(d2)
	if v != 5 {
		t.Fatalf("user_version after re-migrate = %d, want 5", v)
	}
	var docN int
	if err := d2.QueryRow(`SELECT COUNT(*) FROM live_docs`).Scan(&docN); err != nil {
		t.Fatalf("count live_docs: %v", err)
	}
	if docN != 1 {
		t.Fatalf("live_docs rows = %d, want 1 (preserved across migrate)", docN)
	}
}

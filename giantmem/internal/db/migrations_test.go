package db

import (
	"path/filepath"
	"testing"
)

func latestVersion(ms []Migration) int {
	v := 0
	for _, m := range ms {
		if m.Version > v {
			v = m.Version
		}
	}
	return v
}

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
	if want := latestVersion(liveMigrations); v != want {
		t.Fatalf("user_version = %d, want %d", v, want)
	}

	for _, name := range []string{
		"live_docs", "live_docs_fts", "live_docs_ai", "live_docs_ad", "live_docs_au",
		"scopes", "artifact_access",
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

	// re-open => MigrateLive runs again; must stay at the latest version, no error, row intact.
	d2, err := Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer d2.Close()
	if err := MigrateLive(d2); err != nil {
		t.Fatalf("idempotent re-migrate: %v", err)
	}
	v, _ := SchemaVersion(d2)
	if want := latestVersion(liveMigrations); v != want {
		t.Fatalf("user_version after re-migrate = %d, want %d", v, want)
	}
	var docN int
	if err := d2.QueryRow(`SELECT COUNT(*) FROM live_docs`).Scan(&docN); err != nil {
		t.Fatalf("count live_docs: %v", err)
	}
	if docN != 1 {
		t.Fatalf("live_docs rows = %d, want 1 (preserved across migrate)", docN)
	}
}

func TestMigrateArchive_V5ToolUsesSearchable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "archives.db")
	for _, name := range []string{"tool_uses", "tool_uses_fts", "tool_uses_ai", "tool_uses_ad", "session_links"} {
		if !tableExists(t, path, name) {
			t.Fatalf("%s missing after migrate", name)
		}
	}
	d, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if v, _ := SchemaVersion(d); v != latestVersion(archiveMigrations) {
		t.Fatalf("user_version = %d, want %d", v, latestVersion(archiveMigrations))
	}
	if _, err := d.Exec(`INSERT INTO tool_uses (session_id, project, seq, tool, summary, input)
        VALUES ('s1', 'p', 0, 'Bash', 'jq .hits splunk.json', '{"command":"jq .hits splunk.json"}')`); err != nil {
		t.Fatal(err)
	}
	count := func() int {
		var n int
		if err := d.QueryRow(`SELECT COUNT(*) FROM tool_uses_fts WHERE tool_uses_fts MATCH 'splunk'`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if count() != 1 {
		t.Fatal("inserted tool use not searchable")
	}
	if _, err := d.Exec(`DELETE FROM tool_uses WHERE session_id = 's1'`); err != nil {
		t.Fatal(err)
	}
	if count() != 0 {
		t.Fatal("deleted tool use still searchable")
	}
}

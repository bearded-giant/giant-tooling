package artifacts

import (
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/bearded-giant/giant-tooling/giantmem/internal/db"
)

func newLiveDB(t *testing.T) *sql.DB {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "live.db"))
	if err != nil {
		t.Fatalf("open live.db: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

func insertLiveDoc(t *testing.T, d *sql.DB, abs, project, worktree, content string, mtime int64) {
	t.Helper()
	_, err := d.Exec(
		`INSERT INTO live_docs(path, project, worktree_path, feature, dir_type,
            session_id, git_sha, mtime, ingested_at, content)
         VALUES (?,?,?,?,?,?,?,?,?,?)
         ON CONFLICT(path) DO UPDATE SET content=excluded.content, mtime=excluded.mtime,
            project=excluded.project, worktree_path=excluded.worktree_path`,
		abs, project, worktree, "", "", "", "", mtime, "2026-06-01T00:00:00Z", content)
	if err != nil {
		t.Fatalf("insert live_doc %s: %v", abs, err)
	}
}

func TestRelFromLivePath(t *testing.T) {
	cases := []struct {
		abs string
		rel string
		ok  bool
	}{
		{"/Users/x/dev/repo/.giantmem/features/foo/proposal.md", "features/foo/proposal.md", true},
		{"/Users/x/.claude/projects/slug/memory/note.md", "", false},
		{"/Users/x/dev/repo/.giantmem/", "", false},
	}
	for _, c := range cases {
		rel, ok := RelFromLivePath(c.abs)
		if ok != c.ok || rel != c.rel {
			t.Errorf("RelFromLivePath(%q) = (%q,%v), want (%q,%v)", c.abs, rel, ok, c.rel, c.ok)
		}
	}
}

func TestDeriveFromLiveDoc_ClassifyAndFrontmatter(t *testing.T) {
	content := "---\nstatus: ready\nlifecycle: candidate\nscope: personal\n---\nbody text"
	a, ok := DeriveFromLiveDoc("features/foo/proposal.md", content, "repo", "main", "/r")
	if !ok {
		t.Fatal("expected classify ok")
	}
	if a.Type != "proposal" || a.Feature != "foo" {
		t.Errorf("type/feature = %q/%q, want proposal/foo", a.Type, a.Feature)
	}
	if a.Status != "ready" || a.Lifecycle != "candidate" || a.Scope != "personal" {
		t.Errorf("frontmatter not applied: status=%q lifecycle=%q scope=%q", a.Status, a.Lifecycle, a.Scope)
	}
	if !a.HasFront {
		t.Error("HasFront should be true")
	}
	if a.ID != "feat:foo:proposal" {
		t.Errorf("ID = %q, want feat:foo:proposal", a.ID)
	}
	if a.Repo != "repo" || a.Branch != "main" || a.Worktree != "/r" {
		t.Errorf("repo/branch/worktree not set: %q/%q/%q", a.Repo, a.Branch, a.Worktree)
	}
}

func TestDeriveFromLiveDoc_NoFrontmatterDefaultsDurable(t *testing.T) {
	a, ok := DeriveFromLiveDoc("features/foo/design.md", "no frontmatter here", "repo", "", "")
	if !ok {
		t.Fatal("expected classify ok")
	}
	if a.HasFront {
		t.Error("HasFront should be false with no frontmatter")
	}
	if a.Lifecycle != LifecycleDurable {
		t.Errorf("lifecycle = %q, want durable default", a.Lifecycle)
	}
	if a.Status != "ready" {
		t.Errorf("status = %q, want ready default", a.Status)
	}
}

func TestDeriveFromLiveDoc_TasksStatusFromCheckboxes(t *testing.T) {
	content := "# Tasks\n- [x] one\n- [ ] two\n"
	a, _ := DeriveFromLiveDoc("features/foo/tasks.md", content, "repo", "", "")
	if a.Status != "ready" {
		t.Errorf("partial checkboxes status = %q, want ready", a.Status)
	}
	done := "# Tasks\n- [x] one\n- [x] two\n"
	b, _ := DeriveFromLiveDoc("features/foo/tasks.md", done, "repo", "", "")
	if b.Status != "done" {
		t.Errorf("all checked status = %q, want done", b.Status)
	}
}

func TestDeriveFromLiveDoc_NonArtifactRejected(t *testing.T) {
	if _, ok := DeriveFromLiveDoc("WORKSPACE.md", "x", "r", "", ""); ok {
		t.Error("WORKSPACE.md should not classify as an artifact")
	}
}

func TestReconcileTable_UpsertIdempotentDeleteCanonical(t *testing.T) {
	d := newLiveDB(t)
	base := t.TempDir() // no -wt siblings => Canonicalize returns project unchanged

	insertLiveDoc(t, d, "/r/.giantmem/features/foo/proposal.md", "myrepo", "/r",
		"---\nstatus: ready\n---\nbody", 1717200000)
	insertLiveDoc(t, d, "/r/.giantmem/features/foo/tasks.md", "myrepo", "/r",
		"- [ ] a\n- [ ] b\n", 1717200000)
	// memory-style doc (no /.giantmem/) must be ignored entirely.
	insertLiveDoc(t, d, "/Users/x/.claude/projects/slug/memory/note.md", "memory", "",
		"just a memory", 1717200000)

	st, err := ReconcileTable(d, base)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if st.Upserted != 2 {
		t.Fatalf("upserted = %d, want 2 (memory doc excluded)", st.Upserted)
	}
	if got := countArtifacts(t, d); got != 2 {
		t.Fatalf("artifacts rows = %d, want 2", got)
	}

	// canonical_project backfilled on the .giantmem rows.
	if st.Canonical < 2 {
		t.Errorf("canonical backfilled = %d, want >=2", st.Canonical)
	}
	var canon string
	if err := d.QueryRow(
		`SELECT COALESCE(canonical_project,'') FROM live_docs WHERE path=?`,
		"/r/.giantmem/features/foo/proposal.md").Scan(&canon); err != nil {
		t.Fatal(err)
	}
	if canon != "myrepo" {
		t.Errorf("canonical_project = %q, want myrepo", canon)
	}

	// second pass: idempotent — no dup rows, nothing new to canonicalize.
	st2, err := ReconcileTable(d, base)
	if err != nil {
		t.Fatalf("reconcile 2: %v", err)
	}
	if got := countArtifacts(t, d); got != 2 {
		t.Fatalf("artifacts rows after 2nd pass = %d, want 2", got)
	}
	if st2.Canonical != 0 {
		t.Errorf("2nd pass canonical = %d, want 0 (already filled)", st2.Canonical)
	}

	// delete a source row => orphan removed on next pass.
	if _, err := d.Exec(`DELETE FROM live_docs WHERE path=?`, "/r/.giantmem/features/foo/tasks.md"); err != nil {
		t.Fatal(err)
	}
	st3, err := ReconcileTable(d, base)
	if err != nil {
		t.Fatalf("reconcile 3: %v", err)
	}
	if st3.Removed != 1 {
		t.Errorf("removed = %d, want 1", st3.Removed)
	}
	if got := countArtifacts(t, d); got != 1 {
		t.Fatalf("artifacts rows after delete = %d, want 1", got)
	}
}

func TestReconcileTable_BranchFromActiveSessions(t *testing.T) {
	d := newLiveDB(t)
	base := t.TempDir()
	if _, err := d.Exec(
		`INSERT INTO active_sessions(id, worktree_path, branch, last_seen) VALUES (?,?,?,?)`,
		"s1", "/r", "feature-x", "2026-06-01T10:00:00Z"); err != nil {
		t.Fatal(err)
	}
	insertLiveDoc(t, d, "/r/.giantmem/features/foo/proposal.md", "myrepo", "/r", "body", 1717200000)

	if _, err := ReconcileTable(d, base); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	var branch string
	if err := d.QueryRow(`SELECT branch FROM artifacts WHERE id=?`, "feat:foo:proposal").Scan(&branch); err != nil {
		t.Fatal(err)
	}
	if branch != "feature-x" {
		t.Errorf("branch = %q, want feature-x (from active_sessions)", branch)
	}
}

func countArtifacts(t *testing.T, d *sql.DB) int {
	t.Helper()
	var n int
	if err := d.QueryRow(`SELECT COUNT(*) FROM artifacts`).Scan(&n); err != nil {
		t.Fatalf("count artifacts: %v", err)
	}
	return n
}

package archiver

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bearded-giant/giant-tooling/giantmem/internal/db"
)

func write(t *testing.T, p, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
}

// liveBase creates a temp archive base with an initialized live.db and points
// GIANTMEM_ARCHIVE_BASE at it (captureAndVerify reads the env).
func liveBase(t *testing.T) string {
	t.Helper()
	base := t.TempDir()
	d, err := db.OpenLiveOrCreate(filepath.Join(base, "live.db"))
	if err != nil {
		t.Fatalf("open live.db: %v", err)
	}
	d.Close()
	t.Setenv("GIANTMEM_ARCHIVE_BASE", base)
	return base
}

func TestEnclosingGiantmem(t *testing.T) {
	cases := map[string]string{
		"/x/.giantmem":                     "/x/.giantmem",
		"/x/.giantmem/features/foo":        "/x/.giantmem",
		"/a/b/.giantmem/features/foo/r.md": "/a/b/.giantmem",
		"/x/no/giantmem/here":              "",
	}
	for in, want := range cases {
		if got := enclosingGiantmem(in); got != want {
			t.Errorf("enclosingGiantmem(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestArchiveFeature_VerifiesKeepsRowsDeletesDir(t *testing.T) {
	base := liveBase(t)
	repo := filepath.Join(t.TempDir(), "repo")
	ws := filepath.Join(repo, ".giantmem")
	featDir := filepath.Join(ws, "features", "foo")
	proposal := filepath.Join(featDir, "proposal.md")
	facts := filepath.Join(featDir, "facts.md")
	write(t, proposal, "# proposal body")
	write(t, facts, "facts body")
	write(t, filepath.Join(ws, "features", "features.json"), `{"foo":{"status":"complete"}}`)

	res, err := ArchiveFeature(repo, "foo", false, false)
	if err != nil {
		t.Fatalf("archive: %v (res=%+v)", err, res)
	}
	if res.Action != "archived" {
		t.Fatalf("action = %q, want archived (%s)", res.Action, res.Reason)
	}
	if res.Captured != 2 {
		t.Errorf("captured = %d, want 2", res.Captured)
	}
	if _, err := os.Stat(featDir); !os.IsNotExist(err) {
		t.Errorf("feature dir still exists, want removed")
	}

	// rows KEPT — live.db is the archive
	d, err := db.Open(filepath.Join(base, "live.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	var n int
	if err := d.QueryRow("SELECT COUNT(*) FROM live_docs WHERE path IN (?,?)", proposal, facts).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("live_docs rows after archive = %d, want 2 (kept)", n)
	}

	// features.json flipped
	b, _ := os.ReadFile(filepath.Join(ws, "features", "features.json"))
	if !strings.Contains(string(b), `"archived"`) {
		t.Errorf("features.json not marked archived: %s", b)
	}
}

func TestArchiveFeature_AbortsWhenUncaptured(t *testing.T) {
	liveBase(t)
	repo := filepath.Join(t.TempDir(), "repo")
	ws := filepath.Join(repo, ".giantmem")
	featDir := filepath.Join(ws, "features", "big")
	// >5MB: exceeds backfill's maxFileSize, so it can never be captured.
	write(t, filepath.Join(featDir, "huge.md"), strings.Repeat("x", 5_000_001))
	write(t, filepath.Join(featDir, "small.md"), "ok")
	write(t, filepath.Join(ws, "features", "features.json"), `{"big":{"status":"complete"}}`)

	res, err := ArchiveFeature(repo, "big", false, false)
	if err == nil {
		t.Fatalf("expected error (uncaptured file), got nil; res=%+v", res)
	}
	if _, statErr := os.Stat(featDir); os.IsNotExist(statErr) {
		t.Fatalf("feature dir was DELETED despite uncaptured file — data loss")
	}
}

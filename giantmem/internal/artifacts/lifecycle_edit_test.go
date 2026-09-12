package artifacts

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSetLifecycle(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "a.md")
	body := "---\ntype: research\nstatus: ready\nlifecycle: candidate\nupdated: 2026-01-01\n---\n# A\n\nbody with --- inside\n"
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	old := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	if err := os.Chtimes(p, old, old); err != nil {
		t.Fatal(err)
	}

	changed, err := SetLifecycle(p, LifecycleDeprecated)
	if err != nil || !changed {
		t.Fatalf("changed=%v err=%v", changed, err)
	}
	got, _ := os.ReadFile(p)
	want := "---\ntype: research\nstatus: ready\nlifecycle: deprecated\nupdated: 2026-01-01\n---\n# A\n\nbody with --- inside\n"
	if string(got) != want {
		t.Fatalf("got:\n%s", got)
	}
	st, _ := os.Stat(p)
	if !st.ModTime().Equal(old) {
		t.Fatalf("mtime not preserved: %v", st.ModTime())
	}

	// idempotent
	if changed, _ := SetLifecycle(p, LifecycleDeprecated); changed {
		t.Fatal("second flip should be a no-op")
	}

	// no frontmatter -> untouched
	q := filepath.Join(dir, "b.md")
	_ = os.WriteFile(q, []byte("# no frontmatter\nlifecycle: candidate\n"), 0o644)
	if changed, err := SetLifecycle(q, LifecycleDeprecated); err != nil || changed {
		t.Fatalf("no-frontmatter file must be skipped: changed=%v err=%v", changed, err)
	}

	// frontmatter without a lifecycle line -> untouched
	r := filepath.Join(dir, "c.md")
	_ = os.WriteFile(r, []byte("---\ntype: plan\n---\nbody\n"), 0o644)
	if changed, _ := SetLifecycle(r, LifecycleDeprecated); changed {
		t.Fatal("missing lifecycle line must be skipped")
	}

	if _, err := SetLifecycle(p, "bogus"); err == nil {
		t.Fatal("invalid lifecycle must error")
	}
}

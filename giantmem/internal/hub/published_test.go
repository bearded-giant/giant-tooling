package hub

import (
	"os"
	"path/filepath"
	"testing"
)

func TestListPublished(t *testing.T) {
	d, base, _ := fixture(t)
	note := filepath.Join(base, "vault", "areas", "ai", "metric-hoisting.md")
	mustWrite(t, note, "how a churn investigation reaches past the external catalog")
	gone := filepath.Join(base, "vault", "areas", "ai", "deleted.md")
	hand := filepath.Join(base, "vault", "handoffs", "inv", "golden.md")
	mustWrite(t, hand, "start here")
	const other = "99999999-2222-3333-4444-555555555555"
	exec := func(q string, args ...any) {
		if _, err := d.Exec(q, args...); err != nil {
			t.Fatal(err)
		}
	}
	link := func(s, kind, target, label, ts string) {
		exec(`INSERT INTO session_links (session_id, kind, target, label, ts) VALUES (?, ?, ?, ?, ?)`, s, kind, target, label, ts)
	}
	link(sid, "artifact", "https://claude.ai/artifact/A", "metric-hoisting.html", "2026-10-30T10:25:00Z")
	link(other, "artifact", "https://claude.ai/artifact/A", "Metric Hoisting", "2026-10-30T14:49:00Z")
	link(sid, "vault", note, "metric-hoisting", "2026-10-30T10:24:00Z")
	link(sid, "vault", gone, "deleted", "2026-10-29T10:00:00Z")
	link(sid, "handoff", hand, "handoff", "2026-10-30T08:00:00Z")
	link(sid, "handoff", "/repo/.giantmem/handoff.md", "handoff", "2026-10-30T09:00:00Z")
	link(sid, "mr", "https://gitlab.x/-/merge_requests/1", "x!1", "2026-10-30T11:00:00Z")

	all, err := ListPublished(d, PublishedFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 4 {
		t.Fatalf("got %d rows %+v, want 4 (artifact collapsed, .giantmem handoff and mr excluded)", len(all), all)
	}
	a := all[0]
	if a.Type != "claude-artifact" || a.Title != "Metric Hoisting" || a.Sessions != 2 || a.TS != "2026-10-30T14:49:00Z" || a.Project != "proj" {
		t.Errorf("artifact row = %+v", a)
	}
	if all[1].Project != "proj" || all[1].Type != "vault" || all[1].Missing {
		t.Errorf("vault row = %+v", all[1])
	}
	if all[2].Title != "inv/golden" {
		t.Errorf("handoff title = %q", all[2].Title)
	}
	if !all[3].Missing {
		t.Errorf("deleted note not flagged missing: %+v", all[3])
	}

	body, _ := ListPublished(d, PublishedFilter{Query: "external CATALOG"})
	if len(body) != 1 || body[0].Target != note {
		t.Errorf("body search = %+v", body)
	}
	typed, _ := ListPublished(d, PublishedFilter{Types: []string{"vault,handoff"}, Since: "2026-10-30T00:00:00Z"})
	if len(typed) != 2 {
		t.Errorf("type+since = %+v", typed)
	}
	if _, err := os.Stat(gone); err == nil {
		t.Fatal("fixture: gone note exists")
	}
}

package sources

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/bearded-giant/giant-tooling/giantmem/internal/db"
	"github.com/bearded-giant/giant-tooling/giantmem/internal/usage"
)

func TestRun_IndexesUsageWithSubagentOrg(t *testing.T) {
	projects := t.TempDir()
	slug := "-Users-me-dev-proj"
	opus := func(id, ts, usageJSON string) string {
		return `{"type":"assistant","timestamp":"` + ts + `","message":{"id":"` + id + `","model":"claude-opus-5-5","content":[{"type":"text","text":"x"}],"usage":` + usageJSON + `}}`
	}
	// noon UTC keeps every row on one local day for any zone within 11h of UTC
	writeJSONL(t, filepath.Join(projects, slug, hubSID+".jsonl"),
		`{"type":"user","cwd":"/Users/me/dev/proj","timestamp":"2026-11-01T12:00:00Z","message":{"content":"count tokens"}}`,
		`{"type":"attachment","timestamp":"2026-11-01T12:00:00Z","attachment":{"type":"credential_org","organizationUuid":"org-a"}}`,
		opus("m1", "2026-11-01T12:00:01Z", `{"input_tokens":1000000,"cache_creation_input_tokens":0,"cache_read_input_tokens":0,"output_tokens":0}`),
		`{"type":"attachment","timestamp":"2026-11-01T13:00:00Z","attachment":{"type":"credential_org","organizationUuid":"org-b"}}`,
		opus("m2", "2026-11-01T13:00:01Z", `{"input_tokens":0,"cache_creation_input_tokens":0,"cache_read_input_tokens":0,"output_tokens":1000000}`),
	)
	writeJSONL(t, filepath.Join(projects, slug, hubSID, "subagents", "agent-a1.jsonl"),
		`{"type":"user","cwd":"/Users/me/dev/proj","timestamp":"2026-11-01T12:00:05Z","message":{"content":"helper"}}`,
		opus("s1", "2026-11-01T12:00:05Z", `{"input_tokens":0,"cache_creation_input_tokens":0,"cache_read_input_tokens":1000000,"output_tokens":0}`),
		opus("s2", "2026-11-01T13:00:05Z", `{"input_tokens":0,"cache_creation_input_tokens":1000000,"cache_read_input_tokens":0,"output_tokens":0,"cache_creation":{"ephemeral_5m_input_tokens":1000000,"ephemeral_1h_input_tokens":0}}`),
	)

	d, err := db.Open(filepath.Join(t.TempDir(), "archives.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	src := &claudeJSONLSource{cfg: SourceConfig{Name: "claude-jsonl", Kind: "builtin", Enabled: true}}
	opts := EmitOptions{ClaudeProjects: projects, DB: d}
	for _, force := range []bool{false, true} {
		opts.Force = force
		if st, err := Run(context.Background(), d, src, opts); err != nil || st.Errs != 0 {
			t.Fatalf("run: %+v %v", st, err)
		}
	}

	orgs := map[string]string{}
	rows, err := d.Query(`SELECT message_id, org FROM usage WHERE session_id = ?`, hubSID)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var id, org string
		rows.Scan(&id, &org)
		orgs[id] = org
	}
	rows.Close()
	want := map[string]string{"m1": "org-a", "m2": "org-b", "s1": "org-a", "s2": "org-b"}
	if len(orgs) != len(want) {
		t.Fatalf("usage rows = %v, want %v", orgs, want)
	}
	for id, org := range want {
		if orgs[id] != org {
			t.Errorf("%s org = %q, want %q", id, orgs[id], org)
		}
	}

	if err := usage.Sync(d, nil, map[string]string{"org-a": "team"}); err != nil {
		t.Fatal(err)
	}
	type daily struct {
		account string
		msgs    int
		cost    float64
	}
	var got []daily
	rows, err = d.Query(`SELECT account, messages, cost_usd FROM usage_daily WHERE model = 'claude-opus-5-5' ORDER BY account`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var r daily
		rows.Scan(&r.account, &r.msgs, &r.cost)
		got = append(got, r)
	}
	rows.Close()
	// org-a: 1M input ($4) + 1M cache read ($0.20); org-b: 1M output ($20) + 1M 5m cache write ($5)
	wantDaily := []daily{{"org-b", 2, 25}, {"team", 2, 4.2}}
	if len(got) != 2 || got[0] != wantDaily[0] || got[1] != wantDaily[1] {
		t.Fatalf("usage_daily = %+v, want %+v", got, wantDaily)
	}
}

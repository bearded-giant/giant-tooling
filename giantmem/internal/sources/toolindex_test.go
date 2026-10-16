package sources

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bearded-giant/giant-tooling/giantmem/internal/db"
)

const hubSID = "11111111-2222-3333-4444-555555555555"

func writeJSONL(t *testing.T, path string, lines ...string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestSubagentOf(t *testing.T) {
	cases := []struct{ path, parent, agent string }{
		{"/p/-slug/" + hubSID + ".jsonl", "", ""},
		{"/p/-slug/" + hubSID + "/subagents/agent-a1b2.jsonl", hubSID, "a1b2"},
		{"/p/-slug/" + hubSID + "/subagents/workflows/wf_1/agent-c3.jsonl", hubSID, "c3"},
		{"/p/-my-subagents-repo/" + hubSID + ".jsonl", "", ""},
	}
	for _, c := range cases {
		parent, agent := subagentOf("/p", c.path)
		if parent != c.parent || agent != c.agent {
			t.Errorf("%s: got (%q, %q), want (%q, %q)", c.path, parent, agent, c.parent, c.agent)
		}
	}
}

func TestRun_IndexesToolUsesForSessionsAndSubagents(t *testing.T) {
	projects := t.TempDir()
	slug := "-Users-me-dev-proj"
	writeJSONL(t, filepath.Join(projects, slug, hubSID+".jsonl"),
		`{"type":"user","cwd":"/Users/me/dev/proj","timestamp":"2026-10-15T08:00:00Z","message":{"content":"find the churn bug"}}`,
		`{"type":"assistant","timestamp":"2026-10-15T08:00:01Z","message":{"content":[{"type":"text","text":"looking"},{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"jq .hits splunk.json"}}]}}`,
		`{"type":"user","timestamp":"2026-10-15T08:00:02Z","message":{"content":[{"type":"tool_result","tool_use_id":"t1","is_error":true,"content":"jq: error"}]}}`,
		`{"type":"assistant","timestamp":"2026-10-15T08:00:03Z","message":{"content":[{"type":"tool_use","id":"t2","name":"Artifact","input":{"file_path":"/tmp/inbox.html"}}]}}`,
		`{"type":"user","timestamp":"2026-10-15T08:00:04Z","message":{"content":[{"type":"tool_result","tool_use_id":"t2","content":"Published https://claude.ai/code/artifact/abc-123"}]}}`,
	)
	writeJSONL(t, filepath.Join(projects, slug, hubSID, "subagents", "agent-a1.jsonl"),
		`{"type":"user","cwd":"/Users/me/dev/proj","message":{"content":"map the ingest code"}}`,
		`{"type":"assistant","message":{"content":[{"type":"text","text":"searching"},{"type":"tool_use","id":"s1","name":"Grep","input":{"pattern":"subagents","path":"internal"}}]}}`,
	)

	d, err := db.Open(filepath.Join(t.TempDir(), "archives.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	src := &claudeJSONLSource{cfg: SourceConfig{Name: "claude-jsonl", Kind: "builtin", Enabled: true}}
	opts := EmitOptions{ClaudeProjects: projects, DB: d}

	run := func(force bool) {
		opts.Force = force
		st, err := Run(context.Background(), d, src, opts)
		if err != nil || st.Errs != 0 {
			t.Fatalf("run: %+v %v", st, err)
		}
	}
	run(false)

	var sessions, subagents int
	d.QueryRow(`SELECT COUNT(*) FROM documents WHERE source_type = 'session' AND session_id = ?`, hubSID).Scan(&sessions)
	d.QueryRow(`SELECT COUNT(*) FROM documents WHERE source_type = 'subagent' AND session_id = ?`, hubSID).Scan(&subagents)
	if sessions != 1 || subagents != 1 {
		t.Fatalf("session docs = %d, subagent docs = %d, want 1 and 1", sessions, subagents)
	}

	type row struct {
		agent, tool, summary string
		isErr                int
	}
	read := func() []row {
		rows, err := d.Query(`SELECT agent_id, tool, summary, is_error FROM tool_uses WHERE session_id = ? ORDER BY agent_id, seq`, hubSID)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var out []row
		for rows.Next() {
			var r row
			rows.Scan(&r.agent, &r.tool, &r.summary, &r.isErr)
			out = append(out, r)
		}
		return out
	}
	got := read()
	if len(got) != 3 {
		t.Fatalf("tool_uses = %+v, want 3 rows", got)
	}
	if got[0].agent != "" || got[0].tool != "Bash" || got[0].summary != "jq .hits splunk.json" || got[0].isErr != 1 {
		t.Fatalf("main Bash row wrong: %+v", got[0])
	}
	if got[2].agent != "a1" || got[2].tool != "Grep" {
		t.Fatalf("subagent row wrong: %+v", got[2])
	}

	var link string
	d.QueryRow(`SELECT target FROM session_links WHERE session_id = ? AND kind = 'artifact'`, hubSID).Scan(&link)
	if link != "https://claude.ai/code/artifact/abc-123" {
		t.Fatalf("artifact link = %q", link)
	}

	var hits int
	d.QueryRow(`SELECT COUNT(*) FROM tool_uses_fts WHERE tool_uses_fts MATCH 'splunk'`).Scan(&hits)
	if hits != 1 {
		t.Fatalf("fts hits = %d, want 1", hits)
	}

	// a forced re-run replaces rows instead of stacking them
	run(true)
	if again := read(); len(again) != 3 {
		t.Fatalf("after re-run tool_uses = %d rows, want 3", len(again))
	}
}

func TestIndexToolCalls_MRLinkUpsert(t *testing.T) {
	const u = "https://gitlab.rechargeapps.net/identity/recharge-auth/-/merge_requests/63"
	dir := t.TempDir()
	p := filepath.Join(dir, hubSID+".jsonl")
	d, err := db.Open(filepath.Join(dir, "archives.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	doc := Doc{Filepath: p, Project: "dev/proj", SessionID: hubSID}
	index := func(lines ...string) {
		t.Helper()
		writeJSONL(t, p, lines...)
		if err := IndexToolCalls(d, doc, ""); err != nil {
			t.Fatal(err)
		}
	}
	read := func() (role, cwd, label string) {
		t.Helper()
		if err := d.QueryRow(`SELECT role, cwd, label FROM session_links WHERE session_id = ? AND kind = 'mr' AND target = ?`, hubSID, u).Scan(&role, &cwd, &label); err != nil {
			t.Fatal(err)
		}
		return
	}

	push := `{"type":"pr-link","prUrl":"` + u + `","timestamp":"2026-10-16T01:00:00Z"}`
	index(`{"type":"user","cwd":"/wt/a","timestamp":"2026-10-16T00:59:00Z","message":{"content":"go"}}`, push)
	if role, cwd, label := read(); role != "touched" || cwd != "/wt/a" || label != "recharge-auth!63" {
		t.Fatalf("first index = %s %s %s", role, cwd, label)
	}

	// a later transcript (subagent, or the same one re-read) with the create call upgrades the role, never downgrades it
	index(`{"type":"user","cwd":"/wt/b","timestamp":"2026-10-16T01:01:00Z","message":{"content":"go"}}`,
		`{"type":"assistant","timestamp":"2026-10-16T01:01:01Z","message":{"content":[{"type":"tool_use","id":"c","name":"Bash","input":{"command":"glab mr create --fill"}}]}}`,
		`{"type":"user","timestamp":"2026-10-16T01:01:02Z","message":{"content":[{"type":"tool_result","tool_use_id":"c","content":"`+u+`"}]}}`)
	if role, cwd, _ := read(); role != "opened" || cwd != "/wt/a" {
		t.Fatalf("after create = %s %s, want opened /wt/a", role, cwd)
	}
	index(push)
	if role, _, _ := read(); role != "opened" {
		t.Fatalf("role downgraded to %s", role)
	}
}

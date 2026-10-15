package hub

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/bearded-giant/giant-tooling/giantmem/internal/db"
)

const sid = "11111111-2222-3333-4444-555555555555"

func fixture(t *testing.T) (*sql.DB, string, string) {
	t.Helper()
	base := t.TempDir()
	d, err := db.Open(filepath.Join(base, "archives.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	projects := filepath.Join(base, "projects", "-slug")
	transcript := filepath.Join(projects, sid+".jsonl")
	agentPath := filepath.Join(projects, sid, "subagents", "agent-a1.jsonl")
	mustWrite(t, transcript, "{}")
	mustWrite(t, agentPath, "{}")
	mustWrite(t, filepath.Join(projects, sid, "subagents", "agent-a1.meta.json"), `{"agentType":"Explore","description":"map ingest"}`)
	exec := func(q string, args ...any) {
		if _, err := d.Exec(q, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO documents (project, timestamp, source_type, filepath, filename, session_id, indexed_at)
          VALUES ('dev/proj', '20261015_080000', 'session', ?, 'x.jsonl', ?, datetime('now'))`, transcript, sid)
	exec(`INSERT INTO documents (project, timestamp, source_type, filepath, filename, session_id, indexed_at)
          VALUES ('dev/proj', '20261015_080000', 'subagent', ?, 'agent-a1.jsonl', ?, datetime('now'))`, agentPath, sid)
	rows := []struct {
		agent, ts, tool, summary string
		seq, isErr               int
	}{
		{"", "2026-10-15T12:00:00Z", "Bash", "jq .hits splunk.json", 0, 1},
		{"", "2026-10-15T12:05:00Z", "Edit", "/repo/a.go", 1, 0},
		{"a1", "2026-10-15T12:01:00Z", "Grep", "subagents in internal", 0, 0},
		{"", "2026-10-14T09:00:00Z", "Bash", "yesterday", 2, 0},
	}
	for _, r := range rows {
		exec(`INSERT INTO tool_uses (session_id, agent_id, project, seq, ts, tool, summary, input, is_error, result_head)
              VALUES (?, ?, 'dev/proj', ?, ?, ?, ?, '{}', ?, 'out')`, sid, r.agent, r.seq, r.ts, r.tool, r.summary, r.isErr)
	}
	exec(`INSERT INTO session_links (session_id, kind, target, label, ts) VALUES (?, 'mr', 'https://gitlab.x/g/p/-/merge_requests/8', '!8', '2026-10-15T12:06:00Z')`, sid)
	bdir := filepath.Join(base, "bundles", "-slug", sid)
	mustWrite(t, filepath.Join(bdir, "bundle.json"), `{"session_id":"`+sid+`","first_prompt":"find the churn bug","files":[{"path":"scratchpad/q.py","size":3,"kind":"script"}]}`)
	mustWrite(t, filepath.Join(bdir, "claude", "transcript.jsonl"), "{}")
	return d, base, transcript
}

func mustWrite(t *testing.T, p, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestQueryToolUses(t *testing.T) {
	d, _, _ := fixture(t)
	all, err := QueryToolUses(d, ToolQuery{SessionID: sid, WithIO: true})
	if err != nil || len(all) != 4 {
		t.Fatalf("session calls = %d, %v", len(all), err)
	}
	if all[0].AgentID != "" || all[len(all)-1].AgentID != "a1" || all[0].ResultHead != "out" {
		t.Fatalf("order or io wrong: %+v", all)
	}
	main, _ := QueryToolUses(d, ToolQuery{SessionID: sid, Agent: "main"})
	if len(main) != 3 || main[0].Input != "" {
		t.Fatalf("main-only = %+v", main)
	}
	errs, _ := QueryToolUses(d, ToolQuery{Errors: true})
	if len(errs) != 1 || errs[0].Tool != "Bash" {
		t.Fatalf("errors = %+v", errs)
	}
	hits, _ := QueryToolUses(d, ToolQuery{Match: "splunk"})
	if len(hits) != 1 {
		t.Fatalf("fts hits = %+v", hits)
	}
	day, _ := QueryToolUses(d, ToolQuery{Since: "2026-10-15T00:00:00Z", Until: "2026-10-16T00:00:00Z"})
	if len(day) != 3 {
		t.Fatalf("since/until = %d, want 3", len(day))
	}
}

func TestBundleAgentsDayTranscript(t *testing.T) {
	d, base, transcript := fixture(t)
	b, err := LoadBundle(base, sid)
	if err != nil || b.FirstPrompt != "find the churn bug" || len(b.Files) != 1 {
		t.Fatalf("bundle = %+v, %v", b, err)
	}
	if b.Files[0].AbsPath != filepath.Join(b.Dir, "scratchpad/q.py") {
		t.Fatalf("abs path = %s", b.Files[0].AbsPath)
	}
	if _, err := LoadBundle(base, "missing"); err == nil {
		t.Fatal("missing bundle should error")
	}

	agents, err := Agents(d, base, sid)
	if err != nil || len(agents) != 1 || agents[0].AgentType != "Explore" || agents[0].Description != "map ingest" || agents[0].Calls != 1 {
		t.Fatalf("agents = %+v, %v", agents, err)
	}

	day, err := Day(d, base, "2026-10-15T00:00:00Z", "2026-10-16T00:00:00Z")
	if err != nil || len(day) != 1 {
		t.Fatalf("day = %+v, %v", day, err)
	}
	s := day[0]
	if s.Calls != 3 || s.Errors != 1 || s.Links != 1 || s.Files != 1 || s.FirstPrompt == "" || s.Project != "dev/proj" {
		t.Fatalf("day row = %+v", s)
	}

	links, _ := QueryLinks(d, sid)
	if len(links) != 1 || links[0].Label != "!8" {
		t.Fatalf("links = %+v", links)
	}

	if got := TranscriptPath(base, transcript, sid); got != transcript {
		t.Fatalf("live transcript should win, got %s", got)
	}
	os.Remove(transcript)
	if got := TranscriptPath(base, transcript, ""); got != filepath.Join(b.Dir, "claude", "transcript.jsonl") {
		t.Fatalf("pruned transcript should fall back to bundle, got %s", got)
	}
}

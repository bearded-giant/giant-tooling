package sessions

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func text(s string) map[string]any { return map[string]any{"type": "text", "text": s} }

func userStr(ts, s string, extra map[string]any) map[string]any {
	m := map[string]any{"type": "user", "timestamp": ts, "message": map[string]any{"content": s}}
	for k, v := range extra {
		m[k] = v
	}
	return m
}

func TestReports(t *testing.T) {
	path := writeFixture(t,
		map[string]any{"type": "mode", "mode": "default"},
		userStr("2026-10-27T10:00:00Z", "first ask", nil),
		record("assistant", "2026-10-27T10:00:01Z", text("looking"), use("t0", "Bash", map[string]any{"command": "ls"})),
		record("user", "2026-10-27T10:00:02Z", result("t0", false, "ok")),
		record("assistant", "2026-10-27T10:00:03Z", text("draft report")),
		userStr("2026-10-27T10:00:04Z", "Stop hook feedback: open edges listed", map[string]any{"isMeta": true}),
		record("assistant", "2026-10-27T10:00:05Z", text("final report")),
		record("assistant", "2026-10-27T10:00:06Z", text("part 2")),
		map[string]any{"type": "system", "subtype": "turn_duration"},
		userStr("2026-10-27T10:01:00Z", "<command-message>wrap</command-message>\n<command-name>/wrap</command-name>\n<command-args>--no-handoff</command-args>", nil),
		record("assistant", "2026-10-27T10:01:01Z", text("wrap step"), use("t1", "Read", map[string]any{"file_path": "/x"})),
		record("user", "2026-10-27T10:01:02Z", result("t1", false, "body")),
		userStr("2026-10-27T10:01:03Z", "This session is being continued from a previous conversation", map[string]any{"isCompactSummary": true}),
		map[string]any{"type": "assistant", "isSidechain": true, "timestamp": "2026-10-27T10:01:04Z", "message": map[string]any{"content": []any{text("subagent chatter")}}},
		record("assistant", "2026-10-27T10:01:05Z", text("wrap done")),
		userStr("2026-10-27T10:02:00Z", "<local-command-caveat>Caveat: run locally</local-command-caveat>", map[string]any{"isMeta": true}),
		userStr("2026-10-27T10:02:00Z", "<command-name>/model</command-name>\n<command-args>opus</command-args>", nil),
		userStr("2026-10-27T10:02:00Z", "<local-command-stdout>Set model</local-command-stdout>", nil),
		userStr("2026-10-27T10:03:00Z", "[Request interrupted by user]", nil),
		userStr("2026-10-27T10:04:00Z", "<task-notification><task-id>a1</task-id><summary>Agent \"scan\" completed</summary></task-notification>", nil),
		record("assistant", "2026-10-27T10:04:01Z", text("agent result")),
		userStr("2026-10-27T10:05:00Z", "next ask", nil),
		record("assistant", "2026-10-27T10:05:01Z", use("t2", "Bash", map[string]any{"command": "sleep 9"})),
		"not json",
	)
	got, err := Reports(path)
	if err != nil {
		t.Fatal(err)
	}
	want := []Report{
		{Timestamp: "2026-10-27T10:00:06Z", Prompt: "first ask", Text: "final report\n\npart 2"},
		{Timestamp: "2026-10-27T10:01:05Z", Prompt: "/wrap --no-handoff", Text: "wrap done"},
		{Timestamp: "2026-10-27T10:04:01Z", Prompt: `task notification: Agent "scan" completed`, Text: "agent result"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got  %#v\nwant %#v", got, want)
	}
}

func TestPromptCaption(t *testing.T) {
	cases := []struct {
		in, want string
		ok       bool
	}{
		{"<pasted_content id=\"x\">\nhello   world\n</pasted_content> do it", "hello world do it", true},
		{"<bash-input>ls</bash-input>", "", false},
		{"   ", "", false},
		{"fix Map<string> parsing", "fix Map<string> parsing", true},
	}
	for _, c := range cases {
		got, ok := promptCaption(c.in)
		if got != c.want || ok != c.ok {
			t.Errorf("promptCaption(%q) = %q, %v; want %q, %v", c.in, got, ok, c.want, c.ok)
		}
	}
	if got, _ := promptCaption(strings.Repeat("é", 130)); len([]rune(got)) != 120 {
		t.Errorf("caption not clipped to 120 runes: %d", len([]rune(got)))
	}
}

func TestClearedFrom(t *testing.T) {
	dir := t.TempDir()
	start := time.Date(2026, 10, 27, 14, 19, 46, 86e6, time.UTC)
	write := func(name, body string, mtime time.Time) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(p, mtime, mtime); err != nil {
			t.Fatal(err)
		}
		return p
	}
	write("parent.jsonl", "{}\n", start.Add(300*time.Millisecond))
	write("older.jsonl", "{}\n", start.Add(-2*time.Hour))
	write("agent-x.jsonl", "{}\n", start)
	stub := write("stub.jsonl",
		`{"type":"mode"}`+"\n"+
			`{"type":"user","isMeta":true,"timestamp":"2026-10-27T14:19:46.086Z","message":{"content":"<local-command-caveat>x</local-command-caveat>"}}`+"\n"+
			`{"type":"user","timestamp":"2026-10-27T14:19:46.086Z","message":{"content":"<command-name>/clear</command-name>\n<command-args></command-args>"}}`+"\n",
		start.Add(6*time.Minute))
	if got := ClearedFrom(stub); got != "parent" {
		t.Fatalf("ClearedFrom = %q, want parent", got)
	}
	fresh := write("fresh.jsonl",
		`{"type":"user","timestamp":"2026-10-27T14:19:46.086Z","message":{"content":"<command-name>/exit</command-name>"}}`+"\n",
		start)
	if got := ClearedFrom(fresh); got != "" {
		t.Fatalf("ClearedFrom on a non-/clear stub = %q, want empty", got)
	}
}

func TestFirstPrompt(t *testing.T) {
	long := strings.Repeat("word ", 100) + "\nsecond line"
	path := writeFixture(t,
		map[string]any{"type": "mode", "mode": "default"},
		userStr("2026-10-27T10:00:00Z", "<local-command-caveat>Caveat</local-command-caveat>", map[string]any{"isMeta": true}),
		userStr("2026-10-27T10:00:00Z", "<command-name>/model</command-name>", nil),
		userStr("2026-10-27T10:00:00Z", "skill body", map[string]any{"isMeta": true}),
		record("user", "2026-10-27T10:00:01Z", text("<system-reminder>ctx</system-reminder>"), text(long)),
		userStr("2026-10-27T10:01:00Z", "second ask", nil),
	)
	if got := FirstPrompt(path); got != strings.TrimSpace(long) {
		t.Fatalf("got %q", got)
	}
	if got := FirstPrompt(filepath.Join(t.TempDir(), "missing.jsonl")); got != "" {
		t.Fatalf("missing file: got %q", got)
	}
}

package hub

import "testing"

func TestToolCounts(t *testing.T) {
	d, _, _ := fixture(t)
	for i, tool := range []string{"mcp__doit__add_todo", "mcp__doit__list_todos", "mcp__plugin_kai_atlassian__search"} {
		if _, err := d.Exec(`INSERT INTO tool_uses (session_id, agent_id, project, seq, ts, tool, summary, input, is_error)
            VALUES (?, '', 'dev/other', ?, '2026-10-16T08:00:00Z', ?, 's', '{}', 0)`, sid, 10+i, tool); err != nil {
			t.Fatal(err)
		}
	}

	all, err := ToolCounts(d, ToolQuery{}, false)
	if err != nil || len(all) != 6 || all[0] != (ToolCount{Tool: "Bash", Calls: 2, Errors: 1}) {
		t.Fatalf("counts = %+v, %v", all, err)
	}

	byServer, _ := ToolCounts(d, ToolQuery{Tool: "mcp__%"}, true)
	want := []ToolCount{{"mcp__doit", 2, 0}, {"mcp__plugin_kai_atlassian", 1, 0}}
	if len(byServer) != 2 || byServer[0] != want[0] || byServer[1] != want[1] {
		t.Fatalf("by server = %+v", byServer)
	}

	main, _ := ToolCounts(d, ToolQuery{Agent: "main", Project: "dev/proj"}, false)
	for _, c := range main {
		if c.Tool == "Grep" {
			t.Fatalf("main thread filter leaked the subagent's Grep: %+v", main)
		}
	}
	day, _ := ToolCounts(d, ToolQuery{Since: "2026-10-15T00:00:00Z", Until: "2026-10-16T00:00:00Z"}, false)
	if n := sum(day); n != 3 {
		t.Fatalf("window calls = %d, want 3 (%+v)", n, day)
	}
}

func sum(cs []ToolCount) int {
	n := 0
	for _, c := range cs {
		n += c.Calls
	}
	return n
}

func TestSessionLengths(t *testing.T) {
	d, base, _ := fixture(t)
	// fixture main thread: 2026-10-14T09:00, then 2026-10-15T12:00 and 12:05 (resumed a day later)
	ls, err := SessionLengths(d, ToolQuery{}, DefaultIdleCap)
	if err != nil || len(ls) != 1 {
		t.Fatalf("lengths = %+v, %v", ls, err)
	}
	l := ls[0]
	if l.Calls != 3 || l.SpanSec != 27*3600+5*60 || l.ActiveSec != 15*60+5*60 {
		t.Fatalf("resumed session = %+v, want span 27h05m, active 20m", l)
	}
	win, _ := SessionLengths(d, ToolQuery{Since: "2026-10-15T00:00:00Z"}, DefaultIdleCap)
	if len(win) != 1 || win[0].SpanSec != 300 || win[0].ActiveSec != 300 || win[0].Calls != 2 {
		t.Fatalf("windowed = %+v", win)
	}

	days, err := Day(d, base, "2026-10-15T00:00:00Z", "2026-10-16T00:00:00Z")
	if err != nil || len(days) != 1 || days[0].ActiveSec != 300 {
		t.Fatalf("day active = %+v, %v", days, err)
	}

	sum := SummarizeLengths([]SessionLength{
		{Project: "a", ActiveSec: 60, SpanSec: 100},
		{Project: "a", ActiveSec: 120, SpanSec: 200},
		{Project: "a", ActiveSec: 600, SpanSec: 900},
		{Project: "b", ActiveSec: 30, SpanSec: 30},
	})
	if len(sum) != 2 || sum[0].Project != "a" || sum[0].Sessions != 3 || sum[0].ActiveP50 != 120 ||
		sum[0].ActiveP95 != 600 || sum[0].ActiveAvg != 260 || sum[0].SpanP50 != 200 {
		t.Fatalf("summary = %+v", sum)
	}
}

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

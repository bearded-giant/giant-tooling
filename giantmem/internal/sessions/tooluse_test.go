package sessions

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func use(id, name string, input map[string]any) map[string]any {
	return map[string]any{"type": "tool_use", "id": id, "name": name, "input": input}
}

func result(id string, isErr bool, content any) map[string]any {
	b := map[string]any{"type": "tool_result", "tool_use_id": id, "content": content}
	if isErr {
		b["is_error"] = true
	}
	return b
}

func record(typ, ts string, blocks ...map[string]any) map[string]any {
	return map[string]any{"type": typ, "timestamp": ts, "message": map[string]any{"content": blocks}}
}

func writeFixture(t *testing.T, lines ...any) string {
	t.Helper()
	var sb strings.Builder
	for _, l := range lines {
		if s, ok := l.(string); ok {
			sb.WriteString(s + "\n")
			continue
		}
		b, err := json.Marshal(l)
		if err != nil {
			t.Fatal(err)
		}
		sb.Write(b)
		sb.WriteString("\n")
	}
	p := filepath.Join(t.TempDir(), "s.jsonl")
	if err := os.WriteFile(p, []byte(sb.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestExtractToolCalls(t *testing.T) {
	big := strings.Repeat("é", 700000) // ~1.4MB line
	path := writeFixture(t,
		record("assistant", "2026-10-15T10:00:00Z",
			map[string]any{"type": "text", "text": "working"},
			use("t0", "Bash", map[string]any{"command": "ls  -la\n   foo"}),
			use("t1", "Edit", map[string]any{"file_path": "/v/root/areas/eng/note-a.md", "old_string": "a", "new_string": "b"}),
		),
		record("user", "2026-10-15T10:00:01Z",
			result("t0", false, "file1\nfile2"),
			result("t1", true, []any{map[string]any{"type": "text", "text": "edit failed"}}),
		),
		record("assistant", "2026-10-15T10:00:02Z", use("t2", "Grep", map[string]any{"pattern": "foo", "path": "/src"})),
		"{not json at all",
		record("assistant", "2026-10-15T10:00:03Z", use("t3", "mcp__doit__add_todo", map[string]any{"text": "x", "list": "daily"})),
		record("user", "2026-10-15T10:00:04Z", result("t3", false, "Added todo: x [id:abc123]\nsecond line [id:zzz]")),
		record("assistant", "2026-10-15T10:00:05Z",
			use("t4", "Write", map[string]any{"file_path": "/v/root/daily/2026-10-15.md", "content": "d"}),
			use("t5", "Write", map[string]any{"file_path": "/v/root/templates/t.md", "content": "t"}),
			use("t6", "Write", map[string]any{"file_path": "/repo/.giantmem/handoff.md", "content": "h"}),
		),
		record("assistant", "2026-10-15T10:00:06Z", use("t7", "Artifact", map[string]any{"file_path": "/tmp/x/report.html"})),
		record("user", "2026-10-15T10:00:07Z", result("t7", false, "Published https://claude.ai/code/artifact/abc-123 ok")),
		record("assistant", "2026-10-15T10:00:08Z", use("t8", "Artifact", map[string]any{"action": "read", "url": "https://claude.ai/artifact/zzz"})),
		record("user", "2026-10-15T10:00:09Z", result("t8", false, "content of https://claude.ai/artifact/zzz")),
		record("assistant", "2026-10-15T10:00:10Z", use("t9", "Artifact", map[string]any{"url": "https://claude.ai/artifact/qqq", "asset": true, "file_path": "/x/img.png"})),
		record("user", "2026-10-15T10:00:11Z", result("t9", false, "uploaded https://claude.ai/artifact/qqq")),
		record("assistant", "2026-10-15T10:00:12Z", use("t10", "Bash", map[string]any{"command": "python3 ~/x/note.py new --title X"})),
		record("user", "2026-10-15T10:00:13Z", result("t10", false, "/v/root/areas/eng/note-b.md\nnot a path\n/elsewhere/x.md\n/v/root/daily/d.md\n")),
		record("assistant", "2026-10-15T10:00:14Z", use("t11", "Bash", map[string]any{"command": "glab mr view"})),
		record("user", "2026-10-15T10:00:15Z", result("t11", false,
			"see https://gitlab.rechargeapps.net/grp/proj/-/merge_requests/812 and https://github.com/o/r/pull/45 "+
				"and https://gitlab.rechargeapps.net/grp/proj/-/merge_requests/812 again")),
		record("assistant", "2026-10-15T10:00:16Z",
			use("t12", "Edit", map[string]any{"file_path": "/v/root/areas/eng/note-a.md"}),
			use("t13", "Skill", map[string]any{"skill": "wrap", "args": "--no-handoff"}),
			use("t14", "Read", map[string]any{"file_path": "/big.txt"}),
		),
		record("user", "2026-10-15T10:00:17Z", result("t14", false, big)),
	)

	calls, links, err := ExtractToolCalls(path, "/v/root/")
	if err != nil {
		t.Fatal(err)
	}
	if len(calls) != 15 {
		t.Fatalf("got %d calls, want 15", len(calls))
	}
	for i, c := range calls {
		if c.Seq != i {
			t.Errorf("call %d Seq = %d", i, c.Seq)
		}
	}

	want := []struct {
		summary    string
		isErr      bool
		resultHead string
	}{
		{"ls -la foo", false, "file1\nfile2"},
		{"/v/root/areas/eng/note-a.md", true, "edit failed"},
		{"foo in /src", false, ""},
		{`{"list":"daily","text":"x"}`, false, "Added todo: x [id:abc123]\nsecond line [id:zzz]"},
	}
	for i, w := range want {
		c := calls[i]
		if c.Summary != w.summary || c.IsError != w.isErr || c.ResultHead != w.resultHead {
			t.Errorf("call %d = {%q %v %q}, want {%q %v %q}", i, c.Summary, c.IsError, c.ResultHead, w.summary, w.isErr, w.resultHead)
		}
	}
	if c := calls[0]; c.ID != "t0" || c.Tool != "Bash" || c.TS != "2026-10-15T10:00:00Z" || c.Input != `{"command":"ls  -la\n   foo"}` {
		t.Errorf("call 0 fields = %+v", c)
	}
	if got := calls[7].Summary; got != "publish /tmp/x/report.html" {
		t.Errorf("artifact summary = %q", got)
	}
	if got := calls[8].Summary; got != "read https://claude.ai/artifact/zzz" {
		t.Errorf("artifact read summary = %q", got)
	}
	if got := calls[13].Summary; got != "wrap --no-handoff" {
		t.Errorf("skill summary = %q", got)
	}
	if got := calls[14].ResultHead; got != strings.Repeat("é", 500) {
		t.Errorf("big ResultHead has %d runes", len([]rune(got)))
	}

	wantLinks := []Link{
		{Kind: "vault", Target: "/v/root/areas/eng/note-a.md", Label: "note-a", TS: "2026-10-15T10:00:00Z"},
		{Kind: "doit", Target: "abc123", Label: "Added todo: x [id:abc123]", TS: "2026-10-15T10:00:04Z"},
		{Kind: "handoff", Target: "/repo/.giantmem/handoff.md", Label: "handoff", TS: "2026-10-15T10:00:05Z"},
		{Kind: "artifact", Target: "https://claude.ai/code/artifact/abc-123", Label: "report.html", TS: "2026-10-15T10:00:07Z"},
		{Kind: "vault", Target: "/v/root/areas/eng/note-b.md", Label: "note-b", TS: "2026-10-15T10:00:13Z"},
		{Kind: "mr_ref", Target: "https://gitlab.rechargeapps.net/grp/proj/-/merge_requests/812", Label: "proj!812", TS: "2026-10-15T10:00:15Z"},
		{Kind: "mr_ref", Target: "https://github.com/o/r/pull/45", Label: "r#45", TS: "2026-10-15T10:00:15Z"},
	}
	if len(links) != len(wantLinks) {
		t.Fatalf("got %d links %+v, want %d", len(links), links, len(wantLinks))
	}
	for i := range wantLinks {
		if links[i] != wantLinks[i] {
			t.Errorf("link %d = %+v, want %+v", i, links[i], wantLinks[i])
		}
	}
}

func TestExtractToolCallsNoVaultRoot(t *testing.T) {
	path := writeFixture(t,
		record("assistant", "", use("a", "Write", map[string]any{"file_path": "/v/root/areas/n.md"})),
	)
	calls, links, err := ExtractToolCalls(path, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(calls) != 1 || calls[0].TS != "" || len(links) != 0 {
		t.Fatalf("calls=%+v links=%+v", calls, links)
	}
}

func TestExtractVaultHandoff(t *testing.T) {
	path := writeFixture(t,
		record("assistant", "2026-10-15T10:00:00Z",
			use("a", "Write", map[string]any{"file_path": "/v/root/handoffs/proj/main.md"}),
			use("b", "Edit", map[string]any{"file_path": "/v/root/handoffs-old/x.md"}),
		),
	)
	_, links, err := ExtractToolCalls(path, "/v/root")
	if err != nil {
		t.Fatal(err)
	}
	want := []Link{
		{Kind: "handoff", Target: "/v/root/handoffs/proj/main.md", Label: "handoff", TS: "2026-10-15T10:00:00Z"},
		{Kind: "vault", Target: "/v/root/handoffs-old/x.md", Label: "x", TS: "2026-10-15T10:00:00Z"},
	}
	if len(links) != len(want) {
		t.Fatalf("got %d links %+v, want %d", len(links), links, len(want))
	}
	for i := range want {
		if links[i] != want[i] {
			t.Errorf("link %d = %+v, want %+v", i, links[i], want[i])
		}
	}
}

func TestExtractMRLinks(t *testing.T) {
	const (
		created  = "https://gitlab.rechargeapps.net/identity/recharge-auth/-/merge_requests/63"
		pushed   = "https://gitlab.rechargeapps.net/ai-tooling/investigator-agent/-/merge_requests/62"
		other    = "https://gitlab.rechargeapps.net/identity/recharge-auth/-/merge_requests/62"
		mention  = "https://github.com/o/r/pull/7"
		failedPR = "https://github.com/o/r/pull/8"
	)
	withCwd := func(rec map[string]any, cwd string) map[string]any {
		rec["cwd"] = cwd
		return rec
	}
	prLink := func(ts, u string) map[string]any {
		return map[string]any{"type": "pr-link", "sessionId": "s", "prNumber": 1, "prUrl": u, "timestamp": ts}
	}
	path := writeFixture(t,
		withCwd(record("assistant", "2026-10-16T01:00:00Z",
			use("c1", "Bash", map[string]any{"command": "D=$(cat d.md) && glab mr create --title x --description \"$D\" && glab mr list"}),
		), "/wt/better-redis-search"),
		withCwd(record("user", "2026-10-16T01:00:01Z",
			result("c1", false, "Creating merge request for b into main in identity/recharge-auth\n\n"+created+"\n"+other),
		), "/wt/better-redis-search"),
		prLink("2026-10-16T01:00:02Z", created),
		withCwd(record("assistant", "2026-10-16T01:00:03Z", use("c2", "Bash", map[string]any{"command": "git push"})), "/wt/main"),
		withCwd(record("user", "2026-10-16T01:00:04Z", result("c2", false, "View merge request: "+pushed)), "/wt/main"),
		prLink("2026-10-16T01:00:05Z", pushed),
		record("assistant", "2026-10-16T01:00:06Z", use("c3", "Bash", map[string]any{"command": "gh pr create --fill"})),
		record("user", "2026-10-16T01:00:07Z", result("c3", true, "pull request already exists: "+failedPR)),
		record("assistant", "2026-10-16T01:00:08Z", use("c4", "Bash", map[string]any{"command": "glab mr view"})),
		record("user", "2026-10-16T01:00:09Z", result("c4", false, "see "+mention)),
		prLink("2026-10-16T01:00:10Z", created),
	)
	_, links, err := ExtractToolCalls(path, "")
	if err != nil {
		t.Fatal(err)
	}
	want := []Link{
		{Kind: "mr", Target: created, Label: "recharge-auth!63", TS: "2026-10-16T01:00:01Z", Role: "opened", Cwd: "/wt/better-redis-search"},
		{Kind: "mr_ref", Target: other, Label: "recharge-auth!62", TS: "2026-10-16T01:00:01Z"},
		{Kind: "mr", Target: pushed, Label: "investigator-agent!62", TS: "2026-10-16T01:00:05Z", Role: "touched", Cwd: "/wt/main"},
		{Kind: "mr_ref", Target: failedPR, Label: "r#8", TS: "2026-10-16T01:00:07Z"},
		{Kind: "mr_ref", Target: mention, Label: "r#7", TS: "2026-10-16T01:00:09Z"},
	}
	if len(links) != len(want) {
		t.Fatalf("got %d links %+v, want %d", len(links), links, len(want))
	}
	for i := range want {
		if links[i] != want[i] {
			t.Errorf("link %d = %+v, want %+v", i, links[i], want[i])
		}
	}
}

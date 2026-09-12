package health

import "testing"

func TestHookWired(t *testing.T) {
	const dispatchBody = `"command": "python3 ~/.claude/hooks/dispatch.py --event PostToolUse live_index caveman_artifact_nudge"`
	const directBody = `"command": "python3 ~/.claude/hooks/precompact_capture.py"`
	const crossStringBody = `"command": "python3 ~/.claude/hooks/dispatch.py --event Stop debug_stop_check",
	"note": "live_index"`

	cases := []struct {
		name   string
		body   string
		script string
		want   bool
	}{
		{"dispatch bare module", dispatchBody, "live_index.py", true},
		{"direct script path", directBody, "precompact_capture.py", true},
		{"absent", directBody, "live_index.py", false},
		{"does not cross a json string boundary", crossStringBody, "live_index.py", false},
		{"prefix is not a match", `"command": "dispatch.py live_index_v2"`, "live_index.py", false},
	}
	for _, tc := range cases {
		if got := HookWired(tc.body, tc.script); got != tc.want {
			t.Errorf("%s: HookWired(_, %q) = %v, want %v", tc.name, tc.script, got, tc.want)
		}
	}
}

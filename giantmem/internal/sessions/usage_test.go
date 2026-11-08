package sessions

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExtractUsage(t *testing.T) {
	p := filepath.Join(t.TempDir(), "s.jsonl")
	lines := []string{
		`{"type":"attachment","timestamp":"2026-11-01T10:00:00Z","attachment":{"type":"credential_org","organizationUuid":"org-a"}}`,
		`{"type":"assistant","timestamp":"2026-11-01T10:00:01Z","requestId":"r1","message":{"id":"m1","model":"claude-opus-5-5","usage":{"input_tokens":2,"cache_creation_input_tokens":100,"cache_read_input_tokens":1000,"output_tokens":10,"cache_creation":{"ephemeral_5m_input_tokens":40,"ephemeral_1h_input_tokens":60}}}}`,
		`{"type":"assistant","timestamp":"2026-11-01T10:00:02Z","requestId":"r1","message":{"id":"m1","model":"claude-opus-5-5","usage":{"input_tokens":2,"cache_creation_input_tokens":100,"cache_read_input_tokens":1000,"output_tokens":55,"cache_creation":{"ephemeral_5m_input_tokens":40,"ephemeral_1h_input_tokens":60}}}}`,
		`{"type":"assistant","timestamp":"2026-11-01T10:00:03Z","message":{"id":"m2","model":"<synthetic>","usage":{"output_tokens":1}}}`,
		`{"type":"assistant","timestamp":"2026-11-01T10:00:04Z","message":{"id":"m2b","model":"claude-opus-5-5","content":[{"type":"text","text":"no usage"}]}}`,
		`{"type":"attachment","timestamp":"2026-11-01T11:00:00Z","attachment":{"type":"credential_org","organizationUuid":"org-b"}}`,
		`{"type":"assistant","timestamp":"2026-11-01T11:00:01Z","message":{"id":"m3","model":"claude-sonnet-5-5","usage":{"input_tokens":5,"cache_creation_input_tokens":7,"cache_read_input_tokens":0,"output_tokens":3}}}`,
		`{"type":"user","timestamp":"2026-11-01T11:00:02Z","message":{"content":"ok"}}`,
		`not json`,
	}
	if err := os.WriteFile(p, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := ExtractUsage(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("rows = %+v, want 2", got)
	}
	want0 := Usage{MessageID: "m1", TS: "2026-11-01T10:00:02Z", Org: "org-a", Model: "claude-opus-5-5", Input: 2, Cache5m: 40, Cache1h: 60, CacheRead: 1000, Output: 55}
	if got[0] != want0 {
		t.Errorf("m1 = %+v, want %+v", got[0], want0)
	}
	want1 := Usage{MessageID: "m3", TS: "2026-11-01T11:00:01Z", Org: "org-b", Model: "claude-sonnet-5-5", Input: 5, Cache5m: 7, Output: 3}
	if got[1] != want1 {
		t.Errorf("m3 = %+v, want %+v", got[1], want1)
	}
}

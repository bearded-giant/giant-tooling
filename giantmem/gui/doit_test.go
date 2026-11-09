package main

import (
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

type stubCall struct {
	Tool       string         `json:"tool"`
	Args       map[string]any `json:"args"`
	Source     string         `json:"source"`
	ActiveList string         `json:"active_list"`
	DataDir    string         `json:"data_dir"`
}

// a doitd stand-in on a unix socket: records every /call and answers with reply's text or error
func stubDoitd(t *testing.T, reply func(c stubCall) (string, error)) *[]stubCall {
	t.Helper()
	// t.TempDir() paths run past the 104-byte unix socket limit on macOS
	dir, err := os.MkdirTemp("/tmp", "doit-gui-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	t.Setenv("DOIT_STATE_DIR", dir)
	ln, err := net.Listen("unix", filepath.Join(dir, "doitd.sock"))
	if err != nil {
		t.Fatal(err)
	}
	calls := &[]stubCall{}
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var c stubCall
		json.NewDecoder(r.Body).Decode(&c)
		*calls = append(*calls, c)
		text, err := reply(c)
		if err != nil {
			json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": err.Error()})
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"ok": true, "text": text, "lists": []string{c.ActiveList}})
	})}
	go srv.Serve(ln)
	t.Cleanup(func() { srv.Close() })
	return calls
}

func okReply(c stubCall) (string, error) { return "ok", nil }

func doitFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("DOIT_DATA_DIR", dir)
	lists := filepath.Join(dir, "lists")
	os.MkdirAll(lists, 0o755)
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(lists, name+".json"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("proj", `{
  "todos": [
    {"id": "a", "text": "owed in daily #1: rotate <key> & test", "done": false, "in_progress": true,
     "order_index": 1, "created_at": 1793183277123, "future_field": {"x": [1, 2]},
     "twin": {"list": "daily", "id": "da"}, "description": "why\n\n\n----------\nlast modified: 2026-01-01: 00:00"},
    {"id": "b", "text": "second", "done": false, "in_progress": false, "order_index": 2},
    {"id": "c", "text": "child", "done": false, "order_index": 3, "parent_id": "b", "depth": 1}
  ],
  "notes": [{"id": "n1", "title": "t", "body": "b"}],
  "_metadata": {"created_at": 1, "source": "user"}
}`)
	write("daily", `{"todos": [{"id": "da", "text": "1. proj: rotate key", "done": false, "in_progress": true,
  "twin": {"list": "proj", "id": "a"}}], "_metadata": {}}`)
	write("broken", `{"todos": [`)
	return lists
}

func lastCall(t *testing.T, calls *[]stubCall, tool string) stubCall {
	t.Helper()
	if len(*calls) == 0 {
		t.Fatalf("no call reached doitd, wanted %s", tool)
	}
	c := (*calls)[len(*calls)-1]
	if c.Tool != tool {
		t.Fatalf("tool = %s, want %s", c.Tool, tool)
	}
	if c.Source != "user" {
		t.Fatalf("source = %q, want user", c.Source)
	}
	return c
}

func wantArgs(t *testing.T, c stubCall, want map[string]any) {
	t.Helper()
	for k, v := range want {
		if !reflect.DeepEqual(c.Args[k], v) {
			t.Errorf("%s: args[%s] = %#v, want %#v", c.Tool, k, c.Args[k], v)
		}
	}
}

func TestDoitStatusTools(t *testing.T) {
	calls := stubDoitd(t, okReply)
	a := &App{}
	for status, tool := range map[string]string{"in_progress": "start_todo", "done": "complete_todo", "pending": "revert_todo"} {
		if err := a.DoitSetStatus("proj", "b", status); err != nil {
			t.Fatal(err)
		}
		c := lastCall(t, calls, tool)
		wantArgs(t, c, map[string]any{"id": "b", "list": "proj"})
		if c.ActiveList != "proj" {
			t.Fatalf("active list = %q", c.ActiveList)
		}
	}
	n := len(*calls)
	if err := a.DoitSetStatus("proj", "a", "bogus"); err == nil || len(*calls) != n {
		t.Fatal("unknown status should error before any call")
	}
}

func TestDoitUpdateArgs(t *testing.T) {
	calls := stubDoitd(t, okReply)
	a := &App{}
	e := DoitEdit{Text: "second", Description: "why", Priority: "urgent", Due: "2026-11-01", Type: "bug", Deps: "3, #7", Claude: true, Sequence: 1, Worktree: " wt1 ", Feature: "f1"}
	if err := a.DoitUpdate("proj", "b", e); err != nil {
		t.Fatal(err)
	}
	c := lastCall(t, calls, "update_todo")
	wantArgs(t, c, map[string]any{
		"id": "b", "list": "proj", "text": "second", "description": "why", "priority": "urgent", "due": "2026-11-01",
		"type": "bug", "deps": []any{"3", "7"}, "claude": true, "sequence": float64(1), "worktree": "wt1", "feature": "f1",
	})
	if err := a.DoitUpdate("proj", "b", DoitEdit{Text: "x"}); err != nil {
		t.Fatal(err)
	}
	c = lastCall(t, calls, "update_todo")
	wantArgs(t, c, map[string]any{"priority": "none", "due": "", "deps": []any{}, "sequence": float64(0), "claude": false})
	if _, ok := c.Args["type"]; ok {
		t.Fatal("an empty type must not be sent, the item keeps its own")
	}
	n := len(*calls)
	for _, bad := range []DoitEdit{{Text: " "}, {Text: "x", Priority: "high"}, {Text: "x", Due: "11/01/2026"}, {Text: "x", Sequence: -1}} {
		if err := a.DoitUpdate("proj", "a", bad); err == nil {
			t.Errorf("edit %+v should be rejected", bad)
		}
	}
	if len(*calls) != n {
		t.Fatal("rejected edits must not reach doitd")
	}
}

func TestDoitAddDeleteMove(t *testing.T) {
	calls := stubDoitd(t, func(c stubCall) (string, error) {
		if c.Tool == "add_todo" {
			return `Added to "proj": fresh [id:1794250800_0000001]`, nil
		}
		return "ok", nil
	})
	a := &App{}
	id, err := a.DoitAdd("proj", "  fresh  ")
	if err != nil || id != "1794250800_0000001" {
		t.Fatalf("add = %q, %v", id, err)
	}
	wantArgs(t, lastCall(t, calls, "add_todo"), map[string]any{"list": "proj", "text": "fresh"})
	n := len(*calls)
	if _, err := a.DoitAdd("proj", ""); err == nil || len(*calls) != n {
		t.Fatal("empty add should error without a call")
	}

	if err := a.DoitDelete("proj", "a"); err != nil {
		t.Fatal(err)
	}
	wantArgs(t, lastCall(t, calls, "delete_todo"), map[string]any{"id": "a", "list": "proj"})

	if err := a.DoitMove("src", "s1", "dst"); err != nil {
		t.Fatal(err)
	}
	c := lastCall(t, calls, "move_todo")
	wantArgs(t, c, map[string]any{"id": "s1", "from_list": "src", "to_list": "dst"})
	if c.ActiveList != "src" {
		t.Fatalf("active list = %q", c.ActiveList)
	}
	n = len(*calls)
	if err := a.DoitMove("dst", "s1", "dst"); err == nil || len(*calls) != n {
		t.Fatal("same-list move should error without a call")
	}
}

func TestDoitNotes(t *testing.T) {
	calls := stubDoitd(t, func(c stubCall) (string, error) {
		if c.Tool == "create_note" {
			return `Created note on "daily": standup  [id:1794250800_0000002]`, nil
		}
		return "ok", nil
	})
	a := &App{}
	id, err := a.DoitNoteAdd("daily", " standup ", "line 1\nline 2")
	if err != nil || id != "1794250800_0000002" {
		t.Fatalf("note add = %q, %v", id, err)
	}
	wantArgs(t, lastCall(t, calls, "create_note"), map[string]any{"list": "daily", "title": "standup", "body": "line 1\nline 2"})
	if err := a.DoitNoteUpdate("proj", "n1", "renamed", "new body"); err != nil {
		t.Fatal(err)
	}
	wantArgs(t, lastCall(t, calls, "update_note"), map[string]any{"list": "proj", "id": "n1", "title": "renamed", "body": "new body"})
	if err := a.DoitNoteDelete("proj", "n1"); err != nil {
		t.Fatal(err)
	}
	wantArgs(t, lastCall(t, calls, "delete_note"), map[string]any{"list": "proj", "id": "n1"})
	n := len(*calls)
	if _, err := a.DoitNoteAdd("proj", " ", ""); err == nil || len(*calls) != n {
		t.Fatal("empty note should be rejected without a call")
	}
}

func quietTmux(t *testing.T) {
	t.Helper()
	orig := retagDoitWindows
	retagDoitWindows = func(string, string) {}
	t.Cleanup(func() { retagDoitWindows = orig })
}

func TestDoitListOps(t *testing.T) {
	lists := doitFixture(t)
	calls := stubDoitd(t, okReply)
	quietTmux(t)
	a := &App{}
	session := filepath.Join(filepath.Dir(lists), "session.json")
	os.WriteFile(session, []byte(`{"sessions": {"w1": "proj", "w2": "daily"}, "active_list": "proj"}`), 0o644)

	if err := a.DoitListCreate(" fresh-list "); err != nil {
		t.Fatal(err)
	}
	wantArgs(t, lastCall(t, calls, "create_list"), map[string]any{"name": "fresh-list", "source": "user"})
	n := len(*calls)
	for _, bad := range []string{"a b", "../x", "", strings.Repeat("x", 25)} {
		if err := a.DoitListCreate(bad); err == nil {
			t.Errorf("create %q should fail", bad)
		}
	}
	if len(*calls) != n {
		t.Fatal("bad names must not reach doitd")
	}

	if err := a.DoitListRename("proj", "proj2"); err != nil {
		t.Fatal(err)
	}
	wantArgs(t, lastCall(t, calls, "rename_list"), map[string]any{"old_name": "proj", "new_name": "proj2"})
	s := readJSONFile(t, session)
	if s["sessions"].(map[string]any)["w1"] != "proj2" || s["sessions"].(map[string]any)["w2"] != "daily" || s["active_list"] != "proj2" {
		t.Fatalf("session links should follow the rename: %v", s)
	}

	if err := a.DoitListDelete("proj2"); err != nil {
		t.Fatal(err)
	}
	wantArgs(t, lastCall(t, calls, "delete_list"), map[string]any{"name": "proj2"})
	if s := readJSONFile(t, session); s["sessions"].(map[string]any)["w1"] != nil {
		t.Fatalf("window link to the deleted list should drop: %v", s)
	}
}

func readJSONFile(t *testing.T, p string) map[string]any {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	var d map[string]any
	if err := json.Unmarshal(b, &d); err != nil {
		t.Fatal(err)
	}
	return d
}

func TestDoitCallErrors(t *testing.T) {
	stubDoitd(t, func(c stubCall) (string, error) { return "", errors.New(`"daily" is permanent and cannot be deleted.`) })
	quietTmux(t)
	a := &App{}
	err := a.DoitListDelete("daily")
	if err == nil || !strings.Contains(err.Error(), "permanent") {
		t.Fatalf("the tool's error should come back verbatim: %v", err)
	}

	// no daemon and no CLI: a clear error, no write anywhere
	dir, _ := os.MkdirTemp("/tmp", "doit-gui-")
	t.Cleanup(func() { os.RemoveAll(dir) })
	t.Setenv("DOIT_STATE_DIR", dir)
	t.Setenv("PATH", dir)
	t.Setenv("HOME", dir)
	if err := a.DoitSetStatus("proj", "a", "done"); err == nil || !strings.Contains(err.Error(), "doitd not reachable") {
		t.Fatalf("err = %v", err)
	}
}

func TestDoitSyncState(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("DOIT_STATE_DIR", dir)
	a := &App{}
	if s := a.DoitSyncState(); s.Mode != "local" || s.Queue != 0 {
		t.Fatalf("no state file should read as local: %+v", s)
	}
	os.MkdirAll(filepath.Join(dir, "sync"), 0o755)
	os.WriteFile(filepath.Join(dir, "sync", "state.json"), []byte(`{"mode":"sync","online":false,"queue":3,"conflicts":1,"error":"401"}`), 0o644)
	s := a.DoitSyncState()
	if s.Mode != "sync" || s.Online || s.Queue != 3 || s.Conflicts != 1 || s.Error != "401" {
		t.Fatalf("state = %+v", s)
	}
}

func TestDoitListsAndGuards(t *testing.T) {
	doitFixture(t)
	a := &App{}
	if !a.DoitAvailable() {
		t.Fatal("lists dir exists, doit should be available")
	}
	ls, err := a.DoitLists()
	if err != nil || len(ls) != 3 {
		t.Fatalf("lists = %v, %v", ls, err)
	}
	if ls[0].Name != "broken" || ls[0].Error == "" || ls[2].Name != "proj" || len(ls[2].Todos) != 3 || ls[2].Todos[0].Twin.List != "daily" || ls[2].UpdatedAt == 0 {
		t.Fatalf("a corrupt list should carry its error, not sink the rest: %+v", ls)
	}
	if a.DoitMtime() == 0 {
		t.Fatal("mtime should be set")
	}
	t.Setenv("DOIT_DATA_DIR", filepath.Join(t.TempDir(), "none"))
	if a.DoitAvailable() {
		t.Fatal("missing dir should hide doit")
	}
}

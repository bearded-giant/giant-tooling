package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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

func readDoitRaw(t *testing.T, lists, name string) map[string]any {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(lists, name+".json"))
	if err != nil {
		t.Fatal(err)
	}
	var d map[string]any
	if err := json.Unmarshal(b, &d); err != nil {
		t.Fatal(err)
	}
	return d
}

func rawTodo(d map[string]any, id string) map[string]any {
	for _, v := range d["todos"].([]any) {
		if m := v.(map[string]any); m["id"] == id {
			return m
		}
	}
	return nil
}

func TestDoitStatusCycleAndTwin(t *testing.T) {
	lists := doitFixture(t)
	a := &App{}

	if err := a.DoitSetStatus("proj", "b", "in_progress"); err != nil {
		t.Fatal(err)
	}
	d := readDoitRaw(t, lists, "proj")
	if rawTodo(d, "a")["in_progress"] != false || rawTodo(d, "b")["in_progress"] != true {
		t.Fatalf("start should stop the other active item: %v", d["todos"])
	}
	if _, ok := rawTodo(d, "c")["in_progress"]; ok {
		t.Fatal("start should not add in_progress to items that never had it")
	}
	if rawTodo(readDoitRaw(t, lists, "daily"), "da")["in_progress"] != true {
		t.Fatal("start must not touch the twin")
	}

	if err := a.DoitSetStatus("proj", "a", "done"); err != nil {
		t.Fatal(err)
	}
	a1, da := rawTodo(readDoitRaw(t, lists, "proj"), "a"), rawTodo(readDoitRaw(t, lists, "daily"), "da")
	if a1["done"] != true || a1["completed_at"] == nil || da["done"] != true || da["completed_at"] != a1["completed_at"] {
		t.Fatalf("done should sync completed_at to the twin: %v / %v", a1, da)
	}

	if err := a.DoitSetStatus("proj", "a", "pending"); err != nil {
		t.Fatal(err)
	}
	a1, da = rawTodo(readDoitRaw(t, lists, "proj"), "a"), rawTodo(readDoitRaw(t, lists, "daily"), "da")
	if a1["done"] != false || a1["completed_at"] != nil || da["done"] != false || da["completed_at"] != nil {
		t.Fatalf("pending should clear done on both twins: %v / %v", a1, da)
	}
	if err := a.DoitSetStatus("proj", "a", "bogus"); err == nil {
		t.Fatal("unknown status should error")
	}
}

func TestDoitWritePreservesUnknownFields(t *testing.T) {
	lists := doitFixture(t)
	a := &App{}
	if err := a.DoitSetStatus("proj", "b", "done"); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(lists, "proj.json"))
	s := string(b)
	for _, want := range []string{`"created_at": 1793183277123`, `"future_field": {`, `<key> & test`, `"source": "user"`, `"notes": [`} {
		if !strings.Contains(s, want) {
			t.Errorf("write lost %q", want)
		}
	}
	if left, _ := filepath.Glob(filepath.Join(lists, "*.tmp")); len(left) > 0 {
		t.Fatalf("tmp files left behind: %v", left)
	}
}

func TestDoitUpdate(t *testing.T) {
	lists := doitFixture(t)
	a := &App{}
	orig := rawTodo(readDoitRaw(t, lists, "proj"), "a")["description"]

	if err := a.DoitUpdate("proj", "a", DoitEdit{Text: "new text", Description: "why\n", Priority: "urgent", Due: "2026-11-01"}); err != nil {
		t.Fatal(err)
	}
	a1, da := rawTodo(readDoitRaw(t, lists, "proj"), "a"), rawTodo(readDoitRaw(t, lists, "daily"), "da")
	if a1["text"] != "new text" || a1["description"] != orig {
		t.Fatalf("unchanged body should keep its stamp: %v", a1)
	}
	if da["priorities"] != "urgent" || da["due_date"] != "2026-11-01" || da["text"] != "1. proj: rotate key" {
		t.Fatalf("priority/due should sync to the twin, text should not: %v", da)
	}

	if err := a.DoitUpdate("proj", "a", DoitEdit{Text: "new text", Description: "changed"}); err != nil {
		t.Fatal(err)
	}
	a1, da = rawTodo(readDoitRaw(t, lists, "proj"), "a"), rawTodo(readDoitRaw(t, lists, "daily"), "da")
	desc := a1["description"].(string)
	if !strings.HasPrefix(desc, "changed\n\n\n----------\nlast modified: ") || strings.Count(desc, "----------") != 1 {
		t.Fatalf("changed body should get one fresh footer: %q", desc)
	}
	if _, ok := da["priorities"]; ok {
		t.Fatal("cleared priority should clear on the twin")
	}

	for _, e := range []DoitEdit{{Text: " "}, {Text: "x", Priority: "high"}, {Text: "x", Due: "11/01/2026"}} {
		if err := a.DoitUpdate("proj", "a", e); err == nil {
			t.Errorf("edit %+v should be rejected", e)
		}
	}
}

func TestDoitDeleteAndAdd(t *testing.T) {
	lists := doitFixture(t)
	a := &App{}
	if err := a.DoitDelete("proj", "a"); err != nil {
		t.Fatal(err)
	}
	if err := a.DoitDelete("proj", "b"); err != nil {
		t.Fatal(err)
	}
	d := readDoitRaw(t, lists, "proj")
	c := rawTodo(d, "c")
	if rawTodo(d, "a") != nil || c["parent_id"] != nil || c["depth"] != float64(0) {
		t.Fatalf("delete should drop the item and promote its child: %v", d["todos"])
	}
	if del := d["_metadata"].(map[string]any)["deleted_todos"].([]any); len(del) != 2 || del[0].(map[string]any)["id"] != "b" {
		t.Fatalf("deleted_todos should hold newest first: %v", del)
	}
	if len(readDoitRaw(t, lists, "daily")["todos"].([]any)) != 0 {
		t.Fatal("delete should remove the twin")
	}

	id, err := a.DoitAdd("proj", "  fresh  ")
	if err != nil {
		t.Fatal(err)
	}
	n := rawTodo(readDoitRaw(t, lists, "proj"), id)
	if n["text"] != "fresh" || n["order_index"] != float64(4) || n["source"] != "user" {
		t.Fatalf("added todo = %v", n)
	}
	if _, err := a.DoitAdd("proj", ""); err == nil {
		t.Fatal("empty add should error")
	}
}

func TestDoitNoteCRUD(t *testing.T) {
	lists := doitFixture(t)
	a := &App{}
	notes := func(name string) []any {
		n, _ := readDoitRaw(t, lists, name)["notes"].([]any)
		return n
	}

	id, err := a.DoitNoteAdd("daily", " standup ", "line 1\nline 2")
	if err != nil {
		t.Fatal(err)
	}
	n := notes("daily")
	if len(n) != 1 {
		t.Fatalf("list without notes should gain one: %v", n)
	}
	got := n[0].(map[string]any)
	if got["id"] != id || got["title"] != "standup" || got["body"] != "line 1\nline 2" || got["created_at"] == nil || got["updated_at"] == nil {
		t.Fatalf("created note = %v", got)
	}

	if err := a.DoitNoteUpdate("proj", "n1", "renamed", "new body"); err != nil {
		t.Fatal(err)
	}
	got = notes("proj")[0].(map[string]any)
	if got["title"] != "renamed" || got["body"] != "new body" || got["updated_at"] == nil {
		t.Fatalf("updated note = %v", got)
	}

	if err := a.DoitNoteDelete("proj", "n1"); err != nil {
		t.Fatal(err)
	}
	d := readDoitRaw(t, lists, "proj")
	if len(d["notes"].([]any)) != 0 {
		t.Fatal("delete should drop the note")
	}
	if del := d["_metadata"].(map[string]any)["deleted_notes"].([]any); len(del) != 1 || del[0].(map[string]any)["title"] != "renamed" {
		t.Fatalf("deleted note should land in deleted_notes: %v", del)
	}

	if _, err := a.DoitNoteAdd("proj", " ", ""); err == nil {
		t.Fatal("empty note should be rejected")
	}
	if err := a.DoitNoteUpdate("proj", "gone", "x", ""); err == nil {
		t.Fatal("missing note should error")
	}
	if err := a.DoitNoteDelete("proj", "gone"); err == nil {
		t.Fatal("missing note should error")
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
	for _, name := range []string{"", "../proj", ".hidden", "a/b"} {
		if err := a.DoitSetStatus(name, "a", "done"); err == nil {
			t.Errorf("list name %q should be rejected", name)
		}
	}
	t.Setenv("DOIT_DATA_DIR", filepath.Join(t.TempDir(), "none"))
	if a.DoitAvailable() {
		t.Fatal("missing dir should hide doit")
	}
}

func quietTmux(t *testing.T) {
	t.Helper()
	orig := retagDoitWindows
	retagDoitWindows = func(string, string) {}
	t.Cleanup(func() { retagDoitWindows = orig })
}

func TestDoitStartPerWorktreeSlot(t *testing.T) {
	lists := doitFixture(t)
	a := &App{}
	id, err := a.DoitAdd("proj", "other worktree")
	if err != nil {
		t.Fatal(err)
	}
	if err := a.DoitUpdate("proj", id, DoitEdit{Text: "other worktree", Worktree: "wt2"}); err != nil {
		t.Fatal(err)
	}
	if err := a.DoitSetStatus("proj", id, "in_progress"); err != nil {
		t.Fatal(err)
	}
	d := readDoitRaw(t, lists, "proj")
	if rawTodo(d, "a")["in_progress"] != true || rawTodo(d, id)["in_progress"] != true {
		t.Fatalf("a start in another worktree must leave the first slot alone: %v", d["todos"])
	}
	if err := a.DoitSetStatus("proj", "b", "in_progress"); err != nil {
		t.Fatal(err)
	}
	d = readDoitRaw(t, lists, "proj")
	if rawTodo(d, "a")["in_progress"] != false || rawTodo(d, "b")["in_progress"] != true || rawTodo(d, id)["in_progress"] != true {
		t.Fatalf("start should stop only the same slot: %v", d["todos"])
	}
}

func TestDoitUpdateComposesText(t *testing.T) {
	lists := doitFixture(t)
	a := &App{}
	e := DoitEdit{Text: "second", Type: "bug", Deps: "3, #7", Claude: true, Sequence: 1, Worktree: "wt1", Feature: "f1"}
	if err := a.DoitUpdate("proj", "b", e); err != nil {
		t.Fatal(err)
	}
	b := rawTodo(readDoitRaw(t, lists, "proj"), "b")
	if b["text"] != "claude: [bug] second (dep on #3, #7)" || b["worktree"] != "wt1" || b["feature"] != "f1" || b["sequence"] != float64(1) {
		t.Fatalf("composed = %v", b)
	}
	if err := a.DoitUpdate("proj", "c", DoitEdit{Text: "child", Sequence: 1}); err != nil {
		t.Fatal(err)
	}
	d := readDoitRaw(t, lists, "proj")
	if rawTodo(d, "c")["sequence"] != float64(1) || rawTodo(d, "b")["sequence"] != float64(2) {
		t.Fatalf("a colliding sequence should shift the other item down: %v", d["todos"])
	}
	if err := a.DoitUpdate("proj", "b", DoitEdit{Text: "second", Sequence: 0}); err != nil {
		t.Fatal(err)
	}
	b = rawTodo(readDoitRaw(t, lists, "proj"), "b")
	if b["text"] != "second" || b["sequence"] != nil || b["worktree"] != nil {
		t.Fatalf("cleared fields should drop: %v", b)
	}
	if err := a.DoitUpdate("daily", "da", DoitEdit{Text: "rotate", Type: "gate"}); err != nil {
		t.Fatal(err)
	}
	if got := rawTodo(readDoitRaw(t, lists, "daily"), "da")["text"]; got != "[gate] 1. rotate" {
		t.Fatalf("rank should carry over: %v", got)
	}
	if err := a.DoitUpdate("daily", "da", DoitEdit{Text: "[chore] 9. typed prefix", Type: "gate"}); err != nil {
		t.Fatal(err)
	}
	if got := rawTodo(readDoitRaw(t, lists, "daily"), "da")["text"]; got != "[chore] 9. typed prefix" {
		t.Fatalf("a prefix typed into the body should win: %v", got)
	}
	if err := a.DoitUpdate("proj", "b", DoitEdit{Text: "x", Sequence: -1}); err == nil {
		t.Fatal("negative sequence should be rejected")
	}
}

func TestDoitListCreateRenameDelete(t *testing.T) {
	lists := doitFixture(t)
	quietTmux(t)
	a := &App{}
	session := filepath.Join(filepath.Dir(lists), "session.json")
	os.WriteFile(session, []byte(`{"sessions": {"w1": "proj", "w2": "daily"}, "active_list": "proj"}`), 0o644)

	if err := a.DoitListCreate(" fresh-list "); err != nil {
		t.Fatal(err)
	}
	if d := readDoitRaw(t, lists, "fresh-list"); len(d["todos"].([]any)) != 0 || d["_metadata"].(map[string]any)["created_at"] == nil {
		t.Fatalf("created list = %v", d)
	}
	for _, bad := range []string{"fresh-list", "a b", "../x", "", strings.Repeat("x", 25)} {
		if err := a.DoitListCreate(bad); err == nil {
			t.Errorf("create %q should fail", bad)
		}
	}

	if err := a.DoitListRename("proj", "proj2"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(lists, "proj.json")); err == nil {
		t.Fatal("old file should be gone")
	}
	da := rawTodo(readDoitRaw(t, lists, "daily"), "da")
	if da["text"] != "1. proj2: rotate key" || da["twin"].(map[string]any)["list"] != "proj2" {
		t.Fatalf("twin should be relabeled and relinked: %v", da)
	}
	if rawTodo(readDoitRaw(t, lists, "proj2"), "a")["twin"].(map[string]any)["list"] != "daily" {
		t.Fatal("project side twin ref should be untouched")
	}
	s := readDoitRaw(t, filepath.Dir(lists), "session")
	if s["sessions"].(map[string]any)["w1"] != "proj2" || s["sessions"].(map[string]any)["w2"] != "daily" || s["active_list"] != "proj2" {
		t.Fatalf("session links should follow the rename: %v", s)
	}
	for _, c := range [][2]string{{"daily", "x"}, {"nope", "x"}, {"proj2", "daily"}, {"proj2", "bad name"}} {
		if err := a.DoitListRename(c[0], c[1]); err == nil {
			t.Errorf("rename %v should fail", c)
		}
	}

	if err := a.DoitListDelete("proj2"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(lists, "proj2.json")); err == nil {
		t.Fatal("deleted list should leave lists/")
	}
	if archived, _ := filepath.Glob(filepath.Join(filepath.Dir(lists), "deleted", "proj2-*.json")); len(archived) != 1 {
		t.Fatalf("deleted list should be archived: %v", archived)
	}
	da = rawTodo(readDoitRaw(t, lists, "daily"), "da")
	desc, _ := da["description"].(string)
	if da["twin"] != nil || !strings.Contains(desc, `notes from "proj2":`+"\nwhy") || !strings.Contains(desc, `list "proj2" deleted`) {
		t.Fatalf("surviving twin should be detached with the notes: %v", da)
	}
	if s := readDoitRaw(t, filepath.Dir(lists), "session"); s["sessions"].(map[string]any)["w1"] != nil {
		t.Fatalf("window link to the deleted list should drop: %v", s)
	}
	if err := a.DoitListDelete("daily"); err == nil {
		t.Fatal("daily is permanent")
	}
	if err := a.DoitListDelete("nope"); err == nil {
		t.Fatal("missing list should error")
	}
}

func TestDoitMove(t *testing.T) {
	lists := doitFixture(t)
	a := &App{}
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(lists, name+".json"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("src", `{"todos": [
  {"id": "s1", "text": "claude: [bug] 2. fix it (dep on #1, #9)", "done": false, "in_progress": true, "order_index": 1, "sequence": 1, "description": "keep\n\n\n----------\nlast modified: 2026-01-01: 00:00"},
  {"id": "s2", "text": "[x] 1. dep", "done": false, "order_index": 2}
], "_metadata": {}}`)
	write("dst", `{"todos": [
  {"id": "d1", "text": "7. existing", "done": false, "in_progress": true, "order_index": 3}
], "_metadata": {}}`)

	if err := a.DoitMove("src", "s1", "dst"); err != nil {
		t.Fatal(err)
	}
	if rawTodo(readDoitRaw(t, lists, "src"), "s1") != nil {
		t.Fatal("moved item should leave the source")
	}
	m := rawTodo(readDoitRaw(t, lists, "dst"), "s1")
	if m == nil || m["text"] != "claude: [bug] 8. fix it" || m["order_index"] != float64(4) || m["sequence"] != nil {
		t.Fatalf("moved item should take the next rank and order, lose its sequence: %v", m)
	}
	if desc, _ := m["description"].(string); !strings.HasPrefix(desc, "keep\n\nmoved from src #2; its deps there: #1, #9\n\n\n----------\nlast modified: ") {
		t.Fatalf("dropped deps should be noted: %q", desc)
	}
	if rawTodo(readDoitRaw(t, lists, "dst"), "d1")["in_progress"] != false {
		t.Fatal("an in-progress mover takes the slot")
	}

	if err := a.DoitMove("proj", "b", "dst"); err != nil {
		t.Fatal(err)
	}
	c := rawTodo(readDoitRaw(t, lists, "proj"), "c")
	if c["parent_id"] != nil || c["depth"] != float64(0) {
		t.Fatalf("children stay behind, promoted: %v", c)
	}
	if err := a.DoitMove("proj", "a", "dst"); err != nil {
		t.Fatal(err)
	}
	da := rawTodo(readDoitRaw(t, lists, "daily"), "da")
	if da["text"] != "1. dst: rotate key" || da["twin"].(map[string]any)["list"] != "dst" {
		t.Fatalf("owed twin should follow the move: %v", da)
	}
	for _, c := range [][3]string{{"dst", "s1", "dst"}, {"dst", "s1", "nope"}, {"dst", "zz", "src"}} {
		if err := a.DoitMove(c[0], c[1], c[2]); err == nil {
			t.Errorf("move %v should fail", c)
		}
	}
}

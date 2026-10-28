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
	if ls[0].Name != "broken" || ls[0].Error == "" || ls[2].Name != "proj" || len(ls[2].Todos) != 3 || ls[2].Todos[0].Twin.List != "daily" {
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

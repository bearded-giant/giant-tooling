package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

type DoitTwin struct {
	List string `json:"list"`
	ID   string `json:"id"`
}

type DoitTodo struct {
	ID          string    `json:"id"`
	Text        string    `json:"text"`
	Description string    `json:"description"`
	Done        bool      `json:"done"`
	InProgress  bool      `json:"in_progress"`
	Priority    string    `json:"priorities"`
	Due         string    `json:"due_date"`
	Sequence    float64   `json:"sequence"`
	OrderIndex  float64   `json:"order_index"`
	CreatedAt   float64   `json:"created_at"`
	CompletedAt float64   `json:"completed_at"`
	Source      string    `json:"source"`
	Twin        *DoitTwin `json:"twin,omitempty"`
}

type DoitNote struct {
	ID        string  `json:"id"`
	Title     string  `json:"title"`
	Body      string  `json:"body"`
	UpdatedAt float64 `json:"updated_at"`
}

type DoitList struct {
	Name  string     `json:"name"`
	Todos []DoitTodo `json:"todos"`
	Notes []DoitNote `json:"notes"`
	Error string     `json:"error,omitempty"`
}

type DoitEdit struct {
	Text        string `json:"text"`
	Description string `json:"description"`
	Priority    string `json:"priority"`
	Due         string `json:"due"`
}

func doitListsDir() string {
	if d := os.Getenv("DOIT_DATA_DIR"); d != "" {
		return filepath.Join(d, "lists")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "share", "nvim", "doit", "lists")
}

func (a *App) DoitAvailable() bool {
	st, err := os.Stat(doitListsDir())
	return err == nil && st.IsDir()
}

func (a *App) DoitLists() ([]DoitList, error) {
	paths, err := filepath.Glob(filepath.Join(doitListsDir(), "*.json"))
	if err != nil {
		return nil, err
	}
	sort.Strings(paths)
	out := make([]DoitList, 0, len(paths))
	for _, p := range paths {
		l := DoitList{Name: strings.TrimSuffix(filepath.Base(p), ".json")}
		// one corrupt list should not blank the whole view
		if b, err := os.ReadFile(p); err != nil {
			l.Error = err.Error()
		} else if err := json.Unmarshal(b, &l); err != nil {
			l.Error = err.Error()
		}
		if l.Todos == nil {
			l.Todos = []DoitTodo{}
		}
		if l.Notes == nil {
			l.Notes = []DoitNote{}
		}
		out = append(out, l)
	}
	return out, nil
}

// DoitMtime covers both write styles: tmp+rename (mcp, tmux) bumps the dir, in-place writes bump the file.
func (a *App) DoitMtime() int64 {
	dir := doitListsDir()
	var newest int64
	paths, _ := filepath.Glob(filepath.Join(dir, "*.json"))
	for _, p := range append(paths, dir) {
		if st, err := os.Stat(p); err == nil && st.ModTime().UnixNano() > newest {
			newest = st.ModTime().UnixNano()
		}
	}
	return newest
}

func (a *App) DoitSetStatus(list, id, status string) error {
	p, d, err := loadDoit(list)
	if err != nil {
		return err
	}
	t, _ := findDoitTodo(d, id)
	if t == nil {
		return fmt.Errorf("todo %s not in %s", id, list)
	}
	switch status {
	case "in_progress":
		for _, v := range doitTodos(d) {
			if o, ok := v.(map[string]any); ok && o["in_progress"] == true {
				o["in_progress"] = false
			}
		}
		t["in_progress"], t["done"] = true, false
		delete(t, "completed_at")
	case "done":
		t["done"], t["in_progress"], t["completed_at"] = true, false, time.Now().Unix()
	case "pending":
		t["done"], t["in_progress"] = false, false
		delete(t, "completed_at")
	default:
		return fmt.Errorf("unknown status %q", status)
	}
	if err := saveDoit(p, d); err != nil {
		return err
	}
	// start never touches the twin, so the owed copy's active item stays the user's
	if status == "in_progress" {
		return nil
	}
	return syncDoitTwin(t, func(_ map[string]any, tw map[string]any) {
		tw["done"], tw["in_progress"] = t["done"], false
		if c, ok := t["completed_at"]; ok {
			tw["completed_at"] = c
		} else {
			delete(tw, "completed_at")
		}
	})
}

func (a *App) DoitUpdate(list, id string, e DoitEdit) error {
	if strings.TrimSpace(e.Text) == "" {
		return errors.New("todo text is empty")
	}
	switch e.Priority {
	case "", "critical", "urgent", "important":
	default:
		return fmt.Errorf("unknown priority %q", e.Priority)
	}
	if e.Due != "" {
		if _, err := time.Parse("2006-01-02", e.Due); err != nil {
			return fmt.Errorf("due %q is not YYYY-MM-DD", e.Due)
		}
	}
	p, d, err := loadDoit(list)
	if err != nil {
		return err
	}
	t, _ := findDoitTodo(d, id)
	if t == nil {
		return fmt.Errorf("todo %s not in %s", id, list)
	}
	t["text"] = e.Text
	if old, _ := t["description"].(string); doitBody(old) != doitBody(e.Description) {
		t["description"] = doitStamp(e.Description)
	}
	prio := setOrDelete(t, "priorities", e.Priority)
	due := setOrDelete(t, "due_date", e.Due)
	if err := saveDoit(p, d); err != nil {
		return err
	}
	if !prio && !due {
		return nil
	}
	return syncDoitTwin(t, func(_ map[string]any, tw map[string]any) {
		if prio {
			setOrDelete(tw, "priorities", e.Priority)
		}
		if due {
			setOrDelete(tw, "due_date", e.Due)
		}
	})
}

func (a *App) DoitDelete(list, id string) error {
	p, d, err := loadDoit(list)
	if err != nil {
		return err
	}
	t := removeDoitTodo(d, id)
	if t == nil {
		return fmt.Errorf("todo %s not in %s", id, list)
	}
	if err := saveDoit(p, d); err != nil {
		return err
	}
	return syncDoitTwin(t, func(td map[string]any, tw map[string]any) {
		removeDoitTodo(td, tw["id"].(string))
	})
}

func (a *App) DoitAdd(list, text string) (string, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return "", errors.New("todo text is empty")
	}
	p, d, err := loadDoit(list)
	if err != nil {
		return "", err
	}
	todos := doitTodos(d)
	var maxOrder float64
	for _, v := range todos {
		if o, ok := v.(map[string]any); ok {
			if n, ok := o["order_index"].(json.Number); ok {
				if f, _ := n.Float64(); f > maxOrder {
					maxOrder = f
				}
			}
		}
	}
	now := time.Now().Unix()
	id := fmt.Sprintf("%d_%d", now, rand.IntN(9999999))
	d["todos"] = append(todos, map[string]any{
		"id":          id,
		"text":        text,
		"done":        false,
		"in_progress": false,
		"order_index": maxOrder + 1,
		"created_at":  now,
		"source":      "user",
		"description": doitStamp(""),
	})
	return id, saveDoit(p, d)
}

func doitPath(list string) (string, error) {
	if list == "" || list != filepath.Base(list) || strings.HasPrefix(list, ".") {
		return "", fmt.Errorf("bad list name %q", list)
	}
	return filepath.Join(doitListsDir(), list+".json"), nil
}

// lists decode into maps (UseNumber keeps ints exact) so fields this GUI does not know survive a write
func loadDoit(list string) (string, map[string]any, error) {
	p, err := doitPath(list)
	if err != nil {
		return "", nil, err
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return "", nil, err
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	var d map[string]any
	if err := dec.Decode(&d); err != nil {
		return "", nil, fmt.Errorf("%s: %w", list, err)
	}
	return p, d, nil
}

func saveDoit(p string, d map[string]any) error {
	doitMeta(d)["updated_at"] = time.Now().Unix()
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(d); err != nil {
		return err
	}
	// unique tmp: mcp and tmux both write <list>.json.tmp and would race on a shared name
	f, err := os.CreateTemp(filepath.Dir(p), filepath.Base(p)+".*.tmp")
	if err != nil {
		return err
	}
	_, werr := f.Write(buf.Bytes())
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr == nil {
		werr = os.Chmod(f.Name(), 0o644)
	}
	if werr == nil {
		werr = os.Rename(f.Name(), p)
	}
	if werr != nil {
		os.Remove(f.Name())
	}
	return werr
}

func doitMeta(d map[string]any) map[string]any {
	m, ok := d["_metadata"].(map[string]any)
	if !ok {
		m = map[string]any{}
		d["_metadata"] = m
	}
	return m
}

func doitTodos(d map[string]any) []any {
	t, _ := d["todos"].([]any)
	return t
}

func findDoitTodo(d map[string]any, id string) (map[string]any, int) {
	for i, v := range doitTodos(d) {
		if t, ok := v.(map[string]any); ok && t["id"] == id {
			return t, i
		}
	}
	return nil, -1
}

// mirrors mcp removeTodo: direct children go top level, last 10 deletions kept for undo
func removeDoitTodo(d map[string]any, id string) map[string]any {
	t, i := findDoitTodo(d, id)
	if t == nil {
		return nil
	}
	todos := doitTodos(d)
	seen := map[any]bool{id: true}
	for _, v := range todos {
		if c, ok := v.(map[string]any); ok && c["parent_id"] == id {
			delete(c, "parent_id")
			c["depth"] = 0
			redepthDoit(todos, c["id"], 1, seen)
		}
	}
	d["todos"] = append(todos[:i:i], todos[i+1:]...)
	meta := doitMeta(d)
	del, _ := meta["deleted_todos"].([]any)
	del = append([]any{t}, del...)
	if len(del) > 10 {
		del = del[:10]
	}
	meta["deleted_todos"] = del
	return t
}

func redepthDoit(todos []any, parentID any, depth int, seen map[any]bool) {
	if seen[parentID] {
		return
	}
	seen[parentID] = true
	for _, v := range todos {
		if c, ok := v.(map[string]any); ok && c["parent_id"] == parentID {
			c["depth"] = depth
			redepthDoit(todos, c["id"], depth+1, seen)
		}
	}
}

// mirrors mcp syncTwin: a twin whose list or item is gone is skipped, not an error
func syncDoitTwin(t map[string]any, mutate func(d map[string]any, twin map[string]any)) error {
	ref, _ := t["twin"].(map[string]any)
	list, _ := ref["list"].(string)
	id, _ := ref["id"].(string)
	if list == "" || id == "" {
		return nil
	}
	p, d, err := loadDoit(list)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	tw, _ := findDoitTodo(d, id)
	if tw == nil {
		return nil
	}
	mutate(d, tw)
	return saveDoit(p, d)
}

func setOrDelete(m map[string]any, key, val string) bool {
	old, _ := m[key].(string)
	if val == "" {
		delete(m, key)
	} else {
		m[key] = val
	}
	return old != val
}

var doitFooterRE = regexp.MustCompile(`\n*----------\nlast (updated|modified):[\s\S]*$`)

func doitBody(desc string) string {
	return strings.TrimRight(doitFooterRE.ReplaceAllString(desc, ""), " \t\r\n")
}

// same footer the mcp, tmux and nvim surfaces write, so a re-save refreshes it instead of stacking
func doitStamp(desc string) string {
	return doitBody(desc) + "\n\n\n----------\nlast modified: " + time.Now().Format("2006-01-02: 15:04")
}

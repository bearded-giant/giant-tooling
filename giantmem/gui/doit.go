package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
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
	Worktree    string    `json:"worktree"`
	Feature     string    `json:"feature"`
	ParentID    string    `json:"parent_id"`
	Depth       float64   `json:"depth"`
	Twin        *DoitTwin `json:"twin,omitempty"`
}

type DoitNote struct {
	ID        string  `json:"id"`
	Title     string  `json:"title"`
	Body      string  `json:"body"`
	CreatedAt float64 `json:"created_at"`
	UpdatedAt float64 `json:"updated_at"`
}

type DoitList struct {
	Name  string     `json:"name"`
	Todos []DoitTodo `json:"todos"`
	Notes []DoitNote `json:"notes"`
	Error string     `json:"error,omitempty"`
}

// Text is the body only; Type, Deps and Claude are composed around it and the
// item's rank carries over, same contract as the mcp update_todo.
type DoitEdit struct {
	Text        string `json:"text"`
	Description string `json:"description"`
	Priority    string `json:"priority"`
	Due         string `json:"due"`
	Type        string `json:"type"`
	Deps        string `json:"deps"`
	Claude      bool   `json:"claude"`
	Sequence    int    `json:"sequence"`
	Worktree    string `json:"worktree"`
	Feature     string `json:"feature"`
}

const doitOwedList = "daily"

func doitDataDir() string {
	if d := os.Getenv("DOIT_DATA_DIR"); d != "" {
		return d
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "share", "nvim", "doit")
}

func doitListsDir() string {
	return filepath.Join(doitDataDir(), "lists")
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
		stopDoitActive(doitTodos(d), t)
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
	if e.Sequence < 0 {
		return fmt.Errorf("sequence %d is negative", e.Sequence)
	}
	p, d, err := loadDoit(list)
	if err != nil {
		return err
	}
	t, _ := findDoitTodo(d, id)
	if t == nil {
		return fmt.Errorf("todo %s not in %s", id, list)
	}
	old, _ := t["text"].(string)
	t["text"] = composeDoitText(e, old)
	if old, _ := t["description"].(string); doitBody(old) != doitBody(e.Description) {
		t["description"] = doitStamp(e.Description)
	}
	prio := setOrDelete(t, "priorities", e.Priority)
	due := setOrDelete(t, "due_date", e.Due)
	setOrDelete(t, "worktree", strings.TrimSpace(e.Worktree))
	setOrDelete(t, "feature", strings.TrimSpace(e.Feature))
	setDoitSequence(doitTodos(d), t, e.Sequence)
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
	now := time.Now().Unix()
	id := fmt.Sprintf("%d_%d", now, rand.IntN(9999999))
	d["todos"] = append(todos, map[string]any{
		"id":          id,
		"text":        text,
		"done":        false,
		"in_progress": false,
		"order_index": maxDoitOrder(todos) + 1,
		"created_at":  now,
		"source":      "user",
		"description": doitStamp(""),
	})
	return id, saveDoit(p, d)
}

// mirrors mcp move_todo: the subtree stays behind promoted, the item takes the
// target's next rank, deps that pointed into the source list are dropped into a note
func (a *App) DoitMove(from, id, to string) error {
	if from == to {
		return errors.New("source and target list are the same")
	}
	sp, sd, err := loadDoit(from)
	if err != nil {
		return err
	}
	tp, td, err := loadDoit(to)
	if err != nil {
		return err
	}
	t, i := findDoitTodo(sd, id)
	if t == nil {
		return fmt.Errorf("todo %s not in %s", id, from)
	}
	todos := doitTodos(sd)
	promoteDoitChildren(todos, id)
	sd["todos"] = append(todos[:i:i], todos[i+1:]...)
	delete(t, "parent_id")
	t["depth"] = 0

	target := doitTodos(td)
	t["order_index"] = maxDoitOrder(target) + 1
	delete(t, "sequence")
	text, _ := t["text"].(string)
	var rankMap map[int]int
	if rank := parseDoitText(text).rank; rank > 0 {
		rankMap = map[int]int{rank: nextDoitRank(target)}
		renumbered, dropped := renumberDoitText(text, rankMap)
		t["text"] = renumbered
		if len(dropped) > 0 {
			desc, _ := t["description"].(string)
			note := fmt.Sprintf("moved from %s #%d; its deps there: %s", from, rank, strings.Join(dropped, ", "))
			if kept := doitBody(desc); kept != "" {
				note = kept + "\n\n" + note
			}
			t["description"] = doitStamp(note)
		}
	}
	if t["in_progress"] == true {
		stopDoitActive(target, t)
	}
	td["todos"] = append(target, t)

	if err := saveDoit(sp, sd); err != nil {
		return err
	}
	if err := saveDoit(tp, td); err != nil {
		return err
	}
	return syncDoitTwin(t, func(_ map[string]any, tw map[string]any) {
		ref, _ := tw["twin"].(map[string]any)
		list, _ := ref["list"].(string)
		relabelDoitTwin(tw, list, to, rankMap)
		if ref != nil {
			ref["list"] = to
		}
	})
}

func (a *App) DoitListCreate(name string) error {
	name = strings.TrimSpace(name)
	if err := validDoitListName(name); err != nil {
		return err
	}
	p, err := doitPath(name)
	if err != nil {
		return err
	}
	if _, err := os.Stat(p); err == nil {
		return fmt.Errorf("list %q already exists", name)
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	now := time.Now().Unix()
	return writeDoitJSON(p, map[string]any{
		"todos":     []any{},
		"_metadata": map[string]any{"created_at": now, "updated_at": now},
	})
}

// mirrors mcp rename_list: owed twins are relabeled on both sides, tmux window links and session.json follow
func (a *App) DoitListRename(old, name string) error {
	name = strings.TrimSpace(name)
	if err := validDoitListName(name); err != nil {
		return err
	}
	if old == doitOwedList {
		return fmt.Errorf("%q is permanent", old)
	}
	oldPath, err := doitPath(old)
	if err != nil {
		return err
	}
	newPath, err := doitPath(name)
	if err != nil {
		return err
	}
	if _, err := os.Stat(oldPath); err != nil {
		return fmt.Errorf("list %q not found", old)
	}
	if _, err := os.Stat(newPath); err == nil {
		return fmt.Errorf("list %q already exists", name)
	}
	if err := os.Rename(oldPath, newPath); err != nil {
		return err
	}
	_, d, err := loadDoit(name)
	if err != nil {
		return err
	}
	// a twin pair can sit inside this one list, and syncDoitTwin would look for it under the old name
	changed := false
	for _, v := range doitTodos(d) {
		t, _ := v.(map[string]any)
		if ref, _ := t["twin"].(map[string]any); ref != nil && ref["list"] == old {
			relabelDoitTwin(t, old, name, nil)
			ref["list"] = name
			changed = true
		}
	}
	if changed {
		if err := saveDoit(newPath, d); err != nil {
			return err
		}
	}
	for _, v := range doitTodos(d) {
		t, _ := v.(map[string]any)
		err := syncDoitTwin(t, func(_ map[string]any, tw map[string]any) {
			ref, _ := tw["twin"].(map[string]any)
			list, _ := ref["list"].(string)
			relabelDoitTwin(tw, list, name, nil)
			if ref != nil {
				ref["list"] = name
			}
		})
		if err != nil {
			return err
		}
	}
	if err := updateDoitSession(func(s map[string]any) {
		sessions, _ := s["sessions"].(map[string]any)
		for k, v := range sessions {
			if v == old {
				sessions[k] = name
			}
		}
		if s["active_list"] == old {
			s["active_list"] = name
		}
	}); err != nil {
		return err
	}
	retagDoitWindows(old, name)
	return nil
}

// mirrors mcp delete_list: the file moves to deleted/, each owed twin elsewhere stays with the deleted copy's notes
func (a *App) DoitListDelete(name string) error {
	if name == doitOwedList {
		return fmt.Errorf("%q is permanent", name)
	}
	p, d, err := loadDoit(name)
	if err != nil {
		return err
	}
	for _, v := range doitTodos(d) {
		t, _ := v.(map[string]any)
		if err := detachDoitTwin(t, name); err != nil {
			return err
		}
	}
	archived := filepath.Join(doitDataDir(), "deleted", fmt.Sprintf("%s-%d.json", name, time.Now().Unix()))
	if err := os.MkdirAll(filepath.Dir(archived), 0o755); err != nil {
		return err
	}
	if err := os.Rename(p, archived); err != nil {
		return err
	}
	if err := updateDoitSession(func(s map[string]any) {
		sessions, _ := s["sessions"].(map[string]any)
		for k, v := range sessions {
			if v == name {
				delete(sessions, k)
			}
		}
	}); err != nil {
		return err
	}
	retagDoitWindows(name, "")
	return nil
}

func (a *App) DoitNoteAdd(list, title, body string) (string, error) {
	if strings.TrimSpace(title) == "" && strings.TrimSpace(body) == "" {
		return "", errors.New("note is empty")
	}
	p, d, err := loadDoit(list)
	if err != nil {
		return "", err
	}
	now := time.Now().Unix()
	id := fmt.Sprintf("%d_%d", now, rand.IntN(9999999))
	notes, _ := d["notes"].([]any)
	d["notes"] = append(notes, map[string]any{
		"id":         id,
		"title":      strings.TrimSpace(title),
		"body":       body,
		"created_at": now,
		"updated_at": now,
	})
	return id, saveDoit(p, d)
}

func (a *App) DoitNoteUpdate(list, id, title, body string) error {
	if strings.TrimSpace(title) == "" && strings.TrimSpace(body) == "" {
		return errors.New("note is empty")
	}
	p, d, err := loadDoit(list)
	if err != nil {
		return err
	}
	n, _ := findDoitNote(d, id)
	if n == nil {
		return fmt.Errorf("note %s not in %s", id, list)
	}
	n["title"], n["body"], n["updated_at"] = strings.TrimSpace(title), body, time.Now().Unix()
	return saveDoit(p, d)
}

// mirrors mcp delete_note: last 10 kept in _metadata.deleted_notes
func (a *App) DoitNoteDelete(list, id string) error {
	p, d, err := loadDoit(list)
	if err != nil {
		return err
	}
	n, i := findDoitNote(d, id)
	if n == nil {
		return fmt.Errorf("note %s not in %s", id, list)
	}
	notes := d["notes"].([]any)
	d["notes"] = append(notes[:i:i], notes[i+1:]...)
	meta := doitMeta(d)
	del, _ := meta["deleted_notes"].([]any)
	del = append([]any{n}, del...)
	if len(del) > 10 {
		del = del[:10]
	}
	meta["deleted_notes"] = del
	return saveDoit(p, d)
}

func findDoitNote(d map[string]any, id string) (map[string]any, int) {
	notes, _ := d["notes"].([]any)
	for i, v := range notes {
		if n, ok := v.(map[string]any); ok && n["id"] == id {
			return n, i
		}
	}
	return nil, -1
}

// same charset and cap as the mcp listNameSchema and tmux sanitize_list_name: the name is the filename
var doitListNameRE = regexp.MustCompile(`^[\w.-]+$`)

func validDoitListName(name string) error {
	if name == "." || name == ".." || !doitListNameRE.MatchString(name) {
		return fmt.Errorf("list name %q: letters, digits, '.', '_' and '-' only", name)
	}
	if len(name) > 24 {
		return fmt.Errorf("list name %q is over 24 chars", name)
	}
	return nil
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
	d, err := decodeDoit(b)
	if err != nil {
		return "", nil, fmt.Errorf("%s: %w", list, err)
	}
	return p, d, nil
}

func decodeDoit(b []byte) (map[string]any, error) {
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	var d map[string]any
	if err := dec.Decode(&d); err != nil {
		return nil, err
	}
	return d, nil
}

func saveDoit(p string, d map[string]any) error {
	doitMeta(d)["updated_at"] = time.Now().Unix()
	return writeDoitJSON(p, d)
}

func writeDoitJSON(p string, d map[string]any) error {
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

// mirrors mcp updateSession: an unreadable session.json starts over, as the mcp's readSession does
func updateDoitSession(mutate func(s map[string]any)) error {
	p := filepath.Join(doitDataDir(), "session.json")
	s := map[string]any{}
	if b, err := os.ReadFile(p); err == nil {
		if d, err := decodeDoit(b); err == nil {
			s = d
		}
	}
	mutate(s)
	s["timestamp"] = time.Now().Unix()
	return writeDoitJSON(p, s)
}

// a var so tests never touch the user's live tmux server
var retagDoitWindows = func(from, to string) {
	tmux, err := exec.LookPath("tmux")
	if err != nil {
		// a Finder-launched app has no brew PATH
		for _, c := range []string{"/opt/homebrew/bin/tmux", "/usr/local/bin/tmux"} {
			if _, err := os.Stat(c); err == nil {
				tmux = c
				break
			}
		}
	}
	if tmux == "" {
		return
	}
	out, err := exec.Command(tmux, "list-windows", "-a", "-F", "#{window_id}\t#{@doit-window-list}").Output()
	if err != nil {
		return
	}
	for _, line := range strings.Split(string(out), "\n") {
		id, list, ok := strings.Cut(line, "\t")
		if !ok || id == "" || list != from {
			continue
		}
		if to != "" {
			exec.Command(tmux, "set-option", "-wq", "-t", id, "@doit-window-list", to).Run()
			continue
		}
		exec.Command(tmux, "set-option", "-wqu", "-t", id, "@doit-window-list").Run()
		exec.Command(tmux, "set-option", "-wqu", "-t", id, "@doit-window-key").Run()
	}
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

func doitNum(v any) (float64, bool) {
	switch n := v.(type) {
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	case float64:
		return n, true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	}
	return 0, false
}

func maxDoitOrder(todos []any) float64 {
	var max float64
	for _, v := range todos {
		if o, ok := v.(map[string]any); ok {
			if f, ok := doitNum(o["order_index"]); ok && f > max {
				max = f
			}
		}
	}
	return max
}

// mirrors mcp stopActive: one in-progress item per worktree slot, items with no worktree share one
func stopDoitActive(todos []any, todo map[string]any) {
	slot, _ := todo["worktree"].(string)
	for _, v := range todos {
		o, ok := v.(map[string]any)
		if !ok || o["id"] == todo["id"] || o["in_progress"] != true {
			continue
		}
		if w, _ := o["worktree"].(string); w == slot {
			o["in_progress"] = false
		}
	}
}

// mirrors mcp sequence.js: only the colliding run (pos, pos+1, ...) shifts down
func doitSequenced(t map[string]any) (int, bool) {
	if t["done"] == true || t["in_progress"] == true {
		return 0, false
	}
	f, ok := doitNum(t["sequence"])
	return int(f), ok
}

func setDoitSequence(todos []any, target map[string]any, pos int) {
	delete(target, "sequence")
	if pos < 1 {
		return
	}
	taken := map[int]bool{}
	for _, v := range todos {
		if o, ok := v.(map[string]any); ok && o["id"] != target["id"] {
			if s, ok := doitSequenced(o); ok {
				taken[s] = true
			}
		}
	}
	free := pos
	for taken[free] {
		free++
	}
	for _, v := range todos {
		o, ok := v.(map[string]any)
		if !ok || o["id"] == target["id"] {
			continue
		}
		if s, ok := doitSequenced(o); ok && s >= pos && s < free {
			o["sequence"] = s + 1
		}
	}
	target["sequence"] = pos
}

func promoteDoitChildren(todos []any, parentID string) {
	seen := map[any]bool{parentID: true}
	for _, v := range todos {
		if c, ok := v.(map[string]any); ok && c["parent_id"] == parentID {
			delete(c, "parent_id")
			c["depth"] = 0
			redepthDoit(todos, c["id"], 1, seen)
		}
	}
}

// mirrors mcp removeTodo: direct children go top level, last 10 deletions kept for undo
func removeDoitTodo(d map[string]any, id string) map[string]any {
	t, i := findDoitTodo(d, id)
	if t == nil {
		return nil
	}
	todos := doitTodos(d)
	promoteDoitChildren(todos, id)
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

// mirrors mcp detachTwin: the surviving twin keeps the deleted copy's notes and loses the link
func detachDoitTwin(t map[string]any, deleted string) error {
	ref, _ := t["twin"].(map[string]any)
	list, _ := ref["list"].(string)
	id, _ := ref["id"].(string)
	if list == "" || list == deleted {
		return nil
	}
	desc, _ := t["description"].(string)
	notes := strings.TrimSpace(doitBody(desc))
	notes = strings.TrimSpace(strings.TrimPrefix(notes, list+": "+id))
	return syncDoitTwin(t, func(_ map[string]any, tw map[string]any) {
		twDesc, _ := tw["description"].(string)
		body := doitBody(twDesc)
		var parts []string
		if body != "" {
			parts = append(parts, body)
		}
		if notes != "" && !strings.Contains(body, notes) {
			parts = append(parts, fmt.Sprintf("notes from %q:\n%s", deleted, notes))
		}
		parts = append(parts, fmt.Sprintf("list %q deleted %s; twin link dropped", deleted, time.Now().Format("2006-01-02")))
		tw["description"] = doitStamp(strings.Join(parts, "\n\n"))
		delete(tw, "twin")
	})
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

// todo text shape, same as mcp todo-text.js: "claude: [type] N. body (dep on #M, #K)"
var (
	doitShapeRE       = regexp.MustCompile(`(?is)^(claude:\s*)?(?:\[([^\][]+)\]\s*)?(?:(\d+)([a-z]*)\.\s+)?(.*)$`)
	doitDepsRE        = regexp.MustCompile(`(?i)\s*\(dep on [^)]*\)\s*$`)
	doitDepRefRE      = regexp.MustCompile(`(?i)#(\d+)([a-z]*)`)
	doitLabelPrefixRE = regexp.MustCompile(`(?i)^(?:claude:\s*)?(?:\[[^\][]+\]\s*)?(?:\d+[a-z]*\.\s+)?`)
	doitLeadingNumRE  = regexp.MustCompile(`^\d+`)
	doitDepSplitRE    = regexp.MustCompile(`[,\s]+`)
)

type doitText struct {
	claude bool
	typ    string
	rank   int
	label  string
	body   string
}

func parseDoitText(s string) doitText {
	m := doitShapeRE.FindStringSubmatch(s)
	p := doitText{claude: m[1] != "", typ: m[2], body: m[5]}
	if m[3] != "" {
		p.rank, _ = strconv.Atoi(m[3])
		p.label = m[3] + m[4]
	}
	return p
}

func joinDoitText(claude bool, typ, label, body string) string {
	var parts []string
	if claude {
		parts = append(parts, "claude:")
	}
	if typ != "" {
		parts = append(parts, "["+typ+"]")
	}
	if label != "" {
		parts = append(parts, label+".")
	}
	if body != "" {
		parts = append(parts, body)
	}
	return strings.Join(parts, " ")
}

// mirrors mcp composeTodoText: a prefix typed into the body wins, the rank always carries over
func composeDoitText(e DoitEdit, inherit string) string {
	parsed := parseDoitText(e.Text)
	prior := parseDoitText(inherit)
	typ := strings.TrimSpace(e.Type)
	if parsed.typ != "" {
		typ = parsed.typ
	}
	label := parsed.label
	if label == "" {
		label = prior.label
	}
	body := strings.TrimSpace(doitDepsRE.ReplaceAllString(strings.TrimSpace(parsed.body), ""))
	var refs []string
	for _, r := range doitDepSplitRE.Split(strings.TrimSpace(e.Deps), -1) {
		if r = strings.TrimPrefix(r, "#"); r != "" {
			refs = append(refs, "#"+r)
		}
	}
	if len(refs) > 0 {
		body += " (dep on " + strings.Join(refs, ", ") + ")"
	}
	return joinDoitText(e.Claude || parsed.claude, typ, label, body)
}

// mirrors mcp renumberText: deps the map lacks pointed into the old list and are dropped
func renumberDoitText(text string, rankMap map[int]int) (string, []string) {
	m := doitShapeRE.FindStringSubmatch(text)
	body := m[5]
	var dropped []string
	if loc := doitDepsRE.FindStringIndex(body); loc != nil {
		var kept []string
		for _, r := range doitDepRefRE.FindAllStringSubmatch(body[loc[0]:], -1) {
			n, _ := strconv.Atoi(r[1])
			if nr, ok := rankMap[n]; ok {
				kept = append(kept, fmt.Sprintf("#%d%s", nr, r[2]))
			} else {
				dropped = append(dropped, r[0])
			}
		}
		body = strings.TrimRight(body[:loc[0]], " \t")
		if len(kept) > 0 {
			body += " (dep on " + strings.Join(kept, ", ") + ")"
		}
	}
	label := ""
	if m[3] != "" {
		n, _ := strconv.Atoi(m[3])
		if nr, ok := rankMap[n]; ok {
			label = strconv.Itoa(nr) + m[4]
		} else {
			label = m[3] + m[4]
		}
	}
	return joinDoitText(m[1] != "", m[2], label, body), dropped
}

func nextDoitRank(todos []any) int {
	max := 0
	for _, v := range todos {
		if o, ok := v.(map[string]any); ok {
			text, _ := o["text"].(string)
			if r := parseDoitText(text).rank; r > max {
				max = r
			}
		}
	}
	return max + 1
}

// mirrors mcp relabelTwin: an owed twin names its project list ("[gate] 4. proj: body", "from: proj #4")
func relabelDoitTwin(t map[string]any, old, name string, rankMap map[int]int) {
	if old == "" || (old == name && rankMap == nil) {
		return
	}
	text, _ := t["text"].(string)
	prefix := doitLabelPrefixRE.FindString(text)
	rest := text[len(prefix):]
	if strings.HasPrefix(rest, old+": ") {
		t["text"] = prefix + name + ": " + rest[len(old)+2:]
	}
	from := "from: " + old + " #"
	if desc, _ := t["description"].(string); strings.HasPrefix(desc, from) {
		tail := desc[len(from):]
		if n := doitLeadingNumRE.FindString(tail); n != "" && rankMap != nil {
			if nr, ok := rankMap[atoi(n)]; ok {
				tail = strconv.Itoa(nr) + tail[len(n):]
			}
		}
		t["description"] = "from: " + name + " #" + tail
	}
}

func atoi(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}

var doitFooterRE = regexp.MustCompile(`\n*----------\nlast (updated|modified):[\s\S]*$`)

func doitBody(desc string) string {
	return strings.TrimRight(doitFooterRE.ReplaceAllString(desc, ""), " \t\r\n")
}

// same footer the mcp, tmux and nvim surfaces write, so a re-save refreshes it instead of stacking
func doitStamp(desc string) string {
	return doitBody(desc) + "\n\n\n----------\nlast modified: " + time.Now().Format("2006-01-02: 15:04")
}

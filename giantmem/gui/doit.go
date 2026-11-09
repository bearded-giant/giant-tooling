package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
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
	Name      string     `json:"name"`
	Todos     []DoitTodo `json:"todos"`
	Notes     []DoitNote `json:"notes"`
	UpdatedAt int64      `json:"updated_at"`
	Error     string     `json:"error,omitempty"`
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

// DoitSync is doitd's sync/state.json: what the badge shows.
type DoitSync struct {
	Mode      string `json:"mode"`
	Online    bool   `json:"online"`
	Queue     int    `json:"queue"`
	Conflicts int    `json:"conflicts"`
	Error     string `json:"error"`
}

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

// runtime state (socket, sync) lives outside the data dir so a file sync of the lists never carries it
func doitStateDir() string {
	if d := os.Getenv("DOIT_STATE_DIR"); d != "" {
		return d
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "state", "doit")
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
		// file mtime, not _metadata.updated_at: every surface bumps the file, not all stamp the metadata
		if st, err := os.Stat(p); err == nil {
			l.UpdatedAt = st.ModTime().Unix()
		}
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

func (a *App) DoitSyncState() DoitSync {
	s := DoitSync{Mode: "local"}
	b, err := os.ReadFile(filepath.Join(doitStateDir(), "sync", "state.json"))
	if err != nil {
		return s
	}
	json.Unmarshal(b, &s)
	return s
}

type doitCallResult struct {
	OK    bool   `json:"ok"`
	Text  string `json:"text"`
	Error string `json:"error"`
}

// doitCall runs one tool through doitd, the single writer of the lists; this GUI never writes one
// itself. With the daemon down the doit CLI applies the op under the daemon lock.
var doitCall = func(tool string, args map[string]any, activeList string) (string, error) {
	sock := filepath.Join(doitStateDir(), "doitd.sock")
	body, err := json.Marshal(map[string]any{
		"tool": tool, "args": args, "source": "user", "active_list": activeList, "data_dir": doitDataDir(),
	})
	if err != nil {
		return "", err
	}
	client := &http.Client{
		Timeout: 5 * time.Second,
		Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", sock)
		}},
	}
	resp, err := client.Post("http://doitd/call", "application/json", bytes.NewReader(body))
	if err != nil {
		return doitCallCLI(tool, args, activeList, err)
	}
	defer resp.Body.Close()
	var r doitCallResult
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return "", fmt.Errorf("doitd answered badly: %w", err)
	}
	if !r.OK {
		return "", errors.New(r.Error)
	}
	return r.Text, nil
}

func doitCallCLI(tool string, args map[string]any, activeList string, cause error) (string, error) {
	cli, err := exec.LookPath("doit")
	if err != nil {
		// a Finder-launched app has no ~/.local/bin on PATH
		home, _ := os.UserHomeDir()
		cli = filepath.Join(home, ".local", "bin", "doit")
		if _, err := os.Stat(cli); err != nil {
			return "", fmt.Errorf("doitd not reachable (%v) and no doit CLI to fall back to", cause)
		}
	}
	argsJSON, _ := json.Marshal(args)
	cmd := exec.Command(cli, "call", tool, string(argsJSON), "-l", activeList)
	cmd.Env = append(os.Environ(), "DOIT_DATA_DIR="+doitDataDir())
	out, err := cmd.Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) && len(ee.Stderr) > 0 {
			return "", errors.New(strings.TrimSpace(string(ee.Stderr)))
		}
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

var doitIDRE = regexp.MustCompile(`\[id:([^\]\s]+)\]`)

func doitResultID(text string) (string, error) {
	m := doitIDRE.FindStringSubmatch(text)
	if m == nil {
		return "", fmt.Errorf("no id in tool result: %q", text)
	}
	return m[1], nil
}

func (a *App) DoitSetStatus(list, id, status string) error {
	tool := map[string]string{"in_progress": "start_todo", "done": "complete_todo", "pending": "revert_todo"}[status]
	if tool == "" {
		return fmt.Errorf("unknown status %q", status)
	}
	_, err := doitCall(tool, map[string]any{"id": id, "list": list}, list)
	return err
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
	deps := []string{}
	for _, d := range strings.Split(e.Deps, ",") {
		if d = strings.TrimPrefix(strings.TrimSpace(d), "#"); d != "" {
			deps = append(deps, d)
		}
	}
	priority := e.Priority
	if priority == "" {
		priority = "none"
	}
	args := map[string]any{
		"id": id, "list": list, "text": e.Text, "description": e.Description, "priority": priority, "due": e.Due,
		"deps": deps, "claude": e.Claude, "sequence": e.Sequence,
		"worktree": strings.TrimSpace(e.Worktree), "feature": strings.TrimSpace(e.Feature),
	}
	if e.Type != "" {
		args["type"] = e.Type
	}
	_, err := doitCall("update_todo", args, list)
	return err
}

func (a *App) DoitDelete(list, id string) error {
	_, err := doitCall("delete_todo", map[string]any{"id": id, "list": list}, list)
	return err
}

func (a *App) DoitAdd(list, text string) (string, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return "", errors.New("todo text is empty")
	}
	out, err := doitCall("add_todo", map[string]any{"list": list, "text": text}, list)
	if err != nil {
		return "", err
	}
	return doitResultID(out)
}

func (a *App) DoitMove(from, id, to string) error {
	if from == to {
		return errors.New("source and target list are the same")
	}
	_, err := doitCall("move_todo", map[string]any{"id": id, "from_list": from, "to_list": to}, from)
	return err
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

func (a *App) DoitListCreate(name string) error {
	name = strings.TrimSpace(name)
	if err := validDoitListName(name); err != nil {
		return err
	}
	_, err := doitCall("create_list", map[string]any{"name": name, "source": "user"}, name)
	return err
}

// the tool renames the file and relabels owed twins; window links and session.json are this process's to move
func (a *App) DoitListRename(old, name string) error {
	name = strings.TrimSpace(name)
	if err := validDoitListName(name); err != nil {
		return err
	}
	if _, err := doitCall("rename_list", map[string]any{"old_name": old, "new_name": name}, old); err != nil {
		return err
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

func (a *App) DoitListDelete(name string) error {
	if _, err := doitCall("delete_list", map[string]any{"name": name}, name); err != nil {
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
	out, err := doitCall("create_note", map[string]any{"list": list, "title": strings.TrimSpace(title), "body": body}, list)
	if err != nil {
		return "", err
	}
	return doitResultID(out)
}

func (a *App) DoitNoteUpdate(list, id, title, body string) error {
	if strings.TrimSpace(title) == "" && strings.TrimSpace(body) == "" {
		return errors.New("note is empty")
	}
	_, err := doitCall("update_note", map[string]any{"list": list, "id": id, "title": strings.TrimSpace(title), "body": body}, list)
	return err
}

func (a *App) DoitNoteDelete(list, id string) error {
	_, err := doitCall("delete_note", map[string]any{"list": list, "id": id}, list)
	return err
}

// mirrors mcp updateSession: an unreadable session.json starts over, as the mcp's readSession does
func updateDoitSession(mutate func(s map[string]any)) error {
	p := filepath.Join(doitDataDir(), "session.json")
	s := map[string]any{}
	if b, err := os.ReadFile(p); err == nil {
		dec := json.NewDecoder(bytes.NewReader(b))
		dec.UseNumber()
		var d map[string]any
		if err := dec.Decode(&d); err == nil {
			s = d
		}
	}
	mutate(s)
	s["timestamp"] = time.Now().Unix()
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(s); err != nil {
		return err
	}
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

package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/bearded-giant/giant-tooling/giantmem/internal/artifacts"
	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"
	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
)

var (
	errBack = errors.New("back")
	errQuit = errors.New("quit")

	styleOK   = lipgloss.NewStyle().Foreground(lipgloss.Color("2")).Bold(true)
	styleErr  = lipgloss.NewStyle().Foreground(lipgloss.Color("1")).Bold(true)
	styleDim  = lipgloss.NewStyle().Faint(true)
	styleQ    = lipgloss.NewStyle().Foreground(lipgloss.Color("6")).Bold(true)
	styleBold = lipgloss.NewStyle().Bold(true)
	styleAns  = lipgloss.NewStyle().Foreground(lipgloss.Color("6"))

	themeCLI = func() *huh.Theme {
		t := huh.ThemeBase16()
		for _, s := range []*huh.FieldStyles{&t.Focused, &t.Blurred} {
			s.Base = lipgloss.NewStyle().PaddingLeft(2)
			s.Card = s.Base
		}
		return t
	}()
)

const (
	hintSelect  = "(type to filter, enter to pick, esc back)"
	hintInput   = "(enter to submit, esc back)"
	hintConfirm = "(y/n, esc back)"
)

type ui struct {
	ctx    context.Context
	c      *client.Client
	keymap *huh.KeyMap
	ctrlC  bool
}

type choice struct {
	label string
	run   func() error
}

func isInteractive() bool {
	for _, f := range []*os.File{os.Stdin, os.Stdout} {
		st, err := f.Stat()
		if err != nil || st.Mode()&os.ModeCharDevice == 0 {
			return false
		}
	}
	return true
}

func runInteractive(ctx context.Context) error {
	c, err := client.NewInProcessClient(newMCPServer())
	if err != nil {
		return err
	}
	defer c.Close()
	if err := c.Start(ctx); err != nil {
		return err
	}
	req := mcp.InitializeRequest{}
	req.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	req.Params.ClientInfo = mcp.Implementation{Name: "giantmem-cli", Version: Version}
	if _, err := c.Initialize(ctx, req); err != nil {
		return err
	}
	km := huh.NewDefaultKeyMap()
	km.Quit = key.NewBinding(key.WithKeys("ctrl+c", "esc"))
	u := &ui{ctx: ctx, c: c, keymap: km}
	fmt.Println(styleDim.Render("giantmem " + Version))
	if err := u.home(); err != nil && !errors.Is(err, errBack) && !errors.Is(err, errQuit) {
		return err
	}
	return nil
}

func (u *ui) home() error {
	return u.menu("What do you want to do?", []choice{
		{"Find artifacts", u.artifacts},
		{"Search everything", u.search},
		{"Sessions", u.sessions},
		{"Tool calls", func() error { return u.toolCalls("") }},
		{"MRs and PRs", u.mrs},
		{"Features", u.features},
		{"Recent writes", u.recent},
		{"Reindex artifacts", u.reindex},
		{"Query a database", u.query},
		{"Quit", func() error { return errQuit }},
	})
}

// esc and ctrl+c both abort a huh form (and both emit tea.InterruptMsg), so only the key itself tells them apart
func (u *ui) run(fields ...huh.Field) error {
	u.ctrlC = false
	f := huh.NewForm(huh.NewGroup(fields...)).
		WithTheme(themeCLI).
		WithShowHelp(false).
		WithKeyMap(u.keymap).
		WithProgramOptions(tea.WithOutput(os.Stderr), tea.WithReportFocus(), tea.WithFilter(func(_ tea.Model, msg tea.Msg) tea.Msg {
			if k, ok := msg.(tea.KeyMsg); ok && k.Type == tea.KeyCtrlC {
				u.ctrlC = true
			}
			return msg
		}))
	err := f.RunWithContext(u.ctx)
	if errors.Is(err, huh.ErrUserAborted) {
		if u.ctrlC {
			return errQuit
		}
		return errBack
	}
	return err
}

// huh hides a select's title while filtering, so the question prints above the form and becomes a "? q › answer" breadcrumb
func (u *ui) prompt(title, hint string, field huh.Field, answer func() string) error {
	fmt.Fprintf(os.Stderr, "%s %s %s\n", styleQ.Render("?"), styleBold.Render(title), styleDim.Render(hint))
	err := u.run(field)
	fmt.Fprint(os.Stderr, "\033[1A\033[2K")
	if err == nil {
		fmt.Fprintf(os.Stderr, "%s %s %s %s\n", styleQ.Render("?"), styleBold.Render(title), styleDim.Render("›"), styleAns.Render(clip(answer(), 80)))
	}
	return err
}

func (u *ui) choose(title string, labels []string) (int, error) {
	opts := make([]huh.Option[int], len(labels))
	for i, l := range labels {
		opts[i] = huh.NewOption(clip(l, 110), i)
	}
	var i int
	err := u.prompt(title, hintSelect, huh.NewSelect[int]().Options(opts...).Value(&i).Filtering(true), func() string { return labels[i] })
	return i, err
}

func (u *ui) menu(title string, choices []choice) error {
	labels := make([]string, len(choices))
	for i, c := range choices {
		labels[i] = c.label
	}
	for {
		i, err := u.choose(title, labels)
		if err != nil {
			return err
		}
		if err := choices[i].run(); errors.Is(err, errQuit) {
			return err
		} else if err != nil && !errors.Is(err, errBack) {
			fail(err)
		}
	}
}

func (u *ui) pick(title string, labels []string, open func(int) error) error {
	if len(labels) == 0 {
		fmt.Println(styleDim.Render("Nothing found."))
		return nil
	}
	choices := make([]choice, len(labels))
	for i, l := range labels {
		choices[i] = choice{l, func() error { return open(i) }}
	}
	return u.menu(title, choices)
}

func (u *ui) ask(title, placeholder string) (string, error) {
	var s string
	err := u.prompt(title, hintInput, huh.NewInput().Placeholder(placeholder).Value(&s), func() string { return s })
	return strings.TrimSpace(s), err
}

func (u *ui) confirm(title string, def bool) (bool, error) {
	v := def
	err := u.prompt(title, hintConfirm, huh.NewConfirm().Value(&v), func() string {
		if v {
			return "yes"
		}
		return "no"
	})
	return v, err
}

func (u *ui) call(name string, args map[string]any) (string, error) {
	fmt.Fprint(os.Stderr, styleDim.Render("Working..."))
	req := mcp.CallToolRequest{}
	req.Params.Name = name
	req.Params.Arguments = args
	res, err := u.c.CallTool(u.ctx, req)
	fmt.Fprint(os.Stderr, "\r\033[K")
	if err != nil {
		return "", err
	}
	var b strings.Builder
	for _, c := range res.Content {
		if t, ok := mcp.AsTextContent(c); ok {
			b.WriteString(t.Text)
		}
	}
	if res.IsError {
		return "", errors.New(b.String())
	}
	return b.String(), nil
}

func (u *ui) callJSON(name string, args map[string]any, v any) error {
	s, err := u.call(name, args)
	if err != nil {
		return err
	}
	return json.Unmarshal([]byte(s), v)
}

func success(msg string) { fmt.Println(styleOK.Render("Success!") + " " + msg) }
func fail(err error)     { fmt.Println(styleErr.Render("Error!") + " " + err.Error()) }

func pagerCmd(args ...string) *exec.Cmd {
	p := os.Getenv("PAGER")
	if p == "" {
		p = "less -FRX"
	}
	c := exec.Command("sh", append([]string{"-c", p + ` "$@"`, "pager"}, args...)...)
	c.Stdout, c.Stderr = os.Stdout, os.Stderr
	return c
}

func page(text string) {
	c := pagerCmd()
	c.Stdin = strings.NewReader(text)
	if c.Run() != nil {
		fmt.Print(text)
	}
}

func pageFile(path string) {
	c := pagerCmd(path)
	c.Stdin = os.Stdin
	if err := c.Run(); err != nil {
		fail(err)
	}
}

func clip(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > n {
		return string(r[:n-3]) + "..."
	}
	return s
}

// session timestamps come as 20261103_081333
func fmtStamp(ts string) string {
	if t, err := time.Parse("20060102_150405", ts); err == nil {
		return t.Format("2006-01-02 15:04")
	}
	if t, err := time.Parse(time.RFC3339, ts); err == nil {
		return t.Local().Format("2006-01-02 15:04")
	}
	return ts
}

func renderTable(cols []string, rows []map[string]any) string {
	var b strings.Builder
	w := tabwriter.NewWriter(&b, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, strings.Join(cols, "\t"))
	for _, r := range rows {
		cells := make([]string, len(cols))
		for i, c := range cols {
			if r[c] == nil {
				cells[i] = "NULL"
			} else {
				cells[i] = clip(fmt.Sprint(r[c]), 80)
			}
		}
		fmt.Fprintln(w, strings.Join(cells, "\t"))
	}
	w.Flush()
	return b.String()
}

func (u *ui) artifacts() error {
	q, err := u.ask("Find artifacts", "words in the body, or enter for all")
	if err != nil {
		return err
	}
	args := map[string]any{"limit": 200}
	if q != "" {
		args["query"] = q
	}
	if _, ok := artifacts.FindWorkspace("."); ok {
		args["repo"] = "current"
	}
	var r struct {
		Results []struct{ ID, Type, Status, Updated string }
	}
	if err := u.callJSON("find_artifact", args, &r); err != nil {
		return err
	}
	labels := make([]string, len(r.Results))
	for i, a := range r.Results {
		labels[i] = fmt.Sprintf("%-11s %-11s %-10s %s", a.Type, a.Status, a.Updated, a.ID)
	}
	return u.pick(fmt.Sprintf("%d artifacts", len(labels)), labels, func(i int) error {
		var a struct{ Content string }
		if err := u.callJSON("get_artifact", map[string]any{"id": r.Results[i].ID}, &a); err != nil {
			return err
		}
		page(a.Content)
		return nil
	})
}

func (u *ui) search() error {
	q, err := u.ask("Search everything", "FTS5 query over workspaces, sessions, and domain docs")
	if err != nil || q == "" {
		return err
	}
	var r struct {
		Results []struct {
			Filepath, Project, Timestamp, Snippet string
			SourceType                            string `json:"source_type"`
			SessionID                             string `json:"session_id"`
		}
	}
	if err := u.callJSON("search_archive", map[string]any{"query": q, "limit": 50}, &r); err != nil {
		return err
	}
	labels := make([]string, len(r.Results))
	for i, h := range r.Results {
		labels[i] = fmt.Sprintf("%-9s %-16s %-24s %s", h.SourceType, fmtStamp(h.Timestamp), clip(h.Project, 24), filepath.Base(h.Filepath))
	}
	return u.pick(fmt.Sprintf("%d hits for %q", len(labels), q), labels, func(i int) error {
		h := r.Results[i]
		page(fmt.Sprintf("%s\n%s\n\n%s\n", h.Filepath, h.SessionID, h.Snippet))
		return nil
	})
}

func (u *ui) sessions() error {
	var r struct {
		Sessions []struct {
			Project, Topic, Timestamp, Cwd string
			SessionID                      string `json:"session_id"`
		}
	}
	if err := u.callJSON("list_sessions", map[string]any{"limit": 60}, &r); err != nil {
		return err
	}
	labels := make([]string, len(r.Sessions))
	for i, s := range r.Sessions {
		labels[i] = fmt.Sprintf("%s  %-30s %-10s %s", fmtStamp(s.Timestamp), clip(s.Project, 30), s.Topic, s.SessionID[:8])
	}
	return u.pick("Recent sessions", labels, func(i int) error {
		s := r.Sessions[i]
		return u.menu(s.SessionID[:8]+" "+s.Project, []choice{
			{"Summary and last report", func() error { return u.sessionSummary(s.SessionID) }},
			{"Files in its bundle", func() error { return u.bundle(s.SessionID) }},
			{"Tool calls", func() error { return u.toolCalls(s.SessionID) }},
		})
	})
}

func (u *ui) sessionSummary(id string) error {
	var s struct {
		Project, Cwd, Topic, Timestamp string
		SessionID                      string `json:"session_id"`
		LastReport                     string `json:"last_report"`
	}
	if err := u.callJSON("get_session_summary", map[string]any{"id_prefix": id}, &s); err != nil {
		return err
	}
	page(fmt.Sprintf("session  %s\nproject  %s\ntopic    %s\nwhen     %s\nresume   cd %s && claude --resume %s\n\n%s\n",
		s.SessionID, s.Project, s.Topic, fmtStamp(s.Timestamp), s.Cwd, s.SessionID, s.LastReport))
	return nil
}

func (u *ui) bundle(id string) error {
	var r struct {
		Files []struct {
			Kind, Path string
			Size       int64
		}
	}
	if err := u.callJSON("get_session_bundle", map[string]any{"id_prefix": id}, &r); err != nil {
		return err
	}
	labels := make([]string, len(r.Files))
	for i, f := range r.Files {
		labels[i] = fmt.Sprintf("%-10s %8d  %s", f.Kind, f.Size, filepath.Base(f.Path))
	}
	return u.pick("Bundle files", labels, func(i int) error {
		pageFile(r.Files[i].Path)
		return nil
	})
}

func (u *ui) toolCalls(sessionID string) error {
	args := map[string]any{"limit": 100}
	if sessionID != "" {
		args["session_id"] = sessionID
		args["limit"] = 500
	} else {
		q, err := u.ask("Search tool calls", "e.g. glab mr view, jq splunk")
		if err != nil || q == "" {
			return err
		}
		args["query"] = q
	}
	var r struct {
		ToolUses []struct {
			Project, Tool, Summary, Link, Ts string
			SessionID                        string `json:"session_id"`
		} `json:"tool_uses"`
	}
	if err := u.callJSON("find_tool_uses", args, &r); err != nil {
		return err
	}
	labels := make([]string, len(r.ToolUses))
	for i, t := range r.ToolUses {
		labels[i] = fmt.Sprintf("%-16s %-8s %s", fmtStamp(t.Ts), clip(t.Tool, 8), t.Summary)
	}
	return u.pick(fmt.Sprintf("%d tool calls", len(labels)), labels, func(i int) error {
		t := r.ToolUses[i]
		page(fmt.Sprintf("%s  %s  %s\n%s\n\n%s\n", t.Tool, t.Project, fmtStamp(t.Ts), t.Link, t.Summary))
		return nil
	})
}

type mrSession struct {
	SessionID string `json:"session_id"`
	Role      string `json:"role"`
	Resume    string `json:"resume"`
}

func (u *ui) mrs() error {
	var r struct {
		MRs []struct {
			URL, Label string
			LastTs     string `json:"last_ts"`
		} `json:"mrs"`
	}
	if err := u.callJSON("find_mr", map[string]any{"live": false}, &r); err != nil {
		return err
	}
	labels := make([]string, len(r.MRs))
	for i, m := range r.MRs {
		labels[i] = fmt.Sprintf("%s  %s", fmtStamp(m.LastTs), m.Label)
	}
	return u.pick("MRs and PRs sessions opened or touched", labels, func(i int) error {
		var d struct {
			MRs []struct {
				URL, Label string
				Status     struct {
					State, Title string
					Branch       string `json:"source_branch"`
				}
				Opener   mrSession
				Sessions []mrSession
			} `json:"mrs"`
		}
		if err := u.callJSON("find_mr", map[string]any{"ref": r.MRs[i].URL}, &d); err != nil {
			return err
		}
		if len(d.MRs) == 0 {
			return fmt.Errorf("no MR for %s", r.MRs[i].URL)
		}
		m := d.MRs[0]
		var b strings.Builder
		fmt.Fprintf(&b, "%s  %s\n%s\nbranch  %s\nstate   %s\n\n", m.Label, m.Status.Title, m.URL, m.Status.Branch, m.Status.State)
		for _, s := range append([]mrSession{m.Opener}, m.Sessions...) {
			if s.SessionID != "" {
				fmt.Fprintf(&b, "%-8s %s\n         %s\n", s.Role, s.SessionID, s.Resume)
			}
		}
		page(b.String())
		return nil
	})
}

func (u *ui) features() error {
	ws, ok := artifacts.FindWorkspace(".")
	if !ok {
		return errors.New("no .giantmem/ here or above")
	}
	root := filepath.Dir(ws)
	for {
		feats := readFeaturesJSON(filepath.Join(ws, "features", "features.json"))
		labels := []string{"+ New feature"}
		for _, f := range feats {
			labels = append(labels, fmt.Sprintf("%-28v %-12v %v", f["name"], f["status"], f["branch"]))
		}
		i, err := u.choose("Features in "+filepath.Base(root), labels)
		if err != nil {
			return err
		}
		if i == 0 {
			err = u.newFeature(root)
		} else {
			err = u.featureAction(root, fmt.Sprint(feats[i-1]["name"]))
		}
		if errors.Is(err, errQuit) {
			return err
		} else if err != nil && !errors.Is(err, errBack) {
			fail(err)
		}
	}
}

func (u *ui) newFeature(root string) error {
	name, err := u.ask("Feature name", "kebab-case, becomes the branch name too")
	if err != nil || name == "" {
		return err
	}
	checkout, err := u.confirm("Check out branch "+name+"?", true)
	if err != nil {
		return err
	}
	out, err := u.call("feature", map[string]any{"action": "new", "name": name, "cwd": root, "skip_checkout": !checkout})
	if err != nil {
		return err
	}
	success(out)
	return nil
}

func (u *ui) featureAction(root, name string) error {
	actions := []string{"next", "start", "pause", "complete", "reopen", "abandon", "archive"}
	labels := []string{"Next task", "Start", "Pause", "Complete", "Reopen", "Abandon", "Archive"}
	i, err := u.choose(name, labels)
	if err != nil {
		return err
	}
	if actions[i] == "abandon" || actions[i] == "archive" {
		yes, err := u.confirm(fmt.Sprintf("%s %s? Its dir is removed once live.db has every file.", labels[i], name), false)
		if err != nil || !yes {
			return err
		}
	}
	out, err := u.call("feature", map[string]any{"action": actions[i], "name": name, "cwd": root})
	if err != nil {
		return err
	}
	if actions[i] == "next" {
		page(out)
		return nil
	}
	success(out)
	return nil
}

func (u *ui) recent() error {
	var r struct {
		Writes []struct {
			Path, Project string
			Mtime         int64
		}
	}
	if err := u.callJSON("recent_writes", map[string]any{"since": "48h", "limit": 100}, &r); err != nil {
		return err
	}
	labels := make([]string, len(r.Writes))
	for i, w := range r.Writes {
		labels[i] = fmt.Sprintf("%s  %-28s %s", time.Unix(w.Mtime, 0).Format("01-02 15:04"), clip(w.Project, 28), filepath.Base(w.Path))
	}
	return u.pick("Writes in the last 48h", labels, func(i int) error {
		pageFile(r.Writes[i].Path)
		return nil
	})
}

func (u *ui) reindex() error {
	out, err := u.call("reindex_artifacts", map[string]any{})
	if err != nil {
		return err
	}
	success(out)
	return nil
}

func (u *ui) query() error {
	i, err := u.choose("Which database?", []string{"live.db (workspace docs, artifacts)", "archives.db (sessions, tool calls, MRs)"})
	if err != nil {
		return err
	}
	db := []string{"live", "archives"}[i]
	for {
		q, err := u.ask("SQL on "+db+".db", "read-only; e.g. select type, count(*) from artifacts group by 1")
		if err != nil || q == "" {
			return err
		}
		var r struct {
			Columns   []string
			Rows      []map[string]any
			Truncated bool
		}
		if err := u.callJSON("query_db", map[string]any{"db": db, "sql": q, "limit": 500}, &r); err != nil {
			fail(err)
			continue
		}
		out := renderTable(r.Columns, r.Rows)
		if r.Truncated {
			out += "(first 500 rows)\n"
		}
		page(out)
	}
}

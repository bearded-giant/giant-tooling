package sessions

import (
	"bufio"
	"bytes"
	"cmp"
	"encoding/json"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
)

// ToolCall is one tool_use joined with its matching tool_result.
type ToolCall struct {
	Seq        int
	ID         string
	Tool       string
	TS         string
	Input      string
	Summary    string
	IsError    bool
	ResultHead string
}

// Link is something a session produced that outlives it.
type Link struct {
	Kind   string // artifact | vault | handoff | doit | mr | mr_ref
	Target string
	Label  string
	TS     string
	Role   string // mr only: opened | touched
	Cwd    string // mr only: session cwd when linked
}

var (
	artifactURLRe = regexp.MustCompile(`https://claude\.ai/[\w./-]*artifact/[\w-]+`)
	mrURLRe       = regexp.MustCompile(`https://gitlab\.[\w.-]+/[\w./-]+/-/merge_requests/\d+|https://github\.com/[\w.-]+/[\w.-]+/pull/\d+`)
	mrCreateRe    = regexp.MustCompile(`\b(?:glab\s+mr|gh\s+pr)\s+create\b`)
	doitIDRe      = regexp.MustCompile(`\[id:([^\]\s]+)\]`)
)

type contentBlock struct {
	Type      string          `json:"type"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
	ToolUseID string          `json:"tool_use_id"`
	IsError   bool            `json:"is_error"`
	Content   json.RawMessage `json:"content"`
}

type pendingCall struct {
	idx   int
	input map[string]any
}

type linkSet struct {
	root   string
	seen   map[string]bool
	links  []Link
	cwd    string
	opened map[string]bool
}

// ExtractToolCalls streams a transcript and returns its tool calls in file order plus the links they produced.
func ExtractToolCalls(jsonlPath, vaultRoot string) ([]ToolCall, []Link, error) {
	f, err := os.Open(jsonlPath)
	if err != nil {
		return nil, nil, err
	}
	defer f.Close()

	ls := &linkSet{seen: map[string]bool{}, opened: map[string]bool{}}
	if vaultRoot != "" {
		if ls.root, err = filepath.Abs(vaultRoot); err != nil {
			return nil, nil, err
		}
	}
	var calls []ToolCall
	pending := map[string]pendingCall{}

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1024*1024), 16*1024*1024)
	for sc.Scan() {
		var rec struct {
			Type      string `json:"type"`
			Timestamp string `json:"timestamp"`
			Cwd       string `json:"cwd"`
			PrURL     string `json:"prUrl"`
			Message   struct {
				Content json.RawMessage `json:"content"`
			} `json:"message"`
		}
		if json.Unmarshal(sc.Bytes(), &rec) != nil {
			continue
		}
		if rec.Cwd != "" {
			ls.cwd = rec.Cwd
		}
		// the harness writes pr-link when a session creates, updates, or pushes to an MR/PR
		if rec.Type == "pr-link" {
			ls.addMR(rec.PrURL, rec.Timestamp)
			continue
		}
		var blocks []json.RawMessage
		if json.Unmarshal(rec.Message.Content, &blocks) != nil {
			continue
		}
		ts := rec.Timestamp
		for _, raw := range blocks {
			var b contentBlock
			if json.Unmarshal(raw, &b) != nil {
				continue
			}
			switch {
			case b.Type == "tool_use" && rec.Type == "assistant":
				in := map[string]any{}
				_ = json.Unmarshal(b.Input, &in)
				c := ToolCall{Seq: len(calls), ID: b.ID, Tool: b.Name, TS: ts, Input: compactJSON(b.Input)}
				c.Summary = summarize(b.Name, in, c.Input)
				pending[b.ID] = pendingCall{idx: len(calls), input: in}
				calls = append(calls, c)
				ls.fromInput(b.Name, in, ts)
			case b.Type == "tool_result" && rec.Type == "user":
				var v any
				_ = json.Unmarshal(b.Content, &v)
				text := combineText(v, false)
				if p, ok := pending[b.ToolUseID]; ok {
					delete(pending, b.ToolUseID)
					calls[p.idx].IsError = b.IsError
					calls[p.idx].ResultHead = clip(text, 500)
					ls.fromResult(calls[p.idx].Tool, p.input, text, ts, b.IsError)
				}
				for _, u := range mrURLRe.FindAllString(text, -1) {
					ls.add("mr_ref", u, mrLabel(u), ts)
				}
			}
		}
	}
	return calls, ls.finish(), sc.Err()
}

func (s *linkSet) add(kind, target, label, ts string) bool {
	key := kind + "\x00" + target
	if s.seen[key] {
		return false
	}
	s.seen[key] = true
	s.links = append(s.links, Link{Kind: kind, Target: target, Label: label, TS: ts})
	return true
}

func (s *linkSet) addMR(u, ts string) {
	if u != "" && s.add("mr", u, mrLabel(u), ts) {
		s.links[len(s.links)-1].Cwd = s.cwd
	}
}

// finish sets mr roles once the whole transcript is read and drops mentions of MRs the session wrote to.
func (s *linkSet) finish() []Link {
	out := s.links[:0]
	for _, l := range s.links {
		switch l.Kind {
		case "mr":
			l.Role = "touched"
			if s.opened[l.Target] {
				l.Role = "opened"
			}
		case "mr_ref":
			if s.seen["mr\x00"+l.Target] {
				continue
			}
		}
		out = append(out, l)
	}
	return out
}

func (s *linkSet) fromInput(tool string, in map[string]any, ts string) {
	fp := str(in, "file_path")
	if tool == "Write" || tool == "Edit" || tool == "MultiEdit" {
		if p, ok := s.vaultNote(fp); ok {
			s.add("vault", p, noteStem(p), ts)
		}
	}
	if (tool == "Write" || tool == "Edit") && strings.Contains(fp, "/.giantmem/") && strings.HasSuffix(fp, "handoff.md") {
		s.add("handoff", filepath.Clean(fp), "handoff", ts)
	}
}

func (s *linkSet) fromResult(tool string, in map[string]any, text, ts string, isErr bool) {
	switch tool {
	case "Artifact":
		if a := str(in, "action"); (a != "" && a != "publish") || in["asset"] == true {
			return
		}
		if u := artifactURLRe.FindString(text); u != "" {
			label := u
			if fp := str(in, "file_path"); fp != "" {
				label = filepath.Base(fp)
			}
			s.add("artifact", u, label, ts)
		}
	case "Bash":
		cmd := str(in, "command")
		if !isErr && mrCreateRe.MatchString(cmd) {
			// create prints the new URL first; later URLs in a chained command are other MRs
			if u := mrURLRe.FindString(text); u != "" {
				s.opened[u] = true
				s.addMR(u, ts)
			}
		}
		if !strings.Contains(cmd, "note.py") || !strings.Contains(cmd, " new") {
			return
		}
		for _, line := range strings.Split(text, "\n") {
			if p, ok := s.vaultNote(strings.TrimSpace(line)); ok {
				s.add("vault", p, noteStem(p), ts)
			}
		}
	case "mcp__doit__add_todo":
		if m := doitIDRe.FindStringSubmatch(text); m != nil {
			first, _, _ := strings.Cut(text, "\n")
			s.add("doit", m[1], clip(strings.TrimSpace(first), 120), ts)
		}
	}
}

func (s *linkSet) vaultNote(p string) (string, bool) {
	if s.root == "" || !filepath.IsAbs(p) || !strings.HasSuffix(p, ".md") {
		return "", false
	}
	p = filepath.Clean(p)
	sep := string(filepath.Separator)
	for _, skip := range []string{"daily", "templates"} {
		if strings.HasPrefix(p, filepath.Join(s.root, skip)+sep) {
			return "", false
		}
	}
	return p, strings.HasPrefix(p, s.root+sep)
}

func summarize(tool string, in map[string]any, inputJSON string) string {
	var s string
	switch tool {
	case "Bash":
		s = str(in, "command")
	case "Read", "Write", "Edit", "MultiEdit", "NotebookEdit":
		s = cmp.Or(str(in, "file_path"), str(in, "notebook_path"))
	case "Grep":
		s = str(in, "pattern")
		if p := str(in, "path"); p != "" {
			s += " in " + p
		}
	case "Glob":
		s = str(in, "pattern")
	case "WebFetch":
		s = str(in, "url")
	case "WebSearch":
		s = str(in, "query")
	case "Agent", "Task":
		s = str(in, "description")
	case "Skill":
		s = str(in, "skill")
		if a := str(in, "args"); a != "" {
			s += " " + a
		}
	case "Artifact":
		s = cmp.Or(str(in, "action"), "publish") + " " + cmp.Or(str(in, "file_path"), str(in, "url"))
	}
	if s == "" {
		s = inputJSON
	}
	return clip(strings.Join(strings.Fields(s), " "), 300)
}

func compactJSON(raw json.RawMessage) string {
	var buf bytes.Buffer
	if json.Compact(&buf, raw) != nil {
		return "{}"
	}
	return buf.String()
}

// mrLabel names the repo because iids collide across repos.
func mrLabel(u string) string {
	i := strings.LastIndexByte(u, '/')
	if i < 0 {
		return u
	}
	repo, n := u[:i], u[i+1:]
	if strings.HasPrefix(u, "https://github.com/") {
		return path.Base(strings.TrimSuffix(repo, "/pull")) + "#" + n
	}
	return path.Base(strings.TrimSuffix(repo, "/-/merge_requests")) + "!" + n
}

func noteStem(p string) string {
	return strings.TrimSuffix(filepath.Base(p), ".md")
}

func str(m map[string]any, k string) string {
	s, _ := m[k].(string)
	return s
}

// clip walks runes instead of converting to []rune because tool results can run to megabytes.
func clip(s string, n int) string {
	i := 0
	for j := range s {
		if i == n {
			return s[:j]
		}
		i++
	}
	return s
}

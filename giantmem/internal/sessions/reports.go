package sessions

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

type Report struct {
	Timestamp string `json:"timestamp"`
	Prompt    string `json:"prompt"`
	Text      string `json:"text"`
}

type reportRow struct {
	Type             string `json:"type"`
	Timestamp        string `json:"timestamp"`
	IsMeta           bool   `json:"isMeta"`
	IsSidechain      bool   `json:"isSidechain"`
	IsCompactSummary bool   `json:"isCompactSummary"`
	Message          struct {
		Content any `json:"content"`
	} `json:"message"`
}

// stop-hook feedback rows are isMeta: they keep a blocked report and its rewrite in one turn, and drop the blocked text.
func Reports(jsonlPath string) ([]Report, error) {
	f, err := os.Open(jsonlPath)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var out []Report
	var prompt, lastTS, last string
	var trail []string
	flush := func() {
		text := strings.Join(trail, "\n\n")
		if text == "" {
			text = last
		}
		if strings.TrimSpace(text) != "" {
			out = append(out, Report{Timestamp: lastTS, Prompt: prompt, Text: strings.TrimSpace(text)})
		}
		prompt, lastTS, last, trail = "", "", "", nil
	}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1024*1024), 16*1024*1024)
	for sc.Scan() {
		var r reportRow
		if json.Unmarshal(sc.Bytes(), &r) != nil || r.IsSidechain {
			continue
		}
		switch r.Type {
		case "user":
			if r.IsMeta || r.IsCompactSummary {
				trail = nil
				continue
			}
			p, ok := promptCaption(combineText(r.Message.Content, true))
			if !ok {
				continue
			}
			flush()
			prompt = p
		case "assistant":
			text, tools := assistantContent(r.Message.Content, map[string]bool{})
			if strings.TrimSpace(text) != "" {
				trail = append(trail, text)
				last, lastTS = text, r.Timestamp
			}
			if len(tools) > 0 {
				trail = nil
			}
		}
	}
	flush()
	return out, sc.Err()
}

func FirstPrompt(jsonlPath string) string {
	f, err := os.Open(jsonlPath)
	if err != nil {
		return ""
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1024*1024), 16*1024*1024)
	for sc.Scan() {
		var r reportRow
		if json.Unmarshal(sc.Bytes(), &r) != nil || r.Type != "user" || r.IsMeta || r.IsSidechain {
			continue
		}
		// same rule as the bundle hook: hook output and slash-command wrappers arrive as user turns
		if s := strings.TrimSpace(combineText(r.Message.Content, true)); s != "" && !strings.HasPrefix(s, "<") {
			return s
		}
	}
	return ""
}

var leadingTags = regexp.MustCompile(`<[^>]+>`)

func promptCaption(s string) (string, bool) {
	s = strings.TrimSpace(s)
	switch {
	case s == "",
		strings.HasPrefix(s, "<local-command-"),
		strings.HasPrefix(s, "<bash-"),
		strings.HasPrefix(s, "[Request interrupted"):
		return "", false
	case strings.Contains(s, "<command-name>"):
		return caption(tagBody(s, "command-name")+" "+tagBody(s, "command-args"), 120), true
	case strings.HasPrefix(s, "<task-notification>"):
		return caption("task notification: "+tagBody(s, "summary"), 120), true
	case strings.HasPrefix(s, "<"):
		s = leadingTags.ReplaceAllString(s, " ")
	}
	return caption(s, 120), true
}

func tagBody(s, tag string) string {
	_, rest, ok := strings.Cut(s, "<"+tag+">")
	if !ok {
		return ""
	}
	body, _, _ := strings.Cut(rest, "</"+tag+">")
	return strings.TrimSpace(body)
}

func caption(s string, max int) string {
	r := []rune(strings.Join(strings.Fields(s), " "))
	if len(r) > max {
		return string(r[:max-1]) + "…"
	}
	return string(r)
}

// a /clear stub records no parent id, but claude code writes the parent transcript at the /clear, so its mtime marks it.
func ClearedFrom(jsonlPath string) string {
	f, err := os.Open(jsonlPath)
	if err != nil {
		return ""
	}
	defer f.Close()
	var start time.Time
	cleared := false
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1024*1024), 16*1024*1024)
	for sc.Scan() && !(cleared && !start.IsZero()) {
		var r reportRow
		if json.Unmarshal(sc.Bytes(), &r) != nil {
			continue
		}
		if start.IsZero() {
			start, _ = time.Parse(time.RFC3339, r.Timestamp)
		}
		if r.Type == "user" && tagBody(combineText(r.Message.Content, false), "command-name") == "/clear" {
			cleared = true
		}
	}
	if !cleared || start.IsZero() {
		return ""
	}
	self := filepath.Base(jsonlPath)
	matches, _ := filepath.Glob(filepath.Join(filepath.Dir(jsonlPath), "*.jsonl"))
	best, bestGap := "", 10*time.Second
	for _, m := range matches {
		name := filepath.Base(m)
		if name == self || strings.HasPrefix(name, "agent-") {
			continue
		}
		st, err := os.Stat(m)
		if err != nil {
			continue
		}
		gap := st.ModTime().Sub(start).Abs()
		if gap <= bestGap {
			best, bestGap = strings.TrimSuffix(name, ".jsonl"), gap
		}
	}
	return best
}

package sessions

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Turn is one user/assistant turn in a transcript.
type Turn struct {
	Role      string
	Text      string
	ToolUses  []ToolUse
	ToolNames []string
}

// ToolUse is a single tool invocation extracted from an assistant turn.
type ToolUse struct {
	Name  string
	Input map[string]any
}

// Transcript is the parsed transcript of a session.
type Transcript struct {
	SessionID    string
	Cwd          string
	StartedAt    time.Time
	EndedAt      time.Time
	Turns        []Turn
	FilesTouched []string
	BashCount    int
	UserMsgs     int
	AssistantMsgs int
}

// Parse streams a JSONL file and returns a Transcript suitable for export.
func Parse(jsonlPath string) (*Transcript, error) {
	f, err := os.Open(jsonlPath)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	t := &Transcript{
		SessionID: strings.TrimSuffix(filepath.Base(jsonlPath), ".jsonl"),
	}
	files := map[string]bool{}

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1024*1024), 16*1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var msg map[string]any
		if err := json.Unmarshal([]byte(line), &msg); err != nil {
			continue
		}
		if t.Cwd == "" {
			if v, ok := msg["cwd"].(string); ok {
				t.Cwd = v
			}
		}
		if ts, _ := msg["timestamp"].(string); ts != "" {
			if parsed, err := time.Parse(time.RFC3339, ts); err == nil {
				if t.StartedAt.IsZero() {
					t.StartedAt = parsed
				}
				if parsed.After(t.EndedAt) {
					t.EndedAt = parsed
				}
			}
		}
		mtype, _ := msg["type"].(string)
		message, _ := msg["message"].(map[string]any)
		content := message["content"]

		switch mtype {
		case "human", "user":
			text := combineText(content, true)
			if strings.TrimSpace(text) == "" {
				continue
			}
			t.UserMsgs++
			t.Turns = append(t.Turns, Turn{Role: "user", Text: text})
		case "assistant":
			text, tools := assistantContent(content, files)
			if strings.TrimSpace(text) == "" && len(tools) == 0 {
				continue
			}
			t.AssistantMsgs++
			turn := Turn{Role: "assistant", Text: text, ToolUses: tools}
			for _, tu := range tools {
				turn.ToolNames = append(turn.ToolNames, tu.Name)
				if tu.Name == "Bash" {
					t.BashCount++
				}
			}
			t.Turns = append(t.Turns, turn)
		}
	}
	for f := range files {
		t.FilesTouched = append(t.FilesTouched, f)
	}
	return t, sc.Err()
}

func combineText(content any, skipReminders bool) string {
	switch c := content.(type) {
	case string:
		return c
	case []any:
		var parts []string
		for _, item := range c {
			switch b := item.(type) {
			case string:
				parts = append(parts, b)
			case map[string]any:
				if t, _ := b["type"].(string); t == "text" {
					txt, _ := b["text"].(string)
					if skipReminders && strings.Contains(txt, "<system-reminder>") {
						continue
					}
					parts = append(parts, txt)
				}
			}
		}
		return strings.Join(parts, "\n")
	}
	return ""
}

func assistantContent(content any, filesTouched map[string]bool) (string, []ToolUse) {
	arr, ok := content.([]any)
	if !ok {
		if s, ok := content.(string); ok {
			return s, nil
		}
		return "", nil
	}
	var parts []string
	var tools []ToolUse
	for _, item := range arr {
		b, ok := item.(map[string]any)
		if !ok {
			continue
		}
		switch b["type"] {
		case "text":
			if t, _ := b["text"].(string); t != "" {
				parts = append(parts, t)
			}
		case "tool_use":
			name, _ := b["name"].(string)
			input, _ := b["input"].(map[string]any)
			if input == nil {
				input = map[string]any{}
			}
			tools = append(tools, ToolUse{Name: name, Input: input})
			switch name {
			case "Read", "Write", "Edit":
				if fp, _ := input["file_path"].(string); fp != "" {
					filesTouched[fp] = true
				}
			}
		}
	}
	return strings.Join(parts, "\n"), tools
}

// Markdown writes a clean markdown transcript to w.
func Markdown(w io.Writer, t *Transcript, withTools bool) {
	fmt.Fprintf(w, "# session %s\n\n", t.SessionID)
	if t.Cwd != "" {
		fmt.Fprintf(w, "- cwd: `%s`\n", t.Cwd)
	}
	if !t.StartedAt.IsZero() {
		fmt.Fprintf(w, "- started: %s\n", t.StartedAt.Format(time.RFC3339))
	}
	if !t.EndedAt.IsZero() {
		fmt.Fprintf(w, "- ended: %s\n", t.EndedAt.Format(time.RFC3339))
		dur := t.EndedAt.Sub(t.StartedAt)
		if dur > 0 {
			fmt.Fprintf(w, "- duration: %s\n", dur.Round(time.Second))
		}
	}
	fmt.Fprintf(w, "- turns: %d user, %d assistant\n", t.UserMsgs, t.AssistantMsgs)
	if t.BashCount > 0 {
		fmt.Fprintf(w, "- bash invocations: %d\n", t.BashCount)
	}
	if len(t.FilesTouched) > 0 {
		fmt.Fprintf(w, "- files touched: %d\n", len(t.FilesTouched))
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w, "---")
	fmt.Fprintln(w)

	for _, turn := range t.Turns {
		role := "## user"
		if turn.Role == "assistant" {
			role = "## assistant"
		}
		fmt.Fprintln(w, role)
		fmt.Fprintln(w)
		if turn.Text != "" {
			fmt.Fprintln(w, strings.TrimSpace(turn.Text))
			fmt.Fprintln(w)
		}
		if withTools && len(turn.ToolUses) > 0 {
			fmt.Fprintln(w, "<details><summary>tools: "+strings.Join(turn.ToolNames, ", ")+"</summary>")
			fmt.Fprintln(w)
			for _, tu := range turn.ToolUses {
				fmt.Fprintf(w, "- **%s**", tu.Name)
				if tu.Name == "Bash" {
					if cmd, _ := tu.Input["command"].(string); cmd != "" {
						fmt.Fprintf(w, ": `%s`", oneLine(cmd, 120))
					}
				} else if fp, _ := tu.Input["file_path"].(string); fp != "" {
					fmt.Fprintf(w, ": `%s`", fp)
				}
				fmt.Fprintln(w)
			}
			fmt.Fprintln(w)
			fmt.Fprintln(w, "</details>")
			fmt.Fprintln(w)
		}
	}
}

func oneLine(s string, max int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "  ", " ")
	if len(s) > max {
		s = s[:max] + "…"
	}
	return s
}

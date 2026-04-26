package ingest

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

var (
	systemReminderRe   = regexp.MustCompile(`<system-reminder>`)
	claudeProjectRe    = regexp.MustCompile(`^-Users-[^-]+-`)
)

// SessionExtract is the result of parsing a single JSONL session file.
type SessionExtract struct {
	Text      string
	SessionID string
	Cwd       string
}

// ExtractSessionText streams a JSONL conversation and returns indexable text.
// nil result means the file produced no content (skip).
func ExtractSessionText(jsonlPath string) (*SessionExtract, error) {
	f, err := os.Open(jsonlPath)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	sessionID := strings.TrimSuffix(filepath.Base(jsonlPath), ".jsonl")
	var (
		texts        []string
		filesTouched = map[string]bool{}
		bashCmds     []string
		cwd          string
	)

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1024*1024), 16*1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		// fast pre-filter
		if strings.Contains(line, `"tool_result"`) && !strings.Contains(line, `"tool_use"`) {
			continue
		}
		var msg map[string]any
		if err := json.Unmarshal([]byte(line), &msg); err != nil {
			continue
		}
		if cwd == "" {
			if v, ok := msg["cwd"].(string); ok {
				cwd = v
			}
		}
		msgType, _ := msg["type"].(string)
		message, _ := msg["message"].(map[string]any)
		content := message["content"]

		switch msgType {
		case "human", "user":
			extractTextBlocks(content, &texts, true)
		case "assistant":
			extractAssistantBlocks(content, &texts, filesTouched, &bashCmds)
		}
	}
	if len(texts) == 0 {
		return nil, nil
	}

	parts := []string{fmt.Sprintf("session: %s", sessionID)}
	if cwd != "" {
		parts = append(parts, fmt.Sprintf("cwd: %s", cwd))
	}
	parts = append(parts, "")
	parts = append(parts, texts...)

	if len(filesTouched) > 0 {
		var sorted []string
		for k := range filesTouched {
			sorted = append(sorted, k)
		}
		sort.Strings(sorted)
		parts = append(parts, "\n--- files touched ---")
		parts = append(parts, sorted...)
	}
	if len(bashCmds) > 0 {
		parts = append(parts, "\n--- commands ---")
		limit := len(bashCmds)
		if limit > 20 {
			limit = 20
		}
		parts = append(parts, bashCmds[:limit]...)
	}

	return &SessionExtract{
		Text:      strings.Join(parts, "\n"),
		SessionID: sessionID,
		Cwd:       cwd,
	}, nil
}

func extractTextBlocks(content any, texts *[]string, skipReminders bool) {
	switch c := content.(type) {
	case string:
		*texts = append(*texts, c)
	case []any:
		for _, item := range c {
			switch b := item.(type) {
			case string:
				*texts = append(*texts, b)
			case map[string]any:
				if t, _ := b["type"].(string); t == "text" {
					txt, _ := b["text"].(string)
					if skipReminders && systemReminderRe.MatchString(txt) {
						continue
					}
					*texts = append(*texts, txt)
				}
			}
		}
	}
}

func extractAssistantBlocks(content any, texts *[]string, filesTouched map[string]bool, bashCmds *[]string) {
	arr, ok := content.([]any)
	if !ok {
		if s, ok := content.(string); ok {
			*texts = append(*texts, s)
		}
		return
	}
	for _, item := range arr {
		b, ok := item.(map[string]any)
		if !ok {
			continue
		}
		switch b["type"] {
		case "text":
			if t, _ := b["text"].(string); t != "" {
				*texts = append(*texts, t)
			}
		case "tool_use":
			name, _ := b["name"].(string)
			input, _ := b["input"].(map[string]any)
			switch name {
			case "Read", "Write", "Edit":
				if fp, _ := input["file_path"].(string); fp != "" {
					filesTouched[fp] = true
				}
			case "Bash":
				if cmd, _ := input["command"].(string); cmd != "" {
					if len(cmd) > 150 {
						cmd = cmd[:150]
					}
					*bashCmds = append(*bashCmds, cmd)
				}
			}
		}
	}
}

// SessionProjectName derives a human-readable project name from a JSONL path.
// e.g. ~/.claude/projects/-Users-bryan-dev-foo/x.jsonl -> dev/foo
func SessionProjectName(jsonlPath, projectsDir string) string {
	rel, err := filepath.Rel(projectsDir, jsonlPath)
	if err != nil {
		return ""
	}
	parts := strings.Split(rel, string(filepath.Separator))
	if len(parts) == 0 {
		return ""
	}
	dir := claudeProjectRe.ReplaceAllString(parts[0], "")
	// only first two dashes become slashes (matches Python's replace count=2)
	return replaceFirstN(dir, "-", "/", 2)
}

func replaceFirstN(s, old, new string, n int) string {
	for i := 0; i < n; i++ {
		idx := strings.Index(s, old)
		if idx < 0 {
			return s
		}
		s = s[:idx] + new + s[idx+len(old):]
	}
	return s
}

// FileMtime returns the file's mtime. Exported for source plugins.
func FileMtime(path string) (time.Time, error) {
	return fileMtime(path)
}

// fileMtime returns the file's mtime as a unix timestamp.
func fileMtime(path string) (time.Time, error) {
	st, err := os.Stat(path)
	if err != nil {
		return time.Time{}, err
	}
	return st.ModTime(), nil
}

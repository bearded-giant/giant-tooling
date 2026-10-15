package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/bearded-giant/giant-tooling/giantmem/internal/hub"
	"github.com/bearded-giant/giant-tooling/giantmem/internal/search"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// SessionHubData is everything the session detail tabs need beyond the transcript.
type SessionHubData struct {
	SessionID   string      `json:"sessionId"`
	Bundle      *hub.Bundle `json:"bundle,omitempty"`
	BundleError string      `json:"bundleError,omitempty"`
	Links       []hub.Link  `json:"links"`
	Agents      []hub.Agent `json:"agents"`
	Calls       int         `json:"calls"`
	Errors      int         `json:"errors"`
}

func (a *App) SessionHub(sessionID string) (SessionHubData, error) {
	out := SessionHubData{SessionID: sessionID}
	if a.archive == nil || sessionID == "" {
		return out, nil
	}
	if err := a.archive.QueryRow(
		`SELECT COUNT(*), COALESCE(SUM(is_error),0) FROM tool_uses WHERE session_id = ?`, sessionID,
	).Scan(&out.Calls, &out.Errors); err != nil {
		return out, err
	}
	var err error
	if out.Links, err = hub.QueryLinks(a.archive, sessionID); err != nil {
		return out, err
	}
	if out.Agents, err = hub.Agents(a.archive, archiveBase(), sessionID); err != nil {
		return out, err
	}
	if b, err := hub.LoadBundle(archiveBase(), sessionID); err == nil {
		out.Bundle = b
	} else {
		out.BundleError = err.Error()
	}
	return out, nil
}

// SessionToolCalls returns one session's calls with input and result head.
// agent: "" all, "main" main thread, else a subagent id.
func (a *App) SessionToolCalls(sessionID, agent string) ([]hub.ToolUse, error) {
	if a.archive == nil {
		return nil, nil
	}
	return hub.QueryToolUses(a.archive, hub.ToolQuery{SessionID: sessionID, Agent: agent, WithIO: true})
}

// DaySessions lists sessions with tool activity on a local calendar day (YYYY-MM-DD).
func (a *App) DaySessions(day string) ([]hub.DaySession, error) {
	if a.archive == nil {
		return nil, nil
	}
	start, err := time.ParseInLocation("2006-01-02", day, time.Local)
	if err != nil {
		return nil, fmt.Errorf("bad day %q: %w", day, err)
	}
	return hub.Day(a.archive, archiveBase(),
		start.UTC().Format(time.RFC3339), start.AddDate(0, 0, 1).UTC().Format(time.RFC3339))
}

// ResolveTranscript swaps a pruned transcript path for its bundle snapshot.
func (a *App) ResolveTranscript(path string) string {
	return hub.TranscriptPath(archiveBase(), path, "")
}

// TranscriptForSession resolves a session id (deep links carry only the id).
func (a *App) TranscriptForSession(sessionID string) (string, error) {
	p, err := a.SessionPathByID(sessionID)
	if err != nil {
		if snap := hub.TranscriptPath(archiveBase(), "", sessionID); snap != "" {
			return snap, nil
		}
		return "", err
	}
	return hub.TranscriptPath(archiveBase(), p, sessionID), nil
}

type FileHead struct {
	Content   string `json:"content"`
	Size      int64  `json:"size"`
	Truncated bool   `json:"truncated"`
	Binary    bool   `json:"binary"`
}

// ReadFileHead reads at most maxBytes so an 18MB data file can't stall the viewer.
func (a *App) ReadFileHead(path string, maxBytes int) (FileHead, error) {
	var out FileHead
	if maxBytes <= 0 {
		maxBytes = 512 * 1024
	}
	f, err := os.Open(path)
	if err != nil {
		return out, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return out, err
	}
	out.Size = st.Size()
	buf, err := io.ReadAll(io.LimitReader(f, int64(maxBytes)))
	if err != nil {
		return out, err
	}
	sniff := buf
	if len(sniff) > 8192 {
		sniff = sniff[:8192]
	}
	if bytes.IndexByte(sniff, 0) >= 0 {
		out.Binary = true
		return out, nil
	}
	out.Content = string(buf)
	out.Truncated = st.Size() > int64(len(buf))
	return out, nil
}

// OpenPath opens a file with its default app, or reveals it in Finder.
func (a *App) OpenPath(path string, reveal bool) error {
	if path == "" {
		return fmt.Errorf("empty path")
	}
	args := []string{path}
	if reveal {
		args = []string{"-R", path}
	}
	return exec.Command("open", args...).Run()
}

// pendingSession holds a giantmem://session/<id> link that arrived before the
// frontend was listening (cold launch from a click in the daily note).
var pendingSession string

func (a *App) onURLOpen(url string) {
	id := strings.TrimPrefix(url, "giantmem://session/")
	id = strings.Trim(id, "/")
	if id == "" || id == url {
		return
	}
	if a.ctx == nil {
		pendingSession = id
		return
	}
	pendingSession = id
	runtime.EventsEmit(a.ctx, "open-session", id)
	runtime.WindowShow(a.ctx)
}

// TakePendingSession hands the frontend a deep link that arrived before it mounted.
func (a *App) TakePendingSession() string {
	id := pendingSession
	pendingSession = ""
	return id
}

// indexedToolUses answers the tools tab from the tool_uses index (subagents and
// pruned transcripts included). ok=false means the index is empty: scan instead.
func (a *App) indexedToolUses(f ToolUseFilter) ([]ToolUseHit, bool, error) {
	var n int
	if a.archive.QueryRow(`SELECT COUNT(*) FROM (SELECT 1 FROM tool_uses LIMIT 1)`).Scan(&n) != nil || n == 0 {
		return nil, false, nil
	}
	q := hub.ToolQuery{Match: f.Query, Tool: f.ToolName, Project: f.Project, Errors: f.ErrorsOnly, Limit: f.Limit, WithIO: true}
	if f.Since != "" {
		t, err := search.ParseSince(f.Since)
		if err != nil {
			return nil, true, err
		}
		q.Since = t.UTC().Format(time.RFC3339)
	}
	if f.Until != "" {
		t, err := search.ParseUntil(f.Until)
		if err != nil {
			return nil, true, err
		}
		q.Until = t.UTC().Format(time.RFC3339)
	}
	rows, err := hub.QueryToolUses(a.archive, q)
	if err != nil {
		return nil, true, err
	}
	paths := map[string]string{}
	out := make([]ToolUseHit, 0, len(rows))
	for _, r := range rows {
		p, seen := paths[r.SessionID]
		if !seen {
			p, _ = a.TranscriptForSession(r.SessionID)
			paths[r.SessionID] = p
		}
		out = append(out, ToolUseHit{
			SessionPath:  p,
			SessionID:    r.SessionID,
			AgentID:      r.AgentID,
			Project:      r.Project,
			Timestamp:    r.TS,
			TurnIndex:    r.Seq,
			ToolName:     r.Tool,
			InputSummary: r.Summary,
			InputJSON:    r.Input,
			Output:       r.ResultHead,
			OutputClip:   clipText(r.ResultHead, 200),
			IsError:      r.IsError,
		})
	}
	return out, true, nil
}

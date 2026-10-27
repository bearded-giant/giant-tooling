package main

import (
	"bytes"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/bearded-giant/giant-tooling/giantmem/internal/hub"
	"github.com/bearded-giant/giant-tooling/giantmem/internal/search"
	"github.com/bearded-giant/giant-tooling/giantmem/internal/sessions"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// SessionHubData is everything the session detail tabs need beyond the transcript.
type SessionHubData struct {
	SessionID   string      `json:"sessionId"`
	Link        string      `json:"link"`
	Bundle      *hub.Bundle `json:"bundle,omitempty"`
	BundleError string      `json:"bundleError,omitempty"`
	Links       []hub.Link  `json:"links"`
	Agents      []hub.Agent `json:"agents"`
	Calls       int         `json:"calls"`
	Errors      int         `json:"errors"`
}

func (a *App) SessionHub(sessionID string) (SessionHubData, error) {
	out := SessionHubData{SessionID: sessionID, Link: hub.SessionLink(sessionID)}
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

type SessionReportsData struct {
	Reports     []sessions.Report `json:"reports"`
	ClearedFrom string            `json:"clearedFrom,omitempty"`
}

func (a *App) SessionReports(path string) (SessionReportsData, error) {
	path = hub.TranscriptPath(archiveBase(), path, "")
	rs, err := sessions.Reports(path)
	if err != nil {
		return SessionReportsData{}, err
	}
	out := SessionReportsData{Reports: rs}
	if len(rs) == 0 {
		out.Reports = []sessions.Report{}
		out.ClearedFrom = sessions.ClearedFrom(path)
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

// ToolMix ranks the last `days` days of tool calls, MCP tools folded per server.
func (a *App) ToolMix(days int) ([]hub.ToolCount, error) {
	if a.archive == nil {
		return nil, nil
	}
	since := time.Now().AddDate(0, 0, -days).UTC().Format(time.RFC3339)
	return hub.ToolCounts(a.archive, hub.ToolQuery{Since: since}, true)
}

// SessionLengthStats rolls up the last `days` days of session length per project.
func (a *App) SessionLengthStats(days int) ([]hub.LengthSummary, error) {
	if a.archive == nil {
		return nil, nil
	}
	since := time.Now().AddDate(0, 0, -days).UTC().Format(time.RFC3339)
	ls, err := hub.SessionLengths(a.archive, hub.ToolQuery{Since: since}, hub.DefaultIdleCap)
	if err != nil {
		return nil, err
	}
	return hub.SummarizeLengths(ls), nil
}

func (a *App) KeepBundleFile(sessionID, rel string) (string, error) {
	return hub.Keep(archiveBase(), sessionID, rel)
}

func (a *App) SetToolStar(sessionID, agentID string, seq int, on bool) error {
	if a.archive == nil {
		return fmt.Errorf("archives.db not open")
	}
	return hub.SetStar(a.archive, sessionID, agentID, seq, on)
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

type DeepLink struct {
	Kind      string `json:"kind"`
	SessionID string `json:"sessionId,omitempty"`
	Day       string `json:"day,omitempty"`
	Agent     string `json:"agent,omitempty"`
	Seq       int    `json:"seq"`
	File      string `json:"file,omitempty"`
}

func parseLink(raw string) (DeepLink, bool) {
	rest, ok := strings.CutPrefix(raw, "giantmem://")
	if !ok {
		return DeepLink{}, false
	}
	kind, arg, _ := strings.Cut(strings.Trim(rest, "/"), "/")
	switch kind {
	case "session":
		if arg != "" && !strings.Contains(arg, "/") {
			return DeepLink{Kind: kind, SessionID: arg}, true
		}
	case "day":
		if _, err := time.Parse("2006-01-02", arg); err == nil {
			return DeepLink{Kind: kind, Day: arg}, true
		}
	case "tool":
		p := strings.Split(arg, "/")
		if len(p) != 3 || p[0] == "" || p[1] == "" {
			break
		}
		if seq, err := strconv.Atoi(p[2]); err == nil && seq >= 0 {
			return DeepLink{Kind: kind, SessionID: p[0], Agent: p[1], Seq: seq}, true
		}
	case "file":
		sid, rel, _ := strings.Cut(arg, "/")
		file, err := url.PathUnescape(rel)
		if err == nil && sid != "" && file != "" {
			return DeepLink{Kind: kind, SessionID: sid, File: file}, true
		}
	}
	return DeepLink{}, false
}

// pendingLink holds a giantmem:// link that arrived before the frontend was
// listening (cold launch from a click in the daily note). onURLOpen runs on the
// main thread, bound methods do not, hence the mutex.
var (
	pendingMu   sync.Mutex
	pendingLink DeepLink
)

func (a *App) onURLOpen(raw string) {
	l, ok := parseLink(raw)
	if !ok {
		return
	}
	pendingMu.Lock()
	pendingLink = l
	pendingMu.Unlock()
	if a.ctx == nil {
		return
	}
	runtime.EventsEmit(a.ctx, "open-link", l)
	runtime.WindowShow(a.ctx)
}

// TakePendingLink hands the frontend a deep link that arrived before it mounted.
func (a *App) TakePendingLink() DeepLink {
	pendingMu.Lock()
	defer pendingMu.Unlock()
	l := pendingLink
	pendingLink = DeepLink{}
	return l
}

// indexedToolUses answers the tools tab from the tool_uses index (subagents and
// pruned transcripts included). ok=false means the index is empty: scan instead.
func (a *App) indexedToolUses(f ToolUseFilter) ([]ToolUseHit, bool, error) {
	var n int
	if a.archive.QueryRow(`SELECT COUNT(*) FROM (SELECT 1 FROM tool_uses LIMIT 1)`).Scan(&n) != nil || n == 0 {
		return nil, false, nil
	}
	q := hub.ToolQuery{Match: f.Query, Tool: f.ToolName, Project: f.Project, Errors: f.ErrorsOnly, Starred: f.StarredOnly, Limit: f.Limit, WithIO: true}
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
			Link:         r.Link,
			Starred:      r.Starred,
		})
	}
	return out, true, nil
}

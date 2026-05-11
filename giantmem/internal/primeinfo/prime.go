// Package primeinfo contains the DB + filesystem query logic for the prime
// command, shared between the daemon handler and the CLI direct-open path.
package primeinfo

import (
	"database/sql"
	"os"
	"path/filepath"

	"github.com/bearded-giant/giant-tooling/giantmem/internal/project"
)

// Doc is a recently-written live workspace document.
type Doc struct {
	Path    string `json:"path"`
	DirType string `json:"dir_type,omitempty"`
	Feature string `json:"feature,omitempty"`
	Mtime   int64  `json:"mtime"`
}

// Session is a recently-indexed Claude session.
type Session struct {
	SessionID string `json:"session_id"`
	Topic     string `json:"topic,omitempty"`
	Cwd       string `json:"cwd,omitempty"`
	Timestamp string `json:"timestamp"`
}

// Payload is the full primer response.
type Payload struct {
	Cwd            string    `json:"cwd"`
	Project        string    `json:"project"`
	WorktreePath   string    `json:"worktree_path"`
	ActiveFeature  string    `json:"active_feature,omitempty"`
	RecentDocs     []Doc     `json:"recent_docs"`
	RecentSessions []Session `json:"recent_sessions"`
	HistoryTail    []string  `json:"history_tail,omitempty"`
}

// Build queries archive and live for a context primer for cwd.
// archive and live may be nil. recentN/sessionsN/historyN control result limits
// (a value <= 0 is replaced by the default).
func Build(archive, live *sql.DB, cwd, archiveBase string, recentN, sessionsN, historyN int) (*Payload, error) {
	if recentN <= 0 {
		recentN = 3
	}
	if sessionsN <= 0 {
		sessionsN = 2
	}
	if historyN <= 0 {
		historyN = 5
	}

	info := project.Detect(cwd, archiveBase)
	p := &Payload{
		Cwd:           cwd,
		Project:       info.Project,
		WorktreePath:  info.WorktreePath,
		ActiveFeature: project.FeatureFromGiantmem(info.WorktreePath),
	}

	if live != nil {
		rows, err := live.Query(
			`SELECT path, COALESCE(dir_type,''), COALESCE(feature,''), mtime
               FROM live_docs
              WHERE project LIKE ?
              ORDER BY mtime DESC LIMIT ?`,
			"%"+info.Project+"%", recentN,
		)
		if err == nil {
			for rows.Next() {
				var d Doc
				if err := rows.Scan(&d.Path, &d.DirType, &d.Feature, &d.Mtime); err == nil {
					p.RecentDocs = append(p.RecentDocs, d)
				}
			}
			rows.Close()
		}
	}

	if archive != nil {
		rows, err := archive.Query(
			`SELECT COALESCE(session_id,''), COALESCE(topic,''),
                    COALESCE(cwd,''), timestamp
               FROM documents
              WHERE source_type = 'session'
                AND (project LIKE ? OR cwd LIKE ?)
              ORDER BY timestamp DESC LIMIT ?`,
			"%"+info.Project+"%", "%"+info.WorktreePath+"%", sessionsN,
		)
		if err == nil {
			for rows.Next() {
				var s Session
				if err := rows.Scan(&s.SessionID, &s.Topic, &s.Cwd, &s.Timestamp); err == nil {
					p.RecentSessions = append(p.RecentSessions, s)
				}
			}
			rows.Close()
		}
	}

	histPath := filepath.Join(info.WorktreePath, ".giantmem", "history", "sessions.md")
	if raw, err := os.ReadFile(histPath); err == nil {
		lines := splitLines(string(raw))
		for len(lines) > 0 && lines[len(lines)-1] == "" {
			lines = lines[:len(lines)-1]
		}
		if len(lines) > historyN {
			lines = lines[len(lines)-historyN:]
		}
		p.HistoryTail = lines
	}

	return p, nil
}

func splitLines(s string) []string {
	var out []string
	cur := ""
	for _, r := range s {
		if r == '\n' {
			out = append(out, cur)
			cur = ""
			continue
		}
		cur += string(r)
	}
	if cur != "" {
		out = append(out, cur)
	}
	return out
}

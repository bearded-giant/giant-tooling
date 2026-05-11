// Package statusinfo contains the DB query logic for the status command so it
// can be called from both the daemon handler and the CLI direct-open path.
package statusinfo

import (
	"database/sql"
	"os"
	"path/filepath"
	"time"

	"github.com/bearded-giant/giant-tooling/giantmem/internal/project"
)

// Status is the full status snapshot for a workspace.
type Status struct {
	Project         string `json:"project"`
	WorktreePath    string `json:"worktree_path,omitempty"`
	ActiveFeature   string `json:"active_feature,omitempty"`
	LiveDocsToday   int    `json:"live_docs_today"`
	LiveDocsTotal   int    `json:"live_docs_total"`
	StaleWorkspaces int    `json:"stale_workspaces"`
	LastIndexedAt   string `json:"last_indexed_at,omitempty"`
	LastLiveWriteAt string `json:"last_live_write_at,omitempty"`
}

// Build queries archive and live for project status.
// archive and live may be nil (those fields are left zeroed).
// projectFilter overrides the detected project for DB queries (empty = auto).
// staleD controls the stale workspace scan (0 = skip).
func Build(archive, live *sql.DB, cwd, archiveBase, projectFilter string, staleD int) Status {
	info := project.Detect(cwd, archiveBase)
	s := Status{
		Project:       info.Project,
		WorktreePath:  info.WorktreePath,
		ActiveFeature: project.FeatureFromGiantmem(info.WorktreePath),
	}

	filter := projectFilter
	if filter == "" {
		filter = info.Project
	}

	if live != nil {
		startOfDay := time.Now().Truncate(24 * time.Hour).Unix()
		live.QueryRow(
			`SELECT COUNT(*) FROM live_docs WHERE project LIKE ? AND mtime >= ?`,
			"%"+filter+"%", startOfDay,
		).Scan(&s.LiveDocsToday)
		live.QueryRow(
			`SELECT COUNT(*) FROM live_docs WHERE project LIKE ?`,
			"%"+filter+"%",
		).Scan(&s.LiveDocsTotal)
		var lastMtime int64
		live.QueryRow(
			`SELECT MAX(mtime) FROM live_docs WHERE project LIKE ?`,
			"%"+filter+"%",
		).Scan(&lastMtime)
		if lastMtime > 0 {
			s.LastLiveWriteAt = time.Unix(lastMtime, 0).Format(time.RFC3339)
		}
	}

	if archive != nil {
		var ia string
		archive.QueryRow(`SELECT MAX(indexed_at) FROM documents`).Scan(&ia)
		s.LastIndexedAt = ia
	}

	if staleD > 0 {
		home, _ := os.UserHomeDir()
		root := filepath.Join(home, "dev")
		s.StaleWorkspaces = CountStaleWorkspaces(root, staleD)
	}

	return s
}

// CountStaleWorkspaces counts .giantmem dirs under root whose newest .md file
// is older than days.
func CountStaleWorkspaces(root string, days int) int {
	cutoff := time.Now().AddDate(0, 0, -days)
	count := 0
	filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil || !d.IsDir() {
			return nil
		}
		name := d.Name()
		if name == "node_modules" || name == ".git" || name == ".venv" || name == "venv" {
			return filepath.SkipDir
		}
		if name != ".giantmem" {
			return nil
		}
		var newest time.Time
		filepath.WalkDir(p, func(p2 string, d2 os.DirEntry, _ error) error {
			if d2.IsDir() {
				return nil
			}
			if filepath.Ext(p2) != ".md" {
				return nil
			}
			info, err := d2.Info()
			if err != nil {
				return nil
			}
			if info.ModTime().After(newest) {
				newest = info.ModTime()
			}
			return nil
		})
		if !newest.IsZero() && newest.Before(cutoff) {
			count++
		}
		return filepath.SkipDir
	})
	return count
}

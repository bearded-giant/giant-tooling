package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/bryangrimes/gm/internal/db"
	"github.com/bryangrimes/gm/internal/output"
	"github.com/bryangrimes/gm/internal/project"
	"github.com/spf13/cobra"
)

func jsonMarshal(v any) ([]byte, error) {
	return json.MarshalIndent(v, "", "  ")
}

var (
	statusJSON      bool
	statusStaleD    int
	statusRoot      string
	statusProject   string
	statusWriteFile string
)

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "One-shot snapshot for statuslines and quick checks",
	Long: `Returns active_feature for cwd, live_docs written today, last ingest time,
and a count of stale workspaces. Designed to be cheap (<50ms) for statusline use.

Defaults to the current dir. Pass --root <path> to query a different worktree.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		cwd, _ := os.Getwd()
		if statusRoot != "" {
			cwd = statusRoot
		}
		s := buildStatus(cwd)
		if statusWriteFile != "" {
			data, err := jsonMarshalIndent(s)
			if err != nil {
				return err
			}
			if err := os.MkdirAll(filepath.Dir(statusWriteFile), 0o755); err != nil {
				return err
			}
			return os.WriteFile(statusWriteFile, data, 0o644)
		}
		if statusJSON {
			return output.JSON(s)
		}
		printStatus(s)
		return nil
	},
}

func jsonMarshalIndent(v any) ([]byte, error) {
	return jsonMarshal(v)
}

type statusPayload struct {
	Project          string `json:"project"`
	WorktreePath     string `json:"worktree_path,omitempty"`
	ActiveFeature    string `json:"active_feature,omitempty"`
	LiveDocsToday    int    `json:"live_docs_today"`
	LiveDocsTotal    int    `json:"live_docs_total"`
	StaleWorkspaces  int    `json:"stale_workspaces"`
	LastIndexedAt    string `json:"last_indexed_at,omitempty"`
	LastLiveWriteAt  string `json:"last_live_write_at,omitempty"`
}

func buildStatus(cwd string) statusPayload {
	info := project.Detect(cwd, archiveBasePath())
	s := statusPayload{
		Project:       info.Project,
		WorktreePath:  info.WorktreePath,
		ActiveFeature: project.FeatureFromGiantmem(info.WorktreePath),
	}

	if live, err := db.Open(liveDBPath()); err == nil {
		defer live.Close()
		startOfDay := time.Now().Truncate(24 * time.Hour).Unix()
		filter := statusProject
		if filter == "" {
			filter = info.Project
		}
		// today
		row := live.QueryRow(
			`SELECT COUNT(*) FROM live_docs WHERE project LIKE ? AND mtime >= ?`,
			"%"+filter+"%", startOfDay,
		)
		row.Scan(&s.LiveDocsToday)
		// total
		row = live.QueryRow(
			`SELECT COUNT(*) FROM live_docs WHERE project LIKE ?`,
			"%"+filter+"%",
		)
		row.Scan(&s.LiveDocsTotal)
		// last write
		var lastMtime int64
		live.QueryRow(
			`SELECT MAX(mtime) FROM live_docs WHERE project LIKE ?`,
			"%"+filter+"%",
		).Scan(&lastMtime)
		if lastMtime > 0 {
			s.LastLiveWriteAt = time.Unix(lastMtime, 0).Format(time.RFC3339)
		}
	}

	if arc, err := db.Open(archiveDBPath()); err == nil {
		defer arc.Close()
		var ia string
		arc.QueryRow(`SELECT MAX(indexed_at) FROM documents`).Scan(&ia)
		s.LastIndexedAt = ia
	}

	if statusStaleD > 0 {
		home, _ := os.UserHomeDir()
		root := filepath.Join(home, "dev")
		s.StaleWorkspaces = countStaleWorkspaces(root, statusStaleD)
	}

	return s
}

func countStaleWorkspaces(root string, days int) int {
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

func printStatus(s statusPayload) {
	fmt.Printf("project:           %s\n", s.Project)
	if s.ActiveFeature != "" {
		fmt.Printf("active feature:    %s\n", s.ActiveFeature)
	}
	fmt.Printf("live docs today:   %d\n", s.LiveDocsToday)
	fmt.Printf("live docs total:   %d\n", s.LiveDocsTotal)
	if s.LastLiveWriteAt != "" {
		fmt.Printf("last live write:   %s\n", s.LastLiveWriteAt)
	}
	if s.LastIndexedAt != "" {
		fmt.Printf("last archive ix:   %s\n", s.LastIndexedAt)
	}
	if s.StaleWorkspaces > 0 {
		fmt.Printf("stale workspaces:  %d\n", s.StaleWorkspaces)
	}
}

func init() {
	statusCmd.Flags().BoolVar(&statusJSON, "json", false, "JSON output (for statuslines)")
	statusCmd.Flags().IntVar(&statusStaleD, "stale-days", 0, "include stale workspace count (0 disables; statuslines should set 30)")
	statusCmd.Flags().StringVar(&statusRoot, "root", "", "use this dir instead of cwd for project detection")
	statusCmd.Flags().StringVar(&statusProject, "project", "", "override project filter")
	statusCmd.Flags().StringVar(&statusWriteFile, "write-cache", "", "write JSON to this file (used by statusline)")
	rootCmd.AddCommand(statusCmd)
}

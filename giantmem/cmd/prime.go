package cmd

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/bearded-giant/giant-tooling/giantmem/internal/db"
	"github.com/bearded-giant/giant-tooling/giantmem/internal/mrs"
	"github.com/bearded-giant/giant-tooling/giantmem/internal/output"
	"github.com/bearded-giant/giant-tooling/giantmem/internal/project"
	"github.com/spf13/cobra"
)

var (
	primeJSON      bool
	primeRecentN   int
	primeSessionsN int
	primeHistoryN  int
	primeMRsN      int
	primeMRDays    int
)

var primeCmd = &cobra.Command{
	Use:   "prime [path]",
	Short: "Emit a context primer for a workspace (active feature, recent docs/sessions/history)",
	Long: `Designed for Claude Code SessionStart hooks. Walks up from cwd (or the
given path), detects project, reads features.json, queries live.db for the
project's recent docs, archives.db for recent sessions, and the .giantmem/history
log if present.`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cwd, _ := os.Getwd()
		if len(args) > 0 {
			cwd = args[0]
		}
		p, err := buildPrime(cwd)
		if err != nil {
			return err
		}
		if primeJSON {
			return output.JSON(p)
		}
		printPrimeText(p)
		return nil
	},
}

type primePayload struct {
	Cwd            string      `json:"cwd"`
	Project        string      `json:"project"`
	WorktreePath   string      `json:"worktree_path"`
	ActiveFeature  string      `json:"active_feature,omitempty"`
	RecentDocs     []primeDoc  `json:"recent_docs"`
	RecentSessions []primeSess `json:"recent_sessions"`
	HistoryTail    []string    `json:"history_tail,omitempty"`
	OpenMRs        []primeMR   `json:"open_mrs,omitempty"`
}

type primeMR struct {
	Label  string `json:"label"`
	URL    string `json:"url"`
	State  string `json:"state"`
	Level  string `json:"level,omitempty"`
	Title  string `json:"title,omitempty"`
	Opener string `json:"opener"`
	Role   string `json:"role"`
	Cached bool   `json:"cached,omitempty"`
}

type primeDoc struct {
	Path    string `json:"path"`
	DirType string `json:"dir_type,omitempty"`
	Feature string `json:"feature,omitempty"`
	Mtime   int64  `json:"mtime"`
}

type primeSess struct {
	SessionID string `json:"session_id"`
	Topic     string `json:"topic,omitempty"`
	Cwd       string `json:"cwd,omitempty"`
	Timestamp string `json:"timestamp"`
}

func buildPrime(cwd string) (*primePayload, error) {
	info := project.Detect(cwd, archiveBasePath())
	p := &primePayload{
		Cwd:           cwd,
		Project:       info.Project,
		WorktreePath:  info.WorktreePath,
		ActiveFeature: project.FeatureFromGiantmem(info.WorktreePath),
	}

	// recent docs from live.db
	if live, err := db.Open(liveDBPath()); err == nil {
		defer live.Close()
		rows, err := live.Query(
			`SELECT path, COALESCE(dir_type,''), COALESCE(feature,''), mtime
               FROM live_docs
              WHERE project LIKE ?
              ORDER BY mtime DESC LIMIT ?`,
			"%"+info.Project+"%", primeRecentN,
		)
		if err == nil {
			for rows.Next() {
				var d primeDoc
				if err := rows.Scan(&d.Path, &d.DirType, &d.Feature, &d.Mtime); err == nil {
					p.RecentDocs = append(p.RecentDocs, d)
				}
			}
			rows.Close()
		}
	}

	// recent sessions from archives.db
	if arc, err := db.Open(archiveDBPath()); err == nil {
		defer arc.Close()
		rows, err := arc.Query(
			`SELECT COALESCE(session_id,''), COALESCE(topic,''),
                    COALESCE(cwd,''), timestamp
               FROM documents
              WHERE source_type = 'session'
                AND (project LIKE ? OR cwd LIKE ?)
              ORDER BY timestamp DESC LIMIT ?`,
			"%"+info.Project+"%", "%"+info.WorktreePath+"%", primeSessionsN,
		)
		if err == nil {
			for rows.Next() {
				var s primeSess
				if err := rows.Scan(&s.SessionID, &s.Topic, &s.Cwd, &s.Timestamp); err == nil {
					p.RecentSessions = append(p.RecentSessions, s)
				}
			}
			rows.Close()
		}
	}

	if arc, err := db.Open(archiveDBPath()); err == nil {
		p.OpenMRs = primeOpenMRs(arc, info.Project)
		arc.Close()
	}

	// history tail
	histPath := filepath.Join(info.WorktreePath, ".giantmem", "history", "sessions.md")
	if raw, err := os.ReadFile(histPath); err == nil {
		lines := splitLines(string(raw))
		// trim empty trailing lines
		for len(lines) > 0 && lines[len(lines)-1] == "" {
			lines = lines[:len(lines)-1]
		}
		if len(lines) > primeHistoryN {
			lines = lines[len(lines)-primeHistoryN:]
		}
		p.HistoryTail = lines
	}

	return p, nil
}

// primeOpenMRs reads live status under a short deadline because prime runs in a SessionStart hook.
func primeOpenMRs(arc *sql.DB, project string) []primeMR {
	if project == "" {
		return nil
	}
	since := time.Now().AddDate(0, 0, -primeMRDays).UTC().Format(time.RFC3339)
	list, err := mrs.Find(arc, mrs.Filter{Project: project, Since: since})
	if err != nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	mrs.Refresh(ctx, arc, list, true)
	var out []primeMR
	for _, m := range list {
		if m.Status.Terminal() || len(out) == primeMRsN {
			continue
		}
		pm := primeMR{Label: m.Label, URL: m.URL, State: m.Status.Show(), Opener: m.Opener.SessionID[:min(8, len(m.Opener.SessionID))], Role: m.Opener.Role}
		if m.Status != nil {
			pm.Level, pm.Title, pm.Cached = m.Status.Level, m.Status.Title, m.Status.Cached || m.Status.Error != ""
		}
		out = append(out, pm)
	}
	return out
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

func printPrimeText(p *primePayload) {
	fmt.Printf("project: %s\n", p.Project)
	fmt.Printf("worktree: %s\n", p.WorktreePath)
	if p.ActiveFeature != "" {
		fmt.Printf("active feature: %s\n", p.ActiveFeature)
	}
	if len(p.RecentDocs) > 0 {
		fmt.Println("\nrecent docs:")
		for _, d := range p.RecentDocs {
			fmt.Printf("  %s  %s\n", d.DirType, d.Path)
		}
	}
	if len(p.RecentSessions) > 0 {
		fmt.Println("\nrecent sessions:")
		for _, s := range p.RecentSessions {
			fmt.Printf("  %s  %s  %s\n", s.SessionID[:8], s.Topic, s.Timestamp)
		}
	}
	if len(p.OpenMRs) > 0 {
		fmt.Println("\nopen MRs from this project's sessions:")
		for _, m := range p.OpenMRs {
			fmt.Printf("  %s  %s  %s  %s\n", m.Label, m.State, m.Opener, m.URL)
		}
	}
	if len(p.HistoryTail) > 0 {
		fmt.Println("\nhistory tail:")
		for _, l := range p.HistoryTail {
			if l == "" {
				continue
			}
			fmt.Printf("  %s\n", l)
		}
	}
}

func init() {
	primeCmd.Flags().BoolVar(&primeJSON, "json", false, "JSON output (for hooks)")
	primeCmd.Flags().IntVar(&primeRecentN, "recent", 3, "max recent live docs")
	primeCmd.Flags().IntVar(&primeSessionsN, "sessions", 2, "max recent sessions")
	primeCmd.Flags().IntVar(&primeHistoryN, "history", 5, "max history.md tail lines")
	primeCmd.Flags().IntVar(&primeMRsN, "mrs", 8, "max open MRs")
	primeCmd.Flags().IntVar(&primeMRDays, "mr-days", 30, "open MRs a session linked in the last N days")
	rootCmd.AddCommand(primeCmd)
}

// silence unused
var _ = json.Marshal

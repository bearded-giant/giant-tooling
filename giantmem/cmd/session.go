package cmd

import (
	"database/sql"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"text/tabwriter"

	"github.com/bryangrimes/gm/internal/db"
	"github.com/bryangrimes/gm/internal/output"
	"github.com/spf13/cobra"
)

var sessionCmd = &cobra.Command{
	Use:   "session",
	Short: "List, search, and resume Claude Code sessions",
}

var (
	sessListProject string
	sessListLimit   int
	sessFindLimit   int
	sessJSON        bool
)

type sessRow struct {
	ID        string `json:"id"`
	JSONLPath string `json:"jsonl_path"`
	Project   string `json:"project"`
	Cwd       string `json:"cwd,omitempty"`
	Topic     string `json:"topic,omitempty"`
	Timestamp string `json:"timestamp"`
}

var sessListCmd = &cobra.Command{
	Use:   "list",
	Short: "List recent sessions",
	RunE: func(cmd *cobra.Command, args []string) error {
		a, err := db.Open(archiveDBPath())
		if err != nil {
			return err
		}
		defer a.Close()
		if err := db.EnsureArchive(a); err != nil {
			return err
		}

		var (
			conds []string
			qargs []any
		)
		conds = append(conds, "source_type = 'session'")
		if sessListProject != "" {
			conds = append(conds, "project LIKE ?")
			qargs = append(qargs, "%"+sessListProject+"%")
		}
		q := fmt.Sprintf(`
            SELECT COALESCE(session_id,''), filepath, project, COALESCE(cwd,''),
                   COALESCE(topic,''), timestamp
              FROM documents
             WHERE %s
             ORDER BY timestamp DESC
             LIMIT ?`, strings.Join(conds, " AND "))
		qargs = append(qargs, sessListLimit)
		rows, err := a.Query(q, qargs...)
		if err != nil {
			return err
		}
		defer rows.Close()

		var hits []sessRow
		for rows.Next() {
			var r sessRow
			if err := rows.Scan(&r.ID, &r.JSONLPath, &r.Project, &r.Cwd, &r.Topic, &r.Timestamp); err != nil {
				return err
			}
			hits = append(hits, r)
		}
		if sessJSON {
			return output.JSON(hits)
		}
		printSessions(hits)
		return nil
	},
}

var sessFindCmd = &cobra.Command{
	Use:   "find <query>",
	Short: "FTS5 search of session transcripts",
	Args:  cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		query := strings.Join(args, " ")
		a, err := db.Open(archiveDBPath())
		if err != nil {
			return err
		}
		defer a.Close()
		if err := db.EnsureArchive(a); err != nil {
			return err
		}

		q := `
            SELECT bm25(documents_fts), COALESCE(d.session_id,''), d.filepath, d.project,
                   COALESCE(d.cwd,''), COALESCE(d.topic,''), d.timestamp
              FROM documents_fts
              JOIN documents d ON d.id = documents_fts.rowid
             WHERE documents_fts MATCH ?
               AND d.source_type = 'session'
             ORDER BY bm25(documents_fts)
             LIMIT ?`
		rows, err := a.Query(q, query, sessFindLimit)
		if err != nil {
			return err
		}
		defer rows.Close()

		var hits []sessRow
		for rows.Next() {
			var r sessRow
			var score float64
			if err := rows.Scan(&score, &r.ID, &r.JSONLPath, &r.Project, &r.Cwd, &r.Topic, &r.Timestamp); err != nil {
				return err
			}
			hits = append(hits, r)
		}
		if sessJSON {
			return output.JSON(hits)
		}
		printSessions(hits)
		return nil
	},
}

var sessShowCmd = &cobra.Command{
	Use:   "show <id-prefix>",
	Short: "Show a session's metadata",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		a, err := db.Open(archiveDBPath())
		if err != nil {
			return err
		}
		defer a.Close()
		if err := db.EnsureArchive(a); err != nil {
			return err
		}
		r, err := lookupSession(a, args[0])
		if err != nil {
			return err
		}
		fmt.Printf("id        %s\n", r.ID)
		fmt.Printf("project   %s\n", r.Project)
		fmt.Printf("cwd       %s\n", r.Cwd)
		fmt.Printf("topic     %s\n", r.Topic)
		fmt.Printf("timestamp %s\n", r.Timestamp)
		fmt.Printf("jsonl     %s\n", r.JSONLPath)
		return nil
	},
}

var sessResumeCmd = &cobra.Command{
	Use:   "resume <id-prefix>",
	Short: "Resume a session: cd to its cwd, exec `claude --resume <uuid>`",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		a, err := db.Open(archiveDBPath())
		if err != nil {
			return err
		}
		if err := db.EnsureArchive(a); err != nil {
			a.Close()
			return err
		}
		r, err := lookupSession(a, args[0])
		a.Close()
		if err != nil {
			return err
		}
		if r.ID == "" {
			return fmt.Errorf("session id missing on row (filepath=%s)", r.JSONLPath)
		}
		if r.Cwd == "" {
			fmt.Fprintf(os.Stderr, "warning: no cwd recorded; will resume in current directory\n")
		} else {
			target := resolveCwd(r.Cwd)
			if target != r.Cwd {
				fmt.Fprintf(os.Stderr, "note: %s missing; using %s\n", r.Cwd, target)
			}
			if err := os.Chdir(target); err != nil {
				return fmt.Errorf("chdir %s: %w", target, err)
			}
		}
		bin, err := exec.LookPath("claude")
		if err != nil {
			return fmt.Errorf("claude not in PATH")
		}
		fmt.Fprintf(os.Stderr, "resuming %s in %s\n", r.ID, r.Cwd)
		return syscall.Exec(bin, []string{"claude", "--resume", r.ID}, os.Environ())
	},
}

// resolveCwd applies fallbacks when the recorded cwd no longer exists:
// first try <cwd>-wt/main and <cwd>-wt/master (bare-with-worktrees layout).
func resolveCwd(cwd string) string {
	if dirExists(cwd) {
		return cwd
	}
	for _, branch := range []string{"main", "master"} {
		alt := cwd + "-wt/" + branch
		if dirExists(alt) {
			return alt
		}
	}
	return cwd
}

func dirExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}

func lookupSession(a *sql.DB, prefix string) (sessRow, error) {
	q := `
        SELECT COALESCE(session_id,''), filepath, project, COALESCE(cwd,''),
               COALESCE(topic,''), timestamp
          FROM documents
         WHERE source_type = 'session'
           AND (session_id LIKE ? OR filepath LIKE ?)
         ORDER BY timestamp DESC
         LIMIT 5`
	rows, err := a.Query(q, prefix+"%", "%"+prefix+"%")
	if err != nil {
		return sessRow{}, err
	}
	defer rows.Close()
	var matches []sessRow
	for rows.Next() {
		var r sessRow
		if err := rows.Scan(&r.ID, &r.JSONLPath, &r.Project, &r.Cwd, &r.Topic, &r.Timestamp); err != nil {
			return sessRow{}, err
		}
		matches = append(matches, r)
	}
	if len(matches) == 0 {
		return sessRow{}, fmt.Errorf("no session matching %q", prefix)
	}
	if len(matches) > 1 {
		fmt.Fprintln(os.Stderr, "multiple matches:")
		for _, m := range matches {
			fmt.Fprintf(os.Stderr, "  %s  %s  %s\n", m.ID, m.Project, m.Timestamp)
		}
		return sessRow{}, fmt.Errorf("ambiguous prefix %q (%d matches); use a longer prefix", prefix, len(matches))
	}
	return matches[0], nil
}

func printSessions(hits []sessRow) {
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tPROJECT\tCWD\tTOPIC\tTIMESTAMP")
	for _, h := range hits {
		id := h.ID
		if len(id) > 8 {
			id = id[:8]
		}
		cwd := h.Cwd
		if len(cwd) > 50 {
			cwd = "..." + cwd[len(cwd)-47:]
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", id, h.Project, cwd, h.Topic, h.Timestamp)
	}
	w.Flush()
}

func init() {
	sessListCmd.Flags().StringVarP(&sessListProject, "project", "p", "", "filter by project (LIKE)")
	sessListCmd.Flags().IntVarP(&sessListLimit, "limit", "n", 20, "max rows")
	sessListCmd.Flags().BoolVar(&sessJSON, "json", false, "JSON output")

	sessFindCmd.Flags().IntVarP(&sessFindLimit, "limit", "n", 20, "max rows")
	sessFindCmd.Flags().BoolVar(&sessJSON, "json", false, "JSON output")

	sessionCmd.AddCommand(sessListCmd)
	sessionCmd.AddCommand(sessFindCmd)
	sessionCmd.AddCommand(sessShowCmd)
	sessionCmd.AddCommand(sessResumeCmd)
	rootCmd.AddCommand(sessionCmd)
}

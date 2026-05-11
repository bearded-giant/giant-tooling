package cmd

import (
	"database/sql"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/bearded-giant/giant-tooling/giantmem/internal/daemon"
	"github.com/bearded-giant/giant-tooling/giantmem/internal/db"
	"github.com/bearded-giant/giant-tooling/giantmem/internal/output"
	"github.com/bearded-giant/giant-tooling/giantmem/internal/sessioninfo"
	sessionsExport "github.com/bearded-giant/giant-tooling/giantmem/internal/sessions"
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
	sessNoDaemon    bool
)

var sessListCmd = &cobra.Command{
	Use:   "list",
	Short: "List recent sessions",
	RunE: func(cmd *cobra.Command, args []string) error {
		hits, err := dispatchSessionList()
		if err != nil {
			return err
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
		hits, err := dispatchSessionFind(query)
		if err != nil {
			return err
		}
		if sessJSON {
			return output.JSON(hits)
		}
		printSessions(hits)
		return nil
	},
}

// dispatchSessionList tries the daemon first, falls back to direct DB.
func dispatchSessionList() ([]sessioninfo.Row, error) {
	if !sessNoDaemon && os.Getenv("GIANTMEM_NO_DAEMON") == "" {
		sock := daemon.DefaultSocketPath()
		if daemon.SocketAlive(sock, 250*time.Millisecond) {
			cli := daemon.NewClient(sock, 5*time.Second)
			var out struct {
				Rows []sessioninfo.Row `json:"rows"`
			}
			err := cli.Call("session.list", &daemon.SessionListParams{
				Project: sessListProject,
				Limit:   sessListLimit,
			}, &out)
			if err == nil {
				return out.Rows, nil
			}
			if !daemon.IsSchemaDrift(err) {
				fmt.Fprintf(os.Stderr, "daemon error, falling back: %v\n", err)
			}
		}
	}
	return sessionListDirect()
}

func sessionListDirect() ([]sessioninfo.Row, error) {
	a, err := db.Open(archiveDBPath())
	if err != nil {
		return nil, err
	}
	defer a.Close()
	if err := db.EnsureArchive(a); err != nil {
		return nil, err
	}
	return sessioninfo.List(a, sessListProject, sessListLimit)
}

// dispatchSessionFind tries the daemon first, falls back to direct DB.
func dispatchSessionFind(query string) ([]sessioninfo.Row, error) {
	if !sessNoDaemon && os.Getenv("GIANTMEM_NO_DAEMON") == "" {
		sock := daemon.DefaultSocketPath()
		if daemon.SocketAlive(sock, 250*time.Millisecond) {
			cli := daemon.NewClient(sock, 5*time.Second)
			var out struct {
				Rows []sessioninfo.Row `json:"rows"`
			}
			err := cli.Call("session.find", &daemon.SessionFindParams{
				Query: query,
				Limit: sessFindLimit,
			}, &out)
			if err == nil {
				return out.Rows, nil
			}
			if !daemon.IsSchemaDrift(err) {
				fmt.Fprintf(os.Stderr, "daemon error, falling back: %v\n", err)
			}
		}
	}
	return sessionFindDirect(query)
}

func sessionFindDirect(query string) ([]sessioninfo.Row, error) {
	a, err := db.Open(archiveDBPath())
	if err != nil {
		return nil, err
	}
	defer a.Close()
	if err := db.EnsureArchive(a); err != nil {
		return nil, err
	}
	return sessioninfo.Find(a, query, sessFindLimit)
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

// resolveCwd applies fallbacks when the recorded cwd no longer exists.
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
	home, _ := os.UserHomeDir()
	roots := []string{filepath.Join(home, "dev")}
	pattern := filepath.Base(cwd)
	entries, err := loadOrBuildCache(home, roots, false)
	if err != nil {
		return cwd
	}
	matches := matchEntries(entries, pattern)
	if len(matches) == 1 {
		return matches[0].Path
	}
	return cwd
}

func dirExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}

func lookupSession(a *sql.DB, prefix string) (sessioninfo.Row, error) {
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
		return sessioninfo.Row{}, err
	}
	defer rows.Close()
	var matches []sessioninfo.Row
	for rows.Next() {
		var r sessioninfo.Row
		if err := rows.Scan(&r.ID, &r.JSONLPath, &r.Project, &r.Cwd, &r.Topic, &r.Timestamp); err != nil {
			return sessioninfo.Row{}, err
		}
		matches = append(matches, r)
	}
	if len(matches) == 0 {
		return sessioninfo.Row{}, fmt.Errorf("no session matching %q", prefix)
	}
	if len(matches) > 1 {
		fmt.Fprintln(os.Stderr, "multiple matches:")
		for _, m := range matches {
			fmt.Fprintf(os.Stderr, "  %s  %s  %s\n", m.ID, m.Project, m.Timestamp)
		}
		return sessioninfo.Row{}, fmt.Errorf("ambiguous prefix %q (%d matches); use a longer prefix", prefix, len(matches))
	}
	return matches[0], nil
}

func printSessions(hits []sessioninfo.Row) {
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

var (
	sessExportOut   string
	sessExportTools bool
	sessDiffJSON    bool
)

var sessExportCmd = &cobra.Command{
	Use:   "export <id-prefix>",
	Short: "Export a session as a clean markdown transcript",
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
		t, err := sessionsExport.Parse(r.JSONLPath)
		if err != nil {
			return fmt.Errorf("parse jsonl: %w", err)
		}
		var w *os.File
		if sessExportOut == "" {
			w = os.Stdout
		} else {
			w, err = os.Create(sessExportOut)
			if err != nil {
				return err
			}
			defer w.Close()
		}
		sessionsExport.Markdown(w, t, sessExportTools)
		if sessExportOut != "" {
			fmt.Fprintf(os.Stderr, "wrote %s\n", sessExportOut)
		}
		return nil
	},
}

var sessDiffCmd = &cobra.Command{
	Use:   "diff <id-a> <id-b>",
	Short: "Compare two sessions: file sets, topics, lengths",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		a, err := db.Open(archiveDBPath())
		if err != nil {
			return err
		}
		if err := db.EnsureArchive(a); err != nil {
			a.Close()
			return err
		}
		ra, err := lookupSession(a, args[0])
		if err != nil {
			a.Close()
			return err
		}
		rb, err := lookupSession(a, args[1])
		a.Close()
		if err != nil {
			return err
		}
		ta, err := sessionsExport.Parse(ra.JSONLPath)
		if err != nil {
			return err
		}
		tb, err := sessionsExport.Parse(rb.JSONLPath)
		if err != nil {
			return err
		}
		report := buildSessionDiff(ra, rb, ta, tb)
		if sessDiffJSON {
			return output.JSON(report)
		}
		printSessionDiff(report)
		return nil
	},
}

type sessionDiffReport struct {
	A    diffSide   `json:"a"`
	B    diffSide   `json:"b"`
	Only diffShared `json:"only"`
	Both []string   `json:"shared_files"`
}

type diffSide struct {
	ID       string   `json:"id"`
	Topic    string   `json:"topic"`
	Project  string   `json:"project"`
	UserMsgs int      `json:"user_msgs"`
	AsstMsgs int      `json:"assistant_msgs"`
	Bash     int      `json:"bash_count"`
	Files    []string `json:"files,omitempty"`
	Duration string   `json:"duration,omitempty"`
}

type diffShared struct {
	A []string `json:"a_only"`
	B []string `json:"b_only"`
}

func buildSessionDiff(ra, rb sessioninfo.Row, ta, tb *sessionsExport.Transcript) sessionDiffReport {
	mkSet := func(items []string) map[string]bool {
		s := map[string]bool{}
		for _, x := range items {
			s[x] = true
		}
		return s
	}
	sa, sb := mkSet(ta.FilesTouched), mkSet(tb.FilesTouched)
	var aOnly, bOnly, both []string
	for f := range sa {
		if sb[f] {
			both = append(both, f)
		} else {
			aOnly = append(aOnly, f)
		}
	}
	for f := range sb {
		if !sa[f] {
			bOnly = append(bOnly, f)
		}
	}
	dur := func(t *sessionsExport.Transcript) string {
		if t.StartedAt.IsZero() || t.EndedAt.IsZero() {
			return ""
		}
		return t.EndedAt.Sub(t.StartedAt).Round(time.Second).String()
	}
	return sessionDiffReport{
		A: diffSide{
			ID: ra.ID, Topic: ra.Topic, Project: ra.Project,
			UserMsgs: ta.UserMsgs, AsstMsgs: ta.AssistantMsgs, Bash: ta.BashCount,
			Files: ta.FilesTouched, Duration: dur(ta),
		},
		B: diffSide{
			ID: rb.ID, Topic: rb.Topic, Project: rb.Project,
			UserMsgs: tb.UserMsgs, AsstMsgs: tb.AssistantMsgs, Bash: tb.BashCount,
			Files: tb.FilesTouched, Duration: dur(tb),
		},
		Only: diffShared{A: aOnly, B: bOnly},
		Both: both,
	}
}

func printSessionDiff(r sessionDiffReport) {
	fmt.Printf("== A: %s ==\n", r.A.ID)
	fmt.Printf("  project: %s    topic: %s    duration: %s\n", r.A.Project, r.A.Topic, r.A.Duration)
	fmt.Printf("  msgs: %d user / %d asst    bash: %d    files: %d\n",
		r.A.UserMsgs, r.A.AsstMsgs, r.A.Bash, len(r.A.Files))
	fmt.Printf("== B: %s ==\n", r.B.ID)
	fmt.Printf("  project: %s    topic: %s    duration: %s\n", r.B.Project, r.B.Topic, r.B.Duration)
	fmt.Printf("  msgs: %d user / %d asst    bash: %d    files: %d\n",
		r.B.UserMsgs, r.B.AsstMsgs, r.B.Bash, len(r.B.Files))
	fmt.Printf("\nshared files: %d\n", len(r.Both))
	if len(r.Only.A) > 0 {
		fmt.Printf("\nA-only files (%d):\n", len(r.Only.A))
		for _, f := range r.Only.A {
			fmt.Println("  ", f)
		}
	}
	if len(r.Only.B) > 0 {
		fmt.Printf("\nB-only files (%d):\n", len(r.Only.B))
		for _, f := range r.Only.B {
			fmt.Println("  ", f)
		}
	}
}

func init() {
	sessListCmd.Flags().StringVarP(&sessListProject, "project", "p", "", "filter by project (LIKE)")
	sessListCmd.Flags().IntVarP(&sessListLimit, "limit", "n", 20, "max rows")
	sessListCmd.Flags().BoolVar(&sessJSON, "json", false, "JSON output")
	sessListCmd.Flags().BoolVar(&sessNoDaemon, "no-daemon", false, "skip giantmemd; open DBs directly")

	sessFindCmd.Flags().IntVarP(&sessFindLimit, "limit", "n", 20, "max rows")
	sessFindCmd.Flags().BoolVar(&sessJSON, "json", false, "JSON output")
	sessFindCmd.Flags().BoolVar(&sessNoDaemon, "no-daemon", false, "skip giantmemd; open DBs directly")

	sessExportCmd.Flags().StringVarP(&sessExportOut, "out", "o", "", "write to file instead of stdout")
	sessExportCmd.Flags().BoolVar(&sessExportTools, "tools", true, "include tool-call summary blocks")
	sessDiffCmd.Flags().BoolVar(&sessDiffJSON, "json", false, "JSON output")

	sessionCmd.AddCommand(sessListCmd)
	sessionCmd.AddCommand(sessFindCmd)
	sessionCmd.AddCommand(sessShowCmd)
	sessionCmd.AddCommand(sessResumeCmd)
	sessionCmd.AddCommand(sessExportCmd)
	sessionCmd.AddCommand(sessDiffCmd)
	rootCmd.AddCommand(sessionCmd)
}

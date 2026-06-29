package cmd

import (
	"bufio"
	"database/sql"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/bearded-giant/giant-tooling/giantmem/internal/db"
	"github.com/bearded-giant/giant-tooling/giantmem/internal/output"
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

var sessSetTopicCmd = &cobra.Command{
	Use:   "set-topic <id-prefix> <topic>",
	Short: "Pin a session's topic (survives re-ingest)",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		topic := strings.TrimSpace(args[1])
		if topic == "" {
			return fmt.Errorf("topic must be non-empty")
		}
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
		if r.ID == "" {
			return fmt.Errorf("session id missing on row (filepath=%s)", r.JSONLPath)
		}
		now := time.Now().UTC().Format(time.RFC3339)
		if _, err := a.Exec(
			`INSERT INTO session_topic_overrides (session_id, topic, updated_at)
             VALUES (?, ?, ?)
             ON CONFLICT(session_id) DO UPDATE SET topic = excluded.topic, updated_at = excluded.updated_at`,
			r.ID, topic, now,
		); err != nil {
			return err
		}
		if _, err := a.Exec(
			`UPDATE documents SET topic = ? WHERE session_id = ?`,
			topic, r.ID,
		); err != nil {
			return err
		}
		fmt.Printf("pinned %s topic=%s\n", r.ID, topic)
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
//
// 1. try <cwd>-wt/main, <cwd>-wt/master (legacy bare-with-worktrees layout)
// 2. fall back to giantmem-cd matcher: feed the cwd basename and pick a unique
//    worktree (no fzf, --no-fzf single-match only)
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
	// fallback: use the cd matcher with the basename
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

func buildSessionDiff(ra, rb sessRow, ta, tb *sessionsExport.Transcript) sessionDiffReport {
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

var (
	pruneOrphansDelete bool
	pruneOrphansYes    bool
	pruneOrphansJSON   bool
)

type orphanRow struct {
	Dir   string   `json:"dir"`
	Cwds  []string `json:"cwds"`
	Files int      `json:"files"`
	Bytes int64    `json:"bytes"`
}

var sessPruneOrphansCmd = &cobra.Command{
	Use:   "prune-orphans",
	Short: "Remove ~/.claude/projects dirs whose recorded cwd no longer exists",
	Long: `Walk ~/.claude/projects, read each project dir's recorded cwd(s) from its
session JSONLs, and flag a dir only when it has at least one recorded cwd and
NONE of them still exist on disk (merged feature, pruned worktree, deleted or
renamed repo). A dir keeps if any one of its sessions points at a live cwd, so
a renamed repo whose project dir still holds a current-name session is safe.

Session prose stays searchable in archives.db; only 'claude --resume' and
'session export/diff' for the removed dirs are lost.

Lists by default. Pass --delete to remove, with a y/N prompt unless --yes.
Dirs with no recorded cwd are reported as unverifiable and never deleted.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		home, _ := os.UserHomeDir()
		root := filepath.Join(home, ".claude", "projects")
		entries, err := os.ReadDir(root)
		if err != nil {
			return err
		}
		var orphans, unknown []orphanRow
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			dir := filepath.Join(root, e.Name())
			cwds := projectCwds(dir)
			row := orphanRow{Dir: e.Name(), Cwds: cwds, Files: len(jsonlFiles(dir)), Bytes: dirSizeBytes(dir)}
			switch {
			case len(cwds) == 0:
				unknown = append(unknown, row)
			case anyDirExists(cwds):
				// at least one live cwd — keep
			default:
				orphans = append(orphans, row)
			}
		}
		sort.Slice(orphans, func(i, j int) bool { return orphans[i].Bytes > orphans[j].Bytes })

		if pruneOrphansJSON {
			return output.JSON(map[string]any{"orphans": orphans, "unverifiable": unknown})
		}

		var total int64
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "SIZE_MB\tFILES\tCWD (gone)\tDIR")
		for _, r := range orphans {
			total += r.Bytes
			fmt.Fprintf(w, "%d\t%d\t%s\t%s\n", r.Bytes/(1024*1024), r.Files, strings.Join(r.Cwds, ","), r.Dir)
		}
		w.Flush()
		fmt.Printf("\n%d orphaned dirs, %d MB reclaimable\n", len(orphans), total/(1024*1024))
		if len(unknown) > 0 {
			fmt.Printf("(%d dirs have no recorded cwd — unverifiable, left alone)\n", len(unknown))
		}

		if !pruneOrphansDelete || len(orphans) == 0 {
			if len(orphans) > 0 {
				fmt.Println("\nre-run with --delete to remove them")
			}
			return nil
		}

		if !pruneOrphansYes {
			fmt.Printf("\ndelete %d dirs (%d MB)? [y/N] ", len(orphans), total/(1024*1024))
			ans, _ := bufio.NewReader(os.Stdin).ReadString('\n')
			if a := strings.ToLower(strings.TrimSpace(ans)); a != "y" && a != "yes" {
				fmt.Println("aborted")
				return nil
			}
		}
		var freed int64
		for _, r := range orphans {
			if err := os.RemoveAll(filepath.Join(root, r.Dir)); err != nil {
				fmt.Fprintf(os.Stderr, "  failed %s: %v\n", r.Dir, err)
				continue
			}
			freed += r.Bytes
			fmt.Printf("  removed %s\n", r.Dir)
		}
		fmt.Printf("freed %d MB\n", freed/(1024*1024))
		return nil
	},
}

func jsonlFiles(dir string) []string {
	var out []string
	ents, _ := os.ReadDir(dir)
	for _, e := range ents {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".jsonl") {
			out = append(out, filepath.Join(dir, e.Name()))
		}
	}
	return out
}

// projectCwds returns the distinct cwd recorded across a project dir's session
// files (one per session; cwd is fixed at session start).
func projectCwds(dir string) []string {
	seen := map[string]bool{}
	for _, jf := range jsonlFiles(dir) {
		f, err := os.Open(jf)
		if err != nil {
			continue
		}
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 1024*1024), 16*1024*1024)
		n := 0
		for sc.Scan() {
			if n++; n > 200 {
				break
			}
			line := sc.Text()
			if !strings.Contains(line, `"cwd"`) {
				continue
			}
			var m map[string]any
			if json.Unmarshal([]byte(line), &m) != nil {
				continue
			}
			if c, ok := m["cwd"].(string); ok && c != "" {
				seen[c] = true
				break
			}
		}
		f.Close()
	}
	out := make([]string, 0, len(seen))
	for c := range seen {
		out = append(out, c)
	}
	sort.Strings(out)
	return out
}

func anyDirExists(paths []string) bool {
	for _, p := range paths {
		if dirExists(p) {
			return true
		}
	}
	return false
}

func dirSizeBytes(dir string) int64 {
	var t int64
	filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if info, e := d.Info(); e == nil {
			t += info.Size()
		}
		return nil
	})
	return t
}

func init() {
	sessListCmd.Flags().StringVarP(&sessListProject, "project", "p", "", "filter by project (LIKE)")
	sessListCmd.Flags().IntVarP(&sessListLimit, "limit", "n", 20, "max rows")
	sessListCmd.Flags().BoolVar(&sessJSON, "json", false, "JSON output")

	sessFindCmd.Flags().IntVarP(&sessFindLimit, "limit", "n", 20, "max rows")
	sessFindCmd.Flags().BoolVar(&sessJSON, "json", false, "JSON output")

	sessExportCmd.Flags().StringVarP(&sessExportOut, "out", "o", "", "write to file instead of stdout")
	sessExportCmd.Flags().BoolVar(&sessExportTools, "tools", true, "include tool-call summary blocks")
	sessDiffCmd.Flags().BoolVar(&sessDiffJSON, "json", false, "JSON output")

	sessPruneOrphansCmd.Flags().BoolVar(&pruneOrphansDelete, "delete", false, "remove orphaned dirs (default: list only)")
	sessPruneOrphansCmd.Flags().BoolVar(&pruneOrphansYes, "yes", false, "skip the confirmation prompt with --delete")
	sessPruneOrphansCmd.Flags().BoolVar(&pruneOrphansJSON, "json", false, "JSON output")

	sessionCmd.AddCommand(sessListCmd)
	sessionCmd.AddCommand(sessFindCmd)
	sessionCmd.AddCommand(sessShowCmd)
	sessionCmd.AddCommand(sessSetTopicCmd)
	sessionCmd.AddCommand(sessResumeCmd)
	sessionCmd.AddCommand(sessExportCmd)
	sessionCmd.AddCommand(sessDiffCmd)
	sessionCmd.AddCommand(sessPruneOrphansCmd)
	rootCmd.AddCommand(sessionCmd)
}

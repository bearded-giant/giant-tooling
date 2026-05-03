package cmd

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/bearded-giant/giant-tooling/giantmem/internal/db"
	"github.com/bearded-giant/giant-tooling/giantmem/internal/project"
	"github.com/spf13/cobra"
)

var (
	recentLimit          int
	recentDirTypes       string
	recentExcludeCurrent bool
	recentJSON           bool
	recentSince          string
	recentPaths          bool
	recentInteractive    bool
	recentNoCopy         bool
)

var recentCmd = &cobra.Command{
	Use:   "recent",
	Short: "Recently active workspace docs and repos (live.db, ranked by mtime)",
	Long: `Surface recently touched .giantmem docs or repos across all live workspaces,
ranked by mtime. Designed for quick cross-repo pickup: jump to the doc you
last edited in another worktree, or pair with the repo you were just in.

Subcommands:
  giantmem recent docs   - recent .giantmem/*.md docs across projects
  giantmem recent repos  - recent worktrees/repos (any layout) by activity`,
}

var recentDocsCmd = &cobra.Command{
	Use:   "docs",
	Short: "List recently modified .giantmem docs across all live workspaces",
	Long: `List the most recently modified .giantmem/*.md docs (live.db),
ordered by mtime DESC. Filter by dir_type (CSV) and recency.

Examples:
  giantmem recent docs
  giantmem recent docs -t research,plans
  giantmem recent docs --exclude-current --since 14d -n 15
  giantmem recent docs --json   # for slash commands`,
	RunE: runRecentDocs,
}

var recentReposCmd = &cobra.Command{
	Use:   "repos",
	Short: "List recently active repos/worktrees (any layout) by mtime",
	Long: `List distinct repos (worktree_path values) ordered by the most recent
mtime of any .giantmem doc inside. Works for both bare-with-worktrees and
plain repos — anything live-indexed shows up.

Examples:
  giantmem recent repos
  giantmem recent repos --exclude-current -n 15
  giantmem recent repos --json`,
	RunE: runRecentRepos,
}

func init() {
	recentCmd.PersistentFlags().IntVarP(&recentLimit, "limit", "n", 10, "max results")
	recentCmd.PersistentFlags().BoolVar(&recentExcludeCurrent, "exclude-current", false, "exclude rows from current repo (cwd)")
	recentCmd.PersistentFlags().BoolVar(&recentJSON, "json", false, "JSON output")
	recentCmd.PersistentFlags().StringVar(&recentSince, "since", "", "only newer than (e.g. 7d, 2h)")

	recentDocsCmd.Flags().StringVarP(&recentDirTypes, "type", "t", "", "comma-sep dir_types (research,plans,reviews,context,features,domains,filebox,history,prompts,root)")
	recentDocsCmd.Flags().BoolVar(&recentPaths, "paths", false, "absolute paths only (one per line)")
	recentDocsCmd.Flags().BoolVarP(&recentInteractive, "interactive", "i", false, "open results in fzf with preview; selected path is copied to clipboard and printed")
	recentDocsCmd.Flags().BoolVar(&recentNoCopy, "no-copy", false, "with --interactive, skip pbcopy (just print selected path)")

	recentCmd.AddCommand(recentDocsCmd)
	recentCmd.AddCommand(recentReposCmd)
	rootCmd.AddCommand(recentCmd)
}

type recentDocRow struct {
	Idx          int    `json:"idx"`
	Path         string `json:"path"`
	Project      string `json:"project"`
	WorktreePath string `json:"worktree_path"`
	Feature      string `json:"feature,omitempty"`
	DirType      string `json:"dir_type"`
	Mtime        int64  `json:"mtime"`
	MtimeISO     string `json:"mtime_iso"`
	AgeHuman     string `json:"age"`
}

type recentRepoRow struct {
	Idx          int    `json:"idx"`
	Project      string `json:"project"`
	WorktreePath string `json:"worktree_path"`
	Mtime        int64  `json:"mtime"`
	MtimeISO     string `json:"mtime_iso"`
	AgeHuman     string `json:"age"`
	DocCount     int    `json:"doc_count"`
}

func runRecentDocs(cmd *cobra.Command, args []string) error {
	live, err := openLiveDB()
	if err != nil {
		return err
	}
	defer live.Close()

	var conds []string
	var params []any

	if t := strings.TrimSpace(recentDirTypes); t != "" {
		parts := splitRecentCSV(t)
		placeholders := make([]string, len(parts))
		for i, p := range parts {
			placeholders[i] = "?"
			params = append(params, p)
		}
		conds = append(conds, "dir_type IN ("+strings.Join(placeholders, ",")+")")
	}

	if since, ok, err := parseSinceUnix(recentSince); err != nil {
		return err
	} else if ok {
		conds = append(conds, "mtime >= ?")
		params = append(params, since)
	}

	if recentExcludeCurrent {
		curRoot := currentWorktreePath()
		if curRoot != "" {
			conds = append(conds, "worktree_path != ?")
			params = append(params, curRoot)
		}
	}

	where := ""
	if len(conds) > 0 {
		where = "WHERE " + strings.Join(conds, " AND ")
	}

	// over-fetch so post-glob filtering still hits the requested limit
	fetchLimit := recentLimit * 5
	if fetchLimit < 50 {
		fetchLimit = 50
	}
	q := fmt.Sprintf(`SELECT path, project, COALESCE(worktree_path,''), COALESCE(feature,''), COALESCE(dir_type,''), mtime
                      FROM live_docs %s ORDER BY mtime DESC LIMIT ?`, where)
	params = append(params, fetchLimit)

	rows, err := live.Query(q, params...)
	if err != nil {
		return err
	}
	defer rows.Close()

	cfg := loadUserConfig()
	ignoreGlobs := cfg.Recent.effectiveIgnoreDocs()

	now := time.Now()
	out := []recentDocRow{}
	idx := 1
	for rows.Next() {
		var r recentDocRow
		if err := rows.Scan(&r.Path, &r.Project, &r.WorktreePath, &r.Feature, &r.DirType, &r.Mtime); err != nil {
			return err
		}
		if matchAnyGlob(r.Path, ignoreGlobs) {
			continue
		}
		r.Idx = idx
		idx++
		r.MtimeISO = time.Unix(r.Mtime, 0).UTC().Format(time.RFC3339)
		r.AgeHuman = humanizeAge(now.Sub(time.Unix(r.Mtime, 0)))
		out = append(out, r)
		if len(out) >= recentLimit {
			break
		}
	}

	if recentInteractive {
		if len(out) == 0 {
			fmt.Fprintln(os.Stderr, "(no recent docs)")
			return nil
		}
		return runRecentDocsFzf(out)
	}

	if recentJSON {
		return jsonStdout(out)
	}
	if recentPaths {
		for _, r := range out {
			fmt.Println(r.Path)
		}
		return nil
	}

	if len(out) == 0 {
		fmt.Fprintln(os.Stderr, "(no recent docs)")
		return nil
	}

	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "#\tAGE\tPROJECT\tDIR_TYPE\tFEATURE\tPATH")
	for _, r := range out {
		feat := r.Feature
		if feat == "" {
			feat = "-"
		}
		dt := r.DirType
		if dt == "" {
			dt = "-"
		}
		display := shortenPath(r.Path, r.WorktreePath)
		fmt.Fprintf(tw, "%d\t%s\t%s\t%s\t%s\t%s\n", r.Idx, r.AgeHuman, r.Project, dt, feat, display)
	}
	return tw.Flush()
}

func runRecentRepos(cmd *cobra.Command, args []string) error {
	live, err := openLiveDB()
	if err != nil {
		return err
	}
	defer live.Close()

	var conds []string
	var params []any

	if since, ok, err := parseSinceUnix(recentSince); err != nil {
		return err
	} else if ok {
		conds = append(conds, "mtime >= ?")
		params = append(params, since)
	}

	if recentExcludeCurrent {
		curRoot := currentWorktreePath()
		if curRoot != "" {
			conds = append(conds, "worktree_path != ?")
			params = append(params, curRoot)
		}
	}

	conds = append(conds, "worktree_path IS NOT NULL", "worktree_path != ''")

	where := "WHERE " + strings.Join(conds, " AND ")

	q := fmt.Sprintf(`SELECT worktree_path, project, MAX(mtime) AS m, COUNT(*) AS c
                      FROM live_docs %s
                      GROUP BY worktree_path
                      ORDER BY m DESC LIMIT ?`, where)
	params = append(params, recentLimit)

	rows, err := live.Query(q, params...)
	if err != nil {
		return err
	}
	defer rows.Close()

	now := time.Now()
	out := []recentRepoRow{}
	idx := 1
	for rows.Next() {
		var r recentRepoRow
		if err := rows.Scan(&r.WorktreePath, &r.Project, &r.Mtime, &r.DocCount); err != nil {
			return err
		}
		r.Idx = idx
		idx++
		r.MtimeISO = time.Unix(r.Mtime, 0).UTC().Format(time.RFC3339)
		r.AgeHuman = humanizeAge(now.Sub(time.Unix(r.Mtime, 0)))
		out = append(out, r)
	}

	if recentJSON {
		return jsonStdout(out)
	}

	if len(out) == 0 {
		fmt.Fprintln(os.Stderr, "(no recent repos)")
		return nil
	}

	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "#\tAGE\tPROJECT\tDOCS\tPATH")
	for _, r := range out {
		fmt.Fprintf(tw, "%d\t%s\t%s\t%d\t%s\n", r.Idx, r.AgeHuman, r.Project, r.DocCount, r.WorktreePath)
	}
	return tw.Flush()
}

func openLiveDB() (*sql.DB, error) {
	p := liveDBPath()
	if _, err := os.Stat(p); err != nil {
		return nil, fmt.Errorf("live.db not found at %s — run `giantmem index live` first", p)
	}
	return db.Open(p)
}

func currentWorktreePath() string {
	cwd, err := os.Getwd()
	if err != nil {
		return ""
	}
	info := project.Detect(cwd, archiveBasePath())
	if info.WorktreePath == "" {
		return ""
	}
	abs, err := filepath.Abs(info.WorktreePath)
	if err != nil {
		return info.WorktreePath
	}
	return abs
}

func splitRecentCSV(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// parseSinceUnix returns (epoch, ok, err). ok=false when input is empty.
func parseSinceUnix(s string) (int64, bool, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, false, nil
	}
	// duration shorthand: 7d, 2h, 30m
	if d, err := parseDurationShort(s); err == nil {
		return time.Now().Add(-d).Unix(), true, nil
	}
	// RFC3339
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t.Unix(), true, nil
	}
	return 0, false, fmt.Errorf("invalid --since %q (use 7d, 2h, 30m, or RFC3339)", s)
}

func parseDurationShort(s string) (time.Duration, error) {
	// support d (days) on top of stdlib
	if strings.HasSuffix(s, "d") {
		n := strings.TrimSuffix(s, "d")
		var days int
		if _, err := fmt.Sscanf(n, "%d", &days); err != nil {
			return 0, err
		}
		return time.Duration(days) * 24 * time.Hour, nil
	}
	return time.ParseDuration(s)
}

func humanizeAge(d time.Duration) string {
	if d < time.Minute {
		return "now"
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	if d < 24*time.Hour {
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	return fmt.Sprintf("%dd", int(d.Hours()/24))
}

func shortenPath(path, worktree string) string {
	if worktree != "" && strings.HasPrefix(path, worktree+"/") {
		return strings.TrimPrefix(path, worktree+"/")
	}
	home, _ := os.UserHomeDir()
	if home != "" && strings.HasPrefix(path, home+"/") {
		return "~/" + strings.TrimPrefix(path, home+"/")
	}
	return path
}

// runRecentDocsFzf pipes rows into fzf with a preview window. Selected path is
// copied to the clipboard (pbcopy on macOS) and printed to stdout.
//
// Preview tries `bat` first, falls back to `cat`. Header documents key bindings.
func runRecentDocsFzf(rows []recentDocRow) error {
	fzf, err := exec.LookPath("fzf")
	if err != nil {
		return fmt.Errorf("fzf not found in PATH (install via `brew install fzf`)")
	}

	var input strings.Builder
	for _, r := range rows {
		feat := r.Feature
		if feat == "" {
			feat = "-"
		}
		dt := r.DirType
		if dt == "" {
			dt = "-"
		}
		display := shortenPath(r.Path, r.WorktreePath)
		label := fmt.Sprintf("%-4s  %-22s  %-10s  %-22s  %s",
			r.AgeHuman, truncateRecent(r.Project, 22), truncateRecent(dt, 10), truncateRecent(feat, 22), display)
		input.WriteString(r.Path + "\t" + label + "\n")
	}

	previewCmd := `bat --color=always --style=numbers --line-range=:200 {1} 2>/dev/null || cat {1}`

	cmd := exec.Command(fzf,
		"--delimiter", "\t",
		"--with-nth", "2",
		"--preview", previewCmd,
		"--preview-window", "right,60%,wrap",
		"--header", "↵ copy path to clipboard · ctrl-c cancel",
		"--ansi",
		"--height", "90%",
		"--layout", "reverse",
	)
	cmd.Stdin = strings.NewReader(input.String())
	cmd.Stderr = os.Stderr
	out, err := cmd.Output()
	if err != nil {
		// fzf returns 130 on cancel — treat as silent exit, not an error
		if ee, ok := err.(*exec.ExitError); ok && ee.ExitCode() == 130 {
			return nil
		}
		return err
	}
	line := strings.TrimSpace(string(out))
	if line == "" {
		return nil
	}
	fields := strings.SplitN(line, "\t", 2)
	path := fields[0]
	fmt.Println(path)
	if !recentNoCopy {
		if err := copyToClipboard(path); err != nil {
			fmt.Fprintf(os.Stderr, "warning: clipboard copy failed: %v\n", err)
		} else {
			fmt.Fprintln(os.Stderr, "copied to clipboard")
		}
	}
	return nil
}

func copyToClipboard(s string) error {
	candidates := [][]string{
		{"pbcopy"},
		{"wl-copy"},
		{"xclip", "-selection", "clipboard"},
		{"xsel", "--clipboard", "--input"},
	}
	for _, c := range candidates {
		if _, err := exec.LookPath(c[0]); err != nil {
			continue
		}
		cmd := exec.Command(c[0], c[1:]...)
		cmd.Stdin = strings.NewReader(s)
		return cmd.Run()
	}
	return fmt.Errorf("no clipboard tool found (pbcopy/wl-copy/xclip/xsel)")
}

func truncateRecent(s string, n int) string {
	if len(s) <= n {
		return s
	}
	if n <= 1 {
		return s[:n]
	}
	return s[:n-1] + "…"
}

func jsonStdout(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// userConfig maps to ~/.config/giantmem/config.toml. Umbrella for general
// user prefs; today only [recent] is honored.
type userConfig struct {
	Recent recentConfig `toml:"recent"`
}

type recentConfig struct {
	// IgnoreDocs is a list of glob patterns. Matching docs are dropped from
	// `giantmem recent docs`. Pattern semantics:
	//   - if pattern has no "/": match against basename via filepath.Match
	//     (e.g. "*notes.md" matches any file ending in notes.md)
	//   - else: match against the path's tail segments via filepath.Match
	//     (e.g. "*/features/_index.md" matches "<anyseg>/features/_index.md"
	//     anywhere in the path)
	IgnoreDocs []string `toml:"ignore_docs"`

	// AppendIgnoreDocs adds to defaults instead of replacing. If IgnoreDocs
	// is set, it overrides defaults entirely; AppendIgnoreDocs is additive.
	AppendIgnoreDocs []string `toml:"append_ignore_docs"`
}

var defaultRecentIgnoreDocs = []string{
	"*/features/_index.md",
}

func (rc recentConfig) effectiveIgnoreDocs() []string {
	if len(rc.IgnoreDocs) > 0 {
		return append(append([]string{}, rc.IgnoreDocs...), rc.AppendIgnoreDocs...)
	}
	return append(append([]string{}, defaultRecentIgnoreDocs...), rc.AppendIgnoreDocs...)
}

// userConfigPath returns the path to config.toml. Honors XDG_CONFIG_HOME, falls
// back to ~/.config/giantmem/config.toml.
func userConfigPath() string {
	if x := os.Getenv("XDG_CONFIG_HOME"); x != "" {
		return filepath.Join(x, "giantmem", "config.toml")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "giantmem", "config.toml")
}

func loadUserConfig() userConfig {
	var c userConfig
	p := userConfigPath()
	if _, err := os.Stat(p); err != nil {
		return c
	}
	if _, err := toml.DecodeFile(p, &c); err != nil {
		fmt.Fprintf(os.Stderr, "warning: failed to parse %s: %v\n", p, err)
	}
	return c
}

// matchAnyGlob returns true if path matches any pattern. See recentConfig.IgnoreDocs.
func matchAnyGlob(path string, patterns []string) bool {
	if len(patterns) == 0 {
		return false
	}
	base := filepath.Base(path)
	segs := strings.Split(path, "/")
	for _, p := range patterns {
		if p == "" {
			continue
		}
		if !strings.Contains(p, "/") {
			if ok, _ := filepath.Match(p, base); ok {
				return true
			}
			continue
		}
		if ok, _ := filepath.Match(p, path); ok {
			return true
		}
		pSegs := strings.Split(p, "/")
		if len(pSegs) > len(segs) {
			continue
		}
		tail := strings.Join(segs[len(segs)-len(pSegs):], "/")
		if ok, _ := filepath.Match(p, tail); ok {
			return true
		}
	}
	return false
}

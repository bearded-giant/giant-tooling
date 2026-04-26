package cmd

import (
	"database/sql"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/bryangrimes/gm/internal/db"
	"github.com/bryangrimes/gm/internal/output"
	"github.com/spf13/cobra"
)

var (
	findProject     string
	findDirType     string
	findSourceType  string
	findFeature     string
	findLatest      bool
	findLimit       int
	findJSON        bool
	findPaths       bool
	findFull        bool
	findLiveOnly    bool
	findArchOnly    bool
	findSince       string
	findUntil       string
	findInteractive bool
	findOpenEditor  bool
)

var findCmd = &cobra.Command{
	Use:   "find <query>",
	Short: "Search live + archived workspace docs and sessions (FTS5)",
	Long: `Search live workspace docs (live.db) and archived docs + Claude session
transcripts (archives.db). Default queries both and merges by score.

Examples:
  gm find "jwt"
  gm find "hub and spoke" -p chat-orchestrator-wt
  gm find "auth" -t plans -l
  gm find "migration" -s session            # session transcripts only
  gm find "scratch" --live                  # only live workspaces
  gm find "v1" --archive                    # only archives
  gm find "feature x" -f better-search       # by feature name
  gm find "x" --paths | xargs $EDITOR
  gm find "x" --json`,
	Args: cobra.MinimumNArgs(1),
	RunE: runFind,
}

func init() {
	findCmd.Flags().StringVarP(&findProject, "project", "p", "", "filter by project (LIKE)")
	findCmd.Flags().StringVarP(&findDirType, "type", "t", "", "filter by dir_type")
	findCmd.Flags().StringVarP(&findSourceType, "source", "s", "", "filter by source_type (workspace|session|domain|live)")
	findCmd.Flags().StringVarP(&findFeature, "feature", "f", "", "filter by feature name (live.db)")
	findCmd.Flags().BoolVarP(&findLatest, "latest", "l", false, "archive: only latest per project")
	findCmd.Flags().IntVarP(&findLimit, "limit", "n", 20, "max results")
	findCmd.Flags().BoolVar(&findJSON, "json", false, "JSON output")
	findCmd.Flags().BoolVar(&findPaths, "paths", false, "print absolute paths only")
	findCmd.Flags().BoolVar(&findFull, "full", false, "include matching content snippet")
	findCmd.Flags().BoolVar(&findLiveOnly, "live", false, "search only live.db")
	findCmd.Flags().BoolVar(&findArchOnly, "archive", false, "search only archives.db")
	findCmd.Flags().StringVar(&findSince, "since", "", `only docs newer than (e.g. "7d", "2h", RFC3339)`)
	findCmd.Flags().StringVar(&findUntil, "until", "", `only docs older than (e.g. "1d", RFC3339)`)
	findCmd.Flags().BoolVarP(&findInteractive, "interactive", "i", false, "fzf+bat picker; on select prints path or opens with -o")
	findCmd.Flags().BoolVarP(&findOpenEditor, "open", "o", false, "with -i: open selected hit in $EDITOR (default: print path)")
}

type hit struct {
	Score      float64 `json:"score"`
	Source     string  `json:"source"` // "live" or "archive"
	Project    string  `json:"project"`
	Timestamp  string  `json:"timestamp,omitempty"`
	DirType    string  `json:"dir_type,omitempty"`
	Feature    string  `json:"feature,omitempty"`
	Filepath   string  `json:"filepath"`
	Filename   string  `json:"filename"`
	IsLatest   bool    `json:"is_latest,omitempty"`
	SourceType string  `json:"source_type,omitempty"`
	SessionID  string  `json:"session_id,omitempty"`
	Cwd        string  `json:"cwd,omitempty"`
	Topic      string  `json:"topic,omitempty"`
	Snippet    string  `json:"snippet,omitempty"`
}

func runFind(cmd *cobra.Command, args []string) error {
	query := strings.Join(args, " ")
	scope := "both"
	if findLiveOnly {
		scope = "live"
	}
	if findArchOnly {
		scope = "archive"
	}
	// session source filter implicitly disables live
	if findSourceType == "session" {
		scope = "archive"
	}

	var hits []hit

	if scope == "live" || scope == "both" {
		if h, err := queryLive(query); err == nil {
			hits = append(hits, h...)
		} else if scope == "live" {
			return err
		}
	}
	if scope == "archive" || scope == "both" {
		if h, err := queryArchive(query); err == nil {
			hits = append(hits, h...)
		} else if scope == "archive" {
			return err
		}
	}

	sort.SliceStable(hits, func(i, j int) bool { return hits[i].Score < hits[j].Score })
	if len(hits) > findLimit {
		hits = hits[:findLimit]
	}

	if len(hits) == 0 {
		fmt.Fprintln(os.Stderr, "no results")
		return nil
	}

	switch {
	case findInteractive:
		return interactivePick(hits, query, findOpenEditor)
	case findJSON:
		return output.JSON(hits)
	case findPaths:
		for _, h := range hits {
			fmt.Println(h.Filepath)
		}
	default:
		printHits(hits, findFull)
	}
	return nil
}

// parseSinceUntil accepts duration ("7d", "2h", "30m") or RFC3339 timestamp.
// duration mode subtracts (since=true) or adds the duration from now.
func parseSinceUntil(s string, _ bool) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}
	dur, err := parseDuration(s)
	if err != nil {
		return time.Time{}, fmt.Errorf("bad time spec %q: must be duration like 7d/2h or RFC3339", s)
	}
	return time.Now().Add(-dur), nil
}

// interactivePick fans hits into fzf with a bat preview.
func interactivePick(hits []hit, query string, openEditor bool) error {
	fzf, err := exec.LookPath("fzf")
	if err != nil {
		return fmt.Errorf("fzf not found in PATH (brew install fzf)")
	}

	var input strings.Builder
	for _, h := range hits {
		marker := h.SourceType
		if h.DirType != "" && h.DirType != "root" {
			marker = h.SourceType + "/" + h.DirType
		}
		ts := h.Timestamp
		if h.Source == "live" {
			ts = "live"
			marker = "live"
			if h.Feature != "" {
				marker = "live/" + h.Feature
			} else if h.DirType != "" {
				marker = "live/" + h.DirType
			}
		}
		display := fmt.Sprintf("[%6.2f] %-30s %s", h.Score, h.Project+"/"+ts, marker)
		// path TAB display
		input.WriteString(h.Filepath)
		input.WriteString("\t")
		input.WriteString(display)
		input.WriteString("\n")
	}

	preview := fmt.Sprintf(
		"file=$(echo {} | cut -f1); "+
			"if command -v bat >/dev/null; then "+
			"bat --color=always --style=numbers --line-range=:200 \"$file\" 2>/dev/null; "+
			"else sed -n '1,120p' \"$file\"; fi")

	cmd := exec.Command(fzf,
		"--ansi",
		"--delimiter", "\t",
		"--with-nth", "2",
		"--preview", preview,
		"--preview-window", "right:60%:wrap",
		"--header", "query: "+query+" | enter: select | esc: cancel",
		"--bind", "ctrl-u:preview-half-page-up,ctrl-d:preview-half-page-down",
	)
	cmd.Stdin = strings.NewReader(input.String())
	cmd.Stderr = os.Stderr
	out, err := cmd.Output()
	if err != nil {
		// 130 = user cancelled
		if ee, ok := err.(*exec.ExitError); ok && ee.ExitCode() == 130 {
			return nil
		}
		return err
	}
	line := strings.TrimSpace(string(out))
	if line == "" {
		return nil
	}
	path := strings.SplitN(line, "\t", 2)[0]
	if openEditor {
		ed := os.Getenv("EDITOR")
		if ed == "" {
			ed = "vi"
		}
		c := exec.Command(ed, path)
		c.Stdin = os.Stdin
		c.Stdout = os.Stdout
		c.Stderr = os.Stderr
		return c.Run()
	}
	fmt.Println(path)
	return nil
}

func queryLive(query string) ([]hit, error) {
	if _, err := os.Stat(liveDBPath()); err != nil {
		return nil, nil
	}
	d, err := db.Open(liveDBPath())
	if err != nil {
		return nil, err
	}
	defer d.Close()

	conds := []string{"live_docs_fts MATCH ?"}
	qargs := []any{query}
	if findProject != "" {
		conds = append(conds, "ld.project LIKE ?")
		qargs = append(qargs, "%"+findProject+"%")
	}
	if findDirType != "" {
		conds = append(conds, "ld.dir_type = ?")
		qargs = append(qargs, findDirType)
	}
	if findFeature != "" {
		conds = append(conds, "ld.feature = ?")
		qargs = append(qargs, findFeature)
	}
	if findSince != "" {
		t, err := parseSinceUntil(findSince, true)
		if err != nil {
			return nil, err
		}
		conds = append(conds, "ld.mtime >= ?")
		qargs = append(qargs, t.Unix())
	}
	if findUntil != "" {
		t, err := parseSinceUntil(findUntil, false)
		if err != nil {
			return nil, err
		}
		conds = append(conds, "ld.mtime < ?")
		qargs = append(qargs, t.Unix())
	}
	snippet := "''"
	if findFull {
		snippet = "snippet(live_docs_fts, 4, '<', '>', '...', 12)"
	}
	q := fmt.Sprintf(`
        SELECT bm25(live_docs_fts), ld.project, COALESCE(ld.dir_type,''),
               COALESCE(ld.feature,''), ld.path, COALESCE(ld.session_id,''),
               %s
          FROM live_docs_fts
          JOIN live_docs ld ON ld.rowid = live_docs_fts.rowid
         WHERE %s
         ORDER BY bm25(live_docs_fts)
         LIMIT ?`, snippet, strings.Join(conds, " AND "))
	qargs = append(qargs, findLimit)

	rows, err := d.Query(q, qargs...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []hit
	for rows.Next() {
		var h hit
		if err := rows.Scan(&h.Score, &h.Project, &h.DirType, &h.Feature, &h.Filepath, &h.SessionID, &h.Snippet); err != nil {
			return nil, err
		}
		h.Source = "live"
		h.SourceType = "live"
		out = append(out, h)
	}
	return out, rows.Err()
}

func queryArchive(query string) ([]hit, error) {
	if _, err := os.Stat(archiveDBPath()); err != nil {
		return nil, nil
	}
	d, err := db.Open(archiveDBPath())
	if err != nil {
		return nil, err
	}
	defer d.Close()
	if err := db.EnsureArchive(d); err != nil {
		return nil, err
	}

	conds := []string{"documents_fts MATCH ?"}
	qargs := []any{query}
	if findProject != "" {
		conds = append(conds, "d.project LIKE ?")
		qargs = append(qargs, "%"+findProject+"%")
	}
	if findDirType != "" {
		conds = append(conds, "d.dir_type = ?")
		qargs = append(qargs, findDirType)
	}
	if findSourceType != "" {
		conds = append(conds, "d.source_type = ?")
		qargs = append(qargs, findSourceType)
	}
	if findLatest {
		conds = append(conds, "d.is_latest = 1")
	}
	if findSince != "" {
		t, err := parseSinceUntil(findSince, true)
		if err != nil {
			return nil, err
		}
		conds = append(conds, "d.timestamp >= ?")
		qargs = append(qargs, t.Format("20060102_150405"))
	}
	if findUntil != "" {
		t, err := parseSinceUntil(findUntil, false)
		if err != nil {
			return nil, err
		}
		conds = append(conds, "d.timestamp < ?")
		qargs = append(qargs, t.Format("20060102_150405"))
	}

	snippet := "''"
	if findFull {
		snippet = "snippet(documents_fts, 0, '<', '>', '...', 12)"
	}

	q := fmt.Sprintf(`
        SELECT bm25(documents_fts), d.project, d.timestamp, COALESCE(d.dir_type,''),
               d.filepath, d.filename, d.is_latest, d.source_type,
               COALESCE(d.session_id,''), COALESCE(d.cwd,''), COALESCE(d.topic,''),
               %s
          FROM documents_fts
          JOIN documents d ON d.id = documents_fts.rowid
         WHERE %s
         ORDER BY bm25(documents_fts)
         LIMIT ?`, snippet, strings.Join(conds, " AND "))
	qargs = append(qargs, findLimit)

	rows, err := d.Query(q, qargs...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []hit
	for rows.Next() {
		var h hit
		var isLatest int
		if err := rows.Scan(&h.Score, &h.Project, &h.Timestamp, &h.DirType, &h.Filepath,
			&h.Filename, &isLatest, &h.SourceType, &h.SessionID, &h.Cwd, &h.Topic, &h.Snippet); err != nil {
			return nil, err
		}
		h.Source = "archive"
		h.IsLatest = isLatest == 1
		out = append(out, h)
	}
	return out, rows.Err()
}

func printHits(hits []hit, full bool) {
	for _, h := range hits {
		var marker string
		switch h.Source {
		case "live":
			marker = "live"
			if h.Feature != "" {
				marker = "live/" + h.Feature
			} else if h.DirType != "" {
				marker = "live/" + h.DirType
			}
		default:
			marker = h.SourceType
			if h.DirType != "" && h.DirType != "root" {
				marker = h.SourceType + "/" + h.DirType
			}
			if h.IsLatest {
				marker += " (latest)"
			}
		}
		ts := h.Timestamp
		if h.Source == "live" {
			ts = "live"
		}
		fmt.Printf("[%6.2f] %s/%s %s\n", h.Score, h.Project, ts, marker)
		fmt.Printf("        %s\n", h.Filepath)
		if full && h.Snippet != "" {
			fmt.Printf("        %s\n", strings.ReplaceAll(h.Snippet, "\n", " "))
		}
	}
}

// keep db reference compile-clean for future helpers
var _ = (*sql.DB)(nil)

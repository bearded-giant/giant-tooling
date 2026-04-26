package cmd

import (
	"bufio"
	"bytes"
	"database/sql"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/bryangrimes/gm/internal/daemon"
	"github.com/bryangrimes/gm/internal/db"
	"github.com/bryangrimes/gm/internal/output"
	"github.com/bryangrimes/gm/internal/search"
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
	findNoDaemon    bool
)

var findCmd = &cobra.Command{
	Use:   "find <query>",
	Short: "Search live + archived workspace docs and sessions (FTS5)",
	Long: `Search live workspace docs (live.db) and archived docs + Claude session
transcripts (archives.db). Default queries both and merges by score.

If giantmemd is running and reachable, the search runs over the daemon's
already-open DB handles (sub-millisecond after connect). Otherwise the CLI
opens the DBs directly. Pass --no-daemon to force direct mode.`,
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
	findCmd.Flags().BoolVarP(&findInteractive, "interactive", "i", false, "fzf picker over per-match line snippets; on select prints path:line or opens with -o")
	findCmd.Flags().BoolVarP(&findOpenEditor, "open", "o", false, "with -i: open selected hit in $EDITOR at matched line (default: print path:line)")
	findCmd.Flags().BoolVar(&findNoDaemon, "no-daemon", false, "skip giantmemd; open DBs directly")
}

func runFind(cmd *cobra.Command, args []string) error {
	query := strings.Join(args, " ")
	params := search.Params{
		Query:       query,
		Project:     findProject,
		DirType:     findDirType,
		SourceType:  findSourceType,
		Feature:     findFeature,
		Latest:      findLatest,
		LiveOnly:    findLiveOnly,
		ArchiveOnly: findArchOnly,
		Since:       findSince,
		Until:       findUntil,
		Limit:       findLimit,
		IncludeFull: findFull,
	}

	hits, err := dispatchFind(params)
	if err != nil {
		return err
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

// dispatchFind tries the daemon first (when reachable) and falls back to a
// direct DB open. Schema-drift errors trip the fallback automatically.
func dispatchFind(p search.Params) ([]search.Hit, error) {
	if !findNoDaemon && os.Getenv("GIANTMEM_NO_DAEMON") == "" {
		sock := daemon.DefaultSocketPath()
		if daemon.SocketAlive(sock, 250*time.Millisecond) {
			cli := daemon.NewClient(sock, 5*time.Second)
			var out struct {
				Hits []search.Hit `json:"hits"`
			}
			err := cli.Call("find", &daemon.FindParams{
				Query:       p.Query,
				Project:     p.Project,
				DirType:     p.DirType,
				SourceType:  p.SourceType,
				Feature:     p.Feature,
				Latest:      p.Latest,
				LiveOnly:    p.LiveOnly,
				ArchiveOnly: p.ArchiveOnly,
				Since:       p.Since,
				Until:       p.Until,
				Limit:       p.Limit,
				IncludeFull: p.IncludeFull,
			}, &out)
			if err == nil {
				return out.Hits, nil
			}
			if !daemon.IsSchemaDrift(err) {
				fmt.Fprintf(os.Stderr, "daemon error, falling back: %v\n", err)
			}
		}
	}
	return findDirect(p)
}

func findDirect(p search.Params) ([]search.Hit, error) {
	var archive, live *sql.DB
	if _, err := os.Stat(archiveDBPath()); err == nil {
		if d, err := db.Open(archiveDBPath()); err == nil {
			archive = d
			defer archive.Close()
		}
	}
	if _, err := os.Stat(liveDBPath()); err == nil {
		if d, err := db.Open(liveDBPath()); err == nil {
			live = d
			defer live.Close()
		}
	}
	return search.Run(archive, live, p)
}

// matchRow is a per-line match found by ripgrep inside a hit's file.
type matchRow struct {
	Hit  search.Hit
	Line int
	Text string
}

// interactivePick expands hits to per-match line rows via ripgrep and feeds
// them into fzf with a line-aware preview. Falls back to file-level picker
// when ripgrep finds no literal matches (e.g. FTS5 stemming, prefix matches).
func interactivePick(hits []search.Hit, query string, openEditor bool) error {
	if _, err := exec.LookPath("fzf"); err != nil {
		return fmt.Errorf("fzf not found in PATH (brew install fzf)")
	}

	rows, rgErr := expandHitsToMatches(hits, query)
	if rgErr == nil && len(rows) > 0 {
		return fzfPickMatches(rows, query, openEditor)
	}
	if rgErr != nil {
		fmt.Fprintf(os.Stderr, "match expansion unavailable, falling back to file-level picker: %v\n", rgErr)
	}
	return fzfPickFiles(hits, query, openEditor)
}

// expandHitsToMatches runs ripgrep over each hit's filepath and emits one
// matchRow per matched line. Tokenizes the FTS query into literal words and
// passes them as -F -e patterns so the snippet matches what the user typed.
func expandHitsToMatches(hits []search.Hit, query string) ([]matchRow, error) {
	rg, err := exec.LookPath("rg")
	if err != nil {
		return nil, fmt.Errorf("rg not found (brew install ripgrep)")
	}
	tokens := tokenizeFTSQuery(query)
	if len(tokens) == 0 {
		return nil, fmt.Errorf("query has no searchable tokens")
	}

	baseArgs := []string{
		"-n", "-i",
		"--no-heading", "--no-filename",
		"--color=never",
		"--max-columns=4000",
		"-F",
	}
	for _, t := range tokens {
		baseArgs = append(baseArgs, "-e", t)
	}

	var rows []matchRow
	seen := map[string]bool{}
	for _, h := range hits {
		key := h.Filepath
		if seen[key] {
			continue
		}
		seen[key] = true

		args := append(append([]string{}, baseArgs...), h.Filepath)
		out, _ := exec.Command(rg, args...).Output()
		sc := bufio.NewScanner(bytes.NewReader(out))
		sc.Buffer(make([]byte, 0, 1<<20), 16<<20)
		for sc.Scan() {
			ln := sc.Text()
			colon := strings.IndexByte(ln, ':')
			if colon < 0 {
				continue
			}
			num, err := strconv.Atoi(ln[:colon])
			if err != nil {
				continue
			}
			text := strings.TrimSpace(ln[colon+1:])
			if len(text) > 240 {
				text = text[:240] + "…"
			}
			rows = append(rows, matchRow{Hit: h, Line: num, Text: text})
		}
	}
	return rows, nil
}

var ftsTokenRE = regexp.MustCompile(`[\p{L}\p{N}_]+`)

// tokenizeFTSQuery extracts literal word tokens from an FTS5 query string,
// stripping operators (AND/OR/NOT/NEAR), quotes, and column qualifiers so the
// remaining words can be passed to ripgrep as -F patterns.
func tokenizeFTSQuery(q string) []string {
	words := ftsTokenRE.FindAllString(q, -1)
	out := make([]string, 0, len(words))
	for _, w := range words {
		switch strings.ToUpper(w) {
		case "AND", "OR", "NOT", "NEAR":
			continue
		}
		if len(w) < 2 {
			continue
		}
		out = append(out, w)
	}
	return out
}

// fzfPickMatches renders one row per matched line and opens the selection at
// that line on Enter. Display column shows score + project/ts + marker + line
// + truncated match text. Preview window pivots on file extension: jsonl gets
// jq-decoded surrounding lines, everything else gets bat with highlight-line.
func fzfPickMatches(rows []matchRow, query string, openEditor bool) error {
	fzf, _ := exec.LookPath("fzf")
	var input strings.Builder
	for _, r := range rows {
		marker := r.Hit.SourceType
		if r.Hit.DirType != "" && r.Hit.DirType != "root" {
			marker = r.Hit.SourceType + "/" + r.Hit.DirType
		}
		ts := r.Hit.Timestamp
		if r.Hit.Source == "live" {
			ts = "live"
			marker = "live"
			if r.Hit.Feature != "" {
				marker = "live/" + r.Hit.Feature
			} else if r.Hit.DirType != "" {
				marker = "live/" + r.Hit.DirType
			}
		}
		display := fmt.Sprintf("[%6.2f] %-30s %-22s :%-6d  %s",
			r.Hit.Score, r.Hit.Project+"/"+ts, marker, r.Line, r.Text)
		input.WriteString(r.Hit.Filepath)
		input.WriteString("\t")
		input.WriteString(strconv.Itoa(r.Line))
		input.WriteString("\t")
		input.WriteString(display)
		input.WriteString("\n")
	}

	cmd := exec.Command(fzf,
		"--ansi",
		"--delimiter", "\t",
		"--with-nth", "3",
		"--preview", matchPreviewScript(),
		"--preview-window", "right:60%:wrap",
		"--header", "query: "+query+" | enter: open at line | esc: cancel",
		"--bind", "ctrl-u:preview-half-page-up,ctrl-d:preview-half-page-down",
	)
	cmd.Stdin = strings.NewReader(input.String())
	cmd.Stderr = os.Stderr
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok && ee.ExitCode() == 130 {
			return nil
		}
		return err
	}
	sel := strings.TrimSpace(string(out))
	if sel == "" {
		return nil
	}
	parts := strings.SplitN(sel, "\t", 3)
	if len(parts) < 2 {
		return nil
	}
	path, lno := parts[0], parts[1]
	if openEditor {
		return openInEditor(path, lno)
	}
	fmt.Printf("%s:%s\n", path, lno)
	return nil
}

// fzfPickFiles is the legacy file-level picker, kept as fallback when rg
// returns no per-line hits (FTS5 stemming, prefix matches, missing rg).
func fzfPickFiles(hits []search.Hit, query string, openEditor bool) error {
	fzf, _ := exec.LookPath("fzf")
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
		input.WriteString(h.Filepath)
		input.WriteString("\t")
		input.WriteString(display)
		input.WriteString("\n")
	}

	preview := "file={1}; " +
		"if command -v bat >/dev/null 2>&1; then " +
		"bat --color=always --style=numbers --line-range=:200 \"$file\" 2>/dev/null; " +
		"else sed -n '1,120p' \"$file\"; fi"

	cmd := exec.Command(fzf,
		"--ansi",
		"--delimiter", "\t",
		"--with-nth", "2",
		"--preview", preview,
		"--preview-window", "right:60%:wrap",
		"--header", "query: "+query+" | enter: select | esc: cancel (no per-line matches)",
		"--bind", "ctrl-u:preview-half-page-up,ctrl-d:preview-half-page-down",
	)
	cmd.Stdin = strings.NewReader(input.String())
	cmd.Stderr = os.Stderr
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok && ee.ExitCode() == 130 {
			return nil
		}
		return err
	}
	sel := strings.TrimSpace(string(out))
	if sel == "" {
		return nil
	}
	path := strings.SplitN(sel, "\t", 2)[0]
	if openEditor {
		return openInEditor(path, "1")
	}
	fmt.Println(path)
	return nil
}

// matchPreviewScript is the fzf preview command. {1}=path, {2}=line. For
// .jsonl session transcripts we decode 2 lines around the match through jq
// (role/type + content text); for everything else we use bat with a window
// centered on the matched line and the line highlighted.
func matchPreviewScript() string {
	return `
file={1}
line={2}
ext="${file##*.}"
if [ "$ext" = "jsonl" ]; then
  start=$((line - 2)); [ $start -lt 1 ] && start=1
  end=$((line + 2))
  if command -v jq >/dev/null 2>&1; then
    awk -v s=$start -v e=$end 'NR>=s && NR<=e {printf "%d\t%s\n", NR, $0}' "$file" \
      | while IFS=$'\t' read -r n json; do
          marker="  "
          [ "$n" = "$line" ] && marker="▶ "
          printf "%s── line %s ──\n" "$marker" "$n"
          printf '%s\n' "$json" | jq -r '
            def shorten(n): if (tostring | length) > n then (tostring)[0:n] + "…" else tostring end;
            if (.message.role // null) then "  [\(.message.role)] \((.message.content // "" | tostring | .[0:3000]))"
            elif (.type // null)        then "  [\(.type)] \(((.content // .summary // .text // "") | tostring | .[0:3000]))"
            else (. | tostring | .[0:3000]) end
          ' 2>/dev/null || printf '  %s\n' "$(printf '%s' "$json" | cut -c1-3000)"
          echo
        done
  else
    awk -v s=$start -v e=$end -v hi=$line 'NR>=s && NR<=e {
      m = (NR==hi) ? "▶ " : "  "
      print m "── line " NR " ──"
      print "  " substr($0, 1, 3000)
      print ""
    }' "$file"
  fi
else
  s=$((line - 12)); [ $s -lt 1 ] && s=1
  e=$((line + 50))
  if command -v bat >/dev/null 2>&1; then
    bat --color=always --style=numbers --highlight-line "$line" --line-range "$s:$e" "$file"
  else
    awk -v s=$s -v e=$e -v hi=$line 'NR>=s && NR<=e {
      m = (NR==hi) ? "▶ " : "  "
      printf "%s%5d  %s\n", m, NR, $0
    }' "$file"
  fi
fi
`
}

// openInEditor launches $EDITOR at the given line. Uses VS Code's -g
// path:line syntax when EDITOR is code/codium/cursor, otherwise the +N path
// convention (vi/vim/nvim/nano/emacs).
func openInEditor(path, line string) error {
	ed := os.Getenv("EDITOR")
	if ed == "" {
		ed = "vi"
	}
	base := strings.ToLower(filepath.Base(ed))
	var c *exec.Cmd
	if strings.Contains(base, "code") || strings.Contains(base, "cursor") {
		c = exec.Command(ed, "-g", path+":"+line)
	} else {
		c = exec.Command(ed, "+"+line, path)
	}
	c.Stdin = os.Stdin
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	return c.Run()
}

func printHits(hits []search.Hit, full bool) {
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

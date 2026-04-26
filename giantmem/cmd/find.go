package cmd

import (
	"bufio"
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/bearded-giant/giant-tooling/giantmem/internal/daemon"
	"github.com/bearded-giant/giant-tooling/giantmem/internal/db"
	"github.com/bearded-giant/giant-tooling/giantmem/internal/output"
	"github.com/bearded-giant/giant-tooling/giantmem/internal/search"
	"github.com/spf13/cobra"
)

var (
	findProject       string
	findDirType       string
	findSourceType    string
	findFeature       string
	findLatest        bool
	findLimit         int
	findJSON          bool
	findPaths         bool
	findFull          bool
	findLiveOnly      bool
	findArchOnly      bool
	findSince         string
	findUntil         string
	findNoInteractive bool
	findOpenEditor    bool
	findNoDaemon      bool
	findTools         []string
	findExts          []string
	findIncludeRead   bool
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
	findCmd.Flags().StringVarP(&findSourceType, "source", "s", "", "archives.db source_type filter: workspace | session | domain (use --live to scope to live workspaces)")
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
	findCmd.Flags().BoolVarP(&findNoInteractive, "no-interactive", "i", false, "disable fzf picker (script mode); auto-disabled when stdout is not a TTY or when --json/--paths is set")
	findCmd.Flags().BoolVarP(&findOpenEditor, "open", "o", false, "in interactive mode: open selected hit in $EDITOR at matched line (default: print path:line)")
	findCmd.Flags().BoolVar(&findNoDaemon, "no-daemon", false, "skip giantmemd; open DBs directly")
	findCmd.Flags().StringSliceVar(&findTools, "tool", nil, "session filter: only keep matches on lines where Claude used these tool names (e.g. --tool Write,Edit). Case-insensitive. Repeat or comma-separate.")
	findCmd.Flags().StringSliceVar(&findExts, "ext", nil, "session filter: only keep matches where a tool_use touched a file with these extensions (e.g. --ext md,go). Leading dot optional. Composes with --tool.")
	findCmd.Flags().BoolVar(&findIncludeRead, "include-read", false, "include Claude's Read tool calls in session results (default: hidden because Read is high-volume noise)")
	findCmd.AddCommand(findPreviewCmd)
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

	interactive := shouldRunInteractive()
	switch {
	case findJSON:
		return output.JSON(hits)
	case findPaths:
		for _, h := range hits {
			fmt.Println(h.Filepath)
		}
	case interactive || len(findTools) > 0 || len(findExts) > 0:
		return runMatchPipeline(hits, query, findOpenEditor, interactive)
	default:
		printHits(hits, findFull)
	}
	return nil
}

// runMatchPipeline expands file-level hits to per-line matches via ripgrep,
// applies the --tool filter, then either drops into fzf (interactive TTY) or
// emits plain `path:line  display` rows (script mode). Falls back to the
// file-level picker / printer when match expansion finds nothing.
func runMatchPipeline(hits []search.Hit, query string, openEditor, interactive bool) error {
	if interactive {
		if _, err := exec.LookPath("fzf"); err != nil {
			return fmt.Errorf("fzf not found in PATH (brew install fzf)")
		}
	}

	filters := MatchFilters{
		Tools:       findTools,
		Exts:        findExts,
		IncludeRead: findIncludeRead,
	}
	rows, err := expandHitsToMatches(hits, query, filters)
	if err != nil {
		fmt.Fprintf(os.Stderr, "match expansion unavailable: %v\n", err)
		if interactive {
			return fzfPickFiles(hits, query, openEditor)
		}
		printHits(hits, findFull)
		return nil
	}

	if len(rows) == 0 {
		if len(findTools) > 0 || len(findExts) > 0 {
			fmt.Fprintln(os.Stderr, "no matches survived --tool/--ext filter")
			return nil
		}
		if interactive {
			return fzfPickFiles(hits, query, openEditor)
		}
		printHits(hits, findFull)
		return nil
	}

	if interactive {
		return fzfPickMatches(rows, query, openEditor)
	}
	for _, r := range rows {
		fmt.Printf("%s:%d  %s\n", r.Hit.Filepath, r.Line, r.Display)
	}
	return nil
}

// shouldRunInteractive decides whether the fzf picker should fire. Interactive
// is the default whenever stdout is a real TTY, but explicit --no-interactive
// or non-TTY stdout (pipe, file redirect) flips us to plain text output for
// scripting. --json and --paths short-circuit ahead of this check in runFind.
func shouldRunInteractive() bool {
	if findNoInteractive {
		return false
	}
	fi, err := os.Stdout.Stat()
	if err != nil {
		return false
	}
	return (fi.Mode() & os.ModeCharDevice) != 0
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

// MatchFilters bundles the session-aware post-filters applied during rg
// expansion. Lifted out of CLI globals so the MCP handler can pass its own
// values per-request.
type MatchFilters struct {
	Tools       []string
	Exts        []string
	IncludeRead bool
}

// matchRow is a per-line match found by ripgrep inside a hit's file.
// Display is the human-readable text shown in fzf's list column. For .jsonl
// session transcripts it's the decoded role + content/tool summary; for
// other files it's the raw matched line. Tools is the set of tool_use names
// referenced on this line (only populated for .jsonl), used by --tool filter.
type matchRow struct {
	Hit     search.Hit
	Line    int
	Display string
	Tools   []string
}

// expandHitsToMatches runs ripgrep over each hit's filepath and emits one
// matchRow per matched line.
//
// Three matching strategies, in priority order:
//  1. Quoted-phrase short-circuit. If the user wrapped their whole query in
//     double-quotes (e.g. `"thing that foo thing or whatever"`), match the
//     unwrapped content as a fixed string. No tokenization, no operator
//     dropping. This mirrors how the FTS sanitizer treats `"..."` as a
//     literal phrase.
//  2. Phrase regex with flexible separators (default for unquoted queries).
//     Tokens are joined with `[\W_]+` so `hub-and-spoke` matches `hub and
//     spoke`, `hub_and_spoke`, `hub-and-spoke`, etc.
//  3. Literal per-token OR fallback. If the phrase regex finds nothing, try
//     matching any single token. Catches files surfaced by FTS5 stemming
//     where the literal phrase doesn't appear.
func expandHitsToMatches(hits []search.Hit, query string, f MatchFilters) ([]matchRow, error) {
	rg, err := exec.LookPath("rg")
	if err != nil {
		return nil, fmt.Errorf("rg not found (brew install ripgrep)")
	}

	baseArgs := []string{
		"-n", "-i",
		"--no-heading", "--no-filename",
		"--color=never",
		"--max-columns=4000",
	}

	if phrase, ok := unwrapQuotedPhrase(query); ok {
		args := append(append([]string{}, baseArgs...), "-F", "-e", phrase)
		return runRgOverHits(rg, args, hits, nil, f), nil
	}

	tokens := tokenizeFTSQuery(query)
	if len(tokens) == 0 {
		return nil, fmt.Errorf("query has no searchable tokens")
	}

	phraseArgs := append(append([]string{}, baseArgs...), "-e", buildPhraseRegex(tokens))
	literalArgs := append([]string{}, baseArgs...)
	literalArgs = append(literalArgs, "-F")
	for _, t := range tokens {
		literalArgs = append(literalArgs, "-e", t)
	}

	var fallback []string
	if len(tokens) > 1 {
		fallback = literalArgs
	}
	return runRgOverHits(rg, phraseArgs, hits, fallback, f), nil
}

// runRgOverHits executes rg per unique hit filepath. If `primary` returns no
// rows for a file and `fallback` is non-nil, it retries with `fallback`. For
// .jsonl session transcripts, each matched line is decoded so the fzf list
// shows readable text (role + content + tool calls) instead of raw JSON, and
// the tool names are captured for the --tool filter.
func runRgOverHits(rg string, primary []string, hits []search.Hit, fallback []string, f MatchFilters) []matchRow {
	wantTools := normalizeToolFilter(f.Tools)
	wantExts := normalizeExtFilter(f.Exts)
	includeRead := f.IncludeRead || (wantTools != nil && wantTools["read"])

	collect := func(args []string, h search.Hit) []matchRow {
		cargs := append(append([]string{}, args...), h.Filepath)
		out, _ := exec.Command(rg, cargs...).Output()
		isJSONL := strings.HasSuffix(strings.ToLower(h.Filepath), ".jsonl")

		var rows []matchRow
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
			raw := ln[colon+1:]

			row := matchRow{Hit: h, Line: num}
			if isJSONL {
				summary, ok := decodeSessionLine([]byte(raw))
				if ok {
					if !includeRead {
						summary = summary.WithoutReads()
						if summary.IsEmpty() {
							continue
						}
					}
					row.Tools = summary.Tools
					row.Display = summary.OneLine()
				} else {
					row.Display = truncate(strings.TrimSpace(raw), 240)
				}
				if len(wantTools) > 0 && !hasAnyTool(row.Tools, wantTools) {
					continue
				}
				if len(wantExts) > 0 && !hasAnyExt(summary.Files, wantExts) {
					continue
				}
			} else {
				row.Display = truncate(strings.TrimSpace(raw), 240)
			}
			rows = append(rows, row)
		}
		return rows
	}

	var rows []matchRow
	seen := map[string]bool{}
	for _, h := range hits {
		if seen[h.Filepath] {
			continue
		}
		seen[h.Filepath] = true
		r := collect(primary, h)
		if len(r) == 0 && fallback != nil {
			r = collect(fallback, h)
		}
		rows = append(rows, r...)
	}
	return rows
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func normalizeToolFilter(in []string) map[string]bool {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]bool, len(in))
	for _, t := range in {
		t = strings.TrimSpace(t)
		if t == "" {
			continue
		}
		out[strings.ToLower(t)] = true
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func hasAnyTool(have []string, want map[string]bool) bool {
	for _, t := range have {
		if want[strings.ToLower(t)] {
			return true
		}
	}
	return false
}

// normalizeExtFilter accepts comma-or-repeat input like ["md", ".go", "py"]
// and returns a lowercase-normalized set without leading dots.
func normalizeExtFilter(in []string) map[string]bool {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]bool, len(in))
	for _, e := range in {
		e = strings.TrimSpace(e)
		e = strings.TrimPrefix(e, ".")
		if e == "" {
			continue
		}
		out[strings.ToLower(e)] = true
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// hasAnyExt returns true if any file_path in `paths` ends in one of the
// wanted extensions (case-insensitive, no leading dot in the set).
func hasAnyExt(paths []string, want map[string]bool) bool {
	for _, p := range paths {
		ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(p), "."))
		if ext == "" {
			continue
		}
		if want[ext] {
			return true
		}
	}
	return false
}

// unwrapQuotedPhrase returns the literal phrase if the query is wrapped in a
// matching pair of double-quotes (and the inner text is non-empty). Returns
// "", false otherwise. This lets users escape FTS query parsing entirely:
// `"thing that foo or whatever"` is a literal substring search, not
// tokenized.
func unwrapQuotedPhrase(q string) (string, bool) {
	q = strings.TrimSpace(q)
	if len(q) < 2 || q[0] != '"' || q[len(q)-1] != '"' {
		return "", false
	}
	inner := q[1 : len(q)-1]
	if inner == "" || strings.Contains(inner, `"`) {
		return "", false
	}
	return inner, true
}

// buildPhraseRegex joins tokens with `[\W_]+` so the phrase matches across
// any non-word separators (space, hyphen, underscore, slash, comma, ...).
// Tokens are regex-escaped first because tokenizeFTSQuery may include
// alphanumerics that, while safe today, should not depend on that invariant.
func buildPhraseRegex(tokens []string) string {
	escaped := make([]string, len(tokens))
	for i, t := range tokens {
		escaped[i] = regexp.QuoteMeta(t)
	}
	return strings.Join(escaped, `[\W_]+`)
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
			r.Hit.Score, r.Hit.Project+"/"+ts, marker, r.Line, truncate(r.Display, 240))
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

// matchPreviewScript is the fzf preview command. {1}=path, {2}=line. We just
// shell out to the giantmem binary's hidden `find _preview` subcommand which
// renders cleanly in Go (decodes .jsonl into role + content + tool calls,
// uses bat for everything else). Falls back to a hand-rolled bash window
// only if the binary path can't be resolved.
func matchPreviewScript() string {
	gm, err := os.Executable()
	if err != nil || gm == "" {
		gm = "giantmem"
	}
	extra := ""
	if findIncludeRead || toolFilterIncludesRead(findTools) {
		extra = " --include-read"
	}
	return fmt.Sprintf(`%s find _preview%s {1} {2} 2>/dev/null`, shellQuote(gm), extra)
}

func toolFilterIncludesRead(tools []string) bool {
	for _, t := range tools {
		if strings.EqualFold(strings.TrimSpace(t), "Read") {
			return true
		}
	}
	return false
}

func shellQuote(s string) string {
	if !strings.ContainsAny(s, " \t'\"\\$`") {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

var findPreviewIncludeRead bool

// findPreviewCmd is the hidden subcommand fzf calls per row to render the
// preview pane. Hidden because it's an implementation detail of `-i`. The
// `--include-read` flag mirrors the parent flag and is wired into the fzf
// preview command line so the preview pane matches the list behavior.
var findPreviewCmd = &cobra.Command{
	Use:    "_preview <path> <line>",
	Hidden: true,
	Args:   cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		path := args[0]
		line, err := strconv.Atoi(args[1])
		if err != nil {
			return fmt.Errorf("invalid line %q: %w", args[1], err)
		}
		return renderPreview(os.Stdout, path, line, findPreviewIncludeRead)
	},
}

func init() {
	findPreviewCmd.Flags().BoolVar(&findPreviewIncludeRead, "include-read", false, "include Read tool calls in preview rendering (default: hidden)")
}

// renderPreview writes a focused window of `path` centered on `line` to w.
// .jsonl files get Go-decoded role/content/tool-call rendering; everything
// else delegates to bat (color-highlighted) or a plain awk fallback.
func renderPreview(w io.Writer, path string, line int, includeRead bool) error {
	lower := strings.ToLower(path)
	if strings.HasSuffix(lower, ".jsonl") {
		return renderJSONLPreview(w, path, line, 2, includeRead)
	}
	return renderTextPreview(w, path, line, 12, 50)
}

func renderJSONLPreview(w io.Writer, path string, line, ctx int, includeRead bool) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	start := line - ctx
	if start < 1 {
		start = 1
	}
	end := line + ctx
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1<<20), 64<<20)
	n := 0
	for sc.Scan() {
		n++
		if n < start {
			continue
		}
		if n > end {
			break
		}
		marker := "  "
		if n == line {
			marker = "▶ "
		}
		fmt.Fprintf(w, "%s── line %d ──\n", marker, n)
		summary, ok := decodeSessionLine(sc.Bytes())
		if !ok {
			fmt.Fprintf(w, "  %s\n\n", truncate(sc.Text(), 3000))
			continue
		}
		if !includeRead {
			summary = summary.WithoutReads()
			if summary.IsEmpty() {
				fmt.Fprintf(w, "  (Read suppressed — pass --include-read)\n\n")
				continue
			}
		}
		summary.WriteMultiline(w, "  ", 3000)
		fmt.Fprintln(w)
	}
	return sc.Err()
}

func renderTextPreview(w io.Writer, path string, line, before, after int) error {
	if bat, err := exec.LookPath("bat"); err == nil {
		s := line - before
		if s < 1 {
			s = 1
		}
		e := line + after
		c := exec.Command(bat,
			"--color=always",
			"--style=numbers",
			"--highlight-line", strconv.Itoa(line),
			"--line-range", fmt.Sprintf("%d:%d", s, e),
			path)
		c.Stdout = w
		c.Stderr = w
		return c.Run()
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	s := line - before
	if s < 1 {
		s = 1
	}
	e := line + after
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1<<20), 16<<20)
	n := 0
	for sc.Scan() {
		n++
		if n < s {
			continue
		}
		if n > e {
			break
		}
		marker := "  "
		if n == line {
			marker = "▶ "
		}
		fmt.Fprintf(w, "%s%5d  %s\n", marker, n, sc.Text())
	}
	return sc.Err()
}

// sessionLineSummary is the decoded view of a single Claude Code JSONL line.
type sessionLineSummary struct {
	Role  string   // assistant | user | system | summary | (blank for unknown)
	Type  string   // raw .type field (system, summary, user, assistant, attachment, ...)
	Text  string   // primary readable text (text blocks joined)
	Tools []string // tool_use names referenced on this line
	Files []string // file_paths from Write/Edit/Read tool_use
	Calls []toolCallSummary
}

type toolCallSummary struct {
	Name  string
	Input map[string]any
}

// IsEmpty reports whether the summary carries no content worth displaying.
// Used to drop session lines that became blank after Read-suppression.
func (s sessionLineSummary) IsEmpty() bool {
	return s.Text == "" && len(s.Tools) == 0 && len(s.Calls) == 0
}

// WithoutReads returns a copy with Read tool_use entries stripped from
// Tools, Calls, and Files. Text and other tools are preserved. Used to
// honor the default-hidden-Read behavior; users who want them back pass
// --include-read.
func (s sessionLineSummary) WithoutReads() sessionLineSummary {
	out := s
	out.Tools = filterStrings(s.Tools, func(t string) bool {
		return !strings.EqualFold(t, "Read")
	})
	out.Calls = make([]toolCallSummary, 0, len(s.Calls))
	for _, c := range s.Calls {
		if strings.EqualFold(c.Name, "Read") {
			continue
		}
		out.Calls = append(out.Calls, c)
	}
	out.Files = nil
	for _, c := range out.Calls {
		if fp, _ := c.Input["file_path"].(string); fp != "" {
			out.Files = append(out.Files, fp)
		}
	}
	return out
}

func filterStrings(in []string, keep func(string) bool) []string {
	out := make([]string, 0, len(in))
	for _, s := range in {
		if keep(s) {
			out = append(out, s)
		}
	}
	return out
}

// OneLine returns a compact, truncated summary suitable for the fzf list
// column. Format: `[role] text … [Tool file=...] [Tool ...]`.
func (s sessionLineSummary) OneLine() string {
	var b strings.Builder
	tag := s.Role
	if tag == "" {
		tag = s.Type
	}
	if tag != "" {
		fmt.Fprintf(&b, "[%s] ", tag)
	}
	if s.Text != "" {
		b.WriteString(strings.ReplaceAll(strings.ReplaceAll(s.Text, "\n", " "), "\t", " "))
	}
	for _, c := range s.Calls {
		fmt.Fprintf(&b, " ⟨%s", c.Name)
		switch strings.ToLower(c.Name) {
		case "write", "edit", "read", "multiedit":
			if fp, _ := c.Input["file_path"].(string); fp != "" {
				fmt.Fprintf(&b, " %s", fp)
			}
		case "bash":
			if cmd, _ := c.Input["command"].(string); cmd != "" {
				fmt.Fprintf(&b, " $ %s", cmd)
			}
		case "grep":
			if pat, _ := c.Input["pattern"].(string); pat != "" {
				fmt.Fprintf(&b, " /%s/", pat)
			}
		case "glob":
			if pat, _ := c.Input["pattern"].(string); pat != "" {
				fmt.Fprintf(&b, " %s", pat)
			}
		}
		b.WriteString("⟩")
	}
	return b.String()
}

// WriteMultiline emits a richer multi-line rendering for the preview pane.
// Each tool call gets its own line; Write/Edit blocks include a content
// excerpt so the user can confirm the file body.
func (s sessionLineSummary) WriteMultiline(w io.Writer, indent string, maxText int) {
	tag := s.Role
	if tag == "" {
		tag = s.Type
	}
	if tag != "" {
		fmt.Fprintf(w, "%s[%s]\n", indent, tag)
	}
	if s.Text != "" {
		fmt.Fprintf(w, "%s%s\n", indent, truncate(s.Text, maxText))
	}
	for _, c := range s.Calls {
		fmt.Fprintf(w, "%s⟨%s⟩\n", indent, c.Name)
		switch strings.ToLower(c.Name) {
		case "write", "edit", "multiedit":
			if fp, _ := c.Input["file_path"].(string); fp != "" {
				fmt.Fprintf(w, "%s  file: %s\n", indent, fp)
			}
			if content, _ := c.Input["content"].(string); content != "" {
				fmt.Fprintf(w, "%s  content:\n", indent)
				writeIndented(w, indent+"    ", truncate(content, maxText))
			}
			if ns, _ := c.Input["new_string"].(string); ns != "" {
				fmt.Fprintf(w, "%s  new_string:\n", indent)
				writeIndented(w, indent+"    ", truncate(ns, maxText))
			}
			if os_, _ := c.Input["old_string"].(string); os_ != "" {
				fmt.Fprintf(w, "%s  old_string:\n", indent)
				writeIndented(w, indent+"    ", truncate(os_, maxText/2))
			}
		case "read":
			if fp, _ := c.Input["file_path"].(string); fp != "" {
				fmt.Fprintf(w, "%s  file: %s\n", indent, fp)
			}
		case "bash":
			if cmd, _ := c.Input["command"].(string); cmd != "" {
				fmt.Fprintf(w, "%s  $ %s\n", indent, truncate(cmd, maxText))
			}
		case "grep", "glob":
			if pat, _ := c.Input["pattern"].(string); pat != "" {
				fmt.Fprintf(w, "%s  pattern: %s\n", indent, pat)
			}
		default:
			if data, err := json.Marshal(c.Input); err == nil {
				fmt.Fprintf(w, "%s  input: %s\n", indent, truncate(string(data), maxText))
			}
		}
	}
}

func writeIndented(w io.Writer, indent, s string) {
	for _, line := range strings.Split(s, "\n") {
		fmt.Fprintf(w, "%s%s\n", indent, line)
	}
}

// decodeSessionLine parses a Claude Code JSONL line into a readable summary.
// Returns (zero, false) if the line is not valid JSON. Best-effort: missing
// fields are skipped silently because session schemas vary across Claude
// versions and we'd rather render partial than fail.
func decodeSessionLine(line []byte) (sessionLineSummary, bool) {
	var raw struct {
		Type    string          `json:"type"`
		Summary string          `json:"summary"`
		Content json.RawMessage `json:"content"`
		Message struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		} `json:"message"`
		Attachment struct {
			Type    string `json:"type"`
			Stdout  string `json:"stdout"`
			Content string `json:"content"`
		} `json:"attachment"`
	}
	if err := json.Unmarshal(line, &raw); err != nil {
		return sessionLineSummary{}, false
	}
	s := sessionLineSummary{Role: raw.Message.Role, Type: raw.Type}

	// Inline string content (rare).
	var direct string
	if err := json.Unmarshal(raw.Message.Content, &direct); err == nil && direct != "" {
		s.Text = direct
		return s, true
	}

	// Block array (typical for assistant + user-with-tool-results).
	var blocks []map[string]json.RawMessage
	if err := json.Unmarshal(raw.Message.Content, &blocks); err == nil {
		var parts []string
		for _, b := range blocks {
			var btype string
			_ = json.Unmarshal(b["type"], &btype)
			switch btype {
			case "text":
				var t string
				_ = json.Unmarshal(b["text"], &t)
				if t != "" {
					parts = append(parts, t)
				}
			case "thinking":
				var t string
				_ = json.Unmarshal(b["thinking"], &t)
				if t != "" {
					parts = append(parts, "(thinking) "+t)
				}
			case "tool_use":
				var name string
				_ = json.Unmarshal(b["name"], &name)
				var input map[string]any
				_ = json.Unmarshal(b["input"], &input)
				if input == nil {
					input = map[string]any{}
				}
				s.Tools = append(s.Tools, name)
				s.Calls = append(s.Calls, toolCallSummary{Name: name, Input: input})
				if fp, _ := input["file_path"].(string); fp != "" {
					s.Files = append(s.Files, fp)
				}
			case "tool_result":
				var c string
				if err := json.Unmarshal(b["content"], &c); err == nil && c != "" {
					parts = append(parts, "(result) "+c)
				}
			}
		}
		s.Text = strings.Join(parts, " ")
	}

	// summary lines (compact resume metadata) and attachment hooks.
	if s.Text == "" && raw.Summary != "" {
		s.Text = raw.Summary
	}
	if s.Text == "" && raw.Attachment.Stdout != "" {
		s.Text = raw.Attachment.Stdout
	}
	if s.Text == "" && raw.Attachment.Content != "" {
		s.Text = raw.Attachment.Content
	}

	return s, true
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

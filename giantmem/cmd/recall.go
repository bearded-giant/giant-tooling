package cmd

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/bearded-giant/giant-tooling/giantmem/internal/db"
	"github.com/bearded-giant/giant-tooling/giantmem/internal/search"
	"github.com/spf13/cobra"
)

var (
	recallSince string
	recallJSON  bool
	recallTop   int
)

var recallCmd = &cobra.Command{
	Use:   "recall",
	Short: "Recall-hook telemetry: what got injected into prompts and whether it was used",
}

var recallReportCmd = &cobra.Command{
	Use:   "report",
	Short: "Precision of injected recall lines: injected vs read/edited later in the same session",
	Long: `The UserPromptSubmit hook logs every line it injects to recall_log. This
report joins those rows against each session's transcript (tool_use paths) and
live_docs writes, and counts a line as used when the recalled file was touched
later in that session. A proxy for precision, good enough to compare ranker
changes against each other.`,
	RunE: runRecallReport,
}

func init() {
	recallReportCmd.Flags().StringVar(&recallSince, "since", "30d", `window over recall_log.ts (e.g. "7d", "2026-09-01")`)
	recallReportCmd.Flags().BoolVar(&recallJSON, "json", false, "JSON output")
	recallReportCmd.Flags().IntVar(&recallTop, "top", 10, "rows in the most-used / never-used doc lists")
	recallCmd.AddCommand(recallReportCmd)
	rootCmd.AddCommand(recallCmd)
}

type recallRow struct {
	TS      time.Time
	Session string
	Repo    string
	Rank    int
	Tag     string
	Cur     bool
	Key     string
	Path    string
}

// transcriptLine is one JSONL record; only the timestamp is parsed, the raw
// text is what the path check scans.
type transcriptLine struct {
	ts   time.Time
	text string
}

type recallBucket struct {
	Injected int     `json:"injected"`
	Used     int     `json:"used"`
	Prec     float64 `json:"precision"`
}

func (b *recallBucket) add(used bool) {
	b.Injected++
	if used {
		b.Used++
	}
}

func (b *recallBucket) finish() {
	if b.Injected > 0 {
		b.Prec = float64(b.Used) / float64(b.Injected)
	}
}

type recallDocStat struct {
	Path     string `json:"path"`
	Injected int    `json:"injected"`
	Used     int    `json:"used"`
}

type recallReport struct {
	Since      string                   `json:"since"`
	Sessions   int                      `json:"sessions"`
	NoTranscpt int                      `json:"sessions_without_transcript"`
	Overall    recallBucket             `json:"overall"`
	ByTag      map[string]*recallBucket `json:"by_tag"`
	BySlot     map[string]*recallBucket `json:"by_slot"`
	ByRank     map[string]*recallBucket `json:"by_rank"`
	MostUsed   []recallDocStat          `json:"most_used"`
	NeverUsed  []recallDocStat          `json:"never_used"`
}

func runRecallReport(cmd *cobra.Command, args []string) error {
	since, err := search.ParseSince(recallSince)
	if err != nil {
		return err
	}
	live, err := db.Open(liveDBPath())
	if err != nil {
		return err
	}
	defer live.Close()
	var archive *sql.DB
	if a, err := db.Open(archiveDBPath()); err == nil {
		archive = a
		defer archive.Close()
	}

	rows, err := live.Query(
		`SELECT ts, session_id, repo, rank, tag, cur, key, path FROM recall_log
          WHERE ts >= ? ORDER BY session_id, id`, since.UTC().Format(time.RFC3339))
	if err != nil {
		return err
	}
	bySession := map[string][]recallRow{}
	for rows.Next() {
		var r recallRow
		var ts string
		var cur int
		if err := rows.Scan(&ts, &r.Session, &r.Repo, &r.Rank, &r.Tag, &cur, &r.Key, &r.Path); err != nil {
			rows.Close()
			return err
		}
		r.TS, _ = time.Parse(time.RFC3339, ts)
		r.Cur = cur != 0
		bySession[r.Session] = append(bySession[r.Session], r)
	}
	rows.Close()
	if len(bySession) == 0 {
		fmt.Fprintf(os.Stderr, "no recall_log rows since %s\n", recallSince)
		return nil
	}

	rep := recallReport{
		Since:  recallSince,
		ByTag:  map[string]*recallBucket{},
		BySlot: map[string]*recallBucket{},
		ByRank: map[string]*recallBucket{},
	}
	docs := map[string]*recallDocStat{}
	bucket := func(m map[string]*recallBucket, k string) *recallBucket {
		if m[k] == nil {
			m[k] = &recallBucket{}
		}
		return m[k]
	}

	for sid, recs := range bySession {
		rep.Sessions++
		transcript, ok := sessionTranscript(archive, sid)
		if !ok {
			rep.NoTranscpt++
		}
		writes := sessionWrites(live, sid)
		for _, r := range recs {
			if r.Path == "" {
				continue
			}
			// only touches after the injection count; the session may have
			// written the doc earlier, which is not recall doing its job
			used := writes[r.Path].After(r.TS) || touchedAfter(transcript, r.Path, r.TS)
			rep.Overall.add(used)
			bucket(rep.ByTag, r.Tag).add(used)
			slot := "cross"
			if r.Cur {
				slot = "current"
			}
			bucket(rep.BySlot, slot).add(used)
			bucket(rep.ByRank, fmt.Sprintf("%d", r.Rank)).add(used)
			d := docs[r.Path]
			if d == nil {
				d = &recallDocStat{Path: r.Path}
				docs[r.Path] = d
			}
			d.Injected++
			if used {
				d.Used++
			}
		}
	}
	rep.Overall.finish()
	for _, m := range []map[string]*recallBucket{rep.ByTag, rep.BySlot, rep.ByRank} {
		for _, b := range m {
			b.finish()
		}
	}
	all := make([]recallDocStat, 0, len(docs))
	for _, d := range docs {
		all = append(all, *d)
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].Used != all[j].Used {
			return all[i].Used > all[j].Used
		}
		return all[i].Injected > all[j].Injected
	})
	for _, d := range all {
		if d.Used > 0 && len(rep.MostUsed) < recallTop {
			rep.MostUsed = append(rep.MostUsed, d)
		}
	}
	sort.Slice(all, func(i, j int) bool { return all[i].Injected > all[j].Injected })
	for _, d := range all {
		if d.Used == 0 && d.Injected >= 3 && len(rep.NeverUsed) < recallTop {
			rep.NeverUsed = append(rep.NeverUsed, d)
		}
	}

	if recallJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(rep)
	}
	fmt.Printf("# recall report (since %s): sessions=%d (no transcript: %d) injected=%d used=%d precision=%.1f%%\n",
		rep.Since, rep.Sessions, rep.NoTranscpt, rep.Overall.Injected, rep.Overall.Used, rep.Overall.Prec*100)
	printBuckets := func(title string, m map[string]*recallBucket) {
		keys := make([]string, 0, len(m))
		for k := range m {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
		fmt.Fprintf(w, "\n%s\tinjected\tused\tprecision\n", title)
		for _, k := range keys {
			b := m[k]
			fmt.Fprintf(w, "%s\t%d\t%d\t%.1f%%\n", k, b.Injected, b.Used, b.Prec*100)
		}
		w.Flush()
	}
	printBuckets("signal", rep.ByTag)
	printBuckets("slot", rep.BySlot)
	printBuckets("rank", rep.ByRank)
	printDocs := func(title string, ds []recallDocStat) {
		if len(ds) == 0 {
			return
		}
		fmt.Printf("\n%s\n", title)
		for _, d := range ds {
			fmt.Printf("  %3d/%-3d %s\n", d.Used, d.Injected, shortenHome(d.Path))
		}
	}
	printDocs("most used (used/injected)", rep.MostUsed)
	printDocs("never used, injected >= 3", rep.NeverUsed)
	return nil
}

// sessionTranscript returns the session's JSONL lines with timestamps:
// archives.db first, then the ~/.claude/projects glob. Nil when neither has it.
func sessionTranscript(archive *sql.DB, sid string) ([]transcriptLine, bool) {
	if sid == "" {
		return nil, false
	}
	var p string
	if archive != nil {
		_ = archive.QueryRow(
			`SELECT filepath FROM documents WHERE source_type='session' AND session_id=?
              ORDER BY timestamp DESC LIMIT 1`, sid).Scan(&p)
	}
	if p == "" {
		home, _ := os.UserHomeDir()
		matches, _ := filepath.Glob(filepath.Join(home, ".claude", "projects", "*", sid+".jsonl"))
		if len(matches) > 0 {
			p = matches[0]
		}
	}
	if p == "" {
		return nil, false
	}
	raw, err := os.ReadFile(p)
	if err != nil {
		return nil, false
	}
	var out []transcriptLine
	for _, line := range strings.Split(string(raw), "\n") {
		if line == "" {
			continue
		}
		var meta struct {
			Timestamp string `json:"timestamp"`
		}
		_ = json.Unmarshal([]byte(line), &meta)
		ts, _ := time.Parse(time.RFC3339Nano, meta.Timestamp)
		out = append(out, transcriptLine{ts: ts, text: line})
	}
	return out, true
}

// touchedAfter reports whether path appears in a transcript line stamped after
// since. Lines without a parseable timestamp count, to fail toward "used".
func touchedAfter(lines []transcriptLine, path string, since time.Time) bool {
	for _, l := range lines {
		if !l.ts.IsZero() && l.ts.Before(since) {
			continue
		}
		if strings.Contains(l.text, path) {
			return true
		}
	}
	return false
}

// sessionWrites maps each .giantmem path this session wrote to its mtime.
func sessionWrites(live *sql.DB, sid string) map[string]time.Time {
	out := map[string]time.Time{}
	if sid == "" {
		return out
	}
	rows, err := live.Query(`SELECT path, mtime FROM live_docs WHERE session_id = ?`, sid)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var p string
		var mtime int64
		if rows.Scan(&p, &mtime) == nil {
			out[p] = time.Unix(mtime, 0)
		}
	}
	return out
}

func shortenHome(p string) string {
	if home, err := os.UserHomeDir(); err == nil && strings.HasPrefix(p, home) {
		return "~" + p[len(home):]
	}
	return p
}

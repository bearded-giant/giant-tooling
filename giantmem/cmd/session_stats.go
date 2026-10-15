package cmd

import (
	"fmt"
	"os"
	"text/tabwriter"
	"time"

	"github.com/bearded-giant/giant-tooling/giantmem/internal/db"
	"github.com/bearded-giant/giant-tooling/giantmem/internal/hub"
	"github.com/bearded-giant/giant-tooling/giantmem/internal/output"
	"github.com/bearded-giant/giant-tooling/giantmem/internal/search"
	"github.com/spf13/cobra"
)

var (
	lenSince    string
	lenIdle     time.Duration
	lenSessions bool
)

var sessStatsCmd = &cobra.Command{
	Use:   "stats",
	Short: "Session length per project: active time (idle gaps capped) and wall-clock span",
	Long: `Session length from indexed tool calls, main thread only.

active: sum of gaps between consecutive calls, each capped at --idle, so a
session resumed days later counts the work, not the days in between.
span: last call minus first call.

Sessions with no indexed tool calls are not counted.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		a, err := db.Open(archiveDBPath())
		if err != nil {
			return err
		}
		defer a.Close()
		q := hub.ToolQuery{Project: hubProject}
		if lenSince != "" {
			t, err := search.ParseSince(lenSince)
			if err != nil {
				return err
			}
			q.Since = t.UTC().Format(time.RFC3339)
		}
		ls, err := hub.SessionLengths(a, q, lenIdle)
		if err != nil {
			return err
		}
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		if lenSessions {
			if sessJSON {
				return output.JSON(ls)
			}
			fmt.Fprintln(w, "SESSION\tPROJECT\tSTART\tACTIVE\tSPAN\tCALLS")
			for _, l := range ls {
				start := l.FirstTS
				if t, err := time.Parse(time.RFC3339, l.FirstTS); err == nil {
					start = t.Local().Format("2006-01-02 15:04")
				}
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%d\n", l.SessionID[:min(8, len(l.SessionID))], l.Project, start, fmtDur(l.ActiveSec), fmtDur(l.SpanSec), l.Calls)
			}
			return w.Flush()
		}
		sum := hub.SummarizeLengths(ls)
		if sessJSON {
			return output.JSON(sum)
		}
		fmt.Fprintln(w, "PROJECT\tSESSIONS\tACTIVE p50\tACTIVE p95\tACTIVE avg\tSPAN p50")
		for _, s := range sum {
			fmt.Fprintf(w, "%s\t%d\t%s\t%s\t%s\t%s\n", s.Project, s.Sessions, fmtDur(s.ActiveP50), fmtDur(s.ActiveP95), fmtDur(s.ActiveAvg), fmtDur(s.SpanP50))
		}
		return w.Flush()
	},
}

func fmtDur(sec int) string {
	d := time.Duration(sec) * time.Second
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	return fmt.Sprintf("%dh%02dm", int(d.Hours()), int(d.Minutes())%60)
}

func init() {
	sessStatsCmd.Flags().StringVarP(&hubProject, "project", "p", "", "project substring")
	sessStatsCmd.Flags().StringVar(&lenSince, "since", "", "only calls since (e.g. 7d, 2026-10-01)")
	sessStatsCmd.Flags().DurationVar(&lenIdle, "idle", hub.DefaultIdleCap, "gaps longer than this count as idle")
	sessStatsCmd.Flags().BoolVar(&lenSessions, "sessions", false, "one row per session instead of per project")
	sessStatsCmd.Flags().BoolVar(&sessJSON, "json", false, "emit JSON")
	sessionCmd.AddCommand(sessStatsCmd)
}

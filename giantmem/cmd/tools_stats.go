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
	statsSince    string
	statsSession  string
	statsByServer bool
)

var toolsStatsCmd = &cobra.Command{
	Use:   "stats",
	Short: "Rank tool calls by name for a project, session, or time window",
	RunE: func(cmd *cobra.Command, args []string) error {
		a, err := db.Open(archiveDBPath())
		if err != nil {
			return err
		}
		defer a.Close()
		q := hub.ToolQuery{Project: hubProject, Agent: hubAgent, Tool: hubTool}
		if statsSession != "" {
			r, err := lookupSession(a, statsSession)
			if err != nil {
				return err
			}
			q.SessionID = r.ID
		}
		if statsSince != "" {
			t, err := search.ParseSince(statsSince)
			if err != nil {
				return err
			}
			q.Since = t.UTC().Format(time.RFC3339)
		}
		counts, err := hub.ToolCounts(a, q, statsByServer)
		if err != nil {
			return err
		}
		if sessJSON {
			return output.JSON(counts)
		}
		total := 0
		for _, c := range counts {
			total += c.Calls
		}
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "TOOL\tCALLS\t%\tERRORS\tERR%")
		for _, c := range counts {
			fmt.Fprintf(w, "%s\t%d\t%.1f\t%d\t%.1f\n", c.Tool, c.Calls, pct(c.Calls, total), c.Errors, pct(c.Errors, c.Calls))
		}
		fmt.Fprintf(w, "total\t%d\t\t\t\n", total)
		return w.Flush()
	},
}

func pct(n, of int) float64 {
	if of == 0 {
		return 0
	}
	return 100 * float64(n) / float64(of)
}

func init() {
	toolsStatsCmd.Flags().StringVarP(&hubProject, "project", "p", "", "project substring")
	toolsStatsCmd.Flags().StringVar(&statsSince, "since", "", "only calls since (e.g. 7d, 2026-10-01)")
	toolsStatsCmd.Flags().StringVar(&statsSession, "session", "", "one session (id or unique prefix)")
	toolsStatsCmd.Flags().StringVar(&hubAgent, "agent", "", "main = main thread only, or one subagent id")
	toolsStatsCmd.Flags().StringVar(&hubTool, "tool", "", "only tools matching (SQL LIKE, e.g. 'mcp__%')")
	toolsStatsCmd.Flags().BoolVar(&statsByServer, "by-server", false, "fold mcp__<server>__<tool> into one row per server")
	toolsStatsCmd.Flags().BoolVar(&sessJSON, "json", false, "emit JSON")
	toolsCmd.AddCommand(toolsStatsCmd)
}

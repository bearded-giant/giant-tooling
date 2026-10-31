package cmd

import (
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/bearded-giant/giant-tooling/giantmem/internal/db"
	"github.com/bearded-giant/giant-tooling/giantmem/internal/hub"
	"github.com/bearded-giant/giant-tooling/giantmem/internal/output"
)

var (
	pubTypes []string
	pubSince string
	pubUntil string
	pubLimit int
	pubJSON  bool
)

var publishedCmd = &cobra.Command{
	Use:   "published [query]",
	Short: "Pages (claude.ai + artifacts hub), vault notes, and handoffs sessions produced",
	Long: `Every claude-artifact, vault note, and vault handoff a session wrote, newest first,
collapsed across republishes. Query words match title, path, project, and vault note bodies.
Same rows and filters as the GUI artifacts tab.`,
	Example: `  giantmem published --since 2d
  giantmem published metric hoisting -t claude-artifact,vault
  giantmem published --since 2026-10-29 --until 2026-10-30 --json`,
	RunE: func(cmd *cobra.Command, args []string) error {
		a, err := db.Open(archiveDBPath())
		if err != nil {
			return err
		}
		defer a.Close()
		rows, err := hub.ListPublished(a, hub.PublishedFilter{
			Types: pubTypes, Since: pubSince, Until: pubUntil, Query: strings.Join(args, " "),
		})
		if err != nil {
			return err
		}
		if pubLimit > 0 && len(rows) > pubLimit {
			rows = rows[:pubLimit]
		}
		if pubJSON {
			return output.JSON(rows)
		}
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "WHEN\tTYPE\tPROJECT\tTITLE\tTARGET")
		for _, p := range rows {
			target := tildePath(p.Target)
			if p.Missing {
				target += "  (missing)"
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", shortTS(p.TS), p.Type, p.Project, p.Title, target)
		}
		return w.Flush()
	},
}

func init() {
	publishedCmd.Flags().StringSliceVarP(&pubTypes, "type", "t", nil, "claude-artifact, vault, handoff (repeat or comma-separate)")
	publishedCmd.Flags().StringVar(&pubSince, "since", "", `only published on/after (e.g. "2d", "2026-10-29", RFC3339)`)
	publishedCmd.Flags().StringVar(&pubUntil, "until", "", `only published on/before (e.g. "1d", "2026-10-30", RFC3339)`)
	publishedCmd.Flags().IntVarP(&pubLimit, "limit", "n", 50, "max rows (0 = all)")
	publishedCmd.Flags().BoolVar(&pubJSON, "json", false, "JSON output")
	rootCmd.AddCommand(publishedCmd)
}

package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/bryangrimes/gm/internal/health"
	"github.com/bryangrimes/gm/internal/output"
	"github.com/spf13/cobra"
)

var (
	doctorJSON      bool
	doctorRoots     []string
	doctorStaleDays int
)

var doctorCmd = &cobra.Command{
	Use:   "doctor",
	Short: "Run health checks across worktrees, workspaces, archives, hooks, and DBs",
	Long: `Read-only health audit. Reports orphan worktrees, orphan .giantmem/ dirs,
broken latest symlinks, archives.db drift, stale workspaces, missing settings.json
hook entries, missing MCP server, and DB integrity issues. Non-zero exit code if
any error-severity finding.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		home, _ := os.UserHomeDir()
		if len(doctorRoots) == 0 {
			doctorRoots = []string{filepath.Join(home, "dev")}
		}
		findings := health.Run(health.Options{
			ArchiveBase: archiveBasePath(),
			LiveDB:      liveDBPath(),
			ArchiveDB:   archiveDBPath(),
			HomeDir:     home,
			Roots:       doctorRoots,
			StaleDays:   doctorStaleDays,
		})
		summary := health.Summarize(findings)

		if doctorJSON {
			return output.JSON(map[string]any{
				"summary":  summary,
				"findings": findings,
			})
		}

		if len(findings) == 0 {
			fmt.Println("clean — no findings")
			return nil
		}
		// group by severity
		for _, sev := range []string{health.SevError, health.SevWarn, health.SevInfo} {
			label := map[string]string{
				health.SevError: "ERRORS",
				health.SevWarn:  "WARNINGS",
				health.SevInfo:  "INFO",
			}[sev]
			first := true
			for _, f := range findings {
				if f.Severity != sev {
					continue
				}
				if first {
					fmt.Printf("\n== %s ==\n", label)
					first = false
				}
				fmt.Printf("  [%s] %s\n", f.Category, f.Message)
				if f.Path != "" {
					fmt.Printf("       path: %s\n", f.Path)
				}
				if f.Hint != "" {
					fmt.Printf("       hint: %s\n", f.Hint)
				}
			}
		}
		fmt.Printf("\n%d total: %d errors, %d warnings, %d info\n",
			summary.Total, summary.Errors, summary.Warns, summary.Infos)
		if summary.Errors > 0 {
			os.Exit(1)
		}
		return nil
	},
}

func init() {
	doctorCmd.Flags().BoolVar(&doctorJSON, "json", false, "JSON output")
	doctorCmd.Flags().StringSliceVar(&doctorRoots, "root", nil, "roots to scan for orphan workspaces (default ~/dev)")
	doctorCmd.Flags().IntVar(&doctorStaleDays, "stale-days", 30, "minimum age (days) to mark a workspace stale")
	rootCmd.AddCommand(doctorCmd)
}

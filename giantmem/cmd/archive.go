package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"text/tabwriter"

	archive "github.com/bearded-giant/giant-tooling/giantmem/internal/archiver"
	"github.com/spf13/cobra"
)

var archiveCmd = &cobra.Command{
	Use:   "archive",
	Short: "Archive .giantmem directories: move, list, open, dedup, scan stale",
}

var (
	arRunProject string
	arRunDryRun  bool
	arRunNoReinit bool

	arDedupDryRun bool

	arStaleDays  int
	arStaleRoots []string
)

var archiveRunCmd = &cobra.Command{
	Use:   "run [src]",
	Short: "Archive a .giantmem directory (mv to ~/giantmem_archive/<project>/<ts>/) and re-init",
	Long: `Move a .giantmem directory into the archive tree, build the legacy index,
update the "latest" symlink, kick off a background FTS5 ingest, and re-init a
fresh .giantmem in its place.

Defaults: src = ./.giantmem, project = worktree-aware detection.

Replaces: gma`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		src := ""
		if len(args) > 0 {
			src = args[0]
		}
		_, err := archive.Run(src, archiveBasePath(), arRunProject, arRunDryRun, !arRunNoReinit)
		return err
	},
}

var archiveListCmd = &cobra.Command{
	Use:   "list [project]",
	Short: "List archives (all projects, or one project's timestamps)",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		var p string
		if len(args) > 0 {
			p = args[0]
		}
		return archive.List(archiveBasePath(), p)
	},
}

var archiveOpenCmd = &cobra.Command{
	Use:   "open <project> [timestamp]",
	Short: "Open archive in Finder (macOS)",
	Args:  cobra.RangeArgs(1, 2),
	RunE: func(cmd *cobra.Command, args []string) error {
		ts := ""
		if len(args) > 1 {
			ts = args[1]
		}
		return archive.Open(archiveBasePath(), args[0], ts)
	},
}

var archiveDedupCmd = &cobra.Command{
	Use:   "dedup <project>",
	Short: "Move older duplicate files into <project>/_review/ for cleanup",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return archive.Dedup(archiveBasePath(), args[0], arDedupDryRun)
	},
}

var archiveStaleCmd = &cobra.Command{
	Use:   "stale",
	Short: "Find live .giantmem dirs whose newest md is older than --days",
	Long: `Scan roots for .giantmem directories that haven't seen recent activity.
Useful for batch cleanup. Combines with --json for piping into archive run.

Examples:
  gm archive stale --days 30
  gm archive stale --days 14 --root ~/dev/ai`,
	RunE: func(cmd *cobra.Command, args []string) error {
		roots := arStaleRoots
		if len(roots) == 0 {
			home, _ := os.UserHomeDir()
			roots = []string{filepath.Join(home, "dev")}
		}
		results, err := archive.Stale(roots, archiveBasePath(), arStaleDays)
		if err != nil {
			return err
		}
		if len(results) == 0 {
			fmt.Println("no stale workspaces")
			return nil
		}
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "AGE\tWORKTREE\tPROJECT\tSIZE\tPATH")
		for _, r := range results {
			fmt.Fprintf(w, "%dd\t%s\t%s\t%dKB\t%s\n", r.AgeDays, r.Worktree, r.Project, r.Size/1024, r.Path)
		}
		return w.Flush()
	},
}

func init() {
	archiveRunCmd.Flags().StringVar(&arRunProject, "project", "", "override project name")
	archiveRunCmd.Flags().BoolVar(&arRunDryRun, "dry-run", false, "show what would happen")
	archiveRunCmd.Flags().BoolVar(&arRunNoReinit, "no-reinit", false, "skip workspace_init after move")

	archiveDedupCmd.Flags().BoolVar(&arDedupDryRun, "dry-run", false, "preview duplicates only")

	archiveStaleCmd.Flags().IntVar(&arStaleDays, "days", 30, "minimum age in days since newest md")
	archiveStaleCmd.Flags().StringSliceVar(&arStaleRoots, "root", nil, "roots to scan (default ~/dev)")

	archiveCmd.AddCommand(archiveRunCmd)
	archiveCmd.AddCommand(archiveListCmd)
	archiveCmd.AddCommand(archiveOpenCmd)
	archiveCmd.AddCommand(archiveDedupCmd)
	archiveCmd.AddCommand(archiveStaleCmd)
	rootCmd.AddCommand(archiveCmd)
}

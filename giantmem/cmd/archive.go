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
	arRunAll      bool
	arRunForce    bool
	arRunDryRun   bool
	arRunNoReinit bool

	arFeatureForce  bool
	arFeatureDryRun bool

	arDedupDryRun bool

	arStaleDays  int
	arStaleRoots []string
)

var archiveRunCmd = &cobra.Command{
	Use:   "run [src]",
	Short: "Archive every status=complete feature (or --all to wipe .giantmem/)",
	Long: `Default mode: iterate features.json and archive every status=complete
feature — verifies its files are mirrored in live.db, removes its dir (live_docs
rows kept), sets status=archived in features.json.

--all: wipe the entire .giantmem/ at src (default ./.giantmem) after verifying
every file is in live.db, and reinit a fresh workspace in place. live.db is the
durable archive (rows kept; protected by the db-backup cron). No FS snapshot.
Aborts if any file is not captured in live.db.

--force: in default mode, include features whose status != complete.`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		src := ""
		if len(args) > 0 {
			src = args[0]
		}
		if arRunAll {
			return archive.RunAll(src, archiveBasePath(), arRunDryRun, !arRunNoReinit)
		}
		results, err := archive.ArchiveCompleted(src, arRunForce, arRunDryRun)
		if err != nil {
			return err
		}
		return reportFeatureResults(results)
	},
}

var archiveFeatureCmd = &cobra.Command{
	Use:   "feature <name>",
	Short: "Archive a single feature (verify in live.db, rm dir, set status=archived)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		r, err := archive.ArchiveFeature("", args[0], arFeatureForce, arFeatureDryRun)
		if err != nil {
			return err
		}
		return reportFeatureResults([]archive.FeatureResult{r})
	},
}

func reportFeatureResults(results []archive.FeatureResult) error {
	if len(results) == 0 {
		fmt.Println("no features matched")
		return nil
	}
	var archived, skipped, errs int
	for _, r := range results {
		switch r.Action {
		case "archived", "would-archive":
			archived++
			fmt.Printf("  %-30s %-12s -> archived   (kept in db, %d files)\n", r.Name, r.Status, r.Captured)
		case "skipped":
			skipped++
			fmt.Printf("  %-30s %-12s -- skipped   (%s)\n", r.Name, r.Status, r.Reason)
		case "error":
			errs++
			fmt.Printf("  %-30s %-12s !! error     (%s)\n", r.Name, r.Status, r.Reason)
		}
	}
	fmt.Printf("\nsummary: archived=%d skipped=%d errors=%d\n", archived, skipped, errs)
	return nil
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
	archiveRunCmd.Flags().BoolVar(&arRunAll, "all", false, "wipe entire .giantmem/ instead of per-feature archive")
	archiveRunCmd.Flags().BoolVar(&arRunForce, "force", false, "in per-feature mode, include status != complete")
	archiveRunCmd.Flags().BoolVar(&arRunDryRun, "dry-run", false, "show what would happen")
	archiveRunCmd.Flags().BoolVar(&arRunNoReinit, "no-reinit", false, "skip workspace_init after --all wipe")

	archiveFeatureCmd.Flags().BoolVar(&arFeatureForce, "force", false, "allow archiving when status != complete")
	archiveFeatureCmd.Flags().BoolVar(&arFeatureDryRun, "dry-run", false, "show what would happen")

	archiveDedupCmd.Flags().BoolVar(&arDedupDryRun, "dry-run", false, "preview duplicates only")

	archiveStaleCmd.Flags().IntVar(&arStaleDays, "days", 30, "minimum age in days since newest md")
	archiveStaleCmd.Flags().StringSliceVar(&arStaleRoots, "root", nil, "roots to scan (default ~/dev)")

	archiveCmd.AddCommand(archiveRunCmd)
	archiveCmd.AddCommand(archiveFeatureCmd)
	archiveCmd.AddCommand(archiveListCmd)
	archiveCmd.AddCommand(archiveOpenCmd)
	archiveCmd.AddCommand(archiveDedupCmd)
	archiveCmd.AddCommand(archiveStaleCmd)
	rootCmd.AddCommand(archiveCmd)
}

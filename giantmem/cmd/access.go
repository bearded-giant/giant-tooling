package cmd

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/bearded-giant/giant-tooling/giantmem/internal/artifacts"
	"github.com/bearded-giant/giant-tooling/giantmem/internal/db"
	"github.com/spf13/cobra"
)

var accessCmd = &cobra.Command{
	Use:   "access",
	Short: "Inspect and prune the artifact_access log",
}

var (
	accessPruneOlderThan string
	accessPruneDryRun    bool
)

var accessPruneCmd = &cobra.Command{
	Use:   "prune",
	Short: "Drop artifact_access rows older than --older-than (e.g. 180d, 30d, 6h)",
	RunE:  runAccessPrune,
}

var accessTopCmd = &cobra.Command{
	Use:   "top",
	Short: "Show top-N artifacts by access count over the last 30 days",
	RunE:  runAccessTop,
}

var accessTopLimit int

func init() {
	accessPruneCmd.Flags().StringVar(&accessPruneOlderThan, "older-than", "180d", "duration; supported suffixes: d (days), h (hours)")
	accessPruneCmd.Flags().BoolVar(&accessPruneDryRun, "dry-run", false, "report rows that would be dropped without writing")
	accessTopCmd.Flags().IntVar(&accessTopLimit, "limit", 10, "row count")
	accessCmd.AddCommand(accessPruneCmd, accessTopCmd)
	rootCmd.AddCommand(accessCmd)
}

func runAccessPrune(cmd *cobra.Command, args []string) error {
	dur, err := parseDurationDH(accessPruneOlderThan)
	if err != nil {
		return err
	}
	cutoff := time.Now().Add(-dur)
	live, err := db.Open(liveDBPath())
	if err != nil {
		return err
	}
	defer live.Close()
	if accessPruneDryRun {
		var n int
		row := live.QueryRow(
			`SELECT COUNT(*) FROM artifact_access WHERE accessed_at < ?`,
			cutoff.UTC().Format(time.RFC3339),
		)
		if err := row.Scan(&n); err != nil {
			return err
		}
		fmt.Printf("would drop %d rows older than %s\n", n, cutoff.Format(time.RFC3339))
		return nil
	}
	n, err := artifacts.PruneAccessLog(live, cutoff)
	if err != nil {
		return err
	}
	fmt.Printf("dropped %d access rows older than %s\n", n, cutoff.Format(time.RFC3339))
	return nil
}

func runAccessTop(cmd *cobra.Command, args []string) error {
	live, err := db.Open(liveDBPath())
	if err != nil {
		return err
	}
	defer live.Close()
	since := time.Now().AddDate(0, 0, -30)
	top, err := artifacts.TopAccessed(live, since, accessTopLimit)
	if err != nil {
		return err
	}
	if len(top) == 0 {
		fmt.Fprintln(cmd.ErrOrStderr(), "no access rows in last 30d")
		return nil
	}
	fmt.Printf("# top accessed (last 30d, total=%d)\n", len(top))
	for _, s := range top {
		fmt.Printf("%5d  %s\n", s.Count, s.ArtifactID)
	}
	return nil
}

// parseDurationDH accepts strings like "180d", "30d", "6h", "1d12h" (sum).
// Falls back to time.ParseDuration for anything else.
func parseDurationDH(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("duration is empty")
	}
	if d, err := time.ParseDuration(s); err == nil {
		return d, nil
	}
	var total time.Duration
	cur := ""
	for _, ch := range s {
		if ch >= '0' && ch <= '9' {
			cur += string(ch)
			continue
		}
		if cur == "" {
			return 0, fmt.Errorf("malformed duration %q", s)
		}
		n, err := strconv.Atoi(cur)
		if err != nil {
			return 0, err
		}
		cur = ""
		switch ch {
		case 'd':
			total += time.Duration(n) * 24 * time.Hour
		case 'h':
			total += time.Duration(n) * time.Hour
		case 'm':
			total += time.Duration(n) * time.Minute
		default:
			return 0, fmt.Errorf("malformed duration %q: unknown unit %q", s, ch)
		}
	}
	if cur != "" {
		return 0, fmt.Errorf("malformed duration %q: trailing number without unit", s)
	}
	return total, nil
}

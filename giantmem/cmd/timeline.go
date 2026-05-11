package cmd

import (
	"database/sql"
	"fmt"
	"os"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/bearded-giant/giant-tooling/giantmem/internal/daemon"
	"github.com/bearded-giant/giant-tooling/giantmem/internal/db"
	"github.com/bearded-giant/giant-tooling/giantmem/internal/timelineinfo"
	"github.com/spf13/cobra"
)

var (
	timelineDays      int
	timelineProject   string
	timelineSource    string
	timelineNoDaemon  bool
)

var timelineCmd = &cobra.Command{
	Use:   "timeline",
	Short: "Visual text grid of session/archive activity over the last N days",
	Long: `If giantmemd is running the query runs over the daemon's already-open DB
handles. Pass --no-daemon to open the archive DB directly.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		rows, err := dispatchTimeline()
		if err != nil {
			return err
		}
		renderTimeline(rows)
		return nil
	},
}

func dispatchTimeline() ([]timelineinfo.Row, error) {
	if !timelineNoDaemon && os.Getenv("GIANTMEM_NO_DAEMON") == "" {
		sock := daemon.DefaultSocketPath()
		if daemon.SocketAlive(sock, 250*time.Millisecond) {
			cli := daemon.NewClient(sock, 5*time.Second)
			var out struct {
				Rows []timelineinfo.Row `json:"rows"`
			}
			err := cli.Call("timeline", &daemon.TimelineParams{
				Days:    timelineDays,
				Project: timelineProject,
				Source:  timelineSource,
			}, &out)
			if err == nil {
				return out.Rows, nil
			}
			if !daemon.IsSchemaDrift(err) {
				fmt.Fprintf(os.Stderr, "daemon error, falling back: %v\n", err)
			}
		}
	}
	return timelineDirect()
}

func timelineDirect() ([]timelineinfo.Row, error) {
	var archive *sql.DB
	if d, err := db.Open(archiveDBPath()); err == nil {
		archive = d
		defer archive.Close()
	} else {
		return nil, err
	}
	return timelineinfo.Query(archive, timelineDays, timelineProject, timelineSource)
}

func renderTimeline(rows []timelineinfo.Row) {
	if timelineDays <= 0 {
		timelineDays = 14
	}
	end := time.Now()
	start := end.AddDate(0, 0, -timelineDays+1).Truncate(24 * time.Hour)
	bucketCount := timelineDays
	dayKey := func(t time.Time) int {
		return int(t.Sub(start).Hours() / 24)
	}

	grid := map[string][]int{}
	for _, r := range rows {
		t, err := time.Parse("20060102_150405", r.Timestamp)
		if err != nil {
			continue
		}
		day := dayKey(t)
		if day < 0 || day >= bucketCount {
			continue
		}
		if grid[r.Project] == nil {
			grid[r.Project] = make([]int, bucketCount)
		}
		grid[r.Project][day]++
	}

	type gridRow struct {
		name  string
		total int
		cells []int
	}
	var ordered []gridRow
	for p, cells := range grid {
		t := 0
		for _, c := range cells {
			t += c
		}
		ordered = append(ordered, gridRow{p, t, cells})
	}
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].total > ordered[j].total })

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 1, ' ', 0)
	var hdr strings.Builder
	hdr.WriteString("PROJECT\t")
	for i := 0; i < bucketCount; i++ {
		d := start.AddDate(0, 0, i)
		hdr.WriteString(d.Format("01-02"))
		hdr.WriteString("\t")
	}
	hdr.WriteString("TOT")
	fmt.Fprintln(w, hdr.String())

	for _, r := range ordered {
		var b strings.Builder
		b.WriteString(r.name + "\t")
		for _, c := range r.cells {
			b.WriteString(barCell(c))
			b.WriteString("\t")
		}
		b.WriteString(fmt.Sprintf("%d", r.total))
		fmt.Fprintln(w, b.String())
	}
	fmt.Fprintf(w, "\nwindow: %s -> %s  (%d days)\n",
		start.Format("2006-01-02"), end.Format("2006-01-02"), bucketCount)
	w.Flush()
}

func barCell(n int) string {
	switch {
	case n == 0:
		return "·"
	case n < 3:
		return "▁"
	case n < 6:
		return "▂"
	case n < 10:
		return "▃"
	case n < 20:
		return "▅"
	default:
		return "█"
	}
}

func init() {
	timelineCmd.Flags().IntVarP(&timelineDays, "days", "d", 14, "window size in days")
	timelineCmd.Flags().StringVarP(&timelineProject, "project", "p", "", "filter by project (LIKE)")
	timelineCmd.Flags().StringVarP(&timelineSource, "source", "s", "", "filter by source_type (workspace|session|domain)")
	timelineCmd.Flags().BoolVar(&timelineNoDaemon, "no-daemon", false, "skip giantmemd; open archive DB directly")
	rootCmd.AddCommand(timelineCmd)
}

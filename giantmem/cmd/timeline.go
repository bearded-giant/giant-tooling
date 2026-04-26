package cmd

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/bryangrimes/gm/internal/db"
	"github.com/spf13/cobra"
)

var (
	timelineDays    int
	timelineProject string
	timelineSource  string
)

var timelineCmd = &cobra.Command{
	Use:   "timeline",
	Short: "Visual text grid of session/archive activity over the last N days",
	RunE: func(cmd *cobra.Command, args []string) error {
		d, err := db.Open(archiveDBPath())
		if err != nil {
			return err
		}
		defer d.Close()

		end := time.Now()
		start := end.AddDate(0, 0, -timelineDays+1)
		// build day buckets
		bucketCount := timelineDays
		dayKey := func(t time.Time) int {
			return int(t.Sub(start.Truncate(24*time.Hour)).Hours() / 24)
		}

		// query rows in window
		conds := []string{`timestamp >= ?`}
		qargs := []any{start.Format("20060102_150405")}
		if timelineProject != "" {
			conds = append(conds, "project LIKE ?")
			qargs = append(qargs, "%"+timelineProject+"%")
		}
		if timelineSource != "" {
			conds = append(conds, "source_type = ?")
			qargs = append(qargs, timelineSource)
		}
		q := fmt.Sprintf(
			`SELECT project, source_type, timestamp FROM documents WHERE %s`,
			strings.Join(conds, " AND "),
		)
		rows, err := d.Query(q, qargs...)
		if err != nil {
			return err
		}
		defer rows.Close()

		// per-project per-day counts
		grid := map[string][]int{} // project -> [day0..dayN]
		for rows.Next() {
			var proj, src, ts string
			if err := rows.Scan(&proj, &src, &ts); err != nil {
				return err
			}
			t, err := time.Parse("20060102_150405", ts)
			if err != nil {
				continue
			}
			day := dayKey(t)
			if day < 0 || day >= bucketCount {
				continue
			}
			if grid[proj] == nil {
				grid[proj] = make([]int, bucketCount)
			}
			grid[proj][day]++
		}

		// sort projects by total
		type row struct {
			name  string
			total int
			cells []int
		}
		var ordered []row
		for p, cells := range grid {
			t := 0
			for _, c := range cells {
				t += c
			}
			ordered = append(ordered, row{p, t, cells})
		}
		sort.Slice(ordered, func(i, j int) bool { return ordered[i].total > ordered[j].total })

		// header: dates
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
		return w.Flush()
	},
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
	rootCmd.AddCommand(timelineCmd)
}

package cmd

import (
	"database/sql"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/bryangrimes/gm/internal/db"
	"github.com/spf13/cobra"
)

type sqlDB = sql.DB

var (
	tailProject string
	tailFeature string
	tailSince   string
	tailHead    int
	tailEvery   time.Duration
)

var tailCmd = &cobra.Command{
	Use:   "tail",
	Short: "Stream new live workspace writes as they're indexed (tail -f for live.db)",
	Long: `Polls live.db for new rows and prints each one with timestamp, project,
feature, dir_type, path, and a content head. Useful when running multiple
Claude sessions in parallel.

Defaults to all projects. Filter with --project (LIKE) or --feature.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		d, err := db.Open(liveDBPath())
		if err != nil {
			return fmt.Errorf("open live.db: %w (run giantmem index init?)", err)
		}
		defer d.Close()

		// initial high-water mark
		var highWater int64
		if tailSince != "" {
			dur, err := parseDuration(tailSince)
			if err != nil {
				return err
			}
			highWater = time.Now().Add(-dur).Unix()
		} else {
			d.QueryRow(`SELECT COALESCE(MAX(mtime), 0) FROM live_docs`).Scan(&highWater)
		}

		fmt.Fprintf(os.Stderr, "tailing live.db from %s (project=%q feature=%q)\n",
			time.Unix(highWater, 0).Format(time.RFC3339), tailProject, tailFeature)
		fmt.Fprintln(os.Stderr, "Ctrl-C to stop")

		// graceful exit
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
		stop := make(chan struct{})
		go func() {
			<-sig
			close(stop)
		}()

		ticker := time.NewTicker(tailEvery)
		defer ticker.Stop()

		for {
			select {
			case <-stop:
				fmt.Fprintln(os.Stderr, "stopped")
				return nil
			case <-ticker.C:
				rows, err := tailQueryNew(d, highWater)
				if err != nil {
					fmt.Fprintf(os.Stderr, "warn: %v\n", err)
					continue
				}
				for _, r := range rows {
					printTailRow(r)
					if r.Mtime > highWater {
						highWater = r.Mtime
					}
				}
			}
		}
	},
}

type tailRow struct {
	Path      string
	Project   string
	Feature   string
	DirType   string
	Mtime     int64
	IngestedAt string
	Content   string
}

func init() {
	tailCmd.Flags().StringVarP(&tailProject, "project", "p", "", "filter by project (LIKE)")
	tailCmd.Flags().StringVarP(&tailFeature, "feature", "f", "", "filter by active feature")
	tailCmd.Flags().StringVar(&tailSince, "since", "", `start from N ago (e.g. "10m"); default: latest`)
	tailCmd.Flags().IntVar(&tailHead, "head", 80, "characters of content to print per row")
	tailCmd.Flags().DurationVar(&tailEvery, "interval", time.Second, "poll interval")
	rootCmd.AddCommand(tailCmd)
}

func printTailRow(r tailRow) {
	tag := r.DirType
	if r.Feature != "" {
		tag = r.Feature + "/" + r.DirType
	}
	ts := time.Unix(r.Mtime, 0).Format("15:04:05")
	head := strings.ReplaceAll(strings.TrimSpace(r.Content), "\n", " ")
	if len(head) > tailHead {
		head = head[:tailHead] + "…"
	}
	short := r.Path
	home, _ := os.UserHomeDir()
	if strings.HasPrefix(short, home) {
		short = "~" + short[len(home):]
	}
	fmt.Printf("%s  %s/%s  %s\n        %s\n", ts, r.Project, tag, short, head)
}

func tailQueryNew(d *sqlDB, highWater int64) ([]tailRow, error) {
	var conds []string
	var qargs []any
	conds = append(conds, "mtime > ?")
	qargs = append(qargs, highWater)
	if tailProject != "" {
		conds = append(conds, "project LIKE ?")
		qargs = append(qargs, "%"+tailProject+"%")
	}
	if tailFeature != "" {
		conds = append(conds, "feature = ?")
		qargs = append(qargs, tailFeature)
	}
	q := fmt.Sprintf(
		`SELECT path, project, COALESCE(feature,''), COALESCE(dir_type,''),
                mtime, ingested_at, COALESCE(content,'')
           FROM live_docs WHERE %s ORDER BY mtime ASC LIMIT 100`,
		strings.Join(conds, " AND "),
	)
	rows, err := d.Query(q, qargs...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []tailRow
	for rows.Next() {
		var r tailRow
		if err := rows.Scan(&r.Path, &r.Project, &r.Feature, &r.DirType, &r.Mtime, &r.IngestedAt, &r.Content); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// silence
var _ = filepath.Join

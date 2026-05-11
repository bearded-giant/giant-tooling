package cmd

import (
	"database/sql"
	"fmt"
	"os"
	"text/tabwriter"
	"time"

	"github.com/bearded-giant/giant-tooling/giantmem/internal/daemon"
	"github.com/bearded-giant/giant-tooling/giantmem/internal/db"
	"github.com/bearded-giant/giant-tooling/giantmem/internal/statsinfo"
	"github.com/spf13/cobra"
)

var statsNoDaemon bool

var statsCmd = &cobra.Command{
	Use:   "stats",
	Short: "Show indexed document counts by project and source",
	RunE: func(cmd *cobra.Command, args []string) error {
		rows, err := dispatchStats()
		if err != nil {
			return err
		}
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "PROJECT\tSOURCE\tDIR_TYPE\tCOUNT")
		var total int
		for _, r := range rows {
			fmt.Fprintf(w, "%s\t%s\t%s\t%d\n", r.Project, r.SourceType, r.DirType, r.Count)
			total += r.Count
		}
		fmt.Fprintf(w, "\nTOTAL\t\t\t%d\n", total)
		return w.Flush()
	},
}

func dispatchStats() ([]statsinfo.Row, error) {
	if !statsNoDaemon && os.Getenv("GIANTMEM_NO_DAEMON") == "" {
		sock := daemon.DefaultSocketPath()
		if daemon.SocketAlive(sock, 250*time.Millisecond) {
			cli := daemon.NewClient(sock, 5*time.Second)
			var out struct {
				Rows []statsinfo.Row `json:"rows"`
			}
			err := cli.Call("stats", nil, &out)
			if err == nil {
				return out.Rows, nil
			}
			if !daemon.IsSchemaDrift(err) {
				fmt.Fprintf(os.Stderr, "daemon error, falling back: %v\n", err)
			}
		}
	}
	return statsDirect()
}

func statsDirect() ([]statsinfo.Row, error) {
	var archive *sql.DB
	if d, err := db.Open(archiveDBPath()); err == nil {
		archive = d
		defer archive.Close()
	} else {
		return nil, err
	}
	return statsinfo.Query(archive)
}

func init() {
	statsCmd.Flags().BoolVar(&statsNoDaemon, "no-daemon", false, "skip giantmemd; open DBs directly")
}

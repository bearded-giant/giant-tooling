package cmd

import (
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/bryangrimes/gm/internal/db"
	"github.com/spf13/cobra"
)

var statsCmd = &cobra.Command{
	Use:   "stats",
	Short: "Show indexed document counts by project and source",
	RunE: func(cmd *cobra.Command, args []string) error {
		d, err := db.Open(archiveDBPath())
		if err != nil {
			return err
		}
		defer d.Close()

		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "PROJECT\tSOURCE\tDIR_TYPE\tCOUNT")
		rows, err := d.Query(`
            SELECT project, source_type, COALESCE(dir_type,'') AS dir_type, COUNT(*)
              FROM documents
             GROUP BY project, source_type, dir_type
             ORDER BY project, source_type, dir_type`)
		if err != nil {
			return err
		}
		defer rows.Close()
		var total int
		for rows.Next() {
			var p, s, t string
			var c int
			if err := rows.Scan(&p, &s, &t, &c); err != nil {
				return err
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%d\n", p, s, t, c)
			total += c
		}
		fmt.Fprintf(w, "\nTOTAL\t\t\t%d\n", total)
		return w.Flush()
	},
}

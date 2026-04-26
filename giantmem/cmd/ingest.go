package cmd

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/bearded-giant/giant-tooling/giantmem/internal/db"
	"github.com/bearded-giant/giant-tooling/giantmem/internal/sources"
	"github.com/spf13/cobra"
)

var (
	ingestProject        string
	ingestSessionsOnly   bool
	ingestWorkspacesOnly bool
	ingestForce          bool
	ingestSourceFilter   []string
)

var ingestCmd = &cobra.Command{
	Use:   "ingest",
	Short: "Re-index workspace archives and Claude session JSONLs into archives.db",
	Long: `Walks each registered ingest source and rebuilds documents + documents_fts.

Sources are configured at ~/.config/giantmem/sources.toml. Without a config the
default builtins (workspace-md, claude-jsonl, domain-json) are used.

--source can be repeated to limit the run to specific sources. If --source is
omitted, all enabled sources run. --sessions-only / --workspaces-only are kept
as conveniences and translate to source filters.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		home, _ := os.UserHomeDir()
		d, err := db.Open(archiveDBPath())
		if err != nil {
			return err
		}
		defer d.Close()

		cfg, err := sources.LoadConfig(sources.DefaultConfigPath())
		if err != nil {
			return err
		}
		reg, err := sources.NewRegistry(cfg)
		if err != nil {
			return err
		}

		filter := ingestSourceFilter
		if ingestSessionsOnly {
			filter = append(filter, "claude-jsonl")
		}
		if ingestWorkspacesOnly {
			filter = append(filter, "workspace-md", "domain-json")
		}

		opts := sources.EmitOptions{
			ArchiveBase:    archiveBasePath(),
			ClaudeProjects: filepath.Join(home, ".claude", "projects"),
			Project:        ingestProject,
			Force:          ingestForce,
			DB:             d,
		}

		// rebuild non-session scope when running workspace builtins fresh:
		// matches legacy behavior of dropping then re-inserting.
		runners := reg.Filter(filter)
		if shouldDropWorkspace(runners) {
			if err := dropNonSessionRows(d, ingestProject); err != nil {
				return err
			}
		}
		ctx := context.Background()
		perSource := map[string]sources.Stats{}
		for _, src := range runners {
			st, rerr := sources.Run(ctx, d, src, opts)
			if rerr != nil {
				return rerr
			}
			perSource[src.Name()] = st
		}

		printIngestStats(perSource)
		return nil
	},
}

// shouldDropWorkspace returns true when a non-incremental builtin (workspace
// or domain) is going to run and we need to clear the scope first.
func shouldDropWorkspace(runners []sources.Source) bool {
	for _, s := range runners {
		switch s.Name() {
		case "workspace-md", "domain-json":
			return true
		}
	}
	return false
}

func dropNonSessionRows(d *sql.DB, project string) error {
	var cond string
	var args []any
	if project != "" {
		cond = "project = ? AND source_type != 'session'"
		args = []any{project}
	} else {
		cond = "source_type != 'session'"
	}
	rows, err := d.Query("SELECT id FROM documents WHERE "+cond, args...)
	if err != nil {
		return err
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	rows.Close()
	tx, err := d.Begin()
	if err != nil {
		return err
	}
	for _, id := range ids {
		if _, err := tx.Exec("DELETE FROM documents_fts WHERE rowid = ?", id); err != nil {
			tx.Rollback()
			return err
		}
		if _, err := tx.Exec("DELETE FROM documents WHERE id = ?", id); err != nil {
			tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}

func printIngestStats(perSource map[string]sources.Stats) {
	var names []string
	for n := range perSource {
		names = append(names, n)
	}
	sort.Strings(names)
	var totalCount, totalErr int
	for _, n := range names {
		st := perSource[n]
		fmt.Printf("  %-16s %5d docs", n, st.Count)
		if st.Errs > 0 {
			fmt.Printf("  (%d errors)", st.Errs)
		}
		fmt.Println()
		totalCount += st.Count
		totalErr += st.Errs
	}
	fmt.Printf("indexed %d total docs into %s\n", totalCount, archiveDBPath())
	if totalErr > 0 {
		fmt.Printf("skipped %d files (read errors or bad input)\n", totalErr)
	}
}

func init() {
	ingestCmd.Flags().StringVarP(&ingestProject, "project", "p", "", "ingest only this project")
	ingestCmd.Flags().BoolVar(&ingestSessionsOnly, "sessions-only", false, "only index Claude session transcripts (alias for --source claude-jsonl)")
	ingestCmd.Flags().BoolVar(&ingestWorkspacesOnly, "workspaces-only", false, "only index workspace archives (alias for --source workspace-md,domain-json)")
	ingestCmd.Flags().BoolVar(&ingestForce, "force", false, "force full re-index (ignore mtime)")
	ingestCmd.Flags().StringSliceVarP(&ingestSourceFilter, "source", "s", nil, "comma-separated source names to run (default: all enabled)")
	rootCmd.AddCommand(ingestCmd)
}


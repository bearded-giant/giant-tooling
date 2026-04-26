package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/bryangrimes/gm/internal/db"
	"github.com/bryangrimes/gm/internal/ingest"
	"github.com/spf13/cobra"
)

var (
	ingestProject       string
	ingestSessionsOnly  bool
	ingestWorkspacesOnly bool
	ingestForce         bool
)

var ingestCmd = &cobra.Command{
	Use:   "ingest",
	Short: "Re-index workspace archives and Claude session JSONLs into archives.db",
	Long: `Walks the archive tree (~/giantmem_archive/<project>/<ts>/) and the Claude
sessions directory (~/.claude/projects/) and rebuilds documents + documents_fts.

By default the workspace pass drops the matching scope (project or all
non-session rows) and rebuilds. The session pass is incremental by mtime; pass
--force to redo all sessions.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		home, _ := os.UserHomeDir()
		d, err := db.Open(archiveDBPath())
		if err != nil {
			return err
		}
		defer d.Close()

		st, err := ingest.Run(d, ingest.Options{
			ArchiveBase:    archiveBasePath(),
			ClaudeProjects: filepath.Join(home, ".claude", "projects"),
			Project:        ingestProject,
			SessionsOnly:   ingestSessionsOnly,
			WorkspacesOnly: ingestWorkspacesOnly,
			Force:          ingestForce,
		})
		if err != nil {
			return err
		}
		fmt.Printf("indexed %d workspace docs, %d sessions into %s\n",
			st.WorkspaceCount, st.SessionCount, archiveDBPath())
		if total := st.WorkspaceErr + st.SessionErr; total > 0 {
			fmt.Printf("skipped %d files (read errors or duplicates)\n", total)
		}
		return nil
	},
}

func init() {
	ingestCmd.Flags().StringVarP(&ingestProject, "project", "p", "", "ingest only this project")
	ingestCmd.Flags().BoolVar(&ingestSessionsOnly, "sessions-only", false, "only index Claude session transcripts")
	ingestCmd.Flags().BoolVar(&ingestWorkspacesOnly, "workspaces-only", false, "only index workspace archives")
	ingestCmd.Flags().BoolVar(&ingestForce, "force", false, "force full re-index (ignore mtime)")
	rootCmd.AddCommand(ingestCmd)
}

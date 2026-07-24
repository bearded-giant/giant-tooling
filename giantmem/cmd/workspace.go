package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	archive "github.com/bearded-giant/giant-tooling/giantmem/internal/archiver"
	"github.com/spf13/cobra"
)

var workspaceCmd = &cobra.Command{
	Use:   "workspace",
	Short: "Workspace lifecycle: init, status, sync, complete, etc.",
}

func runWorkspaceFunc(fn string, args []string) error {
	lib := archive.WorkspaceLibPath()
	if _, err := os.Stat(lib); err != nil {
		return fmt.Errorf("workspace-lib not found at %s", lib)
	}
	quoted := make([]string, len(args))
	for i, a := range args {
		quoted[i] = bashQuote(a)
	}
	cmdline := fmt.Sprintf("source %q && %s %s", lib, fn, strings.Join(quoted, " "))
	c := exec.Command("bash", "-c", cmdline)
	c.Stdin = os.Stdin
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	c.Env = os.Environ()
	return c.Run()
}

func bashQuote(s string) string {
	if s == "" {
		return `''`
	}
	return `'` + strings.ReplaceAll(s, `'`, `'\''`) + `'`
}

func workspaceSubcmd(name, fn, short string) *cobra.Command {
	return &cobra.Command{
		Use:                name,
		Short:              short,
		DisableFlagParsing: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runWorkspaceFunc(fn, args)
		},
	}
}

func deprecatedWorkspaceSubcmd(name, fn, short, dep string) *cobra.Command {
	c := workspaceSubcmd(name, fn, short)
	c.Deprecated = dep
	return c
}

var (
	wsArchiveDryRun   bool
	wsArchiveNoReinit bool
)

var workspaceArchiveCmd = &cobra.Command{
	Use:   "archive [src]",
	Short: "Archive the entire .giantmem/: verify all files in live.db, wipe, reinit fresh",
	Long: `Wipe the entire .giantmem/ at src (default ./.giantmem) after verifying
every file is in live.db, then reinit a fresh workspace in place. live.db is
the durable archive (rows kept; protected by the db-backup cron). No FS
snapshot. Aborts if any file is not captured in live.db.

Per-feature archiving: giantmem feature archive [name].`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		src := ""
		if len(args) > 0 {
			src = args[0]
		}
		return archive.RunAll(src, archiveBasePath(), wsArchiveDryRun, !wsArchiveNoReinit)
	},
}

func init() {
	workspaceArchiveCmd.Flags().BoolVar(&wsArchiveDryRun, "dry-run", false, "show what would happen")
	workspaceArchiveCmd.Flags().BoolVar(&wsArchiveNoReinit, "no-reinit", false, "skip workspace_init after wipe")

	workspaceCmd.AddCommand(
		workspaceSubcmd("status", "workspace_status", "Show workspace status"),
		workspaceSubcmd("init", "workspace_init", "Initialize .giantmem in [dir] [name]"),
		workspaceSubcmd("bootstrap", "workspace_bootstrap", "Smart init/migrate/sync"),
		workspaceSubcmd("migrate", "workspace_migrate", "Move loose .giantmem files into subdirs"),
		workspaceSubcmd("note", "workspace_session_note", "Add a session note"),
		workspaceSubcmd("discover", "workspace_discover", "Add a discovery note"),
		workspaceSubcmd("complete", "workspace_complete", "Mark workspace complete"),
		workspaceSubcmd("sync", "workspace_sync", "Refresh git log"),
		deprecatedWorkspaceSubcmd("features", "workspace_features", "Show feature status table", "use `giantmem feature list`"),
		deprecatedWorkspaceSubcmd("new-feature", "workspace_new_feature", "Create a feature (proposal/tasks/facts/notes)", "use `giantmem feature new`"),
		deprecatedWorkspaceSubcmd("start-feature", "workspace_start_feature", "Promote a pending feature to in_progress", "use `giantmem feature start`"),
		deprecatedWorkspaceSubcmd("pause-feature", "workspace_pause_feature", "Pause the active (or named) feature", "use `giantmem feature pause`"),
		deprecatedWorkspaceSubcmd("reopen-feature", "workspace_reopen_feature", "Reopen a paused/completed feature", "use `giantmem feature reopen`"),
		deprecatedWorkspaceSubcmd("complete-feature", "workspace_complete_feature", "Mark the active (or named) feature complete", "use `giantmem feature complete`"),
		workspaceSubcmd("gitlog", "workspace_gitlog", "Update git-log.md"),
		workspaceArchiveCmd,
	)
	rootCmd.AddCommand(workspaceCmd)
}

package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	archive "github.com/bryangrimes/gm/internal/archiver"
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

func init() {
	workspaceCmd.AddCommand(
		workspaceSubcmd("status", "workspace_status", "Show workspace status"),
		workspaceSubcmd("init", "workspace_init", "Initialize .giantmem in [dir] [name]"),
		workspaceSubcmd("bootstrap", "workspace_bootstrap", "Smart init/migrate/sync"),
		workspaceSubcmd("migrate", "workspace_migrate", "Move loose .giantmem files into subdirs"),
		workspaceSubcmd("tree", "workspace_tree", "Generate tree.md"),
		workspaceSubcmd("note", "workspace_session_note", "Add a session note"),
		workspaceSubcmd("discover", "workspace_discover", "Add a discovery note"),
		workspaceSubcmd("complete", "workspace_complete", "Mark workspace complete"),
		workspaceSubcmd("sync", "workspace_sync", "Refresh tree + git log"),
		workspaceSubcmd("features", "workspace_features", "Show feature status table"),
		workspaceSubcmd("gitlog", "workspace_gitlog", "Update git-log.md"),
	)
	rootCmd.AddCommand(workspaceCmd)
}

package cmd

import (
	"fmt"
	"os"
	"syscall"

	"github.com/spf13/cobra"
)

const (
	groupWorkflow = "workflow"
	groupSearch   = "search"
	groupInfra    = "infra"
	groupPlumbing = "plumbing"
)

var dbCmd = &cobra.Command{
	Use:   "db",
	Short: "Database plumbing: index, ingest, embed, backup, access, tail, watch",
}

// setupCommandTree runs at Execute time so it sees every init()-registered
// command regardless of file init order.
func setupCommandTree() {
	rootCmd.AddGroup(
		&cobra.Group{ID: groupWorkflow, Title: "Core Workflow:"},
		&cobra.Group{ID: groupSearch, Title: "Search & Recall:"},
		&cobra.Group{ID: groupInfra, Title: "Infrastructure:"},
		&cobra.Group{ID: groupPlumbing, Title: "Plumbing:"},
	)
	assign := func(g string, cmds ...*cobra.Command) {
		for _, c := range cmds {
			c.GroupID = g
		}
	}
	assign(groupWorkflow, featureCmd, workspaceCmd, artifactCmd, sessionCmd, captureCmd, planCmd)
	assign(groupSearch, findCmd, recentCmd, primeCmd, statusCmd, statsCmd, timelineCmd, entityCmd, suggestDomainCmd, cdCmd)
	assign(groupInfra, worktreeCmd, projectCmd, scopeCmd, archiveCmd, daemonCmd, mcpCmd, doctorCmd, configCmd, versionCmd)
	assign(groupPlumbing, dbCmd)

	rootCmd.AddCommand(dbCmd)
	for _, c := range []*cobra.Command{indexCmd, ingestCmd, embedCmd, backupCmd, accessCmd, tailCmd, watchCmd} {
		rootCmd.RemoveCommand(c)
		dbCmd.AddCommand(c)
		rootCmd.AddCommand(forwarderCmd(c.Name(), "db"))
	}

	rootCmd.SetHelpCommandGroupID(groupPlumbing)
	rootCmd.SetCompletionCommandGroupID(groupPlumbing)
}

// forwarderCmd keeps a moved command's old invocation path working by
// re-execing the binary with the new path prepended.
func forwarderCmd(name, newParent string) *cobra.Command {
	return &cobra.Command{
		Use:                name,
		Hidden:             true,
		Deprecated:         fmt.Sprintf("moved — use `giantmem %s %s`", newParent, name),
		DisableFlagParsing: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			exe, err := os.Executable()
			if err != nil {
				return err
			}
			argv := append([]string{exe, newParent, name}, args...)
			return syscall.Exec(exe, argv, os.Environ())
		},
	}
}

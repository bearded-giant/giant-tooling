package cmd

import (
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
)

var (
	flagArchiveBase string
	flagVerbose     bool
)

var rootCmd = &cobra.Command{
	Use:           "giantmem <command>",
	Short:         "giantmem CLI: search, archive, and manage .giantmem workspaces",
	Long:          "giantmem is a unified CLI for searching, archiving, and managing .giantmem workspace artifacts and Claude Code sessions.",
	SilenceUsage:  true,
	SilenceErrors: false,
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func init() {
	defaultBase := os.Getenv("GIANTMEM_ARCHIVE_BASE")
	if defaultBase == "" {
		home, _ := os.UserHomeDir()
		defaultBase = filepath.Join(home, "giantmem_archive")
	}
	rootCmd.PersistentFlags().StringVar(&flagArchiveBase, "archive-base", defaultBase, "archive root (env: GIANTMEM_ARCHIVE_BASE)")
	rootCmd.PersistentFlags().BoolVarP(&flagVerbose, "verbose", "v", false, "verbose output")

	rootCmd.AddCommand(findCmd)
	rootCmd.AddCommand(statsCmd)
	rootCmd.AddCommand(versionCmd)
}

func archiveDBPath() string  { return filepath.Join(flagArchiveBase, "archives.db") }
func liveDBPath() string     { return filepath.Join(flagArchiveBase, "live.db") }
func archiveBasePath() string { return flagArchiveBase }

package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var (
	Version   = "0.1.0-dev"
	Commit    = "unknown"
	BuildDate = "unknown"
)

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Show giantmem version",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Printf("giantmem %s (%s, built %s)\n", Version, Commit, BuildDate)
	},
}

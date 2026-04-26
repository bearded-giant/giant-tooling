package cmd

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/bryangrimes/gm/internal/project"
	"github.com/spf13/cobra"
)

var (
	captureFeature string
	captureGlobal  bool
)

var captureCmd = &cobra.Command{
	Use:   "capture [text...]",
	Short: "Append a timestamped note to the active feature's notes.md (or .giantmem/notes.md)",
	Long: `Quick brain-dump entry point. Reads from args or stdin and appends a
timestamped block to the active feature's notes.md, or .giantmem/notes.md if
no feature is active.

Format:
  ## 2026-04-26 14:33  [session: 40503b40]
  <content>

Examples:
  giantmem capture "idea: speed up ingest with prepared stmts"
  echo "todo: fix worktree detect" | giantmem capture
  giantmem capture --feature better-search "spec: ..."
  giantmem capture --global "global note"`,
	RunE: func(cmd *cobra.Command, args []string) error {
		text := strings.TrimSpace(strings.Join(args, " "))
		if text == "" {
			// read stdin
			b, err := io.ReadAll(bufio.NewReader(os.Stdin))
			if err != nil {
				return err
			}
			text = strings.TrimSpace(string(b))
		}
		if text == "" {
			return fmt.Errorf("no text provided (args or stdin)")
		}

		cwd, _ := os.Getwd()
		info := project.Detect(cwd, archiveBasePath())
		feature := captureFeature
		if feature == "" && !captureGlobal {
			feature = project.FeatureFromGiantmem(info.WorktreePath)
		}

		var target string
		if captureGlobal || feature == "" {
			target = filepath.Join(info.WorktreePath, ".giantmem", "notes.md")
		} else {
			target = filepath.Join(info.WorktreePath, ".giantmem", "features", feature, "notes.md")
		}

		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}

		sid := os.Getenv("CLAUDE_SESSION_ID")
		header := time.Now().Format("## 2006-01-02 15:04")
		if sid != "" && len(sid) >= 8 {
			header += "  [session: " + sid[:8] + "]"
		}
		block := "\n" + header + "\n" + text + "\n"

		f, err := os.OpenFile(target, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			return err
		}
		_, err = f.WriteString(block)
		f.Close()
		if err != nil {
			return err
		}

		scope := "global"
		if feature != "" {
			scope = "feature " + feature
		}
		fmt.Fprintf(os.Stderr, "captured (%s) -> %s\n", scope, target)
		return nil
	},
}

func init() {
	captureCmd.Flags().StringVarP(&captureFeature, "feature", "f", "", "force a specific feature target")
	captureCmd.Flags().BoolVarP(&captureGlobal, "global", "g", false, "force .giantmem/notes.md (skip active feature)")
	rootCmd.AddCommand(captureCmd)
}

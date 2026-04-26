package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"text/tabwriter"

	archive "github.com/bearded-giant/giant-tooling/giantmem/internal/archiver"
	"github.com/spf13/cobra"
)

var worktreeCmd = &cobra.Command{
	Use:   "worktree",
	Short: "Worktree lifecycle: setup wizards, list, status, remove (auto-archives .giantmem)",
	Long: `Setup wizards plus reporting and lifecycle commands for the
bare-with-worktrees layout.

Per-project shortcut functions (e.g. cwt, cwtl, cwtr) live in your shell. Run
"giantmem worktree shell-init" to print the bashrc snippet that binds them.`,
}

var (
	worktreeRemoveDryRun bool
	worktreeRemoveForce  bool
	worktreeRemoveKeep   bool
)

var worktreeListCmd = &cobra.Command{
	Use:   "list [bare-repo-dir]",
	Short: "List worktrees and their .giantmem status",
	Long: `Run from inside any worktree, OR pass the bare repo dir explicitly.
Lists worktrees plus whether each has a live .giantmem/ directory.`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		dir, _ := os.Getwd()
		if len(args) > 0 {
			dir = args[0]
		}
		out, err := exec.Command("git", "-C", dir, "worktree", "list", "--porcelain").Output()
		if err != nil {
			return fmt.Errorf("git worktree list: %w", err)
		}
		entries := parseWorktreeList(string(out))
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "BRANCH\tHEAD\tGIANTMEM\tPATH")
		for _, e := range entries {
			gm := "—"
			gmPath := filepath.Join(e.path, ".giantmem")
			if dirExists(gmPath) {
				gm = "live"
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", e.branch, e.head, gm, e.path)
		}
		return w.Flush()
	},
}

var worktreeRemoveCmd = &cobra.Command{
	Use:   "remove <worktree-path>",
	Short: "Archive .giantmem then `git worktree remove`",
	Long: `Auto-archives the worktree's .giantmem (if any) before deleting the worktree.
Order: gm archive run --no-reinit -> git worktree remove [--force].

Use --keep to skip archive (just removes worktree).`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		wt, err := filepath.Abs(args[0])
		if err != nil {
			return err
		}
		if !dirExists(wt) {
			return fmt.Errorf("worktree path not found: %s", wt)
		}
		gm := filepath.Join(wt, ".giantmem")

		if !worktreeRemoveKeep && dirExists(gm) {
			fmt.Println("== auto-archiving .giantmem ==")
			if _, err := archive.Run(gm, archiveBasePath(), "", worktreeRemoveDryRun, false); err != nil {
				if !worktreeRemoveForce {
					return fmt.Errorf("archive failed; use --force to remove anyway: %w", err)
				}
				fmt.Fprintf(os.Stderr, "warn: archive failed (--force given): %v\n", err)
			}
		}

		fmt.Println("== git worktree remove ==")
		gitArgs := []string{"worktree", "remove"}
		if worktreeRemoveForce {
			gitArgs = append(gitArgs, "--force")
		}
		gitArgs = append(gitArgs, wt)
		if worktreeRemoveDryRun {
			fmt.Printf("(dry run) git %s\n", strings.Join(gitArgs, " "))
			return nil
		}
		c := exec.Command("git", gitArgs...)
		c.Stdout = os.Stdout
		c.Stderr = os.Stderr
		if err := c.Run(); err != nil {
			return fmt.Errorf("git worktree remove: %w", err)
		}
		fmt.Println("done")
		return nil
	},
}

type wtEntry struct {
	path   string
	branch string
	head   string
}

func parseWorktreeList(s string) []wtEntry {
	var out []wtEntry
	var cur wtEntry
	flush := func() {
		if cur.path != "" {
			out = append(out, cur)
		}
		cur = wtEntry{}
	}
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			flush()
			continue
		}
		switch {
		case strings.HasPrefix(line, "worktree "):
			flush()
			cur.path = strings.TrimPrefix(line, "worktree ")
		case strings.HasPrefix(line, "HEAD "):
			cur.head = strings.TrimPrefix(line, "HEAD ")
			if len(cur.head) > 8 {
				cur.head = cur.head[:8]
			}
		case strings.HasPrefix(line, "branch "):
			cur.branch = strings.TrimPrefix(line, "branch refs/heads/")
		case line == "bare":
			cur.branch = "(bare)"
		}
	}
	flush()
	return out
}

// worktreeCorePath returns the path to worktree-core.sh.
func worktreeCorePath() string {
	home, _ := os.UserHomeDir()
	candidates := []string{
		filepath.Join(home, "dev", "giant-tooling", "git-worktrees", "worktree-core.sh"),
		filepath.Join(home, ".claude", "lib", "worktrees", "worktree-core.sh"),
	}
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}
	return candidates[0]
}

// runWorktreeFunc sources worktree-core.sh and invokes the named function.
func runWorktreeFunc(fn string, args []string) error {
	core := worktreeCorePath()
	if _, err := os.Stat(core); err != nil {
		return fmt.Errorf("worktree-core.sh not found at %s", core)
	}
	quoted := make([]string, len(args))
	for i, a := range args {
		quoted[i] = bashQuote(a)
	}
	cmdline := fmt.Sprintf("source %q && %s %s", core, fn, strings.Join(quoted, " "))
	c := exec.Command("bash", "-c", cmdline)
	c.Stdin = os.Stdin
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	c.Env = os.Environ()
	return c.Run()
}

var worktreeInitCmd = &cobra.Command{
	Use:                "init",
	Short:              "Wizard for a fresh worktree project",
	DisableFlagParsing: true,
	RunE: func(cmd *cobra.Command, args []string) error { return runWorktreeFunc("wt_init", args) },
}

var worktreeAdoptCmd = &cobra.Command{
	Use:                "adopt [path]",
	Short:              "Convert an existing repo to bare-with-worktrees layout",
	DisableFlagParsing: true,
	RunE: func(cmd *cobra.Command, args []string) error { return runWorktreeFunc("wt_adopt", args) },
}

var worktreeProjectsCmd = &cobra.Command{
	Use:                "projects",
	Short:              "List all registered worktree projects",
	DisableFlagParsing: true,
	RunE: func(cmd *cobra.Command, args []string) error { return runWorktreeFunc("wt_projects", args) },
}

var worktreeStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "git status across all worktrees in the current bare",
	RunE: func(cmd *cobra.Command, args []string) error {
		dir, _ := os.Getwd()
		out, err := exec.Command("git", "-C", dir, "worktree", "list", "--porcelain").Output()
		if err != nil {
			return fmt.Errorf("git worktree list: %w", err)
		}
		entries := parseWorktreeList(string(out))
		for _, e := range entries {
			if e.branch == "(bare)" {
				continue
			}
			fmt.Printf("== %s (%s) ==\n", e.branch, e.path)
			c := exec.Command("git", "-C", e.path, "status", "--short", "--branch")
			c.Stdout = os.Stdout
			c.Stderr = os.Stderr
			_ = c.Run()
		}
		return nil
	},
}

var worktreeBranchesCmd = &cobra.Command{
	Use:   "branches",
	Short: "List branches in the current bare (git branch -a)",
	RunE: func(cmd *cobra.Command, args []string) error {
		dir, _ := os.Getwd()
		c := exec.Command("git", "-C", dir, "branch", "-a")
		c.Stdout = os.Stdout
		c.Stderr = os.Stderr
		return c.Run()
	},
}

var worktreePruneCmd = &cobra.Command{
	Use:   "prune",
	Short: "git worktree prune (clean up stale metadata)",
	RunE: func(cmd *cobra.Command, args []string) error {
		dir, _ := os.Getwd()
		c := exec.Command("git", "-C", dir, "worktree", "prune", "-v")
		c.Stdout = os.Stdout
		c.Stderr = os.Stderr
		return c.Run()
	},
}

var worktreeRepairCmd = &cobra.Command{
	Use:   "repair",
	Short: "git worktree repair (fix broken admin files after a move)",
	RunE: func(cmd *cobra.Command, args []string) error {
		dir, _ := os.Getwd()
		c := exec.Command("git", "-C", dir, "worktree", "repair")
		c.Stdout = os.Stdout
		c.Stderr = os.Stderr
		return c.Run()
	},
}

var (
	shellInitInstall bool
	shellInitTarget  string
	shellInitDryRun  bool
)

var worktreeShellInitCmd = &cobra.Command{
	Use:   "shell-init",
	Short: "Print (or --install) the bashrc/zshrc snippet that sources worktree-core.sh and binds gj()",
	RunE: func(cmd *cobra.Command, args []string) error {
		snippet := buildShellSnippet()
		if !shellInitInstall {
			fmt.Print(snippet)
			return nil
		}
		target := shellInitTarget
		if target == "" {
			target = pickShellRC()
		}
		return installSnippet(target, snippet, shellInitDryRun)
	},
}

const shellSentinelOpen = "# >>> giantmem shell-init >>>"
const shellSentinelClose = "# <<< giantmem shell-init <<<"

func buildShellSnippet() string {
	core := worktreeCorePath()
	body := fmt.Sprintf(`%s
# Sources worktree-core.sh and binds gj() (fuzzy-jump to a worktree).
source %q

gj() {
  local target
  target=$(giantmem cd "$@") || return $?
  cd "$target"
}
%s
`, shellSentinelOpen, core, shellSentinelClose)
	return body
}

func pickShellRC() string {
	home, _ := os.UserHomeDir()
	shell := os.Getenv("SHELL")
	if strings.Contains(shell, "zsh") {
		return filepath.Join(home, ".zshrc")
	}
	return filepath.Join(home, ".bashrc")
}

func installSnippet(target, snippet string, dryRun bool) error {
	existing, err := os.ReadFile(target)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	body := string(existing)

	openIdx := strings.Index(body, shellSentinelOpen)
	closeIdx := strings.Index(body, shellSentinelClose)
	if openIdx >= 0 && closeIdx > openIdx {
		// replace existing block
		newBody := body[:openIdx] + strings.TrimRight(snippet, "\n") + body[closeIdx+len(shellSentinelClose):]
		if dryRun {
			fmt.Printf("would replace existing block in %s\n", target)
			return nil
		}
		if err := os.WriteFile(target, []byte(newBody), 0o644); err != nil {
			return err
		}
		fmt.Printf("updated existing block in %s\n", target)
		return nil
	}
	// append
	if dryRun {
		fmt.Printf("would append %d lines to %s\n", strings.Count(snippet, "\n"), target)
		return nil
	}
	if !strings.HasSuffix(body, "\n") && body != "" {
		body += "\n"
	}
	body += "\n" + snippet
	if err := os.WriteFile(target, []byte(body), 0o644); err != nil {
		return err
	}
	fmt.Printf("appended block to %s\n", target)
	fmt.Println("source it now with:  source", target)
	return nil
}

func init() {
	worktreeRemoveCmd.Flags().BoolVar(&worktreeRemoveDryRun, "dry-run", false, "show planned actions")
	worktreeRemoveCmd.Flags().BoolVar(&worktreeRemoveForce, "force", false, "force git worktree remove and continue on archive failure")
	worktreeRemoveCmd.Flags().BoolVar(&worktreeRemoveKeep, "keep", false, "skip archive, just remove worktree")

	worktreeShellInitCmd.Flags().BoolVar(&shellInitInstall, "install", false, "append/update sentinel block in target rc file")
	worktreeShellInitCmd.Flags().StringVar(&shellInitTarget, "target", "", "rc file to install into (default: $SHELL-aware ~/.bashrc or ~/.zshrc)")
	worktreeShellInitCmd.Flags().BoolVar(&shellInitDryRun, "dry-run", false, "show what install would do, change nothing")

	worktreeCmd.AddCommand(worktreeListCmd)
	worktreeCmd.AddCommand(worktreeRemoveCmd)
	worktreeCmd.AddCommand(worktreeInitCmd)
	worktreeCmd.AddCommand(worktreeAdoptCmd)
	worktreeCmd.AddCommand(worktreeProjectsCmd)
	worktreeCmd.AddCommand(worktreeStatusCmd)
	worktreeCmd.AddCommand(worktreeBranchesCmd)
	worktreeCmd.AddCommand(worktreePruneCmd)
	worktreeCmd.AddCommand(worktreeRepairCmd)
	worktreeCmd.AddCommand(worktreeShellInitCmd)
	rootCmd.AddCommand(worktreeCmd)
}

package cmd

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"
)

var (
	planTailLines int
	planRoots     []string
	planProject   string
)

var planCmd = &cobra.Command{
	Use:   "plan",
	Short: "Aggregate plans/current.md across worktrees -- 'what am I in the middle of?'",
}

var planListCmd = &cobra.Command{
	Use:   "list",
	Short: "Show current plans across all live workspaces",
	RunE: func(cmd *cobra.Command, args []string) error {
		roots := planRoots
		if len(roots) == 0 {
			home, _ := os.UserHomeDir()
			roots = []string{filepath.Join(home, "dev")}
		}
		plans := scanPlans(roots, planProject)
		if len(plans) == 0 {
			fmt.Fprintln(os.Stderr, "no current plans found")
			return nil
		}
		sort.Slice(plans, func(i, j int) bool { return plans[i].mtime > plans[j].mtime })
		for _, p := range plans {
			fmt.Printf("== %s ==\n", p.label)
			fmt.Printf("   %s\n\n", p.path)
			lines := splitTail(p.body, planTailLines)
			for _, l := range lines {
				fmt.Printf("   %s\n", l)
			}
			fmt.Println()
		}
		return nil
	},
}

type planEntry struct {
	label string
	path  string
	body  string
	mtime int64
}

func scanPlans(roots []string, projectFilter string) []planEntry {
	var out []planEntry
	for _, root := range roots {
		filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if d.IsDir() {
				name := d.Name()
				if name == "node_modules" || name == ".git" || name == ".venv" || name == "venv" {
					return fs.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(p, "current.md") {
				return nil
			}
			// must be inside .giantmem/plans/ or .giantmem/features/<x>/plans/
			if !strings.Contains(p, "/.giantmem/") || !strings.Contains(p, "/plans/") {
				return nil
			}
			label := makePlanLabel(p)
			if projectFilter != "" && !strings.Contains(strings.ToLower(label), strings.ToLower(projectFilter)) {
				return nil
			}
			info, err := d.Info()
			if err != nil {
				return nil
			}
			body, err := os.ReadFile(p)
			if err != nil {
				return nil
			}
			out = append(out, planEntry{
				label: label,
				path:  p,
				body:  string(body),
				mtime: info.ModTime().Unix(),
			})
			return nil
		})
	}
	return out
}

// makePlanLabel returns "<project> [feature]" for a plan path.
func makePlanLabel(p string) string {
	idx := strings.Index(p, "/.giantmem/")
	if idx < 0 {
		return filepath.Dir(p)
	}
	worktree := p[:idx]
	project := filepath.Base(worktree)
	if strings.Contains(p, "/.giantmem/features/") {
		// .giantmem/features/<name>/plans/current.md
		rest := p[idx+len("/.giantmem/features/"):]
		parts := strings.SplitN(rest, "/", 2)
		if len(parts) > 0 && parts[0] != "" {
			return fmt.Sprintf("%s [%s]", project, parts[0])
		}
	}
	return project
}

func splitTail(body string, n int) []string {
	lines := strings.Split(strings.TrimRight(body, "\n"), "\n")
	if n > 0 && len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return lines
}

func init() {
	planListCmd.Flags().IntVarP(&planTailLines, "lines", "n", 30, "tail N lines per plan (0 = all)")
	planListCmd.Flags().StringSliceVar(&planRoots, "root", nil, "roots to scan (default ~/dev)")
	planListCmd.Flags().StringVarP(&planProject, "project", "p", "", "filter labels (LIKE)")
	planCmd.AddCommand(planListCmd)
	rootCmd.AddCommand(planCmd)
}

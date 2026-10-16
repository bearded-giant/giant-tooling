package cmd

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/bearded-giant/giant-tooling/giantmem/internal/db"
	"github.com/bearded-giant/giant-tooling/giantmem/internal/mrs"
	"github.com/bearded-giant/giant-tooling/giantmem/internal/output"
	"github.com/bearded-giant/giant-tooling/giantmem/internal/search"
	"github.com/spf13/cobra"
)

var (
	mrJSON     bool
	mrNoStatus bool
	mrProject  string
	mrSince    string
	mrAll      bool
)

var mrCmd = &cobra.Command{
	Use:   "mr",
	Short: "MRs/PRs that Claude sessions opened or touched: which session, which worktree, live status",
}

var mrWhoseCmd = &cobra.Command{
	Use:   "whose <ref>",
	Short: "Find the session and worktree behind an MR/PR (URL, repo!N, repo#N, or bare !N to list every repo)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		a, err := db.Open(archiveDBPath())
		if err != nil {
			return err
		}
		defer a.Close()
		list, err := loadMRs(cmd.Context(), a, mrs.Filter{Ref: args[0]}, !mrNoStatus, true)
		if err != nil {
			return err
		}
		if mrJSON {
			return output.JSON(list)
		}
		if len(list) == 0 {
			return fmt.Errorf("no session opened or touched %s", args[0])
		}
		if len(list) > 1 {
			fmt.Printf("%d MRs match %s\n\n", len(list), args[0])
		}
		for i, m := range list {
			if i > 0 {
				fmt.Println()
			}
			printWhose(m)
		}
		return nil
	},
}

var mrListCmd = &cobra.Command{
	Use:   "list",
	Short: "Running list of MRs/PRs opened or touched by sessions, open ones by default",
	RunE: func(cmd *cobra.Command, args []string) error {
		a, err := db.Open(archiveDBPath())
		if err != nil {
			return err
		}
		defer a.Close()
		f := mrs.Filter{Project: mrProject}
		if mrSince != "" {
			t, err := search.ParseSince(mrSince)
			if err != nil {
				return err
			}
			f.Since = t.UTC().Format(time.RFC3339)
		}
		list, err := loadMRs(cmd.Context(), a, f, !mrNoStatus, mrJSON)
		if err != nil {
			return err
		}
		if !mrAll {
			keep := list[:0]
			for _, m := range list {
				if !m.Status.Terminal() {
					keep = append(keep, m)
				}
			}
			list = keep
		}
		if mrJSON {
			return output.JSON(list)
		}
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "MR\tSTATE\tTITLE\tBRANCH\tOPENER\tDIR\tURL")
		touched := false
		for _, m := range list {
			title, branch := "", ""
			if m.Status != nil {
				title, branch = m.Status.Title, m.Status.SourceBranch
			}
			opener := m.Opener.SessionID[:min(8, len(m.Opener.SessionID))]
			if m.Opener.Role != "opened" {
				opener = "~" + opener
				touched = true
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n", m.Label, statusCell(m.Status), clipText(title, 40), branch, opener, tildePath(m.Opener.Cwd), m.URL)
		}
		if err := w.Flush(); err != nil {
			return err
		}
		if touched {
			fmt.Println("\n~ no create call seen: first session that touched it")
		}
		return nil
	},
}

func loadMRs(ctx context.Context, a *sql.DB, f mrs.Filter, live, checkouts bool) ([]mrs.MR, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	list, err := mrs.Find(a, f)
	if err != nil {
		return nil, err
	}
	mrs.Refresh(ctx, a, list, live)
	if checkouts {
		mrs.Checkouts(ctx, a, list)
	}
	return list, nil
}

func printWhose(m mrs.MR) {
	title := ""
	if m.Status != nil {
		title = m.Status.Title
	}
	fmt.Printf("%s  %s  %s\n", m.Label, statusCell(m.Status), title)
	fmt.Printf("  url       %s\n", m.URL)
	if m.Status != nil && m.Status.SourceBranch != "" {
		fmt.Printf("  branch    %s\n", m.Status.SourceBranch)
		if !m.Status.Terminal() {
			co := "none found"
			if len(m.Checkout) > 0 {
				co = strings.Join(m.Checkout, ", ")
			}
			fmt.Printf("  checkout  %s\n", co)
		}
	}
	for _, s := range m.Sessions {
		role := s.Role
		if s.Role != "opened" && s.SessionID == m.Opener.SessionID && s.TS == m.Opener.TS {
			role = "first touched"
		}
		ran := ""
		if s.RanIn != "" {
			ran = "  (ran in " + tildePath(s.RanIn) + ")"
		}
		fmt.Printf("  %-9s %s  %s  %s%s\n", role, s.SessionID[:min(8, len(s.SessionID))], shortTS(s.TS), tildePath(s.Cwd), ran)
		if s.Resume != "" {
			fmt.Printf("            %s\n", s.Resume)
		}
	}
}

func statusCell(s *mrs.Status) string {
	out := s.Show()
	if s != nil && s.Error != "" {
		out += "!"
	} else if s != nil && s.Cached && !s.Terminal() {
		out += "*"
	}
	return out
}

func shortTS(ts string) string {
	t, err := time.Parse(time.RFC3339, ts)
	if err != nil {
		return ts
	}
	return t.Local().Format("2006-01-02 15:04")
}

func tildePath(p string) string {
	if home, err := os.UserHomeDir(); err == nil && strings.HasPrefix(p, home+"/") {
		return "~" + p[len(home):]
	}
	return p
}

func clipText(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

func init() {
	mrCmd.PersistentFlags().BoolVar(&mrJSON, "json", false, "JSON output")
	mrCmd.PersistentFlags().BoolVar(&mrNoStatus, "no-status", false, "skip the live glab/gh status read; use cached state")
	mrListCmd.Flags().StringVarP(&mrProject, "project", "p", "", "repo name or session project substring")
	mrListCmd.Flags().StringVar(&mrSince, "since", "30d", "only MRs a session linked since (e.g. 7d, 2026-10-01); empty for all")
	mrListCmd.Flags().BoolVar(&mrAll, "all", false, "include merged and closed")
	mrCmd.AddCommand(mrWhoseCmd, mrListCmd)
	rootCmd.AddCommand(mrCmd)
}

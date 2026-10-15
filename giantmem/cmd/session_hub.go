package cmd

import (
	"database/sql"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/bearded-giant/giant-tooling/giantmem/internal/db"
	"github.com/bearded-giant/giant-tooling/giantmem/internal/hub"
	"github.com/bearded-giant/giant-tooling/giantmem/internal/output"
	"github.com/spf13/cobra"
)

func openArchiveSession(prefix string) (*sql.DB, sessRow, error) {
	a, err := db.Open(archiveDBPath())
	if err != nil {
		return nil, sessRow{}, err
	}
	r, err := lookupSession(a, prefix)
	if err != nil {
		a.Close()
		return nil, sessRow{}, err
	}
	return a, r, nil
}

var (
	hubTool    string
	hubAgent   string
	hubErrors  bool
	hubKind    string
	hubProject string
	hubLimit   int
)

var sessToolsCmd = &cobra.Command{
	Use:   "tools <id-prefix>",
	Short: "List a session's tool calls, subagents included",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		a, r, err := openArchiveSession(args[0])
		if err != nil {
			return err
		}
		defer a.Close()
		hits, err := hub.QueryToolUses(a, hub.ToolQuery{SessionID: r.ID, Tool: hubTool, Agent: hubAgent, Errors: hubErrors})
		if err != nil {
			return err
		}
		if sessJSON {
			return output.JSON(hits)
		}
		printToolUses(hits, false)
		return nil
	},
}

var sessFilesCmd = &cobra.Command{
	Use:   "files <id-prefix>",
	Short: "List a session's archived working files (scratchpad, task output, subagents, workflows)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		a, r, err := openArchiveSession(args[0])
		if err != nil {
			return err
		}
		a.Close()
		m, err := hub.LoadBundle(archiveBasePath(), r.ID)
		if err != nil {
			return err
		}
		if hubKind != "" {
			var keep []hub.BundleFile
			for _, f := range m.Files {
				if f.Kind == hubKind {
					keep = append(keep, f)
				}
			}
			m.Files = keep
		}
		if sessJSON {
			return output.JSON(m)
		}
		fmt.Printf("bundle  %s\n", m.Dir)
		if m.FirstPrompt != "" {
			fmt.Printf("prompt  %s\n", m.FirstPrompt)
		}
		fmt.Println()
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "KIND\tSIZE\tPATH")
		for _, f := range m.Files {
			fmt.Fprintf(w, "%s\t%d\t%s\n", f.Kind, f.Size, f.AbsPath)
		}
		return w.Flush()
	},
}

var sessLinksCmd = &cobra.Command{
	Use:   "links <id-prefix>",
	Short: "List what a session produced: artifacts, vault notes, handoffs, doit items, MRs",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		a, r, err := openArchiveSession(args[0])
		if err != nil {
			return err
		}
		defer a.Close()
		links, err := hub.QueryLinks(a, r.ID)
		if err != nil {
			return err
		}
		if sessJSON {
			return output.JSON(links)
		}
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "KIND\tLABEL\tTARGET")
		for _, l := range links {
			fmt.Fprintf(w, "%s\t%s\t%s\n", l.Kind, l.Label, l.Target)
		}
		return w.Flush()
	},
}

var toolsCmd = &cobra.Command{
	Use:   "tools",
	Short: "Search tool calls across every session and subagent",
}

var toolsFindCmd = &cobra.Command{
	Use:   "find <query>",
	Short: "FTS5 search over tool names, summaries, and inputs (e.g. Bash commands)",
	Args:  cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		a, err := db.Open(archiveDBPath())
		if err != nil {
			return err
		}
		defer a.Close()
		hits, err := hub.QueryToolUses(a, hub.ToolQuery{
			Match:   strings.Join(args, " "),
			Tool:    hubTool,
			Project: hubProject,
			Errors:  hubErrors,
			Limit:   hubLimit,
		})
		if err != nil {
			return err
		}
		if sessJSON {
			return output.JSON(hits)
		}
		printToolUses(hits, true)
		return nil
	},
}

func printToolUses(hits []hub.ToolUse, withSession bool) {
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	if withSession {
		fmt.Fprintln(w, "SESSION\tPROJECT\tAGENT\tTOOL\tSUMMARY")
	} else {
		fmt.Fprintln(w, "SEQ\tAGENT\tTOOL\tSUMMARY")
	}
	for _, h := range hits {
		agent := "main"
		if h.AgentID != "" {
			agent = h.AgentID
			if len(agent) > 10 {
				agent = agent[:10]
			}
		}
		summary := h.Summary
		if h.IsError {
			summary = "[error] " + summary
		}
		if len(summary) > 140 {
			summary = summary[:140] + "..."
		}
		if withSession {
			sid := h.SessionID
			if len(sid) > 8 {
				sid = sid[:8]
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", sid, h.Project, agent, h.Tool, summary)
		} else {
			fmt.Fprintf(w, "%d\t%s\t%s\t%s\n", h.Seq, agent, h.Tool, summary)
		}
	}
	w.Flush()
}

func init() {
	sessToolsCmd.Flags().StringVar(&hubTool, "tool", "", "only this tool (SQL LIKE, e.g. Bash or mcp__doit__%)")
	sessToolsCmd.Flags().StringVar(&hubAgent, "agent", "", "main = main thread only, or one subagent id")
	sessToolsCmd.Flags().BoolVar(&hubErrors, "errors", false, "only calls whose result was an error")
	sessToolsCmd.Flags().BoolVar(&sessJSON, "json", false, "emit JSON")
	sessFilesCmd.Flags().StringVar(&hubKind, "kind", "", "only this kind: script, patch, doc, data, log, workflow, transcript")
	sessFilesCmd.Flags().BoolVar(&sessJSON, "json", false, "emit JSON")
	sessLinksCmd.Flags().BoolVar(&sessJSON, "json", false, "emit JSON")
	toolsFindCmd.Flags().StringVar(&hubTool, "tool", "", "only this tool (SQL LIKE)")
	toolsFindCmd.Flags().StringVarP(&hubProject, "project", "p", "", "project substring")
	toolsFindCmd.Flags().BoolVar(&hubErrors, "errors", false, "only calls whose result was an error")
	toolsFindCmd.Flags().IntVarP(&hubLimit, "limit", "n", 30, "max rows")
	toolsFindCmd.Flags().BoolVar(&sessJSON, "json", false, "emit JSON")
	sessionCmd.AddCommand(sessToolsCmd, sessFilesCmd, sessLinksCmd)
	toolsCmd.AddCommand(toolsFindCmd)
	rootCmd.AddCommand(toolsCmd)
}

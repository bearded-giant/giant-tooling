package cmd

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"github.com/bearded-giant/giant-tooling/giantmem/internal/db"
	"github.com/bearded-giant/giant-tooling/giantmem/internal/output"
	"github.com/bearded-giant/giant-tooling/giantmem/internal/search"
	"github.com/spf13/cobra"
)

type toolUseRow struct {
	SessionID string `json:"session_id"`
	AgentID   string `json:"agent_id,omitempty"`
	Project   string `json:"project"`
	Seq       int    `json:"seq"`
	TS        string `json:"ts,omitempty"`
	Tool      string `json:"tool"`
	Summary   string `json:"summary"`
	IsError   bool   `json:"is_error,omitempty"`
}

type linkRow struct {
	Kind   string `json:"kind"`
	Target string `json:"target"`
	Label  string `json:"label,omitempty"`
	TS     string `json:"ts,omitempty"`
}

type bundleFile struct {
	Path string `json:"path"`
	Size int64  `json:"size"`
	Kind string `json:"kind"`
}

type bundleManifest struct {
	SessionID   string       `json:"session_id"`
	Cwd         string       `json:"cwd,omitempty"`
	Branch      string       `json:"branch,omitempty"`
	FirstPrompt string       `json:"first_prompt,omitempty"`
	Updated     string       `json:"updated,omitempty"`
	Resume      string       `json:"resume,omitempty"`
	Files       []bundleFile `json:"files"`
	Dir         string       `json:"dir"`
}

type toolQuery struct {
	SessionID string
	Match     string
	Tool      string
	Project   string
	Agent     string // "" all, "main" main thread only, else one agent id
	Errors    bool
	Limit     int
}

func queryToolUses(d *sql.DB, q toolQuery) ([]toolUseRow, error) {
	from := "tool_uses t"
	var conds []string
	var args []any
	if q.Match != "" {
		from = "tool_uses_fts f JOIN tool_uses t ON t.id = f.rowid"
		conds = append(conds, "tool_uses_fts MATCH ?")
		args = append(args, search.SanitizeFTSQuery(q.Match))
	}
	if q.SessionID != "" {
		conds = append(conds, "t.session_id = ?")
		args = append(args, q.SessionID)
	}
	if q.Tool != "" {
		conds = append(conds, "t.tool LIKE ?")
		args = append(args, q.Tool)
	}
	if q.Project != "" {
		conds = append(conds, "t.project LIKE ?")
		args = append(args, "%"+q.Project+"%")
	}
	switch q.Agent {
	case "":
	case "main":
		conds = append(conds, "t.agent_id = ''")
	default:
		conds = append(conds, "t.agent_id = ?")
		args = append(args, q.Agent)
	}
	if q.Errors {
		conds = append(conds, "t.is_error = 1")
	}
	where := ""
	if len(conds) > 0 {
		where = "WHERE " + strings.Join(conds, " AND ")
	}
	order := "t.ts DESC, t.seq DESC"
	switch {
	case q.SessionID != "":
		order = "t.agent_id, t.seq"
	case q.Match != "":
		order = "bm25(tool_uses_fts), t.ts DESC"
	}
	sqlq := fmt.Sprintf(`SELECT t.session_id, t.agent_id, t.project, t.seq, COALESCE(t.ts,''),
                                t.tool, t.summary, t.is_error
                           FROM %s %s ORDER BY %s`, from, where, order)
	if q.Limit > 0 {
		sqlq += " LIMIT ?"
		args = append(args, q.Limit)
	}
	rows, err := d.Query(sqlq, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []toolUseRow
	for rows.Next() {
		var r toolUseRow
		var isErr int
		if err := rows.Scan(&r.SessionID, &r.AgentID, &r.Project, &r.Seq, &r.TS, &r.Tool, &r.Summary, &isErr); err != nil {
			return nil, err
		}
		r.IsError = isErr == 1
		out = append(out, r)
	}
	return out, rows.Err()
}

func queryLinks(d *sql.DB, sessionID string) ([]linkRow, error) {
	rows, err := d.Query(`SELECT kind, target, COALESCE(label,''), COALESCE(ts,'')
                            FROM session_links WHERE session_id = ? ORDER BY ts, kind`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []linkRow
	for rows.Next() {
		var r linkRow
		if err := rows.Scan(&r.Kind, &r.Target, &r.Label, &r.TS); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// loadBundle finds <archive>/bundles/<slug>/<sid>/bundle.json; the slug is not
// stored with the session row, so glob for it.
func loadBundle(sessionID string) (*bundleManifest, error) {
	matches, _ := filepath.Glob(filepath.Join(archiveBasePath(), "bundles", "*", sessionID, "bundle.json"))
	if len(matches) == 0 {
		return nil, fmt.Errorf("no bundle for session %s", sessionID)
	}
	raw, err := os.ReadFile(matches[0])
	if err != nil {
		return nil, err
	}
	var m bundleManifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("%s: %w", matches[0], err)
	}
	m.Dir = filepath.Dir(matches[0])
	return &m, nil
}

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
		hits, err := queryToolUses(a, toolQuery{SessionID: r.ID, Tool: hubTool, Agent: hubAgent, Errors: hubErrors})
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
		m, err := loadBundle(r.ID)
		if err != nil {
			return err
		}
		if hubKind != "" {
			var keep []bundleFile
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
			fmt.Fprintf(w, "%s\t%d\t%s\n", f.Kind, f.Size, filepath.Join(m.Dir, f.Path))
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
		links, err := queryLinks(a, r.ID)
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
		hits, err := queryToolUses(a, toolQuery{
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

func printToolUses(hits []toolUseRow, withSession bool) {
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

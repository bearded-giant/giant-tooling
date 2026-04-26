package cmd

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/bryangrimes/gm/internal/db"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/spf13/cobra"
)

var mcpCmd = &cobra.Command{
	Use:   "mcp",
	Short: "MCP server: expose archive search to Claude over stdio",
}

var mcpServeCmd = &cobra.Command{
	Use:   "serve",
	Short: "Run the giantmem MCP stdio server (single tool: search_archive)",
	RunE: func(cmd *cobra.Command, args []string) error {
		s := server.NewMCPServer(
			"giantmem-search",
			Version,
			server.WithToolCapabilities(false),
		)
		tool := mcp.NewTool("search_archive",
			mcp.WithDescription(`Search across archived workspaces, Claude session transcripts, and domain knowledge.
Supports FTS5 syntax: AND, OR, NOT, "phrases", prefix*. Filter by project, source_type, or topic.`),
			mcp.WithReadOnlyHintAnnotation(true),
			mcp.WithDestructiveHintAnnotation(false),
			mcp.WithIdempotentHintAnnotation(true),
			mcp.WithOpenWorldHintAnnotation(false),
			mcp.WithString("query",
				mcp.Required(),
				mcp.Description("FTS5 search query"),
			),
			mcp.WithString("project",
				mcp.Description(`filter by project (e.g. "cc-wt", "claude-code-config")`),
			),
			mcp.WithString("source_type",
				mcp.Description(`filter by source: "workspace", "session", or "domain"`),
			),
			mcp.WithString("topic",
				mcp.Description(`filter by session topic (e.g. "auth", "api", "bug", "feature")`),
			),
			mcp.WithNumber("limit",
				mcp.Description("max results (default 10)"),
				mcp.Min(1),
				mcp.Max(100),
			),
		)
		s.AddTool(tool, mcp.NewTypedToolHandler(searchHandler))
		registerExtraTools(s)
		return server.ServeStdio(s)
	},
}

func registerExtraTools(s *server.MCPServer) {
	readOnly := []mcp.ToolOption{
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithDestructiveHintAnnotation(false),
		mcp.WithIdempotentHintAnnotation(true),
		mcp.WithOpenWorldHintAnnotation(false),
	}
	// list_sessions
	s.AddTool(
		mcp.NewTool("list_sessions",
			append([]mcp.ToolOption{
				mcp.WithDescription("List recent Claude sessions, ordered newest first."),
				mcp.WithString("project",
					mcp.Description(`filter by project (LIKE match: "chat-orchestrator" matches "dev/ai/chat-orchestrator")`)),
				mcp.WithNumber("limit",
					mcp.Description("max rows (default 20)"),
					mcp.Min(1), mcp.Max(200)),
			}, readOnly...)...,
		),
		mcp.NewTypedToolHandler(listSessionsHandler),
	)
	// get_session_summary
	s.AddTool(
		mcp.NewTool("get_session_summary",
			append([]mcp.ToolOption{
				mcp.WithDescription("Return metadata for a session by id-prefix: project, cwd, topic, timestamp, jsonl path."),
				mcp.WithString("id_prefix",
					mcp.Required(),
					mcp.Description("session id or any unique prefix (e.g. 40503b40)")),
			}, readOnly...)...,
		),
		mcp.NewTypedToolHandler(getSessionSummaryHandler),
	)
	// recent_writes
	s.AddTool(
		mcp.NewTool("recent_writes",
			append([]mcp.ToolOption{
				mcp.WithDescription("List recent live workspace writes (.giantmem/*.md indexed by the PostToolUse hook)."),
				mcp.WithString("project",
					mcp.Description("filter by project (LIKE)")),
				mcp.WithString("since",
					mcp.Description(`time window like "24h", "7d", "30m" (default 24h)`)),
				mcp.WithNumber("limit",
					mcp.Description("max rows (default 30)"),
					mcp.Min(1), mcp.Max(200)),
			}, readOnly...)...,
		),
		mcp.NewTypedToolHandler(recentWritesHandler),
	)
	// feature_status
	s.AddTool(
		mcp.NewTool("feature_status",
			append([]mcp.ToolOption{
				mcp.WithDescription("Return features.json status across active workspaces. Filter by project."),
				mcp.WithString("project",
					mcp.Description("filter by project (LIKE)")),
			}, readOnly...)...,
		),
		mcp.NewTypedToolHandler(featureStatusHandler),
	)
	// workspace_tree
	s.AddTool(
		mcp.NewTool("workspace_tree",
			append([]mcp.ToolOption{
				mcp.WithDescription("Show .giantmem/ subdir layout and file counts per dir_type. Defaults to live_docs aggregate; with worktree_path returns on-disk counts."),
				mcp.WithString("project",
					mcp.Description("filter by project (LIKE)")),
				mcp.WithString("worktree_path",
					mcp.Description("if set, walk that worktree's .giantmem/ on disk")),
			}, readOnly...)...,
		),
		mcp.NewTypedToolHandler(workspaceTreeHandler),
	)
}

type searchArgs struct {
	Query      string  `json:"query"`
	Project    string  `json:"project"`
	SourceType string  `json:"source_type"`
	Topic      string  `json:"topic"`
	Limit      float64 `json:"limit"`
}

type searchHit struct {
	Filepath   string  `json:"filepath"`
	Project    string  `json:"project"`
	SourceType string  `json:"source_type"`
	DirType    string  `json:"dir_type,omitempty"`
	Topic      string  `json:"topic,omitempty"`
	SessionID  string  `json:"session_id,omitempty"`
	Timestamp  string  `json:"timestamp"`
	Score      float64 `json:"score"`
	Snippet    string  `json:"snippet,omitempty"`
}

func searchHandler(ctx context.Context, req mcp.CallToolRequest, args searchArgs) (*mcp.CallToolResult, error) {
	if strings.TrimSpace(args.Query) == "" {
		return mcp.NewToolResultError("query is required"), nil
	}
	limit := int(args.Limit)
	if limit <= 0 {
		limit = 10
	}

	d, err := db.Open(archiveDBPath())
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("open db: %v", err)), nil
	}
	defer d.Close()

	hits, err := mcpSearch(d, args.Query, args.Project, args.SourceType, args.Topic, limit)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	out := map[string]any{
		"results": hits,
		"total":   len(hits),
	}
	body, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return mcp.NewToolResultText(string(body)), nil
}

func mcpSearch(d *sql.DB, query, project, sourceType, topic string, limit int) ([]searchHit, error) {
	var conds []string
	var qargs []any
	conds = append(conds, "documents_fts MATCH ?")
	qargs = append(qargs, query)
	if project != "" {
		conds = append(conds, "d.project = ?")
		qargs = append(qargs, project)
	}
	if sourceType != "" {
		conds = append(conds, "d.source_type = ?")
		qargs = append(qargs, sourceType)
	}
	if topic != "" {
		conds = append(conds, "d.topic = ?")
		qargs = append(qargs, topic)
	}
	fetchLimit := limit * 5
	if fetchLimit < 50 {
		fetchLimit = 50
	}

	q := fmt.Sprintf(`
        SELECT d.filepath, d.project, d.timestamp, d.source_type,
               COALESCE(d.dir_type,''), COALESCE(d.session_id,''),
               COALESCE(d.topic,''), bm25(documents_fts),
               snippet(documents_fts, 0, '>>>', '<<<', '...', 40)
          FROM documents_fts
          JOIN documents d ON d.id = documents_fts.rowid
         WHERE %s
         ORDER BY bm25(documents_fts)
         LIMIT ?`, strings.Join(conds, " AND "))
	qargs = append(qargs, fetchLimit)

	rows, err := d.Query(q, qargs...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	now := time.Now()
	var hits []searchHit
	for rows.Next() {
		var h searchHit
		var rank float64
		if err := rows.Scan(&h.Filepath, &h.Project, &h.Timestamp, &h.SourceType,
			&h.DirType, &h.SessionID, &h.Topic, &rank, &h.Snippet); err != nil {
			return nil, err
		}
		days := daysFromTimestamp(h.Timestamp, now)
		decay := 1.0 / (1.0 + float64(days)*0.01)
		h.Score = absFloat(rank) * decay
		hits = append(hits, h)
	}
	// sort by score descending
	for i := 1; i < len(hits); i++ {
		j := i
		for j > 0 && hits[j-1].Score < hits[j].Score {
			hits[j-1], hits[j] = hits[j], hits[j-1]
			j--
		}
	}
	if len(hits) > limit {
		hits = hits[:limit]
	}
	return hits, nil
}

func daysFromTimestamp(ts string, now time.Time) int {
	t, err := time.Parse("20060102_150405", ts)
	if err != nil {
		return 0
	}
	d := int(now.Sub(t).Hours() / 24)
	if d < 0 {
		return 0
	}
	return d
}

func absFloat(f float64) float64 {
	if f < 0 {
		return -f
	}
	return f
}

func init() {
	mcpCmd.AddCommand(mcpServeCmd)
	rootCmd.AddCommand(mcpCmd)
}

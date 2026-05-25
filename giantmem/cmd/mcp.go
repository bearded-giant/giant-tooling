package cmd

import (
	"bufio"
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/bearded-giant/giant-tooling/giantmem/internal/db"
	"github.com/bearded-giant/giant-tooling/giantmem/internal/search"
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
			mcp.WithDescription(`Search archived workspaces, Claude session transcripts, and domain knowledge via FTS5.

Plain queries are auto-quoted so punctuation (e.g. "hub-and-spoke") works. FTS5 syntax (AND, OR, NOT, "phrase", prefix*) passes through untouched.

When tool_filter or ext_filter is set on a session search, results expand to PER-LINE matches inside session JSONL transcripts: each row is one Claude tool_use with role + tool name + file path + a decoded text excerpt. Otherwise file-level results with FTS snippet.

Read tool calls are HIDDEN by default (high-volume noise). Pass include_read=true or include "Read" in tool_filter to surface them.`),
			mcp.WithReadOnlyHintAnnotation(true),
			mcp.WithDestructiveHintAnnotation(false),
			mcp.WithIdempotentHintAnnotation(true),
			mcp.WithOpenWorldHintAnnotation(false),
			mcp.WithString("query",
				mcp.Required(),
				mcp.Description(`FTS5 search query. Plain text is auto-quoted; wrap your own '"exact phrase"' for literal substring; full FTS5 operators supported when present.`),
			),
			mcp.WithString("project",
				mcp.Description(`filter by project, LIKE-matched (e.g. "chat-orchestrator" matches "dev/ai/chat-orchestrator")`),
			),
			mcp.WithString("source_type",
				mcp.Description(`filter by source: "workspace", "session", or "domain"`),
			),
			mcp.WithString("topic",
				mcp.Description(`filter by session topic (e.g. "auth", "api", "bug", "feature")`),
			),
			mcp.WithString("tool_filter",
				mcp.Description(`session-only filter (comma-separated): keep matches on lines where Claude used these tool names (e.g. "Write,Edit,MultiEdit"). Triggers per-line expansion. Case-insensitive.`),
			),
			mcp.WithString("ext_filter",
				mcp.Description(`session-only filter (comma-separated): keep matches where a tool_use touched a file with these extensions (e.g. "md,go"). Composes with tool_filter via AND. Triggers per-line expansion.`),
			),
			mcp.WithBoolean("include_read",
				mcp.Description(`session-only: include Read tool calls in results (default false; Read is hidden because Claude reads files constantly).`),
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

	// find_artifact
	s.AddTool(
		mcp.NewTool("find_artifact",
			append([]mcp.ToolOption{
				mcp.WithDescription("Find typed artifacts across .giantmem/ workspaces. Filter by type, status, feature, domain, repo, branch, and optional fulltext query. Returns artifact ID, type, status, path, repo+branch, and a snippet when query matches."),
				mcp.WithString("type",
					mcp.Description("comma-separated artifact types (source-spec, delta-spec, proposal, design, tasks, plan, research, review, domain, notes, pattern, facts)")),
				mcp.WithString("domain",
					mcp.Description("filter by domain name (e.g. auth, payments)")),
				mcp.WithString("status",
					mcp.Description("comma-separated statuses (draft, ready, done, blocked, stale)")),
				mcp.WithString("feature",
					mcp.Description("filter by feature name")),
				mcp.WithString("repo",
					mcp.Description("repo filter: 'current' (cwd), 'all' (every discovered workspace, default), or a specific repo name")),
				mcp.WithString("branch",
					mcp.Description("branch filter — useful when same feature spans multiple worktrees")),
				mcp.WithString("scope",
					mcp.Description("scope id filter — matches explicit artifact frontmatter or repo membership in ~/.giantmem-global/scopes.yaml")),
				mcp.WithString("lifecycle",
					mcp.Description("comma-separated lifecycle filter (candidate, durable, deprecated). Empty matches all.")),
				mcp.WithString("query",
					mcp.Description("optional substring (case-insensitive) — return artifacts whose body contains it, with a snippet")),
				mcp.WithNumber("limit",
					mcp.Description("max results (default 20)"),
					mcp.Min(1), mcp.Max(200)),
			}, readOnly...)...,
		),
		mcp.NewTypedToolHandler(findArtifactHandler),
	)

	// get_artifact
	s.AddTool(
		mcp.NewTool("get_artifact",
			append([]mcp.ToolOption{
				mcp.WithDescription("Return full frontmatter + body of one artifact by stable ID (e.g. 'feat:openspec-compare:proposal' or 'repo:source-spec:auth')."),
				mcp.WithString("id",
					mcp.Required(),
					mcp.Description("stable artifact ID — see find_artifact results")),
			}, readOnly...)...,
		),
		mcp.NewTypedToolHandler(getArtifactHandler),
	)

	// get_stats
	s.AddTool(
		mcp.NewTool("get_stats",
			append([]mcp.ToolOption{
				mcp.WithDescription("Lightweight counts across artifacts and recent access. Useful for 'how many candidates are pending review' / 'what got touched today' style questions without running a full search. Filters: scope, repo (default all), feature."),
				mcp.WithString("scope",
					mcp.Description("scope id filter")),
				mcp.WithString("repo",
					mcp.Description("'current' (cwd), 'all' (default), or a repo name")),
				mcp.WithString("feature",
					mcp.Description("filter by feature name")),
			}, readOnly...)...,
		),
		mcp.NewTypedToolHandler(getStatsHandler),
	)

	// list_features_with_artifacts
	s.AddTool(
		mcp.NewTool("list_features_with_artifacts",
			append([]mcp.ToolOption{
				mcp.WithDescription("Group artifacts by feature across one or all repos. Useful for 'show me every feature with open delta-specs' style queries."),
				mcp.WithString("repo",
					mcp.Description("'current' (default), 'all', or a repo name")),
				mcp.WithString("artifact_types",
					mcp.Description("optional comma-separated type filter (e.g. 'delta-spec,tasks')")),
			}, readOnly...)...,
		),
		mcp.NewTypedToolHandler(listFeaturesWithArtifactsHandler),
	)
}

type searchArgs struct {
	Query       string  `json:"query"`
	Project     string  `json:"project"`
	SourceType  string  `json:"source_type"`
	Topic       string  `json:"topic"`
	ToolFilter  string  `json:"tool_filter"`
	ExtFilter   string  `json:"ext_filter"`
	IncludeRead bool    `json:"include_read"`
	Limit       float64 `json:"limit"`
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

// matchHit is the per-line result returned when tool_filter or ext_filter is
// set on a session search. Mirrors what `giantmem find ... --tool X` emits.
type matchHit struct {
	Filepath  string   `json:"filepath"`
	Project   string   `json:"project"`
	SessionID string   `json:"session_id,omitempty"`
	Timestamp string   `json:"timestamp"`
	Line      int      `json:"line"`
	Role      string   `json:"role,omitempty"`
	Tools     []string `json:"tools,omitempty"`
	Files     []string `json:"files,omitempty"`
	Excerpt   string   `json:"excerpt"`
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

	tools := splitCSV(args.ToolFilter)
	exts := splitCSV(args.ExtFilter)
	wantPerLine := len(tools) > 0 || len(exts) > 0

	sanitized := search.SanitizeFTSQuery(args.Query)
	hits, err := mcpSearch(d, sanitized, args.Project, args.SourceType, args.Topic, limit)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	if !wantPerLine {
		return jsonResult(map[string]any{
			"results": hits,
			"total":   len(hits),
		})
	}

	// Per-line expansion: feed session hits through the same rg + decode
	// pipeline the CLI uses, so MCP callers get the same `path:line  [role]
	// excerpt ⟨tool ...⟩` precision the user gets from `giantmem find ...`.
	sessionHits := make([]search.Hit, 0, len(hits))
	for _, h := range hits {
		if h.SourceType != "session" {
			continue
		}
		sessionHits = append(sessionHits, search.Hit{
			Score:      h.Score,
			Source:     "archive",
			Project:    h.Project,
			Timestamp:  h.Timestamp,
			DirType:    h.DirType,
			Filepath:   h.Filepath,
			SourceType: h.SourceType,
			SessionID:  h.SessionID,
			Topic:      h.Topic,
		})
	}

	rows, err := expandHitsToMatches(sessionHits, args.Query, MatchFilters{
		Tools:       tools,
		Exts:        exts,
		IncludeRead: args.IncludeRead,
	})
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("match expansion: %v", err)), nil
	}

	out := make([]matchHit, 0, len(rows))
	for _, r := range rows {
		excerpt := r.Display
		if len(excerpt) > 600 {
			excerpt = excerpt[:600] + "…"
		}
		mh := matchHit{
			Filepath:  r.Hit.Filepath,
			Project:   r.Hit.Project,
			SessionID: r.Hit.SessionID,
			Timestamp: r.Hit.Timestamp,
			Line:      r.Line,
			Tools:     r.Tools,
			Excerpt:   excerpt,
		}
		// Pull role + files back out by re-decoding the matched line. Cheap
		// (single jsonl line) and keeps the matchRow surface tight.
		if summary, ok := readSessionLine(r.Hit.Filepath, r.Line); ok {
			if !args.IncludeRead {
				summary = summary.WithoutReads()
			}
			mh.Role = summary.Role
			if mh.Role == "" {
				mh.Role = summary.Type
			}
			mh.Files = summary.Files
		}
		out = append(out, mh)
		if len(out) >= limit {
			break
		}
	}

	return jsonResult(map[string]any{
		"results":           out,
		"total":             len(out),
		"per_line":          true,
		"applied_filters":   map[string]any{"tools": tools, "exts": exts, "include_read": args.IncludeRead},
		"sanitized_query":   sanitized,
	})
}

func splitCSV(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	out := []string{}
	for _, p := range strings.Split(s, ",") {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// readSessionLine reads exactly one line N from a JSONL file and returns its
// decoded summary. Used by the MCP handler to enrich per-line results without
// holding state from the rg expansion pass.
func readSessionLine(path string, line int) (sessionLineSummary, bool) {
	f, err := os.Open(path)
	if err != nil {
		return sessionLineSummary{}, false
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1<<20), 64<<20)
	n := 0
	for sc.Scan() {
		n++
		if n == line {
			return decodeSessionLine(sc.Bytes())
		}
		if n > line {
			break
		}
	}
	return sessionLineSummary{}, false
}

func mcpSearch(d *sql.DB, query, project, sourceType, topic string, limit int) ([]searchHit, error) {
	var conds []string
	var qargs []any
	conds = append(conds, "documents_fts MATCH ?")
	qargs = append(qargs, query)
	if project != "" {
		conds = append(conds, "d.project LIKE ?")
		qargs = append(qargs, "%"+project+"%")
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

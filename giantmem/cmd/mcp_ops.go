package cmd

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

var featureActions = []string{"new", "start", "pause", "reopen", "complete", "abandon", "next", "facts", "list", "archive", "move"}

func registerOpsTools(s *server.MCPServer) {
	hints := func(readOnly, destructive, idempotent bool) []mcp.ToolOption {
		return []mcp.ToolOption{
			mcp.WithReadOnlyHintAnnotation(readOnly),
			mcp.WithDestructiveHintAnnotation(destructive),
			mcp.WithIdempotentHintAnnotation(idempotent),
			mcp.WithOpenWorldHintAnnotation(false),
		}
	}
	cwd := mcp.WithString("cwd",
		mcp.Description("worktree, or any dir inside one; the nearest .giantmem/ walking up is used (default: the dir this server started in, the session's launch dir)"))

	s.AddTool(
		mcp.NewTool("reindex_artifacts",
			append([]mcp.ToolOption{
				mcp.WithDescription("Rebuild a workspace's .giantmem/artifacts.json from disk and reconcile the live.db artifacts table. Run it after changing frontmatter (type, status, lifecycle) by any path other than Write/Edit, so find_artifact and the GUI see the change. Same as `giantmem artifact reindex`."),
				cwd,
			}, hints(false, false, true)...)...,
		),
		mcp.NewTypedToolHandler(reindexArtifactsHandler),
	)

	s.AddTool(
		mcp.NewTool("stale_artifacts",
			append([]mcp.ToolOption{
				mcp.WithDescription("List artifacts that are status: stale or not updated in `days`, skipping status: done. days=0 applies the lifecycle tier policy instead of a fixed cutoff. With apply=true, listed candidate artifacts with no access in idle_days flip to lifecycle: deprecated (only the lifecycle line is rewritten, mtime kept). Same as `giantmem artifact stale`."),
				cwd,
				mcp.WithNumber("days", mcp.Description("age cutoff in days (default 30); 0 = lifecycle tier policy"), mcp.Min(0)),
				mcp.WithBoolean("all_repos", mcp.Description("scan every discovered workspace, not just cwd's")),
				mcp.WithString("scope", mcp.Description("scope id filter")),
				mcp.WithString("lifecycle", mcp.Description("comma-separated lifecycle filter (candidate, durable, deprecated)")),
				mcp.WithBoolean("apply", mcp.Description("flip eligible candidates to lifecycle: deprecated (writes files)")),
				mcp.WithNumber("idle_days", mcp.Description("with apply: only flip artifacts with zero access rows in this window (default 180)"), mcp.Min(1)),
			}, hints(false, false, true)...)...,
		),
		mcp.NewTypedToolHandler(staleArtifactsHandler),
	)

	s.AddTool(
		mcp.NewTool("orphan_artifacts",
			append([]mcp.ToolOption{
				mcp.WithDescription("List artifact-shaped files in a workspace that lack YAML frontmatter, as `type path` lines. Same as `giantmem artifact orphans`."),
				cwd,
			}, hints(true, false, true)...)...,
		),
		mcp.NewTypedToolHandler(orphanArtifactsHandler),
	)

	s.AddTool(
		mcp.NewTool("feature",
			append([]mcp.ToolOption{
				mcp.WithDescription(`Run a feature lifecycle action in a workspace; same as ` + "`giantmem feature <action>`" + ` (feature.py for new..next).
- new: create features/<name>/ (proposal, tasks, facts, notes) and check out branch <name> unless skip_checkout. Takes branch, base, builds_on, discovery, paired, no_paired, skip_checkout.
- start: promote a pending feature to in_progress and check out its branch unless skip_checkout. Takes branch, base, skip_checkout.
- pause: mark paused. Takes note, paused_state.
- reopen: reopen a paused or complete feature. Takes skip_checkout.
- complete: mark complete and merge delta-specs into source-specs. Takes quick, no_merge, reason.
- abandon: mark abandoned, skip the spec merge, then archive the dir unless no_archive. Takes reason, no_archive.
- next: print the next ready task.
- facts: print features/<name>/facts.md (partial name match; ambiguous names list the candidates).
- list: print features.json. Takes status.
- archive: verify every file is in live.db, then remove the feature dir (no name = every complete/abandoned feature). Takes force, dry_run.
- move: move features/<name> into the dest worktree's .giantmem. Takes dest, dry_run.
name defaults to the active feature where the action allows it; new, facts, and move require it. Flags an action does not take are rejected.`),
				mcp.WithString("action", mcp.Required(), mcp.Enum(featureActions...)),
				mcp.WithString("name", mcp.Description("feature name (required for new, facts, and move)")),
				cwd,
				mcp.WithString("branch", mcp.Description("git branch (new, start)")),
				mcp.WithString("base", mcp.Description("base branch (new, start)")),
				mcp.WithString("builds_on", mcp.Description("feature this builds on (new)")),
				mcp.WithString("discovery", mcp.Description("discovery notes seeded into the proposal (new)")),
				mcp.WithBoolean("paired", mcp.Description("paired frontend+backend feature (new)")),
				mcp.WithBoolean("no_paired", mcp.Description("skip the paired counterpart even where the repo map pairs one (new)")),
				mcp.WithBoolean("skip_checkout", mcp.Description("do not switch git branches (new, start, reopen)")),
				mcp.WithString("note", mcp.Description("resumption note (pause)")),
				mcp.WithString("paused_state", mcp.Description("paused-state snapshot (pause)")),
				mcp.WithBoolean("quick", mcp.Description("skip the full completion pass (complete)")),
				mcp.WithBoolean("no_merge", mcp.Description("skip the delta-spec merge (complete)")),
				mcp.WithString("reason", mcp.Description("why (complete, abandon)")),
				mcp.WithBoolean("no_archive", mcp.Description("keep the dir after abandoning (abandon)")),
				mcp.WithString("status", mcp.Description("status filter (list)")),
				mcp.WithBoolean("force", mcp.Description("archive from any status (archive)")),
				mcp.WithBoolean("dry_run", mcp.Description("show what would happen (archive, move)")),
				mcp.WithString("dest", mcp.Description("destination worktree path (move)")),
			}, hints(false, true, false)...)...,
		),
		mcp.NewTypedToolHandler(featureHandler),
	)

	s.AddTool(
		mcp.NewTool("query_db",
			append([]mcp.ToolOption{
				mcp.WithDescription(`Run one read-only SQL statement against giantmem's SQLite stores and get rows back as JSON objects. Use it instead of shelling out to sqlite3. The connection is opened read-only; writes fail.
live.db: live_docs(path, project, canonical_project, worktree_path, feature, dir_type, session_id, git_sha, mtime, ingested_at, content), artifacts(id, type, feature, domain, name, status, lifecycle, scope, repo, branch, path, worktree, size, created, updated, has_front, indexed_at), artifact_access(artifact_id, query, rank, accessed_at), artifact_embedding_meta, scopes, recall_log.
archives.db: documents(project, canonical_project, timestamp, source_type [session, subagent, bundle, workspace, domain], session_id, topic, cwd, filepath, filename), tool_uses(session_id, agent_id, project, seq, ts, tool, summary, input, is_error, result_head), session_links(session_id, kind, target, label, ts, role, cwd), mr_state(url, title, state, verdict, source_branch, checked_at).
Query sqlite_master for anything else.`),
				mcp.WithString("db", mcp.Description("live (default) or archives"), mcp.Enum("live", "archives")),
				mcp.WithString("sql", mcp.Required(), mcp.Description("one SELECT, WITH, or PRAGMA statement")),
				mcp.WithNumber("limit", mcp.Description("max rows returned (default 200)"), mcp.Min(1), mcp.Max(5000)),
			}, hints(true, false, true)...)...,
		),
		mcp.NewTypedToolHandler(queryDBHandler),
	)
}

type cwdArgs struct {
	Cwd string `json:"cwd"`
}

func reindexArtifactsHandler(ctx context.Context, _ mcp.CallToolRequest, a cwdArgs) (*mcp.CallToolResult, error) {
	return runSelf(ctx, a.Cwd, "artifact", "reindex")
}

func orphanArtifactsHandler(ctx context.Context, _ mcp.CallToolRequest, a cwdArgs) (*mcp.CallToolResult, error) {
	return runSelf(ctx, a.Cwd, "artifact", "orphans")
}

type staleArtifactsArgs struct {
	Cwd       string   `json:"cwd"`
	Days      *float64 `json:"days"`
	AllRepos  bool     `json:"all_repos"`
	Scope     string   `json:"scope"`
	Lifecycle string   `json:"lifecycle"`
	Apply     bool     `json:"apply"`
	IdleDays  float64  `json:"idle_days"`
}

func staleArgv(a staleArtifactsArgs) []string {
	days := 30
	if a.Days != nil {
		days = int(*a.Days)
	}
	argv := []string{"artifact", "stale", "--days", strconv.Itoa(days)}
	argv = appendBool(argv, "--all-repos", a.AllRepos)
	argv = appendStr(argv, "--scope", a.Scope)
	argv = appendStr(argv, "--lifecycle", a.Lifecycle)
	argv = appendBool(argv, "--apply", a.Apply)
	if a.IdleDays > 0 {
		argv = append(argv, "--idle-days", strconv.Itoa(int(a.IdleDays)))
	}
	return argv
}

func staleArtifactsHandler(ctx context.Context, _ mcp.CallToolRequest, a staleArtifactsArgs) (*mcp.CallToolResult, error) {
	return runSelf(ctx, a.Cwd, staleArgv(a)...)
}

type featureArgs struct {
	Action       string `json:"action"`
	Name         string `json:"name"`
	Cwd          string `json:"cwd"`
	Branch       string `json:"branch"`
	Base         string `json:"base"`
	BuildsOn     string `json:"builds_on"`
	Discovery    string `json:"discovery"`
	Paired       bool   `json:"paired"`
	NoPaired     bool   `json:"no_paired"`
	SkipCheckout bool   `json:"skip_checkout"`
	Note         string `json:"note"`
	PausedState  string `json:"paused_state"`
	Quick        bool   `json:"quick"`
	NoMerge      bool   `json:"no_merge"`
	Reason       string `json:"reason"`
	NoArchive    bool   `json:"no_archive"`
	Status       string `json:"status"`
	Force        bool   `json:"force"`
	DryRun       bool   `json:"dry_run"`
	Dest         string `json:"dest"`
}

func featureArgv(a featureArgs) ([]string, error) {
	known := false
	for _, v := range featureActions {
		known = known || v == a.Action
	}
	if !known {
		return nil, fmt.Errorf("unknown action %q (want one of %s)", a.Action, strings.Join(featureActions, ", "))
	}
	if (a.Action == "new" || a.Action == "facts" || a.Action == "move") && a.Name == "" {
		return nil, fmt.Errorf("%s needs name", a.Action)
	}
	if a.Action == "move" && a.Dest == "" {
		return nil, fmt.Errorf("move needs dest")
	}
	if a.Action == "list" && a.Name != "" {
		return nil, fmt.Errorf("list takes no name")
	}
	argv := []string{"feature", a.Action}
	if a.Name != "" {
		argv = append(argv, a.Name)
	}
	if a.Dest != "" {
		argv = append(argv, a.Dest)
	}
	argv = appendStr(argv, "--branch", a.Branch)
	argv = appendStr(argv, "--base", a.Base)
	argv = appendStr(argv, "--builds-on", a.BuildsOn)
	argv = appendStr(argv, "--discovery", a.Discovery)
	argv = appendBool(argv, "--paired", a.Paired)
	argv = appendBool(argv, "--no-paired", a.NoPaired)
	argv = appendBool(argv, "--skip-checkout", a.SkipCheckout)
	argv = appendStr(argv, "--note", a.Note)
	argv = appendStr(argv, "--paused-state", a.PausedState)
	argv = appendBool(argv, "--quick", a.Quick)
	argv = appendBool(argv, "--no-merge", a.NoMerge)
	argv = appendStr(argv, "--reason", a.Reason)
	argv = appendBool(argv, "--no-archive", a.NoArchive)
	argv = appendStr(argv, "--status", a.Status)
	argv = appendBool(argv, "--force", a.Force)
	argv = appendBool(argv, "--dry-run", a.DryRun)
	return argv, nil
}

func featureHandler(ctx context.Context, _ mcp.CallToolRequest, a featureArgs) (*mcp.CallToolResult, error) {
	argv, err := featureArgv(a)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return runSelf(ctx, a.Cwd, argv...)
}

func appendStr(argv []string, flag, v string) []string {
	if v == "" {
		return argv
	}
	return append(argv, flag, v)
}

func appendBool(argv []string, flag string, v bool) []string {
	if !v {
		return argv
	}
	return append(argv, flag)
}

// a child process per call: cobra flag vars are package globals, so in-process reuse would race across concurrent calls
func runSelf(ctx context.Context, dir string, args ...string) (*mcp.CallToolResult, error) {
	exe, err := os.Executable()
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	if dir != "" {
		if st, err := os.Stat(dir); err != nil || !st.IsDir() {
			return mcp.NewToolResultError(fmt.Sprintf("cwd %q is not a directory", dir)), nil
		}
	}
	c := exec.CommandContext(ctx, exe, args...)
	c.Dir = dir
	c.Env = append(os.Environ(), "GIANTMEM_ARCHIVE_BASE="+flagArchiveBase)
	out, err := c.CombinedOutput()
	text := strings.TrimSpace(string(out))
	if err != nil {
		return mcp.NewToolResultError(strings.TrimSpace(text + "\n" + err.Error())), nil
	}
	if text == "" {
		text = "ok"
	}
	return mcp.NewToolResultText(text), nil
}

type queryDBArgs struct {
	DB    string  `json:"db"`
	SQL   string  `json:"sql"`
	Limit float64 `json:"limit"`
}

func queryDBHandler(ctx context.Context, _ mcp.CallToolRequest, a queryDBArgs) (*mcp.CallToolResult, error) {
	path := liveDBPath()
	switch a.DB {
	case "", "live":
	case "archives":
		path = archiveDBPath()
	default:
		return mcp.NewToolResultError(fmt.Sprintf("db %q: want live or archives", a.DB)), nil
	}
	if strings.TrimSpace(a.SQL) == "" {
		return mcp.NewToolResultError("sql is required"), nil
	}
	limit := int(a.Limit)
	if limit <= 0 {
		limit = 200
	}
	cols, rows, truncated, err := queryReadOnly(ctx, path, a.SQL, limit)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return jsonResult(map[string]any{"columns": cols, "rows": rows, "count": len(rows), "truncated": truncated})
}

func queryReadOnly(ctx context.Context, path, query string, limit int) ([]string, []map[string]any, bool, error) {
	if _, err := os.Stat(path); err != nil {
		return nil, nil, false, err
	}
	conn, err := sql.Open("sqlite", "file:"+path+"?mode=ro&_pragma=query_only(1)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, nil, false, err
	}
	defer conn.Close()
	rs, err := conn.QueryContext(ctx, query)
	if err != nil {
		return nil, nil, false, err
	}
	defer rs.Close()
	cols, err := rs.Columns()
	if err != nil {
		return nil, nil, false, err
	}
	out := []map[string]any{}
	for rs.Next() {
		if len(out) == limit {
			return cols, out, true, nil
		}
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rs.Scan(ptrs...); err != nil {
			return nil, nil, false, err
		}
		row := make(map[string]any, len(cols))
		for i, c := range cols {
			if b, ok := vals[i].([]byte); ok {
				vals[i] = string(b)
			}
			row[c] = vals[i]
		}
		out = append(out, row)
	}
	return cols, out, false, rs.Err()
}

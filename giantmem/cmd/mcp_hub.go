package cmd

import (
	"context"
	"path/filepath"

	"github.com/bearded-giant/giant-tooling/giantmem/internal/db"
	"github.com/mark3labs/mcp-go/mcp"
)

type findToolUsesArgs struct {
	Query      string  `json:"query"`
	SessionID  string  `json:"session_id"`
	Tool       string  `json:"tool"`
	Project    string  `json:"project"`
	ErrorsOnly bool    `json:"errors_only"`
	Limit      float64 `json:"limit"`
}

func findToolUsesHandler(ctx context.Context, _ mcp.CallToolRequest, args findToolUsesArgs) (*mcp.CallToolResult, error) {
	if args.Query == "" && args.SessionID == "" && args.Tool == "" {
		return mcp.NewToolResultError("pass query, session_id, or tool"), nil
	}
	limit := int(args.Limit)
	if limit <= 0 {
		limit = 30
	}
	d, err := db.Open(archiveDBPath())
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	defer d.Close()
	hits, err := queryToolUses(d, toolQuery{
		SessionID: args.SessionID,
		Match:     args.Query,
		Tool:      args.Tool,
		Project:   args.Project,
		Errors:    args.ErrorsOnly,
		Limit:     limit,
	})
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return jsonResult(map[string]any{"tool_uses": hits, "total": len(hits)})
}

type getSessionBundleArgs struct {
	IDPrefix string `json:"id_prefix"`
	Kind     string `json:"kind"`
}

func getSessionBundleHandler(ctx context.Context, _ mcp.CallToolRequest, args getSessionBundleArgs) (*mcp.CallToolResult, error) {
	d, r, err := openArchiveSession(args.IDPrefix)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	defer d.Close()
	links, err := queryLinks(d, r.ID)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	out := map[string]any{"session_id": r.ID, "project": r.Project, "jsonl_path": r.JSONLPath, "links": links}
	m, err := loadBundle(r.ID)
	if err != nil {
		out["bundle_error"] = err.Error()
		return jsonResult(out)
	}
	var files []map[string]any
	for _, f := range m.Files {
		if args.Kind != "" && f.Kind != args.Kind {
			continue
		}
		files = append(files, map[string]any{"path": filepath.Join(m.Dir, f.Path), "kind": f.Kind, "size": f.Size})
	}
	out["bundle_dir"] = m.Dir
	out["first_prompt"] = m.FirstPrompt
	out["resume"] = m.Resume
	out["files"] = files
	return jsonResult(out)
}

package main

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/bearded-giant/giant-tooling/giantmem/internal/artifacts"
	"github.com/bearded-giant/giant-tooling/giantmem/internal/daemon"
	"github.com/bearded-giant/giant-tooling/giantmem/internal/db"
	"github.com/bearded-giant/giant-tooling/giantmem/internal/search"
)

type App struct {
	ctx     context.Context
	live    *sql.DB
	archive *sql.DB
}

func NewApp() *App {
	return &App{}
}

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	base := archiveBase()
	if live, err := db.Open(filepath.Join(base, "live.db")); err == nil {
		a.live = live
	} else {
		fmt.Fprintf(os.Stderr, "gui: open live.db: %v\n", err)
	}
	if archive, err := db.Open(filepath.Join(base, "archives.db")); err == nil {
		a.archive = archive
	} else {
		fmt.Fprintf(os.Stderr, "gui: open archives.db: %v\n", err)
	}
}

func (a *App) shutdown(ctx context.Context) {
	if a.live != nil {
		a.live.Close()
		a.live = nil
	}
	if a.archive != nil {
		a.archive.Close()
		a.archive = nil
	}
}

// ListArtifacts returns artifact rows filtered + sorted. limit<=0 means no limit.
// Frontend sees a JS-side artifacts.ListFilter object (Type/Status/Lifecycle as
// arrays; Scope/Repo/Branch/Feature/Domain as strings).
func (a *App) ListArtifacts(filter artifacts.ListFilter, sortBy string, limit int) ([]artifacts.Artifact, error) {
	if a.live == nil {
		return nil, fmt.Errorf("live db not open")
	}
	return artifacts.ListArtifacts(a.live, filter, sortBy, limit)
}

// FacetCountsResult bundles the three facet maps returned by
// artifacts.FacetCounts so Wails can ship them as one JS object.
type FacetCountsResult struct {
	ByType      map[string]int `json:"byType"`
	ByLifecycle map[string]int `json:"byLifecycle"`
	ByStatus    map[string]int `json:"byStatus"`
}

func (a *App) FacetCounts() (FacetCountsResult, error) {
	if a.live == nil {
		return FacetCountsResult{}, fmt.Errorf("live db not open")
	}
	t, l, s, err := artifacts.FacetCounts(a.live)
	if err != nil {
		return FacetCountsResult{}, err
	}
	return FacetCountsResult{ByType: t, ByLifecycle: l, ByStatus: s}, nil
}

// SearchHybrid runs the blended ranker. Candidates come from the artifacts
// projection (filtered if filter is non-empty); the query vector is resolved
// via the daemon's `embed` RPC so the GUI never loads an embedding model.
// When the daemon is down, vector score collapses to zero — FTS/recency/access
// still rank the result set.
func (a *App) SearchHybrid(query string, filter artifacts.ListFilter, limit int) ([]search.HybridResult, error) {
	if a.live == nil {
		return nil, fmt.Errorf("live db not open")
	}
	candidates, err := artifacts.ListArtifacts(a.live, filter, "", 0)
	if err != nil {
		return nil, err
	}
	queryVec, _ := daemonEmbed(query)
	return search.Hybrid(a.live, query, queryVec, candidates, search.DefaultHybridWeights(), limit)
}

// SearchFTS runs the FTS5 query path across archives.db + live.db (either may
// be nil — search.Run scopes to whichever is open). Returns ranked hits with
// snippets, suitable for the session viewer's row list.
func (a *App) SearchFTS(params search.Params) ([]search.Hit, error) {
	return search.Run(a.archive, a.live, params)
}

// GetArtifact returns one artifact row by ID. Errors when nothing matches.
func (a *App) GetArtifact(id string) (artifacts.Artifact, error) {
	if a.live == nil {
		return artifacts.Artifact{}, fmt.Errorf("live db not open")
	}
	rows, err := artifacts.ListArtifacts(a.live, artifacts.ListFilter{}, "", 0)
	if err != nil {
		return artifacts.Artifact{}, err
	}
	for _, r := range rows {
		if r.ID == id {
			return r, nil
		}
	}
	return artifacts.Artifact{}, fmt.Errorf("artifact not found: %s", id)
}

// GetArtifactBody returns the raw markdown of one artifact, resolved via the
// stored worktree + .giantmem/ + path. Empty string when the file is missing.
func (a *App) GetArtifactBody(id string) (string, error) {
	art, err := a.GetArtifact(id)
	if err != nil {
		return "", err
	}
	if art.Worktree == "" || art.Path == "" {
		return "", fmt.Errorf("artifact has no path: %s", id)
	}
	abs := filepath.Join(art.Worktree, ".giantmem", art.Path)
	body, err := os.ReadFile(abs)
	if err != nil {
		return "", err
	}
	return string(body), nil
}

// ReadFile returns the raw bytes of any file as a string. Used by the session
// viewer to render Claude transcript JSONL given a Hit.Filepath. No path
// sandboxing yet — GUI is single-user localhost.
func (a *App) ReadFile(path string) (string, error) {
	if path == "" {
		return "", fmt.Errorf("empty path")
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return string(body), nil
}

// daemonEmbed asks the running giantmemd to embed text with its real backend.
// Returns (vec, true) on success; (nil, false) when the daemon is down so
// callers can degrade gracefully. GUI never loads its own embedder.
func daemonEmbed(text string) ([]float32, bool) {
	if text == "" {
		return nil, false
	}
	sock := daemon.DefaultSocketPath()
	if !daemon.SocketAlive(sock, 250*time.Millisecond) {
		return nil, false
	}
	cli := daemon.NewClient(sock, 5*time.Second)
	var out daemon.EmbedResult
	if err := cli.Call("embed", &daemon.EmbedParams{Text: text}, &out); err != nil {
		return nil, false
	}
	if len(out.Vec) == 0 {
		return nil, false
	}
	return out.Vec, true
}

func archiveBase() string {
	if v := os.Getenv("GIANTMEM_ARCHIVE_BASE"); v != "" {
		return v
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "giantmem_archive")
}

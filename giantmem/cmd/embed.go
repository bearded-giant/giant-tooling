package cmd

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/bearded-giant/giant-tooling/giantmem/internal/artifacts"
	"github.com/bearded-giant/giant-tooling/giantmem/internal/db"
	"github.com/bearded-giant/giant-tooling/giantmem/internal/search"
	"github.com/spf13/cobra"
)

var (
	embedBackfill bool
	embedReset    bool
	embedScope    string
	embedRepo     string
	embedBackend  string
	embedLimit    int
)

var embedCmd = &cobra.Command{
	Use:   "embed",
	Short: "Generate and store per-artifact embeddings into live.db (sqlite-vec).",
	Long: `Embed artifact bodies into the vec0 virtual table in live.db.

By default --backfill walks every artifact in the current scope/repo and
embeds any whose body hash has changed. --reset clears existing embeddings
first. --backend selects the embedder; defaults to stub for tests (set
GIANTMEM_EMBED_BACKEND=python for real semantic vectors).`,
	RunE: runEmbed,
}

func init() {
	embedCmd.Flags().BoolVar(&embedBackfill, "backfill", false, "embed all matching artifacts")
	embedCmd.Flags().BoolVar(&embedReset, "reset", false, "drop existing embeddings before backfill")
	embedCmd.Flags().StringVar(&embedScope, "scope", "", "filter by scope id")
	embedCmd.Flags().StringVar(&embedRepo, "repo", "current", "current (default), all, or a repo name")
	embedCmd.Flags().StringVar(&embedBackend, "backend", "", "stub|python|ollama (default: $GIANTMEM_EMBED_BACKEND or stub)")
	embedCmd.Flags().IntVar(&embedLimit, "limit", 0, "stop after N artifacts (0 = unlimited)")
	rootCmd.AddCommand(embedCmd)
}

func runEmbed(cmd *cobra.Command, args []string) error {
	live, err := db.Open(liveDBPath())
	if err != nil {
		return err
	}
	defer live.Close()

	rows, err := embedCollectArtifacts(live)
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		fmt.Fprintln(os.Stderr, "no artifacts to embed")
		return nil
	}

	if embedReset {
		if err := search.ResetEmbeddings(live); err != nil {
			return fmt.Errorf("reset embeddings: %w", err)
		}
		fmt.Fprintln(os.Stderr, "cleared existing embeddings")
	}

	embedder, err := search.NewEmbedder(embedBackend)
	if err != nil {
		return err
	}
	defer embedder.Close()

	if !embedBackfill {
		fmt.Fprintf(os.Stderr,
			"info: --backfill not set; %d artifacts in scope would be embedded with backend=%s dim=%d model=%s\n",
			len(rows), embedBackendLabel(), embedder.Dim(), embedder.ModelName(),
		)
		return nil
	}

	total := len(rows)
	if embedLimit > 0 && embedLimit < total {
		rows = rows[:embedLimit]
		total = embedLimit
	}

	written := 0
	skipped := 0
	failed := 0
	start := time.Now()
	for i, a := range rows {
		body, err := readArtifactBody(a)
		if err != nil || strings.TrimSpace(body) == "" {
			skipped++
			continue
		}
		vec, err := embedder.Embed(body)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[%d/%d] FAIL %s: %v\n", i+1, total, a.ID, err)
			failed++
			continue
		}
		changed, err := search.WriteEmbedding(live, a.ID, body, vec, embedder.ModelName())
		if err != nil {
			fmt.Fprintf(os.Stderr, "[%d/%d] FAIL %s: %v\n", i+1, total, a.ID, err)
			failed++
			continue
		}
		if changed {
			written++
		} else {
			skipped++
		}
		if (i+1)%25 == 0 || i+1 == total {
			fmt.Fprintf(os.Stderr, "[%d/%d] written=%d skipped=%d failed=%d\n",
				i+1, total, written, skipped, failed)
		}
	}
	elapsed := time.Since(start)
	count, _ := search.EmbeddingsCount(live)
	fmt.Printf("done: written=%d skipped=%d failed=%d in %s (total stored=%d, backend=%s, model=%s)\n",
		written, skipped, failed, elapsed.Round(time.Millisecond),
		count, embedBackendLabel(), embedder.ModelName(),
	)
	return nil
}

func embedBackendLabel() string {
	if embedBackend != "" {
		return embedBackend
	}
	if v := os.Getenv("GIANTMEM_EMBED_BACKEND"); v != "" {
		return v
	}
	return "stub"
}

func embedCollectArtifacts(live *sql.DB) ([]artifacts.Artifact, error) {
	var rows []artifacts.Artifact
	// Prefer the projection table: its ids are repo-qualified, matching the
	// key WriteEmbedding stores under, so recall's embedding join lines up.
	// FS Scan/Crawl yields unqualified BuildIDs and is the first-run fallback.
	if artifacts.TableHasRows(live) {
		f := artifacts.ListFilter{}
		switch embedRepo {
		case "all", "":
		case "current":
			if _, idx, err := resolveWorkspace(); err == nil {
				f.Repo = idx.Repo
			}
		default:
			f.Repo = embedRepo
		}
		r, err := artifacts.ListArtifacts(live, f, "", 0)
		if err != nil {
			return nil, err
		}
		rows = r
	} else if embedRepo == "current" {
		_, idx, err := resolveWorkspace()
		if err != nil {
			return nil, err
		}
		rows = idx.Artifacts
	} else {
		all, _, err := artifacts.CrawlAll(0)
		if err != nil {
			return nil, err
		}
		if embedRepo == "all" {
			rows = all
		} else {
			for _, a := range all {
				if a.Repo == embedRepo {
					rows = append(rows, a)
				}
			}
		}
	}

	if embedScope != "" {
		reg, _ := artifacts.LoadScopeRegistry(artifacts.ScopesYAMLPath())
		filtered := make([]artifacts.Artifact, 0, len(rows))
		for _, a := range rows {
			if a.Scope != "" {
				if a.Scope == embedScope {
					filtered = append(filtered, a)
				}
				continue
			}
			if reg != nil && reg.MatchScope(a.Repo, a.Scope, embedScope) {
				filtered = append(filtered, a)
			}
		}
		rows = filtered
	}
	return rows, nil
}

// readArtifactBody reads the file content with frontmatter stripped.
// Returns the cleaned body; empty bodies are skipped at the caller.
func readArtifactBody(a artifacts.Artifact) (string, error) {
	abs := embedAbsPath(a)
	if abs == "" {
		return "", fmt.Errorf("could not resolve path for %s", a.ID)
	}
	raw, err := os.ReadFile(abs)
	if err != nil {
		return "", err
	}
	_, body, _ := artifacts.ParseFrontmatter(string(raw))
	return body, nil
}

func embedAbsPath(a artifacts.Artifact) string {
	if a.Worktree != "" {
		return filepath.Join(a.Worktree, ".giantmem", a.Path)
	}
	cwd, _ := os.Getwd()
	ws, ok := artifacts.FindWorkspace(cwd)
	if !ok {
		return ""
	}
	return filepath.Join(ws, a.Path)
}

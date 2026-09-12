package search

import (
	"database/sql"
	"fmt"
	"math"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/bearded-giant/giant-tooling/giantmem/internal/artifacts"
)

// HybridWeights blends FTS rank, vector similarity, recency, and access
// boost into one score. Defaults per design.md Decision 6. Must sum
// to 1.0 (validated by Validate()).
type HybridWeights struct {
	FTS     float64 `json:"fts"`
	Vector  float64 `json:"vector"`
	Recency float64 `json:"recency"`
	Access  float64 `json:"access"`
}

// DefaultHybridWeights is the shipped tuning. Overridable via env vars
// GIANTMEM_HYBRID_{FTS,VEC,RECENCY,ACCESS}_WEIGHT.
func DefaultHybridWeights() HybridWeights {
	w := HybridWeights{
		FTS:     0.5,
		Vector:  0.25,
		Recency: 0.15,
		Access:  0.1,
	}
	w.FTS = envFloat("GIANTMEM_HYBRID_FTS_WEIGHT", w.FTS)
	w.Vector = envFloat("GIANTMEM_HYBRID_VEC_WEIGHT", w.Vector)
	w.Recency = envFloat("GIANTMEM_HYBRID_RECENCY_WEIGHT", w.Recency)
	w.Access = envFloat("GIANTMEM_HYBRID_ACCESS_WEIGHT", w.Access)
	return w
}

func envFloat(key string, fallback float64) float64 {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return fallback
	}
	return f
}

// Validate reports an error when the weights don't sum to 1.0.
func (w HybridWeights) Validate() error {
	sum := w.FTS + w.Vector + w.Recency + w.Access
	if math.Abs(sum-1.0) > 1e-6 {
		return fmt.Errorf("hybrid weights must sum to 1.0, got %.4f (fts=%.4f vec=%.4f recency=%.4f access=%.4f)",
			sum, w.FTS, w.Vector, w.Recency, w.Access)
	}
	return nil
}

// HybridResult is one row of a hybrid scoring run, with the component
// scores preserved for explainability.
type HybridResult struct {
	Artifact     artifacts.Artifact `json:"artifact"`
	Score        float64            `json:"score"`
	FTSScore     float64            `json:"fts_score"`
	VectorScore  float64            `json:"vector_score"`
	RecencyScore float64            `json:"recency_score"`
	AccessScore  float64            `json:"access_score"`
	// best-matching chunk, when the vector arm scored this artifact
	ChunkOrd int    `json:"chunk_ord"`
	Passage  string `json:"passage,omitempty"`
}

// Hybrid runs the blended ranker over a candidate set.
//
//	query      — user's natural-language query (also used as the FTS body match)
//	queryVec   — the embedding for query; required when w.Vector > 0
//	candidates — Artifact records to score (typically the result of a
//	             filtered crawl: scope/repo/type/lifecycle already applied)
//	live       — live.db handle (used for artifact_embeddings lookup +
//	             access_log counts)
//	w          — weights (call Validate() first)
//
// Returns candidates sorted descending by Score. Limit caps result count.
func Hybrid(
	live *sql.DB,
	query string,
	queryVec []float32,
	candidates []artifacts.Artifact,
	w HybridWeights,
	limit int,
) ([]HybridResult, error) {
	if len(candidates) == 0 {
		return nil, nil
	}
	if limit <= 0 {
		limit = 20
	}

	ftsScores := scoreFTS(live, query, candidates)
	vecScores := map[string]float64{}
	bestChunk := map[string]VecHit{}
	if w.Vector > 0 && live != nil && len(queryVec) > 0 {
		// several hits per artifact now that bodies are chunked; keep the best
		hits, err := NearestNeighbors(live, queryVec, 400)
		if err == nil {
			for _, h := range hits {
				s := distanceToScore(h.Distance)
				if s > vecScores[h.ArtifactID] {
					vecScores[h.ArtifactID] = s
					bestChunk[h.ArtifactID] = h
				}
			}
		}
	}
	accessScores := map[string]float64{}
	if w.Access > 0 && live != nil {
		counts, err := artifacts.AccessCounts(live, time.Now().AddDate(0, 0, -30))
		if err == nil {
			max := 0
			for _, n := range counts {
				if n > max {
					max = n
				}
			}
			if max > 0 {
				for id, n := range counts {
					accessScores[id] = float64(n) / float64(max)
				}
			}
		}
	}
	now := time.Now()

	out := make([]HybridResult, 0, len(candidates))
	for _, a := range candidates {
		ftsS := ftsScores[a.ID]
		vecS := vecScores[a.ID]
		recS := recencyScore(a.Updated, now)
		accS := accessScores[a.ID]

		score := w.FTS*ftsS +
			w.Vector*vecS +
			w.Recency*recS +
			w.Access*accS

		out = append(out, HybridResult{
			Artifact:     a,
			Score:        score,
			FTSScore:     ftsS,
			VectorScore:  vecS,
			RecencyScore: recS,
			AccessScore:  accS,
			ChunkOrd:     bestChunk[a.ID].Ord,
			Passage:      bestChunk[a.ID].Snippet,
		})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Score > out[j].Score })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// distanceToScore maps a cosine/L2 distance (smaller = better) to a [0, 1]
// similarity score. Uses 1 / (1 + d) — monotonic, well-behaved at d=0
// (score=1), asymptotes to 0 as d -> inf.
func distanceToScore(d float64) float64 {
	if d < 0 {
		d = -d
	}
	return 1.0 / (1.0 + d)
}

// scoreFTS ranks candidate bodies with FTS5 bm25 over live_docs_fts (content
// column only), normalized so the best candidate hit scores 1.0. Bodies carry
// their frontmatter, so name/feature/domain matches are covered too. Empty when
// live is nil or nothing matches.
func scoreFTS(live *sql.DB, query string, candidates []artifacts.Artifact) map[string]float64 {
	out := map[string]float64{}
	match := ftsBodyQuery(query)
	if live == nil || match == "" {
		return out
	}
	byPath := make(map[string]string, len(candidates))
	for _, a := range candidates {
		if a.Worktree != "" && a.Path != "" {
			byPath[a.Worktree+"/.giantmem/"+a.Path] = a.ID
		}
	}
	rows, err := live.Query(
		`SELECT ld.path, bm25(live_docs_fts, 0, 0, 0, 0, 1)
           FROM live_docs_fts
           JOIN live_docs ld ON ld.rowid = live_docs_fts.rowid
          WHERE live_docs_fts MATCH ?`, match)
	if err != nil {
		return out
	}
	defer rows.Close()
	raw := map[string]float64{}
	best := 0.0
	for rows.Next() {
		var path string
		var s float64
		if err := rows.Scan(&path, &s); err != nil {
			return out
		}
		id, ok := byPath[path]
		if !ok || s >= 0 {
			continue
		}
		// fts5 bm25 is negative, more negative = better
		if s < best {
			best = s
		}
		raw[id] = s
	}
	if best == 0 {
		return out
	}
	for id, s := range raw {
		out[id] = s / best
	}
	return out
}

var ftsTokenRE = regexp.MustCompile(`[A-Za-z0-9_]+`)

// ftsBodyQuery turns a natural-language query into an OR match over the
// content column, so a prompt-length query still ranks by bm25 instead of
// requiring every word. Queries already using FTS5 syntax pass through.
func ftsBodyQuery(query string) string {
	query = strings.TrimSpace(query)
	if query == "" {
		return ""
	}
	if ftsOperatorRE.MatchString(query) {
		return query
	}
	seen := map[string]bool{}
	terms := make([]string, 0, 12)
	for _, tok := range ftsTokenRE.FindAllString(strings.ToLower(query), -1) {
		if len(tok) < 3 || seen[tok] {
			continue
		}
		seen[tok] = true
		terms = append(terms, `"`+tok+`"`)
		if len(terms) >= 12 {
			break
		}
	}
	if len(terms) == 0 {
		return ""
	}
	return "content: (" + strings.Join(terms, " OR ") + ")"
}

// recencyScore maps an artifact's Updated date to a [0, 1] decay. Today
// = 1.0, 30 days ago = ~0.74, 180 days ago = ~0.16. Exponential half-life
// of ~60 days.
func recencyScore(updated string, now time.Time) float64 {
	if updated == "" {
		return 0
	}
	t, err := time.Parse("2006-01-02", updated)
	if err != nil {
		if t, err = time.Parse(time.RFC3339, updated); err != nil {
			return 0
		}
	}
	ageDays := now.Sub(t).Hours() / 24
	if ageDays < 0 {
		return 1
	}
	return math.Exp(-ageDays / 60.0)
}

package search

import (
	"database/sql"
	"fmt"
	"math"
	"os"
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

	ftsScores := scoreFTS(query, candidates)
	vecScores := map[string]float64{}
	if w.Vector > 0 && live != nil && len(queryVec) > 0 {
		hits, err := NearestNeighbors(live, queryVec, 200)
		if err == nil {
			for _, h := range hits {
				vecScores[h.ArtifactID] = distanceToScore(h.Distance)
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

// scoreFTS returns substring-hit scores per artifact id. Body fetched
// lazily; small files. Score = 1.0 when the query substring appears, 0
// otherwise. Phase 2 keeps it cheap; future iteration could plug
// SQLite FTS bm25 here when artifact bodies are indexed.
func scoreFTS(query string, candidates []artifacts.Artifact) map[string]float64 {
	out := map[string]float64{}
	if strings.TrimSpace(query) == "" {
		return out
	}
	needle := strings.ToLower(query)
	for _, a := range candidates {
		for _, field := range []string{a.ID, a.Feature, a.Domain, a.Name} {
			if field != "" && strings.Contains(strings.ToLower(field), needle) {
				out[a.ID] = 1.0
				break
			}
		}
	}
	return out
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

package artifacts

import (
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// DomainSuggestion is one ranked candidate from SuggestDomains.
type DomainSuggestion struct {
	Domain string  `json:"domain"`
	Score  float64 `json:"score"`
}

// SuggestDomains scores each source-spec domain against `text` using TF-IDF
// over the source-spec corpus. Returns top-N suggestions, score descending.
// Empty result when no source-specs exist or no terms match.
//
// Repos slice scopes which repos' source-specs build the corpus; empty
// means the current workspace only.
func SuggestDomains(text string, candidates []Artifact, limit int) ([]DomainSuggestion, error) {
	if limit <= 0 {
		limit = 3
	}
	if strings.TrimSpace(text) == "" {
		return nil, nil
	}

	queryTokens := tokenize(text)
	if len(queryTokens) == 0 {
		return nil, nil
	}

	// Build per-domain document and DF table.
	docs := map[string][]string{} // domain -> token list (concatenated bodies)
	for _, a := range candidates {
		if a.Type != "source-spec" || a.Domain == "" {
			continue
		}
		body, err := readArtifactBody(a)
		if err != nil {
			continue
		}
		docs[a.Domain] = append(docs[a.Domain], tokenize(body)...)
	}
	if len(docs) == 0 {
		return nil, nil
	}

	// Term-frequency per doc + document-frequency over the corpus.
	termFreq := map[string]map[string]float64{}
	docFreq := map[string]int{}
	for domain, tokens := range docs {
		tf := map[string]float64{}
		seen := map[string]bool{}
		for _, t := range tokens {
			tf[t]++
			if !seen[t] {
				docFreq[t]++
				seen[t] = true
			}
		}
		// L2 normalize
		var sum float64
		for _, v := range tf {
			sum += v * v
		}
		if sum > 0 {
			norm := math.Sqrt(sum)
			for k := range tf {
				tf[k] /= norm
			}
		}
		termFreq[domain] = tf
	}
	N := float64(len(docs))

	// Score query against each domain.
	scores := []DomainSuggestion{}
	for domain, tf := range termFreq {
		var score float64
		for _, qt := range queryTokens {
			df := float64(docFreq[qt])
			if df == 0 {
				continue
			}
			idf := math.Log((N + 1) / (df + 1))
			score += tf[qt] * idf
		}
		if score > 0 {
			scores = append(scores, DomainSuggestion{Domain: domain, Score: score})
		}
	}
	sort.SliceStable(scores, func(i, j int) bool { return scores[i].Score > scores[j].Score })
	if len(scores) > limit {
		scores = scores[:limit]
	}
	return scores, nil
}

var tokenRe = regexp.MustCompile(`[a-zA-Z][a-zA-Z0-9_-]{2,}`)

// stopWords is a tiny English stopword set tuned for spec text.
var stopWords = map[string]bool{
	"the": true, "and": true, "for": true, "with": true, "that": true,
	"this": true, "from": true, "into": true, "via": true, "are": true,
	"any": true, "all": true, "not": true, "but": true, "you": true,
	"your": true, "must": true, "shall": true, "should": true, "may": true,
	"can": true, "when": true, "then": true, "given": true, "where": true,
	"what": true, "have": true, "has": true, "will": true, "use": true,
	"used": true, "uses": true, "see": true, "per": true,
	"one": true, "two": true, "three": true, "out": true, "now": true,
	"how": true, "why": true,
}

func tokenize(s string) []string {
	matches := tokenRe.FindAllString(strings.ToLower(s), -1)
	if matches == nil {
		return nil
	}
	out := make([]string, 0, len(matches))
	for _, m := range matches {
		if stopWords[m] {
			continue
		}
		out = append(out, m)
	}
	return out
}

// readArtifactBody is a small helper that reads the underlying file with
// frontmatter stripped. Returns empty body on any error.
func readArtifactBody(a Artifact) (string, error) {
	abs := artifactAbsPath(a)
	if abs == "" {
		return "", os.ErrNotExist
	}
	raw, err := os.ReadFile(abs)
	if err != nil {
		return "", err
	}
	_, body, _ := ParseFrontmatter(string(raw))
	return body, nil
}

func artifactAbsPath(a Artifact) string {
	if a.Worktree != "" {
		return filepath.Join(a.Worktree, ".giantmem", a.Path)
	}
	return ""
}

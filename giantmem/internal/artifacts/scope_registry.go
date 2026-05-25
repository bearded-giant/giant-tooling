package artifacts

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Scope is one entry in the user's scope registry. Mirrors
// ~/.giantmem-global/scopes.yaml.
type Scope struct {
	ID          string   `json:"scope_id"`
	Description string   `json:"description,omitempty"`
	Tags        []string `json:"tags,omitempty"`
	Repos       []string `json:"repos,omitempty"`
}

// ScopeRegistry is the in-memory cache of the YAML file. Use
// LoadScopeRegistry to populate.
type ScopeRegistry struct {
	mu       sync.RWMutex
	scopes   map[string]Scope // keyed by scope_id
	repoIdx  map[string][]string
	loadedAt time.Time
	path     string
}

// defaultRegistryTTL is how long an in-memory registry is trusted before
// callers should reload from disk. Cheap reload — YAML is tiny.
const defaultRegistryTTL = 5 * time.Minute

// ScopesYAMLPath returns the conventional path
// ~/.giantmem-global/scopes.yaml. Override via GIANTMEM_SCOPES_PATH for
// tests.
func ScopesYAMLPath() string {
	if v := os.Getenv("GIANTMEM_SCOPES_PATH"); v != "" {
		return v
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".giantmem-global", "scopes.yaml")
}

// LoadScopeRegistry parses the YAML at path and returns a populated
// registry. Missing file is not an error — returns an empty registry
// so callers can treat it as "no scopes configured yet".
func LoadScopeRegistry(path string) (*ScopeRegistry, error) {
	r := &ScopeRegistry{
		scopes:  map[string]Scope{},
		repoIdx: map[string][]string{},
		path:    path,
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			r.loadedAt = time.Now()
			return r, nil
		}
		return nil, fmt.Errorf("read scopes yaml: %w", err)
	}
	scopes, err := parseScopesYAML(raw)
	if err != nil {
		return nil, fmt.Errorf("parse scopes yaml: %w", err)
	}
	for id, sc := range scopes {
		sc.ID = id
		r.scopes[id] = sc
		for _, repo := range sc.Repos {
			r.repoIdx[repo] = append(r.repoIdx[repo], id)
		}
	}
	for repo := range r.repoIdx {
		sort.Strings(r.repoIdx[repo])
	}
	r.loadedAt = time.Now()
	return r, nil
}

// Scopes returns a sorted slice of all known scopes (stable order).
func (r *ScopeRegistry) Scopes() []Scope {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Scope, 0, len(r.scopes))
	for _, sc := range r.scopes {
		out = append(out, sc)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// Scope returns the scope record for id, or (Scope{}, false) when
// unknown.
func (r *ScopeRegistry) Scope(id string) (Scope, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	sc, ok := r.scopes[id]
	return sc, ok
}

// ScopesForRepo returns every scope_id whose `repos:` list includes
// repo. A repo can belong to multiple scopes.
func (r *ScopeRegistry) ScopesForRepo(repo string) []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if ids, ok := r.repoIdx[repo]; ok {
		out := make([]string, len(ids))
		copy(out, ids)
		return out
	}
	return nil
}

// MatchScope reports whether a given (repo, explicitScope) pair matches
// the filter scope_id. Explicit scope wins; falls back to registry
// membership.
func (r *ScopeRegistry) MatchScope(repo, explicitScope, filter string) bool {
	if filter == "" {
		return true
	}
	if explicitScope != "" {
		return explicitScope == filter
	}
	for _, id := range r.ScopesForRepo(repo) {
		if id == filter {
			return true
		}
	}
	return false
}

// Path returns the underlying YAML path.
func (r *ScopeRegistry) Path() string { return r.path }

// LoadedAt returns when the registry was last loaded from disk.
func (r *ScopeRegistry) LoadedAt() time.Time { return r.loadedAt }

// Empty reports whether the registry has zero scopes (no file or empty
// file). Convenient for "is the user using scopes yet?" checks.
func (r *ScopeRegistry) Empty() bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.scopes) == 0
}

// SaveScopeRegistry writes the registry back to its path in canonical
// YAML form (sorted scope IDs, repos sorted within each scope).
func SaveScopeRegistry(path string, scopes []Scope) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	var sb strings.Builder
	sb.WriteString("version: 1\n")
	sb.WriteString("scopes:\n")
	sort.Slice(scopes, func(i, j int) bool { return scopes[i].ID < scopes[j].ID })
	for _, sc := range scopes {
		sb.WriteString("  ")
		sb.WriteString(sc.ID)
		sb.WriteString(":\n")
		if sc.Description != "" {
			sb.WriteString("    description: ")
			sb.WriteString(yamlString(sc.Description))
			sb.WriteString("\n")
		}
		if len(sc.Tags) > 0 {
			sb.WriteString("    tags: [")
			for i, t := range sc.Tags {
				if i > 0 {
					sb.WriteString(", ")
				}
				sb.WriteString(yamlString(t))
			}
			sb.WriteString("]\n")
		}
		if len(sc.Repos) > 0 {
			repos := append([]string{}, sc.Repos...)
			sort.Strings(repos)
			sb.WriteString("    repos: [")
			for i, r := range repos {
				if i > 0 {
					sb.WriteString(", ")
				}
				sb.WriteString(yamlString(r))
			}
			sb.WriteString("]\n")
		}
	}
	return os.WriteFile(path, []byte(sb.String()), 0o644)
}

// yamlString quotes a value when it needs quoting (contains special chars).
// Most repo names and scope ids are bare-safe.
func yamlString(s string) string {
	for _, r := range s {
		switch r {
		case ':', '#', '&', '*', '!', '|', '>', '\'', '"', '%', '@', '`', ',', '[', ']', '{', '}':
			return fmt.Sprintf("%q", s)
		}
		if r == ' ' {
			return fmt.Sprintf("%q", s)
		}
	}
	if s == "" {
		return `""`
	}
	return s
}

// parseScopesYAML is a tiny, dependency-free parser for the exact subset
// the registry uses:
//
//	version: 1
//	scopes:
//	  scope_id:
//	    description: ...
//	    tags: [a, b]
//	    repos: [x, y]
//
// Anything outside that grammar is rejected. We intentionally avoid
// pulling in a YAML dep — giantmem builds stay CGO-free + single-binary.
func parseScopesYAML(raw []byte) (map[string]Scope, error) {
	lines := strings.Split(string(raw), "\n")
	out := map[string]Scope{}
	var (
		inScopes  bool
		curID     string
		curScope  Scope
		curIndent int
	)
	flush := func() {
		if curID == "" {
			return
		}
		out[curID] = curScope
		curID = ""
		curScope = Scope{}
	}
	for n, rawLine := range lines {
		line := strings.TrimRight(rawLine, " \t\r")
		if line == "" {
			continue
		}
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		indent := countLeadingSpaces(line)
		trim := strings.TrimSpace(line)

		if indent == 0 {
			flush()
			inScopes = false
			if trim == "scopes:" {
				inScopes = true
				continue
			}
			if strings.HasPrefix(trim, "version:") {
				continue
			}
			// unknown top-level — ignore (allows extension fields)
			continue
		}

		if !inScopes {
			continue
		}

		if indent == 2 && strings.HasSuffix(trim, ":") {
			flush()
			curID = strings.TrimSuffix(trim, ":")
			curScope = Scope{ID: curID}
			curIndent = indent
			continue
		}

		if curID == "" || indent <= curIndent {
			continue
		}

		k, v, ok := splitYAMLKV(trim)
		if !ok {
			return nil, fmt.Errorf("line %d: malformed key/value %q", n+1, trim)
		}
		switch k {
		case "description":
			curScope.Description = unquoteYAML(v)
		case "tags":
			curScope.Tags = parseYAMLInlineList(v)
		case "repos":
			curScope.Repos = parseYAMLInlineList(v)
		}
	}
	flush()
	return out, nil
}

func countLeadingSpaces(s string) int {
	n := 0
	for _, r := range s {
		if r == ' ' {
			n++
			continue
		}
		break
	}
	return n
}

func splitYAMLKV(s string) (string, string, bool) {
	i := strings.IndexByte(s, ':')
	if i < 0 {
		return "", "", false
	}
	return strings.TrimSpace(s[:i]), strings.TrimSpace(s[i+1:]), true
}

func unquoteYAML(s string) string {
	if len(s) >= 2 && (s[0] == '"' || s[0] == '\'') && s[len(s)-1] == s[0] {
		return s[1 : len(s)-1]
	}
	return s
}

func parseYAMLInlineList(v string) []string {
	v = strings.TrimSpace(v)
	if strings.HasPrefix(v, "[") && strings.HasSuffix(v, "]") {
		inner := strings.TrimSuffix(strings.TrimPrefix(v, "["), "]")
		raw := strings.Split(inner, ",")
		out := make([]string, 0, len(raw))
		for _, r := range raw {
			t := unquoteYAML(strings.TrimSpace(r))
			if t == "" {
				continue
			}
			out = append(out, t)
		}
		return out
	}
	return nil
}

package artifacts

import (
	"fmt"
	"strings"
)

// Artifact is one typed unit of project knowledge inside .giantmem/.
// Path is relative to the workspace directory (.giantmem root). Worktree is
// the absolute path of the parent worktree (one level up from .giantmem),
// so consumers can build absolute paths without re-discovering workspaces.
type Artifact struct {
	ID          string `json:"id"`
	Type        string `json:"type"`
	Feature     string `json:"feature,omitempty"`
	Domain      string `json:"domain,omitempty"`
	Name        string `json:"name,omitempty"`
	Status      string `json:"status"`
	Path        string `json:"path"`
	Repo        string `json:"repo"`
	Branch      string `json:"branch"`
	Worktree    string `json:"worktree,omitempty"`
	Size        int64  `json:"size"`
	Updated     string `json:"updated"`
	Created     string `json:"created,omitempty"`
	HasFront    bool   `json:"has_frontmatter"`
	Scope       string `json:"scope,omitempty"`
	Lifecycle   string `json:"lifecycle,omitempty"`
	AccessCount int    `json:"access_count,omitempty"`
}

// Index is the on-disk live view of one workspace's artifacts.
// Stored at <workspace>/.giantmem/artifacts.json.
type Index struct {
	Version   int        `json:"version"`
	Repo      string     `json:"repo"`
	Worktree  string     `json:"worktree"`
	Branch    string     `json:"branch"`
	IndexedAt string     `json:"indexed_at"`
	Artifacts []Artifact `json:"artifacts"`
}

const IndexVersion = 1

// BuildID returns the stable artifact ID. Format:
//   feat:{feature}:{type}[:{disc}]   (feature-scoped)
//   repo:{type}[:{name}]             (repo-level)
func BuildID(a Artifact) string {
	parts := []string{}
	if a.Feature != "" {
		parts = append(parts, "feat", a.Feature, a.Type)
	} else {
		parts = append(parts, "repo", a.Type)
	}
	if a.Domain != "" {
		parts = append(parts, a.Domain)
	} else if a.Name != "" {
		parts = append(parts, a.Name)
	}
	return strings.Join(parts, ":")
}

// ValidType reports whether t is in the v1 taxonomy.
func ValidType(t string) bool {
	switch t {
	case "source-spec", "delta-spec", "proposal", "design", "tasks",
		"plan", "research", "review", "domain", "notes", "pattern", "facts":
		return true
	}
	return false
}

// String renders an Artifact as a TSV row for human inspection.
func (a Artifact) String() string {
	return fmt.Sprintf("%s\t%s\t%s\t%s\t%s\t%s",
		a.ID, a.Type, a.Status, a.Feature, a.Domain, a.Path)
}

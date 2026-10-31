package artifacts

import (
	"path/filepath"
	"strings"
)

// Classification is the path-derived guess at what kind of artifact lives at
// some location under .giantmem/. Frontmatter, when present, overrides Type
// and Status but the rest of the fields fall back here.
type Classification struct {
	Type    string
	Feature string
	Domain  string
	Name    string
}

// Classify infers an artifact's identity from its workspace-relative path.
// Returns ok=false when the path does not match any known artifact location.
func Classify(rel string) (Classification, bool) {
	rel = filepath.ToSlash(rel)
	parts := strings.Split(rel, "/")
	if len(parts) == 0 {
		return Classification{}, false
	}

	last := parts[len(parts)-1]

	// infra + hidden: machine indexes and dot-dirs (.mdlive mirrors) are not
	// artifacts and stay out of browse
	for _, p := range parts {
		if strings.HasPrefix(p, ".") {
			return Classification{}, false
		}
	}
	switch last {
	case "artifacts.json", "features.json", "meta.json", "_index.md", "_history.md":
		return Classification{}, false
	}

	// .giantmem/specs/{domain}/spec.md  -> source-spec
	if len(parts) >= 3 && parts[0] == "specs" && last == "spec.md" {
		return Classification{Type: "source-spec", Domain: parts[1]}, true
	}

	// .giantmem/features/{name}/specs/{domain}/spec.md  -> delta-spec
	if len(parts) >= 5 && parts[0] == "features" && parts[2] == "specs" && last == "spec.md" {
		return Classification{Type: "delta-spec", Feature: parts[1], Domain: parts[3]}, true
	}

	// .giantmem/features/{name}/<known>.md
	if len(parts) >= 3 && parts[0] == "features" {
		feature := parts[1]
		tail := parts[2]
		switch tail {
		case "proposal.md", "spec.md":
			return Classification{Type: "proposal", Feature: feature}, true
		case "design.md":
			return Classification{Type: "design", Feature: feature}, true
		case "tasks.md":
			return Classification{Type: "tasks", Feature: feature}, true
		case "facts.md":
			return Classification{Type: "facts", Feature: feature}, true
		}
		if tail == feature+"-notes.md" {
			return Classification{Type: "notes", Feature: feature}, true
		}
		if c, ok := classifyLeaf(parts[2:]); ok {
			c.Feature = feature
			return c, true
		}
		if c, ok := classifyDir(parts[2:]); ok {
			c.Feature = feature
			return c, true
		}
		// catch-all: any other file inside a feature dir -> generic "file", so
		// ad-hoc outputs survive projection instead of vanishing. Name keeps the
		// feature-relative path for ID uniqueness across nested dirs.
		return Classification{Type: "file", Feature: feature, Name: strings.Join(parts[2:], "/")}, true
	}

	// .giantmem/domains/{name}.json  -> domain
	if len(parts) == 2 && parts[0] == "domains" && strings.HasSuffix(last, ".json") {
		return Classification{Type: "domain", Name: strings.TrimSuffix(last, ".json")}, true
	}

	// .giantmem/context/*.md  -> pattern
	if len(parts) == 2 && parts[0] == "context" && strings.HasSuffix(last, ".md") {
		return Classification{Type: "pattern", Name: strings.TrimSuffix(last, ".md")}, true
	}

	if len(parts) == 1 {
		switch last {
		case "WORKSPACE.md", "workspace.md":
			return Classification{Type: "workspace"}, true
		case "notes.md":
			return Classification{Type: "notes", Name: "notes"}, true
		}
	}
	if parts[0] == "history" && len(parts) >= 2 {
		return Classification{Type: "history", Name: strings.TrimSuffix(strings.Join(parts[1:], "/"), ".md")}, true
	}
	if c, ok := classifyLeaf(parts); ok {
		return c, true
	}
	if parts[0] == "prompts" && len(parts) >= 2 {
		return Classification{Type: "prompt", Name: strings.TrimSuffix(strings.Join(parts[1:], "/"), ".md")}, true
	}
	if parts[0] == "filebox" && len(parts) >= 2 {
		return Classification{Type: "filebox", Name: strings.Join(parts[1:], "/")}, true
	}
	if c, ok := classifyDir(parts); ok {
		return c, true
	}

	// global catch-all: real file in an unanticipated spot beats an untyped
	// hole in the index
	return Classification{Type: "file", Name: rel}, true
}

// classifyLeaf runs before any dir rule: a handoff is a handoff wherever it lands.
func classifyLeaf(parts []string) (Classification, bool) {
	last := parts[len(parts)-1]
	full := strings.Join(parts, "/")
	switch {
	case last == "handoff.md":
		return Classification{Type: "handoff", Name: full}, true
	case strings.HasPrefix(last, "mr-description") && strings.HasSuffix(last, ".md"):
		return Classification{Type: "mr", Name: full}, true
	case strings.HasSuffix(last, ".mmd"):
		return Classification{Type: "diagram", Name: full}, true
	}
	return Classification{}, false
}

func classifyDir(parts []string) (Classification, bool) {
	full := strings.Join(parts, "/")
	if len(parts) < 2 {
		return Classification{}, false
	}
	dir := parts[0]
	switch dir {
	// legacy dirs: Name drops the dir so pre-existing ids stay stable
	case "plans", "research", "reviews":
		t := map[string]string{"plans": "plan", "research": "research", "reviews": "review"}[dir]
		return Classification{Type: t, Name: strings.TrimSuffix(strings.Join(parts[1:], "/"), ".md")}, true
	case "grill":
		return Classification{Type: "review", Name: full}, true
	case "filebox":
		return Classification{Type: "filebox", Name: full}, true
	case "prompts":
		return Classification{Type: "prompt", Name: full}, true
	case "mr-descriptions":
		return Classification{Type: "mr", Name: full}, true
	case "runs":
		return Classification{Type: "run", Name: full}, true
	}
	switch {
	case strings.HasPrefix(dir, "swarm"):
		return Classification{Type: "run", Name: full}, true
	case strings.HasSuffix(dir, "-research"):
		return Classification{Type: "research", Name: full}, true
	}
	return Classification{}, false
}

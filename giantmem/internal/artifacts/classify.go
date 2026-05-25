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
//
// Rules match the Python backfill_frontmatter.py classifier exactly so the
// CLI and the script never disagree about what is "indexable".
func Classify(rel string) (Classification, bool) {
	rel = filepath.ToSlash(rel)
	parts := strings.Split(rel, "/")
	if len(parts) == 0 {
		return Classification{}, false
	}

	last := parts[len(parts)-1]

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
		if len(parts) >= 4 && strings.HasSuffix(last, ".md") {
			leaf := strings.TrimSuffix(last, ".md")
			switch tail {
			case "plans":
				return Classification{Type: "plan", Feature: feature, Name: leaf}, true
			case "research":
				return Classification{Type: "research", Feature: feature, Name: leaf}, true
			case "reviews":
				return Classification{Type: "review", Feature: feature, Name: leaf}, true
			}
		}
	}

	// .giantmem/research/*.md  -> repo-level research
	if len(parts) >= 2 && parts[0] == "research" && strings.HasSuffix(last, ".md") {
		return Classification{Type: "research", Name: strings.TrimSuffix(last, ".md")}, true
	}

	// .giantmem/plans/*.md  -> repo-level plan (transient session scratchpad)
	if len(parts) >= 2 && parts[0] == "plans" && strings.HasSuffix(last, ".md") {
		return Classification{Type: "plan", Name: strings.TrimSuffix(last, ".md")}, true
	}

	// .giantmem/domains/{name}.json  -> domain
	if len(parts) == 2 && parts[0] == "domains" && strings.HasSuffix(last, ".json") {
		return Classification{Type: "domain", Name: strings.TrimSuffix(last, ".json")}, true
	}

	// .giantmem/context/*.md  -> pattern
	if len(parts) == 2 && parts[0] == "context" && strings.HasSuffix(last, ".md") {
		return Classification{Type: "pattern", Name: strings.TrimSuffix(last, ".md")}, true
	}

	return Classification{}, false
}

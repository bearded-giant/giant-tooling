package artifacts

import (
	"strings"
	"time"
)

const (
	LifecycleCandidate  = "candidate"
	LifecycleDurable    = "durable"
	LifecycleDeprecated = "deprecated"
)

// validLifecycle reports whether v is one of the three known stages.
func validLifecycle(v string) bool {
	switch v {
	case LifecycleCandidate, LifecycleDurable, LifecycleDeprecated:
		return true
	}
	return false
}

// defaultLifecycle returns the lifecycle to stamp on a freshly-scanned
// artifact whose frontmatter did not declare one. Path heuristics catch
// AI-generated discoveries; everything else defaults to durable for
// backwards compat with existing artifacts.
func defaultLifecycle(relPath string) string {
	p := strings.ToLower(relPath)
	switch {
	case strings.HasSuffix(p, "context/discoveries.md"):
		return LifecycleCandidate
	case strings.HasPrefix(p, "research/") || strings.Contains(p, "/research/"):
		return LifecycleCandidate
	}
	return LifecycleDurable
}

// RetentionTier groups artifact types by how aggressively their candidate
// versions become stale. Tier values are not stored — they're a pure
// function of Type per design.md Decision 8.
type RetentionTier string

const (
	TierA RetentionTier = "A" // architecture / decisions — never expire
	TierB RetentionTier = "B" // summaries / patterns — 180 days
	TierC RetentionTier = "C" // ephemera — 90 days
)

// TierFor returns the retention tier for an artifact type. Unknown types
// fall through to tier C (most aggressive prune).
func TierFor(artifactType string) RetentionTier {
	switch artifactType {
	case "source-spec", "proposal", "design":
		return TierA
	case "pattern", "research", "notes":
		return TierB
	case "tasks", "plan", "review", "facts", "domain", "delta-spec":
		return TierC
	}
	return TierC
}

// StaleAfter returns the duration after which a candidate-lifecycle
// artifact of the given tier is considered stale. Tier A returns 0
// (never expires) and callers MUST treat 0 as "not stale".
func StaleAfter(t RetentionTier) time.Duration {
	switch t {
	case TierA:
		return 0
	case TierB:
		return 180 * 24 * time.Hour
	case TierC:
		return 90 * 24 * time.Hour
	}
	return 90 * 24 * time.Hour
}

// IsStale returns true when a is stale per its tier + lifecycle, given
// the current time. Durable artifacts in tier B/C are reported stale
// past their threshold but with the understanding that callers may
// surface them as "durable-stale" rather than prune them.
func IsStale(a Artifact, now time.Time) bool {
	tier := TierFor(a.Type)
	threshold := StaleAfter(tier)
	if threshold == 0 {
		return false
	}
	updated := parseArtifactDate(a.Updated)
	if updated.IsZero() {
		return false
	}
	age := now.Sub(updated)
	if age < threshold {
		return false
	}
	switch a.Lifecycle {
	case LifecycleCandidate, "":
		return true
	case LifecycleDurable:
		return true // surface but caller flags as durable-stale
	case LifecycleDeprecated:
		return true
	}
	return false
}

// IsDurableStale reports whether a is past its tier threshold AND marked
// durable — these should be surfaced but not deleted without --force.
func IsDurableStale(a Artifact, now time.Time) bool {
	if a.Lifecycle != LifecycleDurable {
		return false
	}
	tier := TierFor(a.Type)
	threshold := StaleAfter(tier)
	if threshold == 0 {
		return false
	}
	updated := parseArtifactDate(a.Updated)
	if updated.IsZero() {
		return false
	}
	return now.Sub(updated) >= threshold
}

// parseArtifactDate accepts either YYYY-MM-DD or RFC3339, returns zero
// Time on parse failure.
func parseArtifactDate(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	if t, err := time.Parse("2006-01-02", s); err == nil {
		return t
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t
	}
	return time.Time{}
}

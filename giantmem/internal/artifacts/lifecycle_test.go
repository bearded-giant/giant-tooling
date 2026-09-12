package artifacts

import "testing"

func TestDefaultLifecycle(t *testing.T) {
	cases := map[string]string{
		"context/discoveries.md":                  LifecycleCandidate,
		"research/x.md":                           LifecycleCandidate,
		"features/foo/research/x.md":              LifecycleCandidate,
		"history/sessions/20260912_150000_abc.md": LifecycleCandidate,
		"history/precompact_20260912_abc.md":      LifecycleCandidate,
		"features/foo/history/sessions/x.md":      LifecycleCandidate,
		"features/foo/foo-notes.md":               LifecycleCandidate,
		"notes.md":                                LifecycleCandidate,
		"context/tree.md":                         LifecycleCandidate,
		"context/add-tool-to-orchestrator.md":     LifecycleCandidate,
		"features/foo/proposal.md":                LifecycleDurable,
		"context/patterns.md":                     LifecycleDurable,
		"specs/auth/spec.md":                      LifecycleDurable,
		"filebox/history-of-the-project.md":       LifecycleDurable,
	}
	for rel, want := range cases {
		if got := defaultLifecycle(rel); got != want {
			t.Errorf("%s: got %s want %s", rel, got, want)
		}
	}
}

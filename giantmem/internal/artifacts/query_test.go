package artifacts

import (
	"testing"
	"time"
)

func TestListArtifacts_FiltersAccessAndVecJoins(t *testing.T) {
	d := newLiveDB(t)
	base := t.TempDir()

	insertLiveDoc(t, d, "/r/.giantmem/features/foo/proposal.md", "repoA", "/r",
		"---\nstatus: ready\n---\nbody", 1717200000)
	insertLiveDoc(t, d, "/r/.giantmem/features/foo/tasks.md", "repoA", "/r",
		"- [ ] a\n", 1717200000)
	insertLiveDoc(t, d, "/s/.giantmem/research/notes.md", "repoB", "/s",
		"some research", 1717200000)

	if _, err := ReconcileTable(d, base); err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	// access count: log two hits for the proposal in the 30-day window.
	now := time.Now().UTC().Format(time.RFC3339)
	for i := 0; i < 2; i++ {
		if _, err := d.Exec(
			`INSERT INTO artifact_access(artifact_id, accessed_at) VALUES (?, ?)`,
			"feat:foo:proposal", now); err != nil {
			t.Fatal(err)
		}
	}
	// embedding presence for the proposal only.
	if _, err := d.Exec(
		`INSERT INTO artifact_embedding_meta(artifact_id, rowid, body_hash, dim, model, updated_at)
         VALUES (?,?,?,?,?,?)`,
		"feat:foo:proposal", 1, "h", 768, "m", now); err != nil {
		t.Fatal(err)
	}

	// filter by type=proposal.
	rows, err := ListArtifacts(d, ListFilter{Type: []string{"proposal"}}, "", 0)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("proposal filter returned %d rows, want 1", len(rows))
	}
	p := rows[0]
	if p.AccessCount != 2 {
		t.Errorf("access_count = %d, want 2", p.AccessCount)
	}
	if !p.HasVec {
		t.Error("HasVec should be true for the embedded proposal")
	}

	// filter by repo.
	rb, err := ListArtifacts(d, ListFilter{Repo: "repoB"}, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(rb) != 1 || rb[0].Repo != "repoB" {
		t.Fatalf("repoB filter returned %d rows (want 1 repoB)", len(rb))
	}
	if rb[0].HasVec {
		t.Error("repoB research has no embedding; HasVec should be false")
	}

	// multi-value status filter + limit.
	all, err := ListArtifacts(d, ListFilter{}, "type", 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 {
		t.Fatalf("limit=2 returned %d rows", len(all))
	}
}

func TestFacetCounts(t *testing.T) {
	d := newLiveDB(t)
	base := t.TempDir()
	insertLiveDoc(t, d, "/r/.giantmem/features/foo/proposal.md", "repoA", "/r", "body", 1717200000)
	insertLiveDoc(t, d, "/r/.giantmem/features/foo/design.md", "repoA", "/r", "body", 1717200000)
	insertLiveDoc(t, d, "/r/.giantmem/research/notes.md", "repoA", "/r", "body", 1717200000)
	if _, err := ReconcileTable(d, base); err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	byType, byLifecycle, byStatus, err := FacetCounts(d)
	if err != nil {
		t.Fatalf("facets: %v", err)
	}
	if byType["proposal"] != 1 || byType["design"] != 1 || byType["research"] != 1 {
		t.Errorf("byType = %v, want proposal/design/research each 1", byType)
	}
	// research defaults candidate; proposal/design default durable.
	if byLifecycle[LifecycleCandidate] != 1 {
		t.Errorf("candidate count = %d, want 1 (research)", byLifecycle[LifecycleCandidate])
	}
	if byLifecycle[LifecycleDurable] != 2 {
		t.Errorf("durable count = %d, want 2", byLifecycle[LifecycleDurable])
	}
	total := 0
	for _, n := range byStatus {
		total += n
	}
	if total != 3 {
		t.Errorf("status facet total = %d, want 3", total)
	}
}

func TestTableHasRows(t *testing.T) {
	d := newLiveDB(t)
	if TableHasRows(d) {
		t.Error("empty table should report no rows")
	}
	insertLiveDoc(t, d, "/r/.giantmem/features/foo/proposal.md", "repoA", "/r", "body", 1717200000)
	if _, err := ReconcileTable(d, t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if !TableHasRows(d) {
		t.Error("populated table should report rows")
	}
}

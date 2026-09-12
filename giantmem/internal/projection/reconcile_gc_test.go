package projection

import (
	"strings"
	"testing"

	"github.com/bearded-giant/giant-tooling/giantmem/internal/search"
)

// Orphan vectors used to accumulate forever: deleteOrphans dropped the
// artifacts row but nothing touched artifact_embeddings / _meta, so ~77k dead
// rows kept ranking in every KNN. Reconcile must sweep them.
func TestReconcile_PrunesOrphanEmbeddings(t *testing.T) {
	d, base := newLive(t)
	insertDoc(t, d, "/r/.giantmem/features/foo/proposal.md", "---\nstatus: ready\n---\nbody one")
	if _, err := Reconcile(d, base, fakeEmbedder{768}); err != nil {
		t.Fatalf("reconcile 1: %v", err)
	}

	// embedding for an artifact id that does not exist in the artifacts table
	ghost := make([]float32, 768)
	for i := range ghost {
		ghost[i] = 0.3
	}
	if _, err := search.WriteEmbedding(d, "repo:ghost", "ghost body", ghost, "fake:test"); err != nil {
		t.Fatalf("write ghost: %v", err)
	}
	// vec0 row with no meta at all (half-written)
	dangling := "[" + strings.TrimSuffix(strings.Repeat("0.5,", 768), ",") + "]"
	if _, err := d.Exec(`INSERT INTO artifact_embeddings(embedding) VALUES (?)`, dangling); err != nil {
		t.Fatalf("insert dangling: %v", err)
	}

	before, _ := search.EmbeddingsCount(d)
	if before != 2 {
		t.Fatalf("meta before = %d, want 2", before)
	}

	st, err := Reconcile(d, base, fakeEmbedder{768})
	if err != nil {
		t.Fatalf("reconcile 2: %v", err)
	}
	if st.EmbeddingsPruned != 1 {
		t.Fatalf("pruned = %d, want 1", st.EmbeddingsPruned)
	}
	after, _ := search.EmbeddingsCount(d)
	if after != 1 {
		t.Fatalf("meta after = %d, want 1", after)
	}
	if m, _ := search.LoadEmbeddingMeta(d, "repo:ghost"); m != nil {
		t.Fatal("ghost meta should be gone")
	}

	hits, err := search.NearestNeighbors(d, ghost, 10)
	if err != nil {
		t.Fatalf("knn: %v", err)
	}
	if len(hits) != 1 {
		t.Fatalf("knn hits = %d, want 1 (only the live artifact)", len(hits))
	}
	for _, h := range hits {
		if h.ArtifactID == "repo:ghost" {
			t.Fatal("ghost still reachable via KNN")
		}
	}

	var vecRows int
	if err := d.QueryRow(`SELECT COUNT(*) FROM artifact_embeddings`).Scan(&vecRows); err != nil {
		t.Fatalf("count vec rows: %v", err)
	}
	if vecRows != 1 {
		t.Fatalf("vec rows = %d, want 1 (dangling row should be gone)", vecRows)
	}
}

package search

import (
	"path/filepath"
	"testing"

	"github.com/bearded-giant/giant-tooling/giantmem/internal/db"
)

// NearestNeighbors regressed silently once because the vec0 KNN query used a
// bare LIMIT instead of the required `k = ?` constraint, so every hybrid search
// ran with the vector arm returning nothing. This locks the KNN in.
func TestNearestNeighborsReturnsHits(t *testing.T) {
	d, err := db.Open(filepath.Join(t.TempDir(), "live.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer d.Close()

	dim := EmbedDim()
	near := make([]float32, dim)
	far := make([]float32, dim)
	for i := range near {
		near[i] = 0.02
		far[i] = -0.02
	}
	if _, err := WriteEmbedding(d, "near", "near body", near, "test"); err != nil {
		t.Fatalf("write near: %v", err)
	}
	if _, err := WriteEmbedding(d, "far", "far body", far, "test"); err != nil {
		t.Fatalf("write far: %v", err)
	}

	hits, err := NearestNeighbors(d, near, 2)
	if err != nil {
		t.Fatalf("NearestNeighbors: %v", err)
	}
	if len(hits) == 0 {
		t.Fatal("expected neighbors, got none (KNN k-constraint regression?)")
	}
	if hits[0].ArtifactID != "near" {
		t.Fatalf("expected closest = near, got %s", hits[0].ArtifactID)
	}
}

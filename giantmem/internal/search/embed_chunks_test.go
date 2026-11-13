package search

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/bearded-giant/giant-tooling/giantmem/internal/db"
)

type countingEmbedder struct {
	dim   int
	model string
	texts []string
}

func (c *countingEmbedder) Embed(text string) ([]float32, error) {
	c.texts = append(c.texts, text)
	v := make([]float32, c.dim)
	for i := range v {
		v[i] = float32(len(text)%97) / 97
	}
	v[0] = float32(len(c.texts))
	return v, nil
}
func (c *countingEmbedder) Dim() int          { return c.dim }
func (c *countingEmbedder) ModelName() string { return c.model }
func (c *countingEmbedder) Close() error      { return nil }

func section(title string) string {
	return "# " + title + "\n\n" + strings.Repeat(title+" words fill this paragraph. ", 100) + "\n\n"
}

// an edit to one section of a long doc used to re-embed every chunk
func TestEmbedChunks_ReusesUnchangedChunks(t *testing.T) {
	d, err := db.Open(filepath.Join(t.TempDir(), "live.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	e := &countingEmbedder{dim: EmbedDim(), model: "count"}

	body := section("alpha") + section("bravo") + section("charlie") + section("delta")
	chunks := ChunkBody(body)
	if len(chunks) < 4 {
		t.Fatalf("want >= 4 chunks, got %d", len(chunks))
	}
	vecs, reused, err := EmbedChunks(d, e, "doc", chunks)
	if err != nil || reused != 0 || len(e.texts) != len(chunks) {
		t.Fatalf("cold: reused=%d embedded=%d err=%v", reused, len(e.texts), err)
	}
	if _, err := WriteChunkEmbeddings(d, "doc", body, chunks, vecs, e.ModelName()); err != nil {
		t.Fatal(err)
	}

	edited := strings.Replace(body, "charlie words", "charlie WORDS", 1)
	chunks2 := ChunkBody(edited)
	e.texts = nil
	vecs2, reused, err := EmbedChunks(d, e, "doc", chunks2)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("edit: reused %d, embedded %d of %d chunks", reused, len(e.texts), len(chunks2))
	if reused == 0 || len(e.texts) == 0 || reused+len(e.texts) != len(chunks2) {
		t.Fatalf("edit: reused=%d embedded=%d of %d", reused, len(e.texts), len(chunks2))
	}
	for i, c := range chunks2 {
		if c.Text == chunks[i].Text && vecs2[i][0] != vecs[i][0] {
			t.Errorf("chunk %d unchanged but vector not reused: %v vs %v", i, vecs2[i][0], vecs[i][0])
		}
	}

	e.model = "other"
	e.texts = nil
	if _, reused, _ := EmbedChunks(d, e, "doc", chunks2); reused != 0 {
		t.Errorf("model change must not reuse vectors, reused=%d", reused)
	}
}

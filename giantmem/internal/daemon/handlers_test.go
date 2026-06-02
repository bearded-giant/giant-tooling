package daemon

import (
	"encoding/json"
	"testing"
)

type fakeEmb struct {
	dim   int
	model string
}

func (f fakeEmb) Embed(string) ([]float32, error) {
	v := make([]float32, f.dim)
	for i := range v {
		v[i] = 0.5
	}
	return v, nil
}
func (f fakeEmb) Dim() int { return f.dim }
func (f fakeEmb) ModelName() string {
	if f.model != "" {
		return f.model
	}
	return "fake:test"
}
func (f fakeEmb) Close() error { return nil }

func TestHandleEmbed_RealEmbedder(t *testing.T) {
	s := &Server{embedder: fakeEmb{dim: 4}}
	raw, _ := json.Marshal(EmbedParams{Text: "hello"})
	resp := s.handleEmbed(&Request{Params: raw})
	if resp.Error != nil {
		t.Fatalf("unexpected error: %v", resp.Error)
	}
	var out EmbedResult
	if err := json.Unmarshal(resp.Result, &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Vec) != 4 || out.Dim != 4 {
		t.Fatalf("vec=%d dim=%d, want 4/4", len(out.Vec), out.Dim)
	}
}

func TestHandleEmbed_StubRejected(t *testing.T) {
	s := &Server{embedder: fakeEmb{dim: 4, model: "stub:x"}}
	raw, _ := json.Marshal(EmbedParams{Text: "hi"})
	if resp := s.handleEmbed(&Request{Params: raw}); resp.Error == nil {
		t.Fatal("stub embedder must be rejected so the caller falls back")
	}
}

func TestHandleEmbed_NilRejected(t *testing.T) {
	s := &Server{}
	raw, _ := json.Marshal(EmbedParams{Text: "hi"})
	if resp := s.handleEmbed(&Request{Params: raw}); resp.Error == nil {
		t.Fatal("nil embedder must be rejected")
	}
}

func TestHandleEmbed_EmptyTextRejected(t *testing.T) {
	s := &Server{embedder: fakeEmb{dim: 4}}
	raw, _ := json.Marshal(EmbedParams{Text: ""})
	if resp := s.handleEmbed(&Request{Params: raw}); resp.Error == nil {
		t.Fatal("empty text must be rejected")
	}
}

package cmd

import (
	"os"
	"strings"
	"time"

	"github.com/bearded-giant/giant-tooling/giantmem/internal/daemon"
	"github.com/bearded-giant/giant-tooling/giantmem/internal/search"
)

// daemonQueryEmbedding asks the running daemon to embed text with its real
// backend. Returns (vec, true) on success; (nil, false) when the daemon is
// down, disabled, or has only a stub embedder — so callers fall back.
func daemonQueryEmbedding(text string) ([]float32, bool) {
	if os.Getenv("GIANTMEM_NO_DAEMON") != "" {
		return nil, false
	}
	sock := daemon.DefaultSocketPath()
	if !daemon.SocketAlive(sock, 250*time.Millisecond) {
		return nil, false
	}
	cli := daemon.NewClient(sock, 5*time.Second)
	var out daemon.EmbedResult
	if err := cli.Call("embed", &daemon.EmbedParams{Text: text}, &out); err != nil {
		return nil, false
	}
	if len(out.Vec) == 0 {
		return nil, false
	}
	return out.Vec, true
}

// resolveQueryVector returns a query embedding for hybrid search, preferring the
// daemon's real embedder so only the daemon loads the model. Falls back to a
// local real backend when one is configured; returns nil when only a stub is
// available, so callers run FTS-only rather than scoring stored real vectors
// against a non-semantic query vector.
func resolveQueryVector(query string) []float32 {
	if vec, ok := daemonQueryEmbedding(query); ok {
		return vec
	}
	emb, err := search.NewEmbedder("")
	if err != nil {
		return nil
	}
	defer emb.Close()
	if strings.HasPrefix(emb.ModelName(), "stub:") {
		return nil
	}
	vec, err := emb.Embed(query)
	if err != nil {
		return nil
	}
	return vec
}

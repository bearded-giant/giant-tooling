package search

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// DefaultEmbeddingDim is bge-base-en-v1.5 width. Tunable via GIANTMEM_EMBED_DIM.
const DefaultEmbeddingDim = 768

// DefaultEmbeddingModel is the labeled model name written into
// artifact_embedding_meta.model — informational, not loaded.
const DefaultEmbeddingModel = "BAAI/bge-base-en-v1.5"

// Embedder produces a fixed-dimension vector for a body of text.
type Embedder interface {
	Embed(text string) ([]float32, error)
	Dim() int
	ModelName() string
	Close() error
}

// EmbedDim resolves the configured embedding dimension.
func EmbedDim() int {
	v := os.Getenv("GIANTMEM_EMBED_DIM")
	if v == "" {
		return DefaultEmbeddingDim
	}
	if n, err := strconv.Atoi(v); err == nil && n > 0 {
		return n
	}
	return DefaultEmbeddingDim
}

// EmbedModel resolves the configured model label.
func EmbedModel() string {
	if v := os.Getenv("GIANTMEM_EMBED_MODEL"); v != "" {
		return v
	}
	return DefaultEmbeddingModel
}

// NewEmbedder constructs the requested backend. Falls back to stub when
// the requested backend cannot start.
func NewEmbedder(backend string) (Embedder, error) {
	backend = strings.ToLower(strings.TrimSpace(backend))
	if backend == "" {
		backend = strings.ToLower(os.Getenv("GIANTMEM_EMBED_BACKEND"))
	}
	if backend == "" {
		backend = "stub"
	}
	switch backend {
	case "stub":
		return newStubEmbedder(EmbedDim(), EmbedModel()), nil
	case "python":
		emb, err := newPythonEmbedder(EmbedDim(), EmbedModel())
		if err != nil {
			return nil, fmt.Errorf("python embedder: %w", err)
		}
		return emb, nil
	case "ollama":
		return newOllamaEmbedder(EmbedDim(), EmbedModel()), nil
	}
	return nil, fmt.Errorf("unknown embedder backend %q (stub|python|ollama)", backend)
}

// ----- stub embedder --------------------------------------------------------

type stubEmbedder struct {
	dim   int
	model string
}

func newStubEmbedder(dim int, model string) *stubEmbedder {
	return &stubEmbedder{dim: dim, model: "stub:" + model}
}

func (s *stubEmbedder) Dim() int          { return s.dim }
func (s *stubEmbedder) ModelName() string { return s.model }
func (s *stubEmbedder) Close() error      { return nil }

// Embed produces a deterministic, hash-derived vector. NOT semantic — used
// only to test storage + score plumbing. Same text always yields same
// vector. Output normalized to unit length so cosine similarity behaves.
func (s *stubEmbedder) Embed(text string) ([]float32, error) {
	out := make([]float32, s.dim)
	seed := sha256.Sum256([]byte(text))
	for i := range out {
		// Spread the 32-byte digest across `dim` floats with a tiny
		// linear-congruential walk seeded from the digest.
		b := seed[i%len(seed)]
		x := math.Sin(float64(i+1)*float64(b)*0.01) // -1..1
		out[i] = float32(x)
	}
	return normalize(out), nil
}

// ----- python subprocess embedder -------------------------------------------

type pythonEmbedder struct {
	dim   int
	model string

	mu      sync.Mutex
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	stdout  io.ReadCloser
	scanner *json.Decoder
	started bool
}

func newPythonEmbedder(dim int, model string) (*pythonEmbedder, error) {
	e := &pythonEmbedder{dim: dim, model: model}
	if err := e.ensureStarted(); err != nil {
		return nil, err
	}
	return e, nil
}

func (e *pythonEmbedder) ensureStarted() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.started {
		return nil
	}
	scriptPath := PythonEmbedScriptPath()
	if _, err := os.Stat(scriptPath); err != nil {
		return fmt.Errorf("embed.py missing at %s", scriptPath)
	}
	python := os.Getenv("GIANTMEM_EMBED_PYTHON")
	if python == "" {
		python = "python3"
	}
	cmd := exec.Command(python, scriptPath,
		"--model", e.model,
		"--dim", strconv.Itoa(e.dim),
	)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start python embedder: %w", err)
	}
	e.cmd = cmd
	e.stdin = stdin
	e.stdout = stdout
	e.scanner = json.NewDecoder(stdout)
	// consume the ready handshake — daemon emits {"ready": true, ...} before
	// accepting requests
	var ready struct {
		Ready bool   `json:"ready"`
		Error string `json:"error,omitempty"`
	}
	if err := e.scanner.Decode(&ready); err != nil {
		return fmt.Errorf("read ready handshake: %w", err)
	}
	if !ready.Ready {
		return fmt.Errorf("embedder not ready: %s", ready.Error)
	}
	e.started = true
	return nil
}

func PythonEmbedScriptPath() string {
	if v := os.Getenv("GIANTMEM_EMBED_SCRIPT"); v != "" {
		return v
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "dev", "giant-tooling", "workspace", "scripts", "embed.py")
}

func (e *pythonEmbedder) Dim() int          { return e.dim }
func (e *pythonEmbedder) ModelName() string { return "python:" + e.model }

func (e *pythonEmbedder) Embed(text string) ([]float32, error) {
	if err := e.ensureStarted(); err != nil {
		return nil, err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	req, _ := json.Marshal(map[string]any{"text": text})
	if _, err := e.stdin.Write(append(req, '\n')); err != nil {
		return nil, fmt.Errorf("write embed request: %w", err)
	}
	var resp struct {
		Vec   []float32 `json:"vec"`
		Error string    `json:"error,omitempty"`
	}
	if err := e.scanner.Decode(&resp); err != nil {
		return nil, fmt.Errorf("read embed response: %w", err)
	}
	if resp.Error != "" {
		return nil, fmt.Errorf("embedder error: %s", resp.Error)
	}
	if len(resp.Vec) != e.dim {
		return nil, fmt.Errorf("dim mismatch: got %d, expected %d", len(resp.Vec), e.dim)
	}
	return resp.Vec, nil
}

func (e *pythonEmbedder) Close() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !e.started {
		return nil
	}
	_ = e.stdin.Close()
	_ = e.cmd.Wait()
	e.started = false
	return nil
}

// ----- ollama embedder ------------------------------------------------------

type ollamaEmbedder struct {
	dim   int
	model string
	host  string
	cli   *http.Client
}

func newOllamaEmbedder(dim int, model string) *ollamaEmbedder {
	host := os.Getenv("OLLAMA_HOST")
	if host == "" {
		host = "http://127.0.0.1:11434"
	}
	return &ollamaEmbedder{
		dim:   dim,
		model: model,
		host:  host,
		cli:   &http.Client{Timeout: 30 * time.Second},
	}
}

func (o *ollamaEmbedder) Dim() int          { return o.dim }
func (o *ollamaEmbedder) ModelName() string { return "ollama:" + o.model }
func (o *ollamaEmbedder) Close() error      { return nil }

func (o *ollamaEmbedder) Embed(text string) ([]float32, error) {
	payload := map[string]any{"model": o.model, "prompt": text}
	body, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(context.Background(),
		"POST", o.host+"/api/embeddings", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := o.cli.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ollama: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		raw, _ := io.ReadAll(res.Body)
		return nil, fmt.Errorf("ollama %d: %s", res.StatusCode, string(raw))
	}
	var resp struct {
		Embedding []float32 `json:"embedding"`
	}
	if err := json.NewDecoder(res.Body).Decode(&resp); err != nil {
		return nil, err
	}
	if len(resp.Embedding) != o.dim {
		return nil, fmt.Errorf("ollama dim mismatch: got %d, expected %d", len(resp.Embedding), o.dim)
	}
	return resp.Embedding, nil
}

// ----- storage helpers ------------------------------------------------------

// EmbeddingMeta is one chunk's row in artifact_embedding_meta. The vector
// itself lives in the artifact_embeddings vec0 virtual table keyed on rowid.
// Every chunk of an artifact carries the same BodyHash.
type EmbeddingMeta struct {
	ArtifactID string
	Ord        int
	RowID      int64
	Start      int
	End        int
	Snippet    string
	BodyHash   string
	Dim        int
	Model      string
	UpdatedAt  string
}

// BodyHash returns sha256 hex of body (stripped of frontmatter by the caller).
func BodyHash(body string) string {
	sum := sha256.Sum256([]byte(body))
	return hex.EncodeToString(sum[:])
}

// LoadEmbeddingMeta returns the head-chunk (ord 0) meta row for artifactID,
// or (nil, nil) when no embedding exists. Its BodyHash and Dim stand for the
// whole artifact since chunks are written atomically.
func LoadEmbeddingMeta(db *sql.DB, artifactID string) (*EmbeddingMeta, error) {
	var m EmbeddingMeta
	err := db.QueryRow(
		`SELECT artifact_id, ord, rowid, chunk_start, chunk_end, snippet, body_hash, dim, model, updated_at
         FROM artifact_embedding_meta WHERE artifact_id = ? AND ord = 0`,
		artifactID,
	).Scan(&m.ArtifactID, &m.Ord, &m.RowID, &m.Start, &m.End, &m.Snippet, &m.BodyHash, &m.Dim, &m.Model, &m.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &m, nil
}

// WriteEmbedding stores a single whole-body vector (one chunk). Kept for
// callers and tests that embed short texts; production paths chunk first.
func WriteEmbedding(db *sql.DB, artifactID, body string, vec []float32, model string) (bool, error) {
	return WriteChunkEmbeddings(db, artifactID, body,
		[]Chunk{{Ord: 0, Start: 0, End: len(body), Text: body}}, [][]float32{vec}, model)
}

// WriteChunkEmbeddings replaces an artifact's vectors with one row per chunk.
// Skips when the body hash and dim already match. Returns changed=true when a
// write occurred.
func WriteChunkEmbeddings(db *sql.DB, artifactID, body string, chunks []Chunk, vecs [][]float32, model string) (bool, error) {
	if db == nil {
		return false, fmt.Errorf("nil db")
	}
	if len(chunks) == 0 || len(chunks) != len(vecs) {
		return false, fmt.Errorf("chunks/vecs mismatch: %d vs %d", len(chunks), len(vecs))
	}
	dim := len(vecs[0])
	if dim == 0 {
		return false, fmt.Errorf("empty vec")
	}
	hash := BodyHash(body)
	existing, err := LoadEmbeddingMeta(db, artifactID)
	if err != nil {
		return false, err
	}
	if existing != nil && existing.BodyHash == hash && existing.Dim == dim {
		return false, nil
	}

	tx, err := db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(
		`DELETE FROM artifact_embeddings WHERE rowid IN (
            SELECT rowid FROM artifact_embedding_meta WHERE artifact_id = ?)`, artifactID,
	); err != nil {
		return false, err
	}
	if _, err := tx.Exec(`DELETE FROM artifact_embedding_meta WHERE artifact_id = ?`, artifactID); err != nil {
		return false, err
	}
	now := time.Now().UTC().Format(time.RFC3339)
	for i, c := range chunks {
		if len(vecs[i]) != dim {
			return false, fmt.Errorf("chunk %d dim %d != %d", i, len(vecs[i]), dim)
		}
		res, err := tx.Exec(`INSERT INTO artifact_embeddings(embedding) VALUES (?)`, vecToJSON(vecs[i]))
		if err != nil {
			return false, err
		}
		rowID, err := res.LastInsertId()
		if err != nil {
			return false, err
		}
		if _, err := tx.Exec(
			`INSERT INTO artifact_embedding_meta(artifact_id, ord, rowid, chunk_start, chunk_end, snippet,
                 body_hash, dim, model, updated_at)
             VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			artifactID, c.Ord, rowID, c.Start, c.End, Snippet(c.Text), hash, dim, model, now,
		); err != nil {
			return false, err
		}
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

// ResetEmbeddings drops every row in both vec table and meta. Used by
// `giantmem embed --reset`.
func ResetEmbeddings(db *sql.DB) error {
	if _, err := db.Exec(`DELETE FROM artifact_embeddings`); err != nil {
		return err
	}
	if _, err := db.Exec(`DELETE FROM artifact_embedding_meta`); err != nil {
		return err
	}
	return nil
}

// DeleteOrphanEmbeddings removes vectors whose artifact no longer exists, plus
// vec0 rows with no meta. Returns the number of meta rows removed.
func DeleteOrphanEmbeddings(db *sql.DB) (int, error) {
	tx, err := db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	// vec0 rows first while meta still maps artifact_id -> rowid
	if _, err := tx.Exec(
		`DELETE FROM artifact_embeddings WHERE rowid IN (
            SELECT rowid FROM artifact_embedding_meta
            WHERE artifact_id NOT IN (SELECT id FROM artifacts))`,
	); err != nil {
		return 0, err
	}
	res, err := tx.Exec(
		`DELETE FROM artifact_embedding_meta WHERE artifact_id NOT IN (SELECT id FROM artifacts)`,
	)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	if _, err := tx.Exec(
		`DELETE FROM artifact_embeddings WHERE rowid NOT IN (SELECT rowid FROM artifact_embedding_meta)`,
	); err != nil {
		return 0, err
	}
	return int(n), tx.Commit()
}

// EmbeddingsCount returns the number of artifacts with at least one vector.
func EmbeddingsCount(db *sql.DB) (int, error) {
	var n int
	err := db.QueryRow(`SELECT COUNT(DISTINCT artifact_id) FROM artifact_embedding_meta`).Scan(&n)
	return n, err
}

// NearestNeighbors runs a KNN query against the vec0 table and returns one hit
// per matching chunk, sorted ascending by distance (closer first). Callers
// wanting one score per artifact take the best chunk.
func NearestNeighbors(db *sql.DB, vec []float32, limit int) ([]VecHit, error) {
	if limit <= 0 {
		limit = 20
	}
	jsonVec := vecToJSON(vec)
	// vec0 KNN needs the k constraint in WHERE; a bare outer LIMIT is invisible
	// to the KNN planner once a JOIN wraps the vec0 scan.
	rows, err := db.Query(
		`SELECT m.artifact_id, m.ord, m.snippet, e.distance
         FROM artifact_embeddings e
         JOIN artifact_embedding_meta m ON m.rowid = e.rowid
         WHERE e.embedding MATCH ? AND k = ?
         ORDER BY e.distance`,
		jsonVec, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []VecHit{}
	for rows.Next() {
		var h VecHit
		if err := rows.Scan(&h.ArtifactID, &h.Ord, &h.Snippet, &h.Distance); err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

// VecHit is one chunk row from a KNN query.
type VecHit struct {
	ArtifactID string  `json:"id"`
	Ord        int     `json:"ord"`
	Snippet    string  `json:"snippet,omitempty"`
	Distance   float64 `json:"distance"`
}

// vecToJSON formats a float32 slice as the JSON-array form sqlite-vec accepts.
func vecToJSON(vec []float32) string {
	var sb strings.Builder
	sb.WriteByte('[')
	for i, f := range vec {
		if i > 0 {
			sb.WriteByte(',')
		}
		sb.WriteString(strconv.FormatFloat(float64(f), 'f', 6, 32))
	}
	sb.WriteByte(']')
	return sb.String()
}

func normalize(v []float32) []float32 {
	var sum float64
	for _, x := range v {
		sum += float64(x) * float64(x)
	}
	if sum == 0 {
		return v
	}
	norm := math.Sqrt(sum)
	for i := range v {
		v[i] = float32(float64(v[i]) / norm)
	}
	return v
}

package daemon

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	gmdb "github.com/bearded-giant/giant-tooling/giantmem/internal/db"
	"github.com/bearded-giant/giant-tooling/giantmem/internal/projection"
	"github.com/bearded-giant/giant-tooling/giantmem/internal/search"
	"github.com/fsnotify/fsnotify"
)

// Server is the giantmemd unix-socket JSON-RPC server.
type Server struct {
	socketPath  string
	archivePath string
	livePath    string
	startedAt   time.Time
	requests    atomic.Int64

	mu              sync.RWMutex
	archiveDB       *sql.DB
	liveDB          *sql.DB
	archiveSchemaAt int
	liveSchemaAt    int

	// schema versions baked into this binary (compared on every request)
	binaryArchiveSchema int
	binaryLiveSchema    int

	reconcileMu sync.Mutex

	listener net.Listener
}

// NewServer constructs a Server but does not start it.
func NewServer(socketPath, archivePath, livePath string) *Server {
	return &Server{
		socketPath:  socketPath,
		archivePath: archivePath,
		livePath:    livePath,
		startedAt:   time.Now(),
	}
}

// Start opens DBs, binds the socket, and serves until ctx is cancelled.
// Returns when the listener closes.
func (s *Server) Start(ctx context.Context) error {
	if err := s.openDBs(); err != nil {
		return err
	}
	// Set-and-forget projection engine: reconcile once at start, then
	// continuously as the peer writes live_docs. Non-blocking; the socket comes
	// up immediately regardless.
	s.startReconciler(ctx)
	if err := os.MkdirAll(filepath.Dir(s.socketPath), 0o755); err != nil {
		return err
	}
	// remove stale socket
	if _, err := os.Stat(s.socketPath); err == nil {
		_ = os.Remove(s.socketPath)
	}
	l, err := net.Listen("unix", s.socketPath)
	if err != nil {
		return err
	}
	s.listener = l
	// pidfile next to socket
	pidPath := s.socketPath + ".pid"
	_ = os.WriteFile(pidPath, []byte(fmt.Sprintf("%d", os.Getpid())), 0o644)
	go func() {
		<-ctx.Done()
		l.Close()
		_ = os.Remove(pidPath)
	}()

	for {
		conn, err := l.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return nil
			}
			fmt.Fprintf(os.Stderr, "accept: %v\n", err)
			continue
		}
		go s.serveConn(conn)
	}
}

// Close releases server resources. Safe to call multiple times.
func (s *Server) Close() error {
	if s.listener != nil {
		s.listener.Close()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.archiveDB != nil {
		s.archiveDB.Close()
	}
	if s.liveDB != nil {
		s.liveDB.Close()
	}
	_ = os.Remove(s.socketPath)
	return nil
}

func (s *Server) openDBs() error {
	a, err := gmdb.Open(s.archivePath)
	if err != nil {
		return fmt.Errorf("open archive: %w", err)
	}
	s.archiveDB = a
	av, _ := gmdb.SchemaVersion(a)
	s.archiveSchemaAt = av
	s.binaryArchiveSchema = av // baked at start

	if _, err := os.Stat(s.livePath); err == nil {
		l, err := gmdb.Open(s.livePath)
		if err != nil {
			return fmt.Errorf("open live: %w", err)
		}
		s.liveDB = l
		lv, _ := gmdb.SchemaVersion(l)
		s.liveSchemaAt = lv
		s.binaryLiveSchema = lv
	}
	return nil
}

// schemaDriftLocked checks if the on-disk schema versions have advanced past
// what this daemon was launched with. Caller holds s.mu.
func (s *Server) schemaDriftLocked() bool {
	cur, _ := gmdb.SchemaVersion(s.archiveDB)
	if cur != s.binaryArchiveSchema {
		return true
	}
	if s.liveDB != nil {
		curL, _ := gmdb.SchemaVersion(s.liveDB)
		if curL != s.binaryLiveSchema {
			return true
		}
	}
	return false
}

func (s *Server) serveConn(c net.Conn) {
	defer c.Close()
	r := bufio.NewReader(c)
	dec := json.NewDecoder(r)
	enc := json.NewEncoder(c)
	for {
		var req Request
		if err := dec.Decode(&req); err != nil {
			if !errors.Is(err, io.EOF) {
				_ = enc.Encode(errorReply(req.ID, InternalError, "decode: "+err.Error()))
			}
			return
		}
		s.requests.Add(1)
		resp := s.dispatch(&req)
		if err := enc.Encode(resp); err != nil {
			return
		}
	}
}

func (s *Server) dispatch(req *Request) *Response {
	s.mu.RLock()
	drift := s.schemaDriftLocked()
	s.mu.RUnlock()
	if drift {
		return errorReply(req.ID, SchemaMismatch, "schema migration pending; restart giantmemd")
	}

	switch req.Method {
	case "find":
		return s.handleFind(req)
	case "health":
		return s.handleHealth(req)
	case "ping":
		return okReply(req.ID, map[string]string{"pong": "ok"})
	default:
		return errorReply(req.ID, NotFound, "unknown method: "+req.Method)
	}
}

func okReply(id json.RawMessage, v any) *Response {
	b, _ := json.Marshal(v)
	return &Response{JSONRPC: JSONRPCVersion, ID: id, Result: b}
}

func errorReply(id json.RawMessage, code int, msg string) *Response {
	return &Response{
		JSONRPC: JSONRPCVersion,
		ID:      id,
		Error:   &Error{Code: code, Message: msg},
	}
}

func (s *Server) handleHealth(req *Request) *Response {
	var rss uint64
	{
		var m runtime.MemStats
		runtime.ReadMemStats(&m)
		rss = m.Alloc
	}
	s.mu.RLock()
	driftFlag := s.schemaDriftLocked()
	curArch, _ := gmdb.SchemaVersion(s.archiveDB)
	curLive := 0
	if s.liveDB != nil {
		curLive, _ = gmdb.SchemaVersion(s.liveDB)
	}
	s.mu.RUnlock()

	res := HealthResult{
		Uptime:           time.Since(s.startedAt).Truncate(time.Second).String(),
		RSS:              rss,
		Requests:         s.requests.Load(),
		ArchiveSchema:    curArch,
		LiveSchema:       curLive,
		BinarySchemaArch: s.binaryArchiveSchema,
		BinarySchemaLive: s.binaryLiveSchema,
		Drift:            driftFlag,
	}
	return okReply(req.ID, res)
}

// startReconciler kicks off the artifacts-projection engine: one pass at start
// plus a debounced fsnotify watch on live.db so the table tracks peer writes
// without a session restart. No-op when live.db wasn't present at boot.
func (s *Server) startReconciler(ctx context.Context) {
	if s.liveDB == nil {
		return
	}
	archiveBase := filepath.Dir(s.livePath)
	embedder, err := search.NewEmbedder(os.Getenv("GIANTMEM_EMBED_BACKEND"))
	if err != nil {
		fmt.Fprintf(os.Stderr, "giantmemd: embeddings disabled: %v\n", err)
		embedder = nil
	}

	run := func(reason string) {
		s.reconcileMu.Lock()
		defer s.reconcileMu.Unlock()
		st, err := projection.Reconcile(s.liveDB, archiveBase, embedder)
		if err != nil {
			fmt.Fprintf(os.Stderr, "giantmemd: reconcile (%s) failed: %v\n", reason, err)
			return
		}
		fmt.Fprintf(os.Stderr,
			"giantmemd: reconcile (%s) scanned=%d upserted=%d removed=%d canonical=%d embedded=%d\n",
			reason, st.Scanned, st.Upserted, st.Removed, st.Canonical, st.Embedded)
	}

	go run("start")
	go s.watchAndReconcile(ctx, run, embedder)
}

func (s *Server) watchAndReconcile(ctx context.Context, run func(string), embedder search.Embedder) {
	defer func() {
		if embedder != nil {
			embedder.Close()
		}
	}()

	w, err := fsnotify.NewWatcher()
	if err != nil {
		fmt.Fprintf(os.Stderr, "giantmemd: fsnotify unavailable, continuous reconcile off: %v\n", err)
		<-ctx.Done()
		return
	}
	defer w.Close()
	// Watch the archive dir so we catch live.db, live.db-wal, and live.db-shm
	// regardless of which one a given write touches (WAL mode flips between them).
	_ = w.Add(filepath.Dir(s.livePath))

	debounce := time.Second
	var timer *time.Timer
	trigger := func() {
		if timer != nil {
			timer.Stop()
		}
		timer = time.AfterFunc(debounce, func() { run("watch") })
	}

	liveName := filepath.Base(s.livePath)
	for {
		select {
		case <-ctx.Done():
			if timer != nil {
				timer.Stop()
			}
			return
		case ev, ok := <-w.Events:
			if !ok {
				return
			}
			if ev.Op&(fsnotify.Write|fsnotify.Create) == 0 {
				continue
			}
			if !strings.HasPrefix(filepath.Base(ev.Name), liveName) {
				continue // ignore archives.db and unrelated churn
			}
			trigger()
		case werr, ok := <-w.Errors:
			if !ok {
				return
			}
			fmt.Fprintf(os.Stderr, "giantmemd: watch error: %v\n", werr)
		}
	}
}

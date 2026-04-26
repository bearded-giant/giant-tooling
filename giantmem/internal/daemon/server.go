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
	"sync"
	"sync/atomic"
	"time"

	gmdb "github.com/bearded-giant/giant-tooling/giantmem/internal/db"
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

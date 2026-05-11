// Package daemon implements the giantmemd long-running RPC server and a
// thin client. Wire format is JSON-RPC 2.0 framed by newlines on a unix socket.
package daemon

import "encoding/json"

const (
	JSONRPCVersion = "2.0"
	SchemaMismatch = -32001
	NotFound       = -32601
	InternalError  = -32603
)

// Request is one JSON-RPC frame. ID is opaque and echoed in the response.
type Request struct {
	JSONRPC string          `json:"jsonrpc"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
	ID      json.RawMessage `json:"id,omitempty"`
}

// Response mirrors Request. Either Result or Error is set.
type Response struct {
	JSONRPC string          `json:"jsonrpc"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *Error          `json:"error,omitempty"`
	ID      json.RawMessage `json:"id,omitempty"`
}

// Error is the JSON-RPC error envelope.
type Error struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

// FindParams is the shape exchanged on the wire for "find".
type FindParams struct {
	Query        string `json:"query"`
	Project      string `json:"project,omitempty"`
	DirType      string `json:"dir_type,omitempty"`
	SourceType   string `json:"source_type,omitempty"`
	Feature      string `json:"feature,omitempty"`
	Latest       bool   `json:"latest,omitempty"`
	LiveOnly     bool   `json:"live_only,omitempty"`
	ArchiveOnly  bool   `json:"archive_only,omitempty"`
	Since        string `json:"since,omitempty"`
	Until        string `json:"until,omitempty"`
	Limit        int    `json:"limit,omitempty"`
	IncludeFull  bool   `json:"include_full,omitempty"`
}

// HealthResult is the daemon health report.
type HealthResult struct {
	Uptime          string  `json:"uptime"`
	RSS             uint64  `json:"rss_bytes"`
	Requests        int64   `json:"requests"`
	ArchiveSchema   int     `json:"archive_schema"`
	LiveSchema      int     `json:"live_schema"`
	BinarySchemaArch int    `json:"binary_archive_schema"`
	BinarySchemaLive int    `json:"binary_live_schema"`
	Drift           bool    `json:"schema_drift"`
	Bench           *Bench  `json:"bench,omitempty"`
}

// Bench is optional benchmark numbers attached to health.
type Bench struct {
	FindP50Ms   float64 `json:"find_p50_ms"`
	FindP99Ms   float64 `json:"find_p99_ms"`
	StatusP50Ms float64 `json:"status_p50_ms"`
	StatusP99Ms float64 `json:"status_p99_ms"`
	Iterations  int     `json:"iterations"`
}

// StatusParams is the wire shape for the "status" method.
type StatusParams struct {
	Root    string `json:"root,omitempty"`
	Project string `json:"project,omitempty"`
	StaleD  int    `json:"stale_days,omitempty"`
}

// PrimeParams is the wire shape for the "prime" method.
type PrimeParams struct {
	Cwd      string `json:"cwd"`
	RecentN  int    `json:"recent_n,omitempty"`
	SessionN int    `json:"session_n,omitempty"`
	HistoryN int    `json:"history_n,omitempty"`
}

// SessionListParams is the wire shape for the "session.list" method.
type SessionListParams struct {
	Project string `json:"project,omitempty"`
	Limit   int    `json:"limit,omitempty"`
}

// SessionFindParams is the wire shape for the "session.find" method.
type SessionFindParams struct {
	Query string `json:"query"`
	Limit int    `json:"limit,omitempty"`
}

// TimelineParams is the wire shape for the "timeline" method.
type TimelineParams struct {
	Days    int    `json:"days,omitempty"`
	Project string `json:"project,omitempty"`
	Source  string `json:"source,omitempty"`
}

package daemon

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"
)

// DefaultSocketPath returns the canonical giantmemd socket location.
func DefaultSocketPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".cache", "giantmem", "giantmemd.sock")
}

// SocketAlive returns true if the socket file exists and a one-shot ping
// succeeds within timeout.
func SocketAlive(path string, timeout time.Duration) bool {
	if _, err := os.Stat(path); err != nil {
		return false
	}
	c, err := net.DialTimeout("unix", path, timeout)
	if err != nil {
		return false
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(timeout))
	if _, err := c.Write([]byte(`{"jsonrpc":"2.0","method":"ping","id":0}` + "\n")); err != nil {
		return false
	}
	dec := json.NewDecoder(c)
	var r Response
	if err := dec.Decode(&r); err != nil {
		return false
	}
	return r.Error == nil
}

// Client is a single-shot RPC dialer. Each Call dials, sends, reads, closes.
// The daemon serves long-lived connections too, but for a CLI hit it doesn't
// matter and short-lived avoids us tracking state.
type Client struct {
	socketPath string
	timeout    time.Duration
	id         atomic.Int64
}

// NewClient targets a socket path with a per-call timeout.
func NewClient(socketPath string, timeout time.Duration) *Client {
	if timeout == 0 {
		timeout = 5 * time.Second
	}
	return &Client{socketPath: socketPath, timeout: timeout}
}

// Call sends a single request and decodes the result into out (a pointer).
// Returns the RPC Error code/message verbatim if the daemon errored.
func (c *Client) Call(method string, params, out any) error {
	conn, err := net.DialTimeout("unix", c.socketPath, c.timeout)
	if err != nil {
		return err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(c.timeout))

	id := c.id.Add(1)
	req := Request{
		JSONRPC: JSONRPCVersion,
		Method:  method,
		ID:      json.RawMessage(fmt.Sprintf("%d", id)),
	}
	if params != nil {
		b, err := json.Marshal(params)
		if err != nil {
			return err
		}
		req.Params = b
	}
	enc := json.NewEncoder(conn)
	if err := enc.Encode(req); err != nil {
		return err
	}
	dec := json.NewDecoder(bufio.NewReader(conn))
	var resp Response
	if err := dec.Decode(&resp); err != nil {
		return err
	}
	if resp.Error != nil {
		return &RemoteError{Code: resp.Error.Code, Message: resp.Error.Message}
	}
	if out != nil && len(resp.Result) > 0 {
		if err := json.Unmarshal(resp.Result, out); err != nil {
			return err
		}
	}
	return nil
}

// RemoteError carries a JSON-RPC error from the daemon.
type RemoteError struct {
	Code    int
	Message string
}

func (e *RemoteError) Error() string {
	return fmt.Sprintf("daemon error %d: %s", e.Code, e.Message)
}

// IsSchemaDrift returns true if the error is the "restart pending" signal.
func IsSchemaDrift(err error) bool {
	var re *RemoteError
	return errors.As(err, &re) && re.Code == SchemaMismatch
}

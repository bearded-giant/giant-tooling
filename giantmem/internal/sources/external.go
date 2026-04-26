package sources

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
)

// externalSource shells out to an arbitrary command and parses its JSONL stdout.
// Field mapping uses simple dotted-path extraction over the parsed JSON.
type externalSource struct {
	cfg SourceConfig
}

func newExternalSource(cfg SourceConfig) Source {
	return &externalSource{cfg: cfg}
}

func (e *externalSource) Name() string  { return e.cfg.Name }
func (e *externalSource) Kind() string  { return "external" }
func (e *externalSource) Enabled() bool { return e.cfg.Enabled }

func (e *externalSource) Emit(ctx context.Context, opts EmitOptions) (<-chan Doc, <-chan error) {
	docCh := make(chan Doc, 16)
	errCh := make(chan error, 4)

	go func() {
		defer close(docCh)
		defer close(errCh)

		if e.cfg.IngestCmd == "" {
			errCh <- fmt.Errorf("source %s: missing ingest_cmd", e.cfg.Name)
			return
		}
		// shell out via /bin/sh -c so users can pipe / use env vars
		cmd := exec.CommandContext(ctx, "/bin/sh", "-c", e.cfg.IngestCmd)
		stdout, err := cmd.StdoutPipe()
		if err != nil {
			errCh <- fmt.Errorf("source %s: stdout pipe: %w", e.cfg.Name, err)
			return
		}
		if err := cmd.Start(); err != nil {
			errCh <- fmt.Errorf("source %s: start: %w", e.cfg.Name, err)
			return
		}
		scanner := bufio.NewScanner(stdout)
		scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == "" {
				continue
			}
			doc, perr := e.parseLine(line)
			if perr != nil {
				errCh <- perr
				continue
			}
			if doc.SourceType == "" {
				doc.SourceType = e.cfg.Name
			}
			select {
			case docCh <- doc:
			case <-ctx.Done():
				return
			}
		}
		if err := cmd.Wait(); err != nil {
			errCh <- fmt.Errorf("source %s: exit: %w", e.cfg.Name, err)
		}
	}()

	return docCh, errCh
}

func (e *externalSource) parseLine(line string) (Doc, error) {
	var raw map[string]any
	if err := json.Unmarshal([]byte(line), &raw); err != nil {
		return Doc{}, fmt.Errorf("source %s: bad json: %w", e.cfg.Name, err)
	}
	// if no mapping configured, expect canonical Doc shape
	if len(e.cfg.Mapping) == 0 {
		var d Doc
		if err := json.Unmarshal([]byte(line), &d); err != nil {
			return Doc{}, err
		}
		return d, nil
	}
	get := func(field string) string {
		path, ok := e.cfg.Mapping[field]
		if !ok {
			return ""
		}
		return getPath(raw, path)
	}
	d := Doc{
		Filepath:   get("filepath"),
		Project:    get("project"),
		SourceType: get("source_type"),
		Timestamp:  get("timestamp"),
		DirType:    get("dir_type"),
		SessionID:  get("session_id"),
		Topic:      get("topic"),
		Cwd:        get("cwd"),
		Content:    get("content"),
	}
	return d, nil
}

// getPath walks a dotted path like ".team.key" or "team.key" through a parsed
// JSON object. Returns "" if any step misses.
func getPath(data any, path string) string {
	path = strings.TrimPrefix(path, ".")
	if path == "" {
		if s, ok := data.(string); ok {
			return s
		}
		return ""
	}
	parts := strings.Split(path, ".")
	cur := data
	for _, p := range parts {
		m, ok := cur.(map[string]any)
		if !ok {
			return ""
		}
		cur, ok = m[p]
		if !ok {
			return ""
		}
	}
	switch v := cur.(type) {
	case string:
		return v
	case float64:
		return fmt.Sprintf("%g", v)
	case bool:
		return fmt.Sprintf("%t", v)
	default:
		b, _ := json.Marshal(v)
		return string(b)
	}
}

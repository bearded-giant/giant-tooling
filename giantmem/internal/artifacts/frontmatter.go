package artifacts

import (
	"bytes"
	"strings"
)

// ParseFrontmatter splits YAML frontmatter from a markdown body.
//
// Recognizes the convention:
//
//	---
//	key: value
//	key: value
//	---
//	<body>
//
// Returns the map of keys, the body, and ok=true when a frontmatter block was
// present. When ok=false, body equals the original text and the map is nil.
//
// This is a deliberately small parser — no nested structures, no quoted
// values, no lists. Matches the Python backfill script's writer one-to-one so
// stamped files round-trip cleanly.
func ParseFrontmatter(text string) (map[string]string, string, bool) {
	if !strings.HasPrefix(text, "---\n") {
		return nil, text, false
	}
	end := strings.Index(text[4:], "\n---\n")
	if end < 0 {
		return nil, text, false
	}
	block := text[4 : 4+end]
	body := text[4+end+5:]
	fm := map[string]string{}
	for _, line := range strings.Split(block, "\n") {
		line = strings.TrimRight(line, " \t\r")
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if i := strings.IndexByte(line, ':'); i > 0 {
			k := strings.TrimSpace(line[:i])
			v := strings.TrimSpace(line[i+1:])
			fm[k] = v
		}
	}
	return fm, body, true
}

// ParseFrontmatterBytes is the []byte twin for stream callers.
func ParseFrontmatterBytes(raw []byte) (map[string]string, []byte, bool) {
	if !bytes.HasPrefix(raw, []byte("---\n")) {
		return nil, raw, false
	}
	end := bytes.Index(raw[4:], []byte("\n---\n"))
	if end < 0 {
		return nil, raw, false
	}
	block := raw[4 : 4+end]
	body := raw[4+end+5:]
	fm := map[string]string{}
	for _, line := range strings.Split(string(block), "\n") {
		line = strings.TrimRight(line, " \t\r")
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if i := strings.IndexByte(line, ':'); i > 0 {
			k := strings.TrimSpace(line[:i])
			v := strings.TrimSpace(line[i+1:])
			fm[k] = v
		}
	}
	return fm, body, true
}

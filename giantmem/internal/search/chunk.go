package search

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

const (
	ChunkSize    = 3200
	ChunkOverlap = 320
	// ponytail: 48 windows (~140 KB) covers every authored doc; past that it is a dump
	MaxChunks = 48
	// stored per chunk so recall can show the passage without re-reading the file
	SnippetLen = 400
)

// Chunk is one embedding window over an artifact body. Offsets are bytes.
type Chunk struct {
	Ord   int
	Start int
	End   int
	Text  string
}

// ChunkBody splits body into overlapping windows of at most ChunkSize bytes,
// cutting at a markdown heading, then paragraph, then line, then space when
// one falls inside the last ChunkOverlap bytes of the window.
func ChunkBody(body string) []Chunk {
	return chunkBody(body, ChunkSize, ChunkOverlap, MaxChunks)
}

// ChunkBodyFor applies the per-type policy. Session history is excluded from
// recall, and the catch-all `file` type (filebox dumps, swarm output, sidecars)
// is 80% of all chunks by volume, so both keep only the head window.
// ponytail: chunk `file` too if recall starts missing filebox content
func ChunkBodyFor(artifactType, body string) []Chunk {
	chunks := ChunkBody(body)
	if (artifactType == "history" || artifactType == "file") && len(chunks) > 1 {
		return chunks[:1]
	}
	return chunks
}

func chunkBody(body string, size, overlap, maxChunks int) []Chunk {
	if len(body) <= size {
		return []Chunk{{Ord: 0, Start: 0, End: len(body), Text: body}}
	}
	var out []Chunk
	start := 0
	for start < len(body) && len(out) < maxChunks {
		end := start + size
		if end >= len(body) {
			end = len(body)
		} else {
			end = cutPoint(body, start, end, overlap)
		}
		out = append(out, Chunk{Ord: len(out), Start: start, End: end, Text: body[start:end]})
		if end >= len(body) {
			break
		}
		start = nextStart(body, start, end, overlap)
	}
	return out
}

// cutPoint returns the best boundary in (end-overlap, end]: after a newline
// that precedes a heading, after a blank line, after a newline, after a space.
func cutPoint(body string, start, end, overlap int) int {
	lo := end - overlap
	if lo < start+1 {
		lo = start + 1
	}
	window := body[lo:end]
	if i := strings.LastIndex(window, "\n#"); i >= 0 {
		return lo + i + 1
	}
	for _, sep := range []string{"\n\n", "\n", " "} {
		if i := strings.LastIndex(window, sep); i >= 0 {
			return lo + i + len(sep)
		}
	}
	for !utf8.RuneStart(body[end]) {
		end--
	}
	return end
}

// nextStart steps back by overlap, then to a whitespace boundary so no chunk
// begins mid-word or mid-rune. Always advances past start.
func nextStart(body string, start, end, overlap int) int {
	next := end - overlap
	if next <= start {
		next = end
	}
	if i := strings.LastIndexAny(body[start:next], " \n"); i >= 0 && start+i+1 > start {
		next = start + i + 1
	}
	for next < len(body) && !utf8.RuneStart(body[next]) {
		next++
	}
	if next <= start {
		return end
	}
	return next
}

var htmlCommentRE = regexp.MustCompile(`(?s)<!--.*?-->`)

// Snippet drops HTML comments, collapses whitespace, and truncates to
// SnippetLen for storage.
func Snippet(text string) string {
	s := strings.Join(strings.Fields(htmlCommentRE.ReplaceAllString(text, " ")), " ")
	if len(s) <= SnippetLen {
		return s
	}
	cut := SnippetLen
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
}

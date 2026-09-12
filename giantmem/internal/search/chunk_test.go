package search

import (
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestChunkBody_SmallBodyIsOneChunk(t *testing.T) {
	c := ChunkBody("short body")
	if len(c) != 1 || c[0].Start != 0 || c[0].End != len("short body") || c[0].Text != "short body" {
		t.Fatalf("got %+v", c)
	}
}

func TestChunkBody_CoversBodyWithOverlap(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 400; i++ {
		fmt.Fprintf(&b, "paragraph %d has some words in it and then ends here.\n\n", i)
	}
	body := b.String()
	chunks := chunkBody(body, 1000, 100, 1000)
	if len(chunks) < 10 {
		t.Fatalf("expected many chunks, got %d", len(chunks))
	}
	if chunks[len(chunks)-1].End != len(body) {
		t.Fatal("last chunk must reach end of body")
	}
	for i, c := range chunks {
		if c.Ord != i {
			t.Fatalf("ord %d != %d", c.Ord, i)
		}
		if len(c.Text) > 1000 || c.Text != body[c.Start:c.End] {
			t.Fatalf("chunk %d bad window len=%d", i, len(c.Text))
		}
		if i > 0 {
			prev := chunks[i-1]
			if c.Start >= prev.End || c.Start <= prev.Start {
				t.Fatalf("chunk %d start %d not inside previous window (%d,%d)", i, c.Start, prev.Start, prev.End)
			}
			if prevByte := body[c.Start-1]; prevByte != ' ' && prevByte != '\n' {
				t.Fatalf("chunk %d should start on a word boundary, got %q", i, c.Text[:20])
			}
		}
	}
}

func TestChunkBody_PrefersHeadingCut(t *testing.T) {
	// a heading sits inside the overlap tail of the first window
	body := strings.Repeat("word ", 170) + "\n# Section two\n" + strings.Repeat("more ", 300)
	chunks := chunkBody(body, 1000, 200, 10)
	if len(chunks) < 2 {
		t.Fatalf("expected split, got %d", len(chunks))
	}
	if !strings.HasSuffix(chunks[0].Text, "\n") || !strings.HasPrefix(body[chunks[0].End:], "# Section two") {
		t.Fatalf("first chunk should end right before the heading, ends %q", chunks[0].Text[len(chunks[0].Text)-20:])
	}
}

func TestChunkBody_CapsChunkCount(t *testing.T) {
	body := strings.Repeat("x ", 200000)
	chunks := chunkBody(body, 1000, 100, 5)
	if len(chunks) != 5 {
		t.Fatalf("cap not applied: %d", len(chunks))
	}
}

func TestChunkBody_UTF8SafeWithoutWhitespace(t *testing.T) {
	body := strings.Repeat("日本語テキスト", 500) // no spaces, 3-byte runes
	chunks := chunkBody(body, 1000, 100, 100)
	if len(chunks) < 5 {
		t.Fatalf("expected several chunks, got %d", len(chunks))
	}
	for i, c := range chunks {
		if !utf8.ValidString(c.Text) {
			t.Fatalf("chunk %d splits a rune", i)
		}
	}
	if chunks[len(chunks)-1].End != len(body) {
		t.Fatal("must cover body")
	}
}

func TestChunkBodyFor_HistoryAndFileKeepHeadOnly(t *testing.T) {
	body := strings.Repeat("line of session log\n", 1000)
	for _, typ := range []string{"history", "file"} {
		if n := len(ChunkBodyFor(typ, body)); n != 1 {
			t.Fatalf("%s should keep one chunk, got %d", typ, n)
		}
	}
	if n := len(ChunkBodyFor("research", body)); n < 2 {
		t.Fatalf("research should chunk, got %d", n)
	}
}

func TestSnippet(t *testing.T) {
	s := Snippet("<!-- caveman:compressed -->\n  a   b\n\nc  ")
	if s != "a b c" {
		t.Fatalf("got %q", s)
	}
	long := Snippet(strings.Repeat("é", 1000))
	if len(long) > SnippetLen || !utf8.ValidString(long) {
		t.Fatalf("snippet truncation broke utf8 or length: %d", len(long))
	}
}

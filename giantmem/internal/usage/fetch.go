package usage

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"
)

// DocsURL is the markdown form of Anthropic's pricing page.
const DocsURL = "https://platform.claude.com/docs/en/about-claude/pricing.md"

var dollar = regexp.MustCompile(`\$([0-9]+(?:\.[0-9]+)?)`)

var headerFields = map[string]string{
	"base input": "input",
	"5m cache":   "cache_5m",
	"1h cache":   "cache_1h",
	"cache hits": "cache_read",
	"output":     "output",
}

// Fetch downloads the pricing page and parses its model table.
func Fetch(url string) (map[string]Price, error) {
	resp, err := http.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: %s", url, resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	return Parse(string(body))
}

// Parse reads the "## Model pricing" table; a model's first row wins, so Haiku's long-prompt tier is dropped.
func Parse(md string) (map[string]Price, error) {
	out := map[string]Price{}
	cols := map[string]int{}
	in := false
	for _, line := range strings.Split(md, "\n") {
		if strings.HasPrefix(line, "## ") {
			if in {
				break
			}
			in = strings.TrimSpace(line) == "## Model pricing"
			continue
		}
		if !in || !strings.HasPrefix(line, "|") {
			continue
		}
		cells := strings.Split(strings.Trim(strings.TrimSpace(line), "|"), "|")
		for i := range cells {
			cells[i] = strings.TrimSpace(cells[i])
		}
		if len(cols) == 0 {
			for i, c := range cells {
				for key, field := range headerFields {
					if strings.Contains(strings.ToLower(c), key) {
						cols[field] = i
					}
				}
			}
			if len(cols) != len(headerFields) {
				return nil, fmt.Errorf("pricing table header changed: %q", line)
			}
			continue
		}
		id := modelID(cells[0])
		if id == "" || len(cells) <= cols["output"] {
			continue
		}
		if _, dup := out[id]; dup {
			continue
		}
		var p Price
		ok := true
		for field, i := range cols {
			m := dollar.FindStringSubmatch(cells[i])
			if m == nil {
				ok = false
				break
			}
			v, _ := strconv.ParseFloat(m[1], 64)
			switch field {
			case "input":
				p.Input = v
			case "cache_5m":
				p.Cache5m = v
			case "cache_1h":
				p.Cache1h = v
			case "cache_read":
				p.CacheRead = v
			case "output":
				p.Output = v
			}
		}
		if ok {
			out[id] = p
		}
	}
	if len(out) < 5 {
		return nil, fmt.Errorf("parsed %d models from pricing table, want at least 5", len(out))
	}
	return out, nil
}

func modelID(cell string) string {
	name, _, _ := strings.Cut(cell, " (")
	name = strings.TrimSpace(name)
	if !strings.HasPrefix(name, "Claude ") {
		return ""
	}
	return strings.ToLower(strings.NewReplacer(" ", "-", ".", "-").Replace(name))
}

// LoadFile reads a fetched price table; a missing file is an empty table.
func LoadFile(path string) (map[string]Price, error) {
	p := map[string]Price{}
	if _, err := toml.DecodeFile(path, &p); err != nil {
		if os.IsNotExist(err) {
			return p, nil
		}
		return p, fmt.Errorf("%s: %w", path, err)
	}
	return p, nil
}

func SaveFile(path string, prices map[string]Price) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	if err := toml.NewEncoder(f).Encode(prices); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

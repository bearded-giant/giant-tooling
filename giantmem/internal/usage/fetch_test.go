package usage

import (
	"path/filepath"
	"reflect"
	"testing"
)

const pricingMD = `# Pricing

## Model pricing

The following table shows pricing for all Claude models:

| Model | Base input tokens | 5m cache writes | 1h cache writes | Cache hits and refreshes | Output tokens |
| :---- | :---------------- | :-------------- | :-------------- | :----------------------- | :------------ |
| Claude Fable 5.1 | $10 / MTok | $12.50 / MTok | $20 / MTok | $0.25 / MTok<sup>1</sup> | $50 / MTok |
| Claude Mythos 5.1 ([limited availability](https://example.com)) | $10 / MTok | $12.50 / MTok | $20 / MTok | $0.25 / MTok<sup>1</sup> | $50 / MTok |
| Claude Opus 5.5 | $4 / MTok | $5 / MTok | $8 / MTok | $0.20 / MTok<sup>2</sup> | $20 / MTok |
| Claude Opus 4.1 ([retired, except on Bedrock](https://example.com)) | $15 / MTok | $18.75 / MTok | $30 / MTok | $1.50 / MTok | $75 / MTok |
| Claude Haiku 5.5 (for prompts up to 100,000 tokens) | $0.10 / MTok | $0.125 / MTok | $0.20 / MTok | $0.01 / MTok | $0.50 / MTok |
| Claude Haiku 5.5 (for prompts over 100,000 tokens) | $0.50 / MTok | $0.625 / MTok | $1 / MTok | $0.05 / MTok | $2.50 / MTok |
| Claude Haiku 4.5 | $1 / MTok | $1.25 / MTok | $2 / MTok | $0.10 / MTok | $5 / MTok |

## Cloud platform pricing

| Model | Base input tokens | 5m cache writes | 1h cache writes | Cache hits and refreshes | Output tokens |
| :---- | :---------------- | :-------------- | :-------------- | :----------------------- | :------------ |
| Claude Opus 5.5 | $99 / MTok | $99 / MTok | $99 / MTok | $99 / MTok | $99 / MTok |
`

func TestParse(t *testing.T) {
	got, err := Parse(pricingMD)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]Price{
		"claude-fable-5-1":  {Input: 10, Cache5m: 12.5, Cache1h: 20, CacheRead: 0.25, Output: 50},
		"claude-mythos-5-1": {Input: 10, Cache5m: 12.5, Cache1h: 20, CacheRead: 0.25, Output: 50},
		"claude-opus-5-5":   {Input: 4, Cache5m: 5, Cache1h: 8, CacheRead: 0.2, Output: 20},
		"claude-opus-4-1":   {Input: 15, Cache5m: 18.75, Cache1h: 30, CacheRead: 1.5, Output: 75},
		"claude-haiku-5-5":  {Input: 0.1, Cache5m: 0.125, Cache1h: 0.2, CacheRead: 0.01, Output: 0.5},
		"claude-haiku-4-5":  {Input: 1, Cache5m: 1.25, Cache1h: 2, CacheRead: 0.1, Output: 5},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Parse = %+v\nwant %+v", got, want)
	}
}

func TestParse_RejectsChangedHeader(t *testing.T) {
	md := "## Model pricing\n| Model | Input | Output |\n| :-- | :-- | :-- |\n| Claude Opus 5.5 | $4 / MTok | $20 / MTok |\n"
	if _, err := Parse(md); err == nil {
		t.Fatal("want header error")
	}
}

func TestSaveLoadFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prices.toml")
	if got, err := LoadFile(path); err != nil || len(got) != 0 {
		t.Fatalf("missing file: %v %v", got, err)
	}
	want := map[string]Price{"claude-opus-5-5": {Input: 4, Cache5m: 5, Cache1h: 8, CacheRead: 0.2, Output: 20}}
	if err := SaveFile(path, want); err != nil {
		t.Fatal(err)
	}
	got, err := LoadFile(path)
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("LoadFile = %+v %v, want %+v", got, err, want)
	}
}

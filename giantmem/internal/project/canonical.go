package project

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// Canonicalize collapses session-style paths and `-wt` siblings into one
// canonical project name.
//
// rules:
//  1. if name matches an explicit override in ~/.config/giantmem/canonical.json,
//     use the mapping
//  2. session-style "dev/<lang>/foo" -> "foo" (or "foo-wt" if archive sibling exists)
//  3. plain "foo" with sibling "foo-wt" in archiveBase -> "foo-wt"
//  4. otherwise: returned unchanged
func Canonicalize(name, archiveBase string) string {
	if mapped, ok := canonicalOverride(name); ok {
		return mapped
	}
	base := name
	// strip dev/<seg>/ or dev/<seg>/<seg>/ prefix
	if strings.HasPrefix(base, "dev/") {
		parts := strings.SplitN(base, "/", 4)
		if len(parts) >= 3 {
			// dev/<lang>/<rest>
			base = strings.Join(parts[2:], "/")
		}
	}
	// take the leaf
	if idx := strings.LastIndex(base, "/"); idx >= 0 {
		base = base[idx+1:]
	}
	// "-wt" sibling promotion: if archiveBase/<base>-wt exists OR base already has -wt
	if archiveBase != "" {
		if strings.HasSuffix(base, "-wt") {
			return base
		}
		cand := base + "-wt"
		if dirExists(filepath.Join(archiveBase, cand)) {
			return cand
		}
	}
	return base
}

var canonicalCache map[string]string

func canonicalOverride(name string) (string, bool) {
	if canonicalCache == nil {
		canonicalCache = loadCanonicalOverrides()
	}
	v, ok := canonicalCache[name]
	return v, ok
}

func loadCanonicalOverrides() map[string]string {
	home, _ := os.UserHomeDir()
	path := filepath.Join(home, ".config", "giantmem", "canonical.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		return map[string]string{}
	}
	var m map[string]string
	if err := json.Unmarshal(raw, &m); err != nil {
		return map[string]string{}
	}
	return m
}

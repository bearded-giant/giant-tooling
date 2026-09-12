package artifacts

import (
	"fmt"
	"os"
	"strings"
)

// SetLifecycle rewrites only the `lifecycle:` line of an artifact's YAML
// frontmatter and preserves the file's mtime (a lifecycle flip is not an
// edit). Returns false, nil when the file has no frontmatter block or no
// lifecycle line; adding one is the backfill scripts' job.
func SetLifecycle(path, lifecycle string) (bool, error) {
	if !validLifecycle(lifecycle) {
		return false, fmt.Errorf("invalid lifecycle %q", lifecycle)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	text := string(raw)
	if !strings.HasPrefix(text, "---\n") {
		return false, nil
	}
	end := strings.Index(text[4:], "\n---\n")
	if end == -1 {
		return false, nil
	}
	end += 4
	block := text[4:end]
	lines := strings.Split(block, "\n")
	found := false
	for i, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "lifecycle:") {
			if strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "lifecycle:")) == lifecycle {
				return false, nil
			}
			lines[i] = "lifecycle: " + lifecycle
			found = true
			break
		}
	}
	if !found {
		return false, nil
	}
	st, err := os.Stat(path)
	if err != nil {
		return false, err
	}
	out := "---\n" + strings.Join(lines, "\n") + text[end:]
	if err := os.WriteFile(path, []byte(out), st.Mode().Perm()); err != nil {
		return false, err
	}
	return true, os.Chtimes(path, st.ModTime(), st.ModTime())
}

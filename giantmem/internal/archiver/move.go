package archiver

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/bearded-giant/giant-tooling/giantmem/internal/backfill"
	"github.com/bearded-giant/giant-tooling/giantmem/internal/db"
)

// MoveResult reports one feature's move outcome.
type MoveResult struct {
	Name   string
	SrcWS  string // source .giantmem dir
	DestWS string // destination .giantmem dir
	Files  int    // files verified in live.db before move
	Method string // "rename" or "copy"
}

// MoveFeature relocates features/<name> from one workspace to another:
// verifies the files are captured in live.db, moves the dir, transfers the
// features.json entry, and backfills the destination so live_docs rows exist
// under the new paths. Source rows are kept as history (live.db never prunes).
func MoveFeature(srcDir, destDir, name string, dryRun bool) (MoveResult, error) {
	res := MoveResult{Name: name}
	srcWS, err := resolveWorkspace(srcDir)
	if err != nil {
		return res, fmt.Errorf("source: %w", err)
	}
	destWS, err := resolveWorkspace(destDir)
	if err != nil {
		return res, fmt.Errorf("destination: %w", err)
	}
	res.SrcWS, res.DestWS = srcWS, destWS
	if srcWS == destWS {
		return res, fmt.Errorf("source and destination are the same workspace: %s", srcWS)
	}

	srcFeat := filepath.Join(srcWS, "features", name)
	if !dirExists(srcFeat) {
		return res, fmt.Errorf("feature dir not found: %s", srcFeat)
	}
	destFeat := filepath.Join(destWS, "features", name)
	if dirExists(destFeat) {
		return res, fmt.Errorf("feature already exists at destination: %s", destFeat)
	}

	srcJSON := filepath.Join(srcWS, "features", "features.json")
	entries, err := readFeaturesJSON(srcJSON)
	if err != nil {
		return res, fmt.Errorf("read source features.json: %w", err)
	}
	meta, ok := entries[name]
	if !ok {
		return res, fmt.Errorf("feature %q not in %s", name, srcJSON)
	}

	if dryRun {
		res.Method = "would-move"
		return res, nil
	}

	captured, missing, verr := captureAndVerify(srcFeat)
	if verr != nil {
		return res, fmt.Errorf("verify live.db: %w", verr)
	}
	if len(missing) > 0 {
		return res, fmt.Errorf("refusing to move: %d file(s) not captured in live.db (e.g. %s)",
			len(missing), filepath.Base(missing[0]))
	}
	res.Files = captured

	if err := os.MkdirAll(filepath.Join(destWS, "features"), 0o755); err != nil {
		return res, err
	}
	res.Method = "rename"
	if err := os.Rename(srcFeat, destFeat); err != nil {
		// cross-device rename fails; source content is verified in live.db above
		res.Method = "copy"
		if cerr := copyTree(srcFeat, destFeat); cerr != nil {
			os.RemoveAll(destFeat)
			return res, fmt.Errorf("copy: %w", cerr)
		}
		if rerr := os.RemoveAll(srcFeat); rerr != nil {
			return res, fmt.Errorf("remove source after copy: %w", rerr)
		}
	}

	destJSON := filepath.Join(destWS, "features", "features.json")
	destEntries, err := readFeaturesJSON(destJSON)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			return res, fmt.Errorf("read dest features.json: %w", err)
		}
		destEntries = map[string]map[string]any{}
	}
	destEntries[name] = meta
	if err := writeFeaturesJSON(destJSON, destEntries); err != nil {
		return res, fmt.Errorf("write dest features.json: %w", err)
	}

	delete(entries, name)
	if err := writeFeaturesJSON(srcJSON, entries); err != nil {
		return res, fmt.Errorf("write source features.json: %w", err)
	}
	dropIndexRow(filepath.Join(srcWS, "features", "_index.md"), name)

	base := archiveBaseDir()
	if d, oerr := db.Open(filepath.Join(base, "live.db")); oerr == nil {
		defer d.Close()
		if _, berr := backfill.RunOnWorkspace(d, base, destWS); berr != nil {
			fmt.Fprintf(os.Stderr, "warn: backfill destination: %v\n", berr)
		}
	}
	return res, nil
}

func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		info, err := d.Info()
		if err != nil {
			return err
		}
		if d.IsDir() {
			return os.MkdirAll(target, info.Mode().Perm())
		}
		if info.Mode()&fs.ModeSymlink != 0 {
			link, err := os.Readlink(p)
			if err != nil {
				return err
			}
			return os.Symlink(link, target)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(target, b, info.Mode().Perm())
	})
}

// dropIndexRow removes the feature's markdown table row from _index.md
// (best-effort; the file is Claude-maintained and self-heals).
func dropIndexRow(indexPath, name string) {
	b, err := os.ReadFile(indexPath)
	if err != nil {
		return
	}
	needle := "[" + name + "](" + name + "/)"
	lines := strings.Split(string(b), "\n")
	out := lines[:0]
	changed := false
	for _, l := range lines {
		if strings.Contains(l, needle) {
			changed = true
			continue
		}
		out = append(out, l)
	}
	if changed {
		_ = os.WriteFile(indexPath, []byte(strings.Join(out, "\n")), 0o644)
	}
}

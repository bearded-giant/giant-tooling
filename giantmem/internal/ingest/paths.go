package ingest

import (
	"path/filepath"
	"regexp"
	"strings"
)

var TimestampRe = regexp.MustCompile(`^\d{8}_\d{6}$`)

var validDirTypes = map[string]bool{
	"plans":    true,
	"context":  true,
	"research": true,
	"reviews":  true,
	"filebox":  true,
	"history":  true,
	"prompts":  true,
	"features": true,
	"domains":  true,
}

// ParsedPath captures project/timestamp/dir_type from an archive file path.
type ParsedPath struct {
	Project   string
	Timestamp string
	DirType   string
}

// ParseArchivePath extracts metadata from an absolute path inside archiveBase.
// Layout: {archiveBase}/{project}/{timestamp}/[dir_type]/...
func ParseArchivePath(absPath, archiveBase string) (*ParsedPath, bool) {
	rel, err := filepath.Rel(archiveBase, absPath)
	if err != nil {
		return nil, false
	}
	parts := strings.Split(rel, string(filepath.Separator))
	if len(parts) < 3 {
		return nil, false
	}
	if !TimestampRe.MatchString(parts[1]) {
		return nil, false
	}
	p := &ParsedPath{
		Project:   parts[0],
		Timestamp: parts[1],
	}
	if len(parts) > 2 {
		if validDirTypes[parts[2]] {
			p.DirType = parts[2]
		} else {
			p.DirType = "root"
		}
	}
	return p, true
}

package artifacts

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Entity is a typed file-level promotion of a `key_files[]` entry from a
// `domains/*.json` artifact. It carries the path, the domain it belongs to,
// a short purpose blurb, and back-references to any artifact that mentions
// the file path in its body.
type Entity struct {
	Path       string   `json:"path"`
	Domain     string   `json:"domain"`
	Repo       string   `json:"repo"`
	Purpose    string   `json:"purpose,omitempty"`
	Exports    []string `json:"exports,omitempty"`
	References []string `json:"references,omitempty"` // artifact IDs
}

// domainFile is the minimal JSON shape we read from domains/*.json. We
// intentionally accept extras; only key_files matter.
type domainFile struct {
	Domain   string `json:"domain"`
	KeyFiles []struct {
		Path    string   `json:"path"`
		Purpose string   `json:"purpose"`
		Exports []string `json:"exports"`
	} `json:"key_files"`
}

// LoadEntities walks every artifact of type=domain across the supplied
// candidate set and returns a flat slice of file-level entities. Each
// entity's References field is populated by scanning candidate bodies for
// substring matches of the entity's path.
//
// This is intentionally O(domains * paths * candidates); the corpus is
// small in practice. Callers can cap by passing a pre-filtered candidate
// slice (e.g. one repo only).
func LoadEntities(candidates []Artifact) ([]Entity, error) {
	domains := map[string]Artifact{} // domain id -> artifact
	for _, a := range candidates {
		if a.Type == "domain" {
			domains[a.ID] = a
		}
	}
	if len(domains) == 0 {
		return nil, nil
	}

	out := []Entity{}
	for _, a := range domains {
		abs := artifactAbsPath(a)
		if abs == "" {
			continue
		}
		raw, err := os.ReadFile(abs)
		if err != nil {
			continue
		}
		var df domainFile
		if err := json.Unmarshal(raw, &df); err != nil {
			continue
		}
		domain := df.Domain
		if domain == "" {
			domain = a.Name
		}
		for _, kf := range df.KeyFiles {
			if strings.TrimSpace(kf.Path) == "" {
				continue
			}
			out = append(out, Entity{
				Path:    kf.Path,
				Domain:  domain,
				Repo:    a.Repo,
				Purpose: kf.Purpose,
				Exports: kf.Exports,
			})
		}
	}

	// Resolve back-references — substring match the entity path against
	// every non-domain artifact body. Cheap because bodies are small.
	bodies := map[string]string{}
	for _, a := range candidates {
		if a.Type == "domain" {
			continue
		}
		body, err := readArtifactBody(a)
		if err != nil {
			continue
		}
		bodies[a.ID] = body
	}
	for i := range out {
		needle := out[i].Path
		var refs []string
		for id, body := range bodies {
			if strings.Contains(body, needle) {
				refs = append(refs, id)
			}
		}
		sort.Strings(refs)
		out[i].References = refs
	}

	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Domain != out[j].Domain {
			return out[i].Domain < out[j].Domain
		}
		return out[i].Path < out[j].Path
	})
	return out, nil
}

// FindEntity returns the first entity whose path or its basename equals or
// substring-matches `needle`. Returns (Entity{}, false) when not found.
func FindEntity(entities []Entity, needle string) (Entity, bool) {
	needle = strings.TrimSpace(needle)
	if needle == "" {
		return Entity{}, false
	}
	lower := strings.ToLower(needle)
	for _, e := range entities {
		if strings.EqualFold(e.Path, needle) {
			return e, true
		}
		if strings.EqualFold(filepath.Base(e.Path), needle) {
			return e, true
		}
		if strings.Contains(strings.ToLower(e.Path), lower) {
			return e, true
		}
	}
	return Entity{}, false
}

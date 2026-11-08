package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/bearded-giant/giant-tooling/giantmem/internal/artifacts"
	"github.com/bearded-giant/giant-tooling/giantmem/internal/backfill"
	"github.com/bearded-giant/giant-tooling/giantmem/internal/search"
	"github.com/bearded-giant/giant-tooling/giantmem/internal/workspaces"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

type WsWorkspace struct {
	Dir           string            `json:"dir"`
	Label         string            `json:"label"`
	Dead          bool              `json:"dead"`
	Files         int               `json:"files"`
	Mtime         int64             `json:"mtime"`
	ActiveFeature string            `json:"activeFeature"`
	Features      map[string]string `json:"features"`
}

type WsProject struct {
	Name       string        `json:"name"`
	Root       string        `json:"root"`
	Kind       string        `json:"kind"`
	Flat       bool          `json:"flat"`
	Mtime      int64         `json:"mtime"`
	Workspaces []WsWorkspace `json:"workspaces"`
}

type WsNode struct {
	Ws        string `json:"ws"`
	Rel       string `json:"rel"`
	Dir       bool   `json:"dir"`
	Size      int64  `json:"size"`
	Mtime     int64  `json:"mtime"`
	Indexed   bool   `json:"indexed"`
	SessionID string `json:"sessionId"`
}

type UploadResult struct {
	Written   []string `json:"written"`
	Conflicts []string `json:"conflicts"`
	Skipped   []string `json:"skipped"`
}

var baseBranchRank = map[string]int{"main": 1, "master": 1, "develop": 2, "stage": 3}

var guiDiscover struct {
	sync.Mutex
	key string
	at  time.Time
	out []string
}

// the $HOME walk takes about a second and the tab reloads on every live.db write
func guiWorkspaceDirs() []string {
	roots := workspaces.Roots()
	key := strings.Join(roots, ":")
	guiDiscover.Lock()
	defer guiDiscover.Unlock()
	if guiDiscover.key != key || time.Since(guiDiscover.at) > 30*time.Second {
		guiDiscover.key, guiDiscover.at, guiDiscover.out = key, time.Now(), workspaces.Find(roots, 0)
	}
	return guiDiscover.out
}

// the tab sees all of $HOME, but live.db (and so the cli) only holds the cli's own
// discovery scope; workspaces outside it are never written there
func indexScope() map[string]bool {
	m := map[string]bool{}
	for _, g := range artifacts.DiscoverWorkspaces(0) {
		m[filepath.Dir(g)] = true
	}
	return m
}

func (a *App) WorkspaceProjects() ([]WsProject, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var dirs []string
	for _, d := range guiWorkspaceDirs() {
		seen[d] = true
		dirs = append(dirs, d)
	}
	type stat struct {
		n int
		m int64
	}
	indexed := map[string]stat{}
	if a.live != nil {
		rows, err := a.live.Query(`
            SELECT substr(path, 1, instr(path, '/.giantmem/') - 1), COUNT(*), MAX(mtime)
              FROM live_docs
             WHERE instr(path, '/.giantmem/') > 0
             GROUP BY 1`)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var ws string
			var s stat
			if rows.Scan(&ws, &s.n, &s.m) != nil || !strings.HasPrefix(ws+"/", home+"/") || hiddenBelow(home, ws) {
				continue
			}
			indexed[ws] = s
			if !seen[ws] {
				seen[ws] = true
				dirs = append(dirs, ws)
			}
		}
		rows.Close()
	}

	var out []WsProject
	for _, p := range workspaces.Group(home, dirs) {
		wp := WsProject{Name: p.Name, Root: p.Root, Kind: string(p.Kind)}
		for _, m := range p.Members {
			w := WsWorkspace{Dir: m.Dir, Label: m.Label}
			g := filepath.Join(m.Dir, ".giantmem")
			if isPlainDir(g) {
				w.Files, w.Mtime = diskStats(g)
				w.Features, w.ActiveFeature = featureStatuses(g)
			} else {
				w.Dead = true
				w.Files, w.Mtime = indexed[m.Dir].n, indexed[m.Dir].m
			}
			if w.Mtime > wp.Mtime {
				wp.Mtime = w.Mtime
			}
			wp.Workspaces = append(wp.Workspaces, w)
		}
		sortWorkspaces(wp.Workspaces)
		wp.Flat = p.Kind == workspaces.Repo && len(wp.Workspaces) == 1 && wp.Workspaces[0].Dir == p.Root
		out = append(out, wp)
	}
	sort.SliceStable(out, func(i, j int) bool {
		oi, oj := out[i].Kind == string(workspaces.Other), out[j].Kind == string(workspaces.Other)
		if oi != oj {
			return oj
		}
		return out[i].Mtime > out[j].Mtime
	})
	return out, nil
}

// the disk walk never enters hidden dirs (.bare, .claude/worktrees); rows indexed from there stay out too
func hiddenBelow(home, ws string) bool {
	for _, seg := range strings.Split(strings.TrimPrefix(ws, home), "/") {
		if strings.HasPrefix(seg, ".") {
			return true
		}
	}
	return false
}

var skipFiles = map[string]bool{".DS_Store": true, ".gitkeep": true}

func sortWorkspaces(ws []WsWorkspace) {
	rank := func(w WsWorkspace) int {
		if w.Label == workspaces.RootLabel {
			return 0
		}
		if r, ok := baseBranchRank[w.Label]; ok {
			return r
		}
		return 9
	}
	sort.SliceStable(ws, func(i, j int) bool {
		if ws[i].Dead != ws[j].Dead {
			return !ws[i].Dead
		}
		if ri, rj := rank(ws[i]), rank(ws[j]); ri != rj {
			return ri < rj
		}
		return ws[i].Mtime > ws[j].Mtime
	})
}

func diskStats(g string) (int, int64) {
	var n int
	var m int64
	_ = filepath.WalkDir(g, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || skipFiles[d.Name()] {
			return nil
		}
		n++
		if info, err := d.Info(); err == nil && info.ModTime().Unix() > m {
			m = info.ModTime().Unix()
		}
		return nil
	})
	return n, m
}

// features.json comes name-keyed at the top, or under "features" as a map or a list
func featureStatuses(g string) (map[string]string, string) {
	b, err := os.ReadFile(filepath.Join(g, "features", "features.json"))
	if err != nil {
		return nil, ""
	}
	var top map[string]json.RawMessage
	if json.Unmarshal(b, &top) != nil {
		return nil, ""
	}
	items := map[string]json.RawMessage{}
	if raw, ok := top["features"]; ok {
		var list []json.RawMessage
		if json.Unmarshal(raw, &items) != nil && json.Unmarshal(raw, &list) == nil {
			for i, it := range list {
				items[fmt.Sprint(i)] = it
			}
		}
	} else {
		items = top
	}
	out := map[string]string{}
	for k, raw := range items {
		var f struct {
			Name   string `json:"name"`
			Status string `json:"status"`
		}
		if json.Unmarshal(raw, &f) != nil || f.Status == "" {
			continue
		}
		if f.Name == "" {
			f.Name = k
		}
		out[f.Name] = f.Status
	}
	var active string
	if raw, ok := top["active"]; ok {
		_ = json.Unmarshal(raw, &active)
	}
	if active == "" {
		names := make([]string, 0, len(out))
		for n, s := range out {
			if s == "in_progress" {
				names = append(names, n)
			}
		}
		sort.Strings(names)
		if len(names) > 0 {
			active = names[0]
		}
	}
	return out, active
}

func (a *App) WorkspaceFiles(dirs []string) ([]WsNode, error) {
	var out []WsNode
	for _, ws := range dirs {
		g := filepath.Join(ws, ".giantmem")
		idx, err := a.indexedUnder(g)
		if err != nil {
			return nil, err
		}
		if !isPlainDir(g) {
			for p, r := range idx {
				out = append(out, WsNode{Ws: ws, Rel: strings.TrimPrefix(p, g+"/"), Size: r.size, Mtime: r.mtime, Indexed: true, SessionID: r.session})
			}
			continue
		}
		_ = filepath.WalkDir(g, func(p string, d fs.DirEntry, err error) error {
			if err != nil || p == g || skipFiles[d.Name()] {
				return nil
			}
			info, err := d.Info()
			if err != nil {
				return nil
			}
			n := WsNode{Ws: ws, Rel: filepath.ToSlash(strings.TrimPrefix(p, g+"/")), Dir: d.IsDir(), Mtime: info.ModTime().Unix()}
			if !n.Dir {
				n.Size = info.Size()
				r, ok := idx[p]
				n.Indexed, n.SessionID = ok, r.session
			}
			out = append(out, n)
			return nil
		})
	}
	return out, nil
}

type indexedRow struct {
	session string
	mtime   int64
	size    int64
}

func (a *App) indexedUnder(g string) (map[string]indexedRow, error) {
	out := map[string]indexedRow{}
	if a.live == nil {
		return out, nil
	}
	// a range on the primary key; a LIKE scan touched every row's content once per workspace
	rows, err := a.live.Query(`
        SELECT path, COALESCE(session_id, ''), mtime, COALESCE(length(content), 0)
          FROM live_docs
         WHERE path >= ? AND path < ?`, g+"/", g+"0")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var p string
		var r indexedRow
		if err := rows.Scan(&p, &r.session, &r.mtime, &r.size); err != nil {
			return nil, err
		}
		out[p] = r
	}
	return out, rows.Err()
}

func (a *App) WorkspaceSearch(query string, dirs []string, limit int) ([]search.Hit, error) {
	if a.live == nil {
		return nil, fmt.Errorf("live db not open")
	}
	if limit <= 0 {
		limit = 200
	}
	scope := indexScope()
	var indexed, onDisk []string
	for _, d := range dirs {
		if !scope[d] && isPlainDir(filepath.Join(d, ".giantmem")) {
			onDisk = append(onDisk, d)
		} else {
			indexed = append(indexed, d)
		}
	}
	var hits []search.Hit
	if q := search.SanitizeFTSQuery(query); q != "" && len(indexed) > 0 {
		prefixes := make([]string, len(indexed))
		for i, d := range indexed {
			prefixes[i] = filepath.Join(d, ".giantmem") + "/"
		}
		h, err := search.QueryLive(a.live, search.Params{Query: q, PathPrefixes: prefixes, Limit: limit, IncludeFull: true})
		if err != nil {
			return nil, err
		}
		hits = h
	}
	terms := strings.Fields(strings.ToLower(strings.ReplaceAll(query, `"`, "")))
	if len(terms) > 0 && len(onDisk) > 0 && len(hits) < limit {
		hits = append(hits, diskSearch(terms, onDisk, limit-len(hits))...)
	}
	return hits, nil
}

func diskSearch(terms, dirs []string, limit int) []search.Hit {
	var out []search.Hit
	for _, ws := range dirs {
		_ = filepath.WalkDir(filepath.Join(ws, ".giantmem"), func(p string, d fs.DirEntry, err error) error {
			if len(out) >= limit {
				return fs.SkipAll
			}
			if err != nil || d.IsDir() || skipFiles[d.Name()] {
				return nil
			}
			info, err := d.Info()
			if err != nil || info.Size() == 0 || info.Size() > 1<<20 {
				return nil
			}
			body, err := os.ReadFile(p)
			if err != nil || bytes.IndexByte(body[:min(len(body), 8000)], 0) >= 0 {
				return nil
			}
			low := strings.ToLower(string(body))
			at := -1
			for _, t := range terms {
				i := strings.Index(low, t)
				if i < 0 {
					return nil
				}
				if at < 0 {
					at = i
				}
			}
			out = append(out, search.Hit{Source: "disk", Filepath: p, Filename: d.Name(), Snippet: snippetAround(string(body), at, len(terms[0]))})
			return nil
		})
	}
	return out
}

// same < > markers as the fts5 snippet() the frontend already renders
func snippetAround(body string, at, n int) string {
	at = min(max(at, 0), len(body))
	end := min(at+n, len(body))
	from, to := max(at-60, 0), min(end+60, len(body))
	s := "..." + body[from:at] + "<" + body[at:end] + ">" + body[end:to] + "..."
	return strings.Join(strings.Fields(s), " ")
}

// edits made outside claude (vim, git, finder) otherwise reach live.db only on daemon start
func (a *App) ReindexWorkspaces(dirs []string) (backfill.Stats, error) {
	var total backfill.Stats
	if a.live == nil {
		return total, fmt.Errorf("live db not open")
	}
	scope := indexScope()
	for _, d := range dirs {
		g := filepath.Join(d, ".giantmem")
		if !scope[d] || !isPlainDir(g) {
			continue
		}
		st, err := backfill.RunOnWorkspace(a.live, archiveBase(), g)
		if err != nil {
			return total, err
		}
		total.Workspaces += st.Workspaces
		total.Scanned += st.Scanned
		total.Upserted += st.Upserted
		total.Skipped += st.Skipped
	}
	return total, nil
}

func (a *App) DownloadFile(path string) (string, error) {
	home, _ := os.UserHomeDir()
	dest, err := runtime.SaveFileDialog(a.ctx, runtime.SaveDialogOptions{
		Title:            "Download",
		DefaultDirectory: filepath.Join(home, "Downloads"),
		DefaultFilename:  filepath.Base(path),
	})
	if err != nil || dest == "" {
		return "", err
	}
	body, err := os.ReadFile(path)
	if err != nil {
		var content string
		if a.live == nil || a.live.QueryRow(`SELECT content FROM live_docs WHERE path = ?`, path).Scan(&content) != nil {
			return "", err
		}
		body = []byte(content)
	}
	return dest, os.WriteFile(dest, body, 0o644)
}

func (a *App) PickUploadFiles() ([]string, error) {
	return runtime.OpenMultipleFilesDialog(a.ctx, runtime.OpenDialogOptions{Title: "Upload into workspace"})
}

func (a *App) UploadFiles(destDir string, srcs []string, onConflict string) (UploadResult, error) {
	var res UploadResult
	g, err := uploadTarget(destDir)
	if err != nil {
		return res, err
	}
	switch onConflict {
	case "", "overwrite", "keep", "skip":
	default:
		return res, fmt.Errorf("unknown conflict mode %q", onConflict)
	}
	for _, src := range srcs {
		st, err := os.Stat(src)
		if err != nil {
			return res, err
		}
		if !st.Mode().IsRegular() {
			return res, fmt.Errorf("not a regular file: %s", src)
		}
		if _, err := os.Lstat(filepath.Join(destDir, filepath.Base(src))); err == nil {
			res.Conflicts = append(res.Conflicts, filepath.Base(src))
		}
	}
	if len(res.Conflicts) > 0 && onConflict == "" {
		return res, nil
	}
	for _, src := range srcs {
		name := filepath.Base(src)
		target := filepath.Join(destDir, name)
		if st, err := os.Lstat(target); err == nil {
			switch {
			case onConflict == "skip" || (onConflict == "overwrite" && st.IsDir()):
				res.Skipped = append(res.Skipped, name)
				continue
			case onConflict == "keep":
				target = freeName(destDir, name)
			}
		}
		if err := copyAtomic(src, destDir, target); err != nil {
			return res, err
		}
		res.Written = append(res.Written, target)
	}
	if len(res.Written) > 0 && a.live != nil && indexScope()[filepath.Dir(g)] {
		if _, err := backfill.RunOnWorkspace(a.live, archiveBase(), g); err != nil {
			return res, fmt.Errorf("uploaded, reindex failed: %w", err)
		}
	}
	return res, nil
}

// a symlinked dir inside a workspace must not redirect an upload outside it
func uploadTarget(destDir string) (string, error) {
	if !filepath.IsAbs(destDir) {
		return "", fmt.Errorf("upload target must be absolute: %s", destDir)
	}
	for _, seg := range strings.Split(destDir, string(filepath.Separator)) {
		if seg == ".." {
			return "", fmt.Errorf("upload target has ..: %s", destDir)
		}
	}
	clean := filepath.Clean(destDir)
	i := strings.Index(clean+"/", "/.giantmem/")
	if i < 0 {
		return "", fmt.Errorf("upload target is not inside a .giantmem directory: %s", destDir)
	}
	g := clean[:i] + "/.giantmem"
	cur := g
	steps := []string{g}
	if rest := strings.TrimPrefix(clean, g); rest != "" {
		for _, seg := range strings.Split(strings.Trim(rest, "/"), "/") {
			cur = filepath.Join(cur, seg)
			steps = append(steps, cur)
		}
	}
	for _, p := range steps {
		st, err := os.Lstat(p)
		if err != nil {
			return "", err
		}
		if st.Mode()&os.ModeSymlink != 0 || !st.IsDir() {
			return "", fmt.Errorf("upload target crosses a non-directory or symlink: %s", p)
		}
	}
	return g, nil
}

func freeName(dir, name string) string {
	ext := filepath.Ext(name)
	stem := strings.TrimSuffix(name, ext)
	for i := 1; ; i++ {
		p := filepath.Join(dir, fmt.Sprintf("%s (%d)%s", stem, i, ext))
		if _, err := os.Lstat(p); err != nil {
			return p
		}
	}
}

func copyAtomic(src, dir, target string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp, err := os.CreateTemp(dir, ".upload-*")
	if err != nil {
		return err
	}
	if _, err := io.Copy(tmp, in); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	if err := os.Chmod(tmp.Name(), 0o644); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	if err := os.Rename(tmp.Name(), target); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return nil
}

func isPlainDir(p string) bool {
	st, err := os.Lstat(p)
	return err == nil && st.IsDir()
}

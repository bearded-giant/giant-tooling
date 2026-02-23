# giantmem-archive

Archive and search .giantmem/ directories.

Archive location: `~/giantmem_archive/{project}/{timestamp}/`

## Requirements

- Python 3.x - FTS5 search engine (stdlib only, no pip installs)
- `fzf` - interactive result picker
- `bat` - syntax-highlighted preview with match highlight (falls back to sed)
- `rg` (ripgrep) - match term highlighting in preview, legacy search/indexing

## Commands

### archive

Archive (move) a .giantmem/ directory and re-init the workspace.

```bash
giantmem-archive.sh archive [--project <name>] [src]
```

Defaults:
- source: `./.giantmem`
- project: auto-detected from directory (see below)

Project name detection:
- **worktree** (`.git` is a file): parent dir of worktree root (e.g., `edgerouter-wt`)
- **regular repo** (`.git` is a dir): dir containing .giantmem (e.g., `edgerouter`)
- **`--project`**: overrides auto-detection

```bash
gma                                    # mv ./.giantmem to archive, re-init workspace
gma --project cc-wt                    # force project name
giantmem-archive archive ~/path/to/.giantmem  # explicit source
```

Creates: `~/giantmem_archive/{project}/{timestamp}/`
Updates: `latest` symlink in project directory
Builds: `.giantmem-index` for fast searching
Updates: `archives.db` FTS5 database (background, additive per-project)
Re-inits: fresh `.giantmem/` via `workspace_init`

### dedup

Move older duplicate files to `_review/` for cleanup. When the same file (by relative path) exists in multiple timestamp dirs, older versions are moved out. Feature directories (`features/*`) are skipped -- they have their own archive lifecycle via `--feature`.

```bash
giantmem-archive.sh dedup <project> [--dry-run]
```

```bash
giantmem-archive dedup myproj --dry-run    # preview what would move
giantmem-archive dedup myproj              # move older dupes to _review/
rm -rf ~/giantmem_archive/myproj/_review   # delete after review
```

Creates: `~/giantmem_archive/{project}/_review/{timestamp}/{relative_path}`
Rebuilds: search indexes and FTS5 database after cleanup

### search (FTS5 - default)

When `archives.db` exists, search uses SQLite FTS5 with ranked results.

```bash
giantmem-archive.sh search <pattern> [flags]
```

Flags:
| Flag | Description |
|------|-------------|
| `-p <project>` | filter by project |
| `-t <type>` | filter by dir_type: plans, context, research, reviews, filebox, history, prompts, features |
| `-b <branch>` | filter by branch |
| `-l` | search only "latest" archives |
| `-n <N>` | max results (default: 20) |
| `--full` | show matching content snippets |
| `--file-name` | output filepath instead of opening |
| `--no-fzf` | skip fzf picker, plain output |

```bash
gms "jwt"                              # fzf picker with bat preview + highlight
gms "auth" -p cc-wt                    # project-filtered
gms "migration" -t plans               # only plans/
gms "replica" -l                       # latest archives only
gms "auth" -t plans -l                 # type + latest
gms "jwt" --no-fzf                     # plain ranked list, no picker
```

When run interactively, opens fzf with:
- ranked results showing `file:line`
- bat preview centered on the matching line with yellow highlight on search terms
- on select, opens file in default md app (macOS `open`)

```bash
gms "jwt"                              # search -> pick -> opens in default md app
gms "jwt" --file-name                  # search -> pick -> prints filepath
mdv $(gms "jwt" --file-name)           # search -> pick -> render in terminal
```

When piped or with `--no-fzf`, outputs plain ranked list:
```
[score] project/branch/timestamp/dir_type/filename.md:line
```

With `--full`, includes matching content snippet below each result.

Falls back to legacy rg/fzf search if the database doesn't exist. Run `giantmem-search.py ingest` to build it.

### gmq (giantmem-search.py direct)

Ingest and stats go through the Python tool directly:

```bash
gmq ingest                         # rebuild full DB
gmq ingest -p cc-wt                # rebuild for one project
gmq search "jwt"                   # search (same as gms)
gmq stats                          # show counts by project/type
```

Database: `~/giantmem_archive/archives.db`

### index

Rebuild `.giantmem-index` files for legacy search. Not needed for FTS5.

```bash
giantmem-archive.sh index [project]
```

```bash
giantmem-archive.sh index           # all archives
giantmem-archive.sh index myproj    # specific project
giantmem-archive.sh i               # shorthand
```

### list

List archived projects or a project's archives.

```bash
giantmem-archive.sh list [project]
```

```bash
giantmem-archive.sh list           # all projects
giantmem-archive.sh list myproj    # project's branches/timestamps
giantmem-archive.sh l              # shorthand
```

### open

Open archive directory in Finder.

```bash
giantmem-archive.sh open <project> [branch] [timestamp]
```

```bash
giantmem-archive.sh open myproj              # project root
giantmem-archive.sh open myproj main         # latest for branch
giantmem-archive.sh open myproj main 20251220_143022  # specific
giantmem-archive.sh o myproj                 # shorthand
```

## Aliases

In `~/.bash_aliases`:
```bash
alias giantmem-archive='~/dev/giant-tooling/giantmem-archive/giantmem-archive.sh'
alias gma='giantmem-archive archive'
alias gml='giantmem-archive list'
alias gms='giantmem-archive search'
alias gmq='~/dev/giant-tooling/giantmem-archive/giantmem-search.py'
```

Quick reference:
| Alias | Command |
|-------|---------|
| `gms "jwt"` | search -> pick -> open in default md app |
| `gms "auth" -p cc-wt -l` | search with filters |
| `gmq ingest` | rebuild FTS5 database |
| `gmq stats` | show indexed doc counts |
| `gma` | archive (mv) current .giantmem/, re-init |
| `gml` | list archives |

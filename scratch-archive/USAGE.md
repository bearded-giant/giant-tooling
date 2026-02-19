# scratch-archive

Archive and search .giantmem/ directories.

Archive location: `~/giantmem_archive/{project}/{branch}/{timestamp}/`

## Requirements

- Python 3.x - FTS5 search engine (stdlib only, no pip installs)
- `fzf` - interactive result picker
- `bat` - syntax-highlighted preview with match highlight (falls back to sed)
- `rg` (ripgrep) - match term highlighting in preview, legacy search/indexing

## Commands

### archive

Archive a .giantmem/ directory.

```bash
scratch-archive.sh archive [--clean] [--project <name>] [src]
```

Defaults:
- source: `./.giantmem`
- project: auto-detected from directory (see below)
- branch: current git branch

Project name detection:
- **worktree** (`.git` is a file): parent dir of worktree root (e.g., `edgerouter-wt`)
- **regular repo** (`.git` is a dir): dir containing .giantmem (e.g., `edgerouter`)
- **`--project`**: overrides auto-detection

```bash
saa                                    # archive ./.giantmem, auto-detect project+branch
saa --clean                            # archive and remove ./.giantmem
saa --project cc-wt                    # force project name
sa archive ~/path/to/.giantmem         # explicit source
```

Creates: `~/giantmem_archive/{project}/{branch}/{timestamp}/`
Updates: `latest` symlink in branch directory
Builds: `.scratch-index` for fast searching
Updates: `archives.db` FTS5 database (background, additive per-project)

### search (FTS5 - default)

When `archives.db` exists, search uses SQLite FTS5 with ranked results.

```bash
scratch-archive.sh search <pattern> [flags]
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
sas "jwt"                              # fzf picker with bat preview + highlight
sas "auth" -p cc-wt                    # project-filtered
sas "migration" -t plans               # only plans/
sas "replica" -l                       # latest archives only
sas "auth" -t plans -l                 # type + latest
sas "jwt" --no-fzf                     # plain ranked list, no picker
```

When run interactively, opens fzf with:
- ranked results showing `file:line`
- bat preview centered on the matching line with yellow highlight on search terms
- on select, opens file in default md app (macOS `open`)

```bash
sas "jwt"                              # search -> pick -> opens in default md app
sas "jwt" --file-name                    # search -> pick -> prints filepath
mdv $(sas "jwt" --file-name)             # search -> pick -> render in terminal
```

When piped or with `--no-fzf`, outputs plain ranked list:
```
[score] project/branch/timestamp/dir_type/filename.md:line
```

With `--full`, includes matching content snippet below each result.

Falls back to legacy rg/fzf search if the database doesn't exist. Run `scratch-search.py ingest` to build it.

### saq (scratch-search.py direct)

Ingest and stats go through the Python tool directly:

```bash
saq ingest                         # rebuild full DB
saq ingest -p cc-wt                # rebuild for one project
saq search "jwt"                   # search (same as sas)
saq stats                          # show counts by project/type
```

Database: `~/giantmem_archive/archives.db`

### index

Rebuild `.scratch-index` files for legacy search. Not needed for FTS5.

```bash
scratch-archive.sh index [project]
```

```bash
scratch-archive.sh index           # all archives
scratch-archive.sh index myproj    # specific project
scratch-archive.sh i               # shorthand
```

### list

List archived projects or a project's archives.

```bash
scratch-archive.sh list [project]
```

```bash
scratch-archive.sh list           # all projects
scratch-archive.sh list myproj    # project's branches/timestamps
scratch-archive.sh l              # shorthand
```

### open

Open archive directory in Finder.

```bash
scratch-archive.sh open <project> [branch] [timestamp]
```

```bash
scratch-archive.sh open myproj              # project root
scratch-archive.sh open myproj main         # latest for branch
scratch-archive.sh open myproj main 20251220_143022  # specific
scratch-archive.sh o myproj                 # shorthand
```

## Aliases

In `~/.bash_aliases`:
```bash
alias scratch-archive='~/dev/giant-tooling/scratch-archive/scratch-archive.sh'
alias sa='scratch-archive'
alias saa='sa archive'
alias sal='sa list'
alias sas='sa search'
alias sai='sa index'
alias saq='~/dev/giant-tooling/scratch-archive/scratch-search.py'
```

Quick reference:
| Alias | Command |
|-------|---------|
| `sas "jwt"` | search -> pick -> open in default md app |
| `sas "auth" -p cc-wt -l` | search with filters |
| `saq ingest` | rebuild FTS5 database |
| `saq stats` | show indexed doc counts |
| `saa` | archive current .giantmem/ |
| `sal` | list archives |

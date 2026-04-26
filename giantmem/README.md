# giantmem

`giantmem` is the front door to a three-layer system for working across many repos and Claude sessions.

```
[ memory ]    indexed search across docs + Claude session transcripts
   ^
[ workspace ] per-branch context dir (.giantmem/) inside each worktree
   ^
[ worktree ]  bare repo + sibling worktrees, one per branch
```

Worktrees give you isolated branch checkouts. Workspaces (`.giantmem/`) live inside those worktrees and capture per-branch context. The `giantmem` CLI unifies search, archive, sessions, and worktree lifecycle so you have one mental model and one command tree.

The three layers live in three sibling repos by design — they should change independently:

| Layer | Repo | Surfaced via |
|-------|------|--------------|
| Worktrees | `~/dev/giant-tooling/git-worktrees/` | `giantmem worktree`, plus per-project shell shortcuts (`cwt`, `cwtl`, ...) |
| Workspaces | `~/dev/giant-tooling/workspace/` | `giantmem workspace` |
| Memory + CLI | `~/dev/giant-tooling/giantmem/` (this repo) | `giantmem find`, `giantmem session`, `giantmem archive`, `giantmem ingest`, `giantmem mcp` |

For a five-minute onboarding read, see [INTRO.md](INTRO.md). For the full command reference, see [USAGE.md](USAGE.md).

## Install

```
make install                  # builds and copies to ~/.local/bin/giantmem
```

`~/.local/bin` must be on `$PATH`. Tab-completion turns `gia<tab>` into `giantmem` after one shell init.

## Capabilities

- `giantmem find` — FTS5 across live + archive + sessions, merged + ranked.
- `giantmem session list|show|find|resume` — find and resume Claude sessions; `resume` chdirs to the recorded cwd and exec's `claude --resume <uuid>`.
- `giantmem index init|migrate|sessions|live` — bootstrap, consolidate `-wt` projects, backfill cwd, full live rescan.
- `giantmem archive run|list|open|dedup|stale` — move `.giantmem/` into the archive tree, list, find dormant workspaces.
- `giantmem worktree list|remove|init|adopt|projects|status|branches|prune|repair|shell-init` — setup wizards plus native git ops; `remove` autoarchives the workspace.
- `giantmem workspace status|init|migrate|tree|note|discover|complete|sync|features|gitlog|bootstrap` — workspace lifecycle.
- Live indexing hook: `~/.claude/hooks/live_index.py` (PostToolUse, matcher `Write|Edit|MultiEdit`). Filters `.giantmem/**/*.md`, upserts into `live.db`.

See [USAGE.md](USAGE.md) for the daily-driver cheat sheet.

## Usage

```
giantmem                                    # discoverable help
giantmem find <query>                       # FTS5: live + archive + sessions
giantmem find -h                            # any subcommand: -h works at every level
giantmem stats                              # counts by project / source / dir_type

giantmem session list -p chat-orchestrator  # recent sessions for a project (LIKE)
giantmem session find "hub and spoke"       # FTS over session transcripts
giantmem session resume <id-prefix>         # cd cwd, exec claude --resume <uuid>

giantmem archive run                        # mv .giantmem -> archive, ingest, re-init
giantmem archive list [project]             # list archived projects/timestamps
giantmem archive stale --days 30            # find dormant workspaces

giantmem worktree list                      # all worktrees + .giantmem status
giantmem worktree remove <path>             # autoarchive then git worktree remove
```

### Flags (find)

| Flag | Description |
|------|-------------|
| `-p, --project` | filter by project (LIKE) |
| `-t, --type` | filter by dir_type (plans, research, reviews, history, ...) |
| `-s, --source` | filter by source_type (workspace, session, domain) |
| `-f, --feature` | filter by feature (live.db) |
| `-l, --latest` | only latest archive per project |
| `-n, --limit` | max results (default 20) |
| `--live` | only live workspaces |
| `--archive` | only archives |
| `--full` | include matching content snippet |
| `--paths` | print absolute paths only |
| `--json` | JSON output |
| `--archive-base` | override archive root (env: `GIANTMEM_ARCHIVE_BASE`) |

## Config

| Env | Default |
|-----|---------|
| `GIANTMEM_ARCHIVE_BASE` | `~/giantmem_archive` |

## Layout

```
giantmem/            (repo dir; binary builds to ./giantmem)
  main.go
  cmd/               # cobra subcommands
    archive.go
    find.go
    index.go
    session.go
    stats.go
    workspace.go
    worktree.go
    version.go
    root.go
  internal/
    archiver/        # filesystem archive ops
    db/              # sqlite open + schema helpers
    output/          # JSON writers
    project/         # worktree-aware detector
  Makefile
  README.md
  USAGE.md
```

## Shell completion

```
giantmem completion bash > /etc/bash_completion.d/giantmem
giantmem completion zsh  > ~/.zfunc/_giantmem
```

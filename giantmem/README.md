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

For a full stack install (CLI + GUI + daemon LaunchAgent + 5-min session-sweep LaunchAgent + first-run backfill) use the repo-root bootstrap — see [../INSTALL.md](../INSTALL.md):

```
cd ~/dev/giant-tooling && make bootstrap
```

CLI only:

```
make install                  # builds and copies to ~/.local/bin/giantmem
```

`~/.local/bin` must be on `$PATH`. Tab-completion turns `gia<tab>` into `giantmem` after one shell init.

### External tool deps

The CLI shells out to a few standard tools. All are optional unless noted.

| Tool | Required for | Fallback if missing |
|------|--------------|---------------------|
| `fzf` | `giantmem find -i` (interactive picker) | hard-required; command errors out |
| `rg` (ripgrep) | `giantmem find -i` per-line match expansion | falls back to file-level picker |
| `bat` | `giantmem find -i` syntax-highlighted preview for non-jsonl hits | uses `awk` plain-text preview |
| `jq` | `giantmem find -i` decoded preview for `.jsonl` session transcripts (role + content text) | uses raw line truncation |

Install on macOS: `brew install fzf ripgrep bat jq`.

## Capabilities

- `giantmem find` — FTS5 across live + sessions, merged + ranked. Auto-routes through `giantmemd` when available; `--no-daemon` bypasses.
- `giantmem session list|show|find|resume|export|diff` — find, resume, export, and diff Claude sessions; `resume` chdirs to the recorded cwd (falling back to the `cd` matcher) and exec's `claude --resume <uuid>`.
- `giantmem ingest` — primarily ingests Claude session JSONLs into `archives.db`. The legacy snapshot-workspace pass (`workspace-md`/`domain-json`) is still registered but no longer fed by `archive run`; live.db is now the authoritative store for `.giantmem/` content.
- `giantmem index init|migrate|sessions|live|backfill` — bootstrap, consolidate `-wt` projects, backfill cwd, full live rescan. `backfill` walks every `.giantmem/` under `$GIANTMEM_DEV_ROOTS` and upserts every non-empty file (any extension, max 5MB) into `live.db.live_docs`; the daemon also runs this once at startup. Schema is versioned via `PRAGMA user_version`; migrations run automatically on `db.Open`.
- `giantmem archive run|list|open|dedup|stale` — cold filesystem snapshot of `.giantmem/` into `~/giantmem_archive/{project}/{ts}/`. No longer feeds `archives.db` (live.db owns the searchable content); the move is purely a backup. `archive run` still prunes matching live.db rows since the source dir has moved.
- `giantmem worktree list|remove|init|adopt|projects|status|branches|prune|repair|shell-init` — setup wizards plus native git ops; `remove` autoarchives the workspace. `shell-init --install` writes the source-line block into `.bashrc`/`.zshrc` with sentinels.
- `giantmem workspace status|init|migrate|tree|note|discover|complete|sync|features|gitlog|bootstrap` — workspace lifecycle.
- `giantmem doctor [--fix]` — health audit across worktrees, workspaces, archives, hooks, DBs. `--fix` repairs broken latest symlinks, stale MCP entries, missing hooks, drift, and orphan worktrees. `.giantmem-ignore` (per-workspace) and `~/.config/giantmem/global-ignore` quiet known-stale paths.
- `giantmem config` — single source of truth for resolved paths, env vars, hooks, MCP wiring, and schema versions. `--json` for scripts; `--write-defaults` emits a config.toml template.
- `giantmem prime` — workspace context primer (used by SessionStart hook).
- `giantmem cd <pattern>` — fuzzy-jump to any worktree under `~/dev` (pair with `gj` shell wrapper).
- `giantmem status`, `giantmem tail`, `giantmem capture`, `giantmem timeline`, `giantmem plan list`, `giantmem backup push` — daily-polish utilities (statusline JSON, live.db tail-f, quick brain-dump capture, ASCII activity timeline, cross-workspace plan aggregation, archives.db git-backed snapshot).
- `giantmem mcp serve` — MCP tools so Claude can self-discover state: `search_archive`, `list_sessions`, `get_session_summary`, `recent_writes`, `feature_status`, `workspace_tree`, `find_artifact` (+ `scope`/`lifecycle`/`semantic` args), `get_artifact`, `list_features_with_artifacts`, `get_stats`, `find_entity`. All registered with read-only / idempotent / closed-world hints.
- `giantmem daemon start|stop|restart|status|health|install|uninstall|serve` — long-running JSON-RPC server on `~/.cache/giantmem/giantmemd.sock`. Caches DB handles, eliminates ~700ms cold start, detects schema drift and asks for restart. `install` registers a launchd LaunchAgent on macOS.
- `giantmem scope init|list|show|add-repo|sync` — cross-repo scope registry at `~/.giantmem-global/scopes.yaml`; lets `artifact list --scope X` span multiple repos. See [docs/scoped-memory.md](../docs/scoped-memory.md).
- `giantmem access top|prune` — inspect/trim `artifact_access` log. Every `artifact list|show` and MCP `find_artifact` writes rows; `access_count` (30d) feeds the hybrid scorer and surfaces on `--json` output.
- `giantmem embed [--backfill] [--reset] [--scope X] [--repo Y] [--backend stub|python|ollama]` — write per-artifact embeddings into the `artifact_embeddings` vec0 table. Backend defaults to `stub` (deterministic, NOT semantic); `python` spawns `workspace/scripts/embed.py` (sentence-transformers); `ollama` POSTs `OLLAMA_HOST/api/embeddings`. Body-hash gated — re-runs are idempotent.
- `giantmem artifact search <query>` — hybrid scoring (FTS + vector + recency + access). Weights env-tunable (`GIANTMEM_HYBRID_*_WEIGHT`, sum 1.0). Opt-in; default `find` + `artifact list` stay FTS-only.
- `giantmem suggest-domain [text]` — TF-IDF over the source-spec corpus, returns top-N domain candidates. Reads stdin when no positional arg.
- `giantmem entity list|show <path>` — file-level entities promoted from `.giantmem/domains/*.json` `key_files[]`. `show` lists back-references — every artifact body that mentions the path.
- `giantmem watch start|stop|status|run|install` — fsnotify daemon, debounced 2s per workspace, runs `artifact reindex` on `.giantmem/**` edits. PID at `~/.cache/giantmem/giantmem-watch.pid`. `install` writes a macOS LaunchAgent.
- Hooks (Python stdlib): `live_index.py` (PostToolUse → live.db), `session_prime.py` (SessionStart → context primer), `precompact_capture.py` (PreCompact → snapshot), `session_end_ingest.py` (SessionEnd → instant session ingest). All wrap their main in try/except and append failures to `~/.cache/giantmem/hook.log`.

See [USAGE.md](USAGE.md) for the daily-driver cheat sheet and [PLAN-2.md](PLAN-2.md) for the post-v2 roadmap (phases 9-13).

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
| `-s, --source` | archives.db source_type filter: `workspace` (markdown from archived `.giantmem/`), `session` (Claude JSONL transcripts), `domain` (domain JSONs). For live workspaces use `--live`. |
| `-f, --feature` | filter by feature (live.db) |
| `-l, --latest` | only latest archive per project |
| `-n, --limit` | max results (default 20) |
| `--live` | only live workspaces |
| `--archive` | only archives |
| `--full` | include matching content snippet |
| `--paths` | print absolute paths only |
| `--json` | JSON output |
| (default) | interactive fzf picker over per-match line snippets (rg-expanded); preview decodes `.jsonl` via jq, otherwise bat with `--highlight-line`. Auto-disabled when stdout isn't a TTY or when `--json`/`--paths` is set. |
| `-i, --no-interactive` | force script mode (plain text output) even on a TTY |
| `--tool` | session-only filter: keep matches on lines where Claude used these tool names (`Write`, `Edit`, `MultiEdit`, `Read`, `Bash`, `Grep`, ...). Repeat or comma-separate. Triggers per-line expansion in script mode too. See USAGE.md for full tool catalog. |
| `--ext` | session-only filter: keep matches where a tool_use touched a file with these extensions (e.g. `--ext md,go`). Composes with `--tool` via AND. Leading dot optional. |
| `--include-read` | include Claude's `Read` tool calls in session output (default: hidden, since Read is high-volume noise). Auto-enabled when `Read` appears in `--tool`. |
| `-o, --open` | open selection in `$EDITOR` at the matched line (`+N` for vi/vim/nvim/nano/emacs, `-g path:N` for code/cursor); ignored in script mode |
| `--archive-base` | override archive root (env: `GIANTMEM_ARCHIVE_BASE`) |

## Config

| Env | Default | Notes |
|-----|---------|-------|
| `GIANTMEM_ARCHIVE_BASE` | `~/giantmem_archive` | Archive root |
| `GIANTMEM_NO_DAEMON` | unset | When set, CLI bypasses `giantmemd` and opens DBs directly |

| File | Purpose |
|------|---------|
| `~/.config/giantmem/sources.toml` | Ingest source registry (builtins + external plugins). Optional; defaults to the three builtins enabled. |
| `~/.config/giantmem/canonical.json` | Hand-curated project name mappings consumed by `project.Canonicalize`. |
| `.giantmem-ignore` (per-workspace) / `~/.config/giantmem/global-ignore` | gitignore-style globs honored by `doctor` and stale-scan. |
| `~/.cache/giantmem/giantmemd.sock` | Daemon RPC socket (and `.pid` sibling). |
| `~/.cache/giantmem/hook.log` | Hook failure log (rotates at 1 MB). |

## Layout

```
giantmem/            (repo dir; binary builds to ./giantmem)
  main.go
  cmd/               # cobra subcommands
    archive.go backup.go capture.go cd.go config.go daemon.go
    daemon_bench.go daemon_launchd.go doctor.go find.go index.go
    ingest.go mcp.go mcp_tools.go plan.go prime.go session.go
    stats.go status.go tail.go timeline.go workspace.go worktree.go
    version.go root.go z_completions.go
  internal/
    archiver/        # filesystem archive ops
    daemon/          # JSON-RPC server, client, protocol, launchd plist
    db/              # sqlite open + schema versioning + migrations
    health/          # doctor checks
    ingest/          # archive walk, session JSONL parser, topic detector
    output/          # JSON writers
    project/         # worktree-aware detector + Canonicalize
    search/          # FTS5 query layer (shared by CLI + daemon)
    sessions/        # session listing / export / diff helpers
    sources/         # ingest plugin registry: builtins + external + TOML config
  Makefile
  PLAN.md            # original v2 roadmap (phases 1-8)
  PLAN-2.md          # post-v2 roadmap (phases 9-13)
  README.md
  USAGE.md
```

## Shell completion

```
giantmem completion bash > /etc/bash_completion.d/giantmem
giantmem completion zsh  > ~/.zfunc/_giantmem
```

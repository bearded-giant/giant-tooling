# giantmem usage

Daily-driver cheat sheet for the `giantmem` CLI. One binary, one verb tree, `--help` at every level.

Binary: `~/.local/bin/giantmem`. Source: `~/dev/giant-tooling/giantmem/`.

## Install / update

| Command | Effect |
|---------|--------|
| `cd ~/dev/giant-tooling/gm && make install` | rebuild and copy to `~/.local/bin/giantmem` |
| `giantmem version` | show build info |
| `giantmem completion bash > ~/.bash_completion.d/giantmem` | shell completion (bash) |
| `giantmem completion zsh > ~/.zfunc/_giantmem` | shell completion (zsh) |

## Discoverability

`giantmem --help` lists subcommands. Every subcommand supports `-h`. `giantmem session --help`, `giantmem index --help`, `giantmem find --help`. No need to memorize anything past `giantmem`.

## Search (the main event)

`giantmem find <query>` runs an FTS5 query across **live workspaces** (`live.db`) and **archived docs + Claude session transcripts** (`archives.db`), then merges and ranks the results.

| Command | What it does |
|---------|--------------|
| `giantmem find "jwt"` | search everything |
| `giantmem find "hub and spoke" -p chat-orchestrator` | filter by project (LIKE) |
| `giantmem find "auth" -t plans` | filter by `dir_type` (plans, research, reviews, ...) |
| `giantmem find "x" -f better-search` | filter by active feature name |
| `giantmem find "x" --live` | only live workspaces |
| `giantmem find "x" --archive` | only archives |
| `giantmem find "x" -s session` | only Claude session transcripts |
| `giantmem find "x" -l` | only "latest" archive per project |
| `giantmem find "x" -n 50` | limit to 50 results (default 20) |
| `giantmem find "x" --full` | include FTS snippet under each hit |
| `giantmem find "x" --paths` | print absolute paths only (great for piping) |
| `giantmem find "x" --json` | JSON output |
| `giantmem find "x" --paths \| xargs $EDITOR` | open all hits in editor |

`dir_type` values: `plans`, `context`, `research`, `reviews`, `filebox`, `history`, `prompts`, `features`, `domains`, `root`.

`source` values: `workspace`, `session`, `domain`, `live`.

## Sessions: find buried conversations and resume them

The whole point of session search: stop hunting through `~/.claude/projects/**` by hand.

| Command | What it does |
|---------|--------------|
| `giantmem session list` | most recent sessions across all projects |
| `giantmem session list -p chat-orchestrator -n 30` | recent sessions for a project (LIKE match) |
| `giantmem session find "hub and spoke"` | FTS5 over session transcripts |
| `giantmem session show <id-prefix>` | metadata: project, cwd, topic, jsonl path |
| `giantmem session resume <id-prefix>` | chdir to recorded cwd, then `exec claude --resume <uuid>` |

`<id-prefix>` is any unique prefix of the session UUID (e.g. `40503b40`). Ambiguous prefixes print all candidates and exit.

If the recorded cwd no longer exists (e.g. you converted a regular repo to a bare-with-worktrees layout), `giantmem session resume` falls back to `<cwd>-wt/main` then `<cwd>-wt/master` automatically.

## Stats

| Command | What it shows |
|---------|---------------|
| `giantmem stats` | counts grouped by project / source_type / dir_type, plus a total |

## Archive management

Filesystem ops happen in Go. The FTS5 ingest is kicked off in the background after a move so `giantmem find` picks up the new rows automatically.

| Command | What it does |
|---------|--------------|
| `giantmem archive run` | mv current `./.giantmem` to `~/giantmem_archive/<project>/<ts>/`, update `latest` symlink, ingest into FTS, re-init a fresh `.giantmem` |
| `giantmem archive run --no-reinit` | same, but skip workspace re-init (used by `giantmem worktree remove`) |
| `giantmem archive run --project foo --dry-run` | preview what would happen, no fs changes |
| `giantmem archive list` | list archived projects with archive counts |
| `giantmem archive list <project>` | list timestamps for a project, marking `latest` |
| `giantmem archive open <project> [ts]` | open archive in Finder; defaults to `latest` |
| `giantmem archive dedup <project> [--dry-run]` | move older duplicate files (same relative path) into `<project>/_review/` for batch deletion |
| `giantmem archive stale --days N --root PATH...` | scan roots (default `~/dev`) for live `.giantmem/` dirs whose newest md is older than N days |

## Worktree helpers

`giantmem worktree` covers the bare-with-worktrees layout (`~/dev/foo-wt/.bare` + `~/dev/foo-wt/main`).

| Command | What it does |
|---------|--------------|
| `giantmem worktree list` | from inside any worktree, list all worktrees + branch + HEAD + `.giantmem` status |
| `giantmem worktree list <bare-dir>` | run from anywhere; `<bare-dir>` is the worktree parent |
| `giantmem worktree remove <path>` | auto-archive `.giantmem` (unless `--keep`), then `git worktree remove` |
| `giantmem worktree remove <path> --force` | proceed even if archive fails; pass `--force` to git |
| `giantmem worktree remove <path> --dry-run` | print planned actions only |

This is the autoarchive entry point: deleting a worktree no longer leaves `.giantmem` behind to lose track of, and never blocks search because the archive is captured before the worktree disappears.

## Workspace lifecycle

| Command | What it does |
|---------|--------------|
| `giantmem workspace status` | show workspace status |
| `giantmem workspace bootstrap` | smart init/migrate/sync |
| `giantmem workspace migrate` | move loose `.giantmem/` files into the right subdirs |
| `giantmem workspace tree` | regenerate `tree.md` |
| `giantmem workspace note "..."` | add a session note |
| `giantmem workspace discover "..."` | add a discovery note |
| `giantmem workspace complete` | mark workspace complete |
| `giantmem workspace sync` | refresh tree + git log |
| `giantmem workspace features` | show feature status table |
| `giantmem workspace gitlog` | update `git-log.md` |
| `giantmem workspace init [dir] [name]` | initialize `.giantmem/` |

## Index management

You should rarely need this; the live indexing hook handles ongoing writes and the archive ingest is automatic on `giantmem archive run`. Use these for one-off fixups.

| Command | What it does |
|---------|--------------|
| `giantmem index init` | ensure both DB schemas (idempotent; safe to re-run) |
| `giantmem index migrate --dry-run` | preview project consolidations (`foo` → `foo-wt`) |
| `giantmem index migrate` | apply consolidation |
| `giantmem index sessions` | backfill `cwd` on session rows from JSONL files |
| `giantmem index live` | rescan `~/dev` for `.giantmem/**/*.md` and rebuild live.db |
| `giantmem index live <root>...` | rescan only the given roots |
| `giantmem ingest` | full re-index of archives.db (workspaces + sessions) |
| `giantmem ingest -p foo` | re-index a single project |
| `giantmem ingest --sessions-only` | re-ingest Claude session JSONLs only |
| `giantmem ingest --force` | force full session re-ingest, ignoring mtime |

## MCP server

| Command | What it does |
|---------|--------------|
| `giantmem mcp serve` | run as an MCP stdio server. Wired into `~/.claude/settings.json` as the `giantmem-search` MCP. Exposes one tool: `search_archive(query, project?, source_type?, topic?, limit?)`. |

## Live indexing hook

`giantmem` ships with a Claude Code `PostToolUse` hook (`~/.claude/hooks/live_index.py`, matcher `Write|Edit|MultiEdit`). On every Claude `Write`/`Edit` of an `.md` file under a `.giantmem/` directory, it upserts a row into `live.db` capturing project, worktree path, feature (active `in_progress`), `dir_type`, session id, git sha, mtime, and content.

Outside `.giantmem/`, the hook returns immediately (a few ms). Files larger than 5 MB are truncated.

After installing or updating the hook, restart Claude Code so it re-reads `settings.json`. To catch up on docs already on disk, run `giantmem index live ~/dev` once.

## Project naming rules

`giantmem` uses a worktree-aware project detector with a `-wt` consolidation rule.

| Layout | Project name |
|--------|--------------|
| Regular repo at `~/dev/foo` | `foo` |
| Regular repo `~/dev/foo` AND `~/giantmem_archive/foo-wt/` exists | `foo-wt` (consolidated) |
| Bare-with-worktrees `~/dev/foo-wt/main` | `foo-wt` |

`giantmem index migrate` rewrites old `documents.project` values from `foo` to `foo-wt` whenever both buckets exist. Pre-existing `dev/ai/chat-orchestrator` session rows still match `giantmem session list -p chat-orchestrator` because `-p` is a `LIKE` filter.

## Storage layout

```
~/giantmem_archive/
  archives.db    # immutable historical: archived workspace docs, sessions, domains
  live.db        # hot, gitignored: live_docs (.giantmem/**/*.md), active_sessions
  {project}/{timestamp}/...   # archived .giantmem trees
```

`archives.db` is meant to be backupable (push to a private repo, sync to cloud).
`live.db` is rebuilt on demand from disk via `giantmem index live`, so losing it is recoverable.

## Common workflows

1. "Where did I write that hub-and-spoke architecture doc?"
   ```
   giantmem find "hub and spoke" -p chat-orchestrator
   ```

2. "Resume the session I was in last Wednesday."
   ```
   giantmem session list -p chat-orchestrator -n 20
   giantmem session resume <id-prefix>
   ```

3. "Open every plan doc that mentions JWT."
   ```
   giantmem find "jwt" -t plans --paths | xargs $EDITOR
   ```

4. "Show what's been indexed for this repo."
   ```
   giantmem stats | grep my-project
   ```

5. "I just wrote some docs. Make sure they're searchable."
   ```
   giantmem index live $(pwd)
   ```

6. "Search transcripts for a specific debugging incident."
   ```
   giantmem session find "deadlock connection pool"
   ```

## Config

| Env | Default | Effect |
|-----|---------|--------|
| `GIANTMEM_ARCHIVE_BASE` | `~/giantmem_archive` | both DBs and the archive tree live here |
| `--archive-base <path>` | flag form of the above | overrides per command |

## Troubleshooting

The hook isn't firing on writes. Restart Claude Code so it re-reads `~/.claude/settings.json`. Confirm with `tail -f` on the DB modtime: `stat -f %Sm ~/giantmem_archive/live.db` should change after each Claude `Write` to a `.giantmem/**/*.md` file. Force a manual fire:
```
echo '{"tool_name":"Write","tool_input":{"file_path":"<abspath>"},"cwd":"<repo>"}' | python3 ~/.claude/hooks/live_index.py
```

Find returns nothing. Check the right DB exists: `ls -la ~/giantmem_archive/{archives,live}.db`. Run `giantmem stats` to confirm rows. For live rows specifically: `sqlite3 ~/giantmem_archive/live.db "SELECT COUNT(*) FROM live_docs"`. If empty, run `giantmem index live ~/dev`.

`giantmem session resume` says cwd is missing. The recorded cwd was deleted (likely a bare-repo migration). The CLI tries `<cwd>-wt/main` and `<cwd>-wt/master` automatically; if neither exists, fix the path manually or update the row's `cwd`.

`giantmem index migrate` lists nothing. No project pairs need consolidating. Sanity check: `sqlite3 ~/giantmem_archive/archives.db "SELECT DISTINCT project FROM documents ORDER BY project"`.


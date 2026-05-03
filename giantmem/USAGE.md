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
| `giantmem find "x" -s session --tool Write,Edit` | session matches restricted to Claude tool calls of those names |
| `giantmem find "x" -s session --tool Write,Edit,MultiEdit --ext md` | filter further to file_paths ending in `.md` |
| `giantmem find "hub-and-spoke" -s session` | hyphenated / punctuation queries auto-quoted for FTS5 |
| `giantmem find '"exact phrase here"' -s session` | wrap in double-quotes for literal substring matching |

`dir_type` values: `plans`, `context`, `research`, `reviews`, `filebox`, `history`, `prompts`, `features`, `domains`, `root`.

`source` values: `workspace`, `session`, `domain`. (`live` is a separate db — use `--live` flag, not `-s live`.)

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

## Recent (cross-repo pickup)

`giantmem recent` ranks live workspaces by mtime. Built for jumping back into the doc/repo you last touched in another tmux pane without copying paths around. Pairs with the `/recent-docs` slash command (loads doc + auto-pairs the repo).

| Command | What it does |
|---------|--------------|
| `giantmem recent docs` | recent `.giantmem/*.md` docs across all live projects |
| `giantmem recent docs -t research,plans` | filter by `dir_type` (CSV) |
| `giantmem recent docs --exclude-current` | drop docs from current repo |
| `giantmem recent docs --since 7d -n 15` | window + limit |
| `giantmem recent docs --paths` | absolute paths only (one per line, pipe-friendly) |
| `giantmem recent docs --json` | structured (used by slash commands) |
| `giantmem recent docs -i` | interactive fzf picker w/ preview; selection → `pbcopy` + stdout |
| `giantmem recent repos` | recently active repos (any layout — bare worktrees AND plain repos) |
| `giantmem recent repos --exclude-current --json -n 10` | typical slash-command call |

Repos derive from `live_docs.worktree_path` grouped by `MAX(mtime)`, so anything with a recent `.giantmem` write shows up regardless of git layout.

### Ignore globs

`recent docs` honors a user-config ignore list at `~/.config/giantmem/config.toml` (or `$XDG_CONFIG_HOME/giantmem/config.toml`):

```toml
[recent]
# replaces defaults
ignore_docs = ["*/features/_index.md", "*notes.md"]
# OR adds to defaults
append_ignore_docs = ["*notes.md"]
```

Pattern semantics:
- no `/` in pattern → basename glob via `filepath.Match` (e.g. `*notes.md`)
- has `/` → tail-segment glob (e.g. `*/features/_index.md` matches anywhere in the path)

Built-in defaults: `*/features/_index.md`. Use `append_ignore_docs` to add without losing the default.

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
| `giantmem ingest` | full re-index of archives.db across all enabled sources |
| `giantmem ingest -p foo` | re-index a single project |
| `giantmem ingest -s claude-jsonl` | run only the named source (repeatable / comma-separated) |
| `giantmem ingest --sessions-only` | shortcut for `-s claude-jsonl` |
| `giantmem ingest --workspaces-only` | shortcut for `-s workspace-md,domain-json` |
| `giantmem ingest --force` | force full session re-ingest, ignoring mtime |

Sources are configured at `~/.config/giantmem/sources.toml`. Without that file the three builtins (`workspace-md`, `claude-jsonl`, `domain-json`) are used. Add an `[[source]]` block with `kind = "external"`, `ingest_cmd = "..."`, and a `mapping` table to plug in any subprocess that emits JSONL on stdout — see `PLAN-2.md` §12.

## Daemon mode

`giantmemd` is a long-running RPC server. When it's reachable, `giantmem find` calls it over a unix socket instead of opening sqlite from scratch each time.

| Command | What it does |
|---------|--------------|
| `giantmem daemon start` | spawn detached daemon, write pidfile + log under `~/.cache/giantmem/` |
| `giantmem daemon stop` | SIGTERM the running daemon, wait for socket to close |
| `giantmem daemon restart` | stop then start |
| `giantmem daemon status` | print uptime, RSS, request count, schema versions |
| `giantmem daemon health --benchmark` | JSON health + p50/p99 over 200 calls |
| `giantmem daemon install` | macOS: write `~/Library/LaunchAgents/com.giantmem.daemon.plist` and load it |
| `giantmem daemon uninstall` | unload + remove the LaunchAgent |
| `giantmem daemon serve` | foreground mode (used by launchd / `start`) |

Find auto-routes through the daemon when its socket is alive. Pass `--no-daemon` (or set `GIANTMEM_NO_DAEMON=1`) to bypass. Schema migrations are detected at request time: the daemon returns a "schema migration pending; restart giantmemd" error until you `daemon restart`.

## Backup

| Command | What it does |
|---------|--------------|
| `giantmem backup init [remote-url]` | initialize `~/giantmem_archive_backup` (clones a remote, or creates an empty repo). `--force` removes existing dir |
| `giantmem backup push` | copy `archives.db` (and `live.db` if present) into the backup repo, commit, push (skips push if no remote) |
| `giantmem backup push --no-push --message "..."` | commit only, with custom message |
| `giantmem backup status` | show last commit, dirty state, configured remotes |
| `giantmem backup --dir <path>` | use a different backup directory |

Pair with `/schedule` to snapshot weekly.

## Session export / diff

| Command | What it does |
|---------|--------------|
| `giantmem session export <id>` | clean markdown transcript on stdout |
| `giantmem session export <id> -o session.md` | write to file |
| `giantmem session export <id> --tools=false` | omit collapsed tool-call blocks |
| `giantmem session diff <a> <b>` | compare two sessions: msg counts, bash counts, file sets |
| `giantmem session diff <a> <b> --json` | structured output for scripts |

## Plan aggregation

| Command | What it does |
|---------|--------------|
| `giantmem plan list` | tail every `plans/current.md` across all live worktrees, newest first |
| `giantmem plan list -p chat-orch` | filter by project (LIKE) |
| `giantmem plan list -n 10` | tail 10 lines per plan (0 = full) |
| `giantmem plan list --root ~/work` | scan an additional root |

## Activity timeline

| Command | What it does |
|---------|--------------|
| `giantmem timeline` | last 14 days, all projects, sessions+archives |
| `giantmem timeline -d 30 -p chat-orch -s session` | filtered window |

Bars: `·` 0, `▁` 1-2, `▂` 3-5, `▃` 6-9, `▅` 10-19, `█` 20+.

## Recency filter

| Flag | Description |
|------|-------------|
| `giantmem find <q> --since 7d` | only docs/sessions newer than 7 days |
| `giantmem find <q> --since 2h` | last 2 hours |
| `giantmem find <q> --until 1d` | only older than 1 day |
| `giantmem find <q> --since 2026-04-20T00:00:00Z` | RFC3339 timestamp |

Duration units: `s`, `m`, `h`, `d`, `w`. Combinations like `2h30m` work too.

## Interactive search

Interactive is the **default** when stdout is a TTY. Each match is expanded to a per-line hit via ripgrep, fed into fzf with a context-aware preview pane.

| Flag | Description |
|------|-------------|
| `giantmem find <q>` | fzf picker over per-match line snippets (default on TTY) |
| `giantmem find <q> -o` | open selection in `$EDITOR` at the matched line on Enter |
| `giantmem find <q> -i` | force script mode (plain text), even on a TTY |
| `giantmem find <q> --tool Write,Edit` | session-only filter — keep only matches on lines where Claude used these tool names |
| `giantmem find <q> \| ...` | piped → script mode auto-detected (no `-i` needed) |

What the panes show:

- **Left (list)**: `[score] project/timestamp source :line  [role] excerpt ⟨ToolName file_or_command⟩` for `.jsonl` session matches; raw line text for everything else.
- **Right (preview)**: for `.jsonl`, ±2 surrounding lines with role + content + decoded tool calls (Write/Edit show file path + content excerpt; Bash shows the command; Grep/Glob show the pattern). For other files, `bat` with `--highlight-line` and ±12/+50 context.
- Enter without `-o` → prints `path:line` (pipeable).
- Enter with `-o` → opens `$EDITOR +N path` (or `code -g path:N` when `$EDITOR` looks like VS Code / Cursor).

External tool deps (all installable via `brew install fzf ripgrep bat jq`):

| Tool | Required for | Fallback |
|------|--------------|----------|
| `fzf` | interactive picker | hard requirement (errors out) |
| `rg` (ripgrep) | per-line match expansion + `--tool` filter | falls back to file-level picker |
| `bat` | preview for non-jsonl hits | plain awk window with `▶` line marker |
| `jq` | (legacy fallback only — preview now renders in Go) | no longer required |

Tool filter notes:

- Match is case-insensitive. Names match Claude Code's tool catalog (below).
- `--tool` triggers per-line expansion in **script mode** too — output becomes `path:line  [role] excerpt ⟨tool ...⟩` per row, suitable for `xargs`/`awk`.
- `Edit` ≠ `Write`. Files Claude already touched come through as `Edit`. New files come through as `Write`. When unsure, pass both: `--tool Write,Edit` (add `MultiEdit` for batched edits).
- `Read` is hidden by default in session match output (list + preview). Pass `--include-read` to bring it back, or just include `Read` in your `--tool` list — that's interpreted as an explicit opt-in. Lines whose only content was a Read call are dropped entirely; lines with text + Read keep the text and strip the Read render.

### Tool catalog

| Category | Tool names | Notes |
|----------|------------|-------|
| File mutation | `Write`, `Edit`, `MultiEdit`, `NotebookEdit` | the 95% case for "find sessions where Claude wrote a file"; `MultiEdit` is rare (batched non-overlapping edits to one file) |
| File read | `Read` | **hidden by default** because Claude reads files constantly. Pass `--include-read` to see them, or include `Read` in `--tool` (auto-overrides). |
| Search | `Grep`, `Glob`, `LS` | input has `pattern` / `path`; renders as `⟨Grep /pattern/⟩` |
| Shell | `Bash`, `BashOutput`, `KillShell` | `Bash` input has `command`; renders as `⟨Bash $ cmd⟩` |
| Web | `WebFetch`, `WebSearch` | input has `url` or `query` |
| Agent / planning | `Agent` (Task), `TaskCreate`, `TaskUpdate`, `TaskList`, `TaskGet`, `TaskOutput`, `TaskStop`, `ExitPlanMode`, `AskUserQuestion` | sub-agent dispatch + plan mode controls |
| Skills / MCP | `Skill`, `mcp__<server>__<tool>`, `ToolSearch` | MCP tool names are namespaced (e.g. `mcp__plugin_kai_sourcebot__grep`); pass the full name |
| Misc | `ScheduleWakeup`, `Monitor`, `PushNotification`, `RemoteTrigger`, `EnterPlanMode`, `EnterWorktree`, `ExitWorktree`, `CronCreate`, `CronList`, `CronDelete` | rarely useful for content search |

For "Claude wrote/edited a markdown file mentioning X":

```
giantmem find "X" -s session --tool Write,Edit,MultiEdit --ext md
```

### File-extension filter (`--ext`)

| Flag | Description |
|------|-------------|
| `--ext md` | only matches where a `tool_use.input.file_path` ends in `.md` |
| `--ext md,go,py` | any of the listed extensions (case-insensitive, leading dot optional) |

Composes with `--tool` via AND — line must satisfy both filters. Useful for narrowing to docs (`md`), code (`go,py,ts`), config (`yml,toml`), tests (`py` then grep further), etc. Independent of `--tool`, but typically paired since `--ext` only meaningfully applies to file-touching tools.

## Live tail

| Command | What it does |
|---------|--------------|
| `giantmem tail` | stream new live workspace writes as the hook indexes them |
| `giantmem tail -p chat-orch` | filter by project (LIKE) |
| `giantmem tail -f better-search` | filter by active feature |
| `giantmem tail --since 10m` | start from 10 minutes ago instead of "now" |
| `giantmem tail --interval 500ms` | poll faster |

## Quick capture

| Command | What it does |
|---------|--------------|
| `giantmem capture "idea: ..."` | append timestamped block to active feature's `notes.md` (or `.giantmem/notes.md` if none) |
| `echo "..." \| giantmem capture` | reads from stdin |
| `giantmem capture -f better-search "spec: ..."` | force a specific feature |
| `giantmem capture -g "global note"` | force `.giantmem/notes.md` |

## Statusline snapshot

| Command | What it does |
|---------|--------------|
| `giantmem status` | human-readable snapshot for the current dir |
| `giantmem status --json` | JSON for scripts |
| `giantmem status --json --stale-days 30 --write-cache <path>` | atomically write JSON to a path; used by the statusline (cached 30s, fired in background) |

The Claude Code statusline (`~/.claude/hooks/statusline.js`) consumes the cached JSON to render: active feature name and live-docs-today count.

## Health audit

`giantmem doctor` walks the system and reports issues across worktrees, workspaces, archives, hooks, and DBs.

| Command | What it does |
|---------|--------------|
| `giantmem doctor` | full audit grouped by severity (errors, warnings, info). Non-zero exit if any error. |
| `giantmem doctor --json` | machine-readable findings + summary |
| `giantmem doctor --root PATH` | scan additional roots (default `~/dev`) |
| `giantmem doctor --stale-days 14` | adjust staleness threshold |
| `giantmem doctor --fix` | apply fixers for findings (rebind broken latest, ingest drifted projects, prune dead worktrees, etc.) |
| `giantmem doctor --fix --auto` | also auto-archive orphan `.giantmem/` dirs without prompting |
| `giantmem doctor --fix --fix-categories=symlink,drift` | restrict fixers to listed categories |
| `giantmem doctor --fix --fix-dry-run` | preview fix actions without changing anything |

## Per-workspace ignore

Drop a `.giantmem-ignore` at the root of any workspace (sibling to `.giantmem/`) to silence doctor for that dir. Directives:

- `# stale-ok` — workspace is intentionally inactive
- `# orphan-ok` — `.giantmem/` without `.git` ancestor is intentional

Global file at `~/.config/giantmem/global-ignore` applies system-wide.

## Resolved configuration

| Command | What it does |
|---------|--------------|
| `giantmem config` | show binary version, paths, db sizes/schemas, hook + MCP wiring, library locations |
| `giantmem config --json` | structured output for scripts |

## Shell completion

Generate completion scripts:

```
giantmem completion bash > ~/.bash_completion.d/giantmem
giantmem completion zsh > ~/.zfunc/_giantmem
```

Once installed, `--project` and `--feature` flags complete from indexed values; session id-prefixes complete from recent sessions; archive subcommand args complete from project dir names.

## Shell init: install

| Command | What it does |
|---------|--------------|
| `giantmem worktree shell-init` | print the snippet (source + `gj()`) |
| `giantmem worktree shell-init --install` | append/update sentinel-bracketed block in `~/.bashrc` or `~/.zshrc` (auto-detected) |
| `giantmem worktree shell-init --install --target ~/.zshrc` | force a specific target |
| `giantmem worktree shell-init --install --dry-run` | preview the change |

Categories detected: orphan worktrees, orphan `.giantmem/` dirs, broken `latest` symlinks, archives.db drift (project on disk but not indexed), stale workspaces, missing `live_index.py` hook entry, missing or stale `giantmem-search` MCP entry, DB integrity errors.

## SessionStart prime

Claude is auto-primed with workspace context on every session start. `~/.claude/hooks/session_prime.py` calls `giantmem prime --json` for the project dir and injects a `<system-reminder>` containing: active feature, recent live writes, recent sessions, history tail. Visible to Claude only.

| Command | What it does |
|---------|--------------|
| `giantmem prime` | preview the primer in plain text |
| `giantmem prime --json` | JSON form (used by the hook) |
| `giantmem prime <path>` | prime for a path other than `cwd` |

## Fuzzy worktree jump

| Command | What it does |
|---------|--------------|
| `giantmem cd <pattern>` | print best-match worktree path |
| `gj <pattern>` | shell wrapper that cd's to the printed path (defined by `giantmem worktree shell-init`) |
| `giantmem cd --refresh` | rebuild the worktree cache (`~/.cache/giantmem/worktrees.json`, auto-rebuilt every 6h) |
| `giantmem cd --no-fzf` | print all matches instead of opening fzf |

Match priority: exact basename, project, branch, then substring of `project/branch`, branch, project, basename in that order.

## MCP server

`giantmem mcp serve` is the stdio MCP server wired into `~/.claude/settings.json` as `giantmem-search`. Six read-only tools let a Claude session find prior docs, prior conversations, and current workspace state without leaving the editor.

### Wiring

```jsonc
// ~/.claude/settings.json
{
  "mcpServers": {
    "giantmem-search": {
      "command": "giantmem",
      "args": ["mcp", "serve"]
    }
  }
}
```

`giantmem doctor --fix` will install or repair this entry automatically.

### Tools

| Tool | Purpose |
|------|---------|
| `search_archive` | FTS5 over archived workspaces + Claude session JSONL + domain knowledge. **Per-line decoded results** when `tool_filter` or `ext_filter` set. |
| `list_sessions` | recent Claude sessions, newest first |
| `get_session_summary` | metadata for one session by id-prefix |
| `recent_writes` | live `.giantmem/*.md` writes in a time window (`24h`, `7d`, ...) |
| `feature_status` | `features.json` per project |
| `workspace_tree` | `.giantmem/` dir-type counts (from live.db or on-disk) |

### `search_archive` parameters

| Param | Type | Notes |
|-------|------|-------|
| `query` | string, required | plain text auto-quoted (handles `hub-and-spoke`); FTS5 syntax (`AND`, `OR`, `NOT`, `"phrase"`, `prefix*`) passes through; wrap your own `"exact phrase"` for literal substring |
| `project` | string | LIKE filter — `chat-orchestrator` matches `dev/ai/chat-orchestrator` |
| `source_type` | string | `workspace` / `session` / `domain` |
| `topic` | string | session topic (`auth`, `api`, `bug`, `feature`, ...) |
| `tool_filter` | string | comma-separated Claude tool names (`Write,Edit,MultiEdit`); session-only; **triggers per-line expansion** |
| `ext_filter` | string | comma-separated file extensions (`md,go`); session-only; composes with `tool_filter` via AND; **triggers per-line expansion** |
| `include_read` | bool | surface `Read` tool calls (hidden by default); auto-enabled when `Read` is in `tool_filter` |
| `limit` | number | max results, default 10, cap 100 |

### Result shapes

**Default (file-level)** — when no per-line filter is set:

```json
{
  "results": [
    {
      "filepath": "...",
      "project": "dev/ai/chat-orchestrator",
      "source_type": "session",
      "session_id": "40503b40-...",
      "timestamp": "20260425_162607",
      "score": 4.67,
      "snippet": "...>>>hub-and-spoke<<<..."
    }
  ],
  "total": 1
}
```

**Per-line (decoded)** — when `tool_filter` or `ext_filter` is set; session hits get expanded via ripgrep, decoded, filtered:

```json
{
  "per_line": true,
  "applied_filters": {"tools": ["Write","Edit","MultiEdit"], "exts": ["md"], "include_read": false},
  "sanitized_query": "\"hub-and-spoke\"",
  "results": [
    {
      "filepath": ".../40503b40-....jsonl",
      "project": "dev/ai/chat-orchestrator",
      "session_id": "40503b40-...",
      "timestamp": "20260425_162607",
      "line": 481,
      "role": "assistant",
      "tools": ["Edit"],
      "files": ["/Users/bryan/dev/.../customcheckout_service_auth_unify_design.md"],
      "excerpt": "[assistant]  ⟨Edit /Users/.../design.md⟩"
    }
  ],
  "total": 1
}
```

### Common call patterns

| Goal | Args |
|------|------|
| "Find any older doc mentioning hub-and-spoke" | `{"query": "hub-and-spoke"}` |
| "Sessions in chat-orchestrator that touched JWT" | `{"query": "JWT", "project": "chat-orchestrator", "source_type": "session"}` |
| "What .md did Claude author about X" | `{"query": "X", "source_type": "session", "tool_filter": "Write,Edit,MultiEdit", "ext_filter": "md"}` |
| "What Bash commands related to migrations ran" | `{"query": "migration", "source_type": "session", "tool_filter": "Bash"}` |
| "Recent live workspace writes" | (use `recent_writes` instead — it's the right tool) |

### Tips for Claude callers

- Default to no `tool_filter` / `ext_filter` for breadth, narrow only when first-pass results are noisy.
- `Read` is hidden by default — most matches inside Read tool inputs are file-content noise. Pass `include_read: true` only when you specifically need to know what Claude opened.
- `limit` returns up to 100. Per-line expansion can produce many rows per file — increase `limit` if filters are tight.
- The MCP server is read-only and idempotent. Safe to retry; safe to call in parallel.

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


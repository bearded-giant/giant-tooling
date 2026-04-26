# giant-tooling

Workspace, worktree, and giantmem (search + archive + sessions) tooling for Claude Code development workflows.

These tools were extracted from a private utility repo where they evolved over months of daily use. The commit history starts fresh here, but the code is battle-tested.

## Philosophy

This repo is opinionated and bespoke -- it codifies a specific way of working with Claude Code, not a general-purpose toolkit. Reading the code straight through is the fastest way to understand it. A few load-bearing choices shape everything else:

Each project gets its own `.giantmem/` workspace dir. Plans, research, feature specs, session history, and discoveries live there alongside the code. Claude Code reads it on session start and writes back on session end via hooks. Nothing about your work lives in the chat transcript -- it lives in files you can grep, diff, and version. When a session ends or context compacts, the next session picks up exactly where the last one left off.

Worktrees are throwaway. Spin one up per feature, branch, or experiment. Kill it when done. `.giantmem/` archives to `~/giantmem_archive/` on removal so context is never lost. A bare repo with sibling worktrees keeps git data in one fixed spot, and per-project prefix functions (`{prefix}`, `{prefix}l`, etc.) replace muscle-heavy git invocations with two-key moves.

The archive is searchable. Every removed worktree's `.giantmem/` lands in `~/giantmem_archive/` and gets indexed into a SQLite FTS5 database. Past plans, research, and discoveries stay queryable across all projects forever, with temporal decay so newer hits rank higher.

Stdlib-only where it makes sense. `workspace/` and `git-worktrees/` are Bash + Python (stdlib, no pip deps). `giantmem/` is Go — single static binary, modernc.org/sqlite for FTS5, BurntSushi/toml for source plugin config. All hooks are Python stdlib and wrap their main in try/except so a broken hook never breaks a session; failures land in `~/.cache/giantmem/hook.log`. Shell scripts use `set -euo pipefail`. Comments are lowercase. The `giantmemd` daemon is opt-in (auto-routed when its socket is alive, easy to bypass with `--no-daemon` or `GIANTMEM_NO_DAEMON=1`); it caches DB handles to cut ~700ms of cold start per CLI invocation. If something breaks, the call graph is small and the fix is usually obvious.

The whole thing is meant to be forked, edited, and tweaked to your own bespoke workflow. The defaults reflect one author's working style; your mileage will vary. Read the subsystem READMEs (linked below) for the actual command reference and setup walkthroughs.

## What's in here

### workspace/

Manages `.giantmem/` directories that live inside any project repo. The .giantmem dir is a structured workspace for plans, features, research, context, and session history that Claude Code reads and writes during sessions.

The system has two parts. `workspace-lib.sh` provides shell functions (`ws`, `wsb`, `wst`, `wsa`, etc.) for creating, migrating, and inspecting .giantmem dirs from your terminal. Two Python hooks integrate with Claude Code directly: `workspace_session_hook.py` runs at session start to bootstrap .giantmem/ and inject context, and `workspace_session_end.py` parses the JSONL transcript at session end to extract discoveries and create session summaries.

Also includes feature tracking -- `.giantmem/features/` directories with specs, facts, and metadata that persist across sessions, plus a migration tool (`workspace-migrate-features.py`) for converting legacy plan files into the feature structure.

See [workspace/README.md](workspace/README.md) for shell and Claude commands, directory structure, and feature workflow. Design docs in [workspace/docs/](workspace/docs/).

### git-worktrees/

Shared library (`worktree-core.sh`) plus per-project config files (`wt-{name}.sh`) for managing git worktrees. You source `worktree-core.sh` once, then run `wt_init` to scaffold a new project config or `wt_adopt` to convert an existing repo into the bare-plus-worktree layout in place. Each project gets prefix-style shell functions: `{prefix}` (switch/create worktree), `{prefix}l` (list), `{prefix}r` (remove with `.giantmem/` backup), and a dozen more.

The bare repo lives at `{base}/.bare` with worktrees as siblings. New worktrees auto-bootstrap `.giantmem/`, removed ones back up `.giantmem/` to `~/giantmem_archive/`. Stack-specific setup for python/node/lua/bash.

See [git-worktrees/README.md](git-worktrees/README.md) for the full command reference, the `wt_init` and `wt_adopt` flows, and directory layout.

### giantmem/

Go CLI that unifies search, archive, sessions, worktree, workspace, and ingest under one binary (`giantmem`). Replaced the prior bash + python `giantmem-archive/` scripts. Key surfaces: `giantmem find` (FTS5 across live + archive + Claude session transcripts, ranked and merged); `giantmem session list|find|resume|export|diff`; `giantmem archive run|list|stale`; `giantmem worktree`, `giantmem workspace`, and `giantmem doctor [--fix]`; plus `giantmem mcp serve` exposing six tools so Claude can self-discover state.

Storage is two SQLite FTS5 databases (`archives.db` for archived workspaces + Claude sessions + flattened domain JSONs, `live.db` for in-flight `.giantmem/` files). Schema is versioned via `PRAGMA user_version` and migrated on open. Ingest is plugin-driven via `~/.config/giantmem/sources.toml` — the three builtins (`workspace-md`, `claude-jsonl`, `domain-json`) ship enabled by default; external sources are arbitrary subprocesses that emit JSONL on stdout with a small field-mapping table.

Optional companion daemon `giantmemd` (started via `giantmem daemon start`, installable as a launchd LaunchAgent on macOS) serves a JSON-RPC 2.0 unix socket at `~/.cache/giantmem/giantmemd.sock`. The CLI auto-routes through it when alive and falls back to direct DB open otherwise. Schema-drift after a migration returns a "restart pending" error so the daemon never serves stale views.

See [giantmem/README.md](giantmem/README.md) for capabilities, [giantmem/USAGE.md](giantmem/USAGE.md) for the daily-driver cheat sheet, and [giantmem/PLAN.md](giantmem/PLAN.md) + [giantmem/PLAN-2.md](giantmem/PLAN-2.md) for the roadmap and shipped phases.

### domain-search/

Standalone CLI (`domains`) for browsing and searching the code domain knowledge base outside of Claude Code. Domain JSONs are structured explorations of code areas (auth layer, payment flow, etc.) created by `/plan-feature` inside Claude Code sessions. This tool lets you list, show, search, and export them from any terminal.

Commands: `list` (show all domains with staleness), `show` (pretty-print a domain), `search` (keyword search across live workspace domains), `archive` (search the FTS5 database across all projects and history), `export` (dump as shareable markdown), `fzf` (interactive picker with preview).

See [domain-search/usage.md](domain-search/usage.md) for the full command reference and workflow examples.

## Setup

Set the env var (optional, defaults to `$HOME/dev/giant-tooling`):

```bash
export GIANT_TOOLING_DIR="$HOME/dev/giant-tooling"
```

Source the workspace library in your shell config:

```bash
source ~/dev/giant-tooling/workspace/workspace-lib.sh
```

Build and install the `giantmem` CLI (`~/.local/bin` must be on `$PATH`):

```bash
cd ~/dev/giant-tooling/giantmem && make install
```

Optional: start the long-running daemon for warm DB handles. Auto-routed when alive; opt-out with `--no-daemon` or `GIANTMEM_NO_DAEMON=1`.

```bash
giantmem daemon start                         # detached, pidfile + log under ~/.cache/giantmem/
giantmem daemon install                       # macOS: launchd LaunchAgent that survives logout
```

Optional: register external ingest sources by dropping a `[[source]]` block into `~/.config/giantmem/sources.toml` (see [giantmem/PLAN-2.md](giantmem/PLAN-2.md) §12 for the schema).

Source the worktree library, then create or adopt projects:

```bash
source ~/dev/giant-tooling/git-worktrees/worktree-core.sh
wt_init                              # wizard for a fresh clone
wt_adopt /path/to/existing/repo      # convert an existing clone in place
```

Make the domain search CLI available:

```bash
alias domains='~/dev/giant-tooling/domain-search/domains'
```

## Conventions

Python scripts use stdlib only (no pip dependencies). Go code in `giantmem/` keeps its dependency surface tight (cobra, modernc.org/sqlite, BurntSushi/toml, mark3labs/mcp-go) and ships as one static binary. Shell scripts target bash with `set -euo pipefail`. Hook scripts never crash — exceptions are caught and appended to `~/.cache/giantmem/hook.log` so they don't break Claude Code sessions.

## License

MIT. See [LICENSE](LICENSE).

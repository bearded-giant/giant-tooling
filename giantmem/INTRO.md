# giantmem in five minutes

This is the IC-facing intro. If you have never used `giantmem` before, read this first. The full reference is in [USAGE.md](USAGE.md).

## What it is

`giantmem` is a CLI that sits on top of git and Claude Code. It manages three layers, and you do not have to think about which layer you are using day-to-day. They stack:

```
[ memory ]   indexed search across docs + Claude session transcripts
   ^
[ workspace ]   per-branch context dir (.giantmem/) inside each worktree
   ^
[ worktree ]    bare repo + sibling worktrees, one per branch
```

Worktrees are a git layout. Workspaces are a directory of markdown plus a few JSON files. Memory is a SQLite FTS5 index plus a Claude Code hook. The dependency only goes up: a worktree does not know workspaces exist; a workspace does not know about the memory index. `giantmem` gives all three a single front door.

## The five commands you will actually use

```
giantmem find <query>            # search every workspace + archive + Claude session
giantmem session resume <id>     # jump back into a buried Claude session
giantmem worktree list           # what worktrees exist, which have a live workspace
giantmem worktree remove <path>  # archive the workspace then delete the worktree
giantmem archive stale --days 30 # find dormant workspaces to clean up
```

Type `giantmem --help` to see the rest. Every subcommand supports `-h`.

## Setup for a new IC

1. Install: `cd ~/dev/giant-tooling/gm && make install`. Binary lands in `~/.local/bin/giantmem`.
2. Source the worktree shell library so the per-project shortcut functions (e.g. `cwt`, `cwtl`) bind in every shell. Print the line: `giantmem worktree shell-init`. Add the printed `source ...` line to your `.bashrc` / `.zshrc`.
3. Optional: shell completion. `giantmem completion bash > ~/.bash_completion.d/giantmem`.

## Per-project setup

Each repo you want to use as bare-with-worktrees gets a one-time wizard:

```
giantmem worktree init      # greenfield clone (asks for project name, prefix, base dir, ...)
giantmem worktree adopt     # converts an existing clone in place (preserves WIP)
```

The wizard writes a `wt-{name}.sh` config beside `worktree-core.sh`. Source it in your shell rc. Once sourced, you have prefix shortcuts:

```
{prefix} <branch>      # switch to or create a worktree
{prefix}l              # list worktrees
{prefix}r <branch>     # remove worktree (auto-archives .giantmem)
```

Pick `{prefix}` once and live with it. Three keystrokes is the daily driver. `giantmem worktree ...` is for setup, reporting, and cross-cutting ops.

## Day in the life

You sit down to work on a feature. You type `mywt my-branch` and get dropped into a worktree. You run Claude Code there. Claude writes a few markdown files into `.giantmem/research/` and `.giantmem/plans/`. The Claude PostToolUse hook indexes each write into `~/giantmem_archive/live.db` instantly. You run `giantmem find "thing I half remember"` from anywhere on your machine and get a ranked list of every doc, archived doc, and Claude transcript that mentions it. You pick a session, run `giantmem session resume <id-prefix>`, and the CLI chdirs to the recorded cwd and runs `claude --resume <uuid>`. Nothing manual to track or migrate.

When the feature is done you run `mywtr my-branch`, which calls `giantmem worktree remove` under the hood. The `.giantmem/` directory is moved into `~/giantmem_archive/{project}/{timestamp}/` (and `archives.db` gets the rows), then the worktree is deleted. The docs are still searchable forever.

## Where the pieces live

| Concern | Lives at | Owner |
|---------|----------|-------|
| Worktree shell library | `~/dev/giant-tooling/git-worktrees/worktree-core.sh` | git-worktrees |
| Per-project worktree configs | `~/dev/giant-tooling/git-worktrees/wt-*.sh` | git-worktrees |
| Workspace shell library | `~/dev/giant-tooling/workspace/workspace-lib.sh` | workspace |
| Live + archive databases | `~/giantmem_archive/{live,archives}.db` | giantmem |
| Live indexing hook | `~/.claude/hooks/live_index.py` | giantmem |
| `giantmem` CLI | `~/dev/giant-tooling/giantmem/` (binary at `~/.local/bin/giantmem`) | giantmem |

The three repos are intentionally separate so each can change without dragging the others. `giantmem` wraps the other two via subprocess; nothing is statically linked. You can use the worktree library without `giantmem` (manual git fallback) and you can use `giantmem` without the workspace lib (search still works, just no `giantmem workspace`).

## Common confusions

The `.giantmem/` directory inside a worktree is the **workspace**. It holds plans, research, reviews, etc. Do not confuse it with `~/giantmem_archive/`, which is the **archive root** holding moved-out workspaces and the SQLite databases.

Per-project shortcuts (`cwt`, `cwtl`, ...) are shell functions sourced from `wt-{prefix}.sh`. They cannot be replaced by the Go binary because they `cd`. Keep them in your shell rc. `giantmem worktree` exists for setup, reporting, and lifecycle, not for the daily branch-switch flow.

`giantmem find` queries both `live.db` and `archives.db` by default. Use `--live` or `--archive` to scope. Sessions live in `archives.db`; pass `--source session` to filter to Claude transcripts only.

## What to read next

- [USAGE.md](USAGE.md) — full command reference, workflows, troubleshooting
- [README.md](README.md) — install, status, layout
- `giantmem --help` — discoverable everything, walkable from the prompt

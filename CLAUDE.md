# CLAUDE.md

Guidance for AI coding assistants (Claude Code, Cursor, Aider, etc.) working in this repository. `AGENTS.md` is a symlink to this file for cross-tool support.

## Overview

giant-tooling is a collection of shell and Python utilities for Claude Code development workflows. Three subsystems:

1. **workspace/** -- `.giantmem/` directory lifecycle, session hooks, feature tracking
2. **git-worktrees/** -- worktree helper generator that creates per-project shell functions
3. **giantmem-archive/** -- archive and FTS5 search system for `.giantmem/` directories

All scripts use only Python stdlib (no pip dependencies). Shell scripts target bash.

## Architecture

### Workspace System

`workspace-lib.sh` is the core library. It provides shell functions (`workspace_init`, `workspace_bootstrap`, `workspace_migrate`, `workspace_migrate_dir`, etc.) that manage `.giantmem/` directories in any project. Two Python hooks integrate with Claude Code:

- `workspace_session_hook.py` (SessionStart) -- bootstraps `.giantmem/` if missing, injects WORKSPACE.md + recent discoveries into session context
- `workspace_session_end.py` (SessionEnd) -- parses JSONL transcript, extracts discoveries/plans via regex, creates session summary files in `.giantmem/history/sessions/`

The hooks read JSON from stdin and write to stdout/stderr. They call `workspace-lib.sh` functions via subprocess for bootstrapping.

`list-features.sh` renders a formatted table from `.giantmem/features/` directories by reading `spec.md` and `meta.json` from each feature folder.

`workspace-migrate-features.py` converts legacy `.giantmem/plans/` files into the `.giantmem/features/{name}/` structure with `spec.md`, `facts.md`, and `meta.json`.

**Auto-migration:** `workspace_migrate_dir()` detects legacy `scratch/` directories and migrates them: moves to `.giantmem/`, creates a backward-compat symlink (`scratch -> .giantmem`), and updates `.gitignore`. This runs automatically on `workspace_bootstrap` and `workspace_init`.

### Worktree Library

`worktree-core.sh` is the shared library. Two entry points create projects:

- `wt_init` -- wizard that prompts for project name, prefix, base dir, stack, default branches, env files, etc. Writes a `wt-{name}.sh` config file beside core, sources it, calls `wt_register {prefix}` to bind the prefix shell functions. Use this for greenfield projects (clone fresh from URL or repo path you haven't touched).
- `wt_adopt [path]` -- converts an existing non-bare repo into the layout in place. Moves `<repo>/.git` to `<repo>-wt/.bare`, moves the working tree to `<repo>-wt/<branch>/`, manually wires worktree metadata so WIP and untracked files survive. Errors on already-bare repos, detached HEAD, submodules, linked worktrees, or pre-existing target. Run `wt_init` after to bind prefix functions; the wizard detects existing `.bare` and tells you to skip the `{prefix}_init` step.

`wt_register {prefix}` (called from generated configs) binds the per-project shell functions: `{prefix}` (switch/create worktree), `{prefix}l` (list), `{prefix}a` (add), `{prefix}r` (remove with `.giantmem/` backup), `{prefix}p`/`{prefix}pr` (pull), `{prefix}f` (fetch), `{prefix}c` (copy bootstrap files), `{prefix}prune`, `{prefix}repair`, `{prefix}sl`/`{prefix}sb`/`{prefix}so` (workspace archive ops), `{prefix}_init` (bare clone, no-op if `.bare` exists). Stack-specific setup runs on worktree create (python/node/lua/bash).

Generated configs source `worktree-core.sh` via the relative path `${BASH_SOURCE[0]%/*}/worktree-core.sh`. To keep configs in a separate dir (e.g. private dotfiles), symlink `worktree-core.sh` into that dir; configs sourced through the symlink resolve `${BASH_SOURCE[0]}` to the symlink path, so the wizard writes new configs there rather than in this repo.

### Giantmem Archive

`giantmem-archive.sh` handles archiving `.giantmem/` directories to `~/giantmem_archive/{project}/{timestamp}/`. It builds `.giantmem-index` files via ripgrep and delegates FTS5 search to `giantmem-search.py`.

`giantmem-search.py` maintains a SQLite FTS5 database (`~/giantmem_archive/archives.db`). Commands: `ingest` (rebuild DB from file tree), `search` (FTS5 query with fzf picker + bat preview), `stats` (indexed doc counts). Search results show ranked matches with project/timestamp/type metadata and temporal decay (newer results rank higher).

## Key Paths and Environment

- `$GIANT_TOOLING_DIR` -- root of this repo (defaults to `$HOME/dev/giant-tooling`)
- `$GIANTMEM_ARCHIVE_BASE` -- archive location (defaults to `$HOME/giantmem_archive`)
- `worktree-core.sh` lives in `git-worktrees/`. Per-project `wt-{name}.sh` configs land beside it by default, or in a separate dir if a user symlinks `worktree-core.sh` there.

## Conventions

- Shell scripts use `set -euo pipefail`
- Python scripts use `#!/usr/bin/env python3` and stdlib only
- Hook scripts never crash -- all exceptions are silently caught
- Comments are lowercase
- No external Python packages

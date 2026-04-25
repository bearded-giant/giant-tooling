# worktrees

Per-project git worktree management. One shared core (`worktree-core.sh`) plus a small config file per project (`wt-{name}.sh`) that binds prefix-style shell functions, e.g. `myprjwt`, `myprjwtl`, `myprjwtr`.

## Quick start

```bash
# 1. Clone giant-tooling somewhere, e.g.
git clone <repo-url> ~/dev/giant-tooling

# 2. Bootstrap (add to .bashrc / .zshrc)
source ~/dev/giant-tooling/git-worktrees/worktree-core.sh

# 3. Create a project (pick one)
wt_init                              # wizard for a fresh clone
wt_adopt /path/to/existing/repo      # convert an existing clone in place

# 4. Use the prefix functions the wizard binds
{prefix} <branch>                    # switch to or create a worktree
{prefix}l                            # list worktrees
{prefix}r <branch>                   # remove (with .giantmem backup)
```

After step 2, `wt_init`, `wt_adopt`, and `wt_projects` exist in every shell. Run `wt_init` once per project. The wizard writes a `wt-{name}.sh` config file beside `worktree-core.sh`. To keep configs in a separate (e.g. private) dir, see [Install](#install) below.

## Philosophy

The whole thing is opinionated and bespoke. A few load-bearing choices that drive everything else:

The bare repo lives at `{base}/.bare` and worktrees are siblings, not children. This keeps the canonical git data in one fixed spot and lets each branch have its own checkout dir without the usual nested-`.git` confusion. Cost: the layout is non-standard, so `cd repo && git status` only works inside a worktree.

Per-project prefix functions (`cwt`, `awt`, etc.) are how you actually use this. `wt_register` binds twenty-odd functions per prefix using `eval`, then your muscle memory does the rest. Two-key moves replace ten-key git invocations. The wizard exists because writing a config by hand is friction nobody needs.

Worktrees are throwaway. Spin one up for a feature, do the work, kill the worktree when done, never touch git directly. Removal backs `.giantmem/` up to `~/giantmem_archive/` first, so context isn't lost. Stack-aware setup (python/node/lua/bash) runs on create so you don't re-pin versions per worktree.

`wt_init` is for greenfield (clone fresh). `wt_adopt` is for "I already have a working clone with WIP I don't want to lose" -- it converts the existing repo in place, preserving uncommitted/untracked files. The two flows exist because the cost of getting either one wrong is real lost work, and most "convert to worktree" advice on the internet drops your WIP on the floor.

`worktree-core.sh` is bash-only, no external deps, no config files outside of the per-project shell scripts it generates. You can read the whole thing in one sitting. If something breaks, the call graph is small and obvious.

## Files

| File | Purpose |
|------|---------|
| `worktree-core.sh` | Shared library. Defines `wt_init`, `wt_adopt`, `wt_register`, plus all `__wt_*` helpers used by per-project prefixes. |
| `wt-{name}.sh` | Per-project config (one file per project). Sets env vars (base dir, stack, default branches, etc.) and calls `wt_register {prefix}` to bind shell functions. Source it from your shell rc, or auto-load via your dotfiles. |

## Layout

Each project lives in its own base directory with a bare repo and worktree siblings:

```
~/dev/{name}-wt/
  .bare/              git bare repo (origin lives here)
  main/               worktree for main branch
  feature-x/          worktree for feature branch
  wt-bootstrap/       shared files copied into new worktrees (.env, etc.)
```

## Two ways to start a project

### 1. Greenfield: `wt_init` then `{prefix}_init`

Use this when starting from scratch with a remote URL or a repo path you haven't touched yet.

```bash
wt_init
# Wizard prompts: project name, prefix, base dir, stack,
# default branch order, env files, archive name, version file, etc.
# Creates wt-{name}.sh and sources it.

{prefix}_init git@github.com:org/repo.git
# Clones bare into $base/.bare, fetches, creates first worktree
# at $base/{default-branch}/.
```

### 2. Adopt: `wt_adopt` then `wt_init`

Use this when you already have a working clone with WIP, untracked files, or local-only branches you want to keep. Converts the existing repo in place.

```bash
cd ~/dev/myrepo
wt_adopt
# Confirms, then:
#   moves .git -> ~/dev/myrepo-wt/.bare (sets core.bare=true)
#   moves working tree -> ~/dev/myrepo-wt/{current-branch}/
#   wires worktree metadata manually so WIP/untracked survive
# Original ~/dev/myrepo is gone after this.

wt_init
# Wizard. Set base dir to ~/dev/myrepo-wt when prompted.
# Detects existing .bare and tells you to SKIP {prefix}_init.
```

`wt_adopt` errors out on: not a git repo, already bare, detached HEAD, submodules present (unsupported), linked worktree (`.git` is a file), or target `{name}-wt` already exists.

## Generated commands per prefix

After `wt_register {prefix}` runs (from a sourced `wt-*.sh`), these functions exist:

| Command | What |
|---------|------|
| `{prefix} <branch>` | switch to worktree, or create if missing |
| `{prefix}l` | list worktrees |
| `{prefix}b` | list branches |
| `{prefix}s` | status across worktrees |
| `{prefix}a <branch>` | add worktree explicitly |
| `{prefix}r <branch>` | remove worktree (backs up `.giantmem/` to `~/giantmem_archive/`) |
| `{prefix}rn <old> <new>` | rename worktree |
| `{prefix}p` / `{prefix}pr` | pull / pull --rebase |
| `{prefix}f` | fetch |
| `{prefix}c <src> <dst>` | copy bootstrap files between worktrees |
| `{prefix}prune` | git worktree prune |
| `{prefix}repair` | git worktree repair |
| `{prefix}sl` / `{prefix}sb` / `{prefix}so` | workspace archive list / backup / open |
| `{prefix}_init <src>` | bare clone init (no-op if `.bare` exists) |

Workspace aliases (when `WS_BASE` set in config): `{ws}`, `{ws}tree`, `{ws}sync`, `{ws}discover`, `{ws}complete`.

`wt_projects` lists all registered prefixes with their base dirs and archive names.

## Install

1. Clone this repo (or pin its path via `$GIANT_TOOLING_DIR`).
2. Source `git-worktrees/worktree-core.sh` from your shell rc. After this `wt_init`, `wt_adopt`, and `wt_projects` are available globally.
3. Source any `wt-{name}.sh` configs you have, or set up auto-loading from a dir of your choice.

Configs source `worktree-core.sh` via `${BASH_SOURCE[0]%/*}/worktree-core.sh`, so keep them beside core.sh or symlink core.sh into your config dir. The symlink trick lets you keep configs in private dotfiles while pulling core from this repo: `ln -s /path/to/giant-tooling/git-worktrees/worktree-core.sh ~/dotfiles/worktrees/worktree-core.sh`. When the wizard runs through that symlink, `${BASH_SOURCE[0]}` resolves to the symlink path, so new configs land in the dotfiles dir, not in this repo.

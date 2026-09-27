# Workspace System

Context management for Claude Code sessions. Works with git worktrees or standalone in any project. Everything lives in a `.giantmem/` directory at the project root.

## Quick Start

### Worktree projects

Nothing to do. `{prefix} <branch>` (from `git-worktrees/worktree-core.sh`) creates the worktree, runs `workspace_init`, and scaffolds a feature named after the branch. `{prefix}r <branch>` sweeps `.giantmem/` into `live.db` before removing the worktree.

```bash
{prefix} feature-xyz         # worktree + .giantmem/ + features/feature-xyz/
# ... work ...
{prefix}r feature-xyz        # sweep into live.db, remove worktree
```

### Ad-hoc projects

Pick one:

```bash
cd ~/projects/some-project
workspace_init               # after sourcing workspace-lib.sh
giantmem workspace init      # same thing via the Go CLI
```

Inside Claude, `/ws-init` runs `workspace_bootstrap` and fills in `WORKSPACE.md`.

## Setup

Source the library from your shell rc:

```bash
export GIANT_TOOLING_DIR="$HOME/<your-checkout>/giant-tooling"
source "$GIANT_TOOLING_DIR/workspace/workspace-lib.sh"
```

The library defines functions only. If you want short names, add your own aliases:

```bash
alias ws='workspace_status'
alias wsb='workspace_bootstrap'
alias wsm='workspace_migrate'
alias wsd='workspace_discover'
alias wsc='workspace_complete'
alias wssync='workspace_sync'
alias wsf='workspace_features'
wsi() { workspace_init "$PWD" "${1:-$(basename "$PWD")}"; }
```

The rest of this doc uses the full function names.

## Directory Structure

`workspace_init` creates this:

```
project/
└── .giantmem/
    ├── WORKSPACE.md          # branch/project purpose, status
    ├── notes.md              # freeform notes
    ├── features/
    │   ├── _index.md         # feature table, maintained by feature.py
    │   ├── features.json     # status cache, authoritative "active feature" signal
    │   └── {name}/           # one dir per feature (see workspace/README.md)
    ├── specs/
    │   ├── _index.md         # source-of-truth spec registry
    │   ├── _history.md       # append-only merge log
    │   └── {domain}/spec.md  # merged specs, written by /complete-feature
    ├── context/
    │   ├── discoveries.md    # codebase learnings
    │   └── git-log.md        # recent commits (workspace_gitlog)
    ├── plans/
    │   └── current.md        # active plan
    ├── history/
    │   ├── sessions.md       # one line per session
    │   └── sessions/         # one file per session (SessionEnd hook)
    ├── research/
    ├── reviews/
    └── filebox/              # scratch files, samples, exports
```

`artifacts.json` appears at the root once `giantmem artifact reindex` has run; the SessionStart hook reads it for the artifacts summary.

## Claude Code Integration

Two hooks bridge the shell library and Claude Code:

| Event | Hook | Action |
|-------|------|--------|
| SessionStart | `workspace_session_hook.py` | Bootstrap `.giantmem/` if missing, inject context |
| SessionEnd | `workspace_session_end.py` | Write a session file, extract discoveries and plans |

Session start injects, in order: `WORKSPACE.md`, the three most recent session summaries, an artifacts summary from `artifacts.json`, `plans/current.md`, and the last 20 lines of `context/discoveries.md`.

Session end parses the transcript JSONL, writes `history/sessions/{timestamp}_{id}.md`, appends a line to `history/sessions.md`, appends pattern-matched discoveries to `context/discoveries.md`, and writes extracted steps to `plans/current.md` (appending when the file changed within the last hour).

Hook wiring lives in the claude-code-config repo (`hooks/dispatch.py` chains them from `~/.claude/hooks/`), and that repo carries its own copies of both hook files. See `workspace-hooks.md` for the versions in this repo.

## Shell Functions

| Function | Description |
|----------|-------------|
| `workspace_init [dir] [name]` | Create the structure above. Migrates a legacy `scratch/` dir first |
| `workspace_bootstrap` | Smart init: create, migrate loose files, or just sync (use mid-session) |
| `workspace_migrate` | Move loose `.giantmem/*.md` files into subdirs by name and content |
| `workspace_status` | Show `WORKSPACE.md` head, file counts per subdir, last 5 discoveries |
| `workspace_discover "note"` | Append a timestamped line to `context/discoveries.md` |
| `workspace_session_note [note]` | Append a session marker or note to `history/sessions.md` |
| `workspace_complete` | Flip `WORKSPACE.md` status to complete |
| `workspace_sync` | Refresh `context/git-log.md` when inside a git repo |
| `workspace_gitlog` | Write the last 20 commits to `context/git-log.md` |
| `workspace_features` | Print `features/_index.md` |
| `workspace_new_feature <name> [flags]` | Scaffold a feature via `scripts/feature.py new` |
| `workspace_start_feature [name]` | Promote pending to in_progress |
| `workspace_pause_feature [name]` | Pause the active or named feature |
| `workspace_reopen_feature [name]` | Reopen a paused or complete feature |
| `workspace_complete_feature [name]` | Mark complete and merge delta-specs |
| `list-features [--dir <path>] [--all]` | Feature status table from `features.json` |
| `workspace_archive [src] [project]` | Legacy snapshot mover, see workspace/README.md |
| `workspace_archive_list [project]` | List legacy snapshot dirs |
| `workspace_archive_open <project> [branch] [ts]` | Open a legacy snapshot dir in Finder |

`giantmem workspace <cmd>` mirrors the first nine (init, bootstrap, migrate, status, discover, note, complete, sync, gitlog) plus `archive`, and `giantmem feature <cmd>` mirrors the feature verbs. Use whichever is on your PATH.

### Mid-Session Bootstrap

Already in a Claude session and want to start using the workspace? Run `workspace_bootstrap`:

1. No `.giantmem/`: creates the full structure.
2. `.giantmem/` present but no `WORKSPACE.md`: migrates loose files into subdirs and creates `WORKSPACE.md`.
3. Already structured: refreshes `git-log.md` and prints status.

### Migration Logic

`workspace_migrate` looks at each file directly under `.giantmem/` (skipping `WORKSPACE.md` and `notes.md`) and moves it:

| Pattern | Destination |
|---------|-------------|
| `*plan*.md`, `*todo*.md`, `*steps*.md`, `*implementation*.md` | `plans/` |
| `*discover*.md`, `*finding*.md`, `*learn*.md`, `*context*.md` | `context/` |
| `*history*.md`, `*session*.md`, `*log*.md`, `*journal*.md` | `history/` |
| `*research*.md`, `*notes*.md`, `*reference*.md` | `research/` |
| `*review*.md`, `*feedback*.md` | `reviews/` |
| `git-log.md` | `context/` |
| other `.md` | `plans/` if the body mentions plan/step/todo/implement, `context/` if it mentions discover/found/learned/architecture/pattern, else `filebox/` |
| non-markdown | `filebox/` |

## Claude Commands

Slash commands that operate on `.giantmem/` (defined in the claude-code-config repo):

| Command | Purpose |
|---------|---------|
| `/ws-init` | Bootstrap the workspace and fill in `WORKSPACE.md` |
| `/new-feature <name>` | Scaffold a feature folder (`--builds-on <parent>` optional) |
| `/start-feature`, `/pause-feature`, `/reopen-feature` | Feature status transitions |
| `/complete-feature`, `/abandon-feature` | Close a feature; complete merges delta-specs |
| `/plan-feature` | Explore touched code and draft the implementation plan |
| `/list-features` | Feature status table |
| `/feature-facts <name>` | Look up beta flags, config keys, test commands from `facts.md` |
| `/feature-next` | Next ready artifact plus todo and MR state |
| `/feature-report` | QA validation report |
| `/feature-validate [--fix]` | Lint a feature's structure |

`workspace-init.sh` can also drop a legacy `/workspace/{discover,plan,sync,archive}` command set into `.claude/commands/`. Those predate the feature commands above.

## Workflow Example

```bash
{prefix} feature-login       # worktree + workspace + feature scaffold
workspace_status             # check state
```

In Claude:

```
/plan-feature                # fill proposal.md and tasks.md
# ... work ...
/feature-next                # what's left
/complete-feature            # merge delta-specs, mark complete
```

Add notes as you go:

```bash
workspace_discover "[gotcha] Tests require Docker running"
workspace_discover "[entry] API starts from src/main.py"
```

Finish:

```bash
{prefix}r feature-login      # sweep .giantmem into live.db, remove worktree
```

## Integration with Worktree Helpers

`__wt_setup` in `git-worktrees/worktree-core.sh` calls `workspace_init "$target_dir" "$branch"` when the library is sourced, then `scripts/feature.py new "$branch" --skip-checkout` unless the branch is one of the project's default branches. `{prefix}r` and `{prefix}bs` run `giantmem index backfill --workspace` so every file is in `live.db` before the dir goes away.

## Files

| File | Purpose |
|------|---------|
| `workspace-lib.sh` | Shell functions above. Source from your rc and from worktree helpers |
| `workspace-init.sh` | Standalone init script, optionally writes the legacy slash commands |
| `workspace_session_hook.py` | SessionStart hook: bootstrap and inject context |
| `workspace_session_end.py` | SessionEnd hook: session file, discoveries, plans |
| `list-features.sh` | Feature table from `features.json` |
| `workspace-migrate-features.py` | Convert legacy `plans/` files into `features/` dirs |
| `scripts/feature.py` | Feature lifecycle CLI: new, start, pause, reopen, complete, abandon, and more |
| `scripts/merge_delta_spec.py` | Merge a feature's delta-specs into `specs/{domain}/spec.md` |
| `scripts/migrate_spec_to_proposal.py` | Rename legacy `features/{name}/spec.md` to `proposal.md` |
| `scripts/backfill_frontmatter.py` | Add YAML frontmatter to legacy artifacts |
| `scripts/backfill_lifecycle.py` | Add `lifecycle:` frontmatter to existing artifacts |
| `scripts/embed.py` | Long-running embedder daemon used by `giantmem db embed` |
| `docs/workspace-hooks.md` | Hook behavior and file formats |
| `docs/workspace-claude-config.md` | CLAUDE.md snippets that teach Claude the layout |

## Discovery Categories

Tag discoveries so they group well. The SessionEnd hook uses the same set when it extracts them from transcripts:

| Category | Use for |
|----------|---------|
| `[finding]` | Anything discovered, found, learned |
| `[architecture]` | Overall structure, patterns |
| `[gotcha]` | Surprises, traps, caveats |
| `[convention]` | Naming, style, project rules |
| `[dependency]` | External deps, integrations |
| `[config]` | Configuration, env vars, settings |
| `[entry]` | Entry points, main files |

```bash
workspace_discover "[gotcha] Tests require Docker running"
workspace_discover "[config] All secrets in .env, never committed"
```

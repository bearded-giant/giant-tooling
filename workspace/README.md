# Workspace Management System

Scripts for managing Claude Code workspaces: the `.giantmem/` directory and feature tracking inside it. Full walkthrough in `docs/workspace-system.md`. The SessionStart and SessionEnd hooks that call into this library live in the claude-code-config repo.

## Setup

```bash
export GIANT_TOOLING_DIR="$HOME/<your-checkout>/giant-tooling"
source "$GIANT_TOOLING_DIR/workspace/workspace-lib.sh"
```

## Shell Functions

The library defines functions, not aliases. Add short names yourself if you want them (`alias ws='workspace_status'` and so on).

| Function | Description | Notes |
|----------|-------------|-------|
| `workspace_status` | Show workspace status | |
| `workspace_bootstrap` | Init or migrate workspace | Use mid-session |
| `workspace_migrate` | Move loose files to subdirs | Auto-categorizes by filename and content |
| `workspace_discover "note"` | Append a discovery | Goes to `context/discoveries.md` |
| `workspace_complete` | Mark workspace complete | |
| `workspace_sync` | Refresh git log | |
| `workspace_features` | Print `features/_index.md` | |
| `workspace_new_feature <name>` | Scaffold a feature | Wraps `scripts/feature.py new` |
| `workspace_start_feature [name]` | Promote pending to in_progress | |
| `workspace_pause_feature [name]` | Pause | Name optional when one feature is active |
| `workspace_reopen_feature [name]` | Reopen paused or complete | |
| `workspace_complete_feature [name]` | Complete and merge delta-specs | |
| `list-features [--dir <path>] [--all]` | Feature status table | Reads `features.json`; `--all` includes archived |
| `workspace_init [dir] [name]` | Initialize a new workspace | Name defaults to dir name |
| `workspace_archive [src] [project]` | Legacy snapshot mover | See Archiving below |
| `workspace_archive_list [project]` | List legacy snapshots | Omit project to list all |
| `workspace_archive_open <proj> [branch] [ts]` | Open a legacy snapshot in Finder | Uses `latest` if no timestamp |

`giantmem workspace <cmd>` and `giantmem feature <cmd>` expose the same operations from the Go CLI.

## Claude Commands

Run these inside Claude Code sessions (defined in the claude-code-config repo):

| Command | Description | Notes |
|---------|-------------|-------|
| `/ws-init` | Bootstrap `.giantmem/` and fill in `WORKSPACE.md` | |
| `/new-feature <name>` | Create feature folder | `--builds-on <parent>` optional |
| `/start-feature`, `/pause-feature`, `/reopen-feature` | Status transitions | |
| `/complete-feature`, `/abandon-feature` | Close a feature | Complete merges delta-specs into `specs/` |
| `/plan-feature` | Draft the implementation plan | |
| `/list-features` | Feature status table | |
| `/feature-facts <name>` | Beta flags, config keys, test commands | Partial name match supported |
| `/feature-next` | Next ready artifact plus todo and MR state | Read-only |
| `/feature-report [feature]` | QA validation report | |
| `/feature-validate [--fix]` | Lint feature structure | |

## Directory Structure

```
.giantmem/
├── WORKSPACE.md           # project overview
├── notes.md               # freeform notes
├── artifacts.json         # typed index (giantmem artifact reindex)
├── features/
│   ├── _index.md          # feature table
│   ├── features.json      # status cache, authoritative "active feature"
│   └── {feature-name}/
│       ├── proposal.md    # what, why, acceptance criteria
│       ├── tasks.md       # task checklist, status derived from checkbox %
│       ├── facts.md       # branch, base, beta flags, config, test commands
│       ├── {name}-notes.md
│       ├── meta.json      # machine-readable metadata
│       └── specs/{domain}/spec.md   # delta-specs, merged on complete
├── specs/
│   ├── _index.md          # source-of-truth registry
│   ├── _history.md        # merge log
│   └── {domain}/spec.md
├── context/
│   ├── discoveries.md     # codebase learnings
│   └── git-log.md
├── plans/
│   └── current.md         # active plan
├── history/
│   ├── sessions.md        # one line per session
│   └── sessions/          # one file per session
├── research/
├── reviews/
└── filebox/               # raw data, exports
```

## Feature Workflow

1. Create: `/new-feature jwt-session-strict --builds-on jwt-session-enforcement`. A new worktree does this for you, named after the branch.
2. Fill in `proposal.md` (purpose, scope, acceptance criteria) and `facts.md` (beta flags, config keys, test commands). `/plan-feature` helps with both.
3. Work across sessions. `features.json` tracks status, `tasks.md` tracks the checklist, `/feature-next` shows what is left.
4. Close: `/complete-feature` merges the feature's delta-specs into `specs/{domain}/spec.md` and flips status. `/abandon-feature` drops it without a merge.
5. Archive: `giantmem feature archive` verifies every file is in `live.db`, then removes the dir.

## Migration

Convert a legacy `plans/` dir into `features/`:

```bash
# preview
$GIANT_TOOLING_DIR/workspace/workspace-migrate-features.py /path/to/worktree --dry-run

# confirm each feature
$GIANT_TOOLING_DIR/workspace/workspace-migrate-features.py /path/to/worktree --interactive

# migrate everything
$GIANT_TOOLING_DIR/workspace/workspace-migrate-features.py /path/to/worktree
```

Other one-off migrations live in `scripts/`: `migrate_spec_to_proposal.py` (legacy `spec.md` to `proposal.md`), `backfill_frontmatter.py`, and `backfill_lifecycle.py`. Each takes `--dry-run`.

## Files

| File | Purpose |
|------|---------|
| `workspace-lib.sh` | Shell functions for workspace management |
| `workspace-init.sh` | Standalone init script |
| `list-features.sh` | Feature table from `features.json` |
| `workspace-migrate-features.py` | Plan to feature migration tool |
| `scripts/feature.py` | Feature lifecycle CLI behind the slash commands |
| `scripts/merge_delta_spec.py` | Delta-spec merge used by complete |
| `scripts/migrate_spec_to_proposal.py` | Legacy spec.md rename |
| `scripts/backfill_frontmatter.py` | Add frontmatter to legacy artifacts |
| `scripts/backfill_lifecycle.py` | Add `lifecycle:` to existing artifacts |
| `scripts/embed.py` | Embedder daemon for `giantmem db embed` |
| `docs/` | System overview, CLAUDE.md snippets |

## Archiving

`giantmem workspace archive` and `giantmem feature archive` are the current path: verify every file is in `live.db`, then delete the dir. Rows stay searchable, no filesystem copy is made.

`workspace_archive` in the shell library is the older snapshot mover. It moves `.giantmem/` to `$GIANTMEM_ARCHIVE_BASE/{project}/{timestamp}/` (default `~/giantmem_archive`), points a `latest` symlink at it, and re-runs `workspace_init`. `workspace_archive_list` and `workspace_archive_open` browse those snapshot dirs, as does `giantmem archive list|open`.

# Workspace Management System

Scripts and hooks for managing Claude Code workspaces with .giantmem directories and feature tracking.

## Setup

Source the library in your shell config:
```bash
source ~/dev/giant-tooling/workspace/workspace-lib.sh
```

## Shell Commands

| Command | Title | Description | Notes |
|---------|-------|-------------|-------|
| `ws` | Status | Show workspace status | |
| `wsb` | Bootstrap | Init or migrate workspace | Use mid-session |
| `wsm` | Migrate | Move loose files to subdirs | Auto-categorizes by filename |
| `wst` | Tree | Regenerate tree.md | |
| `wsd "note"` | Discover | Add discovery note | Deprecated, use patterns.md |
| `wsc` | Complete | Mark workspace complete | |
| `wssync` | Sync | Refresh tree + git log | |
| `wsf` | Features | List features index | Reads _index.md |
| `wsa` | Archive | Archive .giantmem to ~/giantmem_archive | |
| `wsal [project]` | Archive List | List archives | Omit project to list all |
| `wsao <proj> [branch]` | Archive Open | Open archive in Finder | Uses `latest` if no timestamp |
| `ws-init [name]` | Init | Initialize new workspace | Name defaults to dir name |
| `ws-migrate-features [dir]` | Migrate Features | Convert plans to features | `-i` interactive, `--dry-run` preview |

## Claude Commands

Run these inside Claude Code sessions:

| Command | Title | Description | Notes |
|---------|-------|-------------|-------|
| `/list-features` | List Features | Display feature registry | |
| `/new-feature <name>` | New Feature | Create feature folder | `--builds-on <parent>` optional |
| `/feature-facts <name>` | Feature Facts | Quick lookup beta flags, config | Partial name match supported |
| `/qa-report [feature]` | QA Report | Generate validation report | Swarms auto-generate; manual on-demand |

## Directory Structure

```
.giantmem/
├── WORKSPACE.md           # Project overview
├── features/
│   ├── _index.md          # Feature registry (Claude-maintained)
│   └── {feature-name}/
│       ├── spec.md        # What + why + acceptance criteria
│       ├── facts.md       # Beta flags, config, test commands
│       └── meta.json      # Machine-readable metadata
├── context/
│   ├── patterns.md        # Curated architectural patterns
│   └── tree.md            # Directory tree
├── plans/
│   └── current.md         # Active session work (transient)
├── research/              # External topic research
├── reviews/               # Code review notes
├── history/               # Session logs
├── filebox/               # Raw data, exports
└── prompts/               # Reusable prompt templates
```

## Feature Workflow

1. **Start new feature**: `/new-feature jwt-session-strict --builds-on jwt-session-enforcement`
2. **Fill in spec.md**: Purpose, scope, acceptance criteria
3. **Fill in facts.md**: Beta flags, config keys, test commands
4. **Work across sessions**: Feature folder persists, _index.md tracks status
5. **Generate QA report**: `/qa-report jwt-session-strict` when ready for review
6. **Mark complete**: Update status in _index.md

## Migration

Convert existing plans/ to features/:

```bash
# Preview what would happen
ws-migrate-features /path/to/worktree --dry-run

# Interactive mode - confirm each feature
ws-migrate-features /path/to/worktree -i

# Auto-migrate all
ws-migrate-features /path/to/worktree
```

## Files

| File | Purpose |
|------|---------|
| `workspace-lib.sh` | Shell functions for workspace management |
| `workspace-init.sh` | Standalone init script |
| `workspace_session_hook.py` | Claude Code session start hook |
| `workspace_session_end.py` | Claude Code session end hook |
| `workspace-migrate-features.py` | Plan to feature migration tool |

## Archive Location

Archives stored at: `~/giantmem_archive/{project}/{branch}/{timestamp}/`

Each archive includes a `latest` symlink to the most recent backup.

# Workspace System

Context management for Claude Code sessions. Works with git worktrees or standalone in any project.

## Quick Start

### For Worktree Projects (MA, CC, etc.)

Already integrated. New worktrees automatically get workspace structure:

```bash
mwt feature-xyz              # workspace auto-created in scratch/
# ... work ...
mwtr feature-xyz             # workspace auto-archived
```

### For Ad-Hoc Projects

```bash
cd ~/projects/some-project
wsi                          # initialize workspace
# or: workspace-init.sh
```

## Setup

Add to `~/.bashrc` or `~/.zshrc`:

```bash
# Workspace functions
source "$HOME/dev/giant-tooling/workspace/workspace-lib.sh"

# Short aliases
alias ws='workspace_status'
alias wst='workspace_tree'
alias wsd='workspace_discover'
alias wsc='workspace_complete'
alias wssync='workspace_sync'

# Quick init
wsi() { workspace_init "$PWD" "${1:-$(basename "$PWD")}"; }
```

## Directory Structure

```
project/
└── scratch/
    ├── WORKSPACE.md          # Branch/project purpose, status
    ├── context/
    │   ├── tree.md           # Auto-generated project structure
    │   ├── discoveries.md    # Codebase learnings
    │   └── git-log.md        # Recent commits
    ├── plans/
    │   └── current.md        # Implementation plans
    ├── history/
    │   └── sessions.md       # Session timestamps/notes
    ├── prompts/              # Reusable prompt templates
    ├── research/             # Web research findings
    ├── reviews/              # Code review notes
    └── filebox/              # Scratch files, samples, temp stuff
```

## Claude Code Integration

Hooks automatically manage workspace lifecycle:

| Event | Hook | Action |
|-------|------|--------|
| Session start | `workspace_session_hook.py` | Bootstrap scratch/ if missing, inject context |
| Session end | `workspace_session_end.py` | Extract discoveries/plans from transcript |

**Automatic on session start:**
- Creates `scratch/` structure if missing
- Injects `WORKSPACE.md` content into session
- Injects recent discoveries for continuity

**Automatic on session end:**
- Parses transcript for codebase learnings
- Appends to `scratch/context/discoveries.md`
- Updates `scratch/plans/current.md` with any plans discussed
- Logs session to `scratch/history/sessions.md`

See `WORKSPACE-CLAUDE-HOOKS.md` for full hook documentation.

## Shell Commands

| Command | Alias | Description |
|---------|-------|-------------|
| `workspace_bootstrap` | `wsb` | Smart init: creates, migrates, or syncs (use mid-session) |
| `workspace_migrate` | `wsm` | Move loose scratch files to appropriate subdirs |
| `workspace_init` | `wsi` | Initialize workspace in current dir |
| `workspace_status` | `ws` | Show workspace status and recent discoveries |
| `workspace_tree` | `wst` | Regenerate tree.md |
| `workspace_discover "note"` | `wsd` | Add a discovery note |
| `workspace_complete` | `wsc` | Mark workspace as complete |
| `workspace_sync` | `wssync` | Refresh tree + git log |
| `workspace_session_note` | - | Add session marker/note to history |
| `workspace_gitlog` | - | Update git-log.md |

### Mid-Session Bootstrap

If you're in an existing Claude session and want to start using workspace:

```bash
wsb                    # Smart bootstrap - handles all cases
```

This will:
- **No scratch/**: Create full workspace structure
- **scratch/ with loose files**: Migrate files to subdirs, create WORKSPACE.md
- **Already structured**: Just sync context files

### Migration Logic

`workspace_migrate` categorizes files by name and content:

| Pattern | Destination |
|---------|-------------|
| `*plan*.md`, `*todo*.md`, `*steps*.md` | plans/ |
| `*discover*.md`, `*context*.md` | context/ |
| `*history*.md`, `*session*.md` | history/ |
| `*prompt*.md`, `*template*.md` | prompts/ |
| `*research*.md`, `*notes*.md` | research/ |
| `*review*.md`, `*feedback*.md` | reviews/ |
| `tree.md`, `git-log.md` | context/ |
| Other `.md` files | Checked for content hints, else filebox/ |
| Non-markdown files | filebox/ |

## Claude Slash Commands

If `.claude/commands/workspace/` exists (created by `workspace-init.sh`):

| Command | Purpose |
|---------|---------|
| `/workspace/discover` | Explore codebase, document findings |
| `/workspace/plan` | Create/update implementation plan |
| `/workspace/sync` | Refresh context files |
| `/workspace/archive` | Mark complete, create summary |

## Workflow Examples

### Feature Development (Worktree)

```bash
mwt feature-login            # Create worktree + workspace
ws                           # Check workspace status
```

In Claude:
```
/workspace/discover          # Explore relevant code
/workspace/plan              # Plan the implementation
```

During work:
```bash
wsd "Auth middleware in src/middleware/auth.py"
wsd "JWT tokens stored in Redis with 24h TTL"
wst                          # Refresh tree after adding files
```

Finishing:
```bash
wsc                          # Mark complete
mwtr feature-login           # Archive and remove
```

### Ad-Hoc Investigation

```bash
cd ~/projects/legacy-api
wsi                          # Init workspace
```

In Claude:
```
/workspace/discover          # Map the codebase
```

Add notes as you go:
```bash
wsd "[architecture] Uses hexagonal architecture with ports/adapters"
wsd "[gotcha] Database migrations run on app start, not separately"
wsd "[pattern] All services inherit from BaseService"
```

Check what you've learned:
```bash
ws                           # See status + recent discoveries
cat scratch/context/discoveries.md
```

### Quick Context Refresh

Before a Claude session:
```bash
wssync                       # Update tree.md and git-log.md
ws                           # Review current state
```

## Integration with Worktree Helpers

The workspace library is sourced by worktree helpers. Add this to your helper's setup function:

```bash
# In _m_wt_setup_worktree() or _wt_setup_worktree():
if type workspace_init &>/dev/null; then
    workspace_init "$target_dir" "$branch"
fi
```

Archiving happens automatically via existing `mwtr`/`wtr` scratch backup logic.

## Files

| File | Purpose |
|------|---------|
| `workspace-lib.sh` | Core functions, source in bashrc and worktree helpers |
| `workspace-init.sh` | Standalone init script, creates slash commands |
| `workspace_session_hook.py` | Claude Code SessionStart hook (bootstrap + inject) |
| `workspace_session_end.py` | Claude Code SessionEnd hook (extract + persist) |
| `WORKSPACE-CLAUDE-HOOKS.md` | Hook integration documentation |
| `WORKSPACE-INTEGRATION-ANALYSIS.md` | Full design analysis and rationale |

## Discovery Categories

When adding discoveries with `wsd`, use categories for organization:

- `[architecture]` - Overall structure, patterns used
- `[pattern]` - Code patterns, conventions
- `[gotcha]` - Surprises, traps, things to watch out for
- `[dependency]` - External deps, integrations
- `[convention]` - Naming, style, project-specific rules
- `[entry]` - Entry points, main files
- `[config]` - Configuration, env vars, settings

Example:
```bash
wsd "[gotcha] Tests require Docker running"
wsd "[entry] API starts from src/main.py"
wsd "[config] All secrets in .env, never committed"
```

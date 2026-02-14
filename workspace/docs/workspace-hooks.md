# Workspace Claude Code Hooks

Automatic workspace integration with Claude Code via SessionStart and SessionEnd hooks.

## Overview

Two Python hooks bridge `workspace-lib.sh` with Claude Code sessions:

| Hook | Event | Purpose |
|------|-------|---------|
| `workspace_session_hook.py` | SessionStart | Bootstrap workspace, inject context + recent sessions |
| `workspace_session_end.py` | SessionEnd | Create session summary file, extract discoveries/plans |

```
Session Lifecycle:

  claude starts
       |
       v
  SessionStart hook
       |
       +-- scratch/ exists? --> inject context + recent sessions
       |
       +-- scratch/ missing? --> bootstrap via workspace_init
       |                         then inject context
       v
  [Claude session runs]
       |
       v
  SessionEnd hook
       |
       +-- read transcript JSONL
       +-- extract topic from full session content
       +-- extract user prompts, tool usage with file paths
       +-- create scratch/history/sessions/{timestamp}_{id}.md
       +-- update scratch/history/sessions.md index
       +-- extract discoveries (patterns, gotchas, architecture)
       +-- extract plans (numbered lists, TODOs)
       +-- append to scratch/context/discoveries.md
       +-- update scratch/plans/current.md
       |
       v
  session ends
```

## Installation

### Files

```
~/.claude/hooks/
├── workspace_session_hook.py    # Start hook
├── workspace_session_end.py     # End hook
├── memory_*.py                  # Existing memory hooks
```

Source files in `~/dev/giant-tooling/workspace/`.
Stow-managed copies in `~/dotfiles/claude-code/.claude/hooks/`.

### Settings

In `~/.claude/settings.json` (via stow from `~/dotfiles/claude-code/.claude/settings.json`):

```json
{
  "hooks": {
    "SessionStart": [
      {
        "hooks": [
          {
            "type": "command",
            "command": "python3 ~/.claude/hooks/workspace_session_hook.py"
          },
          {
            "type": "command",
            "command": "python3 ~/.claude/hooks/memory_session_start.py"
          }
        ]
      }
    ],
    "SessionEnd": [
      {
        "hooks": [
          {
            "type": "command",
            "command": "python3 ~/.claude/hooks/workspace_session_end.py"
          },
          {
            "type": "command",
            "command": "python3 ~/.claude/hooks/memory_curate.py"
          }
        ]
      }
    ]
  }
}
```

Workspace hooks run first, then memory hooks.

## Hook Details

### SessionStart: workspace_session_hook.py

**Input** (JSON on stdin):
```json
{
  "session_id": "abc123",
  "cwd": "/path/to/project",
  "source": "startup" | "resume" | "clear"
}
```

**Behavior**:

| Condition | Action |
|-----------|--------|
| `source: startup` + no scratch/ | Run `workspace_init` via workspace-lib.sh |
| `source: startup` + scratch/ exists | Read and inject context |
| `source: resume` | Read and inject context (no bootstrap) |

**Output** (stdout, injected into session):
```
[Workspace bootstrapped for project-name]
Created scratch/ with: context/, plans/, history/, prompts/, research/, reviews/, filebox/

=== WORKSPACE CONTEXT ===
# Workspace: project-name
Started: 2025-12-22
Status: [ ] In Progress  [ ] Complete
...

=== ACTIVE PLAN ===
(if scratch/plans/current.md exists)

=== RECENT DISCOVERIES ===
(last 20 lines of scratch/context/discoveries.md)

---
Remember: Save findings to scratch/context/discoveries.md, plans to scratch/plans/
```

**Configuration**:
```python
WORKSPACE_LIB = Path.home() / "dev/giant-tooling/workspace/workspace-lib.sh"
```

### SessionEnd: workspace_session_end.py

**Input** (JSON on stdin):
```json
{
  "session_id": "abc123",
  "cwd": "/path/to/project",
  "transcript_path": "~/.claude/projects/.../session.jsonl"
}
```

**Behavior**:

1. Skip if no `scratch/` directory
2. Read transcript JSONL file
3. Extract assistant message content
4. Pattern-match for discoveries and plans
5. Persist to workspace files

**Discovery Extraction**:

Searches for patterns indicating codebase learnings:

| Category | Trigger Words |
|----------|---------------|
| `finding` | discovered, found, learned, realized, noticed |
| `architecture` | pattern, architecture, structure |
| `gotcha` | gotcha, caveat, watch out, careful, important |
| `convention` | convention, standard, style, naming |
| `dependency` | dependency, requires, depends on, imports |
| `config` | config, configuration, setting, environment |
| `entry` | entry point, main, bootstrap, init |

**Plan Extraction**:

- Numbered lists (1. 2. 3.)
- Bulleted lists (- *)
- TODO/NEXT/STEP markers

**Output Files**:

| File | Content |
|------|---------|
| `scratch/history/sessions/{ts}_{id}.md` | Individual session summary with full details |
| `scratch/history/sessions.md` | Index with one-liner per session |
| `scratch/context/discoveries.md` | Appended: `- YYYY-MM-DD HH:MM: [category] finding` |
| `scratch/plans/current.md` | Updated with extracted steps |

**Individual Session File Format** (`scratch/history/sessions/20250106_143022_abc123ef.md`):

```markdown
# Session: 2025-01-06 14:30 - 15:22

## Summary
Topic: auth
Brief: Investigated JWT refresh flow and added endpoint

## User Prompts
- Find where auth tokens are validated
- Add a refresh token endpoint
- Run the tests

## Files Touched
### Modified
- /path/to/src/api/auth.py
- /path/to/tests/test_auth.py
### Created
- /path/to/src/services/token_refresh.py
### Read
- /path/to/src/middleware/auth.py
- /path/to/src/config.py

## Tool Usage
- Bash: 8
- Edit: 4
- Grep: 6
- Read: 12

## Commands Run
- `pytest tests/test_auth.py -v`
- `git status`

## Discoveries Extracted
- [architecture] JWT validation in middleware/auth.py
- [config] Token TTL configured in settings.py

## Metadata
- Session ID: abc123ef-1234-5678-abcd-ef0123456789
- Generated: 2025-01-06 15:22:45
```

**Session Index Format** (`scratch/history/sessions.md`):

```
- 2025-01-06 14:30: [auth] abc123ef - Investigated JWT refresh flow... (4 edits, 2 discoveries)
- 2025-01-06 10:15: [api] def456gh - Added user profile endpoint (6 edits, 1 discovery)
- 2025-01-05 16:00: [test] 12345678 - Fixed failing integration tests (2 edits, read-only)
```

**Topic Extraction**:

Topics are derived by analyzing user prompts and assistant content for keywords:

| Topic | Keywords |
|-------|----------|
| `auth` | auth, login, jwt, token, session, password, credential |
| `api` | api, endpoint, route, rest, graphql, request, response |
| `database` | database, sql, query, migration, model, schema, table |
| `test` | test, spec, pytest, jest, coverage, mock, fixture |
| `bug` | bug, fix, error, issue, debug, broken, failing |
| `feature` | feature, implement, add, create, new, build |
| `refactor` | refactor, cleanup, reorganize, restructure, rename |
| `config` | config, setting, env, environment, setup, install |
| `docs` | document, readme, comment, explain, describe |
| `perf` | performance, optimize, speed, slow, fast, cache |
| `ui` | ui, frontend, component, style, css, render, display |
| `deploy` | deploy, ci, cd, pipeline, docker, kubernetes |

**Output** (stderr, visible to user):
```
Workspace: session:20250106_143022_abc123ef.md, 2 discoveries, plans
```

## Transcript Format

The JSONL transcript contains message objects:

```json
{"type": "assistant", "message": {"content": [{"type": "text", "text": "..."}]}}
{"type": "user", "message": {"content": "..."}}
{"type": "tool_use", ...}
{"type": "tool_result", ...}
```

The end hook extracts only assistant text content for analysis.

## Example Session

**Start** (in project with prior sessions):
```
$ claude
=== WORKSPACE CONTEXT ===
# Workspace: my-project
Started: 2025-12-22
...

=== RECENT SESSIONS ===
- 2025-01-05 [auth]: Fixed JWT validation bug in middleware
- 2025-01-04 [api]: Added user profile endpoint
- 2025-01-03 [test]: Set up integration test framework

=== ACTIVE PLAN ===
...

=== RECENT DISCOVERIES ===
...
```

**During session** (Claude learns things):
```
Claude: I discovered that the auth middleware is in src/middleware/auth.py
        and it uses JWT tokens stored in Redis with a 24h TTL.

        The implementation plan:
        1. Add new endpoint in api/routes.py
        2. Create service in services/feature.py
        3. Add tests in tests/test_feature.py
```

**End** (session exit):
```
Workspace: session:20250106_103522_abc123ef.md, 2 discoveries, plans
```

**Result in scratch/**:

`scratch/history/sessions/20250106_103522_abc123ef.md`:
```markdown
# Session: 2025-01-06 10:30 - 10:35

## Summary
Topic: auth
Brief: Add refresh token endpoint

## User Prompts
- Add a refresh token endpoint to the auth API

## Files Touched
### Modified
- /project/src/api/routes.py
- /project/src/services/feature.py
### Read
- /project/src/middleware/auth.py

## Tool Usage
- Edit: 2
- Read: 5
- Bash: 3

## Discoveries Extracted
- [architecture] auth middleware is in src/middleware/auth.py
- [config] JWT tokens stored in Redis with a 24h TTL

## Metadata
- Session ID: abc123ef-...
- Generated: 2025-01-06 10:35:22
```

`scratch/history/sessions.md`:
```
- 2025-01-06 10:35: [auth] abc123ef - Add refresh token endpoint (2 edits, 2 discoveries)
```

`scratch/context/discoveries.md`:
```
- 2025-01-06 10:35: [architecture] auth middleware is in src/middleware/auth.py
- 2025-01-06 10:35: [config] JWT tokens stored in Redis with a 24h TTL
```

## Relationship to Other Components

```
workspace-lib.sh          Shell functions for manual workspace ops
       |
       +-- workspace_init()     Called by start hook
       +-- workspace_tree()     Called by start hook
       |
       v
workspace_session_hook.py    Bootstrap + inject context + recent sessions (SessionStart)
       |
       +-- reads scratch/history/sessions/*.md for recent session context
       |
       v
[Claude session]
       |
       v
workspace_session_end.py     Create session file + extract + persist (SessionEnd)
       |
       v
scratch/                     Persistent workspace state
├── WORKSPACE.md
├── context/
│   ├── discoveries.md       <-- End hook appends here
│   └── tree.md              <-- Start hook generates
├── plans/
│   └── current.md           <-- End hook updates here
└── history/
    ├── sessions.md          <-- End hook appends index line
    └── sessions/            <-- Individual session files (NEW)
        ├── 20250106_103522_abc123ef.md
        ├── 20250105_160000_def456gh.md
        └── ...
```

## Manual vs Automatic

| Action | Manual (shell) | Automatic (hooks) |
|--------|----------------|-------------------|
| Bootstrap workspace | `wsi` / `workspace_init` | SessionStart hook |
| Generate tree | `wst` / `workspace_tree` | SessionStart hook |
| Add discovery | `wsd "note"` | SessionEnd hook (extracted) |
| Update plan | Edit `scratch/plans/current.md` | SessionEnd hook (extracted) |
| Mark complete | `wsc` / `workspace_complete` | Manual only |
| View status | `ws` / `workspace_status` | Manual only |

The hooks automate context injection and extraction. Manual commands still useful for:
- Explicit discovery notes during session
- Marking completion
- Checking status
- Ad-hoc tree refresh

## Troubleshooting

**Hook not running**:
```bash
# Check settings
cat ~/.claude/settings.json | jq '.hooks.SessionStart'
cat ~/.claude/settings.json | jq '.hooks.SessionEnd'

# Check hook exists and is executable
ls -la ~/.claude/hooks/workspace_session_hook.py
ls -la ~/.claude/hooks/workspace_session_end.py
```

**Bootstrap not working**:
```bash
# Check workspace-lib.sh path
cat ~/.claude/hooks/workspace_session_hook.py | grep WORKSPACE_LIB

# Test manually
echo '{"session_id":"test","cwd":"/tmp/test","source":"startup"}' | \
  python3 ~/.claude/hooks/workspace_session_hook.py
```

**End hook not extracting**:
```bash
# Check transcript path in hook input
# The transcript_path must point to valid JSONL

# Test extraction manually (need real transcript)
echo '{"session_id":"test","cwd":"/path/with/scratch","transcript_path":"~/.claude/projects/.../session.jsonl"}' | \
  python3 ~/.claude/hooks/workspace_session_end.py
```

**No discoveries extracted**:
- Check that assistant messages contain trigger words
- Extraction is pattern-based; very unique phrasing may not match
- Manual `wsd "note"` still works as fallback

## Dependencies

- Python 3 (standard library only)
- `workspace-lib.sh` at configured path
- `bash` for workspace_init subprocess
- Claude Code with hooks support

No external Python packages required.

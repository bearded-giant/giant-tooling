# Workspace Guidance for Claude Code

Snippets that tell Claude how to use the `.giantmem/` layout. Paste one into your global `~/.claude/CLAUDE.md` or a per-project CLAUDE.md.

---

## For Global CLAUDE.md

```markdown
## Workspace Context System

When working in a project with a `.giantmem/` directory, use it as persistent session context.

### On Session Start
1. Read `.giantmem/WORKSPACE.md` for branch/project context
2. Read `.giantmem/features/features.json` to find the in_progress feature
3. Read `.giantmem/context/discoveries.md` for prior learnings
4. Read `.giantmem/plans/current.md` for any active plan

### During Work
Route output by directory:

| Directory | Purpose | When to Write |
|-----------|---------|---------------|
| `.giantmem/features/{name}/` | Feature proposal, tasks, facts, notes, delta-specs | Anything scoped to the active feature |
| `.giantmem/specs/{domain}/` | Source-of-truth specs | Only via /complete-feature merge |
| `.giantmem/context/` | Codebase knowledge | Architecture, patterns, gotchas |
| `.giantmem/plans/` | Implementation plans | Repo-level plans with no active feature |
| `.giantmem/history/` | Session summaries | Written by the SessionEnd hook |
| `.giantmem/research/` | Research findings | Web research, doc summaries |
| `.giantmem/reviews/` | Code reviews | Review notes, feedback |
| `.giantmem/filebox/` | Scratch files | Samples, exports, temp data |

### File Conventions

**discoveries.md** is an append-only log:
```
- YYYY-MM-DD HH:MM: [category] finding
```
Categories: finding, architecture, gotcha, convention, dependency, config, entry

**plans/current.md** holds the active plan: goal, steps, files to modify, risks

**history/sessions.md** holds one line per session; `history/sessions/` holds the full summaries

### Research Findings

Save web research and documentation summaries to `.giantmem/research/{topic}.md` with sources, key findings, and relevance to the current work.
```

---

## For Per-Project CLAUDE.md

```markdown
## Workspace

This project uses .giantmem/ for session context. On session start, read:
- @.giantmem/WORKSPACE.md - Branch purpose and status
- @.giantmem/context/discoveries.md - Prior learnings (if exists)
- @.giantmem/plans/current.md - Active plan (if exists)

Save discoveries to .giantmem/context/discoveries.md during work.
```

---

## Minimal Global Addition

```markdown
## Workspace

If `.giantmem/` exists, use it for persistent context:
- Read `.giantmem/WORKSPACE.md` at session start
- Append discoveries to `.giantmem/context/discoveries.md`
- Write plans to `.giantmem/plans/`
```

---

## Directory Reference

```
.giantmem/
├── WORKSPACE.md              # branch/project purpose, status, notes
├── notes.md                  # freeform notes
├── artifacts.json            # typed index, built by giantmem artifact reindex
├── features/
│   ├── _index.md             # feature table
│   ├── features.json         # status cache
│   └── {name}/               # proposal.md, tasks.md, facts.md, {name}-notes.md, meta.json, specs/
├── specs/
│   ├── _index.md             # source-of-truth registry
│   ├── _history.md           # merge log
│   └── {domain}/spec.md
├── context/
│   ├── discoveries.md        # codebase learnings log
│   └── git-log.md            # recent commits
├── plans/
│   └── current.md            # active plan
├── history/
│   ├── sessions.md           # one line per session
│   └── sessions/             # one file per session
├── research/
│   └── {topic}.md
├── reviews/
│   └── {date}-{subject}.md
└── filebox/
```

---

## Integration Notes

The structure is created by `{prefix} <branch>` (worktree helpers, along with a feature named after the branch), by `workspace_init` or `giantmem workspace init` for ad-hoc projects, and by the SessionStart hook when a session opens in a dir without one. `{prefix}r` sweeps it into `live.db` before removing a worktree.

Nothing adds `.giantmem/` to `.gitignore` automatically. Put `**/.giantmem` in your global excludes file (`git config --global core.excludesfile`) so it stays out of every repo.

Shell functions for manual updates: `workspace_status`, `workspace_discover "note"`, `workspace_sync`. Full list in `workspace-system.md`.

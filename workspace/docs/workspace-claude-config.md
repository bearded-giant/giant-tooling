# Workspace Guidance for Claude Code

This guidance tells Claude how to use the `scratch/` workspace structure. Include this in your global `~/.claude/CLAUDE.md` or per-project CLAUDE.md.

---

## For Global CLAUDE.md

Add this to `~/dotfiles/claude-code/.claude/CLAUDE.md`:

```markdown
## Workspace Context System

When working in a project with a `scratch/` directory, use it as persistent session context.

### On Session Start
1. Check if `scratch/WORKSPACE.md` exists - if so, read it for branch/project context
2. Check `scratch/context/discoveries.md` for prior learnings about this codebase
3. Check `scratch/plans/current.md` for any active implementation plan

### During Work
Save learnings and context to the appropriate scratch/ subdirectory:

| Directory | Purpose | When to Write |
|-----------|---------|---------------|
| `scratch/context/` | Codebase knowledge | Discoveries about architecture, patterns, gotchas |
| `scratch/plans/` | Implementation plans | When planning features or refactors |
| `scratch/history/` | Session summaries | End of significant work sessions |
| `scratch/prompts/` | Reusable prompts | Complex prompts worth saving for reuse |
| `scratch/research/` | Research findings | Web research, documentation summaries |
| `scratch/reviews/` | Code reviews | Review notes, feedback, analysis |
| `scratch/filebox/` | Scratch files | Temporary files, samples, exports |

### File Conventions

**discoveries.md** - Append-only log of codebase learnings:
```
- YYYY-MM-DD HH:MM: [category] finding
```
Categories: architecture, pattern, gotcha, dependency, convention, entry, config

**tree.md** - Auto-generated project structure (refresh with shell: `wst` or `workspace_tree`)

**current.md** in plans/ - Active implementation plan with steps, files to modify, risks

**sessions.md** in history/ - Session timestamps and summaries

### Writing Context Files

When you discover something important about the codebase:
1. Append to `scratch/context/discoveries.md` with timestamp and category
2. For major architectural findings, also update `scratch/WORKSPACE.md` Discoveries section

When creating an implementation plan:
1. Write to `scratch/plans/current.md` (or `scratch/plans/{feature-name}.md` for multiple plans)
2. Include: goal, steps, files to modify, dependencies, risks

When completing significant work:
1. Append session summary to `scratch/history/sessions.md`
2. Update `scratch/WORKSPACE.md` status if branch work is complete

### Prompt Templates

Save reusable prompts to `scratch/prompts/` as markdown files:
- `scratch/prompts/research-{topic}.md` - Research request templates
- `scratch/prompts/review-{type}.md` - Code review checklists
- `scratch/prompts/feature-{name}.md` - Feature implementation prompts

### Research Findings

Save web research and documentation summaries to `scratch/research/`:
- `scratch/research/{topic}.md` - Research on specific topics
- Include sources, key findings, and relevance to current work
```

---

## For Per-Project CLAUDE.md

Add a reference to load workspace context:

```markdown
## Workspace

This project uses scratch/ for session context. On session start, read:
- @scratch/WORKSPACE.md - Branch purpose and status
- @scratch/context/discoveries.md - Prior learnings (if exists)
- @scratch/plans/current.md - Active plan (if exists)

Save discoveries to scratch/context/discoveries.md during work.
```

---

## Alternative: Minimal Global Addition

If you want minimal global config, just add this to `~/.claude/CLAUDE.md`:

```markdown
## Workspace

If `scratch/` directory exists, use it for persistent context:
- Read `scratch/WORKSPACE.md` at session start for project context
- Append discoveries to `scratch/context/discoveries.md`
- Write plans to `scratch/plans/`
- Save reusable prompts to `scratch/prompts/`
```

---

## Complete Directory Reference

```
scratch/
├── WORKSPACE.md              # Branch/project purpose, status, notes
├── context/
│   ├── tree.md               # Project structure (auto-generated)
│   ├── discoveries.md        # Codebase learnings log
│   ├── git-log.md            # Recent commits (auto-generated)
│   └── changes.md            # Files modified this session (optional)
├── plans/
│   ├── current.md            # Active implementation plan
│   └── {feature}.md          # Feature-specific plans
├── history/
│   ├── sessions.md           # Session timestamps and summaries
│   └── {date}-summary.md     # Detailed session summaries
├── prompts/
│   ├── research-{topic}.md   # Research request templates
│   ├── review-{type}.md      # Review checklists
│   └── {custom}.md           # Custom reusable prompts
├── research/
│   └── {topic}.md            # Research findings and summaries
├── reviews/
│   └── {date}-{subject}.md   # Code review notes
└── filebox/
    └── *                     # Temporary files, samples, exports
```

---

## Prompt Template Example

Save to `scratch/prompts/research-template.md`:

```markdown
# Research: {TOPIC}

## Goal
{1-3 sentence objective}

## Context
{Why this matters, what decision it informs}

## Search Guidance
Keywords: {keyword1}, {keyword2}
Prefer: {official docs, specific sites}
Avoid: {outdated sources, specific sites}

## Questions
1. {Specific question}
2. {Specific question}

## Output
- [ ] Summary of findings
- [ ] Code examples if applicable
- [ ] Trade-offs analysis
- [ ] Recommendations

## Findings
{Claude fills this in}
```

---

## Integration Notes

The workspace structure is:
- Created automatically by worktree helpers (`mwt`, `cwt`, `wt`)
- Created manually via `wsi` or `workspace-init.sh` for ad-hoc projects
- Archived automatically when worktree is removed (`mwtr`, `cwtr`, `wtr`)
- Gitignored (stays local, no repo bloat)

Shell commands for manual updates:
- `ws` / `workspace_status` - Show workspace status
- `wst` / `workspace_tree` - Regenerate tree.md
- `wsd "note"` / `workspace_discover "note"` - Add discovery
- `wssync` / `workspace_sync` - Refresh tree + git log

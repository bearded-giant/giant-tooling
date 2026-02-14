#!/bin/bash
# workspace-init.sh - Initialize workspace in any directory
# Usage: workspace-init.sh [project-name]

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCRIPT_DIR/workspace-lib.sh"

PROJECT_NAME="${1:-$(basename "$PWD")}"

echo "Initializing workspace for: $PROJECT_NAME"
workspace_init "$PWD" "$PROJECT_NAME"

# Create sample prompt template
if [ ! -f "scratch/prompts/research-template.md" ]; then
    cat > "scratch/prompts/research-template.md" << 'EOF'
# Research: {TOPIC}

## Goal
{1-3 sentence objective}

## Context
{Why this matters, what decision it informs}

## Search Guidance
Keywords: {keyword1}, {keyword2}
Prefer: {official docs, specific sites}
Avoid: {outdated sources}

## Questions
1. {Specific question}
2. {Specific question}

## Output
- [ ] Summary of findings
- [ ] Code examples if applicable
- [ ] Trade-offs analysis
- [ ] Recommendations

## Findings
{To be filled in during research}
EOF
    echo "Created scratch/prompts/research-template.md"
fi

# Create .claude/commands/workspace/ if .claude exists or user wants it
if [ -d ".claude" ] || [ -f "CLAUDE.md" ] || [ -f ".claude/CLAUDE.md" ]; then
    echo ""
    echo "Claude config detected. Create workspace slash commands? (y/N)"
    read -r response
    if [[ "$response" =~ ^[Yy]$ ]]; then
        mkdir -p ".claude/commands/workspace"

        # discover.md
        cat > ".claude/commands/workspace/discover.md" << 'EOF'
Explore the codebase and document findings.

1. Read scratch/context/tree.md for structure overview
2. Search for key patterns: entry points, config files, main modules
3. Append discoveries to scratch/context/discoveries.md in format:
   - YYYY-MM-DD HH:MM: [category] finding description

Categories: architecture, pattern, gotcha, dependency, convention
EOF

        # plan.md
        cat > ".claude/commands/workspace/plan.md" << 'EOF'
Create or update implementation plan.

1. Read scratch/WORKSPACE.md for project/branch purpose
2. Read scratch/context/discoveries.md for context
3. Create/update scratch/plans/current.md with:
   - Goal summary
   - Step-by-step implementation plan
   - Files to modify
   - Risks/considerations
EOF

        # sync.md
        cat > ".claude/commands/workspace/sync.md" << 'EOF'
Refresh workspace context files.

1. Generate fresh tree to scratch/context/tree.md (exclude node_modules, venv, __pycache__, .git, scratch)
2. If git repo, add recent commits summary to scratch/context/git-log.md:
   git log --oneline -20 > scratch/context/git-log.md
3. Report what was updated
EOF

        # archive.md
        cat > ".claude/commands/workspace/archive.md" << 'EOF'
Prepare workspace for completion.

1. Update scratch/WORKSPACE.md - change Status to [x] Complete
2. Create scratch/history/summary.md with:
   - What was accomplished
   - Key decisions made
   - Files changed
3. Report completion status
EOF

        echo "Created .claude/commands/workspace/ with slash commands"
    fi
else
    echo ""
    echo "No Claude config found. Create .claude/commands/workspace/ anyway? (y/N)"
    read -r response
    if [[ "$response" =~ ^[Yy]$ ]]; then
        mkdir -p ".claude/commands/workspace"

        cat > ".claude/commands/workspace/discover.md" << 'EOF'
Explore the codebase and document findings.

1. Read scratch/context/tree.md for structure overview
2. Search for key patterns: entry points, config files, main modules
3. Append discoveries to scratch/context/discoveries.md in format:
   - YYYY-MM-DD HH:MM: [category] finding description

Categories: architecture, pattern, gotcha, dependency, convention
EOF

        cat > ".claude/commands/workspace/plan.md" << 'EOF'
Create or update implementation plan.

1. Read scratch/WORKSPACE.md for project/branch purpose
2. Read scratch/context/discoveries.md for context
3. Create/update scratch/plans/current.md with:
   - Goal summary
   - Step-by-step implementation plan
   - Files to modify
   - Risks/considerations
EOF

        cat > ".claude/commands/workspace/sync.md" << 'EOF'
Refresh workspace context files.

1. Generate fresh tree to scratch/context/tree.md (exclude node_modules, venv, __pycache__, .git, scratch)
2. If git repo, add recent commits summary to scratch/context/git-log.md:
   git log --oneline -20 > scratch/context/git-log.md
3. Report what was updated
EOF

        cat > ".claude/commands/workspace/archive.md" << 'EOF'
Prepare workspace for completion.

1. Update scratch/WORKSPACE.md - change Status to [x] Complete
2. Create scratch/history/summary.md with:
   - What was accomplished
   - Key decisions made
   - Files changed
3. Report completion status
EOF

        echo "Created .claude/commands/workspace/ with slash commands"
    fi
fi

echo ""
echo "Workspace ready. Shell commands (after sourcing workspace-lib.sh):"
echo "  workspace_status    - Show workspace status"
echo "  workspace_tree      - Regenerate tree.md"
echo "  workspace_discover  - Add discovery note"
echo "  workspace_complete  - Mark as complete"
echo "  workspace_sync      - Refresh tree + git log"
echo ""
echo "Or use aliases: ws, wst, wsd, wsc"
echo ""
if [ -d ".claude/commands/workspace" ]; then
    echo "Claude slash commands available:"
    echo "  /workspace/discover  - Explore and document codebase"
    echo "  /workspace/plan      - Create implementation plan"
    echo "  /workspace/sync      - Refresh context files"
    echo "  /workspace/archive   - Mark workspace complete"
fi

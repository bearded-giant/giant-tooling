#!/bin/bash
# workspace-lib.sh - Shared workspace functions
# Source this in worktree helpers OR use standalone
#
# Usage:
#   source /path/to/workspace-lib.sh
#
# Functions:
#   workspace_init [dir] [name]   - Initialize workspace structure
#   workspace_bootstrap           - Smart init: creates, migrates, or syncs
#   workspace_migrate             - Move loose .giantmem files to subdirs
#   workspace_tree [dir]          - Generate tree.md
#   workspace_discover "note"     - Add discovery note
#   workspace_session_note [note] - Add session marker/note
#   workspace_complete            - Mark workspace complete
#   workspace_status              - Show workspace status
#   workspace_sync                - Refresh tree + git log
#   workspace_gitlog              - Update git-log.md
#   workspace_archive [src] [proj] [branch] - Archive to ~/giantmem_archive/
#   workspace_archive_list [proj] - List archives
#   workspace_archive_open <proj> [branch] [ts] - Open archive in Finder
#   list-features [--dir <path>]  - Show feature status table
#
# Archive location: ~/giantmem_archive/{project}/{branch}/{timestamp}/
#
# Aliases (add to .bashrc):
#   alias wsb='workspace_bootstrap'  # Smart bootstrap mid-session
#   alias wsm='workspace_migrate'    # Migrate loose files
#   alias wsa='workspace_archive'    # Archive current .giantmem
#   alias wsal='workspace_archive_list' # List archives
#   alias wsf='workspace_features'   # List features

# Migrate scratch/ to .giantmem/ if needed
workspace_migrate_dir() {
    local target_dir="${1:-$PWD}"
    local old_dir="$target_dir/scratch"
    local new_dir="$target_dir/.giantmem"

    # only migrate if scratch/ exists as a real dir and .giantmem/ does not exist
    [ -d "$old_dir" ] && [ ! -L "$old_dir" ] && [ ! -d "$new_dir" ] || return 0

    echo "Migrating scratch/ -> .giantmem/ ..."
    mv "$old_dir" "$new_dir"
    ln -s .giantmem "$old_dir"
    echo "  moved scratch -> .giantmem (symlink left for compat)"

    # update .gitignore if it has a scratch/ entry
    local gitignore="$target_dir/.gitignore"
    if [ -f "$gitignore" ] && grep -q '^scratch/' "$gitignore"; then
        if ! grep -q '^\.giantmem/' "$gitignore"; then
            sed -i.bak '/^scratch\//a\
.giantmem/' "$gitignore" && rm -f "$gitignore.bak"
            echo "  added .giantmem/ to .gitignore"
        fi
    fi
}

# Initialize workspace structure in a directory
workspace_init() {
    local target_dir="${1:-$PWD}"
    local name="${2:-$(basename "$target_dir")}"
    local scratch_dir="$target_dir/.giantmem"

    # migrate scratch -> .giantmem if needed
    workspace_migrate_dir "$target_dir"

    # Create workspace structure
    mkdir -p "$scratch_dir"/{context,plans,history,filebox,prompts,research,reviews,features}

    # Create WORKSPACE.md if it doesn't exist
    if [ ! -f "$scratch_dir/WORKSPACE.md" ]; then
        cat > "$scratch_dir/WORKSPACE.md" << EOF
# Workspace: $name
Started: $(date '+%Y-%m-%d')
Status: [ ] In Progress  [ ] Complete

## Purpose
<!-- Describe what this branch/project is for -->

## Discoveries
<!-- Learnings about the codebase relevant to this work -->

## Notes
<!-- Session notes, decisions, context -->
EOF
        echo "Created .giantmem/WORKSPACE.md"
    fi

    # Create features/_index.md if missing
    if [ ! -f "$scratch_dir/features/_index.md" ]; then
        cat > "$scratch_dir/features/_index.md" << 'EOF'
# Feature Index

<!-- Claude maintains this file. Use /list-features to display, /new-feature to create -->

## Active Features

| Feature | Status | Beta Flag | Builds On |
|---------|--------|-----------|-----------|

## Quick Reference

<!-- beta flags, config keys, etc. added as features are created -->
EOF
        echo "Created .giantmem/features/_index.md"
    fi

    # Generate initial tree
    workspace_tree "$target_dir"

    echo "Workspace initialized in $scratch_dir"
}

# Generate tree structure
workspace_tree() {
    local target_dir="${1:-$PWD}"
    local context_dir="$target_dir/.giantmem/context"
    mkdir -p "$context_dir"

    if command -v tree &>/dev/null; then
        tree "$target_dir" -I 'node_modules|venv|__pycache__|.git|.giantmem|scratch|.bare|*.pyc|.venv|.tox|dist|build|*.egg-info' -L 4 --dirsfirst > "$context_dir/tree.md" 2>/dev/null
        echo "Updated .giantmem/context/tree.md"
    else
        # Fallback to find
        find "$target_dir" -maxdepth 4 -type f \
            -not -path '*/.git/*' \
            -not -path '*/.giantmem/*' \
            -not -path '*/scratch/*' \
            -not -path '*/node_modules/*' \
            -not -path '*/__pycache__/*' \
            -not -path '*/.venv/*' \
            -not -path '*/venv/*' \
            > "$context_dir/tree.md"
        echo "Updated .giantmem/context/tree.md (using find)"
    fi
}

# Add a discovery note
workspace_discover() {
    local scratch_dir="${PWD}/.giantmem"
    local discoveries="$scratch_dir/context/discoveries.md"

    if [ -z "$1" ]; then
        echo "Usage: workspace_discover 'your discovery note'"
        return 1
    fi

    mkdir -p "$(dirname "$discoveries")"
    echo "- $(date '+%Y-%m-%d %H:%M'): $*" >> "$discoveries"
    echo "Added to discoveries"
}

# Add a session note to history
workspace_session_note() {
    local scratch_dir="${PWD}/.giantmem"
    local history_file="$scratch_dir/history/sessions.md"

    mkdir -p "$(dirname "$history_file")"

    if [ -z "$1" ]; then
        # Just add a session marker
        echo "" >> "$history_file"
        echo "## Session: $(date '+%Y-%m-%d %H:%M')" >> "$history_file"
        echo "Added session marker"
    else
        # Add note to current session
        echo "- $*" >> "$history_file"
        echo "Added session note"
    fi
}

# Mark workspace as complete
workspace_complete() {
    local scratch_dir="${PWD}/.giantmem"
    local workspace_file="$scratch_dir/WORKSPACE.md"

    if [ -f "$workspace_file" ]; then
        # macOS sed vs GNU sed compatibility
        if sed --version 2>/dev/null | grep -q GNU; then
            sed -i 's/\[ \] In Progress/[x] Complete/' "$workspace_file"
        else
            sed -i '' 's/\[ \] In Progress/[x] Complete/' "$workspace_file"
        fi
        echo "Marked workspace as complete"
    else
        echo "No WORKSPACE.md found"
        return 1
    fi
}

# Show workspace status
workspace_status() {
    local scratch_dir="${PWD}/.giantmem"

    if [ ! -d "$scratch_dir" ]; then
        echo "No workspace found in current directory"
        return 1
    fi

    echo "=== Workspace Status ==="
    if [ -f "$scratch_dir/WORKSPACE.md" ]; then
        head -10 "$scratch_dir/WORKSPACE.md"
    fi

    echo ""
    echo "=== Files ==="
    for subdir in features context plans history prompts research reviews filebox; do
        if [ -d "$scratch_dir/$subdir" ]; then
            count=$(find "$scratch_dir/$subdir" -type f 2>/dev/null | wc -l | tr -d ' ')
            echo "  $subdir/: $count files"
        fi
    done

    if [ -f "$scratch_dir/context/discoveries.md" ]; then
        echo ""
        echo "=== Recent Discoveries ==="
        tail -5 "$scratch_dir/context/discoveries.md"
    fi
}

# List features from index
workspace_features() {
    local scratch_dir="${PWD}/.giantmem"
    local index="$scratch_dir/features/_index.md"

    if [ -f "$index" ]; then
        cat "$index"
    else
        echo "No features index found. Run workspace_init first."
        return 1
    fi
}

# Generate git log context
workspace_gitlog() {
    local scratch_dir="${PWD}/.giantmem"
    local context_dir="$scratch_dir/context"

    if ! git rev-parse --is-inside-work-tree &>/dev/null; then
        echo "Not in a git repository"
        return 1
    fi

    mkdir -p "$context_dir"
    git log --oneline -20 > "$context_dir/git-log.md"
    echo "Updated .giantmem/context/git-log.md"
}

# Sync all context (tree + git log)
workspace_sync() {
    workspace_tree
    if git rev-parse --is-inside-work-tree &>/dev/null; then
        workspace_gitlog
    fi
}

# Migrate existing .giantmem files to workspace structure
# Moves loose files into appropriate subdirectories
workspace_migrate() {
    local scratch_dir="${PWD}/.giantmem"
    local migrated=0
    local skipped=0

    if [ ! -d "$scratch_dir" ]; then
        echo "No .giantmem/ directory found. Run workspace_init first."
        return 1
    fi

    # Create structure if missing
    mkdir -p "$scratch_dir"/{context,plans,history,filebox,prompts,research,reviews,features}

    # Process each file in .giantmem root (not in subdirs)
    for file in "$scratch_dir"/*; do
        # Skip if not a file or is WORKSPACE.md
        [ ! -f "$file" ] && continue
        local basename=$(basename "$file")
        [ "$basename" = "WORKSPACE.md" ] && continue

        local dest=""
        local reason=""

        # Categorize by filename patterns
        case "$basename" in
            *plan*.md|*todo*.md|*steps*.md|*implementation*.md)
                dest="plans"
                reason="plan-related name"
                ;;
            *discover*.md|*finding*.md|*learn*.md|*context*.md)
                dest="context"
                reason="discovery-related name"
                ;;
            *history*.md|*session*.md|*log*.md|*journal*.md)
                dest="history"
                reason="history-related name"
                ;;
            *prompt*.md|*template*.md)
                dest="prompts"
                reason="prompt-related name"
                ;;
            *research*.md|*notes*.md|*reference*.md)
                dest="research"
                reason="research-related name"
                ;;
            *review*.md|*feedback*.md)
                dest="reviews"
                reason="review-related name"
                ;;
            tree.md|git-log.md|*.tree)
                dest="context"
                reason="context file"
                ;;
            *.md)
                # Check content for hints
                if grep -qiE 'plan|step|todo|implement' "$file" 2>/dev/null; then
                    dest="plans"
                    reason="content suggests plan"
                elif grep -qiE 'discover|found|learned|architecture|pattern' "$file" 2>/dev/null; then
                    dest="context"
                    reason="content suggests discovery"
                else
                    dest="filebox"
                    reason="unclassified markdown"
                fi
                ;;
            *)
                dest="filebox"
                reason="non-markdown file"
                ;;
        esac

        if [ -n "$dest" ]; then
            mv "$file" "$scratch_dir/$dest/"
            echo "  $basename -> $dest/ ($reason)"
            ((migrated++))
        fi
    done

    # Create WORKSPACE.md if missing
    if [ ! -f "$scratch_dir/WORKSPACE.md" ]; then
        local name=$(basename "$PWD")
        cat > "$scratch_dir/WORKSPACE.md" << EOF
# Workspace: $name
Started: $(date '+%Y-%m-%d')
Status: [ ] In Progress  [ ] Complete

## Purpose
<!-- Describe what this branch/project is for -->

## Discoveries
<!-- Learnings about the codebase relevant to this work -->

## Notes
<!-- Session notes, decisions, context -->
EOF
        echo "  Created WORKSPACE.md"
    fi

    # Generate tree if missing
    if [ ! -f "$scratch_dir/context/tree.md" ]; then
        workspace_tree
    fi

    if [ $migrated -gt 0 ]; then
        echo "Migrated $migrated files to workspace structure"
    else
        echo "No loose files to migrate"
    fi

    workspace_status
}

# Bootstrap workspace mid-session (init or migrate)
# Use this when starting workspace in existing session
workspace_bootstrap() {
    # migrate scratch -> .giantmem if needed
    workspace_migrate_dir "$PWD"

    local scratch_dir="${PWD}/.giantmem"

    if [ ! -d "$scratch_dir" ]; then
        # Fresh init
        echo "Initializing new workspace..."
        workspace_init
    elif [ ! -f "$scratch_dir/WORKSPACE.md" ]; then
        # Has .giantmem but no structure - migrate
        echo "Migrating existing .giantmem to workspace structure..."
        workspace_migrate
    else
        # Already has workspace - just sync
        echo "Workspace exists, syncing context..."
        workspace_sync
        workspace_status
    fi
}

# Central archive location
WORKSPACE_ARCHIVE_BASE="${GIANTMEM_ARCHIVE_BASE:-${SCRATCH_ARCHIVE_BASE:-$HOME/giantmem_archive}}"

# Archive .giantmem directory to central location
# Usage: workspace_archive [source] [project_name] [branch_name]
# Defaults: current .giantmem/, project from parent dir name, branch from git or dir name
workspace_archive() {
    local scratch_source="${1:-$PWD/.giantmem}"
    local project_name="${2:-$(basename "$(dirname "$scratch_source")")}"
    local branch_name="${3:-}"

    # Try to get branch name from git if not provided
    if [ -z "$branch_name" ]; then
        if git rev-parse --is-inside-work-tree &>/dev/null; then
            branch_name=$(git rev-parse --abbrev-ref HEAD 2>/dev/null)
        fi
        # Fallback to directory name
        [ -z "$branch_name" ] && branch_name=$(basename "$(dirname "$scratch_source")")
    fi

    if [ ! -d "$scratch_source" ]; then
        echo "No .giantmem directory found at: $scratch_source"
        return 1
    fi

    # Create archive path: ~/giantmem_archive/{project}/{branch}/{timestamp}/
    local archive_base="$WORKSPACE_ARCHIVE_BASE/$project_name"
    local branch_backup_dir="$archive_base/$branch_name"
    mkdir -p "$branch_backup_dir"

    local timestamp=$(date '+%Y%m%d_%H%M%S')
    local backup_dir="$branch_backup_dir/$timestamp"

    echo "Archiving .giantmem to: $backup_dir"
    if cp -r "$scratch_source" "$backup_dir"; then
        echo "Workspace archived successfully"

        # Update latest symlink
        local latest_link="$branch_backup_dir/latest"
        [ -L "$latest_link" ] && rm "$latest_link"
        ln -s "$timestamp" "$latest_link"
        echo "Created symlink: latest -> $timestamp"

        # Show size
        local size=$(du -sh "$backup_dir" 2>/dev/null | cut -f1)
        echo "Archive size: $size"

        # update fts5 search index
        local search_script="${GIANT_TOOLING_DIR:-$HOME/dev/giant-tooling}/scratch-archive/scratch-search.py"
        [ -f "$search_script" ] && python3 "$search_script" ingest --project "$project_name" 2>/dev/null &

        return 0
    else
        echo "ERROR: Failed to archive .giantmem directory"
        return 1
    fi
}

# List workspace archives for a project
# Usage: workspace_archive_list [project_name]
workspace_archive_list() {
    local project_name="${1:-}"
    local archive_dir="$WORKSPACE_ARCHIVE_BASE"

    if [ -n "$project_name" ]; then
        archive_dir="$archive_dir/$project_name"
    fi

    if [ ! -d "$archive_dir" ]; then
        echo "No archives found"
        return 0
    fi

    echo "Workspace archives in $archive_dir:"
    echo "────────────────────────────────────"

    if [ -n "$project_name" ]; then
        # List branches for specific project
        for branch_dir in "$archive_dir"/*; do
            [ -d "$branch_dir" ] && [ ! -L "$branch_dir" ] || continue
            local branch_name=$(basename "$branch_dir")
            echo ""
            echo "  $branch_name:"
            for backup in "$branch_dir"/*; do
                [ -d "$backup" ] && [ ! -L "$backup" ] || continue
                local backup_name=$(basename "$backup")
                local size=$(du -sh "$backup" 2>/dev/null | cut -f1)
                if [ -L "$branch_dir/latest" ] && [ "$(readlink "$branch_dir/latest")" = "$backup_name" ]; then
                    echo "    $backup_name ($size) <- latest"
                else
                    echo "    $backup_name ($size)"
                fi
            done
        done
    else
        # List all projects
        for project_dir in "$archive_dir"/*; do
            [ -d "$project_dir" ] || continue
            local proj=$(basename "$project_dir")
            local count=$(find "$project_dir" -mindepth 2 -maxdepth 2 -type d -name "[0-9]*_[0-9]*" 2>/dev/null | wc -l | tr -d ' ')
            echo "  $proj/: $count backups"
        done
    fi
}

# Open/browse a workspace archive
# Usage: workspace_archive_open <project> [branch] [timestamp]
workspace_archive_open() {
    if [ -z "$1" ]; then
        echo "Usage: workspace_archive_open <project> [branch] [timestamp]"
        echo "  Opens the archive directory"
        return 1
    fi

    local project="$1"
    local branch="${2:-}"
    local timestamp="${3:-}"
    local target_dir="$WORKSPACE_ARCHIVE_BASE/$project"

    [ -n "$branch" ] && target_dir="$target_dir/$branch"
    [ -n "$timestamp" ] && target_dir="$target_dir/$timestamp"

    # If branch specified but no timestamp, use latest
    if [ -n "$branch" ] && [ -z "$timestamp" ] && [ -L "$target_dir/latest" ]; then
        target_dir="$target_dir/latest"
    fi

    if [ ! -d "$target_dir" ] && [ ! -L "$target_dir" ]; then
        echo "Archive not found: $target_dir"
        return 1
    fi

    echo "Opening: $target_dir"
    open "$target_dir"
}

# list features with rich table output
list-features() {
    local src="${BASH_SOURCE[0]}"
    while [ -L "$src" ]; do src="$(readlink "$src")"; done
    local script_dir="$(cd "$(dirname "$src")" && pwd)"
    "$script_dir/list-features.sh" "$@"
}

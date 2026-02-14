#!/bin/bash

# Git Worktree Helper Functions - Generic Template
# ================================================
#
# USAGE:
# 1. Copy this template: cp worktree-helper-template.sh worktree-myproject.sh
# 2. Edit the copy and replace YOUR-PROJECT with your project name
# 3. Customize the _wt_setup_worktree() function for your project needs
# 4. Source in your shell: source ~/path/to/worktree-myproject.sh
#
# NAMING CONVENTION:
# - Template: worktree-helper-template.sh (this file - DO NOT source directly)
# - Your copy: worktree-{projectname}.sh (e.g., worktree-webapp.sh)
#
# SETUP:
# Your project structure should look like:
#   ~/dev/myproject-worktree/
#   ├── .bare/          (bare git repo)
#   ├── bootstrap/      (optional: files to copy to new worktrees)
#   ├── main/           (worktree for main branch)
#   ├── feature-x/      (worktree for feature branch)
#   └── .last-branch    (tracks last visited worktree)

# CHANGE THIS to match your worktree base directory
# Example: WORKTREE_BASE="$HOME/dev/myproject-worktree"
WORKTREE_BASE="$HOME/dev/YOUR-PROJECT-worktree"

# Source workspace library (for scratch/ workspace management)
WORKSPACE_LIB="${GIANT_TOOLING_DIR:-$HOME/dev/giant-tooling}/workspace/workspace-lib.sh"
[ -f "$WORKSPACE_LIB" ] && source "$WORKSPACE_LIB"
WORKTREE_LAST_FILE="$WORKTREE_BASE/.last-branch"

# Helper: Get current branch name
_wt_current_branch() {
    git rev-parse --abbrev-ref HEAD 2>/dev/null
}

# Helper: Check if we're in a worktree
_wt_in_worktree() {
    [[ "$PWD" == "$WORKTREE_BASE/"* ]] && [ -d ".git" -o -f ".git" ]
}

# Helper: Get branch status vs remote
_wt_branch_status() {
    local branch="$1"
    local upstream=$(git rev-parse --abbrev-ref "$branch@{upstream}" 2>/dev/null)
    
    if [ -z "$upstream" ]; then
        echo "(no upstream)"
        return
    fi
    
    local ahead=$(git rev-list --count "$upstream..$branch" 2>/dev/null)
    local behind=$(git rev-list --count "$branch..$upstream" 2>/dev/null)
    
    if [ "$ahead" -eq 0 ] && [ "$behind" -eq 0 ]; then
        echo "(up to date)"
    elif [ "$ahead" -gt 0 ] && [ "$behind" -eq 0 ]; then
        echo "(ahead $ahead)"
    elif [ "$ahead" -eq 0 ] && [ "$behind" -gt 0 ]; then
        echo "(behind $behind)"
    else
        echo "(diverged: ↑$ahead ↓$behind)"
    fi
}

# Helper: Setup worktree after creation
_wt_setup_worktree() {
    local branch="$1"
    local target_dir="$WORKTREE_BASE/$branch"
    local bootstrap_dir="$WORKTREE_BASE/bootstrap"

    # Create .python-version file for pyenv
    echo "3.11.12" > "$target_dir/.python-version"
    echo "Created .python-version (3.11.12)"

    # =====================================================
    # CUSTOM SETUP LOGIC - Customize for your project
    # =====================================================
    # This function runs after creating a new worktree.
    # Uncomment and modify the examples below as needed.
    
    # --- Bootstrap Files ---
    # Copy common config files from a bootstrap directory
    # if [ -d "$bootstrap_dir" ]; then
    #     echo "Copying bootstrap files..."
    #     # Copy all files including hidden ones
    #     cp -r "$bootstrap_dir"/* "$target_dir"/ 2>/dev/null
    #     cp -r "$bootstrap_dir"/.[^.]* "$target_dir"/ 2>/dev/null
    #     echo "✓ Bootstrap files copied"
    # fi
    
    # --- Environment Files ---
    # Copy .env from another worktree or bootstrap
    # if [ ! -f "$target_dir/.env" ]; then
    #     if [ -f "$bootstrap_dir/.env" ]; then
    #         cp "$bootstrap_dir/.env" "$target_dir/"
    #         echo "✓ .env copied from bootstrap"
    #     elif [ -f "$WORKTREE_BASE/main/.env" ]; then
    #         cp "$WORKTREE_BASE/main/.env" "$target_dir/"
    #         echo "✓ .env copied from main branch"
    #     fi
    # fi
    
    # --- Node.js Project ---
    # if [ -f "$target_dir/package.json" ]; then
    #     echo "Installing npm dependencies..."
    #     (cd "$target_dir" && npm install)
    #     echo "✓ Dependencies installed"
    # fi
    
    # --- Python Project with Poetry ---
    # if [ -f "$target_dir/pyproject.toml" ] && command -v poetry &>/dev/null; then
    #     echo "Setting up Poetry environment..."
    #     (cd "$target_dir" && poetry install)
    #     echo "✓ Poetry dependencies installed"
    #     
    #     # Install pre-commit hooks if configured
    #     if [ -f "$target_dir/.pre-commit-config.yaml" ]; then
    #         (cd "$target_dir" && poetry run pre-commit install)
    #         echo "✓ Pre-commit hooks installed"
    #     fi
    # fi
    
    # --- Python Project with venv ---
    # if [ -f "$target_dir/requirements.txt" ]; then
    #     echo "Setting up Python virtual environment..."
    #     (cd "$target_dir" && python -m venv venv && source venv/bin/activate && pip install -r requirements.txt)
    #     echo "✓ Python environment ready"
    # fi
    
    # --- Ruby Project ---
    # if [ -f "$target_dir/Gemfile" ]; then
    #     echo "Installing Ruby gems..."
    #     (cd "$target_dir" && bundle install)
    #     echo "✓ Gems installed"
    # fi
    
    # --- Docker Compose ---
    # if [ -f "$target_dir/docker-compose.yml" ]; then
    #     echo "Building Docker containers..."
    #     (cd "$target_dir" && docker-compose build)
    #     echo "✓ Docker containers built"
    # fi
    
    # --- Makefile Setup ---
    # if [ -f "$target_dir/Makefile" ] && grep -q "^setup:" "$target_dir/Makefile"; then
    #     echo "Running make setup..."
    #     (cd "$target_dir" && make setup)
    #     echo "✓ Setup completed"
    # fi
    
    # --- Git Hooks ---
    # if command -v pre-commit &>/dev/null && [ -f "$target_dir/.pre-commit-config.yaml" ]; then
    #     echo "Installing pre-commit hooks..."
    #     (cd "$target_dir" && pre-commit install)
    #     echo "✓ Pre-commit hooks installed"
    # fi
    
    # --- Database Setup ---
    # if [ -f "$target_dir/database.yml" ]; then
    #     echo "Setting up database..."
    #     (cd "$target_dir" && bundle exec rake db:create db:migrate)
    #     echo "✓ Database ready"
    # fi
    
    # =====================================================
    # Add your own custom setup steps here!
    # =====================================================

    # Initialize workspace (creates scratch/ with structure)
    if type workspace_init &>/dev/null; then
        workspace_init "$target_dir" "$branch"
    fi

    echo "Worktree setup completed for branch: $branch"
}

# Main function to change to worktree directories or create new ones
wt() {
    if [ -z "$1" ]; then
        # No argument - go to last branch
        if [ -f "$WORKTREE_LAST_FILE" ]; then
            last_branch=$(cat "$WORKTREE_LAST_FILE")
            if [ -d "$WORKTREE_BASE/$last_branch" ]; then
                # Save current branch before switching
                if _wt_in_worktree; then
                    current_branch=$(basename "$PWD")
                    if [ "$current_branch" != "$last_branch" ]; then
                        echo "$current_branch" >"$WORKTREE_LAST_FILE"
                    fi
                fi
                cd "$WORKTREE_BASE/$last_branch"
                echo "➜ $last_branch $(_wt_branch_status $(_wt_current_branch))"
            else
                echo "Last branch '$last_branch' no longer exists"
                cd "$WORKTREE_BASE"
            fi
        else
            echo "No last branch recorded"
            cd "$WORKTREE_BASE"
        fi
    else
        # Argument provided
        if [ -d "$WORKTREE_BASE/$1" ]; then
            # Worktree exists - just go there
            if _wt_in_worktree; then
                current_branch=$(basename "$PWD")
                if [ "$current_branch" != "$1" ]; then
                    echo "$current_branch" >"$WORKTREE_LAST_FILE"
                fi
            fi
            cd "$WORKTREE_BASE/$1"
            echo "➜ $1 $(_wt_branch_status $(_wt_current_branch))"
        else
            # Worktree doesn't exist - create it
            echo "Worktree '$1' not found. Creating..."
            
            cd "$WORKTREE_BASE/.bare" || {
                echo "Error: Cannot access bare repository at $WORKTREE_BASE/.bare"
                return 1
            }
            
            # Determine how to create the worktree
            local created=0
            local error_msg=""
            
            # Case 1: Local branch exists
            if git rev-parse --verify "$1" >/dev/null 2>&1; then
                echo "Found local branch '$1'"
                echo "Create worktree for branch '$1'? (y/N)"
                read -r response
                if [[ ! "$response" =~ ^[Yy]$ ]]; then
                    echo "Cancelled."
                    return 1
                fi
                if git worktree add "../$1" "$1" 2>/dev/null; then
                    echo "✓ Added worktree for existing local branch '$1'"
                    created=1
                else
                    # Check if worktree already exists for this branch
                    if git worktree list | grep -q "/$1 "; then
                        error_msg="Branch '$1' is already checked out in another worktree"
                    else
                        error_msg="Failed to add worktree for branch '$1'"
                    fi
                fi
                
            # Case 2: Remote branch exists
            elif git rev-parse --verify "origin/$1" >/dev/null 2>&1; then
                echo "Found remote branch 'origin/$1'"
                echo "Create worktree for branch '$1' tracking origin/$1? (y/N)"
                read -r response
                if [[ ! "$response" =~ ^[Yy]$ ]]; then
                    echo "Cancelled."
                    return 1
                fi
                if git worktree add -b "$1" "../$1" "origin/$1" 2>/dev/null; then
                    echo "✓ Created local branch '$1' tracking origin/$1"
                    created=1
                else
                    error_msg="Failed to create worktree from remote branch 'origin/$1'"
                fi
                
            # Case 3: Branch doesn't exist - create new
            else
                echo "Branch '$1' not found locally or remotely"
                # Get default branch
                local default_branch=$(git symbolic-ref refs/remotes/origin/HEAD 2>/dev/null | sed 's@^refs/remotes/origin/@@')
                if [ -z "$default_branch" ]; then
                    for branch in main master develop; do
                        if git rev-parse --verify "origin/$branch" >/dev/null 2>&1; then
                            default_branch="$branch"
                            break
                        fi
                    done
                fi
                
                if [ -z "$default_branch" ]; then
                    error_msg="Could not determine default branch"
                else
                    echo "Create new worktree for branch '$1' from $default_branch? (y/N)"
                    read -r response
                    if [[ ! "$response" =~ ^[Yy]$ ]]; then
                        echo "Cancelled."
                        return 1
                    fi
                    if git worktree add -b "$1" "../$1" "origin/$default_branch" 2>/dev/null; then
                        echo "✓ Created new branch '$1' from $default_branch"
                        created=1
                    else
                        error_msg="Failed to create new branch '$1' from $default_branch"
                    fi
                fi
            fi
            
            # Handle success or failure
            if [ $created -eq 1 ]; then
                echo "$1" >"$WORKTREE_LAST_FILE"
                
                # Set upstream for new branches
                if [[ "$created" -eq 1 ]] && [[ ! "$error_msg" =~ "existing local branch" ]]; then
                    cd "$WORKTREE_BASE/$1"
                    echo "Setting upstream to origin/$1..."
                    if git push -u origin "$1" 2>/dev/null; then
                        echo "✓ Upstream set to origin/$1"
                    else
                        echo "Note: Could not push to origin (may need permissions or remote setup)"
                    fi
                fi
                
                _wt_setup_worktree "$1"
                cd "$WORKTREE_BASE/$1"
                echo "➜ $1 (ready)"
            else
                echo "Error: $error_msg"
                # Suggest recovery options
                if [[ "$error_msg" == *"already checked out"* ]]; then
                    echo "Hint: Use 'wtl' to see all worktrees"
                elif [[ "$error_msg" == *"Failed to create worktree"* ]]; then
                    echo "Hint: Try 'git fetch --all' first, or check 'git worktree list'"
                fi
                return 1
            fi
        fi
    fi
}

# List all worktrees with branch status
wtl() {
    local original_dir="$PWD"
    echo "Worktrees in $WORKTREE_BASE:"
    echo "────────────────────────────────────"
    
    cd "$WORKTREE_BASE/.bare" 2>/dev/null || {
        echo "Error: Cannot access bare repository"
        return 1
    }
    
    git worktree list | while read -r line; do
        if [[ "$line" == *"(bare)" ]]; then
            echo "$line"
        else
            # Extract path and branch
            local path=$(echo "$line" | awk '{print $1}')
            local branch=$(echo "$line" | sed -n 's/.*\[\(.*\)\].*/\1/p')
            
            if [ -n "$branch" ] && [ -d "$path" ]; then
                # Get status in that worktree
                local status=$(cd "$path" && _wt_branch_status "$branch" 2>/dev/null || echo "")
                echo "$line $status"
            else
                echo "$line"
            fi
        fi
    done
    
    # Return to original directory
    cd "$original_dir"
}

# List branches (local and remote) with worktree info
wtb() {
    local original_dir="$PWD"
    cd "$WORKTREE_BASE/.bare" 2>/dev/null || {
        echo "Error: Cannot access bare repository"
        return 1
    }
    
    echo "Local branches:"
    echo "──────────────"
    git branch --format='%(refname:short)' | while read -r branch; do
        local wt_path=$(git worktree list --porcelain | grep -B2 "branch refs/heads/$branch" | grep "^worktree" | cut -d' ' -f2)
        if [ -n "$wt_path" ]; then
            echo "  $branch → $(basename "$wt_path")"
        else
            echo "  $branch"
        fi
    done
    
    echo -e "\nRemote branches (origin):"
    echo "────────────────────────"
    git branch -r --format='%(refname:short)' | grep "^origin/" | grep -v "HEAD" | sed 's/^origin\//  /'
    
    # Return to original directory
    cd "$original_dir"
}

# Pull updates for current worktree branch
wtp() {
    if ! _wt_in_worktree; then
        echo "Error: Not in a worktree"
        return 1
    fi
    
    local branch=$(_wt_current_branch)
    echo "Pulling updates for '$branch'..."
    
    if git pull --ff-only 2>&1; then
        echo "✓ Updated '$branch'"
        _wt_branch_status "$branch"
    else
        echo "⚠ Pull failed - you may need to merge or rebase"
        echo "Hint: Use 'git pull --rebase' or resolve conflicts"
    fi
}

# Add a new worktree (explicit command)
wta() {
    if [ -z "$1" ]; then
        echo "Usage: wta <branch-name> [base-branch]"
        return 1
    fi
    
    cd "$WORKTREE_BASE/.bare" || {
        echo "Error: Cannot access bare repository"
        return 1
    }
    
    local created=0
    local error_msg=""
    
    if [ -z "$2" ]; then
        # No base branch specified - same logic as wt()
        if git rev-parse --verify "$1" >/dev/null 2>&1; then
            echo "Found local branch '$1'"
            echo "Create worktree for branch '$1'? (y/N)"
            read -r response
            if [[ ! "$response" =~ ^[Yy]$ ]]; then
                echo "Cancelled."
                return 1
            fi
            if git worktree add "../$1" "$1" 2>/dev/null; then
                echo "✓ Added worktree for existing local branch '$1'"
                created=1
            else
                if git worktree list | grep -q "/$1 "; then
                    error_msg="Branch '$1' is already checked out in another worktree"
                else
                    error_msg="Failed to add worktree for branch '$1'"
                fi
            fi
        elif git rev-parse --verify "origin/$1" >/dev/null 2>&1; then
            echo "Found remote branch 'origin/$1'"
            echo "Create worktree for branch '$1' tracking origin/$1? (y/N)"
            read -r response
            if [[ ! "$response" =~ ^[Yy]$ ]]; then
                echo "Cancelled."
                return 1
            fi
            if git worktree add -b "$1" "../$1" "origin/$1" 2>/dev/null; then
                echo "✓ Created local branch '$1' tracking origin/$1"
                created=1
            else
                error_msg="Failed to create worktree from remote branch"
            fi
        else
            # Get default branch
            local default_branch=$(git symbolic-ref refs/remotes/origin/HEAD 2>/dev/null | sed 's@^refs/remotes/origin/@@')
            if [ -z "$default_branch" ]; then
                for branch in main master develop; do
                    if git rev-parse --verify "origin/$branch" >/dev/null 2>&1; then
                        default_branch="$branch"
                        break
                    fi
                done
            fi
            
            if [ -z "$default_branch" ]; then
                error_msg="Could not determine default branch"
            else
                echo "Create new worktree for branch '$1' from $default_branch? (y/N)"
                read -r response
                if [[ ! "$response" =~ ^[Yy]$ ]]; then
                    echo "Cancelled."
                    return 1
                fi
                if git worktree add -b "$1" "../$1" "origin/$default_branch" 2>/dev/null; then
                    echo "✓ Created new branch '$1' from $default_branch"
                    created=1
                else
                    error_msg="Failed to create new branch"
                fi
            fi
        fi
    else
        # Base branch specified
        if git rev-parse --verify "$2" >/dev/null 2>&1; then
            echo "Create new worktree for branch '$1' from $2? (y/N)"
            read -r response
            if [[ ! "$response" =~ ^[Yy]$ ]]; then
                echo "Cancelled."
                return 1
            fi
            if git worktree add -b "$1" "../$1" "$2" 2>/dev/null; then
                echo "✓ Created new branch '$1' from $2"
                created=1
            else
                error_msg="Failed to create branch from '$2'"
            fi
        else
            error_msg="Base branch '$2' not found"
        fi
    fi
    
    if [ $created -eq 1 ]; then
        echo "$1" >"$WORKTREE_LAST_FILE"
        
        # Set upstream for new branches
        cd "$WORKTREE_BASE/$1"
        local needs_upstream=0
        
        # Check if this is a new branch that needs upstream
        if git branch -vv | grep -q "^\* $1 .*\[origin/$1\]"; then
            echo "Branch already tracking origin/$1"
        else
            needs_upstream=1
        fi
        
        if [ $needs_upstream -eq 1 ]; then
            echo "Setting upstream to origin/$1..."
            if git push -u origin "$1" 2>/dev/null; then
                echo "✓ Upstream set to origin/$1"
            else
                echo "Note: Could not push to origin (may need permissions or remote setup)"
            fi
        fi
        
        _wt_setup_worktree "$1"
        cd "$WORKTREE_BASE/$1"
        echo "➜ $1 (created)"
    else
        echo "Error: $error_msg"
        return 1
    fi
}

# Remove a worktree
wtr() {
    if [ -z "$1" ]; then
        echo "Usage: wtr <branch-name> [-f|--force]"
        return 1
    fi
    
    local branch_name="$1"
    local worktree_dir="$WORKTREE_BASE/$branch_name"
    local scratch_source="$worktree_dir/scratch"
    local scratch_backup_base="${SCRATCH_ARCHIVE_BASE:-$HOME/scratch_archive}/$(basename "$WORKTREE_BASE")"
    
    # Confirmation prompt (unless force flag is used)
    if [ "$2" != "-f" ] && [ "$2" != "--force" ]; then
        echo "Are you sure you want to delete worktree '$branch_name'? (y/N)"
        read -r response
        if [[ ! "$response" =~ ^[Yy]$ ]]; then
            echo "Cancelled"
            return 0
        fi
    fi
    
    # Check if the worktree has a scratch directory to backup
    if [ -d "$scratch_source" ]; then
        # Create the backup base and branch directories if they don't exist
        local branch_backup_dir="$scratch_backup_base/$branch_name"
        mkdir -p "$branch_backup_dir"
        
        # Create a timestamped backup directory name
        local timestamp=$(date '+%Y%m%d_%H%M%S')
        local backup_dir="$branch_backup_dir/$timestamp"
        
        echo "Backing up scratch directory to: $backup_dir"
        if cp -r "$scratch_source" "$backup_dir"; then
            echo "✓ Scratch directory backed up successfully"
            
            # Create/update a symlink called 'latest' within the branch directory
            # This allows multiple backups per branch with easy access to most recent
            local latest_link="$branch_backup_dir/latest"
            if [ -L "$latest_link" ]; then
                rm "$latest_link"
            fi
            ln -s "$timestamp" "$latest_link"
            echo "✓ Created symlink: $latest_link -> $timestamp"

            # update fts5 search index
            local search_script="${GIANT_TOOLING_DIR:-$HOME/dev/giant-tooling}/scratch-archive/scratch-search.py"
            [ -f "$search_script" ] && python3 "$search_script" ingest --project "$(basename "$WORKTREE_BASE")" 2>/dev/null &
        else
            echo "❌ ERROR: Failed to backup scratch directory"
            echo "Worktree removal cancelled to preserve your work"
            return 1
        fi
    fi
    
    cd "$WORKTREE_BASE/.bare" || {
        echo "Error: Cannot access bare repository"
        return 1
    }
    
    local force=""
    if [ "$2" = "-f" ] || [ "$2" = "--force" ]; then
        force="--force"
    fi
    
    if git worktree remove $force "../$1" 2>&1; then
        echo "✓ Removed worktree '$1'"
        
        # Clear last branch if we removed it
        if [ -f "$WORKTREE_LAST_FILE" ]; then
            last_branch=$(cat "$WORKTREE_LAST_FILE")
            if [ "$last_branch" = "$1" ]; then
                rm "$WORKTREE_LAST_FILE"
            fi
        fi
    else
        echo "Error: Failed to remove worktree '$1'"
        echo "Hint: Use 'wtr $1 --force' to force removal"
        return 1
    fi
}

# Rename a worktree (directory + branch + upstream + archives)
wtrn() {
    if [ -z "$1" ] || [ -z "$2" ]; then
        echo "Usage: wtrn <old-name> <new-name>"
        return 1
    fi

    local old_name="$1"
    local new_name="$2"
    local old_dir="$WORKTREE_BASE/$old_name"
    local new_dir="$WORKTREE_BASE/$new_name"

    if [ ! -d "$old_dir" ]; then
        echo "Error: Worktree '$old_name' not found"
        return 1
    fi

    if [ -d "$new_dir" ]; then
        echo "Error: '$new_name' already exists"
        return 1
    fi

    if [[ "$PWD" == "$old_dir"* ]]; then
        echo "Error: Cannot rename worktree you're currently in"
        echo "Hint: Switch to another worktree first"
        return 1
    fi

    cd "$WORKTREE_BASE/.bare" || {
        echo "Error: Cannot access bare repository"
        return 1
    }

    # move worktree directory
    if ! git worktree move "../$old_name" "../$new_name" 2>&1; then
        echo "Error: Failed to move worktree"
        return 1
    fi
    echo "✓ Worktree moved: $old_name -> $new_name"

    # rename the branch
    if git branch -m "$old_name" "$new_name" 2>&1; then
        echo "✓ Branch renamed: $old_name -> $new_name"
    else
        echo "Note: Could not rename branch (may already have the target name)"
    fi

    # update upstream
    cd "$new_dir"
    if git push -u origin "$new_name" 2>/dev/null; then
        echo "✓ Upstream set to origin/$new_name"
        if git push origin --delete "$old_name" 2>/dev/null; then
            echo "✓ Deleted old remote branch origin/$old_name"
        else
            echo "Note: Could not delete old remote branch origin/$old_name"
        fi
    else
        echo "Note: Could not push new branch name to origin"
    fi

    # update .last-branch
    if [ -f "$WORKTREE_LAST_FILE" ]; then
        if [ "$(cat "$WORKTREE_LAST_FILE")" = "$old_name" ]; then
            echo "$new_name" > "$WORKTREE_LAST_FILE"
            echo "✓ Updated last-branch reference"
        fi
    fi

    # migrate scratch archives
    local scratch_archive_base="${SCRATCH_ARCHIVE_BASE:-$HOME/scratch_archive}/$(basename "$WORKTREE_BASE")"
    local old_archive="$scratch_archive_base/$old_name"
    local new_archive="$scratch_archive_base/$new_name"
    if [ -d "$old_archive" ]; then
        if [ -d "$new_archive" ]; then
            echo "Warning: Scratch archive '$new_name' already exists, skipping archive migration"
        else
            mv "$old_archive" "$new_archive"
            echo "✓ Migrated scratch archives: $old_name -> $new_name"
        fi
    fi

    echo "➜ Renamed '$old_name' to '$new_name'"
}

# Quick status of current worktree
wts() {
    if ! _wt_in_worktree; then
        echo "Not in a git worktree"
        return 1
    fi
    
    local branch=$(_wt_current_branch)
    local status=$(_wt_branch_status "$branch")
    
    echo "Branch: $branch $status"
    git status -sb
}

# Fetch updates for all branches
wtf() {
    local original_dir="$PWD"
    cd "$WORKTREE_BASE/.bare" || {
        echo "Error: Cannot access bare repository"
        return 1
    }
    
    echo "Fetching all remotes..."
    if git fetch --all --prune; then
        echo "✓ Fetch completed"
        
        # Show branches with updates
        echo -e "\nBranches with remote changes:"
        git for-each-ref --format='%(refname:short) %(upstream:short)' refs/heads | while read local upstream; do
            if [ -n "$upstream" ]; then
                behind=$(git rev-list --count "$local..$upstream" 2>/dev/null)
                if [ "$behind" -gt 0 ]; then
                    echo "  $local is behind $upstream by $behind commits"
                fi
            fi
        done
    else
        echo "Error: Fetch failed"
        cd "$original_dir"
        return 1
    fi
    
    # Return to original directory
    cd "$original_dir"
}

# Prune worktrees that no longer exist
wtprune() {
    local original_dir="$PWD"
    cd "$WORKTREE_BASE/.bare" || {
        echo "Error: Cannot access bare repository"
        return 1
    }
    
    echo "Pruning stale worktrees..."
    if git worktree prune -v; then
        echo "✓ Prune completed"
    else
        echo "Error: Prune failed"
        cd "$original_dir"
        return 1
    fi
    
    # Return to original directory
    cd "$original_dir"
}

# Auto-complete for wt functions
_wt_complete() {
    local cur="${COMP_WORDS[COMP_CWORD]}"
    # List directories, excluding .bare, .last-branch, bootstrap, and any other non-worktree dirs
    local branches=""
    if [ -d "$WORKTREE_BASE" ]; then
        for dir in "$WORKTREE_BASE"/*; do
            if [ -d "$dir" ]; then
                local basename=$(basename "$dir")
                case "$basename" in
                    .bare|.last-branch|bootstrap|scratch)
                        # Skip these directories
                        ;;
                    *)
                        branches="$branches $basename"
                        ;;
                esac
            fi
        done
    fi
    COMPREPLY=($(compgen -W "$branches" -- "$cur"))
}

_wt_complete_all_branches() {
    local cur="${COMP_WORDS[COMP_CWORD]}"
    cd "$WORKTREE_BASE/.bare" 2>/dev/null || return
    
    # Get all local and remote branches
    local branches=$(git branch -a --format='%(refname:short)' | sed 's/^origin\///' | sort -u)
    COMPREPLY=($(compgen -W "$branches" -- "$cur"))
}

# List scratch backups
wtsl() {
    local scratch_dir="${SCRATCH_ARCHIVE_BASE:-$HOME/scratch_archive}/$(basename "$WORKTREE_BASE")"
    
    if [ ! -d "$scratch_dir" ]; then
        echo "No scratch backups found"
        return 0
    fi
    
    echo "Scratch backups in $scratch_dir:"
    echo "────────────────────────────────────"
    
    # List each branch that has backups
    for branch_dir in "$scratch_dir"/*; do
        if [ -d "$branch_dir" ] && [ ! -L "$branch_dir" ]; then
            local branch_name=$(basename "$branch_dir")
            echo ""
            echo "  $branch_name:"
            
            # List timestamped backups for this branch
            for backup in "$branch_dir"/*; do
                if [ -d "$backup" ] && [[ "$(basename "$backup")" =~ ^[0-9]{8}_[0-9]{6}$ ]]; then
                    local timestamp=$(basename "$backup")
                    local size=$(du -sh "$backup" 2>/dev/null | cut -f1)
                    
                    # Format the timestamp
                    local date_part=${timestamp%_*}
                    local time_part=${timestamp#*_}
                    local formatted_date="${date_part:0:4}-${date_part:4:2}-${date_part:6:2}"
                    local formatted_time="${time_part:0:2}:${time_part:2:2}:${time_part:4:2}"
                    
                    echo "    - $formatted_date $formatted_time ($size)"
                    
                    # Check if this is the latest
                    if [ -L "$branch_dir/latest" ]; then
                        local link_target=$(readlink "$branch_dir/latest")
                        if [ "$link_target" = "$timestamp" ]; then
                            echo "      └─ (latest)"
                        fi
                    fi
                fi
            done
        fi
    done
    
    # Show total count
    local total_backups=$(find "$scratch_dir" -mindepth 2 -maxdepth 2 -type d -name "[0-9]*_[0-9]*" | wc -l | tr -d ' ')
    echo ""
    echo "Total backups: $total_backups"
}

# Manually backup scratch directory
wtsb() {
    if [ -z "$1" ]; then
        # If no argument, try to backup current worktree
        if _wt_in_worktree; then
            local branch_name=$(basename "$PWD")
        else
            echo "Usage: wtsb [branch-name]"
            echo "  Backs up the scratch directory for a worktree"
            echo "  If no branch specified and in a worktree, uses current branch"
            return 1
        fi
    else
        local branch_name="$1"
    fi
    
    local worktree_dir="$WORKTREE_BASE/$branch_name"
    local scratch_source="$worktree_dir/scratch"
    local scratch_backup_base="${SCRATCH_ARCHIVE_BASE:-$HOME/scratch_archive}/$(basename "$WORKTREE_BASE")"
    
    # Check if worktree exists
    if [ ! -d "$worktree_dir" ]; then
        echo "Error: Worktree '$branch_name' not found"
        return 1
    fi
    
    # Check if scratch directory exists
    if [ ! -d "$scratch_source" ]; then
        echo "No scratch directory found in '$branch_name'"
        return 1
    fi
    
    # Create the backup base and branch directories if they don't exist
    local branch_backup_dir="$scratch_backup_base/$branch_name"
    mkdir -p "$branch_backup_dir"
    
    # Create a timestamped backup directory name
    local timestamp=$(date '+%Y%m%d_%H%M%S')
    local backup_dir="$branch_backup_dir/$timestamp"
    
    echo "Backing up scratch directory to: $backup_dir"
    if cp -r "$scratch_source" "$backup_dir"; then
        echo "✓ Scratch directory backed up successfully"
        
        # Create/update a symlink called 'latest' within the branch directory
        local latest_link="$branch_backup_dir/latest"
        if [ -L "$latest_link" ]; then
            rm "$latest_link"
        fi
        ln -s "$timestamp" "$latest_link"
        echo "✓ Created symlink: $latest_link -> $timestamp"
        
        # Show size of backup
        local size=$(du -sh "$backup_dir" 2>/dev/null | cut -f1)
        echo "✓ Backup size: $size"
    else
        echo "❌ ERROR: Failed to backup scratch directory"
        return 1
    fi
}

# Open/browse a scratch backup
wtso() {
    if [ -z "$1" ]; then
        echo "Usage: wtso <branch-name> [timestamp]"
        echo "  Opens the scratch backup directory for a branch"
        echo "  If no timestamp specified, opens the latest (symlink)"
        return 1
    fi
    
    local scratch_dir="${SCRATCH_ARCHIVE_BASE:-$HOME/scratch_archive}/$(basename "$WORKTREE_BASE")"
    local branch_dir="$scratch_dir/$1"
    
    # Check if branch has any backups
    if [ ! -d "$branch_dir" ]; then
        echo "Error: No scratch backups found for branch '$1'"
        echo "Hint: Use 'wtsl' to see available backups"
        return 1
    fi
    
    local target_dir=""
    if [ -n "$2" ]; then
        # Specific timestamp requested
        target_dir="$branch_dir/$2"
    else
        # Use the latest symlink
        target_dir="$branch_dir/latest"
        if [ ! -L "$target_dir" ]; then
            # If no latest symlink, try to find the most recent backup
            local latest_backup=$(ls -1d "$branch_dir"/[0-9]*_[0-9]* 2>/dev/null | sort -r | head -n1)
            if [ -n "$latest_backup" ]; then
                target_dir="$latest_backup"
            else
                echo "Error: No backups found for branch '$1'"
                return 1
            fi
        fi
    fi
    
    if [ -d "$target_dir" ] || [ -L "$target_dir" ]; then
        cd "$target_dir"
        echo "➜ $(pwd)"
    else
        echo "Error: Scratch backup not found for '$1' with timestamp '$2'"
        echo "Hint: Use 'wtsl' to see available backups"
        return 1
    fi
}

# Workspace aliases (generic)
# Rename these with your project prefix if needed
wsstatus() { workspace_status; }
wstree() { workspace_tree; }
wsdiscover() { workspace_discover "$@"; }
wscomplete() { workspace_complete; }
wssync() { workspace_sync; }

complete -F _wt_complete wt
complete -F _wt_complete wtr
complete -F _wt_complete wtrn
complete -F _wt_complete wtsb
complete -F _wt_complete wtso
complete -F _wt_complete_all_branches wta
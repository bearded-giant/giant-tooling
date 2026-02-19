#!/bin/bash

# Worktree Helper Generator
# Creates customized worktree helper scripts for any project/stack

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
OUTPUT_DIR="$SCRIPT_DIR"
PRIVATE_WORKTREES_DIR="$HOME/dotfiles/shell/scripts/worktrees"

# colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'

print_header() {
    echo -e "${BLUE}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
    echo -e "${BLUE}  Worktree Helper Generator${NC}"
    echo -e "${BLUE}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
    echo ""
}

prompt() {
    local var_name="$1"
    local prompt_text="$2"
    local default="$3"

    if [ -n "$default" ]; then
        echo -en "${GREEN}$prompt_text${NC} [${YELLOW}$default${NC}]: "
    else
        echo -en "${GREEN}$prompt_text${NC}: "
    fi

    read -r value
    if [ -z "$value" ] && [ -n "$default" ]; then
        value="$default"
    fi

    eval "$var_name=\"$value\""
}

prompt_choice() {
    local var_name="$1"
    local prompt_text="$2"
    shift 2
    local options=("$@")

    echo -e "${GREEN}$prompt_text${NC}"
    local i=1
    for opt in "${options[@]}"; do
        echo "  $i) $opt"
        ((i++))
    done
    echo -n "Choice [1]: "

    read -r choice
    choice=${choice:-1}

    if [[ "$choice" =~ ^[0-9]+$ ]] && [ "$choice" -ge 1 ] && [ "$choice" -le "${#options[@]}" ]; then
        eval "$var_name=\"${options[$((choice-1))]}\""
    else
        eval "$var_name=\"${options[0]}\""
    fi
}

detect_stack() {
    local project_path="$1"

    if [ ! -d "$project_path" ]; then
        echo "unknown"
        return
    fi

    # check for stack indicators
    if [ -f "$project_path/pyproject.toml" ] || [ -f "$project_path/requirements.txt" ] || [ -f "$project_path/setup.py" ]; then
        echo "python"
    elif [ -f "$project_path/package.json" ]; then
        echo "node"
    elif [ -f "$project_path/go.mod" ]; then
        echo "go"
    elif [ -f "$project_path/Cargo.toml" ]; then
        echo "rust"
    elif [ -f "$project_path/Gemfile" ]; then
        echo "ruby"
    else
        echo "unknown"
    fi
}

detect_package_manager() {
    local project_path="$1"
    local stack="$2"

    case "$stack" in
        python)
            if [ -f "$project_path/pyproject.toml" ] && grep -q "poetry" "$project_path/pyproject.toml" 2>/dev/null; then
                echo "poetry"
            elif [ -f "$project_path/Pipfile" ]; then
                echo "pipenv"
            elif [ -f "$project_path/pyproject.toml" ]; then
                echo "pip"
            elif [ -f "$project_path/requirements.txt" ]; then
                echo "pip"
            else
                echo "pip"
            fi
            ;;
        node)
            if [ -f "$project_path/pnpm-lock.yaml" ]; then
                echo "pnpm"
            elif [ -f "$project_path/yarn.lock" ]; then
                echo "yarn"
            elif [ -f "$project_path/package-lock.json" ]; then
                echo "npm"
            else
                echo "npm"
            fi
            ;;
        *)
            echo "none"
            ;;
    esac
}

generate_setup_function() {
    local stack="$1"
    local pkg_mgr="$2"
    local prefix="$3"
    local worktree_base_var="$4"

    cat << 'SETUP_START'
# Helper: Setup worktree after creation
_${PREFIX}_wt_setup_worktree() {
    local branch="$1"
    local target_dir="${WORKTREE_BASE_VAR}/$branch"
    local bootstrap_dir="${WORKTREE_BASE_VAR}/wt-bootstrap"

SETUP_START

    # stack-specific version file
    case "$stack" in
        python)
            cat << 'PYTHON_VERSION'
    # Create .python-version file for pyenv
    if [ ! -f "$target_dir/.python-version" ]; then
        if [ -f "$bootstrap_dir/.python-version" ]; then
            cp "$bootstrap_dir/.python-version" "$target_dir/"
            echo "Created .python-version from bootstrap"
        else
            echo "3.11" > "$target_dir/.python-version"
            echo "Created .python-version (3.11)"
        fi
    fi

PYTHON_VERSION
            ;;
        node)
            cat << 'NODE_VERSION'
    # Create .nvmrc or .node-version file if needed
    if [ ! -f "$target_dir/.nvmrc" ] && [ ! -f "$target_dir/.node-version" ]; then
        if [ -f "$bootstrap_dir/.nvmrc" ]; then
            cp "$bootstrap_dir/.nvmrc" "$target_dir/"
            echo "Created .nvmrc from bootstrap"
        elif [ -f "$bootstrap_dir/.node-version" ]; then
            cp "$bootstrap_dir/.node-version" "$target_dir/"
            echo "Created .node-version from bootstrap"
        fi
    fi

NODE_VERSION
            ;;
    esac

    # common bootstrap logic
    cat << 'BOOTSTRAP'
    # Copy bootstrap files
    if [ -d "$bootstrap_dir" ]; then
        echo "Copying bootstrap files..."
        cp -r "$bootstrap_dir"/* "$target_dir"/ 2>/dev/null
        cp -r "$bootstrap_dir"/.[^.]* "$target_dir"/ 2>/dev/null
        echo "✓ Bootstrap files copied"

        # Setup .claude directory
        if [ ! -d "$target_dir/.claude" ]; then
            mkdir -p "$target_dir/.claude"
            echo "✓ .claude directory created"
        fi

        # Link or copy CLAUDE.md
        local main_claude="${WORKTREE_BASE_VAR}/main/.claude/CLAUDE.md"
        if [ -f "$main_claude" ] && [ ! -f "$target_dir/.claude/CLAUDE.md" ]; then
            ln -sf "$main_claude" "$target_dir/.claude/CLAUDE.md"
            echo "✓ CLAUDE.md symlinked from main"
        elif [ -f "${WORKTREE_BASE_VAR}/CLAUDE.md" ] && [ ! -f "$target_dir/.claude/CLAUDE.md" ]; then
            cp "${WORKTREE_BASE_VAR}/CLAUDE.md" "$target_dir/.claude/"
            echo "✓ CLAUDE.md copied to .claude/"
        elif [ -f "$bootstrap_dir/CLAUDE.md" ] && [ ! -f "$target_dir/.claude/CLAUDE.md" ]; then
            cp "$bootstrap_dir/CLAUDE.md" "$target_dir/.claude/"
            echo "✓ CLAUDE.md copied from wt-bootstrap"
        fi

BOOTSTRAP

    # stack-specific env files
    case "$stack" in
        python)
            cat << 'PYTHON_ENV'

        # Copy .env file
        if [ -f "$bootstrap_dir/.env" ] && [ ! -f "$target_dir/.env" ]; then
            cp "$bootstrap_dir/.env" "$target_dir/"
            echo "✓ .env copied"
        fi
PYTHON_ENV
            ;;
        node)
            cat << 'NODE_ENV'

        # Copy .env.local file
        if [ -f "$bootstrap_dir/.env.local" ] && [ ! -f "$target_dir/.env.local" ]; then
            cp "$bootstrap_dir/.env.local" "$target_dir/"
            echo "✓ .env.local copied"
        fi
NODE_ENV
            ;;
    esac

    cat << 'BOOTSTRAP_END'
    else
        echo "⚠ wt-bootstrap directory not found at $bootstrap_dir"
    fi

    # Initialize workspace (creates .giantmem/ with structure)
    if type workspace_init &>/dev/null; then
        workspace_init "$target_dir" "$branch"
    else
        if [ ! -d "$target_dir/.giantmem" ]; then
            mkdir -p "$target_dir/.giantmem"
            echo "✓ .giantmem directory created"
        fi
    fi

    cd "$target_dir"

BOOTSTRAP_END

    # stack-specific install hint
    case "$stack" in
        python)
            case "$pkg_mgr" in
                poetry)
                    echo '    echo "ℹ Run '\''posh'\'' or '\''poetry install'\'' to set up dependencies"'
                    ;;
                pipenv)
                    echo '    echo "ℹ Run '\''pipenv install'\'' to set up dependencies"'
                    ;;
                pip)
                    echo '    echo "ℹ Run '\''pip install -r requirements.txt'\'' to set up dependencies"'
                    ;;
            esac
            ;;
        node)
            case "$pkg_mgr" in
                pnpm)
                    echo '    if [ -f "package.json" ]; then echo "ℹ Run '\''pnpm install'\'' to set up dependencies"; fi'
                    ;;
                yarn)
                    echo '    if [ -f "package.json" ]; then echo "ℹ Run '\''yarn install'\'' to set up dependencies"; fi'
                    ;;
                npm)
                    echo '    if [ -f "package.json" ]; then echo "ℹ Run '\''npm install'\'' to set up dependencies"; fi'
                    ;;
            esac
            ;;
        go)
            echo '    if [ -f "go.mod" ]; then echo "ℹ Run '\''go mod download'\'' to fetch dependencies"; fi'
            ;;
        rust)
            echo '    if [ -f "Cargo.toml" ]; then echo "ℹ Run '\''cargo build'\'' to compile"; fi'
            ;;
        ruby)
            echo '    if [ -f "Gemfile" ]; then echo "ℹ Run '\''bundle install'\'' to set up dependencies"; fi'
            ;;
    esac

    echo "}"
}

generate_script() {
    local project_name="$1"
    local worktree_base="$2"
    local prefix="$3"
    local stack="$4"
    local pkg_mgr="$5"
    local output_file="$6"

    # uppercase prefix for variable names
    local PREFIX_UPPER=$(echo "$prefix" | tr '[:lower:]' '[:upper:]')
    local WORKTREE_BASE_VAR="${PREFIX_UPPER}_WORKTREE_BASE"
    local WORKTREE_LAST_VAR="${PREFIX_UPPER}_WORKTREE_LAST_FILE"

    cat > "$output_file" << HEADER
#!/bin/bash

# Git Worktree Helper Functions - ${project_name}
# Generated by worktree-helper-generator.sh
# Stack: ${stack} | Package Manager: ${pkg_mgr}
# Add this to your ~/.bashrc

# Project configuration
${WORKTREE_BASE_VAR}="${worktree_base}"
${WORKTREE_LAST_VAR}="\$${WORKTREE_BASE_VAR}/.last-branch"

# Source workspace library
WORKSPACE_LIB="\${GIANT_TOOLING_DIR:-\$HOME/dev/giant-tooling}/workspace/workspace-lib.sh"
[ -f "\$WORKSPACE_LIB" ] && source "\$WORKSPACE_LIB"

# Helper: Get current branch name
_${prefix}_wt_current_branch() {
    git rev-parse --abbrev-ref HEAD 2>/dev/null
}

# Helper: Check if we're in a worktree
_${prefix}_wt_in_worktree() {
    [[ "\$PWD" == "\$${WORKTREE_BASE_VAR}/"* ]] && [ -d ".git" -o -f ".git" ]
}

# Helper: Get branch status vs remote
_${prefix}_wt_branch_status() {
    local branch="\$1"
    local upstream=\$(git rev-parse --abbrev-ref "\$branch@{upstream}" 2>/dev/null)

    if [ -z "\$upstream" ]; then
        echo "(no upstream)"
        return
    fi

    local ahead=\$(git rev-list --count "\$upstream..\$branch" 2>/dev/null)
    local behind=\$(git rev-list --count "\$branch..\$upstream" 2>/dev/null)

    if [ "\$ahead" -eq 0 ] && [ "\$behind" -eq 0 ]; then
        echo "(up to date)"
    elif [ "\$ahead" -gt 0 ] && [ "\$behind" -eq 0 ]; then
        echo "(ahead \$ahead)"
    elif [ "\$ahead" -eq 0 ] && [ "\$behind" -gt 0 ]; then
        echo "(behind \$behind)"
    else
        echo "(diverged: ↑\$ahead ↓\$behind)"
    fi
}

HEADER

    # generate setup function with proper variable substitution
    cat >> "$output_file" << SETUP_FUNC
# Helper: Setup worktree after creation
_${prefix}_wt_setup_worktree() {
    local branch="\$1"
    local target_dir="\$${WORKTREE_BASE_VAR}/\$branch"
    local bootstrap_dir="\$${WORKTREE_BASE_VAR}/wt-bootstrap"

SETUP_FUNC

    # stack-specific version file
    case "$stack" in
        python)
            cat >> "$output_file" << 'PYTHON_VERSION'
    # Create .python-version file for pyenv
    if [ ! -f "$target_dir/.python-version" ]; then
        if [ -f "$bootstrap_dir/.python-version" ]; then
            cp "$bootstrap_dir/.python-version" "$target_dir/"
            echo "Created .python-version from bootstrap"
        else
            echo "3.11" > "$target_dir/.python-version"
            echo "Created .python-version (3.11)"
        fi
    fi

PYTHON_VERSION
            ;;
        node)
            cat >> "$output_file" << 'NODE_VERSION'
    # Create .nvmrc or .node-version file if needed
    if [ ! -f "$target_dir/.nvmrc" ] && [ ! -f "$target_dir/.node-version" ]; then
        if [ -f "$bootstrap_dir/.nvmrc" ]; then
            cp "$bootstrap_dir/.nvmrc" "$target_dir/"
            echo "Created .nvmrc from bootstrap"
        elif [ -f "$bootstrap_dir/.node-version" ]; then
            cp "$bootstrap_dir/.node-version" "$target_dir/"
            echo "Created .node-version from bootstrap"
        fi
    fi

NODE_VERSION
            ;;
    esac

    # common bootstrap - need to substitute the variable
    cat >> "$output_file" << BOOTSTRAP
    # Copy bootstrap files
    if [ -d "\$bootstrap_dir" ]; then
        echo "Copying bootstrap files..."
        cp -r "\$bootstrap_dir"/* "\$target_dir"/ 2>/dev/null
        cp -r "\$bootstrap_dir"/.[^.]* "\$target_dir"/ 2>/dev/null
        echo "✓ Bootstrap files copied"

        # Setup .claude directory
        if [ ! -d "\$target_dir/.claude" ]; then
            mkdir -p "\$target_dir/.claude"
            echo "✓ .claude directory created"
        fi

        # Link or copy CLAUDE.md
        local main_claude="\$${WORKTREE_BASE_VAR}/main/.claude/CLAUDE.md"
        if [ -f "\$main_claude" ] && [ ! -f "\$target_dir/.claude/CLAUDE.md" ]; then
            ln -sf "\$main_claude" "\$target_dir/.claude/CLAUDE.md"
            echo "✓ CLAUDE.md symlinked from main"
        elif [ -f "\$${WORKTREE_BASE_VAR}/CLAUDE.md" ] && [ ! -f "\$target_dir/.claude/CLAUDE.md" ]; then
            cp "\$${WORKTREE_BASE_VAR}/CLAUDE.md" "\$target_dir/.claude/"
            echo "✓ CLAUDE.md copied to .claude/"
        elif [ -f "\$bootstrap_dir/CLAUDE.md" ] && [ ! -f "\$target_dir/.claude/CLAUDE.md" ]; then
            cp "\$bootstrap_dir/CLAUDE.md" "\$target_dir/.claude/"
            echo "✓ CLAUDE.md copied from wt-bootstrap"
        fi

BOOTSTRAP

    # stack-specific env files
    case "$stack" in
        python)
            cat >> "$output_file" << 'PYTHON_ENV'

        # Copy .env file
        if [ -f "$bootstrap_dir/.env" ] && [ ! -f "$target_dir/.env" ]; then
            cp "$bootstrap_dir/.env" "$target_dir/"
            echo "✓ .env copied"
        fi
PYTHON_ENV
            ;;
        node)
            cat >> "$output_file" << 'NODE_ENV'

        # Copy .env.local file
        if [ -f "$bootstrap_dir/.env.local" ] && [ ! -f "$target_dir/.env.local" ]; then
            cp "$bootstrap_dir/.env.local" "$target_dir/"
            echo "✓ .env.local copied"
        fi
NODE_ENV
            ;;
    esac

    cat >> "$output_file" << BOOTSTRAP_END
    else
        echo "⚠ wt-bootstrap directory not found at \$bootstrap_dir"
    fi

    # Initialize workspace (creates .giantmem/ with structure)
    if type workspace_init &>/dev/null; then
        workspace_init "\$target_dir" "\$branch"
    else
        if [ ! -d "\$target_dir/.giantmem" ]; then
            mkdir -p "\$target_dir/.giantmem"
            echo "✓ .giantmem directory created"
        fi
    fi

    cd "\$target_dir"

BOOTSTRAP_END

    # stack-specific install hint
    case "$stack" in
        python)
            case "$pkg_mgr" in
                poetry)
                    echo '    echo "ℹ Run '\''posh'\'' or '\''poetry install'\'' to set up dependencies"' >> "$output_file"
                    ;;
                pipenv)
                    echo '    echo "ℹ Run '\''pipenv install'\'' to set up dependencies"' >> "$output_file"
                    ;;
                pip)
                    echo '    echo "ℹ Run '\''pip install -r requirements.txt'\'' to set up dependencies"' >> "$output_file"
                    ;;
            esac
            ;;
        node)
            case "$pkg_mgr" in
                pnpm)
                    echo '    if [ -f "package.json" ]; then echo "ℹ Run '\''pnpm install'\'' to set up dependencies"; fi' >> "$output_file"
                    ;;
                yarn)
                    echo '    if [ -f "package.json" ]; then echo "ℹ Run '\''yarn install'\'' to set up dependencies"; fi' >> "$output_file"
                    ;;
                npm)
                    echo '    if [ -f "package.json" ]; then echo "ℹ Run '\''npm install'\'' to set up dependencies"; fi' >> "$output_file"
                    ;;
            esac
            ;;
        go)
            echo '    if [ -f "go.mod" ]; then echo "ℹ Run '\''go mod download'\'' to fetch dependencies"; fi' >> "$output_file"
            ;;
        rust)
            echo '    if [ -f "Cargo.toml" ]; then echo "ℹ Run '\''cargo build'\'' to compile"; fi' >> "$output_file"
            ;;
        ruby)
            echo '    if [ -f "Gemfile" ]; then echo "ℹ Run '\''bundle install'\'' to set up dependencies"; fi' >> "$output_file"
            ;;
    esac

    echo "}" >> "$output_file"

    # Now append the main functions
    cat >> "$output_file" << MAIN_FUNC

# Initialize bare repo structure from existing repo
${prefix}_init() {
    local source_repo="\$1"

    # Expand ~ to \$HOME
    source_repo="\${source_repo/#\\~/$HOME}"

    if [ -d "\$${WORKTREE_BASE_VAR}/.bare" ]; then
        echo "Worktree structure already exists at \$${WORKTREE_BASE_VAR}"
        echo "Use '${prefix} <branch>' to create worktrees"
        return 0
    fi

    if [ -z "\$source_repo" ]; then
        echo "Usage: ${prefix}_init <source-repo-path-or-url>"
        echo ""
        echo "Examples:"
        echo "  ${prefix}_init /path/to/existing/repo"
        echo "  ${prefix}_init git@github.com:org/repo.git"
        return 1
    fi

    echo "Initializing worktree structure at \$${WORKTREE_BASE_VAR}"

    # Create base directory
    mkdir -p "\$${WORKTREE_BASE_VAR}"

    # Clone as bare repo
    echo "Cloning bare repository..."
    if git clone --bare "\$source_repo" "\$${WORKTREE_BASE_VAR}/.bare"; then
        echo "Bare repository cloned"
    else
        echo "Error: Failed to clone repository"
        return 1
    fi

    cd "\$${WORKTREE_BASE_VAR}/.bare"

    # If cloned from local repo, fix origin to point to actual remote
    if [ -d "\$source_repo" ]; then
        local real_remote=\$(git -C "\$source_repo" remote get-url origin 2>/dev/null)
        if [ -n "\$real_remote" ]; then
            echo "Updating origin to actual remote: \$real_remote"
            git remote set-url origin "\$real_remote"
        else
            echo "Warning: Source repo has no origin remote configured"
        fi
    fi

    # Configure remote fetch
    git config remote.origin.fetch "+refs/heads/*:refs/remotes/origin/*"
    git fetch origin

    # Determine default branch
    local default_branch=\$(git symbolic-ref refs/remotes/origin/HEAD 2>/dev/null | sed 's@^refs/remotes/origin/@@')
    if [ -z "\$default_branch" ]; then
        for branch in main master develop; do
            if git rev-parse --verify "origin/\$branch" >/dev/null 2>&1; then
                default_branch="\$branch"
                break
            fi
        done
    fi

    if [ -z "\$default_branch" ]; then
        echo "Error: Could not determine default branch"
        return 1
    fi

    echo "Default branch: \$default_branch"

    # Create first worktree
    echo "Creating worktree for '\$default_branch'..."
    if git worktree add "../\$default_branch" "\$default_branch"; then
        echo "Worktree created: \$${WORKTREE_BASE_VAR}/\$default_branch"
    else
        echo "Error: Failed to create worktree"
        return 1
    fi

    # Create wt-bootstrap directory
    mkdir -p "\$${WORKTREE_BASE_VAR}/wt-bootstrap"
    echo "Created wt-bootstrap directory for shared files"

    echo ""
    echo "Initialization complete!"
    echo ""
    echo "Next steps:"
    echo "  1. cd \$${WORKTREE_BASE_VAR}/\$default_branch"
    echo "  2. Copy any .env files to wt-bootstrap/"
    echo "  3. Use '${prefix} <branch>' to create new worktrees"

    cd "\$${WORKTREE_BASE_VAR}/\$default_branch"
}

# Main function to change to worktree directories or create new ones
${prefix}() {
    # Check if bare repo exists, offer to init if not
    if [ ! -d "\$${WORKTREE_BASE_VAR}/.bare" ]; then
        echo "Worktree structure not initialized at \$${WORKTREE_BASE_VAR}"
        echo ""
        echo "To initialize, provide one of:"
        echo "  - Local repo path:  /path/to/existing/repo"
        echo "  - Git clone URL:    git@github.com:org/repo.git"
        echo ""
        echo -n "Source: "
        read -r source_repo
        if [ -n "\$source_repo" ]; then
            ${prefix}_init "\$source_repo"
            [ \$? -ne 0 ] && return 1
        else
            echo "Cancelled. Run '${prefix}_init <source>' to initialize."
            return 1
        fi
    fi

    if [ -z "\$1" ]; then
        # No argument - go to last branch
        if [ -f "\$${WORKTREE_LAST_VAR}" ]; then
            last_branch=\$(cat "\$${WORKTREE_LAST_VAR}")
            if [ -d "\$${WORKTREE_BASE_VAR}/\$last_branch" ]; then
                if _${prefix}_wt_in_worktree; then
                    current_branch=\$(basename "\$PWD")
                    if [ "\$current_branch" != "\$last_branch" ]; then
                        echo "\$current_branch" >"\$${WORKTREE_LAST_VAR}"
                    fi
                fi
                cd "\$${WORKTREE_BASE_VAR}/\$last_branch"
                echo "➜ \$last_branch \$(_${prefix}_wt_branch_status \$(_${prefix}_wt_current_branch))"
            else
                echo "Last branch '\$last_branch' no longer exists"
                cd "\$${WORKTREE_BASE_VAR}"
            fi
        else
            echo "No last branch recorded"
            cd "\$${WORKTREE_BASE_VAR}"
        fi
    else
        if [ -d "\$${WORKTREE_BASE_VAR}/\$1" ]; then
            if _${prefix}_wt_in_worktree; then
                current_branch=\$(basename "\$PWD")
                if [ "\$current_branch" != "\$1" ]; then
                    echo "\$current_branch" >"\$${WORKTREE_LAST_VAR}"
                fi
            fi
            cd "\$${WORKTREE_BASE_VAR}/\$1"
            echo "➜ \$1 \$(_${prefix}_wt_branch_status \$(_${prefix}_wt_current_branch))"
        else
            echo "Worktree '\$1' not found. Creating..."

            cd "\$${WORKTREE_BASE_VAR}/.bare" || {
                echo "Error: Cannot access bare repository at \$${WORKTREE_BASE_VAR}/.bare"
                return 1
            }

            local created=0
            local error_msg=""

            if git rev-parse --verify "\$1" >/dev/null 2>&1; then
                echo "Found local branch '\$1'"
                echo "Create worktree for branch '\$1'? (y/N)"
                read -r response
                if [[ ! "\$response" =~ ^[Yy]\$ ]]; then
                    echo "Cancelled."
                    return 1
                fi
                if git worktree add "../\$1" "\$1" 2>/dev/null; then
                    echo "✓ Added worktree for existing local branch '\$1'"
                    created=1
                else
                    if git worktree list | grep -q "/\$1 "; then
                        error_msg="Branch '\$1' is already checked out in another worktree"
                    else
                        error_msg="Failed to add worktree for branch '\$1'"
                    fi
                fi
            elif git rev-parse --verify "origin/\$1" >/dev/null 2>&1; then
                echo "Found remote branch 'origin/\$1'"
                echo "Create worktree for branch '\$1' tracking origin/\$1? (y/N)"
                read -r response
                if [[ ! "\$response" =~ ^[Yy]\$ ]]; then
                    echo "Cancelled."
                    return 1
                fi
                if git worktree add -b "\$1" "../\$1" "origin/\$1" 2>/dev/null; then
                    echo "✓ Created local branch '\$1' tracking origin/\$1"
                    created=1
                else
                    error_msg="Failed to create worktree from remote branch 'origin/\$1'"
                fi
            else
                echo "Branch '\$1' not found locally or remotely"
                local default_branch=\$(git symbolic-ref refs/remotes/origin/HEAD 2>/dev/null | sed 's@^refs/remotes/origin/@@')
                if [ -z "\$default_branch" ]; then
                    for branch in main master develop; do
                        if git rev-parse --verify "origin/\$branch" >/dev/null 2>&1; then
                            default_branch="\$branch"
                            break
                        fi
                    done
                fi

                if [ -z "\$default_branch" ]; then
                    error_msg="Could not determine default branch"
                else
                    echo "Create new worktree for branch '\$1' from \$default_branch? (y/N)"
                    read -r response
                    if [[ ! "\$response" =~ ^[Yy]\$ ]]; then
                        echo "Cancelled."
                        return 1
                    fi
                    if git worktree add -b "\$1" "../\$1" "origin/\$default_branch" 2>/dev/null; then
                        echo "✓ Created new branch '\$1' from \$default_branch"
                        created=1
                    else
                        error_msg="Failed to create new branch '\$1' from \$default_branch"
                    fi
                fi
            fi

            if [ \$created -eq 1 ]; then
                echo "\$1" >"\$${WORKTREE_LAST_VAR}"
                cd "\$${WORKTREE_BASE_VAR}/\$1"
                echo "Setting upstream to origin/\$1..."
                if git push -u origin "\$1" 2>/dev/null; then
                    echo "✓ Upstream set to origin/\$1"
                else
                    echo "Note: Could not push to origin (may need permissions or remote setup)"
                fi
                _${prefix}_wt_setup_worktree "\$1"
                cd "\$${WORKTREE_BASE_VAR}/\$1"
                echo "➜ \$1 (ready)"
            else
                echo "Error: \$error_msg"
                if [[ "\$error_msg" == *"already checked out"* ]]; then
                    echo "Hint: Use '${prefix}l' to see all worktrees"
                elif [[ "\$error_msg" == *"Failed to create worktree"* ]]; then
                    echo "Hint: Try 'git fetch --all' first, or check 'git worktree list'"
                fi
                return 1
            fi
        fi
    fi
}

# List all worktrees with branch status
${prefix}l() {
    local original_dir="\$PWD"
    echo "Worktrees in \$${WORKTREE_BASE_VAR}:"
    echo "────────────────────────────────────"

    cd "\$${WORKTREE_BASE_VAR}/.bare" 2>/dev/null || {
        echo "Error: Cannot access bare repository"
        return 1
    }

    git worktree list | while read -r line; do
        if [[ "\$line" == *"(bare)" ]]; then
            echo "\$line"
        else
            local path=\$(echo "\$line" | awk '{print \$1}')
            local branch=\$(echo "\$line" | sed -n 's/.*\[\(.*\)\].*/\1/p')
            if [ -n "\$branch" ] && [ -d "\$path" ]; then
                local status=\$(cd "\$path" && _${prefix}_wt_branch_status "\$branch" 2>/dev/null || echo "")
                echo "\$line \$status"
            else
                echo "\$line"
            fi
        fi
    done
    cd "\$original_dir"
}

# List branches (local and remote) with worktree info
${prefix}b() {
    local original_dir="\$PWD"
    cd "\$${WORKTREE_BASE_VAR}/.bare" 2>/dev/null || {
        echo "Error: Cannot access bare repository"
        return 1
    }

    echo "Local branches:"
    echo "──────────────"
    git branch --format='%(refname:short)' | while read -r branch; do
        local wt_path=\$(git worktree list --porcelain | grep -B2 "branch refs/heads/\$branch" | grep "^worktree" | cut -d' ' -f2)
        if [ -n "\$wt_path" ]; then
            echo "  \$branch → \$(basename "\$wt_path")"
        else
            echo "  \$branch"
        fi
    done

    echo -e "\nRemote branches (origin):"
    echo "────────────────────────"
    git branch -r --format='%(refname:short)' | grep "^origin/" | grep -v "HEAD" | sed 's/^origin\//  /'
    cd "\$original_dir"
}

# Pull updates for current worktree branch
${prefix}p() {
    if ! _${prefix}_wt_in_worktree; then
        echo "Error: Not in a worktree"
        return 1
    fi

    local branch=\$(_${prefix}_wt_current_branch)
    echo "Pulling updates for '\$branch'..."

    if git pull --ff-only 2>&1; then
        echo "✓ Updated '\$branch'"
        _${prefix}_wt_branch_status "\$branch"
    else
        echo "⚠ Pull failed - you may need to merge or rebase"
        echo "Hint: Use 'git pull --rebase' or resolve conflicts"
    fi
}

# Pull with rebase for current worktree branch
${prefix}pr() {
    if ! _${prefix}_wt_in_worktree; then
        echo "Error: Not in a worktree"
        return 1
    fi

    local branch=\$(_${prefix}_wt_current_branch)
    local target_branch="\$1"

    if [ -n "\$target_branch" ]; then
        echo "Pulling with rebase for '\$branch' from origin/\$target_branch..."
        if git pull --rebase origin "\$target_branch" 2>&1; then
            echo "✓ Rebased '\$branch' on origin/\$target_branch"
            _${prefix}_wt_branch_status "\$branch"
        else
            echo "⚠ Rebase failed - resolve conflicts or 'git rebase --abort'"
        fi
    else
        echo "Pulling with rebase for '\$branch'..."
        if git pull --rebase 2>&1; then
            echo "✓ Rebased '\$branch'"
            _${prefix}_wt_branch_status "\$branch"
        else
            echo "⚠ Rebase failed - resolve conflicts or 'git rebase --abort'"
        fi
    fi
}

# Add a new worktree (explicit command)
${prefix}a() {
    if [ -z "\$1" ]; then
        echo "Usage: ${prefix}a <branch-name> [base-branch]"
        return 1
    fi

    cd "\$${WORKTREE_BASE_VAR}/.bare" || {
        echo "Error: Cannot access bare repository"
        return 1
    }

    local created=0
    local error_msg=""

    if [ -z "\$2" ]; then
        if git rev-parse --verify "\$1" >/dev/null 2>&1; then
            echo "Found local branch '\$1'"
            echo "Create worktree for branch '\$1'? (y/N)"
            read -r response
            if [[ ! "\$response" =~ ^[Yy]\$ ]]; then
                echo "Cancelled."
                return 1
            fi
            if git worktree add "../\$1" "\$1" 2>/dev/null; then
                echo "✓ Added worktree for existing local branch '\$1'"
                created=1
            else
                if git worktree list | grep -q "/\$1 "; then
                    error_msg="Branch '\$1' is already checked out in another worktree"
                else
                    error_msg="Failed to add worktree for branch '\$1'"
                fi
            fi
        elif git rev-parse --verify "origin/\$1" >/dev/null 2>&1; then
            echo "Found remote branch 'origin/\$1'"
            echo "Create worktree for branch '\$1' tracking origin/\$1? (y/N)"
            read -r response
            if [[ ! "\$response" =~ ^[Yy]\$ ]]; then
                echo "Cancelled."
                return 1
            fi
            if git worktree add -b "\$1" "../\$1" "origin/\$1" 2>/dev/null; then
                echo "✓ Created local branch '\$1' tracking origin/\$1"
                created=1
            else
                error_msg="Failed to create worktree from remote branch"
            fi
        else
            local default_branch=\$(git symbolic-ref refs/remotes/origin/HEAD 2>/dev/null | sed 's@^refs/remotes/origin/@@')
            if [ -z "\$default_branch" ]; then
                for branch in main master develop; do
                    if git rev-parse --verify "origin/\$branch" >/dev/null 2>&1; then
                        default_branch="\$branch"
                        break
                    fi
                done
            fi

            if [ -z "\$default_branch" ]; then
                error_msg="Could not determine default branch"
            else
                echo "Create new worktree for branch '\$1' from \$default_branch? (y/N)"
                read -r response
                if [[ ! "\$response" =~ ^[Yy]\$ ]]; then
                    echo "Cancelled."
                    return 1
                fi
                if git worktree add -b "\$1" "../\$1" "origin/\$default_branch" 2>/dev/null; then
                    echo "✓ Created new branch '\$1' from \$default_branch"
                    created=1
                else
                    error_msg="Failed to create new branch"
                fi
            fi
        fi
    else
        if git rev-parse --verify "\$2" >/dev/null 2>&1; then
            echo "Create new worktree for branch '\$1' from \$2? (y/N)"
            read -r response
            if [[ ! "\$response" =~ ^[Yy]\$ ]]; then
                echo "Cancelled."
                return 1
            fi
            if git worktree add -b "\$1" "../\$1" "\$2" 2>/dev/null; then
                echo "✓ Created new branch '\$1' from \$2"
                created=1
            else
                error_msg="Failed to create branch from '\$2'"
            fi
        else
            error_msg="Base branch '\$2' not found"
        fi
    fi

    if [ \$created -eq 1 ]; then
        echo "\$1" >"\$${WORKTREE_LAST_VAR}"
        cd "\$${WORKTREE_BASE_VAR}/\$1"
        if ! git branch -vv | grep -q "^\* \$1 .*\[origin/\$1\]"; then
            echo "Setting upstream to origin/\$1..."
            if git push -u origin "\$1" 2>/dev/null; then
                echo "✓ Upstream set to origin/\$1"
            else
                echo "Note: Could not push to origin"
            fi
        fi
        _${prefix}_wt_setup_worktree "\$1"
        cd "\$${WORKTREE_BASE_VAR}/\$1"
        echo "➜ \$1 (created)"
    else
        echo "Error: \$error_msg"
        return 1
    fi
}

# Remove a worktree
${prefix}r() {
    if [ -z "\$1" ]; then
        echo "Usage: ${prefix}r <branch-name> [-f|--force]"
        return 1
    fi

    local branch_name="\$1"
    local worktree_dir="\$${WORKTREE_BASE_VAR}/\$branch_name"
    local workspace_source="\$worktree_dir/.giantmem"
    [ ! -d "\$workspace_source" ] && workspace_source="\$worktree_dir/scratch"
    local workspace_backup_base="\${GIANTMEM_ARCHIVE_BASE:-\${SCRATCH_ARCHIVE_BASE:-\$HOME/giantmem_archive}}/\$(basename "\$${WORKTREE_BASE_VAR}")"

    if [ "\$2" != "-f" ] && [ "\$2" != "--force" ]; then
        echo "Are you sure you want to delete worktree '\$branch_name'? (y/N)"
        read -r response
        if [[ ! "\$response" =~ ^[Yy]\$ ]]; then
            echo "Cancelled"
            return 0
        fi
    fi

    if [ -d "\$workspace_source" ]; then
        local branch_backup_dir="\$workspace_backup_base/\$branch_name"
        mkdir -p "\$branch_backup_dir"
        local timestamp=\$(date '+%Y%m%d_%H%M%S')
        local backup_dir="\$branch_backup_dir/\$timestamp"

        echo "Backing up workspace directory to: \$backup_dir"
        if cp -r "\$workspace_source" "\$backup_dir"; then
            echo "✓ Workspace directory backed up successfully"
            local latest_link="\$branch_backup_dir/latest"
            [ -L "\$latest_link" ] && rm "\$latest_link"
            ln -s "\$timestamp" "\$latest_link"
            echo "✓ Created symlink: \$latest_link -> \$timestamp"

            local search_script="\${GIANT_TOOLING_DIR:-\$HOME/dev/giant-tooling}/scratch-archive/scratch-search.py"
            [ -f "\$search_script" ] && python3 "\$search_script" ingest --project "\$(basename "\$${WORKTREE_BASE_VAR}")" 2>/dev/null &
        else
            echo "❌ ERROR: Failed to backup workspace directory"
            echo "Worktree removal cancelled to preserve your work"
            return 1
        fi
    fi

    cd "\$${WORKTREE_BASE_VAR}/.bare" || {
        echo "Error: Cannot access bare repository"
        return 1
    }

    local force=""
    [ "\$2" = "-f" ] || [ "\$2" = "--force" ] && force="--force"

    if git worktree remove \$force "../\$1" 2>&1; then
        echo "✓ Removed worktree '\$1'"
        git worktree prune
        if [ -f "\$${WORKTREE_LAST_VAR}" ]; then
            [ "\$(cat "\$${WORKTREE_LAST_VAR}")" = "\$1" ] && rm "\$${WORKTREE_LAST_VAR}"
        fi
    else
        echo "Error: Failed to remove worktree '\$1'"
        echo "Hint: Use '${prefix}r \$1 --force' to force removal"
        return 1
    fi
}

# Rename a worktree (directory + branch + upstream + archives)
${prefix}rn() {
    if [ -z "\$1" ] || [ -z "\$2" ]; then
        echo "Usage: ${prefix}rn <old-name> <new-name>"
        return 1
    fi

    local old_name="\$1"
    local new_name="\$2"
    local old_dir="\$${WORKTREE_BASE_VAR}/\$old_name"
    local new_dir="\$${WORKTREE_BASE_VAR}/\$new_name"

    if [ ! -d "\$old_dir" ]; then
        echo "Error: Worktree '\$old_name' not found"
        return 1
    fi

    if [ -d "\$new_dir" ]; then
        echo "Error: '\$new_name' already exists"
        return 1
    fi

    if [[ "\$PWD" == "\$old_dir"* ]]; then
        echo "Error: Cannot rename worktree you're currently in"
        echo "Hint: Switch to another worktree first"
        return 1
    fi

    cd "\$${WORKTREE_BASE_VAR}/.bare" || {
        echo "Error: Cannot access bare repository"
        return 1
    }

    # move worktree directory
    if ! git worktree move "../\$old_name" "../\$new_name" 2>&1; then
        echo "Error: Failed to move worktree"
        return 1
    fi
    echo "✓ Worktree moved: \$old_name -> \$new_name"

    # rename the branch
    if git branch -m "\$old_name" "\$new_name" 2>&1; then
        echo "✓ Branch renamed: \$old_name -> \$new_name"
    else
        echo "Note: Could not rename branch (may already have the target name)"
    fi

    # update upstream
    cd "\$new_dir"
    if git push -u origin "\$new_name" 2>/dev/null; then
        echo "✓ Upstream set to origin/\$new_name"
        if git push origin --delete "\$old_name" 2>/dev/null; then
            echo "✓ Deleted old remote branch origin/\$old_name"
        else
            echo "Note: Could not delete old remote branch origin/\$old_name"
        fi
    else
        echo "Note: Could not push new branch name to origin"
    fi

    # update .last-branch
    if [ -f "\$${WORKTREE_LAST_VAR}" ]; then
        if [ "\$(cat "\$${WORKTREE_LAST_VAR}")" = "\$old_name" ]; then
            echo "\$new_name" > "\$${WORKTREE_LAST_VAR}"
            echo "✓ Updated last-branch reference"
        fi
    fi

    # migrate workspace archives
    local workspace_archive_base="\${GIANTMEM_ARCHIVE_BASE:-\${SCRATCH_ARCHIVE_BASE:-\$HOME/giantmem_archive}}/\$(basename "\$${WORKTREE_BASE_VAR}")"
    local old_archive="\$workspace_archive_base/\$old_name"
    local new_archive="\$workspace_archive_base/\$new_name"
    if [ -d "\$old_archive" ]; then
        if [ -d "\$new_archive" ]; then
            echo "Warning: Workspace archive '\$new_name' already exists, skipping archive migration"
        else
            mv "\$old_archive" "\$new_archive"
            echo "✓ Migrated workspace archives: \$old_name -> \$new_name"
        fi
    fi

    echo "➜ Renamed '\$old_name' to '\$new_name'"
}

# Quick status of current worktree
${prefix}s() {
    if ! _${prefix}_wt_in_worktree; then
        echo "Not in a git worktree"
        return 1
    fi

    local branch=\$(_${prefix}_wt_current_branch)
    local status=\$(_${prefix}_wt_branch_status "\$branch")

    echo "Branch: \$branch \$status"
    git status -sb
}

# Fetch updates for all branches
${prefix}f() {
    local original_dir="\$PWD"
    cd "\$${WORKTREE_BASE_VAR}/.bare" || {
        echo "Error: Cannot access bare repository"
        return 1
    }

    echo "Fetching all remotes..."
    if git fetch --all --prune; then
        echo "✓ Fetch completed"
        echo -e "\nBranches with remote changes:"
        git for-each-ref --format='%(refname:short) %(upstream:short)' refs/heads | while read local upstream; do
            if [ -n "\$upstream" ]; then
                behind=\$(git rev-list --count "\$local..\$upstream" 2>/dev/null)
                [ "\$behind" -gt 0 ] && echo "  \$local is behind \$upstream by \$behind commits"
            fi
        done
    else
        echo "Error: Fetch failed"
        cd "\$original_dir"
        return 1
    fi
    cd "\$original_dir"
}

# Prune worktrees that no longer exist
${prefix}prune() {
    local original_dir="\$PWD"
    cd "\$${WORKTREE_BASE_VAR}/.bare" || {
        echo "Error: Cannot access bare repository"
        return 1
    }

    echo "Pruning stale worktrees..."
    if git worktree prune -v; then
        echo "✓ Prune completed"
    else
        echo "Error: Prune failed"
        cd "\$original_dir"
        return 1
    fi
    cd "\$original_dir"
}

# Repair worktrees after directory rename/move
${prefix}repair() {
    local original_dir="\$PWD"
    cd "\$${WORKTREE_BASE_VAR}/.bare" || {
        echo "Error: Cannot access bare repository"
        return 1
    }

    echo "Repairing worktrees in \$${WORKTREE_BASE_VAR}..."
    git worktree prune -v

    for dir in "\$${WORKTREE_BASE_VAR}"/*; do
        if [ -d "\$dir" ] && [ -f "\$dir/.git" ]; then
            local dirname=\$(basename "\$dir")
            case "\$dirname" in
            .bare | wt-bootstrap | .giantmem | scratch | node_modules)
                ;;
            *)
                if ! git worktree list | grep -q "\$dir"; then
                    echo "Found orphaned worktree: \$dirname"
                    if git rev-parse --verify "\$dirname" >/dev/null 2>&1; then
                        echo "Re-adding worktree for branch: \$dirname"
                        rm -f "\$dir/.git"
                        if git worktree add "\$dir" "\$dirname" 2>/dev/null; then
                            echo "✓ Repaired worktree: \$dirname"
                        else
                            echo "⚠ Could not repair \$dirname"
                        fi
                    else
                        echo "⚠ Branch '\$dirname' not found in repository"
                    fi
                else
                    echo "✓ Worktree OK: \$dirname"
                fi
                ;;
            esac
        fi
    done

    echo "Repair complete. Running '${prefix}l' to show status..."
    cd "\$original_dir"
    ${prefix}l
}

# Copy bootstrap files from existing worktree to another
${prefix}c() {
    if [ -z "\$1" ]; then
        echo "Usage: ${prefix}c <target-worktree> [source-worktree]"
        return 1
    fi

    local target_dir="\$${WORKTREE_BASE_VAR}/\$1"
    local source_dir=""

    if [ ! -d "\$target_dir" ]; then
        echo "Error: Target worktree '\$1' not found"
        return 1
    fi

    if [ -n "\$2" ]; then
        source_dir="\$${WORKTREE_BASE_VAR}/\$2"
        if [ ! -d "\$source_dir" ]; then
            echo "Error: Source worktree '\$2' not found"
            return 1
        fi
    else
        for wt in "\$${WORKTREE_BASE_VAR}"/*; do
            if [ -d "\$wt" ] && [ "\$wt" != "\$target_dir" ]; then
                local bn=\$(basename "\$wt")
                case "\$bn" in .bare|wt-bootstrap|.giantmem|scratch|node_modules) continue ;; esac
                if [ -f "\$wt/.env" ] || [ -f "\$wt/.env.local" ] || [ -d "\$wt/.claude" ]; then
                    source_dir="\$wt"
                    echo "Using source worktree: \$bn"
                    break
                fi
            fi
        done
        if [ -z "\$source_dir" ]; then
            echo "Error: No suitable source worktree found"
            return 1
        fi
    fi

    echo "Copying bootstrap files from \$(basename "\$source_dir") to \$(basename "\$target_dir")..."

    # Copy env files
    for envfile in .env .env.local .env.development; do
        if [ -f "\$source_dir/\$envfile" ] && [ ! -f "\$target_dir/\$envfile" ]; then
            cp "\$source_dir/\$envfile" "\$target_dir/"
            echo "✓ \$envfile copied"
        fi
    done

    # Setup .claude directory
    if [ ! -d "\$target_dir/.claude" ]; then
        mkdir -p "\$target_dir/.claude"
        echo "✓ .claude directory created"
    fi

    # CLAUDE.md
    local main_claude="\$${WORKTREE_BASE_VAR}/main/.claude/CLAUDE.md"
    if [ -f "\$main_claude" ] && [ ! -f "\$target_dir/.claude/CLAUDE.md" ]; then
        ln -sf "\$main_claude" "\$target_dir/.claude/CLAUDE.md"
        echo "✓ CLAUDE.md symlinked from main"
    elif [ -f "\$source_dir/.claude/CLAUDE.md" ] && [ ! -f "\$target_dir/.claude/CLAUDE.md" ]; then
        cp "\$source_dir/.claude/CLAUDE.md" "\$target_dir/.claude/"
        echo "✓ CLAUDE.md copied"
    fi

    echo "Bootstrap files copy completed for '\$1'"
}

# Backup workspace directory from current worktree
${prefix}bs() {
    if ! _${prefix}_wt_in_worktree; then
        echo "Not in a git worktree"
        return 1
    fi

    local worktree_dir="\$(git rev-parse --show-toplevel)"
    local worktree_name="\$(basename "\$worktree_dir")"
    local workspace_source="\$worktree_dir/.giantmem"
    [ ! -d "\$workspace_source" ] && workspace_source="\$worktree_dir/scratch"
    local workspace_backup_base="\${GIANTMEM_ARCHIVE_BASE:-\${SCRATCH_ARCHIVE_BASE:-\$HOME/giantmem_archive}}/\$(basename "\$${WORKTREE_BASE_VAR}")"

    if [ -d "\$workspace_source" ]; then
        local branch_backup_dir="\$workspace_backup_base/\$worktree_name"
        mkdir -p "\$branch_backup_dir"
        local timestamp=\$(date '+%Y%m%d_%H%M%S')
        local backup_dir="\$branch_backup_dir/\$timestamp"

        echo "Backing up workspace directory to: \$backup_dir"
        if cp -r "\$workspace_source" "\$backup_dir"; then
            echo "✓ Workspace directory backed up successfully"
            local latest_link="\$branch_backup_dir/latest"
            [ -L "\$latest_link" ] && rm "\$latest_link"
            ln -s "\$timestamp" "\$latest_link"
            echo "✓ Created symlink: \$latest_link -> \$timestamp"
        else
            echo "Error: Failed to backup workspace directory"
            return 1
        fi
    else
        echo "No workspace directory found to backup"
    fi
}

# List workspace backups
${prefix}sl() {
    local workspace_dir="\${GIANTMEM_ARCHIVE_BASE:-\${SCRATCH_ARCHIVE_BASE:-\$HOME/giantmem_archive}}/\$(basename "\$${WORKTREE_BASE_VAR}")"

    if [ ! -d "\$workspace_dir" ]; then
        echo "No workspace backups found"
        return 0
    fi

    echo "Workspace backups in \$workspace_dir:"
    echo "────────────────────────────────────"

    for branch_dir in "\$workspace_dir"/*; do
        if [ -d "\$branch_dir" ] && [ ! -L "\$branch_dir" ]; then
            local branch_name=\$(basename "\$branch_dir")
            echo ""
            echo "  \$branch_name:"
            for backup in "\$branch_dir"/*; do
                if [ -d "\$backup" ] && [[ "\$(basename "\$backup")" =~ ^[0-9]{8}_[0-9]{6}\$ ]]; then
                    local timestamp=\$(basename "\$backup")
                    local size=\$(du -sh "\$backup" 2>/dev/null | cut -f1)
                    local date_part=\${timestamp%_*}
                    local time_part=\${timestamp#*_}
                    local formatted_date="\${date_part:0:4}-\${date_part:4:2}-\${date_part:6:2}"
                    local formatted_time="\${time_part:0:2}:\${time_part:2:2}:\${time_part:4:2}"
                    echo "    - \$formatted_date \$formatted_time (\$size)"
                    if [ -L "\$branch_dir/latest" ]; then
                        [ "\$(readlink "\$branch_dir/latest")" = "\$timestamp" ] && echo "      └─ (latest)"
                    fi
                fi
            done
        fi
    done

    local total_backups=\$(find "\$workspace_dir" -mindepth 2 -maxdepth 2 -type d -name "[0-9]*_[0-9]*" | wc -l | tr -d ' ')
    echo ""
    echo "Total backups: \$total_backups"
}

# Manually backup workspace directory
${prefix}sb() {
    if [ -z "\$1" ]; then
        if _${prefix}_wt_in_worktree; then
            local branch_name=\$(basename "\$PWD")
        else
            echo "Usage: ${prefix}sb [branch-name]"
            return 1
        fi
    else
        local branch_name="\$1"
    fi

    local worktree_dir="\$${WORKTREE_BASE_VAR}/\$branch_name"
    local workspace_source="\$worktree_dir/.giantmem"
    [ ! -d "\$workspace_source" ] && workspace_source="\$worktree_dir/scratch"
    local workspace_backup_base="\${GIANTMEM_ARCHIVE_BASE:-\${SCRATCH_ARCHIVE_BASE:-\$HOME/giantmem_archive}}/\$(basename "\$${WORKTREE_BASE_VAR}")"

    if [ ! -d "\$worktree_dir" ]; then
        echo "Error: Worktree '\$branch_name' not found"
        return 1
    fi

    if [ ! -d "\$workspace_source" ]; then
        echo "No workspace directory found in '\$branch_name'"
        return 1
    fi

    local branch_backup_dir="\$workspace_backup_base/\$branch_name"
    mkdir -p "\$branch_backup_dir"
    local timestamp=\$(date '+%Y%m%d_%H%M%S')
    local backup_dir="\$branch_backup_dir/\$timestamp"

    echo "Backing up workspace directory to: \$backup_dir"
    if cp -r "\$workspace_source" "\$backup_dir"; then
        echo "✓ Workspace directory backed up successfully"
        local latest_link="\$branch_backup_dir/latest"
        [ -L "\$latest_link" ] && rm "\$latest_link"
        ln -s "\$timestamp" "\$latest_link"
        echo "✓ Created symlink: \$latest_link -> \$timestamp"
        local size=\$(du -sh "\$backup_dir" 2>/dev/null | cut -f1)
        echo "✓ Backup size: \$size"
    else
        echo "❌ ERROR: Failed to backup workspace directory"
        return 1
    fi
}

# Open/browse a workspace backup
${prefix}so() {
    if [ -z "\$1" ]; then
        echo "Usage: ${prefix}so <branch-name> [timestamp]"
        return 1
    fi

    local workspace_dir="\${GIANTMEM_ARCHIVE_BASE:-\${SCRATCH_ARCHIVE_BASE:-\$HOME/giantmem_archive}}/\$(basename "\$${WORKTREE_BASE_VAR}")"
    local branch_dir="\$workspace_dir/\$1"

    if [ ! -d "\$branch_dir" ]; then
        echo "Error: No workspace backups found for branch '\$1'"
        echo "Hint: Use '${prefix}sl' to see available backups"
        return 1
    fi

    local target_dir=""
    if [ -n "\$2" ]; then
        target_dir="\$branch_dir/\$2"
    else
        target_dir="\$branch_dir/latest"
        if [ ! -L "\$target_dir" ]; then
            local latest_backup=\$(ls -1d "\$branch_dir"/[0-9]*_[0-9]* 2>/dev/null | sort -r | head -n1)
            if [ -n "\$latest_backup" ]; then
                target_dir="\$latest_backup"
            else
                echo "Error: No backups found for branch '\$1'"
                return 1
            fi
        fi
    fi

    if [ -d "\$target_dir" ] || [ -L "\$target_dir" ]; then
        cd "\$target_dir"
        echo "➜ \$(pwd)"
    else
        echo "Error: Workspace backup not found"
        return 1
    fi
}

# Workspace aliases
${prefix}ws() { workspace_status; }
${prefix}tree() { workspace_tree; }
${prefix}discover() { workspace_discover "\$@"; }
${prefix}complete() { workspace_complete; }
${prefix}sync() { workspace_sync; }

# Auto-complete
_${prefix}_complete() {
    local cur="\${COMP_WORDS[COMP_CWORD]}"
    local branches=()
    if [ -d "\$${WORKTREE_BASE_VAR}" ]; then
        for dir in "\$${WORKTREE_BASE_VAR}"/*; do
            if [ -d "\$dir" ]; then
                local bn=\$(basename "\$dir")
                case "\$bn" in .bare|.last-branch|wt-bootstrap|.giantmem|scratch|node_modules|.*) continue ;; esac
                branches+=("\$bn")
            fi
        done
    fi
    COMPREPLY=(\$(compgen -W "\${branches[*]}" -- "\$cur"))
}

_${prefix}_complete_all_branches() {
    local cur="\${COMP_WORDS[COMP_CWORD]}"
    cd "\$${WORKTREE_BASE_VAR}/.bare" 2>/dev/null || return
    local branches=\$(git branch -a --format='%(refname:short)' | sed 's/^origin\///' | sort -u)
    COMPREPLY=(\$(compgen -W "\$branches" -- "\$cur"))
}

complete -F _${prefix}_complete ${prefix}
complete -F _${prefix}_complete ${prefix}r
complete -F _${prefix}_complete ${prefix}rn
complete -F _${prefix}_complete ${prefix}c
complete -F _${prefix}_complete ${prefix}sb
complete -F _${prefix}_complete ${prefix}so
complete -F _${prefix}_complete_all_branches ${prefix}a
MAIN_FUNC

    chmod +x "$output_file"
    echo -e "${GREEN}✓ Generated: $output_file${NC}"
}

# Main wizard
main() {
    print_header

    echo -e "This wizard will generate a worktree helper script for your project.\n"

    # Project name
    prompt PROJECT_NAME "Project name (e.g., myapp, customcheckout)" ""
    if [ -z "$PROJECT_NAME" ]; then
        echo -e "${RED}Error: Project name is required${NC}"
        exit 1
    fi

    # Function prefix
    local default_prefix=$(echo "$PROJECT_NAME" | sed 's/-//g' | cut -c1-4)
    prompt PREFIX "Function prefix (short, e.g., cwt, fewt)" "$default_prefix"

    # Worktree base directory
    local default_base="\$HOME/dev/${PROJECT_NAME}-wt"
    prompt WORKTREE_BASE "Worktree base directory" "$default_base"

    # Expand for detection
    local expanded_base=$(eval echo "$WORKTREE_BASE")

    # Detect or ask for stack
    echo ""
    local detected_stack=$(detect_stack "$expanded_base")
    if [ "$detected_stack" != "unknown" ] && [ -d "$expanded_base" ]; then
        echo -e "${YELLOW}Detected stack: $detected_stack${NC}"
        prompt_choice STACK "Confirm or choose stack:" "$detected_stack" "python" "node" "go" "rust" "ruby" "other"
    else
        prompt_choice STACK "Select project stack:" "python" "node" "go" "rust" "ruby" "other"
    fi

    # Detect or ask for package manager
    if [ "$STACK" = "python" ] || [ "$STACK" = "node" ]; then
        local detected_pm=$(detect_package_manager "$expanded_base" "$STACK")
        echo -e "${YELLOW}Detected package manager: $detected_pm${NC}"

        if [ "$STACK" = "python" ]; then
            prompt_choice PKG_MGR "Confirm or choose package manager:" "$detected_pm" "poetry" "pip" "pipenv"
        else
            prompt_choice PKG_MGR "Confirm or choose package manager:" "$detected_pm" "pnpm" "npm" "yarn"
        fi
    else
        PKG_MGR="none"
    fi

    # Output file - offer private dir if it exists
    local output_file="$OUTPUT_DIR/worktree-helper-${PROJECT_NAME}.sh"
    if [ -d "$PRIVATE_WORKTREES_DIR" ]; then
        echo ""
        echo -e "${GREEN}Output location:${NC}"
        echo "  1) $OUTPUT_DIR (public, requires manual source line)"
        echo "  2) $PRIVATE_WORKTREES_DIR (private, auto-loaded)"
        echo -n "Choice [1]: "
        read -r loc_choice
        if [ "$loc_choice" = "2" ]; then
            output_file="$PRIVATE_WORKTREES_DIR/worktree-helper-${PROJECT_NAME}.sh"
            OUTPUT_IS_PRIVATE=1
        fi
    fi
    prompt OUTPUT_FILE "Output file" "$output_file"

    # Summary
    echo ""
    echo -e "${BLUE}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
    echo -e "${BLUE}  Summary${NC}"
    echo -e "${BLUE}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
    echo -e "  Project:         ${YELLOW}$PROJECT_NAME${NC}"
    echo -e "  Prefix:          ${YELLOW}$PREFIX${NC}"
    echo -e "  Worktree base:   ${YELLOW}$WORKTREE_BASE${NC}"
    echo -e "  Stack:           ${YELLOW}$STACK${NC}"
    echo -e "  Package manager: ${YELLOW}$PKG_MGR${NC}"
    echo -e "  Output file:     ${YELLOW}$OUTPUT_FILE${NC}"
    echo ""

    echo -e "${GREEN}Commands that will be created:${NC}"
    echo "  ${PREFIX}_init    - Initialize bare repo (first time setup)"
    echo "  ${PREFIX}         - Switch to/create worktree"
    echo "  ${PREFIX}l        - List worktrees"
    echo "  ${PREFIX}b        - List branches"
    echo "  ${PREFIX}a        - Add worktree explicitly"
    echo "  ${PREFIX}r        - Remove worktree"
    echo "  ${PREFIX}rn       - Rename worktree (branch + dir + archives)"
    echo "  ${PREFIX}p        - Pull (ff-only)"
    echo "  ${PREFIX}pr       - Pull with rebase"
    echo "  ${PREFIX}s        - Status"
    echo "  ${PREFIX}f        - Fetch all"
    echo "  ${PREFIX}c        - Copy bootstrap files"
    echo "  ${PREFIX}bs       - Backup workspace (current)"
    echo "  ${PREFIX}sb       - Backup workspace (any)"
    echo "  ${PREFIX}sl       - List workspace backups"
    echo "  ${PREFIX}so       - Open workspace backup"
    echo "  ${PREFIX}prune    - Prune stale worktrees"
    echo "  ${PREFIX}repair   - Repair worktrees"
    echo ""

    echo -n "Generate script? (Y/n): "
    read -r confirm
    if [[ "$confirm" =~ ^[Nn]$ ]]; then
        echo "Cancelled."
        exit 0
    fi

    # Generate
    generate_script "$PROJECT_NAME" "$WORKTREE_BASE" "$PREFIX" "$STACK" "$PKG_MGR" "$OUTPUT_FILE"

    echo ""

    # Ask about sourcing (skip if using auto-loaded private dir)
    if [ "$OUTPUT_IS_PRIVATE" = "1" ]; then
        echo -e "${GREEN}✓ Script saved to auto-loaded directory${NC}"
        echo ""
        prompt_reload ""
    else
        add_source_line "$OUTPUT_FILE"
    fi
}

add_source_line() {
    local script_path="$1"

    echo -e "${BLUE}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
    echo -e "${BLUE}  Shell Configuration${NC}"
    echo -e "${BLUE}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
    echo ""
    echo "To use the generated commands, the script needs to be sourced in your shell config."
    echo ""

    # Detect common shell configs
    local shell_configs=()
    [ -f "$HOME/.bashrc" ] && shell_configs+=("$HOME/.bashrc")
    [ -f "$HOME/.zshrc" ] && shell_configs+=("$HOME/.zshrc")
    [ -f "$HOME/.bash_profile" ] && shell_configs+=("$HOME/.bash_profile")
    [ -f "$HOME/dotfiles/shell/.bashrc" ] && shell_configs+=("$HOME/dotfiles/shell/.bashrc")
    [ -f "$HOME/dotfiles/shell/.rechargerc" ] && shell_configs+=("$HOME/dotfiles/shell/.rechargerc")

    echo -e "${GREEN}Add source line to shell config?${NC}"
    echo "  1) Skip - I'll add it manually"

    local i=2
    for cfg in "${shell_configs[@]}"; do
        echo "  $i) $cfg"
        ((i++))
    done
    echo "  $i) Other (enter custom path)"

    echo -n "Choice [1]: "
    read -r choice
    choice=${choice:-1}

    if [ "$choice" = "1" ]; then
        echo ""
        echo -e "${GREEN}Done! To use, add this line to your shell config:${NC}"
        echo -e "  source $script_path"
        echo ""
        return 0
    fi

    local target_config=""
    local max_choice=$((${#shell_configs[@]} + 2))

    if [ "$choice" = "$max_choice" ]; then
        # Custom path
        echo -n "Enter path to shell config file: "
        read -r target_config
        target_config=$(eval echo "$target_config")  # expand ~
    elif [[ "$choice" =~ ^[0-9]+$ ]] && [ "$choice" -ge 2 ] && [ "$choice" -lt "$max_choice" ]; then
        target_config="${shell_configs[$((choice-2))]}"
    else
        echo -e "${RED}Invalid choice${NC}"
        return 1
    fi

    if [ -z "$target_config" ]; then
        echo -e "${RED}No config file specified${NC}"
        return 1
    fi

    # Check if file exists, create if needed
    if [ ! -f "$target_config" ]; then
        echo -n "File doesn't exist. Create it? (y/N): "
        read -r create_file
        if [[ "$create_file" =~ ^[Yy]$ ]]; then
            mkdir -p "$(dirname "$target_config")"
            touch "$target_config"
            echo -e "${GREEN}✓ Created $target_config${NC}"
        else
            echo "Cancelled."
            return 1
        fi
    fi

    # Check if already sourced
    if grep -q "source.*$(basename "$script_path")" "$target_config" 2>/dev/null; then
        echo -e "${YELLOW}⚠ Script already appears to be sourced in $target_config${NC}"
        echo -n "Add anyway? (y/N): "
        read -r add_anyway
        if [[ ! "$add_anyway" =~ ^[Yy]$ ]]; then
            echo "Skipped."
            prompt_reload
            return 0
        fi
    fi

    # Add the source line
    local source_line="source \"$script_path\""
    echo "" >> "$target_config"
    echo "# Worktree helper for $(basename "$script_path" .sh | sed 's/worktree-helper-//')" >> "$target_config"
    echo "$source_line" >> "$target_config"

    echo -e "${GREEN}✓ Added to $target_config:${NC}"
    echo "  $source_line"
    echo ""

    prompt_reload "$target_config"
}

prompt_reload() {
    local config_file="$1"

    echo -e "${BLUE}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
    echo -e "${BLUE}  Almost done!${NC}"
    echo -e "${BLUE}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
    echo ""
    echo "To start using the new commands, reload your shell:"
    echo ""
    if [ -n "$config_file" ]; then
        echo -e "  ${YELLOW}source $config_file${NC}"
        echo ""
        echo "Or simply open a new terminal window."
    else
        echo -e "  ${YELLOW}source ~/.bashrc${NC}  (or your shell config)"
        echo ""
        echo "Or simply open a new terminal window."
    fi
    echo ""
}

main "$@"

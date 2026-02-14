#!/usr/bin/env bash
# scratch-archive.sh - standalone scratch directory archiving
# archives scratch/ dirs to ~/scratch_archive/{project}/{branch}/{timestamp}/

set -euo pipefail

SCRATCH_ARCHIVE_BASE="${SCRATCH_ARCHIVE_BASE:-$HOME/scratch_archive}"
SCRATCH_INDEX_FILE=".scratch-index"

# build search index for an archive directory
build_index() {
    local archive_dir="$1"
    local index_file="$archive_dir/$SCRATCH_INDEX_FILE"

    # index all .md files: filepath:line:content
    # --no-ignore: don't respect gitignore (archives are outside repos)
    # pattern "." matches any character (i.e., non-empty lines)
    rg -n --no-ignore --glob "*.md" "." "$archive_dir" 2>/dev/null > "$index_file" || true

    local count=$(wc -l < "$index_file" | tr -d ' ')
    echo "Indexed: $count lines"
}

usage() {
    cat <<EOF
Usage: $(basename "$0") <command> [options]

Commands:
  archive [--clean] [--project <name>] [src]  Archive a scratch directory
  list [project]                    List archives (all or specific project)
  open <project> [branch] [ts]      Open archive in Finder
  search <pattern> [flags]          Search archives with fzf (interactive)
  index [project]                   Rebuild search indexes for archives
  help                              Show this help

Search flags:
  -p <project>   Filter by project
  -t <type>      Filter by type: plans|context|research|reviews|filebox|history|prompts
  -d <path>      Search specific dir (e.g., master/20251220_143022)
  -l             Search only "latest" archives
  -C <n>         Context lines in preview (default: 5)

Examples:
  $(basename "$0") archive                        # archive ./scratch with auto-detect
  $(basename "$0") archive --clean                # archive and remove ./scratch
  $(basename "$0") archive --project cc-wt        # archive under cc-wt/{branch}/
  $(basename "$0") archive --project cc-wt --clean  # worktree: archive and remove
  $(basename "$0") list                           # list all projects
  $(basename "$0") list myproj                    # list archives for project
  $(basename "$0") open myproj main               # open latest for branch
  $(basename "$0") search "jwt"                   # search all archives
  $(basename "$0") search "replica" -p mas        # search specific project
  $(basename "$0") search "auth" -t plans -l      # search only latest, plans only

Archive location: $SCRATCH_ARCHIVE_BASE/{project}/{branch}/{timestamp}/
EOF
}

do_archive() {
    local clean=false
    local project_override=""

    # parse flags
    while [[ $# -gt 0 ]]; do
        case "$1" in
            --clean) clean=true; shift ;;
            --project) project_override="$2"; shift 2 ;;
            *) break ;;
        esac
    done

    local scratch_source="${1:-$PWD/scratch}"
    local project_name=""
    local branch_name=""

    if [ -n "$project_override" ]; then
        project_name="$project_override"
    else
        # detect worktree: .git is a file, not a directory
        local git_toplevel
        git_toplevel=$(git rev-parse --show-toplevel 2>/dev/null)
        if [ -n "$git_toplevel" ] && [ -f "$git_toplevel/.git" ]; then
            # worktree: project is the parent dir (e.g., edgerouter-wt)
            project_name=$(basename "$(dirname "$git_toplevel")")
        else
            project_name=$(basename "$(dirname "$(realpath "$scratch_source")")")
        fi
    fi

    # try git branch if not provided
    if [ -z "$branch_name" ]; then
        if git rev-parse --is-inside-work-tree &>/dev/null; then
            branch_name=$(git rev-parse --abbrev-ref HEAD 2>/dev/null)
        fi
        [ -z "$branch_name" ] && branch_name=$(basename "$(dirname "$(realpath "$scratch_source")")")
    fi

    if [ ! -d "$scratch_source" ]; then
        echo "ERROR: No scratch directory found at: $scratch_source"
        return 1
    fi

    local branch_backup_dir="$SCRATCH_ARCHIVE_BASE/$project_name/$branch_name"
    mkdir -p "$branch_backup_dir"

    local timestamp=$(date '+%Y%m%d_%H%M%S')
    local backup_dir="$branch_backup_dir/$timestamp"

    echo "Archiving: $scratch_source"
    echo "      To: $backup_dir"

    if cp -r "$scratch_source" "$backup_dir"; then
        # build search index
        build_index "$backup_dir"

        # update latest symlink
        local latest_link="$branch_backup_dir/latest"
        [ -L "$latest_link" ] && rm "$latest_link"
        ln -s "$timestamp" "$latest_link"

        local size=$(du -sh "$backup_dir" 2>/dev/null | cut -f1)
        echo "Archived: $size (latest -> $timestamp)"

        # update fts5 search db in background
        python3 "$(dirname "$0")/scratch-search.py" ingest --project "$project_name" 2>/dev/null &

        if [ "$clean" = true ]; then
            rm -rf "$scratch_source"
            echo "Cleaned: $scratch_source"
        fi

        return 0
    else
        echo "ERROR: Failed to archive"
        return 1
    fi
}

do_list() {
    local project_name="${1:-}"
    local archive_dir="$SCRATCH_ARCHIVE_BASE"

    [ -n "$project_name" ] && archive_dir="$archive_dir/$project_name"

    if [ ! -d "$archive_dir" ]; then
        echo "No archives found"
        return 0
    fi

    echo "Archives in $archive_dir:"
    echo "────────────────────────────────────"

    if [ -n "$project_name" ]; then
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
        for project_dir in "$archive_dir"/*; do
            [ -d "$project_dir" ] || continue
            local proj=$(basename "$project_dir")
            local count=$(find "$project_dir" -mindepth 2 -maxdepth 2 -type d -name "[0-9]*_[0-9]*" 2>/dev/null | wc -l | tr -d ' ')
            echo "  $proj/: $count archives"
        done
    fi
}

do_open() {
    if [ -z "${1:-}" ]; then
        echo "Usage: $(basename "$0") open <project> [branch] [timestamp]"
        return 1
    fi

    local project="$1"
    local branch="${2:-}"
    local timestamp="${3:-}"
    local target_dir="$SCRATCH_ARCHIVE_BASE/$project"

    [ -n "$branch" ] && target_dir="$target_dir/$branch"
    [ -n "$timestamp" ] && target_dir="$target_dir/$timestamp"

    # use latest symlink if branch specified but no timestamp
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

do_index() {
    local project="${1:-}"
    local search_path="$SCRATCH_ARCHIVE_BASE"

    [ -n "$project" ] && search_path="$search_path/$project"

    if [ ! -d "$search_path" ]; then
        echo "Path not found: $search_path"
        return 1
    fi

    if ! command -v rg &>/dev/null; then
        echo "ERROR: ripgrep (rg) is required. Install with: brew install ripgrep"
        return 1
    fi

    echo "Rebuilding indexes in $search_path..."
    local count=0

    # find all timestamp directories (YYYYMMDD_HHMMSS pattern)
    while IFS= read -r archive_dir; do
        [ -d "$archive_dir" ] || continue
        echo -n "  $(basename "$(dirname "$archive_dir")")/$(basename "$archive_dir"): "
        build_index "$archive_dir"
        count=$((count + 1))
    done < <(find "$search_path" -type d -name "[0-9]*_[0-9]*" 2>/dev/null)

    echo "Rebuilt $count indexes"
}

do_search() {
    local script_dir
    script_dir="$(dirname "$0")"
    local db_path="$SCRATCH_ARCHIVE_BASE/archives.db"

    # use fts5 python search when db exists
    if [ -f "$db_path" ]; then
        python3 "$script_dir/scratch-search.py" search "$@"
        return $?
    fi

    # fallback: legacy rg/fzf search
    local pattern=""
    local project=""
    local type=""
    local dir=""
    local latest_only=false
    local context=5

    while [[ $# -gt 0 ]]; do
        case "$1" in
            -p) project="$2"; shift 2 ;;
            -t) type="$2"; shift 2 ;;
            -d) dir="$2"; shift 2 ;;
            -l) latest_only=true; shift ;;
            -C) context="$2"; shift 2 ;;
            -*)
                echo "Unknown flag: $1"
                return 1
                ;;
            *)
                if [ -z "$pattern" ]; then
                    pattern="$1"
                else
                    echo "Unexpected argument: $1"
                    return 1
                fi
                shift
                ;;
        esac
    done

    if [ -z "$pattern" ]; then
        echo "Usage: $(basename "$0") search <pattern> [-p project] [-t type] [-d dir] [-l] [-C n]"
        return 1
    fi

    if ! command -v fzf &>/dev/null; then
        echo "ERROR: fzf is required for search. Install with: brew install fzf"
        return 1
    fi
    if ! command -v rg &>/dev/null; then
        echo "ERROR: ripgrep (rg) is required for search. Install with: brew install ripgrep"
        return 1
    fi

    local search_path="$SCRATCH_ARCHIVE_BASE"
    if [ -n "$project" ]; then
        search_path="$search_path/$project"
        if [ ! -d "$search_path" ]; then
            echo "Project not found: $project"
            return 1
        fi
    fi
    if [ -n "$dir" ]; then
        search_path="$search_path/$dir"
        if [ ! -d "$search_path" ]; then
            echo "Directory not found: $dir"
            return 1
        fi
    fi

    local grep_paths=""
    if [ "$latest_only" = true ]; then
        while IFS= read -r latest_link; do
            resolved=$(readlink -f "$latest_link" 2>/dev/null)
            [ -d "$resolved" ] && grep_paths="$grep_paths $resolved"
        done < <(find "$search_path" -name "latest" -type l 2>/dev/null)

        if [ -z "$grep_paths" ]; then
            echo "No 'latest' archives found"
            return 1
        fi
    else
        grep_paths="$search_path"
    fi

    if [ -n "$type" ]; then
        case "$type" in
            plans|context|research|reviews|filebox|history|prompts) ;;
            *)
                echo "Invalid type: $type"
                echo "Valid types: plans, context, research, reviews, filebox, history, prompts"
                return 1
                ;;
        esac
    fi

    local results=""
    local index_files=""

    for search_dir in $grep_paths; do
        while IFS= read -r idx; do
            index_files="$index_files $idx"
        done < <(find "$search_dir" -name "$SCRATCH_INDEX_FILE" -type f 2>/dev/null)
    done

    if [ -n "$index_files" ]; then
        if [ -n "$type" ]; then
            results=$(cat $index_files 2>/dev/null | rg "$pattern" | rg "/$type/" || true)
        else
            results=$(cat $index_files 2>/dev/null | rg "$pattern" || true)
        fi
    fi

    if [ -z "$results" ]; then
        if [ -n "$type" ]; then
            results=$(rg -n --no-ignore --glob "*.md" "$pattern" $grep_paths 2>/dev/null | rg "/$type/" || true)
        else
            results=$(rg -n --no-ignore --glob "*.md" "$pattern" $grep_paths 2>/dev/null || true)
        fi
    fi

    if [ -z "$results" ]; then
        echo "No matches found for: $pattern"
        return 0
    fi

    local selected
    selected=$(echo "$results" | \
        fzf --ansi \
            --delimiter ':' \
            --preview 'line={2}; start=$((line > 10 ? line - 10 : 1)); bat --color=always --highlight-line "$line" --line-range "$start:$((line + 10))" {1} 2>/dev/null || sed -n "$start,$((line + 10))p" {1}' \
            --preview-window 'right:60%:wrap' \
            --header "Searching: $pattern | Enter to select | Esc to cancel" \
            --bind 'ctrl-u:preview-half-page-up,ctrl-d:preview-half-page-down' \
        || true)

    if [ -n "$selected" ]; then
        local file line
        file=$(echo "$selected" | cut -d: -f1)
        line=$(echo "$selected" | cut -d: -f2)
        echo "$file:$line"
    fi
}

# main
case "${1:-help}" in
    archive|a)
        shift
        do_archive "$@"
        ;;
    list|ls|l)
        shift
        do_list "$@"
        ;;
    open|o)
        shift
        do_open "$@"
        ;;
    search|s)
        shift
        do_search "$@"
        ;;
    index|idx|i)
        shift
        do_index "$@"
        ;;
    help|-h|--help)
        usage
        ;;
    *)
        echo "Unknown command: $1"
        usage
        exit 1
        ;;
esac

#!/bin/bash
# workspace-init.sh - Initialize workspace in any directory
# Usage: workspace-init.sh [project-name]

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCRIPT_DIR/workspace-lib.sh"

PROJECT_NAME="${1:-$(basename "$PWD")}"

echo "Initializing workspace for: $PROJECT_NAME"
workspace_init "$PWD" "$PROJECT_NAME"

echo ""
echo "Workspace ready. Shell functions (after sourcing workspace-lib.sh):"
echo "  workspace_status    - Show workspace status"
echo "  workspace_complete  - Mark as complete"
echo "  workspace_sync      - Refresh git log"
echo "Inside Claude: /ws-init, /new-feature <name>, /list-features"

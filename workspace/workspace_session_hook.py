#!/usr/bin/env python3
"""
Workspace Session Hook for Claude Code
Hook: SessionStart

Bootstraps workspace structure and injects context when session begins.

Input (JSON on stdin):
{
    "session_id": "...",
    "cwd": "/current/working/directory",
    "source": "startup" | "resume" | "clear"
}

Output: Workspace context injected into session via stdout.

Workflow:
1. Check if .giantmem/ exists in cwd (fallback: scratch/)
2. If not, bootstrap via workspace-lib.sh
3. Read WORKSPACE.md and discoveries.md
4. Output context for Claude to use

NOTE: Uses only Python standard library (no external dependencies)
"""

import sys
import json
import os
import subprocess
from pathlib import Path

# path to workspace-lib.sh - local copy in claude-code-config/lib, fallback to giant-tooling
WORKSPACE_LIB = Path(
    os.environ.get(
        "WORKSPACE_LIB", str(Path.home() / ".claude/lib/workspace/workspace-lib.sh")
    )
)
if not WORKSPACE_LIB.exists():
    WORKSPACE_LIB = (
        Path(
            os.environ.get("GIANT_TOOLING_DIR", str(Path.home() / "dev/giant-tooling"))
        )
        / "workspace/workspace-lib.sh"
    )


def bootstrap_workspace(cwd: str) -> bool:
    """
    Bootstrap workspace structure using workspace-lib.sh.
    Returns True if bootstrap was performed.
    """
    workspace_dir = Path(cwd) / ".giantmem"
    if not workspace_dir.exists():
        workspace_dir = Path(cwd) / "scratch"

    if workspace_dir.exists():
        return False

    if not WORKSPACE_LIB.exists():
        return False

    try:
        cmd = f'source "{WORKSPACE_LIB}" && workspace_init "{cwd}"'
        subprocess.run(["bash", "-c", cmd], cwd=cwd, capture_output=True, timeout=10)
        return True
    except Exception:
        return False


def read_workspace_context(cwd: str) -> dict:
    """
    Read workspace context files.
    Returns dict with available context.
    """
    workspace_dir = Path(cwd) / ".giantmem"
    if not workspace_dir.exists():
        workspace_dir = Path(cwd) / "scratch"
    context = {
        "workspace_md": None,
        "discoveries": None,
        "current_plan": None,
        "recent_sessions": None,
        "feature_index": None,
        "bootstrapped": False,
    }

    if not workspace_dir.exists():
        return context

    # read WORKSPACE.md
    workspace_file = workspace_dir / "WORKSPACE.md"
    if workspace_file.exists():
        try:
            context["workspace_md"] = workspace_file.read_text()[:3500]
        except Exception:
            pass

    # read current plan if exists
    plan_file = workspace_dir / "plans" / "current.md"
    if plan_file.exists():
        try:
            context["current_plan"] = plan_file.read_text()[:1500]
        except Exception:
            pass

    return context


def format_context_output(context: dict, cwd: str, bootstrapped: bool) -> str:
    """
    Format workspace context for injection into Claude session.
    """
    parts = []

    project_name = Path(cwd).name

    if bootstrapped:
        parts.append(f"[Workspace bootstrapped for {project_name}]")
        parts.append(
            "Created .giantmem/ with: context/, plans/, history/, research/, reviews/, filebox/"
        )
        parts.append("")

    if context.get("workspace_md"):
        parts.append("=== WORKSPACE CONTEXT ===")
        parts.append(context["workspace_md"])
        parts.append("")

    if context.get("current_plan"):
        parts.append("=== ACTIVE PLAN ===")
        parts.append(context["current_plan"])
        parts.append("")

    return "\n".join(parts) if parts else ""


def main():
    """Main hook entry point."""
    try:
        input_data = json.load(sys.stdin)

        cwd = input_data.get("cwd", os.getcwd())
        source = input_data.get("source", "startup")

        # Only bootstrap on fresh startup, not resume
        bootstrapped = False
        if source == "startup":
            bootstrapped = bootstrap_workspace(cwd)

        # Read workspace context
        context = read_workspace_context(cwd)

        # Format and output
        output = format_context_output(context, cwd, bootstrapped)

        if output:
            print(output)

    except Exception:
        # Never crash the hook
        pass


if __name__ == "__main__":
    main()

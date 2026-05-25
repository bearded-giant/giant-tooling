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
1. Check if .giantmem/ (or legacy scratch/) exists in cwd
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
from datetime import datetime

# Path to workspace-lib.sh - adjust if needed
WORKSPACE_LIB = Path(os.environ.get("GIANT_TOOLING_DIR", Path.home() / "dev/giant-tooling")) / "workspace/workspace-lib.sh"


def bootstrap_workspace(cwd: str) -> bool:
    """
    Bootstrap workspace structure using workspace-lib.sh.
    Returns True if bootstrap was performed.
    """
    workspace_dir = Path(cwd) / ".giantmem"
    if workspace_dir.exists():
        return False
    # fallback: check for legacy scratch dir
    legacy_dir = Path(cwd) / "scratch"
    if legacy_dir.exists():
        return False

    if not WORKSPACE_LIB.exists():
        return False

    try:
        # Source the lib and call workspace_init
        cmd = f'source "{WORKSPACE_LIB}" && workspace_init "{cwd}"'
        subprocess.run(
            ["bash", "-c", cmd],
            cwd=cwd,
            capture_output=True,
            timeout=10
        )
        return True
    except Exception:
        return False


def read_recent_sessions(workspace_dir: Path, limit: int = 3) -> list:
    """
    Read recent session summaries for context injection.
    Returns list of (filename, topic, brief) tuples.
    """
    sessions_dir = workspace_dir / "history" / "sessions"
    if not sessions_dir.exists():
        return []

    sessions = []
    try:
        # get session files sorted by name (newest first due to timestamp prefix)
        session_files = sorted(sessions_dir.glob("*.md"), reverse=True)

        for session_file in session_files[:limit]:
            try:
                content = session_file.read_text()
                # extract topic and brief from file
                topic = "general"
                brief = ""

                for line in content.split('\n'):
                    if line.startswith('Topic:'):
                        topic = line.replace('Topic:', '').strip()
                    elif line.startswith('Brief:'):
                        brief = line.replace('Brief:', '').strip()
                        break

                sessions.append((session_file.name, topic, brief))
            except Exception:
                continue
    except Exception:
        pass

    return sessions


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
        "tree": None,
        "current_plan": None,
        "recent_sessions": None,
        "bootstrapped": False,
        "artifacts": None,
    }

    if not workspace_dir.exists():
        return context

    # read WORKSPACE.md
    workspace_file = workspace_dir / "WORKSPACE.md"
    if workspace_file.exists():
        try:
            context["workspace_md"] = workspace_file.read_text()[:2000]
        except Exception:
            pass

    # read discoveries
    discoveries_file = workspace_dir / "context" / "discoveries.md"
    if discoveries_file.exists():
        try:
            content = discoveries_file.read_text()
            # get last 20 discoveries (most recent context)
            lines = content.strip().split("\n")
            context["discoveries"] = "\n".join(lines[-20:])
        except Exception:
            pass

    # read tree (truncated)
    tree_file = workspace_dir / "context" / "tree.md"
    if tree_file.exists():
        try:
            content = tree_file.read_text()
            # truncate to first 100 lines
            lines = content.split("\n")[:100]
            context["tree"] = "\n".join(lines)
        except Exception:
            pass

    # read current plan if exists
    plan_file = workspace_dir / "plans" / "current.md"
    if plan_file.exists():
        try:
            context["current_plan"] = plan_file.read_text()[:1500]
        except Exception:
            pass

    # read recent sessions
    recent = read_recent_sessions(workspace_dir)
    if recent:
        context["recent_sessions"] = recent

    # read artifacts summary from artifacts.json (built by giantmem artifact reindex)
    artifacts_file = workspace_dir / "artifacts.json"
    if artifacts_file.exists():
        try:
            data = json.loads(artifacts_file.read_text())
            context["artifacts"] = summarize_artifacts(data)
        except Exception:
            pass

    return context


def summarize_artifacts(data: dict) -> dict:
    """Build a compact summary for the session-start context block."""
    rows = data.get("artifacts", [])
    by_feature = {}
    by_type = {}
    for a in rows:
        f = a.get("feature") or "(repo)"
        by_feature.setdefault(f, {}).setdefault(a.get("status", "ready"), []).append(a)
        t = a.get("type", "unknown")
        by_type[t] = by_type.get(t, 0) + 1

    return {
        "total": len(rows),
        "by_type": by_type,
        "by_feature": by_feature,
        "repo": data.get("repo", ""),
        "branch": data.get("branch", ""),
    }


def format_context_output(context: dict, cwd: str, bootstrapped: bool) -> str:
    """
    Format workspace context for injection into Claude session.
    """
    parts = []

    project_name = Path(cwd).name

    if bootstrapped:
        parts.append(f"[Workspace bootstrapped for {project_name}]")
        parts.append("Created .giantmem/ with: context/, plans/, history/, prompts/, research/, reviews/, filebox/")
        parts.append("")

    if context.get("workspace_md"):
        parts.append("=== WORKSPACE CONTEXT ===")
        parts.append(context["workspace_md"])
        parts.append("")

    if context.get("recent_sessions"):
        parts.append("=== RECENT SESSIONS ===")
        for filename, topic, brief in context["recent_sessions"]:
            # extract date from filename (YYYYMMDD_HHMMSS_id.md)
            date_part = filename[:8] if len(filename) > 8 else filename
            formatted_date = f"{date_part[:4]}-{date_part[4:6]}-{date_part[6:8]}" if len(date_part) == 8 else date_part
            parts.append(f"- {formatted_date} [{topic}]: {brief}")
        parts.append("")

    if context.get("artifacts"):
        art = context["artifacts"]
        parts.append("=== ACTIVE ARTIFACTS ===")
        parts.append(f"repo={art['repo']} branch={art['branch']} total={art['total']}")
        type_summary = ", ".join(f"{k}={v}" for k, v in sorted(art["by_type"].items()))
        parts.append(f"by type: {type_summary}")
        for feature, by_status in sorted(art["by_feature"].items()):
            if feature == "(repo)":
                continue
            status_line = " ".join(
                f"{s}:{len(items)}" for s, items in sorted(by_status.items())
            )
            ready_items = [a for a in by_status.get("ready", []) if a.get("type") in ("delta-spec", "tasks", "design", "plan")]
            top = ", ".join(
                f"{a.get('type')}/{a.get('domain') or a.get('name') or ''}".rstrip("/")
                for a in ready_items[:3]
            )
            line = f"  {feature}: {status_line}"
            if top:
                line += f"  ready: {top}"
            parts.append(line)
        parts.append("query more: `giantmem artifact list -f <feature>` or MCP `find_artifact`")
        parts.append("")

    if context.get("current_plan"):
        parts.append("=== ACTIVE PLAN ===")
        parts.append(context["current_plan"])
        parts.append("")

    if context.get("discoveries"):
        parts.append("=== RECENT DISCOVERIES ===")
        parts.append(context["discoveries"])
        parts.append("")

    if parts:
        parts.append("---")
        parts.append("Remember: Save findings to .giantmem/context/discoveries.md, plans to .giantmem/plans/")

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

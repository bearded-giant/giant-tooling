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
import re
import subprocess
from pathlib import Path

# Path to workspace-lib.sh - adjust if needed
WORKSPACE_LIB = Path(os.environ.get("GIANT_TOOLING_DIR", Path.home() / "dev/giant-tooling")) / "workspace/workspace-lib.sh"

PRELOAD_PACKS_PATH = Path.home() / ".claude" / "config" / "preload_packs.yaml"
SCOPES_YAML_PATH = Path(os.environ.get("GIANTMEM_SCOPES_PATH", Path.home() / ".giantmem-global" / "scopes.yaml"))


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


# ----- preload packs --------------------------------------------------------

def parse_preload_packs_yaml(raw: str) -> dict:
    """
    Parse the minimal YAML subset used by preload_packs.yaml.
    Stdlib-only — no PyYAML dependency.

    Grammar accepted:
      version: 1
      packs:
        <pack_name>:
          <layer_key>:
            name: <str>
            query: <str>
            query_template: <str>
            scope_filter: <str>
            repo_filter: <str>
            limit: <int>
            types: [<str>, <str>]
            lifecycle: [<str>, <str>]
            static_files:
              - <path>
              - <path>
    """
    packs: dict = {}
    lines = raw.split("\n")
    in_packs = False
    pack_name = None
    layer_key = None
    layer: dict = {}
    pending_list_key = None

    def flush_layer():
        nonlocal layer, layer_key
        if pack_name and layer_key and layer:
            packs.setdefault(pack_name, {})[layer_key] = layer
        layer = {}
        layer_key = None

    for raw_line in lines:
        line = raw_line.rstrip()
        if not line or line.lstrip().startswith("#"):
            continue
        indent = len(line) - len(line.lstrip())
        trim = line.strip()

        # top-level
        if indent == 0:
            flush_layer()
            pack_name = None
            in_packs = trim == "packs:"
            continue
        if not in_packs:
            continue

        # 2-space indent: pack name
        if indent == 2 and trim.endswith(":"):
            flush_layer()
            pack_name = trim[:-1]
            continue

        # 4-space indent: layer key
        if indent == 4 and trim.endswith(":"):
            flush_layer()
            layer_key = trim[:-1]
            pending_list_key = None
            continue

        # 6+ space indent: layer field
        if indent >= 6:
            if pending_list_key is not None and trim.startswith("- "):
                val = _yaml_unquote(trim[2:].strip())
                layer.setdefault(pending_list_key, []).append(val)
                continue
            pending_list_key = None
            k, _, v = trim.partition(":")
            k = k.strip()
            v = v.strip()
            if not k:
                continue
            if v == "":
                # next lines are a list
                pending_list_key = k
                continue
            if v.startswith("[") and v.endswith("]"):
                inner = v[1:-1].strip()
                layer[k] = [_yaml_unquote(p.strip()) for p in inner.split(",") if p.strip()]
                continue
            if v.isdigit():
                layer[k] = int(v)
                continue
            layer[k] = _yaml_unquote(v)

    flush_layer()
    return packs


def _yaml_unquote(s: str) -> str:
    if len(s) >= 2 and (s[0] == '"' or s[0] == "'") and s[-1] == s[0]:
        return s[1:-1]
    return s


def detect_active_feature(workspace_dir: Path) -> str:
    """Return name of feature with status: in_progress, or '' if none."""
    features_json = workspace_dir / "features" / "features.json"
    if not features_json.exists():
        return ""
    try:
        data = json.loads(features_json.read_text())
    except Exception:
        return ""
    if not isinstance(data, dict):
        return ""
    for name, entry in data.items():
        if isinstance(entry, dict) and entry.get("status") == "in_progress":
            return name
    return ""


def detect_active_scope(repo: str) -> str:
    """Return first scope id whose repos: list contains repo, or ''."""
    if not SCOPES_YAML_PATH.exists() or not repo:
        return ""
    try:
        raw = SCOPES_YAML_PATH.read_text()
    except Exception:
        return ""
    # Tiny inline parser — just enough to walk scopes -> repos list per id.
    cur_scope = None
    in_scopes = False
    matches: list[str] = []
    for line in raw.split("\n"):
        line = line.rstrip()
        if not line or line.lstrip().startswith("#"):
            continue
        indent = len(line) - len(line.lstrip())
        trim = line.strip()
        if indent == 0:
            in_scopes = trim == "scopes:"
            continue
        if not in_scopes:
            continue
        if indent == 2 and trim.endswith(":"):
            cur_scope = trim[:-1]
            continue
        if cur_scope and trim.startswith("repos:"):
            v = trim[len("repos:"):].strip()
            if v.startswith("[") and v.endswith("]"):
                inner = v[1:-1]
                repos = [_yaml_unquote(p.strip()) for p in inner.split(",") if p.strip()]
                if repo in repos:
                    matches.append(cur_scope)
    return matches[0] if matches else ""


def resolve_placeholders(template: str, ctx: dict) -> str:
    if not template:
        return ""
    def sub(match: "re.Match[str]") -> str:
        key = match.group(1)
        return str(ctx.get(key, ""))
    return re.sub(r"\{([a-zA-Z_]+)\}", sub, template).strip()


def run_giantmem_artifact_list(args_list: list[str], cwd: str) -> list[dict]:
    """Invoke giantmem artifact list --json with the given extra args. Returns artifacts list."""
    cmd = ["giantmem", "artifact", "list", "--json"] + args_list
    try:
        res = subprocess.run(cmd, capture_output=True, text=True, cwd=cwd, timeout=10)
    except (FileNotFoundError, subprocess.TimeoutExpired):
        return []
    if res.returncode != 0 or not res.stdout:
        return []
    try:
        data = json.loads(res.stdout)
    except Exception:
        return []
    return data.get("artifacts", [])


def execute_pack_layer(layer: dict, ctx: dict, cwd: str) -> str:
    """Materialize one pack layer to its display body."""
    body_parts: list[str] = []

    # Static files inlined verbatim
    static = layer.get("static_files") or []
    for raw_path in static:
        resolved = Path(resolve_placeholders(raw_path, ctx)).expanduser()
        try:
            content = resolved.read_text()
            body_parts.append(content.strip())
        except Exception:
            body_parts.append(f"[missing: {resolved}]")

    query_template = layer.get("query_template") or layer.get("query", "")
    query = resolve_placeholders(query_template, ctx) if query_template else ""

    has_filters = any(layer.get(k) for k in ("scope_filter", "repo_filter", "types", "lifecycle"))
    if query or has_filters:
        extra: list[str] = []
        scope_filter = resolve_placeholders(str(layer.get("scope_filter") or ""), ctx)
        if scope_filter:
            extra += ["--scope", scope_filter]
        repo_filter = resolve_placeholders(str(layer.get("repo_filter") or ""), ctx)
        if repo_filter:
            extra += ["--repo", repo_filter]
        if layer.get("types"):
            extra += ["-t", ",".join(layer["types"])]
        if layer.get("lifecycle"):
            extra += ["--lifecycle", ",".join(layer["lifecycle"])]
        limit = int(layer.get("limit", 5))
        rows = run_giantmem_artifact_list(extra, cwd)
        # Query narrows the filter set; if it eliminates everything we
        # fall back to the unfiltered slice so the pack is never empty
        # when matching artifacts exist.
        if query:
            narrowed = [r for r in rows if _row_matches_query(r, query, cwd)]
            if narrowed:
                rows = narrowed
        rows = rows[:limit]
        if rows:
            for r in rows:
                ident = r.get("id", "")
                summary = f"{r.get('type', '?'):12s} {r.get('status', ''):8s} {r.get('lifecycle', ''):9s} {ident}"
                body_parts.append(summary)
        else:
            body_parts.append(f"[no matches for query={query!r} filters={extra}]")

    return "\n".join(p for p in body_parts if p)


def _row_matches_query(row: dict, query: str, cwd: str) -> bool:
    """Cheap fulltext gate: substring match in feature/domain/name/id, or file body."""
    needle = query.lower()
    for k in ("id", "feature", "domain", "name"):
        if needle in str(row.get(k, "")).lower():
            return True
    # Optional body fetch (cheap — files are small)
    try:
        path = row.get("path")
        if path:
            abs_path = Path(cwd) / ".giantmem" / path
            if abs_path.exists():
                return needle in abs_path.read_text(errors="replace").lower()
    except Exception:
        pass
    return False


def load_preload_packs(cwd: str, workspace_dir: Path, repo: str, branch: str) -> list[tuple[str, str]]:
    """
    Returns [(section_label, body), ...] for each pack/layer.
    Empty list when no preload_packs.yaml exists — caller falls back to legacy output unchanged.
    """
    if not PRELOAD_PACKS_PATH.exists():
        return []
    try:
        packs = parse_preload_packs_yaml(PRELOAD_PACKS_PATH.read_text())
    except Exception:
        return []
    if not packs:
        return []

    active_feature = detect_active_feature(workspace_dir)
    active_scope = detect_active_scope(repo)
    ctx = {
        "active_feature": active_feature,
        "active_scope": active_scope,
        "repo": repo,
        "branch": branch,
    }

    out: list[tuple[str, str]] = []
    for pack_name, layers in packs.items():
        for layer_key, layer in layers.items():
            layer_name = layer.get("name", layer_key)
            label = f"=== PRELOAD PACK: {pack_name}/{layer_name} ==="
            body = execute_pack_layer(layer, ctx, cwd)
            out.append((label, body))
    return out


def detect_repo_branch(cwd: str) -> tuple[str, str]:
    repo = Path(cwd).name
    branch = ""
    try:
        res = subprocess.run(
            ["git", "-C", cwd, "rev-parse", "--abbrev-ref", "HEAD"],
            capture_output=True, text=True, timeout=3,
        )
        if res.returncode == 0:
            branch = res.stdout.strip()
    except Exception:
        pass
    return repo, branch


# ----- end preload packs ----------------------------------------------------


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

    # preload packs (additive — never replaces legacy sections in phase 1)
    try:
        repo, branch = detect_repo_branch(cwd)
        packs = load_preload_packs(cwd, workspace_dir, repo, branch)
        if packs:
            context["preload_packs"] = packs
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

    if context.get("preload_packs"):
        for label, body in context["preload_packs"]:
            parts.append(label)
            parts.append(body if body else "(empty)")
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

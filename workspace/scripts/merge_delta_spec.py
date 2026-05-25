#!/usr/bin/env python3
"""
Merge feature delta-specs into the repo source-of-truth specs.

Usage:
    merge_delta_spec.py <feature> [workspace_dir] [--dry-run] [--reason "<why>"]

For the named feature:
  1. Walks .giantmem/features/{feature}/specs/{domain}/spec.md (delta-specs).
  2. For each domain, opens .giantmem/specs/{domain}/spec.md (source-spec),
     creating it from template if missing.
  3. Applies ADDED (append), MODIFIED (replace by Requirement name), REMOVED
     (delete by name).
  4. Flips the delta-spec frontmatter status to "done".
  5. Appends an entry to .giantmem/features/{feature}/spec_history.md AND
     .giantmem/specs/_history.md (both per decision 2).

Idempotent: if the source-spec already contains a Requirement with the same
name added by this delta, the ADDED op no-ops on that Requirement.

Loose rules: missing delta-specs are a silent skip, not an error.
"""

import re
import sys
import argparse
from pathlib import Path
from datetime import datetime, timezone
from dataclasses import dataclass, field


REQ_HEADING = re.compile(r"^### Requirement:\s*(.+?)\s*$")
SECTION_HEADING = re.compile(r"^##\s+(ADDED|MODIFIED|REMOVED)\s+Requirements\s*$",
                             re.IGNORECASE)


@dataclass
class Spec:
    frontmatter: dict
    body_prefix: str
    requirements: dict
    body_suffix: str


@dataclass
class Delta:
    domain: str
    path: Path
    added: dict = field(default_factory=dict)
    modified: dict = field(default_factory=dict)
    removed: list = field(default_factory=list)


def parse_frontmatter(text: str):
    if not text.startswith("---\n"):
        return {}, text
    end = text.find("\n---\n", 4)
    if end < 0:
        return {}, text
    block = text[4:end]
    body = text[end + 5:]
    fm = {}
    for line in block.split("\n"):
        line = line.rstrip()
        if not line or line.startswith("#"):
            continue
        if ":" in line:
            k, _, v = line.partition(":")
            fm[k.strip()] = v.strip()
    return fm, body


def render_frontmatter(fm: dict) -> str:
    keys = ["type", "feature", "repo", "branch", "domain", "status",
            "name", "created", "updated", "tags"]
    seen = set()
    lines = ["---"]
    for k in keys:
        if k in fm:
            lines.append(f"{k}: {fm[k]}")
            seen.add(k)
    for k, v in fm.items():
        if k not in seen:
            lines.append(f"{k}: {v}")
    lines.append("---\n")
    return "\n".join(lines)


def parse_requirements(body: str) -> dict:
    """Find ### Requirement: blocks anywhere in body. Returns {name: block_text}."""
    lines = body.split("\n")
    requirements = {}
    current_name = None
    current_buf = []
    for line in lines:
        m = REQ_HEADING.match(line)
        if m:
            if current_name is not None:
                requirements[current_name] = "\n".join(current_buf).rstrip()
            current_name = m.group(1).strip()
            current_buf = [line]
            continue
        if current_name is not None:
            if SECTION_HEADING.match(line):
                break
            if line.startswith("## "):
                break
            current_buf.append(line)
    if current_name is not None:
        requirements[current_name] = "\n".join(current_buf).rstrip()
    return requirements


def parse_delta(path: Path, domain: str) -> Delta:
    text = path.read_text()
    _, body = parse_frontmatter(text)
    delta = Delta(domain=domain, path=path)

    section = None
    section_lines = []
    sections = {}

    def flush():
        nonlocal section, section_lines
        if section is not None:
            sections[section] = "\n".join(section_lines)
        section = None
        section_lines = []

    for line in body.split("\n"):
        m = SECTION_HEADING.match(line)
        if m:
            flush()
            section = m.group(1).upper()
            continue
        if section is not None:
            section_lines.append(line)
    flush()

    if "ADDED" in sections:
        delta.added = parse_requirements(sections["ADDED"])
    if "MODIFIED" in sections:
        delta.modified = parse_requirements(sections["MODIFIED"])
    if "REMOVED" in sections:
        delta.removed = [name for name in parse_requirements(sections["REMOVED"]).keys()]
    return delta


def load_or_init_source(workspace: Path, domain: str, today: str,
                       repo: str, branch: str) -> Spec:
    path = workspace / "specs" / domain / "spec.md"
    if not path.exists():
        path.parent.mkdir(parents=True, exist_ok=True)
        fm = {
            "type": "source-spec",
            "domain": domain,
            "status": "done",
            "repo": repo,
            "branch": branch,
            "created": today,
            "updated": today,
        }
        body_prefix = (
            f"\n# {domain} Specification\n\n"
            "## Purpose\n\n<!-- one line: what this domain governs -->\n\n"
            "## Requirements\n\n"
        )
        return Spec(fm, body_prefix, {}, "")

    text = path.read_text()
    fm, body = parse_frontmatter(text)
    body_prefix, requirements, body_suffix = split_requirements_block(body)
    return Spec(fm, body_prefix, requirements, body_suffix)


def split_requirements_block(body: str):
    """Split body into (before-Requirements, requirements_dict, after-last-requirement-section)."""
    lines = body.split("\n")
    req_start = None
    for i, line in enumerate(lines):
        if re.match(r"^##\s+Requirements\s*$", line):
            req_start = i
            break
    if req_start is None:
        return body, {}, ""

    after_req_start = req_start + 1
    next_h2 = None
    for j in range(after_req_start, len(lines)):
        if lines[j].startswith("## ") and not re.match(r"^##\s+Requirements\s*$", lines[j]):
            next_h2 = j
            break
    end = next_h2 if next_h2 is not None else len(lines)

    prefix = "\n".join(lines[:after_req_start]) + "\n"
    req_block = "\n".join(lines[after_req_start:end])
    suffix = "\n".join(lines[end:]) if next_h2 is not None else ""
    requirements = parse_requirements(req_block)
    return prefix, requirements, suffix


def render_source(spec: Spec, today: str) -> str:
    spec.frontmatter["updated"] = today
    out = render_frontmatter(spec.frontmatter)
    out += spec.body_prefix.rstrip() + "\n\n"
    for name in sorted(spec.requirements.keys()):
        out += spec.requirements[name].rstrip() + "\n\n"
    if spec.body_suffix:
        out += spec.body_suffix.rstrip() + "\n"
    return out


def apply_delta(spec: Spec, delta: Delta):
    """Mutate spec in place. Returns (added_count, modified_count, removed_count, skipped_idempotent)."""
    added = 0
    modified = 0
    removed = 0
    skipped = 0
    for name, block in delta.added.items():
        if name in spec.requirements:
            if spec.requirements[name].strip() == block.strip():
                skipped += 1
            else:
                spec.requirements[name] = block
                modified += 1
        else:
            spec.requirements[name] = block
            added += 1
    for name, block in delta.modified.items():
        if name in spec.requirements:
            spec.requirements[name] = block
            modified += 1
        else:
            spec.requirements[name] = block
            added += 1
    for name in delta.removed:
        if name in spec.requirements:
            del spec.requirements[name]
            removed += 1
    return added, modified, removed, skipped


def flip_delta_status(path: Path, today: str):
    text = path.read_text()
    fm, body = parse_frontmatter(text)
    if fm.get("status") == "done":
        return
    fm["status"] = "done"
    fm["updated"] = today
    path.write_text(render_frontmatter(fm) + body)


def detect_repo(workspace: Path):
    import subprocess
    repo_root = workspace.parent if workspace.name == ".giantmem" else workspace
    repo_name = repo_root.name
    branch = "unknown"
    try:
        r = subprocess.run(
            ["git", "-C", str(repo_root), "rev-parse", "--abbrev-ref", "HEAD"],
            capture_output=True, text=True, check=False,
        )
        if r.returncode == 0:
            branch = r.stdout.strip()
    except FileNotFoundError:
        pass
    return repo_name, branch


def write_history(workspace: Path, feature: str, deltas: list, results: list,
                  reason: str, today: str):
    domains = sorted({d.domain for d in deltas})
    total_added = sum(r[0] for r in results)
    total_mod = sum(r[1] for r in results)
    total_rem = sum(r[2] for r in results)

    block = (
        f"\n## Merged {today} — feature: {feature}\n"
        f"Domains: {', '.join(domains)}\n"
        f"Added: {total_added}\n"
        f"Modified: {total_mod}\n"
        f"Removed: {total_rem}\n"
    )
    if reason:
        block += f"Reason: {reason}\n"

    feature_hist = workspace / "features" / feature / "spec_history.md"
    feature_hist.parent.mkdir(parents=True, exist_ok=True)
    if not feature_hist.exists():
        feature_hist.write_text(f"# {feature} spec history\n")
    with feature_hist.open("a") as f:
        f.write(block)

    repo_hist = workspace / "specs" / "_history.md"
    repo_hist.parent.mkdir(parents=True, exist_ok=True)
    if not repo_hist.exists():
        repo_hist.write_text("# Spec Merge History\n\n<!-- Append-only log -->\n")
    with repo_hist.open("a") as f:
        f.write(block)


def update_specs_index(workspace: Path, deltas: list, today: str):
    path = workspace / "specs" / "_index.md"
    if not path.exists():
        return
    existing = path.read_text()
    seen_domains = set()
    for line in existing.split("\n"):
        m = re.match(r"^\|\s*([^\s|]+)", line)
        if m and m.group(1) not in ("Domain", "---", ""):
            seen_domains.add(m.group(1))

    new_rows = []
    for d in deltas:
        if d.domain in seen_domains:
            continue
        spec_path = workspace / "specs" / d.domain / "spec.md"
        req_count = 0
        if spec_path.exists():
            _, body = parse_frontmatter(spec_path.read_text())
            req_count = len(parse_requirements(body))
        new_rows.append(f"| {d.domain} | {today} | {req_count} |")
    if not new_rows:
        return
    with path.open("a") as f:
        for row in new_rows:
            f.write(row + "\n")


def main():
    parser = argparse.ArgumentParser(description="Merge feature delta-specs into source-of-truth")
    parser.add_argument("feature", help="Feature name (kebab-case)")
    parser.add_argument("workspace_dir", nargs="?", default=".")
    parser.add_argument("--dry-run", action="store_true")
    parser.add_argument("--reason", default="", help="Why this merge — recorded in history")
    args = parser.parse_args()

    ws = Path(args.workspace_dir).resolve()
    if ws.name != ".giantmem":
        cand = ws / ".giantmem"
        if cand.exists():
            ws = cand
    if not ws.exists():
        print(f"error: .giantmem/ not found at {ws}", file=sys.stderr)
        sys.exit(1)

    feature_dir = ws / "features" / args.feature
    if not feature_dir.exists():
        print(f"error: feature {args.feature} not found at {feature_dir}", file=sys.stderr)
        sys.exit(1)

    specs_dir = feature_dir / "specs"
    deltas = []
    if specs_dir.exists():
        for domain_dir in sorted(p for p in specs_dir.iterdir() if p.is_dir()):
            spec_path = domain_dir / "spec.md"
            if spec_path.exists():
                deltas.append(parse_delta(spec_path, domain_dir.name))

    if not deltas:
        print(f"no delta-specs found for {args.feature}, skipping merge")
        return

    repo, branch = detect_repo(ws)
    today = datetime.now(tz=timezone.utc).strftime("%Y-%m-%d")

    print(f"feature: {args.feature}")
    print(f"domains: {', '.join(d.domain for d in deltas)}")
    print(f"reason: {args.reason or '(none)'}")
    print()

    results = []
    for d in deltas:
        spec = load_or_init_source(ws, d.domain, today, repo, branch)
        added, modified, removed, skipped = apply_delta(spec, d)
        results.append((added, modified, removed, skipped))
        print(f"  {d.domain}: +{added} ~{modified} -{removed} (skipped idempotent: {skipped})")
        if args.dry_run:
            continue
        target = ws / "specs" / d.domain / "spec.md"
        target.parent.mkdir(parents=True, exist_ok=True)
        target.write_text(render_source(spec, today))
        flip_delta_status(d.path, today)

    if args.dry_run:
        print("\n(dry-run: no files written)")
        return

    total_changes = sum(r[0] + r[1] + r[2] for r in results)
    if total_changes == 0:
        print("\nno changes — history and index not updated (idempotent re-run)")
        return

    write_history(ws, args.feature, deltas, results, args.reason, today)
    update_specs_index(ws, deltas, today)
    print("\nmerged. history + index updated.")


if __name__ == "__main__":
    main()

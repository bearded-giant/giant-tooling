#!/usr/bin/env python3
"""Deterministic /new-feature scaffolder. Replaces the 15-step LLM-interpreted procedure
with one process: status detect, branch resolve+checkout, file scaffold, json/index update,
reindex, topic pin. Model only handles judgment (rare re-prompt) + echoing open questions.

One bash call replaces ~10 tool round-trips.
"""
import argparse
import json
import os
import subprocess
import sys
from datetime import date

PAIRED_MAP = {
    os.path.expanduser("~/dev/python/cc-wt/"): {
        "root": os.path.expanduser("~/dev/javascript/frontend-wt/"),
        "base": "master",
        "cmd": ["fewta", "{branch}", "master"],
    },
    os.path.expanduser("~/dev/javascript/frontend-wt/"): {
        "root": os.path.expanduser("~/dev/python/cc-wt/"),
        "base": "stage",
        "cmd": ["cwta", "{branch}", "stage"],
    },
}
BASE_FALLBACKS = ["develop", "stage", "main", "master"]


def run(cmd, **kw):
    return subprocess.run(cmd, capture_output=True, text=True, **kw)


def git(args, cwd):
    return run(["git", *args], cwd=cwd)


def fail(msg):
    print(f"new-feature: {msg}", file=sys.stderr)
    sys.exit(1)


def title_of(name):
    return " ".join(w.capitalize() for w in name.split("-"))


def detect_base(cwd):
    r = git(["symbolic-ref", "--short", "refs/remotes/origin/HEAD"], cwd)
    if r.returncode == 0 and r.stdout.strip():
        return r.stdout.strip().removeprefix("origin/")
    for cand in BASE_FALLBACKS:
        if git(["ls-remote", "--exit-code", "--heads", "origin", cand], cwd).returncode == 0:
            return cand
    for cand in BASE_FALLBACKS:
        if git(["show-ref", "--verify", "--quiet", f"refs/heads/{cand}"], cwd).returncode == 0:
            return cand
    return None


def resolve_paired(cwd):
    top = git(["rev-parse", "--show-toplevel"], cwd).stdout.strip()
    for prefix, row in PAIRED_MAP.items():
        if top.startswith(prefix.rstrip("/")):
            return row
    return None


def checkout_branch(branch, base, cwd):
    """returns one of: ALREADY_ON, REUSED_LOCAL, REUSED_REMOTE, CREATED_FROM_BASE"""
    if git(["rev-parse", "--abbrev-ref", "HEAD"], cwd).stdout.strip() == branch:
        return "ALREADY_ON"
    if git(["show-ref", "--verify", "--quiet", f"refs/heads/{branch}"], cwd).returncode == 0:
        git(["checkout", branch], cwd)
        return "REUSED_LOCAL"
    if git(["ls-remote", "--exit-code", "--heads", "origin", branch], cwd).returncode == 0:
        git(["fetch", "origin", branch], cwd)
        git(["checkout", "-b", branch, f"origin/{branch}"], cwd)
        return "REUSED_REMOTE"
    git(["fetch", "origin", base], cwd)
    if git(["checkout", "-b", branch, f"origin/{base}"], cwd).returncode != 0:
        # base may be local-only (no origin ref) — create off local base
        git(["checkout", "-b", branch, base], cwd)
    return "CREATED_FROM_BASE"


def proposal_md(name, status, today, discovery):
    title = title_of(name)
    if status == "in_progress":
        return f"""---
type: proposal
feature: {name}
status: ready
created: {today}
updated: {today}
---

# Proposal: {title}

## Open Questions for User

<!--
ALWAYS at top. Numbered list. Mark each [BLOCKING] or [non-blocking].
Remove this section once empty. Buried questions get missed.
-->
1. ...

## Intent

<!-- one paragraph: the problem this solves -->

## Scope

In scope:
-

Out of scope:
-

## Approach

<!-- high-level technical direction. Implementation details belong in design.md. -->

## Behavior Deltas

Tracked separately in `features/{name}/specs/{{domain}}/spec.md` (delta-spec, `ADDED`/`MODIFIED`/`REMOVED`).
On `/complete-feature`, deltas merge into `.giantmem/specs/{{domain}}/spec.md` (source-spec).
"""
    intent = discovery or "<!-- describe what this feature does and why -->"
    return f"""---
type: proposal
feature: {name}
status: draft
created: {today}
updated: {today}
---

# Proposal: {title}

## Open Questions for User

<!--
ALWAYS at top. Numbered list. Mark each [BLOCKING] or [non-blocking].
Remove this section once empty. Buried questions get missed.
-->
1. ...

## Intent

{intent}

## Discovery Context

{discovery or "<!-- what prompted this stub -->"}

## Scope

In scope:
-

Out of scope:
-
"""


def tasks_md(name, today):
    return f"""---
type: tasks
feature: {name}
status: draft
created: {today}
updated: {today}
---

# Tasks

<!--
status auto-derives from checkbox %:
  0%       = draft
  0 < x < 100% = ready
  100%     = done
no manual status updates needed — `giantmem artifact list -f {name}` reflects live %.
-->

## 1. {{Section}}

- [ ] 1.1 ...
"""


def facts_md(name, today, branch, base, paired, cp_branch, cp_wt, cp_base):
    return f"""---
type: facts
feature: {name}
status: ready
created: {today}
updated: {today}
---

# {name} facts

## Branch

branch: {branch or "pending"}
base: {base or "tbd"}

## Paired Counterpart

paired: {str(paired).lower()}
counterpart_branch: {cp_branch or "n/a"}
counterpart_worktree: {cp_wt or "n/a"}
counterpart_base: {cp_base or "n/a"}

## Identifiers

beta_flag:
config_keys:
  -

## Endpoints

affected:
  -
new:
  -

## Key Files

-

## Test Commands

```bash
# add test commands here
```
"""


def notes_md(name, today):
    return f"""---
type: notes
feature: {name}
status: living
lifecycle: durable
created: {today}
updated: {today}
---

<!-- living cheat sheet for this feature: append reusable commands, queries, identifiers, env vars, scripts as we cover them during work. free-form, append-only, silent (no chat announcement). see skill: feature-management § living feature notes. -->
"""


def update_index(idx_path, name, status, builds_on, cp_branch):
    section = "## Active Features" if status == "in_progress" else "## Pending Features"
    row = f"| [{name}]({name}/) | {status} | | {builds_on or '-'} | {cp_branch or '-'} |"
    if not os.path.exists(idx_path):
        return False
    lines = open(idx_path).read().splitlines()
    try:
        si = next(i for i, ln in enumerate(lines) if ln.strip() == section)
    except StopIteration:
        return False
    # find separator row (|---|) after the section header
    sep = next((i for i in range(si, len(lines)) if lines[i].lstrip().startswith("|") and "---" in lines[i]), None)
    if sep is None:
        return False
    after = sep + 1
    if after < len(lines) and "_none_" in lines[after]:
        lines[after] = row  # replace placeholder
    else:
        lines.insert(after, row)
    open(idx_path, "w").write("\n".join(lines) + "\n")
    return True


def main():
    ap = argparse.ArgumentParser(prog="new-feature")
    ap.add_argument("name")
    ap.add_argument("--branch")
    ap.add_argument("--base")
    ap.add_argument("--builds-on", default="")
    ap.add_argument("--paired", action="store_true")
    ap.add_argument("--no-paired", action="store_true")
    ap.add_argument("--skip-checkout", action="store_true")
    ap.add_argument("--discovery", default="", help="discovery context for a pending stub")
    ap.add_argument("--cwd", default=os.getcwd())
    a = ap.parse_args()

    cwd = a.cwd
    feats_dir = os.path.join(cwd, ".giantmem", "features")
    if not os.path.isdir(feats_dir):
        fail(".giantmem/features/ not found — run /ws-init first")

    name = a.name
    fdir = os.path.join(feats_dir, name)
    if os.path.exists(os.path.join(fdir, "meta.json")):
        fail(f"feature '{name}' already exists ({fdir})")

    today = date.today().isoformat()
    fjson_path = os.path.join(feats_dir, "features.json")
    fjson = {}
    if os.path.exists(fjson_path):
        try:
            fjson = json.load(open(fjson_path))
        except json.JSONDecodeError:
            fjson = {}

    status = "pending" if any(f.get("status") == "in_progress" for f in fjson.values()) else "in_progress"

    branch = base = None
    co_state = "n/a"
    paired = False
    cp_branch = cp_wt = cp_base = None

    if status == "in_progress":
        base = a.base or detect_base(cwd)
        if not base:
            fail("could not detect base branch, pass --base explicitly")
        head = git(["rev-parse", "--abbrev-ref", "HEAD"], cwd).stdout.strip()
        # default branch = feature name when sitting on the base branch (avoids the base==branch guard)
        branch = a.branch or (name if head == base else head)
        if branch == base:
            fail(f"feature branch and base branch are the same ('{branch}') — pass --branch= explicitly")
        if not a.skip_checkout:
            co_state = checkout_branch(branch, base, cwd)

        # paired counterpart
        row = resolve_paired(cwd)
        if a.no_paired or row is None:
            paired = False
            if a.paired and row is None:
                print("note: --paired ignored (repo has no paired counterpart in map)", file=sys.stderr)
        elif a.paired:
            paired = True
            cp_branch = branch
            cp_base = row["base"]
            cp_wt = row["root"] + cp_branch
            cmd = [c.format(branch=cp_branch) for c in row["cmd"]]
            r = run(cmd)
            if r.returncode != 0:
                bare = row["root"].rstrip("/") + ".bare"
                run(["git", "-C", bare, "worktree", "add", "-b", cp_branch,
                     f"../{cp_branch}", f"origin/{cp_base}"])

    # scaffold
    os.makedirs(os.path.join(fdir, "specs"), exist_ok=True)
    open(os.path.join(fdir, "proposal.md"), "w").write(proposal_md(name, status, today, a.discovery))
    open(os.path.join(fdir, "tasks.md"), "w").write(tasks_md(name, today))
    open(os.path.join(fdir, "facts.md"), "w").write(
        facts_md(name, today, branch, base, paired, cp_branch, cp_wt, cp_base))
    open(os.path.join(fdir, f"{name}-notes.md"), "w").write(notes_md(name, today))

    fe = {"enabled": False}
    if paired:
        fe = {"enabled": True, "branch": cp_branch, "base_branch": cp_base, "worktree": cp_wt}
    meta = {
        "name": name, "status": status, "branch": branch or "", "base_branch": base or "",
        "builds_on": [a.builds_on] if a.builds_on else [], "beta_flag": "",
        "frontend": fe, "created": today, "last_session": today,
    }
    json.dump(meta, open(os.path.join(fdir, "meta.json"), "w"), indent=2)

    fjson[name] = {
        "name": name, "status": status, "branch": branch or "", "base_branch": base or "",
        "builds_on": a.builds_on or "none", "beta_flag": "",
        "frontend": fe if paired else None, "created": today, "last_session": today,
    }
    json.dump(fjson, open(fjson_path, "w"), indent=2)

    idx_ok = update_index(os.path.join(feats_dir, "_index.md"), name, status, a.builds_on, cp_branch)

    # reindex + topic pin (best-effort)
    reindex = "skipped (giantmem not on PATH)"
    topic = "skipped"
    if run(["which", "giantmem"]).returncode == 0:
        r = run(["giantmem", "artifact", "reindex"], cwd=cwd)
        reindex = (r.stdout.strip().splitlines() or ["done"])[-1]
        proj = os.path.expanduser("~/.claude/projects/" + cwd.replace("/", "-"))
        if os.path.isdir(proj):
            jsonls = sorted(
                (f for f in os.listdir(proj) if f.endswith(".jsonl")),
                key=lambda f: os.path.getmtime(os.path.join(proj, f)), reverse=True)
            if jsonls:
                sid = jsonls[0][:-6]
                if run(["giantmem", "session", "set-topic", sid, name], cwd=cwd).returncode == 0:
                    topic = f"pinned {sid} -> {name}"

    out = {
        "feature": name, "status": status,
        "status_reason": "no active feature" if status == "in_progress"
        else "another feature is in_progress",
        "branch": branch, "base": base, "checkout": co_state,
        "paired": paired, "counterpart_worktree": cp_wt,
        "files": ["proposal.md", "tasks.md", "facts.md", f"{name}-notes.md", "meta.json"],
        "index_updated": idx_ok, "reindex": reindex, "topic": topic,
        "open_questions": "none (placeholder only)",
        "dir": fdir,
    }
    print(json.dumps(out, indent=2))


if __name__ == "__main__":
    main()

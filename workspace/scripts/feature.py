#!/usr/bin/env python3
"""Deterministic feature-lifecycle CLI. One process per verb replaces the long
LLM-interpreted command procedures (new/start/pause/reopen/complete/migrate/facts/next).

The mechanical parts — status flips across proposal.md/meta.json/features.json/_index.md,
branch resolve+checkout, delta-spec merge, reindex, topic pin — run here. Prose slots
(resumption notes, facts cleanup, domain refresh) are optional flags or left to the model.

features.json status is the authoritative "active feature" signal (session-recovery reads it);
plans/current.md is freeform and left to the model.
"""
import argparse
import json
import os
import re
import subprocess
import sys
from datetime import date
from typing import NoReturn

PAIRED_MAP = {
    os.path.expanduser("~/dev/python/cc-wt/"): {
        "root": os.path.expanduser("~/dev/javascript/frontend-wt/"),
        "base": "master", "cmd": ["fewta", "{branch}", "master"]},
    os.path.expanduser("~/dev/javascript/frontend-wt/"): {
        "root": os.path.expanduser("~/dev/python/cc-wt/"),
        "base": "stage", "cmd": ["cwta", "{branch}", "stage"]},
}
BASE_FALLBACKS = ["develop", "stage", "main", "master"]
# type -> (requires, optional). Used by `next` when ~/.claude/config/artifact_dag.yaml is absent.
DEFAULT_DAG = {
    "proposal": ([], False), "delta-spec": (["proposal"], True),
    "design": (["proposal"], True), "tasks": (["proposal"], False),
    "review": (["tasks"], True),
}


def run(cmd, **kw):
    return subprocess.run(cmd, capture_output=True, text=True, **kw)


def git(args, cwd):
    return run(["git", *args], cwd=cwd)


def fail(msg) -> NoReturn:
    print(f"feature: {msg}", file=sys.stderr)
    sys.exit(1)


def title_of(name):
    return " ".join(w.capitalize() for w in name.split("-"))


def feats_dir(cwd):
    d = os.path.join(cwd, ".giantmem", "features")
    if not os.path.isdir(d):
        fail(".giantmem/features/ not found — run /ws-init first")
    return d


def load_features(fd):
    p = os.path.join(fd, "features.json")
    if os.path.exists(p):
        try:
            return json.load(open(p)), p
        except json.JSONDecodeError:
            pass
    return {}, p


def proposal_path(fdir):
    """live file is proposal.md; spec.md is a legacy symlink."""
    for n in ("proposal.md", "spec.md"):
        p = os.path.join(fdir, n)
        if os.path.exists(p):
            return p
    return os.path.join(fdir, "proposal.md")


# ---- frontmatter helpers (operate on the --- block at top of a .md) ----
def _fm_bounds(lines):
    if not lines or lines[0].strip() != "---":
        return None
    for i in range(1, len(lines)):
        if lines[i].strip() == "---":
            return (1, i)
    return None


def fm_set(text, key, value):
    lines = text.splitlines()
    b = _fm_bounds(lines)
    if not b:
        return text
    lo, hi = b
    for i in range(lo, hi):
        if re.match(rf"^{re.escape(key)}:", lines[i]):
            lines[i] = f"{key}: {value}"
            return "\n".join(lines) + ("\n" if text.endswith("\n") else "")
    lines.insert(hi, f"{key}: {value}")
    return "\n".join(lines) + ("\n" if text.endswith("\n") else "")


def fm_remove(text, key):
    lines = text.splitlines()
    b = _fm_bounds(lines)
    if not b:
        return text
    lo, hi = b
    out = [ln for idx, ln in enumerate(lines)
           if not (lo <= idx < hi and re.match(rf"^{re.escape(key)}:", ln))]
    return "\n".join(out) + ("\n" if text.endswith("\n") else "")


def edit_file(path, fn):
    if not os.path.exists(path):
        return False
    open(path, "w").write(fn(open(path).read()))
    return True


def append_section(text, header, body):
    sep = "" if text.endswith("\n") else "\n"
    return f"{text}{sep}\n{header}\n\n{body}\n"


def strip_section(text, header):
    """remove `header` and everything until the next `## ` header or EOF."""
    lines = text.splitlines()
    out, skip = [], False
    for ln in lines:
        if ln.strip() == header:
            skip = True
            continue
        if skip and ln.startswith("## "):
            skip = False
        if not skip:
            out.append(ln)
    return "\n".join(out).rstrip() + "\n"


def section_body(text, header):
    """return lines of `header` section (for surfacing resumption notes)."""
    lines, grab, out = text.splitlines(), False, []
    for ln in lines:
        if ln.strip() == header:
            grab = True
            continue
        if grab and (ln.startswith("## ") or ln.startswith("### ")):
            break
        if grab:
            out.append(ln)
    return "\n".join(out).strip()


# ---- _index.md row move ----
SECTION_FOR = {"in_progress": "## Active Features", "pending": "## Pending Features",
               "paused": "## Paused Features", "complete": "## Completed Features"}


def move_index_row(idx_path, name, status, builds_on="", beta="", cp_branch=""):
    if not os.path.exists(idx_path):
        return False
    lines = open(idx_path).read().splitlines()
    lines = [ln for ln in lines if f"[{name}]({name}/)" not in ln]  # drop old row anywhere
    section = SECTION_FOR.get(status)
    if not section:
        return False
    try:
        si = next(i for i, ln in enumerate(lines) if ln.strip() == section)
    except StopIteration:
        return False
    sep = next((i for i in range(si, len(lines))
                if lines[i].lstrip().startswith("|") and "---" in lines[i]), None)
    if sep is None:
        return False
    if status == "complete":  # completed table has no Paired column
        row = f"| [{name}]({name}/) | {status} | {beta or ''} | {builds_on or '-'} |"
    else:
        row = f"| [{name}]({name}/) | {status} | {beta or ''} | {builds_on or '-'} | {cp_branch or '-'} |"
    after = sep + 1
    if after < len(lines) and "_none_" in lines[after]:
        lines[after] = row
    else:
        lines.insert(after, row)
    open(idx_path, "w").write("\n".join(lines) + "\n")
    return True


# ---- git branch resolution ----
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
        git(["checkout", "-b", branch, base], cwd)
    return "CREATED_FROM_BASE"


def reindex_and_pin(cwd, name):
    reindex, topic = "skipped (giantmem not on PATH)", "skipped"
    if run(["which", "giantmem"]).returncode == 0:
        r = run(["giantmem", "artifact", "reindex"], cwd=cwd)
        reindex = (r.stdout.strip().splitlines() or ["done"])[-1]
        proj = os.path.expanduser("~/.claude/projects/" + cwd.replace("/", "-"))
        if os.path.isdir(proj):
            js = sorted((f for f in os.listdir(proj) if f.endswith(".jsonl")),
                        key=lambda f: os.path.getmtime(os.path.join(proj, f)), reverse=True)
            if js and run(["giantmem", "session", "set-topic", js[0][:-6], name], cwd=cwd).returncode == 0:
                topic = f"pinned -> {name}"
    return reindex, topic


# ---- templates (new) ----
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


def facts_md(name, today, branch, base, paired, cpb, cpw, cpbase):
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
counterpart_branch: {cpb or "n/a"}
counterpart_worktree: {cpw or "n/a"}
counterpart_base: {cpbase or "n/a"}

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


def emit(obj):
    print(json.dumps(obj, indent=2))


# ---------------- subcommands ----------------
def cmd_new(a, cwd):
    fd = feats_dir(cwd)
    name = a.name
    fdir = os.path.join(fd, name)
    if os.path.exists(os.path.join(fdir, "meta.json")):
        fail(f"feature '{name}' already exists ({fdir})")
    today = date.today().isoformat()
    fjson, fjson_path = load_features(fd)
    status = "pending" if any(f.get("status") == "in_progress" for f in fjson.values()) else "in_progress"

    branch = base = None
    co_state = "n/a"
    paired = False
    cpb = cpw = cpbase = None
    if status == "in_progress":
        base = a.base or detect_base(cwd)
        if not base:
            fail("could not detect base branch, pass --base explicitly")
        head = git(["rev-parse", "--abbrev-ref", "HEAD"], cwd).stdout.strip()
        branch = a.branch or (name if head == base else head)
        if branch == base:
            fail(f"feature branch and base branch are the same ('{branch}') — pass --branch= explicitly")
        if not a.skip_checkout:
            co_state = checkout_branch(branch, base, cwd)
        row = resolve_paired(cwd)
        if a.no_paired or row is None:
            paired = False
            if a.paired and row is None:
                print("note: --paired ignored (no paired counterpart in map)", file=sys.stderr)
        elif a.paired:
            paired, cpb, cpbase = True, branch, row["base"]
            cpw = row["root"] + cpb
            if run([c.format(branch=cpb) for c in row["cmd"]]).returncode != 0:
                bare = row["root"].rstrip("/") + ".bare"
                run(["git", "-C", bare, "worktree", "add", "-b", cpb, f"../{cpb}", f"origin/{cpbase}"])

    os.makedirs(os.path.join(fdir, "specs"), exist_ok=True)
    open(os.path.join(fdir, "proposal.md"), "w").write(proposal_md(name, status, today, a.discovery))
    open(os.path.join(fdir, "tasks.md"), "w").write(tasks_md(name, today))
    open(os.path.join(fdir, "facts.md"), "w").write(facts_md(name, today, branch, base, paired, cpb, cpw, cpbase))
    open(os.path.join(fdir, f"{name}-notes.md"), "w").write(notes_md(name, today))

    fe = {"enabled": True, "branch": cpb, "base_branch": cpbase, "worktree": cpw} if paired else {"enabled": False}
    meta = {"name": name, "status": status, "branch": branch or "", "base_branch": base or "",
            "builds_on": [a.builds_on] if a.builds_on else [], "beta_flag": "",
            "frontend": fe, "created": today, "last_session": today}
    json.dump(meta, open(os.path.join(fdir, "meta.json"), "w"), indent=2)
    fjson[name] = {"name": name, "status": status, "branch": branch or "", "base_branch": base or "",
                   "builds_on": a.builds_on or "none", "beta_flag": "",
                   "frontend": fe if paired else None, "created": today, "last_session": today}
    json.dump(fjson, open(fjson_path, "w"), indent=2)
    idx = move_index_row(os.path.join(fd, "_index.md"), name, status, a.builds_on, "", cpb or "")
    reindex, topic = reindex_and_pin(cwd, name)
    emit({"verb": "new", "feature": name, "status": status,
          "status_reason": "no active feature" if status == "in_progress" else "another feature in_progress",
          "branch": branch, "base": base, "checkout": co_state, "paired": paired,
          "counterpart_worktree": cpw, "index_updated": idx, "reindex": reindex, "topic": topic,
          "open_questions": "none (placeholder only)", "dir": fdir})


def _resolve_feature(a, fd, want_statuses=None, infer_active=False):
    fjson, _ = load_features(fd)
    name = getattr(a, "feature", None)
    if not name and infer_active:
        actives = [n for n, f in fjson.items() if f.get("status") == "in_progress"]
        if len(actives) == 1:
            name = actives[0]
        elif len(actives) > 1:
            fail(f"multiple in_progress features {actives} — pass a name")
        else:
            fail("no in_progress feature — pass a name")
    if not name:
        pend = [n for n, f in fjson.items() if not want_statuses or f.get("status") in want_statuses]
        fail(f"feature name required. candidates: {pend}")
    fdir = os.path.join(fd, name)
    if not os.path.isdir(fdir):
        fail(f"feature '{name}' not found at {fdir}")
    cur = fjson.get(name, {}).get("status")
    if want_statuses and cur not in want_statuses:
        fail(f"feature '{name}' is '{cur}', expected one of {want_statuses}")
    return name, fdir, fjson, cur


def _save_feature_status(fd, fjson, name, **fields):
    fjson.setdefault(name, {"name": name}).update(fields)
    json.dump(fjson, open(os.path.join(fd, "features.json"), "w"), indent=2)
    mp = os.path.join(fd, name, "meta.json")
    if os.path.exists(mp):
        try:
            m = json.load(open(mp))
            m.update(fields)
            json.dump(m, open(mp, "w"), indent=2)
        except json.JSONDecodeError:
            pass


def cmd_start(a, cwd):
    fd = feats_dir(cwd)
    name, fdir, fjson, _ = _resolve_feature(a, fd, want_statuses={"pending"})
    today = date.today().isoformat()
    entry = fjson.get(name, {})
    base = a.base or entry.get("base_branch") or detect_base(cwd)
    if not base:
        fail("could not detect base branch, pass --base explicitly")
    branch = a.branch or entry.get("branch") or name
    co = checkout_branch(branch, base, cwd) if not a.skip_checkout else "skipped"
    pp = proposal_path(fdir)
    edit_file(pp, lambda t: fm_set(fm_set(t, "status", "in_progress"), "started", today))
    edit_file(os.path.join(fdir, "facts.md"),
              lambda t: re.sub(r"(?m)^base: .*$", f"base: {base}",
                               re.sub(r"(?m)^branch: .*$", f"branch: {branch}", t)))
    _save_feature_status(fd, fjson, name, status="in_progress", branch=branch,
                         base_branch=base, last_session=today)
    idx = move_index_row(os.path.join(fd, "_index.md"), name, "in_progress",
                         entry.get("builds_on", ""), entry.get("beta_flag", ""))
    reindex, topic = reindex_and_pin(cwd, name)
    emit({"verb": "start", "feature": name, "status": "in_progress", "branch": branch,
          "base": base, "checkout": co, "index_updated": idx, "reindex": reindex, "topic": topic,
          "note": "plans/current.md + spec section expansion left to model if needed"})


def cmd_pause(a, cwd):
    fd = feats_dir(cwd)
    name, fdir, fjson, _ = _resolve_feature(a, fd, want_statuses={"in_progress"}, infer_active=True)
    today = date.today().isoformat()
    note = a.note or "<!-- fill: what was in progress, next steps, blockers -->"
    pstate = a.paused_state or "<!-- fill: last known working state, partial work not captured elsewhere -->"
    pp = proposal_path(fdir)
    edit_file(pp, lambda t: append_section(fm_set(fm_set(t, "status", "paused"), "paused", today),
                                           "## Resumption Notes", note))
    edit_file(os.path.join(fdir, "facts.md"),
              lambda t: append_section(t, "## Paused State", pstate))
    _save_feature_status(fd, fjson, name, status="paused", last_session=today)
    entry = fjson.get(name, {})
    idx = move_index_row(os.path.join(fd, "_index.md"), name, "paused",
                         entry.get("builds_on", ""), entry.get("beta_flag", ""))
    reindex, _ = reindex_and_pin(cwd, name)
    emit({"verb": "pause", "feature": name, "status": "paused", "index_updated": idx,
          "reindex": reindex, "resumption_note": "stub" if not a.note else "provided",
          "resume_with": f"/reopen-feature {name}"})


def cmd_reopen(a, cwd):
    fd = feats_dir(cwd)
    name, fdir, fjson, prev = _resolve_feature(a, fd, want_statuses={"complete", "paused"})
    today = date.today().isoformat()
    entry = fjson.get(name, {})
    branch = entry.get("branch") or ""
    co = "no branch recorded"
    if branch and not a.skip_checkout:
        if git(["show-ref", "--verify", "--quiet", f"refs/heads/{branch}"], cwd).returncode == 0:
            git(["checkout", branch], cwd)
            co = "REUSED_LOCAL"
        elif git(["ls-remote", "--exit-code", "--heads", "origin", branch], cwd).returncode == 0:
            git(["fetch", "origin", branch], cwd)
            git(["checkout", "-b", branch, f"origin/{branch}"], cwd)
            co = "REUSED_REMOTE"
        else:
            co = f"branch '{branch}' gone (local+remote) — recreate manually"
    pp = proposal_path(fdir)
    resume = ""
    if os.path.exists(pp):
        resume = section_body(open(pp).read(), "## Resumption Notes")
    edit_file(pp, lambda t: fm_remove(fm_remove(fm_set(t, "status", "in_progress"), "completed"), "paused"))
    edit_file(os.path.join(fdir, "facts.md"), lambda t: strip_section(t, "## Paused State"))
    _save_feature_status(fd, fjson, name, status="in_progress", last_session=today)
    idx = move_index_row(os.path.join(fd, "_index.md"), name, "in_progress",
                         entry.get("builds_on", ""), entry.get("beta_flag", ""))
    reindex, topic = reindex_and_pin(cwd, name)
    emit({"verb": "reopen", "feature": name, "from": prev, "status": "in_progress",
          "branch": branch or None, "checkout": co, "index_updated": idx, "reindex": reindex,
          "topic": topic, "resumption_notes": resume or None,
          "note": "review acceptance criteria; uncheck reworked items manually"})


def cmd_complete(a, cwd):
    fd = feats_dir(cwd)
    name, fdir, fjson, _ = _resolve_feature(a, fd, want_statuses={"in_progress", "paused"}, infer_active=True)
    today = date.today().isoformat()
    pp = proposal_path(fdir)
    edit_file(pp, lambda t: fm_set(fm_set(t, "status", "done"), "completed", today))
    _save_feature_status(fd, fjson, name, status="complete", last_session=today)
    entry = fjson.get(name, {})
    idx = move_index_row(os.path.join(fd, "_index.md"), name, "complete",
                         entry.get("builds_on", ""), entry.get("beta_flag", ""))

    merge = "skipped (--quick)" if a.quick else "skipped (no delta-specs)"
    specs = os.path.join(fdir, "specs")
    has_specs = False
    if os.path.isdir(specs):
        has_specs = any(os.path.isdir(os.path.join(specs, d)) for d in os.listdir(specs))
    if not a.quick and not a.no_merge and has_specs:
        msp = os.path.expanduser("~/dev/giant-tooling/workspace/scripts/merge_delta_spec.py")
        if os.path.exists(msp):
            r = run(["python3", msp, name, "--reason", a.reason or f"{name} completion"], cwd=cwd)
            merge = (r.stdout.strip().splitlines() or ["merged"])[-1] if r.returncode == 0 else f"merge failed: {r.stderr.strip()[:120]}"
        else:
            merge = "merge_delta_spec.py not found"

    reindex, _ = reindex_and_pin(cwd, name)
    fe = entry.get("frontend") or {}
    paired = fe.get("worktree") if fe.get("enabled") else None
    emit({"verb": "complete", "feature": name, "status": "complete", "quick": a.quick,
          "merge": merge, "index_updated": idx, "reindex": reindex,
          "paired_counterpart": paired and f"{fe.get('branch')} at {paired} — needs separate MR",
          "note": "facts cleanup + acceptance-criteria marking + domain refresh left to model if needed"})


def cmd_migrate(a, cwd):
    fd = feats_dir(cwd)
    out = {}
    for d in sorted(os.listdir(fd)):
        fdir = os.path.join(fd, d)
        if not os.path.isdir(fdir):
            continue
        mp = os.path.join(fdir, "meta.json")
        if os.path.exists(mp):
            try:
                m = json.load(open(mp))
            except json.JSONDecodeError:
                m = {}
        else:
            m = {}
        pp = proposal_path(fdir)
        ftxt = open(os.path.join(fdir, "facts.md")).read() if os.path.exists(os.path.join(fdir, "facts.md")) else ""
        ptxt = open(pp).read() if os.path.exists(pp) else ""

        def frm(text, key):
            mm = re.search(rf"(?m)^{key}:\s*(.+)$", text)
            return mm.group(1).strip() if mm else ""
        status = m.get("status") or frm(ptxt, "status") or "unknown"
        created = m.get("created") or frm(ptxt, "created") or "n/a"
        last = m.get("last_session") or m.get("completed") or created
        if last == "n/a" and os.path.exists(pp):
            from datetime import datetime
            last = datetime.fromtimestamp(os.path.getmtime(pp)).date().isoformat()
        bo = m.get("builds_on")
        bo = (bo[0] if isinstance(bo, list) and bo else bo) or frm(ptxt, "builds_on") or "none"
        out[d] = {"name": d, "status": status,
                  "branch": m.get("branch") or frm(ftxt, "branch") or "",
                  "base_branch": m.get("base_branch") or frm(ftxt, "base") or "",
                  "builds_on": bo, "beta_flag": m.get("beta_flag") or frm(ftxt, "beta_flag") or "",
                  "frontend": m.get("frontend"), "created": created, "last_session": last}
    json.dump(out, open(os.path.join(fd, "features.json"), "w"), indent=2)
    emit({"verb": "migrate", "indexed": len(out),
          "features": {n: out[n]["status"] for n in out}})


def cmd_facts(a, cwd):
    fd = feats_dir(cwd)
    cands = [d for d in sorted(os.listdir(fd))
             if os.path.isdir(os.path.join(fd, d)) and a.name in d]
    if not cands:
        fail(f"no feature matching '{a.name}'")
    exact = [c for c in cands if c == a.name]
    if len(cands) > 1 and not exact:
        print(json.dumps({"verb": "facts", "ambiguous": cands}, indent=2))
        return
    name = exact[0] if exact else cands[0]
    fdir = os.path.join(fd, name)
    fp = os.path.join(fdir, "facts.md")
    if not os.path.exists(fp):
        fail(f"no facts.md for '{name}'")
    last = ""
    mp = os.path.join(fdir, "meta.json")
    if os.path.exists(mp):
        try:
            last = json.load(open(mp)).get("last_session", "")
        except json.JSONDecodeError:
            pass
    print(f"# feature: {name}" + (f"   last_session: {last}" if last else ""))
    print()
    print(open(fp).read())


def _load_dag():
    p = os.path.expanduser("~/.claude/config/artifact_dag.yaml")
    if os.path.exists(p):
        try:
            import yaml
            raw = yaml.safe_load(open(p)) or {}
            dag = {}
            for typ, spec in raw.items():
                if isinstance(spec, dict):
                    dag[typ] = (spec.get("requires", []) or [], bool(spec.get("optional", False)))
            if dag:
                return dag
        except Exception:
            pass
    return DEFAULT_DAG


def cmd_next(a, cwd):
    fd = feats_dir(cwd)
    fjson, _ = load_features(fd)
    name = a.feature
    if not name:
        actives = [n for n, f in fjson.items() if f.get("status") == "in_progress"]
        if len(actives) == 1:
            name = actives[0]
        elif not actives:
            fail("no active feature")
        else:
            fail(f"multiple in_progress {actives} — pass a name")
    branch = fjson.get(name, {}).get("branch", "")
    aj = os.path.join(cwd, ".giantmem", "artifacts.json")
    present = {}  # type -> set(status)
    if os.path.exists(aj):
        d = json.load(open(aj))
        arts = d if isinstance(d, list) else d.get("artifacts", list(d.values()) if isinstance(d, dict) else [])
        for art in arts:
            if not isinstance(art, dict):
                continue
            if f"features/{name}/" in (art.get("path") or ""):
                present.setdefault(art.get("type"), set()).add(art.get("status"))
    dag = _load_dag()

    def done(typ):
        return "done" in present.get(typ, set())
    done_t = [t for t in dag if done(t)]
    ready, blocked = [], []
    for typ, (reqs, opt) in dag.items():
        if done(typ):
            continue
        if all(done(r) for r in reqs):
            statuses = present.get(typ, set())
            if not statuses or "draft" in statuses or "ready" in statuses:
                ready.append((typ, opt))
        elif not opt:
            blocked.append((typ, [r for r in reqs if not done(r)]))
    order = list(dag.keys())
    ready.sort(key=lambda x: (order.index(x[0]) if x[0] in order else 99, x[0]))
    print(f"feature: {name}  branch: {branch or '-'}")
    print()
    if ready:
        nt, nopt = ready[0]
        print(f"next ready: {nt}" + ("  (optional)" if nopt else ""))
        print(f"  path: .giantmem/features/{name}/{nt}.md")
        for t, opt in ready[1:]:
            print(f"also ready: {t}" + ("  (optional)" if opt else ""))
    elif not done_t:
        print("start with proposal — ~/.claude/templates/proposal.md")
    else:
        print("all DAG nodes done — /complete-feature when ready")
    if blocked:
        print("blocked:")
        for t, miss in blocked:
            print(f"  - {t}  (requires {', '.join(miss)} done)")
    if done_t:
        print("done: " + ", ".join(done_t))


def build_parser():
    p = argparse.ArgumentParser(prog="feature")
    p.add_argument("--cwd", default=os.getcwd())
    sub = p.add_subparsers(dest="verb", required=True)

    n = sub.add_parser("new")
    n.add_argument("name")
    n.add_argument("--branch")
    n.add_argument("--base")
    n.add_argument("--builds-on", default="")
    n.add_argument("--paired", action="store_true")
    n.add_argument("--no-paired", action="store_true")
    n.add_argument("--skip-checkout", action="store_true")
    n.add_argument("--discovery", default="")
    n.set_defaults(fn=cmd_new)

    s = sub.add_parser("start")
    s.add_argument("feature", nargs="?")
    s.add_argument("--branch")
    s.add_argument("--base")
    s.add_argument("--skip-checkout", action="store_true")
    s.set_defaults(fn=cmd_start)

    pa = sub.add_parser("pause")
    pa.add_argument("feature", nargs="?")
    pa.add_argument("--note", help="resumption note (else placeholder)")
    pa.add_argument("--paused-state", help="paused-state snapshot (else placeholder)")
    pa.set_defaults(fn=cmd_pause)

    r = sub.add_parser("reopen")
    r.add_argument("feature", nargs="?")
    r.add_argument("--skip-checkout", action="store_true")
    r.set_defaults(fn=cmd_reopen)

    c = sub.add_parser("complete")
    c.add_argument("feature", nargs="?")
    c.add_argument("--quick", action="store_true")
    c.add_argument("--no-merge", action="store_true")
    c.add_argument("--reason", default="")
    c.set_defaults(fn=cmd_complete)

    sub.add_parser("migrate").set_defaults(fn=cmd_migrate)

    f = sub.add_parser("facts")
    f.add_argument("name")
    f.set_defaults(fn=cmd_facts)

    nx = sub.add_parser("next")
    nx.add_argument("feature", nargs="?")
    nx.set_defaults(fn=cmd_next)
    return p


def main():
    a = build_parser().parse_args()
    a.fn(a, a.cwd)


if __name__ == "__main__":
    main()

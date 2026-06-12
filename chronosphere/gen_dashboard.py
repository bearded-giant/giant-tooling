#!/usr/bin/env python3
"""Generate a Chronosphere dashboard YAML for an ai-tooling service.

Clones the orchestrator template, swaps cluster/workload regex + slug/name,
optionally retains OTel app panels (rewritten to the app's metric prefix), or
strips them when the app has no OTel/prom metrics yet.

Usage:
  python3 gen_dashboard.py --slug chat-api --name "Chat API" \
      --httproute-regex 'httproute/ai-tooling/recharge-chat-api-.+/.+' \
      --workload-regex 'recharge-chat-api-.*' \
      --out chat-api.yaml

  # with OTel/prom app metrics
  python3 gen_dashboard.py --slug support-agent --name "Support Agent" \
      --httproute-regex 'httproute/ai-tooling/support-agent-.+/.+' \
      --workload-regex 'support-agent-.*' \
      --otel-prefix support_agent \
      --otel-route-metric api_request_duration_ms \
      --out support-agent.yaml

  # from apps.yaml
  python3 gen_dashboard.py --from-registry chat-api
"""
from __future__ import annotations

import argparse
import json
import re
import sys
from pathlib import Path

HERE = Path(__file__).resolve().parent
DEFAULT_TEMPLATE = HERE / "template.yaml"
DEFAULT_REGISTRY = HERE / "apps.yaml"

OTEL_PANEL_KEYS = {
    "apiRequestP95LatencyByRoute",
    "auditOutboundWriteRateByTool",
    "slackDispatchP95",
    "apiRequestRateByStatusClass",
}

TEMPLATE_CLUSTER_REGEX = r"httproute/ai-tooling/chat-orchestrator-.+/.+"
TEMPLATE_WORKLOAD_REGEX = r"chat-orchestrator-.*"
TEMPLATE_OTEL_PREFIX = "chat_orchestrator"


def parse_args() -> argparse.Namespace:
    p = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    p.add_argument("--slug", help="Dashboard slug (kebab-case, e.g. chat-api)")
    p.add_argument("--name", help="Display name (e.g. 'Chat API')")
    p.add_argument(
        "--httproute-regex",
        help="envoy_cluster_name regex (e.g. httproute/ai-tooling/recharge-chat-api-.+/.+)",
    )
    p.add_argument(
        "--workload-regex",
        help="k8s_workload_name regex for App-containers panel (e.g. recharge-chat-api-.*)",
    )
    p.add_argument(
        "--otel-prefix",
        help="Metric prefix if app emits OTel/prom metrics (e.g. support_agent). Omit to strip OTel panels.",
    )
    p.add_argument(
        "--keep-otel-panel-keys",
        help="Comma-separated subset of OTel panel keys to keep (default: all of "
        + ",".join(sorted(OTEL_PANEL_KEYS))
        + "). Ignored unless --otel-prefix set.",
    )
    p.add_argument("--collection-slug", default="recharge-chat", help="Collection slug")
    p.add_argument("--template", type=Path, default=DEFAULT_TEMPLATE, help="Template YAML")
    p.add_argument("--out", type=Path, help="Output YAML path (defaults to <slug>.yaml in CWD)")
    p.add_argument(
        "--from-registry",
        help="Pull params from apps.yaml under this slug. CLI flags override registry values.",
    )
    p.add_argument(
        "--registry",
        type=Path,
        default=DEFAULT_REGISTRY,
        help="apps.yaml registry path (default: " + str(DEFAULT_REGISTRY) + ")",
    )
    return p.parse_args()


def load_registry(path: Path, slug: str) -> dict:
    if not path.exists():
        sys.exit(f"registry not found: {path}")
    try:
        import yaml  # type: ignore
    except ImportError:
        sys.exit("PyYAML required for --from-registry. install: pip install pyyaml")
    data = yaml.safe_load(path.read_text()) or {}
    apps = data.get("apps") or {}
    if slug not in apps:
        sys.exit(f"slug '{slug}' not in {path}. defined: {sorted(apps)}")
    return apps[slug]


def extract_dashboard_json(text: str) -> tuple[str, str, str, str]:
    m = re.search(r"(?m)^(  dashboard_json: ')(.+)('\s*)$", text, re.DOTALL)
    if not m:
        sys.exit("template missing dashboard_json line")
    raw = m.group(2).replace("''", "'")
    return m.group(1), raw, m.group(3), text


def reserialize(prefix: str, dash: dict, suffix: str, text: str, full_match: re.Match[str]) -> str:
    new_raw = json.dumps(dash, separators=(",", ":")).replace("'", "''")
    new_line = prefix + new_raw + suffix
    return text[: full_match.start()] + new_line + text[full_match.end() :]


def swap_in_strings(obj, swaps: list[tuple[str, str]]):
    if isinstance(obj, dict):
        return {k: swap_in_strings(v, swaps) for k, v in obj.items()}
    if isinstance(obj, list):
        return [swap_in_strings(v, swaps) for v in obj]
    if isinstance(obj, str):
        out = obj
        for old, new in swaps:
            out = out.replace(old, new)
        return out
    return obj


def main() -> None:
    args = parse_args()

    registry = {}
    if args.from_registry:
        registry = load_registry(args.registry, args.from_registry)
        args.slug = args.slug or registry.get("slug") or args.from_registry

    def pick(field: str, *, required: bool = True):
        val = getattr(args, field.replace("-", "_"), None)
        if val is None:
            val = registry.get(field)
        if required and not val:
            sys.exit(f"missing --{field} (and not in registry)")
        return val

    slug = str(pick("slug"))
    name = str(pick("name"))
    httproute_regex = str(pick("httproute-regex"))
    workload_regex = str(pick("workload-regex"))
    otel_prefix = args.otel_prefix or registry.get("otel-prefix")
    collection_slug = str(args.collection_slug or registry.get("collection-slug") or "recharge-chat")

    template_text = args.template.read_text()
    full = re.search(r"(?m)^(  dashboard_json: ')(.+)('\s*)$", template_text, re.DOTALL)
    if not full:
        sys.exit(f"template {args.template} missing dashboard_json line")
    raw = full.group(2).replace("''", "'")
    dash = json.loads(raw)

    spec = dash["spec"]
    panels = spec["panels"]
    items = spec["layouts"][0]["spec"]["items"]

    swaps = [
        (TEMPLATE_CLUSTER_REGEX, httproute_regex),
        (TEMPLATE_WORKLOAD_REGEX, workload_regex),
    ]
    if otel_prefix:
        swaps.append((TEMPLATE_OTEL_PREFIX + "_", otel_prefix + "_"))
    swapped_spec = swap_in_strings(spec, swaps)
    dash["spec"] = swapped_spec
    spec = dash["spec"]
    panels = spec["panels"]
    items = spec["layouts"][0]["spec"]["items"]

    if not otel_prefix:
        keep_subset = OTEL_PANEL_KEYS
    elif args.keep_otel_panel_keys:
        keep_subset = {k.strip() for k in args.keep_otel_panel_keys.split(",") if k.strip()}
        unknown = keep_subset - OTEL_PANEL_KEYS
        if unknown:
            sys.exit(f"unknown OTel panel keys: {unknown}. valid: {OTEL_PANEL_KEYS}")
    else:
        keep_subset = OTEL_PANEL_KEYS

    to_strip = set() if otel_prefix else OTEL_PANEL_KEYS
    if otel_prefix and args.keep_otel_panel_keys:
        to_strip = OTEL_PANEL_KEYS - keep_subset

    if to_strip:
        for k in to_strip:
            panels.pop(k, None)
        spec["layouts"][0]["spec"]["items"] = [
            it for it in items if it["content"]["$ref"].split("/")[-1] not in to_strip
        ]

    text_after_swap = reserialize(full.group(1), dash, full.group(3), template_text, full)

    text_after_swap = re.sub(r"(?m)^(\s+slug:\s*).*$", lambda _m: _m.group(1) + slug, text_after_swap, count=1)
    text_after_swap = re.sub(r"(?m)^(\s+name:\s*).*$", lambda _m: _m.group(1) + name, text_after_swap, count=1)
    text_after_swap = re.sub(
        r"(?m)^(\s+collection_slug:\s*).*$",
        lambda _m: _m.group(1) + collection_slug,
        text_after_swap,
        count=1,
    )

    out_path = args.out or Path(f"{slug}.yaml")
    out_path.write_text(text_after_swap)
    stripped_note = f" stripped={sorted(to_strip)}" if to_strip else ""
    otel_note = f" otel_prefix={otel_prefix}" if otel_prefix else " otel=off"
    print(f"wrote {out_path} slug={slug}{otel_note}{stripped_note}")
    print(f"apply: chronoctl dashboards create -f {out_path}   # or update if exists")


if __name__ == "__main__":
    main()

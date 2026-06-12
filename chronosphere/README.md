# Chronosphere dashboards — ai-tooling toolkit

<!-- caveman:compressed -->

Portable generator + registry for cloning the chat-orchestrator dashboard onto
any ai-tooling service deployed behind Envoy Gateway. Outputs a `chronoctl`-ready
YAML.

## When to use

New ai-tooling Python service deployed with HTTPRoute under namespace
`ai-tooling`. Want envoy traffic panels (req volume, p50/p95 latency, 2xx/3xx/4xx/5xx
breakdown, pod count). Optional: OTel/prom app metric panels.

## Quick start

```bash
cd ~/dev/giant-tooling/chronosphere
python3 gen_dashboard.py --from-registry chat-api
chronoctl dashboards create -f chat-api.yaml   # first time
chronoctl dashboards update -f chat-api.yaml   # subsequent
```

`--from-registry` needs PyYAML. If system python3 lacks it, either
`pip3 install --user pyyaml` or run from a venv that has it
(`uv run python3 gen_dashboard.py ...` inside any uv-managed repo works).
Without `--from-registry` no PyYAML needed — pass flags directly.

Output `<slug>.yaml` lands in CWD. Move into the consumer repo's
`.giantmem/filebox/` if you want to keep it under version control.

## What gets generated

Cloned from `template.yaml` (= chat-orchestrator dashboard snapshot). Each clone
swaps:

| Template token | Swap with |
|---|---|
| `httproute/ai-tooling/chat-orchestrator-.+/.+` | `--httproute-regex` |
| `chat-orchestrator-.*` (workload regex) | `--workload-regex` |
| `chat_orchestrator_` (OTel prefix) | `--otel-prefix` + `_`, OR strip OTel panels |
| `slug:` / `name:` / `collection_slug:` | top-level YAML fields |

Panels in the base template:
- Envoy: Total Requests, req/min, Response Time (mean), Response Code Breakdown, 2/3/4/5xx, App containers
- OTel (app-emitted, kept only if `--otel-prefix` set):
  - `apiRequestP95LatencyByRoute`  — `{prefix}_api_request_duration_ms_bucket`
  - `auditOutboundWriteRateByTool` — `{prefix}_audit_outbound_written_total`
  - `slackDispatchP95`             — `{prefix}_slack_dispatch_duration_ms_bucket`

If the OTel metric names don't match your app, do a final pass by hand or
extend the generator.

## Adding a new app

1. Discover HTTPRoute name. In Chronosphere Metrics Explorer:
   ```
   group by (envoy_cluster_name) (envoy_cluster_upstream_rq{env="prod",envoy_cluster_name=~"httproute/ai-tooling/<app>.*"})
   ```
2. Pin cluster regex. Usually `httproute/ai-tooling/<app>-.+/.+` covers all
   `-recharge`, `-harness-recharge`, `-mcp-recharge`, `-dashboard-recharge` variants.
3. Check for OTel/prom metrics:
   ```
   group by (__name__) ({__name__=~"<app_underscore>_.*",env="prod"})
   ```
   Empty → leave `otel-prefix: null`. Non-empty + matches expected OTel panel
   schema → set `otel-prefix: <app_underscore>`.
4. Add entry to `apps.yaml`. Commit. Run `python3 gen_dashboard.py --from-registry <slug>`.
5. Apply: `chronoctl dashboards create -f <slug>.yaml`.

## Flags (CLI overrides registry)

```
--slug                dashboard slug
--name                display name
--httproute-regex     envoy_cluster_name regex
--workload-regex      k8s_workload_name regex (App-containers panel)
--otel-prefix         metric prefix (e.g. support_agent). omit → strip OTel panels.
--keep-otel-panel-keys
                      comma-list subset of OTel panel keys to keep
                      (apiRequestP95LatencyByRoute,auditOutboundWriteRateByTool,slackDispatchP95)
--collection-slug     chronoctl collection (default: recharge-chat)
--template            path to template.yaml
--from-registry       lookup slug in apps.yaml; CLI flags override
--out                 output path (default: <slug>.yaml)
```

## chronoctl reference

```bash
chronoctl dashboards list
chronoctl dashboards read <slug> -o yaml > <slug>.yaml
chronoctl dashboards create -f <slug>.yaml
chronoctl dashboards update -f <slug>.yaml
chronoctl dashboards delete <slug>
```

## Refreshing the template

Template = snapshot of chat-orchestrator dashboard. Refresh when orchestrator
gains new panels worth propagating:

```bash
chronoctl dashboards read orchestrator -o yaml > ~/dev/giant-tooling/chronosphere/template.yaml
```

Keep the template's `slug: orchestrator` / `name: Orchestrator` /
`collection_slug: recharge-chat` lines intact — the generator overwrites those
fields per-app from the registry / flags.

## Caveats

1. YAML single-quote escape: `'` → `''` inside `dashboard_json:`. Generator
   handles round-trip.
2. OTel `_total` suffix added by OTel→Prom translator. If a panel "No data"
   after generation, drop `_total` from the metric name and re-apply.
3. nginx_ingress / CloudArmour panels not relevant under Envoy Gateway —
   template already excludes them. If you see ones leak in via a stale template
   refresh, delete them and regen.
4. Probes (`/healthz`, `/readyz`) bypass envoy → naturally excluded from
   `envoy_cluster_upstream_rq*` counts.
5. `env="$env"` retained. Stage/prod toggle works only if app deploys to both;
   else hard-code `env="prod"` after generation.
6. Namespace is hard-coded `ai-tooling`. Other namespaces require template
   edits (this is the only repo of ours that does NOT use `$env` for namespace).

## Files

| Path | Purpose |
|---|---|
| `template.yaml` | base dashboard snapshot (= chat-orchestrator) |
| `apps.yaml` | per-app param registry |
| `gen_dashboard.py` | generator |
| `README.md` | this file |

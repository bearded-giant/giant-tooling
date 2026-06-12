# HOWTO — Chronosphere dashboard in 3 steps

<!-- caveman:compressed -->

Toolkit at `~/dev/giant-tooling/chronosphere/`:

| File | Purpose |
|---|---|
| `template.yaml` | base snapshot (= chat-orchestrator dashboard) |
| `apps.yaml` | per-app registry |
| `gen_dashboard.py` | clone + substitute generator |

## Add new app

### 1. Discover envoy cluster name

Chronosphere Metrics Explorer:

```
group by (envoy_cluster_name) (envoy_cluster_upstream_rq{env="prod",envoy_cluster_name=~"httproute/.*<app>.*"})
```

Note exact pattern. Cluster shape varies by app:

| App | Pattern |
|---|---|
| chat-orchestrator (multi-route) | `httproute/ai-tooling/chat-orchestrator-recharge/rule/0`, `-harness-recharge/rule/0`, ... → regex `chat-orchestrator-.+/.+` |
| chat-api (single route) | `httproute/ai-tooling/recharge-chat-api/rule/0` → regex `recharge-chat-api/rule/.+` |

Always run discovery first. Do not assume orchestrator's `app-.+/.+` shape.

### 2. Edit `apps.yaml`

```yaml
apps:
  <slug>:
    name: <Display Name>
    httproute-regex: httproute/ai-tooling/<from step 1>
    workload-regex: <k8s_workload_name pattern>
    otel-prefix: null         # null = strip OTel panels. set if app emits prom/OTel.
    collection-slug: recharge-chat
```

### 3. Generate + apply

```bash
cd ~/dev/giant-tooling/chronosphere
uv run python3 gen_dashboard.py --from-registry <slug> --out <slug>.yaml
chronoctl dashboards create -f <slug>.yaml   # first time
chronoctl dashboards update -f <slug>.yaml   # subsequent
```

URL after apply: `https://recharge.chronosphere.io/dashboards/<slug>`

## One-off (no registry edit)

```bash
uv run python3 ~/dev/giant-tooling/chronosphere/gen_dashboard.py \
  --slug foo --name "Foo Service" \
  --httproute-regex 'httproute/ai-tooling/foo/rule/.+' \
  --workload-regex 'foo-.*' \
  --out foo.yaml
```

Add `--otel-prefix foo_service` to keep p95/audit/slack panels (else stripped).

## Gotchas

- `--from-registry` needs PyYAML. `uv run python3` inside any uv repo works. System `python3` may lack it. Without `--from-registry`, no PyYAML needed.
- Probes (`/healthz`, `/readyz`) bypass envoy — naturally excluded.
- OTel `_total` suffix added by OTel→Prom. If a panel shows "No data" post-apply, drop `_total` and re-apply.
- Namespace hardcoded `ai-tooling` in template. Other namespaces need template edits.

## Verify after apply

```bash
# does the cluster have any traffic at all?
chronoctl query instant 'sum(increase(envoy_cluster_upstream_rq_total{envoy_cluster_name=~"<your regex>"}[24h]))'
```

If 0 → app isn't receiving traffic. Check FE routing / monolith proxy / k8s wiring before debugging panels.

import json

SLUG = "ai-platform-runs"
NAME = "AI Platform — Engine and Agent Runs"
COLLECTION = "ai-tools"

ENGINE = 'job="investigator-agent",env=~"$env"'
AGENTS = 'job="ai-agents",env=~"$env"'

COUNT = {"abbreviate": True, "decimal_places": 0, "kind": "Decimal"}
USD = {"abbreviate": False, "decimal_places": 2, "kind": "Decimal"}
PCT = {"abbreviate": False, "decimal_places": 1, "kind": "Percent"}
MS = {"kind": "Milliseconds"}
SECONDS = {"kind": "Seconds"}


def q(query, legend):
    return {"query": query, "series_name_format": legend}


def ts_panel(name, description, queries, unit, stack=False):
    visual = {"area_opacity": 0.3, "line_width": 1.5, "point_radius": 3}
    if stack:
        visual["stack"] = "All"
    return {
        "kind": "Panel",
        "spec": {
            "display": {"description": description, "name": name},
            "formulas": [],
            "links": [],
            "plugin": {
                "kind": "TimeSeriesChart",
                "spec": {
                    "legend": {"mode": "List", "position": "Bottom", "values": []},
                    "tooltip": {"mode": "nearby"},
                    "visual": visual,
                    "y_axis": {"min": 0, "unit": unit},
                },
            },
            "queries": [
                {"kind": "DataQuery", "spec": {"plugin": {"kind": "PrometheusTimeSeriesQuery", "spec": s}}}
                for s in queries
            ],
        },
    }


# one sample per fixed window (engine 5 min, agents 10 min): sum_over_time[1h] is per hour, gauges look back past a window
panels = {
    "engineRuns": ts_panel(
        "Engine runs per hour by status",
        "Runs finished in the trailing hour, from the engine's 5 minute eval sweep.",
        [q(f"sum by (status) (sum_over_time(engine_runs{{{ENGINE}}}[1h]))", "{{ status }}")],
        COUNT,
        stack=True,
    ),
    "engineFailed": ts_panel(
        "Engine failed ratio (1h)",
        "Runs ending in error or failed over all runs finished in the trailing hour.",
        [
            q(
                f'100 * sum(sum_over_time(engine_runs{{{ENGINE},status=~"error|failed"}}[1h])) '
                f"/ sum(sum_over_time(engine_runs{{{ENGINE}}}[1h]))",
                "failed %",
            )
        ],
        PCT,
    ),
    "engineDeadline": ts_panel(
        "Engine deadline hits per hour",
        "Runs that hit their deadline (stopped, partial, or reads dropped at the deadline). Monitor: >= 5 in 1h.",
        [q(f"sum(sum_over_time(engine_deadline_hits{{{ENGINE}}}[1h]))", "deadline hits"), q("vector(5)", "alert line")],
        COUNT,
    ),
    "engineDuration": ts_panel(
        "Engine run duration by source",
        "p50 and p95 of total_ms per 5 minute window.",
        [
            q(
                f"max by (source, quantile) (max_over_time(engine_run_duration_ms{{{ENGINE}}}[10m]))",
                "{{ source }} p{{ quantile }}",
            )
        ],
        MS,
    ),
    "engineCost": ts_panel(
        "Engine cost per hour by source (USD)",
        "Model cost of runs finished in the trailing hour.",
        [q(f"sum by (source) (sum_over_time(engine_run_cost_usd{{{ENGINE}}}[1h]))", "{{ source }}")],
        USD,
        stack=True,
    ),
    "agentRuns": ts_panel(
        "Agent runs per hour by agent and status",
        "Runs finished in the trailing hour, from the agents app's 10 minute silent tick.",
        [q(f"sum by (agent, status) (sum_over_time(agent_runs{{{AGENTS}}}[1h]))", "{{ agent }} {{ status }}")],
        COUNT,
        stack=True,
    ),
    "agentFailed": ts_panel(
        "Agent failed runs per hour",
        "Monitor: >= 3 in 1h.",
        [
            q(f'sum by (agent) (sum_over_time(agent_runs{{{AGENTS},status="failed"}}[1h]))', "{{ agent }}"),
            q("vector(3)", "alert line"),
        ],
        COUNT,
    ),
    "tickAge": ts_panel(
        "Agent dispatcher tick age",
        "Seconds since each agent's dispatcher last ticked, read at push time. Monitor: > 600.",
        [
            q(f"max by (agent) (max_over_time(agent_dispatcher_tick_age_s{{{AGENTS}}}[20m]))", "{{ agent }}"),
            q("vector(600)", "alert line"),
        ],
        SECONDS,
    ),
    "agentDuration": ts_panel(
        "Agent run duration",
        "p50 and p95 of duration_ms per 10 minute window.",
        [
            q(
                f"max by (agent, quantile) (max_over_time(agent_run_duration_ms{{{AGENTS}}}[20m]))",
                "{{ agent }} p{{ quantile }}",
            )
        ],
        MS,
    ),
    "agentCost": ts_panel(
        "Agent cost per hour (USD)",
        "Cost of agent runs finished in the trailing hour.",
        [q(f"sum by (agent) (sum_over_time(agent_run_cost_usd{{{AGENTS}}}[1h]))", "{{ agent }}")],
        USD,
        stack=True,
    ),
}

order = [
    ("engineRuns", "engineFailed"),
    ("engineDeadline", "engineDuration"),
    ("engineCost", "agentRuns"),
    ("agentFailed", "tickAge"),
    ("agentDuration", "agentCost"),
]
layout = [
    {"content": {"$ref": f"#/spec/panels/{key}"}, "height": 8, "width": 12, "x": 12 * col, "y": 8 * row}
    for row, pair in enumerate(order)
    for col, key in enumerate(pair)
]
assert sorted(k for pair in order for k in pair) == sorted(panels)

env_var = {
    "kind": "ListVariable",
    "spec": {
        "allow_all_value": True,
        "allow_multiple": True,
        "custom_all_value": ".*",
        "default_value": ["prod"],
        "display": {"name": "env"},
        "name": "env",
        "plugin": {
            "kind": "StaticListVariable",
            "spec": {"values": [{"label": "prod", "value": "prod"}, {"label": "stage", "value": "stage"}]},
        },
        "refresh": "DashboardLoad",
    },
}

dashboard_json = {
    "kind": "Dashboard",
    "spec": {
        "duration": "24h",
        "events": [],
        "layouts": [{"kind": "Grid", "spec": {"items": layout}}],
        "panels": panels,
        "variables": [env_var],
    },
    "spec_version": "1",
}

blob = json.dumps(dashboard_json, separators=(",", ":"))
blob_yaml = blob.replace("'", "''")

out = f"""api_version: v1/config
kind: Dashboard
spec:
  slug: {SLUG}
  name: {NAME}
  collection_slug: {COLLECTION}
  dashboard_json: '{blob_yaml}'
"""

with open(f"{SLUG}.yaml", "w") as f:
    f.write(out)

print(f"wrote {SLUG}.yaml ({len(blob)} bytes json)")

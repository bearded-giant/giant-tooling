import json

# ponytail: requests hardcoded per service — no kube-state resource-request metric in chronosphere
WORKLOADS = "chat-orchestrator-recharge|support-agent-recharge|analytics-agent"
GIB = 1024 * 1024 * 1024

SLUG = "ai-tooling-memory-autoscaling"
NAME = "AI Tooling — Memory Autoscaling"
COLLECTION = "recharge-chat"


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
                {
                    "kind": "DataQuery",
                    "spec": {"plugin": {"kind": "PrometheusTimeSeriesQuery", "spec": q}},
                }
                for q in queries
            ],
        },
    }


def mem_util_query(workload, req_gib, label):
    return {
        "query": (
            f'avg(k8s_pod_memory_working_set{{env="$env",k8s_workload_name="{workload}"}}) '
            f"/ ({req_gib} * {GIB}) * 100"
        ),
        "series_name_format": label,
    }


panels = {
    "podCount": ts_panel(
        "Pod count by workload",
        "Replica count per autoscaled workload — floor/ceiling: orch 2-5, support 5-8, analytics 4-6",
        [
            {
                "query": (
                    'count by (k8s_workload_name) (avg_over_time('
                    f'k8s_pod_phase{{env="$env", k8s_workload_name=~"{WORKLOADS}"}}[$__rate_interval]))'
                ),
                "series_name_format": "{{ k8s_workload_name }}",
            }
        ],
        {"abbreviate": True, "decimal_places": 0, "kind": "Decimal"},
    ),
    "memUtil": ts_panel(
        "Memory utilization % (KEDA trigger signal)",
        "working_set / memory request — this is what the KEDA memory trigger scales on. Target 80%.",
        [
            mem_util_query("chat-orchestrator-recharge", 2, "chat-orchestrator (req 2Gi)"),
            mem_util_query("support-agent-recharge", 4, "support-agent (req 4Gi)"),
            mem_util_query("analytics-agent", 2, "analytics-agent (req 2Gi)"),
            {"query": "vector(80)", "series_name_format": "trigger target (80%)"},
        ],
        {"abbreviate": True, "decimal_places": 1, "kind": "Percent"},
    ),
    "memWorkingSet": ts_panel(
        "Memory working-set per pod (avg)",
        "Absolute avg working-set bytes per workload. Limits: orch 3Gi, support 6Gi, analytics 3Gi.",
        [
            {
                "query": (
                    'avg by (k8s_workload_name) '
                    f'(k8s_pod_memory_working_set{{env="$env",k8s_workload_name=~"{WORKLOADS}"}})'
                ),
                "series_name_format": "{{ k8s_workload_name }}",
            }
        ],
        {"abbreviate": True, "kind": "Bytes"},
    ),
}

layout = [
    {"content": {"$ref": "#/spec/panels/podCount"}, "height": 8, "width": 12, "x": 0, "y": 0},
    {"content": {"$ref": "#/spec/panels/memUtil"}, "height": 8, "width": 12, "x": 12, "y": 0},
    {"content": {"$ref": "#/spec/panels/memWorkingSet"}, "height": 8, "width": 24, "x": 0, "y": 8},
]

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
            "spec": {
                "values": [
                    {"label": "prod", "value": "prod"},
                    {"label": "stage", "value": "stage"},
                ]
            },
        },
        "refresh": "DashboardLoad",
    },
}

dashboard_json = {
    "kind": "Dashboard",
    "spec": {
        "duration": "3h",
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

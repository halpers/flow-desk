"""Exercise local TLS, metrics, metadata traces and a reversible service-down alert."""

import json
import ssl
import time
from datetime import UTC, datetime, timedelta
from uuid import uuid4

import httpx
from compose_runtime import ROOT, run
from eval_common import PublicAPI, settings

FILES = [
    "docker-compose.yaml",
    "deploy/compose.monitoring.yaml",
    "deploy/compose.tls.yaml",
    "deploy/compose.mcp-acceptance.yaml",
]


def eventually(function, seconds=45):
    deadline = time.monotonic() + seconds
    while True:
        value = function()
        if value:
            return value
        if time.monotonic() > deadline:
            raise AssertionError("MONITORING_WAIT_TIMEOUT")
        time.sleep(1)


def main():
    config = settings()
    report = {
        "started_at": datetime.now(UTC).isoformat(),
        "scope": "local synthetic acceptance",
        "remote_deployment": False,
        "paid_model_calls": False,
    }
    base = "https://localhost:" + config.get("TLS_HOST_PORT", "13443")
    trust = ssl.create_default_context(cafile=str(ROOT / "runtime/tls/localhost.crt"))
    api = PublicAPI(config, "demo.operator", base=base, verify=trust)
    try:
        assert api.http.get("/").status_code == 200
        cookies = list(api.http.cookies.jar)
        assert cookies and all(cookie.secure for cookie in cookies)
        task = api.wait(api.start("查询订单 FD202611020001"))
        assert task["status"] == "succeeded", task["error_code"]
        report["tls_verified_login_and_task"] = True
        report["run_id"] = task["id"]
    finally:
        api.close()
    prom = "http://localhost:" + config.get("PROMETHEUS_HOST_PORT", "19090")
    jaeger = "http://localhost:" + config.get("JAEGER_HOST_PORT", "16686")
    grafana = "http://localhost:" + config.get("GRAFANA_HOST_PORT", "13001")
    with httpx.Client(timeout=10, trust_env=False) as http:

        def query(expression):
            result = http.get(prom + "/api/v1/query", params={"query": expression})
            result.raise_for_status()
            return result.json()["data"]["result"]

        eventually(lambda: len(query('up{job=~"flowdesk-.*"} == 1')) == 2)
        assert query('flowdesk_agent_commands{status="completed"}')
        assert query("flowdesk_agent_model_tokens")
        assert query("flowdesk_agent_embedding_tokens")
        assert query("flowdesk_agent_retrieval_node_p95_milliseconds")
        report["scrape_jobs_healthy"] = ["flowdesk-api", "flowdesk-agent"]
        dashboard = http.get(
            grafana + "/api/dashboards/uid/flowdesk-operations",
            auth=("admin", config["GRAFANA_ADMIN_PASSWORD"]),
        )
        dashboard.raise_for_status()
        report["dashboard_panels"] = len(dashboard.json()["dashboard"]["panels"])
        health = http.get(
            grafana + "/api/datasources/uid/flowdesk-prometheus/health",
            auth=("admin", config["GRAFANA_ADMIN_PASSWORD"]),
        )
        health.raise_for_status()
        report["grafana_prometheus_health"] = health.json()["status"]
        for panel in dashboard.json()["dashboard"]["panels"]:
            for target in panel.get("targets", []):
                query(target["expr"])
        report["dashboard_queries_valid"] = True

        def read_spans(response):
            response.raise_for_status()
            spans = []
            for line in response.text.splitlines():
                for resource in (
                    json.loads(line).get("result", {}).get("resourceSpans", [])
                ):
                    service = next(
                        (
                            item["value"].get("stringValue", "")
                            for item in resource["resource"].get("attributes", [])
                            if item["key"] == "service.name"
                        ),
                        "",
                    )
                    for scope in resource.get("scopeSpans", []):
                        spans.extend((service, span) for span in scope.get("spans", []))
            return spans

        def traces():
            response = http.get(
                jaeger + "/api/v3/traces",
                params={
                    "query.serviceName": "flowdesk-agent",
                    "query.startTimeMin": (
                        datetime.now(UTC) - timedelta(minutes=10)
                    ).isoformat(),
                    "query.startTimeMax": datetime.now(UTC).isoformat(),
                    "query.searchDepth": 50,
                },
            )
            return [
                span["traceId"]
                for _, span in read_spans(response)
                if any(
                    item["key"] == "flowdesk.run_id"
                    and item["value"].get("stringValue") == task["id"]
                    for item in span.get("attributes", [])
                )
            ]

        found = eventually(traces)

        def shared_trace():
            for identifier in set(found):
                spans = read_spans(http.get(jaeger + "/api/v3/traces/" + identifier))
                if {service for service, _ in spans} >= {
                    "flowdesk-api",
                    "flowdesk-agent",
                }:
                    return identifier, spans
            return None

        identifier, shared = eventually(shared_trace)
        report["shared_trace_id"] = identifier
        report["trace_services"] = sorted({service for service, _ in shared})
        report["trace_span_names"] = sorted({span["name"] for _, span in shared})

        canary = "PRIVATE_METADATA_REDACTION_CANARY"
        trace_id = uuid4().hex
        now = time.time_ns()
        span = {
            "traceId": trace_id,
            "spanId": uuid4().hex[:16],
            "name": "flowdesk.redaction_probe",
            "startTimeUnixNano": str(now),
            "endTimeUnixNano": str(now + 1000000),
            "attributes": [{"key": "prompt", "value": {"stringValue": canary}}],
            "status": {"code": 2, "message": canary},
            "events": [{"name": canary, "timeUnixNano": str(now)}],
        }
        payload = {
            "resourceSpans": [
                {
                    "resource": {
                        "attributes": [
                            {
                                "key": "service.name",
                                "value": {"stringValue": "flowdesk-redaction-probe"},
                            },
                            {"key": "credential", "value": {"stringValue": canary}},
                        ]
                    },
                    "scopeSpans": [{"spans": [span]}],
                }
            ]
        }
        response = http.post(
            "http://localhost:" + config.get("OTLP_HOST_PORT", "14318") + "/v1/traces",
            json=payload,
        )
        response.raise_for_status()

        def probe():
            response = http.get(jaeger + "/api/v3/traces/" + trace_id)
            return read_spans(response) if response.status_code == 200 else None

        redacted = eventually(probe)
        assert canary not in json.dumps(redacted)
        report["collector_attributes_events_status_redacted"] = True
        try:
            run(["stop", "agent"], compose_files=FILES)
            eventually(
                lambda: query(
                    'ALERTS{alertname="FlowDeskServiceDown",alertstate="firing",'
                    'job="flowdesk-agent"}'
                )
            )
            report["controlled_service_down_alert_fired"] = True
        finally:
            run(
                [
                    "up",
                    "--detach",
                    "--no-deps",
                    "--wait",
                    "--wait-timeout",
                    "180",
                    "agent",
                ],
                compose_files=FILES,
            )
        eventually(lambda: len(query('up{job="flowdesk-agent"} == 1')) == 1)
        eventually(
            lambda: (
                not query(
                    'ALERTS{alertname="FlowDeskServiceDown",alertstate="firing",'
                    'job="flowdesk-agent"}'
                )
            )
        )
        report["alert_resolved_and_agent_healthy"] = True
    report["completed_at"] = datetime.now(UTC).isoformat()
    (ROOT / "reports/m6/monitoring-local.json").write_text(
        json.dumps(report, indent=2) + "\n", encoding="utf-8"
    )
    print(
        "Local TLS, two scrapes, dashboard, shared trace, redaction and alert recovery passed."
    )


if __name__ == "__main__":
    main()

"""Reproducible synthetic M3 acceptance, separate from the M5 quality benchmark."""

import argparse
import json
import time
from contextlib import contextmanager
from datetime import UTC, datetime
from pathlib import Path
from uuid import uuid4

import httpx
import psycopg
from psycopg.rows import dict_row

ROOT = Path(__file__).resolve().parents[1]


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument(
        "--profile", choices=["mock", "openai-compatible"], required=True
    )
    parser.add_argument("--limit", type=int, default=10)
    args = parser.parse_args()
    settings = dict(
        line.split("=", 1)
        for line in (ROOT / ".env").read_text(encoding="utf-8-sig").splitlines()
        if line and not line.startswith("#")
    )
    if args.profile != settings.get("MODEL_PROVIDER", "mock"):
        raise SystemExit("Profile differs from .env; recreate Agent before running")
    base = "http://localhost:" + settings["WEB_HOST_PORT"]
    fixtures = json.loads(
        (ROOT / "flow-desk-agent/eval/m3-policies.json").read_text(encoding="utf-8")
    )
    report = {
        "profile": args.profile,
        "chat_model": settings.get("MODEL_NAME")
        if args.profile != "mock"
        else "FakeModel",
        "retrieval_profile": "char-ngram-v1+fts+rrf",
        "started_at": datetime.now(UTC).isoformat(),
        "cases": [],
        "run_ids": [],
        "cost_status": "unknown",
        "kind": "functional acceptance, not M5 quality benchmark",
    }

    def call(client, method, path, body=None, expected=200):
        response = client.request(
            method,
            "/api/v1" + path,
            json=body,
            headers={"Idempotency-Key": str(uuid4())},
        )
        if response.status_code != expected:
            raise RuntimeError(
                f"{method} {path}: {response.status_code} {response.json().get('code')}"
            )
        return response.json()["data"]

    @contextmanager
    def login(account):
        with httpx.Client(
            base_url=base,
            timeout=15,
            trust_env=False,
            headers={"Origin": base, "X-FlowDesk-Client": "web"},
        ) as client:
            token = call(
                client,
                "POST",
                "/auth/login",
                {"account": account, "password": settings["DEMO_PASSWORD"]},
            )
            client.headers["Authorization"] = "Bearer " + token["access_token"]
            try:
                yield client
            finally:
                client.post("/api/v1/auth/logout")

    def prepare(admin, foreign=False):
        name = "M3 acceptance fixtures v1"
        bases = call(admin, "GET", "/knowledge-bases")["items"]
        knowledge = next((item for item in bases if item["name"] == name), None)
        knowledge = knowledge or call(
            admin,
            "POST",
            "/knowledge-bases",
            {"name": name, "read_roles": ["operator", "supervisor", "admin"]},
            201,
        )
        documents = call(admin, "GET", f"/knowledge-bases/{knowledge['id']}/documents")[
            "items"
        ]
        versions = []
        for fixture in fixtures:
            doc = next(
                (item for item in documents if item["title"] == fixture["title"]), None
            )
            if doc:
                version = call(admin, "GET", f"/documents/{doc['id']}/versions")[
                    "items"
                ][0]
            else:
                text = fixture["content"] + (
                    " 其他租户专属标记：FOREIGN_POLICY_SENTINEL。" if foreign else ""
                )
                version = call(
                    admin,
                    "POST",
                    f"/knowledge-bases/{knowledge['id']}/documents",
                    {
                        "title": fixture["title"],
                        "filename": fixture["id"] + ".md",
                        "content": text,
                    },
                    202,
                )
            deadline = time.monotonic() + 60
            while version["index_status"] != "ready":
                if time.monotonic() > deadline:
                    raise RuntimeError("INDEX_TIMEOUT")
                time.sleep(0.3)
                version = call(
                    admin, "GET", f"/documents/{version['document_id']}/versions"
                )["items"][0]
            doc = next(
                item
                for item in call(
                    admin, "GET", f"/knowledge-bases/{knowledge['id']}/documents"
                )["items"]
                if item["id"] == version["document_id"]
            )
            if doc["active_version_id"] != version["id"]:
                call(
                    admin,
                    "POST",
                    f"/documents/{doc['id']}/activate",
                    {"version_id": version["id"], "expected_version": doc["version"]},
                )
            versions.append(version)
        return knowledge, versions

    try:
        with (
            login("demo.admin") as admin,
            login("other.admin") as other_admin,
            login("demo.operator") as operator,
            login("other.operator") as other,
        ):
            knowledge, versions = prepare(admin)
            foreign_base, _ = prepare(other_admin, True)
            assert (
                other.get(
                    f"/api/v1/knowledge-bases/{knowledge['id']}/documents"
                ).status_code
                == 404
            )
            assert (
                operator.get(
                    f"/api/v1/knowledge-bases/{foreign_base['id']}/documents"
                ).status_code
                == 404
            )
            first = None
            for fixture, version in zip(fixtures[: args.limit], versions):
                conversation = call(
                    operator,
                    "POST",
                    "/conversations",
                    {"title": "M3 " + fixture["id"]},
                    201,
                )
                begin = time.monotonic()
                run = call(
                    operator,
                    "POST",
                    f"/conversations/{conversation['id']}/messages",
                    {
                        "client_message_id": str(uuid4()),
                        "content": fixture["question"],
                        "attachments": [],
                    },
                    202,
                )
                report["run_ids"].append(run["id"])
                deadline = time.monotonic() + 125
                while (
                    run["status"] not in ("succeeded", "failed", "expired")
                    and time.monotonic() < deadline
                ):
                    time.sleep(0.25)
                    run = call(operator, "GET", f"/runs/{run['id']}")
                message = run["current_message"]
                passed = (
                    run["status"] == "succeeded"
                    and fixture["expected"] in message["content"]
                    and any(
                        item["version_id"] == version["id"]
                        for item in message["citations"]
                    )
                )
                assert "FOREIGN_POLICY_SENTINEL" not in message["content"]
                for citation in message["citations"]:
                    evidence = call(operator, "GET", "/citations/" + citation["id"])
                    assert "FOREIGN_POLICY_SENTINEL" not in evidence["excerpt"]
                    assert (
                        other.get("/api/v1/citations/" + citation["id"]).status_code
                        == 404
                    )
                report["cases"].append(
                    {
                        "task_id": fixture["id"],
                        "passed": passed,
                        "status": run["status"],
                        "error_code": run["error_code"],
                        "citation_count": len(message["citations"]),
                        "duration_ms": round((time.monotonic() - begin) * 1000),
                    }
                )
                print(
                    f"{fixture['id']}: {run['status']}, assertions={passed}", flush=True
                )
                if not passed:
                    raise RuntimeError("POLICY_ASSERTION_FAILED")
                first = first or run
            if first:
                document = next(
                    item
                    for item in call(
                        admin, "GET", f"/knowledge-bases/{knowledge['id']}/documents"
                    )["items"]
                    if item["id"] == versions[0]["document_id"]
                )
                call(
                    admin,
                    "POST",
                    f"/documents/{document['id']}/withdraw",
                    {"expected_version": document["version"]},
                )
                for citation in first["current_message"]["citations"]:
                    if citation["version_id"] == versions[0]["id"]:
                        assert (
                            operator.get(
                                "/api/v1/citations/" + citation["id"]
                            ).status_code
                            == 409
                        )
                snapshot = call(operator, "GET", f"/runs/{first['id']}")
                assert "七天" not in snapshot["current_message"]["content"]
                assert (
                    "七天"
                    not in operator.get(f"/api/v1/runs/{first['id']}/events").text
                )
                report["withdrawal_and_replay_passed"] = True
            report["tenant_isolation_passed"] = True
    finally:
        if report["run_ids"]:
            dsn = f"postgresql://flowdesk_owner:{settings['POSTGRES_PASSWORD']}@localhost:{settings['PG_HOST_PORT']}/flowdesk"
            with psycopg.connect(
                dsn,
                row_factory=dict_row,
                connect_timeout=5,
                options="-cstatement_timeout=10000",
            ) as conn:
                report["usage"] = conn.execute(
                    "SELECT count(*) AS calls,coalesce(sum(input_tokens),0) AS input_tokens,coalesce(sum(output_tokens),0) AS output_tokens,coalesce(sum(reserved_units),0) AS reserved_units FROM agent.model_calls WHERE run_id::text=ANY(%s)",
                    (report["run_ids"],),
                ).fetchone()
        report["completed_at"] = datetime.now(UTC).isoformat()
        output = ROOT / "reports/m3" / f"acceptance-{args.profile}.json"
        output.write_text(
            json.dumps(report, ensure_ascii=False, indent=2) + "\n", encoding="utf-8"
        )
        print("Saved " + str(output.relative_to(ROOT)))


if __name__ == "__main__":
    main()

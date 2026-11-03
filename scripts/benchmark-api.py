"""20-client API benchmark against a fresh disposable DB and separate Go process."""

import concurrent.futures
import json
import os
import platform
import socket
import subprocess
import time
from datetime import UTC, datetime
from uuid import uuid4

import httpx
import psycopg
from eval_common import ROOT, PublicAPI, settings
from evaluate import percentile
from psycopg import sql
from psycopg.conninfo import make_conninfo

config = settings()
database = "fd_perf_" + uuid4().hex[:12]
owner_options = {
    "host": "localhost",
    "port": config["PG_HOST_PORT"],
    "dbname": "flowdesk",
    "user": "flowdesk_owner",
    "password": config["POSTGRES_PASSWORD"],
    "autocommit": True,
}
with socket.socket() as port_socket:
    port_socket.bind(("127.0.0.1", 0))
    port = port_socket.getsockname()[1]
base = f"http://127.0.0.1:{port}"
report = {
    "started_at": datetime.now(UTC).isoformat(),
    "isolation": "fresh disposable database and separate native Go API process",
    "environment": {
        "platform": platform.platform(),
        "cpu": platform.processor(),
        "logical_cpus": os.cpu_count(),
        "go": subprocess.check_output(["go", "version"], text=True).strip(),
        "database": "PostgreSQL 18 in local Docker/WSL",
        "model": "excluded",
        "api_runtime": "native Windows",
        "memory_limit": "No explicit local process/container memory limit; no capacity guarantee",
        "timer_resolution_seconds": time.get_clock_info("monotonic").resolution,
    },
    "concurrency": 20,
    "requests": 1000,
    "target_p95_ms": 300,
    "samples": [],
}
process = None
client = None
work = ROOT / "work"
with psycopg.connect(**owner_options) as owner:
    owner.execute(sql.SQL("CREATE DATABASE {}").format(sql.Identifier(database)))
    try:
        with psycopg.connect(**{**owner_options, "dbname": database}) as conn:
            conn.execute("CREATE SCHEMA business")
        environment = {
            **os.environ,
            "DATABASE_URL": make_conninfo(
                host="localhost",
                port=config["PG_HOST_PORT"],
                dbname=database,
                user="flowdesk_owner",
                password=config["POSTGRES_PASSWORD"],
                options="-csearch_path=business",
                sslmode="disable",
            ),
            "DEMO_PASSWORD": config["DEMO_PASSWORD"],
        }
        with (work / "m5-perf-migrate.log").open("w", encoding="utf-8") as logfile:
            subprocess.run(
                [str(work / "m5-perf-migrate.exe")],
                cwd=ROOT / "flow-desk-serv/flowdesk",
                env=environment,
                stdout=logfile,
                stderr=logfile,
                check=True,
            )
        with psycopg.connect(
            **{**owner_options, "dbname": database}, options="-csearch_path=business"
        ) as conn:
            actor, tenant = conn.execute(
                "SELECT id,tenant_id FROM users WHERE account='demo.operator'"
            ).fetchone()
            conn.execute(
                "INSERT INTO orders(id,tenant_id,order_no,masked_customer_name,status,total_amount,currency,version,placed_at) SELECT gen_random_uuid(),%s,'PF'||lpad(value::text,12,'0'),'synthetic','processing','100.90','CNY',1,'2026-11-02T00:00:00Z' FROM generate_series(1,9950) value",
                (tenant,),
            )
            conn.execute(
                "INSERT INTO tickets(id,tenant_id,ticket_no,order_id,title,description,category,priority,status,created_by,version) SELECT gen_random_uuid(),%s,'PF'||row_number() over()::text,id,'Synthetic load fixture','No customer data','other','normal','open',%s,1 FROM orders WHERE tenant_id=%s",
                (tenant, actor, tenant),
            )
            conn.execute("GRANT USAGE ON SCHEMA business TO fd_business")
            conn.execute(
                "GRANT SELECT,INSERT,UPDATE,DELETE ON ALL TABLES IN SCHEMA business TO fd_business"
            )
            report["tenant_orders"] = conn.execute(
                "SELECT count(*) FROM orders WHERE tenant_id=%s", (tenant,)
            ).fetchone()[0]
            report["tenant_tickets"] = conn.execute(
                "SELECT count(*) FROM tickets WHERE tenant_id=%s", (tenant,)
            ).fetchone()[0]
            conn.execute("ANALYZE")
        environment.update(
            DATABASE_URL=make_conninfo(
                host="localhost",
                port=config["PG_HOST_PORT"],
                dbname=database,
                user="fd_business",
                password=config["BUSINESS_PASSWORD"],
                options="-csearch_path=business",
                sslmode="disable",
            ),
            JWT_SECRET=config["JWT_SECRET"],
            INTERNAL_TOKEN=config["INTERNAL_TOKEN"],
            AGENT_URL="http://127.0.0.1:1",
            ALLOWED_ORIGINS=base,
            COOKIE_SECURE="false",
        )
        source = (ROOT / "flow-desk-serv/flowdesk/etc/flowdesk-api.yaml").read_text(
            encoding="utf-8"
        )
        temporary = work / "m5-perf-api.yaml"
        temporary.write_text(
            source.replace("Port: 8080", f"Port: {port}").replace(
                "Host: 0.0.0.0", "Host: 127.0.0.1"
            ),
            encoding="utf-8",
        )
        with (work / "m5-perf-api.log").open("w", encoding="utf-8") as logfile:
            process = subprocess.Popen(
                [str(work / "m5-perf-api.exe"), "-f", str(temporary)],
                cwd=ROOT,
                env=environment,
                stdout=logfile,
                stderr=logfile,
                creationflags=subprocess.CREATE_NO_WINDOW if os.name == "nt" else 0,
            )
        deadline = time.monotonic() + 30
        while time.monotonic() < deadline:
            if process.poll() is not None:
                raise RuntimeError(
                    "isolated API exited; inspect ignored work/m5-perf-api.log"
                )
            try:
                if httpx.get(base + "/health/live", trust_env=False).status_code == 200:
                    break
            except httpx.HTTPError:
                pass
            time.sleep(0.15)
        client = PublicAPI(config, "demo.operator", base)
        order = client.call("GET", "/orders?limit=1")["items"][0]
        ticket = client.call("GET", "/tickets?limit=1")["items"][0]
        paths = [
            "/orders?limit=20",
            "/tickets?limit=20",
            "/orders/" + order["id"],
            "/tickets/" + ticket["id"],
        ]
        for index in range(40):
            client.call("GET", paths[index % 4])

        def request(index):
            started = time.monotonic()
            response = client.http.get("/api/v1" + paths[index % 4])
            return {
                "endpoint": paths[index % 4].split("?")[0].rsplit("/", 1)[0]
                if index % 4 >= 2
                else paths[index % 4].split("?")[0],
                "status": response.status_code,
                "duration_ms": round((time.monotonic() - started) * 1000, 3),
            }

        started = time.monotonic()
        with concurrent.futures.ThreadPoolExecutor(max_workers=20) as executor:
            report["samples"] = list(executor.map(request, range(1000)))
        report["elapsed_seconds"] = round(time.monotonic() - started, 3)
        durations = [sample["duration_ms"] for sample in report["samples"]]
        report["p50_ms"] = percentile(durations, 0.5)
        report["p95_ms"] = percentile(durations, 0.95)
        report["errors"] = sum(sample["status"] != 200 for sample in report["samples"])
        report["target_met"] = report["p95_ms"] < 300 and report["errors"] == 0
    finally:
        if client:
            client.close()
        if process and process.poll() is None:
            process.terminate()
            try:
                process.wait(timeout=10)
            except subprocess.TimeoutExpired:
                process.kill()
                process.wait(timeout=5)
        assert database.startswith("fd_perf_") and len(database) == 20
        owner.execute(
            sql.SQL("DROP DATABASE {} WITH (FORCE)").format(sql.Identifier(database))
        )
        report["database_cleaned"] = True
        report["completed_at"] = datetime.now(UTC).isoformat()
        (ROOT / "reports/m5/api-performance.json").write_text(
            json.dumps(report, ensure_ascii=False, indent=2) + "\n", encoding="utf-8"
        )
print(
    json.dumps(
        {key: value for key, value in report.items() if key != "samples"},
        ensure_ascii=True,
    )
)

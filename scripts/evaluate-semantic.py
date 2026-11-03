"""Independent synthetic retrieval comparison in a disposable Agent schema."""

import argparse
import asyncio
import hashlib
import json
import os
import statistics
import sys
import time
from datetime import UTC, datetime
from pathlib import Path
from uuid import NAMESPACE_URL, UUID, uuid4, uuid5

import httpx
import psycopg
from psycopg import sql
from psycopg.conninfo import make_conninfo
from psycopg.rows import dict_row
from psycopg_pool import AsyncConnectionPool

ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(ROOT / "flow-desk-agent/src"))
from flowdesk_agent.api.schemas import ExecutionCommand  # noqa: E402
from flowdesk_agent.embeddings import EmbeddingClient  # noqa: E402
from flowdesk_agent.models import CompatibleModel  # noqa: E402
from flowdesk_agent.reranking import ModelReranker  # noqa: E402
from flowdesk_agent.retrieval import KnowledgeIndex, Retriever  # noqa: E402
from flowdesk_agent.semantic_index import SemanticIndex  # noqa: E402
from flowdesk_agent.storage.migrations import migrate  # noqa: E402


def summarize(cases):
    return {
        "total": len(cases),
        "hits_at_5": sum(case["rank"] is not None for case in cases),
        "recall_at_5": sum(case["rank"] is not None for case in cases) / len(cases),
        "recall_at_1": sum(case["rank"] == 1 for case in cases) / len(cases),
        "mrr_at_5": sum(1 / case["rank"] if case["rank"] else 0 for case in cases) / len(cases),
        "latency_p50_ms": round(statistics.median(case["duration_ms"] for case in cases)),
        "latency_p95_ms": sorted(case["duration_ms"] for case in cases)[
            min(len(cases) - 1, int(len(cases) * 0.95))
        ],
        "cases": cases,
    }


async def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "--real", action="store_true", help="Call the configured embedding provider"
    )
    parser.add_argument("--rerank", action="store_true", help="Also call configured chat reranking")
    args = parser.parse_args()
    if args.rerank and not args.real:
        parser.error("--rerank requires --real")
    config = dict(
        line.split("=", 1)
        for line in (ROOT / ".env").read_text(encoding="utf-8-sig").splitlines()
        if line and not line.startswith("#") and "=" in line
    )
    dataset_path = ROOT / "flow-desk-agent/eval/m6-retrieval-v1.json"
    dataset = json.loads(dataset_path.read_text(encoding="utf-8"))
    cases = dataset["cases"]
    assert len(cases) == 24 and len({case["id"] for case in cases}) == len(cases)
    schema = "fd_m6_eval_" + uuid4().hex
    dsn = make_conninfo(
        host=os.getenv("FLOWDESK_EVAL_DB_HOST", "127.0.0.1"),
        port=os.getenv("FLOWDESK_EVAL_DB_PORT", config["PG_HOST_PORT"]),
        dbname="flowdesk",
        user="flowdesk_owner",
        password=config["POSTGRES_PASSWORD"],
        sslmode="disable",
    )
    owner = await psycopg.AsyncConnection.connect(dsn, autocommit=True, row_factory=dict_row)
    await owner.execute(sql.SQL("CREATE SCHEMA {}").format(sql.Identifier(schema)))
    report = {
        "started_at": datetime.now(UTC).isoformat(),
        "fixture": dataset["version"],
        "fixture_sha256": hashlib.sha256(dataset_path.read_bytes()).hexdigest(),
        "synthetic": True,
        "isolated_schema": True,
        "real_embedding": args.real,
        "reranking": args.rerank,
        "cost_status": "unknown",
        "scope": "tenant/version filters before ranking; Go evidence validation covered separately",
        "method": "24 independent paraphrases; rerank reorders the same five candidates; "
        "combined latency includes the original cold semantic query",
    }
    try:
        async with AsyncConnectionPool(
            make_conninfo(dsn, options="-csearch_path=" + schema),
            min_size=1,
            max_size=8,
            open=False,
            kwargs={
                "autocommit": True,
                "row_factory": dict_row,
                "prepare_threshold": 0,
            },
        ) as pool:
            await pool.wait(timeout=10)
            await migrate(pool)
            tenant, foreign = uuid4(), uuid4()
            sources, labels = {}, {}
            for case in cases:
                version = uuid5(NAMESPACE_URL, dataset["version"] + ":" + case["id"])
                labels[case["id"]] = version
                sources[str(version)] = {
                    "version_id": str(version),
                    "tenant_id": str(tenant),
                    "knowledge_base_id": str(uuid4()),
                    "document_id": str(uuid4()),
                    "content": case["policy"],
                    "content_hash": hashlib.sha256(case["policy"].encode()).hexdigest(),
                }
            hidden = uuid4()
            sources[str(hidden)] = {
                "version_id": str(hidden),
                "tenant_id": str(foreign),
                "knowledge_base_id": str(uuid4()),
                "document_id": str(uuid4()),
                "content": "合成外租户政策：快递停滞后可以催件，附带未授权特例。",
                "content_hash": hashlib.sha256(
                    "合成外租户政策：快递停滞后可以催件，附带未授权特例。".encode()
                ).hexdigest(),
            }

            async def go(request):
                if request.url.path.endswith("/source"):
                    return httpx.Response(
                        200, json={"data": sources[request.url.path.split("/")[-2]]}
                    )
                return httpx.Response(200, json={"data": {}})

            async with httpx.AsyncClient(
                base_url="https://synthetic-go.test", transport=httpx.MockTransport(go)
            ) as http:
                index = KnowledgeIndex(pool, http)
                for version in sources:
                    await index.accept(uuid4(), UUID(version))
                    await index.process(await index.claim())
            embedder = EmbeddingClient(pool, settings=config) if args.real else None
            if embedder:
                for version in labels.values():
                    await SemanticIndex(pool, embedder).rebuild(version)
                report["embedding_model"] = embedder.model
                report["embedding_dimension"] = embedder.dimension
                report["index_profile"] = embedder.profile
            baseline = Retriever(pool, None, profile="baseline")
            semantic = (
                Retriever(pool, None, embedder=embedder, profile="semantic") if args.real else None
            )
            measurements = {"baseline": []}
            if semantic:
                measurements["semantic"] = []
            if args.rerank:
                measurements["semantic_rerank"] = []
            versions = [*labels.values(), hidden]
            for case in cases:
                query, target = case["query"], labels[case["id"]]
                semantic_rows, semantic_duration = None, 0
                for name, retriever in (("baseline", baseline), ("semantic", semantic)):
                    if retriever is None:
                        continue
                    started = time.monotonic()
                    rows = await retriever.rank(query, tenant, versions, 5)
                    duration = round((time.monotonic() - started) * 1000)
                    assert all(row["tenant_id"] == tenant for row in rows)
                    if name == "semantic":
                        semantic_rows, semantic_duration = rows, duration
                    measurements[name].append(
                        {
                            "id": case["id"],
                            "rank": next(
                                (
                                    position
                                    for position, row in enumerate(rows, 1)
                                    if row["version_id"] == target
                                ),
                                None,
                            ),
                            "duration_ms": duration,
                            "returned_ids": [
                                next(
                                    key
                                    for key, version in labels.items()
                                    if version == row["version_id"]
                                )
                                for row in rows
                            ],
                        }
                    )
                if args.rerank:
                    command = ExecutionCommand(
                        dispatch_id=uuid4(),
                        run_id=uuid4(),
                        lease_generation=1,
                        command="execute",
                        graph_version="ticket-v1",
                    )
                    reranker = ModelReranker(pool, CompatibleModel(pool, command, settings=config))
                    started = time.monotonic()
                    rows = await reranker.rank(query, semantic_rows, embedder.profile)
                    measurements["semantic_rerank"].append(
                        {
                            "id": case["id"],
                            "rank": next(
                                (
                                    position
                                    for position, row in enumerate(rows, 1)
                                    if row["version_id"] == target
                                ),
                                None,
                            ),
                            "duration_ms": semantic_duration
                            + round((time.monotonic() - started) * 1000),
                        }
                    )
            report["results"] = {name: summarize(rows) for name, rows in measurements.items()}
            async with pool.connection() as connection:
                for table in ("embedding_calls", "model_calls"):
                    row = await (
                        await connection.execute(
                            "SELECT count(*) AS attempts,"
                            "coalesce(sum(input_tokens),0) AS input_tokens,"
                            "count(*) FILTER(WHERE status='failed') AS failures FROM " + table
                        )
                    ).fetchone()
                    report[table] = row
                report["model_output_tokens"] = (
                    await (
                        await connection.execute(
                            "SELECT coalesce(sum(output_tokens),0) AS total FROM model_calls"
                        )
                    ).fetchone()
                )["total"]
                statuses = await (
                    await connection.execute(
                        "SELECT status,count(*) AS total FROM retrieval_attempts GROUP BY status"
                    )
                ).fetchall()
                report["rerank_statuses"] = statuses
        report["completed_at"] = datetime.now(UTC).isoformat()
    except Exception as error:
        report["status"] = "invalid"
        report["error_type"] = type(error).__name__
        raise
    finally:
        try:
            for table in ("embedding_calls", "model_calls"):
                report[table] = await (
                    await owner.execute(
                        sql.SQL(
                            "SELECT count(*) AS attempts,coalesce(sum(input_tokens),0) "
                            "AS input_tokens,count(*) FILTER(WHERE status='failed') "
                            "AS failures FROM {}.{}"
                        ).format(sql.Identifier(schema), sql.Identifier(table))
                    )
                ).fetchone()
        except psycopg.Error:
            report["accounting_status"] = "unavailable"
        finally:
            await owner.execute(sql.SQL("DROP SCHEMA {} CASCADE").format(sql.Identifier(schema)))
            await owner.close()
        report["temporary_schema_removed"] = True
        report["completed_at"] = datetime.now(UTC).isoformat()
        destination = ROOT / "reports/m6"
        destination.mkdir(parents=True, exist_ok=True)
        filename = "retrieval-real" if args.real else "retrieval-baseline"
        if report.get("status") == "invalid":
            filename += "-invalid-" + datetime.now(UTC).strftime("%Y%m%dT%H%M%S")
        path = destination / (filename + ".json")
        path.write_text(json.dumps(report, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
    print(
        json.dumps(
            {
                name: {key: value for key, value in row.items() if key != "cases"}
                for name, row in report["results"].items()
            }
        )
    )
    print("Temporary schema removed; report written to " + path.relative_to(ROOT).as_posix())


if __name__ == "__main__":
    if sys.platform == "win32":
        asyncio.set_event_loop_policy(asyncio.WindowsSelectorEventLoopPolicy())
    asyncio.run(main())

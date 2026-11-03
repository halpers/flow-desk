"""Compare lexical baseline and deployed RRF on identical fixed labeled policies."""

import asyncio
import json
import sys
import time
from datetime import UTC, datetime
from uuid import UUID

from eval_common import ROOT, PublicAPI, settings
from psycopg.rows import dict_row
from psycopg_pool import AsyncConnectionPool

sys.path.insert(0, str(ROOT / "flow-desk-agent/src"))
from flowdesk_agent.retrieval import Retriever


async def main():
    config = settings()
    admin = PublicAPI(config, "demo.admin")
    try:
        # Use current Go-authorized document versions, never bypass ACL using an all-tenant query.
        bases = admin.call("GET", "/knowledge-bases")["items"]
        base = next(
            item for item in bases if item["name"] == "M3 acceptance fixtures v1"
        )
        documents = admin.call("GET", f"/knowledge-bases/{base['id']}/documents")[
            "items"
        ]
        policies = json.loads(
            (ROOT / "flow-desk-agent/eval/m3-policies.json").read_text(encoding="utf-8")
        )
        versions = [
            UUID(document["active_version_id"])
            for document in documents
            if document["active_version_id"]
        ]
        tenant = admin.call("GET", "/auth/me")["tenant_id"]
        from psycopg.conninfo import make_conninfo

        dsn = make_conninfo(
            host="127.0.0.1",
            port=config["PG_HOST_PORT"],
            dbname="flowdesk",
            user="fd_agent",
            password=config["AGENT_PASSWORD"],
            options="-csearch_path=agent",
            connect_timeout=5,
            sslmode="disable",
        )
        report = {
            "started_at": datetime.now(UTC).isoformat(),
            "fixture": "m3-policies-v1",
            "k": 5,
            "filter": "current Go-authorized tenant and active version allowlist before ranking",
            "profiles": {},
            "semantic_embedding": False,
        }
        async with AsyncConnectionPool(
            dsn,
            open=False,
            min_size=1,
            max_size=4,
            kwargs={
                "autocommit": True,
                "prepare_threshold": 0,
                "row_factory": dict_row,
            },
        ) as pool:
            await pool.wait(timeout=10)
            retriever = Retriever(pool, None)
            for mode in ("lexical", "hybrid"):
                cases = []
                for policy in policies:
                    document = next(
                        item for item in documents if item["title"] == policy["title"]
                    )
                    for suffix in ("", " 请说明办理要求。"):
                        started = time.monotonic()
                        chosen = await retriever.rank(
                            policy["question"] + suffix, UUID(tenant), versions, 5, mode
                        )
                        relevant = document["active_version_id"]
                        cases.append(
                            {
                                "query_id": policy["id"]
                                + ("-variant" if suffix else ""),
                                "relevant_version": relevant,
                                "returned_versions": [
                                    str(row["version_id"]) for row in chosen
                                ],
                                "hit": any(
                                    str(row["version_id"]) == relevant for row in chosen
                                ),
                                "rank": next(
                                    (
                                        index
                                        for index, row in enumerate(chosen, 1)
                                        if str(row["version_id"]) == relevant
                                    ),
                                    None,
                                ),
                                "duration_ms": round(
                                    (time.monotonic() - started) * 1000
                                ),
                            }
                        )
                report["profiles"][mode] = {
                    "hits": sum(case["hit"] for case in cases),
                    "total": len(cases),
                    "recall_at_5": sum(case["hit"] for case in cases) / len(cases),
                    "cases": cases,
                }
        report["completed_at"] = datetime.now(UTC).isoformat()
        (ROOT / "reports/m5/retrieval-comparison.json").write_text(
            json.dumps(report, ensure_ascii=False, indent=2) + "\n", encoding="utf-8"
        )
        print(
            json.dumps(
                {
                    mode: {
                        key: value for key, value in result.items() if key != "cases"
                    }
                    for mode, result in report["profiles"].items()
                }
            )
        )
    finally:
        admin.close()


if __name__ == "__main__":
    if sys.platform == "win32":
        asyncio.set_event_loop_policy(asyncio.WindowsSelectorEventLoopPolicy())
    asyncio.run(main())

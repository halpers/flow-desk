"""Verify real deployment roles in a disposable mock stack; no provider calls."""

import argparse
import asyncio
import json
import sys
from pathlib import Path
from uuid import uuid4

import psycopg
from langgraph.checkpoint.postgres.aio import AsyncPostgresSaver
from langgraph.graph import END, START, StateGraph
from psycopg.conninfo import make_conninfo
from psycopg.rows import dict_row
from psycopg_pool import AsyncConnectionPool

ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(ROOT / "flow-desk-agent/src"))
from flowdesk_agent.storage.migrations import MIGRATIONS, migrate  # noqa: E402
from flowdesk_agent.storage.permissions import grant_runtime, verify_runtime  # noqa: E402


async def run(args):
    environment = args.env_file.resolve(strict=True)
    if not environment.is_relative_to(ROOT / "work"):
        raise ValueError("Only disposable work/ environment files are accepted")
    settings = dict(
        line.split("=", 1)
        for line in environment.read_text().splitlines()
        if "=" in line and not line.startswith("#")
    )
    if settings.get("MODEL_PROVIDER") != "mock":
        raise ValueError("Only mock stacks are accepted")
    base = dict(
        host="127.0.0.1",
        port=settings.get("PG_HOST_PORT", "55432"),
        dbname="flowdesk",
        options="-csearch_path=agent",
        connect_timeout=5,
    )
    runtime_dsn = make_conninfo(user="fd_agent", password=settings["AGENT_PASSWORD"], **base)
    migration_dsn = make_conninfo(
        user="fd_agent_migrator", password=settings["AGENT_MIGRATION_PASSWORD"], **base
    )
    options = dict(
        open=False,
        min_size=1,
        max_size=3,
        kwargs={"autocommit": True, "row_factory": dict_row, "prepare_threshold": 0},
    )
    checks = []
    async with (
        AsyncConnectionPool(runtime_dsn, **options) as runtime,
        AsyncConnectionPool(migration_dsn, **options) as migration,
    ):
        await runtime.wait(timeout=10)
        await migration.wait(timeout=10)
        await verify_runtime(runtime)
        checks.append("runtime identity, schema versions and least-privilege boundary")
        async with runtime.connection() as connection:
            for query in (
                "CREATE TABLE agent.denied(id integer)",
                "CREATE TABLE public.denied(id integer)",
                "CREATE TEMP TABLE denied(id integer)",
                "CREATE SCHEMA denied",
                "ALTER TABLE execution_commands ADD denied integer",
                "TRUNCATE execution_commands",
                "UPDATE schema_versions SET applied_at=now()",
                "DELETE FROM checkpoint_migrations",
                "SET ROLE fd_agent_migrator",
                "SELECT * FROM business.users LIMIT 1",
            ):
                try:
                    await connection.execute(query)
                except psycopg.errors.InsufficientPrivilege:
                    continue
                raise AssertionError("Runtime privilege boundary failed")
        checks.append("DDL, temporary schema, role elevation and business access rejected")
        graph = StateGraph(dict)
        graph.add_node("write", lambda state: {"synthetic": True})
        graph.add_edge(START, "write")
        graph.add_edge("write", END)
        config = {"configurable": {"thread_id": "run:" + str(uuid4())}}
        app = graph.compile(checkpointer=AsyncPostgresSaver(runtime))
        assert await app.ainvoke({}, config) == {"synthetic": True}
        restored = graph.compile(checkpointer=AsyncPostgresSaver(runtime))
        assert (await restored.aget_state(config)).values == {"synthetic": True}
        checks.append("DML-only runtime persists and restores real LangGraph checkpoints")
        await migrate(migration)
        async with migration.connection() as connection:
            await grant_runtime(connection)
        await verify_runtime(runtime)
        checks.append("explicit migration and grants replay without widening runtime privileges")
    report = {
        "phase": "M7.25",
        "profile": "mock",
        "migration_count": len(MIGRATIONS),
        "model_calls": 0,
        "checks": checks,
    }
    args.output.parent.mkdir(parents=True, exist_ok=True)
    args.output.write_text(json.dumps(report, indent=2) + "\n", encoding="utf-8")
    print(json.dumps({"phase": "M7.25", "passed": len(checks)}))


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--env-file", type=Path, required=True)
    parser.add_argument("--output", type=Path, required=True)
    args = parser.parse_args()
    try:
        # Psycopg async connections require selector IO on Windows, including
        # Python 3.12 where the default runner selects a ProactorEventLoop.
        loop_factory = asyncio.SelectorEventLoop if sys.platform == "win32" else None
        with asyncio.Runner(loop_factory=loop_factory) as runner:
            runner.run(run(args))
    except (psycopg.Error, OSError, ValueError, RuntimeError, AssertionError):
        raise SystemExit(
            "AGENT_MIGRATION_ACCEPTANCE_FAILED; check isolated stack and role setup"
        ) from None


if __name__ == "__main__":
    main()

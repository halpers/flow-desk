"""Internal HTTP API. Business state belongs to the Go service."""

import asyncio
import hmac
import os
from contextlib import asynccontextmanager
from uuid import UUID

import httpx
from fastapi import Depends, FastAPI, Header, HTTPException
from fastapi.responses import Response
from psycopg.rows import dict_row
from psycopg_pool import AsyncConnectionPool
from pydantic import BaseModel

from flowdesk_agent.api.schemas import (
    CancelCommand,
    CleanupCommand,
    CommandReceipt,
    ExecutionCommand,
    IndexCommand,
)
from flowdesk_agent.embeddings import configured_embedder
from flowdesk_agent.knowledge_cleanup import cleanup_index
from flowdesk_agent.observability import metrics, telemetry
from flowdesk_agent.observability.operations import AgentTraceData, read_trace
from flowdesk_agent.retrieval import KnowledgeIndex
from flowdesk_agent.runtime import AgentRuntime
from flowdesk_agent.storage.commands import CommandConflict
from flowdesk_agent.storage.migrations import migrate
from flowdesk_agent.tools.go_client import ToolError


class Health(BaseModel):
    status: str
    service: str


@asynccontextmanager
async def lifespan(app: FastAPI):
    token = os.environ["INTERNAL_TOKEN"]
    if len(token) < 32:
        raise RuntimeError("INTERNAL_TOKEN must contain at least 32 characters")
    pool = AsyncConnectionPool(
        os.environ["AGENT_DATABASE_URL"],
        open=False,
        min_size=1,
        max_size=32,
        kwargs={"autocommit": True, "prepare_threshold": 0, "row_factory": dict_row},
    )
    await pool.open(wait=True)
    tracing = telemetry.configure()
    try:
        await migrate(pool)
        async with httpx.AsyncClient(
            base_url=os.environ["GO_BASE_URL"],
            timeout=5,
            trust_env=False,
            headers={"Authorization": f"Bearer {token}"},
            event_hooks={"request": [telemetry.inject_go_trace]},
        ) as http:
            runtime = AgentRuntime(pool, http)
            app.state.pool = pool
            app.state.runtime = runtime
            app.state.knowledge = KnowledgeIndex(pool, http, embedder=configured_embedder(pool))
            workers = [asyncio.create_task(runtime.execution_loop())]
            workers.append(asyncio.create_task(app.state.knowledge.loop()))
            workers.extend(asyncio.create_task(runtime.callback_loop()) for _ in range(4))
            app.state.workers = workers
            try:
                yield
            finally:
                for worker in workers:
                    worker.cancel()
                await asyncio.gather(*workers, return_exceptions=True)
    finally:
        await pool.close()
        if tracing:
            await asyncio.to_thread(tracing.shutdown)


app = FastAPI(title="FlowDesk Agent", version="0.1.0", lifespan=lifespan)


def service_auth(authorization: str = Header(default="")):
    expected = os.environ.get("INTERNAL_TOKEN", "")
    if len(expected) < 32 or not hmac.compare_digest(authorization, f"Bearer {expected}"):
        raise HTTPException(401, "service authentication required")


@app.post("/internal/v1/knowledge/index", status_code=202, dependencies=[Depends(service_auth)])
async def accept_index(command: IndexCommand):
    try:
        await app.state.knowledge.accept(command.job_id, command.version_id)
    except ToolError as exc:
        raise HTTPException(409, "index command conflict") from exc
    return {"job_id": str(command.job_id), "status": "accepted"}


@app.post("/internal/v1/knowledge/cleanup", status_code=202, dependencies=[Depends(service_auth)])
async def accept_cleanup(command: CleanupCommand):
    try:
        await cleanup_index(app.state.pool, command)
    except ToolError as exc:
        raise HTTPException(409, "cleanup command conflict") from exc
    return {"version_id": str(command.version_id), "status": "purged"}


@app.post(
    "/internal/v1/executions",
    status_code=202,
    response_model=CommandReceipt,
    dependencies=[Depends(service_auth)],
)
async def accept_execution(command: ExecutionCommand, traceparent: str = Header(default="")):
    try:
        row = await app.state.runtime.commands.accept(
            command, telemetry.safe_traceparent(traceparent)
        )
    except CommandConflict as exc:
        raise HTTPException(409, "dispatch payload conflict") from exc
    return {"dispatch_id": row["dispatch_id"], "status": row["status"]}


@app.get(
    "/internal/v1/executions/{dispatch_id}",
    response_model=CommandReceipt,
    dependencies=[Depends(service_auth)],
)
async def get_execution(dispatch_id: UUID):
    row = await app.state.runtime.commands.find(dispatch_id)
    if row is None:
        raise HTTPException(404, "execution not found")
    return {"dispatch_id": row["dispatch_id"], "status": row["status"]}


@app.post(
    "/internal/v1/executions/{run_id}/cancel", status_code=202, dependencies=[Depends(service_auth)]
)
async def cancel_execution(run_id: UUID, command: CancelCommand):
    if run_id != command.run_id:
        raise HTTPException(400, "run mismatch")
    await app.state.runtime.commands.cancel(command)
    return {"dispatch_id": str(command.dispatch_id), "status": "cancelled"}


@app.get("/health/live", response_model=Health)
async def live():
    return {"status": "ok", "service": "flowdesk-agent"}


@app.get(
    "/internal/v1/runs/{run_id}/trace",
    response_model=AgentTraceData,
    dependencies=[Depends(service_auth)],
)
async def run_trace(run_id: UUID, tenant_id: UUID):
    return await read_trace(app.state.pool, run_id, tenant_id)


@app.get("/metrics", include_in_schema=False)
async def scrape_metrics():
    try:
        body = await metrics.render(app.state.pool)
    except Exception:
        return Response("flowdesk_agent_up 0\n", status_code=503, media_type="text/plain")
    return Response(body, media_type="text/plain; version=0.0.4")


@app.get("/health/ready", response_model=Health)
async def ready():
    try:
        if any(worker.done() for worker in app.state.workers):
            raise RuntimeError("worker unavailable")
        async with app.state.pool.connection() as conn:
            await conn.execute("SELECT 1")
    except Exception as exc:
        raise HTTPException(503, "database unavailable") from exc
    return {"status": "ok", "service": "flowdesk-agent"}

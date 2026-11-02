"""Internal HTTP API. Business state belongs to the Go service."""

import os
from contextlib import asynccontextmanager

from fastapi import FastAPI, HTTPException
from psycopg_pool import AsyncConnectionPool
from pydantic import BaseModel


class Health(BaseModel):
    status: str
    service: str


@asynccontextmanager
async def lifespan(app: FastAPI):
    pool = AsyncConnectionPool(os.environ["AGENT_DATABASE_URL"], open=False)
    await pool.open(wait=True)
    app.state.pool = pool
    yield
    await pool.close()


app = FastAPI(title="FlowDesk Agent", version="0.1.0", lifespan=lifespan)


@app.get("/health/live", response_model=Health)
async def live():
    return {"status": "ok", "service": "flowdesk-agent"}


@app.get("/health/ready", response_model=Health)
async def ready():
    try:
        async with app.state.pool.connection() as conn:
            await conn.execute("SELECT 1")
    except Exception as exc:
        raise HTTPException(503, "database unavailable") from exc
    return {"status": "ok", "service": "flowdesk-agent"}

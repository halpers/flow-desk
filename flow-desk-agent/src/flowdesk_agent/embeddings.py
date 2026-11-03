"""Bounded compatible embeddings; tenant-scoped cache and persistent attempt accounting."""

import asyncio
import hashlib
import json
import math
import os
import time
from urllib.parse import urlparse
from uuid import uuid4

import httpx
from pydantic import BaseModel, ConfigDict, Field, FiniteFloat, ValidationError

from flowdesk_agent.tools.go_client import ToolError

MAX_DIMENSION = 8192
BATCH_SIZE = 16
MAX_TEXT_BYTES = 24000
MAX_INDEX_CALLS = 200
MAX_INDEX_BYTES = 4_000_000
MAX_QUERY_CALLS = 8
MAX_QUERY_BYTES = 24000
HTTP_ATTEMPTS = 2
HTTP_TIMEOUT_SECONDS = 12


class EmbeddingItem(BaseModel):
    model_config = ConfigDict(extra="ignore", strict=True)
    index: int = Field(ge=0, strict=True)
    embedding: list[FiniteFloat] = Field(min_length=1, max_length=MAX_DIMENSION)


class EmbeddingResponse(BaseModel):
    model_config = ConfigDict(extra="ignore", strict=True)
    data: list[EmbeddingItem] = Field(min_length=1, max_length=BATCH_SIZE)
    usage: dict[str, int] = Field(default_factory=dict)


def vector_literal(values):
    return "[" + ",".join(format(value, ".9g") for value in values) + "]"


def configured_embedder(pool, *, settings=None, transport=None):
    config = settings if settings is not None else os.environ
    keys = ("EMBEDDING_BASE_URL", "EMBEDDING_MODEL", "EMBEDDING_API_KEY")
    if not any(config.get(key) for key in keys):
        return None
    return EmbeddingClient(pool, settings=config, transport=transport)


class EmbeddingClient:
    def __init__(self, pool, *, settings=None, transport=None):
        self.pool = pool
        config = settings if settings is not None else os.environ
        self.base_url = config.get("EMBEDDING_BASE_URL", "").rstrip("/")
        self.model = config.get("EMBEDDING_MODEL", "")
        self.key = config.get("EMBEDDING_API_KEY", "")
        self.revision = config.get("EMBEDDING_REVISION", "1")
        try:
            self.requested_dimension = int(config.get("EMBEDDING_DIMENSIONS", "0") or "0")
        except ValueError as exc:
            raise ToolError("EMBEDDING_CONFIG_INVALID") from exc
        endpoint = urlparse(self.base_url)
        if (
            not all((self.base_url, self.model, self.key, self.revision))
            or endpoint.scheme not in ("http", "https")
            or not endpoint.hostname
            or endpoint.username
            or endpoint.password
            or endpoint.query
            or endpoint.fragment
            or not 0 <= self.requested_dimension <= MAX_DIMENSION
        ):
            raise ToolError("EMBEDDING_CONFIG_INVALID")
        profile = json.dumps(
            [self.base_url, self.model, self.requested_dimension, self.revision],
            separators=(",", ":"),
        )
        self.profile = "semantic-v1-" + hashlib.sha256(profile.encode()).hexdigest()[:24]
        self.dimension = self.requested_dimension or None
        self.transport = transport

    def validate_vector(self, values):
        if (
            not 1 <= len(values) <= MAX_DIMENSION
            or any(isinstance(value, bool) or not math.isfinite(value) for value in values)
            or not any(abs(value) > 1e-12 for value in values)
        ):
            raise ToolError("EMBEDDING_RESPONSE_INVALID")
        if self.dimension is not None and len(values) != self.dimension:
            raise ToolError("EMBEDDING_DIMENSION_CHANGED")
        self.dimension = len(values)
        return values

    async def reserve(self, tenant_id, operation, request_hash, byte_count):
        is_index = operation.startswith("index:")
        call_limit = MAX_INDEX_CALLS if is_index else MAX_QUERY_CALLS
        byte_limit = MAX_INDEX_BYTES if is_index else MAX_QUERY_BYTES
        async with self.pool.connection() as connection, connection.transaction():
            await connection.execute(
                "SELECT pg_advisory_xact_lock(hashtextextended(%s,0))",
                (f"{tenant_id}:{self.profile}:{operation}",),
            )
            usage = await (
                await connection.execute(
                    "SELECT count(*) AS calls,coalesce(sum(reserved_bytes),0) AS bytes "
                    "FROM embedding_calls WHERE tenant_id=%s AND profile=%s AND operation=%s",
                    (tenant_id, self.profile, operation),
                )
            ).fetchone()
            if usage["calls"] >= call_limit or usage["bytes"] + byte_count > byte_limit:
                raise ToolError("EMBEDDING_BUDGET_EXCEEDED")
            call_id = uuid4()
            await connection.execute(
                "INSERT INTO embedding_calls(call_id,tenant_id,profile,operation,"
                "request_hash,reserved_bytes,status) VALUES(%s,%s,%s,%s,%s,%s,'reserved')",
                (call_id, tenant_id, self.profile, operation, request_hash, byte_count),
            )
            return call_id

    async def encode(self, tenant_id, texts, operation, *, guard=None):
        if (
            not texts
            or len(texts) > 1000
            or len(operation) > 120
            or not operation.startswith(("index:", "query:"))
            or any(not text.strip() or len(text.encode()) > MAX_TEXT_BYTES for text in texts)
        ):
            raise ToolError("EMBEDDING_ARGUMENT_INVALID")
        hashes = [hashlib.sha256(text.encode()).hexdigest() for text in texts]
        async with self.pool.connection() as connection:
            rows = await (
                await connection.execute(
                    "SELECT content_hash,embedding::text AS embedding FROM embedding_cache "
                    "WHERE tenant_id=%s AND profile=%s AND content_hash=ANY(%s)",
                    (tenant_id, self.profile, hashes),
                )
            ).fetchall()
        results = {
            row["content_hash"]: self.validate_vector(json.loads(row["embedding"])) for row in rows
        }
        missing = list(dict.fromkeys(digest for digest in hashes if digest not in results))
        sources = dict(zip(hashes, texts, strict=True))
        async with httpx.AsyncClient(
            base_url=self.base_url + "/",
            headers={"Authorization": "Bearer " + self.key},
            timeout=HTTP_TIMEOUT_SECONDS,
            trust_env=False,
            follow_redirects=False,
            transport=self.transport,
        ) as http:
            for start in range(0, len(missing), BATCH_SIZE):
                batch = missing[start : start + BATCH_SIZE]
                payload = {
                    "model": self.model,
                    "input": [sources[digest] for digest in batch],
                    "encoding_format": "float",
                }
                if self.requested_dimension:
                    payload["dimensions"] = self.requested_dimension
                request_hash = hashlib.sha256(
                    json.dumps(payload, sort_keys=True).encode()
                ).hexdigest()
                for attempt in range(HTTP_ATTEMPTS):
                    if guard:
                        await guard()
                    call_id = await self.reserve(
                        tenant_id,
                        operation,
                        request_hash,
                        sum(len(sources[digest].encode()) for digest in batch),
                    )
                    started = time.monotonic()
                    code, usage, vectors, retry = "EMBEDDING_UNAVAILABLE", None, None, True
                    try:
                        response = await http.post("embeddings", json=payload)
                        if response.status_code == 200:
                            body = EmbeddingResponse.model_validate(response.json())
                            ordered = sorted(body.data, key=lambda item: item.index)
                            if [item.index for item in ordered] != list(range(len(batch))):
                                raise ToolError("EMBEDDING_RESPONSE_INVALID")
                            vectors = [self.validate_vector(item.embedding) for item in ordered]
                            usage = body.usage.get("prompt_tokens", body.usage.get("total_tokens"))
                            if usage is not None and not 0 <= usage <= 1_000_000_000:
                                raise ToolError("EMBEDDING_RESPONSE_INVALID")
                        else:
                            retry = response.status_code == 429 or response.status_code >= 500
                            code = (
                                "EMBEDDING_RATE_LIMITED"
                                if response.status_code == 429
                                else ("EMBEDDING_UNAVAILABLE" if retry else "EMBEDDING_REJECTED")
                            )
                    except (ValueError, KeyError, ValidationError, ToolError) as exc:
                        code = (
                            exc.code if isinstance(exc, ToolError) else "EMBEDDING_RESPONSE_INVALID"
                        )
                        vectors, retry = None, False
                    except httpx.HTTPError:
                        pass
                    async with self.pool.connection() as connection, connection.transaction():
                        await connection.execute(
                            "UPDATE embedding_calls SET status=%s,input_tokens=%s,dimension=%s,"
                            "duration_ms=%s,error_code=%s,completed_at=now() WHERE call_id=%s",
                            (
                                "completed" if vectors else "failed",
                                usage,
                                self.dimension if vectors else None,
                                round((time.monotonic() - started) * 1000),
                                "" if vectors else code,
                                call_id,
                            ),
                        )
                        if vectors:
                            for digest, values in zip(batch, vectors, strict=True):
                                await connection.execute(
                                    "INSERT INTO embedding_cache(tenant_id,profile,content_hash,"
                                    "dimension,embedding) VALUES(%s,%s,%s,%s,%s::knowledge.vector) "
                                    "ON CONFLICT(tenant_id,profile,content_hash) DO NOTHING",
                                    (
                                        tenant_id,
                                        self.profile,
                                        digest,
                                        len(values),
                                        vector_literal(values),
                                    ),
                                )
                                results[digest] = values
                    if vectors:
                        break
                    if not retry or attempt == HTTP_ATTEMPTS - 1:
                        raise ToolError(code)
                    await asyncio.sleep(0.2)
        return [results[digest] for digest in hashes]

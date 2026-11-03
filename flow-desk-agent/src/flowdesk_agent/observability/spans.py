"""Persist node attempts separately from checkpoints and business transactions."""

import asyncio
import logging
import time
from uuid import uuid4

from langgraph.errors import GraphInterrupt

logger = logging.getLogger(__name__)


class SpanRecorder:
    def __init__(self, pool, command):
        self.pool, self.command = pool, command

    def wrap(self, node_name, function):
        async def measured(state):
            started = time.monotonic()
            status = "completed"
            try:
                return await function(state)
            except GraphInterrupt:
                status = "interrupted"
                raise
            except asyncio.CancelledError:
                status = "cancelled"
                raise
            except BaseException:
                status = "failed"
                raise
            finally:
                duration = round((time.monotonic() - started) * 1000)
                try:
                    async with self.pool.connection() as conn:
                        await conn.execute(
                            "INSERT INTO execution_spans(span_id,run_id,dispatch_id,"
                            "lease_generation,node_name,status,duration_ms) "
                            "VALUES(%s,%s,%s,%s,%s,%s,%s)",
                            (
                                uuid4(),
                                self.command.run_id,
                                self.command.dispatch_id,
                                self.command.lease_generation,
                                node_name,
                                status,
                                duration,
                            ),
                        )
                except Exception:
                    # Tracing cannot undo a committed effect or override a graph interrupt.
                    logger.warning(
                        "trace persistence failed run_id=%s node=%s",
                        self.command.run_id,
                        node_name,
                    )

        return measured

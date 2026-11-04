"""OTel metadata only. No automatic HTTP, SQL, model, or exception-body instrumentation."""

import asyncio
import os
import re
from contextlib import contextmanager

from langgraph.errors import GraphInterrupt
from opentelemetry import trace
from opentelemetry.trace import Status, StatusCode
from opentelemetry.trace.propagation.tracecontext import TraceContextTextMapPropagator

propagation = TraceContextTextMapPropagator()


def safe_traceparent(value: str) -> str:
    if not re.fullmatch(r"00-[0-9a-f]{32}-[0-9a-f]{16}-0[01]", value):
        return ""
    context = trace.get_current_span(propagation.extract({"traceparent": value})).get_span_context()
    return value if context.is_valid else ""


def current_headers():
    headers = {}
    propagation.inject(headers)
    return headers


async def inject_go_trace(request):
    if "traceparent" not in request.headers:
        request.headers.update(current_headers())


def configure():
    endpoint = os.getenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", "")
    if not endpoint:
        return None
    from opentelemetry.exporter.otlp.proto.http.trace_exporter import OTLPSpanExporter
    from opentelemetry.sdk.resources import Resource
    from opentelemetry.sdk.trace import TracerProvider
    from opentelemetry.sdk.trace.export import BatchSpanProcessor

    provider = TracerProvider(resource=Resource.create({"service.name": "flowdesk-agent"}))
    provider.add_span_processor(
        BatchSpanProcessor(
            OTLPSpanExporter(endpoint=endpoint, timeout=3), schedule_delay_millis=500
        )
    )
    trace.set_tracer_provider(provider)
    return provider


@contextmanager
def execution_span(command, traceparent=""):
    context = propagation.extract({"traceparent": safe_traceparent(traceparent)})
    with trace.get_tracer("flowdesk.agent").start_as_current_span(
        "agent.execute",
        context=context,
        attributes={
            "flowdesk.run_id": str(command.run_id),
            "flowdesk.dispatch_id": str(command.dispatch_id),
            "flowdesk.generation": command.lease_generation,
        },
        record_exception=False,
        set_status_on_exception=False,
    ):
        yield


@contextmanager
def node_span(name, command):
    with trace.get_tracer("flowdesk.agent").start_as_current_span(
        "agent.node." + name,
        attributes={"flowdesk.run_id": str(command.run_id)},
        record_exception=False,
        set_status_on_exception=False,
    ) as span:
        try:
            yield span
        except GraphInterrupt:
            span.set_attribute("flowdesk.node.status", "interrupted")
            raise
        except asyncio.CancelledError:
            span.set_attribute("flowdesk.node.status", "cancelled")
            raise
        except BaseException:
            # No exception class/message/stacktrace is exported.
            span.set_status(Status(StatusCode.ERROR))
            span.set_attribute("flowdesk.node.status", "failed")
            raise
        else:
            span.set_attribute("flowdesk.node.status", "completed")

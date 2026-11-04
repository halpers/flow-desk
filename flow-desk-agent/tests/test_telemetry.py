import asyncio
from uuid import uuid4

from opentelemetry.sdk.trace import TracerProvider
from opentelemetry.sdk.trace.export import SimpleSpanProcessor
from opentelemetry.sdk.trace.export.in_memory_span_exporter import InMemorySpanExporter
from test_durable_runtime import command, isolated_pool

from flowdesk_agent.observability import metrics, telemetry
from flowdesk_agent.storage.commands import CommandStore


def test_trace_parent_is_durable_but_does_not_change_command_identity():
    async def scenario():
        async with isolated_pool() as pool:
            item = command()
            parent = "00-" + uuid4().hex + "-0123456789abcdef-01"
            store = CommandStore(pool)
            await store.accept(item, telemetry.safe_traceparent(parent))
            replay = await store.accept(item, "")
            assert replay["traceparent"] == parent
            assert (await store.find(item.dispatch_id))["traceparent"] == parent
            text = await metrics.render(pool)
            assert 'flowdesk_agent_commands{status="queued"} 1' in text
            assert "flowdesk_agent_up 1" in text
            assert not any(value in text for value in (str(item.run_id), parent, "tenant_id"))

    asyncio.run(scenario())


def test_otel_propagation_and_failure_do_not_export_private_state(monkeypatch):
    exporter = InMemorySpanExporter()
    provider = TracerProvider()
    provider.add_span_processor(SimpleSpanProcessor(exporter))
    tracer = provider.get_tracer("synthetic")
    monkeypatch.setattr(telemetry.trace, "get_tracer", lambda name: tracer)
    item = command()
    parent = "00-" + uuid4().hex + "-0123456789abcdef-01"
    assert telemetry.safe_traceparent(parent) == parent
    assert telemetry.safe_traceparent("bad-parent-secret") == ""
    try:
        with telemetry.execution_span(item, parent):
            with telemetry.node_span("synthetic", item):
                assert (
                    telemetry.current_headers()["traceparent"].split("-")[1] == parent.split("-")[1]
                )
                raise RuntimeError("PRIVATE_STATE_CANARY")
    except RuntimeError:
        pass
    spans = exporter.get_finished_spans()
    assert len(spans) == 2 and all(not span.events for span in spans)
    assert all(span.context.trace_id == int(parent.split("-")[1], 16) for span in spans)
    assert all("PRIVATE_STATE_CANARY" not in str(span.attributes) for span in spans)
    assert all(not span.status.description for span in spans)
    provider.shutdown()

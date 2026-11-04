import asyncio
import json
from uuid import uuid4

import httpx
import pytest
from langgraph.checkpoint.postgres.aio import AsyncPostgresSaver
from langgraph.types import Command
from test_actions_recovery import DurableTools
from test_durable_runtime import command, isolated_pool

from flowdesk_agent.graph.read_graph import build_graph
from flowdesk_agent.models import CompatibleModel
from flowdesk_agent.tools.go_client import ToolError


class ScenarioTools(DurableTools):
    def __init__(self):
        super().__init__(command().model_copy(update={"graph_version": "ticket-v2"}))
        self.tickets = [
            {"id": self.ticket_id, "ticket_no": "T" + uuid4().hex, "version": 4, "status": "open"},
            {
                "id": str(uuid4()),
                "ticket_no": "T" + uuid4().hex,
                "version": 9,
                "status": "in_progress",
            },
        ]
        self.proposals = {}

    async def call(self, tool, arguments):
        result = await super().call(tool, arguments)
        if tool == "ticket.search":
            return {"tickets": self.tickets}
        return result

    async def prepare_action(self, action_id, tool, arguments, evidence_ids):
        proposal = (tool, arguments, evidence_ids)
        assert self.proposals.setdefault(action_id, proposal) == proposal
        return await super().prepare_action(action_id, tool, arguments, evidence_ids)

    async def execute_action(self, action_id):
        tool, arguments, _ = self.proposals[action_id]
        self.effects.setdefault(
            action_id,
            {
                "action_id": action_id,
                "tool_name": tool,
                "business_object_type": "ticket",
                "business_object_id": arguments["ticket_id"],
                "result": {"ticket_no": self.tickets[1]["ticket_no"], "status": "resolved"},
            },
        )
        return await super().execute_action(action_id)


@pytest.mark.parametrize("action,word", [("start_processing", "接手处理"), ("resolve", "办结")])
def test_workflow_checkpoint_replays_same_proposal_and_committed_receipt(action, word):
    async def scenario():
        async with isolated_pool() as pool:
            tools = ScenarioTools()
            config = {"configurable": {"thread_id": f"run:{tools.command.run_id}"}}
            content = (
                f"请{word}工单 {tools.tickets[1]['ticket_no']}，"
                "订单 FD202611020002，处理原因：合成回访结论"
            )
            async with pool.connection() as conn:
                graph = build_graph(tools, AsyncPostgresSaver(conn))
                with pytest.raises(RuntimeError, match="prepare"):
                    await graph.ainvoke({"content": content}, config)
                action_id = (await graph.aget_state(config)).values["action_id"]
                proposal = tools.proposals[action_id]
                assert proposal[0] == "ticket." + action
                assert proposal[1]["ticket_id"] == tools.tickets[1]["id"]
                assert proposal[1]["expected_version"] == 9
            async with pool.connection() as conn:
                graph = build_graph(tools, AsyncPostgresSaver(conn))
                await graph.ainvoke(None, config)
                assert not tools.effects
                assert tools.allocated == [action_id, action_id]
            async with pool.connection() as conn:
                graph = build_graph(tools, AsyncPostgresSaver(conn))
                with pytest.raises(RuntimeError, match="COMMIT"):
                    await graph.ainvoke(
                        Command(resume={"decision": "approved", "action_id": action_id}), config
                    )
            async with pool.connection() as conn:
                graph = build_graph(tools, AsyncPostgresSaver(conn))
                await graph.ainvoke(None, config)
            assert len(tools.effects) == 1
            assert tools.events[-1][0] == "run.succeeded"
            assert tools.events[-2][1]["action_receipts"] == [{"action_id": action_id}]

    asyncio.run(scenario())


def test_multiple_tickets_require_explicit_selection_and_unknown_number_never_falls_back():
    async def scenario():
        async with isolated_pool() as pool, pool.connection() as conn:
            tools = ScenarioTools()
            tools.prepare_crash = False
            config = {"configurable": {"thread_id": f"run:{tools.command.run_id}"}}
            graph = build_graph(tools, AsyncPostgresSaver(conn))
            await graph.ainvoke({"content": "请接手处理工单，订单 FD202611020002"}, config)
            state = await graph.aget_state(config)
            assert not tools.allocated
            assert all(ticket["ticket_no"] in state.values["question"] for ticket in tools.tickets)
            await graph.ainvoke(
                Command(
                    resume={
                        "input_id": state.values["input_id"],
                        "input": tools.tickets[1]["ticket_no"],
                    }
                ),
                config,
            )
            assert next(iter(tools.proposals.values()))[1]["ticket_id"] == tools.tickets[1]["id"]
            # An explicit inaccessible/mistyped number must never target the only visible ticket.
            tools = ScenarioTools()
            tools.prepare_crash = False
            tools.tickets = tools.tickets[:1]
            graph = build_graph(tools, AsyncPostgresSaver(conn))
            config = {"configurable": {"thread_id": f"run:{tools.command.run_id}"}}
            await graph.ainvoke(
                {"content": f"请办结工单 T{uuid4().hex}，订单 FD202611020002"}, config
            )
            assert not tools.allocated
            state = await graph.aget_state(config)
            await graph.ainvoke(
                Command(
                    resume={
                        "input_id": state.values["input_id"],
                        "input": tools.tickets[0]["ticket_no"],
                    }
                ),
                config,
            )
            assert next(iter(tools.proposals.values()))[1]["ticket_id"] == tools.tickets[0]["id"]

    asyncio.run(scenario())


def test_compatible_model_new_plan_is_versioned_and_legacy_schema_remains_strict():
    async def scenario():
        async with isolated_pool() as pool:

            async def handle(request):
                body = json.loads(request.content)
                assert "capability" not in json.dumps(body)
                return httpx.Response(
                    200,
                    json={
                        "choices": [
                            {
                                "finish_reason": "stop",
                                "message": {
                                    "content": json.dumps(
                                        {
                                            "order_no": "FD202611020002",
                                            "requested_action": "resolve",
                                            "knowledge_query": "",
                                            "ticket_category": "logistics",
                                        }
                                    )
                                },
                            }
                        ]
                    },
                )

            settings = {
                "MODEL_BASE_URL": "https://synthetic.test/v1",
                "MODEL_NAME": "synthetic",
                "MODEL_API_KEY": "synthetic-key",
            }
            for version in ("ticket-v2", "ticket-v1", "read-v1"):
                model = CompatibleModel(
                    pool,
                    command().model_copy(update={"graph_version": version}),
                    transport=httpx.MockTransport(handle),
                    settings=settings,
                )
                if version == "ticket-v2":
                    plan = await model.plan("办结工单 FD202611020002")
                    assert (
                        plan.requested_action == "resolve" and plan.ticket_category == "logistics"
                    )
                else:
                    with pytest.raises(ToolError, match="MODEL_OUTPUT_INVALID"):
                        await model.plan("办结工单 FD202611020002")

    asyncio.run(scenario())

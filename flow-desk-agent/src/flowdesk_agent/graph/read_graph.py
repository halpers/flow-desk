"""Deterministic M2 graph. Trusted authority lives in the client, outside checkpoint state."""

import re
from typing import TypedDict
from uuid import uuid5

from langgraph.graph import END, START, StateGraph
from langgraph.types import interrupt

from flowdesk_agent.models import Answer, Plan
from flowdesk_agent.runtime_gen import ToolName
from flowdesk_agent.tools.go_client import GoTools, ToolError

ORDER_PATTERN = re.compile(r"(?<![A-Za-z0-9])FD\d{12}(?![A-Za-z0-9])", re.IGNORECASE)


class TaskState(TypedDict, total=False):
    content: str
    plan: dict
    order: dict
    logistics: list[dict]
    answer: str
    evidence: list[dict]
    tickets: list[dict]
    input_id: str
    question: str
    action_id: str
    approval: dict
    receipt: dict
    blocked: str


class FakeModel:
    async def plan(self, content: str) -> Plan:
        match = ORDER_PATTERN.search(content)
        action = "query"
        if "备注" in content:
            action = "add_note"
        elif re.search(r"(?:创建|建立|开|建).{0,6}工单", content):
            action = "create_ticket"
        return Plan(
            order_no=match.group(0).upper() if match else None,
            requested_action=action,
            knowledge_query=content[:500]
            if any(
                word in content for word in ("政策", "规则", "退货", "退款", "赔付", "发票", "保修")
            )
            else "",
        )

    async def answer(self, state: TaskState) -> str | Answer:
        plan = Plan.model_validate(state["plan"])
        if state.get("receipt"):
            receipt = state["receipt"]
            label = "工单已创建" if receipt["tool_name"] == "ticket.create" else "备注已保存"
            return Answer(
                content=f"{label}，工单编号 {receipt['result']['ticket_no']}。",
                evidence_ids=[item["id"] for item in state.get("evidence", [])],
            )
        if state.get("blocked"):
            return Answer(content=state["blocked"], evidence_ids=[])
        if plan.knowledge_query:
            evidence = state.get("evidence", [])
            return Answer(
                content="\n".join(item["excerpt"] for item in evidence)
                if evidence
                else "当前授权知识库中没有足够依据，请联系主管核实。",
                evidence_ids=[item["id"] for item in evidence],
            )
        if not plan.order_no:
            return "请提供以 FD 开头的完整订单号，例如 FD202611020001。"
        order = state["order"]
        labels = {
            "in_transit": "运输中",
            "delivered": "已送达",
            "processing": "处理中",
            "cancelled": "已取消",
        }
        result = (
            f"订单 {order['order_no']}，客户 {order['masked_customer_name']}，"
            f"状态：{labels.get(order['status'], order['status'])}。"
            f"金额 {order['total_amount']} {order['currency']}，订单版本 {order['version']}。"
        )
        if state.get("logistics"):
            event = state["logistics"][-1]
            result += (
                f"\n最新物流：{event['description']}；来源更新时间：{event['source_updated_at']}。"
            )
        else:
            result += "\n当前没有物流记录。"
        return result


def build_graph(tools: GoTools, checkpointer, model=None, retriever=None):
    model = model or FakeModel()

    async def route(state: TaskState):
        await tools.authorize()
        plan = Plan.model_validate(await model.plan(state["content"]))
        if plan.order_no and plan.order_no not in {
            match.upper() for match in ORDER_PATTERN.findall(state["content"])
        }:
            raise ToolError("MODEL_PLAN_INVALID")
        await tools.guard()
        return {"plan": plan.model_dump()}

    async def read(state: TaskState):
        plan = Plan.model_validate(state["plan"])
        arguments = {"order_no": plan.order_no}
        order = await tools.call(ToolName.ORDER_LOOKUP, arguments)
        logistics = await tools.call(ToolName.LOGISTICS_TRACK, arguments)
        tickets = (
            await tools.call(ToolName.TICKET_SEARCH, arguments)
            if plan.requested_action != "query" or "工单" in state["content"]
            else {}
        )
        return {
            "order": order["order"],
            "logistics": logistics.get("logistics", []),
            "tickets": tickets.get("tickets", []),
        }

    async def finalize(state: TaskState):
        context = await tools.authorize()
        plan = Plan.model_validate(state["plan"])
        output = (
            Answer(content="当前授权知识库中没有足够依据，请联系主管核实。", evidence_ids=[])
            if plan.knowledge_query and not state.get("evidence") and not state.get("receipt")
            else await model.answer(state)
        )
        answer = (
            Answer(content=output, evidence_ids=[])
            if isinstance(output, str)
            else Answer.model_validate(output)
        )
        valid_ids = {item["id"] for item in state.get("evidence", [])}
        if (
            len(set(answer.evidence_ids)) != len(answer.evidence_ids)
            or not set(answer.evidence_ids) <= valid_ids
        ):
            raise ToolError("MODEL_CITATION_INVALID")
        if state.get("evidence") and not answer.evidence_ids:
            raise ToolError("MODEL_CITATION_MISSING")
        content = answer.content
        if state.get("receipt"):
            receipt = state["receipt"]
            label = "已创建工单" if receipt["tool_name"] == "ticket.create" else "已添加备注"
            content = f"{label}：{receipt['result']['ticket_no']}。\n{content}"
        await tools.guard()
        await tools.callbacks.emit(
            tools.command,
            "message.completed",
            {
                "message_id": str(context.message_id),
                "content": content,
                "citations": [{"id": identifier} for identifier in answer.evidence_ids],
                "action_receipts": [{"action_id": state["receipt"]["action_id"]}]
                if state.get("receipt")
                else [],
            },
        )
        terminal = await tools.callbacks.emit(tools.command, "run.succeeded", {})
        await tools.callbacks.wait(terminal)
        return {"answer": content}

    async def retrieve(state: TaskState):
        query = Plan.model_validate(state["plan"]).knowledge_query
        return {"evidence": await retriever.search(query) if query and retriever else []}

    async def allocate_input(state: TaskState):
        plan = Plan.model_validate(state["plan"])
        question = "请提供以 FD 开头的完整订单号，例如 FD202611020002。"
        if plan.order_no:
            question = "请选择要添加备注的工单，回复完整工单编号：" + "、".join(
                item["ticket_no"] for item in state.get("tickets", [])
            )
        return {"input_id": str(uuid5(tools.command.dispatch_id, "input")), "question": question}

    async def wait_input(state: TaskState):
        resumed = interrupt(
            {"kind": "input", "input_id": state["input_id"], "question": state["question"]}
        )
        if (
            not isinstance(resumed, dict)
            or resumed.get("input_id") != state["input_id"]
            or not isinstance(resumed.get("input"), str)
        ):
            raise ToolError("RESUME_MISMATCH")
        return {
            "content": state["content"] + "\n补充：" + resumed["input"],
            "evidence": [],
            "blocked": "",
        }

    def selected_ticket(state: TaskState):
        tickets = state.get("tickets", [])
        explicit = [item for item in tickets if item["ticket_no"] in state["content"]]
        return explicit[0] if len(explicit) == 1 else tickets[0] if len(tickets) == 1 else None

    def next_after_retrieve(state: TaskState):
        plan = Plan.model_validate(state["plan"])
        if plan.requested_action == "query" or (plan.knowledge_query and not state.get("evidence")):
            return "finalize"
        if plan.requested_action == "add_note":
            if not state.get("tickets"):
                return "blocked"
            if selected_ticket(state) is None:
                return "input"
        return "action"

    async def blocked(state: TaskState):
        return {"blocked": "该订单没有可访问的工单，尚未添加备注。请先创建工单。"}

    async def allocate_action(state: TaskState):
        return {"action_id": str(uuid5(tools.command.run_id, "action:v1"))}

    async def prepare_action(state: TaskState):
        plan = Plan.model_validate(state["plan"])
        if plan.requested_action == "create_ticket":
            tool = "ticket.create"
            arguments = {
                "order_id": state["order"]["id"],
                "expected_version": state["order"]["version"],
                "title": f"订单 {state['order']['order_no']} 售后跟进",
                "description": state["content"][:4000],
                "category": "after_sales",
                "priority": "normal",
            }
        else:
            tool = "ticket.add_note"
            ticket = selected_ticket(state)
            match = re.search(r"备注(?:内容)?[：:]\s*(.+)", state["content"], re.S)
            arguments = {
                "ticket_id": ticket["id"],
                "expected_version": ticket["version"],
                "content": (match.group(1) if match else state["content"])[:4000],
            }
        approval = await tools.prepare_action(
            state["action_id"], tool, arguments, [item["id"] for item in state.get("evidence", [])]
        )
        return {"approval": approval}

    async def wait_approval(state: TaskState):
        resumed = interrupt({"kind": "approval", "action_id": state["action_id"]})
        if (
            not isinstance(resumed, dict)
            or resumed.get("decision") != "approved"
            or resumed.get("action_id") != state["action_id"]
        ):
            raise ToolError("RESUME_MISMATCH")
        return {}

    async def execute_action(state: TaskState):
        return {"receipt": await tools.execute_action(state["action_id"])}

    builder = StateGraph(TaskState)
    builder.add_node("understand_and_route", route)
    builder.add_node("read_business_tools", read)
    builder.add_node("finalize", finalize)
    builder.add_node("retrieve_knowledge", retrieve)
    builder.add_node("allocate_input", allocate_input)
    builder.add_node("wait_input", wait_input)
    builder.add_node("blocked", blocked)
    builder.add_node("allocate_action", allocate_action)
    builder.add_node("prepare_action", prepare_action)
    builder.add_node("wait_approval", wait_approval)
    builder.add_node("execute_action", execute_action)
    builder.add_edge(START, "understand_and_route")
    builder.add_conditional_edges(
        "understand_and_route",
        lambda state: (
            "read"
            if state["plan"]["order_no"]
            else "answer"
            if state["plan"].get("knowledge_query")
            else "input"
        ),
        {"read": "read_business_tools", "answer": "retrieve_knowledge", "input": "allocate_input"},
    )
    builder.add_edge("read_business_tools", "retrieve_knowledge")
    builder.add_conditional_edges(
        "retrieve_knowledge",
        next_after_retrieve,
        {
            "finalize": "finalize",
            "blocked": "blocked",
            "input": "allocate_input",
            "action": "allocate_action",
        },
    )
    builder.add_edge("blocked", "finalize")
    builder.add_edge("allocate_input", "wait_input")
    builder.add_edge("wait_input", "understand_and_route")
    builder.add_edge("allocate_action", "prepare_action")
    builder.add_edge("prepare_action", "wait_approval")
    builder.add_edge("wait_approval", "execute_action")
    builder.add_edge("execute_action", "finalize")
    builder.add_edge("finalize", END)
    return builder.compile(checkpointer=checkpointer)

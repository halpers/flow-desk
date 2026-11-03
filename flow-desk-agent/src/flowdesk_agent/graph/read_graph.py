"""Deterministic M2 graph. Trusted authority lives in the client, outside checkpoint state."""

import re
from typing import TypedDict

from langgraph.graph import END, START, StateGraph

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


class FakeModel:
    async def plan(self, content: str) -> Plan:
        match = ORDER_PATTERN.search(content)
        action = "query"
        if "备注" in content:
            action = "add_note"
        elif any(word in content for word in ("创建工单", "建立工单", "开工单")):
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

    async def answer(self, state: TaskState) -> str:
        plan = Plan.model_validate(state["plan"])
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
        if plan.requested_action != "query":
            result += "\n当前仅提供查询，尚未创建工单或添加备注。"
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
        return {"plan": plan.model_dump()}

    async def read(state: TaskState):
        plan = Plan.model_validate(state["plan"])
        arguments = {"order_no": plan.order_no}
        order = await tools.call(ToolName.ORDER_LOOKUP, arguments)
        logistics = await tools.call(ToolName.LOGISTICS_TRACK, arguments)
        return {"order": order["order"], "logistics": logistics.get("logistics", [])}

    async def finalize(state: TaskState):
        context = await tools.authorize()
        plan = Plan.model_validate(state["plan"])
        output = (
            Answer(content="当前授权知识库中没有足够依据，请联系主管核实。", evidence_ids=[])
            if plan.knowledge_query and not state.get("evidence")
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
        await tools.guard()
        await tools.callbacks.emit(
            tools.command,
            "message.completed",
            {
                "message_id": str(context.message_id),
                "content": answer.content,
                "citations": [{"id": identifier} for identifier in answer.evidence_ids],
                "action_receipts": [],
            },
        )
        terminal = await tools.callbacks.emit(tools.command, "run.succeeded", {})
        await tools.callbacks.wait(terminal)
        return {"answer": answer.content}

    async def retrieve(state: TaskState):
        query = Plan.model_validate(state["plan"]).knowledge_query
        return {"evidence": await retriever.search(query) if query and retriever else []}

    builder = StateGraph(TaskState)
    builder.add_node("understand_and_route", route)
    builder.add_node("read_business_tools", read)
    builder.add_node("finalize", finalize)
    builder.add_node("retrieve_knowledge", retrieve)
    builder.add_edge(START, "understand_and_route")
    builder.add_conditional_edges(
        "understand_and_route",
        lambda state: "read" if state["plan"]["order_no"] else "answer",
        {"read": "read_business_tools", "answer": "retrieve_knowledge"},
    )
    builder.add_edge("read_business_tools", "retrieve_knowledge")
    builder.add_edge("retrieve_knowledge", "finalize")
    builder.add_edge("finalize", END)
    return builder.compile(checkpointer=checkpointer)

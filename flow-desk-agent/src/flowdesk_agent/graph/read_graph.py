"""Deterministic M2 graph. Trusted authority lives in the client, outside checkpoint state."""

import re
from typing import Literal, TypedDict

from langgraph.graph import END, START, StateGraph
from pydantic import BaseModel, ConfigDict

from flowdesk_agent.runtime_gen import ToolName
from flowdesk_agent.tools.go_client import GoTools

ORDER_PATTERN = re.compile(r"(?<![A-Za-z0-9])FD\d{12}(?![A-Za-z0-9])", re.IGNORECASE)


class Plan(BaseModel):
    model_config = ConfigDict(extra="forbid", frozen=True)
    order_no: str | None
    requested_action: Literal["query", "create_ticket", "add_note"]


class TaskState(TypedDict, total=False):
    content: str
    plan: dict
    order: dict
    logistics: list[dict]
    answer: str


class FakeModel:
    async def plan(self, content: str) -> Plan:
        match = ORDER_PATTERN.search(content)
        action = "query"
        if "备注" in content:
            action = "add_note"
        elif any(word in content for word in ("创建工单", "建立工单", "开工单")):
            action = "create_ticket"
        return Plan(order_no=match.group(0).upper() if match else None, requested_action=action)

    async def answer(self, state: TaskState) -> str:
        plan = Plan.model_validate(state["plan"])
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


def build_graph(tools: GoTools, checkpointer, model=None):
    model = model or FakeModel()

    async def route(state: TaskState):
        await tools.authorize()
        plan = Plan.model_validate(await model.plan(state["content"]))
        return {"plan": plan.model_dump()}

    async def read(state: TaskState):
        plan = Plan.model_validate(state["plan"])
        arguments = {"order_no": plan.order_no}
        order = await tools.call(ToolName.ORDER_LOOKUP, arguments)
        logistics = await tools.call(ToolName.LOGISTICS_TRACK, arguments)
        return {"order": order["order"], "logistics": logistics.get("logistics", [])}

    async def finalize(state: TaskState):
        context = await tools.authorize()
        answer = await model.answer(state)
        await tools.guard()
        await tools.callbacks.emit(
            tools.command,
            "message.completed",
            {
                "message_id": str(context.message_id),
                "content": answer,
                "citations": [],
                "action_receipts": [],
            },
        )
        terminal = await tools.callbacks.emit(tools.command, "run.succeeded", {})
        await tools.callbacks.wait(terminal)
        return {"answer": answer}

    builder = StateGraph(TaskState)
    builder.add_node("understand_and_route", route)
    builder.add_node("read_business_tools", read)
    builder.add_node("finalize", finalize)
    builder.add_edge(START, "understand_and_route")
    builder.add_conditional_edges(
        "understand_and_route",
        lambda state: "read" if state["plan"]["order_no"] else "answer",
        {"read": "read_business_tools", "answer": "finalize"},
    )
    builder.add_edge("read_business_tools", "finalize")
    builder.add_edge("finalize", END)
    return builder.compile(checkpointer=checkpointer)

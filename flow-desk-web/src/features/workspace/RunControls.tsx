import { useRef, useState } from "react";
import { Alert, Button, Input, Space, Tag } from "antd";
import { api } from "../../shared/api/client";
import { newId } from "../../shared/id";
import type { Run } from "../../shared/events/watch";
import { terminal } from "../../shared/events/watch";
import { TicketDetail } from "./Tickets";

const actionTitles: Record<string, string> = {
  "ticket.create": "确认创建工单",
  "ticket.add_note": "确认添加内部备注",
  "ticket.start_processing": "确认接手处理工单",
  "ticket.resolve": "确认办结工单",
};
const receiptLabels: Record<string, string> = {
  "ticket.create": "工单已创建",
  "ticket.add_note": "备注已保存",
  "ticket.start_processing": "工单已接手处理",
  "ticket.resolve": "工单已办结",
};
const statusLabels: Record<string, string> = {
  open: "待处理",
  in_progress: "处理中",
  resolved: "已办结",
  closed: "已关闭",
};

export function Receipt({ value }: { value: Record<string, unknown> }) {
  const result = value.result as Record<string, unknown> | undefined;
  const [open, setOpen] = useState(false);
  if (typeof value.business_object_id !== "string" || !result) return null;
  return (
    <div className="receipt-card">
      <Tag color="green">
        {receiptLabels[String(value.tool_name)] || "办理已完成"}
      </Tag>
      <Button type="link" onClick={() => setOpen(true)}>
        {String(result.ticket_no || "查看工单")}
      </Button>
      <small>已完成的业务结果会保留，即使任务取消或总结失败。</small>
      {open && (
        <TicketDetail
          id={value.business_object_id}
          onClose={() => setOpen(false)}
        />
      )}
    </div>
  );
}

export function RunControls({
  run,
  onUpdated,
}: {
  run: Run;
  onUpdated: (run: Run) => void;
}) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [input, setInput] = useState("");
  const pending = useRef<{ payload: string; key: string } | null>(null);
  async function submit(path: string, body: object) {
    if (busy) return;
    setBusy(true);
    setError("");
    const payload = JSON.stringify(body);
    const signature = path + payload;
    if (pending.current?.payload !== signature)
      pending.current = { payload: signature, key: newId() };
    try {
      const updated = await api<Run>(path, {
        method: "POST",
        headers: { "Idempotency-Key": pending.current.key },
        body: payload,
      });
      pending.current = null;
      setInput("");
      onUpdated(updated);
    } catch (cause) {
      setError((cause as Error).message);
      try {
        onUpdated(await api<Run>(`/runs/${run.id}`));
      } catch {
        /* Keep the current snapshot when offline. */
      }
    } finally {
      setBusy(false);
    }
  }
  const approval = run.approvals.find((item) => item.status === "pending");
  const requested = run.pending_inputs[0];
  const snapshot = approval?.display_snapshot;
  const fields = snapshot?.arguments as Record<string, unknown> | undefined;
  return (
    <div className="run-controls">
      {error && <Alert type="error" title={error} />}
      {run.status === "waiting_input" && requested && (
        <section className="action-card">
          <h3>需要补充信息</h3>
          <p>{requested.question}</p>
          <Input.TextArea
            aria-label="补充信息"
            value={input}
            maxLength={2000}
            onChange={(event) => setInput(event.target.value)}
          />
          <Button
            type="primary"
            loading={busy}
            disabled={!input.trim()}
            onClick={() =>
              submit(`/runs/${run.id}/inputs`, {
                input_id: requested.id,
                expected_version: run.version,
                content: input.trim(),
              })
            }
          >
            提交补充信息
          </Button>
        </section>
      )}
      {run.status === "waiting_approval" && approval && (
        <section className="action-card" aria-label="待确认操作">
          <h3>{actionTitles[String(snapshot?.tool_name)] || "确认操作"}</h3>
          <p>{String(snapshot?.order_no || snapshot?.ticket_no || "")}</p>
          {typeof snapshot?.to_status === "string" && (
            <p>
              状态：
              {statusLabels[String(snapshot.from_status)] ||
                String(snapshot.from_status)}{" "}
              → {statusLabels[snapshot.to_status] || snapshot.to_status}
              ；办理人：{String(snapshot.actor_display_name)}
            </p>
          )}
          {fields &&
            Object.entries(fields)
              .filter(([key]) =>
                [
                  "title",
                  "description",
                  "category",
                  "priority",
                  "content",
                  "reason",
                ].includes(key),
              )
              .map(([key, value]) => (
                <div key={key} className="action-field">
                  <strong>
                    {
                      (
                        {
                          title: "标题",
                          description: "问题描述",
                          category: "分类",
                          priority: "优先级",
                          content: "备注内容",
                          reason:
                            snapshot?.tool_name === "ticket.resolve"
                              ? "处理结论"
                              : "处理原因",
                        } as Record<string, string>
                      )[key]
                    }
                  </strong>
                  <span>
                    {key === "category"
                      ? (
                          {
                            after_sales: "售后",
                            logistics: "物流",
                            other: "其他",
                          } as Record<string, string>
                        )[String(value)] || String(value)
                      : key === "priority"
                        ? (
                            { normal: "普通", urgent: "紧急" } as Record<
                              string,
                              string
                            >
                          )[String(value)] || String(value)
                        : String(value)}
                  </span>
                </div>
              ))}
          <small>
            有效至 {new Date(approval.expires_at).toLocaleString("zh-CN")}
            ；需要修改时请拒绝后重新提交。
          </small>
          <Space>
            <Button
              type="primary"
              loading={busy}
              onClick={() =>
                submit(`/approvals/${approval.id}/decision`, {
                  expected_version: approval.version,
                  decision: "approve",
                  reason: "已核对操作内容",
                })
              }
            >
              确认办理
            </Button>
            <Button
              disabled={busy}
              onClick={() =>
                submit(`/approvals/${approval.id}/decision`, {
                  expected_version: approval.version,
                  decision: "reject",
                  reason: "用户拒绝",
                })
              }
            >
              拒绝办理
            </Button>
          </Space>
        </section>
      )}
      {!terminal(run.status) && (
        <Button
          danger
          disabled={busy}
          onClick={() =>
            submit(`/runs/${run.id}/cancel`, { expected_version: run.version })
          }
        >
          取消任务
        </Button>
      )}
      {run.action_receipts.map((receipt) => (
        <Receipt key={receipt.action_id} value={receipt} />
      ))}
    </div>
  );
}

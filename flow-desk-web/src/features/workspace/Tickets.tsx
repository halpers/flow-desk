import { useRef, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Alert, Button, Drawer, Input, Modal, Select, Space, Tag } from "antd";
import { api, type User } from "../../shared/api/client";
import { newId } from "../../shared/id";
import type { Models } from "../../shared/api/models";
import { TeamScope } from "../teams/Teams";

type Ticket = Models["TicketData"];
const labels: Record<string, string> = {
  open: "待处理",
  in_progress: "处理中",
  resolved: "已解决",
  closed: "已关闭",
};

export function TicketDetail({
  id,
  onClose,
  user,
}: {
  id: string;
  onClose: () => void;
  user?: User;
}) {
  const cache = useQueryClient();
  const [note, setNote] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const pending = useRef<{ body: string; key: string } | null>(null);
  const ticket = useQuery({
    queryKey: ["tickets", "detail", id],
    queryFn: ({ signal }) => api<Ticket>(`/tickets/${id}`, { signal }),
  });
  const timeline = useQuery({
    queryKey: ["tickets", "timeline", id],
    queryFn: ({ signal }) =>
      api<Models["TimelinePage"]>(`/tickets/${id}/events?limit=100`, {
        signal,
      }),
  });
  async function mutate(path: string, body: object, method = "POST") {
    setBusy(true);
    setError("");
    const payload = JSON.stringify(body);
    const signature = path + payload;
    if (pending.current?.body !== signature)
      pending.current = { body: signature, key: newId() };
    try {
      await api(`/tickets/${id}${path}`, {
        method,
        headers: { "Idempotency-Key": pending.current.key },
        body: payload,
      });
      pending.current = null;
      setNote("");
    } catch (cause) {
      setError((cause as Error).message);
    } finally {
      setBusy(false);
      void cache.invalidateQueries({ queryKey: ["tickets"] });
    }
  }
  const item = ticket.isError ? undefined : ticket.data;
  const allowed: Record<string, string[]> = {
    open: ["in_progress", "closed"],
    in_progress: ["resolved", "closed"],
    resolved: ["in_progress", "closed"],
  };
  return (
    <Modal
      open
      title={item?.ticket_no || "工单详情"}
      width={680}
      onCancel={onClose}
      footer={null}
    >
      {(error || ticket.error || timeline.error) && (
        <Alert
          type="error"
          title={error || (ticket.error || timeline.error)?.message}
        />
      )}
      {item && (
        <>
          <h3>{item.title}</h3>
          <Tag>{labels[item.status]}</Tag>
          <p className="message-content">{item.description}</p>
          {user?.permissions.includes("team:manage") && (
            <Space>
              <span>共享范围</span>
              <TeamScope
                value={item.team_id}
                disabled={busy}
                onChange={(team_id) =>
                  mutate(
                    "/team",
                    { team_id, expected_version: item.version },
                    "PATCH",
                  )
                }
              />
            </Space>
          )}
          {user?.permissions.includes("ticket:manage") && (
            <Space wrap>
              <Button
                disabled={busy}
                onClick={() =>
                  mutate(
                    "/assignment",
                    {
                      expected_version: item.version,
                      assignee_user_id: user.id,
                    },
                    "PATCH",
                  )
                }
              >
                指派给我
              </Button>
              <Select
                aria-label="更改工单状态"
                placeholder="更改状态"
                disabled={busy}
                value={undefined}
                options={(allowed[item.status] || []).map((value) => ({
                  value,
                  label: labels[value],
                }))}
                onChange={(status) =>
                  mutate(
                    "/status",
                    { expected_version: item.version, status },
                    "PATCH",
                  )
                }
              />
            </Space>
          )}
          <h4>处理记录</h4>
          <div className="ticket-timeline">
            {timeline.data?.items.map((event) => (
              <article key={event.id}>
                <small>
                  {new Date(event.created_at).toLocaleString("zh-CN")}
                </small>
                <p className="message-content">{event.summary}</p>
              </article>
            ))}
          </div>
          {item.status !== "closed" && (
            <>
              <Input.TextArea
                aria-label="内部备注"
                value={note}
                maxLength={4000}
                onChange={(event) => setNote(event.target.value)}
              />
              <Button
                disabled={!note.trim()}
                loading={busy}
                onClick={() =>
                  mutate("/notes", {
                    expected_version: item.version,
                    content: note.trim(),
                  })
                }
              >
                保存内部备注
              </Button>
            </>
          )}
        </>
      )}
    </Modal>
  );
}

export function Tickets({ user }: { user: User }) {
  const cache = useQueryClient();
  const [open, setOpen] = useState(false);
  const [selected, setSelected] = useState("");
  const [orderNo, setOrderNo] = useState("");
  const [title, setTitle] = useState("");
  const [description, setDescription] = useState("");
  const [cursor, setCursor] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const pending = useRef<{ body: string; key: string } | null>(null);
  const list = useQuery({
    queryKey: ["tickets", user.tenant_id, user.id, cursor],
    enabled: open,
    queryFn: ({ signal }) =>
      api<Models["TicketPage"]>(
        `/tickets?limit=30&cursor=${encodeURIComponent(cursor)}`,
        { signal },
      ),
  });
  async function create() {
    setBusy(true);
    setError("");
    try {
      const orders = await api<Models["OrderPage"]>(
        `/orders?limit=1&order_no=${encodeURIComponent(orderNo.trim())}`,
      );
      const order = orders.items[0];
      if (!order) throw new Error("未找到此订单");
      const body = JSON.stringify({
        order_id: order.id,
        expected_version: order.version,
        title: title.trim(),
        description: description.trim(),
        category: "after_sales",
        priority: "normal",
      });
      if (pending.current?.body !== body)
        pending.current = { body, key: newId() };
      const result = await api<Ticket>("/tickets", {
        method: "POST",
        headers: { "Idempotency-Key": pending.current.key },
        body,
      });
      pending.current = null;
      setTitle("");
      setDescription("");
      setSelected(result.id);
      void cache.invalidateQueries({ queryKey: ["tickets"] });
    } catch (cause) {
      setError((cause as Error).message);
    } finally {
      setBusy(false);
    }
  }
  return (
    <>
      <Button onClick={() => setOpen(true)}>工单台</Button>
      <Drawer
        open={open}
        onClose={() => setOpen(false)}
        title="工单台"
        size={720}
      >
        {(error || list.error) && (
          <Alert type="error" title={error || list.error?.message} />
        )}
        <section className="action-card">
          <h3>手工创建工单</h3>
          <Input
            aria-label="关联订单号"
            placeholder="完整订单号"
            value={orderNo}
            maxLength={50}
            onChange={(event) => setOrderNo(event.target.value)}
          />
          <Input
            aria-label="工单标题"
            placeholder="工单标题"
            value={title}
            maxLength={120}
            onChange={(event) => setTitle(event.target.value)}
          />
          <Input.TextArea
            aria-label="工单描述"
            placeholder="问题描述"
            value={description}
            maxLength={4000}
            onChange={(event) => setDescription(event.target.value)}
          />
          <Button
            type="primary"
            loading={busy}
            disabled={!orderNo.trim() || !title.trim() || !description.trim()}
            onClick={create}
          >
            创建工单
          </Button>
        </section>
        {list.data?.items.map((item) => (
          <article key={item.id} className="ticket-row">
            <div>
              <Tag>{labels[item.status]}</Tag>
              <strong>{item.title}</strong>
              <small>{item.ticket_no}</small>
            </div>
            <Button onClick={() => setSelected(item.id)}>查看工单</Button>
          </article>
        ))}
        <Space>
          {cursor && <Button onClick={() => setCursor("")}>返回第一页</Button>}
          {list.data?.has_more && (
            <Button onClick={() => setCursor(list.data!.next_cursor)}>
              下一页工单
            </Button>
          )}
        </Space>
      </Drawer>
      {selected && (
        <TicketDetail
          id={selected}
          onClose={() => setSelected("")}
          user={user}
        />
      )}
    </>
  );
}

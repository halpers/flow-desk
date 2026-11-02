import { useEffect, useRef, useState } from "react";
import {
  useInfiniteQuery,
  useQuery,
  useQueryClient,
} from "@tanstack/react-query";
import { Alert, Button, Empty, Input, Space, Spin, Tag } from "antd";
import { api, ApiError, type User } from "../../shared/api/client";
import type { Models as Schemas } from "../../shared/api/models";
import { terminal, watchRun, type Run } from "../../shared/events/watch";
import type { RunEvent } from "../../shared/events/sse";

type Conversation = Schemas["ConversationData"];
type Message = Schemas["MessageData"];
const statuses: Record<string, string> = {
  queued: "排队中",
  running: "查询中",
  succeeded: "已完成",
  failed: "处理失败",
  expired: "执行超时",
  cancelled: "已取消",
};
const progressLabel = (event: RunEvent) =>
  String(
    event.payload.summary ||
      (
        {
          "run.queued": "任务已排队",
          "run.started": "开始处理",
          "message.completed": "答复已保存",
          "run.succeeded": "查询完成",
          "run.failed": "处理失败",
          "run.expired": "执行超时",
        } as Record<string, string>
      )[event.type] ||
      event.type,
  );

export function Workspace({ user }: { user: User }) {
  const cache = useQueryClient();
  const scope = ["workspace", user.tenant_id, user.id];
  const [selected, setSelected] = useState("");
  const [runId, setRunId] = useState("");
  const [run, setRun] = useState<Run | null>(null);
  const [events, setEvents] = useState<RunEvent[]>([]);
  const [connection, setConnection] = useState("");
  const [draft, setDraft] = useState("");
  const [error, setError] = useState("");
  const [sending, setSending] = useState(false);
  const [creating, setCreating] = useState(false);
  const [messagesMore, setMessagesMore] = useState("");
  const [orderFilter, setOrderFilter] = useState("");
  const [orderSearch, setOrderSearch] = useState("");
  const [orderId, setOrderId] = useState("");
  const pending = useRef<{
    conversation: string;
    content: string;
    key: string;
    message: string;
  } | null>(null);
  const createKey = useRef<string | null>(null);
  const selectedRef = useRef(selected);
  selectedRef.current = selected;
  const conversations = useInfiniteQuery({
    queryKey: [...scope, "conversations"],
    initialPageParam: "",
    queryFn: ({ pageParam, signal }) =>
      api<Schemas["ConversationPage"]>(
        `/conversations?limit=30&cursor=${encodeURIComponent(pageParam)}`,
        { signal },
      ),
    getNextPageParam: (page) => (page.has_more ? page.next_cursor : undefined),
  });
  const conversationItems =
    conversations.data?.pages.flatMap((page) => page.items) || [];
  const conversation = useQuery({
    queryKey: [...scope, "conversation", selected],
    enabled: !!selected,
    queryFn: ({ signal }) =>
      api<Conversation>(`/conversations/${selected}`, { signal }),
  });
  const messages = useQuery({
    queryKey: [...scope, "messages", selected, messagesMore],
    enabled: !!selected,
    queryFn: ({ signal }) =>
      api<Schemas["MessagePage"]>(
        `/conversations/${selected}/messages?limit=100&cursor=${encodeURIComponent(messagesMore)}`,
        { signal },
      ),
  });
  const orders = useInfiniteQuery({
    queryKey: [...scope, "orders", orderSearch],
    initialPageParam: "",
    queryFn: ({ pageParam, signal }) =>
      api<Schemas["OrderPage"]>(
        `/orders?limit=10&order_no=${encodeURIComponent(orderSearch)}&cursor=${encodeURIComponent(pageParam)}`,
        { signal },
      ),
    getNextPageParam: (page) => (page.has_more ? page.next_cursor : undefined),
  });
  const logistics = useQuery({
    queryKey: [...scope, "logistics", orderId],
    enabled: !!orderId,
    queryFn: ({ signal }) =>
      api<Schemas["LogisticsPage"]>(`/orders/${orderId}/logistics`, { signal }),
  });

  const choose = (id: string) => {
    selectedRef.current = id;
    setSelected(id);
    setMessagesMore("");
    setRunId("");
    setRun(null);
    setEvents([]);
    setError("");
  };
  useEffect(() => {
    if (!selected && conversationItems.length)
      setSelected(conversationItems[0].id);
  }, [conversations.data]);
  useEffect(() => {
    if (conversation.data?.active_run_id)
      setRunId(conversation.data.active_run_id);
  }, [conversation.data]);
  useEffect(() => {
    if (!runId && messages.data?.items.length)
      setRunId(messages.data.items.at(-1)!.run_id);
  }, [messages.data, runId]);
  useEffect(() => {
    if (!runId) return;
    const controller = new AbortController();
    setEvents([]);
    setConnection("连接中");
    void watchRun(
      runId,
      controller.signal,
      (snapshot) => {
        if (
          controller.signal.aborted ||
          selectedRef.current !== selected ||
          snapshot.conversation_id !== selected
        )
          return;
        setRun(snapshot);
        cache.setQueriesData<Schemas["MessagePage"]>(
          { queryKey: [...scope, "messages", selected] },
          (old) =>
            old
              ? {
                  ...old,
                  items: old.items.map((message) =>
                    message.id === snapshot.current_message.id
                      ? snapshot.current_message
                      : message,
                  ),
                }
              : old,
        );
        if (terminal(snapshot.status)) {
          void cache.invalidateQueries({
            queryKey: [...scope, "conversation", selected],
          });
          void cache.invalidateQueries({
            queryKey: [...scope, "conversations"],
          });
        }
      },
      (event) => {
        if (!controller.signal.aborted)
          setEvents((old) =>
            old.some((item) => item.seq === event.seq) ? old : [...old, event],
          );
      },
      (state) => {
        if (!controller.signal.aborted) setConnection(state);
      },
    );
    return () => controller.abort();
  }, [runId, selected]);

  async function create() {
    setCreating(true);
    setError("");
    createKey.current ??= crypto.randomUUID();
    try {
      const result = await api<Conversation>("/conversations", {
        method: "POST",
        headers: { "Idempotency-Key": createKey.current },
        body: JSON.stringify({ title: "订单服务咨询" }),
      });
      createKey.current = null;
      choose(result.id);
      await cache.invalidateQueries({ queryKey: [...scope, "conversations"] });
    } catch (error) {
      setError((error as Error).message);
    } finally {
      setCreating(false);
    }
  }
  async function send() {
    if (!selected || !draft.trim() || sending) return;
    setSending(true);
    setError("");
    const content = draft.trim();
    if (
      !pending.current ||
      pending.current.content !== content ||
      pending.current.conversation !== selected
    )
      pending.current = {
        conversation: selected,
        content,
        key: crypto.randomUUID(),
        message: crypto.randomUUID(),
      };
    const request = pending.current;
    try {
      const result = await api<Run>(`/conversations/${selected}/messages`, {
        method: "POST",
        headers: { "Idempotency-Key": request.key },
        body: JSON.stringify({
          client_message_id: request.message,
          content,
          attachments: [],
        }),
      });
      pending.current = null;
      setDraft("");
      setRun(result);
      setRunId(result.id);
      await cache.invalidateQueries({
        queryKey: [...scope, "messages", selected],
      });
    } catch (error) {
      setError((error as Error).message);
      if (
        error instanceof ApiError &&
        error.code === "CONVERSATION_BUSY" &&
        typeof error.data?.active_run_id === "string"
      )
        setRunId(error.data.active_run_id);
    } finally {
      setSending(false);
    }
  }
  const active = !!run && !terminal(run.status);
  const items: Message[] = messages.data?.items || [];
  return (
    <div className="workspace">
      <aside className="panel conversations">
        <div className="panel-heading">
          <h2>服务会话</h2>
          <Button onClick={create} loading={creating}>
            新建会话
          </Button>
        </div>
        {conversations.isPending && <Spin />}
        {conversations.isError && (
          <Alert type="error" title="会话列表暂时不可用" />
        )}
        <nav aria-label="服务会话">
          {conversationItems.map((item) => (
            <button
              key={item.id}
              className={`conversation ${selected === item.id ? "selected" : ""}`}
              onClick={() => choose(item.id)}
            >
              <strong>{item.title}</strong>
              <small>
                {new Date(item.updated_at).toLocaleString("zh-CN")}
                {item.active_run_id ? " · 处理中" : ""}
              </small>
            </button>
          ))}
        </nav>
        {conversations.hasNextPage && (
          <Button onClick={() => conversations.fetchNextPage()}>
            加载更多会话
          </Button>
        )}
        <div className="workspace-note">
          当前工作空间内的订单与物流查询。
          <br />
          每次查询均保留进度和结果。
        </div>
      </aside>
      <section className="panel conversation-panel">
        <div className="panel-heading">
          <h2>{conversation.data?.title || "开始一次服务咨询"}</h2>
          {run && (
            <Tag color={run.status === "succeeded" ? "green" : "blue"}>
              {statuses[run.status] || run.status}
            </Tag>
          )}
        </div>
        <div className="messages" aria-live="polite">
          {!selected && <Empty description="新建会话，输入订单号开始查询" />}
          {selected && !messages.isPending && !items.length && (
            <div className="welcome">
              <div className="eyebrow">FLOWDESK ASSISTANT</div>
              <h3>订单服务，从这里开始</h3>
              <p>查询订单状态与最新物流，结果来自当前工作空间的业务记录。</p>
              <Button
                onClick={() => setDraft("查询 FD202611020002 的订单状态和物流")}
              >
                查询示例订单 →
              </Button>
            </div>
          )}
          {messages.isPending && <Spin />}
          {messages.isError && <Alert type="error" title="消息加载失败" />}
          {items.map((message) => (
            <article key={message.id} className={`message ${message.role}`}>
              <div className="message-author">
                {message.role === "user" ? "你" : "FlowDesk"}{" "}
                <small>
                  {new Date(message.created_at).toLocaleTimeString("zh-CN")}
                </small>
              </div>
              <div className="message-content">
                {message.content ||
                  (message.status === "aborted"
                    ? "本次查询未完成，请重试。"
                    : "正在查询，请稍候…")}
              </div>
              {message.status === "aborted" && <Tag color="red">查询中止</Tag>}
            </article>
          ))}
          {messages.data?.has_more && (
            <Button onClick={() => setMessagesMore(messages.data!.next_cursor)}>
              查看后续消息
            </Button>
          )}
          {run?.error_code && (
            <Alert type="warning" title={`本次任务未完成：${run.error_code}`} />
          )}
        </div>
        <div className="composer">
          {error && (
            <Alert
              type="error"
              title={error}
              closable
              onClose={() => setError("")}
            />
          )}
          <label htmlFor="message-input">服务问题</label>
          <textarea
            key={selected}
            className="message-input"
            id="message-input"
            value={draft}
            onChange={(event) => setDraft(event.target.value)}
            maxLength={8000}
            rows={3}
            placeholder="例如：查询 FD202611020002 的订单状态和物流"
            disabled={!selected}
          />
          <div className="composer-footer">
            <span>订单号以 FD 开头 · 当前支持查询</span>
            <Button
              type="primary"
              onClick={send}
              loading={sending}
              disabled={!selected || !draft.trim() || active}
            >
              发送查询
            </Button>
          </div>
        </div>
      </section>
      <aside className="workspace-right">
        <section className="panel">
          <div className="panel-heading">
            <h2>任务进度</h2>
            <Tag>{connection || "等待任务"}</Tag>
          </div>
          <ol className="progress" aria-label="任务进度">
            {events.map((event) => (
              <li key={event.seq}>
                <span>{progressLabel(event)}</span>
                <small>#{event.seq}</small>
              </li>
            ))}
          </ol>
          {!events.length && <p className="muted">发起查询后展示处理过程。</p>}
        </section>
        <section className="panel order-panel">
          <div className="panel-heading">
            <h2>订单速查</h2>
          </div>
          <Space.Compact style={{ width: "100%" }}>
            <Input
              aria-label="订单号筛选"
              value={orderFilter}
              onChange={(event) => setOrderFilter(event.target.value)}
              placeholder="订单号"
            />
            <Button onClick={() => setOrderSearch(orderFilter.trim())}>
              筛选
            </Button>
          </Space.Compact>
          {orders.isError && <Alert type="error" title="订单暂时不可用" />}
          <div className="order-list">
            {orders.data?.pages
              .flatMap((page) => page.items)
              .map((order) => (
                <button
                  className={`order ${orderId === order.id ? "selected" : ""}`}
                  key={order.id}
                  onClick={() => setOrderId(order.id)}
                >
                  <strong>{order.order_no}</strong>
                  <span>
                    {order.masked_customer_name} · {order.total_amount}{" "}
                    {order.currency}
                  </span>
                  <small>{order.status}</small>
                </button>
              ))}
          </div>
          {orders.hasNextPage && (
            <Button onClick={() => orders.fetchNextPage()}>更多订单</Button>
          )}
          {logistics.data && (
            <div className="logistics">
              <h3>物流记录</h3>
              {logistics.data.items.map((event) => (
                <p key={event.id}>
                  {event.description}
                  <small>
                    {new Date(event.source_updated_at).toLocaleString("zh-CN")}
                  </small>
                </p>
              ))}
              {!logistics.data.items.length && <p>暂无物流记录</p>}
            </div>
          )}
        </section>
      </aside>
    </div>
  );
}

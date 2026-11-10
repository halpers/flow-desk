import { useEffect, useRef, useState } from "react";
import {
  useInfiniteQuery,
  useQuery,
  useQueryClient,
} from "@tanstack/react-query";
import {
  Alert,
  Button,
  Descriptions,
  Drawer,
  Empty,
  Input,
  Modal,
  Select,
  Space,
  Spin,
  Table,
  Tag,
} from "antd";
import { api, type User } from "../../shared/api/client";
import type { Models } from "../../shared/api/models";
import { RunLifecycle } from "./RunLifecycle";
import { RunHistory } from "./RunHistory";
import { RunHistorySchedule } from "./RunHistorySchedule";
import { RunHistoryBatch } from "./RunHistoryBatch";
import { newId } from "../../shared/id";

type Task = Models["RunOperationPage"]["items"][number];
const statuses: Record<string, string> = {
  queued: "排队中",
  running: "处理中",
  waiting_input: "等待补充信息",
  waiting_approval: "等待确认",
  cancelling: "取消中",
  succeeded: "已完成",
  failed: "处理失败",
  expired: "执行超时",
  cancelled: "已取消",
};
const retryReasons: Record<string, string> = {
  RUN_HISTORY_FENCED: "历史任务执行身份已封存，请新建任务并重新核对当前授权",
  OWNER_REQUIRED: "仅任务所有者可重试",
  NOT_RETRYABLE_STATE: "仅失败或超时的任务可重试",
  RETRY_REQUIRES_REVIEW:
    "此任务曾准备业务动作或补充输入，请回原会话核对办理状态和回执",
  ALREADY_RETRIED: "已创建重试任务",
  RETRY_LIMIT: "已达到三次重试上限",
};
const time = (value: string) => new Date(value).toLocaleString();

export function Operations({
  user,
  onOpen,
}: {
  user: User;
  onOpen: (conversationId: string, runId: string) => void;
}) {
  const queryClient = useQueryClient();
  const [accessError, setAccessError] = useState("");
  const [open, setOpen] = useState(false);
  const [scope, setScope] = useState("own");
  const [status, setStatus] = useState("");
  const [search, setSearch] = useState("");
  const [runId, setRunId] = useState("");
  const [selected, setSelected] = useState<Task | null>(null);
  const [confirm, setConfirm] = useState<Task | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [historySelection, setHistorySelection] = useState<string[]>([]);
  useEffect(() => {
    setHistorySelection([]);
  }, [scope, status, runId, open, user.tenant_id, user.id]);
  const pending = useRef<{ id: string; version: number; key: string } | null>(
    null,
  );
  const tasks = useInfiniteQuery({
    queryKey: ["operations", user.tenant_id, user.id, scope, status, runId],
    enabled: open,
    initialPageParam: "",
    queryFn: ({ pageParam, signal }) =>
      api<Models["RunOperationPage"]>(
        `/runs?limit=20&scope=${scope}&status=${status}&run_id=${encodeURIComponent(runId)}&cursor=${encodeURIComponent(pageParam)}`,
        { signal },
      ),
    getNextPageParam: (page) => (page.has_more ? page.next_cursor : undefined),
  });
  const trace = useQuery({
    queryKey: ["run-trace", user.tenant_id, user.id, selected?.id],
    enabled: open && !!selected,
    queryFn: ({ signal }) =>
      api<Models["RunTraceData"]>(`/runs/${selected!.id}/trace`, { signal }),
    retry: false,
  });
  const current = trace.data?.run || selected;
  const observation = trace.data?.observation;
  const usage = observation?.usage;
  const navigate = (task: Task, id = task.id) => {
    setSelected(null);
    setOpen(false);
    onOpen(task.conversation_id, id);
  };
  const locate = (id: string) => {
    setSelected(null);
    setSearch(id);
    setRunId(id);
    setStatus("");
  };
  async function retry() {
    if (!confirm || busy) return;
    const request =
      pending.current?.id === confirm.id &&
      pending.current.version === confirm.version
        ? pending.current
        : { id: confirm.id, version: confirm.version, key: newId() };
    pending.current = request;
    setBusy(true);
    setError("");
    try {
      const child = await api<Models["RunData"]>(`/runs/${request.id}/retry`, {
        method: "POST",
        headers: { "Idempotency-Key": request.key },
        body: JSON.stringify({ expected_version: request.version }),
      });
      pending.current = null;
      setConfirm(null);
      navigate(confirm, child.id);
    } catch (failure) {
      setError(
        failure instanceof Error
          ? failure.message
          : "重试请求失败，请再次获取结果",
      );
    } finally {
      setBusy(false);
    }
  }
  return (
    <>
      <Button onClick={() => setOpen(true)}>任务记录</Button>
      <Drawer
        title="任务记录"
        open={open}
        onClose={() => {
          setOpen(false);
          setSelected(null);
        }}
        size="large"
        destroyOnHidden
      >
        <Space orientation="vertical" style={{ width: "100%" }} size="middle">
          <Space wrap>
            <Select
              aria-label="任务范围"
              value={scope}
              onChange={setScope}
              options={[
                { value: "own", label: "我的任务" },
                ...(user.permissions.includes("run:observe")
                  ? [{ value: "tenant", label: "租户任务" }]
                  : []),
              ]}
            />
            <Select
              aria-label="任务状态筛选"
              style={{ width: 160 }}
              value={status}
              onChange={setStatus}
              options={[
                { value: "", label: "全部状态" },
                ...Object.entries(statuses).map(([value, label]) => ({
                  value,
                  label,
                })),
              ]}
            />
            <Button
              onClick={() => {
                setAccessError("");
                void tasks.refetch();
              }}
              loading={tasks.isFetching}
            >
              刷新任务
            </Button>
          </Space>
          {user.role === "admin" &&
            user.permissions.includes("run:observe") &&
            !accessError && (
              <Space>
                <RunHistorySchedule
                  key={`schedule:${user.tenant_id}:${user.id}`}
                  onDenied={(message) => {
                    setHistorySelection([]);
                    setSelected(null);
                    setConfirm(null);
                    setAccessError(message);
                    queryClient.removeQueries({
                      queryKey: ["run-trace", user.tenant_id, user.id],
                    });
                    queryClient.removeQueries({
                      queryKey: ["operations", user.tenant_id, user.id],
                    });
                  }}
                />
                <span>已选择 {historySelection.length} / 10 项</span>
                <RunHistoryBatch
                  key={`${user.tenant_id}:${user.id}`}
                  selectedIds={historySelection}
                  onReset={() => {
                    setHistorySelection([]);
                    void queryClient.invalidateQueries({
                      queryKey: ["operations", user.tenant_id, user.id],
                    });
                  }}
                  onDenied={(message) => {
                    setHistorySelection([]);
                    setSelected(null);
                    setConfirm(null);
                    setAccessError(message);
                    queryClient.removeQueries({
                      queryKey: ["run-trace", user.tenant_id, user.id],
                    });
                    queryClient.removeQueries({
                      queryKey: ["operations", user.tenant_id, user.id],
                    });
                  }}
                />
              </Space>
            )}
          <Input.Search
            aria-label="按任务 ID 定位"
            placeholder="完整任务 ID"
            value={search}
            onChange={(event) => setSearch(event.target.value)}
            onSearch={(value) => setRunId(value.trim())}
            enterButton="定位任务"
            allowClear
          />
          {accessError && <Alert type="error" title={accessError} />}
          {tasks.isError && (
            <Alert
              type="error"
              title="任务列表加载失败"
              description={tasks.error.message}
            />
          )}
          {!accessError && (
            <Table<Task>
              rowSelection={
                user.role === "admin" &&
                user.permissions.includes("run:observe")
                  ? {
                      selectedRowKeys: historySelection,
                      hideSelectAll: true,
                      onChange: (keys) => setHistorySelection(keys.map(String)),
                      getCheckboxProps: (task) => ({
                        "aria-label": `选择归档任务 ${task.id}`,
                        disabled:
                          tasks.isFetching ||
                          (historySelection.length >= 10 &&
                            !historySelection.includes(task.id)),
                      }),
                    }
                  : undefined
              }
              rowKey="id"
              loading={tasks.isPending}
              pagination={false}
              scroll={{ x: 650 }}
              dataSource={tasks.data?.pages.flatMap((page) => page.items) || []}
              columns={[
                {
                  title: "任务",
                  key: "id",
                  render: (_, task) => (
                    <Button
                      type="link"
                      onClick={() => setSelected(task)}
                      aria-label={`查看任务 ${task.id}`}
                    >
                      {task.id.slice(0, 8)}
                    </Button>
                  ),
                },
                {
                  title: "状态",
                  key: "status",
                  render: (_, task) => (
                    <Tag>{statuses[task.status] || task.status}</Tag>
                  ),
                },
                { title: "创建时间", dataIndex: "created_at", render: time },
                {
                  title: "来源",
                  key: "retry",
                  render: (_, task) =>
                    task.retry_of_run_id
                      ? `重试 ${task.retry_depth}/3`
                      : "原任务",
                },
                {
                  title: "所有者",
                  key: "actor",
                  render: (_, task) =>
                    task.actor_user_id === user.id
                      ? "我"
                      : task.actor_user_id.slice(0, 8),
                },
              ]}
            />
          )}
          {!accessError && tasks.hasNextPage && (
            <Button
              loading={tasks.isFetchingNextPage}
              onClick={() => void tasks.fetchNextPage()}
            >
              加载更多任务
            </Button>
          )}
        </Space>
        <Drawer
          title="任务详情"
          open={!!selected}
          onClose={() => setSelected(null)}
          size="large"
          destroyOnHidden
        >
          {current && (
            <Space
              orientation="vertical"
              size="middle"
              style={{ width: "100%" }}
            >
              <Descriptions
                column={1}
                size="small"
                items={[
                  { key: "id", label: "任务 ID", children: current.id },
                  {
                    key: "status",
                    label: "状态",
                    children: statuses[current.status] || current.status,
                  },
                  {
                    key: "owner",
                    label: "所有者",
                    children:
                      current.actor_user_id === user.id
                        ? "我"
                        : current.actor_user_id,
                  },
                  {
                    key: "error",
                    label: "错误分类",
                    children: current.error_code || "无",
                  },
                  {
                    key: "created",
                    label: "创建时间",
                    children: time(current.created_at),
                  },
                  {
                    key: "updated",
                    label: "更新时间",
                    children: time(current.updated_at),
                  },
                  {
                    key: "parent",
                    label: "重试来源",
                    children: current.retry_of_run_id ? (
                      <Button
                        type="link"
                        onClick={() => locate(current.retry_of_run_id)}
                      >
                        {current.retry_of_run_id}
                      </Button>
                    ) : (
                      "原任务"
                    ),
                  },
                  {
                    key: "child",
                    label: "后续任务",
                    children: current.retried_by_run_id ? (
                      <Button
                        type="link"
                        onClick={() => locate(current.retried_by_run_id)}
                      >
                        {current.retried_by_run_id}
                      </Button>
                    ) : (
                      "无"
                    ),
                  },
                ]}
              />
              <Space wrap>
                <RunLifecycle
                  key={`${user.tenant_id}:${user.id}:${current.id}`}
                  runId={current.id}
                  onDenied={(message) => {
                    setSelected(null);
                    setConfirm(null);
                    setAccessError(message);
                    queryClient.removeQueries({
                      queryKey: ["run-trace", user.tenant_id, user.id],
                    });
                    queryClient.removeQueries({
                      queryKey: ["operations", user.tenant_id, user.id],
                    });
                  }}
                />
                <RunHistory
                  key={`${user.tenant_id}:${user.id}:${current.id}`}
                  runId={current.id}
                  version={current.version}
                  onDenied={(message) => {
                    setSelected(null);
                    setConfirm(null);
                    setAccessError(message);
                    queryClient.removeQueries({
                      queryKey: ["run-trace", user.tenant_id, user.id],
                    });
                    queryClient.removeQueries({
                      queryKey: ["operations", user.tenant_id, user.id],
                    });
                  }}
                />
                <Button
                  onClick={() => void trace.refetch()}
                  loading={trace.isFetching}
                >
                  刷新执行记录
                </Button>
                {current.actor_user_id === user.id && (
                  <Button onClick={() => navigate(current)}>打开会话</Button>
                )}
                <Button
                  disabled={!current.can_retry}
                  onClick={() => {
                    setConfirm(current);
                    setError("");
                  }}
                >
                  重试任务
                </Button>
              </Space>
              {!current.can_retry && (
                <Alert
                  type="info"
                  title={
                    retryReasons[current.retry_block_reason] || "当前不可重试"
                  }
                />
              )}
              {trace.isPending && <Spin />}
              {trace.isError && (
                <Alert
                  type="error"
                  title="执行记录加载失败"
                  description={trace.error.message}
                />
              )}
              {observation && !observation.available && (
                <Empty description="暂无执行记录：任务尚未启动，或是未登记观测的历史任务" />
              )}
              {observation?.available && usage && (
                <>
                  {observation.truncated && (
                    <Alert
                      type="info"
                      title="记录较多，仅显示前 100 条；下列用量为全部记录的累计值"
                    />
                  )}
                  <Descriptions
                    title="用量"
                    column={1}
                    size="small"
                    items={[
                      {
                        key: "tools",
                        label: "业务工具调用",
                        children: current.tool_call_count,
                      },
                      {
                        key: "model",
                        label: "模型尝试",
                        children:
                          usage.model_attempts === 0
                            ? "0（无模型调用记录）"
                            : usage.model_attempts,
                      },
                      {
                        key: "tokens",
                        label: "模型输入 / 输出 tokens",
                        children: usage.model_tokens_known
                          ? `${usage.input_tokens} / ${usage.output_tokens}`
                          : `未知（已记录 ${usage.input_tokens} / ${usage.output_tokens}）`,
                      },
                      {
                        key: "retrieval",
                        label: "检索次数",
                        children: usage.retrieval_attempts,
                      },
                      {
                        key: "embedding",
                        label: "查询向量调用 / 输入 tokens",
                        children: `${usage.embedding_attempts} / ${usage.embedding_tokens_known ? usage.embedding_input_tokens : "未知"}`,
                      },
                      {
                        key: "cost",
                        label: "费用",
                        children: "未知（未配置价格基线）",
                      },
                    ]}
                  />
                  <h3>节点执行记录</h3>
                  <Table
                    rowKey={(_, index) => String(index)}
                    pagination={false}
                    size="small"
                    dataSource={observation.nodes}
                    columns={[
                      { title: "节点", dataIndex: "node" },
                      { title: "状态", dataIndex: "status" },
                      { title: "耗时 ms", dataIndex: "duration_ms" },
                      { title: "执行代次", dataIndex: "generation" },
                    ]}
                  />
                  <h3>模型调用记录</h3>
                  <Table
                    rowKey={(_, index) => String(index)}
                    pagination={false}
                    size="small"
                    dataSource={observation.models}
                    columns={[
                      { title: "状态", dataIndex: "status" },
                      { title: "错误分类", dataIndex: "error_code" },
                      {
                        title: "耗时 ms",
                        key: "duration",
                        render: (_, model) =>
                          model.duration_known ? model.duration_ms : "未知",
                      },
                      {
                        title: "输入 / 输出 tokens",
                        key: "tokens",
                        render: (_, model) =>
                          model.tokens_known
                            ? `${model.input_tokens} / ${model.output_tokens}`
                            : "未知",
                      },
                    ]}
                  />
                  <h3>工具成功缓存记录</h3>
                  <Table
                    rowKey="tool"
                    pagination={false}
                    size="small"
                    dataSource={observation.tools}
                    columns={[
                      { title: "工具", dataIndex: "tool" },
                      { title: "记录数", dataIndex: "completed_count" },
                    ]}
                  />
                </>
              )}
            </Space>
          )}
          <Modal
            title="确认重试任务"
            open={!!confirm}
            onCancel={() => {
              if (!busy) setConfirm(null);
            }}
            onOk={() => void retry()}
            confirmLoading={busy}
            okText="确认重试"
            cancelText="返回"
            closable={!busy}
            maskClosable={!busy}
          >
            <p>
              这将创建一个新任务，重新查询当前业务数据和政策。原任务与回执会保留；需要办理时仍须重新确认。
            </p>
            {error && (
              <Alert
                type="error"
                title={error}
                description="若请求响应丢失，再次确认会获取同一个重试任务。"
              />
            )}
          </Modal>
        </Drawer>
      </Drawer>
    </>
  );
}

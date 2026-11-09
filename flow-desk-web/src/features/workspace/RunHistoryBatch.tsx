import { useEffect, useRef, useState } from "react";
import {
  Alert,
  Button,
  Checkbox,
  Drawer,
  Input,
  Space,
  Spin,
  Table,
  Tag,
} from "antd";
import { api, ApiError } from "../../shared/api/client";
import type { Models } from "../../shared/api/models";
import { newId } from "../../shared/id";
import { protectionLabels } from "./RunLifecycle";

type Preview =
  | Models["RunHistoryBatchPreviewData"]
  | Models["RunHistoryRestoreBatchPreviewData"];
type PreviewItem = Preview["items"][number];
type Target = "archived" | "live";
type Batch = Models["RunHistoryBatchData"];
type Page = Models["RunHistoryBatchPage"];
type Pending = {
  key: string;
  path: string;
  body: {
    confirm: boolean;
    items: {
      run_id: string;
      expected_version: number;
      expected_generation: number;
    }[];
  };
};
const reasons: Record<string, string> = {
  ...protectionLabels,
  RUN_HISTORY_PENDING: "已有待核对维护操作",
  RUN_HISTORY_ARCHIVED: "已经归档",
  RUN_HISTORY_NOT_ARCHIVED: "当前尚未归档，不能批量恢复",
  RUN_HISTORY_PROTECTED: "保留或恢复条件仍受保护",
  RUN_HISTORY_LIMIT: "超过单任务维护上限",
};

export function RunHistoryBatch(props: {
  selectedIds: string[];
  onDenied: (message: string) => void;
  onReset: () => void;
}) {
  const [target, setTarget] = useState<Target | null>(null);
  return (
    <>
      <Button onClick={() => setTarget("archived")}>批量归档与批次记录</Button>
      <Button onClick={() => setTarget("live")}>批量恢复与批次记录</Button>
      {target && (
        <BatchPanel
          {...props}
          target={target}
          onClose={() => setTarget(null)}
        />
      )}
    </>
  );
}

function BatchPanel({
  target,
  selectedIds,
  onDenied,
  onReset,
  onClose,
}: {
  selectedIds: string[];
  onDenied: (message: string) => void;
  onReset: () => void;
  onClose: () => void;
  target: Target;
}) {
  const restore = target === "live";
  const action = restore ? "恢复" : "归档";
  const createPath = restore
    ? "/run-history-restore-batches"
    : "/run-history-batches";
  const selection = useRef([...selectedIds].sort());
  const active = useRef<AbortController | null>(null);
  const pending = useRef<Pending | null>(null);
  const [preview, setPreview] = useState<Preview | null>(null);
  const [batch, setBatch] = useState<Batch | null>(null);
  const [page, setPage] = useState<Page | null>(null);
  const [cursor, setCursor] = useState("");
  const [batchId, setBatchId] = useState("");
  const [confirmed, setConfirmed] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [uncertain, setUncertain] = useState(false);
  const callbacks = useRef({ onDenied, onReset });
  callbacks.current = { onDenied, onReset };

  async function perform(work: (signal: AbortSignal) => Promise<void>) {
    active.current?.abort();
    const controller = new AbortController();
    active.current = controller;
    setBusy(true);
    setError("");
    try {
      await work(controller.signal);
    } catch (reason) {
      if (controller.signal.aborted) return;
      if (
        reason instanceof ApiError &&
        [401, 403, 404].includes(reason.status)
      ) {
        pending.current = null;
        setPreview(null);
        setBatch(null);
        setPage(null);
        setConfirmed(false);
        callbacks.current.onDenied("批次访问权限已变化，请刷新任务列表。");
        onClose();
      } else {
        setError(
          reason instanceof ApiError && reason.status === 409
            ? "任务状态或保护条件已变化。已登记的批次可继续核对；新批次请刷新任务后重新预览。"
            : "本次结果尚未确认。请读取最近批次，或使用原请求继续核对。",
        );
      }
    } finally {
      if (!controller.signal.aborted) setBusy(false);
    }
  }
  async function loadPage(signal: AbortSignal, next: string) {
    setPage(null);
    setPreview(null);
    setBatch(null);
    setConfirmed(false);
    const result = await api<Page>(
      `/run-history-batches?limit=10&cursor=${encodeURIComponent(next)}`,
      { signal },
    );
    if (!signal.aborted) {
      setPage(result);
      setCursor(next);
    }
  }
  async function loadBatch(signal: AbortSignal, id: string) {
    setPage(null);
    setPreview(null);
    setBatch(null);
    setConfirmed(false);
    const result = await api<Batch>(
      `/run-history-batches/${encodeURIComponent(id)}`,
      { signal },
    );
    if (!signal.aborted) {
      setBatch(result);
      setBatchId(result.batch_id);
    }
  }
  useEffect(() => {
    void perform((signal) => loadPage(signal, ""));
    return () => active.current?.abort();
  }, []);

  async function previewSelection() {
    if (pending.current) return;
    setPreview(null);
    setBatch(null);
    setPage(null);
    setConfirmed(false);
    await perform(async (signal) => {
      const result = await api<Preview>(`${createPath}/preview`, {
        method: "POST",
        signal,
        body: JSON.stringify({ run_ids: selection.current }),
      });
      if (!signal.aborted) setPreview(result);
    });
  }
  async function progress(signal: AbortSignal, initial: Batch, one?: string) {
    let current = initial;
    for (const item of initial.items) {
      if (
        signal.aborted ||
        (one && item.operation_id !== one) ||
        item.status === "completed"
      )
        continue;
      const result = await api<Batch>(
        `/run-history-batches/${initial.batch_id}/operations/${item.operation_id}/resume`,
        { method: "POST", signal },
      );
      if (signal.aborted) return;
      current = result;
      setBatch(result);
    }
    if (!signal.aborted) setBatch(current);
  }
  async function create() {
    if (!pending.current && (!confirmed || !preview?.can_create)) return;
    const request = pending.current ?? {
      key: newId(),
      path: createPath,
      body: {
        confirm: true,
        items: preview!.items.map(
          ({ run_id, expected_version, expected_generation }) => ({
            run_id,
            expected_version,
            expected_generation,
          }),
        ),
      },
    };
    pending.current = request;
    setUncertain(true);
    await perform(async (signal) => {
      const receipt = await api<Models["RunHistoryBatchReceipt"]>(
        request.path,
        {
          method: "POST",
          signal,
          headers: { "Idempotency-Key": request.key },
          body: JSON.stringify(request.body),
        },
      );
      if (signal.aborted) return;
      pending.current = null;
      setUncertain(false);
      setPreview(null);
      setConfirmed(false);
      setBatchId(receipt.batch_id);
      callbacks.current.onReset();
      const current = await api<Batch>(
        `/run-history-batches/${receipt.batch_id}`,
        { signal },
      );
      if (signal.aborted) return;
      setBatch(current);
      await progress(signal, current);
    });
  }
  const columns = [
    {
      title: "任务 ID",
      dataIndex: "run_id",
      key: "run_id",
      render: (id: string) => (
        <span style={{ overflowWrap: "anywhere" }}>{id}</span>
      ),
    },
  ];
  return (
    <Drawer
      open
      title={`任务历史批量${action}`}
      size={880}
      onClose={() => {
        active.current?.abort();
        onClose();
      }}
    >
      <Space orientation="vertical" style={{ width: "100%" }} size="middle">
        <Alert
          type="info"
          title={`统一确认，逐项${action}`}
          description={
            restore
              ? "单批最多10项，仅恢复已归档任务的消息、事件和检查点存储。恢复不会重新执行任务，也不会解除永久执行封存。待核对项可从最近批次继续。"
              : "单批最多10项。已完成项保留结果，待核对项可以继续；刷新后从最近批次找回。消息、事件和检查点完整保留，任务执行身份永久封存。"
          }
        />
        <Space wrap>
          <Button
            disabled={busy || uncertain || selection.current.length === 0}
            onClick={() => void previewSelection()}
          >
            预览所选 {selection.current.length} 项
          </Button>
          <Button
            disabled={busy}
            onClick={() => void perform((signal) => loadPage(signal, ""))}
          >
            最近维护批次
          </Button>
        </Space>
        <Input.Search
          aria-label="按维护批次 ID 定位"
          placeholder="完整批次 ID"
          value={batchId}
          onChange={(event) => setBatchId(event.target.value)}
          disabled={busy}
          enterButton="读取批次"
          onSearch={(id) => {
            if (id.trim())
              void perform((signal) => loadBatch(signal, id.trim()));
          }}
        />
        {busy && <Spin />}
        {error && <Alert type="error" title={error} />}
        {uncertain && (
          <Alert
            type="warning"
            title="保留了原批次请求"
            description="原请求重试会核对同一批次。关闭或刷新后，也可从最近批次查看已经登记的结果。"
          />
        )}
        {uncertain && (
          <Button disabled={busy} onClick={() => void create()}>
            再次核对原批次请求
          </Button>
        )}
        {!busy && page && (
          <>
            <Table<Batch>
              rowKey="batch_id"
              pagination={false}
              dataSource={page.items}
              columns={[
                {
                  title: "批次",
                  key: "batch",
                  render: (_, item) => (
                    <Button
                      aria-label={`查看${item.target === "live" ? "恢复" : "归档"}批次 ${item.batch_id}`}
                      type="link"
                      onClick={() =>
                        void perform((signal) =>
                          loadBatch(signal, item.batch_id),
                        )
                      }
                    >
                      {item.batch_id.slice(0, 8)}
                    </Button>
                  ),
                },
                {
                  title: "方向",
                  key: "target",
                  render: (_, item) => (
                    <Tag>
                      {item.target === "live" ? "恢复存储" : "归档历史"}
                    </Tag>
                  ),
                },
                {
                  title: "登记时间",
                  dataIndex: "created_at",
                  render: (value: string) => new Date(value).toLocaleString(),
                },
                {
                  title: "已完成 / 总项",
                  key: "count",
                  render: (_, item) =>
                    `${item.completed_count} / ${item.items.length}`,
                },
                {
                  title: "状态",
                  dataIndex: "status",
                  render: (value: string) => (
                    <Tag>{value === "completed" ? "全部完成" : "待核对"}</Tag>
                  ),
                },
              ]}
            />
            <Space>
              <Button
                disabled={!cursor}
                onClick={() => void perform((signal) => loadPage(signal, ""))}
              >
                回到最新批次
              </Button>
              <Button
                disabled={!page.has_more}
                onClick={() =>
                  void perform((signal) => loadPage(signal, page.next_cursor))
                }
              >
                下一页批次
              </Button>
            </Space>
          </>
        )}
        {!busy && preview && (
          <>
            <Table<PreviewItem>
              rowKey="run_id"
              pagination={false}
              dataSource={preview.items}
              columns={[
                ...columns,
                {
                  title: "预览结果",
                  key: "eligible",
                  render: (_, item) =>
                    (
                      "can_restore" in item
                        ? item.can_restore
                        : item.can_archive
                    ) ? (
                      <Tag color="green">可{action}</Tag>
                    ) : (
                      item.protection_reasons
                        .map((code) => reasons[code] || "保护条件尚未确认")
                        .join("；")
                    ),
                },
              ]}
            />
            {!preview.can_create && (
              <Alert
                type="warning"
                title="含受保护任务，请关闭后调整选择并重新预览。"
              />
            )}
            <Checkbox
              checked={confirmed}
              disabled={!preview.can_create}
              onChange={(event) => setConfirmed(event.target.checked)}
            >
              {restore
                ? "我已核对全部所选任务，确认批量恢复存储且不重新执行任务"
                : "我已核对全部所选任务，确认批量归档并永久封存执行身份"}
            </Checkbox>
            <Button
              type="primary"
              disabled={!confirmed || !preview.can_create}
              onClick={() => void create()}
            >
              {uncertain ? "使用原批次请求重试" : `确认批量${action}`}
            </Button>
          </>
        )}
        {batch && (
          <>
            <p>
              批次 ID：{batch.batch_id} ·{" "}
              {batch.target === "live" ? "恢复存储" : "归档历史"}
            </p>
            <Alert
              type={batch.status === "completed" ? "success" : "warning"}
              title={`已完成 ${batch.completed_count} / ${batch.items.length} 项`}
              description="这里显示原维护操作的结果；后续再次归档或恢复后，旧批次仍保留原完成回执。当前存储状态请进入任务详情核对，恢复不解除执行封存。"
            />
            <Space>
              <Button
                disabled={busy}
                onClick={() =>
                  void perform((signal) => loadBatch(signal, batch.batch_id))
                }
              >
                刷新本批次
              </Button>
              <Button
                disabled={busy || batch.status === "completed"}
                onClick={() =>
                  void perform(async (signal) => {
                    const current = await api<Batch>(
                      `/run-history-batches/${batch.batch_id}`,
                      { signal },
                    );
                    if (!signal.aborted) {
                      setBatch(current);
                      await progress(signal, current);
                    }
                  })
                }
              >
                继续待核对项
              </Button>
            </Space>
            <Table
              rowKey="operation_id"
              pagination={false}
              dataSource={batch.items}
              columns={[
                ...columns,
                {
                  title: "状态",
                  dataIndex: "status",
                  render: (value: string) => (
                    <Tag>
                      {value === "completed"
                        ? `${batch.target === "live" ? "恢复" : "归档"}完成`
                        : "待核对"}
                    </Tag>
                  ),
                },
                {
                  title: "操作",
                  key: "operation",
                  render: (_, item) => (
                    <Button
                      disabled={busy || item.status === "completed"}
                      aria-label={`继续${item.target === "live" ? "恢复" : "归档"} ${item.run_id}`}
                      onClick={() =>
                        void perform((signal) =>
                          progress(signal, batch, item.operation_id),
                        )
                      }
                    >
                      继续本项
                    </Button>
                  ),
                },
              ]}
            />
          </>
        )}
      </Space>
    </Drawer>
  );
}

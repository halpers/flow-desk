import { useEffect, useRef, useState } from "react";
import {
  Alert,
  Button,
  Checkbox,
  Descriptions,
  Drawer,
  Space,
  Spin,
  Tag,
} from "antd";
import { api, ApiError } from "../../shared/api/client";
import type { Models } from "../../shared/api/models";
import { newId } from "../../shared/id";
import { protectionLabels } from "./RunLifecycle";
export function RunHistory(props: {
  runId: string;
  version: number;
  onDenied: (message: string) => void;
}) {
  const [open, setOpen] = useState(false);
  return (
    <>
      <Button onClick={() => setOpen(true)}>历史归档与恢复</Button>
      {open && <HistoryPanel {...props} onClose={() => setOpen(false)} />}
    </>
  );
}
function HistoryPanel({
  runId,
  version,
  onDenied,
  onClose,
}: {
  runId: string;
  version: number;
  onDenied: (message: string) => void;
  onClose: () => void;
}) {
  const [data, setData] = useState<Models["RunHistoryData"] | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");
  const [confirm, setConfirm] = useState(false);
  const [action, setAction] = useState<"archive" | "restore" | null>(null);
  const [request, setRequest] = useState<{
    target: string;
    body: {
      expected_version: number;
      expected_generation: number;
      confirm: boolean;
      idempotency_key: string;
    };
  } | null>(null);
  const [revision, setRevision] = useState(0);
  const mounted = useRef(true);
  const denial = useRef(onDenied);
  denial.current = onDenied;
  function rejected(reason: unknown) {
    setData(null);
    setConfirm(false);
    if (reason instanceof ApiError && [401, 403, 404].includes(reason.status)) {
      setRequest(null);
      setAction(null);
      denial.current("任务历史访问权限已变化，请刷新任务列表。");
    } else
      setError(
        reason instanceof ApiError && reason.status === 409
          ? "状态或保护条件已变化，请刷新后重新核对。"
          : "维护结果尚未确认，请刷新查看已有操作，或使用原请求重试。",
      );
  }
  useEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
    };
  }, []);
  useEffect(() => {
    const controller = new AbortController();
    setLoading(true);
    setData(null);
    setError("");
    setConfirm(false);
    void api<Models["RunHistoryData"]>(`/runs/${runId}/history`, {
      signal: controller.signal,
    })
      .then((result) => {
        if (!controller.signal.aborted) setData(result);
      })
      .catch((reason) => {
        if (!controller.signal.aborted) rejected(reason);
      })
      .finally(() => {
        if (!controller.signal.aborted) setLoading(false);
      });
    return () => controller.abort();
  }, [runId, revision]);
  async function execute(target: "archive" | "restore") {
    if (!data || !confirm) return;
    const chosen = request ?? {
      target,
      body: {
        expected_version: version,
        expected_generation: data.generation,
        confirm: true,
        idempotency_key: newId(),
      },
    };
    setRequest(chosen);
    setLoading(true);
    setError("");
    try {
      const { idempotency_key, ...body } = chosen.body;
      await api<Models["RunHistoryOperationData"]>(
        `/runs/${runId}/history/${chosen.target}`,
        {
          method: "POST",
          headers: { "Idempotency-Key": idempotency_key },
          body: JSON.stringify(body),
        },
      );
      if (mounted.current) {
        setRequest(null);
        setAction(null);
        setRevision((value) => value + 1);
      }
    } catch (reason) {
      if (mounted.current) rejected(reason);
    } finally {
      if (mounted.current) setLoading(false);
    }
  }
  async function resume() {
    if (!data?.operation.operation_id) return;
    setLoading(true);
    setData(null);
    setError("");
    try {
      await api(
        `/runs/${runId}/history/operations/${data.operation.operation_id}/resume`,
        { method: "POST" },
      );
      if (mounted.current) {
        setRequest(null);
        setAction(null);
        setRevision((value) => value + 1);
      }
    } catch (reason) {
      if (mounted.current) rejected(reason);
    } finally {
      if (mounted.current) setLoading(false);
    }
  }
  return (
    <Drawer open title="任务历史归档与恢复" onClose={onClose} size={680}>
      <Space orientation="vertical" style={{ width: "100%" }}>
        <Alert
          type="info"
          title="完整保留消息、事件和图检查点"
          description="归档将正文和检查点移入历史存储；会话、事件回放和任务详情继续可读。恢复只还原存储，任务执行身份永久封存。"
        />
        <Button
          aria-label="刷新历史存储状态"
          disabled={loading}
          loading={loading}
          onClick={() => {
            setRequest(null);
            setAction(null);
            setRevision((value) => value + 1);
          }}
        >
          刷新历史存储状态
        </Button>
        {loading && <Spin />}
        {error && <Alert type="error" title={error} />}
        {!loading && data && (
          <>
            <Descriptions
              column={1}
              items={[
                {
                  key: "state",
                  label: "存储状态",
                  children: (
                    <Tag>
                      {data.state === "archived"
                        ? "已归档"
                        : data.state === "pending"
                          ? "维护待确认"
                          : "在线"}
                    </Tag>
                  ),
                },
                {
                  key: "generation",
                  label: "维护代次",
                  children: data.generation,
                },
                { key: "id", label: "任务 ID", children: runId },
              ]}
            />
            {data.operation.status === "completed" && (
              <Descriptions
                column={1}
                items={[
                  {
                    key: "operation",
                    label: "完成回执",
                    children: data.operation.operation_id,
                  },
                  {
                    key: "counts",
                    label: "检查点 / 数据块 / 写入",
                    children: `${data.operation.checkpoint_count} / ${data.operation.checkpoint_blob_count} / ${data.operation.checkpoint_write_count}`,
                  },
                ]}
              />
            )}
            {data.protection_reasons.length > 0 && (
              <Alert
                type="warning"
                title="当前仍有保留保护"
                description={data.protection_reasons
                  .map(
                    (reason) => protectionLabels[reason] || "保留条件尚未确认",
                  )
                  .join("；")}
              />
            )}
            {data.state === "pending" && data.can_manage && (
              <>
                <p>
                  已有持久操作：{data.operation.operation_id}
                  。继续会核对原代次及回执，不创建第二份操作。
                </p>
                <Button onClick={() => void resume()}>继续已有维护操作</Button>
              </>
            )}
            {data.can_manage && data.state !== "pending" && (
              <Space>
                <Button
                  disabled={!data.can_archive || !!request}
                  onClick={() => {
                    setAction("archive");
                    setConfirm(false);
                  }}
                >
                  预览归档
                </Button>
                <Button
                  disabled={!data.can_restore || !!request}
                  onClick={() => {
                    setAction("restore");
                    setConfirm(false);
                  }}
                >
                  预览恢复
                </Button>
              </Space>
            )}
            {action && (
              <>
                <p>
                  {action === "archive"
                    ? "将本任务消息正文、事件内容及检查点移动到历史存储，保留所有身份和完整内容。"
                    : "将本任务历史内容及检查点还原至在线存储，不重新执行任务或开放旧命令。"}
                </p>
                <Checkbox
                  checked={confirm}
                  disabled={loading}
                  onChange={(event) => setConfirm(event.target.checked)}
                >
                  我已核对并确认{action === "archive" ? "归档" : "恢复"}
                  本任务历史
                </Checkbox>
                <Button
                  disabled={!confirm || loading}
                  onClick={() => void execute(action)}
                >
                  {request
                    ? "使用原请求重试"
                    : action === "archive"
                      ? "确认归档"
                      : "确认恢复"}
                </Button>
              </>
            )}
          </>
        )}
        {!loading && request && !data && (
          <>
            <p>保留了原请求身份，可用原键核对结果。</p>
            <Button onClick={() => setRevision((value) => value + 1)}>
              重新读取后核对原请求
            </Button>
          </>
        )}
      </Space>
    </Drawer>
  );
}

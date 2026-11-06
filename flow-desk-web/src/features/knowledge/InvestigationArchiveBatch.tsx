import { useEffect, useState } from "react";
import { Alert, Button, Checkbox, Modal, Space } from "antd";
import { api, ApiError } from "../../shared/api/client";
import type { Models } from "../../shared/api/models";
import { newId } from "../../shared/id";

export function InvestigationArchiveBatch({
  objectRef,
  revisions,
  expectedRevision,
  onCancel,
  onArchived,
  onDenied,
}: {
  objectRef: string;
  revisions: number[];
  expectedRevision: number;
  onCancel: () => void;
  onArchived: (receipt: Models["InvestigationArchiveBatchData"]) => void;
  onDenied: (message: string) => void;
}) {
  const [request] = useState(() => ({
    key: newId(),
    body: {
      expected_revision: expectedRevision,
      revisions: [...revisions].sort((left, right) => left - right),
      confirm: true,
    },
  }));
  const [preview, setPreview] = useState<
    Models["InvestigationArchivePreviewData"] | null
  >(null);
  const [loading, setLoading] = useState(true);
  const [confirmed, setConfirmed] = useState(false);
  const [busy, setBusy] = useState(false);
  const [attempted, setAttempted] = useState(false);
  const [error, setError] = useState("");
  useEffect(() => {
    const controller = new AbortController();
    void api<Models["InvestigationArchivePreviewData"]>(
      `/source-investigations/${objectRef}/history/archive-preview`,
      {
        method: "POST",
        signal: controller.signal,
        body: JSON.stringify({
          expected_revision: request.body.expected_revision,
          revisions: request.body.revisions,
        }),
      },
    )
      .then((result) => {
        if (controller.signal.aborted) return;
        if (
          result.object_ref !== objectRef ||
          result.current_revision !== request.body.expected_revision ||
          JSON.stringify(result.items.map((item) => item.revision)) !==
            JSON.stringify(request.body.revisions)
        )
          throw new Error("预览与选中修订不一致，请重新读取历史");
        setPreview(result);
      })
      .catch((reason: unknown) => {
        if (controller.signal.aborted) return;
        if (
          reason instanceof ApiError &&
          [401, 403, 404].includes(reason.status)
        )
          onDenied(reason.message);
        else setError((reason as Error).message);
      })
      .finally(() => {
        if (!controller.signal.aborted) setLoading(false);
      });
    return () => controller.abort();
  }, [objectRef, request, onDenied]);
  async function archive() {
    if (!preview || !confirmed || busy) return;
    setBusy(true);
    setAttempted(true);
    setError("");
    try {
      const receipt = await api<Models["InvestigationArchiveBatchData"]>(
        `/source-investigations/${objectRef}/history/archive-batch`,
        {
          method: "POST",
          headers: { "Idempotency-Key": request.key },
          body: JSON.stringify(request.body),
        },
      );
      onArchived(receipt);
    } catch (reason) {
      if (reason instanceof ApiError && [401, 403, 404].includes(reason.status))
        onDenied(reason.message);
      else setError((reason as Error).message);
    } finally {
      setBusy(false);
    }
  }
  const labels: Record<string, string> = {
    open: "待跟进",
    investigating: "调查中",
    closed: "已结束跟进",
  };
  return (
    <Modal
      title="确认批量归档调查历史"
      open
      width={740}
      maskClosable={false}
      keyboard={false}
      closable={!busy && !attempted}
      onCancel={() => {
        if (!busy && !attempted) onCancel();
      }}
      footer={
        <Space>
          <Button disabled={busy} onClick={onCancel}>
            {attempted ? "放弃重试并重新读取" : "取消"}
          </Button>
          <Button
            type="primary"
            disabled={loading || !preview || !confirmed || busy}
            loading={busy}
            onClick={() => void archive()}
          >
            {attempted ? "重试批量归档" : "确认批量归档"}
          </Button>
        </Space>
      }
    >
      <p>对象指纹：{objectRef}</p>
      <p>
        期望当前修订：{request.body.expected_revision}。本批选中{" "}
        {request.body.revisions.length}{" "}
        个旧修订，当前说明、状态和修订号保持不变。
      </p>
      {loading && <p>正在读取所选修订预览…</p>}
      {preview && (
        <>
          <p>
            已核对本批 {preview.items.length} 个修订（单批最多{" "}
            {preview.maximum_items} 个）。
          </p>
          {preview.items.map((item) => (
            <section
              className="investigation-batch-preview-item"
              key={item.revision}
            >
              <strong>修订 {item.revision}</strong>
              <p>
                状态：{labels[item.status] || item.status} · 操作人：
                {item.changed_by} · 原时间：{item.changed_at}
              </p>
              <p style={{ whiteSpace: "pre-wrap", overflowWrap: "anywhere" }}>
                {item.note}
              </p>
            </section>
          ))}
        </>
      )}
      <p>
        预览不锁定资格。提交时逐项重新检查调查已结束、最近3个修订及30天保留保护；任一项冲突整批回滚。完整历史仍可查看和恢复，不操作存储对象。
      </p>
      <Checkbox
        checked={confirmed}
        disabled={loading || !preview || busy}
        onChange={(event) => setConfirmed(event.target.checked)}
      >
        我确认将以上选中修订整批归档并保留完整历史
      </Checkbox>
      {attempted && (
        <p>
          响应未确认时使用原目标、原正文和原幂等键重试。重新读取不撤销已提交批次；冲突请重新核对，勿换键盲重试。
        </p>
      )}
      {error && <Alert type="error" title={error} />}
    </Modal>
  );
}

import { useState } from "react";
import { Alert, Button, Checkbox, Modal, Space } from "antd";
import { api, ApiError } from "../../shared/api/client";
import type { Models } from "../../shared/api/models";
import { newId } from "../../shared/id";

export function InvestigationArchive({
  objectRef,
  source,
  expectedRevision,
  onCancel,
  onArchived,
  onDenied,
}: {
  objectRef: string;
  source: Models["InvestigationHistoryPage"]["items"][number];
  expectedRevision: number;
  onCancel: () => void;
  onArchived: (receipt: Models["InvestigationArchiveData"]) => void;
  onDenied: (message: string) => void;
}) {
  const [request] = useState(() => ({
    key: newId(),
    body: { expected_revision: expectedRevision, confirm: true },
  }));
  const [confirmed, setConfirmed] = useState(false);
  const [busy, setBusy] = useState(false);
  const [attempted, setAttempted] = useState(false);
  const [error, setError] = useState("");
  async function archive() {
    if (!confirmed || busy) return;
    setBusy(true);
    setAttempted(true);
    setError("");
    try {
      const receipt = await api<Models["InvestigationArchiveData"]>(
        `/source-investigations/${objectRef}/history/${source.revision}/archive-metadata`,
        {
          method: "POST",
          headers: { "Idempotency-Key": request.key },
          body: JSON.stringify(request.body),
        },
      );
      onArchived(receipt);
    } catch (reason) {
      const message = (reason as Error).message;
      if (reason instanceof ApiError && [401, 403, 404].includes(reason.status))
        onDenied(message);
      else setError(message);
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
      title="确认归档调查修订"
      open
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
            disabled={!confirmed || busy}
            loading={busy}
            onClick={() => void archive()}
          >
            {attempted ? "重试归档修订" : "确认归档"}
          </Button>
        </Space>
      }
    >
      <p>
        归档历史修订 {source.revision}，状态「
        {labels[source.status] || source.status}」，要求当前调查仍为修订{" "}
        {request.body.expected_revision}。
      </p>
      <p>
        操作人：{source.changed_by} · 原时间：{source.changed_at}
      </p>
      <p style={{ whiteSpace: "pre-wrap", overflowWrap: "anywhere" }}>
        {source.note}
      </p>
      <p>
        仅已结束跟进的调查可整理历史，最近3个修订及未满30天的修订继续保留。归档后完整内容仍可查看和恢复，当前说明、状态和修订号不变，也不会操作存储对象。
      </p>
      <Checkbox
        checked={confirmed}
        disabled={busy}
        onChange={(event) => setConfirmed(event.target.checked)}
      >
        我确认归档此修订并保留完整历史
      </Checkbox>
      {attempted && (
        <p>
          响应未确认时使用原请求和原幂等键重试；重新读取不会撤销已提交归档。修订变化时请重新核对，勿换键盲重试。
        </p>
      )}
      {error && <Alert type="error" title={error} />}
    </Modal>
  );
}

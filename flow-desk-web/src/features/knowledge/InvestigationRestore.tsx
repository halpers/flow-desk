import { useState } from "react";
import { Alert, Button, Checkbox, Modal, Space } from "antd";
import { api, ApiError } from "../../shared/api/client";
import type { Models } from "../../shared/api/models";
import { newId } from "../../shared/id";

export function InvestigationRestore({
  objectRef,
  source,
  expectedRevision,
  onCancel,
  onRestored,
  onDenied,
}: {
  objectRef: string;
  source: Models["InvestigationHistoryPage"]["items"][number];
  expectedRevision: number;
  onCancel: () => void;
  onRestored: (receipt: Models["InvestigationRestoreData"]) => void;
  onDenied: (message: string) => void;
}) {
  // Freeze the key and body for the lifetime of this confirmation, including uncertain responses.
  const [request] = useState(() => ({
    key: newId(),
    body: { expected_revision: expectedRevision, confirm: true },
  }));
  const [confirmed, setConfirmed] = useState(false);
  const [busy, setBusy] = useState(false);
  const [attempted, setAttempted] = useState(false);
  const [error, setError] = useState("");
  async function restore() {
    if (!confirmed || busy) return;
    setBusy(true);
    setAttempted(true);
    setError("");
    try {
      const receipt = await api<Models["InvestigationRestoreData"]>(
        `/source-investigations/${objectRef}/history/${source.revision}/restore`,
        {
          method: "POST",
          headers: { "Idempotency-Key": request.key },
          body: JSON.stringify(request.body),
        },
      );
      onRestored(receipt);
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
      title="确认恢复调查历史"
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
            onClick={() => void restore()}
          >
            {attempted ? "重试恢复历史" : "确认恢复"}
          </Button>
        </Space>
      }
    >
      <p>
        将历史修订 {source.revision} 的说明和状态「
        {labels[source.status] || source.status}」保存为修订{" "}
        {request.body.expected_revision + 1}，要求当前修订仍为{" "}
        {request.body.expected_revision}。
      </p>
      {source.is_baseline && (
        <p>这是升级时保留的基线，更早未记录的内容无法恢复。</p>
      )}
      <p style={{ whiteSpace: "pre-wrap", overflowWrap: "anywhere" }}>
        {source.note}
      </p>
      <p>
        当前说明和状态将被替换，编辑器内尚未保存的内容会丢弃。已有历史保持原样；本次恢复不会核验归属、登记、修复或删除存储对象。
      </p>
      <Checkbox
        checked={confirmed}
        disabled={busy}
        onChange={(event) => setConfirmed(event.target.checked)}
      >
        我确认将以上历史内容保存为新修订
      </Checkbox>
      {attempted && (
        <p>
          响应未确认时重试使用原请求和原幂等键；若提示修订变化，请重新读取并核对。重新读取不会撤销已经提交的恢复。
        </p>
      )}
      {error && <Alert type="error" title={error} />}
    </Modal>
  );
}

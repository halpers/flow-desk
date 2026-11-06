import { useEffect, useState } from "react";
import {
  Alert,
  Button,
  Checkbox,
  Drawer,
  Input,
  Modal,
  Select,
  Space,
} from "antd";
import { api, ApiError } from "../../shared/api/client";
import type { Models } from "../../shared/api/models";
import { newId } from "../../shared/id";
import { InvestigationHistory } from "./InvestigationHistory";

type RecordData = Models["SourceInvestigationData"];
const labels: Record<string, string> = {
  open: "待跟进",
  investigating: "调查中",
  closed: "已结束跟进",
};
type Pending = {
  key: string;
  body: {
    selection_token: string;
    expected_revision: number;
    status: string;
    note: string;
    confirm: boolean;
  };
  attempted: boolean;
};

export function SourceInvestigation({
  objectRef,
  selectionToken,
  onClose,
  onSaved,
}: {
  objectRef: string;
  selectionToken: string;
  onClose: () => void;
  onSaved?: () => void;
}) {
  const [data, setData] = useState<RecordData | null>(null);
  const [note, setNote] = useState("");
  const [status, setStatus] = useState("");
  const [busy, setBusy] = useState(false);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [reload, setReload] = useState(0);
  const [pending, setPending] = useState<Pending | null>(null);
  const [confirmed, setConfirmed] = useState(false);
  const [historyReceipt, setHistoryReceipt] = useState("");
  const [historyOpen, setHistoryOpen] = useState(false);
  const path = `/source-investigations/${objectRef}`;
  useEffect(() => {
    const controller = new AbortController();
    void api<RecordData>(path, { signal: controller.signal })
      .then((record) => {
        if (controller.signal.aborted) return;
        setData(record);
        setNote(record.note);
        setStatus(record.status);
      })
      .catch((reason: unknown) => {
        if (!controller.signal.aborted) setError((reason as Error).message);
      })
      .finally(() => {
        if (!controller.signal.aborted) setLoading(false);
      });
    return () => controller.abort();
  }, [path, reload]);
  function refresh() {
    setHistoryReceipt("");
    setHistoryOpen(false);
    setPending(null);
    setConfirmed(false);
    setData(null);
    setNote("");
    setStatus("");
    setError("");
    setLoading(true);
    setReload((value) => value + 1);
  }
  async function save() {
    if (!pending || !confirmed || busy) return;
    const snapshot = { ...pending, attempted: true };
    setPending(snapshot);
    setBusy(true);
    setError("");
    try {
      const receipt = await api<RecordData>(path, {
        method: "PUT",
        headers: { "Idempotency-Key": snapshot.key },
        body: JSON.stringify(snapshot.body),
      });
      setData(receipt);
      setNote(receipt.note);
      setStatus(receipt.status);
      setPending(null);
      setConfirmed(false);
      setHistoryOpen(false);
      onSaved?.();
    } catch (reason) {
      setError((reason as Error).message);
      if (
        reason instanceof ApiError &&
        (reason.status === 401 ||
          reason.status === 403 ||
          reason.status === 404)
      ) {
        setData(null);
        setNote("");
        setStatus("");
        setPending(null);
        setConfirmed(false);
      }
    } finally {
      setBusy(false);
    }
  }
  return (
    <Drawer
      title="巡检调查记录"
      open
      closable={!historyOpen}
      onClose={() => {
        if (!busy && !historyOpen) onClose();
      }}
      size={660}
    >
      <p>
        记录人工调查说明和跟进进度。结束跟进不代表归属已确认，也不会登记、修复或删除存储对象。
      </p>
      <p style={{ overflowWrap: "anywhere" }}>对象指纹：{objectRef}</p>
      <Button disabled={busy || loading || historyOpen} onClick={refresh}>
        重新读取调查记录
      </Button>
      {data && data.revision > 0 && !loading && (
        <Button
          disabled={busy || !!pending || historyOpen}
          onClick={() => setHistoryOpen(true)}
        >
          查看编辑历史
        </Button>
      )}
      {historyOpen && data && (
        <InvestigationHistory
          objectRef={objectRef}
          onClose={() => setHistoryOpen(false)}
          onArchived={(receipt) => {
            refresh();
            setHistoryReceipt(
              `已归档历史修订 ${receipt.revision}，完整内容仍可查阅与恢复。当前记录以重新读取结果为准。`,
            );
            onSaved?.();
          }}
          onRestored={(receipt) => {
            refresh();
            setHistoryReceipt(
              `已将历史修订 ${receipt.restored_from_revision} 保存为修订 ${receipt.investigation.revision}。当前记录以重新读取结果为准。`,
            );
            onSaved?.();
          }}
          onDenied={(message) => {
            setHistoryOpen(false);
            setData(null);
            setNote("");
            setStatus("");
            setPending(null);
            setConfirmed(false);
            setHistoryReceipt("");
            setError(message);
          }}
        />
      )}
      {historyReceipt && <Alert type="success" title={historyReceipt} />}
      {error && <Alert type="error" title={error} />}
      {loading && <p>正在读取调查记录…</p>}
      {!loading && data && (
        <>
          <p>
            修订号：{data.revision}
            {data.updated_at &&
              ` · 更新时间：${data.updated_at} · 更新人：${data.updated_by}`}
          </p>
          {data.revision === 0 && (
            <p>尚未保存调查记录。首次保存需要本次巡检提供的有效凭据。</p>
          )}
          <label htmlFor="investigation-status">跟进状态</label>
          <Select
            id="investigation-status"
            aria-label="跟进状态"
            style={{ width: "100%" }}
            value={status}
            disabled={busy || !!pending || historyOpen}
            onChange={setStatus}
            options={data.allowed_statuses.map((value) => ({
              value,
              label: labels[value] || value,
            }))}
          />
          <label htmlFor="investigation-note">调查说明</label>
          <Input.TextArea
            id="investigation-note"
            value={note}
            rows={7}
            disabled={busy || !!pending || historyOpen}
            onChange={(event) => setNote(event.target.value)}
            placeholder="填写核对过程、结论依据及下一步（1–2000 字）"
          />
          <p>
            {Array.from(note.trim()).length} / 2000
            字；请勿填写密钥、密码或客户敏感内容。
          </p>
          <Button
            type="primary"
            disabled={
              busy ||
              historyOpen ||
              !!pending ||
              !note.trim() ||
              Array.from(note.trim()).length > 2000 ||
              (!data.revision && !selectionToken)
            }
            onClick={() => {
              setError("");
              setConfirmed(false);
              setPending({
                key: newId(),
                attempted: false,
                body: {
                  selection_token: data.revision ? "" : selectionToken,
                  expected_revision: data.revision,
                  status,
                  note: note.trim(),
                  confirm: true,
                },
              });
            }}
          >
            保存调查记录
          </Button>
        </>
      )}
      <Modal
        title="确认保存调查记录"
        open={!!pending}
        maskClosable={false}
        keyboard={false}
        closable={!busy && !pending?.attempted}
        onCancel={() => {
          if (!busy && !pending?.attempted) setPending(null);
        }}
        footer={
          <Space>
            {pending?.attempted ? (
              <Button disabled={busy} onClick={refresh}>
                放弃重试并重新读取
              </Button>
            ) : (
              <Button disabled={busy} onClick={() => setPending(null)}>
                取消
              </Button>
            )}
            <Button
              type="primary"
              disabled={!confirmed || busy}
              loading={busy}
              onClick={() => void save()}
            >
              {pending?.attempted ? "重试保存调查记录" : "确认保存"}
            </Button>
          </Space>
        }
      >
        <p>
          基于修订号 {pending?.body.expected_revision} 保存为「
          {labels[pending?.body.status || ""] || pending?.body.status}」。
        </p>
        <p style={{ whiteSpace: "pre-wrap", overflowWrap: "anywhere" }}>
          {pending?.body.note}
        </p>
        <Checkbox
          checked={confirmed}
          disabled={busy}
          onChange={(event) => setConfirmed(event.target.checked)}
        >
          我确认保存以上调查记录
        </Checkbox>
        {pending?.attempted && (
          <p>
            响应未确认时，重试使用原请求和原幂等键。重新读取会放弃当前重试并获取服务器最新记录。
          </p>
        )}
        {error && <Alert type="error" title={error} />}
      </Modal>
    </Drawer>
  );
}

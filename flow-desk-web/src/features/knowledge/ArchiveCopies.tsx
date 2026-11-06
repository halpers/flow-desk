import { useRef, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Alert, Button, Checkbox, Drawer, Empty, Modal } from "antd";
import { api } from "../../shared/api/client";
import type { Models } from "../../shared/api/models";
import { newId } from "../../shared/id";
import { SourceCopyHistory } from "./SourceCopyHistory";

type Copy = Models["ArchiveCopiesData"]["items"][number];
const reasons: Record<string, string> = {
  cleanup_disabled: "清理开关尚未启用",
  cleanup_pending: "清理中",
  retired: "副本字节已清理",
  restore_required: "请先将原件恢复至数据库",
  version_retired: "知识版本已进入清理流程",
  current_generation: "当前归档批次受保护",
  store_mismatch: "当前存储配置不匹配",
  original_unverified: "数据库原件尚未通过完整性校验",
};

export function ArchiveCopies({
  documentId,
  versionId,
  scope,
}: {
  documentId: string;
  versionId: string;
  scope: string[];
}) {
  const [open, setOpen] = useState(false);
  const [selected, setSelected] = useState<Copy | null>(null);
  const [confirmed, setConfirmed] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const keys = useRef(new Map<string, string>());
  const path = `/documents/${documentId}/versions/${versionId}/archive-copies`;
  const copies = useQuery({
    queryKey: [...scope, "archive-copies", versionId],
    queryFn: () => api<Models["ArchiveCopiesData"]>(path),
    enabled: open,
    refetchInterval: (query) =>
      open &&
      query.state.data?.items.some((item) => item.status === "cleanup_pending")
        ? 2000
        : false,
  });
  return (
    <>
      <Button
        size="small"
        onClick={() => {
          setError("");
          setOpen(true);
        }}
      >
        归档副本清理
      </Button>
      <Drawer
        title="归档副本清理"
        open={open}
        onClose={() => {
          if (!busy) setOpen(false);
        }}
        size="large"
      >
        <p>
          先恢复并核对数据库原件，再清理旧归档副本。清理保留零字节防重写标记，不减少存储桶对象数量；数据库原件与历史引用保留。
        </p>
        <Button
          disabled={busy}
          onClick={() => {
            setError("");
            void copies.refetch();
          }}
        >
          刷新归档副本
        </Button>
        <SourceCopyHistory
          documentId={documentId}
          versionId={versionId}
          scope={scope}
          onArchived={() => copies.refetch()}
        />
        {(copies.error || error) && (
          <Alert
            type="error"
            title={error || (copies.error as Error).message}
          />
        )}
        {!copies.isPending && copies.data?.items.length === 0 && (
          <Empty description="暂无已登记的归档副本" />
        )}
        {copies.data?.items.map((item) => (
          <section className="archive-copy" key={item.id}>
            <p>
              副本 {item.id.slice(0, 8)} · 批次 {item.source_epoch} · 原大小{" "}
              {item.source_size} 字节
            </p>
            <p>
              {item.reasons.length
                ? item.reasons
                    .map((reason) => reasons[reason] || reason)
                    .join("、")
                : "可清理的冗余副本"}
            </p>
            <Button
              danger
              disabled={busy || !item.eligible}
              onClick={() => {
                setSelected(item);
                setConfirmed(false);
                setError("");
              }}
            >
              清理此归档副本
            </Button>
          </section>
        ))}
      </Drawer>
      <Modal
        title="确认清理归档副本"
        open={selected !== null}
        confirmLoading={busy}
        okText="确认清理副本字节"
        okButtonProps={{ danger: true, disabled: !confirmed }}
        onCancel={() => {
          if (!busy) setSelected(null);
        }}
        onOk={async () => {
          if (!selected || !copies.data || !confirmed) return;
          setBusy(true);
          setError("");
          const fingerprint = `${selected.id}:${copies.data.source_epoch}`;
          let key = keys.current.get(fingerprint);
          if (!key) {
            key = newId();
            keys.current.set(fingerprint, key);
          }
          try {
            await api(`${path}/${selected.id}/cleanup`, {
              method: "POST",
              headers: { "Idempotency-Key": key },
              body: JSON.stringify({
                confirm: true,
                expected_source_epoch: copies.data.source_epoch,
              }),
            });
            setSelected(null);
            await copies.refetch();
          } catch (reason) {
            setError((reason as Error).message);
          } finally {
            setBusy(false);
          }
        }}
      >
        <p>
          副本 {selected?.id}
          。清理不能撤销，原文件仍保存在数据库；恢复旧副本需要备份。
        </p>
        <Checkbox
          checked={confirmed}
          onChange={(event) => setConfirmed(event.target.checked)}
        >
          我已核对数据库原件与备份
        </Checkbox>
        {error && <Alert type="error" title={error} />}
      </Modal>
    </>
  );
}

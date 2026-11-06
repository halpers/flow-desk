import { useRef, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import {
  Alert,
  Button,
  Checkbox,
  Drawer,
  Empty,
  Modal,
  Space,
  Tag,
} from "antd";
import { api } from "../../shared/api/client";
import type { Models } from "../../shared/api/models";
import { newId } from "../../shared/id";

type Item = Models["SourceCopyHistoryPage"]["items"][number];
const reasons: Record<string, string> = {
  already_archived: "登记已归档",
  not_retired: "请先完成副本字节清理",
  retirement_incomplete: "清理时间尚未确认",
  current_generation: "当前代次受保护",
  cleanup_delivery_pending: "清理任务尚未确认完成",
};

export function SourceCopyHistory({
  documentId,
  versionId,
  scope,
  onArchived,
}: {
  documentId: string;
  versionId: string;
  scope: string[];
  onArchived: () => Promise<unknown>;
}) {
  const [open, setOpen] = useState(false);
  const [cursors, setCursors] = useState([""]);
  const [selection, setSelection] = useState<{
    item: Item;
    epoch: number;
  } | null>(null);
  const [confirmed, setConfirmed] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [receipt, setReceipt] = useState<
    Models["SourceMetadataArchiveData"] | null
  >(null);
  const keys = useRef(new Map<string, string>());
  const cursor = cursors[cursors.length - 1];
  const path = `/documents/${documentId}/versions/${versionId}/archive-copies`;
  const history = useQuery({
    queryKey: [...scope, "source-copy-history", versionId, cursor],
    queryFn: () =>
      api<Models["SourceCopyHistoryPage"]>(
        `${path}/history?limit=20&cursor=${encodeURIComponent(cursor)}`,
      ),
    enabled: open,
    staleTime: 0,
    refetchOnMount: "always",
    retry: false,
  });
  return (
    <>
      <Button
        size="small"
        onClick={() => {
          setOpen(true);
          setCursors([""]);
          setError("");
          void history.refetch();
        }}
      >
        完整副本历史
      </Button>
      <Drawer
        title="完整副本历史"
        open={open}
        size={760}
        onClose={() => {
          if (!busy) setOpen(false);
        }}
      >
        <p>
          包含已清理及已归档的副本登记。登记归档保留完整身份和历史查询，存储防重写标记继续保留。
        </p>
        <Space>
          <Button
            disabled={busy || history.isFetching}
            onClick={() => {
              setCursors([""]);
              void history.refetch();
            }}
          >
            刷新副本历史
          </Button>
          <Button
            disabled={busy || history.isFetching || cursors.length === 1}
            onClick={() => setCursors((values) => values.slice(0, -1))}
          >
            上一页
          </Button>
          <span>第 {cursors.length} 页</span>
          <Button
            disabled={
              busy ||
              history.isFetching ||
              history.isError ||
              !history.data?.has_more
            }
            onClick={() => {
              if (history.data?.next_cursor)
                setCursors((values) => [...values, history.data!.next_cursor]);
            }}
          >
            下一页
          </Button>
        </Space>
        {receipt && (
          <Alert
            type="success"
            title={`登记已归档：${receipt.object_id}`}
            description={`归档时间：${receipt.archived_at}`}
          />
        )}
        {history.isError && (
          <Alert type="error" title="副本历史加载失败，请刷新重试" />
        )}
        {!history.isError &&
          !history.isFetching &&
          history.data?.items.map((item) => (
            <section
              className="source-history-item"
              key={item.id}
              style={{ marginTop: 16 }}
            >
              <strong>副本 {item.id}</strong>{" "}
              <Tag>
                {item.metadata_archived
                  ? "登记已归档"
                  : item.status === "retired"
                    ? "字节已清理"
                    : "登记保留"}
              </Tag>
              <p>
                批次 {item.source_epoch} · 原大小 {item.source_size} 字节 ·
                登记时间 {item.created_at}
              </p>
              {item.retired_at && <p>清理时间：{item.retired_at}</p>}
              {item.archived_at && <p>归档时间：{item.archived_at}</p>}
              <p>
                {reasons[item.archive_block_reason] ||
                  item.archive_block_reason ||
                  "可归档退休登记"}
              </p>
              <Button
                disabled={busy || !item.can_archive}
                onClick={() => {
                  setSelection({ item, epoch: history.data!.source_epoch });
                  setConfirmed(false);
                  setError("");
                }}
              >
                归档此副本登记
              </Button>
            </section>
          ))}
        {!history.isFetching &&
          !history.isError &&
          !history.data?.items.length && <Empty description="暂无副本历史" />}
        <Modal
          title="确认归档副本登记"
          open={selection !== null}
          confirmLoading={busy}
          okText="确认归档登记"
          okButtonProps={{ disabled: !confirmed }}
          onCancel={() => {
            if (!busy) setSelection(null);
          }}
          onOk={async () => {
            if (!selection || !confirmed || busy) return;
            setBusy(true);
            setError("");
            const fingerprint = `${selection.item.id}:${selection.epoch}`;
            let key = keys.current.get(fingerprint);
            if (!key) {
              key = newId();
              keys.current.set(fingerprint, key);
            }
            try {
              const result = await api<Models["SourceMetadataArchiveData"]>(
                `${path}/${selection.item.id}/archive-metadata`,
                {
                  method: "POST",
                  headers: { "Idempotency-Key": key },
                  body: JSON.stringify({
                    confirm: true,
                    expected_source_epoch: selection.epoch,
                  }),
                },
              );
              setReceipt(result);
              setSelection(null);
              await onArchived();
              await history.refetch();
            } catch (reason) {
              setError((reason as Error).message);
            } finally {
              setBusy(false);
            }
          }}
        >
          <p>
            副本 {selection?.item.id}
            。登记将移入历史表；对象身份、清理回执和防重写标记保留，完整历史仍可查询。
          </p>
          <Checkbox
            checked={confirmed}
            onChange={(event) => setConfirmed(event.target.checked)}
          >
            我已核对退休登记与备份
          </Checkbox>
          {error && <Alert type="error" title={error} />}
        </Modal>
      </Drawer>
    </>
  );
}

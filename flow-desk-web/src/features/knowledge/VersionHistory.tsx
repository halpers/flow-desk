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
import { SourceCopyHistory } from "./SourceCopyHistory";

const reasons: Record<string, string> = {
  already_archived: "元数据已归档",
  content_not_purged: "请先完成版本内容清理",
  active: "当前发布版本受保护",
  referenced: "历史引用受保护",
  archive_history: "原文件存储历史受保护",
  knowledge_busy: "知识任务尚未完成",
};

export function VersionHistory({
  documentId,
  scope,
  onArchived,
}: {
  documentId: string;
  scope: string[];
  onArchived: () => Promise<void>;
}) {
  const [open, setOpen] = useState(false);
  const [cursors, setCursors] = useState([""]);
  const [candidate, setCandidate] = useState<
    Models["VersionHistoryItem"] | null
  >(null);
  const [expected, setExpected] = useState(0);
  const [confirmed, setConfirmed] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const keys = useRef(new Map<string, string>());
  const cursor = cursors[cursors.length - 1];
  const history = useQuery({
    queryKey: [...scope, "history", documentId, cursor],
    queryFn: () =>
      api<Models["VersionHistoryPage"]>(
        `/documents/${documentId}/history?limit=20&cursor=${encodeURIComponent(cursor)}`,
      ),
    enabled: open,
    // Old page eligibility is refreshed every time a page is entered.
    staleTime: 0,
    refetchOnMount: "always",
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
        完整版本历史
      </Button>
      <Drawer
        title="完整版本历史"
        open={open}
        size={720}
        onClose={() => {
          if (!busy) setOpen(false);
        }}
      >
        <p>
          包含已清理及已归档的版本详情。元数据归档保留历史可查；已清理的原文无法通过此操作恢复。
        </p>
        <Space>
          <Button
            disabled={busy || history.isFetching}
            onClick={() => {
              setCursors([""]);
              void history.refetch();
            }}
          >
            刷新历史
          </Button>
          <Button
            disabled={busy || history.isFetching || cursors.length === 1}
            onClick={() => setCursors((values) => values.slice(0, -1))}
          >
            上一页
          </Button>
          <span>第 {cursors.length} 页</span>
          <Button
            disabled={busy || history.isFetching || !history.data?.has_more}
            onClick={() => {
              if (history.data?.next_cursor)
                setCursors((values) => [...values, history.data!.next_cursor]);
            }}
          >
            下一页
          </Button>
        </Space>
        {history.isError && (
          <Alert type="error" title="历史加载失败，请刷新重试" />
        )}
        {!history.isError &&
          history.data?.items.map((item) => (
            <section
              className="history-item"
              key={item.id}
              style={{ marginTop: 16 }}
            >
              <strong>{item.filename}</strong>{" "}
              <Tag>
                {item.metadata_archived
                  ? "元数据已归档"
                  : item.cleanup_state === "purged"
                    ? "内容已清理"
                    : item.cleanup_state === "cleanup_pending"
                      ? "清理中"
                      : "内容保留"}
              </Tag>
              <p>
                {item.id} · {item.created_at}
              </p>
              <p>
                {item.source_format} · {item.source_size} 字节 ·{" "}
                {item.chunk_count} 个片段
              </p>
              <p>
                提取方式：{item.parser_version || "未记录"} · OCR：
                {item.ocr_mode} · 阅读方式：{item.pdf_layout}
              </p>
              {item.source_format === "pdf" && (
                <SourceCopyHistory
                  documentId={documentId}
                  versionId={item.id}
                  scope={scope}
                  onArchived={onArchived}
                />
              )}
              {item.error_code && <p>索引错误：{item.error_code}</p>}
              {item.archived_at && <p>归档时间：{item.archived_at}</p>}
              <p>
                {reasons[item.archive_block_reason] ||
                  item.archive_block_reason ||
                  "可归档版本详情"}
              </p>
              <Button
                disabled={busy || history.isFetching || !item.can_archive}
                onClick={() => {
                  setCandidate(item);
                  setExpected(history.data!.document_version);
                  setConfirmed(false);
                  setError("");
                }}
              >
                归档版本详情
              </Button>
            </section>
          ))}
        {!history.isFetching &&
          !history.isError &&
          !history.data?.items.length && <Empty description="暂无版本历史" />}
        <Modal
          title="确认归档版本详情"
          open={candidate !== null}
          confirmLoading={busy}
          okText="确认归档详情"
          okButtonProps={{ disabled: !confirmed }}
          onCancel={() => {
            if (!busy) setCandidate(null);
          }}
          onOk={async () => {
            if (!candidate || !confirmed) return;
            setBusy(true);
            setError("");
            const fingerprint = `${candidate.id}:${expected}`;
            let key = keys.current.get(fingerprint);
            if (!key) {
              key = newId();
              keys.current.set(fingerprint, key);
            }
            try {
              await api(
                `/documents/${documentId}/versions/${candidate.id}/archive-metadata`,
                {
                  method: "POST",
                  headers: { "Idempotency-Key": key },
                  body: JSON.stringify({
                    confirm: true,
                    expected_document_version: expected,
                  }),
                },
              );
              setCandidate(null);
              await onArchived();
              await history.refetch();
            } catch (reason) {
              setError((reason as Error).message);
            } finally {
              setBusy(false);
            }
          }}
        >
          <p>{candidate?.filename}</p>
          <p>
            版本详情将移入历史归档，完整历史仍可查看。此操作无法恢复已清理的原文。
          </p>
          <Checkbox
            checked={confirmed}
            onChange={(event) => setConfirmed(event.target.checked)}
          >
            我已核对归档范围与备份
          </Checkbox>
          {error && <Alert type="error" title={error} />}
        </Modal>
      </Drawer>
    </>
  );
}

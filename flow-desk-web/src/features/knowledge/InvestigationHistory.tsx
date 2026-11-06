import { useEffect, useState } from "react";
import { Alert, Button, Checkbox, Drawer, Empty, Space, Tag } from "antd";
import { api } from "../../shared/api/client";
import { InvestigationArchiveBatch } from "./InvestigationArchiveBatch";
import { InvestigationArchive } from "./InvestigationArchive";
import { InvestigationRestore } from "./InvestigationRestore";
import type { Models } from "../../shared/api/models";

const archiveReasons: Record<string, string> = {
  already_archived: "已归档，完整历史保留",
  investigation_active: "调查尚未结束，继续保留",
  keep_latest: "保留最近3个修订",
  minimum_age: "未满30天，继续保留",
};
const labels: Record<string, string> = {
  open: "待跟进",
  investigating: "调查中",
  closed: "已结束跟进",
};

export function InvestigationHistory({
  objectRef,
  onClose,
  onArchived,
  onBatchArchived,
  onRestored,
  onDenied,
}: {
  objectRef: string;
  onClose: () => void;
  onBatchArchived: (receipt: Models["InvestigationArchiveBatchData"]) => void;
  onArchived: (receipt: Models["InvestigationArchiveData"]) => void;
  onRestored: (receipt: Models["InvestigationRestoreData"]) => void;
  onDenied: (message: string) => void;
}) {
  const [selection, setSelection] = useState<number[]>([]);
  const [batchOpen, setBatchOpen] = useState(false);
  const [pending, setPending] = useState<{
    kind: "restore" | "archive";
    source: Models["InvestigationHistoryPage"]["items"][number];
    expected: number;
  } | null>(null);
  const confirmationOpen = !!pending || batchOpen;
  const [cursors, setCursors] = useState([""]);
  const [reload, setReload] = useState(0);
  const [data, setData] = useState<Models["InvestigationHistoryPage"] | null>(
    null,
  );
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const cursor = cursors[cursors.length - 1];
  useEffect(() => {
    const controller = new AbortController();
    void api<Models["InvestigationHistoryPage"]>(
      `/source-investigations/${objectRef}/history?limit=20&cursor=${encodeURIComponent(cursor)}`,
      { signal: controller.signal },
    )
      .then((page) => {
        if (!controller.signal.aborted) setData(page);
      })
      .catch((reason: unknown) => {
        if (!controller.signal.aborted) setError((reason as Error).message);
      })
      .finally(() => {
        if (!controller.signal.aborted) setLoading(false);
      });
    return () => controller.abort();
  }, [objectRef, cursor, reload]);
  function navigate(values: string[]) {
    setSelection([]);
    setData(null);
    setError("");
    setLoading(true);
    setCursors(values);
  }
  return (
    <Drawer
      title="调查编辑历史"
      open
      closable={!confirmationOpen}
      onClose={() => {
        if (!confirmationOpen) onClose();
      }}
      size={740}
    >
      <p>
        按修订号从新到旧查看每次保存的说明、状态、操作人和时间。选择旧修订可核对并确认恢复为新修订，已有历史继续保留。
      </p>
      <p style={{ overflowWrap: "anywhere" }}>对象指纹：{objectRef}</p>
      <Space wrap>
        <Button
          disabled={loading || confirmationOpen}
          onClick={() => {
            navigate([""]);
            setReload((value) => value + 1);
          }}
        >
          刷新编辑历史
        </Button>
        <Button
          disabled={loading || confirmationOpen || cursors.length === 1}
          onClick={() => navigate(cursors.slice(0, -1))}
        >
          上一页
        </Button>
        <span>第 {cursors.length} 页</span>
        <Button
          disabled={loading || confirmationOpen || !!error || !data?.has_more}
          onClick={() => {
            if (data?.next_cursor) navigate([...cursors, data.next_cursor]);
          }}
        >
          下一页
        </Button>
      </Space>
      <Button
        disabled={
          loading || !!error || confirmationOpen || selection.length === 0
        }
        onClick={() => setBatchOpen(true)}
      >
        批量归档选中修订（{selection.length}）
      </Button>
      <p>批量选择仅限当前页；翻页或刷新会清空选择，提交前逐项预览。</p>
      {loading && <p>正在读取编辑历史…</p>}
      {error && <Alert type="error" title={error} />}
      {!loading && !error && data && (
        <>
          <p>
            当前修订：{data.current_revision} · 本次翻页上界：
            {data.snapshot_revision}
          </p>
          {data.first_recorded_revision > 1 && (
            <Alert
              type="info"
              title={`历史从修订 ${data.first_recorded_revision} 开始，更早的修改未记录，无法补回。`}
            />
          )}
          {data.current_revision > data.snapshot_revision && (
            <Alert type="info" title="已有新的修订，请刷新编辑历史查看。" />
          )}
          {data.items.map((item) => (
            <section
              className="investigation-revision-item"
              key={item.revision}
              style={{ marginTop: 20 }}
            >
              <Space wrap>
                <Checkbox
                  aria-label={`选择归档修订 ${item.revision}`}
                  checked={selection.includes(item.revision)}
                  disabled={confirmationOpen || !item.can_archive}
                  onChange={(event) =>
                    setSelection((values) =>
                      event.target.checked
                        ? [...values, item.revision].sort(
                            (left, right) => left - right,
                          )
                        : values.filter((value) => value !== item.revision),
                    )
                  }
                />
                <strong>修订 {item.revision}</strong>
                <Tag>{labels[item.status] || item.status}</Tag>
                {item.is_baseline && <Tag>升级基线</Tag>}
                {item.metadata_archived && <Tag>已归档</Tag>}
                <Button
                  disabled={confirmationOpen || !item.can_archive}
                  onClick={() =>
                    setPending({
                      kind: "archive",
                      source: item,
                      expected: data.current_revision,
                    })
                  }
                >
                  归档此修订
                </Button>
                <Button
                  disabled={
                    confirmationOpen || item.revision >= data.current_revision
                  }
                  onClick={() =>
                    setPending({
                      kind: "restore",
                      source: item,
                      expected: data.current_revision,
                    })
                  }
                >
                  恢复此修订
                </Button>
              </Space>
              <p>
                操作人：{item.changed_by} · 时间：{item.changed_at}
              </p>
              {item.archived_at && <p>归档时间：{item.archived_at}</p>}
              {!item.can_archive && item.archive_block_reason && (
                <p>
                  {archiveReasons[item.archive_block_reason] ||
                    "此修订暂不可归档"}
                </p>
              )}
              {item.is_baseline && (
                <p>升级时保留的最新内容，并非升级时发生的一次修改。</p>
              )}
              <p
                className="investigation-revision-note"
                style={{ whiteSpace: "pre-wrap", overflowWrap: "anywhere" }}
              >
                {item.note}
              </p>
            </section>
          ))}
          {!data.items.length && <Empty description="此页没有编辑历史" />}
        </>
      )}
      {pending?.kind === "restore" && (
        <InvestigationRestore
          objectRef={objectRef}
          source={pending.source}
          expectedRevision={pending.expected}
          onCancel={() => {
            const wasPending = pending;
            setPending(null);
            if (wasPending) {
              navigate([""]);
              setReload((value) => value + 1);
            }
          }}
          onRestored={onRestored}
          onDenied={onDenied}
        />
      )}
      {pending?.kind === "archive" && (
        <InvestigationArchive
          objectRef={objectRef}
          source={pending.source}
          expectedRevision={pending.expected}
          onCancel={() => {
            setPending(null);
            navigate([""]);
            setReload((value) => value + 1);
          }}
          onArchived={onArchived}
          onDenied={onDenied}
        />
      )}
      {batchOpen && data && (
        <InvestigationArchiveBatch
          objectRef={objectRef}
          revisions={selection}
          expectedRevision={data.current_revision}
          onCancel={() => {
            setBatchOpen(false);
            navigate([""]);
            setReload((value) => value + 1);
          }}
          onArchived={onBatchArchived}
          onDenied={onDenied}
        />
      )}
    </Drawer>
  );
}

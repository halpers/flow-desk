import { useEffect, useState } from "react";
import { Alert, Button, Drawer, Empty, Space, Tag } from "antd";
import { api } from "../../shared/api/client";
import { InvestigationRestore } from "./InvestigationRestore";
import type { Models } from "../../shared/api/models";

const labels: Record<string, string> = {
  open: "待跟进",
  investigating: "调查中",
  closed: "已结束跟进",
};

export function InvestigationHistory({
  objectRef,
  onClose,
  onRestored,
  onDenied,
}: {
  objectRef: string;
  onClose: () => void;
  onRestored: (receipt: Models["InvestigationRestoreData"]) => void;
  onDenied: (message: string) => void;
}) {
  const [pending, setPending] = useState<{
    source: Models["InvestigationHistoryPage"]["items"][number];
    expected: number;
  } | null>(null);
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
    setData(null);
    setError("");
    setLoading(true);
    setCursors(values);
  }
  return (
    <Drawer
      title="调查编辑历史"
      open
      closable={!pending}
      onClose={() => {
        if (!pending) onClose();
      }}
      size={740}
    >
      <p>
        按修订号从新到旧查看每次保存的说明、状态、操作人和时间。选择旧修订可核对并确认恢复为新修订，已有历史继续保留。
      </p>
      <p style={{ overflowWrap: "anywhere" }}>对象指纹：{objectRef}</p>
      <Space wrap>
        <Button
          disabled={loading || !!pending}
          onClick={() => {
            navigate([""]);
            setReload((value) => value + 1);
          }}
        >
          刷新编辑历史
        </Button>
        <Button
          disabled={loading || !!pending || cursors.length === 1}
          onClick={() => navigate(cursors.slice(0, -1))}
        >
          上一页
        </Button>
        <span>第 {cursors.length} 页</span>
        <Button
          disabled={loading || !!pending || !!error || !data?.has_more}
          onClick={() => {
            if (data?.next_cursor) navigate([...cursors, data.next_cursor]);
          }}
        >
          下一页
        </Button>
      </Space>
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
                <strong>修订 {item.revision}</strong>
                <Tag>{labels[item.status] || item.status}</Tag>
                {item.is_baseline && <Tag>升级基线</Tag>}
                <Button
                  disabled={!!pending || item.revision >= data.current_revision}
                  onClick={() =>
                    setPending({
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
      {pending && (
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
    </Drawer>
  );
}

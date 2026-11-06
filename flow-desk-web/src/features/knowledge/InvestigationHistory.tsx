import { useEffect, useState } from "react";
import { Alert, Button, Drawer, Empty, Space, Tag } from "antd";
import { api } from "../../shared/api/client";
import type { Models } from "../../shared/api/models";

const labels: Record<string, string> = {
  open: "待跟进",
  investigating: "调查中",
  closed: "已结束跟进",
};

export function InvestigationHistory({
  objectRef,
  onClose,
}: {
  objectRef: string;
  onClose: () => void;
}) {
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
    <Drawer title="调查编辑历史" open onClose={onClose} size={740}>
      <p>
        按修订号从新到旧查看每次保存的说明、状态、操作人和时间。历史仅供查阅，修改请返回当前调查记录。
      </p>
      <p style={{ overflowWrap: "anywhere" }}>对象指纹：{objectRef}</p>
      <Space wrap>
        <Button
          disabled={loading}
          onClick={() => {
            navigate([""]);
            setReload((value) => value + 1);
          }}
        >
          刷新编辑历史
        </Button>
        <Button
          disabled={loading || cursors.length === 1}
          onClick={() => navigate(cursors.slice(0, -1))}
        >
          上一页
        </Button>
        <span>第 {cursors.length} 页</span>
        <Button
          disabled={loading || !!error || !data?.has_more}
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
    </Drawer>
  );
}

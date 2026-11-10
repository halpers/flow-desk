import { useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Alert, Button, Drawer, Empty, Select, Space, Tag } from "antd";
import { api } from "../../shared/api/client";
import type { Models } from "../../shared/api/models";
import { SourceInvestigation } from "./SourceInvestigation";

import { InvestigationAutomation } from "./InvestigationAutomation";

const labels: Record<string, string> = {
  open: "待跟进",
  investigating: "调查中",
  closed: "已结束跟进",
};

export function InvestigationWorklist({ scope }: { scope: string[] }) {
  const queryClient = useQueryClient();
  const [denied, setDenied] = useState("");
  const [open, setOpen] = useState(false);
  const [status, setStatus] = useState("");
  const [cursors, setCursors] = useState([""]);
  const [selected, setSelected] = useState<string | null>(null);
  const cursor = cursors[cursors.length - 1];
  const query = useQuery({
    queryKey: [...scope, "investigation-worklist", status, cursor],
    queryFn: ({ signal }) =>
      api<Models["SourceInvestigationListPage"]>(
        `/source-investigations?limit=20&status=${encodeURIComponent(status)}&cursor=${encodeURIComponent(cursor)}`,
        { signal },
      ),
    enabled: open,
    staleTime: 0,
    refetchOnMount: "always",
    retry: false,
    gcTime: 0,
  });
  function refresh() {
    if (cursor) setCursors([""]);
    else void query.refetch();
  }
  return (
    <>
      {denied && <Alert type="error" title={denied} />}
      <Button
        onClick={() => {
          setOpen(true);
          setCursors([""]);
          void query.refetch();
        }}
      >
        调查工作台
      </Button>
      <Drawer
        title="巡检调查工作台"
        open={open}
        onClose={() => setOpen(false)}
        size={720}
      >
        <p>
          按创建时间从新到旧查看当前可访问的调查。可在存储离线或巡检暂停后继续跟进；跟进状态不代表对象归属或删除权限。
        </p>
        <InvestigationAutomation
          onDenied={(message) => {
            setDenied(message);
            setSelected(null);
            setOpen(false);
            void queryClient.cancelQueries({ queryKey: scope });
            queryClient.removeQueries({ queryKey: scope });
          }}
        />
        <Space wrap>
          <Select
            aria-label="筛选跟进状态"
            value={status}
            style={{ width: 180 }}
            disabled={query.isFetching}
            options={[
              { value: "", label: "全部状态" },
              ...(query.data?.allowed_statuses || []).map((value) => ({
                value,
                label: labels[value] || value,
              })),
            ]}
            onChange={(value) => {
              setStatus(value);
              setCursors([""]);
            }}
          />
          <Button disabled={query.isFetching} onClick={refresh}>
            刷新调查列表
          </Button>
          <Button
            disabled={query.isFetching || cursors.length === 1}
            onClick={() => setCursors((values) => values.slice(0, -1))}
          >
            上一页
          </Button>
          <span>第 {cursors.length} 页</span>
          <Button
            disabled={
              query.isFetching || query.isError || !query.data?.has_more
            }
            onClick={() => {
              if (query.data?.next_cursor)
                setCursors((values) => [...values, query.data.next_cursor]);
            }}
          >
            下一页
          </Button>
        </Space>
        {query.isFetching && <p>正在读取调查列表…</p>}
        {query.isError && (
          <Alert type="error" title={(query.error as Error).message} />
        )}
        {!query.isFetching && !query.isError && query.data && (
          <>
            {query.data.items.map((item) => (
              <section
                className="investigation-worklist-item"
                key={item.object_ref}
                style={{ marginTop: 16 }}
              >
                <Tag>{labels[item.status] || item.status}</Tag>
                <span>修订 {item.revision}</span>
                <p style={{ overflowWrap: "anywhere" }}>
                  对象指纹：{item.object_ref}
                </p>
                <p>创建：{item.created_at}</p>
                <p>
                  最近更新：{item.updated_at} · 更新人：{item.updated_by}
                </p>
                <Button onClick={() => setSelected(item.object_ref)}>
                  继续跟进
                </Button>
              </section>
            ))}
            {!query.data.items.length && (
              <Empty
                description={
                  query.data.has_more
                    ? "本页无可展示调查，可继续下一页"
                    : "此页无可展示调查"
                }
              />
            )}
            <p>
              列表按当前权限筛选；状态及权限变化后请刷新，空页仍可能有后页。
            </p>
          </>
        )}
      </Drawer>
      {selected && (
        <SourceInvestigation
          key={selected}
          objectRef={selected}
          selectionToken=""
          onSaved={() => {
            void query.refetch();
          }}
          onClose={() => setSelected(null)}
        />
      )}
    </>
  );
}

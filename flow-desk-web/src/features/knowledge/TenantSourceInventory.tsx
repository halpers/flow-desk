import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Alert, Button, Drawer, Empty, Space, Tag } from "antd";
import { api } from "../../shared/api/client";
import type { Models } from "../../shared/api/models";
import { SourceInventory } from "./SourceInventory";

const associations: Record<string, string> = {
  unknown_version: "未找到版本记录，待核对",
  unrecognized_key: "非标准对象，待核对",
  known_version: "已关联当前可访问版本",
};
const reconciliation: Record<string, string> = {
  unresolved: "归属尚未确认",
  unrecognized: "无法识别标准副本",
  version_mismatch: "名称中的摘要或格式与版本不符",
  unregistered: "版本存在，副本未登记",
  registration_mismatch: "登记信息异常",
  current: "当前原件，列表大小匹配",
  registered: "已登记，列表大小匹配",
  cleanup_pending: "登记显示清理中",
  retired_zero_bytes: "登记已清理，列表为零字节",
  size_mismatch: "列表大小异常",
};

export function TenantSourceInventory({ scope }: { scope: string[] }) {
  const [open, setOpen] = useState(false);
  const [cursors, setCursors] = useState([""]);
  const cursor = cursors[cursors.length - 1];
  const query = useQuery({
    queryKey: [...scope, "tenant-source-inventory", cursor],
    queryFn: ({ signal }) =>
      api<Models["TenantSourceInventoryPage"]>(
        `/source-inventory?limit=20&cursor=${encodeURIComponent(cursor)}`,
        { signal },
      ),
    enabled: open,
    staleTime: 0,
    refetchOnMount: "always",
    retry: false,
  });
  return (
    <>
      <Button
        onClick={() => {
          setCursors([""]);
          setOpen(true);
        }}
      >
        租户对象巡检
      </Button>
      <Drawer
        title="租户对象巡检与归属核对"
        open={open}
        onClose={() => setOpen(false)}
        size={720}
      >
        <p>
          逐页核对当前租户的存储对象。对象名不能证明归属，未知项需人工核对。这里只观察列表元数据，不核验内容，也不能据此判断原件丢失或执行删除。
        </p>
        <Space>
          <Button
            disabled={query.isFetching}
            onClick={() => {
              if (!cursor) void query.refetch();
              else setCursors([""]);
            }}
          >
            刷新巡检
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
        {query.isError && (
          <Alert type="error" title={(query.error as Error).message} />
        )}
        {!query.isError && query.data && (
          <>
            <p>
              观察时间：{query.data.observed_at} · 本页扫描{" "}
              {query.data.scanned_count} 项，显示 {query.data.items.length}{" "}
              项。无权限的已知版本对象不展示，空页仍可能有后页。
            </p>
            {query.data.items.map((item) => (
              <section
                className="tenant-source-inventory-item"
                key={item.object_ref}
                style={{ marginTop: 16 }}
              >
                <Tag
                  color={
                    item.association !== "known_version" ||
                    item.reconciliation.includes("mismatch")
                      ? "orange"
                      : undefined
                  }
                >
                  {associations[item.association] || item.association}
                </Tag>
                <p>
                  {reconciliation[item.reconciliation] || item.reconciliation}
                </p>
                <p style={{ overflowWrap: "anywhere" }}>
                  对象指纹：{item.object_ref}
                </p>
                <p>
                  {item.size} 字节 · {item.modified_at}
                </p>
                {item.document_id && item.version_id && (
                  <>
                    <p>版本编号：{item.version_id}</p>
                    <SourceInventory
                      documentId={item.document_id}
                      versionId={item.version_id}
                      scope={scope}
                    />
                  </>
                )}
              </section>
            ))}
            {!query.data.items.length && (
              <Empty
                description={
                  query.data.has_more
                    ? "本页无可展示对象，可继续下一页"
                    : "此页无可展示对象"
                }
              />
            )}
          </>
        )}
      </Drawer>
    </>
  );
}

import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Alert, Button, Drawer, Empty, Space, Tag } from "antd";
import { api } from "../../shared/api/client";
import type { Models } from "../../shared/api/models";
import { AdoptSource } from "./AdoptSource";

const results: Record<string, string> = {
  unregistered: "未登记，待人工核对",
  unrecognized: "非标准定位，待人工核对",
  registration_mismatch: "登记信息异常",
  current: "当前原件，列表大小匹配",
  registered: "已登记，列表大小匹配",
  cleanup_pending: "登记显示清理中",
  retired_zero_bytes: "登记已清理，列表为零字节",
  size_mismatch: "列表大小异常",
};

export function SourceInventory({
  documentId,
  versionId,
  scope,
}: {
  documentId: string;
  versionId: string;
  scope: string[];
}) {
  const [open, setOpen] = useState(false);
  const [cursors, setCursors] = useState([""]);
  const cursor = cursors[cursors.length - 1];
  const inventory = useQuery({
    queryKey: [...scope, "source-inventory", versionId, cursor],
    queryFn: ({ signal }) =>
      api<Models["SourceInventoryPage"]>(
        `/documents/${documentId}/versions/${versionId}/source-inventory?limit=20&cursor=${encodeURIComponent(cursor)}`,
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
        size="small"
        onClick={() => {
          setOpen(true);
          setCursors([""]);
        }}
      >
        对象巡检
      </Button>
      <Drawer
        title="对象巡检与登记对账"
        open={open}
        onClose={() => setOpen(false)}
        size={720}
      >
        <p>
          按当前版本核对存储列表与登记记录。未登记项需人工核对，不能据此删除。巡检不核验原文内容或清理标记完整性，也不能据此判断未列出的原件丢失。
        </p>
        <Space>
          <Button
            disabled={inventory.isFetching}
            onClick={() => {
              if (cursor === "") void inventory.refetch();
              else setCursors([""]);
            }}
          >
            刷新巡检
          </Button>
          <Button
            disabled={inventory.isFetching || cursors.length === 1}
            onClick={() => setCursors((values) => values.slice(0, -1))}
          >
            上一页
          </Button>
          <span>第 {cursors.length} 页</span>
          <Button
            disabled={
              inventory.isFetching ||
              inventory.isError ||
              !inventory.data?.has_more
            }
            onClick={() => {
              if (inventory.data?.next_cursor)
                setCursors((values) => [
                  ...values,
                  inventory.data!.next_cursor,
                ]);
            }}
          >
            下一页
          </Button>
        </Space>
        {inventory.isError && (
          <Alert type="error" title={(inventory.error as Error).message} />
        )}
        {!inventory.isError && inventory.data && (
          <>
            <p>
              观察时间：{inventory.data.observed_at} · 当前批次{" "}
              {inventory.data.source_epoch} · 原件 {inventory.data.source_size}{" "}
              字节
            </p>
            {inventory.data.items.map((item) => (
              <section
                className="source-inventory-item"
                key={item.object_ref}
                style={{ marginTop: 16 }}
              >
                <Tag
                  color={
                    item.reconciliation === "unregistered" ||
                    item.reconciliation.includes("mismatch")
                      ? "orange"
                      : undefined
                  }
                >
                  {results[item.reconciliation] || item.reconciliation}
                </Tag>
                <p style={{ overflowWrap: "anywhere" }}>
                  对象指纹：{item.object_ref}
                </p>
                <p>
                  {item.size} 字节 · {item.modified_at}
                </p>
                <p>
                  {item.key_kind === "legacy"
                    ? "旧式对象"
                    : item.key_kind === "generation"
                      ? `批次 ${item.source_epoch}`
                      : "非标准对象"}
                </p>
                {item.registered_id && <p>登记编号：{item.registered_id}</p>}
                {!item.registered_id && (
                  <AdoptSource
                    documentId={documentId}
                    versionId={versionId}
                    sourceEpoch={inventory.data.source_epoch}
                    item={item}
                    scope={scope}
                  />
                )}
              </section>
            ))}
            {!inventory.data.items.length && (
              <Empty description="此页未列出对象" />
            )}
          </>
        )}
      </Drawer>
    </>
  );
}

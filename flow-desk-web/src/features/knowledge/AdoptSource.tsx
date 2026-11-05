import { useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { Alert, Button, Checkbox, Modal } from "antd";
import { api } from "../../shared/api/client";
import type { Models } from "../../shared/api/models";
import { newId } from "../../shared/id";

type Item = Models["SourceInventoryPage"]["items"][number];
const reasons: Record<string, string> = {
  adoption_disabled: "副本登记尚未启用",
  already_registered: "副本已有登记",
  unrecognized: "仅支持标准旧副本",
  size_mismatch: "列表大小异常，需人工调查",
  restore_required: "请先恢复并核对数据库原件，再刷新巡检",
  original_unverified: "数据库原件未通过完整性检查",
  current_generation: "仅支持早于当前批次的副本",
};

export function AdoptSource({
  documentId,
  versionId,
  sourceEpoch,
  item,
  scope,
}: {
  documentId: string;
  versionId: string;
  sourceEpoch: number;
  item: Item;
  scope: string[];
}) {
  const cache = useQueryClient();
  const [selection, setSelection] = useState<{
    token: string;
    epoch: number;
    key: string;
  } | null>(null);
  const [confirmed, setConfirmed] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  return (
    <>
      <p>{reasons[item.adoption_reason] || item.adoption_reason}</p>
      <Button
        disabled={!item.adoption_eligible}
        onClick={() => {
          setSelection({
            token: item.adoption_token,
            epoch: sourceEpoch,
            key: newId(),
          });
          setConfirmed(false);
          setError("");
        }}
      >
        核验并登记副本
      </Button>
      <Modal
        title="确认核验并登记副本"
        open={selection !== null}
        onCancel={() => {
          if (!busy) setSelection(null);
        }}
        footer={
          <Button
            type="primary"
            loading={busy}
            disabled={!confirmed}
            onClick={async () => {
              if (!selection || busy || !confirmed) return;
              setBusy(true);
              setError("");
              try {
                await api(
                  `/documents/${documentId}/versions/${versionId}/source-adoptions`,
                  {
                    method: "POST",
                    headers: { "Idempotency-Key": selection.key },
                    body: JSON.stringify({
                      object_ref: item.object_ref,
                      adoption_token: selection.token,
                      expected_source_epoch: selection.epoch,
                      confirm: true,
                    }),
                  },
                );
                setSelection(null);
                await cache.invalidateQueries({ queryKey: scope });
              } catch (caught) {
                setError((caught as Error).message);
              } finally {
                setBusy(false);
              }
            }}
          >
            确认核验并登记
          </Button>
        }
      >
        <p>
          将读取此副本并核对大小及
          SHA256，匹配后登记为当前版本的旧副本。数据库原件保留；副本清理需另行预览和确认。
        </p>
        <p style={{ overflowWrap: "anywhere" }}>对象指纹：{item.object_ref}</p>
        <p>
          旧批次 {item.source_epoch} · {item.size} 字节
        </p>
        <Checkbox
          checked={confirmed}
          disabled={busy}
          onChange={(event) => setConfirmed(event.target.checked)}
        >
          我已恢复并核对数据库原件，同意登记此副本
        </Checkbox>
        {error && <Alert type="error" title={error} />}
      </Modal>
    </>
  );
}

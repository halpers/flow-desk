import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Alert, Button, Card, Checkbox, InputNumber, Modal, Space } from "antd";
import { api } from "../../shared/api/client";
import type { Models } from "../../shared/api/models";
import { newId } from "../../shared/id";

type Draft = { keep: number; days: number; revision: number; key: string };

export function RetentionPolicy({
  baseId,
  scope,
}: {
  baseId: string;
  scope: string[];
}) {
  const path = `/knowledge-bases/${baseId}/retention-policy`;
  const policy = useQuery({
    queryKey: [...scope, "retention-policy", baseId],
    queryFn: () => api<Models["RetentionPolicyData"]>(path),
  });
  const [draft, setDraft] = useState<Draft | null>(null);
  const [confirmed, setConfirmed] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  function change(field: "keep" | "days", value: number | null) {
    if (!draft || value === null) return;
    setDraft({ ...draft, [field]: value, key: newId() });
    setConfirmed(false);
    setError("");
  }
  return (
    <Card size="small" title="保留策略" style={{ marginTop: 16 }}>
      <p>规则适用于本知识库全部文档。仍需逐版本预览和确认清理。</p>
      {policy.data && (
        <p>
          保留最近 {policy.data.keep_latest} 个版本，至少保存{" "}
          {policy.data.minimum_age_days} 天 · 策略修订 {policy.data.revision}
        </p>
      )}
      {(policy.error || (error && !draft)) && (
        <Alert type="error" title={error || (policy.error as Error).message} />
      )}
      <Space>
        <Button
          disabled={!policy.data || busy}
          onClick={() => {
            if (!policy.data) return;
            setDraft({
              keep: policy.data.keep_latest,
              days: policy.data.minimum_age_days,
              revision: policy.data.revision,
              key: newId(),
            });
            setConfirmed(false);
            setError("");
          }}
        >
          编辑保留策略
        </Button>
        <Button
          disabled={busy}
          onClick={async () => {
            setDraft(null);
            setError("");
            await policy.refetch();
          }}
        >
          刷新保留策略
        </Button>
      </Space>
      <Modal
        title="修改知识库保留策略"
        open={draft !== null}
        confirmLoading={busy}
        okText="确认保存策略"
        okButtonProps={{ disabled: !confirmed }}
        onCancel={() => {
          if (!busy) {
            setDraft(null);
            setError("");
          }
        }}
        onOk={async () => {
          if (!draft || !confirmed) return;
          setBusy(true);
          setError("");
          try {
            await api(path, {
              method: "PUT",
              headers: { "Idempotency-Key": draft.key },
              body: JSON.stringify({
                keep_latest: draft.keep,
                minimum_age_days: draft.days,
                expected_revision: draft.revision,
              }),
            });
            setDraft(null);
            await policy.refetch();
          } catch (reason) {
            setError((reason as Error).message);
          } finally {
            setBusy(false);
          }
        }}
      >
        <p>
          修改基于策略修订 {draft?.revision}
          。保存后旧清理预览失效；已确认的清理任务继续执行。
        </p>
        <p>
          <label>
            最近保留版本数{" "}
            <InputNumber
              aria-label="最近保留版本数"
              min={1}
              max={100}
              precision={0}
              value={draft?.keep}
              disabled={busy}
              onChange={(value) => change("keep", value)}
            />
          </label>
          （1–100）
        </p>
        <p>
          <label>
            最短保留天数{" "}
            <InputNumber
              aria-label="最短保留天数"
              min={1}
              max={3650}
              precision={0}
              value={draft?.days}
              disabled={busy}
              onChange={(value) => change("days", value)}
            />
          </label>
          （1–3650）
        </p>
        <p>
          降低参数可能增加可清理候选；激活、引用、索引中和归档历史保护继续生效。
        </p>
        <Checkbox
          checked={confirmed}
          disabled={busy}
          onChange={(event) => setConfirmed(event.target.checked)}
        >
          我已核对本知识库的保留要求
        </Checkbox>
        {error && <Alert type="error" title={error} />}
      </Modal>
    </Card>
  );
}

import { useEffect, useRef, useState } from "react";
import {
  Alert,
  Button,
  Checkbox,
  Descriptions,
  Drawer,
  InputNumber,
  Space,
  Spin,
  Switch,
} from "antd";
import { api, ApiError } from "../../shared/api/client";
import type { Models } from "../../shared/api/models";
import { newId } from "../../shared/id";

const outcomes: Record<string, string> = {
  never: "尚未检查",
  returned: "超时调查已回待跟进",
  skipped: "本项不可流转，已跳过",
  empty: "本轮没有候选，下一轮从头检查",
  authorization_required: "持续授权已失效，需要管理员重新确认",
};
type Draft = {
  enabled: boolean;
  idle_days: number;
  interval_minutes: number;
  expected_revision: number;
  confirm: boolean;
};
export function InvestigationAutomation({
  onDenied,
}: {
  onDenied: (message: string) => void;
}) {
  const [open, setOpen] = useState(false);
  return (
    <>
      <Button onClick={() => setOpen(true)}>调查自动流转</Button>
      {open && (
        <SchedulePanel onClose={() => setOpen(false)} onDenied={onDenied} />
      )}
    </>
  );
}
function SchedulePanel({
  onClose,
  onDenied,
}: {
  onClose: () => void;
  onDenied: (message: string) => void;
}) {
  const [data, setData] = useState<
    Models["InvestigationAutomationData"] | null
  >(null);
  const [enabled, setEnabled] = useState(false);
  const [days, setDays] = useState(7);
  const [minutes, setMinutes] = useState(60);
  const [confirmed, setConfirmed] = useState(false);
  const [pending, setPending] = useState<{ body: Draft; key: string } | null>(
    null,
  );
  const [loading, setLoading] = useState(false);
  const [saving, setSaving] = useState(false);
  const [revision, setRevision] = useState(0);
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  const mounted = useRef(true);
  const denial = useRef(onDenied);
  denial.current = onDenied;
  useEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
    };
  }, []);
  function rejected(reason: unknown) {
    if (reason instanceof ApiError && [401, 403, 404].includes(reason.status)) {
      setData(null);
      setPending(null);
      setConfirmed(false);
      denial.current("调查自动流转权限已变化，请刷新调查列表。");
      onClose();
    } else
      setError(
        reason instanceof Error ? reason.message : "请求失败，请重新核对策略。",
      );
  }
  useEffect(() => {
    const controller = new AbortController();
    setData(null);
    setLoading(true);
    setConfirmed(false);
    setError("");
    void api<Models["InvestigationAutomationData"]>(
      "/investigation-automation",
      {
        signal: controller.signal,
      },
    )
      .then((result) => {
        if (!controller.signal.aborted) {
          setData(result);
          setEnabled(result.enabled);
          setDays(result.idle_days);
          setMinutes(result.interval_minutes);
        }
      })
      .catch((reason: unknown) => {
        if (!controller.signal.aborted) rejected(reason);
      })
      .finally(() => {
        if (!controller.signal.aborted) setLoading(false);
      });
    return () => controller.abort();
    // A refresh always fetches a newly authorized snapshot, never a shared cache.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [revision]);
  async function save() {
    if (!data || !confirmed || saving) return;
    const request = pending ?? {
      key: newId(),
      body: {
        enabled,
        idle_days: days,
        interval_minutes: minutes,
        expected_revision: data.revision,
        confirm: true,
      },
    };
    setPending(request);
    setSaving(true);
    setError("");
    setNotice("");
    try {
      await api<Models["InvestigationAutomationData"]>(
        "/investigation-automation",
        {
          method: "PUT",
          headers: { "Idempotency-Key": request.key },
          body: JSON.stringify(request.body),
        },
      );
      if (mounted.current) {
        setPending(null);
        setNotice("保存已确认，下面显示重新读取的当前策略。");
        setRevision((value) => value + 1);
      }
    } catch (reason) {
      if (mounted.current) {
        rejected(reason);
        setNotice("保存结果未确认；可重试原保存，或刷新后核对当前策略。");
      }
    } finally {
      if (mounted.current) setSaving(false);
    }
  }
  const frozen = loading || saving || !!pending;
  return (
    <Drawer
      open
      title="超时调查自动回待办"
      size={680}
      closable={!saving}
      maskClosable={!saving}
      onClose={() => {
        if (!saving) onClose();
      }}
    >
      <Space orientation="vertical" size="middle" style={{ width: "100%" }}>
        <Alert
          type="info"
          title="默认关闭，启用后持续生效"
          description="调查中连续超过指定天数未更新的记录将回到待跟进，每个间隔最多检查一项。保留原说明和完整编辑历史。普通退出不停止规则；改密或权限变化后需重新授权。"
        />
        <Button
          disabled={loading || saving}
          onClick={() => {
            setPending(null);
            setNotice("");
            setRevision((value) => value + 1);
          }}
        >
          刷新流转策略
        </Button>
        {loading && <Spin />}
        {error && <Alert type="error" title={error} />}
        {notice && <Alert type="info" title={notice} />}
        {data && (
          <>
            <Descriptions
              column={1}
              size="small"
              items={[
                { key: "revision", label: "策略修订", children: data.revision },
                {
                  key: "status",
                  label: "当前状态",
                  children: data.enabled ? "已启用" : "已关闭",
                },
                {
                  key: "owner",
                  label: "授权管理员",
                  children: data.owner_user_id || "未配置",
                },
                {
                  key: "next",
                  label: "下次检查",
                  children:
                    data.enabled && data.next_run_at
                      ? new Date(data.next_run_at).toLocaleString()
                      : "未启用",
                },
                {
                  key: "checked",
                  label: "上次检查",
                  children: data.last_checked_at
                    ? new Date(data.last_checked_at).toLocaleString()
                    : "尚未检查",
                },
                {
                  key: "outcome",
                  label: "最近结果",
                  children: outcomes[data.last_outcome] || data.last_outcome,
                },
                {
                  key: "ref",
                  label: "最近自动流转对象",
                  children: data.last_object_ref || "无或当前不可访问",
                },
                {
                  key: "revision-result",
                  label: "流转后修订",
                  children: data.last_revision || "无",
                },
              ]}
            />
            {!data.runtime_enabled && (
              <Alert
                type="info"
                title="后台调度尚未开启。策略可保存，需运维开启调度后才会自动检查。"
              />
            )}
            {data.enabled && !data.authorization_valid && (
              <Alert
                type="warning"
                title="持续授权已失效，后台不会继续流转。请当前管理员核对后重新确认保存。"
              />
            )}
            <label>
              启用调查自动流转{" "}
              <Switch
                aria-label="启用调查自动流转"
                checked={enabled}
                disabled={frozen}
                onChange={setEnabled}
              />
            </label>
            <label>
              连续未更新天数{" "}
              <InputNumber
                aria-label="调查自动流转闲置天数"
                min={1}
                max={3650}
                precision={0}
                value={days}
                disabled={frozen}
                onChange={(value) => {
                  if (value !== null) setDays(value);
                }}
              />
            </label>
            <label>
              检查间隔（分钟）{" "}
              <InputNumber
                aria-label="调查自动流转检查间隔"
                min={5}
                max={10080}
                precision={0}
                value={minutes}
                disabled={frozen}
                onChange={(value) => {
                  if (value !== null) setMinutes(value);
                }}
              />
            </label>
            <Checkbox
              checked={confirmed}
              disabled={loading || saving}
              onChange={(event) => setConfirmed(event.target.checked)}
            >
              我已核对当前修订，并确认保存持续流转授权或关闭策略
            </Checkbox>
            <Button
              type="primary"
              loading={saving}
              disabled={!confirmed || loading}
              onClick={() => void save()}
            >
              {pending ? "重试原保存" : "确认保存流转策略"}
            </Button>
          </>
        )}
      </Space>
    </Drawer>
  );
}

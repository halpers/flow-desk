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
  completed: "归档已确认",
  skipped: "本项受保护，已跳过",
  empty: "本轮没有候选，下一轮从头检查",
  pending: "维护待核对",
  unavailable: "暂未确认，后续继续原操作",
  authorization_required: "持续授权已失效，需要管理员重新确认",
};
type Draft = {
  enabled: boolean;
  minimum_age_days: number;
  interval_minutes: number;
  expected_revision: number;
  confirm: boolean;
};
export function RunHistorySchedule({
  onDenied,
}: {
  onDenied: (message: string) => void;
}) {
  const [open, setOpen] = useState(false);
  return (
    <>
      <Button onClick={() => setOpen(true)}>定时归档</Button>
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
  const [data, setData] = useState<Models["RunHistoryScheduleData"] | null>(
    null,
  );
  const [enabled, setEnabled] = useState(false);
  const [days, setDays] = useState(30);
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
      denial.current("定时归档权限已变化，请刷新任务记录。");
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
    void api<Models["RunHistoryScheduleData"]>("/run-history-schedule", {
      signal: controller.signal,
    })
      .then((result) => {
        if (!controller.signal.aborted) {
          setData(result);
          setEnabled(result.enabled);
          setDays(result.minimum_age_days);
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
        minimum_age_days: days,
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
      await api<Models["RunHistoryScheduleData"]>("/run-history-schedule", {
        method: "PUT",
        headers: { "Idempotency-Key": request.key },
        body: JSON.stringify(request.body),
      });
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
      title="定时任务历史归档"
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
          description="仅归档超过保留期且通过全部保护检查的首次维护任务，每个间隔最多检查一项。消息和事件继续可读，恢复不会重启旧任务。普通退出不停止策略；改密或权限变化后需重新授权。"
        />
        <Button
          disabled={loading || saving}
          onClick={() => {
            setPending(null);
            setNotice("");
            setRevision((value) => value + 1);
          }}
        >
          刷新归档策略
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
                  key: "run",
                  label: "最近维护任务",
                  children: data.last_run_id || "无",
                },
                {
                  key: "operation",
                  label: "原操作 ID",
                  children: data.operation.operation_id || "无",
                },
                {
                  key: "operation-status",
                  label: "原操作状态",
                  children: data.operation.operation_id
                    ? data.operation.status === "completed"
                      ? "已完成"
                      : "待核对：请在原任务的历史窗口继续"
                    : "无",
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
                title="持续授权已失效，后台不会继续归档。请当前管理员重新确认保存；已有待确认操作需先人工核对。"
              />
            )}
            <label>
              启用定时归档{" "}
              <Switch
                aria-label="启用定时归档"
                checked={enabled}
                disabled={frozen}
                onChange={setEnabled}
              />
            </label>
            <label>
              至少保留天数{" "}
              <InputNumber
                aria-label="定时归档保留天数"
                min={30}
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
                aria-label="定时归档检查间隔"
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
              我已核对当前修订，并确认保存持续归档授权或关闭策略
            </Checkbox>
            <Button
              type="primary"
              loading={saving}
              disabled={!confirmed || loading}
              onClick={() => void save()}
            >
              {pending ? "重试原保存" : "确认保存归档策略"}
            </Button>
          </>
        )}
      </Space>
    </Drawer>
  );
}

import { useEffect, useRef, useState } from "react";
import { Alert, Button, Descriptions, Drawer, Space, Spin, Tag } from "antd";
import { api, ApiError } from "../../shared/api/client";
import type { Models } from "../../shared/api/models";

export const protectionLabels: Record<string, string> = {
  AGENT_UNAVAILABLE: "执行历史服务暂不可用，修复连接后可继续核对已有操作",
  RUN_HISTORY_LIMIT: "任务历史超过单次维护上限，继续保留当前内容",
  RUN_NOT_TERMINAL: "任务尚未结束，保留恢复所需记录",
  BUSINESS_LEASE_ACTIVE: "业务执行租约仍有效",
  DISPATCH_UNSETTLED: "任务投递尚未完成，或仍有待处理死信",
  EVIDENCE_REFERENCES: "保留知识引用及关联证据",
  CONVERSATION_ACTIVE: "同一会话仍有执行中的任务",
  AGENT_SCOPE_UNAVAILABLE: "执行记录尚未绑定到当前租户，无法确认归档条件",
  COMMAND_QUEUED: "执行命令仍在排队",
  COMMAND_RUNNING: "执行命令仍在运行",
  COMMAND_WAITING: "执行命令仍等待补充或确认",
  COMMAND_LEASE_ACTIVE: "执行命令的租约仍有效",
  CALLBACK_UNACKNOWLEDGED: "仍有未确认的执行回调",
  CALLBACK_LEASE_ACTIVE: "执行回调的投递租约仍有效",
  MODEL_RESERVED: "仍有未结算的模型调用预算",
  EMBEDDING_RESERVED: "仍有未结算的向量调用预算",
  MINIMUM_AGE_NOT_REACHED: "距任务最后更新未满最低保留天数",
  APPROVAL_UNSETTLED: "仍有未完成的动作确认记录",
  INPUT_UNSETTLED: "仍有待补充的信息",
  BUSINESS_REFERENCES: "保留业务动作、回执及资源关联",
  RETRY_CHAIN_REFERENCES: "保留重试来源及后续任务关系",
  MODEL_STATE_UNKNOWN: "存在状态未知的模型调用，需核对后继续保留",
};

const businessLabels: [keyof Models["RunLifecycleData"]["business"], string][] =
  [
    ["message_count", "关联消息"],
    ["event_count", "任务事件"],
    ["action_count", "业务动作"],
    ["approval_count", "确认记录"],
    ["pending_approval_count", "待确认记录"],
    ["pending_input_count", "待补充信息记录"],
    ["receipt_count", "业务回执"],
    ["resource_reference_count", "业务资源关联"],
    ["evidence_count", "引用证据"],
    ["dispatch_count", "投递记录"],
    ["unsettled_dispatch_count", "未完成投递"],
    ["retry_link_count", "重试关联"],
    ["active_conversation_run_count", "同会话活动任务"],
  ];
const agentLabels: [keyof Models["RunLifecycleData"]["agent"], string][] = [
  ["command_count", "执行命令"],
  ["queued_command_count", "排队命令"],
  ["running_command_count", "运行命令"],
  ["waiting_command_count", "等待命令"],
  ["live_command_lease_count", "有效命令租约"],
  ["callback_count", "执行回调"],
  ["unacknowledged_callback_count", "未确认回调"],
  ["live_callback_lease_count", "有效回调租约"],
  ["model_call_count", "模型调用记录"],
  ["reserved_model_call_count", "未结算模型调用"],
  ["embedding_call_count", "向量调用记录"],
  ["reserved_embedding_call_count", "未结算向量调用"],
  ["checkpoint_count", "检查点"],
  ["checkpoint_blob_count", "检查点数据块"],
  ["checkpoint_write_count", "检查点写入记录"],
  ["tool_result_count", "工具结果缓存"],
  ["execution_span_count", "节点观测记录"],
  ["retrieval_attempt_count", "检索记录"],
];

export function RunLifecycle({
  runId,
  onDenied,
}: {
  runId: string;
  onDenied: (message: string) => void;
}) {
  const [open, setOpen] = useState(false);
  return (
    <>
      <Button onClick={() => setOpen(true)}>生命周期预览</Button>
      {open && (
        <LifecyclePreview
          key={runId}
          runId={runId}
          onClose={() => setOpen(false)}
          onDenied={onDenied}
        />
      )}
    </>
  );
}

function LifecyclePreview({
  runId,
  onClose,
  onDenied,
}: {
  runId: string;
  onClose: () => void;
  onDenied: (message: string) => void;
}) {
  const [data, setData] = useState<Models["RunLifecycleData"] | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [reload, setReload] = useState(0);
  const denialHandler = useRef(onDenied);
  denialHandler.current = onDenied;
  useEffect(() => {
    const controller = new AbortController();
    void api<Models["RunLifecycleData"]>(`/runs/${runId}/lifecycle`, {
      signal: controller.signal,
    })
      .then((result) => {
        if (!controller.signal.aborted) setData(result);
      })
      .catch((reason: unknown) => {
        if (controller.signal.aborted) return;
        setData(null);
        if (
          reason instanceof ApiError &&
          [401, 403, 404].includes(reason.status)
        ) {
          denialHandler.current(
            reason.status === 401
              ? "登录已失效，请重新登录后查询任务。"
              : "任务访问权限已变化，请刷新任务列表后重新查询。",
          );
        } else if (
          reason instanceof ApiError &&
          reason.code === "LIFECYCLE_CHANGED"
        ) {
          setError("任务在盘点期间发生变化，请刷新生命周期预览后重新核对。");
        } else {
          setError(
            "生命周期预览暂不可用，请稍后刷新。未确认的条件继续受到保护。",
          );
        }
      })
      .finally(() => {
        if (!controller.signal.aborted) setLoading(false);
      });
    return () => controller.abort();
  }, [runId, reload]);
  return (
    <Drawer title="任务生命周期预览" open onClose={onClose} size={680}>
      <Space orientation="vertical" size="middle" style={{ width: "100%" }}>
        <Alert
          type="info"
          title="仅盘点，不会归档或删除数据"
          description="候选仅供规划，执行前需重新检查。统计来自不同服务的观察时点，不锁定后续归档资格。"
        />
        <p style={{ overflowWrap: "anywhere" }}>任务 ID：{runId}</p>
        <Button
          aria-label="刷新生命周期预览"
          disabled={loading}
          loading={loading}
          onClick={() => {
            setData(null);
            setError("");
            setLoading(true);
            setReload((value) => value + 1);
          }}
        >
          刷新生命周期预览
        </Button>
        {loading && <Spin aria-label="正在读取生命周期预览" />}
        {error && <Alert type="error" title={error} />}
        {!loading && data && (
          <div className="run-lifecycle-data">
            <Descriptions
              column={1}
              size="small"
              items={[
                {
                  key: "observed",
                  label: "业务观察时间",
                  children: new Date(data.observed_at).toLocaleString(),
                },
                {
                  key: "age",
                  label: "最低保留天数",
                  children: data.minimum_age_days,
                },
                {
                  key: "candidate",
                  label: "规划状态",
                  children: (
                    <Tag>
                      {data.archive_candidate ? "归档规划候选" : "继续保留"}
                    </Tag>
                  ),
                },
              ]}
            />
            {data.protection_reasons.length > 0 ? (
              <section aria-label="保护原因">
                <h3>保护原因</h3>
                <ul>
                  {data.protection_reasons.map((reason) => (
                    <li key={reason}>
                      {protectionLabels[reason] ||
                        "存在需继续保留的关联或恢复条件"}
                    </li>
                  ))}
                </ul>
              </section>
            ) : (
              <p>当前未观察到阻止归档规划的条件，仍需在实际执行前重新核对。</p>
            )}
            <Descriptions
              title="业务数据盘点"
              column={1}
              size="small"
              items={[
                ...businessLabels.map(([key, label]) => ({
                  key,
                  label,
                  children: String(data.business[key]),
                })),
                {
                  key: "lease_active",
                  label: "业务租约有效",
                  children: data.business.lease_active ? "是" : "否",
                },
              ]}
            />
            {!data.agent.available ? (
              <Alert
                type="warning"
                title="执行侧盘点不可用，继续保留全部恢复记录"
              />
            ) : (
              <Descriptions
                title="执行数据盘点"
                column={1}
                size="small"
                items={[
                  {
                    key: "observed",
                    label: "执行观察时间",
                    children: new Date(data.agent.observed_at).toLocaleString(),
                  },
                  ...agentLabels.map(([key, label]) => ({
                    key,
                    label,
                    children: String(data.agent[key]),
                  })),
                ]}
              />
            )}
          </div>
        )}
      </Space>
    </Drawer>
  );
}

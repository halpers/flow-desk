import { useRef, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import {
  Alert,
  Button,
  Card,
  Drawer,
  Form,
  Input,
  Modal,
  Select,
  Space,
  Switch,
  Tabs,
  Tag,
} from "antd";
import { api, type User } from "../../shared/api/client";
import type { Models } from "../../shared/api/models";
import { newId } from "../../shared/id";

type Account = Models["ManagedUserPage"]["items"][number];
type Role = Models["ManagedRolePage"]["items"][number];
type Editor =
  | { kind: "account"; row?: Account }
  | { kind: "password"; row: Account }
  | { kind: "role"; row?: Role };
const permissionLabels: Record<string, string> = {
  "conversation:use": "会话办理",
  "order:read": "订单查询",
  "ticket:read": "工单查询",
  "ticket:create": "创建工单",
  "ticket:note": "添加工单备注",
  "ticket:manage": "工单管理",
  "audit:read": "查看审计",
  "run:observe": "租户任务观测",
  "knowledge:manage": "知识管理",
  "team:manage": "团队管理",
  "account:manage": "账号管理",
  "role:manage": "角色管理",
};

export function Accounts({ user }: { user: User }) {
  const cache = useQueryClient();
  const [open, setOpen] = useState(false);
  const [editor, setEditor] = useState<Editor>();
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [cursors, setCursors] = useState([""]);
  const pending = useRef<{ signature: string; key: string } | null>(null);
  const accountsAllowed = user.permissions.includes("account:manage");
  const rolesAllowed = user.permissions.includes("role:manage");
  const cursor = cursors.at(-1)!;
  const roles = useQuery({
    queryKey: ["accounts", "roles"],
    queryFn: () => api<Models["ManagedRolePage"]>("/admin/roles"),
    enabled: open && (accountsAllowed || rolesAllowed),
  });
  const accounts = useQuery({
    queryKey: ["accounts", "users", cursor],
    queryFn: () =>
      api<Models["ManagedUserPage"]>(
        `/admin/users?limit=20&cursor=${encodeURIComponent(cursor)}`,
      ),
    enabled: open && accountsAllowed,
  });
  const roleOptions = roles.data?.items.map((role) => ({
    value: role.id,
    label: `${role.name}${role.enabled ? "" : "（已禁用）"}`,
    disabled: !role.enabled,
  }));
  function closeEditor() {
    setEditor(undefined);
    pending.current = null;
    setError("");
  }
  function edit(next: Editor) {
    pending.current = null;
    setError("");
    setEditor(next);
  }
  async function save(values: Record<string, unknown>) {
    if (!editor || busy) return;
    const row = editor.row;
    const root = editor.kind === "role" ? "/admin/roles" : "/admin/users";
    const path =
      root +
      (row ? `/${row.id}` : "") +
      (editor.kind === "password" ? "/reset-password" : "");
    const body = {
      ...values,
      ...(row ? { expected_revision: row.revision } : {}),
    };
    const signature = path + JSON.stringify(body);
    if (pending.current?.signature !== signature)
      pending.current = { signature, key: newId() };
    setBusy(true);
    setError("");
    try {
      await api(path, {
        method: row && editor.kind !== "password" ? "PUT" : "POST",
        headers: { "Idempotency-Key": pending.current.key },
        body: JSON.stringify(body),
      });
      closeEditor();
      await Promise.all(
        ["accounts", "teams", "knowledge"].map((key) =>
          cache.invalidateQueries({ queryKey: [key] }),
        ),
      );
    } catch (reason) {
      setError((reason as Error).message);
    } finally {
      setBusy(false);
    }
  }
  if (!accountsAllowed && !rolesAllowed) return null;
  const row = editor?.row;
  const title =
    editor?.kind === "password"
      ? "重置账号密码"
      : editor?.kind === "role"
        ? row
          ? "编辑角色"
          : "新增角色"
        : row
          ? "编辑账号"
          : "新增账号";
  return (
    <>
      <Button onClick={() => setOpen(true)}>账号与角色</Button>
      <Drawer
        title="账号与角色管理"
        size={760}
        open={open}
        onClose={() => {
          if (!busy) {
            closeEditor();
            setOpen(false);
          }
        }}
      >
        <p>
          每个账号至少选择一个内置基础角色，可附加多个增量角色。自定义权限不改变团队和知识库的访问范围。
        </p>
        <Alert
          type="info"
          title="账号修改或角色调整会使相关账号的全部会话失效。当前账号请由其他管理员维护。"
        />
        {(roles.isError || accounts.isError) && (
          <Alert type="error" title="加载失败，请刷新重试" />
        )}
        <Button
          onClick={() => cache.invalidateQueries({ queryKey: ["accounts"] })}
        >
          刷新管理数据
        </Button>
        <Tabs
          items={[
            ...(accountsAllowed
              ? [
                  {
                    key: "users",
                    label: "账号",
                    children: (
                      <Space orientation="vertical" style={{ width: "100%" }}>
                        <Button
                          type="primary"
                          disabled={!roles.data}
                          onClick={() => edit({ kind: "account" })}
                        >
                          新增账号
                        </Button>
                        {accounts.data?.items.map((account) => (
                          <Card
                            size="small"
                            key={account.id}
                            title={`${account.display_name} (${account.account})`}
                          >
                            <Space wrap>
                              <Tag>{account.enabled ? "启用" : "停用"}</Tag>
                              <Tag>{account.role}</Tag>
                              <span>修订 {account.revision}</span>
                            </Space>
                            <p>
                              {account.role_ids
                                .map(
                                  (id) =>
                                    roles.data?.items.find(
                                      (item) => item.id === id,
                                    )?.name || "角色加载中",
                                )
                                .join("、")}
                            </p>
                            <Space>
                              <Button
                                disabled={account.id === user.id}
                                onClick={() =>
                                  edit({ kind: "account", row: account })
                                }
                              >
                                编辑账号
                              </Button>
                              <Button
                                disabled={account.id === user.id}
                                onClick={() =>
                                  edit({ kind: "password", row: account })
                                }
                              >
                                重置密码
                              </Button>
                            </Space>
                          </Card>
                        ))}
                        <Space>
                          <Button
                            disabled={cursors.length === 1}
                            onClick={() => setCursors(cursors.slice(0, -1))}
                          >
                            上一页
                          </Button>
                          <Button
                            disabled={!accounts.data?.has_more}
                            onClick={() =>
                              setCursors([
                                ...cursors,
                                accounts.data!.next_cursor,
                              ])
                            }
                          >
                            下一页
                          </Button>
                        </Space>
                      </Space>
                    ),
                  },
                ]
              : []),
            {
              key: "roles",
              label: "角色",
              children: (
                <Space orientation="vertical" style={{ width: "100%" }}>
                  {rolesAllowed && (
                    <Button
                      type="primary"
                      onClick={() => edit({ kind: "role" })}
                    >
                      新增角色
                    </Button>
                  )}
                  {roles.data?.items.map((role) => (
                    <Card size="small" key={role.id} title={role.name}>
                      <Space wrap>
                        <Tag>
                          {role.builtin_role
                            ? "内置基础角色"
                            : "自定义增量角色"}
                        </Tag>
                        <Tag>{role.enabled ? "启用" : "停用"}</Tag>
                      </Space>
                      <p>
                        {role.permissions
                          .map(
                            (permission) =>
                              permissionLabels[permission] || permission,
                          )
                          .join("、") || "没有额外权限"}
                      </p>
                      {rolesAllowed && !role.builtin_role && (
                        <Button
                          onClick={() => edit({ kind: "role", row: role })}
                        >
                          编辑角色
                        </Button>
                      )}
                    </Card>
                  ))}
                </Space>
              ),
            },
          ]}
        />
      </Drawer>
      <Modal
        title={title}
        open={!!editor}
        footer={null}
        destroyOnHidden
        closable={!busy}
        maskClosable={!busy}
        onCancel={closeEditor}
      >
        {editor && (
          <Form
            key={`${editor.kind}:${row?.id || "new"}:${row?.revision || 0}`}
            layout="vertical"
            disabled={busy}
            initialValues={
              editor.kind === "password"
                ? {}
                : row ||
                  (editor.kind === "role"
                    ? { enabled: true, permissions: [] }
                    : {
                        enabled: true,
                        role_ids: roles.data?.items
                          .filter((role) => role.builtin_role === "operator")
                          .map((role) => role.id),
                      })
            }
            onFinish={save}
          >
            {editor.kind === "role" ? (
              <>
                <Form.Item
                  name="name"
                  label="角色名称"
                  rules={[{ required: true, whitespace: true, max: 80 }]}
                >
                  <Input maxLength={80} />
                </Form.Item>
                <Form.Item name="permissions" label="额外权限">
                  <Select
                    mode="multiple"
                    virtual={false}
                    optionFilterProp="label"
                    options={roles.data?.extra_permissions.map(
                      (permission) => ({
                        value: permission,
                        label: permissionLabels[permission] || permission,
                      }),
                    )}
                  />
                </Form.Item>
                {row && (
                  <Form.Item
                    name="enabled"
                    label="启用角色"
                    valuePropName="checked"
                  >
                    <Switch />
                  </Form.Item>
                )}
              </>
            ) : (
              <>
                {editor.kind === "account" && (
                  <>
                    {!row && (
                      <Form.Item
                        name="account"
                        label="新账号名"
                        rules={[
                          {
                            required: true,
                            pattern: /^[a-z][a-z0-9._-]{2,99}$/,
                            message:
                              "3–100 位，以小写字母开头，允许小写字母、数字、点、下划线或横线",
                          },
                        ]}
                      >
                        <Input autoComplete="off" maxLength={100} />
                      </Form.Item>
                    )}
                    <Form.Item
                      name="display_name"
                      label="显示名称"
                      rules={[{ required: true, whitespace: true, max: 80 }]}
                    >
                      <Input maxLength={80} />
                    </Form.Item>
                    <Form.Item
                      name="role_ids"
                      label="分配角色"
                      rules={[
                        { required: true, type: "array", min: 1, max: 8 },
                      ]}
                    >
                      <Select
                        mode="multiple"
                        virtual={false}
                        optionFilterProp="label"
                        options={roleOptions}
                      />
                    </Form.Item>
                    {row && (
                      <Form.Item
                        name="enabled"
                        label="启用账号"
                        valuePropName="checked"
                      >
                        <Switch />
                      </Form.Item>
                    )}
                  </>
                )}
                {(editor.kind === "password" || !row) && (
                  <>
                    <p>
                      密码需 12–72 UTF-8
                      字节，至少包含一个字母和数字。请通过受控渠道交付本人。
                    </p>
                    <Form.Item
                      name="password"
                      label="设置新密码"
                      rules={[
                        { required: true },
                        {
                          validator: (_, value: string) =>
                            !value ||
                            (new TextEncoder().encode(value).length >= 12 &&
                              new TextEncoder().encode(value).length <= 72 &&
                              /\p{L}/u.test(value) &&
                              /\p{Nd}/u.test(value))
                              ? Promise.resolve()
                              : Promise.reject(
                                  new Error("密码长度或复杂度不符合要求"),
                                ),
                        },
                      ]}
                    >
                      <Input.Password autoComplete="new-password" />
                    </Form.Item>
                  </>
                )}
              </>
            )}
            {error && <Alert type="error" title={error} />}
            <Button type="primary" htmlType="submit" loading={busy}>
              保存
              {editor.kind === "password"
                ? "新密码"
                : editor.kind === "role"
                  ? "角色"
                  : "账号"}
            </Button>
          </Form>
        )}
      </Modal>
    </>
  );
}

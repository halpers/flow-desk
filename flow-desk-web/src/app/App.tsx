import { useEffect, useState } from "react";
import { Alert, Button, Card, Form, Input, Space, Spin, Tag } from "antd";
import { api, login, logout, refresh, type User } from "../shared/api/client";
import {
  clearSession,
  passwordReauthenticationRequired,
} from "../shared/api/client";
import { useQueryClient } from "@tanstack/react-query";
import { Workspace } from "../features/workspace/Workspace";
import { Knowledge } from "../features/knowledge/Knowledge";
import { Teams } from "../features/teams/Teams";
import { Accounts } from "../features/accounts/Accounts";
import { SelfPassword } from "../features/accounts/SelfPassword";

export function App() {
  const cache = useQueryClient();
  const [user, setUser] = useState<User | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [sessionNotice, setSessionNotice] = useState(() =>
    passwordReauthenticationRequired()
      ? "修改密码结果需要重新核对。请先使用新密码登录；如未成功，再尝试原密码或联系管理员。"
      : "",
  );
  const signedOut = (notice: string) => {
    clearSession();
    void cache.cancelQueries();
    cache.clear();
    setSessionNotice(notice);
    setUser(null);
  };
  const load = async () => setUser(await api<User>("/auth/me"));
  useEffect(() => {
    const expired = () => {
      clearSession();
      cache.clear();
      setUser(null);
    };
    window.addEventListener("flowdesk:session-expired", expired);
    return () =>
      window.removeEventListener("flowdesk:session-expired", expired);
  }, [cache]);
  useEffect(() => {
    if (passwordReauthenticationRequired()) {
      clearSession();
      setLoading(false);
      return;
    }
    refresh()
      .then(load)
      .catch(() => {})
      .finally(() => setLoading(false));
  }, []);
  if (loading)
    return (
      <div className="shell">
        <Spin aria-label="正在恢复会话" />
      </div>
    );
  if (!user)
    return (
      <main className="shell login">
        <div className="brand">
          FlowDesk <span className="muted">智办台</span>
        </div>
        <p className="subtitle">让每一次服务都有依据、有进展、有回执。</p>
        <Card>
          <Form
            layout="vertical"
            onFinish={async (values) => {
              setError("");
              setLoading(true);
              try {
                await login(values.account, values.password);
                await load();
              } catch (e) {
                setError((e as Error).message);
              } finally {
                setLoading(false);
              }
            }}
          >
            <Form.Item
              name="account"
              label="工作账号"
              rules={[{ required: true }]}
            >
              <Input autoComplete="username" />
            </Form.Item>
            <Form.Item
              name="password"
              label="密码"
              rules={[{ required: true }]}
            >
              <Input.Password autoComplete="current-password" />
            </Form.Item>
            {sessionNotice && <Alert title={sessionNotice} type="info" />}
            {error && <Alert title={error} type="error" />}
            <Button type="primary" htmlType="submit" block>
              登录工作台
            </Button>
          </Form>
        </Card>
      </main>
    );
  return (
    <main className="shell workbench">
      <header className="topbar">
        <div>
          <div className="brand">
            FlowDesk <span className="muted">/ 智办台</span>
          </div>
          <p className="subtitle">从问题到解决，让流程清晰可见。</p>
        </div>
        <Space>
          <SelfPassword
            key={`password:${user.tenant_id}:${user.id}`}
            onSignedOut={signedOut}
          />
          <Accounts key={`accounts:${user.tenant_id}:${user.id}`} user={user} />
          <Teams key={`teams:${user.tenant_id}:${user.id}`} user={user} />
          <Knowledge key={`${user.tenant_id}:${user.id}`} user={user} />
          <Tag>{user.role}</Tag>
          <span>{user.display_name}</span>
          <Button
            onClick={async () => {
              try {
                await logout();
              } finally {
                await cache.cancelQueries();
                cache.clear();
                setUser(null);
              }
            }}
          >
            退出
          </Button>
        </Space>
      </header>
      <Workspace key={`${user.tenant_id}:${user.id}`} user={user} />
    </main>
  );
}

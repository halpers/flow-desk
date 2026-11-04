import { useRef, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Alert, Button, Card, Drawer, Input, Select, Space, Tag } from "antd";
import { api, type User } from "../../shared/api/client";
import type { Models } from "../../shared/api/models";

export function TeamScope({
  value,
  disabled,
  onChange,
}: {
  value: string;
  disabled?: boolean;
  onChange: (team: string) => void;
}) {
  const teams = useQuery({
    queryKey: ["teams"],
    queryFn: () => api<Models["TeamPage"]>("/teams"),
  });
  return (
    <Select
      aria-label="资源团队范围"
      style={{ minWidth: 210 }}
      value={value}
      disabled={disabled || teams.isPending || teams.isError}
      options={[
        { value: "", label: "租户内共享" },
        ...(teams.data?.items.map((team) => ({
          value: team.id,
          label: team.name,
        })) || []),
      ]}
      onChange={onChange}
    />
  );
}

export function Teams({ user }: { user: User }) {
  const cache = useQueryClient();
  const [open, setOpen] = useState(false);
  const [selected, setSelected] = useState("");
  const [name, setName] = useState("");
  const [person, setPerson] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const pending = useRef<{ signature: string; key: string } | null>(null);
  const admin = user.permissions.includes("team:manage");
  const teams = useQuery({
    queryKey: ["teams"],
    queryFn: () => api<Models["TeamPage"]>("/teams"),
    enabled: open,
  });
  const team = teams.data?.items.find((item) => item.id === selected);
  const directory = useQuery({
    queryKey: ["teams", "directory"],
    queryFn: () => api<Models["TeamUserPage"]>("/team-users"),
    enabled: open && admin,
  });
  const members = useQuery({
    queryKey: ["teams", selected, "members"],
    queryFn: () => api<Models["TeamUserPage"]>(`/teams/${selected}/members`),
    enabled: open && admin && !!team,
  });
  async function mutate(path: string, body: object, method = "PUT") {
    const signature = path + JSON.stringify(body);
    if (pending.current?.signature !== signature)
      pending.current = { signature, key: crypto.randomUUID() };
    setError("");
    setBusy(true);
    try {
      await api(path, {
        method,
        headers: { "Idempotency-Key": pending.current.key },
        body: JSON.stringify(body),
      });
      pending.current = null;
      await cache.invalidateQueries({ queryKey: ["teams"] });
      await cache.invalidateQueries({ queryKey: ["tickets"] });
      await cache.invalidateQueries({ queryKey: ["knowledge"] });
      setName("");
      setPerson("");
    } catch (reason) {
      setError((reason as Error).message);
    } finally {
      setBusy(false);
    }
  }
  return (
    <>
      <Button aria-label="团队" onClick={() => setOpen(true)}>
        团队
      </Button>
      <Drawer
        title="团队与成员"
        size={640}
        open={open}
        onClose={() => setOpen(false)}
      >
        <p>
          团队范围内共享工单与知识库，角色权限继续生效。移除成员后，新操作和历史内容会重新检查权限。
        </p>
        {(teams.isError || directory.isError || members.isError) && (
          <Alert type="error" title="团队数据加载失败，请重试" />
        )}
        {error && <Alert type="error" title={error} />}
        {admin && (
          <Card title="创建团队" size="small">
            <Space.Compact>
              <Input
                aria-label="团队名称"
                maxLength={80}
                value={name}
                onChange={(event) => setName(event.target.value)}
              />
              <Button
                loading={busy}
                disabled={!name.trim()}
                onClick={() => mutate("/teams", { name }, "POST")}
              >
                创建团队
              </Button>
            </Space.Compact>
          </Card>
        )}
        <Select
          aria-label="选择团队"
          style={{ width: "100%", margin: "16px 0" }}
          value={selected || undefined}
          placeholder="选择团队"
          options={teams.data?.items.map((item) => ({
            value: item.id,
            label: item.name,
          }))}
          onChange={setSelected}
        />
        {!teams.isPending && !teams.data?.items.length && (
          <p>当前没有可访问的团队。</p>
        )}
        {team && (
          <Card title={team.name} size="small">
            <p>成员修订 {team.revision}</p>
            {admin && (
              <>
                <Space wrap>
                  <Select
                    aria-label="添加团队成员"
                    style={{ minWidth: 210 }}
                    value={person || undefined}
                    placeholder="选择本租户账号"
                    options={directory.data?.items
                      .filter(
                        (item) =>
                          item.enabled &&
                          !members.data?.items.some(
                            (member) => member.id === item.id,
                          ),
                      )
                      .map((item) => ({
                        value: item.id,
                        label: `${item.display_name} (${item.role})`,
                      }))}
                    onChange={setPerson}
                  />
                  <Button
                    disabled={!person || busy || members.isPending}
                    onClick={() =>
                      mutate(`/teams/${team.id}/members/${person}`, {
                        member: true,
                        expected_revision: team.revision,
                      })
                    }
                  >
                    添加成员
                  </Button>
                </Space>
                {members.data?.items.map((member) => (
                  <div key={member.id} style={{ marginTop: 12 }}>
                    <Space>
                      <span>{member.display_name}</span>
                      <Tag>{member.role}</Tag>
                      {!member.enabled && <Tag>已停用</Tag>}
                      <Button
                        danger
                        disabled={busy}
                        onClick={() =>
                          mutate(`/teams/${team.id}/members/${member.id}`, {
                            member: false,
                            expected_revision: team.revision,
                          })
                        }
                      >
                        移除成员
                      </Button>
                    </Space>
                  </div>
                ))}
              </>
            )}
          </Card>
        )}
      </Drawer>
    </>
  );
}

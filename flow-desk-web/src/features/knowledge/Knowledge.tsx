import { useRef, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import {
  Alert,
  Button,
  Card,
  Drawer,
  Empty,
  Input,
  Modal,
  Select,
  Space,
  Tag,
} from "antd";
import { api, type User } from "../../shared/api/client";
import type { Models } from "../../shared/api/models";

type Document = Models["DocumentPage"]["items"][number];
const roleOptions = [
  { value: "operator", label: "操作员" },
  { value: "supervisor", label: "主管" },
  { value: "admin", label: "管理员" },
];
const allRoles = roleOptions.map((item) => item.value);
const post = (body: unknown, key?: string, method = "POST"): RequestInit => ({
  method,
  headers: key ? { "Idempotency-Key": key } : {},
  body: JSON.stringify(body),
});

function DocumentVersions({
  document,
  scope,
  refresh,
}: {
  document: Document;
  scope: string[];
  refresh: () => Promise<void>;
}) {
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const versions = useQuery({
    queryKey: [...scope, "versions", document.id],
    queryFn: () =>
      api<Models["VersionPage"]>(`/documents/${document.id}/versions`),
    refetchInterval: (query) =>
      query.state.data?.items.some((item) => item.index_status === "pending")
        ? 2000
        : false,
  });
  const uploadKey = useRef(crypto.randomUUID());
  const change = async (path: string, body: unknown) => {
    setBusy(true);
    setError("");
    try {
      await api(path, post(body));
      await refresh();
      await versions.refetch();
    } catch (reason) {
      setError((reason as Error).message);
    } finally {
      setBusy(false);
    }
  };
  return (
    <div className="knowledge-document">
      <Space wrap>
        <strong>{document.title}</strong>
        <Tag>
          {document.status === "active"
            ? "已发布"
            : document.status === "withdrawn"
              ? "已撤回"
              : "草稿"}
        </Tag>
        <small>修订 {document.version}</small>
      </Space>
      {versions.isError && <Alert type="error" title="版本加载失败" />}
      {versions.data?.items.map((version) => (
        <div className="knowledge-version" key={version.id}>
          <span>
            {version.filename} · {version.id.slice(0, 8)} ·{" "}
            {version.index_status === "ready"
              ? `${version.chunk_count} 个片段`
              : version.index_status === "pending"
                ? "正在索引"
                : "索引失败"}
          </span>
          <Button
            size="small"
            loading={busy}
            disabled={
              version.index_status !== "ready" ||
              document.active_version_id === version.id
            }
            onClick={() =>
              change(`/documents/${document.id}/activate`, {
                version_id: version.id,
                expected_version: document.version,
              })
            }
          >
            {document.active_version_id === version.id
              ? "当前版本"
              : "激活版本"}
          </Button>
        </div>
      ))}
      <Space wrap>
        <label>
          上传新版本{" "}
          <input
            aria-label={`上传${document.title}新版本`}
            type="file"
            accept=".txt,.md"
            disabled={busy}
            onChange={async (event) => {
              const element = event.currentTarget;
              const file = element.files?.[0];
              if (!file) return;
              setBusy(true);
              setError("");
              try {
                if (file.size > 256 * 1024)
                  throw new Error("文件不能超过 256 KiB");
                await api(
                  `/documents/${document.id}/versions`,
                  post(
                    { filename: file.name, content: await file.text() },
                    uploadKey.current,
                  ),
                );
                uploadKey.current = crypto.randomUUID();
                await versions.refetch();
              } catch (reason) {
                setError((reason as Error).message);
              } finally {
                setBusy(false);
                element.value = "";
              }
            }}
          />
        </label>
        <Button
          danger
          size="small"
          disabled={document.status !== "active" || busy}
          onClick={() =>
            change(`/documents/${document.id}/withdraw`, {
              expected_version: document.version,
            })
          }
        >
          撤回文档
        </Button>
      </Space>
      {error && <Alert title={error} type="error" />}
    </div>
  );
}

export function Knowledge({ user }: { user: User }) {
  const cache = useQueryClient();
  const scope = ["knowledge", user.tenant_id, user.id];
  const admin = user.role === "admin";
  const [open, setOpen] = useState(false);
  const [baseId, setBaseId] = useState("");
  const [name, setName] = useState("");
  const [title, setTitle] = useState("");
  const [roles, setRoles] = useState(allRoles);
  const [file, setFile] = useState<File | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const key = useRef(crypto.randomUUID());
  const bases = useQuery({
    queryKey: [...scope, "bases"],
    queryFn: () => api<Models["KnowledgeBasePage"]>("/knowledge-bases"),
    enabled: open,
  });
  const selected = baseId || bases.data?.items[0]?.id || "";
  const base = bases.data?.items.find((item) => item.id === selected);
  const documents = useQuery({
    queryKey: [...scope, "documents", selected],
    queryFn: () =>
      api<Models["DocumentPage"]>(`/knowledge-bases/${selected}/documents`),
    enabled: open && !!selected,
  });
  const refresh = async () => {
    await cache.invalidateQueries({ queryKey: scope });
    await cache.invalidateQueries({
      queryKey: ["workspace", user.tenant_id, user.id],
    });
  };
  const perform = async (action: () => Promise<void>) => {
    setError("");
    setBusy(true);
    try {
      await action();
      key.current = crypto.randomUUID();
      await refresh();
    } catch (reason) {
      setError((reason as Error).message);
    } finally {
      setBusy(false);
    }
  };
  return (
    <>
      <Button onClick={() => setOpen(true)}>知识库</Button>
      <Drawer
        title="政策知识库"
        open={open}
        onClose={() => setOpen(false)}
        size={720}
      >
        <p className="muted">
          答复只引用当前授权的已发布版本。撤回文档后，引用访问将立即受限。
        </p>
        {bases.isError && <Alert type="error" title="知识库加载失败" />}
        <Select
          aria-label="选择知识库"
          style={{ width: "100%" }}
          value={selected || undefined}
          placeholder="选择知识库"
          options={bases.data?.items.map((item) => ({
            value: item.id,
            label: item.name,
          }))}
          onChange={setBaseId}
        />
        {admin && (
          <Card size="small" title="新建知识库" style={{ marginTop: 16 }}>
            <Space.Compact style={{ width: "100%" }}>
              <Input
                aria-label="知识库名称"
                placeholder="知识库名称"
                maxLength={120}
                value={name}
                onChange={(event) => {
                  setName(event.target.value);
                  key.current = crypto.randomUUID();
                }}
              />
              <Button
                loading={busy}
                disabled={!name.trim()}
                onClick={() =>
                  perform(async () => {
                    await api(
                      "/knowledge-bases",
                      post({ name, read_roles: allRoles }, key.current),
                    );
                    setName("");
                  })
                }
              >
                新建知识库
              </Button>
            </Space.Compact>
          </Card>
        )}
        {admin && base && (
          <Card size="small" title="访问权限" style={{ marginTop: 16 }}>
            <p>
              当前角色：{base.read_roles.join("、")} · 权限修订{" "}
              {base.acl_revision}
            </p>
            <Select
              aria-label="知识库可读角色"
              mode="multiple"
              style={{ width: "100%" }}
              value={roles}
              options={roleOptions}
              onChange={setRoles}
            />
            <Button
              style={{ marginTop: 8 }}
              loading={busy}
              disabled={!roles.includes("admin")}
              onClick={() =>
                perform(async () => {
                  await api(
                    `/knowledge-bases/${base.id}`,
                    post(
                      {
                        read_roles: roles,
                        expected_revision: base.acl_revision,
                      },
                      undefined,
                      "PATCH",
                    ),
                  );
                })
              }
            >
              保存访问权限
            </Button>
          </Card>
        )}
        {admin && selected && (
          <Card size="small" title="上传政策文档" style={{ marginTop: 16 }}>
            <p className="muted">
              UTF-8 TXT / Markdown，最多 256 KiB、20,000
              字符。索引完成后需手动激活。
            </p>
            <Input
              aria-label="文档标题"
              placeholder="文档标题"
              value={title}
              maxLength={120}
              onChange={(event) => {
                setTitle(event.target.value);
                key.current = crypto.randomUUID();
              }}
            />
            <input
              style={{ margin: "12px 0" }}
              aria-label="知识文件"
              type="file"
              accept=".txt,.md"
              onChange={(event) => {
                setFile(event.target.files?.[0] || null);
                key.current = crypto.randomUUID();
              }}
            />
            <Button
              loading={busy}
              disabled={!file || !title.trim()}
              onClick={() =>
                perform(async () => {
                  if (!file) return;
                  if (file.size > 256 * 1024)
                    throw new Error("文件不能超过 256 KiB");
                  await api(
                    `/knowledge-bases/${selected}/documents`,
                    post(
                      {
                        title,
                        filename: file.name,
                        content: await file.text(),
                      },
                      key.current,
                    ),
                  );
                  setTitle("");
                })
              }
            >
              上传并索引
            </Button>
          </Card>
        )}
        {error && (
          <Alert style={{ marginTop: 12 }} type="error" title={error} />
        )}
        {documents.isError && <Alert type="error" title="文档加载失败" />}
        {documents.data?.items.map((document) =>
          admin ? (
            <DocumentVersions
              key={document.id}
              document={document}
              scope={scope}
              refresh={refresh}
            />
          ) : (
            <Card key={document.id} size="small" style={{ marginTop: 12 }}>
              {document.title} ·{" "}
              {document.status === "active" ? "已发布" : "未发布"}
            </Card>
          ),
        )}
        {!documents.data?.items.length && <Empty description="暂无政策文档" />}
      </Drawer>
    </>
  );
}

export function Citation({ id, title }: { id: string; title: string }) {
  const [open, setOpen] = useState(false);
  const [data, setData] = useState<Models["EvidenceData"] | null>(null);
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(false);
  return (
    <>
      <Button
        type="link"
        size="small"
        loading={loading}
        onClick={async () => {
          setOpen(true);
          setData(null);
          setError("");
          setLoading(true);
          try {
            setData(await api(`/citations/${encodeURIComponent(id)}`));
          } catch (reason) {
            setError((reason as Error).message);
          } finally {
            setLoading(false);
          }
        }}
      >
        依据：{title}
      </Button>
      <Modal
        title={data?.title || "政策引用"}
        open={open}
        onCancel={() => setOpen(false)}
        footer={null}
      >
        {error && <Alert type="warning" title={error} />}
        {data && (
          <>
            <p className="citation-excerpt">{data.excerpt}</p>
            <small className="muted">
              版本 {data.version_id} · 片段 {data.chunk_id}
            </small>
          </>
        )}
      </Modal>
    </>
  );
}

import { useRef, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import {
  Alert,
  Button,
  Card,
  Checkbox,
  Drawer,
  Empty,
  Input,
  Modal,
  Select,
  Space,
  Tag,
} from "antd";
import { api, type User } from "../../shared/api/client";
import { newId } from "../../shared/id";
import type { Models } from "../../shared/api/models";
import { TeamScope } from "../teams/Teams";
import { indexErrors, readKnowledgeFile } from "./files";

type Document = Models["DocumentPage"]["items"][number];
const roleOptions = [
  { value: "operator", label: "操作员" },
  { value: "supervisor", label: "主管" },
  { value: "admin", label: "管理员" },
];
const allRoles = roleOptions.map((item) => item.value);
const retentionReasons: Record<string, string> = {
  active: "当前激活版本",
  referenced: "已被任务引用",
  recent: "最近版本保留",
  too_young: "未满最短保留期",
  archive_history: "存在归档历史，原件受保护",
  index_busy: "索引或投递尚未完成",
  cleanup_pending: "清理中，可稍后刷新",
  purged: "内容已清理，元数据保留",
};
const ocrOptions = [
  { value: "off", label: "仅文本层" },
  { value: "auto", label: "自动识别无文字页" },
  { value: "force", label: "整页扫描识别" },
];
const layoutOptions = [
  { value: "plain", label: "原始顺序" },
  { value: "columns", label: "双栏先左后右" },
  { value: "table", label: "表格行列对齐" },
];
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
  const [retention, setRetention] = useState<Models["RetentionPlan"] | null>(
    null,
  );
  const [cleanup, setCleanup] = useState<Models["RetentionItem"] | null>(null);
  const [cleanupConfirmed, setCleanupConfirmed] = useState(false);
  const cleanupKeys = useRef(new Map<string, string>());
  const [ocrMode, setOcrMode] = useState("off");
  const [pdfLayout, setPdfLayout] = useState("plain");
  const [review, setReview] = useState<{
    id: string;
    expected: number;
    kind: "ocr" | "layout";
  } | null>(null);
  const [confirmed, setConfirmed] = useState(false);
  const [preview, setPreview] = useState<Models["VersionPagesData"] | null>(
    null,
  );
  const versions = useQuery({
    queryKey: [...scope, "versions", document.id],
    queryFn: () =>
      api<Models["VersionPage"]>(`/documents/${document.id}/versions`),
    refetchInterval: (query) =>
      query.state.data?.items.some(
        (item) =>
          item.index_status === "pending" ||
          item.cleanup_state === "cleanup_pending",
      )
        ? 2000
        : false,
  });
  const uploadKey = useRef(newId());
  const uploadPayload = useRef("");
  const download = async (versionId: string) => {
    setBusy(true);
    setError("");
    try {
      const source = await api<Models["OriginalSourceData"]>(
        `/documents/${document.id}/versions/${versionId}/source`,
      );
      const bytes = Uint8Array.from(atob(source.content_base64), (value) =>
        value.charCodeAt(0),
      );
      const url = URL.createObjectURL(
        new Blob([bytes], { type: "application/octet-stream" }),
      );
      const anchor = window.document.createElement("a");
      anchor.href = url;
      anchor.download = source.filename;
      anchor.click();
      window.setTimeout(() => URL.revokeObjectURL(url), 1000);
    } catch (reason) {
      setError((reason as Error).message);
    } finally {
      setBusy(false);
    }
  };
  const change = async (path: string, body: unknown) => {
    setBusy(true);
    setError("");
    try {
      await api(path, post(body));
      await refresh();
      await versions.refetch();
      return true;
    } catch (reason) {
      setError((reason as Error).message);
      return false;
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
        <Button
          size="small"
          disabled={busy}
          onClick={() => void versions.refetch()}
        >
          刷新版本
        </Button>
        <Button
          size="small"
          disabled={busy}
          onClick={async () => {
            setError("");
            try {
              setRetention(await api(`/documents/${document.id}/retention`));
            } catch (reason) {
              setError((reason as Error).message);
            }
          }}
        >
          版本保留与清理
        </Button>
      </Space>
      {versions.isError && <Alert type="error" title="版本加载失败" />}
      {versions.data?.items.map((version) => (
        <div className="knowledge-version" key={version.id}>
          <span>
            {version.filename} · {version.id.slice(0, 8)} ·{" "}
            {version.cleanup_state === "purged"
              ? "内容已清理"
              : version.cleanup_state === "cleanup_pending"
                ? "正在清理"
                : version.index_status === "ready"
                  ? `${version.chunk_count} 个片段`
                  : version.index_status === "pending"
                    ? "正在索引"
                    : indexErrors[version.error_code] || "索引失败"}
            {version.source_format === "pdf" &&
              version.index_status === "ready" &&
              ` · ${version.page_count} 页（${version.text_page_count} 页有文字）`}
            {version.ocr_page_count > 0 &&
              ` · OCR ${version.ocr_page_count} 页，激活前需核对`}
            {version.source_format === "pdf" &&
              ` · ${ocrOptions.find((option) => option.value === version.ocr_mode)?.label || "仅文本层"}`}
            {version.source_format === "pdf" &&
              ` · ${layoutOptions.find((option) => option.value === version.pdf_layout)?.label || "原始顺序"}`}
          </span>
          <Button
            size="small"
            disabled={busy || version.cleanup_state !== "retained"}
            onClick={() => void download(version.id)}
          >
            下载原文件
          </Button>
          {version.source_format === "pdf" &&
            version.cleanup_state === "retained" && (
              <Button
                size="small"
                disabled={busy}
                onClick={async () => {
                  setBusy(true);
                  setError("");
                  try {
                    await api(
                      `/documents/${document.id}/versions/${version.id}/${version.source_storage === "s3" ? "restore-source" : "archive"}`,
                      post({}, newId()),
                    );
                    await versions.refetch();
                  } catch (reason) {
                    setError((reason as Error).message);
                  } finally {
                    setBusy(false);
                  }
                }}
              >
                {version.source_storage === "s3"
                  ? "恢复至数据库"
                  : "归档原文件"}
              </Button>
            )}
          {version.source_format === "pdf" &&
            version.cleanup_state === "retained" &&
            version.index_status === "ready" && (
              <Button
                size="small"
                onClick={async () => {
                  setError("");
                  try {
                    setPreview(
                      await api(
                        `/documents/${document.id}/versions/${version.id}/pages`,
                      ),
                    );
                  } catch (reason) {
                    setError((reason as Error).message);
                  }
                }}
              >
                预览提取文本
              </Button>
            )}
          <Button
            size="small"
            loading={busy}
            disabled={
              version.index_status !== "ready" ||
              version.cleanup_state !== "retained" ||
              document.active_version_id === version.id
            }
            onClick={() => {
              if (
                version.ocr_page_count > 0 ||
                (version.pdf_layout && version.pdf_layout !== "plain")
              ) {
                setConfirmed(false);
                setReview({
                  id: version.id,
                  expected: document.version,
                  kind: version.ocr_page_count > 0 ? "ocr" : "layout",
                });
                return;
              }
              void change(`/documents/${document.id}/activate`, {
                version_id: version.id,
                expected_version: document.version,
              });
            }}
          >
            {document.active_version_id === version.id
              ? "当前版本"
              : "激活版本"}
          </Button>
        </div>
      ))}
      <Space wrap>
        <Select
          aria-label={`${document.title} PDF 识别方式`}
          value={ocrMode}
          options={ocrOptions}
          onChange={(value) => {
            setOcrMode(value);
            if (value !== "off") setPdfLayout("plain");
          }}
          disabled={busy}
          style={{ width: 200 }}
        />
        <Select
          aria-label={`${document.title} PDF 阅读方式`}
          value={pdfLayout}
          options={layoutOptions}
          onChange={setPdfLayout}
          disabled={busy || ocrMode !== "off"}
          style={{ width: 180 }}
        />
        <label>
          上传新版本{" "}
          <input
            aria-label={`上传${document.title}新版本`}
            type="file"
            accept=".txt,.md,.pdf"
            disabled={busy}
            onChange={async (event) => {
              const element = event.currentTarget;
              const file = element.files?.[0];
              if (!file) return;
              setBusy(true);
              setError("");
              try {
                const body = await readKnowledgeFile(file, ocrMode, pdfLayout);
                const serialized = JSON.stringify(body);
                if (uploadPayload.current !== serialized)
                  uploadKey.current = newId();
                uploadPayload.current = serialized;
                await api(
                  `/documents/${document.id}/versions`,
                  post(body, uploadKey.current),
                );
                uploadKey.current = newId();
                uploadPayload.current = "";
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
      <Drawer
        title="版本保留与清理"
        open={retention !== null}
        onClose={() => setRetention(null)}
        size="large"
      >
        <p>
          保留最近 {retention?.keep_latest} 个版本，至少保存{" "}
          {retention?.minimum_age_days}{" "}
          天。激活、引用、索引中及归档历史版本受保护。清理不删除引用和审计，也不会删除
          S3 对象。
        </p>
        <Button
          disabled={busy}
          onClick={async () => {
            try {
              setRetention(await api(`/documents/${document.id}/retention`));
            } catch (reason) {
              setError((reason as Error).message);
            }
          }}
        >
          刷新清理预览
        </Button>
        {retention?.items.map((item) => (
          <section key={item.version_id} className="retention-item">
            <strong>{item.filename}</strong>
            <p>
              {item.version_id.slice(0, 8)} ·{" "}
              {new Date(item.created_at).toLocaleString()} · {item.source_size}{" "}
              字节
            </p>
            <p>
              {item.reasons.length
                ? item.reasons
                    .map((reason) => retentionReasons[reason] || reason)
                    .join("、")
                : "可清理"}
            </p>
            <Button
              danger
              disabled={busy || !item.eligible}
              onClick={() => {
                setCleanup(item);
                setCleanupConfirmed(false);
              }}
            >
              清理此版本
            </Button>
          </section>
        ))}
        {error && <Alert title={error} type="error" />}
      </Drawer>
      <Modal
        title="确认清理知识版本"
        open={cleanup !== null}
        confirmLoading={busy}
        okText="确认不可撤销清理"
        okButtonProps={{ danger: true, disabled: !cleanupConfirmed }}
        onCancel={() => {
          if (!busy) setCleanup(null);
        }}
        onOk={async () => {
          if (!cleanup || !retention || !cleanupConfirmed) return;
          setBusy(true);
          setError("");
          const fingerprint = `${cleanup.version_id}:${retention.document_version}`;
          let key = cleanupKeys.current.get(fingerprint);
          if (!key) {
            key = newId();
            cleanupKeys.current.set(fingerprint, key);
          }
          try {
            await api(
              `/documents/${document.id}/versions/${cleanup.version_id}/cleanup`,
              post(
                {
                  confirm: true,
                  expected_document_version: retention.document_version,
                },
                key,
              ),
            );
            setCleanup(null);
            await versions.refetch();
            setRetention(await api(`/documents/${document.id}/retention`));
          } catch (reason) {
            setError((reason as Error).message);
          } finally {
            setBusy(false);
          }
        }}
      >
        <p>
          {cleanup?.filename} · {cleanup?.version_id}
        </p>
        <p>
          将删除数据库原文与检索索引；版本元数据保留。清理后无法重新激活或下载，恢复需要备份。
        </p>
        <Checkbox
          checked={cleanupConfirmed}
          onChange={(event) => setCleanupConfirmed(event.target.checked)}
        >
          我已确认备份与清理范围
        </Checkbox>
        {error && <Alert title={error} type="error" />}
      </Modal>
      <Modal
        title="PDF 提取文本"
        open={preview !== null}
        onCancel={() => setPreview(null)}
        footer={null}
      >
        <p>
          请逐页核对提取文本，尤其是 OCR
          页的数字、金额和条款，以及双栏顺序、表格行列。仅文本层模式不会识别图片。
        </p>
        {preview?.pages.map((item) => (
          <section key={item.page_number}>
            <strong>第 {item.page_number} 页</strong>
            <Tag>
              {item.extraction_method === "ocr"
                ? "OCR 识别"
                : item.extraction_method === "empty"
                  ? "未识别到文字"
                  : "文本层"}
            </Tag>
            <p className="citation-excerpt pdf-preview">
              {item.text || "此页未提取到文字"}
            </p>
          </section>
        ))}
      </Modal>
      <Modal
        title={
          review?.kind === "layout" ? "核对 PDF 阅读顺序" : "核对 OCR 文本"
        }
        open={review !== null}
        confirmLoading={busy}
        okText="确认并激活"
        cancelText="取消"
        okButtonProps={{ disabled: !confirmed }}
        onCancel={() => {
          if (!busy) setReview(null);
        }}
        onOk={async () => {
          if (
            review &&
            confirmed &&
            (await change(`/documents/${document.id}/activate`, {
              version_id: review.id,
              expected_version: review.expected,
              confirm_ocr: review.kind === "ocr",
              confirm_layout: review.kind === "layout",
            }))
          )
            setReview(null);
        }}
      >
        <p>
          {review?.kind === "layout"
            ? "请对照原文件检查双栏先后顺序及表格行列对应关系，再确认发布。复杂跨栏、合并单元格和扫描表格可能无法正确还原。"
            : "OCR 可能误识别数字和条款。请先使用“预览提取文本”核对，再确认发布。"}
        </p>
        <Checkbox
          checked={confirmed}
          onChange={(event) => setConfirmed(event.target.checked)}
        >
          {review?.kind === "layout"
            ? "我已核对双栏顺序和表格行列"
            : "我已核对 OCR 提取文本"}
        </Checkbox>
        {error && <Alert type="error" title={error} />}
      </Modal>
    </div>
  );
}

export function Knowledge({ user }: { user: User }) {
  const cache = useQueryClient();
  const scope = ["knowledge", user.tenant_id, user.id];
  const admin = user.permissions.includes("knowledge:manage");
  const [open, setOpen] = useState(false);
  const [baseId, setBaseId] = useState("");
  const [name, setName] = useState("");
  const [title, setTitle] = useState("");
  const [roles, setRoles] = useState(allRoles);
  const [file, setFile] = useState<File | null>(null);
  const [ocrMode, setOcrMode] = useState("off");
  const [pdfLayout, setPdfLayout] = useState("plain");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const key = useRef(newId());
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
      key.current = newId();
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
                  key.current = newId();
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
            <Space>
              <span>共享范围</span>
              <TeamScope
                value={base.team_id}
                disabled={busy}
                onChange={(team_id) =>
                  perform(async () => {
                    await api(
                      `/knowledge-bases/${base.id}/team`,
                      post(
                        { team_id, expected_revision: base.acl_revision },
                        key.current,
                        "PATCH",
                      ),
                    );
                  })
                }
              />
            </Space>
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
              UTF-8 TXT / Markdown：256 KiB、20,000 字符。文本 PDF：2 MiB、50
              页、100,000 字符。中英文扫描件可选择 OCR，每次最多识别 10 页。
              自动模式仅识别无文字页；含页眉文字的扫描页请选择整页识别。识别后请核对再激活。
              规则双栏或文字表格可在仅文本层模式下选择阅读方式，上传后请预览核对。
            </p>
            <Input
              aria-label="文档标题"
              placeholder="文档标题"
              value={title}
              maxLength={120}
              onChange={(event) => {
                setTitle(event.target.value);
                key.current = newId();
              }}
            />
            <input
              style={{ margin: "12px 0" }}
              aria-label="知识文件"
              type="file"
              accept=".txt,.md,.pdf"
              onChange={(event) => {
                setFile(event.target.files?.[0] || null);
                key.current = newId();
              }}
            />
            <Select
              aria-label="PDF 识别方式"
              value={ocrMode}
              options={ocrOptions}
              style={{ width: 220, marginRight: 8 }}
              onChange={(value) => {
                setOcrMode(value);
                if (value !== "off") setPdfLayout("plain");
                key.current = newId();
              }}
            />
            <Select
              aria-label="PDF 阅读方式"
              value={pdfLayout}
              options={layoutOptions}
              disabled={busy || ocrMode !== "off"}
              style={{ width: 180, marginRight: 8 }}
              onChange={(value) => {
                setPdfLayout(value);
                key.current = newId();
              }}
            />
            <Button
              loading={busy}
              disabled={!file || !title.trim()}
              onClick={() =>
                perform(async () => {
                  if (!file) return;
                  await api(
                    `/knowledge-bases/${selected}/documents`,
                    post(
                      {
                        title,
                        ...(await readKnowledgeFile(file, ocrMode, pdfLayout)),
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

export function Citation({
  id,
  title,
  pageNumber = 0,
}: {
  id: string;
  title: string;
  pageNumber?: number;
}) {
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
        {pageNumber > 0 && ` · 第 ${pageNumber} 页`}
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
            {data.extraction_method === "ocr" && (
              <Alert
                type="warning"
                title="此依据来自 OCR 识别文本，数字和条款请核对原件。"
              />
            )}
            <p className="citation-excerpt">{data.excerpt}</p>
            <small className="muted">
              {data.page_number > 0 && `第 ${data.page_number} 页 · `}版本{" "}
              {data.version_id} · 片段 {data.chunk_id}
            </small>
          </>
        )}
      </Modal>
    </>
  );
}

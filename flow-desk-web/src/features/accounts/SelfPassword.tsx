import { useEffect, useRef, useState } from "react";
import { Alert, Button, Checkbox, Form, Input, Modal } from "antd";
import {
  ApiError,
  api,
  beginPasswordReauthentication,
  clearPasswordReauthentication,
} from "../../shared/api/client";
import type { paths } from "../../shared/api/generated";

type ChangePasswordBody =
  paths["/api/v1/auth/password"]["post"]["requestBody"]["content"]["application/json"];

export function SelfPassword({
  onSignedOut,
}: {
  onSignedOut: (notice: string) => void;
}) {
  const [open, setOpen] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [form] = Form.useForm();
  const confirmed = Form.useWatch("confirm", form);
  const controller = useRef<AbortController | null>(null);
  const mounted = useRef(true);
  useEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
      controller.current?.abort();
      form.resetFields();
    };
  }, [form]);
  const close = () => {
    if (busy) return;
    form.resetFields();
    setError("");
    setOpen(false);
  };
  async function submit(values: {
    current_password: string;
    new_password: string;
    confirm: boolean;
  }) {
    if (busy) return;
    setBusy(true);
    setError("");
    const active = new AbortController();
    controller.current = active;
    const timer = window.setTimeout(() => active.abort(), 15_000);
    try {
      beginPasswordReauthentication();
      const body: ChangePasswordBody = {
        current_password: values.current_password,
        new_password: values.new_password,
        confirm: values.confirm,
      };
      await api(
        "/auth/password",
        { method: "POST", body: JSON.stringify(body), signal: active.signal },
        false,
      );
      form.resetFields();
      if (mounted.current)
        onSignedOut("密码已修改，所有设备的会话已退出。请使用新密码重新登录。");
    } catch (problem) {
      form.resetFields();
      if (!mounted.current) return;
      if (
        problem instanceof ApiError &&
        [400, 403, 409, 429].includes(problem.status)
      ) {
        clearPasswordReauthentication();
        setError(problem.message);
      } else {
        onSignedOut(
          "修改结果尚未确认，已退出当前页面。请先使用新密码登录；如未成功，再尝试原密码或联系管理员。系统不会自动重试修改密码。",
        );
      }
    } finally {
      window.clearTimeout(timer);
      if (controller.current === active) controller.current = null;
      if (mounted.current) setBusy(false);
    }
  }
  return (
    <>
      <Button
        onClick={() => {
          form.resetFields();
          setError("");
          setOpen(true);
        }}
      >
        修改密码
      </Button>
      <Modal
        title="修改自己的密码"
        open={open}
        onCancel={close}
        footer={null}
        closable={!busy}
        maskClosable={!busy}
        keyboard={!busy}
        destroyOnHidden
      >
        <Alert
          type="info"
          title="修改成功后，所有设备都需要重新登录；正在处理的任务可能因会话撤销而停止，已有办理回执会保留。"
        />
        <Form
          form={form}
          layout="vertical"
          onFinish={submit}
          preserve={false}
          disabled={busy}
        >
          <Form.Item
            name="current_password"
            label="当前密码"
            rules={[{ required: true, message: "请输入当前密码" }]}
          >
            <Input.Password autoComplete="current-password" maxLength={128} />
          </Form.Item>
          <Form.Item
            name="new_password"
            label="新密码"
            rules={[
              { required: true, message: "请输入新密码" },
              {
                validator: (_, value: string) => {
                  if (!value) return Promise.resolve();
                  const bytes = new TextEncoder().encode(value).length;
                  return bytes >= 12 &&
                    bytes <= 72 &&
                    /\p{L}/u.test(value) &&
                    /\p{Nd}/u.test(value)
                    ? Promise.resolve()
                    : Promise.reject(
                        new Error("使用12–72字节，至少包含一个字母和一个数字"),
                      );
                },
              },
            ]}
          >
            <Input.Password autoComplete="new-password" maxLength={72} />
          </Form.Item>
          <Form.Item
            name="repeat_password"
            label="再次输入新密码"
            dependencies={["new_password"]}
            rules={[
              { required: true, message: "请再次输入新密码" },
              ({ getFieldValue }) => ({
                validator: (_, value) =>
                  !value || value === getFieldValue("new_password")
                    ? Promise.resolve()
                    : Promise.reject(new Error("两次新密码不一致")),
              }),
            ]}
          >
            <Input.Password autoComplete="new-password" maxLength={72} />
          </Form.Item>
          <Form.Item name="confirm" valuePropName="checked">
            <Checkbox>我确认修改密码并退出所有设备</Checkbox>
          </Form.Item>
          {error && <Alert title={error} type="error" />}
          <Button
            type="primary"
            htmlType="submit"
            loading={busy}
            disabled={!confirmed}
          >
            确认修改并重新登录
          </Button>
        </Form>
      </Modal>
    </>
  );
}

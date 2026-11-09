import fs from "node:fs";
import { expect, test } from "@playwright/test";
test("history confirms tier changes, reuses an uncertain request, reconciles an existing operation and clears denial", async ({
  page,
}) => {
  const fixturePath = process.env.FLOWDESK_RUN_HISTORY_FIXTURE;
  test.skip(
    !fixturePath,
    "check-run-history.py must create a disposable aged task fixture",
  );
  const fixture = JSON.parse(fs.readFileSync(fixturePath!, "utf8"));
  const errors: string[] = [];
  page.on("pageerror", (error) => errors.push(error.name));
  await page.goto("/");
  await page.getByLabel("工作账号").fill("demo.admin");
  await page
    .getByLabel("密码", { exact: true })
    .fill(process.env.FLOWDESK_TEST_PASSWORD!);
  await page.getByRole("button", { name: "登录工作台" }).click();
  await page.getByRole("button", { name: "任务记录", exact: true }).click();
  const operations = page.getByRole("dialog", {
    name: "任务记录",
    exact: true,
  });
  await operations.getByLabel("任务范围").click();
  await page.getByText("租户任务", { exact: true }).click();
  await operations.getByLabel("按任务 ID 定位").fill(fixture.run_id);
  await operations
    .getByRole("button", { name: "定位任务", exact: true })
    .click();
  await operations
    .getByRole("button", { name: `查看任务 ${fixture.run_id}`, exact: true })
    .click();
  const details = page.getByRole("dialog", { name: "任务详情", exact: true });
  await expect(details).toContainText("节点执行记录");
  await details
    .getByRole("button", { name: "历史归档与恢复", exact: true })
    .click();
  const history = page.getByRole("dialog", {
    name: "任务历史归档与恢复",
    exact: true,
  });
  await expect(history).toContainText("在线");
  await expect(history).toContainText("任务执行身份永久封存");
  const keys: string[] = [];
  let loseReply = true;
  await page.route(
    `**/api/v1/runs/${fixture.run_id}/history/archive`,
    async (route) => {
      keys.push(route.request().headers()["idempotency-key"]);
      const response = await route.fetch();
      expect(response.status()).toBe(200);
      if (loseReply) {
        loseReply = false;
        await route.fulfill({
          status: 503,
          json: {
            code: "SYNTHETIC_LOST_REPLY",
            message: "synthetic lost reply",
          },
        });
      } else await route.fulfill({ response });
    },
  );
  await history.getByRole("button", { name: "预览归档", exact: true }).click();
  const confirmArchive = history.getByRole("button", {
    name: "确认归档",
    exact: true,
  });
  await expect(confirmArchive).toBeDisabled();
  await history.getByRole("checkbox").check();
  await confirmArchive.click();
  await expect(history).toContainText("维护结果尚未确认");
  await expect(history.getByText("在线", { exact: true })).toHaveCount(0);
  await history
    .getByRole("button", { name: "重新读取后核对原请求", exact: true })
    .click();
  await expect(history).toContainText("已归档");
  await history.getByRole("checkbox").check();
  await history
    .getByRole("button", { name: "使用原请求重试", exact: true })
    .click();
  await expect(
    history.getByRole("button", { name: "预览恢复", exact: true }),
  ).toBeEnabled();
  expect(keys.length).toBe(2);
  expect(keys[0]).toBeTruthy();
  expect(keys[1]).toBe(keys[0]);
  // Inject a pending metadata response only to exercise the browser recovery UI.
  // Actual durable pending state, post-IO revocation and rollback are database tests.
  let pending: unknown;
  let showPending = false;
  await page.route(
    `**/api/v1/runs/${fixture.run_id}/history/restore`,
    async (route) => {
      const response = await route.fetch();
      expect(response.status()).toBe(200);
      pending = (await response.json()).data;
      showPending = true;
      await route.fulfill({
        status: 503,
        json: { code: "SYNTHETIC_LOST_REPLY" },
      });
    },
  );
  await page.route(
    `**/api/v1/runs/${fixture.run_id}/history`,
    async (route) => {
      const response = await route.fetch();
      const body = await response.json();
      if (showPending)
        body.data = {
          ...body.data,
          state: "pending",
          operation: { ...(pending as object), status: "pending" },
        };
      await route.fulfill({ response, json: body });
    },
  );
  await history.getByRole("button", { name: "预览恢复", exact: true }).click();
  await expect(
    history.getByRole("button", { name: "确认恢复", exact: true }),
  ).toBeDisabled();
  await history.getByRole("checkbox").check();
  await history.getByRole("button", { name: "确认恢复", exact: true }).click();
  await expect(history).toContainText("维护结果尚未确认");
  await history
    .getByRole("button", { name: "重新读取后核对原请求", exact: true })
    .click();
  await expect(history).toContainText("维护待确认");
  showPending = false;
  await history
    .getByRole("button", { name: "继续已有维护操作", exact: true })
    .click();
  await expect(history.getByText("在线", { exact: true })).toBeVisible();
  await page.unroute(`**/api/v1/runs/${fixture.run_id}/history`);
  await page.route(`**/api/v1/runs/${fixture.run_id}/history`, (route) =>
    route.fulfill({ status: 403, json: { code: "FORBIDDEN" } }),
  );
  await history
    .getByRole("button", { name: "刷新历史存储状态", exact: true })
    .click();
  await expect(history).toBeHidden();
  await expect(details).toBeHidden();
  await expect(operations).toContainText("任务历史访问权限已变化");
  await expect(
    operations.getByRole("button", { name: /^查看任务 / }),
  ).toHaveCount(0);
  expect(errors).toEqual([]);
});

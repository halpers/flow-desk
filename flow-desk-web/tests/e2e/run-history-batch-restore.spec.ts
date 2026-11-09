import fs from "node:fs";
import { expect, test } from "@playwright/test";

test("batch restoration preserves its direction through lost replies, partial completion and reload", async ({
  page,
}) => {
  const fixturePath = process.env.FLOWDESK_RUN_HISTORY_RESTORE_BATCH_FIXTURE;
  test.skip(
    !fixturePath,
    "Run check-run-history-batch-restore.py in a disposable stack first",
  );
  const fixture: { run_ids: string[] } = JSON.parse(
    fs.readFileSync(fixturePath!, "utf8"),
  );
  const errors: string[] = [];
  page.on("pageerror", (error) => errors.push(error.name));
  await page.goto("/");
  await page.getByLabel("工作账号").fill("demo.admin");
  await page
    .getByLabel("密码", { exact: true })
    .fill(process.env.FLOWDESK_TEST_PASSWORD!);
  await page.getByRole("button", { name: "登录工作台" }).click();
  async function openOperations() {
    await page.getByRole("button", { name: "任务记录", exact: true }).click();
    return page.getByRole("dialog", { name: "任务记录", exact: true });
  }
  let operations = await openOperations();
  await operations.getByLabel("任务范围").click();
  await page.getByText("租户任务", { exact: true }).click();
  for (const id of fixture.run_ids)
    await operations
      .getByRole("checkbox", { name: `选择归档任务 ${id}`, exact: true })
      .check();
  await operations
    .getByRole("button", { name: "批量恢复与批次记录", exact: true })
    .click();
  const panel = page.getByRole("dialog", {
    name: "任务历史批量恢复",
    exact: true,
  });
  await expect(
    panel.getByRole("button", { name: "预览所选 2 项", exact: true }),
  ).toBeEnabled();
  await panel
    .getByRole("button", { name: "预览所选 2 项", exact: true })
    .click();
  const confirm = panel.getByRole("button", {
    name: "确认批量恢复",
    exact: true,
  });
  await expect(confirm).toBeDisabled();
  await expect(panel).toContainText("恢复不会重新执行任务");
  const keys: string[] = [];
  let loseCreationReply = true;
  let batchId = "";
  await page.route("**/api/v1/run-history-restore-batches", async (route) => {
    if (route.request().method() !== "POST") {
      await route.continue();
      return;
    }
    keys.push(route.request().headers()["idempotency-key"]);
    const response = await route.fetch();
    expect(response.status()).toBe(200);
    batchId = (await response.json()).data.batch_id;
    if (loseCreationReply) {
      loseCreationReply = false;
      await route.fulfill({
        status: 503,
        json: { code: "SYNTHETIC_LOST_REPLY" },
      });
    } else await route.fulfill({ response });
  });
  let advances = 0;
  let loseItemReply = true;
  await page.route(
    "**/api/v1/run-history-batches/*/operations/*/resume",
    async (route) => {
      advances++;
      const response = await route.fetch();
      expect(response.status()).toBe(200);
      if (loseItemReply) {
        loseItemReply = false;
        await route.fulfill({
          status: 503,
          json: { code: "SYNTHETIC_LOST_ITEM_REPLY" },
        });
      } else await route.fulfill({ response });
    },
  );
  await panel.getByRole("checkbox").check();
  await confirm.click();
  await expect(panel).toContainText("保留了原批次请求");
  await panel
    .getByRole("button", { name: "再次核对原批次请求", exact: true })
    .click();
  await expect(panel).toContainText("本次结果尚未确认");
  expect(keys).toHaveLength(2);
  expect(keys[0]).toBeTruthy();
  expect(keys[1]).toBe(keys[0]);
  await panel.getByRole("button", { name: "刷新本批次", exact: true }).click();
  await expect(panel).toContainText("已完成 1 / 2 项");
  await page.reload();
  operations = await openOperations();
  await operations
    .getByRole("button", { name: "批量恢复与批次记录", exact: true })
    .click();
  await panel
    .getByRole("button", { name: `查看恢复批次 ${batchId}`, exact: true })
    .click();
  await expect(panel).toContainText("已完成 1 / 2 项");
  await panel
    .getByRole("button", { name: "继续待核对项", exact: true })
    .click();
  await expect(panel).toContainText("已完成 2 / 2 项");
  await expect(panel).toContainText("恢复存储");
  expect(advances).toBe(2);
  await page.route(`**/api/v1/run-history-batches/${batchId}`, (route) =>
    route.fulfill({ status: 403, json: { code: "PERMISSION_DENIED" } }),
  );
  await panel.getByRole("button", { name: "刷新本批次", exact: true }).click();
  await expect(panel).toBeHidden();
  await expect(operations).toContainText("批次访问权限已变化");
  await expect(
    operations.getByRole("button", { name: /^查看任务 / }),
  ).toHaveCount(0);
  expect(errors).toEqual([]);
});

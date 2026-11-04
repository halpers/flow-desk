import { expect, test } from "@playwright/test";

test("task trace and a lost retry response preserve one linked child", async ({
  page,
}) => {
  const errors: string[] = [];
  page.on("pageerror", (error) => errors.push(error.name));
  await page.goto("/");
  await page.getByLabel("工作账号").fill("demo.operator");
  await page
    .getByLabel("密码", { exact: true })
    .fill(process.env.FLOWDESK_TEST_PASSWORD!);
  await page.getByRole("button", { name: "登录工作台" }).click();
  await page.getByRole("button", { name: "新建会话" }).click();
  await page.getByLabel("服务问题").fill("查询 FD209901010001 的订单和物流");
  const created = page.waitForResponse(
    (response) =>
      response.url().endsWith("/messages") &&
      response.request().method() === "POST",
  );
  await page.getByRole("button", { name: "发送任务", exact: true }).click();
  const parent = (await (await created).json()).data;
  await expect(
    page.locator(".conversation-panel .panel-heading"),
  ).toContainText("处理失败", { timeout: 30000 });
  await page.getByRole("button", { name: "任务记录", exact: true }).click();
  await page.getByLabel("按任务 ID 定位").fill(parent.id);
  await page.getByRole("button", { name: "定位任务" }).click();
  await page.getByRole("button", { name: `查看任务 ${parent.id}` }).click();
  const details = page.getByRole("dialog", { name: "任务详情" });
  await expect(details).toContainText("节点执行记录", { timeout: 15000 });
  await expect(details).toContainText("read_business_tools");
  await expect(details).toContainText("0（无模型调用记录）");
  await expect(details).toContainText("费用");
  await expect(details).not.toContainText("FD209901010001");
  let lost = false;
  let childId = "";
  const keys: string[] = [];
  await page.route(`**/runs/${parent.id}/retry`, async (route) => {
    keys.push(route.request().headers()["idempotency-key"]);
    if (!lost) {
      const response = await route.fetch();
      expect(response.status()).toBe(202);
      childId = (await response.json()).data.id;
      lost = true;
      await route.abort("failed");
    } else await route.continue();
  });
  await details.getByRole("button", { name: "重试任务", exact: true }).click();
  const confirmation = page.getByRole("dialog", { name: "确认重试任务" });
  await confirmation
    .getByRole("button", { name: "确认重试", exact: true })
    .click();
  await expect(confirmation.getByRole("alert")).toBeVisible();
  const replay = page.waitForResponse((response) =>
    response.url().endsWith(`/runs/${parent.id}/retry`),
  );
  await confirmation
    .getByRole("button", { name: "确认重试", exact: true })
    .click();
  expect((await (await replay).json()).data.id).toBe(childId);
  expect(keys).toHaveLength(2);
  expect(keys[0]).toBe(keys[1]);
  await expect(confirmation).not.toBeVisible();
  await expect(page.locator(".message.user")).toHaveCount(2);
  await expect(
    page.locator(".conversation-panel .panel-heading"),
  ).toContainText("处理失败", { timeout: 30000 });
  await page.getByRole("button", { name: "任务记录", exact: true }).click();
  await page.getByRole("button", { name: `查看任务 ${parent.id}` }).click();
  await expect(details).toContainText(childId);
  await expect(
    details.getByRole("button", { name: "重试任务", exact: true }),
  ).toBeDisabled();
  expect(errors).toEqual([]);
});

import { expect, test } from "@playwright/test";

test("input and exact approval survive reload and a lost decision response", async ({
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
  async function newConversation() {
    await Promise.all([
      page.waitForResponse(
        (response) =>
          response.url().endsWith("/api/v1/conversations") &&
          response.request().method() === "POST",
      ),
      page.getByRole("button", { name: "新建会话" }).click(),
    ]);
  }
  async function send(content: string) {
    await page.getByLabel("服务问题").fill(content);
    await page.getByRole("button", { name: "发送任务" }).click();
  }
  await newConversation();
  await send("请创建工单，登记合成配送问题");
  await expect(page.getByRole("heading", { name: "需要补充信息" })).toBeVisible(
    { timeout: 30000 },
  );
  await page.reload();
  await expect(page.getByLabel("补充信息")).toBeVisible({ timeout: 15000 });
  await page.getByLabel("补充信息").fill("FD202611020002");
  await page.getByRole("button", { name: "提交补充信息" }).click();
  await expect(page.getByLabel("待确认操作")).toContainText("FD202611020002", {
    timeout: 30000,
  });
  await page.reload();
  await expect(page.getByRole("button", { name: "确认办理" })).toBeVisible({
    timeout: 15000,
  });
  let dropped = false;
  await page.route("**/api/v1/approvals/*/decision", async (route) => {
    const body = route.request().postDataJSON();
    expect(Object.keys(body).sort()).toEqual([
      "decision",
      "expected_version",
      "reason",
    ]);
    if (!dropped) {
      dropped = true;
      await route.fetch();
      await route.abort();
    } else await route.continue();
  });
  await page.getByRole("button", { name: "确认办理" }).click();
  await expect(page.locator(".receipt-card")).toHaveCount(1, {
    timeout: 30000,
  });
  const ticketNo = (await page
    .locator(".receipt-card button")
    .textContent())!.trim();
  expect(ticketNo).toMatch(/^T[0-9a-f]{32}$/);
  await expect(page.locator(".message.assistant")).toContainText(ticketNo);
  await page.reload();
  await expect(page.locator(".receipt-card")).toHaveCount(1, {
    timeout: 15000,
  });
  await send(
    `订单 FD202611020002 的工单 ${ticketNo} 添加备注：E2E合成回访已登记`,
  );
  await expect(page.getByLabel("待确认操作")).toContainText(
    "E2E合成回访已登记",
    { timeout: 30000 },
  );
  await page.getByRole("button", { name: "确认办理" }).click();
  await expect(page.locator(".receipt-card")).toHaveCount(2, {
    timeout: 30000,
  });
  await page.locator(".receipt-card button").last().click();
  await expect(page.getByRole("dialog")).toContainText("E2E合成回访已登记");
  await page.getByRole("button", { name: "Close", exact: true }).click();
  await newConversation();
  await send("创建工单 FD202611020002");
  await expect(page.getByRole("button", { name: "拒绝办理" })).toBeVisible({
    timeout: 30000,
  });
  await page.getByRole("button", { name: "拒绝办理" }).click();
  await expect(page.getByText("已取消", { exact: true }).first()).toBeVisible();
  await expect(page.locator(".receipt-card")).toHaveCount(0);
  await newConversation();
  await send("创建工单 FD202611020002");
  await expect(page.getByRole("button", { name: "确认办理" })).toBeVisible({
    timeout: 30000,
  });
  await page.getByRole("button", { name: "取消任务" }).click();
  await expect(page.getByText("已取消", { exact: true }).first()).toBeVisible();
  await expect(page.getByRole("button", { name: "确认办理" })).toHaveCount(0);
  await expect(page.locator(".receipt-card")).toHaveCount(0);
  await page.screenshot({ path: "../work/m4-cancelled.png", fullPage: true });
  expect(dropped).toBe(true);
  expect(errors).toEqual([]);
});

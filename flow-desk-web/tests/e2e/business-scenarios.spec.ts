import { expect, test } from "@playwright/test";

test("logistics ticket can be accepted and resolved through exact approvals", async ({
  page,
}) => {
  const errors: string[] = [];
  page.on("pageerror", (error) => errors.push(error.name));
  await page.goto("/");
  await page.getByLabel("工作账号").fill("demo.supervisor");
  await page
    .getByLabel("密码", { exact: true })
    .fill(process.env.FLOWDESK_TEST_PASSWORD!);
  await page.getByRole("button", { name: "登录工作台" }).click();
  await page.getByRole("button", { name: "新建会话" }).click();
  async function send(content: string) {
    await page.getByLabel("服务问题").fill(content);
    await page.getByRole("button", { name: "发送任务" }).click();
  }
  await send("请创建物流工单，跟进订单 FD202611020002 的配送问题");
  await expect(page.getByLabel("待确认操作")).toContainText("物流", {
    timeout: 30000,
  });
  await page.getByRole("button", { name: "确认办理" }).click();
  await expect(page.locator(".receipt-card")).toHaveCount(1, {
    timeout: 30000,
  });
  const ticketNo = (await page
    .locator(".receipt-card button")
    .textContent())!.trim();
  await expect(page.locator(".message.assistant")).toContainText(ticketNo);
  await send(
    `请接手处理工单 ${ticketNo}，订单 FD202611020002，处理原因：E2E合成物流跟进`,
  );
  await expect(
    page.getByRole("heading", { name: "确认接手处理工单" }),
  ).toBeVisible({ timeout: 30000 });
  await expect(page.getByLabel("待确认操作")).toContainText("待处理 → 处理中");
  await page.reload();
  await expect(
    page.getByRole("heading", { name: "确认接手处理工单" }),
  ).toBeVisible({ timeout: 15000 });
  let dropped = false;
  await page.route("**/api/v1/approvals/*/decision", async (route) => {
    if (!dropped) {
      dropped = true;
      await route.fetch();
      await route.abort();
    } else await route.continue();
  });
  await page.getByRole("button", { name: "确认办理" }).click();
  await expect(page.locator(".receipt-card")).toHaveCount(2, {
    timeout: 30000,
  });
  await expect(page.locator(".receipt-card").last()).toContainText(
    "工单已接手处理",
  );
  await expect(
    page
      .locator(".run-controls")
      .last()
      .getByRole("button", { name: "取消任务" }),
  ).toHaveCount(0, { timeout: 15000 });
  await send(
    `请办结工单 ${ticketNo}，订单 FD202611020002，处理结论：E2E合成已联系客户并确认送达`,
  );
  await expect(page.getByRole("heading", { name: "确认办结工单" })).toBeVisible(
    { timeout: 30000 },
  );
  await expect(page.getByLabel("待确认操作")).toContainText("处理中 → 已办结");
  await expect(page.getByLabel("待确认操作")).toContainText(
    "E2E合成已联系客户并确认送达",
  );
  await page.getByRole("button", { name: "确认办理" }).click();
  await expect(page.locator(".receipt-card")).toHaveCount(3, {
    timeout: 30000,
  });
  await expect(page.locator(".receipt-card").last()).toContainText(
    "工单已办结",
  );
  await page.reload();
  await expect(page.locator(".receipt-card")).toHaveCount(3, {
    timeout: 15000,
  });
  await page.locator(".receipt-card button").last().click();
  await expect(page.getByRole("dialog")).toContainText(
    "E2E合成已联系客户并确认送达",
  );
  await expect(
    page.getByRole("dialog").locator(".ticket-timeline article"),
  ).toHaveCount(3);
  expect(dropped).toBe(true);
  expect(errors).toEqual([]);
});

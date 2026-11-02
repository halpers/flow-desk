import { expect, test } from "@playwright/test";

test("query survives a broken progress stream, reload and logout", async ({
  page,
}) => {
  const pageErrors: string[] = [];
  page.on("pageerror", (error) => pageErrors.push(error.name));
  await page.goto("/");
  await page.getByLabel("工作账号").fill("demo.operator");
  await page
    .getByLabel("密码", { exact: true })
    .fill(process.env.FLOWDESK_TEST_PASSWORD!);
  await page.getByRole("button", { name: "登录工作台" }).click();
  await expect(page.getByRole("heading", { name: "服务会话" })).toBeVisible();
  await Promise.all([
    page.waitForResponse(
      (response) =>
        response.url().endsWith("/api/v1/conversations") &&
        response.request().method() === "POST",
    ),
    page.getByRole("button", { name: "新建会话" }).click(),
  ]);
  await expect(page.getByLabel("服务问题")).toBeEnabled();
  await page
    .getByLabel("服务问题")
    .fill("查询 FD202611020002 的订单状态和物流");
  await expect(page.getByLabel("服务问题")).toHaveValue(
    "查询 FD202611020002 的订单状态和物流",
  );
  await expect(page.getByRole("button", { name: "发送查询" })).toBeEnabled();
  let disconnected = false;
  await page.route("**/api/v1/runs/*/events", async (route) => {
    if (!disconnected) {
      disconnected = true;
      await route.abort();
    } else await route.continue();
  });
  await page.getByRole("button", { name: "发送查询" }).click();
  await expect(page.locator(".message.assistant")).toContainText("102.90", {
    timeout: 45000,
  });
  await expect(page.locator(".message.assistant")).toContainText(
    "来源更新时间",
  );
  expect(disconnected).toBe(true);
  await page.reload();
  await expect(page.locator(".message.assistant")).toContainText("102.90", {
    timeout: 20000,
  });
  await page.screenshot({ path: "../work/m2-workspace.png", fullPage: true });
  await page.getByRole("button", { name: /^退\s*出$/ }).click();
  await expect(page.getByRole("button", { name: "登录工作台" })).toBeVisible();
  await expect(page.locator(".message")).toHaveCount(0);
  expect(pageErrors).toEqual([]);
});

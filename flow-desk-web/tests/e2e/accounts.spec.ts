import { expect, test, type Page } from "@playwright/test";

async function login(page: Page, account: string, password: string) {
  await page.goto("/");
  await page.getByLabel("工作账号").fill(account);
  await page.getByLabel("密码", { exact: true }).fill(password);
  await page.getByRole("button", { name: "登录工作台" }).click();
  await expect(page.getByRole("button", { name: /退\s*出/ })).toBeVisible();
}

test("account management retries lost responses and revokes changed credentials", async ({
  page,
  browser,
}) => {
  const issues: string[] = [];
  page.on("pageerror", (error) => issues.push(error.name));
  await login(page, "demo.admin", process.env.FLOWDESK_TEST_PASSWORD!);
  const suffix = Date.now().toString();
  const roleName = "E2E additive " + suffix;
  const account = "e2e." + suffix;
  const displayName = "E2E account " + suffix;
  const password = "Synthetic-browser-123";
  const replacement = "Synthetic-replaced-456";
  await page.getByRole("button", { name: "账号与角色", exact: true }).click();
  await page.getByRole("tab", { name: "角色", exact: true }).click();
  await page.getByRole("button", { name: "新增角色", exact: true }).click();
  await page.getByLabel("角色名称").fill(roleName);
  await page.getByLabel("额外权限").click();
  await page.getByRole("option", { name: "知识管理", exact: true }).click();
  await page.getByLabel("额外权限").press("Escape");
  const keys: string[] = [];
  await page.route("**/api/v1/admin/roles", async (route) => {
    if (route.request().method() !== "POST") return route.continue();
    keys.push(route.request().headers()["idempotency-key"]);
    if (keys.length === 1) {
      const response = await route.fetch();
      expect(response.status()).toBe(201);
      await route.abort("failed");
    } else await route.continue();
  });
  await page.getByRole("button", { name: "保存角色", exact: true }).click();
  await expect(
    page
      .getByRole("dialog", { name: "新增角色", exact: true })
      .getByRole("alert"),
  ).toBeVisible();
  await page.getByRole("button", { name: "保存角色", exact: true }).click();
  await expect(
    page.getByRole("dialog", { name: "新增角色", exact: true }),
  ).toHaveCount(0);
  expect(keys).toHaveLength(2);
  expect(keys[1]).toBe(keys[0]);
  await page.unroute("**/api/v1/admin/roles");
  await expect(page.getByText(roleName, { exact: true })).toHaveCount(1);
  await page.getByRole("tab", { name: "账号", exact: true }).click();
  await page.getByRole("button", { name: "新增账号", exact: true }).click();
  await page.getByLabel("新账号名").fill(account);
  await page.getByLabel("显示名称").fill(displayName);
  await page.getByLabel("分配角色").click();
  await page.getByRole("option", { name: roleName, exact: true }).click();
  await page.getByLabel("分配角色").press("Escape");
  await page.getByLabel("设置新密码").fill(password);
  await page.getByRole("button", { name: "保存账号", exact: true }).click();
  const card = page.locator(".ant-card").filter({
    has: page.getByText(`${displayName} (${account})`, { exact: true }),
  });
  await expect(card).toBeVisible();
  const targetContext = await browser.newContext({
    baseURL: new URL(page.url()).origin,
  });
  const target = await targetContext.newPage();
  try {
    await login(target, account, password);
    await expect(
      target.getByRole("button", { name: "账号与角色", exact: true }),
    ).toHaveCount(0);
    await card.getByRole("button", { name: "重置密码", exact: true }).click();
    await page.getByLabel("设置新密码").fill(replacement);
    await page.getByRole("button", { name: "保存新密码", exact: true }).click();
    await expect(
      page.getByRole("dialog", { name: "重置账号密码", exact: true }),
    ).toHaveCount(0);
    await target.reload();
    await expect(target.getByLabel("工作账号")).toBeVisible();
    await login(target, account, replacement);
    await card.getByRole("button", { name: "编辑账号", exact: true }).click();
    await page.getByLabel("启用账号").click();
    await page.getByRole("button", { name: "保存账号", exact: true }).click();
    await expect(card.getByText("停用", { exact: true })).toBeVisible();
    await target.reload();
    await expect(target.getByLabel("工作账号")).toBeVisible();
  } finally {
    await targetContext.close();
  }
  expect(issues).toEqual([]);
});

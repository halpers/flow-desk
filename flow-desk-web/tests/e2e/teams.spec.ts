import { expect, test, type Page } from "@playwright/test";

async function login(page: Page, account: string) {
  await page.goto("/");
  await page.getByLabel("工作账号").fill(account);
  await page
    .getByLabel("密码", { exact: true })
    .fill(process.env.FLOWDESK_TEST_PASSWORD!);
  await page.getByRole("button", { name: "登录工作台" }).click();
  await expect(
    page.getByRole("button", { name: "团队", exact: true }),
  ).toBeVisible();
}

test("team scope and member removal change ticket visibility", async ({
  page,
  browser,
}) => {
  const issues: string[] = [];
  page.on("pageerror", (error) => issues.push(error.name));
  await login(page, "demo.admin");
  const name = `E2E team ${Date.now()}`;
  await page.getByRole("button", { name: "团队", exact: true }).click();
  await page.getByLabel("团队名称").fill(name);
  await page.getByRole("button", { name: "创建团队", exact: true }).click();
  await page.getByLabel("选择团队").click();
  await page.getByText(name, { exact: true }).last().click();
  await page.getByLabel("添加团队成员").click();
  await page.getByText("demo.operator (operator)", { exact: true }).click();
  await page.getByRole("button", { name: "添加成员", exact: true }).click();
  await expect(
    page.getByRole("button", { name: "移除成员", exact: true }),
  ).toBeVisible();
  await page.getByRole("button", { name: "Close", exact: true }).click();
  await page.getByRole("button", { name: "工单台", exact: true }).click();
  await page.getByLabel("关联订单号").fill("FD202611020002");
  await page.getByLabel("工单标题").fill(name);
  await page
    .getByLabel("工单描述")
    .fill("Synthetic team ticket visible only to authorized members.");
  await page.getByRole("button", { name: "创建工单", exact: true }).click();
  const detail = page
    .getByRole("dialog")
    .filter({ has: page.getByLabel("资源团队范围") });
  await detail.getByLabel("资源团队范围").click();
  const scopeResponse = page.waitForResponse(
    (response) =>
      response.url().endsWith("/team") &&
      response.request().method() === "PATCH",
  );
  await page.getByText(name, { exact: true }).last().click();
  const changed = await scopeResponse;
  expect(changed.status()).toBe(200);
  expect((await changed.json()).data.team_id).toBeTruthy();

  const memberContext = await browser.newContext({
    baseURL: new URL(page.url()).origin,
    ignoreHTTPSErrors: true,
  });
  const member = await memberContext.newPage();
  try {
    await login(member, "demo.operator");
    await member.getByRole("button", { name: "工单台", exact: true }).click();
    await expect(member.getByText(name, { exact: true })).toBeVisible();
    await member
      .locator("article.ticket-row")
      .filter({ hasText: name })
      .getByRole("button", { name: "查看工单" })
      .click();
    await expect(
      member.getByRole("dialog").filter({ hasText: "Synthetic team ticket" }),
    ).toContainText("Synthetic team ticket");
    await expect(member.getByLabel("资源团队范围")).toHaveCount(0);
    await detail.getByRole("button", { name: "Close", exact: true }).click();
    await page.getByRole("button", { name: "Close", exact: true }).click();
    await page.getByRole("button", { name: "团队", exact: true }).click();
    await page.getByRole("button", { name: "移除成员", exact: true }).click();
    await expect(
      page.getByRole("button", { name: "移除成员", exact: true }),
    ).toHaveCount(0);
    await member.reload();
    await member.getByRole("button", { name: "工单台", exact: true }).click();
    await expect(member.getByText(name, { exact: true })).toHaveCount(0);
  } finally {
    await memberContext.close();
  }
  expect(issues).toEqual([]);
});

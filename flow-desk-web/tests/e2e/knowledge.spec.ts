import { expect, test } from "@playwright/test";

test("administrator uploads, activates and withdraws a policy with readable citations", async ({
  page,
}) => {
  const errors: string[] = [];
  page.on("pageerror", (error) => errors.push(error.name));
  await page.goto("/");
  await page.getByLabel("工作账号").fill("demo.admin");
  await page
    .getByLabel("密码", { exact: true })
    .fill(process.env.FLOWDESK_TEST_PASSWORD!);
  await page.getByRole("button", { name: "登录工作台" }).click();
  await page.getByRole("button", { name: "知识库", exact: true }).click();
  const name = "E2E policy " + Date.now();
  await page.getByLabel("知识库名称").fill(name);
  await Promise.all([
    page.waitForResponse(
      (response) =>
        response.url().endsWith("/knowledge-bases") &&
        response.request().method() === "POST",
    ),
    page.getByRole("button", { name: "新建知识库", exact: true }).click(),
  ]);
  await page.getByLabel("选择知识库").click();
  await page.getByText(name, { exact: true }).last().click();
  const title = "E2E专属保修政策" + Date.now();
  await page.getByLabel("文档标题").fill(title);
  await page
    .getByLabel("知识文件")
    .setInputFiles({
      name: "warranty.md",
      mimeType: "text/markdown",
      buffer: Buffer.from(title + "：提供三十六个月保修服务。"),
    });
  await page.getByRole("button", { name: "上传并索引" }).click();
  await expect(page.getByRole("button", { name: "激活版本" })).toBeEnabled({
    timeout: 30000,
  });
  await page.getByRole("button", { name: "激活版本" }).click();
  await expect(page.getByRole("button", { name: "当前版本" })).toBeVisible();
  await page.getByRole("button", { name: "Close", exact: true }).click();
  await Promise.all([
    page.waitForResponse(
      (response) =>
        response.url().endsWith("/conversations") &&
        response.request().method() === "POST",
    ),
    page.getByRole("button", { name: "新建会话" }).click(),
  ]);
  await page.getByLabel("服务问题").fill(title + "规定保修多久？");
  await page.getByRole("button", { name: "发送任务" }).click();
  await expect(page.locator(".message.assistant")).toContainText("三十六个月", {
    timeout: 45000,
  });
  await page
    .getByRole("button", { name: "依据：" + title, exact: true })
    .click();
  await expect(page.locator(".citation-excerpt")).toContainText("三十六个月");
  await page
    .getByRole("dialog")
    .getByRole("button", { name: "Close", exact: true })
    .click();
  await page.getByRole("button", { name: "知识库", exact: true }).click();
  await page.getByRole("button", { name: "撤回文档" }).click();
  await expect(page.getByText("已撤回", { exact: true })).toBeVisible();
  await page.getByRole("button", { name: "Close", exact: true }).click();
  await page.reload();
  await expect(page.locator(".message.assistant")).toContainText("已撤回", {
    timeout: 20000,
  });
  await expect(page.locator(".message.assistant")).not.toContainText(
    "三十六个月",
  );
  await page.screenshot({ path: "../work/m3-knowledge.png", fullPage: true });
  expect(errors).toEqual([]);
});

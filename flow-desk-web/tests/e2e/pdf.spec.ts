import { expect, test } from "@playwright/test";
import path from "node:path";

test("PDF text preview, physical page citations, rejection and withdrawal", async ({
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
  const base = "PDF E2E " + Date.now();
  await page.getByLabel("知识库名称").fill(base);
  await Promise.all([
    page.waitForResponse(
      (response) =>
        response.url().endsWith("/knowledge-bases") &&
        response.request().method() === "POST",
    ),
    page.getByRole("button", { name: "新建知识库", exact: true }).click(),
  ]);
  await page.getByLabel("选择知识库").click();
  await page.getByText(base, { exact: true }).last().click();
  const title = "PDF专属保修政策" + Date.now();
  const fixtures = path.resolve("../flow-desk-agent/tests/fixtures");
  await page.getByLabel("文档标题").fill(title);
  await page
    .getByLabel("知识文件")
    .setInputFiles(path.join(fixtures, "policy.pdf"));
  await page.getByRole("button", { name: "上传并索引" }).click();
  const document = page
    .locator(".knowledge-document")
    .filter({ hasText: title });
  await expect(document.getByRole("button", { name: "激活版本" })).toBeEnabled({
    timeout: 30000,
  });
  await expect(document).toContainText("3 页（3 页有文字）");
  await document.getByRole("button", { name: "预览提取文本" }).click();
  const preview = page.getByRole("dialog", { name: "PDF 提取文本" });
  await expect(preview).toContainText("第 2 页");
  await expect(preview).toContainText("三十六个月");
  await preview.getByRole("button", { name: "Close", exact: true }).click();
  await document.getByRole("button", { name: "激活版本" }).click();
  await expect(
    document.getByRole("button", { name: "当前版本" }),
  ).toBeVisible();
  // Replay a committed upload whose response was lost, preserving the version ID.
  let lostUpload = false;
  await page.route("**/documents/*/versions", async (route) => {
    if (route.request().method() === "POST" && !lostUpload) {
      lostUpload = true;
      await route.fetch();
      await route.abort("failed");
    } else await route.continue();
  });
  await document
    .getByLabel(`上传${title}新版本`)
    .setInputFiles(path.join(fixtures, "policy.pdf"));
  await expect(document.getByRole("alert")).toBeVisible();
  await document
    .getByLabel(`上传${title}新版本`)
    .setInputFiles(path.join(fixtures, "policy.pdf"));
  await expect(document.locator(".knowledge-version")).toHaveCount(2);
  expect(lostUpload).toBe(true);
  await page.unroute("**/documents/*/versions");
  await page.getByLabel("文档标题").fill("扫描件验证");
  await page
    .getByLabel("知识文件")
    .setInputFiles(path.join(fixtures, "scan.pdf"));
  await page.getByRole("button", { name: "上传并索引" }).click();
  const scan = page
    .locator(".knowledge-document")
    .filter({ hasText: "扫描件验证" });
  await expect(scan).toContainText("扫描件需要先完成 OCR", { timeout: 30000 });
  await expect(scan.getByRole("button", { name: "激活版本" })).toBeDisabled();
  await page.getByRole("button", { name: "Close", exact: true }).click();
  await Promise.all([
    page.waitForResponse(
      (response) =>
        response.url().endsWith("/conversations") &&
        response.request().method() === "POST",
    ),
    page.getByRole("button", { name: "新建会话" }).click(),
  ]);
  await page.getByLabel("服务问题").fill("PDF专属保修政策规定保修多久？");
  await page.getByRole("button", { name: "发送任务" }).click();
  await expect(page.locator(".message.assistant")).toContainText("三十六个月", {
    timeout: 45000,
  });
  await page
    .getByRole("button", { name: `依据：${title} · 第 2 页`, exact: true })
    .click();
  const citation = page.getByRole("dialog", { name: title });
  await expect(citation.locator(".citation-excerpt")).toContainText(
    "三十六个月",
  );
  await expect(citation).toContainText("第 2 页");
  await citation.getByRole("button", { name: "Close", exact: true }).click();
  await page.getByRole("button", { name: "知识库", exact: true }).click();
  await document.getByRole("button", { name: "撤回文档" }).click();
  await expect(document).toContainText("已撤回");
  await page.getByRole("button", { name: "Close", exact: true }).click();
  await page.reload();
  await expect(page.locator(".message.assistant")).toContainText("已撤回", {
    timeout: 20000,
  });
  await expect(page.locator(".message.assistant")).not.toContainText(
    "三十六个月",
  );
  expect(errors).toEqual([]);
});

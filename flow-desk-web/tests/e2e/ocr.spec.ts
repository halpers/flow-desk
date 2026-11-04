import { expect, test } from "@playwright/test";
import path from "node:path";

test("scan recognition, page preview and explicit OCR activation review", async ({
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
  const name = "OCR E2E " + Date.now();
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
  const title = "合成 OCR 保修 " + Date.now();
  await page.getByLabel("文档标题").fill(title);
  await page.getByLabel("PDF 识别方式", { exact: true }).click();
  await page.getByText("自动识别无文字页", { exact: true }).last().click();
  await page
    .getByLabel("知识文件")
    .setInputFiles(
      path.resolve("../flow-desk-agent/tests/fixtures/ocr-policy.pdf"),
    );
  await page.getByRole("button", { name: "上传并索引" }).click();
  const document = page
    .locator(".knowledge-document")
    .filter({ hasText: title });
  await expect(document.getByRole("button", { name: "激活版本" })).toBeEnabled({
    timeout: 75000,
  });
  await expect(document).toContainText("OCR 2 页");
  await document.getByRole("button", { name: "预览提取文本" }).click();
  const preview = page.getByRole("dialog", { name: "PDF 提取文本" });
  await expect(preview).toContainText("第 2 页");
  await expect(preview).toContainText("三十六个月");
  await expect(preview.getByText("OCR 识别", { exact: true })).toHaveCount(2);
  await preview.getByRole("button", { name: "Close", exact: true }).click();
  await document.getByRole("button", { name: "激活版本" }).click();
  const review = page.getByRole("dialog", { name: "核对 OCR 文本" });
  await expect(
    review.getByRole("button", { name: "确认并激活" }),
  ).toBeDisabled();
  await review.getByRole("checkbox", { name: "我已核对 OCR 提取文本" }).check();
  await review.getByRole("button", { name: "确认并激活" }).click();
  await expect(review).not.toBeVisible();
  await expect(
    document.getByRole("button", { name: "当前版本" }),
  ).toBeVisible();
  await page
    .getByRole("dialog", { name: "政策知识库" })
    .getByRole("button", { name: "Close", exact: true })
    .click();
  await Promise.all([
    page.waitForResponse(
      (response) =>
        response.url().endsWith("/conversations") &&
        response.request().method() === "POST",
    ),
    page.getByRole("button", { name: "新建会话" }).click(),
  ]);
  await page.getByLabel("服务问题").fill("极光保修政策规定设备保修期限多久？");
  await page.getByRole("button", { name: "发送任务" }).click();
  await expect(page.locator(".message.assistant")).toContainText("三十六个月", {
    timeout: 45000,
  });
  await page
    .getByRole("button", { name: `依据：${title} · 第 2 页`, exact: true })
    .click();
  const citation = page.getByRole("dialog", { name: title });
  await expect(citation).toContainText("此依据来自 OCR 识别文本");
  await expect(citation.locator(".citation-excerpt")).toContainText(
    "三十六个月",
  );
  await citation.getByRole("button", { name: "Close", exact: true }).click();
  await page.getByRole("button", { name: "知识库", exact: true }).click();
  await document.getByRole("button", { name: "撤回文档" }).click();
  await expect(document).toContainText("已撤回");
  await page
    .getByRole("dialog", { name: "政策知识库" })
    .getByRole("button", { name: "Close", exact: true })
    .click();
  await page.reload();
  await expect(page.locator(".message.assistant")).toContainText("已撤回", {
    timeout: 20000,
  });
  expect(errors).toEqual([]);
});

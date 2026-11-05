import { expect, test } from "@playwright/test";
import path from "node:path";

test("explicit PDF layout, ordered preview and activation review", async ({
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
  const name = "Layout E2E " + Date.now();
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
  const title = "合成双栏政策 " + Date.now();
  await page.getByLabel("文档标题").fill(title);
  await page.getByLabel("PDF 阅读方式", { exact: true }).click();
  await page.getByText("双栏先左后右", { exact: true }).last().click();
  await page
    .getByLabel("知识文件")
    .setInputFiles(
      path.resolve("../flow-desk-agent/tests/fixtures/layout-columns.pdf"),
    );
  await page.getByRole("button", { name: "上传并索引" }).click();
  const document = page
    .locator(".knowledge-document")
    .filter({ hasText: title });
  await expect(document.getByRole("button", { name: "激活版本" })).toBeEnabled({
    timeout: 45000,
  });
  await expect(document).toContainText("双栏先左后右");
  await document.getByRole("button", { name: "预览提取文本" }).click();
  const preview = page.getByRole("dialog", { name: "PDF 提取文本" });
  const text = await preview.locator(".pdf-preview").nth(1).innerText();
  expect(text.indexOf("左栏五")).toBeLessThan(text.indexOf("右栏一"));
  await expect(preview).toContainText("四十八个月");
  await preview.getByRole("button", { name: "Close", exact: true }).click();
  await document.getByRole("button", { name: "激活版本" }).click();
  const review = page.getByRole("dialog", { name: "核对 PDF 阅读顺序" });
  await expect(
    review.getByRole("button", { name: "确认并激活" }),
  ).toBeDisabled();
  await review
    .getByRole("checkbox", { name: "我已核对双栏顺序和表格行列" })
    .check();
  await review.getByRole("button", { name: "确认并激活" }).click();
  await expect(
    document.getByRole("button", { name: "当前版本" }),
  ).toBeVisible();
  await document.getByRole("button", { name: "撤回文档" }).click();
  await expect(document).toContainText("已撤回");
  // Version uploads must carry the independently selected layout, too.
  await document.getByLabel(`${title} PDF 阅读方式`).click();
  await page.getByText("表格行列对齐", { exact: true }).last().click();
  await document
    .getByLabel(`上传${title}新版本`)
    .setInputFiles(
      path.resolve("../flow-desk-agent/tests/fixtures/layout-table.pdf"),
    );
  const table = document
    .locator(".knowledge-version")
    .filter({ hasText: "layout-table.pdf" });
  await expect(table.getByRole("button", { name: "激活版本" })).toBeEnabled({
    timeout: 45000,
  });
  await expect(table).toContainText("表格行列对齐");
  await table.getByRole("button", { name: "预览提取文本" }).click();
  const tableText = await preview.locator(".pdf-preview").innerText();
  const row = tableText
    .split("\n")
    .find((line) => line.includes("天琴专业设备"));
  expect(row).toContain("六十个月");
  expect(row).toContain("设备编号与发票");
  expect(errors).toEqual([]);
});

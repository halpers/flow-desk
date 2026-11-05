import { expect, test, type Page } from "@playwright/test";
import fs from "node:fs";

async function login(page: Page) {
  await page.goto("/");
  await page.getByLabel("工作账号").fill("demo.admin");
  await page
    .getByLabel("密码", { exact: true })
    .fill(process.env.FLOWDESK_TEST_PASSWORD!);
  await page.getByRole("button", { name: "登录工作台" }).click();
  await page.getByRole("button", { name: "知识库", exact: true }).click();
}

test("retention preview protects recent versions", async ({ page }) => {
  await login(page);
  const title = "Retention protection " + Date.now();
  await page.getByLabel("文档标题").fill(title);
  await page
    .getByLabel("知识文件")
    .setInputFiles({
      name: "retention-recent.txt",
      mimeType: "text/plain",
      buffer: Buffer.from("Synthetic protected recent policy."),
    });
  await page.getByRole("button", { name: "上传并索引" }).click();
  const document = page
    .locator(".knowledge-document")
    .filter({ hasText: title });
  await expect(document.getByRole("button", { name: "激活版本" })).toBeEnabled({
    timeout: 30000,
  });
  await document.getByRole("button", { name: "版本保留与清理" }).click();
  const drawer = page.getByRole("dialog", {
    name: "版本保留与清理",
    exact: true,
  });
  await expect(drawer).toContainText("最近版本保留");
  await expect(drawer).toContainText("未满最短保留期");
  await expect(
    drawer.getByRole("button", { name: "清理此版本" }),
  ).toBeDisabled();
});

test("confirmed cleanup recovers a lost response and disables original reads", async ({
  page,
}) => {
  test.skip(
    !process.env.FLOWDESK_RETENTION_FIXTURE,
    "Requires disposable aged versions from check-retention.py",
  );
  const fixture = JSON.parse(
    fs.readFileSync(process.env.FLOWDESK_RETENTION_FIXTURE!, "utf8"),
  );
  const errors: string[] = [];
  page.on("pageerror", (error) => errors.push(error.name));
  await login(page);
  await page.getByLabel("选择知识库").click();
  await page.getByText(fixture.base_name, { exact: true }).last().click();
  const document = page
    .locator(".knowledge-document")
    .filter({ hasText: "Retention browser fixture" });
  await document.getByRole("button", { name: "版本保留与清理" }).click();
  const drawer = page.getByRole("dialog", {
    name: "版本保留与清理",
    exact: true,
  });
  const candidate = drawer
    .locator(".retention-item")
    .filter({ hasText: "retention-0.txt" });
  await expect(
    candidate.getByRole("button", { name: "清理此版本" }),
  ).toBeEnabled();
  await candidate.getByRole("button", { name: "清理此版本" }).click();
  const confirm = page.getByRole("dialog", {
    name: "确认清理知识版本",
    exact: true,
  });
  await expect(
    confirm.getByRole("button", { name: "确认不可撤销清理" }),
  ).toBeDisabled();
  await confirm.getByText("我已确认备份与清理范围", { exact: true }).click();
  let lost = false;
  await page.route(
    `**/versions/${fixture.version_id}/cleanup`,
    async (route) => {
      if (lost) return route.continue();
      const response = await route.fetch();
      expect(response.status()).toBe(202);
      lost = true;
      await route.abort("failed");
    },
  );
  await confirm.getByRole("button", { name: "确认不可撤销清理" }).click();
  await expect(confirm.getByRole("alert")).toBeVisible();
  await confirm.getByRole("button", { name: "确认不可撤销清理" }).click();
  await expect(confirm).toHaveCount(0);
  await expect
    .poll(async () => {
      await drawer.getByRole("button", { name: "刷新清理预览" }).click();
      return candidate.textContent();
    })
    .toContain("内容已清理，元数据保留");
  await drawer.getByRole("button", { name: "Close", exact: true }).click();
  const version = document
    .locator(".knowledge-version")
    .filter({ hasText: "retention-0.txt" });
  await document.getByRole("button", { name: "刷新版本" }).click();
  await expect(version).toContainText("内容已清理");
  await expect(
    version.getByRole("button", { name: "下载原文件" }),
  ).toBeDisabled();
  await expect(
    version.getByRole("button", { name: "激活版本" }),
  ).toBeDisabled();
  expect(lost).toBe(true);
  expect(errors).toEqual([]);
});

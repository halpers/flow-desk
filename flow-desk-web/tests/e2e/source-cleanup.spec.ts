import { expect, test } from "@playwright/test";
import fs from "node:fs/promises";
import path from "node:path";

test("archive copy cleanup confirms, recovers lost response and preserves original", async ({
  page,
}) => {
  test.skip(
    !process.env.FLOWDESK_SOURCE_CLEANUP_FIXTURE,
    "Requires disposable S3 fixture from check-source-cleanup.py",
  );
  const fixture = JSON.parse(
    await fs.readFile(process.env.FLOWDESK_SOURCE_CLEANUP_FIXTURE!, "utf8"),
  );
  const errors: string[] = [];
  page.on("pageerror", (error) => errors.push(error.name));
  await page.goto("/");
  await page.getByLabel("工作账号").fill("demo.admin");
  await page
    .getByLabel("密码", { exact: true })
    .fill(process.env.FLOWDESK_TEST_PASSWORD!);
  await page.getByRole("button", { name: "登录工作台" }).click();
  await page.getByRole("button", { name: "知识库", exact: true }).click();
  await page.getByLabel("选择知识库").click();
  await page.getByText(fixture.base_name, { exact: true }).last().click();
  const document = page
    .locator(".knowledge-document")
    .filter({ hasText: "Archive cleanup acceptance" });
  await document
    .getByRole("button", { name: "归档副本清理", exact: true })
    .click();
  const drawer = page.getByRole("dialog", {
    name: "归档副本清理",
    exact: true,
  });
  const candidate = drawer
    .locator(".archive-copy")
    .filter({ hasText: fixture.copy_id.slice(0, 8) });
  await expect(candidate).toContainText("请先将原件恢复至数据库");
  await expect(
    candidate.getByRole("button", { name: "清理此归档副本" }),
  ).toBeDisabled();
  await drawer.getByRole("button", { name: "Close", exact: true }).click();
  await document
    .getByRole("button", { name: "恢复至数据库", exact: true })
    .click();
  await expect(
    document.getByRole("button", { name: "归档原文件" }),
  ).toBeVisible();
  await document
    .getByRole("button", { name: "归档副本清理", exact: true })
    .click();
  await drawer.getByRole("button", { name: "刷新归档副本" }).click();
  await expect(
    candidate.getByRole("button", { name: "清理此归档副本" }),
  ).toBeEnabled();
  await candidate.getByRole("button", { name: "清理此归档副本" }).click();
  const confirm = page.getByRole("dialog", {
    name: "确认清理归档副本",
    exact: true,
  });
  await expect(
    confirm.getByRole("button", { name: "确认清理副本字节" }),
  ).toBeDisabled();
  await confirm.getByText("我已核对数据库原件与备份", { exact: true }).click();
  let lost = false;
  await page.route(
    `**/archive-copies/${fixture.copy_id}/cleanup`,
    async (route) => {
      if (lost) return route.continue();
      const response = await route.fetch();
      expect(response.status()).toBe(202);
      lost = true;
      await route.abort("failed");
    },
  );
  await confirm.getByRole("button", { name: "确认清理副本字节" }).click();
  await expect(confirm.getByRole("alert")).toBeVisible();
  await confirm.getByRole("button", { name: "确认清理副本字节" }).click();
  await expect(confirm).toHaveCount(0);
  await expect
    .poll(async () => {
      await drawer.getByRole("button", { name: "刷新归档副本" }).click();
      return candidate.textContent();
    })
    .toContain("副本字节已清理");
  await drawer.getByRole("button", { name: "Close", exact: true }).click();
  const downloaded = page.waitForEvent("download");
  await document.getByRole("button", { name: "下载原文件" }).click();
  expect(await fs.readFile((await (await downloaded).path())!)).toEqual(
    await fs.readFile(
      path.resolve("../flow-desk-agent/tests/fixtures/policy.pdf"),
    ),
  );
  expect(lost).toBe(true);
  expect(errors).toEqual([]);
});

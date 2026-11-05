import { expect, test } from "@playwright/test";
import fs from "node:fs/promises";

test("complete history pages and metadata confirmation recover a lost response", async ({
  page,
}) => {
  test.skip(
    !process.env.FLOWDESK_HISTORY_FIXTURE,
    "Requires disposable history archive fixture",
  );
  const fixture = JSON.parse(
    await fs.readFile(process.env.FLOWDESK_HISTORY_FIXTURE!, "utf8"),
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
    .filter({ hasText: "History archive acceptance" });
  await document.getByRole("button", { name: "完整版本历史" }).click();
  const drawer = page.getByRole("dialog", {
    name: "完整版本历史",
    exact: true,
  });
  await expect(drawer.locator(".history-item")).toHaveCount(20);
  const firstIDs = await drawer
    .locator(".history-item p:first-of-type")
    .allTextContents();
  await drawer.getByRole("button", { name: "下一页", exact: true }).click();
  await expect(drawer.getByText("第 2 页", { exact: true })).toBeVisible();
  await expect(drawer.locator(".history-item")).toHaveCount(20);
  const secondIDs = await drawer
    .locator(".history-item p:first-of-type")
    .allTextContents();
  expect(secondIDs.some((id) => firstIDs.includes(id))).toBe(false);
  await drawer.getByRole("button", { name: "上一页", exact: true }).click();
  await expect(drawer.getByText("第 1 页", { exact: true })).toBeVisible();
  const item = drawer
    .locator(".history-item")
    .filter({ hasText: fixture.version_id });
  await item.getByRole("button", { name: "归档版本详情", exact: true }).click();
  const confirm = page.getByRole("dialog", {
    name: "确认归档版本详情",
    exact: true,
  });
  await expect(
    confirm.getByRole("button", { name: "确认归档详情", exact: true }),
  ).toBeDisabled();
  await confirm.getByText("我已核对归档范围与备份", { exact: true }).click();
  let lost = false;
  const keys: string[] = [];
  const path = `**/versions/${fixture.version_id}/archive-metadata`;
  await page.route(path, async (route) => {
    keys.push(route.request().headers()["idempotency-key"]);
    if (lost) return route.continue();
    const response = await route.fetch();
    expect(response.status()).toBe(200);
    lost = true;
    await route.abort("failed");
  });
  await confirm
    .getByRole("button", { name: "确认归档详情", exact: true })
    .click();
  await expect(confirm.getByRole("alert")).toBeVisible();
  await confirm
    .getByRole("button", { name: "确认归档详情", exact: true })
    .click();
  await expect(confirm).toHaveCount(0);
  await expect(item).toContainText("元数据已归档");
  await expect(item).toContainText(fixture.filename);
  await expect(
    item.getByRole("button", { name: "归档版本详情", exact: true }),
  ).toBeDisabled();
  expect(keys).toHaveLength(2);
  expect(keys[0]).toBeTruthy();
  expect(keys[0]).toBe(keys[1]);
  expect(errors).toEqual([]);
});

import { expect, test } from "@playwright/test";
import fs from "node:fs/promises";

test("full copy history pages and archive confirmation recover a lost committed response", async ({
  page,
}) => {
  test.skip(
    !process.env.FLOWDESK_SOURCE_HISTORY_FIXTURE,
    "Requires disposable source history fixture",
  );
  const fixture = JSON.parse(
    await fs.readFile(process.env.FLOWDESK_SOURCE_HISTORY_FIXTURE!, "utf8"),
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
    .filter({ hasText: "Source history acceptance" });
  await document
    .getByRole("button", { name: "归档副本清理", exact: true })
    .click();
  const copies = page.getByRole("dialog", {
    name: "归档副本清理",
    exact: true,
  });
  await copies
    .getByRole("button", { name: "完整副本历史", exact: true })
    .click();
  const history = page.getByRole("dialog", {
    name: "完整副本历史",
    exact: true,
  });
  await expect(history.locator(".source-history-item")).toHaveCount(20);
  const firstIDs = await history
    .locator(".source-history-item strong")
    .allTextContents();
  await history.getByRole("button", { name: "下一页", exact: true }).click();
  await expect(history.getByText("第 2 页", { exact: true })).toBeVisible();
  await expect(history.locator(".source-history-item")).toHaveCount(20);
  const secondIDs = await history
    .locator(".source-history-item strong")
    .allTextContents();
  expect(secondIDs.some((id) => firstIDs.includes(id))).toBe(false);
  await history.getByRole("button", { name: "上一页", exact: true }).click();
  const item = history
    .locator(".source-history-item")
    .filter({ hasText: fixture.copy_id });
  await item
    .getByRole("button", { name: "归档此副本登记", exact: true })
    .click();
  const confirm = page.getByRole("dialog", {
    name: "确认归档副本登记",
    exact: true,
  });
  await expect(
    confirm.getByRole("button", { name: "确认归档登记", exact: true }),
  ).toBeDisabled();
  await confirm.getByText("我已核对退休登记与备份", { exact: true }).click();
  const keys: string[] = [];
  const bodies: string[] = [];
  let lost = false;
  await page.route(
    `**/archive-copies/${fixture.copy_id}/archive-metadata`,
    async (route) => {
      keys.push(route.request().headers()["idempotency-key"]);
      bodies.push(route.request().postData()!);
      if (lost) return route.continue();
      const response = await route.fetch();
      expect(response.status()).toBe(200);
      lost = true;
      await route.abort("failed");
    },
  );
  await confirm
    .getByRole("button", { name: "确认归档登记", exact: true })
    .click();
  await expect(confirm.getByRole("alert")).toBeVisible();
  await confirm
    .getByRole("button", { name: "确认归档登记", exact: true })
    .click();
  await expect(confirm).toHaveCount(0);
  await expect(item).toContainText("登记已归档");
  await expect(
    item.getByRole("button", { name: "归档此副本登记", exact: true }),
  ).toBeDisabled();
  expect(keys).toHaveLength(2);
  expect(keys[0]).toBeTruthy();
  expect(keys[0]).toBe(keys[1]);
  expect(bodies[0]).toBe(bodies[1]);
  for (let pageNumber = 2; pageNumber <= 6; pageNumber++) {
    await history.getByRole("button", { name: "下一页", exact: true }).click();
    await expect(
      history.getByText(`第 ${pageNumber} 页`, { exact: true }),
    ).toBeVisible();
    await expect(history.locator(".source-history-item")).toHaveCount(
      pageNumber === 6 ? 9 : 20,
    );
  }
  await expect(
    history.getByRole("button", { name: "下一页", exact: true }),
  ).toBeDisabled();
  await history
    .getByRole("button", { name: "刷新副本历史", exact: true })
    .click();
  await expect(history.getByText("第 1 页", { exact: true })).toBeVisible();
  await expect(history.locator(".source-history-item")).toHaveCount(20);
  let blocked = false;
  await page.route("**/archive-copies/history?**", async (route) => {
    if (blocked) return route.continue();
    blocked = true;
    await route.fulfill({
      status: 403,
      contentType: "application/json",
      body: JSON.stringify({
        code: "FORBIDDEN",
        request_id: "synthetic-history-revoked",
      }),
    });
  });
  await history
    .getByRole("button", { name: "刷新副本历史", exact: true })
    .click();
  await expect(
    history.getByRole("alert").filter({ hasText: "副本历史加载失败" }),
  ).toBeVisible();
  await expect(history.locator(".source-history-item")).toHaveCount(0);
  await expect(
    history.getByRole("button", { name: "下一页", exact: true }),
  ).toBeDisabled();
  await history
    .getByRole("button", { name: "刷新副本历史", exact: true })
    .click();
  await expect(history.locator(".source-history-item")).toHaveCount(20);
  expect(errors).toEqual([]);
});

import { expect, test } from "@playwright/test";
import fs from "node:fs/promises";

test("object inventory reconciles read-only pages and refreshes from the beginning", async ({
  page,
}) => {
  test.skip(
    !process.env.FLOWDESK_SOURCE_INVENTORY_FIXTURE,
    "Requires disposable S3 inventory fixture",
  );
  const fixture = JSON.parse(
    await fs.readFile(process.env.FLOWDESK_SOURCE_INVENTORY_FIXTURE!, "utf8"),
  );
  const errors: string[] = [];
  const requests: string[] = [];
  page.on("pageerror", (error) => errors.push(error.name));
  page.on("request", (request) => {
    if (request.url().includes(`/versions/${fixture.version_id}/`))
      requests.push(request.method());
  });
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
    .filter({ hasText: "Source inventory acceptance" });
  await document.getByRole("button", { name: "对象巡检", exact: true }).click();
  const drawer = page.getByRole("dialog", {
    name: "对象巡检与登记对账",
    exact: true,
  });
  await expect(drawer).toContainText("巡检不核验原文内容");
  await expect(drawer.locator(".source-inventory-item")).toHaveCount(20);
  await expect(drawer).toContainText("未登记，待人工核对");
  const first = await drawer
    .locator(".source-inventory-item")
    .first()
    .textContent();
  await drawer.getByRole("button", { name: "下一页", exact: true }).click();
  await expect(drawer.locator(".source-inventory-item")).toHaveCount(3);
  await expect(
    drawer.getByRole("button", { name: "下一页", exact: true }),
  ).toBeDisabled();
  await drawer.getByRole("button", { name: "上一页", exact: true }).click();
  await expect(drawer.locator(".source-inventory-item")).toHaveCount(20);
  await expect(drawer.locator(".source-inventory-item").first()).toHaveText(
    first!,
  );
  await drawer.getByRole("button", { name: "下一页", exact: true }).click();
  await expect(drawer.locator(".source-inventory-item")).toHaveCount(3);
  await drawer.getByRole("button", { name: "刷新巡检", exact: true }).click();
  await expect(drawer.locator(".source-inventory-item")).toHaveCount(20);
  await expect(
    drawer.getByRole("button", { name: "上一页", exact: true }),
  ).toBeDisabled();
  await expect(drawer).not.toContainText("knowledge/");
  await expect(
    drawer.getByRole("button", { name: /清理|删除|认领/ }),
  ).toHaveCount(0);
  expect(requests.length).toBeGreaterThan(1);
  expect(requests.every((method) => method === "GET")).toBe(true);
  expect(errors).toEqual([]);
});

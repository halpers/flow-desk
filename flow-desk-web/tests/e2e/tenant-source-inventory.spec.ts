import { expect, test } from "@playwright/test";
import fs from "node:fs/promises";

test("tenant inventory shows unknown objects, pages read-only and opens authorized version inspection", async ({
  page,
}) => {
  test.skip(
    !process.env.FLOWDESK_TENANT_SOURCE_INVENTORY_FIXTURE,
    "Requires disposable tenant S3 fixture",
  );
  const fixture = JSON.parse(
    await fs.readFile(
      process.env.FLOWDESK_TENANT_SOURCE_INVENTORY_FIXTURE!,
      "utf8",
    ),
  );
  const errors: string[] = [];
  const methods: string[] = [];
  page.on("pageerror", (error) => errors.push(error.name));
  page.on("request", (request) => {
    if (new URL(request.url()).pathname === "/api/v1/source-inventory")
      methods.push(request.method());
  });
  await page.goto("/");
  await page.getByLabel("工作账号").fill(fixture.account);
  await page
    .getByLabel("密码", { exact: true })
    .fill(process.env.FLOWDESK_TEST_PASSWORD!);
  await page.getByRole("button", { name: "登录工作台" }).click();
  await page.getByRole("button", { name: "知识库", exact: true }).click();
  await page.getByRole("button", { name: "租户对象巡检", exact: true }).click();
  const drawer = page.getByRole("dialog", {
    name: "租户对象巡检与归属核对",
    exact: true,
  });
  await expect(drawer.locator(".tenant-source-inventory-item")).toHaveCount(20);
  await expect(drawer).toContainText("对象名不能证明归属");
  const first = await drawer
    .locator(".tenant-source-inventory-item")
    .first()
    .textContent();
  let known = drawer
    .locator(".tenant-source-inventory-item")
    .filter({ hasText: fixture.version_id });
  if ((await known.count()) === 0) {
    await drawer.getByRole("button", { name: "下一页", exact: true }).click();
    await expect(drawer.locator(".tenant-source-inventory-item")).toHaveCount(
      5,
    );
    await expect(
      drawer.getByRole("button", { name: "下一页", exact: true }),
    ).toBeDisabled();
    await drawer.getByRole("button", { name: "上一页", exact: true }).click();
    await expect(drawer.locator(".tenant-source-inventory-item")).toHaveCount(
      20,
    );
    await expect(
      drawer.locator(".tenant-source-inventory-item").first(),
    ).toHaveText(first!);
    await drawer.getByRole("button", { name: "下一页", exact: true }).click();
    await expect(drawer.locator(".tenant-source-inventory-item")).toHaveCount(
      5,
    );
  }
  known = drawer
    .locator(".tenant-source-inventory-item")
    .filter({ hasText: fixture.version_id });
  await known
    .first()
    .getByRole("button", { name: "对象巡检", exact: true })
    .click();
  const versionDrawer = page.getByRole("dialog", {
    name: "对象巡检与登记对账",
    exact: true,
  });
  await expect(versionDrawer).toContainText("当前原件，列表大小匹配");
  await versionDrawer.getByRole("button", { name: "Close" }).click();
  await drawer.getByRole("button", { name: "刷新巡检", exact: true }).click();
  await expect(drawer.locator(".tenant-source-inventory-item")).toHaveCount(20);
  await drawer.getByRole("button", { name: "下一页", exact: true }).click();
  await expect(drawer.locator(".tenant-source-inventory-item")).toHaveCount(5);
  await expect(
    drawer.getByRole("button", { name: "下一页", exact: true }),
  ).toBeDisabled();
  await expect(drawer).not.toContainText("knowledge/");
  await expect(
    drawer.getByRole("button", { name: /删除|认领|修复/ }),
  ).toHaveCount(0);
  expect(methods.length).toBeGreaterThan(1);
  expect(methods.every((method) => method === "GET")).toBe(true);
  expect(errors).toEqual([]);
});

test("ordinary operator cannot see tenant inventory entry", async ({
  page,
}) => {
  await page.goto("/");
  await page.getByLabel("工作账号").fill("demo.operator");
  await page
    .getByLabel("密码", { exact: true })
    .fill(process.env.FLOWDESK_TEST_PASSWORD!);
  await page.getByRole("button", { name: "登录工作台" }).click();
  await page.getByRole("button", { name: "知识库", exact: true }).click();
  await expect(
    page.getByRole("button", { name: "租户对象巡检", exact: true }),
  ).toHaveCount(0);
});

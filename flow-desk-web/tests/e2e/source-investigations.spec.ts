import { expect, test } from "@playwright/test";
import fs from "node:fs/promises";

test("investigation confirmation, lost-response recovery, stale edit and denied refresh", async ({
  page,
}) => {
  test.skip(
    !process.env.FLOWDESK_SOURCE_INVESTIGATIONS_FIXTURE,
    "Requires disposable investigation fixture",
  );
  const fixture = JSON.parse(
    await fs.readFile(
      process.env.FLOWDESK_SOURCE_INVESTIGATIONS_FIXTURE!,
      "utf8",
    ),
  );
  const errors: string[] = [];
  page.on("pageerror", (error) => errors.push(error.name));
  await page.goto("/");
  await page.getByLabel("工作账号").fill(fixture.account);
  await page
    .getByLabel("密码", { exact: true })
    .fill(process.env.FLOWDESK_TEST_PASSWORD!);
  await page.getByRole("button", { name: "登录工作台" }).click();
  await page.getByRole("button", { name: "知识库", exact: true }).click();
  await page.getByRole("button", { name: "租户对象巡检", exact: true }).click();
  const inventory = page.getByRole("dialog", {
    name: "租户对象巡检与归属核对",
    exact: true,
  });
  await expect(inventory.locator(".tenant-source-inventory-item")).toHaveCount(
    20,
  );
  const item = inventory
    .locator(".tenant-source-inventory-item")
    .filter({ hasText: fixture.object_ref });
  if (!(await item.count())) {
    await inventory
      .getByRole("button", { name: "下一页", exact: true })
      .click();
    await expect(
      inventory.locator(".tenant-source-inventory-item"),
    ).toHaveCount(5);
  }
  await item.getByRole("button", { name: "调查记录", exact: true }).click();
  const drawer = page.getByRole("dialog", {
    name: "巡检调查记录",
    exact: true,
  });
  await expect(drawer).toContainText("修订号：0");
  const note = "Synthetic browser review <script>literal text</script>";
  await drawer.getByLabel("调查说明", { exact: true }).fill(note);
  await drawer
    .getByRole("button", { name: "保存调查记录", exact: true })
    .click();
  const confirmation = page.getByRole("dialog", {
    name: "确认保存调查记录",
    exact: true,
  });
  await expect(
    confirmation.getByRole("button", { name: "确认保存", exact: true }),
  ).toBeDisabled();
  await confirmation.getByLabel("我确认保存以上调查记录").check();
  const requests: { key: string; body: string }[] = [];
  let receipt: { revision: number; note: string } | null = null;
  const route = `**/api/v1/source-investigations/${fixture.object_ref}`;
  await page.route(route, async (requestRoute) => {
    const request = requestRoute.request();
    if (request.method() !== "PUT") {
      await requestRoute.fallback();
      return;
    }
    requests.push({
      key: request.headers()["idempotency-key"],
      body: request.postData()!,
    });
    const response = await requestRoute.fetch();
    expect(response.status()).toBe(200);
    receipt = (await response.json()).data;
    if (requests.length === 1) await requestRoute.abort("failed");
    else await requestRoute.fulfill({ response });
  });
  await confirmation
    .getByRole("button", { name: "确认保存", exact: true })
    .click();
  await expect(confirmation).toContainText("Failed to fetch");
  await expect(drawer.getByLabel("调查说明", { exact: true })).toBeDisabled();
  await confirmation
    .getByRole("button", { name: "重试保存调查记录", exact: true })
    .click();
  await expect(confirmation).toBeHidden();
  expect(requests).toHaveLength(2);
  expect(requests[1]).toEqual(requests[0]);
  expect(receipt).toMatchObject({ revision: 1, note });
  await expect(drawer).toContainText("修订号：1");
  await expect(drawer.getByLabel("调查说明", { exact: true })).toHaveValue(
    note,
  );
  await page.unroute(route);
  // A concurrent administrator saves after this editor's revision was loaded.
  await page.route(route, async (requestRoute) => {
    if (requestRoute.request().method() !== "PUT") {
      await requestRoute.fallback();
      return;
    }
    const body = requestRoute.request().postDataJSON();
    const concurrent = await requestRoute.fetch({
      headers: {
        ...requestRoute.request().headers(),
        "Idempotency-Key": crypto.randomUUID(),
      },
      data: { ...body, note: "Concurrent synthetic review" },
    });
    expect(concurrent.status()).toBe(200);
    const stale = await requestRoute.fetch();
    expect(stale.status()).toBe(409);
    await requestRoute.fulfill({ response: stale });
  });
  await drawer.getByLabel("调查说明", { exact: true }).fill("Stale local edit");
  await drawer
    .getByRole("button", { name: "保存调查记录", exact: true })
    .click();
  await confirmation.getByLabel("我确认保存以上调查记录").check();
  await confirmation
    .getByRole("button", { name: "确认保存", exact: true })
    .click();
  await expect(confirmation).toContainText("调查记录已变化");
  await page.unroute(route);
  await confirmation
    .getByRole("button", { name: "放弃重试并重新读取", exact: true })
    .click();
  await expect(drawer).toContainText("修订号：2");
  await expect(drawer.getByLabel("调查说明", { exact: true })).toHaveValue(
    "Concurrent synthetic review",
  );
  await drawer.getByRole("button", { name: "Close", exact: true }).click();
  await inventory.getByLabel("调查对象指纹").fill(fixture.object_ref);
  await inventory
    .getByRole("button", { name: "查询调查记录", exact: true })
    .click();
  await expect(drawer.getByLabel("调查说明", { exact: true })).toHaveValue(
    "Concurrent synthetic review",
  );
  // ACL revocation uses the existing 404 resource-hiding contract on save.
  await page.route(route, async (requestRoute) => {
    if (requestRoute.request().method() !== "PUT") {
      await requestRoute.fallback();
      return;
    }
    await requestRoute.fulfill({
      status: 404,
      contentType: "application/json",
      body: JSON.stringify({
        code: "RESOURCE_NOT_FOUND",
        message: "Synthetic resource access hidden",
      }),
    });
  });
  await drawer
    .getByLabel("调查说明", { exact: true })
    .fill("Must be hidden after access changes");
  await drawer
    .getByRole("button", { name: "保存调查记录", exact: true })
    .click();
  await confirmation.getByLabel("我确认保存以上调查记录").check();
  await confirmation
    .getByRole("button", { name: "确认保存", exact: true })
    .click();
  await expect(confirmation).toBeHidden();
  await expect(drawer).toContainText("Synthetic resource access hidden");
  await expect(drawer.getByLabel("调查说明", { exact: true })).toHaveCount(0);
  await expect(drawer).not.toContainText("Must be hidden after access changes");
  await page.unroute(route);
  await drawer
    .getByRole("button", { name: "重新读取调查记录", exact: true })
    .click();
  await expect(drawer.getByLabel("调查说明", { exact: true })).toHaveValue(
    "Concurrent synthetic review",
  );
  await page.route(route, async (requestRoute) =>
    requestRoute.fulfill({
      status: 403,
      contentType: "application/json",
      body: JSON.stringify({
        code: "FORBIDDEN",
        message: "Synthetic permission revoked",
      }),
    }),
  );
  await drawer
    .getByRole("button", { name: "重新读取调查记录", exact: true })
    .click();
  await expect(drawer).toContainText("Synthetic permission revoked");
  await expect(drawer.getByLabel("调查说明", { exact: true })).toHaveCount(0);
  await expect(drawer).not.toContainText("Concurrent synthetic review");
  await page.unroute(route);
  expect(errors).toEqual([]);
});

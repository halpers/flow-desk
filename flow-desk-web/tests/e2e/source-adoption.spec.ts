import { expect, test } from "@playwright/test";
import fs from "node:fs/promises";

test("historical copy requires restore and confirmation, then recovers a lost registration response", async ({
  page,
}) => {
  test.skip(
    !process.env.FLOWDESK_SOURCE_ADOPTION_FIXTURE,
    "Requires disposable S3 adoption fixture",
  );
  const fixture = JSON.parse(
    await fs.readFile(process.env.FLOWDESK_SOURCE_ADOPTION_FIXTURE!, "utf8"),
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
    .filter({ hasText: fixture.document_title });
  await document.getByRole("button", { name: "对象巡检", exact: true }).click();
  const drawer = page.getByRole("dialog", {
    name: "对象巡检与登记对账",
    exact: true,
  });
  const candidate = drawer.locator(".source-inventory-item");
  await expect(candidate).toContainText("请先恢复并核对数据库原件");
  await expect(
    candidate.getByRole("button", { name: "核验并登记副本", exact: true }),
  ).toBeDisabled();
  await drawer.getByRole("button", { name: "Close", exact: true }).click();
  await document
    .getByRole("button", { name: "恢复至数据库", exact: true })
    .click();
  await expect(
    document.getByRole("button", { name: "归档原文件", exact: true }),
  ).toBeVisible();
  await document.getByRole("button", { name: "对象巡检", exact: true }).click();
  await expect(
    candidate.getByRole("button", { name: "核验并登记副本", exact: true }),
  ).toBeEnabled();
  await candidate
    .getByRole("button", { name: "核验并登记副本", exact: true })
    .click();
  const confirm = page.getByRole("dialog", {
    name: "确认核验并登记副本",
    exact: true,
  });
  const submit = confirm.getByRole("button", {
    name: "确认核验并登记",
    exact: true,
  });
  await expect(submit).toBeDisabled();
  await confirm
    .getByText("我已恢复并核对数据库原件，同意登记此副本", { exact: true })
    .click();
  let lost = false;
  let firstReceipt: unknown;
  const keys: string[] = [];
  await page.route(
    `**/versions/${fixture.version_id}/source-adoptions`,
    async (route) => {
      keys.push(route.request().headers()["idempotency-key"]);
      if (lost) return route.continue();
      const response = await route.fetch();
      expect(response.status()).toBe(200);
      firstReceipt = (await response.json()).data;
      lost = true;
      await route.abort("failed");
    },
  );
  await submit.click();
  await expect(confirm.getByRole("alert")).toBeVisible();
  const replay = page.waitForResponse(
    (response) =>
      response
        .url()
        .endsWith(`/versions/${fixture.version_id}/source-adoptions`) &&
      response.status() === 200,
  );
  await submit.click();
  expect((await (await replay).json()).data).toEqual(firstReceipt);
  await expect(confirm).toHaveCount(0);
  await expect(candidate).toContainText("已登记，列表大小匹配");
  await expect(
    candidate.getByRole("button", { name: "核验并登记副本", exact: true }),
  ).toHaveCount(0);
  await drawer.getByRole("button", { name: "Close", exact: true }).click();
  await document
    .getByRole("button", { name: "归档副本清理", exact: true })
    .click();
  const copies = page.getByRole("dialog", {
    name: "归档副本清理",
    exact: true,
  });
  await expect(copies.locator(".archive-copy")).toHaveCount(1);
  await expect(
    copies.getByRole("button", { name: "清理此归档副本", exact: true }),
  ).toBeEnabled();
  expect(lost).toBe(true);
  expect(keys).toHaveLength(2);
  expect(keys[0]).toBe(keys[1]);
  expect(errors).toEqual([]);
});

import { expect, test } from "@playwright/test";
import fs from "node:fs/promises";

test("investigation worklist pages, filters, edits with confirmation and hides stale results", async ({
  page,
}) => {
  test.skip(
    !process.env.FLOWDESK_INVESTIGATION_WORKLIST_FIXTURE,
    "Requires disposable investigation worklist fixture",
  );
  const fixture = JSON.parse(
    await fs.readFile(
      process.env.FLOWDESK_INVESTIGATION_WORKLIST_FIXTURE!,
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
  await page.getByRole("button", { name: "调查工作台", exact: true }).click();
  const drawer = page.getByRole("dialog", {
    name: "巡检调查工作台",
    exact: true,
  });
  const rows = drawer.locator(".investigation-worklist-item");
  const refs: string[] = [];
  const pages = Math.ceil(fixture.total / 20);
  for (let index = 0; index < pages; index++) {
    await expect(rows).toHaveCount(Math.min(20, fixture.total - index * 20));
    for (const text of await rows.allTextContents())
      refs.push(text.match(/[0-9a-f]{64}/)![0]);
    if (index < pages - 1)
      await drawer.getByRole("button", { name: "下一页", exact: true }).click();
  }
  expect(refs).toHaveLength(fixture.total);
  expect(new Set(refs).size).toBe(fixture.total);
  await expect(
    drawer.getByRole("button", { name: "下一页", exact: true }),
  ).toBeDisabled();
  await drawer.getByRole("button", { name: "上一页", exact: true }).click();
  await expect(rows).toHaveCount(20);
  await drawer
    .getByRole("button", { name: "刷新调查列表", exact: true })
    .click();
  await expect(drawer).toContainText("第 1 页");
  await expect(rows).toHaveCount(20);
  await expect(rows.first()).toContainText(refs[0]);
  await expect(drawer).not.toContainText("PRIVATE_WORKLIST");
  await expect(drawer).not.toContainText("knowledge/");
  await drawer.getByRole("combobox", { name: "筛选跟进状态" }).click();
  await page
    .locator(
      ".ant-select-dropdown:not(.ant-select-dropdown-hidden) .ant-select-item-option",
    )
    .filter({ hasText: "待跟进" })
    .click();
  await expect(rows).toHaveCount(20);
  await expect(rows.first()).toContainText("待跟进");
  const selected = (await rows.first().textContent())!.match(
    /[0-9a-f]{64}/,
  )![0];
  await rows
    .first()
    .getByRole("button", { name: "继续跟进", exact: true })
    .click();
  const editor = page.getByRole("dialog", {
    name: "巡检调查记录",
    exact: true,
  });
  await expect(editor.getByLabel("调查说明", { exact: true })).toHaveValue(
    "PRIVATE_WORKLIST_PAGINATION_NOTE",
  );
  await editor.getByLabel("跟进状态", { exact: true }).click();
  await page
    .locator(
      ".ant-select-dropdown:not(.ant-select-dropdown-hidden) .ant-select-item-option",
    )
    .filter({ hasText: "已结束跟进" })
    .click();
  await editor
    .getByLabel("调查说明", { exact: true })
    .fill("Synthetic worklist follow-up completed");
  await editor
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
  await confirmation
    .getByRole("button", { name: "确认保存", exact: true })
    .click();
  await expect(confirmation).toBeHidden();
  await expect(editor).toContainText("修订号：2");
  await editor.getByRole("button", { name: "Close", exact: true }).click();
  await expect(rows.filter({ hasText: selected })).toHaveCount(0);
  await drawer.getByRole("combobox", { name: "筛选跟进状态" }).click();
  await page
    .locator(
      ".ant-select-dropdown:not(.ant-select-dropdown-hidden) .ant-select-item-option",
    )
    .filter({ hasText: "已结束跟进" })
    .click();
  await expect(rows).toHaveCount(20);
  for (
    let index = 0;
    index < 3 && !(await rows.filter({ hasText: selected }).count());
    index++
  ) {
    await drawer.getByRole("button", { name: "下一页", exact: true }).click();
    await expect(
      drawer.getByText("正在读取调查列表…", { exact: true }),
    ).toHaveCount(0);
  }
  await expect(rows.filter({ hasText: selected })).toContainText("修订 2");
  await rows
    .filter({ hasText: selected })
    .getByRole("button", { name: "继续跟进", exact: true })
    .click();
  await expect(editor.getByLabel("调查说明", { exact: true })).toHaveValue(
    "Synthetic worklist follow-up completed",
  );
  await editor.getByRole("button", { name: "Close", exact: true }).click();
  await page.route("**/api/v1/source-investigations?*", async (route) =>
    route.fulfill({
      status: 403,
      contentType: "application/json",
      body: JSON.stringify({
        code: "PERMISSION_DENIED",
        message: "Synthetic worklist access revoked",
      }),
    }),
  );
  await drawer
    .getByRole("button", { name: "刷新调查列表", exact: true })
    .click();
  await expect(drawer).toContainText("Synthetic worklist access revoked");
  await expect(rows).toHaveCount(0);
  await expect(
    drawer.getByRole("button", { name: "下一页", exact: true }),
  ).toBeDisabled();
  await page.unroute("**/api/v1/source-investigations?*");
  expect(errors).toEqual([]);
});

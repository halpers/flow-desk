import { expect, test } from "@playwright/test";
import fs from "node:fs/promises";

test("investigation history pages immutable notes, refreshes after edits and hides denied content", async ({
  page,
}) => {
  test.skip(
    !process.env.FLOWDESK_INVESTIGATION_HISTORY_FIXTURE,
    "Requires disposable investigation history fixture",
  );
  const fixture = JSON.parse(
    await fs.readFile(
      process.env.FLOWDESK_INVESTIGATION_HISTORY_FIXTURE!,
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
  const worklist = page.getByRole("dialog", {
    name: "巡检调查工作台",
    exact: true,
  });
  await worklist
    .locator(".investigation-worklist-item")
    .filter({ hasText: fixture.object_ref })
    .getByRole("button", { name: "继续跟进", exact: true })
    .click();
  const editor = page.getByRole("dialog", {
    name: "巡检调查记录",
    exact: true,
  });
  await expect(editor).toContainText(`修订号：${fixture.revision}`);
  await editor
    .getByRole("button", { name: "查看编辑历史", exact: true })
    .click();
  const history = page.getByRole("dialog", {
    name: "调查编辑历史",
    exact: true,
  });
  const rows = history.locator(".investigation-revision-item");
  const revisions: number[] = [];
  for (let index = 0; index < Math.ceil(fixture.revision / 20); index++) {
    await expect(rows).toHaveCount(Math.min(20, fixture.revision - index * 20));
    for (const text of await rows.allTextContents())
      revisions.push(Number(text.match(/修订 (\d+)/)![1]));
    if (index < Math.ceil(fixture.revision / 20) - 1)
      await history
        .getByRole("button", { name: "下一页", exact: true })
        .click();
  }
  expect(revisions).toEqual(
    Array.from(
      { length: fixture.revision },
      (_, index) => fixture.revision - index,
    ),
  );
  await expect(rows.last()).toContainText("<script>literal</script>");
  await expect(rows.locator("script")).toHaveCount(0);
  await expect(history).not.toContainText("knowledge/");
  await expect(
    history.getByRole("button", { name: "下一页", exact: true }),
  ).toBeDisabled();
  await history.getByRole("button", { name: "上一页", exact: true }).click();
  await expect(rows).toHaveCount(20);
  await history
    .getByRole("button", { name: "刷新编辑历史", exact: true })
    .click();
  await expect(history).toContainText("第 1 页");
  await expect(rows.first()).toContainText(`修订 ${fixture.revision}`);
  await history.getByRole("button", { name: "Close", exact: true }).click();
  await editor
    .getByLabel("调查说明", { exact: true })
    .fill("Synthetic history follow-up after review");
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
  await expect(editor).toContainText(`修订号：${fixture.revision + 1}`);
  await editor
    .getByRole("button", { name: "查看编辑历史", exact: true })
    .click();
  await expect(rows).toHaveCount(20);
  await expect(rows.first()).toContainText(`修订 ${fixture.revision + 1}`);
  await expect(rows.first()).toContainText(
    "Synthetic history follow-up after review",
  );
  await expect(rows.nth(1)).toContainText(
    `PRIVATE_HISTORY_REVISION_${fixture.revision}`,
  );
  await page.route(
    "**/api/v1/source-investigations/*/history?*",
    async (route) =>
      route.fulfill({
        status: 403,
        contentType: "application/json",
        body: JSON.stringify({
          code: "PERMISSION_DENIED",
          message: "Synthetic history access revoked",
        }),
      }),
  );
  await history
    .getByRole("button", { name: "刷新编辑历史", exact: true })
    .click();
  await expect(history).toContainText("Synthetic history access revoked");
  await expect(rows).toHaveCount(0);
  await expect(history).not.toContainText("PRIVATE_HISTORY");
  await expect(
    history.getByRole("button", { name: "下一页", exact: true }),
  ).toBeDisabled();
  await page.unroute("**/api/v1/source-investigations/*/history?*");
  await history.getByRole("button", { name: "Close", exact: true }).click();
  await editor
    .getByRole("button", { name: "查看编辑历史", exact: true })
    .click();
  await expect(rows.first()).toContainText(`修订 ${fixture.revision + 1}`);
  expect(errors).toEqual([]);
});

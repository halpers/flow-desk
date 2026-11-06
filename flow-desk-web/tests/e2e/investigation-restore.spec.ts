import { expect, test } from "@playwright/test";
import fs from "node:fs/promises";

test("history restore confirms exact content, retries one receipt, rejects concurrent edits and clears denied notes", async ({
  page,
}) => {
  test.skip(
    !process.env.FLOWDESK_INVESTIGATION_RESTORE_FIXTURE,
    "Requires disposable restore fixture",
  );
  const fixture = JSON.parse(
    await fs.readFile(
      process.env.FLOWDESK_INVESTIGATION_RESTORE_FIXTURE!,
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
    .getByLabel("调查说明", { exact: true })
    .fill("Unsaved draft must not become the restore source");
  const history = page.getByRole("dialog", {
    name: "调查编辑历史",
    exact: true,
  });
  const rows = history.locator(".investigation-revision-item");
  const modal = page.getByRole("dialog", {
    name: "确认恢复调查历史",
    exact: true,
  });
  async function openHistory() {
    await editor
      .getByRole("button", { name: "查看编辑历史", exact: true })
      .click();
    await expect(rows.first()).toBeVisible();
  }
  async function select(revision: number) {
    await rows
      .filter({ hasText: `修订 ${revision}` })
      .getByRole("button", { name: "恢复此修订", exact: true })
      .click();
  }
  await openHistory();
  await expect(
    rows.first().getByRole("button", { name: "恢复此修订", exact: true }),
  ).toBeDisabled();
  await select(1);
  await expect(modal).toContainText(fixture.source_note);
  await expect(modal.locator("script")).toHaveCount(0);
  await expect(
    modal.getByRole("button", { name: "确认恢复", exact: true }),
  ).toBeDisabled();
  await expect(modal).not.toContainText("Unsaved draft");
  await modal.getByRole("button", { name: /取\s*消/ }).click();
  await expect(modal).toBeHidden();
  await expect(rows).toHaveCount(4);
  const captures: { key: string; body: string; url: string }[] = [];
  let authorization = "";
  const pattern = "**/api/v1/source-investigations/*/history/*/restore";
  await page.route(pattern, async (route) => {
    const request = route.request();
    const headers = await request.allHeaders();
    authorization = headers.authorization;
    captures.push({
      key: headers["idempotency-key"],
      body: request.postData()!,
      url: request.url(),
    });
    if (captures.length === 1) {
      const response = await route.fetch();
      expect(response.status()).toBe(200);
      await route.abort("failed");
    } else await route.continue();
  });
  async function externalEdit(expected: number, note: string) {
    const response = await page.request.put(
      `/api/v1/source-investigations/${fixture.object_ref}`,
      {
        headers: {
          Authorization: authorization,
          "Idempotency-Key": `browser-${crypto.randomUUID()}`,
          "X-FlowDesk-Client": "web",
          Origin: new URL(page.url()).origin,
        },
        data: {
          expected_revision: expected,
          selection_token: "",
          confirm: true,
          note,
          status: "closed",
        },
      },
    );
    expect(response.status()).toBe(200);
  }
  await select(1);
  await modal.getByLabel("我确认将以上历史内容保存为新修订").check();
  await modal.getByRole("button", { name: "确认恢复", exact: true }).click();
  await expect(
    modal.getByRole("button", { name: "重试恢复历史", exact: true }),
  ).toBeEnabled();
  await expect.poll(() => captures.length).toBe(1);
  await externalEdit(5, "Concurrent edit after committed restore");
  await modal
    .getByRole("button", { name: "重试恢复历史", exact: true })
    .click();
  await expect(modal).toBeHidden();
  expect(captures).toHaveLength(2);
  expect(captures[0]).toEqual(captures[1]);
  expect(JSON.parse(captures[0].body)).toEqual({
    expected_revision: 4,
    confirm: true,
  });
  await expect(editor).toContainText("已将历史修订 1 保存为修订 5");
  await expect(editor).toContainText("修订号：6");
  await expect(editor.getByLabel("调查说明", { exact: true })).toHaveValue(
    "Concurrent edit after committed restore",
  );
  await openHistory();
  await expect(rows).toHaveCount(6);
  await expect(rows.filter({ hasText: "修订 5" })).toContainText(
    fixture.source_note,
  );
  await expect(rows.last()).toContainText(fixture.source_note);
  await select(2);
  await externalEdit(6, "Concurrent edit before confirmation");
  await modal.getByLabel("我确认将以上历史内容保存为新修订").check();
  await modal.getByRole("button", { name: "确认恢复", exact: true }).click();
  await expect(modal).toContainText("调查记录已变化");
  await modal
    .getByRole("button", { name: "放弃重试并重新读取", exact: true })
    .click();
  await expect(rows.first()).toContainText("修订 7");
  await select(2);
  await modal.getByLabel("我确认将以上历史内容保存为新修订").check();
  await modal.getByRole("button", { name: "确认恢复", exact: true }).click();
  await expect(modal).toBeHidden();
  await expect(editor).toContainText("修订号：8");
  await expect(editor.getByLabel("调查说明", { exact: true })).toHaveValue(
    "PRIVATE_RESTORE_SECOND",
  );
  await page.unroute(pattern);
  await page.route(pattern, async (route) =>
    route.fulfill({
      status: 403,
      contentType: "application/json",
      body: JSON.stringify({
        code: "PERMISSION_DENIED",
        message: "Synthetic restore access revoked",
      }),
    }),
  );
  await openHistory();
  await select(1);
  await modal.getByLabel("我确认将以上历史内容保存为新修订").check();
  await modal.getByRole("button", { name: "确认恢复", exact: true }).click();
  await expect(modal).toBeHidden();
  await expect(history).toBeHidden();
  await expect(editor).toContainText("Synthetic restore access revoked");
  await expect(editor).not.toContainText("PRIVATE_RESTORE");
  await expect(editor.getByLabel("调查说明", { exact: true })).toHaveCount(0);
  await expect(
    editor.getByRole("button", { name: "查看编辑历史", exact: true }),
  ).toHaveCount(0);
  expect(errors).toEqual([]);
});

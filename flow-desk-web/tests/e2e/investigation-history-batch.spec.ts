import { expect, test } from "@playwright/test";
import fs from "node:fs/promises";

test("history batch previews exact selection, clears paging choices, retries one receipt and hides denied content", async ({
  page,
}) => {
  test.skip(
    !process.env.FLOWDESK_INVESTIGATION_BATCH_FIXTURE,
    "Requires disposable batch fixture",
  );
  const fixture = JSON.parse(
    await fs.readFile(
      process.env.FLOWDESK_INVESTIGATION_BATCH_FIXTURE!,
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
    name: "确认批量归档调查历史",
    exact: true,
  });
  async function openHistory() {
    await editor
      .getByRole("button", { name: "查看编辑历史", exact: true })
      .click();
    await expect(rows.first()).toBeVisible();
  }

  const [first, second] = fixture.target_revisions;
  function row(revision: number) {
    return rows.filter({
      has: page.locator("strong", {
        hasText: new RegExp(`^修订 ${revision}$`),
      }),
    });
  }
  async function select(revisions: number[]) {
    for (const revision of revisions)
      await row(revision)
        .getByRole("checkbox", {
          name: `选择归档修订 ${revision}`,
          exact: true,
        })
        .check();
  }
  async function preview(revisions: number[]) {
    await select(revisions);
    await history
      .getByRole("button", {
        name: `批量归档选中修订（${revisions.length}）`,
        exact: true,
      })
      .click();
    await expect(
      modal.locator(".investigation-batch-preview-item"),
    ).toHaveCount(revisions.length);
  }
  await openHistory();
  await expect(rows.first().getByRole("checkbox")).toBeDisabled();
  await select([first, second]);
  await history.getByRole("button", { name: "下一页", exact: true }).click();
  await expect(
    history.getByRole("button", { name: "批量归档选中修订（0）", exact: true }),
  ).toBeDisabled();
  await history.getByRole("button", { name: "上一页", exact: true }).click();
  await expect(row(first).getByRole("checkbox")).not.toBeChecked();
  await preview([first, second]);
  for (const note of fixture.source_notes)
    await expect(modal).toContainText(note);
  await expect(modal.locator("script")).toHaveCount(0);
  await expect(modal).not.toContainText("Unsaved draft");
  await expect(
    modal.getByRole("button", { name: "确认批量归档", exact: true }),
  ).toBeDisabled();
  await modal.getByRole("button", { name: /取\s*消/ }).click();
  await expect(modal).toBeHidden();
  await expect(row(first).getByText("已归档", { exact: true })).toHaveCount(0);
  const captures: { key: string; body: string; url: string }[] = [];
  let authorization = "";
  const pattern = "**/api/v1/source-investigations/*/history/archive-batch";
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
  const confirmLabel = "我确认将以上选中修订整批归档并保留完整历史";
  await preview([second, first]);
  await modal.getByLabel(confirmLabel).check();
  await modal
    .getByRole("button", { name: "确认批量归档", exact: true })
    .click();
  await expect(
    modal.getByRole("button", { name: "重试批量归档", exact: true }),
  ).toBeEnabled();
  await expect.poll(() => captures.length).toBe(1);
  await externalEdit(fixture.revision, "Concurrent edit after committed batch");
  await modal
    .getByRole("button", { name: "重试批量归档", exact: true })
    .click();
  await expect(modal).toBeHidden();
  expect(captures).toHaveLength(2);
  expect(captures[0]).toEqual(captures[1]);
  expect(JSON.parse(captures[0].body)).toEqual({
    expected_revision: fixture.revision,
    revisions: [first, second],
    confirm: true,
  });
  await expect(editor).toContainText("已批量归档 2 个历史修订");
  await expect(editor).toContainText(`修订号：${fixture.revision + 1}`);
  await expect(editor.getByLabel("调查说明", { exact: true })).toHaveValue(
    "Concurrent edit after committed batch",
  );
  await openHistory();
  for (const revision of [first, second]) {
    await expect(
      row(revision).getByText("已归档", { exact: true }),
    ).toBeVisible();
    await expect(row(revision).getByRole("checkbox")).toBeDisabled();
  }
  await row(first)
    .getByRole("button", { name: "恢复此修订", exact: true })
    .click();
  const restore = page.getByRole("dialog", {
    name: "确认恢复调查历史",
    exact: true,
  });
  await expect(restore).toContainText(fixture.source_notes[0]);
  await restore.getByLabel("我确认将以上历史内容保存为新修订").check();
  await restore.getByRole("button", { name: "确认恢复", exact: true }).click();
  await expect(restore).toBeHidden();
  await expect(editor).toContainText(`修订号：${fixture.revision + 2}`);
  await openHistory();
  await preview([second + 1, second + 2]);
  await externalEdit(
    fixture.revision + 2,
    "Concurrent edit before batch confirmation",
  );
  await modal.getByLabel(confirmLabel).check();
  await modal
    .getByRole("button", { name: "确认批量归档", exact: true })
    .click();
  await expect(modal).toContainText("调查记录已变化");
  await modal
    .getByRole("button", { name: "放弃重试并重新读取", exact: true })
    .click();
  await expect(rows.first()).toContainText(`修订 ${fixture.revision + 3}`);
  await expect(
    row(second + 1).getByText("已归档", { exact: true }),
  ).toHaveCount(0);
  const previewPattern =
    "**/api/v1/source-investigations/*/history/archive-preview";
  await page.route(previewPattern, (route) =>
    route.fulfill({
      status: 403,
      contentType: "application/json",
      body: JSON.stringify({
        code: "PERMISSION_DENIED",
        message: "Synthetic batch preview access revoked",
      }),
    }),
  );
  await select([second + 1]);
  await history
    .getByRole("button", { name: "批量归档选中修订（1）", exact: true })
    .click();
  await expect(history).toBeHidden();
  await expect(modal).toBeHidden();
  await expect(editor).toContainText("Synthetic batch preview access revoked");
  await expect(editor.getByLabel("调查说明", { exact: true })).toHaveCount(0);
  await expect(editor).not.toContainText("PRIVATE_ARCHIVE");
  await page.unroute(previewPattern);
  await editor
    .getByRole("button", { name: "重新读取调查记录", exact: true })
    .click();
  await expect(editor).toContainText(`修订号：${fixture.revision + 3}`);
  await page.unroute(pattern);
  await page.route(pattern, (route) =>
    route.fulfill({
      status: 403,
      contentType: "application/json",
      body: JSON.stringify({
        code: "PERMISSION_DENIED",
        message: "Synthetic batch execution access revoked",
      }),
    }),
  );
  await openHistory();
  await preview([second + 1, second + 2]);
  await modal.getByLabel(confirmLabel).check();
  await modal
    .getByRole("button", { name: "确认批量归档", exact: true })
    .click();
  await expect(modal).toBeHidden();
  await expect(history).toBeHidden();
  await expect(editor).toContainText(
    "Synthetic batch execution access revoked",
  );
  await expect(editor.getByLabel("调查说明", { exact: true })).toHaveCount(0);
  await expect(editor).not.toContainText("PRIVATE_ARCHIVE");
  expect(errors).toEqual([]);
});

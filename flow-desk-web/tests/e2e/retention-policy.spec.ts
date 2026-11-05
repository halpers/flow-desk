import { expect, test } from "@playwright/test";
import fs from "node:fs/promises";

test("policy confirmation retries lost responses and invalidates old cleanup previews", async ({
  page,
}) => {
  test.skip(
    !process.env.FLOWDESK_RETENTION_POLICY_FIXTURE,
    "Requires disposable aged policy fixture",
  );
  const fixture = JSON.parse(
    await fs.readFile(process.env.FLOWDESK_RETENTION_POLICY_FIXTURE!, "utf8"),
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
  const card = page
    .locator(".ant-card")
    .filter({
      has: page.locator(".ant-card-head-title", { hasText: /^保留策略$/ }),
    });
  await expect(card).toContainText(`策略修订 ${fixture.policy_revision}`);
  await card.getByRole("button", { name: "编辑保留策略" }).click();
  const editor = page.getByRole("dialog", {
    name: "修改知识库保留策略",
    exact: true,
  });
  await editor.getByLabel("最近保留版本数", { exact: true }).fill("1");
  await editor.getByLabel("最短保留天数", { exact: true }).fill("1");
  await expect(
    editor.getByRole("button", { name: "确认保存策略" }),
  ).toBeDisabled();
  await editor.getByText("我已核对本知识库的保留要求", { exact: true }).click();
  let lost = false;
  const policyPath = `**/knowledge-bases/${fixture.knowledge_base_id}/retention-policy`;
  await page.route(policyPath, async (route) => {
    if (lost || route.request().method() !== "PUT") return route.continue();
    const response = await route.fetch();
    expect(response.status()).toBe(200);
    lost = true;
    await route.abort("failed");
  });
  await editor.getByRole("button", { name: "确认保存策略" }).click();
  await expect(editor.getByRole("alert")).toBeVisible();
  await editor.getByRole("button", { name: "确认保存策略" }).click();
  await expect(editor).toHaveCount(0);
  await expect(card).toContainText(`策略修订 ${fixture.policy_revision + 1}`);
  await page.unroute(policyPath);
  const document = page
    .locator(".knowledge-document")
    .filter({ hasText: "Retention policy acceptance" });
  await document.getByRole("button", { name: "版本保留与清理" }).click();
  const drawer = page.getByRole("dialog", {
    name: "版本保留与清理",
    exact: true,
  });
  const candidate = drawer
    .locator(".retention-item")
    .filter({ hasText: "policy-1.txt" });
  await expect(
    candidate.getByRole("button", { name: "清理此版本" }),
  ).toBeEnabled();
  await candidate.getByRole("button", { name: "清理此版本" }).click();
  const confirm = page.getByRole("dialog", {
    name: "确认清理知识版本",
    exact: true,
  });
  await confirm.getByText("我已确认备份与清理范围", { exact: true }).click();
  let changed = false;
  await page.route(
    `**/versions/${fixture.version_id}/cleanup`,
    async (route) => {
      const request = route.request();
      const origin = new URL(request.url()).origin;
      const response = await page.request.put(
        `${origin}/api/v1/knowledge-bases/${fixture.knowledge_base_id}/retention-policy`,
        {
          headers: {
            Authorization: request.headers()["authorization"],
            Origin: origin,
            "X-FlowDesk-Client": "web",
            "Idempotency-Key": "policy-concurrent-" + Date.now(),
          },
          data: {
            keep_latest: 4,
            minimum_age_days: 90,
            expected_revision: fixture.policy_revision + 1,
          },
        },
      );
      expect(response.status()).toBe(200);
      changed = true;
      await route.continue();
    },
  );
  await confirm.getByRole("button", { name: "确认不可撤销清理" }).click();
  await expect(confirm.getByRole("alert")).toContainText("保留策略已变化");
  await confirm.getByRole("button", { name: "Cancel", exact: true }).click();
  await drawer.getByRole("button", { name: "刷新清理预览" }).click();
  await expect(drawer).toContainText(`策略修订 ${fixture.policy_revision + 2}`);
  await expect(
    candidate.getByRole("button", { name: "清理此版本" }),
  ).toBeDisabled();
  await expect(candidate).toContainText("未满最短保留期");
  expect(lost && changed).toBe(true);
  expect(errors).toEqual([]);
});

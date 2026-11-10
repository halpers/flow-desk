import { readFileSync } from "node:fs";
import { expect, test } from "@playwright/test";

const fixturePath = process.env.FLOWDESK_INVESTIGATION_AUTOMATION_FIXTURE;
const fixture = fixturePath
  ? (JSON.parse(readFileSync(fixturePath, "utf8")) as { account: string })
  : null;
test.skip(
  !fixture,
  "Requires an isolated schedule tenant from check-investigation-automation.py",
);
for (const loseReply of [false, true]) {
  test(`investigation automation confirmation survives ${loseReply ? "lost" : "successful"} save reply and reload`, async ({
    page,
  }) => {
    const errors: string[] = [];
    page.on("pageerror", (error) => errors.push(error.message));
    await page.goto("/");
    await page.getByLabel("工作账号").fill(fixture!.account);
    await page
      .getByLabel("密码", { exact: true })
      .fill(process.env.FLOWDESK_TEST_PASSWORD!);
    await page.getByRole("button", { name: "登录工作台" }).click();
    await page.getByRole("button", { name: "知识库", exact: true }).click();
    await page.getByRole("button", { name: "调查工作台", exact: true }).click();
    await page
      .getByRole("button", { name: "调查自动流转", exact: true })
      .click();
    let panel = page.getByRole("dialog", {
      name: "超时调查自动回待办",
      exact: true,
    });
    await expect(
      panel.getByRole("switch", { name: "启用调查自动流转" }),
    ).toBeVisible();
    await expect(
      panel.getByRole("button", { name: "确认保存流转策略" }),
    ).toBeDisabled();
    await panel.getByRole("switch", { name: "启用调查自动流转" }).check();
    await panel.getByLabel("调查自动流转检查间隔").fill("5");
    await panel.getByRole("checkbox").check();
    const requests: { key: string; body: string }[] = [];
    if (loseReply) {
      await page.route("**/api/v1/investigation-automation", async (route) => {
        if (route.request().method() !== "PUT") return route.continue();
        requests.push({
          key: route.request().headers()["idempotency-key"],
          body: route.request().postData()!,
        });
        if (requests.length === 1) {
          await route.fetch();
          await route.abort("failed");
        } else await route.continue();
      });
    }
    await panel.getByRole("button", { name: "确认保存流转策略" }).click();
    if (loseReply) {
      await expect(
        panel.getByRole("button", { name: "重试原保存" }),
      ).toBeVisible();
      await expect(panel.getByRole("switch")).toBeDisabled();
      await panel.getByRole("button", { name: "重试原保存" }).click();
      await expect.poll(() => requests.length).toBe(2);
      expect(requests[1]).toEqual(requests[0]);
    }
    await expect(panel).toContainText("保存已确认");
    await expect(panel.getByRole("switch")).toBeChecked();
    await page.reload();
    await page.getByRole("button", { name: "知识库", exact: true }).click();
    await page.getByRole("button", { name: "调查工作台", exact: true }).click();
    await page
      .getByRole("button", { name: "调查自动流转", exact: true })
      .click();
    panel = page.getByRole("dialog", {
      name: "超时调查自动回待办",
      exact: true,
    });
    await expect(panel.getByRole("switch")).toBeChecked();
    await panel.getByRole("switch").uncheck();
    await panel.getByRole("checkbox").check();
    await panel.getByRole("button", { name: "确认保存流转策略" }).click();
    await expect(panel).toContainText("保存已确认");
    await expect(panel.getByRole("switch")).not.toBeChecked();
    if (loseReply) {
      await page.unroute("**/api/v1/investigation-automation");
      await page.route("**/api/v1/investigation-automation", (route) =>
        route.fulfill({
          status: 403,
          contentType: "application/json",
          body: JSON.stringify({
            code: "PERMISSION_DENIED",
            message: "Synthetic denied",
          }),
        }),
      );
      await panel.getByRole("button", { name: "刷新流转策略" }).click();
      await expect(panel).not.toBeVisible();
      await expect(
        page.getByText("调查自动流转权限已变化，请刷新调查列表。", {
          exact: true,
        }),
      ).toBeVisible();
    }
    expect(errors).toEqual([]);
  });
}

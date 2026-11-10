import { readFileSync } from "node:fs";
import { expect, test } from "@playwright/test";

const fixturePath = process.env.FLOWDESK_RUN_HISTORY_SCHEDULE_FIXTURE;
const fixture = fixturePath
  ? (JSON.parse(readFileSync(fixturePath, "utf8")) as { account: string })
  : null;
test.skip(
  !fixture,
  "Requires an isolated schedule tenant from check-run-history-schedule.py",
);
for (const loseReply of [false, true]) {
  test(`scheduled archival confirmation survives ${loseReply ? "lost" : "successful"} save reply and reload`, async ({
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
    await page.getByRole("button", { name: "任务记录", exact: true }).click();
    await page.getByRole("button", { name: "定时归档", exact: true }).click();
    let panel = page.getByRole("dialog", {
      name: "定时任务历史归档",
      exact: true,
    });
    await expect(
      panel.getByRole("switch", { name: "启用定时归档" }),
    ).toBeVisible();
    await expect(
      panel.getByRole("button", { name: "确认保存归档策略" }),
    ).toBeDisabled();
    await panel.getByRole("switch", { name: "启用定时归档" }).check();
    await panel.getByLabel("定时归档检查间隔").fill("5");
    await panel.getByRole("checkbox").check();
    const requests: { key: string; body: string }[] = [];
    if (loseReply) {
      await page.route("**/api/v1/run-history-schedule", async (route) => {
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
    await panel.getByRole("button", { name: "确认保存归档策略" }).click();
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
    await page.getByRole("button", { name: "任务记录", exact: true }).click();
    await page.getByRole("button", { name: "定时归档", exact: true }).click();
    panel = page.getByRole("dialog", { name: "定时任务历史归档", exact: true });
    await expect(panel.getByRole("switch")).toBeChecked();
    await panel.getByRole("switch").uncheck();
    await panel.getByRole("checkbox").check();
    await panel.getByRole("button", { name: "确认保存归档策略" }).click();
    await expect(panel).toContainText("保存已确认");
    await expect(panel.getByRole("switch")).not.toBeChecked();
    if (loseReply) {
      await page.unroute("**/api/v1/run-history-schedule");
      await page.route("**/api/v1/run-history-schedule", (route) =>
        route.fulfill({
          status: 403,
          contentType: "application/json",
          body: JSON.stringify({
            code: "PERMISSION_DENIED",
            message: "Synthetic denied",
          }),
        }),
      );
      await panel.getByRole("button", { name: "刷新归档策略" }).click();
      await expect(panel).not.toBeVisible();
      await expect(
        page.getByText("定时归档权限已变化，请刷新任务记录。", { exact: true }),
      ).toBeVisible();
    }
    expect(errors).toEqual([]);
  });
}

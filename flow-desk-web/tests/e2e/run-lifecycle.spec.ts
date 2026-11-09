import { expect, test } from "@playwright/test";

test("lifecycle reads current counts without writes, hides stale results and clears denied task details", async ({
  page,
}) => {
  const errors: string[] = [];
  page.on("pageerror", (error) => errors.push(error.name));
  await page.goto("/");
  await page.getByLabel("工作账号").fill("demo.operator");
  await page
    .getByLabel("密码", { exact: true })
    .fill(process.env.FLOWDESK_TEST_PASSWORD!);
  await page.getByRole("button", { name: "登录工作台" }).click();

  async function createFailedTask(order: string) {
    await page.getByRole("button", { name: "新建会话", exact: true }).click();
    await page.getByLabel("服务问题").fill(`查询 ${order} 的订单和物流`);
    const response = page.waitForResponse(
      (value) =>
        value.url().endsWith("/messages") &&
        value.request().method() === "POST",
    );
    await page.getByRole("button", { name: "发送任务", exact: true }).click();
    const run = (await (await response).json()).data;
    await expect(
      page.locator(".conversation-panel .panel-heading"),
    ).toContainText("处理失败", { timeout: 30000 });
    return run.id as string;
  }

  const first = await createFailedTask("FD209901010081");
  const second = await createFailedTask("FD209901010082");
  let mode: "real" | "delay" | "unavailable" | "changed" | "denied" = "real";
  let release: (() => void) | undefined;
  let reads = 0;
  const writes: string[] = [];
  const observedIds: string[] = [];
  page.on("request", (request) => {
    if (
      request.url().includes("/api/v1/") &&
      !request.url().includes("/auth/") &&
      request.method() !== "GET"
    )
      writes.push(request.method());
  });
  await page.route("**/api/v1/runs/*/lifecycle", async (route) => {
    expect(route.request().method()).toBe("GET");
    reads++;
    const behavior = mode;
    if (behavior === "delay")
      await new Promise<void>((resolve) => {
        release = resolve;
      });
    const status =
      behavior === "unavailable"
        ? 503
        : behavior === "changed"
          ? 409
          : behavior === "denied"
            ? 403
            : 200;
    if (status !== 200) {
      await route.fulfill({
        status,
        json: {
          code:
            behavior === "unavailable"
              ? "LIFECYCLE_UNAVAILABLE"
              : behavior === "changed"
                ? "LIFECYCLE_CHANGED"
                : "FORBIDDEN",
          message: "synthetic test response",
        },
      });
      return;
    }
    const response = await route.fetch();
    expect(response.status()).toBe(200);
    const body = await response.json();
    expect(body.data.observation_only).toBe(true);
    expect(body.data.minimum_age_days).toBe(30);
    expect(body.data.business.message_count).toBe(2);
    expect(body.data.agent.available).toBe(true);
    expect(body.data.agent.command_count).toBeGreaterThan(0);
    expect(body.data.agent.checkpoint_count).toBeGreaterThan(0);
    expect(body.data.protection_reasons).toContain("MINIMUM_AGE_NOT_REACHED");
    expect(JSON.stringify(body)).not.toContain("FD20990101008");
    expect(JSON.stringify(body)).not.toContain('"content"');
    observedIds.push(body.data.run.id);
    await route.fulfill({ response });
  });

  const operations = page.getByRole("dialog", {
    name: "任务记录",
    exact: true,
  });
  const details = page.getByRole("dialog", { name: "任务详情", exact: true });
  const preview = page.getByRole("dialog", {
    name: "任务生命周期预览",
    exact: true,
  });
  const counts = preview.locator(".run-lifecycle-data");
  await page.getByRole("button", { name: "任务记录", exact: true }).click();
  async function selectTask(id: string) {
    await operations.getByLabel("按任务 ID 定位").fill(id);
    await operations
      .getByRole("button", { name: "定位任务", exact: true })
      .click();
    await operations
      .getByRole("button", { name: `查看任务 ${id}`, exact: true })
      .click();
    await expect(details).toContainText("节点执行记录", { timeout: 15000 });
  }
  await selectTask(first);
  expect(reads).toBe(0);
  await details
    .getByRole("button", { name: "生命周期预览", exact: true })
    .click();
  await expect(counts).toBeVisible();
  await expect(preview).toContainText("仅盘点，不会归档或删除数据");
  await expect(preview).toContainText("候选仅供规划，执行前需重新检查");
  await expect(preview).toContainText("未满最低保留天数");
  await expect(preview).toContainText("业务数据盘点");
  await expect(preview).toContainText("执行数据盘点");
  await expect(preview).not.toContainText("FD209901010081");
  await expect(
    preview.getByRole("button", { name: /删除|确认归档/ }),
  ).toHaveCount(0);

  mode = "delay";
  const refreshRequest = page.waitForRequest((request) =>
    request.url().endsWith(`/runs/${first}/lifecycle`),
  );
  await preview
    .getByRole("button", { name: "刷新生命周期预览", exact: true })
    .click();
  await refreshRequest;
  await expect(counts).toBeHidden();
  await expect.poll(() => !!release).toBe(true);
  release!();
  release = undefined;
  mode = "real";
  await expect(counts).toBeVisible();
  expect(reads).toBe(2);

  mode = "unavailable";
  await preview
    .getByRole("button", { name: "刷新生命周期预览", exact: true })
    .click();
  await expect(preview).toContainText("未确认的条件继续受到保护");
  await expect(counts).toHaveCount(0);
  mode = "changed";
  await preview
    .getByRole("button", { name: "刷新生命周期预览", exact: true })
    .click();
  await expect(preview).toContainText("任务在盘点期间发生变化");
  await expect(counts).toHaveCount(0);
  mode = "real";
  await preview
    .getByRole("button", { name: "刷新生命周期预览", exact: true })
    .click();
  await expect(counts).toBeVisible();
  await preview.getByRole("button", { name: "Close", exact: true }).click();
  await expect(preview).toBeHidden();
  await details.getByRole("button", { name: "Close", exact: true }).click();
  await selectTask(second);
  await details
    .getByRole("button", { name: "生命周期预览", exact: true })
    .click();
  await expect(counts).toBeVisible();
  await expect(preview).toContainText(second);
  await expect(preview).not.toContainText(first);
  expect(observedIds.at(-1)).toBe(second);

  mode = "denied";
  await preview
    .getByRole("button", { name: "刷新生命周期预览", exact: true })
    .click();
  await expect(preview).toBeHidden();
  await expect(details).toBeHidden();
  await expect(operations).toContainText("任务访问权限已变化");
  await expect(
    operations.getByRole("button", { name: /^查看任务 / }),
  ).toHaveCount(0);
  mode = "real";
  await operations
    .getByRole("button", { name: "刷新任务", exact: true })
    .click();
  await selectTask(first);
  const priorReads = reads;
  await details
    .getByRole("button", { name: "生命周期预览", exact: true })
    .click();
  await expect(counts).toBeVisible();
  expect(reads).toBe(priorReads + 1);
  expect(observedIds.at(-1)).toBe(first);
  expect(writes).toEqual([]);
  expect(errors).toEqual([]);
});

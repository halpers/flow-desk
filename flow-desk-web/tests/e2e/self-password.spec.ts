import { expect, test, type Browser, type Page } from "@playwright/test";

const original = "Synthetic-browser-old-123";
const replacement = "Synthetic-browser-new-456";

async function createOwner(browser: Browser, origin: string) {
  const administrator = await browser.newContext({ baseURL: origin });
  try {
    const headers = { Origin: origin, "X-FlowDesk-Client": "web" };
    const signed = await administrator.request.post("/api/v1/auth/login", {
      headers,
      data: {
        account: "demo.admin",
        password: process.env.FLOWDESK_TEST_PASSWORD!,
      },
    });
    expect(signed.status()).toBe(200);
    const authenticated = {
      ...headers,
      Authorization: `Bearer ${(await signed.json()).data.access_token}`,
    };
    const roles = await administrator.request.get("/api/v1/admin/roles", {
      headers: authenticated,
    });
    expect(roles.status()).toBe(200);
    const role = (await roles.json()).data.items.find(
      (row: { builtin_role: string }) => row.builtin_role === "operator",
    );
    const account = `password.e2e.${crypto.randomUUID().slice(0, 8)}`;
    const created = await administrator.request.post("/api/v1/admin/users", {
      headers: { ...authenticated, "Idempotency-Key": crypto.randomUUID() },
      data: {
        account,
        display_name: "Synthetic password browser",
        password: original,
        role_ids: [role.id],
      },
    });
    expect(created.status()).toBe(201);
    return account;
  } finally {
    await administrator.close();
  }
}

async function signIn(page: Page, account: string, password: string) {
  await page.goto("/");
  await page.getByLabel("工作账号").fill(account);
  await page.getByLabel("密码", { exact: true }).fill(password);
  await page.getByRole("button", { name: "登录工作台" }).click();
}

for (const loseReply of [false, true]) {
  test(`own password clears old sessions and resolves ${loseReply ? "lost" : "successful"} reply by new login`, async ({
    page,
    browser,
  }) => {
    const origin = new URL(test.info().project.use.baseURL!).origin;
    const account = await createOwner(browser, origin);
    const errors: string[] = [];
    page.on("pageerror", (error) => errors.push(error.name));
    const otherContext = await browser.newContext({ baseURL: origin });
    try {
      const other = await otherContext.newPage();
      await signIn(other, account, original);
      await expect(
        other.getByRole("button", { name: "修改密码", exact: true }),
      ).toBeVisible();
      await signIn(page, account, original);
      await page.getByRole("button", { name: "修改密码", exact: true }).click();
      const dialog = page.getByRole("dialog", {
        name: "修改自己的密码",
        exact: true,
      });
      const submit = dialog.getByRole("button", {
        name: "确认修改并重新登录",
        exact: true,
      });
      await expect(submit).toBeDisabled();
      await dialog
        .getByLabel("当前密码", { exact: true })
        .fill("Wrong-password-123");
      await dialog.getByLabel("新密码", { exact: true }).fill(replacement);
      await dialog
        .getByLabel("再次输入新密码", { exact: true })
        .fill(replacement);
      await dialog.getByRole("checkbox").check();
      await submit.click();
      await expect(dialog).toContainText("当前密码不正确");
      await expect(dialog.getByLabel("当前密码", { exact: true })).toHaveValue(
        "",
      );
      await expect(dialog.getByLabel("新密码", { exact: true })).toHaveValue(
        "",
      );
      expect(
        await page.evaluate(() =>
          sessionStorage.getItem("flowdesk.password_reauthentication_required"),
        ),
      ).toBeNull();
      let writes = 0;
      await page.route("**/api/v1/auth/password", async (route) => {
        writes++;
        expect(
          await page.evaluate(() =>
            sessionStorage.getItem(
              "flowdesk.password_reauthentication_required",
            ),
          ),
        ).toBe("1");
        expect(route.request().headers()["idempotency-key"]).toBeUndefined();
        const response = await route.fetch();
        expect(response.status()).toBe(200);
        if (loseReply)
          await route.fulfill({
            status: 503,
            json: { code: "SYNTHETIC_LOST_REPLY" },
          });
        else await route.fulfill({ response });
      });
      await dialog.getByLabel("当前密码", { exact: true }).fill(original);
      await dialog.getByLabel("新密码", { exact: true }).fill(replacement);
      await dialog
        .getByLabel("再次输入新密码", { exact: true })
        .fill(replacement);
      await dialog.getByRole("checkbox").check();
      await submit.click();
      await expect(page.getByLabel("工作账号")).toBeVisible();
      await expect(dialog).toBeHidden();
      await expect(
        page.getByRole("button", { name: "任务记录", exact: true }),
      ).toHaveCount(0);
      await expect(page.getByLabel("密码", { exact: true })).toHaveValue("");
      await expect(page.getByRole("alert")).toContainText(
        loseReply ? "修改结果尚未确认" : "密码已修改",
      );
      expect(writes).toBe(1);
      expect(
        await page.evaluate(() =>
          sessionStorage.getItem("flowdesk.password_reauthentication_required"),
        ),
      ).toBe("1");
      let automaticRefreshes = 0;
      await page.route("**/api/v1/auth/refresh", async (route) => {
        automaticRefreshes++;
        await route.continue();
      });
      await page.reload();
      await expect(page.getByLabel("工作账号")).toBeVisible();
      expect(automaticRefreshes).toBe(0);
      await other.reload();
      await expect(other.getByLabel("工作账号")).toBeVisible();
      await signIn(page, account, original);
      await expect(
        page.getByRole("alert").filter({ hasText: "账号、密码或会话无效" }),
      ).toBeVisible();
      await page.getByLabel("工作账号").fill(account);
      await page.getByLabel("密码", { exact: true }).fill(replacement);
      await page.getByRole("button", { name: "登录工作台" }).click();
      await expect(
        page.getByRole("button", { name: "修改密码", exact: true }),
      ).toBeVisible();
      const storage = await page.evaluate(() =>
        JSON.stringify({
          local: { ...localStorage },
          session: { ...sessionStorage },
        }),
      );
      expect(
        await page.evaluate(() =>
          sessionStorage.getItem("flowdesk.password_reauthentication_required"),
        ),
      ).toBeNull();
      expect(storage).not.toContain(original);
      expect(storage).not.toContain(replacement);
      expect(writes).toBe(1);
      expect(errors).toEqual([]);
    } finally {
      await otherContext.close();
    }
  });
}

test("uncommitted 503 keeps reload at explicit login even while its refresh cookie remains valid", async ({
  page,
  browser,
}) => {
  const origin = new URL(test.info().project.use.baseURL!).origin;
  const account = await createOwner(browser, origin);
  await signIn(page, account, original);
  await page.getByRole("button", { name: "修改密码", exact: true }).click();
  const dialog = page.getByRole("dialog", {
    name: "修改自己的密码",
    exact: true,
  });
  await dialog.getByLabel("当前密码", { exact: true }).fill(original);
  await dialog.getByLabel("新密码", { exact: true }).fill(replacement);
  await dialog.getByLabel("再次输入新密码", { exact: true }).fill(replacement);
  await dialog.getByRole("checkbox").check();
  let writes = 0;
  await page.route("**/api/v1/auth/password", async (route) => {
    writes++;
    expect(
      await page.evaluate(() =>
        sessionStorage.getItem("flowdesk.password_reauthentication_required"),
      ),
    ).toBe("1");
    // Intentionally do not contact the backend: the old password/cookie stay valid.
    await route.fulfill({
      status: 503,
      json: { code: "SYNTHETIC_NOT_SUBMITTED" },
    });
  });
  await dialog
    .getByRole("button", { name: "确认修改并重新登录", exact: true })
    .click();
  await expect(page.getByLabel("工作账号")).toBeVisible();
  await expect(page.getByRole("alert")).toContainText("修改结果尚未确认");
  const manualRefresh = await page.request.post("/api/v1/auth/refresh", {
    headers: { Origin: origin, "X-FlowDesk-Client": "web" },
  });
  expect(manualRefresh.status()).toBe(200);
  let automaticRefreshes = 0;
  await page.route("**/api/v1/auth/refresh", async (route) => {
    automaticRefreshes++;
    await route.continue();
  });
  await page.reload();
  await expect(page.getByLabel("工作账号")).toBeVisible();
  await expect(page.getByRole("alert")).toContainText(
    "修改密码结果需要重新核对",
  );
  expect(automaticRefreshes).toBe(0);
  expect(writes).toBe(1);
  await signIn(page, account, replacement);
  await expect(
    page.getByRole("alert").filter({ hasText: "账号、密码或会话无效" }),
  ).toBeVisible();
  expect(
    await page.evaluate(() =>
      sessionStorage.getItem("flowdesk.password_reauthentication_required"),
    ),
  ).toBe("1");
  await page.getByLabel("工作账号").fill(account);
  await page.getByLabel("密码", { exact: true }).fill(original);
  await page.getByRole("button", { name: "登录工作台" }).click();
  await expect(
    page.getByRole("button", { name: "修改密码", exact: true }),
  ).toBeVisible();
  expect(
    await page.evaluate(() =>
      sessionStorage.getItem("flowdesk.password_reauthentication_required"),
    ),
  ).toBeNull();
  expect(writes).toBe(1);
});

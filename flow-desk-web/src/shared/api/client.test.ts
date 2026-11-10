import { afterEach, beforeEach, expect, test, vi } from "vitest";

let stored: Map<string, string>;
const fetchMock = vi.fn<typeof fetch>();
const tokenReply = () =>
  new Response(
    JSON.stringify({
      data: { access_token: "synthetic-access", expires_in: 900 },
    }),
    { status: 200 },
  );

beforeEach(() => {
  vi.resetModules();
  stored = new Map();
  vi.stubGlobal("sessionStorage", {
    getItem: (key: string) => stored.get(key) ?? null,
    setItem: (key: string, value: string) => stored.set(key, value),
    removeItem: (key: string) => stored.delete(key),
  });
  vi.stubGlobal("fetch", fetchMock);
  fetchMock.mockReset();
});
afterEach(() => {
  vi.unstubAllGlobals();
});

test("uncertain password result survives reload and only explicit successful login clears the boolean fence", async () => {
  const first = await import("./client");
  first.beginPasswordReauthentication();
  expect([...stored.values()]).toEqual(["1"]);
  first.clearSession();
  await expect(first.refresh()).rejects.toMatchObject({
    code: "REAUTHENTICATION_REQUIRED",
  });
  expect(fetchMock).not.toHaveBeenCalled();
  vi.resetModules();
  const reloaded = await import("./client");
  expect(reloaded.passwordReauthenticationRequired()).toBe(true);
  await expect(reloaded.refresh()).rejects.toMatchObject({ status: 401 });
  expect(fetchMock).not.toHaveBeenCalled();
  fetchMock.mockResolvedValueOnce(
    new Response(JSON.stringify({ code: "UNAUTHENTICATED" }), { status: 401 }),
  );
  await expect(
    reloaded.login("synthetic", "Synthetic-wrong-123"),
  ).rejects.toMatchObject({ status: 401 });
  expect(reloaded.passwordReauthenticationRequired()).toBe(true);
  fetchMock.mockResolvedValueOnce(tokenReply());
  await reloaded.login("synthetic", "Synthetic-new-456");
  expect(reloaded.passwordReauthenticationRequired()).toBe(false);
  expect(stored.size).toBe(0);
  fetchMock.mockResolvedValueOnce(tokenReply());
  await reloaded.refresh();
  expect(fetchMock).toHaveBeenCalledTimes(3);
});

test("refresh already in flight cannot restore an access token after the password fence is set", async () => {
  const client = await import("./client");
  let resolveReply!: (response: Response) => void;
  fetchMock.mockImplementationOnce(
    () =>
      new Promise<Response>((resolve) => {
        resolveReply = resolve;
      }),
  );
  const refreshing = client.refresh();
  client.beginPasswordReauthentication();
  resolveReply(tokenReply());
  await expect(refreshing).rejects.toMatchObject({
    code: "REAUTHENTICATION_REQUIRED",
  });
  client.clearPasswordReauthentication();
  fetchMock.mockResolvedValueOnce(new Response("{}", { status: 200 }));
  await client.authorizedFetch("/auth/me", {}, false);
  expect(
    new Headers(fetchMock.mock.calls[1][1]?.headers).has("Authorization"),
  ).toBe(false);
});

test("failure to persist the boolean fence rejects password preparation before any network call", async () => {
  vi.stubGlobal("sessionStorage", {
    getItem: () => null,
    setItem: () => {
      throw new Error("storage unavailable");
    },
  });
  const client = await import("./client");
  expect(() => client.beginPasswordReauthentication()).toThrowError(
    "浏览器无法保存重新登录标记",
  );
  expect(fetchMock).not.toHaveBeenCalled();
});

test("old automatic refresh cannot expire or overwrite an explicit login after password sign-out", async () => {
  const client = await import("./client");
  const expired = vi.fn();
  vi.stubGlobal("window", { dispatchEvent: expired });
  let resolveRefresh!: (response: Response) => void;
  let markArrived!: () => void;
  const arrived = new Promise<void>((resolve) => {
    markArrived = resolve;
  });
  let meAuthorization: string | null = null;
  fetchMock.mockImplementation(async (url, init) => {
    if (url === "/api/v1/protected") return new Response("{}", { status: 401 });
    if (url === "/api/v1/auth/refresh")
      return new Promise<Response>((resolve) => {
        resolveRefresh = resolve;
        markArrived();
      });
    if (url === "/api/v1/auth/login")
      return new Response(
        JSON.stringify({
          data: { access_token: "synthetic-new-explicit-login" },
        }),
        { status: 200 },
      );
    meAuthorization = new Headers(init?.headers).get("Authorization");
    return new Response("{}", { status: 200 });
  });
  const oldRequest = client
    .authorizedFetch("/protected")
    .catch((error: unknown) => error);
  await arrived;
  client.beginPasswordReauthentication();
  client.clearSession();
  await client.login("synthetic", "Synthetic-new-456");
  resolveRefresh(tokenReply());
  await expect(oldRequest).resolves.toMatchObject({ code: "SESSION_CHANGED" });
  await client.authorizedFetch("/auth/me", {}, false);
  expect(meAuthorization).toBe("Bearer synthetic-new-explicit-login");
  expect(expired).not.toHaveBeenCalled();
  expect(client.passwordReauthenticationRequired()).toBe(false);
});

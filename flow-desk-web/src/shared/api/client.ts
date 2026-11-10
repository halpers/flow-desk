import type { paths } from "./generated";
export type User = NonNullable<
  paths["/api/v1/auth/me"]["get"]["responses"][200]["content"]["application/json"]["data"]
>;
let accessToken = "";
let refreshing: Promise<void> | null = null;
let sessionGeneration = 0;

export class ApiError extends Error {
  constructor(
    public status: number,
    public code: string,
    message: string,
    public data?: Record<string, unknown>,
  ) {
    super(message);
  }
}
const passwordReauthenticationKey =
  "flowdesk.password_reauthentication_required";
let passwordReauthentication = false;

export function passwordReauthenticationRequired(): boolean {
  if (passwordReauthentication) return true;
  try {
    return sessionStorage.getItem(passwordReauthenticationKey) === "1";
  } catch {
    return false;
  }
}

// Persist only a boolean, before sending any password. If browser storage is
// unavailable the request is rejected locally, so reload cannot bypass the fence.
export function beginPasswordReauthentication() {
  try {
    sessionStorage.setItem(passwordReauthenticationKey, "1");
    if (sessionStorage.getItem(passwordReauthenticationKey) !== "1")
      throw new Error();
  } catch {
    throw new ApiError(
      409,
      "REAUTHENTICATION_STORAGE_UNAVAILABLE",
      "浏览器无法保存重新登录标记，请允许当前站点的会话存储后再试",
    );
  }
  passwordReauthentication = true;
}

export function clearPasswordReauthentication() {
  passwordReauthentication = false;
  try {
    sessionStorage.removeItem(passwordReauthenticationKey);
  } catch {
    /* Keep a persisted fence if storage became unavailable. */
  }
}

const reauthenticationError = () =>
  new ApiError(401, "REAUTHENTICATION_REQUIRED", "请重新输入账号和密码登录");

export const clearSession = () => {
  accessToken = "";
  sessionGeneration++;
};
export async function authorizedFetch(
  path: string,
  init: RequestInit = {},
  retry = true,
): Promise<Response> {
  const generation = sessionGeneration;
  const headers = new Headers(init.headers);
  if (init.body) headers.set("Content-Type", "application/json");
  headers.set("X-FlowDesk-Client", "web");
  if (accessToken) headers.set("Authorization", `Bearer ${accessToken}`);
  const response = await fetch(`/api/v1${path}`, {
    ...init,
    headers,
    credentials: "same-origin",
  });
  if (response.status === 401 && retry && !path.startsWith("/auth/")) {
    await response.body?.cancel();
    if (generation !== sessionGeneration)
      throw new ApiError(401, "SESSION_CHANGED", "会话已变化，请重新操作");
    try {
      await refresh();
    } catch (error) {
      if (generation === sessionGeneration)
        window.dispatchEvent(new Event("flowdesk:session-expired"));
      throw error;
    }
    if (generation !== sessionGeneration)
      throw new ApiError(401, "SESSION_CHANGED", "会话已变化，请重新操作");
    return authorizedFetch(path, init, false);
  }
  return response;
}
export async function api<T>(
  path: string,
  init: RequestInit = {},
  retry = true,
): Promise<T> {
  const response = await authorizedFetch(path, init, retry);
  const body = await response.json();
  if (!response.ok)
    throw new ApiError(
      response.status,
      body.code || "REQUEST_FAILED",
      body.message || "请求失败",
      body.data,
    );
  return body.data as T;
}
export async function login(account: string, password: string) {
  const generation = ++sessionGeneration;
  const data = await api<
    NonNullable<
      paths["/api/v1/auth/login"]["post"]["responses"][200]["content"]["application/json"]["data"]
    >
  >("/auth/login", {
    method: "POST",
    body: JSON.stringify({ account, password }),
  });
  if (generation === sessionGeneration) {
    clearPasswordReauthentication();
    accessToken = data.access_token;
  }
}
export function refresh() {
  if (passwordReauthenticationRequired())
    return Promise.reject(reauthenticationError());
  const generation = sessionGeneration;
  refreshing ??= api<
    NonNullable<
      paths["/api/v1/auth/refresh"]["post"]["responses"][200]["content"]["application/json"]["data"]
    >
  >("/auth/refresh", { method: "POST" }, false)
    .then((data) => {
      if (passwordReauthenticationRequired()) throw reauthenticationError();
      if (generation === sessionGeneration) accessToken = data.access_token;
    })
    .finally(() => {
      refreshing = null;
    });
  return refreshing;
}
export async function logout() {
  try {
    await api("/auth/logout", { method: "POST" });
  } finally {
    clearSession();
  }
}

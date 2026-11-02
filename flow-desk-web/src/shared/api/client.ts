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
export const clearSession = () => {
  accessToken = "";
  sessionGeneration++;
};
export async function authorizedFetch(
  path: string,
  init: RequestInit = {},
  retry = true,
): Promise<Response> {
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
    try {
      await refresh();
    } catch (error) {
      window.dispatchEvent(new Event("flowdesk:session-expired"));
      throw error;
    }
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
  if (generation === sessionGeneration) accessToken = data.access_token;
}
export function refresh() {
  const generation = sessionGeneration;
  refreshing ??= api<
    NonNullable<
      paths["/api/v1/auth/refresh"]["post"]["responses"][200]["content"]["application/json"]["data"]
    >
  >("/auth/refresh", { method: "POST" }, false)
    .then((data) => {
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

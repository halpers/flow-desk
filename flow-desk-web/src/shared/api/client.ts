import type {paths} from './generated';
export type User = paths['/api/v1/auth/me']['get']['responses'][200]['content']['application/json']['data'];
let accessToken = '';
let refreshing: Promise<void> | null = null;
export const clearSession = () => {accessToken = '';};
export async function api<T>(path: string, init: RequestInit = {}, retry = true): Promise<T> {
  const headers = new Headers(init.headers);
  headers.set('Content-Type', 'application/json');
  headers.set('X-FlowDesk-Client', 'web');
  if (accessToken) headers.set('Authorization', `Bearer ${accessToken}`);
  const response = await fetch(`/api/v1${path}`, {...init, headers, credentials: 'same-origin'});
  if (response.status === 401 && retry && !path.startsWith('/auth/')) {
    await refresh(); return api(path, init, false);
  }
  const body = await response.json();
  if (!response.ok) throw new Error(body.message || body.code || '请求失败');
  return body.data as T;
}
export async function login(account: string, password: string) {
  const data = await api<{access_token: string}>('/auth/login', {method: 'POST', body: JSON.stringify({account, password})});
  accessToken = data.access_token;
}
export function refresh() {
  refreshing ??= api<{access_token: string}>('/auth/refresh', {method:'POST'}, false).then(data => {accessToken=data.access_token;}).finally(()=>{refreshing=null;});
  return refreshing;
}
export async function logout() {try {await api('/auth/logout',{method:'POST'});} finally {clearSession();}}

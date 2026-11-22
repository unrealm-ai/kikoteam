import { ref } from 'vue';

export class D2ApiError extends Error {
  readonly status: number;
  readonly code: string;

  constructor(status: number, code: string, message: string) {
    super(message);
    this.status = status;
    this.code = code;
  }
}

export const activeWorkspaceId = ref<string>('');
export const pendingApprovalsCount = ref<number>(0);

const AUTH_TOKEN_STORAGE_KEY = 'kiko:d2:auth-token';

export function getAuthToken(): string {
  try {
    return typeof window !== 'undefined' ? (window.localStorage.getItem(AUTH_TOKEN_STORAGE_KEY) || '') : '';
  } catch {
    return '';
  }
}

export function setAuthToken(token: string | null | undefined): void {
  try {
    if (typeof window === 'undefined') return;
    const trimmed = token?.trim();
    if (trimmed) {
      window.localStorage.setItem(AUTH_TOKEN_STORAGE_KEY, trimmed);
    } else {
      window.localStorage.removeItem(AUTH_TOKEN_STORAGE_KEY);
    }
  } catch {
    // ignore storage errors
  }
}

export function clearAuthToken(): void {
  setAuthToken(null);
}

export async function apiRequest<T>(path: string, init: RequestInit = {}): Promise<T> {
  const headers = new Headers(init.headers);
  if (init.body !== undefined && !headers.has('Content-Type')) {
    headers.set('Content-Type', 'application/json');
  }
  const token = getAuthToken();
  if (token && !headers.has('Authorization')) {
    headers.set('Authorization', `Bearer ${token}`);
  }
  const res = await fetch(`/api${path}`, {
    credentials: 'include',
    ...init,
    headers
  });

  const text = await res.text();
  let payload: unknown = null;
  let invalidJson = false;
  if (text.trim()) {
    try {
      payload = JSON.parse(text);
    } catch {
      invalidJson = true;
    }
  }

  if (!res.ok) {
    const errObj = !invalidJson && payload && typeof payload === 'object' && 'error' in payload
      ? (payload as { error?: { code?: string; message?: string } }).error
      : undefined;
    throw new D2ApiError(
      res.status,
      errObj?.code || `HTTP_${res.status}`,
      errObj?.message || res.statusText || '请求失败'
    );
  }

  if (res.status === 204 || res.status === 205) {
    return undefined as T;
  }

  if (!text.trim()) {
    throw new D2ApiError(res.status, 'EMPTY_JSON_RESPONSE', '服务端返回空响应，无法解析 JSON 数据');
  }

  if (invalidJson) {
    throw new D2ApiError(res.status, 'INVALID_JSON_RESPONSE', '服务端返回了非 JSON 格式响应，已拦截异常结果');
  }

  return payload as T;
}

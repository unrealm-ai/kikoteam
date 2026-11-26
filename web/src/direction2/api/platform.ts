import type { AuthSessionResponse, UserProfile, WorkspaceInfo, SendEmailCodeResponse } from '../types';
import { D2ApiError } from './client';
export function readInvitationToken(): string {
  const url = new URL(location.href);
  let token = url.searchParams.get('invitation_token') || '';
  if (!token && url.pathname.startsWith('/invite/')) {
    const raw = url.pathname.slice(8);
    try { token = decodeURIComponent(raw); } catch { token = raw; }
  }
  if (token) {
    url.searchParams.delete('invitation_token');
    if (url.pathname.startsWith('/invite/')) url.pathname = '/';
    history.replaceState(null, '', url);
  }
  return token;
}
export function validPlatformPassword(value: string): boolean {
  const bytes = new TextEncoder().encode(value).length;
  return bytes >= 10 && bytes <= 72;
}
export const realPlatform = import.meta.env?.VITE_PLATFORM_REAL === true;
export async function platformRequest<T>(path: string, init: RequestInit = {}): Promise<T> {
  const headers = new Headers(init.headers);
  if (init.body !== undefined) headers.set('Content-Type', 'application/json');
  if (init.method && !['GET', 'HEAD'].includes(init.method)) headers.set('X-CSRF-Protection', '1');
  const response = await fetch(`/api/v1${path}`, { ...init, signal: init.signal || AbortSignal.timeout(20000), credentials: 'include', headers });
  const payload = response.status === 204 ? null : await response.json().catch(() => null);
  if (!response.ok) {
    const requestId = payload?.error?.request_id || response.headers.get('X-Request-ID');
    throw new D2ApiError(response.status, payload?.error?.code || `HTTP_${response.status}`, `${payload?.error?.code === 'email_verification_required' ? '邮箱尚未验证。请通过邮箱验证码重置密码完成验证；若未开放邮件功能，请联系管理员。' : payload?.error?.message || '请求失败'}${requestId ? `（请求 ${requestId}）` : ''}`);
  }
  if (response.status !== 204 && payload === null) throw new D2ApiError(response.status, 'INVALID_JSON_RESPONSE', '服务端响应格式错误');
  return payload as T;
}
type SessionWire = { user: { id: string; email: string; name: string; email_verified?: boolean;is_platform_admin?:boolean }; workspaces?: WorkspaceWire[] };
export type WorkspaceWire = { id: string; name: string; description?: string; role: string; version: number };
export function workspaceFromWire(w: WorkspaceWire): WorkspaceInfo { return { ...w, desc: w.description || '' }; }
function userFromWire(user: SessionWire['user']): UserProfile { return { ...user, isPlatformAdmin:user.is_platform_admin===true, role: '', avatarText: user.name.slice(0, 2), authProvider: 'email', emailVerified: user.email_verified === true }; }
function sessionFromWire(s: SessionWire): AuthSessionResponse { return { user: userFromWire(s.user), workspaces: (s.workspaces || []).map(workspaceFromWire) }; }
const challenges = new Map<string, string>();
const key = (email: string, purpose: string) => `${purpose}:${email.trim().toLowerCase()}`;
export const post = (body: unknown): RequestInit => ({ method: 'POST', body: JSON.stringify(body) });
export type AuthConfig = { password_registration_enabled: boolean; email_code_enabled: boolean; registration_verification_required: boolean };
export const platformAuth = {
  getConfig: () => platformRequest<AuthConfig>('/auth/options'),
  getMe: async () => userFromWire((await platformRequest<SessionWire>('/me')).user),
  getWorkspaces: async () => {
    const all: WorkspaceWire[] = [];
    for (let page = 1; ; page++) {
      const result = await platformRequest<{items: WorkspaceWire[]; total: number}>(`/workspaces?page=${page}&page_size=100`);
      all.push(...result.items);
      if (result.items.length < 100 || all.length >= result.total) break;
    }
    return all.map(workspaceFromWire);
  },
  loginWithEmail: async (p: { email: string; password: string }) => sessionFromWire(await platformRequest<SessionWire>('/auth/sessions', post({ ...p, method: 'password' }))),
  loginWithEmailCode: async (p: { email: string; code: string }) => sessionFromWire(await platformRequest<SessionWire>('/auth/sessions', post({ ...p, method: 'code', challenge_id: challenges.get(key(p.email, 'login')) }))),
  sendEmailCode: async (p: { email: string; purpose: 'login' | 'register' | 'reset_password' }): Promise<SendEmailCodeResponse> => {
    const result = await platformRequest<{ challenge_id: string; expires_at: string; resend_after: number }>('/auth/email-codes', post(p));
    challenges.set(key(p.email, p.purpose), result.challenge_id);
    return { sent: true, ...p, expiresInSeconds: Math.max(0, Math.floor((Date.parse(result.expires_at) - Date.now()) / 1000)), resendAfterSeconds: result.resend_after };
  },
  registerWithEmail: async (p: { email: string; name: string; code: string; password: string; method?: 'password' | 'code'; referral_code?: string }) => sessionFromWire(await platformRequest<SessionWire>('/auth/registrations', post(p.method === 'password' ? { email: p.email, name: p.name, password: p.password, method: 'password', referral_code: p.referral_code } : { ...p, method: 'code', challenge_id: challenges.get(key(p.email, 'register')) }))),
  resetPassword: async (p: { email: string; code: string; newPassword: string }) => { await platformRequest('/auth/password-reset', post({ email: p.email, code: p.code, new_password: p.newPassword, challenge_id: challenges.get(key(p.email, 'reset_password')) })); return { ok: true, email: p.email }; },
  logout: async () => { await platformRequest('/auth/session', { method: 'DELETE' }); return { ok: true }; }
};

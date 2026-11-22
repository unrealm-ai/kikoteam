import { platformRequest } from './platform';
export interface FeishuPermission { scope: string; required: boolean; status: 'unknown' | 'granted' | 'missing' }
export interface FeishuDiagnostics {
  checked_at: string; credential_status: string; bot_status: string; directory_status: string; scope_status: string;
  permissions: FeishuPermission[]; ready: boolean; error_code: string; error_detail?: string;
}
export interface FeishuPermissionGroup {
  id:string; label:string; required:boolean; identity:string; scopes:string[];
  note?:string; status?:string; scopeStates?:FeishuPermission[]; missingScopes?:string[];
}
export function feishuPermissionReadiness(d?:FeishuDiagnostics):boolean|undefined {
  if(d && ([d.credential_status,d.bot_status,d.directory_status,d.scope_status].includes('failed') || d.permissions?.some(p=>p.required && p.status==='missing')))return false;
  if(d?.ready === true && d.scope_status !== 'unknown')return true;
  return undefined;
}
export function feishuAuthorizationUrl(appId: string, scope?: string) {
  if (!scope || !/^cli_[A-Za-z0-9_-]+$/.test(appId)) return undefined;
  return `https://open.feishu.cn/app/${appId}/auth?${new URLSearchParams({q:scope,op_from:'openapi'})}`;
}
export interface FeishuRegistration {
  connection_id?:string; permission_groups?:FeishuPermissionGroup[];
  next_check_at?: string; auto_retry_blocked?: boolean; auto_check?: {eligible:boolean;in_progress:boolean;next_check_at?:string};
  id: string; state: string; app_id: string; verification_url?: string; expires_at: string; next_poll_at: string;
  updated_at: string; credential_saved: boolean; error_code: string; diagnostics?: FeishuDiagnostics;
}
export interface FeishuSetupStatus { registration: FeishuRegistration | null; permissions: FeishuPermission[] }
export function feishuRegistrationFailure(r?: FeishuRegistration | null) {
  if (r?.state !== 'needs_attention') return undefined;
  const code = r.error_code || r.diagnostics?.error_code || '';
  const credential = code === 'feishu_authentication_failed' || code === 'feishu_auth_unavailable';
  const directory = code === 'feishu_directory_check_unavailable' || r.diagnostics?.directory_status === 'failed';
  const diagnosticDetail = r.diagnostics?.error_code === code ? r.diagnostics.error_detail : '';
  return {
    reason: credential ? '获取飞书应用的租户令牌失败' : directory ? '读取飞书通讯录授权范围失败' : '飞书接入检查未通过',
    detail: diagnosticDetail || code,
    automaticRetry: !r.auto_retry_blocked && r.auto_check?.eligible === true,
    canReauthorize: r.credential_saved && !r.connection_id
  };
}
const base = (w: string, a: string) => `/workspaces/${encodeURIComponent(w)}/agents/${encodeURIComponent(a)}/channels/feishu`;
const bounded = (signal: AbortSignal, timeout = 25000) => AbortSignal.any([signal, AbortSignal.timeout(timeout)]);
export const feishuSetupApi = {
  get: (w: string, a: string, signal: AbortSignal) => platformRequest<FeishuSetupStatus>(`${base(w,a)}/registration`, {signal:bounded(signal)}),
  create: (w: string, a: string, key: string, signal: AbortSignal, replaceRegistrationId?: string) => platformRequest<FeishuSetupStatus>(`${base(w,a)}/registration`, {method:'POST',body:JSON.stringify({replace_registration_id:replaceRegistrationId}),headers:{'Idempotency-Key':key},signal:bounded(signal)}),
  action: (w: string, a: string, id: string, action: 'poll' | 'retry' | 'cancel', signal: AbortSignal) => platformRequest<FeishuSetupStatus>(`${base(w,a)}/registration/${encodeURIComponent(id)}/${action}`, {method:'POST',body:'{}',signal:bounded(signal, action === 'retry' ? 70000 : 25000)}),
  diagnose: (w: string, a: string, signal: AbortSignal) => platformRequest<FeishuDiagnostics>(`${base(w,a)}/diagnostics`, {method:'POST',body:'{}',signal:bounded(signal,70000)})
};
export function feishuVerificationURL(raw?: string): string | null {
  try { const url = new URL(raw || ''); return url.protocol === 'https:' && ['accounts.feishu.cn','open.feishu.cn'].includes(url.hostname) && !url.username && !url.password && (!url.port || url.port === '443') ? url.href : null; } catch { return null; }
}

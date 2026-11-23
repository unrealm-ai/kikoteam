import { platformRequest } from './platform';
import type { FeishuDiagnostics, FeishuPermissionGroup } from './feishu-setup';
export interface FeishuDepartmentRule { department_id: string; include_descendants: boolean }
export interface FeishuRules {
  trigger: { mention: boolean; keywords: string[]; regexes: string[] };
  history: { enabled: boolean; limit: number };
  access: { group_members_enabled: boolean; mode: 'all' | 'allowlist' | 'denylist'; allow_ids: string[]; deny_ids: string[]; allow_department_ids?: string[]; deny_department_ids?: string[]; allow_department_rules?: FeishuDepartmentRule[]; deny_department_rules?: FeishuDepartmentRule[]; allow_chat_ids?: string[]; deny_chat_ids?: string[] };
}
export interface ChannelRebindImpact {
  allow_entries: number;
  deny_entries: number;
  package_names: string[];
  package_deny_names: string[];
  sharing_names: string[];
  notification_names: string[];
}
export interface FeishuBinding extends FeishuRules {
  id: string; agent_id: string; provider: string; app_id: string; external_tenant_id: string;
  identity_epoch?: number; identity_confirmed?: boolean; identity_verified_generation?: number; tenant_namespace?: string;
  enabled: boolean; has_secret: boolean; connection_generation: number; rules_version: number; config_revision: number;
  applied: { connection_generation: number; rules_version: number; config_revision: number; status: string; error_code?: string };
  permission_diagnostics?: FeishuDiagnostics;
  permission_groups?: FeishuPermissionGroup[];
  directory_status?: FeishuDirectoryStatus;
  dingtalk_proactive_send_permission_missing?: boolean;
  rebind_impact?: ChannelRebindImpact;
}
export interface FeishuConfiguration extends FeishuRules { version: number; app_id: string; app_secret?: string; enabled: boolean }
const path = (w: string, a: string) => `/workspaces/${encodeURIComponent(w)}/agents/${encodeURIComponent(a)}/channels/feishu`;
export const feishuApi = {
  get: (w: string, a: string, signal?: AbortSignal) => platformRequest<FeishuBinding>(path(w, a), signal ? { signal } : {}),
  save: (w: string, a: string, body: FeishuConfiguration, signal?: AbortSignal) => platformRequest<FeishuBinding>(path(w, a), { method: 'PUT', body: JSON.stringify(body), ...(signal ? { signal } : {}) })
};

export interface FeishuDirectoryStatus {
  status: 'never' | 'syncing' | 'ready' | 'failed' | 'invalidated';
  snapshot_version: number; complete: boolean; synced_at?: string | null; expires_at?: string | null;
  error_code?: string; user_count: number; department_count: number;
}
export interface FeishuDirectoryUser { id: string; display_name: string; department_ids: string[]; status: 'active' | 'inactive' | 'unknown' }
export interface FeishuDirectoryDepartment { id: string; display_name: string; parent_id: string }
export interface FeishuDirectoryPage { items: (FeishuDirectoryUser | FeishuDirectoryDepartment)[]; next_cursor?: string | null; snapshot_version: number }
const directorySignal = (signal: AbortSignal) => AbortSignal.any([signal, AbortSignal.timeout(20000)]);
const directoryPath = (w: string, a: string, provider: 'feishu'|'dingtalk' = 'feishu') => `/workspaces/${encodeURIComponent(w)}/agents/${encodeURIComponent(a)}/channels/${provider}/directory`;
export const feishuDirectoryApi = {
  status: (w: string, a: string, signal: AbortSignal, provider: 'feishu'|'dingtalk' = 'feishu') => platformRequest<FeishuDirectoryStatus>(directoryPath(w, a, provider), { signal: directorySignal(signal) }),
  sync: (w: string, a: string, signal: AbortSignal, provider: 'feishu'|'dingtalk' = 'feishu') => platformRequest<FeishuDirectoryStatus>(`${directoryPath(w, a, provider)}/sync`, { method: 'POST', signal: directorySignal(signal) }),
  list: (w: string, a: string, kind: 'users' | 'departments', query: { q: string; cursor: string; snapshot_version: number; parent_id?: string; department_id?: string }, signal: AbortSignal, provider: 'feishu'|'dingtalk' = 'feishu') => {
    const params = new URLSearchParams({ limit: '50', snapshot_version: String(query.snapshot_version) });
    if (query.q) params.set('q', query.q);
    if (query.parent_id !== undefined) params.set('parent_id', query.parent_id);
    if (query.department_id) params.set('department_id', query.department_id);
    if (query.cursor) params.set('cursor', query.cursor);
    return platformRequest<FeishuDirectoryPage>(`${directoryPath(w, a, provider)}/${kind}?${params}`, { signal: directorySignal(signal) });
  }
};

import { platformRequest } from './platform';
import type { Page } from './manager';
export interface WorkItem {
  linked_task_ids?: string[];
  id: string;
  title: string;
  goal: string;
  criteria: string;
  status: string;
  reason: string;
  origin_employee: string;
  current_employee: string;
  created_at: string;
  updated_at: string;
}
export interface WorkEvent {
  id: string;
  kind: string;
  employee: string;
  target_employee: string;
  summary: string;
  created_at: string;
  deliveries: { conversation_id: string; destination: string; status: string }[];
}
export interface WorkDetail extends WorkItem {
  events: WorkEvent[];
  has_more: boolean;
}
export interface WorkScope {
  mode: 'inherit' | 'all' | 'allowlist' | 'denylist';
  employee_ids: string[];
}
export interface ReceiverPolicy {
  version: string;
  scope: WorkScope;
}
export function workStatusLabel(status: string) {
  return status === 'pending' ? '待处理' : workStatuses[status] || status;
}
export const workStatuses: Record<string, string> = {
  working: '办理中',
  awaiting_acceptance: '待发起人验收',
  completed: '已验收',
  failed: '失败结案',
  blocked: '待核对',
  cancelled: '已取消',
};
const base = (w: string) => '/workspaces/' + encodeURIComponent(w);
const receiver = (w: string, a: string) =>
  base(w) + '/agents/' + encodeURIComponent(a) + '/handoff-scope';
export const workApi = {
  list: (w: string, query: URLSearchParams, signal: AbortSignal) =>
    platformRequest<Page<WorkItem> & {status_counts?:Record<string,number>}>(base(w) + '/work-items?' + query, { signal }),
  detail: (w: string, id: string, before: string, signal: AbortSignal) =>
    platformRequest<WorkDetail>(
      base(w) +
        '/work-items/' +
        encodeURIComponent(id) +
        (before ? '?before=' + encodeURIComponent(before) : ''),
      { signal },
    ),
  receiver: (w: string, a: string, signal: AbortSignal) =>
    platformRequest<ReceiverPolicy>(receiver(w, a), { signal }),
  saveReceiver: (w: string, a: string, policy: ReceiverPolicy, signal: AbortSignal) =>
    platformRequest<ReceiverPolicy>(receiver(w, a), {
      method: 'PUT',
      body: JSON.stringify(policy),
      signal,
    }),
};

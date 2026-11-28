import { platformRequest, post } from './platform';
import type { Execution, Page } from './manager';
import type { TaskArtifact } from './public-task';
export interface TaskInput { confirmation_request_id?: string; id: string; seq: string; text: string; status: string; delivery_mode: 'steer' | 'queue'; turn_id: string | null; created_at: string; rejection_reason?: string | null; cancel_reason?: string | null }
export interface TaskTurn { id: string; turn_no: string; status: string; started_at: string; finished_at: string | null; has_unknown_tool_results: boolean }
type TaskAction = { allowed: boolean; reason: string | null };
export interface ReportedProgress {
  turn_id: string; seq: string; occurred_at: string; plan_version: string; summary: string;
  steps: { id: string; label: string; status: 'pending' | 'in_progress' | 'done' | 'skipped' }[];
  metric: { step_id: string; done: number; total: number; unit: string } | null;
}
export interface TaskRecord {
  video_generation_status?: string;
  reported_progress?: ReportedProgress | null;
  work_item_id?: string;
  planning_state?: 'planned' | 'started' | 'canceled'; planning_version?: string;
  task_id: string; session_id: string; conversation_id?: string; task_space_id?: string | null; title: string | null;
  agent: { id: string; name: string; avatar: string };
  source: { kind: string; chat_session_id: string | null; chat_input_id: string | null };
  lifecycle: string; execution: Execution; latest_input: Pick<TaskInput, 'id' | 'status' | 'delivery_mode' | 'turn_id'> | null; latest_turn: TaskTurn | null;
  created_at: string; revision: string; accepts_input: boolean; input_block_reason: string | null;
  actions: Record<'rename' | 'stop' | 'archive', TaskAction> & { supplement?: TaskAction };
}
export interface TaskProgress {
  phase: string; assignee?: string; confirmation?: { recipient?: string; question?: string };
  handoffs?: { assignee: string; phase: string }[]; delivery?: { channel: string; phase: string }[];
}
export interface TaskDetail extends TaskRecord { images?: {id:string;name:string}[]; videos?: {id:string;name:string}[]; video_generations?: {generation_id:string;status:string;seconds:number}[]; progress?: TaskProgress; artifacts?: TaskArtifact[]; initial_input: TaskInput | null; latest_result: { turn_id: string; text: string; truncated: boolean } | null }
export interface TaskSupplementReceipt { input_id: string; input_seq: string; context_state: "pending" | "committed" | "unknown" | "absent" | null }
export interface TaskReport { supplement_receipts?: TaskSupplementReceipt[]; supplement_receipts_truncated?: boolean; report_id: string | null; source_input_id: string; source_input_seq: string; source_turn_id: string | null; outcome: string; status: string; reason: string | null; created_at: string | null; target: { session_id: string; input_id: string; input_status: string; context_state: string | null; turn_id: string | null; turn_status: string | null } | null }
export type TaskFilters = Partial<Record<'q' | 'work_item_id' | 'agent_id' | 'conversation_id' | 'task_space_id' | 'execution_state' | 'latest_turn_status' | 'lifecycle', string>>;
const base = (w: string) => `/workspaces/${encodeURIComponent(w)}`;
const task = (w: string, id: string) => `${base(w)}/tasks/${encodeURIComponent(id)}`;
const session = (w: string, id: string) => `${base(w)}/sessions/${encodeURIComponent(id)}`;
const options = (signal?: AbortSignal) => signal ? { signal } : {};
const query = (values: Record<string, string>) => new URLSearchParams(Object.entries(values).filter(([, v]) => v)).toString();
export const taskApi = {
  list: (w: string, filters: TaskFilters, cursor = '', signal?: AbortSignal, counts = false) => platformRequest<Page<TaskRecord> & {status_counts?: Record<string,number>}>(`${base(w)}/tasks?${query({ ...filters, ...(counts ? {include_counts:'true'} : {}), cursor, limit: '20' })}`, options(signal)),
  detail: (w: string, id: string, signal?: AbortSignal) => platformRequest<TaskDetail>(task(w, id), options(signal)),
  reports: (w: string, id: string, cursor = '', signal?: AbortSignal, turnId = '') => platformRequest<Page<TaskReport>>(`${task(w, id)}/reports?${query({ cursor, turn_id: turnId, limit: '20' })}`, options(signal)),
  inputs: (w: string, id: string, cursor = '', signal?: AbortSignal) => platformRequest<Page<TaskInput>>(`${session(w, id)}/inputs?${query({ cursor, limit: '50' })}`, options(signal)),
  turns: (w: string, id: string, cursor = '', signal?: AbortSignal) => platformRequest<Page<TaskTurn>>(`${session(w, id)}/turns?${query({ cursor, limit: '50' })}`, options(signal)),
  withdraw: (w: string, id: string, input: string) => platformRequest<TaskInput>(`${session(w, id)}/inputs/${encodeURIComponent(input)}/withdraw`, post({})),
  rename: (w: string, id: string, title: string, revision: string) => platformRequest(`${session(w, id)}`, { method: 'PATCH', body: JSON.stringify({ title, expected_revision: revision }) }),
  archive: (w: string, id: string) => platformRequest(`${session(w, id)}/archive`, post({})),
  artifactBlob: async (w: string, id: string, artifactId: string, signal?: AbortSignal): Promise<Blob> => {
    const res = await fetch('/api/v1' + task(w, id) + '/artifacts/' + encodeURIComponent(artifactId), {
      credentials: 'include',
      cache: 'no-store',
      ...(signal ? { signal } : {})
    });
    if (!res.ok) throw new Error('产物暂不可读');
    return res.blob();
  },
  downloadArtifact: async (w: string, id: string, file: TaskArtifact, signal?: AbortSignal) => {
    const blob = await taskApi.artifactBlob(w, id, file.id, signal);
    const url = URL.createObjectURL(blob);
    const link = document.createElement('a');
    link.href = url;
    link.download = file.name;
    try { link.click(); } finally { setTimeout(() => URL.revokeObjectURL(url), 0); }
  }
};

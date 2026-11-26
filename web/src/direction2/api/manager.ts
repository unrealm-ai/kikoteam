import { platformRequest, post } from './platform';
export interface Conversation { web_user_id?: string | null; id: string; agent_id: string; lifecycle: string; chat_session_id: string | null; cleanup_pending: boolean; last_message_at?: string | null }
export interface RequiredAction { type: 'confirmation' | 'human_confirmation' | 'work_item'; request_id: string; title: string; message: string; turn_id: string; expires_at?: string }
export interface Execution { required_actions?: RequiredAction[]; state: string; active_turn_id: string | null; queued_input_count: number; background_process_count: number | null; block_reason: string | null }
export interface ManagerSession { id: string; created_at?: string; conversation_id: string; task_id: string | null; mode: 'chat' | 'task'; title: string | null; lifecycle: string; accepts_input: boolean; input_block_reason: string | null; execution: Execution }
export interface SessionEvent { attachments?: import("./project-files").SessionAttachment[]; action?: RequiredAction; required_actions?: RequiredAction[]; type: string; session_id: string; seq: string; occurred_at: string; message_id?: string; tool_use_id?: string; turn_id?: string; status?: string; content?: { type: string; text: string }[]; name?: string; summary?: string; input?: unknown; output?: unknown; input_state?: string; output_state?: string; execution?: Execution; lifecycle?: string; error?: { message?: string }; message?: string; [key: string]: unknown }
export interface Page<T> { items: T[]; next_cursor: string | null }
export interface EventPage extends Page<SessionEvent> { resume_cursor: string | null }
const base = (w: string) => `/workspaces/${encodeURIComponent(w)}`;
const sessionPath = (w: string, s: string) => `${base(w)}/sessions/${encodeURIComponent(s)}`;
const query = (p: Record<string, string>) => new URLSearchParams(p).toString();
export const managerApi = {
  direct: (w: string, agent: string, signal?: AbortSignal) => platformRequest<Conversation>(`${base(w)}/conversations/web-direct`, { ...post({ agent_id: agent }), ...(signal ? { signal } : {}) }),
  conversations: (w: string, cursor = '', signal?: AbortSignal) => platformRequest<Page<Conversation>>(`${base(w)}/conversations?${query({ limit: '50', ...(cursor ? { cursor } : {}) })}`, { ...(signal ? { signal } : {}) }),
  conversation: (w: string, id: string, signal?: AbortSignal) => platformRequest<Conversation>(`${base(w)}/conversations/${encodeURIComponent(id)}`, { ...(signal ? { signal } : {}) }),
  createChat: (w: string, conversation: string, text: string, key: string, signal?: AbortSignal, paths: string[] = [], ids: string[] = []) => platformRequest<ManagerSession>(`${base(w)}/sessions`, { ...post({ conversation_id: conversation, mode: 'chat', input: { text, ...(paths.length ? {attachment_paths:paths}: {}), ...(ids.length ? {attachment_ids:ids}: {}) } }), headers: { 'Idempotency-Key': key }, ...(signal ? { signal } : {}) }),
  session: (w: string, id: string, signal?: AbortSignal) => platformRequest<ManagerSession>(sessionPath(w, id), { ...(signal ? { signal } : {}) }),
  tasks: (w: string, conversation: string, cursor = '', signal?: AbortSignal) => platformRequest<Page<ManagerSession>>(`${base(w)}/sessions?${query({ conversation_id: conversation, mode: 'task', limit: '50', ...(cursor ? { cursor } : {}) })}`, { ...(signal ? { signal } : {}) }),
  history: (w: string, id: string, cursor = '', signal?: AbortSignal, turn = '') => platformRequest<EventPage>(`${sessionPath(w, id)}/events?${query({ direction: 'backward', limit: '50', ...(cursor ? { cursor } : {}), ...(turn ? { turn_id: turn } : {}) })}`, { ...(signal ? { signal } : {}) }),
  streamURL: (w: string, id: string, cursor: string, turn = '') => `/api/v1${sessionPath(w, id)}/events/stream?${query({ after: cursor, ...(turn ? { turn_id: turn } : {}) })}`,
  input: (w: string, id: string, text: string, key: string, signal?: AbortSignal, deliveryMode: 'steer' | 'queue' = 'steer', paths: string[] = [], ids: string[] = []) => platformRequest(`${sessionPath(w, id)}/inputs`, { ...post({ text, delivery_mode: deliveryMode, ...(paths.length ? {attachment_paths:paths}: {}), ...(ids.length ? {attachment_ids:ids}: {}) }), headers: { 'Idempotency-Key': key }, ...(signal ? { signal } : {}) }),
  confirm: (w: string, id: string, requestID: string, confirmed: boolean) => platformRequest(`${sessionPath(w, id)}/inputs`, { ...post({ confirmation: { request_id: requestID, confirmed } }), headers: { 'Idempotency-Key': requestID } }),
  stop: (w: string, id: string) => platformRequest<ManagerSession>(`${sessionPath(w, id)}/stop`, post({}))
};
export const eventTypes = ['work_item.notice', 'user.message', 'agent.message', 'agent.thinking', 'agent.tool_use', 'agent.tool_result', 'input.updated', 'turn.updated', 'session.updated', 'session.requires_action', 'session.error'];
export function mergeEvents(current: SessionEvent[], incoming: SessionEvent[], session: string): SessionEvent[] {
  const map = new Map(current.map(e => [e.seq, e]));
  for (const e of incoming) {
    if (e.session_id !== session || !/^[1-9]\d*$/.test(e.seq)) throw new Error('会话事件身份或序号无效');
    map.set(e.seq, e);
  }
  return [...map.values()].sort((a, b) => BigInt(a.seq) < BigInt(b.seq) ? -1 : 1);
}
export function projectEvents(events: SessionEvent[]): SessionEvent[] {
  const map = new Map<string, SessionEvent>();
  for (const e of events) {
    if (e.type === 'session.requires_action') {
      for (const action of e.required_actions || []) {
        const key = `confirmation:${action.request_id}`;
        if (!map.has(key)) map.set(key, { ...e, action });
      }
      continue;
    }
    const key = e.message_id ? `message:${e.message_id}` : e.tool_use_id ? `tool:${e.tool_use_id}` : e.type === 'turn.updated' ? `turn:${e.turn_id}` : e.type === 'session.error' ? `error:${e.seq}` : '';
    if (key) map.set(key, e);
  }
  // Map retains initial message position while snapshots replace its content.
  return [...map.values()];
}

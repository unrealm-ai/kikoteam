import { platformRequest } from './platform';

export type ChatScheduleFrequency = 'once' | 'hourly' | 'daily' | 'weekly' | 'monthly';
export interface ChatScheduleRule {
  frequency: ChatScheduleFrequency;
  timezone?: string;
  at?: string;
  interval?: number;
  minute?: number;
  times?: string[];
  weekdays?: number[];
  month_days?: number[];
}
export interface ManagedChatSchedule {
  schedule_id: string;
  version: string;
  instruction: string;
  title?: string;
  status: 'active' | 'paused' | 'completed' | 'canceled';
  channel: string;
  chat_type: string;
  source_label?: string;
  schedule: ChatScheduleRule;
  next_due?: string;
}
type Patch = { schedule?: ChatScheduleRule; enabled?: boolean };
const base = (workspace: string, employee: string) =>
  `/workspaces/${encodeURIComponent(workspace)}/agents/${encodeURIComponent(employee)}/chat-schedules`;
export const chatSchedulesApi = {
  list: (workspace: string, employee: string, signal: AbortSignal) =>
    platformRequest<{ items: ManagedChatSchedule[] }>(base(workspace, employee), { signal }),
  patch: (workspace: string, employee: string, row: ManagedChatSchedule, patch: Patch, signal: AbortSignal) =>
    platformRequest<ManagedChatSchedule>(`${base(workspace, employee)}/${encodeURIComponent(row.schedule_id)}`, {
      method: 'PATCH', body: JSON.stringify({ version: row.version, ...patch }), signal,
    }),
};

import { platformRequest } from './platform';
export interface AssistantCard { key: string; title: string; description: string; input: string }
export interface AssistantEntry { agent_id?: string; cards: AssistantCard[]; experienced?: boolean; pending?: boolean }
export interface AssistantSettings { version: number; enabled: boolean; available: boolean }
export const assistantApi = {
  entry: (workspace: string, signal?: AbortSignal) => platformRequest<AssistantEntry>(`/workspaces/${encodeURIComponent(workspace)}/assistant`, signal ? {signal} : {}),
  settings: (signal: AbortSignal) => platformRequest<AssistantSettings>('/platform/assistant-settings', {signal}),
  save: (value: AssistantSettings, signal: AbortSignal) => platformRequest<AssistantSettings>('/platform/assistant-settings', {
    signal, method: 'PATCH', body: JSON.stringify({expected_version: value.version, enabled: value.enabled}),
  }),
};

import { platformRequest } from './platform';
export interface AssistantCard { key: string; title: string; description: string; input: string }
export interface AssistantEntry { agent_id?: string; cards: AssistantCard[]; pending?: boolean }
export const assistantApi = {
  entry: (workspace: string, signal?: AbortSignal) => platformRequest<AssistantEntry>(`/workspaces/${encodeURIComponent(workspace)}/assistant`, signal ? {signal} : {}),
};

import { platformRequest, post } from './platform';
import type { SelectionRef } from './selectors';

export interface HumanRecipient {
  id?: string;
  name?: string;
  scope: string;
  is_default?: boolean;
  ref: SelectionRef;
}

export interface HumanRecipients {
  version: string;
  recipients: HumanRecipient[];
}

export type HumanDecision = 'approve' | 'reject' | 'information' | 'revise' | 'needs_more';
export interface HumanQuestionItem {
  id: string;
  text: string;
  required: boolean;
  answerType: 'text' | 'choice' | 'boolean' | 'number';
  constraints?: string[];
}
export interface HumanAnswerItem {
  questionId: string;
  value: string;
}
export interface HumanRequest {
  id: string;
  version: string;
  state: string;
  question: {
    question: string;
    background?: string;
    answer_type: 'decision' | 'information';
    questions?: HumanQuestionItem[];
    proposedAction?: string;
    impactScope?: string;
  };
}

export type HistoryFilter = 'pending' | 'resolved' | 'all';
export interface HumanHistoryItem extends Omit<HumanRequest, 'question'> {
  detail_visible: boolean;
  question?: HumanRequest['question'];
  recipient_name?: string;
  recipient_provider?: string;
  agent_name?: string;
  created_at: string;
  answered_at?: string;
  answer?: {decision: HumanDecision; text?: string; answers?: HumanAnswerItem[]};
  clarification?: string;
}
export interface HumanHistoryPage {
  items: HumanHistoryItem[];
  total: number;
  pending: number;
  resolved: number;
}
const readSignal = (signal: AbortSignal) => AbortSignal.any([signal, AbortSignal.timeout(20_000)]);
const root = (workspace: string) => `/workspaces/${encodeURIComponent(workspace)}`;
const recipients = (workspace: string, employee: string) => `${root(workspace)}/agents/${encodeURIComponent(employee)}/human-recipients`;

export const humanRequestsApi = {
  recipients: (workspace: string, employee: string, signal: AbortSignal) =>
    platformRequest<HumanRecipients>(recipients(workspace, employee), { signal }),
  saveRecipients: (workspace: string, employee: string, value: HumanRecipients, signal: AbortSignal) =>
    platformRequest<HumanRecipients>(recipients(workspace, employee), { method: 'PATCH', body: JSON.stringify(value), signal }),
  inbox: (workspace: string, signal?: AbortSignal, offset = 0, id = '') =>
    platformRequest<{ items: HumanRequest[]; total?:number }>(`${root(workspace)}/human-requests${id ? '?request_id=' + encodeURIComponent(id) : offset ? '?offset=' + offset : ''}`, signal ? { signal: readSignal(signal) } : {}),
  summary: (workspace: string) => platformRequest<{pending: number}>(root(workspace) + '/human-requests/summary'),
  history: (workspace: string, signal: AbortSignal, filter: HistoryFilter = 'all', offset = 0, id = '') => {
    const query = new URLSearchParams({ filter, offset: String(offset) });
    if (id) query.set('request_id', id);
    return platformRequest<HumanHistoryPage>(root(workspace) + '/human-requests/history?' + query, { signal: readSignal(signal) });
  },
  answer: (workspace: string, request: Pick<HumanRequest, 'id' | 'version'>, decision: HumanDecision, text: string, signal: AbortSignal, answers?: HumanAnswerItem[]) =>
    platformRequest<{ id: string; state: 'approved' | 'rejected' | 'revised' | 'needs_more' | 'pending'; remaining?: number[]; missingFields?: string[]; requires_approval?: boolean }>(`${root(workspace)}/human-requests/${encodeURIComponent(request.id)}/answer`,
      { ...post({ version: request.version, decision, text, answers }), signal }),
};

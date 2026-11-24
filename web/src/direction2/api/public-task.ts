import { D2ApiError } from './client';

export interface TaskActivity { kind: string; text: string; occurred_at: string }
export interface TaskArtifact { id: string; name: string; size: number }
export interface PublicTask {
  title: string;
  status: string;
  permission: 'read' | 'write';
  can_append: boolean;
  can_stop: boolean;
  can_continue: boolean;
  stop_version?: string;
  cursor: string;
  activities: TaskActivity[];
  activity_truncated: boolean;
  artifacts: TaskArtifact[];
  input_receipt?: { state: 'accepted' | 'queued' | 'admitted' };
}

const path = '/api/v1/public/task-detail';

export function publicTaskToken(pathname: string): string | null {
  const match = /^\/tasks\/([A-Za-z0-9_-]+)\/?$/.exec(pathname);
  return match?.[1] || null;
}

async function request(token: string, signal: AbortSignal, suffix = '', init: RequestInit = {}) {
  const headers = new Headers(init.headers);
  headers.set('Authorization', 'Bearer ' + token);
  if (init.body !== undefined) headers.set('Content-Type', 'application/json');
  const response = await fetch(path + suffix, {
    ...init, headers, signal, credentials: 'omit', redirect: 'error', cache: 'no-store', referrerPolicy: 'no-referrer',
  });
  if (response.ok) return response;
  const body = await response.json().catch(() => null);
  throw new D2ApiError(response.status, body?.error?.code || '', body?.error?.message || '无法访问任务详情');
}

export async function readTaskSnapshot(response: Response): Promise<PublicTask> {
  return parseTaskSnapshot(await response.json());
}

export function parseTaskSnapshot(value: unknown): PublicTask {
  if (!value || typeof value !== 'object') throw new Error('任务详情响应格式无效');
  const task = value as Partial<PublicTask>;
  if (typeof task.cursor !== 'string' || !task.cursor || typeof task.title !== 'string' || typeof task.status !== 'string' ||
      (task.permission !== 'read' && task.permission !== 'write') ||
      !Array.isArray(task.activities) || !task.activities.every(item => item && typeof item.text === 'string' && typeof item.kind === 'string' && typeof item.occurred_at === 'string') ||
      !Array.isArray(task.artifacts) || !task.artifacts.every(file => file && typeof file.id === 'string' && typeof file.name === 'string' && Number.isSafeInteger(file.size))) {
    throw new Error('任务详情响应格式无效');
  }
  return task as PublicTask;
}

export const publicTaskApi = {
  snapshot: async (token: string, signal: AbortSignal) => readTaskSnapshot(await request(token, signal)),
  stream: (token: string, cursor: string, signal: AbortSignal) => request(token, signal, '/events', {
    headers: { 'Accept': 'text/event-stream', 'Last-Event-ID': cursor },
  }),
  call: async (token: string, op: 'append' | 'stop' | 'continue', body: Record<string, unknown>, key: string, signal: AbortSignal) =>
    readTaskSnapshot(await request(token, signal, '/' + op, {
      method: 'POST', body: JSON.stringify(body), headers: { 'Idempotency-Key': key },
    })),
  artifact: async (token: string, id: string, signal: AbortSignal) => request(token, signal, '/artifacts/' + encodeURIComponent(id)),
};

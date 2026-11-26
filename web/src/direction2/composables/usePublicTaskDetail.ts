import { computed, onScopeDispose, ref, shallowRef } from 'vue';
import { eventTypes, mergeEvents, projectEvents, type SessionEvent } from '../api/manager';
import { D2ApiError } from '../api/client';
import { parseTaskSnapshot, publicTaskApi, type PublicTask, type TaskArtifact } from '../api/public-task';

type SSEFrame = { id: string; event: string; data: string };

export async function readTaskStream(response: Response, apply: (frame: SSEFrame) => void) {
  if (!response.body || !response.headers.get('Content-Type')?.startsWith('text/event-stream')) throw new Error('未建立任务实时连接');
  const reader = response.body.pipeThrough(new TextDecoderStream()).getReader();
  let buffer = '', id = '', event = '', data: string[] = [], dataSize = 0;
  try {
    for (;;) {
      const next = await reader.read();
      if (next.done) return;
      buffer += next.value;
      let end: number;
      while ((end = buffer.indexOf('\n')) >= 0) {
        const line = buffer.slice(0, end).replace(/\r$/, '');
        buffer = buffer.slice(end + 1);
        if (!line) {
          if (data.length) apply({ id, event, data: data.join('\n') });
          id = ''; event = ''; data = []; dataSize = 0;
          continue;
        }
        if (line.startsWith(':')) continue;
        const split = line.indexOf(':');
        const field = split < 0 ? line : line.slice(0, split);
        const value = split < 0 ? '' : line.slice(split + 1).replace(/^ /, '');
        if (field === 'id') id = value;
        else if (field === 'event') event = value;
        else if (field === 'data') {
          dataSize += value.length + 1;
          data.push(value);
        }
        if (dataSize > 4_194_304) throw new Error('任务事件超过大小限制');
      }
      if (buffer.length > 4_194_304) throw new Error('任务事件超过大小限制');
    }
  } finally {
    await reader.cancel().catch(() => {});
    reader.releaseLock();
  }
}

function isDenied(error: unknown) {
  return error instanceof D2ApiError && [401, 403, 404, 410].includes(error.status);
}

function requestKey() {
  return Array.from(crypto.getRandomValues(new Uint8Array(16)), n => n.toString(16).padStart(2, '0')).join('');
}

export function usePublicTaskDetail(token: string) {
  const task = shallowRef<PublicTask | null>(null), loading = ref(false), error = ref(''), connection = ref('');
  const events = shallowRef<SessionEvent[]>([]), olderCursor = ref<string | null>(null), loadingOlder = ref(false), executionError = ref('');
  let executionTimer: ReturnType<typeof setTimeout> | undefined;
  let session = '';
  const lifetime = new AbortController();
  let current: AbortController | undefined, timer: ReturnType<typeof setTimeout> | undefined;
  let cursor = '', attemptedCursorRecovery = false, generation = 0, attempts = 0;
  const mutationKeys = new Map<string, string>();

  function abandon() {
    generation++;
    current?.abort();
    if (timer) clearTimeout(timer);
    timer = undefined;
    if (executionTimer) clearTimeout(executionTimer);
    executionTimer = undefined;
    loadingOlder.value = false;
  }

  function deny() {
    abandon();
    task.value = null; events.value = []; session = ''; olderCursor.value = null; executionError.value = ''; loading.value = false; cursor = '';
    connection.value = '订阅已停止'; error.value = '此任务详情链接无效、已过期或已撤销。';
  }

  function apply(next: PublicTask, eventID = next.cursor) {
    if (eventID !== cursor || !task.value) task.value = next;
    cursor = eventID;
  }

  function onFrame(frame: SSEFrame) {
    if (frame.event === 'task.updated') {
      const snapshot = parseTaskSnapshot(JSON.parse(frame.data));
      if (!frame.id) throw new Error('任务事件缺少续读游标');
      apply(snapshot, frame.id);
      attempts = 0; attemptedCursorRecovery = false;
    } else if (frame.event === 'stream.error') {
      const raw = JSON.parse(frame.data);
      const issue = (raw.error || raw) as { status?: number; code?: string; message?: string };
      throw new D2ApiError(issue.status || 0, issue.code || '', issue.message || '实时连接已结束');
    }
  }

  async function connect(g: number, signal: AbortSignal) {
    try {
      const response = await publicTaskApi.stream(token, cursor, signal);
      if (g !== generation) return;
      connection.value = '已连接'; error.value = '';
      await readTaskStream(response, frame => { if (g === generation) onFrame(frame); });
      if (g !== generation) return;
      throw new Error('实时连接中断');
    } catch (err) {
      if (g !== generation) return;
      if (isDenied(err) || err instanceof D2ApiError && ['invalid_token', 'token_expired', 'token_revoked', 'task_link_invalid', 'task_detail_unavailable', 'task_detail_link_unavailable'].includes(err.code)) { deny(); return; }
      if (err instanceof D2ApiError && ['invalid_cursor', 'cursor_expired'].includes(err.code)) {
        if (!attemptedCursorRecovery) { attemptedCursorRecovery = true; await load(false); return; }
        connection.value = '订阅已停止'; error.value = '无法续读任务，请重新加载。'; return;
      }
      if (++attempts > 3) {
        connection.value = '订阅已停止';
        error.value = '无法继续接收执行进度，请重新加载。';
        return;
      }
      connection.value = '连接中断，正在重连';
      timer = setTimeout(() => void connect(g, signal), 1000 * attempts);
    }
  }

  function merge(incoming: SessionEvent[]) {
    if (!session && incoming.length) session = incoming[0]!.session_id;
    if (incoming.length && !session) throw new Error('任务事件缺少会话标识');
    events.value = mergeEvents(events.value, incoming, session);
  }

  async function execution(g: number, signal: AbortSignal, after: string, retries = 0) {
    try {
      const response = await publicTaskApi.execution(token, after, signal);
      if (g !== generation) return;
      executionError.value = '';
      await readTaskStream(response, frame => {
        if (g !== generation) return;
        if (frame.event === 'stream.error') {
          const value = JSON.parse(frame.data).error;
          throw new D2ApiError(value?.code === 'task_detail_link_unavailable' ? 404 : 0, value?.code || '', value?.message || '执行事件连接中断');
        }
        if (!eventTypes.includes(frame.event) || !frame.id) throw new Error('无效的执行事件');
        const item = JSON.parse(frame.data) as SessionEvent;
        if (item.type !== frame.event) throw new Error('执行事件类型不一致');
        merge([item]); after = frame.id; retries = 0;
      });
      throw new Error('执行事件连接中断');
    } catch (err) {
      if (g !== generation) return;
      if (isDenied(err)) { deny(); return; }
      if (err instanceof D2ApiError && err.code === 'invalid_cursor' && retries < 3) {
        await loadExecution(g, signal, true);
        return;
      }
      if (retries >= 3) { executionError.value = '执行过程连接已断开，请重新加载。'; return; }
      executionTimer = setTimeout(() => void execution(g, signal, after, retries + 1), 1000 * (retries + 1));
    }
  }

  async function loadExecution(g: number, signal: AbortSignal, recover = false) {
    try {
      const page = await publicTaskApi.history(token, '', AbortSignal.any([signal, AbortSignal.timeout(15_000)]));
      if (g !== generation) return;
      if (!page.resume_cursor) throw new Error('执行历史缺少续读游标');
      if (recover) { events.value = []; session = ''; }
      merge(page.items);
      olderCursor.value = page.next_cursor;
      void execution(g, signal, page.resume_cursor, recover ? 3 : 0);
    } catch (err) {
      if (g !== generation) return;
      if (isDenied(err)) deny();
      else executionError.value = '无法读取执行过程，请重新加载。';
    }
  }

  async function older() {
    const id = olderCursor.value, g = generation, signal = current?.signal;
    if (!id || !signal || loadingOlder.value) return;
    loadingOlder.value = true;
    try {
      const page = await publicTaskApi.history(token, id, AbortSignal.any([signal, AbortSignal.timeout(15_000)]));
      if (g !== generation) return;
      if (page.next_cursor === id) throw new Error('执行历史游标重复');
      merge(page.items);
      olderCursor.value = page.next_cursor;
    } catch (err) {
      if (g !== generation) return;
      if (isDenied(err)) deny();
      else executionError.value = '无法读取更早的执行记录，请重新加载。';
    } finally { if (g === generation) loadingOlder.value = false; }
  }

  async function load(reset = true) {
    if (!token || lifetime.signal.aborted) return;
    abandon(); attempts = 0;
    if (reset) attemptedCursorRecovery = false;
    const g = generation, handler = current = new AbortController();
    loading.value = !task.value;
    error.value = ''; executionError.value = '';
    try {
      const snapshot = await publicTaskApi.snapshot(token, AbortSignal.any([handler.signal, AbortSignal.timeout(15_000)]));
      if (g !== generation) return;
      task.value = snapshot; cursor = snapshot.cursor;
      void connect(g, handler.signal);
      void loadExecution(g, handler.signal);
    } catch (err) {
      if (g !== generation) return;
      if (isDenied(err)) deny();
      else error.value = '读取任务详情失败，请重新加载。';
    } finally { if (g === generation) loading.value = false; }
  }

  async function mutate(op: 'append' | 'continue' | 'stop', payload: Record<string, unknown>) {
    const fingerprint = JSON.stringify([op, payload]);
    const key = mutationKeys.get(fingerprint) || requestKey();
    mutationKeys.set(fingerprint, key);
    try {
      const snapshot = await publicTaskApi.call(token, op, payload, key, AbortSignal.any([lifetime.signal, AbortSignal.timeout(20_000)]));
      mutationKeys.delete(fingerprint);
      // An action response may predate already-applied stream events. Request the current snapshot instead.
      void load();
      return snapshot.input_receipt?.state;
    } catch (err) {
      if (err instanceof D2ApiError && [401, 404, 410].includes(err.status)) deny();
      else if (err instanceof D2ApiError && err.status < 500 && ![408, 429].includes(err.status)) { mutationKeys.delete(fingerprint); void load(); }
      throw err;
    }
  }

  async function download(file: TaskArtifact) {
    try {
      const response = await publicTaskApi.artifact(token, file.id, lifetime.signal);
      const blob = await response.blob();
      if (lifetime.signal.aborted) return;
      const url = URL.createObjectURL(blob);
      const link = document.createElement('a'); link.href = url; link.download = file.name;
      try { link.click(); } finally { setTimeout(() => URL.revokeObjectURL(url), 0); }
    } catch (err) { if (isDenied(err)) void load(); throw err; }
  }

  onScopeDispose(() => { abandon(); lifetime.abort(); mutationKeys.clear(); });
  if (token) void load();
  return { task, loading, error, connection, load, mutate, download, rows: computed(() => projectEvents(events.value)), older, olderCursor, loadingOlder, executionError };
}

export type PublicTaskDetailFlow = ReturnType<typeof usePublicTaskDetail>;

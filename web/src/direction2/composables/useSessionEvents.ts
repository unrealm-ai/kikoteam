import { computed, onScopeDispose, ref, shallowRef, watch, type Ref } from 'vue';
import { D2ApiError } from '../api/client';
import { eventTypes, managerApi, mergeEvents, projectEvents, type ManagerSession, type SessionEvent } from '../api/manager';

export function useSessionEvents(workspace: Ref<string>, sessionID: Ref<string>, changed?: () => void, turnID?: Ref<string | undefined>) {
  const events = shallowRef<SessionEvent[]>([]);
  const session = shallowRef<ManagerSession | null>(null);
  const loading = ref(false), loadingOlder = ref(false), error = ref(''), connection = ref('');
  const olderCursor = ref<string | null>(null);
  let reconnectAttempts = 0;
  let refreshAgain = false;
  let generation = 0, controller: AbortController | null = null, source: EventSource | null = null;
  let retry: ReturnType<typeof setTimeout> | null = null, refreshPending = false;
  function close() { generation++; controller?.abort(); source?.close(); source = null; if (retry) clearTimeout(retry); retry = null; refreshPending = false; refreshAgain = false; }
  async function refresh() {
    const g = generation;
    if (!sessionID.value || !controller) return;
    if (refreshPending) { refreshAgain = true; return; }
    refreshPending = true;
    try { const value = await managerApi.session(workspace.value, sessionID.value, controller.signal); if (g === generation) session.value = value; }
    catch (e) { if (g === generation) error.value = e instanceof Error ? e.message : '状态刷新失败'; }
    finally { if (g === generation) { refreshPending = false; if (refreshAgain) { refreshAgain = false; void refresh(); } } }
  }
  function open(cursor: string, g: number, w: string, id: string) {
    if (g !== generation) return;
    source?.close();
    const stream = new EventSource(managerApi.streamURL(w, id, cursor, turnID?.value || ''), { withCredentials: true }); source = stream;
    let last = cursor, reconnecting = false;
    stream.onopen = () => { if (g === generation) { connection.value = '已连接'; error.value = ''; reconnectAttempts = 0; } };
    async function reconnect(message = '连接中断，正在重连') {
      if (g !== generation || reconnecting) return;
      reconnecting = true; stream.close(); connection.value = message;
      if (++reconnectAttempts > 3) { connection.value = '订阅已停止'; error.value = '连续重连失败，请重新加载会话（可能是登录、权限或游标已变化）'; return; }
      try {
        const current = await managerApi.session(w, id, controller!.signal);
        if (g !== generation) return;
        session.value = current;
        retry = setTimeout(() => open(last, g, w, id), 2000);
      } catch (e) {
        if (g !== generation) return;
        error.value = e instanceof Error ? e.message : '无法恢复连接';
        if (e instanceof D2ApiError && [400, 401, 403, 404].includes(e.status)) { connection.value = '订阅已停止'; events.value = []; session.value = null; return; }
        retry = setTimeout(() => { reconnecting = false; void reconnect(); }, 3000);
      }
    }
    stream.onerror = () => { void reconnect(); };
    for (const type of eventTypes) stream.addEventListener(type, raw => {
      if (g !== generation) return;
      try {
        const event = raw as MessageEvent<string>; const value = JSON.parse(event.data) as SessionEvent;
        events.value = mergeEvents(events.value, [value], id);
        if (!event.lastEventId) throw new Error('可靠事件缺少续读游标');
        last = event.lastEventId;
        // The progress event is already a full snapshot. A counter update
        // does not invalidate the session or task detail resources.
        if (value.type === 'task.progress_updated') return;
        if (value.type.endsWith('.updated') || value.type === 'session.requires_action') void refresh();
        changed?.();
      } catch (e) { stream.close(); error.value = e instanceof Error ? e.message : '事件格式错误'; connection.value = '订阅已停止'; }
    });
    stream.addEventListener('stream.error', raw => {
      if (g !== generation) return;
      let message = '事件订阅中断', code = '';
      try { const value = JSON.parse((raw as MessageEvent<string>).data); message = value.message || message; code = value.code || ''; } catch { /* keep safe fallback */ }
      if (['invalid_cursor', 'invalid_argument'].includes(code)) { stream.close(); connection.value = '订阅已停止'; error.value = message; return; }
      void reconnect(message);
    });
  }
  async function load() {
    close(); reconnectAttempts = 0; events.value = []; session.value = null; olderCursor.value = null; error.value = ''; connection.value = ''; loadingOlder.value = false;
    const g = generation, w = workspace.value, id = sessionID.value;
    if (!w || !id) { loading.value = false; return; }
    controller = new AbortController(); loading.value = true;
    try {
      const page = await managerApi.history(w, id, '', controller.signal, turnID?.value || '');
      if (g !== generation) return;
      const resource = await managerApi.session(w, id, controller.signal);
      if (g !== generation) return;
      session.value = resource; events.value = mergeEvents([], page.items, id); olderCursor.value = page.next_cursor;
      if (!page.resume_cursor) throw new Error('历史响应缺少实时续读游标');
      open(page.resume_cursor, g, w, id);
    } catch (e) { if (g === generation) error.value = e instanceof Error ? e.message : '会话加载失败'; }
    finally { if (g === generation) loading.value = false; }
  }
  async function older() {
    const cursor = olderCursor.value, g = generation;
    if (!cursor || loadingOlder.value || !controller) return;
    loadingOlder.value = true;
    try {
      const page = await managerApi.history(workspace.value, sessionID.value, cursor, controller.signal, turnID?.value || '');
      if (g !== generation) return;
      if (page.next_cursor === cursor) throw new Error('历史分页游标重复');
      events.value = mergeEvents(events.value, page.items, sessionID.value); olderCursor.value = page.next_cursor;
    } catch (e) { if (g === generation) error.value = e instanceof Error ? e.message : '旧消息加载失败'; }
    finally { if (g === generation) loadingOlder.value = false; }
  }
  watch(turnID ? [workspace, sessionID, turnID] : [workspace, sessionID], () => { void load(); }, { immediate: true });
  onScopeDispose(close);
  return { events, rows: computed(() => projectEvents(events.value)), session, loading, loadingOlder, error, connection, olderCursor, older, reload: load, refresh };
}

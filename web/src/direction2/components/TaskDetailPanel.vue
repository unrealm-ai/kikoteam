<script setup lang="ts">
import { copyText } from '../utils/clipboard';
import WorkItemTimeline from './WorkItemTimeline.vue';
import { computed, onMounted, onUnmounted, ref, watch } from 'vue';
import { NButton, NInput, NSelect, NSkeleton } from 'naive-ui';
import { taskApi, type TaskDetail, type TaskInput, type TaskTurn, type TaskReport } from '../api/tasks';
import type { Page } from '../api/manager';
import { D2ApiError } from '../api/client';
import ManagerSessionPanel from './ManagerSessionPanel.vue';
import SessionMarkdown from './SessionMarkdown.vue';
import TaskSupplementReceipts from './TaskSupplementReceipts.vue';
import { sessionStatus, workItemProgress } from '../utils/session-presentation';
const props = defineProps<{ workspaceId: string; taskId: string; taskCenter?: boolean; taskRecordActions?: boolean; initialTurnId?: string | undefined; presentation?: 'sidebar' | 'drawer' | 'page' }>();
const emit = defineEmits<{ close: []; taskChanged: []; openWork: [id: string]; openSourceConversation: [agent: string, conversation: string] }>();
const detail = ref<TaskDetail | null>(null), loading = ref(false), error = ref(''), actionError = ref(''), notice = ref('');
const tab = ref('process'), turn = ref(props.initialTurnId || ''), inputs = ref<TaskInput[]>([]), turns = ref<TaskTurn[]>([]), reports = ref<TaskReport[]>([]);
const historicalReport = ref<TaskReport | null>(null);
const inputCursor = ref<string | null>(null), turnCursor = ref<string | null>(null), reportCursor = ref<string | null>(null);
const renameOpen = ref(false), title = ref(''), renameRevision = ref(''), busy = ref(false), refreshing = ref(false);
let generation = 0, controller = new AbortController(), timer: ReturnType<typeof setInterval> | undefined;
let refreshAfterWrite = false;
let initialized = false, lastEventRefresh = 0, historicalRevision = 0;
const selectedTurn = computed(() => turns.value.find(t => t.id === turn.value) || (!turn.value ? detail.value?.latest_turn : null));
const selectedReport = computed(() => {
  const id = turn.value || detail.value?.latest_turn?.id;
  if (!id) return null;
  return reports.value.find(r => r.source_turn_id === id) ?? (historicalReport.value?.source_turn_id === id ? historicalReport.value : null);
});
async function refreshHistoricalReport() {
  const id = turn.value, g = generation, taskId = detail.value?.task_id;
  if (!id || !taskId || reports.value.some(r => r.source_turn_id === id)) return;
  const revision = ++historicalRevision;
  try {
    const page = await taskApi.reports(props.workspaceId, taskId, '', controller.signal, id);
    if (g === generation && revision === historicalRevision && turn.value === id) historicalReport.value = page.items.find(r => r.source_turn_id === id) ?? null;
  } catch (e) {
    if (g === generation && revision === historicalRevision && turn.value === id && e instanceof D2ApiError && [401, 403, 404].includes(e.status)) historicalReport.value = null;
  }
}
const duration = computed(() => { const t = selectedTurn.value; if (!t?.finished_at) return ''; const seconds = (Date.parse(t.finished_at) - Date.parse(t.started_at)) / 1000; return seconds >= 0 ? `${seconds.toFixed(1)} 秒` : ''; });
const turnOptions = computed(() => [{ label: '最近执行过程（实时）', value: '' }, ...(turn.value && !turns.value.some(t => t.id === turn.value) ? [{ label: '指定历史轮次', value: turn.value }] : []), ...turns.value.map(t => ({ label: `第 ${t.turn_no} 轮 · ${sessionStatus(t.status)}`, value: t.id }))]);
const recordsEditable = computed(() => !props.taskCenter || props.taskRecordActions);
const canRename = computed(() => recordsEditable.value && detail.value?.actions.rename.allowed);
const canWithdraw = (input: TaskInput) => recordsEditable.value && input.status === 'queued' && !input.confirmation_request_id;
const canSupplement = computed(() => detail.value?.accepts_input && detail.value.actions.supplement?.allowed !== false);
const canArchive = computed(() => detail.value?.actions.archive.allowed && !(props.taskCenter && turn.value));
const planned = computed(() => detail.value?.planning_state === 'planned' || detail.value?.planning_state === 'canceled');
const executionLabel = computed(() => detail.value?.execution.block_reason === 'engine_recovery_pending' ? sessionStatus(detail.value.execution.block_reason) : detail.value?.planning_state === 'planned' ? '待办：尚未启动' : detail.value?.planning_state === 'canceled' ? '待办已取消（未执行）' : workItemProgress(detail.value?.execution) || (detail.value?.execution.required_actions?.length ? '待确认' : detail.value?.execution.state === 'idle' ? '当前无执行轮次' : sessionStatus(detail.value?.execution.state)));
const reportStatus: Record<string, string> = { pending: '等待回报补偿', admitted: '已提交到主会话', suppressed: '未回报到主会话', not_enabled: '历史输入未启用回报', not_applicable: '无来源主会话' };
function fail(e: unknown) { return e instanceof Error ? e.message : '加载失败，请重试'; }
async function loadedPages<T>(read: (cursor: string) => Promise<Page<T>>, count: number) {
  const items: T[] = [], seen = new Set<string>(); let cursor = '';
  for (;;) { const page = await read(cursor); items.push(...page.items); if (!page.next_cursor || items.length >= Math.max(count, 1)) return { items, next_cursor: page.next_cursor }; if (seen.has(page.next_cursor) || page.next_cursor === cursor) throw new Error('分页游标重复'); seen.add(page.next_cursor); cursor = page.next_cursor; }
}
async function refresh(reset = false) {
  if (refreshing.value || !props.taskId || document.hidden) return;
  const g = generation, w = props.workspaceId, id = props.taskId; refreshing.value = true;
  try {
    const d = await taskApi.detail(w, id, controller.signal); if (g !== generation) return;
    detail.value = d;
    reset = reset || !initialized;
    const signal = controller.signal;
    const [i, t, r] = await Promise.all([loadedPages(c => taskApi.inputs(w, d.session_id, c, signal), reset ? 0 : inputs.value.length), loadedPages(c => taskApi.turns(w, d.session_id, c, signal), reset ? 0 : turns.value.length), loadedPages(c => taskApi.reports(w, id, c, signal), reset ? 0 : reports.value.length)]);
    if (g !== generation) return;
    inputs.value = i.items; turns.value = t.items; reports.value = r.items;
    inputCursor.value = i.next_cursor; turnCursor.value = t.next_cursor; reportCursor.value = r.next_cursor; initialized = true;
    void refreshHistoricalReport();
    error.value = '';
  } catch (e) { if (g === generation) { error.value = fail(e); if (e instanceof D2ApiError && [401, 403, 404].includes(e.status)) { detail.value = null; inputs.value = []; turns.value = []; reports.value = []; historicalReport.value = null; controller.abort(); } } }
  finally {
    if (g === generation) {
      loading.value = false;
      refreshing.value = false;
      if (refreshAfterWrite) {
        refreshAfterWrite = false;
        void refresh();
      }
    }
  }
}
async function load() { generation++; refreshAfterWrite = false; controller.abort(); controller = new AbortController(); initialized = false; detail.value = null; inputs.value = []; turns.value = []; reports.value = []; historicalReport.value = null; error.value = ''; actionError.value = ''; notice.value = ''; turn.value = props.initialTurnId || ''; tab.value = 'process'; renameOpen.value = false; refreshing.value = false; loading.value = true; inputCursor.value = null; turnCursor.value = null; reportCursor.value = null; await refresh(true); }
async function more(kind: 'inputs' | 'turns' | 'reports') {
  const d = detail.value, g = generation; if (!d || busy.value || refreshing.value) return; busy.value = true;
  try {
    const cursor = kind === 'inputs' ? inputCursor.value : kind === 'turns' ? turnCursor.value : reportCursor.value; if (!cursor) return;
    if (kind === 'inputs') { const p = await taskApi.inputs(props.workspaceId, d.session_id, cursor, controller.signal); if (g !== generation) return; if (p.next_cursor === cursor) throw new Error('输入分页游标重复'); inputs.value = [...new Map([...inputs.value, ...p.items].map(v => [v.id, v])).values()]; inputCursor.value = p.next_cursor; }
    else if (kind === 'turns') { const p = await taskApi.turns(props.workspaceId, d.session_id, cursor, controller.signal); if (g !== generation) return; if (p.next_cursor === cursor) throw new Error('轮次分页游标重复'); turns.value = [...new Map([...turns.value, ...p.items].map(v => [v.id, v])).values()]; turnCursor.value = p.next_cursor; }
    else { const p = await taskApi.reports(props.workspaceId, props.taskId, cursor, controller.signal); if (g !== generation) return; if (p.next_cursor === cursor) throw new Error('回报分页游标重复'); reports.value = [...new Map([...reports.value, ...p.items].map(v => [v.source_input_id, v])).values()]; reportCursor.value = p.next_cursor; }
  } catch (e) { if (g === generation) actionError.value = fail(e); } finally { if (g === generation) busy.value = false; }
}
function toggleRename() {
  const d = detail.value;
  if (!d || !canRename.value || busy.value) return;
  renameOpen.value = !renameOpen.value;
  if (!renameOpen.value) return;
  title.value = d.title || '';
  renameRevision.value = d.revision;
}
async function action(kind: 'rename' | 'archive' | 'withdraw', input = '') {
  const d = detail.value, g = generation; if (!d || busy.value) return;
  if (kind === 'archive' && !canArchive.value) return;
  if (kind === 'rename' && (!canRename.value || !title.value.trim() || !renameRevision.value)) return;
  if (kind === 'withdraw' && !inputs.value.some(item => item.id === input && canWithdraw(item))) return;
  busy.value = true; actionError.value = ''; notice.value = '';
  try { if (kind === 'rename') await taskApi.rename(props.workspaceId, d.session_id, title.value.trim(), renameRevision.value); else if (kind === 'archive') await taskApi.archive(props.workspaceId, d.session_id); else await taskApi.withdraw(props.workspaceId, d.session_id, input); if (g !== generation) return; renameOpen.value = false; notice.value = kind === 'archive' ? '任务已归档，历史仍可查看' : kind === 'withdraw' ? '输入已撤回' : '标题已更新'; await commandAccepted(); }
  catch (e) { if (g === generation) { actionError.value = fail(e); await refresh(); } } finally { if (g === generation) busy.value = false; }
}
async function commandAccepted() {
  emit('taskChanged');
  if (refreshing.value) refreshAfterWrite = true;
  else await refresh();
}
function changed() { emit('taskChanged'); if (Date.now() - lastEventRefresh > 1000) { lastEventRefresh = Date.now(); void refresh(); } }
async function copyID() { try { await copyText(props.taskId); notice.value = '任务 ID 已复制'; } catch { actionError.value = '复制失败'; } }
function visible() { if (!document.hidden) void refresh(); }
watch(() => [props.workspaceId, props.taskId], () => { busy.value = false; void load(); }, { immediate: true });
watch(() => props.initialTurnId, v => { turn.value = v || ''; });
watch(turn, () => { historicalRevision++; historicalReport.value = null; void refreshHistoricalReport(); });
onMounted(() => { timer = setInterval(() => { void refresh(); }, 5000); document.addEventListener('visibilitychange', visible); });
onUnmounted(() => { generation++; controller.abort(); if (timer) clearInterval(timer); document.removeEventListener('visibilitychange', visible); });
</script>
<template>
  <section class="task-detail-panel" :class="[presentation, {'task-drawer-body':taskCenter}]" aria-label="任务详情">
    <header><span class="eyebrow">{{ taskCenter ? "任务执行记录" : "任务详情" }}</span><NButton size="small" @click="emit('close')">关闭任务详情</NButton></header>
    <p v-if="error" class="error" role="alert">{{ error }} <NButton size="small" @click="load">重新加载</NButton></p><NSkeleton v-if="loading" text :repeat="4" />
    <template v-if="detail">
      <div class="task-summary" :class="{'t-summary-card': taskCenter}"><h2>{{ detail.title || '未命名任务' }}</h2><p>{{ detail.agent.name }} · {{ detail.task_space_id ? '任务空间中的独立任务' : detail.source.kind === 'chat_delegation' ? '由主对话触发' : '来源未指定' }}</p><div class="facts ts-row"><span>{{ executionLabel }}</span><span>{{ sessionStatus(detail.lifecycle) }}</span><span v-if="detail.latest_turn">最近：{{ sessionStatus(detail.latest_turn.status) }}</span><span v-if="duration">本轮用时 {{ duration }}</span></div>
        <p v-if="detail.execution.queued_input_count">{{ detail.execution.queued_input_count }} 条输入排队中</p><p v-if="detail.execution.background_process_count === null || detail.execution.background_process_count">{{ detail.execution.background_process_count === null ? '后台活动待核对' : `${detail.execution.background_process_count} 个后台进程仍在运行` }}</p><p v-if="detail.latest_turn?.has_unknown_tool_results" class="warning">存在结果未知的工具调用，请查看执行过程。</p>
        <details><summary>任务要求与标识</summary><SessionMarkdown :text="detail.initial_input?.text || '尚无任务输入'" /><p class="muted">任务 {{ detail.task_id }}<br>创建于 {{ new Date(detail.created_at).toLocaleString('zh-CN') }}</p><NButton size="tiny" @click="copyID">复制任务 ID</NButton></details>
        <div class="meta-actions"><NButton v-if="detail.source.chat_session_id && detail.conversation_id" size="tiny" @click="emit('openSourceConversation', detail.agent.id, detail.conversation_id)">返回来源对话</NButton><NButton v-if="recordsEditable" size="tiny" :disabled="!canRename || busy" @click="toggleRename">修改标题</NButton><NButton size="tiny" :disabled="!canArchive || busy" @click="action('archive')">归档任务</NButton></div><div v-if="renameOpen && recordsEditable" class="rename"><NInput v-model:value="title" maxlength="200" placeholder="任务标题" /><NButton size="small" :disabled="!canRename || !title.trim() || busy" @click="action('rename')">保存标题</NButton></div>
      </div>
      <details v-if="taskCenter && detail.latest_result?.text" class="result-preview"><summary>最近一轮结果{{detail.latest_result.truncated?'（摘要）':''}}</summary><SessionMarkdown :text="detail.latest_result.text"/></details>
      <p v-if="actionError" class="error" role="alert">{{ actionError }}</p><p v-if="notice" class="muted" role="status">{{ notice }}</p>
      <p v-if="planned" role="status">{{ detail.planning_state === 'planned' ? '仅保存了待办，没有执行输入或模型运行。请返回来源主对话查询、补充、启动或取消待办；这里不能直接发送执行输入。' : '此待办在启动前已取消，没有执行结果，也不能继续或恢复。' }}</p>
      <nav aria-label="任务详情视图"><button v-for="t in [{id:'process',label:'执行过程'},{id:'inputs',label:'输入记录'},{id:'reports',label:'主会话回报'}]" :key="t.id" :aria-current="tab === t.id ? 'page' : undefined" @click="tab = t.id">{{ t.label }}</button><button v-if="detail.work_item_id" :aria-current="tab==='work' ? 'page' : undefined" @click="tab='work'">事项时间线</button></nav>
      <div v-if="tab==='work' && detail.work_item_id" class="records"><NButton v-if="taskCenter" size="tiny" @click="emit('openWork', detail.work_item_id)">前往事项看板</NButton><WorkItemTimeline :workspace="workspaceId" :item-id="detail.work_item_id"/></div>
      <div v-show="tab === 'process'" class="process"><div class="turn-select"><NSelect v-model:value="turn" :options="turnOptions" aria-label="选择执行轮次" /><NButton v-if="turnCursor" size="tiny" @click="more('turns')">加载更多轮次</NButton></div><TaskSupplementReceipts :report="selectedReport" />
        <p v-if="taskCenter && !turn && detail.actions.supplement?.reason === 'system_managed_input'" class="muted">此任务的输入由系统派发。</p>
        <ManagerSessionPanel v-if="!planned" :key="detail.session_id" :workspace="workspaceId" :session-id="detail.session_id" :title="detail.agent.name" :turn-id="turn" :read-only="!!turn || (taskCenter && !canSupplement)" :task-center="taskCenter"
          :stop-disabled="!detail.actions.stop.allowed" task embedded @changed="changed" @sent="commandAccepted" @stopped="commandAccepted" /></div>
      <div v-if="tab === 'inputs'" class="records"><p v-if="!inputs.length" class="muted">尚无输入记录</p><article v-for="i in inputs" :key="i.id"><strong>输入 {{ i.seq }} · {{ sessionStatus(i.status) }}</strong><small>{{ i.confirmation_request_id ? '确认答复' : i.delivery_mode === 'steer' ? '引导当前执行' : '下一轮处理' }}</small><SessionMarkdown :text="i.text" /><p v-if="i.rejection_reason || i.cancel_reason" class="muted">{{ i.rejection_reason || i.cancel_reason }}</p><NButton v-if="canWithdraw(i)" size="tiny" :disabled="busy" @click="action('withdraw', i.id)">撤回此输入</NButton><NButton v-if="i.turn_id" size="tiny" @click="turn = i.turn_id; tab = 'process'">查看关联轮次</NButton></article><NButton v-if="inputCursor" :disabled="busy" @click="more('inputs')">加载更多输入</NButton></div>
      <div v-if="tab === 'reports'" class="records"><p v-if="detail.task_space_id" class="muted">此任务没有来源对话，不会向主会话回报。</p><p v-else class="muted">回报受理、主会话处理和回复分别确认。</p><p v-if="!reports.length" class="muted">尚无已结束轮次的回报</p><article v-for="r in reports" :key="r.source_input_id"><strong>{{ r.source_turn_id ? '轮次回报' : '未执行输入回报' }} · {{ sessionStatus(r.outcome) }}</strong><p>{{ reportStatus[r.status] || r.status }}</p><p v-if="r.reason" class="warning">{{ r.reason }}</p><p v-if="r.target" class="muted">主会话输入：{{ sessionStatus(r.target.input_status) }}<br>上下文：{{ r.target.context_state === 'committed' ? '已确认接收' : r.target.context_state || '尚无接收证据' }}<br>主会话轮次：{{ sessionStatus(r.target.turn_status || undefined) }}</p><TaskSupplementReceipts :report="r" /><NButton v-if="r.source_turn_id" size="tiny" @click="turn = r.source_turn_id; tab = 'process'">查看来源轮次</NButton></article><NButton v-if="reportCursor" :disabled="busy" @click="more('reports')">加载更多回报</NButton></div>
    </template>
  </section>
</template>
<style scoped>
.task-detail-panel{display:flex;flex-direction:column;height:100%;min-height:420px;min-width:0;background:var(--id-bg-surface);color:var(--id-text-primary)}header{display:flex;justify-content:space-between;align-items:center;padding:14px 18px;border-bottom:1px solid var(--id-border-subtle)}.eyebrow,.muted,small{font-size:12px;color:var(--id-text-muted)}.task-summary{padding:18px;max-height:36%;overflow:auto}.task-summary h2{font-size:18px;margin:0 0 8px}.task-summary p{font-size:12px;color:var(--id-text-secondary);overflow-wrap:anywhere}.facts,.meta-actions,.rename{display:flex;gap:8px;flex-wrap:wrap;margin:12px 0}.facts span{padding:4px 7px;background:var(--id-bg-subtle);border-radius:5px;font-size:11px}.rename .n-input{flex:1;min-width:150px}summary{cursor:pointer;font-size:12px;margin:10px 0}nav{display:flex;border-block:1px solid var(--id-border-subtle);padding:0 18px}nav button{background:none;border:0;border-bottom:2px solid transparent;padding:12px 8px;cursor:pointer;color:var(--id-text-secondary);font:inherit;font-size:12px}nav button[aria-current=page]{border-color:var(--id-accent-primary);color:var(--id-accent-primary)}.process{display:flex;flex-direction:column;flex:1;min-height:0}.turn-select{padding:10px 18px;display:flex;gap:8px}.process :deep(.session-panel){border:0;border-radius:0;min-height:260px}.process :deep(.messages){padding:16px}.records{flex:1;overflow:auto;padding:18px}.records article{border-bottom:1px solid var(--id-border-subtle);padding:14px 0;overflow-wrap:anywhere}.records strong{font-size:13px}.records small{display:block;margin:6px 0}.error,.warning{padding:8px 18px;font-size:12px}.error{color:var(--id-status-danger)}.warning{color:var(--id-status-warning)}.task-detail-panel>p{margin:5px 0;padding-inline:18px}button:focus-visible,summary:focus-visible{outline:2px solid var(--id-accent-primary);outline-offset:2px}
.t-summary-card {
  background: var(--id-bg-subtle);
  border: 1px solid var(--id-border-subtle);
  border-radius: var(--id-radius-lg);
  padding: 12px 14px;
  display: flex;
  flex-direction: column;
  gap: 8px;
  font-size: 12px;
  color: var(--id-text-secondary);
}
.t-summary-card strong {
  color: var(--id-text-primary);
  font-weight: 600;
}
.ts-row {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 6px 18px;
}

.task-drawer-body .eyebrow{font-size:16px;font-weight:600;color:var(--id-text-primary)}
.task-drawer-body .task-summary{margin:14px 16px 8px;flex-shrink:0}
.task-drawer-body .task-summary h2{font-size:15px}
.task-drawer-body .task-summary p,.task-drawer-body .facts,.task-drawer-body .meta-actions:empty{margin:0}
.result-preview{margin:0 16px;max-height:25%;overflow:auto}
.task-drawer-body nav{flex-wrap:wrap}
</style>

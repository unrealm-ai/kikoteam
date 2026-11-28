<script setup lang="ts">
import ConversationControlIcon from './ConversationControlIcon.vue';
import { computed, nextTick, onUnmounted, ref, toRef, watch } from 'vue';
import { NButton, NInput, NSkeleton } from 'naive-ui';
import { managerApi, type SessionEvent } from '../api/manager';
import type { PublicTaskDetailFlow } from '../composables/usePublicTaskDetail';
import type { TaskArtifact } from '../api/public-task';
import { useSessionEvents } from '../composables/useSessionEvents';
import {downloadProjectFile,getChatAttachment,type ProjectAttachment,type SessionAttachment} from '../api/project-files';
import SessionMarkdown from './SessionMarkdown.vue';
import SessionToolCall from './SessionToolCall.vue';
import TaskStepProgress from './TaskStepProgress.vue';
import type { TaskRecord, ReportedProgress } from '../api/tasks';
import SessionTaskReport from './SessionTaskReport.vue';
import D2EmployeeAvatar from './D2EmployeeAvatar.vue';
import { clockTime, eventText, presentationRows, sessionStatus as status, taskReport, toolTaskTarget, type TaskNavigationTarget } from '../utils/session-presentation';
import { classifyArtifact, formatArtifactSize } from '../utils/artifact-preview';
const props = withDefaults(defineProps<{ workspace: string; sessionId: string; attachmentPaths?:string[]; attachmentIds?:string[]; attachmentPending?:boolean; attachmentLookup?:boolean; uploadedAttachments?:Record<string, ProjectAttachment>; title: string; subtitle?: string | undefined; agentAvatar?: string | undefined; assistantAvatar?: boolean | undefined; assistantAvatarBusy?: boolean | undefined; agentId?: string | undefined; task?: boolean; taskCenter?: boolean; stopDisabled?: boolean; readOnly?: boolean; tasks?: TaskRecord[]; initialProgress?: ReportedProgress | null | undefined; turnId?: string | undefined; embedded?: boolean; publicDetail?: PublicTaskDetailFlow | undefined; artifacts?: TaskArtifact[] | undefined; initialInputText?: string | undefined; initialInputTime?: string | undefined; selectedArtifactId?: string | undefined }>(), { assistantAvatarBusy: undefined });
const emit = defineEmits<{ taskTargets: [targets: TaskNavigationTarget[]]; changed: []; sent: []; stopped: []; sending:[busy:boolean]; report:[]; close: []; openTask: [target: TaskNavigationTarget, turn?: string]; selectArtifact: [file: TaskArtifact]; downloadArtifact: [file: TaskArtifact] }>();
const flow = useSessionEvents(toRef(props, 'workspace'), toRef(props, 'sessionId'), () => emit('changed'), toRef(props, 'turnId'));
const { session } = flow;
const loading = computed(() => props.publicDetail?.loading.value ?? flow.loading.value);
const loadingOlder = computed(() => props.publicDetail?.loadingOlder.value ?? flow.loadingOlder.value);
const olderCursor = computed(() => props.publicDetail ? props.publicDetail.olderCursor.value : flow.olderCursor.value);
const error = computed(() => props.publicDetail?.executionError.value ?? flow.error.value);
const connection = computed(() => props.publicDetail?.connection.value ?? flow.connection.value);
const shared = computed(() => props.publicDetail?.task.value);
const rows = computed(() => props.publicDetail?.rows.value ?? flow.rows.value);
const currentProgress = computed(() => {
  const execution = session.value?.execution;
  if (!props.task || props.publicDetail || execution?.state !== 'running' || execution.required_actions?.length) return null;
  const initial = props.initialProgress?.turn_id === execution.active_turn_id ? props.initialProgress : null;
  // The HTTP snapshot can be newer than the first history page, or older than a live event.
  for (let i = flow.events.value.length - 1; i >= 0; i--) {
    const event = flow.events.value[i]!;
    if (initial && BigInt(event.seq) <= BigInt(initial.seq)) break;
    if (event.type === 'task.progress_updated' && event.turn_id === execution.active_turn_id) return event as unknown as ReportedProgress;
  }
  return initial;
});
const scroll = ref<HTMLElement | null>(null);
const stickToBottom = ref(true);
const initialBottomPending = ref(true);
type HistoryAnchor = { key: string; offset: number };
let prepend: { anchor: HistoryAnchor | undefined } | undefined;
const messageKey = (row: SessionEvent) => row.action ? `action:${row.action.request_id}`
  : row.message_id ? `message:${row.message_id}`
  : row.tool_use_id ? `tool:${row.tool_use_id}`
  : row.type === 'turn.updated' && row.turn_id ? `turn:${row.turn_id}`
  : `event:${row.seq}`;

function visibleHistoryAnchor(el: HTMLElement | null): HistoryAnchor | undefined {
  if (!el || !el.clientHeight) return;
  const viewport = el.getBoundingClientRect();
  for (const node of el.querySelectorAll<HTMLElement>('[data-history-key]')) {
    const bounds = node.getBoundingClientRect();
    if (bounds.bottom > viewport.top && bounds.top < viewport.bottom && node.dataset.historyKey) {
      return { key: node.dataset.historyKey, offset: bounds.top - viewport.top };
    }
  }
}
let lastScrollTop = 0;

function resetScrollState() {
  stickToBottom.value = true;
  initialBottomPending.value = true;
  prepend = undefined;
  lastScrollTop = 0;
}

function reload() {
  resetScrollState();
  return props.publicDetail ? props.publicDetail.load() : flow.reload();
}
const writable = computed(() => !props.publicDetail || shared.value?.permission === 'write');
const canCompose = computed(() => writable.value && !props.readOnly && (props.publicDetail ? !!(shared.value?.can_append || shared.value?.can_continue) : !!session.value?.accepts_input));
const continuing = computed(() => Boolean(shared.value?.can_continue));
const held = computed(() => props.publicDetail
  ? ['waiting_for_action', 'waiting_for_confirmation', 'waiting_for_handoff'].includes(shared.value?.status || '')
  : !!(props.task && session.value?.execution.required_actions?.some(action => ['human_confirmation', 'work_item'].includes(action.type))));

const text = ref(''), submitting = ref(false), actionError = ref(''), notice = ref(''), controlBusy = ref(false);
function prefillTrial(){if(typeof sessionStorage==='undefined'||typeof location==='undefined')return;const agent=new URLSearchParams(location.hash.slice(1)).get('emp');if(!agent)return;const key='kiko:template-trial:'+props.workspace+':'+agent;const draft=sessionStorage.getItem(key);if(draft){text.value=draft;sessionStorage.removeItem(key)}}
if (!props.taskCenter && !props.publicDetail) prefillTrial();
const stopLabel = computed(() => props.task ? '停止此任务' : '停止主对话');
const downloadError = ref('');
const artifactBusy = ref('');
const effectiveArtifacts = computed(() => props.artifacts?.length ? props.artifacts : shared.value?.artifacts || []);
const hasUserMessage = computed(() => messages.value.some(({ event }) => isUser(event)));
async function triggerArtifactDownload(file: TaskArtifact) {
  if (!props.publicDetail) {
    emit('downloadArtifact', file);
    return;
  }
  if (artifactBusy.value) return;
  artifactBusy.value = file.id; downloadError.value = '';
  try { await props.publicDetail.download(file); }
  catch { downloadError.value = '文件不可用，请重新加载后重试。'; }
  finally { artifactBusy.value = ''; }
}
const refreshedAttachments = ref<Record<string, SessionAttachment>>({});
const readingAttachments = ref(new Set<string>());
let downloads = new AbortController();
watch(() => [props.workspace, props.sessionId], () => {
  resetScrollState();
  downloads.abort(); downloads = new AbortController(); downloadError.value = '';
  refreshedAttachments.value = {}; readingAttachments.value = new Set();
});
onUnmounted(() => downloads.abort());
function liveAttachment(file: SessionAttachment): SessionAttachment {
  if (!file.id || file.status === 'failed') return file;
  const uploaded = props.uploadedAttachments?.[file.id];
  return uploaded ? { ...uploaded, id:file.id, status:'ready' } : refreshedAttachments.value[file.id] ?? file;
}
async function downloadAttachment(file: SessionAttachment) {
  const conversation = session.value?.conversation_id, signal = downloads.signal;
  if (!conversation || file.id && readingAttachments.value.has(file.id)) return;
  downloadError.value = '';
  try {
    let current = liveAttachment(file);
    if (!current.path && file.id && props.attachmentLookup && current.status !== 'failed') {
      readingAttachments.value.add(file.id);
      const state = await getChatAttachment(props.workspace, conversation, file.id, signal);
      if (signal.aborted) return;
      refreshedAttachments.value[file.id] = state;
      current = state;
    }
    if (current.path && (!current.status || current.status === 'ready')) await downloadProjectFile(props.workspace, conversation, current.path, signal);
  }
  catch (e) { if (!signal.aborted) downloadError.value = file.name + '：' + (e instanceof Error ? e.message : '文件不可用'); }
  finally { if (!signal.aborted && file.id) readingAttachments.value.delete(file.id); }
}
watch(()=>rows.value.filter(e=>e.source_kind==='task_report').at(-1)?.seq,seq=>{if(seq)emit('report');});
let pending: { paths:string[]; ids:string[]; text: string; key: string } | null = null;
const sendLabel = computed(() => continuing.value ? '提交并重新开始' : actionError.value && pending ? '重试发送' : '发送');
watch(() => props.sessionId, () => { text.value = ''; pending = null; actionError.value = ''; notice.value = ''; });
const messages = computed(() => {
  const pending = new Set(session.value?.execution.required_actions?.map(action => action.request_id));
  return presentationRows(rows.value).filter(({ event }) => !event.action || !pending.has(event.action.request_id));
});
watch(() => messages.value.flatMap(({event})=>{
  const target = event.name==='task_delegation_create_session' && toolTaskTarget(event);
  return target ? [target] : [];
}), targets=>{if (!props.task && !props.publicDetail) emit('taskTargets', targets)}, {immediate:true});
// A report refers to the turn that produced it. A later queued input must not
// inherit its result, even when both inputs belong to the same task.
const reportedResults = computed(() => {
  const results = new Map<string, string>();
  for (const row of rows.value) {
    const report = taskReport(row);
    if (report?.turnID && report.outcome === 'completed' && !report.unknown) {
      const text = report.result.trim();
      results.set(report.turnID, text.length > 160 ? text.slice(0, 160) + '…' : text);
    }
  }
  return results;
});
function linkedTask(event: SessionEvent) {
  const target = taskReport(event)?.task || toolTaskTarget(event);
  return target && props.tasks?.find(task => (target.kind === 'task' ? task.task_id : task.session_id) === target.id);
}
function isUser(event: SessionEvent) { return event.source_kind !== 'task_report' && event.source_kind !== 'work_item_amendment' && (event.type === 'user.message' || event.type === 'input.updated'); }
function onEnter(event: KeyboardEvent) { if (event.isComposing || event.keyCode === 229 || continuing.value) return; event.preventDefault(); void send(); }
async function send() {
  if (!canCompose.value || submitting.value || controlBusy.value || props.attachmentPending || (!text.value.trim() && !props.attachmentPaths?.length && !props.attachmentIds?.length)) return;
  if (props.publicDetail) { await sendShared(); return; }
  const w = props.workspace, id = props.sessionId;
  if (!pending || pending.text !== text.value || JSON.stringify(pending.paths)!==JSON.stringify(props.attachmentPaths||[]) || JSON.stringify(pending.ids)!==JSON.stringify(props.attachmentIds||[])) pending = { paths:[...(props.attachmentPaths||[])],ids:[...(props.attachmentIds||[])],text: text.value, key: crypto.randomUUID() };
  const request = pending; submitting.value = true; emit('sending',true); actionError.value = ''; notice.value = '';
  try { await managerApi.input(w, id, request.text, request.key, undefined, 'steer',request.paths,request.ids); if (id !== props.sessionId || w !== props.workspace) return; text.value = ''; pending = null; emit('sent');notice.value = '输入已受理'; await flow.refresh(); }
  catch (e) { if (id === props.sessionId && w === props.workspace) actionError.value = e instanceof Error ? e.message : '发送失败，可原样重试'; }
  finally { submitting.value = false;emit('sending',false); }
}
async function sendShared() {
  const adapter = props.publicDetail, snapshot = shared.value;
  if (!adapter || !snapshot) return;
  const restart = continuing.value, content = text.value.trim();
  if (!content || restart && snapshot.stop_version == null) return;
  submitting.value = true; actionError.value = ''; notice.value = '';
  try {
    const state = await adapter.mutate(restart ? 'continue' : 'append',
      restart ? { text: content, stop_version: snapshot.stop_version } : { text: content, delivery_mode: 'steer' });
    text.value = '';
    notice.value = restart ? '已受理，等待开始新一轮' : state === 'admitted' ? '当前轮已接收补充' : state === 'queued' ? '已记录，排队处理' : '补充已提交';
  } catch (e) { actionError.value = e instanceof Error ? e.message : '提交失败，可原样重试'; }
  finally { submitting.value = false; }
}

const answering = ref('');
async function confirm(requestID: string, confirmed: boolean) {
  const id = props.sessionId, w = props.workspace;
  if (answering.value || props.readOnly || props.taskCenter) return;
  answering.value = requestID;
  actionError.value = '';
  try {
    await managerApi.confirm(w, id, requestID, confirmed);
    if (id === props.sessionId && w === props.workspace) notice.value = '已回答';
  } catch (e) {
    if (id === props.sessionId && w === props.workspace) actionError.value = e instanceof Error ? e.message : '答复未提交';
  } finally {
    answering.value = '';
    if (id === props.sessionId && w === props.workspace) await flow.refresh();
  }
}
const stopUnavailable = computed(() => !writable.value || !!props.turnId || controlBusy.value || submitting.value || held.value || (props.publicDetail ? !shared.value?.can_stop : props.stopDisabled || !session.value || session.value.lifecycle === 'archived'));
const isActiveExecution = computed(() => ['running', 'queued', 'stopping'].includes(session.value?.execution.state || ''));
const assistantWorking = computed(() => props.assistantAvatarBusy === undefined ? undefined :
  props.assistantAvatarBusy || ['running', 'stopping'].includes(session.value?.execution.state || ''));
async function stop() {
  if (stopUnavailable.value) return;
  const id = props.sessionId, w = props.workspace; controlBusy.value = true; actionError.value = ''; notice.value = '';
  try {
    if (props.publicDetail) { await props.publicDetail.mutate('stop', {}); notice.value = '停止请求已受理，请等待状态确认'; return; }
    await managerApi.stop(w, id); if (id === props.sessionId && w === props.workspace) { notice.value = '停止请求已受理，请等待状态确认'; emit('stopped'); await flow.refresh(); } }
  catch (e) { if (id === props.sessionId && w === props.workspace) actionError.value = e instanceof Error ? e.message : '停止失败'; }
  finally { controlBusy.value = false; }
}
function onMessagesScroll() {
  const el = scroll.value;
  if (!el || loading.value || prepend || loadingOlder.value) return;
  initialBottomPending.value = false;
  const currentTop = el.scrollTop;
  const scrolledUp = currentTop < lastScrollTop;
  lastScrollTop = currentTop;
  const distanceFromBottom = el.scrollHeight - currentTop - el.clientHeight;
  stickToBottom.value = distanceFromBottom <= 80;
  if (currentTop <= 60 && olderCursor.value && el.scrollHeight > el.clientHeight && (scrolledUp || !stickToBottom.value)) {
    void older();
  }
}

async function older() {
  if (!olderCursor.value || loadingOlder.value || prepend) return;
  const el = scroll.value;
  const pending = { anchor: visibleHistoryAnchor(el) };
  prepend = pending;
  initialBottomPending.value = false;
  stickToBottom.value = false;
  try {
    await (props.publicDetail ? props.publicDetail.older() : flow.older());
    await nextTick();
    if (prepend !== pending || !el || scroll.value !== el) return;
    const anchor = pending.anchor;
    if (anchor && el.clientHeight) {
      const node = Array.from(el.querySelectorAll<HTMLElement>('[data-history-key]'))
        .find(item => item.dataset.historyKey === anchor.key);
      if (node) {
        // Adjust only the visible row's displacement. Live events below it
        // and any anchoring already applied by the browser are not prepends.
        el.scrollTop += node.getBoundingClientRect().top - el.getBoundingClientRect().top - anchor.offset;
      }
    }
    lastScrollTop = el.scrollTop;
  } finally {
    if (prepend === pending) prepend = undefined;
  }
}

watch(rows, async (nextRows, prevRows) => {
  if (prepend) {
    // Vue's pre-update watcher sees the old DOM. Capture the reader's
    // current position even if they kept scrolling during the request.
    prepend.anchor = visibleHistoryAnchor(scroll.value) ?? prepend.anchor;
    return;
  }
  if (loadingOlder.value) return;
  const elBefore = scroll.value;
  const firstLoad = initialBottomPending.value && nextRows.length > 0 && (!prevRows || prevRows.length === 0);
  const atBottomBefore = !elBefore || elBefore.scrollHeight === 0 || (elBefore.scrollHeight - elBefore.scrollTop - elBefore.clientHeight <= 80);
  const shouldFollow = firstLoad || (stickToBottom.value && atBottomBefore);
  await nextTick();
  const el = scroll.value;
  if (!el || !el.clientHeight || prepend || loadingOlder.value) return;
  if (shouldFollow && (firstLoad || stickToBottom.value)) {
    el.scrollTop = el.scrollHeight;
    lastScrollTop = el.scrollTop;
    stickToBottom.value = true;
    if (nextRows.length > 0) initialBottomPending.value = false;
  }
}, { immediate: true });
</script>
<template>
  <section class="session-panel" :aria-label="title">
    <header v-if="!embedded" class="unified-session-header">
      <slot name="header-prefix" />
      <D2EmployeeAvatar v-if="!task && agentId" :name="title" :preset="agentAvatar" :identity-key="agentId" :assistant="assistantAvatar" :assistant-busy="assistantWorking" size="sm" />
      <div class="session-heading">
        <div class="session-heading-top">
          <strong>{{ title }}</strong>
          <span class="connection muted"><i :class="{ connected: connection === '已连接' }" aria-hidden="true" />{{ connection || '正在连接' }} · {{ status(session?.execution.state) }}</span>
        </div>
        <span class="eyebrow">{{ subtitle || (task ? '任务执行过程' : '与你的 AI 员工协作') }}</span>
      </div>
      <div class="header-actions">
        <slot name="header-actions" />
        <NButton v-if="task" size="small" @click="emit('close')">关闭任务详情</NButton>
      </div>
    </header>
    <p v-if="session?.execution.block_reason" class="warning">{{ status(session.execution.block_reason) }}</p>
    <div v-if="error" class="error" role="alert">{{ error }} <NButton size="small" @click="reload">重新加载</NButton></div>
    <TaskStepProgress v-if="currentProgress" :progress="currentProgress" />
    <div class="session-content"><div ref="scroll" class="messages" @scroll="onMessagesScroll">
      <NSkeleton v-if="loading" text :repeat="4" />
      <NButton v-if="olderCursor" size="small" :loading="loadingOlder" @click="older">加载更早历史消息</NButton>

      <article v-if="task && initialInputText && !hasUserMessage && !turnId" class="event user initial-prompt">
        <div class="message-meta"><strong>{{ publicDetail ? '任务输入' : '你' }}</strong><time v-if="initialInputTime" :datetime="initialInputTime">{{ clockTime(initialInputTime) }}</time></div>
        <p class="body">{{ initialInputText }}</p>
      </article>
      <p v-if="!loading && !rows.length && !initialInputText" class="empty muted">{{ taskCenter || task ? '暂无执行记录。' : '暂无消息，发送工作需求开始协作。' }}</p>
      <article v-for="{ event: row, replies } in messages" :key="messageKey(row)" :data-history-key="messageKey(row)" :class="['event', { user: isUser(row), tool: !!row.tool_use_id, thinking: row.type === 'agent.thinking', report: row.source_kind === 'task_report', 'turn-event': row.type === 'turn.updated', 'error-event': row.type === 'session.error' }]">
        <template v-if="row.action"><strong>{{ row.action.title }}</strong><p class="body">{{ row.action.message }}</p></template>
        <SessionToolCall v-else-if="row.tool_use_id" :event="row" :task="linkedTask(row) || undefined" :results="reportedResults" :task-title="linkedTask(row)?.title || undefined" :can-open-task="!publicDetail && !!toolTaskTarget(row)" @open-task="emit('openTask', $event)" />
        <SessionTaskReport v-else-if="row.source_kind === 'task_report'" :workspace="publicDetail ? undefined : workspace" :event="row" :reply="replies" :title="linkedTask(row)?.title || undefined" :can-open-task="!publicDetail && !!taskReport(row)?.task" @open-task="emit('openTask', $event, taskReport(row)?.turnID)" />
        <template v-else-if="row.type === 'turn.updated'"><div class="turn-divider"><span class="muted">{{ status(row.status) }}</span></div><p v-if="row.error" class="error">{{ row.error.message }}</p></template>
        <template v-else>
          <div class="message-meta"><strong>{{ row.source_kind === 'work_item_amendment' ? '事项要求更新' : row.source_kind === 'work_item_notice' || row.source_kind === 'work_item_assignment' || row.source_kind === 'work_item_result' ? '事项交接' : isUser(row) ? (publicDetail ? '任务输入' : '你') : row.type === 'session.error' ? '会话提示' : title }}</strong><time v-if="row.occurred_at" :datetime="row.occurred_at">{{ clockTime(row.occurred_at) }}</time><span v-if="row.type === 'agent.thinking'" class="thinking-badge">思考过程</span></div>
          <p v-if="isUser(row)" class="body">{{ eventText(row) }}</p><details v-else-if="row.type === 'agent.thinking'" class="thinking-box"><summary>查看思考过程</summary><SessionMarkdown :text="eventText(row)" /></details><SessionMarkdown v-else :text="eventText(row)" />
          <template v-for="(file, index) in row.attachments" :key="file.id || file.path || index">
            <button v-if="!publicDetail && liveAttachment(file).status !== 'failed' && (liveAttachment(file).path || file.id && attachmentLookup)" class="attachment-link" :title="liveAttachment(file).path" :disabled="!!file.id && readingAttachments.has(file.id)" @click="downloadAttachment(file)">▤ {{ liveAttachment(file).name }} · {{ liveAttachment(file).path || liveAttachment(file).status === 'ready' ? (file.size_bytes/1024).toFixed(1) + ' KB ↓' : '准备中，点击查看状态' }}</button>
            <span v-else class="attachment-link" role="status">▤ {{ liveAttachment(file).name }} · {{ liveAttachment(file).status === 'failed' ? '附件不可用' : liveAttachment(file).status === 'ready' ? '已就绪' : '正在准备附件' }}</span>
          </template>
          <small v-if="isUser(row) || row.status && row.status !== 'completed'" class="msg-state-tag">{{ row.status === 'queued' && row.delivery_mode === 'steer' ? '待引导' : status(row.status) }}</small>
        </template>
      </article>
      <section v-if="effectiveArtifacts.length" class="event inline-artifacts" aria-label="本任务产物">
        <div class="inline-artifacts-head"><span>任务产物 ({{ effectiveArtifacts.length }})</span><small>点击在右侧画布预览或下载</small></div>
        <div class="inline-artifacts-grid">
          <div v-for="file in effectiveArtifacts" :key="file.id" :class="['artifact-surface-card', { selected: selectedArtifactId === file.id }]">
            <button type="button" class="artifact-card-main" @click="emit('selectArtifact', file)">
              <span class="artifact-badge" :class="classifyArtifact(file).kind">{{ classifyArtifact(file).badge }}</span>
              <span class="artifact-info">
                <strong>{{ file.name }}</strong>
                <small>{{ classifyArtifact(file).label }}<template v-if="file.size != null"> · {{ formatArtifactSize(file.size) }}</template></small>
              </span>
              <span class="artifact-preview-cue">{{ classifyArtifact(file).previewable ? '预览 →' : '仅下载' }}</span>
            </button>
            <NButton size="tiny" secondary :disabled="Boolean(artifactBusy)" @click="triggerArtifactDownload(file)">下载</NButton>
          </div>
        </div>
      </section>
    </div></div>
    <p v-if="downloadError" class="error" role="alert">{{ downloadError }}</p>
    <footer v-if="!turnId && writable">
      <section v-for="action in session?.execution.required_actions" :key="action.request_id" class="confirmation">
        <strong>{{ action.title }}</strong><p>{{ action.message }}</p>
        <div v-if="!taskCenter && action.type === 'confirmation'" class="actions"><NButton size="small" :disabled="!!answering || readOnly || !session?.accepts_input" @click="confirm(action.request_id, false)">否</NButton><NButton size="small" type="primary" :disabled="!!answering || readOnly || !session?.accepts_input" @click="confirm(action.request_id, true)">是</NButton></div>
      </section>
      <p v-if="actionError" class="error" role="alert">{{ actionError }}</p><p v-if="notice" class="muted" role="status">{{ notice }}</p>
      <p v-if="session && !session.accepts_input" class="warning">{{ session.lifecycle === 'archived' ? '此会话已归档，历史仍可查看，不能继续发送。' : `暂不能输入：${session.input_block_reason || session.lifecycle}` }}</p>
      <p v-if="held" class="warning">当前任务在等待确认或交接。补充会先排队，暂不能停止。</p>
      <div class="composer-card" :class="{ disabled: !canCompose, 'is-active': isActiveExecution }">
        <NInput v-model:value="text" type="textarea" :autosize="{ minRows: 2, maxRows: 5 }" :disabled="!canCompose || submitting || controlBusy" :placeholder="continuing ? '填写新一轮的要求' : task ? '给此任务补充要求，按顺序接续执行...' : '发送消息，任务执行期间仍可继续对话'" @keydown.enter.exact="onEnter" />
        <div class="actions">
          <div v-if="$slots.attachments" class="composer-attachments"><slot name="attachments" /></div>
          <div class="composer-right-controls">
            <span class="composer-hint">Enter 发送 · Shift + Enter 换行</span>
            <NButton v-if="!publicDetail || shared?.can_stop" size="small" circle :secondary="!isActiveExecution" :type="isActiveExecution ? 'error' : 'default'" class="stop-exec-btn" :class="{ quiet: !isActiveExecution }" :loading="controlBusy" :disabled="stopUnavailable" :title="stopLabel" :aria-label="stopLabel" @click="stop">
              <template #icon><ConversationControlIcon kind="stop" /></template>
            </NButton>
            <NButton size="small" type="primary" circle class="send-exec-btn" :loading="submitting" :disabled="attachmentPending || (!text.trim() && !attachmentPaths?.length && !attachmentIds?.length) || !canCompose || submitting || controlBusy" :title="sendLabel" :aria-label="sendLabel" @click="send">
              <template #icon><ConversationControlIcon kind="send" /></template>
            </NButton>
          </div>
        </div>
      </div>
    </footer>
  </section>
</template>
<style scoped>
.attachment-link{border:1px solid var(--id-border-subtle);background:var(--id-bg-surface);color:inherit;padding:6px 10px;border-radius:var(--id-radius-md);cursor:pointer;margin:4px;font-size:12px;transition:border-color var(--id-transition-fast)}
.attachment-link:hover{border-color:var(--id-border-strong)}
.confirmation{padding:12px 0;border-bottom:1px solid var(--id-border-subtle);overflow-wrap:anywhere}
.confirmation p{white-space:pre-wrap}
.session-panel{min-width:0;flex:1;display:flex;flex-direction:column;border:1px solid var(--id-border-subtle);border-radius:var(--id-radius-lg);background:var(--id-bg-surface);overflow:hidden;height:100%;min-height:420px}
.unified-session-header{display:flex;align-items:center;gap:12px;min-height:58px;padding:10px 22px;border-bottom:1px solid var(--id-border-hairline);background:var(--id-bg-surface);flex-wrap:wrap}
.session-heading{flex:1;min-width:120px;display:flex;flex-direction:column;gap:2px}
.session-heading-top{display:flex;align-items:center;gap:8px;flex-wrap:wrap}
.session-heading strong{font-size:14.5px;font-weight:600;color:var(--id-text-primary)}
.header-actions{display:flex;align-items:center;gap:8px;flex-wrap:wrap}
.eyebrow{font-size:12px;color:var(--id-text-muted);overflow:hidden;text-overflow:ellipsis;white-space:nowrap}
.connection{display:inline-flex;align-items:center;gap:5px;padding:1px 8px;border-radius:var(--id-radius-pill);background:var(--id-bg-subtle);border:1px solid var(--id-border-subtle);font-size:11px;color:var(--id-text-muted)}
.connection:has(i.connected){background:#ECFDF5;border-color:#A7F3D0;color:#047857;font-weight:500}
.connection i{width:6px;height:6px;border-radius:50%;background:var(--id-text-muted)}
.connection i.connected{background:var(--id-status-success-dot)}
.messages{flex:1;min-height:0;overflow:auto;padding:20px clamp(18px,3vw,36px) 12px;background:var(--id-bg-surface);display:flex;flex-direction:column;gap:14px;scrollbar-gutter:stable}
.event{width:100%;min-width:0;max-width:740px;align-self:center;font-size:13.5px;overflow-wrap:anywhere}
.event.tool,.event.thinking{padding-left:14px;border-left:2px solid var(--id-border-subtle)}
.event.user{align-self:flex-end;max-width:min(82%,640px);width:fit-content;padding:10px 14px;border-radius:14px 14px 4px 14px;background:#F4F3EE;border:1px solid var(--id-border-subtle);color:var(--id-text-primary)}
.message-meta{display:flex;align-items:center;gap:8px;font-size:11px;color:var(--id-text-muted);margin-bottom:6px}
.message-meta strong{font-weight:600;color:var(--id-text-secondary)}
.message-meta time{font-size:11px;font-family:var(--id-font-mono)}
.body{white-space:pre-wrap;word-break:break-word;margin:0;line-height:1.75}
.event small{display:block;margin-top:6px}
.turn-event{padding-top:4px;font-size:11px}
.turn-divider{display:flex;align-items:center;gap:10px;color:var(--id-text-muted)}
.turn-divider::before,.turn-divider::after{content:"";flex:1;height:1px;background:var(--id-border-subtle)}
.turn-event .muted{font-size:11px}
.error-event{border-left:2px solid var(--id-status-danger);padding-left:12px}
.thinking-box{padding:8px 12px;border-radius:var(--id-radius-md);background:var(--id-bg-surface);border:1px solid var(--id-border-subtle)}
.thinking-box summary{color:var(--id-text-secondary);font-size:12px;font-weight:500;cursor:pointer}
.thinking-box[open] summary{margin-bottom:8px}
.thinking-badge{padding:1px 6px;border-radius:var(--id-radius-pill);background:var(--id-bg-muted);border:1px solid var(--id-border-subtle);font-size:10px}
.msg-state-tag{width:fit-content;padding:1px 6px;border-radius:var(--id-radius-sm);background:var(--id-bg-surface);border:1px solid var(--id-border-subtle)}
.inline-artifacts{margin-top:4px;padding:12px 14px;border-radius:12px;background:var(--id-bg-surface);border:1px solid var(--id-border-subtle)}
.inline-artifacts-head{display:flex;align-items:center;justify-content:space-between;gap:8px;margin-bottom:10px;font-size:12px;font-weight:600;color:var(--id-text-primary)}
.inline-artifacts-head small{font-weight:400;margin-top:0}
.inline-artifacts-grid{display:flex;flex-direction:column;gap:8px}
.artifact-surface-card{display:flex;align-items:center;gap:8px;padding:8px 10px;border-radius:10px;border:1px solid var(--id-border-subtle);background:var(--id-bg-subtle);transition:border-color var(--id-transition-fast),background-color var(--id-transition-fast)}
.artifact-surface-card:hover,.artifact-surface-card.selected{border-color:var(--id-accent-primary);background:var(--id-accent-primary-soft)}
.artifact-card-main{flex:1;min-width:0;display:flex;align-items:center;gap:10px;background:transparent;border:0;padding:0;text-align:left;cursor:pointer;color:inherit;font:inherit}
.artifact-badge{flex-shrink:0;min-width:36px;text-align:center;padding:3px 6px;border-radius:6px;font-family:var(--id-font-mono);font-size:10px;font-weight:700;background:var(--id-bg-muted);color:var(--id-text-secondary);border:1px solid var(--id-border-subtle)}
.artifact-badge.markdown{background:var(--id-accent-primary-soft);color:var(--id-accent-primary);border-color:var(--id-accent-primary-border)}
.artifact-badge.csv{background:var(--id-status-success-soft);color:var(--id-status-success);border-color:var(--id-status-success-border)}
.artifact-badge.code{background:var(--id-status-info-soft);color:var(--id-status-info);border-color:var(--id-status-info-border)}
.artifact-badge.image{background:var(--id-status-warning-soft);color:var(--id-status-warning);border-color:var(--id-status-warning-border)}
.artifact-info{flex:1;min-width:0;display:flex;flex-direction:column;gap:2px}
.artifact-info strong{font-size:12px;font-weight:600;color:var(--id-text-primary);overflow:hidden;text-overflow:ellipsis;white-space:nowrap}
.artifact-info small{margin-top:0;font-size:11px}
.artifact-preview-cue{flex-shrink:0;font-size:11px;font-weight:500;color:var(--id-accent-primary)}
small,.muted{font-size:12px;color:var(--id-text-muted)}.empty{margin:auto;text-align:center}.error,.warning{padding:8px 12px;font-size:13px}.error{color:var(--id-status-danger)}.warning{color:var(--id-status-warning)}
.session-panel>footer{padding:8px clamp(18px,3vw,36px) 16px;background:var(--id-bg-surface);border-top:0}
.composer-card{width:100%;max-width:740px;margin:0 auto;background:var(--id-bg-subtle);border:1px solid #DFDDD6;border-radius:14px;padding:10px 14px 9px;box-shadow:0 2px 10px rgba(28,25,23,0.03);transition:border-color var(--id-transition-fast),box-shadow var(--id-transition-fast)}
.composer-card:focus-within{border-color:var(--id-border-strong);box-shadow:0 3px 14px rgba(28,25,23,0.06)}
.composer-card.disabled{opacity:0.78;background:var(--id-bg-muted)}
.composer-attachments{flex:1 1 auto;min-width:0}
.composer-card :deep(.n-input){--n-border:none !important;--n-border-hover:none !important;--n-border-focus:none !important;--n-box-shadow-focus:none !important;--n-color:transparent !important;--n-color-focus:transparent !important;--n-color-disabled:transparent !important}
.composer-card :deep(.n-input__textarea-el){padding:2px 2px}
.actions{display:flex;align-items:center;justify-content:space-between;gap:10px;margin-top:6px;padding-top:6px;flex-wrap:wrap}
.composer-right-controls{margin-left:auto;display:flex;align-items:center;gap:8px;flex-shrink:0}
.composer-hint{color:var(--id-text-placeholder);font-size:11px}
.stop-exec-btn.quiet{opacity:0.75}
.send-exec-btn{--n-color:#1C1917 !important;--n-color-hover:#292524 !important;--n-color-pressed:#0C0A09 !important;--n-color-disabled:#A8A29E !important;--n-border:1px solid #1C1917 !important;--n-border-hover:1px solid #292524 !important;--n-border-pressed:1px solid #0C0A09 !important;--n-border-disabled:1px solid #A8A29E !important}
@media(max-width:1200px){.composer-hint{display:none}}
@media(max-width:640px){.messages{padding:16px 12px}.unified-session-header,.session-panel>footer{padding:12px}.connection{font-size:10px}.actions{gap:6px}.event.user{max-width:94%}}
.session-content{display:flex;flex:1;min-height:0;min-width:0}
</style>

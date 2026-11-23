<script setup lang="ts">
import { computed, nextTick, onUnmounted, ref, toRef, watch } from 'vue';
import { NButton, NInput, NSelect, NSkeleton } from 'naive-ui';
import { managerApi, type ManagerSession, type SessionEvent } from '../api/manager';
import { useSessionEvents } from '../composables/useSessionEvents';
import {downloadProjectFile,getChatAttachment,type ProjectAttachment,type SessionAttachment} from '../api/project-files';
import SessionMarkdown from './SessionMarkdown.vue';
import SessionToolCall from './SessionToolCall.vue';
import SessionTaskReport from './SessionTaskReport.vue';
import { clockTime, eventText, presentationRows, sessionStatus as status, taskReport, taskSessionID } from '../utils/session-presentation';
const props = defineProps<{ workspace: string; sessionId: string; attachmentPaths?:string[]; attachmentIds?:string[]; attachmentPending?:boolean; attachmentLookup?:boolean; uploadedAttachments?:Record<string, ProjectAttachment>; title: string; task?: boolean; taskCenter?: boolean; stopDisabled?: boolean; readOnly?: boolean; tasks?: ManagerSession[]; turnId?: string | undefined; embedded?: boolean }>();
const emit = defineEmits<{ changed: []; sent: []; stopped: []; sending:[busy:boolean]; report:[]; close: []; openTask: [id: string, turn?: string] }>();
const flow = useSessionEvents(toRef(props, 'workspace'), toRef(props, 'sessionId'), () => emit('changed'), toRef(props, 'turnId'));
const { rows, session, loading, loadingOlder, error, connection, olderCursor } = flow;
const text = ref(''), submitting = ref(false), actionError = ref(''), notice = ref(''), controlBusy = ref(false);
function prefillTrial(){if(typeof sessionStorage==='undefined'||typeof location==='undefined')return;const agent=new URLSearchParams(location.hash.slice(1)).get('emp');if(!agent)return;const key='kiko:template-trial:'+props.workspace+':'+agent;const draft=sessionStorage.getItem(key);if(draft){text.value=draft;sessionStorage.removeItem(key)}}
if (!props.taskCenter) prefillTrial();
const deliveryMode = ref<'steer' | 'queue'>('steer');
const deliveryOptions = [{ label: '引导当前执行', value: 'steer' }, { label: '排队到下一轮', value: 'queue' }];
const scroll = ref<HTMLElement | null>(null);
const downloadError = ref('');
const refreshedAttachments = ref<Record<string, SessionAttachment>>({});
const readingAttachments = ref(new Set<string>());
let downloads = new AbortController();
watch(() => [props.workspace, props.sessionId], () => {
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
let pending: { paths:string[]; ids:string[]; text: string; key: string; mode: 'steer' | 'queue' } | null = null;
watch(() => props.sessionId, () => { text.value = ''; pending = null; actionError.value = ''; notice.value = ''; });
const messages = computed(() => {
  const pending = new Set(session.value?.execution.required_actions?.map(action => action.request_id));
  return presentationRows(rows.value).filter(({ event }) => !event.action || !pending.has(event.action.request_id));
});
function linkedTask(event: SessionEvent) { const id = taskReport(event)?.sessionID || taskSessionID(event); return props.tasks?.find(task => task.id === id); }
function isUser(event: SessionEvent) { return event.source_kind !== 'task_report' && event.source_kind !== 'work_item_amendment' && (event.type === 'user.message' || event.type === 'input.updated'); }
function onEnter(event: KeyboardEvent) { if (event.isComposing || event.keyCode === 229) return; event.preventDefault(); void send(); }
async function send() {
  if (props.readOnly || submitting.value || props.attachmentPending || (!text.value.trim() && !props.attachmentPaths?.length && !props.attachmentIds?.length) || !session.value?.accepts_input) return;
  const w = props.workspace, id = props.sessionId;
  if (!pending || pending.text !== text.value || pending.mode !== deliveryMode.value || JSON.stringify(pending.paths)!==JSON.stringify(props.attachmentPaths||[]) || JSON.stringify(pending.ids)!==JSON.stringify(props.attachmentIds||[])) pending = { paths:[...(props.attachmentPaths||[])],ids:[...(props.attachmentIds||[])],text: text.value, key: crypto.randomUUID(), mode: deliveryMode.value };
  const request = pending; submitting.value = true; emit('sending',true); actionError.value = ''; notice.value = '';
  try { await managerApi.input(w, id, request.text, request.key, undefined, request.mode,request.paths,request.ids); if (id !== props.sessionId || w !== props.workspace) return; text.value = ''; pending = null; emit('sent');notice.value = '输入已受理'; await flow.refresh(); }
  catch (e) { if (id === props.sessionId && w === props.workspace) actionError.value = e instanceof Error ? e.message : '发送失败，可原样重试'; }
  finally { submitting.value = false;emit('sending',false); }
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
const stopUnavailable = computed(() => props.stopDisabled || !!props.turnId || !session.value || session.value.lifecycle === 'archived' || controlBusy.value);
async function stop() {
  if (stopUnavailable.value) return;
  const id = props.sessionId, w = props.workspace; controlBusy.value = true; actionError.value = ''; notice.value = '';
  try { await managerApi.stop(w, id); if (id === props.sessionId && w === props.workspace) { notice.value = '停止请求已受理，请等待状态确认'; emit('stopped'); await flow.refresh(); } }
  catch (e) { if (id === props.sessionId && w === props.workspace) actionError.value = e instanceof Error ? e.message : '停止失败'; }
  finally { controlBusy.value = false; }
}
async function older() { const el = scroll.value, before = el?.scrollHeight || 0; await flow.older(); await nextTick(); if (el) el.scrollTop += el.scrollHeight - before; }
watch(rows, async () => { const el = scroll.value; const bottom = !el || el.scrollHeight - el.scrollTop - el.clientHeight < 100; const paging = loadingOlder.value; await nextTick(); if (el && bottom && !paging) el.scrollTop = el.scrollHeight; });
</script>
<template>
  <section class="session-panel" :aria-label="title">
    <header v-if="!embedded"><div class="session-heading"><span class="eyebrow">{{ task ? '任务执行过程' : '与你的 AI 员工协作' }}</span><strong>{{ title }}</strong></div><span class="connection muted"><i :class="{ connected: connection === '已连接' }" aria-hidden="true" />{{ connection || '正在连接' }} · {{ status(session?.execution.state) }}</span><NButton v-if="task" size="small" @click="emit('close')">关闭任务详情</NButton></header>
    <p v-if="session?.execution.block_reason" class="warning">{{ status(session.execution.block_reason) }}</p>
    <div v-if="error" class="error" role="alert">{{ error }} <NButton size="small" @click="flow.reload">重新加载</NButton></div>
    <div ref="scroll" class="messages">
      <NSkeleton v-if="loading" text :repeat="4" />
      <NButton v-if="olderCursor" size="small" :loading="loadingOlder" @click="older">加载更早历史消息</NButton>
      <p v-if="!loading && !rows.length" class="empty muted">{{ taskCenter ? '暂无执行记录。' : '暂无消息，发送工作需求开始协作。' }}</p>
      <article v-for="{ event: row, replies } in messages" :key="row.action?.request_id || row.message_id || row.tool_use_id || row.turn_id || row.seq" :class="['event', { user: isUser(row), tool: !!row.tool_use_id, report: row.source_kind === 'task_report', 'turn-event': row.type === 'turn.updated', 'error-event': row.type === 'session.error' }]">
        <template v-if="row.action"><strong>{{ row.action.title }}</strong><p class="body">{{ row.action.message }}</p></template>
        <SessionToolCall v-else-if="row.tool_use_id" :event="row" :task-title="linkedTask(row)?.title || undefined" :can-open-task="!!taskSessionID(row)" @open-task="emit('openTask', $event)" />
        <SessionTaskReport v-else-if="row.source_kind === 'task_report'" :event="row" :reply="replies" :title="linkedTask(row)?.title || undefined" :can-open-task="!!taskReport(row)?.sessionID" @open-task="emit('openTask', $event, taskReport(row)?.turnID)" />
        <template v-else-if="row.type === 'turn.updated'"><span class="muted">本轮：{{ status(row.status) }}</span><p v-if="row.error" class="error">{{ row.error.message }}</p></template>
        <template v-else>
          <div class="message-meta"><strong>{{ row.source_kind === 'work_item_amendment' ? '事项要求更新' : row.source_kind === 'work_item_notice' || row.source_kind === 'work_item_assignment' || row.source_kind === 'work_item_result' ? '事项交接' : isUser(row) ? '你' : row.type === 'session.error' ? '会话提示' : title }}</strong><time v-if="row.occurred_at" :datetime="row.occurred_at">{{ clockTime(row.occurred_at) }}</time><span v-if="row.type === 'agent.thinking'">思考过程</span></div>
          <p v-if="isUser(row)" class="body">{{ eventText(row) }}</p><details v-else-if="row.type === 'agent.thinking'" class="thinking"><summary>查看思考过程</summary><SessionMarkdown :text="eventText(row)" /></details><SessionMarkdown v-else :text="eventText(row)" />
          <template v-for="(file, index) in row.attachments" :key="file.id || file.path || index">
            <button v-if="liveAttachment(file).status !== 'failed' && (liveAttachment(file).path || file.id && attachmentLookup)" class="attachment-link" :title="liveAttachment(file).path" :disabled="!!file.id && readingAttachments.has(file.id)" @click="downloadAttachment(file)">▤ {{ liveAttachment(file).name }} · {{ liveAttachment(file).path || liveAttachment(file).status === 'ready' ? (file.size_bytes/1024).toFixed(1) + ' KB ↓' : '准备中，点击查看状态' }}</button>
            <span v-else class="attachment-link" role="status">▤ {{ liveAttachment(file).name }} · {{ liveAttachment(file).status === 'failed' ? '附件不可用' : liveAttachment(file).status === 'ready' ? '已就绪' : '正在准备附件' }}</span>
          </template>
          <small v-if="isUser(row) || row.status && row.status !== 'completed'">{{ row.status === 'queued' && row.delivery_mode === 'steer' ? '待引导' : status(row.status) }}</small>
        </template>
      </article>
    </div>
    <p v-if="downloadError" class="error" role="alert">{{ downloadError }}</p>
    <footer v-if="!turnId">
      <section v-for="action in session?.execution.required_actions" :key="action.request_id" class="confirmation">
        <strong>{{ action.title }}</strong><p>{{ action.message }}</p>
        <div v-if="!taskCenter && action.type === 'confirmation'" class="actions"><NButton size="small" :disabled="!!answering || readOnly || !session?.accepts_input" @click="confirm(action.request_id, false)">否</NButton><NButton size="small" type="primary" :disabled="!!answering || readOnly || !session?.accepts_input" @click="confirm(action.request_id, true)">是</NButton></div>
      </section>
      <p v-if="actionError" class="error" role="alert">{{ actionError }}</p><p v-if="notice" class="muted" role="status">{{ notice }}</p>
      <p v-if="session && !session.accepts_input" class="warning">{{ session.lifecycle === 'archived' ? '此会话已归档，历史仍可查看，不能继续发送。' : `暂不能输入：${session.input_block_reason || session.lifecycle}` }}</p>
      <slot name="attachments" />
      <NInput v-model:value="text" type="textarea" :rows="2" :disabled="readOnly || !session?.accepts_input || submitting" :placeholder="task ? '给此任务补充要求' : '发送消息，任务执行期间仍可继续对话'" @keydown.enter.exact="onEnter" />
      <div class="actions"><span class="composer-hint">Enter 发送 · Shift + Enter 换行</span><NSelect v-model:value="deliveryMode" aria-label="输入处理方式" :options="deliveryOptions" style="width:160px" :disabled="readOnly || !session?.accepts_input || submitting" /><NButton :loading="controlBusy" :disabled="stopUnavailable" @click="stop">{{ task ? '停止此任务' : '停止主对话' }}</NButton><NButton type="primary" :loading="submitting" :disabled="attachmentPending || (!text.trim() && !attachmentPaths?.length && !attachmentIds?.length) || readOnly || !session?.accepts_input || submitting" @click="send">{{ actionError && pending ? '重试发送' : '发送' }}</NButton></div>
    </footer>
  </section>
</template>
<style scoped>
.attachment-link{border:1px solid var(--id-border-subtle);background:var(--id-bg-surface);color:inherit;padding:6px 10px;border-radius:6px;cursor:pointer;margin:4px}.confirmation{padding:12px 0;border-bottom:1px solid var(--id-border-subtle);overflow-wrap:anywhere}.confirmation p{white-space:pre-wrap}.session-panel{min-width:0;flex:1;display:flex;flex-direction:column;border:1px solid var(--id-border-subtle);border-radius:var(--id-radius-lg);background:var(--id-bg-surface);overflow:hidden;height:100%;min-height:420px}.session-panel>header{display:flex;align-items:center;gap:12px;padding:16px 20px;border-bottom:1px solid var(--id-border-subtle);flex-wrap:wrap}.session-heading{flex:1;min-width:100px}.session-heading strong{display:block;font-size:14px}.eyebrow{font-size:10px;color:var(--id-text-muted)}.connection{display:flex;align-items:center;gap:5px}.connection i{width:5px;height:5px;border-radius:50%;background:var(--id-text-muted)}.connection i.connected{background:var(--id-status-success)}.messages{flex:1;min-height:0;overflow:auto;padding:24px clamp(16px,3vw,40px);background:var(--id-bg-subtle);display:flex;flex-direction:column;gap:18px;scrollbar-gutter:stable}.event{width:100%;min-width:0;max-width:780px;align-self:center;font-size:13px;overflow-wrap:anywhere}.event.user{align-self:flex-end;max-width:min(85%,680px);width:fit-content;padding:12px 16px;border-radius:14px 14px 4px 14px;background:var(--id-bg-muted);border:1px solid var(--id-border-subtle)}.message-meta{display:flex;align-items:center;gap:8px;font-size:11px;color:var(--id-text-muted);margin-bottom:8px}.message-meta strong{font-weight:500;color:var(--id-text-secondary)}.message-meta time{font-size:10px}.body{white-space:pre-wrap;word-break:break-word;margin:0;line-height:1.8}.event small{display:block;margin-top:6px}.turn-event{border-top:1px solid var(--id-border-subtle);padding-top:8px;font-size:11px}.turn-event .muted{font-size:11px}.error-event{border-left:2px solid var(--id-status-danger);padding-left:12px}.thinking summary{color:var(--id-text-muted);font-size:12px;cursor:pointer}.thinking[open] summary{margin-bottom:10px}small,.muted{font-size:12px;color:var(--id-text-muted)}.empty{margin:auto;text-align:center}.error,.warning{padding:8px 12px;font-size:13px}.error{color:var(--id-status-danger)}.warning{color:var(--id-status-warning)}.session-panel>footer{padding:14px 20px;border-top:1px solid var(--id-border-subtle)}.actions{display:flex;align-items:center;justify-content:flex-end;gap:8px;margin-top:10px;flex-wrap:wrap}.composer-hint{margin-right:auto;color:var(--id-text-muted);font-size:10px}@media(max-width:1200px){.composer-hint{display:none}}@media(max-width:640px){.messages{padding:16px 12px}.session-panel>header,.session-panel>footer{padding:12px}.connection{font-size:10px}.actions{gap:6px}.event.user{max-width:94%}}
</style>

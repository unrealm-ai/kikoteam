<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue';
import { NButton, NInput, NSkeleton } from 'naive-ui';
import ProjectFileTree from '../components/ProjectFileTree.vue';
import ProjectDraftFiles from '../components/ProjectDraftFiles.vue';
import {useProjectDraft} from '../composables/useProjectDraft';
import D2EmployeeAvatar from '../components/D2EmployeeAvatar.vue';
import ManagerSessionPanel from '../components/ManagerSessionPanel.vue';
import TaskDetailPanel from '../components/TaskDetailPanel.vue';
import { activeWorkspaceId, D2ApiError } from '../api/client';
import { agentsApi, type Agent } from '../api/agents';
import { managerApi, type Conversation, type ManagerSession } from '../api/manager';
import { taskApi } from '../api/tasks';
import { sessionStatus, workItemProgress, type TaskNavigationTarget } from '../utils/session-presentation';
const props = defineProps<{ selectedEmployeeId?: string | undefined; selectedConversationId?: string | undefined }>();
const agents = ref<Agent[]>([]), selected = ref(''), search = ref(''), listError = ref(''), loading = ref(false);
const conversation = ref<Conversation | null>(null), chatID = ref(''), taskID = ref(''), tasks = ref<ManagerSession[]>([]), taskCursor = ref<string | null>(null);
const error = ref(''), tasksError = ref(''), firstText = ref(''), sending = ref(false), opening = ref(false);
const showFiles=ref(false),treeMounted=ref(false),tree=ref<InstanceType<typeof ProjectFileTree>>(),dragDepth=ref(0),chatSending=ref(false);
const filesAvailable=computed(()=>!!conversation.value&&(!!conversation.value.web_user_id||!props.selectedConversationId));
const draft=useProjectDraft(()=>({workspace:activeWorkspaceId.value,conversation:conversation.value?.id||''}),()=>{void tree.value?.refresh();},true);
const locked=computed(()=>sending.value||chatSending.value||conversation.value?.lifecycle!=='active'||!employee.value?.enabled);
function toggleFiles(){treeMounted.value=true;showFiles.value=!showFiles.value;if(showFiles.value)showTasks.value=false;}
function dragFiles(e:DragEvent){return !!e.dataTransfer?.types.includes('Files')&&filesAvailable.value&&!locked.value;}
function dragEnter(e:DragEvent){if(dragFiles(e)){e.preventDefault();dragDepth.value++;}}
function dropFiles(e:DragEvent){dragDepth.value=0;if(!dragFiles(e))return;e.preventDefault();void draft.enqueue(Array.from(e.dataTransfer!.files));}
const showTasks = ref(false), mobileChat = ref(false), taskFullscreen = ref(false);
const detailTaskID = ref(''), detailTurnID = ref('');
let lastTaskRefresh = 0, tasksLoaded = false;
let taskOpenGeneration = 0;
let generation = 0, controller = new AbortController(), listController = new AbortController(), timer: ReturnType<typeof setInterval> | null = null, taskBusy = false;
let firstRequest: { paths:string[]; ids:string[]; text: string; key: string; inputKey: string } | null = null;
const employee = computed(() => agents.value.find(a => a.id === selected.value));
const filtered = computed(() => agents.value.filter(a => `${a.name} ${a.role}`.toLowerCase().includes(search.value.toLowerCase())));
async function loadAgents() {
  listController.abort(); listController = new AbortController(); const signal = listController.signal, w = activeWorkspaceId.value;
  loading.value = true; listError.value = ''; agents.value = [];
  try {
    const all: Agent[] = []; const seen = new Set<string>();
    for (let page = 1; ; page++) {
      const result = await agentsApi.list(w, page, signal); if (signal.aborted || w !== activeWorkspaceId.value) return;
      if (result.page !== page || result.items.some(a => seen.has(a.id))) throw new Error('员工分页响应重复或页码错误');
      result.items.forEach(a => seen.add(a.id)); all.push(...result.items);
      if (!result.items.length || all.length >= result.total) break;
    }
    agents.value = all; selected.value = all.some(a => a.id === props.selectedEmployeeId) ? props.selectedEmployeeId! : all[0]?.id || '';
  } catch (e) { if (!signal.aborted) listError.value = e instanceof Error ? e.message : '员工加载失败'; }
  finally { if (!signal.aborted) loading.value = false; }
}
async function refreshTasks(more = false) {
  if (taskBusy || !conversation.value || document.hidden) return;
  if (!more && Date.now() - lastTaskRefresh < 1000) return;
  lastTaskRefresh = Date.now();
  const g = generation, w = activeWorkspaceId.value, c = conversation.value.id, cursor = more ? taskCursor.value || '' : '';
  taskBusy = true;
  try {
    const page = await managerApi.tasks(w, c, cursor, controller.signal); if (g !== generation) return;
    if (cursor && page.next_cursor === cursor) throw new Error('任务分页游标重复');
    const overlapsKnownPage = page.items.some(t => tasks.value.some(known => known.id === t.id));
    const previous = more ? tasks.value : await Promise.all(tasks.value.filter(t => !page.items.some(fresh => fresh.id === t.id)).map(t => managerApi.session(w, t.id, controller.signal)));
    if (g !== generation) return;
    const map = new Map(previous.map(t => [t.id, t])); page.items.forEach(t => map.set(t.id, t)); tasks.value = [...map.values()].sort((a, b) => (b.created_at || '').localeCompare(a.created_at || '') || b.id.localeCompare(a.id));
    if (more || !tasksLoaded || (page.next_cursor && !overlapsKnownPage)) taskCursor.value = page.next_cursor; tasksLoaded = true; tasksError.value = '';
  } catch (e) { if (g === generation) tasksError.value = e instanceof Error ? e.message : '任务列表加载失败'; }
  finally { if (g === generation) taskBusy = false; }
}
async function open() {
  draft.reset();showFiles.value=false;treeMounted.value=false;dragDepth.value=0;chatSending.value=false;
  const g = ++generation; controller.abort(); controller = new AbortController();
  conversation.value = null; chatID.value = ''; taskID.value = ''; taskFullscreen.value = false; tasks.value = []; taskCursor.value = null; taskBusy = false; lastTaskRefresh = 0; tasksLoaded = false;
  opening.value = false; firstText.value = ''; firstRequest = null; error.value = ''; tasksError.value = ''; sending.value = false;
  if (!selected.value || !activeWorkspaceId.value) return;
  opening.value = true;
  const trialKey='kiko:template-trial:'+activeWorkspaceId.value+':'+selected.value;
  firstText.value=typeof sessionStorage==='undefined'?'':sessionStorage.getItem(trialKey)||'';
  try { const c = props.selectedConversationId && selected.value === props.selectedEmployeeId ? await managerApi.conversation(activeWorkspaceId.value, props.selectedConversationId, controller.signal) : await managerApi.direct(activeWorkspaceId.value, selected.value, controller.signal); if (g !== generation) return; if (c.agent_id !== selected.value) throw new Error('来源对话与员工不匹配'); conversation.value = c; chatID.value = c.chat_session_id || ''; if(!chatID.value&&typeof sessionStorage!=='undefined')sessionStorage.removeItem(trialKey); void refreshTasks(); }
  catch (e) { if (g === generation) error.value = e instanceof Error ? e.message : '对话加载失败'; }
  finally { if (g === generation) opening.value = false; }
}
function onFirstEnter(event: KeyboardEvent) { if (event.isComposing || event.keyCode === 229) return; event.preventDefault(); void firstSend(); }
async function firstSend() {
  if (!conversation.value || conversation.value.lifecycle !== 'active' || sending.value || draft.unreserved.value || (!firstText.value.trim()&&!draft.paths.value.length&&!draft.attachmentIds.value.length)) return;
  const g = generation, w = activeWorkspaceId.value, cid = conversation.value.id, signal = controller.signal;
  if (!firstRequest || firstRequest.text !== firstText.value || JSON.stringify(firstRequest.paths)!==JSON.stringify(draft.paths.value) || JSON.stringify(firstRequest.ids)!==JSON.stringify(draft.attachmentIds.value)) firstRequest = { paths:[...draft.paths.value], ids:[...draft.attachmentIds.value], text: firstText.value, key: crypto.randomUUID(), inputKey: crypto.randomUUID() };
  const request = firstRequest; sending.value = true; error.value = '';
  try {
    let session: ManagerSession;
    try { session = await managerApi.createChat(w, cid, request.text, request.key, signal, request.paths, request.ids); }
    catch (e) {
      if (g !== generation) return;
      if (!(e instanceof D2ApiError) || e.code !== 'chat_session_exists') throw e;
      const c = await managerApi.conversation(w, cid, signal); if (g !== generation) return; if (!c.chat_session_id) throw new Error('主对话尚不可读取，请重试');
      await managerApi.input(w, c.chat_session_id, request.text, request.inputKey, signal,'steer',request.paths,request.ids);
      if (g !== generation) return;
      session = await managerApi.session(w, c.chat_session_id, signal);
    }
    if (g !== generation) return; chatID.value = session.id; firstText.value = ''; draft.clearSent(); firstRequest = null;
  } catch (e) { if (g === generation) error.value = e instanceof Error ? e.message : '发送结果未知，请原样重试'; }
  finally { if (g === generation) sending.value = false; }
}
function visibility() { if (!document.hidden) void refreshTasks(); }
async function locateTask(target: TaskNavigationTarget, w: string, signal: AbortSignal) {
  const known = tasks.value.find(task => (target.kind === 'task' ? task.task_id : task.id) === target.id);
  if (known) return { taskID: known.task_id, sessionID: known.id, conversationID: known.conversation_id, taskMode: known.mode === 'task' };
  if (target.kind === 'task') {
    const v = await taskApi.detail(w, target.id, signal);
    return { taskID: v.task_id, sessionID: v.session_id, conversationID: v.conversation_id, taskMode: v.task_id === target.id };
  }
  const v = await managerApi.session(w, target.id, signal);
  return { taskID: v.task_id, sessionID: v.id, conversationID: v.conversation_id, taskMode: v.mode === 'task' && v.id === target.id };
}
async function openTask(target: TaskNavigationTarget, turn = '') {
  const g = generation, w = activeWorkspaceId.value, attempt = ++taskOpenGeneration;
  if (!conversation.value) return;
  try {
    const task = await locateTask(target, w, controller.signal);
    if (g !== generation || attempt !== taskOpenGeneration || task.conversationID !== conversation.value?.id || !task.taskMode || !task.taskID || !task.sessionID) return;
    if (detailTaskID.value !== task.taskID) taskFullscreen.value = false;
    taskID.value = task.sessionID; detailTaskID.value = task.taskID; detailTurnID.value = turn; showTasks.value = true; showFiles.value = false;
  } catch (e) { if (g === generation && attempt === taskOpenGeneration) tasksError.value = e instanceof Error ? e.message : '任务详情不可读取'; }
}
watch(selected, () => { void open(); });
watch(showTasks, visible => { if (!visible) { taskOpenGeneration++; taskFullscreen.value = false; } });
watch(taskID, id => { if (!id) { taskOpenGeneration++; taskFullscreen.value = false; } });
watch(() => props.selectedEmployeeId, id => { if (id && agents.value.some(a => a.id === id)) { selected.value = id; mobileChat.value = true; } });
watch(() => props.selectedConversationId, () => { void open(); });
watch(activeWorkspaceId, () => { selected.value = ''; void loadAgents(); });
onMounted(() => { void loadAgents(); timer = setInterval(() => { void refreshTasks(); }, 5000); document.addEventListener('visibilitychange', visibility); });
onUnmounted(() => { generation++; controller.abort(); listController.abort(); if (timer) clearInterval(timer); document.removeEventListener('visibilitychange', visibility); });
</script>
<template>
  <div class="conversation-workspace" :class="{ 'mobile-chat': mobileChat }">
    <aside class="employee-list"><h2>对话</h2><NInput v-model:value="search" placeholder="搜索 AI 员工" /><p v-if="listError" class="error">{{ listError }} <NButton @click="loadAgents">重试</NButton></p><NSkeleton v-if="loading" text :repeat="4" /><button v-for="agent in filtered" :key="agent.id" class="employee" :class="{ selected: selected === agent.id }" @click="selected = agent.id; mobileChat = true"><D2EmployeeAvatar :name="agent.name" :preset="agent.avatar" :identity-key="agent.id" size="md" /><span><strong>{{ agent.name }}</strong><small>{{ agent.role || '固定私聊' }}</small></span></button><p v-if="!loading && !filtered.length" class="muted">暂无 AI 员工</p></aside>
    <main class="chat-area"><div class="toolbar"><NButton class="back" size="small" @click="mobileChat = false">返回员工列表</NButton><strong>{{ employee?.name || '选择 AI 员工' }}</strong><span class="muted toolbar-note">对话协作 · 任务持续跟进</span><NButton v-if="conversation" size="small" @click="showFiles=false; showTasks = !showTasks; if (!showTasks) { taskFullscreen = false; taskID = ''; }">{{ showTasks ? '收起任务栏' : `会话任务 (${tasks.length})` }}</NButton><NButton v-if="filesAvailable" size="small" :aria-expanded="showFiles" @click="toggleFiles">项目文件</NButton></div>
      <p v-if="conversation && conversation.lifecycle !== 'active'" class="muted" role="status">此主对话已归档，不能继续发送；历史消息仍保留，不会自动新建替代对话。</p>
      <p v-if="error" class="error" role="alert">{{ error }} <NButton v-if="!conversation" @click="open">重新加载</NButton></p><NSkeleton v-if="opening" text :repeat="4" />
      <div v-else-if="conversation" class="panels">
        <div class="chat-column" @dragenter="dragEnter" @dragover="dragFiles($event)&&$event.preventDefault()" @dragleave="dragDepth=Math.max(0,dragDepth-1)" @drop="dropFiles">
        <ManagerSessionPanel v-if="chatID" :attachment-paths="draft.paths.value" :attachment-ids="draft.attachmentIds.value" :attachment-pending="draft.unreserved.value" :attachment-lookup="filesAvailable" :uploaded-attachments="draft.uploadedAttachments.value" @sending="chatSending=$event" @sent="draft.clearSent" @report="tree?.refresh()" :key="chatID" :workspace="activeWorkspaceId" :session-id="chatID" :read-only="conversation.lifecycle !== 'active'" :title="employee?.name || '主对话'" :tasks="tasks" @changed="refreshTasks()" @open-task="openTask"><template #attachments><ProjectDraftFiles v-if="filesAvailable" :draft="draft" :disabled="locked"/></template></ManagerSessionPanel>
        <section v-else-if="conversation.lifecycle === 'active'" class="new-chat"><p>暂无历史消息，请向「{{ employee?.name }}」发送工作需求。</p><ProjectDraftFiles v-if="filesAvailable" :draft="draft" :disabled="locked"/><NInput v-model:value="firstText" type="textarea" :disabled="sending || !employee?.enabled" placeholder="输入消息" @keydown.enter.exact="onFirstEnter" /><NButton type="primary" :loading="sending" :disabled="draft.unreserved.value || (!firstText.trim() && !draft.paths.value.length && !draft.attachmentIds.value.length) || sending || !employee?.enabled" @click="firstSend">{{ error ? '重试发送' : '发送' }}</NButton></section>
        <div v-if="dragDepth>0" class="drop-hint" role="status"><span>↑</span><strong>松开以添加附件</strong><small>支持文件和图片</small></div></div>
        <ProjectFileTree v-if="treeMounted&&filesAvailable" v-show="showFiles" ref="tree" :key="conversation.id" :workspace="activeWorkspaceId" :conversation="conversation.id" :read-only="locked" @close="showFiles=false" @reference="draft.reference"/>
        <aside v-if="showTasks" class="tasks" :class="{ 'has-detail': taskID, 'is-fullscreen': taskFullscreen && !!taskID && !!detailTaskID }"><div class="task-list-heading"><h3>本会话任务 <span>{{ tasks.length }}</span></h3><p class="muted">任务可并行运行，本轮结束后仍可继续。</p><p v-if="tasksError" class="error">{{ tasksError }}</p></div><div class="task-list"><button v-for="task in tasks" :key="task.id" :class="['task', { selected: taskID === task.id }]" @click="openTask(task.task_id ? {kind:'task',id:task.task_id} : {kind:'session',id:task.id})"><strong>{{ task.title || '任务' }}</strong><small>{{ workItemProgress(task.execution) || (task.execution.required_actions?.length ? '待确认' : sessionStatus(task.execution.state)) }} · {{ sessionStatus(task.lifecycle) }}</small></button><p v-if="!tasks.length" class="muted">尚未触发任务</p><NButton v-if="taskCursor" size="small" @click="refreshTasks(true)">加载更多任务</NButton></div><div v-if="taskID && detailTaskID" class="task-detail"><TaskDetailPanel :key="detailTaskID" v-model:fullscreen="taskFullscreen" :workspace-id="activeWorkspaceId" :task-id="detailTaskID" :initial-turn-id="detailTurnID" presentation="sidebar" @task-changed="refreshTasks()" @close="taskFullscreen = false; taskID = ''" @open-source-conversation="taskFullscreen = false" /></div></aside>
      </div>
    </main>
  </div>
</template>
<style scoped>
.chat-column{position:relative;display:flex;flex:1;min-width:0;min-height:0}.drop-hint{pointer-events:none;position:absolute;z-index:2;bottom:145px;left:16px;right:16px;padding:32px;display:grid;justify-items:center;gap:8px;border:2px dashed #398659;border-radius:12px;background:#effaf4f5;color:#176d3d}.drop-hint span{font-size:32px}.drop-hint strong{font-size:20px}@media(max-width:1200px){.chat-column{min-height:480px}}.conversation-workspace{display:flex;gap:16px;height:calc(100vh - 120px);min-height:540px;color:var(--id-text-primary)}.employee-list{width:220px;flex-shrink:0;border:1px solid var(--id-border-subtle);border-radius:var(--id-radius-lg);background:var(--id-bg-surface);padding:16px;overflow:auto}h2{font-size:18px;margin:0 0 16px}h3{font-size:14px;margin:0 0 6px}.employee,.task{display:flex;align-items:center;gap:10px;width:100%;padding:12px;margin-top:8px;text-align:left;cursor:pointer;background:var(--id-bg-surface);border:1px solid var(--id-border-subtle);border-radius:var(--id-radius-md);color:var(--id-text-primary)}.employee.selected,.task.selected{background:var(--id-bg-subtle);border-color:var(--id-accent-primary)}.employee span{min-width:0}.employee strong,.task strong{display:block;font-size:13px;overflow-wrap:anywhere}.employee small,.task small{display:block;font-size:12px;color:var(--id-text-muted);margin-top:4px}.chat-area{flex:1;min-width:0;display:flex;flex-direction:column;gap:12px;overflow:auto}.toolbar{display:flex;align-items:center;gap:12px;flex-wrap:wrap}.toolbar strong{flex:1}.panels{display:flex;gap:12px;flex:1;min-height:420px}.tasks{width:240px;flex-shrink:0;border:1px solid var(--id-border-subtle);border-radius:var(--id-radius-lg);background:var(--id-bg-surface);overflow:hidden;display:flex;flex-direction:column}.tasks.has-detail{width:clamp(340px,30vw,440px)}.task-list-heading{padding:16px 16px 0}.task-list-heading p{margin:6px 0}.task-list-heading h3 span{color:var(--id-text-muted);font-size:11px;margin-left:5px}.task-list{padding:0 12px 12px;overflow:auto;min-height:0}.has-detail .task-list{max-height:150px;flex-shrink:0}.task{flex-direction:column;align-items:flex-start;gap:0;padding:10px 12px}.task-detail{flex:1;min-height:0;display:flex;border-top:1px solid var(--id-border-subtle)}.task-detail :deep(.session-panel){border:0;border-radius:0;min-height:340px}.task-detail :deep(.messages){padding:16px}.task-detail :deep(.composer-hint){display:none}.new-chat{flex:1;border:1px solid var(--id-border-subtle);border-radius:var(--id-radius-lg);padding:24px;background:var(--id-bg-surface);display:flex;flex-direction:column;gap:16px;justify-content:center}.muted{font-size:12px;color:var(--id-text-muted)}.error{font-size:13px;color:var(--id-status-danger)}.back{display:none}button:focus-visible{outline:2px solid var(--id-accent-primary);outline-offset:2px}@media(max-width:1400px){.employee-list{width:180px;padding:12px}.toolbar-note{display:none}}@media(max-width:1200px){.panels{flex-direction:column}.panels>.session-panel{height:600px;flex:none}.tasks,.tasks.has-detail{width:auto;flex-shrink:0}.task-list{max-height:220px}.task-detail{height:560px;flex:none}}@media(max-width:640px){.conversation-workspace{height:calc(100vh - 100px)}.employee-list{width:100%}.chat-area{display:none}.mobile-chat .employee-list{display:none}.mobile-chat .chat-area{display:flex}.back{display:inline-flex}.panels>.session-panel{height:calc(100vh - 200px);min-height:420px}}
.tasks.is-fullscreen{position:fixed;inset:0;z-index:1200;width:100vw;height:100dvh;max-width:none;border:0;border-radius:0}.tasks.is-fullscreen .task-list-heading,.tasks.is-fullscreen .task-list{display:none}.tasks.is-fullscreen .task-detail{flex:1;height:100%;min-height:0;border-top:0}.tasks.is-fullscreen .task-detail :deep(.composer-hint){display:inline}
</style>

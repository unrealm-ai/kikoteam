<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue';
import { NButton, NInput, NModal, NSkeleton } from 'naive-ui';
import ProjectFileTree from '../components/ProjectFileTree.vue';
import ProjectDraftFiles from '../components/ProjectDraftFiles.vue';
import {useProjectDraft} from '../composables/useProjectDraft';
import D2EmployeeAvatar from '../components/D2EmployeeAvatar.vue';
import AssistantDemoCards from '../components/AssistantDemoCards.vue';
import { assistantApi, type AssistantCard, type AssistantEntry } from '../api/assistant';
import ManagerSessionPanel from '../components/ManagerSessionPanel.vue';
import TaskDetailPanel from '../components/TaskDetailPanel.vue';
import { activeWorkspaceId, D2ApiError } from '../api/client';
import { agentsApi, type Agent } from '../api/agents';
import { managerApi, type Conversation, type ManagerSession } from '../api/manager';
import { taskApi } from '../api/tasks';
import { sessionStatus, workItemProgress, type TaskNavigationTarget } from '../utils/session-presentation';
const props = defineProps<{ accountId?: string | undefined; selectedEmployeeId?: string | undefined; selectedConversationId?: string | undefined; assistantEntry?: AssistantEntry | undefined;
 assistantDemo?: {card: AssistantCard; workspace: string; agent: string; key: string} | undefined }>();
const emit = defineEmits<{demoConsumed: [key: string]}>();
const demoOpen = ref(false);
const demoCards = computed(() => props.assistantEntry?.agent_id === selected.value && !props.selectedConversationId ? props.assistantEntry?.cards || [] : []);
const agents = ref<Agent[]>([]), startedIds = ref<string[]>([]), selected = ref(''), search = ref(''), listError = ref(''), loading = ref(false);
const newChatOpen = ref(false), modalSearch = ref('');
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
const sidebarAgents = computed(() => {
  const byId = new Map(agents.value.map(a => [a.id, a]));
  return startedIds.value.flatMap(id => { const agent = byId.get(id); return agent ? [agent] : []; });
});
const filtered = computed(() => sidebarAgents.value.filter(a => `${a.name} ${a.role}`.toLowerCase().includes(search.value.toLowerCase())));
const modalAgents = computed(() => {
  const q = modalSearch.value.trim().toLowerCase();
  const started = new Set(startedIds.value);
  const matching = agents.value.filter(a => !q || `${a.name} ${a.role}`.toLowerCase().includes(q));
  return [
    ...matching.filter(a => !started.has(a.id)),
    ...matching.filter(a => started.has(a.id)),
  ];
});
const unstartedCount = computed(() => agents.value.filter(a => !startedIds.value.includes(a.id)).length);
const runningTasksCount = computed(() => tasks.value.filter(t => ['running', 'queued', 'stopping'].includes(t.execution.state)).length);
function storageKey(w: string) { return `kiko:started-chats:v2:${props.accountId}:${w}`; }
function readStoredStarted(w: string): string[] {
  try {
    if (typeof localStorage === 'undefined' || !props.accountId || !w) return [];
    const raw = localStorage.getItem(storageKey(w));
    const parsed = raw ? JSON.parse(raw) : [];
    return Array.isArray(parsed) ? parsed.filter((id): id is string => typeof id === 'string') : [];
  } catch { return []; }
}
function persistStarted(w: string, ids: string[]) {
  try {
    if (typeof localStorage !== 'undefined' && props.accountId && w) localStorage.setItem(storageKey(w), JSON.stringify(ids));
  } catch { /* ignore storage quota/privacy failures */ }
}
function markStarted(agentId: string) {
  if (!agentId || startedIds.value.includes(agentId)) return;
  const next = [agentId, ...startedIds.value];
  startedIds.value = next;
  persistStarted(activeWorkspaceId.value, next);
}
function startAgentChat(agentId: string) {
  markStarted(agentId);
  selected.value = agentId;
  mobileChat.value = true;
  newChatOpen.value = false;
  modalSearch.value = '';
}
async function loadAgents() {
  listController.abort(); listController = new AbortController(); const signal = listController.signal, w = activeWorkspaceId.value;
  loading.value = true; listError.value = ''; agents.value = []; startedIds.value = [];
  try {
    const convPromise = managerApi.conversations(w, '', signal).catch(() => null);
    const all: Agent[] = []; const seen = new Set<string>();
    for (let page = 1; ; page++) {
      const result = await agentsApi.list(w, page, signal); if (signal.aborted || w !== activeWorkspaceId.value) return;
      if (result.page !== page || result.items.some(a => seen.has(a.id))) throw new Error('员工分页响应重复或页码错误');
      result.items.forEach(a => seen.add(a.id)); all.push(...result.items);
      if (!result.items.length || all.length >= result.total) break;
    }
    const convPage = await convPromise;
    if (signal.aborted || w !== activeWorkspaceId.value) return;
    const validIds = new Set(all.map(a => a.id));
    const fromBackend = Array.isArray(convPage?.items)
      ? convPage.items.filter(c => c.chat_session_id && validIds.has(c.agent_id)).map(c => c.agent_id)
      : [];
    const merged = [...new Set([
      ...fromBackend,
      ...readStoredStarted(w).filter(id => validIds.has(id)),
    ])];
    startedIds.value = merged;
    persistStarted(w, merged);
    agents.value = all;
    selected.value = props.selectedEmployeeId && validIds.has(props.selectedEmployeeId) ? props.selectedEmployeeId : (merged[0] || all[0]?.id || '');
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
  demoOpen.value=false;draft.reset();showFiles.value=false;treeMounted.value=false;dragDepth.value=0;chatSending.value=false;
  const g = ++generation; controller.abort(); controller = new AbortController();
  conversation.value = null; chatID.value = ''; taskID.value = ''; taskFullscreen.value = false; tasks.value = []; taskCursor.value = null; taskBusy = false; lastTaskRefresh = 0; tasksLoaded = false;
  opening.value = false; firstText.value = ''; firstRequest = null; error.value = ''; tasksError.value = ''; sending.value = false;
  if (!selected.value || !activeWorkspaceId.value) return;
  opening.value = true;
  const trialKey='kiko:template-trial:'+activeWorkspaceId.value+':'+selected.value;
  firstText.value=typeof sessionStorage==='undefined'?'':sessionStorage.getItem(trialKey)||'';
  try { const c = props.selectedConversationId && selected.value === props.selectedEmployeeId ? await managerApi.conversation(activeWorkspaceId.value, props.selectedConversationId, controller.signal) : await managerApi.direct(activeWorkspaceId.value, selected.value, controller.signal); if (g !== generation) return; if (c.agent_id !== selected.value) throw new Error('来源对话与员工不匹配'); conversation.value = c; chatID.value = c.chat_session_id || ''; if (c.chat_session_id) markStarted(c.agent_id); if(!chatID.value&&typeof sessionStorage!=='undefined')sessionStorage.removeItem(trialKey); void refreshTasks(); }
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
    if (g !== generation) return; chatID.value = session.id; markStarted(selected.value); firstText.value = ''; draft.clearSent(); firstRequest = null;
  } catch (e) { if (g === generation) error.value = e instanceof Error ? e.message : '发送结果未知，请原样重试'; }
  finally { if (g === generation) sending.value = false; }
}
async function sendDemo(card: AssistantCard, key: string = crypto.randomUUID()) {
  if (!demoCards.value.some(item => item.key === card.key && item.input === card.input) || !conversation.value || locked.value) return;
  const g = generation, w = activeWorkspaceId.value, cid = conversation.value.id, signal = controller.signal;
  sending.value = true;
  error.value = '';
  demoOpen.value = false;
  try {
    const live = await assistantApi.entry(w, signal);
    if (g !== generation) return;
    if (live.agent_id !== selected.value || !live.cards.some(item => item.key === card.key && item.input === card.input)) {
      throw new Error('体验权限或样例已更新，请重新打开体验入口。');
    }
    if (chatID.value) {
      await managerApi.input(w, chatID.value, card.input, key, signal, 'queue');
      return;
    }
    let session: ManagerSession;
    try { session = await managerApi.createChat(w, cid, card.input, key, signal); }
    catch (e) {
      if (g !== generation) return;
      if (!(e instanceof D2ApiError) || e.code !== 'chat_session_exists') throw e;
      const c = await managerApi.conversation(w, cid, signal);
      if (!c.chat_session_id || g !== generation) return;
      await managerApi.input(w, c.chat_session_id, card.input, key, signal, 'queue');
      session = await managerApi.session(w, c.chat_session_id, signal);
    }
    if (g === generation) { chatID.value = session.id; markStarted(selected.value); }
  } catch (e) {
    if (g === generation) error.value = e instanceof Error ? e.message : '体验消息未确认发送，请检查对话后重试';
  } finally {
    if (g === generation) sending.value = false;
  }
}
watch([() => props.assistantDemo, conversation, selected, demoCards, locked], ([req, conv]) => {
  if (!req || !conv || locked.value || conv.agent_id !== req.agent || selected.value !== req.agent || activeWorkspaceId.value !== req.workspace || !demoCards.value.length) return;
  emit('demoConsumed', req.key);
  void sendDemo(req.card, req.key);
});

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
watch([activeWorkspaceId, () => props.accountId], () => { selected.value = ''; newChatOpen.value = false; modalSearch.value = ''; void loadAgents(); });
onMounted(() => { void loadAgents(); timer = setInterval(() => { void refreshTasks(); }, 5000); document.addEventListener('visibilitychange', visibility); });
onUnmounted(() => { generation++; controller.abort(); listController.abort(); if (timer) clearInterval(timer); document.removeEventListener('visibilitychange', visibility); });
</script>
<template>
  <div class="conversation-workspace" :class="{ 'mobile-chat': mobileChat }">
    <aside class="employee-list">
      <div class="sidebar-top">
        <div class="sidebar-title-row">
          <h2>协作会话 <span class="sidebar-count-pill">{{ sidebarAgents.length }}</span></h2>
          <NButton v-if="agents.length" size="tiny" type="primary" secondary @click="newChatOpen = true">+ 发起对话</NButton>
        </div>
        <NInput v-model:value="search" size="small" clearable placeholder="搜索已开启会话..." />
      </div>
      <p v-if="listError" class="error">{{ listError }} <NButton size="tiny" @click="loadAgents">重试</NButton></p>
      <NSkeleton v-if="loading" text :repeat="4" />
      <div class="sidebar-items">
        <button v-for="agent in filtered" :key="agent.id" class="employee" :class="{ selected: selected === agent.id }" @click="selected = agent.id; mobileChat = true">
          <D2EmployeeAvatar :name="agent.name" :preset="agent.avatar" :identity-key="agent.id" size="md" />
          <span class="emp-meta">
            <span class="emp-name-row">
              <strong>{{ agent.name }}</strong>
              <span class="mini-ai">AI</span>
            </span>
            <small>{{ agent.role || '1:1 专属工作区私聊' }}</small>
            <small v-if="selected === agent.id && runningTasksCount > 0" class="emp-running-tag">{{ runningTasksCount }} 个任务运行中</small>
          </span>
        </button>
        <div v-if="!loading && !filtered.length" class="sidebar-empty">
          <p class="muted">暂无匹配会话</p>
          <NButton v-if="unstartedCount > 0" size="tiny" secondary @click="newChatOpen = true">从员工名录发起</NButton>
        </div>
      </div>
    </aside>

    <main class="chat-area">
      <header v-if="!chatID" class="unified-fallback-header">
        <NButton class="back" size="small" @click="mobileChat = false">返回员工列表</NButton>
        <D2EmployeeAvatar v-if="employee" :name="employee.name" :preset="employee.avatar" :identity-key="employee.id" size="sm" />
        <div class="fallback-heading">
          <strong>{{ employee?.name || '选择 AI 员工' }}</strong>
          <span class="muted">{{ employee?.role || '对话协作 · 任务持续跟进' }}</span>
        </div>
        <div class="fallback-actions">
          <NButton v-if="demoCards.length && conversation?.lifecycle === 'active'" size="small" secondary @click="demoOpen = !demoOpen">体验场景</NButton>
          <NButton v-if="conversation" size="small" :type="showTasks ? 'primary' : 'default'" secondary @click="showFiles=false; showTasks = !showTasks; if (!showTasks) { taskFullscreen = false; taskID = ''; }">{{ showTasks ? '收起任务栏' : `会话任务 (${tasks.length})` }}</NButton>
          <NButton v-if="filesAvailable" size="small" :type="showFiles ? 'primary' : 'default'" secondary :aria-expanded="showFiles" @click="toggleFiles">项目文件</NButton>
        </div>
      </header>
      <AssistantDemoCards v-if="demoOpen && demoCards.length" :cards="demoCards" :disabled="locked" @choose="card => sendDemo(card)" />
      <p v-if="conversation && conversation.lifecycle !== 'active'" class="muted archived-banner" role="status">此主对话已归档，不能继续发送；历史消息仍保留，不会自动新建替代对话。</p>
      <p v-if="error" class="error" role="alert">{{ error }} <NButton v-if="!conversation" @click="open">重新加载</NButton></p>
      <NSkeleton v-if="opening" text :repeat="4" />
      <div v-else-if="conversation" class="panels">
        <div class="chat-column" @dragenter="dragEnter" @dragover="dragFiles($event)&&$event.preventDefault()" @dragleave="dragDepth=Math.max(0,dragDepth-1)" @drop="dropFiles">
          <ManagerSessionPanel
            v-if="chatID"
            :key="chatID"
            :workspace="activeWorkspaceId"
            :session-id="chatID"
            :read-only="conversation.lifecycle !== 'active'"
            :title="employee?.name || '主对话'"
            :subtitle="employee?.role || '对话协作 · 任务持续跟进'"
            :agent-avatar="employee?.avatar"
            :agent-id="employee?.id"
            :tasks="tasks"
            :attachment-paths="draft.paths.value"
            :attachment-ids="draft.attachmentIds.value"
            :attachment-pending="draft.unreserved.value"
            :attachment-lookup="filesAvailable"
            :uploaded-attachments="draft.uploadedAttachments.value"
            @sending="chatSending=$event"
            @sent="draft.clearSent"
            @report="tree?.refresh()"
            @changed="refreshTasks()"
            @open-task="openTask"
          >
            <template #header-prefix>
              <NButton class="back" size="small" @click="mobileChat = false">返回员工列表</NButton>
            </template>
            <template #header-actions>
              <NButton v-if="conversation" size="small" :type="showTasks ? 'primary' : 'default'" secondary @click="showFiles=false; showTasks = !showTasks; if (!showTasks) { taskFullscreen = false; taskID = ''; }">{{ showTasks ? '收起任务栏' : `会话任务 (${tasks.length})` }}</NButton>
              <NButton v-if="filesAvailable" size="small" :type="showFiles ? 'primary' : 'default'" secondary :aria-expanded="showFiles" @click="toggleFiles">项目文件</NButton>
            </template>
            <template #attachments>
              <ProjectDraftFiles v-if="filesAvailable" :draft="draft" :disabled="locked" />
            </template>
          </ManagerSessionPanel>
          <section v-else-if="conversation.lifecycle === 'active'" class="new-chat">
            <div class="new-chat-body">
              <div class="welcome-card">
                <D2EmployeeAvatar v-if="employee" :name="employee.name" :preset="employee.avatar" :identity-key="employee.id" size="lg" />
                <h3>与「{{ employee?.name }}」开始协作</h3>
                <p class="muted">{{ employee?.role ? `${employee.role}。` : '' }}暂无历史消息，请向「{{ employee?.name }}」发送工作需求。</p>
                <AssistantDemoCards v-if="demoCards.length && !assistantEntry?.experienced && !demoOpen" :cards="demoCards" :disabled="locked" @choose="card => sendDemo(card)" />
                <div v-else class="quick-prompts">
                  <button type="button" class="prompt-chip" @click="firstText = '帮我梳理当前工作的核心进度与待办事项'">梳理核心进度与待办 ↗</button>
                  <button type="button" class="prompt-chip" @click="firstText = '请先查看项目目录文件并汇报关键发现'">查看项目目录并汇报 ↗</button>
                </div>
              </div>
            </div>
            <footer class="new-chat-footer">
              <div class="composer-card">
                <div v-if="filesAvailable" class="composer-attachments">
                  <ProjectDraftFiles :draft="draft" :disabled="locked" />
                </div>
                <NInput v-model:value="firstText" type="textarea" :autosize="{ minRows: 2, maxRows: 5 }" :disabled="sending || !employee?.enabled" placeholder="输入工作需求，任务执行期间仍可继续对话..." @keydown.enter.exact="onFirstEnter" />
                <div class="composer-actions">
                  <span class="composer-hint">Enter 发送 · Shift + Enter 换行</span>
                  <NButton size="small" type="primary" :loading="sending" :disabled="draft.unreserved.value || (!firstText.trim() && !draft.paths.value.length && !draft.attachmentIds.value.length) || sending || !employee?.enabled" @click="firstSend">{{ error ? '重试发送' : '发送' }}</NButton>
                </div>
              </div>
            </footer>
          </section>
          <div v-if="dragDepth>0" class="drop-hint" role="status"><span>↑</span><strong>松开以添加附件</strong><small>支持文件和图片</small></div>
        </div>
        <ProjectFileTree v-if="treeMounted&&filesAvailable" v-show="showFiles" ref="tree" :key="conversation.id" :workspace="activeWorkspaceId" :conversation="conversation.id" :read-only="locked" @close="showFiles=false" @reference="draft.reference" />
        <aside v-if="showTasks" class="tasks" :class="{ 'has-detail': taskID, 'is-fullscreen': taskFullscreen && !!taskID && !!detailTaskID }">
          <div class="task-list-heading">
            <div class="task-heading-row">
              <h3>本会话任务 <span>{{ tasks.length }}</span></h3>
              <NButton v-if="taskID" size="tiny" secondary @click="taskID = ''">返回任务列表</NButton>
            </div>
            <p v-if="!taskID" class="muted">任务可并行运行，本轮结束后仍可继续。</p>
            <p v-if="tasksError" class="error">{{ tasksError }}</p>
          </div>
          <div class="task-list">
            <button v-for="task in tasks" :key="task.id" :class="['task', { selected: taskID === task.id }]" @click="openTask(task.task_id ? {kind:'task',id:task.task_id} : {kind:'session',id:task.id})">
              <strong>{{ task.title || '任务' }}</strong>
              <small>{{ workItemProgress(task.execution) || (task.execution.required_actions?.length ? '待确认' : sessionStatus(task.execution.state)) }} · {{ sessionStatus(task.lifecycle) }}</small>
            </button>
            <p v-if="!tasks.length" class="muted">尚未触发任务</p>
            <NButton v-if="taskCursor" size="small" @click="refreshTasks(true)">加载更多任务</NButton>
          </div>
          <div v-if="taskID && detailTaskID" class="task-detail">
            <TaskDetailPanel :key="detailTaskID" v-model:fullscreen="taskFullscreen" :workspace-id="activeWorkspaceId" :task-id="detailTaskID" :initial-turn-id="detailTurnID" presentation="sidebar" @task-changed="refreshTasks()" @close="taskFullscreen = false; taskID = ''" @open-source-conversation="taskFullscreen = false" />
          </div>
        </aside>
      </div>
    </main>

    <NModal v-model:show="newChatOpen" preset="card" title="发起 1:1 私聊对话" style="width: min(440px, 94vw)">
      <div class="new-chat-modal-body">
        <p class="muted" style="margin:0 0 10px">仅展示工作区可用 AI 员工；尚未开启会话的员工优先排在前方，点击即可加入左侧列表。</p>
        <NInput v-model:value="modalSearch" size="small" clearable placeholder="搜索 AI 员工姓名或岗位..." style="margin-bottom:10px" />
        <div class="modal-agent-list">
          <button v-for="agent in modalAgents" :key="agent.id" type="button" class="modal-agent-item" @click="startAgentChat(agent.id)">
            <D2EmployeeAvatar :name="agent.name" :preset="agent.avatar" :identity-key="agent.id" size="sm" />
            <div class="modal-agent-info">
              <strong>{{ agent.name }}</strong>
              <small>{{ agent.role || 'AI 员工' }}</small>
            </div>
            <span class="modal-agent-cue">{{ startedIds.includes(agent.id) ? '打开会话 →' : '开启私聊 →' }}</span>
          </button>
          <p v-if="!modalAgents.length" class="muted" style="text-align:center;padding:16px 0">没有匹配的 AI 员工</p>
        </div>
      </div>
    </NModal>
  </div>
</template>
<style scoped>
.conversation-workspace{display:flex;gap:14px;height:calc(100vh - 120px);min-height:540px;color:var(--id-text-primary)}
.employee-list{width:248px;flex-shrink:0;border:1px solid var(--id-border-subtle);border-radius:var(--id-radius-lg);background:var(--id-bg-surface);display:flex;flex-direction:column;overflow:hidden}
.sidebar-top{padding:12px 12px 10px;border-bottom:1px solid var(--id-border-subtle);background:var(--id-bg-subtle);display:flex;flex-direction:column;gap:8px}
.sidebar-title-row{display:flex;align-items:center;justify-content:space-between;gap:8px}
h2{font-size:14px;font-weight:600;margin:0;display:flex;align-items:center;gap:6px}
.sidebar-count-pill{font-size:11px;font-family:var(--id-font-mono);font-variant-numeric:tabular-nums;padding:1px 7px;border-radius:var(--id-radius-pill);background:var(--id-bg-muted);border:1px solid var(--id-border-subtle);color:var(--id-text-secondary);font-weight:500}
h3{font-size:14px;margin:0}
.sidebar-items{flex:1;overflow:auto;padding:8px;display:flex;flex-direction:column;gap:4px}
.sidebar-empty{padding:24px 12px;text-align:center;display:flex;flex-direction:column;align-items:center;gap:8px}
.employee,.task{display:flex;align-items:flex-start;gap:10px;width:100%;padding:10px;text-align:left;cursor:pointer;background:transparent;border:1px solid transparent;border-radius:var(--id-radius-md);color:var(--id-text-primary);transition:background-color var(--id-transition-fast),border-color var(--id-transition-fast)}
.employee:hover,.task:hover{background:var(--id-bg-hover)}
.employee.selected,.task.selected{background:var(--id-bg-muted);border-color:var(--id-border-strong)}
.emp-meta{flex:1;min-width:0;display:flex;flex-direction:column;gap:2px}
.emp-name-row{display:flex;align-items:center;gap:6px;min-width:0}
.employee strong,.task strong{display:block;font-size:13px;font-weight:600;overflow:hidden;text-overflow:ellipsis;white-space:nowrap}
.mini-ai{font-size:10px;font-family:var(--id-font-mono);color:var(--id-text-secondary);background:var(--id-bg-surface);border:1px solid var(--id-border-subtle);padding:0 5px;border-radius:var(--id-radius-sm);flex-shrink:0}
.employee small,.task small{display:block;font-size:12px;color:var(--id-text-muted);overflow:hidden;text-overflow:ellipsis;white-space:nowrap}
.emp-running-tag{color:var(--id-accent-primary) !important;font-weight:500;margin-top:2px}
.chat-area{flex:1;min-width:0;display:flex;flex-direction:column;gap:10px;overflow:hidden}
.unified-fallback-header{display:flex;align-items:center;gap:12px;padding:12px 18px;border:1px solid var(--id-border-subtle);border-radius:var(--id-radius-lg);background:var(--id-bg-surface);flex-wrap:wrap}
.fallback-heading{flex:1;min-width:120px;display:flex;flex-direction:column;gap:2px}
.fallback-heading strong{font-size:14px;font-weight:600}
.fallback-actions{display:flex;align-items:center;gap:8px;flex-wrap:wrap}
.archived-banner{margin:0;padding:8px 14px;border:1px solid var(--id-border-subtle);border-radius:var(--id-radius-md);background:var(--id-bg-surface)}
.panels{display:flex;gap:12px;flex:1;min-height:420px;min-width:0}
.chat-column{position:relative;display:flex;flex:1;min-width:0;min-height:0}
.drop-hint{pointer-events:none;position:absolute;z-index:2;bottom:145px;left:16px;right:16px;padding:32px;display:grid;justify-items:center;gap:8px;border:2px dashed var(--id-accent-primary);border-radius:12px;background:var(--id-bg-surface);color:var(--id-accent-primary);box-shadow:var(--id-shadow-lg)}
.drop-hint span{font-size:32px}.drop-hint strong{font-size:18px}
.new-chat{flex:1;border:1px solid var(--id-border-subtle);border-radius:var(--id-radius-lg);background:var(--id-bg-surface);display:flex;flex-direction:column;overflow:hidden;min-height:420px}
.new-chat-body{flex:1;display:flex;align-items:center;justify-content:center;padding:24px;background:var(--id-bg-subtle);overflow:auto}
.welcome-card{max-width:440px;text-align:center;display:flex;flex-direction:column;align-items:center;gap:10px}
.welcome-card h3{font-size:16px;font-weight:600;color:var(--id-text-primary);margin-top:4px}
.welcome-card p{margin:0;line-height:1.6}
.quick-prompts{display:flex;flex-wrap:wrap;justify-content:center;gap:8px;margin-top:6px}
.prompt-chip{border:1px solid var(--id-border-subtle);background:var(--id-bg-surface);color:var(--id-text-secondary);border-radius:var(--id-radius-pill);padding:5px 12px;font-size:12px;cursor:pointer;transition:border-color var(--id-transition-fast),color var(--id-transition-fast)}
.prompt-chip:hover{border-color:var(--id-accent-primary);color:var(--id-accent-primary)}
.new-chat-footer{padding:10px 18px 14px;background:var(--id-bg-subtle);border-top:1px solid var(--id-border-subtle)}
.composer-card{background:var(--id-bg-surface);border:1px solid var(--id-border-subtle);border-radius:12px;padding:10px 12px;box-shadow:var(--id-shadow-sm);transition:border-color var(--id-transition-fast),box-shadow var(--id-transition-fast)}
.composer-card:focus-within{border-color:var(--id-accent-primary-border);box-shadow:var(--id-focus-ring)}
.composer-attachments{padding-bottom:6px;margin-bottom:4px;border-bottom:1px solid var(--id-border-hairline)}
.composer-card :deep(.n-input){--n-border:none !important;--n-border-hover:none !important;--n-border-focus:none !important;--n-box-shadow-focus:none !important;--n-color:transparent !important}
.composer-actions{display:flex;align-items:center;justify-content:flex-end;gap:8px;margin-top:8px;padding-top:8px;border-top:1px solid var(--id-border-hairline)}
.composer-hint{margin-right:auto;color:var(--id-text-muted);font-size:11px}
.tasks{width:260px;flex-shrink:0;border:1px solid var(--id-border-subtle);border-radius:var(--id-radius-lg);background:var(--id-bg-surface);overflow:hidden;display:flex;flex-direction:column}
.tasks.has-detail{width:clamp(380px,34vw,480px)}
.task-list-heading{padding:12px 14px 8px;border-bottom:1px solid var(--id-border-hairline)}
.task-heading-row{display:flex;align-items:center;justify-content:space-between;gap:8px}
.task-list-heading p{margin:4px 0 0}
.task-list-heading h3 span{color:var(--id-text-muted);font-size:11px;margin-left:5px}
.task-list{padding:8px;overflow:auto;min-height:0;display:flex;flex-direction:column;gap:4px}
.has-detail .task-list{flex-direction:row;flex-wrap:nowrap;overflow-x:auto;overflow-y:hidden;max-height:54px;flex-shrink:0;padding:6px 10px;background:var(--id-bg-subtle);border-bottom:1px solid var(--id-border-subtle)}
.task{flex-direction:column;align-items:flex-start;gap:2px;padding:9px 10px;border:1px solid var(--id-border-subtle);background:var(--id-bg-surface)}
.has-detail .task{width:auto;max-width:190px;flex-shrink:0;padding:5px 10px}
.has-detail .task.selected{border-color:var(--id-accent-primary);background:var(--id-accent-primary-soft)}
.has-detail .task small{display:none}
.task-detail{flex:1;min-height:0;display:flex}
.task-detail :deep(.session-panel){border:0;border-radius:0;min-height:340px}
.task-detail :deep(.messages){padding:16px}
.task-detail :deep(.composer-hint){display:none}
.modal-agent-list{display:flex;flex-direction:column;gap:6px;max-height:320px;overflow:auto}
.modal-agent-item{display:flex;align-items:center;gap:10px;width:100%;padding:9px 10px;border-radius:var(--id-radius-md);border:1px solid var(--id-border-subtle);background:var(--id-bg-surface);text-align:left;cursor:pointer;color:inherit;transition:border-color var(--id-transition-fast),background-color var(--id-transition-fast)}
.modal-agent-item:hover{border-color:var(--id-accent-primary);background:var(--id-bg-subtle)}
.modal-agent-info{flex:1;min-width:0;display:flex;flex-direction:column;gap:2px}
.modal-agent-info strong{font-size:13px;font-weight:600}
.modal-agent-info small{font-size:12px;color:var(--id-text-muted);overflow:hidden;text-overflow:ellipsis;white-space:nowrap}
.modal-agent-cue{font-size:12px;color:var(--id-accent-primary);font-weight:500;flex-shrink:0}
.muted{font-size:12px;color:var(--id-text-muted)}
.error{font-size:13px;color:var(--id-status-danger)}
.back{display:none}
button:focus-visible{outline:2px solid var(--id-accent-primary);outline-offset:2px}
@media(max-width:1400px){.employee-list{width:210px}}
@media(max-width:1200px){.chat-column{min-height:480px}.panels{flex-direction:column}.panels>.session-panel{height:600px;flex:none}.tasks,.tasks.has-detail{width:auto;flex-shrink:0}.task-list{max-height:220px}.task-detail{height:560px;flex:none}}
@media(max-width:640px){.conversation-workspace{height:calc(100vh - 100px)}.employee-list{width:100%}.chat-area{display:none}.mobile-chat .employee-list{display:none}.mobile-chat .chat-area{display:flex}.back{display:inline-flex}.panels>.session-panel{height:calc(100vh - 200px);min-height:420px}}
.tasks.is-fullscreen{position:fixed;inset:0;z-index:1200;width:100vw;height:100dvh;max-width:none;border:0;border-radius:0}
.tasks.is-fullscreen .task-list-heading,.tasks.is-fullscreen .task-list{display:none}
.tasks.is-fullscreen .task-detail{flex:1;height:100%;min-height:0;border-top:0}
.tasks.is-fullscreen .task-detail :deep(.composer-hint){display:inline}
</style>

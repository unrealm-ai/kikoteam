<script setup lang="ts">
import { copyText } from '../utils/clipboard';
import WorkItemTimeline from './WorkItemTimeline.vue';
import D2EmployeeAvatar from './D2EmployeeAvatar.vue';
import { computed, onMounted, onUnmounted, ref, watch } from 'vue';
import { NButton, NInput, NSkeleton } from 'naive-ui';
import { taskApi, type TaskDetail, type TaskInput, type TaskReport } from '../api/tasks';
import type { TaskArtifact } from '../api/public-task';
import type { Page } from '../api/manager';
import { D2ApiError } from '../api/client';
import ManagerSessionPanel from './ManagerSessionPanel.vue';
import SessionMarkdown from './SessionMarkdown.vue';
import TaskSupplementReceipts from './TaskSupplementReceipts.vue';
import TaskProgressSummary from './TaskProgressSummary.vue';
import { usePublicTaskDetail } from '../composables/usePublicTaskDetail';
import { sessionStatus, taskPhaseLabel, workItemProgress } from '../utils/session-presentation';
import { classifyArtifact, fileExtension, formatArtifactSize, parseCsvRows } from '../utils/artifact-preview';

const props = withDefaults(defineProps<{
  workspaceId?: string;
  taskId?: string;
  publicToken?: string;
  taskCenter?: boolean;
  taskRecordActions?: boolean;
  initialTurnId?: string | undefined;
  presentation?: 'sidebar' | 'drawer' | 'page';
  fullscreen?: boolean;
}>(), {
  workspaceId: '',
  taskId: '',
  publicToken: '',
  fullscreen: false,
});

const emit = defineEmits<{
  close: [];
  taskChanged: [];
  openWork: [id: string];
  openSourceConversation: [agent: string, conversation: string];
  'update:fullscreen': [value: boolean];
}>();

const guest = props.publicToken ? usePublicTaskDetail(props.publicToken) : undefined;
const guestTask = computed(() => guest?.task.value);
const visibleError = computed(() => guest ? guest.error.value : error.value);
const visibleLoading = computed(() => guest ? guest.loading.value : loading.value);
function reload() { return guest ? guest.load() : load(); }

const detail = ref<TaskDetail | null>(null);
const loading = ref(false);
const error = ref('');
const actionError = ref('');
const notice = ref('');
const progress = computed(() => guestTask.value?.progress || detail.value?.progress);
const artifacts = computed<TaskArtifact[]>(() => guest ? (guestTask.value?.artifacts || []) : (detail.value?.artifacts || []));

const isFullscreen = ref(Boolean(props.fullscreen));
watch(() => props.fullscreen, v => { isFullscreen.value = Boolean(v); });
function setFullscreen(value: boolean) {
  isFullscreen.value = value;
  emit('update:fullscreen', value);
}
function toggleFullscreen() {
  setFullscreen(!isFullscreen.value);
}

const showDualSurface = computed(() => Boolean(guest) || props.presentation === 'page' || isFullscreen.value);
const narrowQuery = typeof window !== 'undefined' ? window.matchMedia('(max-width: 960px)') : undefined;
const narrow = ref(Boolean(narrowQuery?.matches));
const mobileSurface = ref(false);
let previewExpandedDrawer = false;
function updateNarrow(event: MediaQueryListEvent) {
  narrow.value = event.matches;
  if (!event.matches) mobileSurface.value = false;
}
function openMobileSurface() { mobileSurface.value = true; }
function closeMobileSurface() {
  mobileSurface.value = false;
  if (previewExpandedDrawer) setFullscreen(false);
  previewExpandedDrawer = false;
}
watch(isFullscreen, expanded => {
  if (!expanded && !guest) {
    mobileSurface.value = false;
    previewExpandedDrawer = false;
  }
});
const tab = ref<'process' | 'artifacts' | 'status' | 'inputs' | 'reports' | 'work'>('process');
const surfaceTab = ref<'artifacts' | 'status' | 'inputs' | 'reports' | 'work'>('artifacts');

const turn = ref(props.initialTurnId || '');
const inputs = ref<TaskInput[]>([]);
const reports = ref<TaskReport[]>([]);
const historicalReport = ref<TaskReport | null>(null);
const inputCursor = ref<string | null>(null);
const reportCursor = ref<string | null>(null);
const renameOpen = ref(false);
const title = ref('');
const renameRevision = ref('');
const busy = ref(false);
const refreshing = ref(false);
let generation = 0;
let controller = new AbortController();
let timer: ReturnType<typeof setInterval> | undefined;
let refreshAfterWrite = false;
let initialized = false;
let lastEventRefresh = 0;
let historicalRevision = 0;

const latestReport = computed(() => {
  const id = detail.value?.latest_turn?.id;
  return id ? reports.value.find(r => r.source_turn_id === id) ?? null : null;
});
const visibleReports = computed(() => {
  const selected = historicalReport.value;
  return selected && !reports.value.some(report => report.source_input_id === selected.source_input_id)
    ? [...reports.value, selected]
    : reports.value;
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

const duration = computed(() => {
  const t = detail.value?.latest_turn;
  if (!t?.finished_at) return '';
  const seconds = (Date.parse(t.finished_at) - Date.parse(t.started_at)) / 1000;
  return seconds >= 0 ? `${seconds.toFixed(1)} 秒` : '';
});

const recordsEditable = computed(() => !props.taskCenter || props.taskRecordActions);
const canRename = computed(() => recordsEditable.value && detail.value?.actions.rename.allowed);
const canWithdraw = (input: TaskInput) => recordsEditable.value && input.status === 'queued' && !input.confirmation_request_id;
const canSupplement = computed(() => detail.value?.accepts_input && detail.value.actions.supplement?.allowed !== false);
const canArchive = computed(() => detail.value?.actions.archive.allowed);
const planned = computed(() => detail.value?.planning_state === 'planned' || detail.value?.planning_state === 'canceled');
const executionLabel = computed(() => detail.value?.execution.block_reason === 'engine_recovery_pending'
  ? taskPhaseLabel(detail.value.execution.block_reason)
  : detail.value?.planning_state === 'planned'
    ? '待办：尚未启动'
    : detail.value?.planning_state === 'canceled'
      ? '待办已取消（未执行）'
      : workItemProgress(detail.value?.execution) || (detail.value?.execution.required_actions?.length ? '待确认' : detail.value?.execution.state === 'idle' ? '当前无执行轮次' : sessionStatus(detail.value?.execution.state)));
const primaryPhase = computed(() => progress.value?.phase || guestTask.value?.status || detail.value?.execution.state || '');
const primaryStatusLabel = computed(() => progress.value?.phase
  ? taskPhaseLabel(progress.value.phase)
  : guestTask.value
    ? taskPhaseLabel(guestTask.value.status)
    : executionLabel.value);

const reportStatus: Record<string, string> = {
  pending: '等待回报补偿',
  admitted: '已提交到主会话',
  suppressed: '未回报到主会话',
  not_enabled: '历史输入未启用回报',
  not_applicable: '无来源主会话',
};

function fail(e: unknown) { return e instanceof Error ? e.message : '加载失败，请重试'; }

async function loadedPages<T>(read: (cursor: string) => Promise<Page<T>>, count: number) {
  const items: T[] = [], seen = new Set<string>();
  let cursor = '';
  for (;;) {
    const page = await read(cursor);
    items.push(...page.items);
    if (!page.next_cursor || items.length >= Math.max(count, 1)) return { items, next_cursor: page.next_cursor };
    if (seen.has(page.next_cursor) || page.next_cursor === cursor) throw new Error('分页游标重复');
    seen.add(page.next_cursor);
    cursor = page.next_cursor;
  }
}

async function refresh(reset = false) {
  if (refreshing.value || !props.taskId || document.hidden) return;
  const g = generation, w = props.workspaceId, id = props.taskId;
  refreshing.value = true;
  try {
    const d = await taskApi.detail(w, id, controller.signal);
    if (g !== generation) return;
    detail.value = d;
    reset = reset || !initialized;
    const signal = controller.signal;
    const [i, r] = await Promise.all([
      loadedPages(c => taskApi.inputs(w, d.session_id, c, signal), reset ? 0 : inputs.value.length),
      loadedPages(c => taskApi.reports(w, id, c, signal), reset ? 0 : reports.value.length),
    ]);
    if (g !== generation) return;
    inputs.value = i.items;
    reports.value = r.items;
    inputCursor.value = i.next_cursor;
    reportCursor.value = r.next_cursor;
    initialized = true;
    void refreshHistoricalReport();
    error.value = '';
  } catch (e) {
    if (g === generation) {
      error.value = fail(e);
      if (e instanceof D2ApiError && [401, 403, 404].includes(e.status)) {
        detail.value = null;
        inputs.value = [];
        reports.value = [];
        historicalReport.value = null;
        controller.abort();
      }
    }
  } finally {
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

async function load() {
  generation++;
  refreshAfterWrite = false;
  controller.abort();
  controller = new AbortController();
  initialized = false;
  detail.value = null;
  inputs.value = [];
  reports.value = [];
  historicalReport.value = null;
  error.value = '';
  actionError.value = '';
  notice.value = '';
  turn.value = props.initialTurnId || '';
  tab.value = 'process';
  surfaceTab.value = 'artifacts';
  mobileSurface.value = false;
  previewExpandedDrawer = false;
  clearArtifactPreview();
  renameOpen.value = false;
  refreshing.value = false;
  loading.value = true;
  inputCursor.value = null;
  reportCursor.value = null;
  await refresh(true);
}

async function more(kind: 'inputs' | 'reports') {
  const d = detail.value, g = generation;
  if (!d || busy.value || refreshing.value) return;
  busy.value = true;
  try {
    const cursor = kind === 'inputs' ? inputCursor.value : reportCursor.value;
    if (!cursor) return;
    if (kind === 'inputs') {
      const p = await taskApi.inputs(props.workspaceId, d.session_id, cursor, controller.signal);
      if (g !== generation) return;
      if (p.next_cursor === cursor) throw new Error('输入分页游标重复');
      inputs.value = [...new Map([...inputs.value, ...p.items].map(v => [v.id, v])).values()];
      inputCursor.value = p.next_cursor;
    } else {
      const p = await taskApi.reports(props.workspaceId, props.taskId, cursor, controller.signal);
      if (g !== generation) return;
      if (p.next_cursor === cursor) throw new Error('回报分页游标重复');
      reports.value = [...new Map([...reports.value, ...p.items].map(v => [v.source_input_id, v])).values()];
      reportCursor.value = p.next_cursor;
    }
  } catch (e) {
    if (g === generation) actionError.value = fail(e);
  } finally {
    if (g === generation) busy.value = false;
  }
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
  const d = detail.value, g = generation;
  if (!d || busy.value) return;
  if (kind === 'archive' && !canArchive.value) return;
  if (kind === 'rename' && (!canRename.value || !title.value.trim() || !renameRevision.value)) return;
  if (kind === 'withdraw' && !inputs.value.some(item => item.id === input && canWithdraw(item))) return;
  busy.value = true;
  actionError.value = '';
  notice.value = '';
  try {
    if (kind === 'rename') await taskApi.rename(props.workspaceId, d.session_id, title.value.trim(), renameRevision.value);
    else if (kind === 'archive') await taskApi.archive(props.workspaceId, d.session_id);
    else await taskApi.withdraw(props.workspaceId, d.session_id, input);
    if (g !== generation) return;
    renameOpen.value = false;
    notice.value = kind === 'archive' ? '任务已归档，历史仍可查看' : kind === 'withdraw' ? '输入已撤回' : '标题已更新';
    await commandAccepted();
  } catch (e) {
    if (g === generation) {
      actionError.value = fail(e);
      await refresh();
    }
  } finally {
    if (g === generation) busy.value = false;
  }
}

async function commandAccepted() {
  if (guest) return;
  emit('taskChanged');
  if (refreshing.value) refreshAfterWrite = true;
  else await refresh();
}

function changed() {
  emit('taskChanged');
  if (Date.now() - lastEventRefresh > 1000) {
    lastEventRefresh = Date.now();
    void refresh();
  }
}

async function copyID() {
  try {
    await copyText(props.taskId);
    notice.value = '任务 ID 已复制';
  } catch {
    actionError.value = '复制失败';
  }
}

function visible() { if (!document.hidden) void refresh(); }

// Right Surface: Artifact Selection & Inline Preview
const PREVIEW_TEXT_LIMIT = 2 * 1024 * 1024;
const selectedArtifact = ref<TaskArtifact | null>(null);
const artifactViewMode = ref<'preview' | 'source'>('preview');
const previewLoading = ref(false);
const previewError = ref('');
const previewText = ref('');
const previewImageUrl = ref('');
const previewCsvRows = ref<string[][]>([]);
const downloadBusyId = ref('');
let previewSeq = 0;

function revokePreviewImage() {
  if (previewImageUrl.value) {
    URL.revokeObjectURL(previewImageUrl.value);
    previewImageUrl.value = '';
  }
}

function clearArtifactPreview() {
  previewSeq++;
  selectedArtifact.value = null;
  artifactViewMode.value = 'preview';
  previewLoading.value = false;
  previewError.value = '';
  previewText.value = '';
  previewCsvRows.value = [];
  revokePreviewImage();
}

async function copyArtifactContent() {
  if (!previewText.value) return;
  try {
    await copyText(previewText.value);
    notice.value = '产物内容已复制';
  } catch {
    actionError.value = '复制失败';
  }
}

async function readArtifactBlob(file: TaskArtifact): Promise<Blob> {
  if (guest) return guest.readArtifact(file);
  return taskApi.artifactBlob(props.workspaceId, props.taskId, file.id, controller.signal);
}

async function selectArtifactForPreview(file: TaskArtifact, reveal = true) {
  if (reveal && !guest && props.presentation !== 'page' && !isFullscreen.value) {
    previewExpandedDrawer = narrow.value;
    setFullscreen(true);
  }
  if (reveal && narrow.value) openMobileSurface();
  surfaceTab.value = 'artifacts';
  tab.value = showDualSurface.value ? 'process' : 'artifacts';
  selectedArtifact.value = file;
  artifactViewMode.value = 'preview';
  const seq = ++previewSeq;
  previewError.value = '';
  previewText.value = '';
  previewCsvRows.value = [];
  revokePreviewImage();

  const meta = classifyArtifact(file);
  if (!meta.previewable) return;
  if (file.size > PREVIEW_TEXT_LIMIT) {
    previewError.value = '文件超过 2 MB，请下载后查看完整内容。';
    return;
  }

  previewLoading.value = true;
  try {
    const blob = await readArtifactBlob(file);
    if (seq !== previewSeq) return;
    if (meta.kind === 'image') {
      previewImageUrl.value = URL.createObjectURL(blob);
      return;
    }
    const content = await blob.text();
    if (seq !== previewSeq) return;
    previewText.value = content;
    if (meta.kind === 'csv') {
      const delimiter = fileExtension(file.name) === 'tsv' ? '\t' : ',';
      previewCsvRows.value = parseCsvRows(content, delimiter, 200);
    }
  } catch (e) {
    if (seq === previewSeq) {
      previewError.value = e instanceof Error ? e.message : '无法加载产物预览';
    }
  } finally {
    if (seq === previewSeq) previewLoading.value = false;
  }
}

async function downloadArtifact(file: TaskArtifact) {
  if (downloadBusyId.value) return;
  downloadBusyId.value = file.id;
  actionError.value = '';
  try {
    if (guest) await guest.download(file);
    else await taskApi.downloadArtifact(props.workspaceId, props.taskId, file, controller.signal);
  } catch (e) {
    actionError.value = e instanceof Error ? e.message : '产物下载失败';
  } finally {
    downloadBusyId.value = '';
  }
}

function pickDefaultArtifact(list: TaskArtifact[]): TaskArtifact | undefined {
  return list.find(item => classifyArtifact(item).previewable) ?? list[0];
}

watch(artifacts, list => {
  if (!list.length) {
    if (selectedArtifact.value) clearArtifactPreview();
    return;
  }
  if (selectedArtifact.value && list.some(item => item.id === selectedArtifact.value?.id)) return;
  if (showDualSurface.value && (!narrow.value || mobileSurface.value) && !selectedArtifact.value) {
    const preferred = pickDefaultArtifact(list);
    if (preferred) void selectArtifactForPreview(preferred, false);
  }
}, { immediate: true });

watch([showDualSurface, narrow, mobileSurface], ([dual, isNarrow, mobileOpen]) => {
  if (dual && (!isNarrow || mobileOpen) && !selectedArtifact.value && artifacts.value.length) {
    const preferred = pickDefaultArtifact(artifacts.value);
    if (preferred) void selectArtifactForPreview(preferred, false);
  }
});

function selectTab(next: 'process' | 'artifacts' | 'status' | 'inputs' | 'reports' | 'work') {
  if (showDualSurface.value) {
    if (next === 'process') {
      tab.value = 'process';
      return;
    }
    surfaceTab.value = next;
    return;
  }
  tab.value = next;
  if (next === 'artifacts' && !selectedArtifact.value && artifacts.value.length) {
    const preferred = pickDefaultArtifact(artifacts.value);
    if (preferred) void selectArtifactForPreview(preferred, false);
  }
}

type TabId = 'process' | 'artifacts' | 'status' | 'inputs' | 'reports' | 'work';

const navTabs = computed(() => {
  if (guest) {
    return [
      { id: 'artifacts' as const, label: artifacts.value.length ? `产物详情 (${artifacts.value.length})` : '产物详情' },
      { id: 'status' as const, label: '任务状态' },
    ];
  }
  const base: Array<{ id: TabId; label: string }> = showDualSurface.value
    ? [
        { id: 'artifacts', label: artifacts.value.length ? `产物详情 (${artifacts.value.length})` : '产物详情' },
        { id: 'status', label: '任务状态' },
        { id: 'inputs', label: '输入记录' },
        { id: 'reports', label: '主会话回报' },
      ]
    : [
        { id: 'process', label: '执行过程' },
        { id: 'artifacts', label: artifacts.value.length ? `产物详情 (${artifacts.value.length})` : '产物详情' },
        { id: 'status', label: '任务状态' },
        { id: 'inputs', label: '输入记录' },
        { id: 'reports', label: '主会话回报' },
      ];
  if (detail.value?.work_item_id) base.push({ id: 'work', label: '事项时间线' });
  return base;
});

function isTabActive(id: 'process' | 'artifacts' | 'status' | 'inputs' | 'reports' | 'work') {
  return showDualSurface.value ? surfaceTab.value === id : tab.value === id;
}

if (!guest) watch(() => [props.workspaceId, props.taskId], () => { busy.value = false; void load(); }, { immediate: true });
watch(() => props.initialTurnId, v => { turn.value = v || ''; });
watch(turn, () => { historicalRevision++; historicalReport.value = null; void refreshHistoricalReport(); });
onMounted(() => {
  narrowQuery?.addEventListener('change', updateNarrow);
  if (guest) return;
  timer = setInterval(() => { void refresh(); }, 5000);
  document.addEventListener('visibilitychange', visible);
});
onUnmounted(() => {
  narrowQuery?.removeEventListener('change', updateNarrow);
  generation++;
  controller.abort();
  revokePreviewImage();
  if (timer) clearInterval(timer);
  document.removeEventListener('visibilitychange', visible);
});
</script>

<template>
  <section
    class="task-detail-panel"
    :class="[presentation, { 'task-drawer-body': taskCenter, 'dual-surface': showDualSurface, 'is-fullscreen': isFullscreen }]"
    aria-label="任务详情"
  >
    <!-- Compact Agent Session Header -->
    <header class="session-topbar">
      <div class="topbar-left">
        <NButton v-if="!guest && isFullscreen" size="small" secondary @click="setFullscreen(false)">
          ← {{ taskCenter ? '返回任务列表' : '返回对话' }}
        </NButton>
        <D2EmployeeAvatar
          v-if="detail"
          :name="detail.agent.name"
          :preset="detail.agent.avatar"
          :identity-key="detail.agent.id"
          size="sm"
        />
        <div class="task-summary">
          <div class="title-row">
            <h2>{{ (detail?.title ?? guestTask?.title) || '未命名任务' }}</h2>
            <span class="status-badge" :class="primaryPhase">
              {{ primaryStatusLabel }}
            </span>
            <span v-if="guestTask?.permission === 'read'" class="meta-chip">只读</span>
            <span v-if="guestTask" class="meta-chip connection-chip">{{ guest?.connection.value || '正在连接' }}</span>
          </div>
          <div class="sub-row">
            <template v-if="detail">
              <span>{{ detail.agent.name }}</span>
              <span>·</span>
              <span>{{ detail.task_space_id ? '任务空间' : detail.source.kind === 'chat_delegation' ? '由主对话触发' : '独立任务' }}</span>
              <span>·</span>
              <span>{{ sessionStatus(detail.lifecycle) }}</span>
              <template v-if="detail.latest_turn"><span>·</span><span>最近：{{ sessionStatus(detail.latest_turn.status) }}</span></template>
              <template v-if="duration"><span>·</span><span>用时 {{ duration }}</span></template>
            </template>
            <template v-else>
              <span class="eyebrow">授权任务工作台</span>
            </template>
          </div>
        </div>
      </div>

      <div class="topbar-actions">
        <NButton v-if="narrow && showDualSurface && (detail || guestTask)" size="tiny" secondary
          @click="mobileSurface ? closeMobileSurface() : openMobileSurface()">
          {{ mobileSurface ? '返回执行过程' : '查看产物与状态' }}
        </NButton>
        <template v-if="detail">
          <NButton
            v-if="detail.source.chat_session_id && detail.conversation_id && presentation !== 'sidebar'"
            size="tiny"
            secondary
            @click="emit('openSourceConversation', detail.agent.id, detail.conversation_id)"
          >
            返回来源对话
          </NButton>
          <NButton v-if="recordsEditable" size="tiny" secondary :disabled="!canRename || busy" @click="toggleRename">修改标题</NButton>
          <NButton size="tiny" secondary :disabled="!canArchive || busy" @click="action('archive')">归档任务</NButton>
        </template>
        <NButton v-if="!guest && presentation !== 'page'" size="tiny" secondary @click="toggleFullscreen">
          {{ isFullscreen ? '⤡ 退出全屏' : `⤢ 放大全屏${artifacts.length ? ` · 产物 ${artifacts.length}` : ''}` }}
        </NButton>
        <NButton v-if="!guest" size="small" @click="emit('close')">关闭任务详情</NButton>
      </div>
    </header>

    <!-- Inline Rename Bar & Lightweight Runtime Banners -->
    <div v-if="renameOpen && recordsEditable && detail" class="rename rename-strip">
      <NInput v-model:value="title" size="small" maxlength="200" placeholder="任务标题" />
      <NButton size="small" type="primary" :disabled="!canRename || !title.trim() || busy" @click="action('rename')">保存标题</NButton>
      <NButton size="small" @click="renameOpen = false">取消</NButton>
    </div>

    <div v-if="visibleError" class="banner error" role="alert">
      {{ visibleError }} <NButton size="tiny" @click="reload">重新加载</NButton>
    </div>
    <NSkeleton v-if="visibleLoading" text :repeat="4" style="padding: 16px;" />

    <template v-if="detail || guestTask">
      <div v-if="detail && (detail.execution.queued_input_count || detail.execution.background_process_count === null || detail.execution.background_process_count || detail.latest_turn?.has_unknown_tool_results || planned)" class="runtime-strips">
        <span v-if="detail.execution.queued_input_count" class="strip-pill">{{ detail.execution.queued_input_count }} 条输入排队中</span>
        <span v-if="detail.execution.background_process_count === null || detail.execution.background_process_count" class="strip-pill">
          {{ detail.execution.background_process_count === null ? '后台活动待核对' : `${detail.execution.background_process_count} 个后台进程仍在运行` }}
        </span>
        <span v-if="detail.latest_turn?.has_unknown_tool_results" class="strip-pill warn">存在结果未知的工具调用，请查看执行过程</span>
        <span v-if="planned" class="strip-pill warn" role="status">
          {{ detail.planning_state === 'planned' ? '仅保存了待办，没有执行输入或模型运行。请返回来源主对话查询、补充、启动或取消待办。' : '此待办在启动前已取消，没有执行结果，也不能继续或恢复。' }}
        </span>
      </div>
      <p v-if="actionError" class="banner error" role="alert">{{ actionError }}</p>
      <p v-if="notice" class="banner muted" role="status">{{ notice }}</p>

      <!-- Drawer Single-Column Mode: Only show nav strip when not dual-surface -->
      <nav v-if="!showDualSurface" class="drawer-nav" aria-label="任务详情视图">
        <button
          v-for="t in navTabs"
          :key="t.id"
          :aria-current="isTabActive(t.id) ? 'page' : undefined"
          @click="selectTab(t.id)"
        >
          {{ t.label }}
        </button>
      </nav>

      <!-- Main Body: Single-column in Drawer, Dual-column (Sequential Stream + Right Surface) in Fullscreen/Page -->
      <div class="workspace-body" :class="{ 'dual-pane': showDualSurface }">
        <!-- Left / Primary Pane: Sequential Agent Stream -->
        <div v-show="showDualSurface ? !narrow || !mobileSurface : tab === 'process'" class="process">
          <TaskSupplementReceipts v-if="detail" :report="latestReport" />
          <p v-if="detail && taskCenter && detail.actions.supplement?.reason === 'system_managed_input'" class="banner muted">
            此任务的输入由系统派发。
          </p>
          <ManagerSessionPanel
            v-if="!planned"
            :key="detail?.session_id || 'shared'"
            :workspace="workspaceId"
            :session-id="detail?.session_id || ''"
            :title="detail?.agent.name || '执行 Agent'"
            :read-only="!guest && !canSupplement"
            :task-center="taskCenter"
            :public-detail="guest"
            :artifacts="artifacts"
            :selected-artifact-id="selectedArtifact?.id"
            :initial-input-text="detail?.initial_input?.text"
            :initial-input-time="detail?.initial_input?.created_at || detail?.created_at"
            :stop-disabled="!detail?.actions.stop.allowed"
            task
            embedded
            @changed="changed"
            @sent="commandAccepted"
            @stopped="commandAccepted"
            @select-artifact="selectArtifactForPreview"
            @download-artifact="downloadArtifact"
          />
          <div v-if="!showDualSurface && progress" class="drawer-inline-progress">
            <TaskProgressSummary :progress="progress" />
          </div>
        </div>

        <!-- Secondary / Right Surface Pane (Always visible in Dual-Surface; tab-switched in Drawer) -->
        <aside v-show="showDualSurface ? !narrow || mobileSurface : tab !== 'process'" class="surface-pane" aria-label="右侧产物与状态画布">
          <nav v-if="showDualSurface" class="surface-nav" aria-label="任务详情视图">
            <button
              v-for="t in navTabs"
              :key="t.id"
              :aria-current="isTabActive(t.id) ? 'page' : undefined"
              @click="selectTab(t.id)"
            >
              {{ t.label }}
            </button>
          </nav>

          <!-- Surface Pane 1: Artifacts & Inline Preview -->
          <div v-if="isTabActive('artifacts')" class="surface-content artifacts-surface">
            <div v-if="!artifacts.length" class="surface-empty">
              <strong>暂无任务产物</strong>
              <p class="muted">任务执行产出报告、表格、代码或图片后，将自动展示在此处并支持在线预览。</p>
            </div>
            <template v-else>
              <div class="artifact-selector-list">
                <div
                  v-for="file in artifacts"
                  :key="file.id"
                  :class="['artifact-row', { active: selectedArtifact?.id === file.id }]"
                >
                  <button type="button" class="artifact-row-select" @click="selectArtifactForPreview(file, false)">
                    <span class="artifact-badge" :class="classifyArtifact(file).kind">{{ classifyArtifact(file).badge }}</span>
                    <span class="artifact-meta">
                      <strong>{{ file.name }}</strong>
                      <small>{{ classifyArtifact(file).label }}<template v-if="file.size != null"> · {{ formatArtifactSize(file.size) }}</template></small>
                    </span>
                  </button>
                  <NButton size="tiny" secondary :loading="downloadBusyId === file.id" @click="downloadArtifact(file)">下载</NButton>
                </div>
              </div>

              <section v-if="selectedArtifact" class="artifact-preview-surface" aria-label="产物预览画布">
                <header class="preview-header">
                  <div class="preview-title">
                    <strong>{{ selectedArtifact.name }}</strong>
                    <small>{{ classifyArtifact(selectedArtifact).label }}<template v-if="selectedArtifact.size != null"> · {{ formatArtifactSize(selectedArtifact.size) }}</template></small>
                  </div>
                  <div class="preview-toolbar-actions">
                    <div
                      v-if="!previewLoading && !previewError && previewText && (classifyArtifact(selectedArtifact).kind === 'markdown' || classifyArtifact(selectedArtifact).kind === 'csv')"
                      class="preview-mode-switch"
                      role="group"
                      aria-label="产物视图切换"
                    >
                      <button type="button" :class="{ active: artifactViewMode === 'preview' }" @click="artifactViewMode = 'preview'">预览</button>
                      <button type="button" :class="{ active: artifactViewMode === 'source' }" @click="artifactViewMode = 'source'">源码</button>
                    </div>
                    <NButton v-if="!previewLoading && !previewError && previewText" size="tiny" secondary @click="copyArtifactContent">复制内容</NButton>
                    <NButton size="tiny" type="primary" secondary :loading="downloadBusyId === selectedArtifact.id" @click="downloadArtifact(selectedArtifact)">下载原文件</NButton>
                  </div>
                </header>
                <div class="preview-body">
                  <NSkeleton v-if="previewLoading" text :repeat="6" />
                  <p v-else-if="previewError" class="error">{{ previewError }}</p>
                  <pre v-else-if="artifactViewMode === 'source' && previewText" class="surface-code-block">{{ previewText }}</pre>
                  <SessionMarkdown v-else-if="classifyArtifact(selectedArtifact).kind === 'markdown'" :text="previewText" />
                  <div v-else-if="classifyArtifact(selectedArtifact).kind === 'csv'" class="csv-preview-wrap">
                    <table v-if="previewCsvRows.length" class="surface-csv-table">
                      <thead>
                        <tr><th v-for="(cell, idx) in previewCsvRows[0]" :key="idx">{{ cell }}</th></tr>
                      </thead>
                      <tbody>
                        <tr v-for="(row, rIdx) in previewCsvRows.slice(1)" :key="rIdx">
                          <td v-for="(cell, cIdx) in row" :key="cIdx">{{ cell }}</td>
                        </tr>
                      </tbody>
                    </table>
                    <p v-else class="muted">表格文件为空</p>
                  </div>
                  <pre v-else-if="classifyArtifact(selectedArtifact).kind === 'code'" class="surface-code-block">{{ previewText }}</pre>
                  <div v-else-if="classifyArtifact(selectedArtifact).kind === 'image' && previewImageUrl" class="image-preview-wrap">
                    <img :src="previewImageUrl" :alt="selectedArtifact.name" />
                  </div>
                  <div v-else class="binary-preview-card">
                    <strong>{{ selectedArtifact.name }}</strong>
                    <p class="muted">该文件格式暂不支持浏览器内联渲染，请直接下载查看。</p>
                    <NButton size="small" type="primary" :loading="downloadBusyId === selectedArtifact.id" @click="downloadArtifact(selectedArtifact)">下载 {{ selectedArtifact.name }}</NButton>
                  </div>
                </div>
              </section>
            </template>
          </div>

          <!-- Surface Pane 2: Task Status Summary (Keep mounted cleanly in DOM so summary facts remain accessible) -->
          <div v-show="isTabActive('status')" class="surface-content status-surface">
            <div class="surface-card">
              <h3>任务状态摘要</h3>
              <TaskProgressSummary v-if="progress" :progress="progress" />
              <p v-else class="muted">当前阶段：{{ primaryStatusLabel }}</p>
            </div>
            <div v-if="detail" class="surface-card">
              <h3>任务元信息</h3>
              <p class="muted">任务 ID：{{ detail.task_id }}<br />创建时间：{{ new Date(detail.created_at).toLocaleString('zh-CN') }}</p>
              <NButton size="tiny" secondary @click="copyID">复制任务 ID</NButton>
              <details v-if="detail.initial_input?.text" style="margin-top:10px">
                <summary>初始任务要求</summary>
                <SessionMarkdown :text="detail.initial_input.text" />
              </details>
              <details v-if="taskCenter && detail.latest_result?.text" style="margin-top:10px">
                <summary>最近一轮结果{{ detail.latest_result.truncated ? '（摘要）' : '' }}</summary>
                <SessionMarkdown :text="detail.latest_result.text" />
              </details>
            </div>
            <p v-if="guest" class="muted guest-boundary-note">
              此免登链接仅授权查看当前任务及获准产物，不开放工作区任务列表或关联任务跳转。
            </p>
          </div>

          <!-- Surface Pane 3: Inputs (Web Only) -->
          <div v-if="!guest && isTabActive('inputs')" class="surface-content records">
            <p v-if="!inputs.length" class="muted">尚无输入记录</p>
            <article v-for="i in inputs" :key="i.id">
              <strong>输入 {{ i.seq }} · {{ sessionStatus(i.status) }}</strong>
              <small>{{ i.confirmation_request_id ? '确认答复' : i.delivery_mode === 'steer' ? '引导当前执行' : '下一轮处理' }}</small>
              <SessionMarkdown :text="i.text" />
              <p v-if="i.rejection_reason || i.cancel_reason" class="muted">{{ i.rejection_reason || i.cancel_reason }}</p>
              <NButton v-if="canWithdraw(i)" size="tiny" :disabled="busy" @click="action('withdraw', i.id)">撤回此输入</NButton>
            </article>
            <NButton v-if="inputCursor" size="small" :disabled="busy" @click="more('inputs')">加载更多输入</NButton>
          </div>

          <!-- Surface Pane 4: Reports (Web Only) -->
          <div v-if="!guest && isTabActive('reports') && detail" class="surface-content records">
            <p v-if="detail.task_space_id" class="muted">此任务没有来源对话，不会向主会话回报。</p>
            <p v-else class="muted">回报受理、主会话处理和回复分别确认。</p>
            <p v-if="!visibleReports.length" class="muted">尚无已结束轮次的回报</p>
            <article v-for="r in visibleReports" :key="r.source_input_id">
              <strong>{{ r.source_turn_id ? '轮次回报' : '未执行输入回报' }} · {{ sessionStatus(r.outcome) }}</strong>
              <p>{{ reportStatus[r.status] || r.status }}</p>
              <p v-if="r.reason" class="warning">{{ r.reason }}</p>
              <p v-if="r.target" class="muted">
                主会话输入：{{ sessionStatus(r.target.input_status) }}<br />
                上下文：{{ r.target.context_state === 'committed' ? '已确认接收' : r.target.context_state || '尚无接收证据' }}<br />
                主会话轮次：{{ sessionStatus(r.target.turn_status || undefined) }}
              </p>
              <TaskSupplementReceipts :report="r" />
            </article>
            <NButton v-if="reportCursor" size="small" :disabled="busy" @click="more('reports')">加载更多回报</NButton>
          </div>

          <!-- Surface Pane 5: Work Item Timeline (Web Only) -->
          <div v-if="!guest && isTabActive('work') && detail?.work_item_id" class="surface-content records">
            <NButton v-if="taskCenter" size="tiny" secondary style="margin-bottom: 10px;" @click="emit('openWork', detail.work_item_id)">前往事项看板</NButton>
            <WorkItemTimeline :workspace="workspaceId" :item-id="detail.work_item_id" />
          </div>
        </aside>
      </div>
    </template>
  </section>
</template>

<style scoped>
.task-detail-panel {
  display: flex;
  flex-direction: column;
  height: 100%;
  min-height: 420px;
  min-width: 0;
  background: var(--id-bg-surface);
  color: var(--id-text-primary);
}
.task-detail-panel.sidebar.is-fullscreen {
  position: fixed;
  inset: 0;
  z-index: 1200;
  width: 100vw;
  height: 100dvh;
}
.session-topbar {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 12px;
  padding: 10px 16px;
  border-bottom: 1px solid var(--id-border-subtle);
  background: var(--id-bg-surface);
  flex-wrap: wrap;
}
.topbar-left {
  display: flex;
  align-items: center;
  gap: 10px;
  min-width: 0;
  flex: 1 1 18rem;
}
.task-summary {
  min-width: 0;
  flex: 1;
  display: flex;
  flex-direction: column;
  gap: 2px;
}
.title-row {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
}
.title-row h2 {
  margin: 0;
  font-size: 15px;
  font-weight: 600;
  line-height: 1.35;
  overflow-wrap: anywhere;
}
.sub-row {
  display: flex;
  align-items: center;
  gap: 6px;
  flex-wrap: wrap;
  font-size: 11px;
  color: var(--id-text-muted);
}
.status-badge {
  display: inline-flex;
  align-items: center;
  white-space: nowrap;
  padding: 2px 8px;
  border-radius: var(--id-radius-pill);
  font-size: 11px;
  font-weight: 500;
  background: var(--id-bg-muted);
  color: var(--id-text-secondary);
  border: 1px solid var(--id-border-subtle);
}
.status-badge.running,
.status-badge.queued,
.status-badge.preparing_environment,
.status-badge.background_active,
.status-badge.waiting_resume {
  background: var(--id-status-info-soft);
  color: var(--id-status-info);
  border-color: var(--id-status-info-border);
}
.status-badge.completed, .status-badge.success {
  background: var(--id-status-success-soft);
  color: var(--id-status-success);
  border-color: var(--id-status-success-border);
}
.status-badge.waiting_confirmation,
.status-badge.waiting_for_confirmation,
.status-badge.preparing_confirmation,
.status-badge.waiting_handoff,
.status-badge.waiting_for_handoff,
.status-badge.waiting_acceptance,
.status-badge.waiting_for_action,
.status-badge.stopping,
.status-badge.engine_recovery_pending {
  background: var(--id-status-warning-soft);
  color: var(--id-status-warning);
  border-color: var(--id-status-warning-border);
}
.status-badge.failed,
.status-badge.handoff_failed,
.status-badge.blocked,
.status-badge.interrupted,
.status-badge.rejected {
  background: var(--id-status-danger-soft);
  color: var(--id-status-danger);
  border-color: var(--id-status-danger-border);
}
.meta-chip {
  padding: 2px 7px;
  border-radius: var(--id-radius-pill);
  background: var(--id-bg-subtle);
  border: 1px solid var(--id-border-subtle);
  font-size: 11px;
  color: var(--id-text-secondary);
}
.topbar-actions {
  display: flex;
  align-items: center;
  gap: 6px;
  flex-wrap: wrap;
}
.rename-strip {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 8px 16px;
  border-bottom: 1px solid var(--id-border-subtle);
  background: var(--id-bg-subtle);
}
.rename-strip .n-input {
  max-width: 360px;
}
.runtime-strips {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
  padding: 6px 16px;
  background: var(--id-bg-subtle);
  border-bottom: 1px solid var(--id-border-subtle);
}
.strip-pill {
  font-size: 11px;
  padding: 2px 8px;
  border-radius: var(--id-radius-sm);
  background: var(--id-bg-surface);
  border: 1px solid var(--id-border-subtle);
  color: var(--id-text-secondary);
}
.strip-pill.warn {
  color: var(--id-status-warning);
  border-color: var(--id-status-warning-border);
  background: var(--id-status-warning-soft);
}
.banner {
  margin: 0;
  padding: 6px 16px;
  font-size: 12px;
  border-bottom: 1px solid var(--id-border-subtle);
}
.error { color: var(--id-status-danger); }
.warning { color: var(--id-status-warning); }
.muted, .eyebrow, small { font-size: 12px; color: var(--id-text-muted); }

nav {
  display: flex;
  gap: 4px;
  padding: 0 14px;
  border-bottom: 1px solid var(--id-border-subtle);
  background: var(--id-bg-subtle);
  flex-wrap: wrap;
}
nav button {
  background: none;
  border: 0;
  border-bottom: 2px solid transparent;
  padding: 10px 8px;
  cursor: pointer;
  color: var(--id-text-secondary);
  font: inherit;
  font-size: 12px;
  font-weight: 500;
}
nav button[aria-current=page] {
  border-color: var(--id-accent-primary);
  color: var(--id-accent-primary);
  font-weight: 600;
}

.workspace-body {
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
}
.workspace-body.dual-pane {
  flex-direction: row;
}
.process {
  display: flex;
  flex-direction: column;
  flex: 1.35;
  min-width: 0;
  min-height: 0;
}
.process :deep(.session-panel) {
  border: 0;
  border-radius: 0;
  min-height: 260px;
}
.drawer-inline-progress {
  padding: 8px 16px;
  border-top: 1px solid var(--id-border-subtle);
  background: var(--id-bg-subtle);
}

.surface-pane {
  flex: 1;
  min-width: 0;
  min-height: 0;
  display: flex;
  flex-direction: column;
  background: var(--id-bg-surface);
}
.workspace-body.dual-pane .surface-pane {
  width: clamp(340px, 40%, 540px);
  flex: none;
  border-left: 1px solid var(--id-border-subtle);
}
.surface-content {
  flex: 1;
  min-height: 0;
  overflow: auto;
  padding: 14px 16px;
  display: flex;
  flex-direction: column;
  gap: 12px;
}
.surface-empty {
  margin: auto;
  text-align: center;
  max-width: 280px;
}
.surface-empty strong {
  display: block;
  margin-bottom: 6px;
  font-size: 13px;
}
.artifact-selector-list {
  display: flex;
  flex-direction: row;
  flex-wrap: nowrap;
  overflow-x: auto;
  gap: 6px;
  flex-shrink: 0;
  padding-bottom: 2px;
}
.artifact-row {
  display: flex;
  align-items: center;
  gap: 6px;
  padding: 5px 8px;
  border-radius: var(--id-radius-md);
  border: 1px solid var(--id-border-subtle);
  background: var(--id-bg-subtle);
  flex: 0 0 auto;
  max-width: 240px;
  transition: border-color var(--id-transition-fast), background-color var(--id-transition-fast);
}
.artifact-row.active {
  border-color: var(--id-accent-primary);
  background: var(--id-accent-primary-soft);
}
.artifact-row-select {
  flex: 1;
  min-width: 0;
  display: flex;
  align-items: center;
  gap: 8px;
  background: transparent;
  border: 0;
  padding: 0;
  text-align: left;
  cursor: pointer;
  color: inherit;
  font: inherit;
}
.artifact-badge {
  flex-shrink: 0;
  min-width: 32px;
  text-align: center;
  padding: 2px 5px;
  border-radius: 5px;
  font-family: var(--id-font-mono);
  font-size: 10px;
  font-weight: 700;
  background: var(--id-bg-muted);
  color: var(--id-text-secondary);
  border: 1px solid var(--id-border-subtle);
}
.artifact-badge.markdown { background: var(--id-accent-primary-soft); color: var(--id-accent-primary); border-color: var(--id-accent-primary-border); }
.artifact-badge.csv { background: var(--id-status-success-soft); color: var(--id-status-success); border-color: var(--id-status-success-border); }
.artifact-badge.code { background: var(--id-status-info-soft); color: var(--id-status-info); border-color: var(--id-status-info-border); }
.artifact-badge.image { background: var(--id-status-warning-soft); color: var(--id-status-warning); border-color: var(--id-status-warning-border); }
.artifact-meta {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 1px;
}
.artifact-meta strong {
  font-size: 12px;
  font-weight: 600;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.artifact-meta small {
  font-size: 10px;
  white-space: nowrap;
}

.artifact-preview-surface {
  flex: 1;
  min-height: 220px;
  display: flex;
  flex-direction: column;
  border: 1px solid var(--id-border-subtle);
  border-radius: 10px;
  background: var(--id-bg-subtle);
  overflow: hidden;
}
.preview-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  padding: 9px 12px;
  border-bottom: 1px solid var(--id-border-subtle);
  background: var(--id-bg-surface);
  flex-wrap: wrap;
}
.preview-toolbar-actions {
  display: flex;
  align-items: center;
  gap: 6px;
  flex-shrink: 0;
}
.preview-mode-switch {
  display: inline-flex;
  padding: 2px;
  border-radius: var(--id-radius-sm);
  border: 1px solid var(--id-border-subtle);
  background: var(--id-bg-subtle);
}
.preview-mode-switch button {
  border: 0;
  background: transparent;
  padding: 2px 8px;
  border-radius: 3px;
  font: inherit;
  font-size: 11px;
  color: var(--id-text-secondary);
  cursor: pointer;
}
.preview-mode-switch button.active {
  background: var(--id-bg-surface);
  color: var(--id-text-primary);
  font-weight: 600;
  box-shadow: var(--id-shadow-xs);
}
.preview-title {
  min-width: 0;
  display: flex;
  flex-direction: column;
}
.preview-title strong {
  font-size: 12px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.preview-title small {
  font-size: 11px;
}
.preview-body {
  flex: 1;
  min-height: 0;
  overflow: auto;
  padding: 14px;
}
.surface-code-block {
  margin: 0;
  white-space: pre-wrap;
  overflow-wrap: anywhere;
  font: 12px/1.65 var(--id-font-mono);
  color: var(--id-text-primary);
}
.csv-preview-wrap {
  overflow: auto;
}
.surface-csv-table {
  width: 100%;
  border-collapse: collapse;
  font-size: 12px;
  font-variant-numeric: tabular-nums;
}
.surface-csv-table th, .surface-csv-table td {
  padding: 6px 10px;
  border-bottom: 1px solid var(--id-border-subtle);
  text-align: left;
  white-space: nowrap;
}
.surface-csv-table th {
  background: var(--id-bg-surface);
  font-weight: 600;
  color: var(--id-text-secondary);
}
.image-preview-wrap {
  display: flex;
  justify-content: center;
  align-items: center;
}
.image-preview-wrap img {
  max-width: 100%;
  max-height: 460px;
  border-radius: 8px;
  border: 1px solid var(--id-border-subtle);
}
.binary-preview-card {
  padding: 20px;
  text-align: center;
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 8px;
}

.surface-card {
  padding: 12px 14px;
  border-radius: var(--id-radius-lg);
  border: 1px solid var(--id-border-subtle);
  background: var(--id-bg-subtle);
}
.surface-card h3 {
  margin: 0 0 8px;
  font-size: 13px;
  font-weight: 600;
}
.records article {
  border-bottom: 1px solid var(--id-border-subtle);
  padding: 12px 0;
  overflow-wrap: anywhere;
}
.records strong { font-size: 13px; }
.records small { display: block; margin: 4px 0; }
.guest-boundary-note {
  padding: 10px 12px;
  border-radius: var(--id-radius-md);
  border: 1px dashed var(--id-border-subtle);
  background: var(--id-bg-subtle);
  line-height: 1.6;
}
summary { cursor: pointer; font-size: 12px; }
button:focus-visible, summary:focus-visible { outline: 2px solid var(--id-accent-primary); outline-offset: 2px; }

@media (max-width: 960px) {
  .workspace-body.dual-pane {
    flex-direction: column;
  }
  .workspace-body.dual-pane .surface-pane {
    width: 100%;
    flex: 1;
    border-left: 0;
    border-top: 1px solid var(--id-border-subtle);
  }
}
</style>

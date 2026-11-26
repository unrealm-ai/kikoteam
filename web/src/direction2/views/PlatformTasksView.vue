<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue';
import {
  NButton,
  NDrawer,
  NDrawerContent,
  NInput,
  NSelect,
  NSkeleton,
} from 'naive-ui';
import { activeWorkspaceId, D2ApiError } from '../api/client';
import { useTaskEmployees } from '../composables/useTaskEmployees';
import { taskApi, type TaskRecord, type TaskFilters } from '../api/tasks';
import TaskDetailPanel from '../components/TaskDetailPanel.vue';
import D2EmployeeAvatar from '../components/D2EmployeeAvatar.vue';
import { copyText } from '../utils/clipboard';
import { sessionStatus, workItemProgress } from '../utils/session-presentation';
const props = defineProps<{
  selectedTaskId?: string | undefined;
  selectedEmployeeId?: string | undefined;
}>();
const emit = defineEmits<{
  navigate: [
    view: string,
    param?: string,
    targetId?: string,
    extraType?: string,
  ];
}>();
const counts = ref<Record<string, number>>({});
const { agents, agentError, loadAgents } = useTaskEmployees(activeWorkspaceId);
const q = ref(''),
  employee = ref(props.selectedEmployeeId || ''),
  state = ref(''),
  result = ref(''),
  lifecycle = ref('');
const tasks = ref<TaskRecord[]>([]),
  loading = ref(false),
  error = ref('');
const selected = ref(props.selectedTaskId || ''),
  drawerFullscreen = ref(false),
  copiedTaskId = ref(''),
  cursor = ref(''),
  next = ref<string | null>(null),
  previous = ref<string[]>([]);
let copyTimer: ReturnType<typeof setTimeout> | undefined;
let generation = 0,
  controller = new AbortController(),
  timer: ReturnType<typeof setInterval> | undefined,
  debounce: ReturnType<typeof setTimeout> | undefined,
  busy = false;
const filters = computed<TaskFilters>(() => ({
  q: q.value.trim(),
  agent_id: employee.value,
  execution_state: state.value,
  latest_turn_status: result.value,
  lifecycle: lifecycle.value,
}));
const stateOptions = [
  { label: '全部执行状态', value: '' },
  ...['queued', 'running', 'stopping', 'blocked', 'idle'].map((s) => ({
    label: s === 'idle' ? '当前无执行轮次' : sessionStatus(s),
    value: s,
  })),
];
const resultOptions = [
  { label: '全部轮次结果', value: '' },
  ...['running', 'completed', 'failed', 'interrupted', 'canceled'].map((s) => ({
    label: sessionStatus(s),
    value: s,
  })),
];
async function load(silent = false) {
  if (busy || document.hidden) return;
  busy = true;
  const g = generation;
  if (!silent) loading.value = true;
  try {
    const page = await taskApi.list(
      activeWorkspaceId.value,
      filters.value,
      cursor.value,
      controller.signal,
      true,
    );
    if (g !== generation) return;
    if (page.next_cursor && page.next_cursor === cursor.value)
      throw new Error('任务分页游标重复');
    counts.value = page.status_counts || {};
    tasks.value = page.items;
    next.value = page.next_cursor;
    error.value = '';
  } catch (e) {
    if (g === generation) {
      error.value = e instanceof Error ? e.message : '任务加载失败';
      if (e instanceof D2ApiError && [401, 403, 404].includes(e.status)) {
        counts.value = {};
        tasks.value = [];
        selected.value = '';
      }
    }
  } finally {
    if (g === generation) {
      busy = false;
      loading.value = false;
    }
  }
}
function restart() {
  generation++;
  controller.abort();
  controller = new AbortController();
  busy = false;
  cursor.value = '';
  next.value = null;
  previous.value = [];
  counts.value = {};
  tasks.value = [];
  void load();
}
function page(forward: boolean) {
  if (busy) return;
  if (forward && next.value) {
    previous.value.push(cursor.value);
    cursor.value = next.value;
  } else if (!forward && previous.value.length)
    cursor.value = previous.value.pop() || '';
  void load();
}
function open(id: string) {
  drawerFullscreen.value = false;
  selected.value = id;
  emit('navigate', 'tasks', 'tasks', id, 'task');
}
function close() {
  drawerFullscreen.value = false;
  selected.value = '';
  emit('navigate', 'tasks', 'tasks');
}
function visibility() {
  if (!document.hidden) void load(true);
}
watch([employee, state, result, lifecycle], restart);
watch(q, () => {
  if (debounce) clearTimeout(debounce);
  debounce = setTimeout(restart, 300);
});
watch(
  () => props.selectedTaskId,
  (id) => {
    selected.value = id || '';
  },
);
watch(
  () => props.selectedEmployeeId,
  (id) => {
    employee.value = id || '';
  },
);
watch(activeWorkspaceId, () => {
  selected.value = '';
  restart();
});
onMounted(() => {
  void load();
  timer = setInterval(() => {
    void load(true);
  }, 5000);
  document.addEventListener('visibilitychange', visibility);
});
onUnmounted(() => {
  generation++;
  controller.abort();
  if (timer) clearInterval(timer);
  if (debounce) clearTimeout(debounce);
  if (copyTimer) clearTimeout(copyTimer);
  document.removeEventListener('visibilitychange', visibility);
});
defineExpose({ refresh: load });
function taskStatusTone(task: TaskRecord) {
  const execution = task.execution;
  if (task.planning_state === 'planned' || task.planning_state === 'canceled') return 'neutral';
  if (execution.state === 'blocked' || execution.required_actions?.length) return 'warning';
  if (execution.state === 'queued' || execution.state === 'running' || execution.state === 'stopping' ||
      (execution.state === 'idle' && (execution.background_process_count ?? 0) > 0)) return 'info';
  if (execution.state !== 'idle') return 'neutral';
  if (task.latest_turn?.status === 'completed') return 'success';
  if (task.latest_turn?.status === 'failed') return 'danger';
  return 'neutral';
}
const rowDotClass = { info: 'running', warning: 'blocked', success: 'completed', danger: 'failed', neutral: '' };
function elapsed(t: TaskRecord) {
  const turn = t.latest_turn;
  if (!turn?.finished_at) return '—';
  const seconds =
    (Date.parse(turn.finished_at) - Date.parse(turn.started_at)) / 1000;
  return seconds >= 0 ? seconds.toFixed(1) + 's' : '—';
}
async function copyTaskId(id: string) {
  try {
    await copyText(id);
    copiedTaskId.value = id;
    if (copyTimer) clearTimeout(copyTimer);
    copyTimer = setTimeout(() => {
      if (copiedTaskId.value === id) copiedTaskId.value = '';
    }, 1800);
  } catch { /* clipboard unavailable */ }
}
function openWork(id: string) {
  emit('navigate', 'tasks', 'work-items', id, 'work-item');
}
</script>
<template>
  <section class="tasks-table-container" aria-label="任务记录">
    <div class="tasks-filter-bar">
      <div class="filter-left">
        <span class="lbl">执行状态</span>
        <button
          v-for="opt in stateOptions"
          :key="opt.value"
          type="button"
          class="filter-chip"
          :class="{ active: state === opt.value }"
          :aria-pressed="state === opt.value"
          @click="state = opt.value"
        >
          {{ opt.value ? opt.label : '全部'
          }}<template v-if="!loading && !error">
            ({{
              opt.value
                ? counts[opt.value] || 0
                : Object.values(counts).reduce((a, b) => a + b, 0)
            }})</template
          >
        </button>
      </div>
      <div class="filter-right">
        <NInput
          v-model:value="q"
          clearable
          size="small"
          class="task-filter-input"
          maxlength="200"
          placeholder="搜索任务标题 / 完整任务 ID"
          aria-label="搜索任务"
        />
        <NSelect
          v-model:value="employee"
          filterable
          size="small"
          class="task-filter-select"
          :options="[{ label: '全部员工', value: '' }, ...agents]"
          aria-label="按员工筛选"
        />
        <NSelect
          v-model:value="result"
          size="small"
          class="task-filter-select"
          :options="resultOptions"
          aria-label="按最近轮次筛选"
        />
        <NSelect
          v-model:value="lifecycle"
          size="small"
          class="task-filter-select"
          :options="[
            { label: '全部记录', value: '' },
            { label: '可继续', value: 'active' },
            { label: '已归档', value: 'archived' },
          ]"
          aria-label="按归档状态筛选"
        />
      </div>
    </div>
    <p v-if="agentError" class="error">
      {{ agentError }}
      <NButton size="tiny" @click="loadAgents">重试员工筛选</NButton>
    </p>
    <p v-if="error" role="alert" class="error">{{ error }}</p>
    <NSkeleton v-if="loading && !tasks.length" text :repeat="6" />
    <div v-else class="tasks-table-card">
      <table class="custom-d2-table">
        <thead>
          <tr>
            <th>任务</th>
            <th>执行阶段与状态</th>
            <th>执行员工</th>
            <th>来源与关联事项</th>
            <th class="col-right">耗时与操作</th>
          </tr>
        </thead>
        <tbody>
          <tr
            v-for="task in tasks"
            :key="task.task_id"
            class="task-row-clickable"
            @click="open(task.task_id)"
          >
            <td>
              <div class="task-title-line">
                <span
                  class="row-dot"
                  :class="rowDotClass[taskStatusTone(task)]"
                  aria-hidden="true"
                />
                <button class="task-title" @click.stop="open(task.task_id)">
                  {{ task.title || '未命名任务' }}
                </button>
                <span v-if="task.lifecycle === 'archived'" class="lifecycle-chip"
                  >已归档</span
                >
              </div>
              <div class="task-sub-meta">
                <button
                  type="button"
                  class="task-id-code"
                  :title="'点击复制完整任务 ID：' + task.task_id"
                  @click.stop="copyTaskId(task.task_id)"
                >
                  {{
                    copiedTaskId === task.task_id
                      ? '已复制'
                      : '#' + task.task_id.slice(0, 8)
                  }}
                </button>
                <span>{{ sessionStatus(task.lifecycle) }}</span>
              </div>
            </td>
            <td>
              <span
                class="status-pill"
                :class="taskStatusTone(task)"
                ><span class="status-dot" />{{
                  task.planning_state === 'planned'
                    ? '待办：尚未启动'
                    : task.planning_state === 'canceled'
                      ? '待办已取消（未执行）'
                      : workItemProgress(task.execution) ||
                        (task.execution.required_actions?.length
                          ? '待确认'
                          : task.execution.state === 'idle'
                            ? '当前无执行轮次'
                            : sessionStatus(task.execution.state))
                }}</span
              >
              <small class="stage-subline">
                最近：{{
                  task.latest_turn
                    ? sessionStatus(task.latest_turn.status)
                    : task.latest_input
                      ? sessionStatus(task.latest_input.status)
                      : '尚未开始'
                }}<template v-if="task.latest_turn?.has_unknown_tool_results">
                  · 有工具结果待核对</template
                ><template v-if="task.execution.queued_input_count">
                  · {{ task.execution.queued_input_count }} 条排队</template
                ><template
                  v-if="
                    task.execution.background_process_count === null ||
                    task.execution.background_process_count
                  "
                >
                  ·
                  {{
                    task.execution.background_process_count === null
                      ? '后台活动待核对'
                      : `${task.execution.background_process_count} 个后台进程`
                  }}</template
                ><template v-if="task.execution.block_reason">
                  · {{ sessionStatus(task.execution.block_reason) }}</template
                >
              </small>
            </td>
            <td>
              <span class="agent"
                ><D2EmployeeAvatar
                  :name="task.agent.name"
                  :preset="task.agent.avatar"
                  :identity-key="task.agent.id"
                  size="sm"
                /><span class="emp-cell-name">{{ task.agent.name }}</span></span
              >
            </td>
            <td>
              <div class="source-work-col">
                <button
                  v-if="task.source.chat_session_id"
                  class="text-link"
                  @click.stop="
                    emit(
                      'navigate',
                      'chat',
                      task.conversation_id,
                      task.agent.id,
                      'employee',
                    )
                  "
                >
                  主对话 ↗</button
                ><span v-else class="source-muted">{{
                  task.task_space_id ? '任务空间' : '未指定'
                }}</span>
                <button
                  v-if="task.work_item_id"
                  class="jump-work-item-btn"
                  @click.stop="openWork(task.work_item_id)"
                >
                  前往事项 ↗
                </button>
              </div>
            </td>
            <td class="col-right">
              <div class="time-action-cell">
                <div class="time-meta">
                  <span class="mono-num">{{ elapsed(task) }}</span>
                  <small class="time">{{
                    new Date(task.created_at).toLocaleString('zh-CN')
                  }}</small>
                </div>
                <NButton size="tiny" secondary @click.stop="open(task.task_id)"
                  >日志与产物</NButton
                >
              </div>
            </td>
          </tr>
        </tbody>
      </table>
      <p v-if="!tasks.length && !error" class="empty">
        当前筛选条件下暂无任务记录。
      </p>
    </div>
    <footer class="task-page-nav">
      <span>仅展示当前账号有权查看的任务</span
      ><NButton v-if="previous.length" size="small" @click="restart"
        >查看最新任务</NButton
      ><NButton
        size="small"
        :disabled="!previous.length || loading"
        @click="page(false)"
        >上一页</NButton
      ><NButton size="small" :disabled="!next || loading" @click="page(true)"
        >下一页</NButton
      >
    </footer>
    <NDrawer
      :show="!!selected"
      :width="drawerFullscreen ? '100vw' : 'min(920px, 95vw)'"
      @update:show="
        (v) => {
          if (!v) close();
        }
      "
      ><NDrawerContent
        :native-scrollbar="false"
        body-content-style="padding:0;height:100%"
        ><TaskDetailPanel
          v-if="selected"
          :key="selected"
          v-model:fullscreen="drawerFullscreen"
          :workspace-id="activeWorkspaceId"
          :task-id="selected"
          presentation="drawer"
          task-center
          task-record-actions
          @open-work="openWork"
          @close="close"
          @task-changed="load(true)"
          @open-source-conversation="
            (agent, conversation) =>
              emit('navigate', 'chat', conversation, agent, 'employee')
          " /></NDrawerContent
    ></NDrawer>
  </section>
</template>
<style scoped src="../styles/task-center.css"></style>
<style scoped>
/* 2. Tasks Table */
.tasks-table-container {
  display: flex;
  flex-direction: column;
  gap: 12px;
}
.tasks-filter-bar {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 12px;
  background: var(--id-bg-surface);
  padding: 10px 14px;
  border: 1px solid var(--id-border-subtle);
  border-radius: var(--id-radius-lg);
}
.tasks-table-card {
  background: var(--id-bg-surface);
  border: 1px solid var(--id-border-subtle);
  border-radius: var(--id-radius-lg);
  overflow-x: auto;
}
.custom-d2-table {
  width: 100%;
  border-collapse: collapse;
  font-size: 13px;
  text-align: left;
}
.custom-d2-table th {
  background: var(--id-bg-subtle);
  padding: 10px 14px;
  font-size: 12px;
  font-weight: 500;
  color: var(--id-text-secondary);
  border-bottom: 1px solid var(--id-border-subtle);
  white-space: nowrap;
}
.custom-d2-table td {
  padding: 12px 14px;
  border-bottom: 1px solid var(--id-border-hairline);
  color: var(--id-text-primary);
  vertical-align: middle;
}
.custom-d2-table tbody tr {
  transition: background-color var(--id-transition-fast);
}
.custom-d2-table tbody tr:hover td {
  background: var(--id-bg-subtle);
}
.custom-d2-table tbody tr:last-child td {
  border-bottom: none;
}
.table-empty-cell {
  padding: 36px 0;
}
.task-id-code {
  font-family: var(--id-font-mono);
  font-variant-numeric: tabular-nums;
  font-size: 11px;
  background: var(--id-bg-muted);
  border: 1px solid var(--id-border-subtle);
  padding: 1px 6px;
  border-radius: var(--id-radius-sm);
  color: var(--id-text-secondary);
  white-space: nowrap;
  cursor: pointer;
  transition: border-color var(--id-transition-fast), color var(--id-transition-fast);
}
.task-id-code:hover {
  border-color: var(--id-border-strong);
  color: var(--id-text-primary);
}
.task-title-line {
  display: flex;
  align-items: center;
  gap: 8px;
}
.row-dot {
  width: 7px;
  height: 7px;
  border-radius: 50%;
  flex-shrink: 0;
  background: var(--id-text-muted);
}
.row-dot.running { background: var(--id-status-info-dot); }
.row-dot.blocked { background: var(--id-status-warning-dot); }
.row-dot.completed { background: var(--id-status-success-dot); }
.row-dot.failed { background: var(--id-status-danger-dot); }
.lifecycle-chip {
  font-size: 11px;
  padding: 1px 6px;
  border-radius: var(--id-radius-sm);
  background: var(--id-bg-muted);
  border: 1px solid var(--id-border-subtle);
  color: var(--id-text-muted);
  white-space: nowrap;
}
.task-sub-meta {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-top: 5px;
  padding-left: 15px;
  font-size: 11px;
  color: var(--id-text-muted);
}
.stage-subline {
  margin-top: 5px;
}
.source-work-col {
  display: flex;
  flex-direction: column;
  align-items: flex-start;
  gap: 5px;
}
.source-muted {
  font-size: 12px;
  color: var(--id-text-secondary);
}
.col-right {
  text-align: right;
}
.time-action-cell {
  display: flex;
  align-items: center;
  justify-content: flex-end;
  gap: 12px;
}
.time-meta {
  display: flex;
  flex-direction: column;
  align-items: flex-end;
}
.time-meta .mono-num {
  font-family: var(--id-font-mono);
  font-variant-numeric: tabular-nums;
  font-size: 12px;
  font-weight: 500;
  color: var(--id-text-primary);
}
.time-meta .time {
  margin-top: 2px;
}
.emp-cell-name {
  font-weight: 500;
  color: var(--id-text-primary);
  white-space: nowrap;
}
.jump-work-item-btn {
  background: var(--id-bg-surface);
  border: 1px solid var(--id-border-subtle);
  border-radius: var(--id-radius-sm);
  padding: 3px 8px;
  font-size: 12px;
  color: var(--id-text-primary);
  cursor: pointer;
  transition:
    background-color var(--id-transition-fast),
    border-color var(--id-transition-fast);
}
.jump-work-item-btn:hover {
  background: var(--id-bg-hover);
  border-color: var(--id-border-strong);
}
.tasks-filter-bar {
  flex-wrap: wrap;
}
.task-row-clickable {
  cursor: pointer;
}
.task-title,
.text-link {
  background: none;
  border: 0;
  padding: 0;
  text-align: left;
  color: var(--id-accent-primary);
  font: inherit;
  cursor: pointer;
}
.agent {
  display: inline-flex;
  align-items: center;
  gap: 6px;
}
small {
  display: block;
  font-size: 11px;
  margin-top: 5px;
  color: var(--id-text-muted);
  overflow-wrap: anywhere;
}
.empty {
  padding: 32px;
  text-align: center;
}
.error {
  color: var(--id-status-danger);
}
footer {
  font-size: 12px;
  color: var(--id-text-secondary);
}
@media (max-width: 760px) {
  .custom-d2-table {
    min-width: 850px;
  }
  .filter-right {
    width: 100%;
  }
  .task-filter-select,
  .task-filter-input {
    flex: 1 1 140px;
  }
}
</style>

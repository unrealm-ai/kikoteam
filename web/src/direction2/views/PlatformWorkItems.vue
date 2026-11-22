<script setup lang="ts">
import { ref, onUnmounted, watch, toRef, computed } from 'vue';
import { NButton, NDrawer, NDrawerContent, NSkeleton, NSelect } from 'naive-ui';
import {
  workApi,
  workStatuses,
  workStatusLabel,
  type WorkItem,
} from '../api/work-items';
import { useTaskEmployees } from '../composables/useTaskEmployees';
import WorkItemTimeline from '../components/WorkItemTimeline.vue';
const props = defineProps<{
  workspace: string;
  selected?: string | undefined;
}>();
const emit = defineEmits<{ open: [id?: string]; openTask: [id: string] }>();
const status = ref(''),
  employee = ref(''),
  rows = ref<WorkItem[]>([]),
  loading = ref(false),
  error = ref('');
const cursor = ref(''),
  previous = ref<string[]>([]),
  next = ref<string | null>(null);
const options = [
  { label: '全部状态', value: '' },
  ...Object.entries(workStatuses).map(([value, label]) => ({ value, label })),
];
const { agents, agentError, loadAgents } = useTaskEmployees(
  toRef(props, 'workspace'),
);
const counts = ref<Record<string, number>>({});
const lanes = [
  { title: '推进中', states: ['pending', 'working'], empty: '暂无推进中事项' },
  {
    title: '待验收 / 等待中',
    states: ['awaiting_acceptance', 'blocked'],
    empty: '暂无待介入事项',
  },
  {
    title: '已闭环与终止',
    states: ['completed', 'failed', 'cancelled'],
    empty: '暂无已闭环或终止事项',
  },
];
const board = computed(() =>
  lanes.map((lane) => ({
    ...lane,
    items: rows.value.filter((row) => lane.states.includes(row.status)),
    count: lane.states.reduce(
      (sum, status) => sum + (counts.value[status] || 0),
      0,
    ),
  })),
);
let request: AbortController | undefined;
async function load() {
  request?.abort();
  request = new AbortController();
  const { signal } = request,
    query = new URLSearchParams({ include_counts: 'true' });
  if (status.value) query.set('status', status.value);
  if (employee.value) query.set('employee_id', employee.value);
  if (cursor.value) query.set('cursor', cursor.value);
  error.value = '';
  loading.value = true;
  try {
    const page = await workApi.list(props.workspace, query, signal);
    if (!signal.aborted) {
      rows.value = page.items;
      counts.value = page.status_counts || {};
      next.value = page.next_cursor;
    }
  } catch (e) {
    if (!signal.aborted) {
      rows.value = [];
      counts.value = {};
      next.value = null;
      error.value = e instanceof Error ? e.message : '事项读取失败';
    }
  } finally {
    if (!signal.aborted) loading.value = false;
  }
}
function page(forward: boolean) {
  if (forward && next.value) {
    previous.value.push(cursor.value);
    cursor.value = next.value;
  } else cursor.value = previous.value.pop() || '';
  void load();
}
watch(
  () => [props.workspace, status.value, employee.value],
  () => {
    rows.value = [];
    counts.value = {};
    cursor.value = '';
    previous.value = [];
    next.value = null;
    void load();
  },
  { immediate: true },
);
onUnmounted(() => request?.abort());
defineExpose({ refresh: load });
</script>
<template>
  <section class="kanban-container" aria-label="协作事项看板">
    <div class="kanban-intro-bar">
      <div class="intro-text">
        「事项」汇总跨群与跨员工的协同推进情况；点击卡片可查看关联任务与处理进展。
      </div>
      <div class="kanban-actions">
        <span v-if="!loading && !error" class="stats-badge"
          >共
          {{ Object.values(counts).reduce((a, b) => a + b, 0) }}
          项协同事项</span
        >
      </div>
    </div>
    <div class="filter-right">
      <NSelect
        v-model:value="status"
        size="small"
        class="task-filter-select"
        :options="options"
        aria-label="事项状态"
      />
      <NSelect
        v-model:value="employee"
        size="small"
        class="task-filter-select"
        filterable
        :options="[{ value: '', label: '全部经办员工' }, ...agents]"
        aria-label="经办员工"
      />
    </div>
    <p v-if="agentError" role="alert">
      {{ agentError }}
      <NButton size="tiny" @click="loadAgents">重试员工筛选</NButton>
    </p>
    <p v-if="error" class="task-read-error" role="alert">
      {{ error }} <NButton size="tiny" @click="load()">重新加载</NButton>
    </p>
    <div v-else class="kanban-board">
      <section v-for="lane in board" :key="lane.title" class="kanban-lane">
        <div class="lane-header">
          <span class="lane-title">{{ lane.title }}</span
          ><span class="lane-count">{{ loading ? '…' : lane.count }}</span>
        </div>
        <div class="lane-body">
          <NSkeleton v-if="loading" text :repeat="3" />
          <div v-else-if="!lane.items.length" class="empty-lane-placeholder">
            {{ lane.count ? '本页暂无此状态事项' : lane.empty }}
          </div>
          <article
            v-for="item in lane.items"
            v-else
            :key="item.id"
            class="wi-card"
          >
            <span class="wi-card-header"
              ><span class="wi-code" :title="item.id"
                >#{{ item.id.slice(0, 8) }}</span
              >
              <span
                class="status-pill"
                :class="{
                  info: item.status === 'working',
                  warning: ['blocked', 'awaiting_acceptance'].includes(
                    item.status,
                  ),
                  success: item.status === 'completed',
                  danger: item.status === 'failed',
                }"
                ><span class="status-dot" />{{
                  workStatusLabel(item.status)
                }}</span
              ></span
            >
            <button
              type="button"
              class="wi-title"
              @click="emit('open', item.id)"
            >
              {{ item.title }}
            </button>
            <span
              v-if="
                item.origin_employee &&
                item.origin_employee !== item.current_employee
              "
              class="wi-handoff-pill"
              >{{ item.origin_employee }} → {{ item.current_employee }}</span
            >
            <span class="wi-summary">{{ item.reason || item.goal }}</span>
            <span
              v-if="
                ['working', 'blocked', 'awaiting_acceptance'].includes(
                  item.status,
                ) && item.linked_task_ids?.length
              "
              class="wi-tasks-link-row"
            >
              <span class="link-label">关联任务</span>
              <button
                v-for="id in item.linked_task_ids"
                :key="id"
                type="button"
                class="task-quick-tag"
                :class="{ highlight: item.status !== 'working' }"
                :title="id"
                :aria-label="'查看关联任务 ' + id"
                @click="emit('openTask', id)"
              >
                {{ id.length > 20 ? id.slice(0, 4) + '…' + id.slice(-4) : id }}
              </button>
            </span>
            <span class="wi-card-footer"
              ><span class="wi-group">{{ item.current_employee }}</span
              ><time class="wi-time">{{
                new Date(item.updated_at).toLocaleString('zh-CN')
              }}</time></span
            >
          </article>
        </div>
      </section>
    </div>
    <nav
      v-if="previous.length || next"
      class="task-page-nav"
      aria-label="事项分页"
    >
      <NButton
        size="small"
        :disabled="loading || !previous.length"
        @click="page(false)"
        >上一页</NButton
      ><NButton size="small" :disabled="loading || !next" @click="page(true)"
        >下一页</NButton
      >
    </nav>
    <NDrawer
      :show="!!selected"
      :width="'min(640px, 95vw)'"
      @update:show="
        (v) => {
          if (!v) emit('open');
        }
      "
    >
      <NDrawerContent title="事项详情" closable
        ><WorkItemTimeline
          v-if="selected"
          :workspace="workspace"
          :item-id="selected"
          show-tasks
          @open-task="(id) => emit('openTask', id)"
      /></NDrawerContent>
    </NDrawer>
  </section>
</template>
<style scoped src="../styles/task-center.css"></style>
<style scoped>
/* 1. Kanban Board */
.kanban-container {
  display: flex;
  flex-direction: column;
  gap: 12px;
}
.kanban-intro-bar {
  background: var(--id-bg-surface);
  border: 1px solid var(--id-border-subtle);
  padding: 10px 16px;
  border-radius: var(--id-radius-md);
  font-size: 13px;
  color: var(--id-text-secondary);
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 12px;
  line-height: 1.5;
}
.intro-text strong {
  color: var(--id-text-primary);
  font-weight: 600;
}
.stats-badge {
  font-size: 12px;
  font-weight: 500;
  font-variant-numeric: tabular-nums;
  color: var(--id-text-primary);
  background: var(--id-bg-muted);
  border: 1px solid var(--id-border-subtle);
  padding: 3px 10px;
  border-radius: var(--id-radius-pill);
  white-space: nowrap;
}

.kanban-board {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: 14px;
  align-items: stretch;
  overflow-x: auto;
  padding-bottom: 4px;
}
.kanban-lane {
  background: var(--id-bg-subtle);
  border: 1px solid var(--id-border-subtle);
  border-radius: var(--id-radius-lg);
  display: flex;
  flex-direction: column;
  min-height: 500px;
}
.lane-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  padding: 10px 14px;
  border-bottom: 1px solid var(--id-border-subtle);
  background: var(--id-bg-surface);
  border-radius: var(--id-radius-lg) var(--id-radius-lg) 0 0;
}
.lane-title {
  font-size: 13px;
  font-weight: 600;
  color: var(--id-text-primary);
}
.lane-count {
  background: var(--id-bg-muted);
  border: 1px solid var(--id-border-subtle);
  color: var(--id-text-secondary);
  font-size: 12px;
  font-weight: 600;
  font-variant-numeric: tabular-nums;
  padding: 1px 8px;
  border-radius: var(--id-radius-pill);
  font-family: var(--id-font-mono);
}
.lane-body {
  padding: 12px;
  display: flex;
  flex-direction: column;
  gap: 10px;
  flex: 1;
}
.empty-lane-placeholder {
  display: flex;
  align-items: center;
  justify-content: center;
  color: var(--id-text-muted);
  font-size: 12px;
  height: 120px;
}

.wi-card {
  background: var(--id-bg-surface);
  border: 1px solid var(--id-border-subtle);
  border-radius: var(--id-radius-md);
  padding: 12px 14px;
  cursor: pointer;
  transition:
    border-color var(--id-transition-fast),
    background-color var(--id-transition-fast);
  display: flex;
  flex-direction: column;
  gap: 8px;
  outline: none;
}
.wi-card:hover {
  border-color: var(--id-border-strong);
  background: var(--id-bg-surface);
}
.wi-card:focus-within {
  box-shadow: var(--id-focus-ring);
  border-color: var(--id-accent-primary);
}
.wi-card-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 8px;
}
.wi-code {
  font-family: var(--id-font-mono);
  font-variant-numeric: tabular-nums;
  font-size: 12px;
  color: var(--id-text-muted);
  font-weight: 600;
}
.wi-title {
  font-size: 13px;
  font-weight: 600;
  color: var(--id-text-primary);
  margin: 0;
  line-height: 1.45;
}
.wi-summary {
  font-size: 13px;
  color: var(--id-text-secondary);
  margin: 0;
  line-height: 1.5;
}
.wi-handoff-pill {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 4px 6px;
  background: var(--id-bg-subtle);
  padding: 6px 8px;
  border-radius: var(--id-radius-sm);
  font-size: 12px;
  color: var(--id-text-secondary);
  border: 1px solid var(--id-border-hairline);
}
.handoff-label {
  color: var(--id-text-muted);
}
.handoff-node {
  color: var(--id-text-primary);
  font-weight: 500;
}
.wi-handoff-pill .arrow {
  color: var(--id-text-muted);
}
.wi-tasks-link-row {
  display: flex;
  align-items: center;
  gap: 6px;
  flex-wrap: wrap;
}
.link-label {
  font-size: 12px;
  color: var(--id-text-muted);
}
.task-quick-tag {
  background: var(--id-bg-subtle);
  border: 1px solid var(--id-border-subtle);
  color: var(--id-text-primary);
  font-family: var(--id-font-mono);
  font-variant-numeric: tabular-nums;
  font-size: 12px;
  padding: 2px 8px;
  border-radius: var(--id-radius-sm);
  cursor: pointer;
  transition:
    background-color var(--id-transition-fast),
    border-color var(--id-transition-fast);
}
.task-quick-tag:hover {
  background: var(--id-bg-hover);
  border-color: var(--id-border-strong);
}
.task-quick-tag:focus-visible {
  outline: none;
  box-shadow: var(--id-focus-ring);
}
.task-quick-tag.highlight {
  background: var(--id-status-warning-soft);
  border-color: var(--id-status-warning-border);
  color: var(--id-status-warning);
}
.task-quick-tag.highlight:hover {
  border-color: var(--id-status-warning-dot);
}
.wi-card-footer {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 8px;
  font-size: 12px;
  color: var(--id-text-muted);
  border-top: 1px solid var(--id-border-hairline);
  padding-top: 8px;
  margin-top: auto;
}
.wi-group {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.wi-time {
  font-family: var(--id-font-mono);
  font-variant-numeric: tabular-nums;
  white-space: nowrap;
  flex-shrink: 0;
}

/* The title button covers the card. Related task buttons remain individually focusable. */
.wi-title {
  border: 0;
  background: transparent;
  padding: 0;
  text-align: left;
  cursor: pointer;
}
.wi-title::after {
  content: '';
  position: absolute;
  inset: 0;
}
.task-quick-tag {
  position: relative;
}
.wi-card {
  position: relative;
  width: 100%;
  text-align: left;
  font: inherit;
  color: inherit;
}
@media (max-width: 1050px) {
  .kanban-board {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }
}
@media (max-width: 760px) {
  .kanban-board {
    grid-template-columns: 1fr;
  }
  .kanban-lane {
    min-height: 220px;
  }
  .kanban-intro-bar {
    flex-wrap: wrap;
  }
}
</style>

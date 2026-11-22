<script setup lang="ts">
import { computed, ref } from 'vue';
import { NButton, NTab, NTabs } from 'naive-ui';
import PlatformTasksView from './PlatformTasksView.vue';
import PlatformWorkItems from './PlatformWorkItems.vue';
import PlatformTaskSpacesView from './PlatformTaskSpacesView.vue';
import PlatformHumanInbox from './PlatformHumanInbox.vue';
const props = defineProps<{
  workspace: string;
  workspacePending?: number;
  initialTab?: string | undefined;
  selectedTaskId?: string | undefined;
  selectedEmployeeId?: string | undefined;
  selectedWorkId?: string | undefined;
  selectedApprovalId?: string | undefined;
}>();
const emit = defineEmits<{
  count: [number];
  navigate: [view: string, param?: string, id?: string, type?: string];
}>();
const page = ref<{ refresh: () => void } | null>(null);
const currentTab = computed(() => {
  if (props.selectedApprovalId) return 'approvals';
  if (props.selectedWorkId) return 'work-items';
  if (props.selectedTaskId || props.selectedEmployeeId) return 'tasks';
  const tab = props.initialTab;
  if (tab === 'approvals-resolved' || tab === 'approvals-all') return 'approvals';
  return tab === 'tasks' || tab === 'spaces' || tab === 'approvals'
    ? tab
    : 'work-items';
});
function handleModuleTabChange(tab: string) {
  emit('navigate', 'tasks', tab);
}
function openTask(id: string) {
  emit('navigate', 'tasks', 'tasks', id, 'task');
}
function openWork(id?: string) {
  emit('navigate', 'tasks', 'work-items', id, 'work-item');
}
</script>
<template>
  <div class="d2-tasks-collab">
    <div class="collab-header">
      <div class="header-top-row">
        <div class="header-titles">
          <h2 class="view-title">任务与协同中心</h2>
          <p class="view-subtitle">
            跟踪业务协作事项进展、查看员工任务执行记录与待人工确认事项。
          </p>
        </div>
        <NButton size="small" secondary @click="page?.refresh()">
          刷新
        </NButton>
      </div>

      <NTabs
        :value="currentTab"
        type="segment"
        size="small"
        class="module-tabs"
        @update:value="handleModuleTabChange"
      >
        <NTab name="work-items">协作事项看板</NTab>
        <NTab name="tasks">任务记录</NTab>
        <NTab name="approvals">待人工确认</NTab>
        <NTab name="spaces">任务空间</NTab>
      </NTabs>
    </div>

    <PlatformWorkItems
      v-if="currentTab === 'work-items'"
      ref="page"
      :workspace="workspace"
      :selected="selectedWorkId"
      @open="openWork"
      @open-task="openTask"
    />
    <PlatformTasksView
      v-else-if="currentTab === 'tasks'"
      ref="page"
      :selected-task-id="selectedTaskId"
      :selected-employee-id="selectedEmployeeId"
      @navigate="(...args) => emit('navigate', ...args)"
    />
    <PlatformHumanInbox
      v-else-if="currentTab === 'approvals'"
      ref="page"
      read-only
      :workspace="workspace"
      :workspace-pending="workspacePending"
      :initial-filter="initialTab === 'approvals-all' ? 'all' : initialTab === 'approvals-resolved' ? 'resolved' : 'pending'"
      @filter="value => emit('navigate', 'tasks', value === 'pending' ? 'approvals' : 'approvals-' + value)"
      :selected-id="selectedApprovalId"
      @count="(n) => emit('count', n)"
    />
    <PlatformTaskSpacesView
      v-else
      ref="page"
      read-only
      :workspace="workspace"
      @open-work="openWork"
    />
  </div>
</template>
<style scoped>
.d2-tasks-collab {
  display: flex;
  flex-direction: column;
  gap: 16px;
}
.collab-header {
  display: flex;
  flex-direction: column;
  gap: 12px;
  padding: 4px 0 4px 0;
}
.header-top-row {
  display: flex;
  justify-content: space-between;
  align-items: flex-start;
  gap: 16px;
}
.view-title {
  font-size: 20px;
  font-weight: 600;
  letter-spacing: -0.02em;
  color: var(--id-text-primary);
  margin: 0 0 4px 0;
}
.view-subtitle {
  font-size: 13px;
  color: var(--id-text-secondary);
  margin: 0;
}

/* Segmented Tabs (Neutral Zinc track + elevated surface active pill) */
:deep(.module-tabs.n-tabs) {
  width: fit-content;
  max-width: 100%;
}
:deep(.module-tabs.n-tabs--segment-type .n-tabs-rail) {
  background: var(--id-bg-muted) !important;
  border: 1px solid var(--id-border-subtle);
  padding: 3px;
  border-radius: var(--id-radius-md);
}
:deep(.module-tabs.n-tabs--segment-type .n-tabs-tab) {
  color: var(--id-text-secondary) !important;
  font-weight: 500 !important;
  font-size: 13px !important;
  padding: 5px 14px !important;
  white-space: nowrap;
}
:deep(.module-tabs.n-tabs--segment-type .n-tabs-tab .n-tabs-tab__label) {
  color: var(--id-text-secondary) !important;
  font-weight: 500 !important;
  font-variant-numeric: tabular-nums;
  display: inline-block !important;
  visibility: visible !important;
}
:deep(.module-tabs.n-tabs--segment-type .n-tabs-capsule) {
  background: var(--id-bg-surface) !important;
  border: 1px solid var(--id-border-subtle);
  box-shadow: none !important;
  border-radius: var(--id-radius-sm);
}
:deep(.module-tabs.n-tabs--segment-type .n-tabs-tab.n-tabs-tab--active),
:deep(
  .module-tabs.n-tabs--segment-type
    .n-tabs-tab.n-tabs-tab--active
    .n-tabs-tab__label
) {
  color: var(--id-text-primary) !important;
  font-weight: 600 !important;
}

@media (max-width: 760px) {
  .module-tabs {
    max-width: 100%;
    width: 100%;
  }
  :deep(.module-tabs .n-tabs-rail) {
    flex-wrap: wrap;
    height: auto;
    padding: 4px;
    gap: 4px;
  }
  :deep(.module-tabs .n-tabs-capsule) {
    display: none;
  }
  :deep(.module-tabs .n-tabs-tab-wrapper) {
    flex: 1 1 110px;
  }
  :deep(.module-tabs .n-tabs-tab) {
    padding: 6px 8px;
    border-radius: 6px;
  }
  :deep(.module-tabs .n-tabs-tab--active) {
    background: var(--id-bg-surface);
    box-shadow: var(--id-shadow-sm);
  }
  .header-top-row {
    align-items: center;
  }
}
</style>

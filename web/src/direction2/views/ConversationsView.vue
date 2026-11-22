<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue';
import { NButton, NEmpty, NInput, NSkeleton, useMessage } from 'naive-ui';
import D2EmployeeAvatar from '../components/D2EmployeeAvatar.vue';
import D2ChannelLogo from '../components/D2ChannelLogo.vue';
import EmployeeSingleChatBox from '../components/EmployeeSingleChatBox.vue';
import { chatApi } from '../api';
import type { DigitalEmployee, EmployeeConversationSession, ChatEmployeeSummaryItem } from '../types';

const props = defineProps<{
  selectedEmployeeId?: string | undefined;
}>();

const emit = defineEmits<{
  navigate: [target: string, param?: string, targetId?: string];
}>();

const message = useMessage();
const searchQuery = ref('');
const chatEntries = ref<ChatEmployeeSummaryItem[]>([]);
const activeEmployeeId = ref<string>(props.selectedEmployeeId || '');
const mobileActivePane = ref<'list' | 'chat'>(props.selectedEmployeeId ? 'chat' : 'list');
const isLoading = ref(false);
const hasLoadedOnce = ref(false);
const loadError = ref<string | null>(null);

async function loadChatDirectory() {
  isLoading.value = true;
  loadError.value = null;
  try {
    const list = await chatApi.listChatEmployeesSummary();
    chatEntries.value = list;
    if (props.selectedEmployeeId && list.some(item => item.employee.id === props.selectedEmployeeId)) {
      activeEmployeeId.value = props.selectedEmployeeId;
      mobileActivePane.value = 'chat';
    } else if (!activeEmployeeId.value && list.length > 0) {
      activeEmployeeId.value = list[0]!.employee.id;
    }
    hasLoadedOnce.value = true;
  } catch (err) {
    const msg = err instanceof Error ? err.message : '加载工作区对话列表失败';
    loadError.value = msg;
    if (hasLoadedOnce.value) {
      message.error(msg);
    }
  } finally {
    isLoading.value = false;
  }
}

onMounted(loadChatDirectory);

watch(
  () => props.selectedEmployeeId,
  id => {
    if (id) {
      activeEmployeeId.value = id;
      mobileActivePane.value = 'chat';
    }
  },
  { immediate: true }
);

const employees = computed(() => chatEntries.value.map(item => item.employee));

const filteredEmployees = computed(() => {
  const q = searchQuery.value.trim().toLowerCase();
  if (!q) return employees.value;
  return employees.value.filter(
    e => e.name.toLowerCase().includes(q) || e.role.toLowerCase().includes(q)
  );
});

const selectedEmployee = computed<DigitalEmployee | undefined>(() => {
  return (
    employees.value.find(e => e.id === activeEmployeeId.value) ||
    employees.value[0]
  );
});

function selectEmployee(emp: DigitalEmployee) {
  activeEmployeeId.value = emp.id;
  mobileActivePane.value = 'chat';
}

function getLastMessage(empId: string): string {
  const summary = chatEntries.value.find(item => item.employee.id === empId)?.sessionSummary;
  return summary?.lastMessagePreview || '点击开始工作区协同对话';
}

function getLastTime(empId: string): string {
  const summary = chatEntries.value.find(item => item.employee.id === empId)?.sessionSummary;
  return summary?.lastMessageTime || summary?.updatedAt || '';
}

function onTurnCompleted(updatedSession: EmployeeConversationSession) {
  const idx = chatEntries.value.findIndex(item => item.employee.id === updatedSession.employeeId);
  if (idx >= 0) {
    const last = updatedSession.messages[updatedSession.messages.length - 1];
    chatEntries.value[idx]!.sessionSummary = {
      id: updatedSession.id,
      employeeId: updatedSession.employeeId,
      title: updatedSession.title,
      updatedAt: updatedSession.updatedAt,
      lastMessagePreview: last?.content || '',
      lastMessageTime: last?.time || updatedSession.updatedAt,
      totalMessages: updatedSession.totalMessages ?? updatedSession.messages.length,
      sessionTasksCount: updatedSession.sessionTasks.length,
      activeExecutionId: updatedSession.activeExecutionId
    };
  }
}
</script>

<template>
  <div class="d2-conversations-page">
    <div class="conv-header">
      <div>
        <h2 class="view-title">工作区对话</h2>
        <p class="view-subtitle">
          选择 AI 员工下发工作指令或跟进协作进展。
        </p>
      </div>
      <NButton size="small" secondary @click="emit('navigate', 'employees', 'overview', selectedEmployee?.id)">
        管理当前员工配置
      </NButton>
    </div>

    <!-- 首屏加载骨架屏 -->
    <div v-if="isLoading && !hasLoadedOnce" class="conversations-layout skeleton-conv-layout">
      <div class="chat-employee-sidebar skeleton-sidebar">
        <div class="sidebar-header">
          <NSkeleton text style="width: 60%; height: 18px;" />
          <NSkeleton text style="width: 100%; height: 28px; margin-top: 8px;" />
        </div>
        <div class="sidebar-emp-list">
          <div v-for="i in 3" :key="i" class="skeleton-emp-item">
            <NSkeleton circle style="width: 32px; height: 32px; flex-shrink: 0;" />
            <div style="flex: 1; display: flex; flex-direction: column; gap: 6px;">
              <NSkeleton text style="width: 65%; height: 14px;" />
              <NSkeleton text style="width: 90%; height: 12px;" />
            </div>
          </div>
        </div>
      </div>
      <div class="conv-main-slot skeleton-chat-slot">
        <div class="skeleton-chat-head">
          <NSkeleton text style="width: 240px; height: 20px;" />
          <NSkeleton text style="width: 120px; height: 26px;" />
        </div>
        <div class="skeleton-chat-body">
          <NSkeleton text style="width: 58%; height: 64px; border-radius: 8px;" />
          <NSkeleton text style="width: 44%; height: 48px; border-radius: 8px; align-self: flex-end;" />
          <NSkeleton text style="width: 66%; height: 72px; border-radius: 8px;" />
        </div>
      </div>
    </div>

    <!-- 首屏可恢复错误态 -->
    <div v-else-if="loadError && !hasLoadedOnce" class="error-state-card">
      <div class="error-state-title">无法加载工作区对话列表</div>
      <div class="error-state-desc">{{ loadError }}</div>
      <NButton type="primary" size="small" :loading="isLoading" @click="loadChatDirectory()">
        重试加载
      </NButton>
    </div>

    <div v-else class="conversations-layout" :class="`mobile-pane-${mobileActivePane}`">
      <!-- 外部跨员工对话切换列表 -->
      <aside class="chat-employee-sidebar">
        <div class="sidebar-header">
          <div class="sh-top">
            <strong class="sh-title">可协作 AI 员工</strong>
            <span class="sidebar-count-pill">{{ filteredEmployees.length }} 位</span>
          </div>
          <NInput
            v-model:value="searchQuery"
            size="small"
            placeholder="搜索员工名称或岗位..."
            clearable
            style="margin-top: 8px;"
          />
        </div>

        <div class="sidebar-emp-list">
          <div v-if="filteredEmployees.length === 0" class="sidebar-empty">
            <NEmpty description="暂无匹配的 AI 员工" size="small" />
          </div>
          <button
            v-for="emp in filteredEmployees"
            :key="emp.id"
            type="button"
            class="sidebar-emp-item"
            :class="{ active: emp.id === selectedEmployee?.id }"
            @click="selectEmployee(emp)"
          >
            <D2EmployeeAvatar :name="emp.name" :preset="emp.avatar" :identity-key="emp.id" size="sm" />
            <div class="emp-item-main">
              <div class="emp-item-top">
                <span class="emp-item-name">
                  <span class="emp-item-name-text" :title="emp.name">{{ emp.name }}</span>
                  <span class="mini-ai">AI</span>
                </span>
                <span class="emp-item-time">{{ getLastTime(emp.id) }}</span>
              </div>
              <div class="emp-item-channels">
                <D2ChannelLogo
                  v-for="ch in emp.channels"
                  :key="ch.platform"
                  :platform="ch.platform"
                  :size="13"
                  :title="ch.platformName"
                />
                <span class="emp-item-role" :title="emp.role">{{ emp.role }}</span>
              </div>
              <div class="emp-item-msg" :title="getLastMessage(emp.id)">{{ getLastMessage(emp.id) }}</div>
            </div>
          </button>
        </div>
      </aside>

      <!-- 右侧：该 AI 员工唯一的持久 1:1 对话框 -->
      <div class="conv-main-slot">
        <EmployeeSingleChatBox
          v-if="selectedEmployee"
          :employee="selectedEmployee"
          @turn-completed="onTurnCompleted"
          @back-to-list="mobileActivePane = 'list'"
        />
        <div v-else class="conv-empty-slot">
          <NEmpty description="请选择或新建一位 AI 员工后开始协同对话">
            <template #extra>
              <NButton type="primary" size="small" @click="emit('navigate', 'employees', 'create')">
                + 新建 AI 员工
              </NButton>
            </template>
          </NEmpty>
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped>
.d2-conversations-page {
  display: flex;
  flex-direction: column;
  gap: 16px;
}
.conv-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
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
.conversations-layout {
  display: flex;
  gap: 16px;
  align-items: stretch;
  height: calc(100vh - 156px);
  min-height: 520px;
}
.chat-employee-sidebar {
  width: 248px;
  flex-shrink: 0;
  border: 1px solid var(--id-border-subtle);
  border-radius: var(--id-radius-lg);
  display: flex;
  flex-direction: column;
  background: var(--id-bg-surface);
  overflow: hidden;
  height: 100%;
}
@media (max-width: 1180px) {
  .chat-employee-sidebar {
    width: 224px;
  }
}
.sidebar-header {
  padding: 12px 14px;
  border-bottom: 1px solid var(--id-border-subtle);
  background: var(--id-bg-subtle);
}
.sh-top {
  display: flex;
  justify-content: space-between;
  align-items: center;
  font-size: 13px;
  color: var(--id-text-primary);
}
.sh-title {
  font-weight: 600;
}
.sidebar-count-pill {
  font-size: 12px;
  font-family: var(--id-font-mono);
  font-variant-numeric: tabular-nums;
  font-weight: 500;
  color: var(--id-text-secondary);
  background: var(--id-bg-muted);
  border: 1px solid var(--id-border-subtle);
  padding: 1px 8px;
  border-radius: var(--id-radius-pill);
}
.sidebar-emp-list {
  flex: 1;
  overflow-y: auto;
  padding: 8px;
  display: flex;
  flex-direction: column;
  gap: 4px;
}
.sidebar-empty {
  padding: 36px 12px;
}
.sidebar-emp-item {
  width: 100%;
  text-align: left;
  background: transparent;
  border: 1px solid transparent;
  font: inherit;
  display: flex;
  align-items: flex-start;
  gap: 10px;
  padding: 10px;
  border-radius: var(--id-radius-md);
  cursor: pointer;
  transition: background-color var(--id-transition-fast), border-color var(--id-transition-fast);
}
.sidebar-emp-item:hover {
  background: var(--id-bg-hover);
}
.sidebar-emp-item:focus-visible {
  outline: none;
  box-shadow: var(--id-focus-ring);
}
.sidebar-emp-item.active {
  background: var(--id-bg-muted);
  border-color: var(--id-border-strong);
}
.emp-item-main {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 4px;
}
.emp-item-top {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 6px;
}
.emp-item-name {
  font-size: 13px;
  font-weight: 600;
  color: var(--id-text-primary);
  display: inline-flex;
  align-items: center;
  gap: 5px;
  min-width: 0;
}
.emp-item-name-text {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.mini-ai {
  font-size: 12px;
  font-family: var(--id-font-mono);
  font-weight: 500;
  color: var(--id-text-secondary);
  background: var(--id-bg-muted);
  border: 1px solid var(--id-border-subtle);
  padding: 0 5px;
  border-radius: var(--id-radius-sm);
  line-height: 1.4;
  flex-shrink: 0;
}
.sidebar-emp-item.active .mini-ai {
  background: var(--id-bg-surface);
}
.emp-item-time {
  font-family: var(--id-font-mono);
  font-variant-numeric: tabular-nums;
  font-size: 12px;
  color: var(--id-text-muted);
  flex-shrink: 0;
}
.emp-item-channels {
  display: flex;
  align-items: center;
  gap: 5px;
  min-width: 0;
}
.emp-item-role {
  font-size: 12px;
  color: var(--id-text-secondary);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.emp-item-msg {
  font-size: 12px;
  color: var(--id-text-muted);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.conv-main-slot {
  flex: 1;
  min-width: 0;
  height: 100%;
}
.conv-empty-slot {
  height: 100%;
  display: flex;
  align-items: center;
  justify-content: center;
  background: var(--id-bg-surface);
  border: 1px solid var(--id-border-subtle);
  border-radius: var(--id-radius-lg);
}

/* Skeleton & Error States */
.skeleton-emp-item {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 10px;
}
.skeleton-chat-slot {
  background: var(--id-bg-surface);
  border: 1px solid var(--id-border-subtle);
  border-radius: var(--id-radius-lg);
  display: flex;
  flex-direction: column;
}
.skeleton-chat-head {
  padding: 14px 16px;
  border-bottom: 1px solid var(--id-border-subtle);
  display: flex;
  justify-content: space-between;
  align-items: center;
}
.skeleton-chat-body {
  flex: 1;
  padding: 20px;
  display: flex;
  flex-direction: column;
  gap: 14px;
}
.error-state-card {
  background: var(--id-bg-surface);
  border: 1px solid var(--id-status-danger-border);
  border-radius: var(--id-radius-lg);
  padding: 40px 24px;
  text-align: center;
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 10px;
}
.error-state-title {
  font-size: 15px;
  font-weight: 600;
  color: var(--id-text-primary);
}
.error-state-desc {
  font-size: 13px;
  color: var(--id-text-secondary);
}

@media (max-width: 960px) {
  .conversations-layout {
    flex-direction: column;
    height: auto;
    min-height: 0;
  }
  .conversations-layout.mobile-pane-chat .chat-employee-sidebar {
    display: none;
  }
  .conversations-layout.mobile-pane-list .conv-main-slot {
    display: none;
  }
  .chat-employee-sidebar {
    width: 100%;
    height: auto;
    max-height: calc(100vh - 200px);
    min-height: 360px;
  }
  .conv-main-slot {
    height: auto;
    min-height: 500px;
  }
}

@media (max-width: 760px) {
  .conv-header {
    flex-direction: column;
    align-items: flex-start;
    gap: 8px;
  }
}
</style>

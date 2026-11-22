<script setup lang="ts">
import { nextTick, onMounted, onUnmounted, ref, watch } from 'vue';
import { NButton, NInput, NSkeleton, useMessage, type InputInst } from 'naive-ui';
import D2EmployeeAvatar from './D2EmployeeAvatar.vue';
import D2ChannelLogo from './D2ChannelLogo.vue';
import { chatApi, getOrCreateActionIdempotencyKey, clearActionIdempotencyKey } from '../api';
import { getEmployeeStatusMeta, getExecutionEngineMeta, getTaskStatusMeta } from '../utils/presentation';
import type { DigitalEmployee, EmployeeConversationSession } from '../types';

const props = defineProps<{
  employee: DigitalEmployee;
}>();

const emit = defineEmits<{
  turnCompleted: [session: EmployeeConversationSession];
  backToList: [];
}>();

const message = useMessage();
const messagesScrollRef = ref<HTMLElement | null>(null);
const chatInputRef = ref<InputInst | null>(null);

function scrollToBottom() {
  void nextTick(() => {
    const el = messagesScrollRef.value;
    if (el) {
      el.scrollTop = el.scrollHeight;
    }
  });
}

// 严格一对一唯一持久会话：一个用户 <-> 一个 AI 员工仅存在唯一的持久对话窗口
const session = ref<EmployeeConversationSession>({
  id: `sess-${props.employee.id}`,
  employeeId: props.employee.id,
  title: `与「${props.employee.name}」的唯一直连对话`,
  createdAt: '今天',
  updatedAt: '加载中',
  messages: [],
  sessionTasks: []
});

const chatInputText = ref('');
const isGenerating = ref(false);
const activeExecutionId = ref<string | null>(null);
const activeExecutionStatus = ref<'accepted' | 'running' | 'completed' | 'interrupted' | 'failed' | null>(null);
const activeThinkingText = ref('');
const showInSessionTasks = ref(false);
const simulateSendError = ref(false);
const lastFailedUserMessage = ref<string | null>(null);
const loadingOlder = ref(false);
const isInterrupting = ref(false);
const isLoadingSession = ref(false);
const hasLoadedSession = ref(false);
const sessionLoadError = ref<string | null>(null);
const expandedThinkingIds = ref<Record<string, boolean>>({});
let execPollTimer: ReturnType<typeof setInterval> | null = null;
let chatRequestSeq = 0;
let pollSeq = 0;

function tagToneClass(type: 'info' | 'warning' | 'success' | 'error' | 'default'): 'info' | 'warning' | 'success' | 'danger' | 'neutral' {
  if (type === 'error') return 'danger';
  if (type === 'default') return 'neutral';
  return type;
}

function toggleThinking(msgId: string) {
  expandedThinkingIds.value[msgId] = !expandedThinkingIds.value[msgId];
}

function stopExecutionPoll() {
  pollSeq += 1;
  if (execPollTimer) {
    clearInterval(execPollTimer);
    execPollTimer = null;
  }
}

function startExecutionPoll(empId: string, executionId: string, expectedSeq = chatRequestSeq) {
  stopExecutionPoll();
  const token = pollSeq;
  activeExecutionId.value = executionId;
  execPollTimer = setInterval(async () => {
    if (
      token !== pollSeq ||
      expectedSeq !== chatRequestSeq ||
      props.employee.id !== empId ||
      activeExecutionId.value !== executionId
    ) {
      stopExecutionPoll();
      return;
    }
    try {
      const exec = await chatApi.getExecutionStatus(empId, executionId);
      if (
        token !== pollSeq ||
        expectedSeq !== chatRequestSeq ||
        props.employee.id !== empId ||
        activeExecutionId.value !== executionId
      ) {
        stopExecutionPoll();
        return;
      }
      activeExecutionStatus.value = exec.status;
      if (exec.thinking) activeThinkingText.value = exec.thinking;
      if (exec.status === 'completed' || exec.status === 'interrupted' || exec.status === 'failed') {
        stopExecutionPoll();
        const fresh = await chatApi.getEmployeeConversation(empId, { limit: 30 });
        if (expectedSeq !== chatRequestSeq || props.employee.id !== empId) return;
        isGenerating.value = false;
        activeExecutionId.value = null;
        activeThinkingText.value = '';
        session.value = fresh;
        emit('turnCompleted', fresh);
        if (exec.status === 'failed') {
          message.error(exec.errorMessage || '对话执行失败');
        }
      }
    } catch {
      // ignore transient poll hiccup
    }
  }, 350);
}

async function loadConversation(empId: string) {
  const seq = ++chatRequestSeq;
  stopExecutionPoll();
  isLoadingSession.value = true;
  sessionLoadError.value = null;
  try {
    const loaded = await chatApi.getEmployeeConversation(empId, { limit: 30 });
    if (seq !== chatRequestSeq || props.employee.id !== empId) return;
    session.value = loaded;
    hasLoadedSession.value = true;
    if (loaded.activeExecutionId) {
      isGenerating.value = true;
      activeExecutionStatus.value = 'running';
      activeThinkingText.value = '正在恢复之前的任务进度...';
      startExecutionPoll(empId, loaded.activeExecutionId, seq);
    } else {
      isGenerating.value = false;
      activeExecutionId.value = null;
      activeExecutionStatus.value = null;
      activeThinkingText.value = '';
    }
  } catch (err) {
    if (seq !== chatRequestSeq || props.employee.id !== empId) return;
    const msg = err instanceof Error ? err.message : '加载对话上下文失败';
    sessionLoadError.value = msg;
    if (hasLoadedSession.value) {
      message.error(msg);
    }
  } finally {
    if (seq === chatRequestSeq && props.employee.id === empId) {
      isLoadingSession.value = false;
    }
  }
}

async function loadOlderMessages() {
  if (!session.value.hasMoreMessages || !session.value.nextBeforeCursor || loadingOlder.value) return;
  loadingOlder.value = true;
  const empId = props.employee.id;
  const seq = chatRequestSeq;
  try {
    const older = await chatApi.getEmployeeConversation(empId, {
      limit: 20,
      before: session.value.nextBeforeCursor
    });
    if (seq !== chatRequestSeq || props.employee.id !== empId) return;
    session.value = {
      ...session.value,
      messages: [...older.messages, ...session.value.messages],
      hasMoreMessages: older.hasMoreMessages,
      nextBeforeCursor: older.nextBeforeCursor
    };
  } catch (err) {
    if (seq !== chatRequestSeq || props.employee.id !== empId) return;
    message.error(err instanceof Error ? err.message : '加载历史消息失败');
  } finally {
    loadingOlder.value = false;
  }
}

watch(
  () => props.employee.id,
  empId => {
    chatRequestSeq += 1;
    stopExecutionPoll();
    isInterrupting.value = false;
    lastFailedUserMessage.value = null;
    chatInputText.value = '';
    isGenerating.value = false;
    activeExecutionId.value = null;
    activeExecutionStatus.value = null;
    activeThinkingText.value = '';
    hasLoadedSession.value = false;
    sessionLoadError.value = null;
    expandedThinkingIds.value = {};
    showInSessionTasks.value = false;
    loadConversation(empId);
  }
);

watch(
  () => [session.value.messages.length, isGenerating.value],
  ([newLen, generating], [oldLen]) => {
    if (loadingOlder.value) return;
    if (generating || (typeof oldLen === 'number' && typeof newLen === 'number' && newLen > oldLen)) {
      scrollToBottom();
    }
  }
);

onMounted(() => {
  loadConversation(props.employee.id);
});

onUnmounted(() => {
  chatRequestSeq += 1;
  stopExecutionPoll();
});

async function sendChatMessage(retryText?: string) {
  const userText = retryText || chatInputText.value.trim();
  if (!userText || isGenerating.value) return;
  if (!retryText) chatInputText.value = '';

  const targetEmp = props.employee;
  if (targetEmp.status === 'disabled') {
    message.error('该员工已被管理员停用，无法接收与处理新消息');
    return;
  }

  if (simulateSendError.value || userText.includes('/fail')) {
    lastFailedUserMessage.value = userText;
    message.error('消息发送失败：网络超时或暂无访问权限');
    return;
  }
  lastFailedUserMessage.value = null;

  const seq = ++chatRequestSeq;
  const chatPayload = { content: userText };
  const idemKey = getOrCreateActionIdempotencyKey('chat_turn', targetEmp.id, 'chat', chatPayload);
  isGenerating.value = true;
  activeExecutionStatus.value = 'accepted';
  activeThinkingText.value = '正在理解您的指令...';

  try {
    const submitted = await chatApi.sendTurn(targetEmp.id, userText, {
      clientMessageId: `cmsg-${idemKey}`,
      idempotencyKey: idemKey
    });
    clearActionIdempotencyKey('chat_turn', targetEmp.id, 'chat', chatPayload);
    if (seq !== chatRequestSeq || props.employee.id !== targetEmp.id) return;
    session.value = submitted.session;
    activeExecutionId.value = submitted.executionId;
    activeExecutionStatus.value = submitted.status;
    startExecutionPoll(targetEmp.id, submitted.executionId, seq);
  } catch (err) {
    if (seq !== chatRequestSeq || props.employee.id !== targetEmp.id) return;
    isGenerating.value = false;
    activeExecutionStatus.value = 'failed';
    activeThinkingText.value = '';
    lastFailedUserMessage.value = userText;
    message.error(err instanceof Error ? err.message : '对话消息发送失败');
  }
}

function retryLastMessage() {
  if (lastFailedUserMessage.value) {
    const cleanText = lastFailedUserMessage.value.replace('/fail', '').trim() || '重试发送需求';
    simulateSendError.value = false;
    sendChatMessage(cleanText);
  }
}

async function interruptGeneration() {
  if (!isGenerating.value || isInterrupting.value) return;
  const targetEmpId = props.employee.id;
  const targetExecId = activeExecutionId.value;
  const seq = chatRequestSeq;
  isInterrupting.value = true;
  try {
    if (targetExecId) {
      const res = await chatApi.interruptExecution(targetEmpId, targetExecId);
      if (seq !== chatRequestSeq || props.employee.id !== targetEmpId) return;
      session.value = res.session;
      activeExecutionStatus.value = res.execution.status;
      if (res.execution.status === 'interrupted' || res.execution.status === 'completed' || res.execution.status === 'failed') {
        stopExecutionPoll();
        isGenerating.value = false;
        activeExecutionId.value = null;
        activeThinkingText.value = '';
        if (res.execution.status === 'interrupted') {
          message.warning('已停止当前处理，对话记录已保留。');
        }
      }
    } else {
      const updated = await chatApi.interruptConversation(targetEmpId);
      if (seq !== chatRequestSeq || props.employee.id !== targetEmpId) return;
      stopExecutionPoll();
      session.value = updated;
      isGenerating.value = false;
      activeExecutionId.value = null;
      activeExecutionStatus.value = 'interrupted';
      activeThinkingText.value = '';
      message.warning('已停止当前处理，对话记录已保留。');
    }
  } catch (err) {
    if (seq !== chatRequestSeq || props.employee.id !== targetEmpId) return;
    message.error(err instanceof Error ? `停止请求失败：${err.message}` : '停止执行失败，请稍后刷新查看');
    // 真实回查服务端执行与会话状态，禁止在中断失败后伪造“已中断”
    try {
      const [exec, fresh] = await Promise.all([
        targetExecId ? chatApi.getExecutionStatus(targetEmpId, targetExecId).catch(() => null) : Promise.resolve(null),
        chatApi.getEmployeeConversation(targetEmpId, { limit: 30 })
      ]);
      if (seq !== chatRequestSeq || props.employee.id !== targetEmpId) return;
      session.value = fresh;
      if (exec) {
        activeExecutionStatus.value = exec.status;
        if (exec.status === 'completed' || exec.status === 'interrupted' || exec.status === 'failed') {
          stopExecutionPoll();
          isGenerating.value = false;
          activeExecutionId.value = null;
          activeThinkingText.value = '';
        } else {
          isGenerating.value = true;
          activeExecutionId.value = exec.executionId;
          startExecutionPoll(targetEmpId, exec.executionId, seq);
        }
      }
    } catch {
      // 无法核对状态时保持未确认中断前的服务端推断状态
    }
  } finally {
    if (seq === chatRequestSeq && props.employee.id === targetEmpId) {
      isInterrupting.value = false;
    }
  }
}

function insertNewlineAtCursor(target: HTMLTextAreaElement) {
  const start = target.selectionStart ?? chatInputText.value.length;
  const end = target.selectionEnd ?? start;
  const current = chatInputText.value;
  chatInputText.value = `${current.slice(0, start)}\n${current.slice(end)}`;
  void nextTick(() => {
    const nextPos = start + 1;
    target.setSelectionRange(nextPos, nextPos);
    if (end === current.length) {
      target.scrollTop = target.scrollHeight;
    }
  });
}

function handleChatInputKeydown(e: KeyboardEvent) {
  if (e.key !== 'Enter') return;
  if (e.isComposing || e.keyCode === 229) return;

  // Control + Enter 插入换行（同时保留 Shift + Enter 原生换行）
  if (e.ctrlKey) {
    e.preventDefault();
    const textareaEl = e.target instanceof HTMLTextAreaElement ? e.target : chatInputRef.value?.textareaElRef;
    if (textareaEl) {
      insertNewlineAtCursor(textareaEl);
    } else {
      chatInputText.value += '\n';
    }
    return;
  }

  if (e.shiftKey || e.metaKey || e.altKey) return;

  e.preventDefault();
  void sendChatMessage();
}
</script>

<template>
  <div class="single-chat-shell">
    <div class="chat-viewport-card">
      <!-- 对话顶部栏：紧凑展示核心状态与岗位说明 -->
      <div class="chat-header-bar">
        <div class="ch-info-left">
          <button
            type="button"
            class="mobile-back-list-btn"
            aria-label="返回 AI 员工列表"
            @click="emit('backToList')"
          >
            ← 列表
          </button>
          <D2EmployeeAvatar :name="employee.name" :preset="employee.avatar" :identity-key="employee.id" size="sm" />
          <div class="ch-info-meta">
            <div class="ch-title-line">
              <strong class="ch-emp-name">{{ employee.name }}</strong>
              <span class="ai-tag">AI</span>
              <span class="status-pill" :class="tagToneClass(getEmployeeStatusMeta(employee.status).tagType)">
                <span class="status-dot" />
                {{ getEmployeeStatusMeta(employee.status).label }}
              </span>
              <span
                class="ch-model-txt"
                :title="`执行引擎：${getExecutionEngineMeta(employee.executionEngine).label} · 基座模型：${employee.modelName}`"
              >
                {{ getExecutionEngineMeta(employee.executionEngine).shortLabel }} · {{ employee.modelName.replace(/\s*\(.*?\)\s*/g, '') }}
              </span>
              <div v-if="employee.channels.length" class="ch-channels-strip">
                <D2ChannelLogo
                  v-for="ch in employee.channels"
                  :key="ch.platform"
                  :platform="ch.platform"
                  :size="15"
                  :title="`${ch.platformName} (${ch.statusText})`"
                />
              </div>
            </div>
            <div class="ch-sub-line" :title="employee.role">
              {{ employee.role }}
            </div>
          </div>
        </div>
        <div class="ch-actions">
          <NButton size="small" secondary @click="showInSessionTasks = !showInSessionTasks">
            {{ showInSessionTasks ? '收起任务栏' : `会话任务 (${session.sessionTasks.length})` }}
          </NButton>
        </div>
      </div>

      <!-- 消息滚动区 -->
      <div ref="messagesScrollRef" class="chat-messages-scroll">
        <div v-if="isLoadingSession && !hasLoadedSession" class="chat-messages-inner chat-skeleton-list">
          <div class="skeleton-msg left">
            <NSkeleton text style="width: 72%; height: 68px; border-radius: 8px;" />
          </div>
          <div class="skeleton-msg right">
            <NSkeleton text style="width: 48%; height: 48px; border-radius: 8px;" />
          </div>
          <div class="skeleton-msg left">
            <NSkeleton text style="width: 64%; height: 80px; border-radius: 8px;" />
          </div>
        </div>

        <div v-else-if="sessionLoadError && !hasLoadedSession" class="session-error-card">
          <div class="session-error-title">无法加载与「{{ employee.name }}」的会话</div>
          <div class="session-error-desc">{{ sessionLoadError }}</div>
          <NButton type="primary" size="small" :loading="isLoadingSession" @click="loadConversation(employee.id)">
            重新加载会话
          </NButton>
        </div>

        <div v-else class="chat-messages-inner">
          <div v-if="session.hasMoreMessages" class="load-older-bar">
            <NButton size="small" secondary :loading="loadingOlder" @click="loadOlderMessages">
              加载更早历史消息
            </NButton>
          </div>

          <div v-if="session.messages.length === 0 && !isGenerating" class="chat-empty-hint">
            暂无历史协同记录，请在下方输入框直接向「{{ employee.name }}」下达工作指令。
          </div>

          <div
            v-for="msg in session.messages"
            :key="msg.id"
            class="chat-msg-row"
            :class="msg.sender"
          >
            <div class="msg-bubble">
              <div v-if="msg.thinking" class="msg-thinking-box">
                <button
                  type="button"
                  class="thinking-toggle-btn"
                  @click="toggleThinking(msg.id)"
                >
                  <span class="th-label">思考过程</span>
                  <span class="th-chevron">{{ expandedThinkingIds[msg.id] ? '收起 ▲' : '展开详情 ▼' }}</span>
                </button>
                <p v-if="expandedThinkingIds[msg.id]" class="thinking-content">{{ msg.thinking }}</p>
              </div>
              <div class="msg-content">{{ msg.content }}</div>
              <div v-if="msg.generatedTaskId" class="msg-task-pill">
                <span>已关联任务：<code>{{ msg.generatedTaskId }}</code></span>
              </div>
              <span class="msg-time">{{ msg.time }}</span>
            </div>
          </div>

          <div v-if="isGenerating" class="chat-msg-row assistant generating">
            <div class="msg-bubble">
              <div class="msg-thinking-box active">
                <span class="th-label">
                  {{ activeExecutionStatus === 'accepted' ? '正在启动' : '正在处理中' }}
                </span>
                <p class="thinking-content">{{ activeThinkingText }}</p>
              </div>
              <div class="generating-dots">
                <span></span><span></span><span></span>
              </div>
              <NButton
                size="small"
                type="warning"
                secondary
                :loading="isInterrupting"
                :disabled="isInterrupting"
                style="margin-top: 6px; align-self: flex-start;"
                @click="interruptGeneration"
              >
                中断本次执行
              </NButton>
            </div>
          </div>

          <div v-if="lastFailedUserMessage" class="chat-msg-row user error-row">
            <div class="msg-bubble error-bubble">
              <div class="msg-content">{{ lastFailedUserMessage }}</div>
              <div class="error-retry-pill">
                <span>发送失败: 网络超时或权限受阻</span>
                <NButton size="tiny" type="error" @click="retryLastMessage">重试发送</NButton>
              </div>
            </div>
          </div>
        </div>
      </div>

      <!-- 输入栏 -->
      <div class="chat-input-bar">
        <div v-if="employee.status === 'disabled'" class="chat-disabled-banner">
          该 AI 员工当前已被停用，仅可查看历史消息，无法发送指令与执行任务。
        </div>
        <div v-else class="input-controls-flex">
          <NInput
            ref="chatInputRef"
            v-model:value="chatInputText"
            type="textarea"
            :rows="2"
            :placeholder="`向「${employee.name}」下发工作需求（Enter 发送，Ctrl + Enter 换行）...`"
            :disabled="isGenerating"
            @keydown="handleChatInputKeydown"
          />
          <div class="input-actions-col">
            <NButton
              type="primary"
              size="small"
              :loading="isGenerating"
              :disabled="!chatInputText.trim()"
              @click="sendChatMessage()"
            >
              发送指令
            </NButton>
          </div>
        </div>
      </div>
    </div>

    <!-- 右侧可展开本会话生成任务面板 -->
    <div v-if="showInSessionTasks" class="session-tasks-panel">
      <div class="stp-header">
        <strong>本会话任务列表 ({{ session.sessionTasks.length }})</strong>
        <button type="button" class="close-btn" title="收起任务面板" @click="showInSessionTasks = false">✕</button>
      </div>
      <div class="stp-list">
        <div
          v-for="task in session.sessionTasks"
          :key="task.id"
          class="stp-task-card"
        >
          <div class="stp-title-row">
            <code class="stp-task-code">{{ task.taskId }}</code>
            <span class="status-pill" :class="tagToneClass(getTaskStatusMeta(task.status).tagType)">
              <span class="status-dot" />
              {{ getTaskStatusMeta(task.status).label }}
            </span>
          </div>
          <p class="stp-tool">工具：{{ task.toolUsed }}</p>
          <p class="stp-artifact">{{ task.outputArtifact || '暂无执行产出摘要' }}</p>
          <div class="stp-logs">
            <span v-for="(log, idx) in task.logs" :key="idx">{{ log }}</span>
            <span v-if="!task.logs?.length" class="stp-no-logs">暂无执行日志</span>
          </div>
        </div>
        <div v-if="!session.sessionTasks.length" class="empty-note">
          当前对话暂未生成任务
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped>
.single-chat-shell {
  display: flex;
  gap: 16px;
  height: 100%;
  min-height: 520px;
  min-width: 0;
}
.chat-viewport-card {
  flex: 1;
  min-width: 0;
  border: 1px solid var(--id-border-subtle);
  border-radius: var(--id-radius-lg);
  display: flex;
  flex-direction: column;
  overflow: hidden;
  background: var(--id-bg-surface);
}
.chat-header-bar {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 12px;
  padding: 12px 16px;
  background: var(--id-bg-surface);
  border-bottom: 1px solid var(--id-border-subtle);
}
.ch-info-left {
  display: flex;
  align-items: center;
  gap: 12px;
  min-width: 0;
}
.ch-info-meta {
  display: flex;
  flex-direction: column;
  gap: 3px;
  min-width: 0;
}
.ch-title-line {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
}
.ch-emp-name {
  font-size: 14px;
  font-weight: 600;
  color: var(--id-text-primary);
}
.ai-tag {
  font-size: 12px;
  font-family: var(--id-font-mono);
  font-weight: 500;
  color: var(--id-text-secondary);
  background: var(--id-bg-muted);
  border: 1px solid var(--id-border-subtle);
  padding: 0 5px;
  border-radius: var(--id-radius-sm);
  line-height: 1.4;
}
.ch-model-txt {
  font-size: 12px;
  font-family: var(--id-font-mono);
  color: var(--id-text-secondary);
  background: var(--id-bg-subtle);
  border: 1px solid var(--id-border-hairline);
  padding: 1px 6px;
  border-radius: var(--id-radius-sm);
  max-width: 150px;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.mobile-back-list-btn {
  display: none;
  appearance: none;
  background: var(--id-bg-subtle);
  border: 1px solid var(--id-border-subtle);
  color: var(--id-text-primary);
  border-radius: var(--id-radius-md);
  padding: 4px 8px;
  font-size: 12px;
  font-weight: 500;
  cursor: pointer;
  flex-shrink: 0;
}
.mobile-back-list-btn:hover {
  background: var(--id-bg-muted);
}
@media (max-width: 960px) {
  .mobile-back-list-btn {
    display: inline-flex;
    align-items: center;
  }
}
.ch-channels-strip {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  padding-left: 8px;
  border-left: 1px solid var(--id-border-subtle);
}
.ch-sub-line {
  font-size: 12px;
  color: var(--id-text-secondary);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.ch-actions {
  display: flex;
  gap: 8px;
  flex-shrink: 0;
}

/* Shared Micro Status Pill */
.status-pill {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  font-size: 12px;
  font-weight: 500;
  line-height: 1.3;
  padding: 2px 8px;
  border-radius: var(--id-radius-pill);
  border: 1px solid var(--id-border-subtle);
  background: var(--id-bg-muted);
  color: var(--id-text-secondary);
  white-space: nowrap;
}
.status-dot {
  width: 6px;
  height: 6px;
  border-radius: 50%;
  background: var(--id-text-muted);
  flex-shrink: 0;
}
.status-pill.info {
  background: var(--id-status-info-soft);
  border-color: var(--id-status-info-border);
  color: var(--id-status-info);
}
.status-pill.info .status-dot { background: var(--id-status-info-dot); }
.status-pill.warning {
  background: var(--id-status-warning-soft);
  border-color: var(--id-status-warning-border);
  color: var(--id-status-warning);
}
.status-pill.warning .status-dot { background: var(--id-status-warning-dot); }
.status-pill.success {
  background: var(--id-status-success-soft);
  border-color: var(--id-status-success-border);
  color: var(--id-status-success);
}
.status-pill.success .status-dot { background: var(--id-status-success-dot); }
.status-pill.danger {
  background: var(--id-status-danger-soft);
  border-color: var(--id-status-danger-border);
  color: var(--id-status-danger);
}
.status-pill.danger .status-dot { background: var(--id-status-danger-dot); }
.status-pill.neutral {
  background: var(--id-bg-muted);
  border-color: var(--id-border-subtle);
  color: var(--id-text-secondary);
}

.chat-messages-scroll {
  flex: 1;
  padding: 20px;
  overflow-y: auto;
  background: var(--id-bg-subtle);
}
.chat-messages-inner {
  max-width: 780px;
  width: 100%;
  margin: 0 auto;
  display: flex;
  flex-direction: column;
  gap: 14px;
}
.load-older-bar {
  text-align: center;
  margin-bottom: 4px;
}
.chat-empty-hint {
  text-align: center;
  padding: 48px 16px;
  font-size: 13px;
  color: var(--id-text-muted);
}
.chat-skeleton-list {
  gap: 16px;
}
.skeleton-msg {
  display: flex;
}
.skeleton-msg.left { justify-content: flex-start; }
.skeleton-msg.right { justify-content: flex-end; }
.session-error-card {
  max-width: 480px;
  margin: 48px auto;
  background: var(--id-bg-surface);
  border: 1px solid var(--id-status-danger-border);
  border-radius: var(--id-radius-lg);
  padding: 24px;
  text-align: center;
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 8px;
}
.session-error-title {
  font-size: 14px;
  font-weight: 600;
  color: var(--id-text-primary);
}
.session-error-desc {
  font-size: 13px;
  color: var(--id-text-secondary);
}

.chat-msg-row {
  display: flex;
  width: 100%;
}
.chat-msg-row.user {
  justify-content: flex-end;
}
.chat-msg-row.assistant {
  justify-content: flex-start;
}
.msg-bubble {
  max-width: 82%;
  padding: 12px 14px;
  border-radius: var(--id-radius-lg);
  font-size: 13px;
  line-height: 1.6;
  display: flex;
  flex-direction: column;
  gap: 8px;
}
.chat-msg-row.user .msg-bubble {
  background: var(--id-bg-terminal-header);
  color: var(--id-bg-canvas);
  border-bottom-right-radius: 2px;
}
.chat-msg-row.assistant .msg-bubble {
  background: var(--id-bg-surface);
  color: var(--id-text-primary);
  border: 1px solid var(--id-border-subtle);
  border-bottom-left-radius: 2px;
}
.msg-content {
  white-space: pre-wrap;
  word-break: break-word;
}
.msg-thinking-box {
  background: var(--id-bg-subtle);
  border: 1px solid var(--id-border-subtle);
  padding: 6px 10px;
  border-radius: var(--id-radius-md);
  font-size: 12px;
  color: var(--id-text-secondary);
}
.msg-thinking-box.active {
  border-left: 3px solid var(--id-accent-primary);
}
.thinking-toggle-btn {
  width: 100%;
  background: transparent;
  border: none;
  padding: 0;
  font: inherit;
  color: var(--id-text-secondary);
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 12px;
  cursor: pointer;
  text-align: left;
}
.thinking-toggle-btn:hover {
  color: var(--id-text-primary);
}
.th-label {
  font-weight: 500;
  color: var(--id-text-secondary);
}
.th-chevron {
  font-size: 12px;
  color: var(--id-text-muted);
}
.thinking-content {
  margin: 6px 0 0 0;
  padding-top: 6px;
  border-top: 1px solid var(--id-border-hairline);
  white-space: pre-line;
  line-height: 1.5;
  color: var(--id-text-secondary);
}
.msg-task-pill {
  font-size: 12px;
  background: var(--id-bg-subtle);
  border: 1px solid var(--id-border-subtle);
  color: var(--id-text-primary);
  padding: 6px 10px;
  border-radius: var(--id-radius-sm);
}
.msg-task-pill code {
  font-family: var(--id-font-mono);
  color: var(--id-accent-primary);
}
.msg-time {
  font-family: var(--id-font-mono);
  font-variant-numeric: tabular-nums;
  font-size: 12px;
  color: var(--id-text-muted);
  align-self: flex-end;
}
.chat-msg-row.user .msg-time {
  color: var(--id-text-placeholder);
}
.error-bubble {
  background: var(--id-status-danger-soft) !important;
  border: 1px solid var(--id-status-danger-border) !important;
  color: var(--id-status-danger) !important;
}
.error-retry-pill {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  font-size: 12px;
  margin-top: 4px;
  padding-top: 6px;
  border-top: 1px dashed var(--id-status-danger-border);
}
.generating-dots {
  display: inline-flex;
  gap: 4px;
  align-items: center;
  padding: 4px 0;
}
.generating-dots span {
  width: 6px;
  height: 6px;
  border-radius: 50%;
  background: var(--id-accent-primary);
  animation: pulse-dot 1.2s infinite ease-in-out;
}
.generating-dots span:nth-child(2) { animation-delay: 0.2s; }
.generating-dots span:nth-child(3) { animation-delay: 0.4s; }
@keyframes pulse-dot {
  0%, 80%, 100% { opacity: 0.3; transform: scale(0.85); }
  40% { opacity: 1; transform: scale(1.1); }
}

.chat-input-bar {
  padding: 12px 16px;
  border-top: 1px solid var(--id-border-subtle);
  background: var(--id-bg-surface);
}
.chat-disabled-banner {
  padding: 10px 12px;
  background: var(--id-status-warning-soft);
  border: 1px solid var(--id-status-warning-border);
  color: var(--id-status-warning);
  font-size: 12px;
  text-align: center;
  border-radius: var(--id-radius-md);
}
.input-controls-flex {
  display: flex;
  gap: 10px;
}
.input-actions-col {
  display: flex;
  flex-direction: column;
  justify-content: flex-end;
}

.session-tasks-panel {
  width: 300px;
  border: 1px solid var(--id-border-subtle);
  border-radius: var(--id-radius-lg);
  background: var(--id-bg-surface);
  display: flex;
  flex-direction: column;
  flex-shrink: 0;
  overflow: hidden;
}
.stp-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  padding: 12px 14px;
  border-bottom: 1px solid var(--id-border-subtle);
  background: var(--id-bg-subtle);
  font-size: 13px;
  color: var(--id-text-primary);
}
.stp-header strong {
  font-weight: 600;
  font-variant-numeric: tabular-nums;
}
.close-btn {
  background: none;
  border: none;
  cursor: pointer;
  color: var(--id-text-muted);
  font-size: 13px;
  padding: 2px 6px;
  border-radius: var(--id-radius-sm);
  transition: background-color var(--id-transition-fast), color var(--id-transition-fast);
}
.close-btn:hover {
  background: var(--id-bg-hover);
  color: var(--id-text-primary);
}
.close-btn:focus-visible {
  outline: none;
  box-shadow: var(--id-focus-ring);
}
.stp-list {
  flex: 1;
  padding: 10px;
  overflow-y: auto;
  display: flex;
  flex-direction: column;
  gap: 10px;
}
.stp-task-card {
  background: var(--id-bg-subtle);
  border: 1px solid var(--id-border-subtle);
  border-radius: var(--id-radius-md);
  padding: 10px 12px;
  font-size: 12px;
  display: flex;
  flex-direction: column;
  gap: 6px;
}
.stp-title-row {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 6px;
}
.stp-task-code {
  font-family: var(--id-font-mono);
  font-variant-numeric: tabular-nums;
  font-size: 12px;
  color: var(--id-text-primary);
  font-weight: 600;
}
.stp-tool {
  margin: 0;
  color: var(--id-text-secondary);
}
.stp-artifact {
  margin: 0;
  color: var(--id-text-primary);
  font-weight: 500;
}
.stp-logs {
  background: var(--id-bg-terminal);
  border: 1px solid var(--id-border-strong);
  color: var(--id-text-terminal);
  padding: 8px 10px;
  border-radius: var(--id-radius-sm);
  font-family: var(--id-font-mono);
  font-size: 12px;
  max-height: 88px;
  overflow-y: auto;
  display: flex;
  flex-direction: column;
  gap: 3px;
  line-height: 1.45;
}
.stp-no-logs {
  color: var(--id-text-placeholder);
}
.empty-note {
  font-size: 12px;
  color: var(--id-text-muted);
  padding: 20px 12px;
  text-align: center;
}

@media (max-width: 900px) {
  .single-chat-shell {
    flex-direction: column;
    min-height: 460px;
  }
  .chat-viewport-card {
    min-height: 420px;
  }
  .session-tasks-panel {
    width: 100%;
    max-height: 280px;
  }
}

@media (max-width: 760px) {
  .ch-channels-strip {
    border-left: none;
    padding-left: 0;
  }
}

@media (max-width: 640px) {
  .chat-header-bar {
    flex-direction: column;
    align-items: flex-start;
  }
  .ch-actions {
    width: 100%;
    flex-wrap: wrap;
  }
  .chat-messages-scroll {
    padding: 12px;
  }
  .msg-bubble {
    max-width: 92%;
  }
  .input-controls-flex {
    flex-direction: column;
  }
  .input-actions-col {
    flex-direction: row;
    justify-content: flex-end;
  }
}
</style>

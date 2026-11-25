<script setup lang="ts">
import { nextTick, onMounted, onUnmounted, ref, watch } from 'vue';
import { NAlert, NButton, NInput, NSelect, NSkeleton } from 'naive-ui';
import SessionMarkdown from '../components/SessionMarkdown.vue';
import { D2ApiError } from '../api/client';
import {
  humanRequestsApi,
  type HumanDecision,
  type HumanAnswerItem,
  type HumanRequest,
  type HumanHistoryItem,
  type HistoryFilter,
} from '../api/human-requests';

const props = defineProps<{
  workspace: string;
  readOnly?: boolean;
  selectedId?: string | undefined;
  initialFilter?: HistoryFilter;
  workspacePending?: number | undefined;
}>();
const emit = defineEmits<{ count: [value: number]; filter: [value: HistoryFilter] }>();
const items = ref<DisplayItem[]>([]);
const filter = ref<HistoryFilter>('pending');
const focusId = ref<string>();
const historyCounts = ref({ pending: 0, resolved: 0, all: 0 });
const filters = [
  { key: 'pending', label: '待处理' },
  { key: 'resolved', label: '已处理' },
  { key: 'all', label: '全部' },
] as const;
const states: Record<string, string> = {
  preparing: '准备中',
  pending: '待处理',
  blocked: '待核对',
  approved: '已通过',
  rejected: '已拒绝',
  revised: '已退回修改',
  cancelled: '已取消',
};
const providers: Record<string, string> = {
  web: '工作台',
  feishu: '飞书',
  dingtalk: '钉钉',
  wecom: '企业微信',
};
const decisionLabels: Record<HumanDecision, string> = {
  approve: '同意',
  reject: '拒绝',
  revise: '退回修改',
  information: '已补充信息',
  needs_more: '要求补充',
};
type DisplayItem = Omit<HumanRequest, 'question'> & Partial<HumanHistoryItem>;
const drafts = ref<Record<string, string>>({});
const structuredDrafts = ref<Record<string, Record<string, string>>>({});
const multiDrafts = ref<Record<string, Record<string, string[]>>>({});
const loading = ref(false);
const answering = ref('');
const error = ref('');
const denied = ref(false);
const notice = ref('');
const focusError = ref('');
const offset = ref(0),
  total = ref(0),
  focused = ref<DisplayItem>();
let read = 0;
let controller = new AbortController(),
  reading: AbortController | undefined,
  pollTimer: ReturnType<typeof setInterval> | undefined,
  scrollPending = false,
  displayedOffset = 0;

function detailVisible(item: DisplayItem) {
  return !props.readOnly || item.detail_visible === true;
}

function numbered(item: DisplayItem) {
  if (item.question?.questions?.length) return false;
  const lines = item.question?.question.trim().split('\n') ?? [];
  return (
    lines.length >= 2 &&
    lines.length <= 10 &&
    lines.every((line, index) =>
      new RegExp(`^${index + 1}[.、)）]\\s*\\S`).test(line.trim()),
    )
  );
}

function authError(cause: unknown) {
  return cause instanceof D2ApiError && [401, 403].includes(cause.status);
}

function transientError(cause: unknown) {
  return (
    !(cause instanceof D2ApiError) ||
    cause.status >= 500 ||
    [408, 425, 429].includes(cause.status)
  );
}

async function load(silent = false) {
  reading?.abort();
  const request = new AbortController();
  reading = request;
  const { signal } = request;
  const generation = ++read;
  const workspace = props.workspace;
  const readOnly = props.readOnly;
  const currentFilter = filter.value;
  const id = focusId.value;
  const current = () => generation === read && !signal.aborted;
  loading.value = !silent;
  if (!silent) error.value = '';
  try {
    const collection = (start: number) =>
      readOnly
        ? humanRequestsApi.history(workspace, signal, currentFilter, start)
        : humanRequestsApi.inbox(workspace, signal, start);
    let pageOffset = offset.value;
    let response = await collection(pageOffset);
    if (!current()) return;
    if (pageOffset > 0 && !response.items.length) {
      pageOffset = 0;
      response = await collection(pageOffset);
      if (!current()) return;
    }
    // Hold the new page until a focused lookup has either succeeded or failed
    // locally; an authorization error on either request invalidates both.
    let targetItem: DisplayItem | undefined;
    let targetError = '';
    if (id && !response.items.some((item) => item.id === id)) {
      try {
        const target = readOnly
          ? await humanRequestsApi.history(workspace, signal, 'all', 0, id)
          : await humanRequestsApi.inbox(workspace, signal, 0, id);
        targetItem = target.items.find((item) => item.id === id);
      } catch (cause) {
        if (!current()) return;
        if (authError(cause)) throw cause;
        if (!(cause instanceof D2ApiError && [400, 404].includes(cause.status)))
          targetError = '无法加载链接指向的确认请求。';
      }
    }
    if (!current()) return;
    items.value = response.items;
    offset.value = displayedOffset = pageOffset;
    denied.value = false;
    total.value = response.total ?? response.items.length;
    focused.value = targetItem;
    focusError.value = targetError;
    error.value = '';
    if ('resolved' in response && 'pending' in response) {
      const { pending, resolved } = response;
      historyCounts.value = { pending, resolved, all: pending + resolved };
      emit('count', pending);
    } else if (!readOnly) emit('count', total.value);
    if (id && scrollPending) {
      await nextTick();
      if (current()) {
        const card = document.getElementById('approval-card-' + id);
        if (card) {
          scrollPending = false;
          card.scrollIntoView?.({ block: 'nearest' });
        }
      }
    }
  } catch (cause) {
    if (!current()) return;
    offset.value = displayedOffset;
    if (authError(cause)) {
      denied.value = true;
      drafts.value = {};
      notice.value = '';
      emit('count', 0);
    }
    if (!transientError(cause)) {
      items.value = [];
      focused.value = undefined;
      focusError.value = '';
      total.value = 0;
      historyCounts.value = { pending: 0, resolved: 0, all: 0 };
    }
    error.value = cause instanceof Error ? cause.message : '待确认加载失败';
  } finally {
    if (current()) {
      loading.value = false;
      reading = undefined;
    }
  }
}
function selectFilter(value: HistoryFilter) {
  if (filter.value === value && !focusId.value) return;
  if (props.initialFilter !== undefined) {
    emit('filter', value);
    return;
  }
  focusId.value = undefined;
  filter.value = value;
  offset.value = displayedOffset = 0;
  items.value = [];
  focused.value = undefined;
  focusError.value = '';
  total.value = 0;
  void load();
}
function turnPage(delta: number) {
  offset.value += delta;
  void load();
}
defineExpose({ refresh: load });
function showAnswer(item: DisplayItem, answer: HumanAnswerItem): string {
  const options = item.question?.questions?.find((q) => q.id === answer.questionId)?.options;
  const label = (id: string) => options?.find((opt) => opt.id === id)?.label ?? id;
  return answer.optionIds?.map(label).join('、') ?? (answer.value ? label(answer.value) : '');
}
async function answer(item: DisplayItem, decision: HumanDecision) {
  if (props.readOnly || !item.question || answering.value || loading.value) return;
  const { signal } = controller;
  answering.value = item.id;
  notice.value = '';
  error.value = '';
  try {
    const body = drafts.value[item.id]?.trim() || '';
    const answers = decision === 'approve' || decision === 'information'
      ? item.question.questions?.flatMap<HumanAnswerItem>((question) => {
          if (question.answerType === "multi_choice") {
            const optionIds = multiDrafts.value[item.id]?.[question.id];
            return optionIds?.length ? [{ questionId: question.id, optionIds }] : [];
          }
          const value = structuredDrafts.value[item.id]?.[question.id]?.trim();
          return value ? [{ questionId: question.id, value }] : [];
        })
      : undefined;
    const result = await humanRequestsApi.answer(
      props.workspace,
      item,
      decision,
      body,
      signal,
      answers,
    );
    if (signal.aborted) return;
    if (result.missingFields?.length) {
      error.value = '请补齐标为必答的小题。';
      return;
    }
    if (result.state !== 'pending' && result.state !== 'needs_more') {
      items.value = items.value.filter((request) => request.id !== item.id);
      if (focused.value?.id === item.id) focused.value = undefined;
      total.value = Math.max(0, total.value - 1);
      emit('count', total.value);
    }
    delete drafts.value[item.id];
    delete structuredDrafts.value[item.id];
    delete multiDrafts.value[item.id];
    if (result.state === 'pending') {
      notice.value = result.remaining?.length
        ? `已记录，仍需回答小题 ${result.remaining.join('、')}。`
        : '小题已全部记录，请单独点击「同意」。';
    } else {
      notice.value = {
        approved: '答复已记录，原任务已排队继续。',
        rejected: '已拒绝，任务保持停止。',
        revised: '已退回修改，任务会拟定新方案并重新发起确认。',
        needs_more: '补问已记录，任务仍在等待确认。',
      }[result.state];
    }
  } catch (cause) {
    if (signal.aborted) return;
    if (cause instanceof D2ApiError && [403, 404, 409].includes(cause.status))
      await load();
    if (!signal.aborted)
      error.value = cause instanceof Error ? cause.message : '答复提交失败';
  } finally {
    if (!signal.aborted) answering.value = '';
  }
}
watch(
  () => [props.workspace, props.selectedId, props.initialFilter, props.readOnly],
  () => {
    controller.abort();
    controller = new AbortController();
    offset.value = displayedOffset = 0;
    denied.value = false;
    focusId.value = props.selectedId;
    scrollPending = !!props.selectedId;
    focused.value = undefined;
    focusError.value = '';
    filter.value = props.selectedId ? 'all' : props.initialFilter || 'pending';
    historyCounts.value = { pending: 0, resolved: 0, all: 0 };
    total.value = 0;
    drafts.value = {};
    items.value = [];
    answering.value = '';
    notice.value = '';
    void load();
  },
  { immediate: true },
);
function refreshVisibleHistory() {
  if (props.readOnly && !document.hidden && !reading) void load(true);
}
watch(() => props.workspacePending, count => {
  if (count !== undefined && count !== historyCounts.value.pending) refreshVisibleHistory();
});
onMounted(() => {
  pollTimer = setInterval(refreshVisibleHistory, 10_000);
  document.addEventListener('visibilitychange', refreshVisibleHistory);
});
onUnmounted(() => {
  controller.abort();
  reading?.abort();
  if (pollTimer) clearInterval(pollTimer);
  document.removeEventListener('visibilitychange', refreshVisibleHistory);
});
</script>

<template>
  <section
    class="approvals-container"
    :aria-label="readOnly ? '人工确认' : '待人工确认'"
  >
    <header v-if="!readOnly" class="inbox-header">
      <h2>待人工确认</h2>
      <NButton :loading="loading" :disabled="!!answering" @click="load()"
        >刷新</NButton
      >
    </header>
    <div v-if="readOnly" class="approvals-intro-card">
      <div class="ai-text">
        <strong>人工确认</strong><span>查看当前工作区的确认状态。有查看权限时会显示问题与处理记录。</span>
      </div>
    </div>
    <div v-if="readOnly && !denied" class="approval-filter-bar">
      <button
        v-for="opt in filters"
        :key="opt.key"
        type="button"
        class="filter-chip"
        :class="{ active: filter === opt.key }"
        :aria-pressed="filter === opt.key"
        @click="selectFilter(opt.key)"
      >
        {{ opt.label }} ({{ historyCounts[opt.key] }})
      </button>
    </div>
    <p v-if="focusError" role="status">{{ focusError }}</p>
    <p
      v-else-if="
        focusId &&
        !loading &&
        !error &&
        !focused &&
        !items.some((item) => item.id === focusId)
      "
    >
      此确认请求当前不可查看。
    </p>
    <NAlert v-if="notice" type="success" role="status">{{ notice }}</NAlert>
    <NAlert v-if="error" type="error" role="alert">{{ error }}</NAlert>
    <NSkeleton v-if="loading && !items.length" text :repeat="4" />
    <p v-else-if="!items.length && !focused && !error" class="empty">
      {{
        readOnly
          ? '当前筛选下没有可见的确认记录。'
          : '目前没有分配给你的确认请求。'
      }}
    </p>
    <article
      v-for="item in focused ? [focused, ...items] : items"
      :key="item.id"
      class="approval-card"
      :id="'approval-card-' + item.id"
      :class="{ highlighted: item.id === focusId }"
    >
      <div v-if="detailVisible(item) && item.question" class="appr-header">
        <div class="appr-title-wrap">
          <span
            class="status-pill"
            :class="
              item.question.answer_type === 'decision' ? 'warning' : 'info'
            "
            ><span class="status-dot" />{{
              item.question.answer_type === 'decision' ? '决策确认' : '信息待补充'
            }}</span
          >
          <div v-if="readOnly" class="appr-title" role="heading" aria-level="3">
            <SessionMarkdown :text="item.question.question" />
          </div>
          <h3 v-else class="appr-title">{{ item.question.question }}</h3>
        </div>
      </div>
      <h3 v-else class="appr-title">人工确认</h3>
      <div v-if="readOnly" class="history-meta">
        <strong>{{ states[item.state] || item.state }}</strong>
        <span v-if="detailVisible(item)"
          >处理人　{{ item.recipient_name || '名称不可用'
          }}<template v-if="item.recipient_provider"
            >（{{
              providers[item.recipient_provider] || item.recipient_provider
            }}）</template
          ></span
        >
        <span v-if="detailVisible(item) && item.agent_name">发起员工　{{ item.agent_name }}</span>
        <time v-if="item.created_at"
          >发起于 {{ new Date(item.created_at).toLocaleString('zh-CN') }}</time
        >
        <time v-if="!detailVisible(item) && item.answered_at"
          >处理于 {{ new Date(item.answered_at).toLocaleString('zh-CN') }}</time
        >
      </div>
      <p v-if="readOnly && !detailVisible(item)" class="reason-text">仅可查看处理状态。</p>
      <div v-if="detailVisible(item) && item.question?.background" class="appr-reason-box">
        <strong class="reason-label">背景与说明</strong>
        <SessionMarkdown v-if="readOnly" :text="item.question.background" />
        <p v-else class="reason-text">{{ item.question.background }}</p>
      </div>
      <div v-if="detailVisible(item) && (item.question?.proposedAction || item.question?.impactScope)" class="appr-reason-box">
        <p v-if="item.question?.proposedAction">拟执行 {{ item.question.proposedAction }}</p>
        <p v-if="item.question?.impactScope">影响范围 {{ item.question.impactScope }}</p>
      </div>
      <div v-if="!readOnly && item.question?.questions?.length" class="appr-reason-box">
        <label v-for="question in item.question.questions" :key="question.id">
          <span>{{ question.text }}<template v-if="question.required">（必答）</template></span>
          <NSelect
            v-if="question.options?.length && question.answerType === 'multi_choice'"
            multiple
            :value="multiDrafts[item.id]?.[question.id] ?? []"
            :options="question.options.map(({ id, label }) => ({ value: id, label }))"
            :aria-label="question.text"
            :disabled="!!answering"
            @update:value="(value: string[]) => { (multiDrafts[item.id] ??= {})[question.id] = value; }"
          />
          <NSelect
            v-else-if="question.options?.length && question.answerType === 'choice'"
            :value="structuredDrafts[item.id]?.[question.id] || null"
            :options="question.options.map(({ id, label }) => ({ value: id, label }))"
            :aria-label="question.text"
            :disabled="!!answering"
            @update:value="(value: string) => { (structuredDrafts[item.id] ??= {})[question.id] = value; }"
          />
          <NInput
            v-else
            :value="structuredDrafts[item.id]?.[question.id] || ''"
            :placeholder="question.constraints?.join('、') ?? ''"
            :aria-label="question.text"
            :disabled="!!answering"
            maxlength="2000"
            @update:value="(value) => {
              (structuredDrafts[item.id] ??= {})[question.id] = value;
            }"
          />
        </label>
      </div>
      <section v-if="readOnly && detailVisible(item) && item.clarification" class="appr-reason-box">
        <strong>最近补问</strong><SessionMarkdown :text="item.clarification" />
      </section>
      <section
        v-if="
          readOnly && detailVisible(item) && (item.answer || item.answered_at)
        "
        class="appr-reason-box"
      >
        <strong>处理结果</strong>
        <p v-if="item.answer">
          {{ decisionLabels[item.answer.decision] || item.answer.decision }}
        </p>
        <SessionMarkdown v-if="item.answer?.text" :text="item.answer.text" />
        <p v-for="answer in item.answer?.answers" :key="answer.questionId">
          {{ item.question?.questions?.find((question) => question.id === answer.questionId)?.text || answer.questionId }}
          {{ showAnswer(item, answer) }}
        </p>
        <time v-if="item.answered_at">{{
          new Date(item.answered_at).toLocaleString('zh-CN')
        }}</time>
      </section>
      <small>确认编号 {{ item.id }}</small>
      <NInput
        v-if="!readOnly && item.question"
        :value="drafts[item.id] || ''"
        @update:value="
          (value) => {
            drafts[item.id] = value;
          }
        "
        type="textarea"
        maxlength="4000"
        :placeholder="
          numbered(item)
            ? '按小题填写，例如：第一个 500 元；第二个 周五'
            : item.question.answer_type === 'decision'
              ? '说明（选填）；退回修改或补问时请填写内容'
              : '填写答复、退回理由或补问内容'
        "
        aria-label="答复内容"
        :disabled="!!answering"
      />
      <div v-if="!readOnly && item.question" class="answers">
        <NButton
          :disabled="!!answering || loading"
          @click="answer(item, 'reject')"
          >拒绝</NButton
        >
        <NButton
          :disabled="!!answering || loading || !drafts[item.id]?.trim()"
          @click="answer(item, 'needs_more')"
          >补问</NButton
        >
        <NButton
          :disabled="!!answering || loading || !drafts[item.id]?.trim()"
          @click="answer(item, 'revise')"
          >退回修改</NButton
        >
        <NButton
          v-if="item.question.answer_type === 'decision'"
          type="primary"
          :disabled="!!answering || loading"
          @click="answer(item, 'approve')"
          >同意</NButton
        >
        <NButton
          v-if="item.question.answer_type === 'information' || numbered(item)"
          :type="
            item.question.answer_type === 'information' ? 'primary' : 'default'
          "
          :disabled="!!answering || loading || (!item.question.questions?.length && !drafts[item.id]?.trim())"
          @click="answer(item, 'information')"
          >提交答复</NButton
        >
      </div>
    </article>
    <nav v-if="total > 100" class="task-page-nav">
      <NButton
        size="small"
        :disabled="loading || offset === 0"
        @click="turnPage(-100)"
        >上一页</NButton
      ><NButton
        size="small"
        :disabled="loading || offset + 100 >= total"
        @click="turnPage(100)"
        >下一页</NButton
      >
    </nav>
  </section>
</template>

<style scoped src="../styles/task-center.css"></style>
<style scoped>
.history-meta {
  display: flex;
  flex-wrap: wrap;
  gap: 6px 16px;
  color: var(--id-text-secondary);
  font-size: 12px;
}
.appr-title :deep(p) {
  margin: 0;
}

.approvals-container {
  display: flex;
  flex-direction: column;
  gap: 12px;
}
.approvals-intro-card {
  background: var(--id-bg-surface);
  border: 1px solid var(--id-border-subtle);
  border-left: 3px solid var(--id-status-warning-dot);
  padding: 12px 16px;
  border-radius: var(--id-radius-md);
  color: var(--id-text-secondary);
  font-size: 13px;
  line-height: 1.5;
}
.ai-text strong {
  color: var(--id-text-primary);
  font-weight: 600;
}
.approval-filter-bar {
  display: flex;
  align-items: center;
  gap: 6px;
  flex-wrap: wrap;
}
.approval-card {
  background: var(--id-bg-surface);
  border: 1px solid var(--id-border-subtle);
  border-radius: var(--id-radius-lg);
  padding: 16px;
  display: flex;
  flex-direction: column;
  gap: 12px;
}
.approval-card.blocked {
  border-left: 3px solid var(--id-status-danger-dot);
}
.approval-card.highlighted {
  border-color: var(--id-accent);
  box-shadow: var(--id-focus-ring);
}
.blocked-alert-box {
  background: var(--id-status-danger-soft);
  border: 1px solid var(--id-status-danger-border);
  padding: 10px 14px;
  border-radius: var(--id-radius-md);
  display: flex;
  justify-content: space-between;
  align-items: center;
  flex-wrap: wrap;
  gap: 12px;
}
.ba-text strong {
  color: var(--id-status-danger);
  font-size: 13px;
  font-weight: 600;
}
.ba-text p {
  color: var(--id-text-secondary);
  font-size: 12px;
  margin: 2px 0 0 0;
}
.appr-header {
  display: flex;
  justify-content: space-between;
  align-items: flex-start;
  gap: 12px;
}
.appr-title-wrap {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 8px;
}
.appr-title {
  font-size: 15px;
  font-weight: 600;
  color: var(--id-text-primary);
  margin: 0;
}
.appr-time {
  font-family: var(--id-font-mono);
  font-variant-numeric: tabular-nums;
  font-size: 12px;
  color: var(--id-text-muted);
  white-space: nowrap;
}
.appr-meta-bar {
  display: flex;
  flex-wrap: wrap;
  gap: 8px 18px;
  font-size: 12px;
  color: var(--id-text-secondary);
  background: var(--id-bg-subtle);
  border: 1px solid var(--id-border-hairline);
  padding: 8px 12px;
  border-radius: var(--id-radius-sm);
}
.appr-meta-bar strong {
  color: var(--id-text-primary);
  font-weight: 500;
}
.work-item-link-btn {
  background: none;
  border: none;
  padding: 0;
  color: var(--id-accent-primary);
  font-family: var(--id-font-mono);
  font-size: 12px;
  cursor: pointer;
}
.work-item-link-btn:hover {
  text-decoration: underline;
}
.appr-reason-box {
  font-size: 13px;
  color: var(--id-text-secondary);
  line-height: 1.5;
}
.reason-label,
.decision-label,
.input-label {
  font-size: 12px;
  font-weight: 600;
  color: var(--id-text-secondary);
}
.reason-text {
  margin: 4px 0 0 0;
  color: var(--id-text-primary);
}
.missing-prompt-hint {
  margin: 2px 0 4px 0;
  font-size: 13px;
  color: var(--id-text-primary);
}

.appr-title,
.reason-text {
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}

.inbox {
  max-width: 55rem;
  display: grid;
  gap: 14px;
}
.inbox-header,
.answers {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 10px;
}
h2,
h3,
p {
  margin: 0;
}
h2 {
  font-size: 22px;
}
h3 {
  font-size: 16px;
}
.question {
  display: grid;
  gap: 12px;
  padding: 20px;
  border: 1px solid var(--id-border-subtle);
  border-radius: 8px;
  background: var(--id-bg-surface);
  overflow-wrap: anywhere;
}
.question.selected {
  border-color: var(--id-accent-primary);
}
.question p,
.question h3 {
  white-space: pre-wrap;
}
small,
.empty {
  color: var(--id-text-muted);
}
.empty {
  padding: 22px 0;
}
.answers {
  justify-content: flex-end;
  flex-wrap: wrap;
}
</style>

<script setup lang="ts">
import { onUnmounted, ref, watch } from 'vue';
import WorkItemTasks from './WorkItemTasks.vue';
import { NButton } from 'naive-ui';
import { workApi, workStatusLabel, type WorkDetail } from '../api/work-items';
const props = defineProps<{
  workspace: string;
  itemId: string;
  showTasks?: boolean;
}>();
const emit = defineEmits<{ openTask: [id: string] }>();
const detail = ref<WorkDetail>(),
  loading = ref(false),
  error = ref('');
const labels: Record<string, string> = {
  created: '首次交接',
  handoff: '转交',
  started: '开始办理',
  submitted: '提交验收',
  returned: '退回整改',
  completed: '验收通过',
  blocked: '等待核对',
  failed: '失败结案',
  cancelled: '事项取消',
};
const delivery: Record<string, string> = {
  pending: '待投递',
  leased: '等待发送',
  accepted: '渠道已受理',
  confirmed: '渠道已确认',
  sending: '投递中',
  sent: '已送达',
  delivered: '已送达',
  failed: '投递失败',
  cancelled: '已取消',
  unavailable: '来源不可用',
  unknown: '结果待核对',
};
let request: AbortController | undefined;
async function load(older = false) {
  request?.abort();
  request = new AbortController();
  const { signal } = request,
    previous = older ? detail.value?.events || [] : [];
  loading.value = true;
  error.value = '';
  try {
    const result = await workApi.detail(
      props.workspace,
      props.itemId,
      previous.at(-1)?.id || '',
      signal,
    );
    if (!signal.aborted)
      detail.value = { ...result, events: [...previous, ...result.events] };
  } catch (e) {
    if (!signal.aborted) {
      detail.value = undefined;
      error.value = e instanceof Error ? e.message : '事项读取失败';
    }
  } finally {
    if (!signal.aborted) loading.value = false;
  }
}
watch(
  () => [props.workspace, props.itemId],
  () => {
    detail.value = undefined;
    void load();
  },
  { immediate: true },
);
onUnmounted(() => request?.abort());
</script>
<template>
  <section class="wi-detail-content" aria-label="事项时间线">
    <div v-if="detail" class="detail-top-card">
      <h3 class="detail-main-title">{{ detail.title }}</h3>
      <div class="detail-tag-row">
        <span class="status-pill">{{ workStatusLabel(detail.status) }}</span>
        <span class="detail-group"
          >{{ detail.origin_employee }} →
          {{ detail.current_employee }}负责</span
        >
      </div>
    </div>
    <div>
      <NButton size="small" :loading="loading" @click="load()">刷新</NButton>
    </div>
    <p v-if="error" role="alert">{{ error }}</p>
    <template v-if="detail">
      <div class="detail-section">
        <strong class="d-label">业务目标</strong>
        <p class="d-val">{{ detail.goal }}</p>
      </div>
      <div class="detail-section">
        <strong class="d-label">完成验收标准</strong>
        <p class="d-val">{{ detail.criteria }}</p>
      </div>
      <div v-if="detail.reason" class="detail-section">
        <strong class="d-label">当前进展</strong>
        <p class="d-val highlight">{{ detail.reason }}</p>
      </div>
      <WorkItemTasks
        v-if="showTasks"
        :key="workspace + ':' + itemId"
        :workspace="workspace"
        :item-id="itemId"
        @open="(id) => emit('openTask', id)"
      />
      <div class="detail-section">
        <strong class="d-label">交接时间线</strong>
        <div class="events-list">
          <div
            v-for="event in detail.events"
            :key="event.id"
            class="event-item"
          >
            <time class="ev-time">{{
              new Date(event.created_at).toLocaleString('zh-CN')
            }}</time>
            <div class="ev-body">
              <strong
                >{{ labels[event.kind] || event.kind }} · {{ event.employee
                }}<template v-if="event.target_employee">
                  → {{ event.target_employee }}</template
                ></strong
              >
              <p v-if="event.summary">{{ event.summary }}</p>
              <p
                v-for="receipt in event.deliveries"
                :key="receipt.conversation_id"
              >
                {{ receipt.destination }} ·
                {{ delivery[receipt.status] || receipt.status }}
              </p>
            </div>
          </div>
        </div>
      </div>
      <NButton
        v-if="detail.has_more"
        size="small"
        :disabled="loading"
        @click="load(true)"
        >查看更早记录</NButton
      >
    </template>
  </section>
</template>
<style scoped src="../styles/task-center.css"></style>
<style scoped>
.wi-detail-content {
  display: flex;
  flex-direction: column;
  gap: 16px;
}
.detail-top-card {
  background: var(--id-bg-subtle);
  border: 1px solid var(--id-border-subtle);
  border-radius: var(--id-radius-lg);
  padding: 14px;
  display: flex;
  flex-direction: column;
  gap: 10px;
}
.detail-main-title {
  margin: 0;
  font-size: 15px;
  font-weight: 600;
  color: var(--id-text-primary);
  line-height: 1.45;
}
.detail-tag-row {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 8px;
}
.detail-group {
  font-size: 12px;
  color: var(--id-text-secondary);
}
.detail-section {
  display: flex;
  flex-direction: column;
  gap: 6px;
}
.d-label {
  font-size: 12px;
  font-weight: 600;
  color: var(--id-text-secondary);
}
.d-val {
  margin: 0;
  font-size: 13px;
  color: var(--id-text-primary);
  line-height: 1.55;
}
.d-val.highlight {
  background: var(--id-bg-subtle);
  border: 1px solid var(--id-border-subtle);
  border-radius: var(--id-radius-md);
  padding: 10px 12px;
}
.linked-tasks-card-list {
  display: flex;
  flex-direction: column;
  gap: 8px;
}
.linked-task-item {
  display: flex;
  justify-content: space-between;
  align-items: center;
  flex-wrap: wrap;
  gap: 12px;
  background: var(--id-bg-subtle);
  border: 1px solid var(--id-border-subtle);
  border-radius: var(--id-radius-md);
  padding: 10px 12px;
}
.task-info {
  display: flex;
  flex-direction: column;
  gap: 2px;
}
.t-id {
  font-family: var(--id-font-mono);
  font-variant-numeric: tabular-nums;
  font-size: 12px;
  font-weight: 600;
  color: var(--id-text-primary);
}
.t-sub {
  font-size: 12px;
  color: var(--id-text-muted);
}
.handoff-chain {
  display: flex;
  flex-direction: column;
  gap: 8px;
}
.chain-node {
  display: flex;
  align-items: center;
  gap: 10px;
  background: var(--id-bg-surface);
  border: 1px solid var(--id-border-subtle);
  border-radius: var(--id-radius-md);
  padding: 8px 12px;
}
.node-badge {
  width: 22px;
  height: 22px;
  border-radius: 50%;
  background: var(--id-bg-muted);
  border: 1px solid var(--id-border-subtle);
  color: var(--id-text-primary);
  font-family: var(--id-font-mono);
  font-variant-numeric: tabular-nums;
  font-size: 12px;
  font-weight: 600;
  display: flex;
  align-items: center;
  justify-content: center;
  flex-shrink: 0;
}
.node-meta {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 6px 12px;
  font-size: 12px;
}
.node-act {
  color: var(--id-text-secondary);
}
.node-time {
  color: var(--id-text-muted);
  font-family: var(--id-font-mono);
  font-variant-numeric: tabular-nums;
  font-size: 12px;
}
.events-list {
  display: flex;
  flex-direction: column;
  gap: 8px;
}
.event-item {
  display: flex;
  gap: 12px;
  background: var(--id-bg-subtle);
  border: 1px solid var(--id-border-subtle);
  border-radius: var(--id-radius-md);
  padding: 10px 12px;
}
.ev-time {
  font-size: 12px;
  color: var(--id-text-muted);
  font-family: var(--id-font-mono);
  font-variant-numeric: tabular-nums;
  white-space: nowrap;
}
.ev-body {
  flex: 1;
  font-size: 12px;
  color: var(--id-text-primary);
}
.ev-body p {
  margin: 4px 0 0 0;
  color: var(--id-text-secondary);
  line-height: 1.5;
}
.note-section {
  background: var(--id-bg-subtle);
  border: 1px solid var(--id-border-subtle);
  border-radius: var(--id-radius-lg);
  padding: 12px 14px;
}
.note-input-row {
  display: flex;
  gap: 8px;
  align-items: center;
}
</style>

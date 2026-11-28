<script setup lang="ts">
import { onUnmounted, ref, watch } from 'vue';
import { NAlert, NButton, NCard, NEmpty, NForm, NFormItem, NInput, NInputNumber, NModal, NSelect, NSkeleton, NTag } from 'naive-ui';
import { scheduleRunReason, scheduleExecutionStatus } from './schedule-run-labels';
import { D2ApiError } from '../api/client';
import { chatSchedulesApi, type ChatScheduleFrequency, type ChatScheduleRule, type ManagedChatSchedule } from '../api/chat-schedules';

const props = defineProps<{ workspace: string; employeeId: string }>();
const items = ref<ManagedChatSchedule[]>([]);
const loading = ref(false), busy = ref(false), error = ref(''), editorError = ref('');
const editing = ref<ManagedChatSchedule>();
const frequency = ref<ChatScheduleFrequency>('daily');
const timezone = ref('Asia/Shanghai'), at = ref(''), times = ref('09:00'), days = ref('');
const interval = ref(1), minute = ref(0);
let controller = new AbortController();
const types = { once: '仅一次', hourly: '按小时', daily: '每天', weekly: '每周', monthly: '每月' };
const statuses = { active: '已启用', paused: '已停用', completed: '已结束', canceled: '已取消' };
const providers: Record<string, string> = { web: 'Web', feishu: '飞书', dingtalk: '钉钉', wecom: '企微' };
const options = Object.entries(types).map(([value, label]) => ({ value, label }));
const weekdays = ['周一','周二','周三','周四','周五','周六','周日'];
function source(row: ManagedChatSchedule) {
  return [providers[row.channel] || row.channel, row.chat_type === 'group' ? '群聊' : '私聊', row.source_label].filter(Boolean).join(' · ');
}
function timeText(ts: string | undefined, tz = 'Asia/Shanghai') {
  return ts ? new Intl.DateTimeFormat('zh-CN', { timeZone: tz, dateStyle: 'medium', timeStyle: 'short', hour12: false }).format(new Date(ts)) : '无';
}
function ruleText(rule: ChatScheduleRule) {
  if (rule.frequency === 'once') return `仅一次 · ${timeText(rule.at, rule.timezone)}`;
  if (rule.frequency === 'hourly') return (rule.interval || 1) > 1 ? `每隔 ${rule.interval} 小时` : `每小时第 ${rule.minute ?? 0} 分钟`;
  const marks = rule.frequency === 'weekly' ? rule.weekdays?.map(d => weekdays[d - 1]).join('、') : rule.frequency === 'monthly' ? rule.month_days?.map(d => d === -1 ? '最后一天' : `${d} 日`).join('、') : '';
  return [types[rule.frequency], marks, rule.times?.join('、')].filter(Boolean).join(' · ');
}
async function refresh() {
  controller.abort();
  controller = new AbortController();
  const req = controller;
  error.value = ''; loading.value = true; busy.value = false; editing.value = undefined;
  try {
    const result = await chatSchedulesApi.list(props.workspace, props.employeeId, req.signal);
    if (!req.signal.aborted) items.value = result.items;
  } catch (e) {
    if (!req.signal.aborted) { items.value = []; error.value = e instanceof Error ? e.message : '加载定时任务失败'; }
  } finally {
    if (!req.signal.aborted) loading.value = false;
  }
}
function open(row: ManagedChatSchedule) {
  const r = row.schedule;
  frequency.value = r.frequency; timezone.value = r.timezone || 'Asia/Shanghai';
  at.value = r.at || ''; times.value = r.times?.join(', ') || '09:00';
  days.value = (r.frequency === 'weekly' ? r.weekdays : r.month_days)?.join(', ') || '';
  interval.value = r.interval || 1; minute.value = r.minute || 0;
  editorError.value = ''; editing.value = row;
}
function tokens(value: string) {
  return value.split(/[,，、\s]+/).filter(Boolean);
}
function buildRule(): ChatScheduleRule {
  const r: ChatScheduleRule = { frequency: frequency.value, timezone: timezone.value.trim() || 'Asia/Shanghai' };
  if (r.frequency === 'once') r.at = at.value.trim();
  else if (r.frequency === 'hourly') {
    if (interval.value > 1) r.interval = interval.value;
    else r.minute = minute.value;
  } else {
    r.times = tokens(times.value);
    if (r.frequency === 'weekly') r.weekdays = tokens(days.value).map(Number);
    if (r.frequency === 'monthly') r.month_days = tokens(days.value).map(Number);
  }
  return r;
}
async function patch(row: ManagedChatSchedule, change: { schedule?: ChatScheduleRule; enabled?: boolean }) {
  if (busy.value) return;
  busy.value = true; editorError.value = ''; error.value = '';
  const req = controller;
  try {
    const result = await chatSchedulesApi.patch(props.workspace, props.employeeId, row, change, req.signal);
    if (!req.signal.aborted) {
      items.value = items.value.map(v => v.schedule_id === result.schedule_id ? result : v);
      editing.value = undefined;
    }
  } catch (e) {
    if (!req.signal.aborted) {
      const msg = e instanceof Error ? e.message : '保存失败';
      if (e instanceof D2ApiError && [401,403,404].includes(e.status)) {
        items.value = []; editing.value = undefined; error.value = msg;
      } else if (editing.value) editorError.value = msg;
      else error.value = msg;
    }
  } finally {
    if (!req.signal.aborted) busy.value = false;
  }
}
watch(() => [props.workspace, props.employeeId], () => { items.value = []; void refresh(); }, { immediate: true });
onUnmounted(() => controller.abort());
</script>

<template>
  <div class="chat-schedule-list">
    <div class="schedule-heading">
      <span>适合把当前对话里的事交给员工定时做，例如每周更新这份周报。只在原对话创建和取消。</span>
      <NButton size="small" secondary :disabled="busy || loading" @click="refresh">刷新</NButton>
    </div>
    <NAlert v-if="error" type="error">{{ error }}</NAlert>
    <NSkeleton v-if="loading" text :repeat="3" />
    <NEmpty v-else-if="!items.length && !error" description="暂无会话定时任务" />
    <NCard v-for="row in items" v-else :key="row.schedule_id" size="small" :title="row.title || row.instruction.slice(0, 38)">
      <template #header-extra><NTag size="small" :type="row.status === 'active' ? 'success' : 'default'">{{ statuses[row.status] }}</NTag></template>
      <div class="schedule-heading">
        <div class="schedule-meta">
          <strong>{{ source(row) }}</strong>
          <span>{{ ruleText(row.schedule) }} · {{ row.schedule.timezone || 'Asia/Shanghai' }}</span>
          <span>下次执行：{{ timeText(row.next_due, row.schedule.timezone) }}</span>
        </div>
        <div v-if="row.status === 'active' || row.status === 'paused'" class="schedule-actions">
          <NButton size="small" secondary :disabled="busy" @click="open(row)">改时间</NButton>
          <NButton size="small" secondary :disabled="busy" @click="patch(row, { enabled: row.status === 'paused' })">{{ row.status === 'paused' ? '启用' : '停用' }}</NButton>
        </div>
      </div>
      <details class="schedule-instruction"><summary>任务说明</summary><p>{{ row.instruction }}</p></details>
      <details class="schedule-instruction">
        <summary>最近触发记录</summary>
        <p v-if="!row.recent_runs?.length">暂无触发记录。</p>
        <ol v-else>
          <li v-for="run in row.recent_runs" :key="run.due">
            {{ timeText(run.due, row.schedule.timezone) }} · {{ run.status === 'accepted' ? '已创建任务' : '已跳过' }}
            <template v-if="run.reason"> · {{ scheduleRunReason(run.reason) }}</template>
            <template v-if="run.execution_status"> · {{ scheduleExecutionStatus(run.execution_status) }}</template>
            <small v-if="run.task_id"> · 任务 {{ run.task_id }}</small>
          </li>
        </ol>
      </details>
    </NCard>
    <NModal :show="!!editing" preset="card" title="修改定时时间" style="width: min(480px, 92vw)" :mask-closable="!busy" :closable="!busy" @update:show="v => { if (!v && !busy) editing = undefined }">
      <NAlert v-if="editorError" type="error">{{ editorError }}</NAlert>
      <NForm label-placement="top" :disabled="busy">
        <NFormItem label="重复方式"><NSelect v-model:value="frequency" :options="options" /></NFormItem>
        <NFormItem label="时区"><NInput v-model:value="timezone" placeholder="Asia/Shanghai" /></NFormItem>
        <NFormItem v-if="frequency === 'once'" label="执行时间（含时区偏移）"><NInput v-model:value="at" placeholder="2026-12-01T09:00:00+08:00" /></NFormItem>
        <template v-else-if="frequency === 'hourly'">
          <NFormItem label="间隔小时数"><NInputNumber v-model:value="interval" :min="1" :max="168" /></NFormItem>
          <NFormItem v-if="interval <= 1" label="在每小时的第几分钟执行"><NInputNumber v-model:value="minute" :min="0" :max="59" /></NFormItem>
        </template>
        <template v-else>
          <NFormItem label="执行时间，可用逗号分隔"><NInput v-model:value="times" placeholder="09:00, 18:00" /></NFormItem>
          <NFormItem v-if="frequency === 'weekly'" label="星期一到星期日（1–7），可多选"><NInput v-model:value="days" placeholder="1, 3, 5" /></NFormItem>
          <NFormItem v-if="frequency === 'monthly'" label="日期（1–31），-1 表示最后一天"><NInput v-model:value="days" placeholder="1, 15, -1" /></NFormItem>
        </template>
      </NForm>
      <template #footer>
        <div class="schedule-actions">
          <NButton :disabled="busy" @click="editing = undefined">取消</NButton>
          <NButton type="primary" :loading="busy" @click="editing && patch(editing, { schedule: buildRule() })">保存</NButton>
        </div>
      </template>
    </NModal>
  </div>
</template>

<style scoped>
.chat-schedule-list, .schedule-meta { display: grid; gap: 8px; }
.schedule-heading { display: flex; justify-content: space-between; align-items: flex-start; gap: 12px; flex-wrap: wrap; }
.schedule-meta, .schedule-heading > span { color: var(--id-text-secondary); }
.schedule-actions { display: flex; gap: 8px; justify-content: flex-end; flex-wrap: wrap; }
.schedule-instruction { margin-top: 10px; }
.schedule-instruction p { white-space: pre-wrap; overflow-wrap: anywhere; }
</style>

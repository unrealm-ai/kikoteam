<script setup lang="ts">
import type { TaskProgress } from '../api/tasks';
import { taskPhaseLabel } from '../utils/session-presentation';

defineProps<{ progress: TaskProgress }>();

const delivery: Record<string, string> = {
  confirmed: '平台已确认发送', pending: '等待发送或回执', unconfirmed: '发送结果暂未确认',
  partial: '部分会话已确认发送', failed: '未能发送',
};
const channels: Record<string, string> = { web: 'Web', feishu: '飞书', dingtalk: '钉钉', wecom: '企微' };
const label = (phase: string) => taskPhaseLabel(phase);
</script>

<template>
  <dl class="task-progress" aria-label="任务状态摘要">
    <div><dt>任务状态</dt><dd>{{ label(progress.phase) }}</dd></div>
    <div v-if="progress.assignee"><dt>任务员工</dt><dd>{{ progress.assignee }}</dd></div>
    <div v-if="progress.confirmation?.recipient"><dt>确认人</dt><dd>{{ progress.confirmation.recipient }}</dd></div>
    <div v-if="progress.confirmation?.question"><dt>确认内容</dt><dd>{{ progress.confirmation.question }}</dd></div>
    <div v-for="(item, index) in progress.handoffs" :key="index"><dt>接手任务</dt><dd>{{ item.assignee }} · {{ label(item.phase) }}</dd></div>
    <div v-for="item in progress.delivery" :key="item.channel"><dt>结果通知</dt><dd>{{ channels[item.channel] }} · {{ delivery[item.phase] || '发送结果暂未确认' }}</dd></div>
  </dl>
</template>

<style scoped>
.task-progress{margin:8px 0;font-size:12px}
.task-progress div{display:grid;grid-template-columns:5em minmax(0,1fr);gap:8px;margin:4px 0}
.task-progress dt{color:var(--id-text-muted)}
.task-progress dd{margin:0;overflow-wrap:anywhere}
</style>

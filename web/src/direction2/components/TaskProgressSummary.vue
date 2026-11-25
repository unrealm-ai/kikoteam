<script setup lang="ts">
import type { TaskProgress } from '../api/tasks';

defineProps<{ progress: TaskProgress }>();

const phases: Record<string, string> = {
  planned: '待办：尚未启动', todo_canceled: '待办已取消（未执行）', queued: '排队中',
  preparing_environment: '环境准备中', running: '执行中', background_active: '后台步骤仍在执行',
  preparing_confirmation: '正在准备人工确认', waiting_confirmation: '等待人工确认',
  waiting_resume: '已收到答复，等待继续执行', waiting_handoff: '已交接，等待处理结果',
  waiting_acceptance: '已提交成果，等待原任务验收', handoff_failed: '交接处理失败', completed: '已完成', failed: '失败',
  stopping: '正在停止', stopped: '已停止', canceled: '已取消', cancelled: '已取消',
  interrupted: '执行中断', archived: '已归档', blocked: '执行受阻', idle: '当前无执行轮次',
  waiting_for_action: '等待处理', rejected: '未能开始执行',
  engine_recovery_pending: '正在恢复执行环境，等待旧引擎交还执行权', unknown: '状态暂未确认', success: '已完成',
};
const delivery: Record<string, string> = {
  confirmed: '平台已确认发送', pending: '等待发送或回执', unconfirmed: '发送结果暂未确认',
  partial: '部分会话已确认发送', failed: '未能发送',
};
const channels: Record<string, string> = { web: 'Web', feishu: '飞书', dingtalk: '钉钉', wecom: '企微' };
const label = (phase: string) => phases[phase] || '状态暂未确认';
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

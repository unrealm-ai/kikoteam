<script setup lang="ts">
import { computed } from 'vue';
import type { SessionEvent } from '../api/manager';
import { eventText, sessionStatus, taskReport, type TaskNavigationTarget } from '../utils/session-presentation';
import SessionMarkdown from './SessionMarkdown.vue';
const props = defineProps<{ event: SessionEvent; title?: string | undefined; canOpenTask?: boolean | undefined; reply?: SessionEvent[] | undefined }>();
const emit = defineEmits<{ openTask: [target: TaskNavigationTarget] }>();
const report = computed(() => taskReport(props.event));
const successful = computed(() => report.value?.outcome === 'completed' && !report.value.unknown);
</script>
<template>
  <section :class="['report-card', { successful, failed: report?.outcome === 'failed' || report?.outcome === 'rejected' }]">
    <header><span class="report-symbol" aria-hidden="true">{{ successful ? '✓' : '↳' }}</span><div><span class="report-label">任务回报</span><h3>{{ title || '任务执行结果' }}</h3></div><span class="report-outcome">{{ report ? sessionStatus(report.outcome) : '结果待核对' }}</span></header>
    <p v-if="!report" class="report-warning">回报格式暂不可识别，请通过会话任务查看实际结果。</p>
    <template v-else>
      <div class="report-facts"><span>本轮结果已返回</span><span v-if="event.status === 'queued'">等待主对话处理</span><span v-else-if="event.status === 'started'">正在整理回报</span><span v-else>{{ sessionStatus(event.status) }}</span></div>
      <p v-if="report.unknown" class="report-warning">存在结果未知的工具调用，不能确认任务全部成功。</p>
      <p v-if="report.background === null || report.background > 0 || report.queued > 0" class="report-warning">{{ report.background === null ? '后台进程状态待确认。' : report.background > 0 ? `仍有 ${report.background} 个后台进程。` : '' }}{{ report.queued > 0 ? `仍有 ${report.queued} 条排队输入。` : '' }}</p>
      <div v-if="reply?.length" class="report-answer"><SessionMarkdown v-for="message in reply" :key="message.message_id || message.seq" :text="eventText(message)" /></div>
      <details class="report-original" :open="!reply?.length"><summary>{{ reply?.length ? '查看原始任务回报' : '查看本轮结果' }}</summary><SessionMarkdown :text="report.result || '本轮没有返回文本结果。'" /></details>
      <footer><span>本轮结束不代表整个任务已完成</span><button v-if="canOpenTask" type="button" @click="emit('openTask', report.task)">查看任务与执行过程 ↗</button></footer>
    </template>
  </section>
</template>
<style scoped>
.report-card{border:1px solid var(--id-border-subtle);border-radius:12px;overflow:hidden;background:var(--id-bg-surface);min-width:0}.report-card>header{display:flex;align-items:center;gap:10px;padding:17px 18px 12px}.report-symbol{width:34px;height:34px;display:grid;place-items:center;border-radius:10px;font-size:17px;background:var(--id-status-warning-soft);color:var(--id-status-warning);flex-shrink:0}.successful .report-symbol{background:var(--id-status-success-soft);color:var(--id-status-success)}.failed .report-symbol,.failed .report-outcome{background:var(--id-status-danger-soft);color:var(--id-status-danger)}.report-label{color:var(--id-text-muted);font-size:11px}.report-card h3{margin:1px 0 0;font-size:14px;font-weight:600;overflow-wrap:anywhere}.report-outcome{margin-left:auto;flex-shrink:0;font-size:11px;color:var(--id-text-secondary);padding:3px 7px;border-radius:5px;background:var(--id-bg-muted)}.successful .report-outcome{color:var(--id-status-success);background:var(--id-status-success-soft)}.report-facts{display:flex;gap:12px;flex-wrap:wrap;padding:0 18px 12px;color:var(--id-text-muted);font-size:11px}.report-warning{margin:0;padding:8px 18px;color:var(--id-status-warning);font-size:12px}.report-answer{padding:2px 18px 16px;display:flex;flex-direction:column;gap:14px}.report-original{border-top:1px solid var(--id-border-subtle);padding:11px 18px}.report-original summary{color:var(--id-text-muted);font-size:11px;cursor:pointer}.report-original[open] summary{margin-bottom:14px}.report-card footer{display:flex;justify-content:space-between;gap:10px;flex-wrap:wrap;padding:12px 18px;background:var(--id-bg-subtle);border-top:1px solid var(--id-border-subtle);font-size:11px;color:var(--id-text-muted)}.report-card footer button{border:0;background:transparent;color:var(--id-accent-primary);font:inherit;padding:0;cursor:pointer}button:focus-visible,summary:focus-visible{outline:2px solid var(--id-accent-primary);outline-offset:3px}
</style>

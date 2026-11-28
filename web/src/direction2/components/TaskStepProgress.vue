<script setup lang="ts">
import type { ReportedProgress } from '../api/tasks';
import { clockTime } from '../utils/session-presentation';
defineProps<{ progress: ReportedProgress; previous?: boolean }>();
const labels = {pending:'待开始', in_progress:'进行中', done:'已完成', skipped:'已跳过'};
</script>
<template>
  <section class="step-progress" :aria-label="previous ? '上一轮进展' : '当前进展'">
    <div class="meta"><span>{{ previous ? '上一轮进展' : '当前进展' }}</span><time :datetime="progress.occurred_at" :title="new Date(progress.occurred_at).toLocaleString()">更新于 {{ clockTime(progress.occurred_at) }}</time></div>
    <p>{{ progress.summary }}</p>
    <div v-if="progress.metric" class="metric">
      <span>{{ progress.steps.find(step => step.id === progress.metric?.step_id)?.label }}</span>
      <progress :value="progress.metric.done" :max="progress.metric.total" aria-label="步骤进度" />
      <span>{{ progress.metric.done }} / {{ progress.metric.total }} {{ progress.metric.unit }}</span>
    </div>
    <details v-if="progress.steps.length"><summary>查看步骤</summary>
      <ol><li v-for="step in progress.steps" :key="step.id"><span>{{ step.label }}</span><small :class="step.status">{{ labels[step.status] }}</small></li></ol>
    </details>
  </section>
</template>
<style scoped>
.step-progress{display:grid;gap:6px;padding:10px 12px;background:var(--id-bg-subtle);border-radius:var(--id-radius-md);min-width:0;color:var(--id-text-primary)}
.meta,.metric,li{display:flex;align-items:center;justify-content:space-between;gap:10px}
.meta,.metric,small,summary{font-size:11px;color:var(--id-text-secondary)}
p,ol{margin:0}p,li span{overflow-wrap:anywhere}
progress{flex:1;min-width:24px;height:5px;accent-color:var(--id-accent-primary)}
.metric{flex-wrap:wrap}.metric span{overflow-wrap:anywhere}
summary{cursor:pointer;width:fit-content}
ol{padding:5px 0 0;list-style:none}li{padding:3px 0}small{flex:none}.in_progress{color:var(--id-accent-primary)}.done{color:var(--id-status-success)}
</style>

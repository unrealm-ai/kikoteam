<script setup lang="ts">
import { computed } from 'vue';
import type { ReportedProgress } from '../api/tasks';
import { clockTime } from '../utils/session-presentation';

const props = defineProps<{ progress: ReportedProgress; previous?: boolean; inCard?: boolean; hasResultPreview?: boolean }>();
const labels = { pending: '待开始', in_progress: '进行中', done: '已完成', skipped: '已跳过' };
const completed = computed(() => props.progress.steps.filter(step => step.status === 'done').length);
const activeSteps = computed(() => props.progress.steps.filter(step => step.status === 'in_progress'));
const summaryStep = computed(() => activeSteps.value.length === 1 ? activeSteps.value[0]?.id : null);
</script>

<template>
  <section class="step-progress" :aria-label="previous ? '上一轮进展' : '当前进展'">
    <header class="step-heading">
      <strong>{{ progress.steps.length ? '执行步骤' : previous ? '上一轮进展' : '当前进展' }}</strong>
      <span v-if="progress.steps.length">{{ completed }} / {{ progress.steps.length }} 已完成</span>
    </header>
    <p v-if="!summaryStep && !(hasResultPreview && progress.steps.length)" class="summary">{{ progress.summary }}</p>
    <ol v-if="progress.steps.length" class="timeline">
      <li v-for="step in progress.steps" :key="step.id" :class="step.status">
        <span class="marker" aria-hidden="true">{{ step.status === 'done' ? '✓' : step.status === 'skipped' ? '−' : '' }}</span>
        <div class="step-copy">
          <span class="step-label">{{ step.label }}</span>
          <small :class="{ 'visually-hidden': step.status !== 'skipped' }">{{ labels[step.status] }}</small>
          <p v-if="summaryStep === step.id && progress.summary !== step.label">{{ progress.summary }}</p>
          <div v-if="progress.metric?.step_id === step.id" class="metric">
            <progress :value="progress.metric.done" :max="progress.metric.total" :aria-label="step.label + '进度'" />
            <span>{{ progress.metric.done }} / {{ progress.metric.total }} {{ progress.metric.unit }}</span>
          </div>
        </div>
      </li>
    </ol>
    <time v-if="!inCard" :datetime="progress.occurred_at">更新于 {{ clockTime(progress.occurred_at) }}</time>
  </section>
</template>

<style scoped>
.step-progress { min-width: 0; padding: 13px 16px; color: var(--id-text-primary); }
.step-heading, .metric { display: flex; align-items: center; justify-content: space-between; gap: 12px; }
.step-heading strong { font-size: 13px; font-weight: 600; }
.step-heading span, time, small, .metric span { color: var(--id-text-muted); font-size: 11px; }
p { margin: 3px 0 0; color: var(--id-text-secondary); font-size: 12px; line-height: 1.6; overflow-wrap: anywhere; }
.summary { margin-top: 6px; }
.timeline { margin: 8px 0 0; padding: 0; list-style: none; }
.timeline li { position: relative; display: grid; grid-template-columns: 16px minmax(0,1fr); gap: 9px; padding: 7px 8px; border-radius: 6px; }
.timeline li:not(:last-child)::after { content: ''; position: absolute; left: 15px; top: 24px; bottom: -8px; width: 1px; background: var(--id-border-subtle); }
.marker { z-index: 1; box-sizing: border-box; width: 15px; height: 15px; margin-top: 2px; display: grid; place-items: center; border: 1.5px solid var(--id-border-strong); border-radius: 50%; background: var(--id-bg-surface); font: bold 10px/1 sans-serif; }
.done .marker { color: white; border-color: var(--id-status-success); background: var(--id-status-success); }
.in_progress { background: var(--id-accent-primary-soft); }
.in_progress .marker { border: 3px solid var(--id-accent-primary); }
.in_progress .step-label { color: var(--id-accent-primary); font-weight: 600; }
.pending, .skipped { color: var(--id-text-muted); }
.step-label { font-size: 13px; line-height: 19px; overflow-wrap: anywhere; }
small { margin-left: 6px; }
.visually-hidden { position: absolute; width: 1px; height: 1px; padding: 0; margin: -1px; overflow: hidden; clip-path: inset(50%); white-space: nowrap; }
.metric { margin-top: 5px; flex-wrap: wrap; }
.metric progress { flex: 1; min-width: 24px; height: 5px; accent-color: var(--id-accent-primary); }
time { display: block; margin-top: 8px; }
</style>

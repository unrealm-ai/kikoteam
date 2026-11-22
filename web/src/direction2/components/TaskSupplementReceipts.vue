<script setup lang="ts">
import type { TaskReport, TaskSupplementReceipt } from '../api/tasks';
defineProps<{ report: TaskReport | null }>();
function receiptText(state: TaskSupplementReceipt['context_state']) {
  if (state === 'committed') return '已进入本轮上下文';
  if (state === 'absent') return '未进入本轮上下文';
  if (state === 'pending') return '等待接收证据';
  return '接收状态待核对';
}
</script>
<template>
  <details v-if="report?.supplement_receipts?.length" class="task-supplements">
    <summary>本轮补充（{{ report.supplement_receipts.length }}{{ report.supplement_receipts_truncated ? '+' : '' }}）</summary>
    <ul>
      <li v-for="receipt in report.supplement_receipts" :key="receipt.input_id">输入 {{ receipt.input_seq }}：{{ receiptText(receipt.context_state) }}</li>
    </ul>
    <p>回执只说明是否进入本轮上下文，不代表模型采纳了补充要求。</p>
  </details>
</template>
<style scoped>
.task-supplements{padding:0 18px;font-size:12px;color:var(--id-text-secondary)}
summary{cursor:pointer;padding-block:6px}
ul{padding-inline-start:20px}
p{color:var(--id-text-muted)}
</style>

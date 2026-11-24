<script setup lang="ts">
import { computed, ref } from 'vue';
import type { SessionEvent } from '../api/manager';
import { isTaskDetailLinkTool, availability, duration, jsonText, outputText, record, taskAction, toolTaskTarget, text, toolFailed, toolName, toolOutput, toolStatus, toolTarget, type TaskNavigationTarget } from '../utils/session-presentation';
const props = defineProps<{ event: SessionEvent; taskTitle?: string | undefined; canOpenTask?: boolean | undefined }>();
const emit = defineEmits<{ openTask: [target: TaskNavigationTarget] }>();
const tab = ref<'input' | 'output'>('output');
const action = computed(() => taskAction(props.event));
const output = computed(() => record(toolOutput(props.event)));
const input = computed(() => props.event.input_state === 'available' ? record(props.event.input) : {});
const target = computed(() => toolTaskTarget(props.event));
const status = computed(() => toolStatus(props.event));
const failed = computed(() => toolFailed(props.event));
const instruction = computed(() => text(input.value.instruction) || text(input.value.text));
const error = computed(() => props.event.error?.message || text(record(output.value.error).message));
const outputID = computed(() => `tool-output-${props.event.tool_use_id || props.event.seq}`);
const inputID = computed(() => `tool-input-${props.event.tool_use_id || props.event.seq}`);
function moveTab(event: KeyboardEvent) {
  if (!['ArrowLeft', 'ArrowRight', 'Home', 'End'].includes(event.key)) return;
  event.preventDefault();
  tab.value = event.key === 'Home' ? 'input' : event.key === 'End' ? 'output' : tab.value === 'input' ? 'output' : 'input';
  (event.currentTarget as HTMLElement).querySelectorAll<HTMLButtonElement>('[role="tab"]')[tab.value === 'input' ? 0 : 1]?.focus();
}
</script>
<template>
  <section :class="['tool-card', { 'task-action': action, failed, pending: event.status !== 'completed' || status === '后台运行中' || status === '调用结束' || status === '已提交投递' || status === '送达待核对' }]">
    <header v-if="action" class="task-action-header">
      <span class="tool-symbol" aria-hidden="true">↗</span>
      <div class="action-title"><span>{{ action }}</span><strong>{{ taskTitle || text(input.title) || '会话任务' }}</strong></div>
      <span class="tool-status">{{ status }}</span>
    </header>
    <div v-if="action" class="task-action-body">
      <p v-if="error" class="tool-error">{{ error }}</p>
      <p v-else-if="failed" class="tool-error">操作未成功，请展开调用详情查看原因。</p>
      <p v-else-if="event.name === 'task_delegation_create_session'">{{ status === '已触发' ? '任务已受理，执行进展与结果可在任务中查看。' : event.status === 'completed' ? '调用已结束，尚无法确认任务受理结果。' : '正在向员工发起任务，以实际受理结果为准。' }}</p>
      <p v-else-if="event.name === 'task_delegation_stop_session'">停止请求与实际停止状态分别确认。</p>
      <p v-else-if="isTaskDetailLinkTool(event.name) && status === '已提交投递'">发送请求已受理，尚未确认送达。</p>
      <p v-else-if="isTaskDetailLinkTool(event.name) && status === '送达待核对'">渠道尚未确认送达，请稍后核对。</p>
      <p v-else-if="isTaskDetailLinkTool(event.name) && status === '未发送'" class="tool-error">渠道未发送任务详情链接。</p>
      <p v-else>{{ event.summary || '任务操作与结果已记录。' }}</p>
      <button v-if="canOpenTask && target" type="button" class="task-link" @click="emit('openTask', target)">查看任务 <span aria-hidden="true">↗</span></button>
      <details v-if="instruction" class="task-instruction"><summary>任务要求</summary><p>{{ instruction }}</p></details>
    </div>
    <details class="tool-details">
      <summary>
        <template v-if="!action"><span class="tool-symbol" aria-hidden="true">{{ failed ? '!' : event.status === 'completed' ? '✓' : '⋯' }}</span><span class="tool-heading"><strong>{{ toolName(event) }}</strong><span v-if="toolTarget(event)" class="tool-target">{{ toolTarget(event) }}</span></span><span class="tool-status">{{ status }}</span></template>
        <span v-else>调用详情</span><span v-if="duration(event)" class="tool-duration">{{ duration(event) }}</span><span class="chevron" aria-hidden="true">⌄</span>
      </summary>
      <div class="tool-content">
        <p v-if="error && !action" class="tool-error" role="status">{{ error }}</p>
        <div class="io-tabs" role="tablist" aria-label="工具输入与输出" @keydown="moveTab"><button :id="`${inputID}-tab`" type="button" role="tab" :tabindex="tab === 'input' ? 0 : -1" :aria-selected="tab === 'input'" :aria-controls="inputID" @click="tab = 'input'">输入</button><button :id="`${outputID}-tab`" type="button" role="tab" :tabindex="tab === 'output' ? 0 : -1" :aria-selected="tab === 'output'" :aria-controls="outputID" @click="tab = 'output'">输出</button></div>
        <div v-if="tab === 'input'" :id="inputID" role="tabpanel" :aria-labelledby="`${inputID}-tab`">
          <template v-if="event.input_state === 'available'"><template v-if="typeof input.command === 'string'"><div class="field-label">执行命令</div><pre>{{ input.command }}</pre><dl v-if="input.cwd"><dt>工作目录</dt><dd>{{ input.cwd }}</dd></dl></template><template v-else><div class="field-label">请求参数</div><pre>{{ jsonText(event.input) }}</pre></template></template>
          <p v-else class="unavailable">输入：{{ availability(event.input_state) }}</p>
        </div>
        <div v-else :id="outputID" role="tabpanel" :aria-labelledby="`${outputID}-tab`">
          <template v-if="event.output_state === 'available'"><div class="output-meta"><span>{{ status }}</span><span v-if="typeof output.exit_code === 'number'">退出码 {{ output.exit_code }}</span></div><p v-if="output.output_truncated || output.has_more || output.output_complete === false" class="unavailable">当前输出不完整，仅展示已返回内容。</p><pre v-if="outputText(event)">{{ outputText(event) }}</pre><p v-else class="unavailable">工具未返回文本内容。</p><details class="raw-output"><summary>原始响应</summary><pre>{{ jsonText(event.output) }}</pre></details></template>
          <p v-else class="unavailable">输出：{{ availability(event.output_state) }}</p>
        </div>
        <div class="tool-technical-name">{{ event.name || '工具' }}</div>
      </div>
    </details>
  </section>
</template>
<style scoped>
.tool-card{border:1px solid var(--id-border-subtle);border-radius:12px;background:var(--id-bg-surface);overflow:hidden;font-size:12px;min-width:0}.tool-details>summary{list-style:none;display:flex;align-items:center;gap:10px;padding:13px 14px;cursor:pointer}.tool-details>summary::-webkit-details-marker{display:none}.tool-symbol{display:grid;place-items:center;flex-shrink:0;width:28px;height:28px;border-radius:8px;background:var(--id-status-success-soft);color:var(--id-status-success);font-size:16px}.tool-heading{display:flex;flex:1;min-width:0;flex-direction:column;gap:3px}.tool-heading strong{font-size:12px;font-weight:500}.tool-target{max-width:100%;overflow:hidden;text-overflow:ellipsis;white-space:nowrap;color:var(--id-text-muted);font-size:11px}.tool-status{font-size:11px;color:var(--id-status-success);flex-shrink:0}.tool-duration{font-size:11px;color:var(--id-text-muted);margin-left:auto;white-space:nowrap}.chevron{color:var(--id-text-muted)}.tool-details[open]>summary .chevron{transform:rotate(180deg)}.pending:not(.failed) .tool-status{color:var(--id-text-muted)}.pending:not(.failed) .tool-symbol{background:var(--id-bg-muted);color:var(--id-text-muted)}.failed .tool-status,.tool-error{color:var(--id-status-danger)}.failed .tool-symbol{background:var(--id-status-danger-soft);color:var(--id-status-danger)}.tool-content{padding:0 14px 14px;min-width:0}.io-tabs{display:flex;gap:20px;border-bottom:1px solid var(--id-border-subtle);margin-bottom:12px}.io-tabs button{padding:9px 2px;border:0;border-bottom:2px solid transparent;color:var(--id-text-secondary);background:transparent;cursor:pointer;font:inherit}.io-tabs button[aria-selected=true]{color:var(--id-accent-primary);border-bottom-color:var(--id-accent-primary)}pre{white-space:pre-wrap;overflow-wrap:anywhere;max-height:340px;overflow:auto;padding:12px;border-radius:6px;background:var(--id-bg-muted);font:12px/1.7 var(--id-font-mono);margin:8px 0}.output-meta{display:flex;justify-content:space-between;gap:12px;color:var(--id-text-secondary);font-size:11px}.field-label,.unavailable,.raw-output,.tool-technical-name{color:var(--id-text-muted);font-size:11px}.unavailable{margin:8px 0}.raw-output{margin-top:12px}.raw-output summary,.task-instruction summary{cursor:pointer}.tool-technical-name{margin-top:12px;overflow-wrap:anywhere}dl{display:flex;gap:12px;overflow-wrap:anywhere}dt{color:var(--id-text-muted)}dd{margin:0}.task-action-header{display:flex;align-items:center;gap:10px;padding:16px 16px 8px}.task-action-header .tool-symbol{background:var(--id-accent-primary-soft);color:var(--id-accent-primary);width:34px;height:34px}.action-title{flex:1;min-width:0}.action-title>span{font-size:11px;color:var(--id-text-muted)}.action-title strong{display:block;font-size:13px;overflow-wrap:anywhere}.task-action-body{padding:0 16px 12px}.task-action-body>p{color:var(--id-text-secondary);margin:6px 0;font-size:12px}.task-action-body>.tool-error{color:var(--id-status-danger)}.task-link{border:0;padding:5px 0;background:transparent;color:var(--id-accent-primary);font:inherit;font-size:12px;cursor:pointer}.task-action .tool-details{border-top:1px solid var(--id-border-subtle);color:var(--id-text-secondary)}.task-instruction{margin-top:8px;color:var(--id-text-muted);font-size:11px}.task-instruction p{white-space:pre-wrap;overflow-wrap:anywhere;color:var(--id-text-primary);max-height:300px;overflow:auto}button:focus-visible,summary:focus-visible{outline:2px solid var(--id-accent-primary);outline-offset:-2px}
</style>

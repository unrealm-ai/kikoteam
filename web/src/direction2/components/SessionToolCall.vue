<script setup lang="ts">
import { computed, ref } from 'vue';
import type { SessionEvent } from '../api/manager';
import type { TaskRecord } from '../api/tasks';
import TaskStepProgress from './TaskStepProgress.vue';
import { taskCardState } from '../utils/session-presentation';
import { isTaskDetailLinkTool, availability, duration, jsonText, outputText, record, taskAction, toolTaskTarget, text, toolFailed, toolName, toolOutput, toolStatus, toolTarget, type TaskNavigationTarget } from '../utils/session-presentation';
const props = defineProps<{ event: SessionEvent; results?: Map<string, string>; task?: TaskRecord | undefined; taskTitle?: string | undefined; canOpenTask?: boolean | undefined }>();
const emit = defineEmits<{ openTask: [target: TaskNavigationTarget] }>();
const tab = ref<'input' | 'output'>('output');
const action = computed(() => taskAction(props.event));
const output = computed(() => record(toolOutput(props.event)));
const input = computed(() => props.event.input_state === 'available' ? record(props.event.input) : {});
const target = computed(() => toolTaskTarget(props.event));
const status = computed(() => toolStatus(props.event));
const failed = computed(() => toolFailed(props.event));
const live = computed(() => status.value === '已触发' && props.task ? taskCardState(props.task) : undefined);
const isBash = computed(() => !action.value && ['bash_start', 'bash_poll', 'bash_write', 'bash_stop', 'bash_terminate'].includes(props.event.name || ''));
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
  <section :class="['tool-card', { 'task-action': action, 'is-bash': isBash, failed, pending: event.status !== 'completed' || status === '后台运行中' || status === '调用结束' || status === '已提交投递' || status === '送达待核对' }]">
    <header v-if="action" class="task-action-header">
      <span class="tool-symbol" aria-hidden="true">↗</span>
      <div class="action-title"><span>{{ action }}</span><strong>{{ taskTitle || text(input.title) || '会话任务' }}</strong></div>
      <span class="tool-status" :data-task-state="live?.state">{{ live?.label || status }}</span>
    </header>
    <div v-if="action" class="task-action-body">
      <p v-if="error" class="tool-error">{{ error }}</p>
      <p v-else-if="failed" class="tool-error">操作未成功，请展开调用详情查看原因。</p>
      <template v-else-if="live">
        <p v-if="live.message">{{ live.message }}</p>
        <p v-else-if="live.state === 'completed' && task?.latest_turn && results?.get(task.latest_turn.id)">{{ results.get(task.latest_turn.id) }}</p>
        <TaskStepProgress v-if="task?.reported_progress && live.showProgress" :progress="task.reported_progress" :previous="!task.execution.active_turn_id" />
      </template>
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
        <template v-if="!action"><span class="tool-symbol" aria-hidden="true">{{ failed ? '!' : event.status === 'completed' ? '✓' : '⋯' }}</span><span class="tool-heading"><strong>{{ toolName(event) }}</strong><span v-if="toolTarget(event)" class="tool-target" :class="{ 'bash-cmd': isBash }">{{ toolTarget(event) }}</span></span><span class="tool-status">{{ status }}</span></template>
        <span v-else>调用详情</span><span v-if="duration(event)" class="tool-duration">{{ duration(event) }}</span><span class="chevron" aria-hidden="true">⌄</span>
      </summary>
      <div class="tool-content">
        <p v-if="error && !action" class="tool-error" role="status">{{ error }}</p>
        <div class="io-tabs" role="tablist" aria-label="工具输入与输出" @keydown="moveTab"><button :id="`${inputID}-tab`" type="button" role="tab" :tabindex="tab === 'input' ? 0 : -1" :aria-selected="tab === 'input'" :aria-controls="inputID" @click="tab = 'input'">输入</button><button :id="`${outputID}-tab`" type="button" role="tab" :tabindex="tab === 'output' ? 0 : -1" :aria-selected="tab === 'output'" :aria-controls="outputID" @click="tab = 'output'">输出</button></div>
        <div v-if="tab === 'input'" :id="inputID" role="tabpanel" :aria-labelledby="`${inputID}-tab`">
          <template v-if="event.input_state === 'available'"><template v-if="typeof input.command === 'string'"><div class="field-label">执行命令</div><pre :class="{ 'terminal-pre': isBash }">{{ input.command }}</pre><dl v-if="input.cwd"><dt>工作目录</dt><dd>{{ input.cwd }}</dd></dl></template><template v-else><div class="field-label">请求参数</div><pre>{{ jsonText(event.input) }}</pre></template></template>
          <p v-else class="unavailable">输入：{{ availability(event.input_state) }}</p>
        </div>
        <div v-else :id="outputID" role="tabpanel" :aria-labelledby="`${outputID}-tab`">
          <template v-if="event.output_state === 'available'"><div class="output-meta"><span>{{ status }}</span><span v-if="typeof output.exit_code === 'number'">退出码 {{ output.exit_code }}</span></div><p v-if="output.output_truncated || output.has_more || output.output_complete === false" class="unavailable">当前输出不完整，仅展示已返回内容。</p><pre v-if="outputText(event)" :class="{ 'terminal-pre': isBash }">{{ outputText(event) }}</pre><p v-else class="unavailable">工具未返回文本内容。</p><details class="raw-output"><summary>原始响应</summary><pre>{{ jsonText(event.output) }}</pre></details></template>
          <p v-else class="unavailable">输出：{{ availability(event.output_state) }}</p>
        </div>
        <div class="tool-technical-name">{{ event.name || '工具' }}</div>
      </div>
    </details>
  </section>
</template>
<style scoped>
.tool-status[data-task-state]{color:var(--id-text-secondary);background:var(--id-bg-muted)}
.tool-status[data-task-state="running"]{color:var(--id-accent-primary);background:var(--id-accent-primary-soft)}
.tool-status[data-task-state="failed"],.tool-status[data-task-state="blocked"]{color:var(--id-status-danger);background:var(--id-status-danger-soft)}
.tool-status[data-task-state="completed"]{color:var(--id-status-success);background:var(--id-status-success-soft)}
.tool-card{border:1px solid var(--id-border-subtle);border-radius:var(--id-radius-lg);background:var(--id-bg-surface);overflow:hidden;font-size:12px;min-width:0;transition:border-color var(--id-transition-fast)}
.tool-card:not(.task-action):hover{border-color:var(--id-border-strong)}
.tool-details>summary{list-style:none;display:flex;align-items:center;gap:8px;padding:7px 11px;min-height:34px;cursor:pointer;transition:background-color var(--id-transition-fast)}
.tool-details>summary:hover{background:var(--id-bg-subtle)}
.tool-details>summary::-webkit-details-marker{display:none}
.tool-symbol{display:grid;place-items:center;flex-shrink:0;width:20px;height:20px;border-radius:5px;background:var(--id-status-success-soft);color:var(--id-status-success);font-size:12px;font-weight:700}
.tool-heading{display:flex;flex:1;min-width:0;flex-direction:row;align-items:center;gap:8px}
.tool-heading strong{font-size:12px;font-weight:500;color:var(--id-text-primary);white-space:nowrap;flex-shrink:0}
.tool-target{max-width:100%;overflow:hidden;text-overflow:ellipsis;white-space:nowrap;color:var(--id-text-muted);font-family:var(--id-font-mono);font-size:11px;padding:1px 6px;border-radius:var(--id-radius-sm);background:var(--id-bg-subtle);border:1px solid var(--id-border-hairline)}
.tool-target.bash-cmd{background:var(--id-bg-terminal-header);color:var(--id-text-terminal);border-color:var(--id-border-terminal)}
.tool-target.bash-cmd::before{content:"$ ";opacity:0.65}
.tool-status{font-size:11px;font-weight:500;color:var(--id-status-success);background:var(--id-status-success-soft);padding:1px 7px;border-radius:var(--id-radius-pill);flex-shrink:0}
.tool-duration{font-size:11px;font-family:var(--id-font-mono);font-variant-numeric:tabular-nums;color:var(--id-text-muted);white-space:nowrap;flex-shrink:0}
.chevron{color:var(--id-text-muted);transition:transform var(--id-transition-fast);flex-shrink:0}
.tool-details[open]>summary .chevron{transform:rotate(180deg)}
.pending:not(.failed) .tool-status{color:var(--id-text-secondary);background:var(--id-bg-muted)}
.pending:not(.failed) .tool-symbol{background:var(--id-bg-muted);color:var(--id-text-muted)}
.failed .tool-status{color:var(--id-status-danger);background:var(--id-status-danger-soft)}
.tool-error{color:var(--id-status-danger)}
.failed .tool-symbol{background:var(--id-status-danger-soft);color:var(--id-status-danger)}
.tool-content{padding:4px 12px 12px;border-top:1px solid var(--id-border-hairline);min-width:0}
.io-tabs{display:flex;gap:18px;border-bottom:1px solid var(--id-border-subtle);margin-bottom:10px}
.io-tabs button{padding:7px 2px;border:0;border-bottom:2px solid transparent;color:var(--id-text-secondary);background:transparent;cursor:pointer;font:inherit;font-weight:500}
.io-tabs button[aria-selected=true]{color:var(--id-accent-primary);border-bottom-color:var(--id-accent-primary)}
pre{white-space:pre-wrap;overflow-wrap:anywhere;max-height:320px;overflow:auto;padding:10px 12px;border-radius:var(--id-radius-md);background:var(--id-bg-muted);border:1px solid var(--id-border-hairline);font:12px/1.65 var(--id-font-mono);margin:8px 0}
pre.terminal-pre{background:var(--id-bg-terminal);color:var(--id-text-terminal);border-color:var(--id-border-terminal)}
.output-meta{display:flex;justify-content:space-between;gap:12px;color:var(--id-text-secondary);font-size:11px;font-family:var(--id-font-mono)}
.field-label,.unavailable,.raw-output,.tool-technical-name{color:var(--id-text-muted);font-size:11px}
.unavailable{margin:8px 0}
.raw-output{margin-top:10px}
.raw-output summary,.task-instruction summary{cursor:pointer}
.tool-technical-name{margin-top:10px;overflow-wrap:anywhere;font-family:var(--id-font-mono)}
dl{display:flex;gap:12px;overflow-wrap:anywhere;font-size:11px}
dt{color:var(--id-text-muted)}
dd{margin:0;font-family:var(--id-font-mono)}
.task-action{border-left:3px solid var(--id-accent-primary);border-radius:var(--id-radius-lg)}
.task-action .tool-details>summary{padding:8px 14px;min-height:32px;color:var(--id-text-secondary)}
.task-action-header{display:flex;align-items:center;gap:10px;padding:12px 14px 6px}
.task-action-header .tool-symbol{background:var(--id-accent-primary-soft);color:var(--id-accent-primary);width:28px;height:28px;border-radius:var(--id-radius-md);font-size:15px}
.action-title{flex:1;min-width:0}
.action-title>span{font-size:11px;color:var(--id-text-muted)}
.action-title strong{display:block;font-size:13px;font-weight:600;color:var(--id-text-primary);overflow-wrap:anywhere}
.task-action-body{padding:0 14px 10px 52px}
.task-action-body>p{color:var(--id-text-secondary);margin:4px 0 6px;font-size:12px;line-height:1.55}
.task-action-body>.tool-error{color:var(--id-status-danger)}
.task-link{display:inline-flex;align-items:center;gap:4px;border:1px solid var(--id-accent-primary-border);border-radius:var(--id-radius-sm);padding:3px 9px;background:var(--id-accent-primary-soft);color:var(--id-accent-primary);font:inherit;font-size:12px;font-weight:500;cursor:pointer;transition:background-color var(--id-transition-fast)}
.task-link:hover{background:var(--id-accent-primary);color:var(--id-text-inverse)}
.task-action .tool-details{border-top:1px solid var(--id-border-hairline);color:var(--id-text-secondary)}
.task-instruction{margin-top:8px;color:var(--id-text-muted);font-size:11px}
.task-instruction p{white-space:pre-wrap;overflow-wrap:anywhere;color:var(--id-text-primary);max-height:300px;overflow:auto}
button:focus-visible,summary:focus-visible{outline:2px solid var(--id-accent-primary);outline-offset:-2px}
</style>

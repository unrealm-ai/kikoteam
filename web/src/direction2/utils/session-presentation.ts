import type { SessionEvent } from '../api/manager';

export function record(value: unknown): Record<string, unknown> {
  return value && typeof value === 'object' && !Array.isArray(value) ? value as Record<string, unknown> : {};
}
export function text(value: unknown): string { return typeof value === 'string' ? value : ''; }
export function eventText(event: SessionEvent): string {
  return event.content?.filter(c => c.type === 'text').map(c => c.text).join('\n') || event.error?.message || event.message || event.summary || '';
}
export function workItemProgress(execution?: { required_actions?: { type: string; message: string }[] } | null): string | undefined {
  return execution?.required_actions?.find(action => action.type === 'work_item')?.message;
}

export function sessionStatus(value?: string): string {
  return ({ engine_recovery_pending: '正在恢复执行环境，等待旧引擎交还执行权', awaiting_confirmation: '等待用户确认', queued: '已排队', started: '处理中', processed: '已处理', canceled: '已取消', rejected: '已拒绝', running: '运行中', stopping: '正在停止', completed: '本轮完成', failed: '失败', interrupted: '已中断', idle: '等待输入', blocked: '受阻', unknown: '结果未知', active: '可继续', archived: '已归档' } as Record<string, string>)[value || ''] || value || '等待状态';
}
export function availability(value?: string): string {
  return ({ pending: '尚未提供', unavailable: '暂不可用', forbidden: '无权查看', redacted: '内容已隐藏', omitted: '内容未提供', truncated: '内容过大，未完整提供' } as Record<string, string>)[value || ''] || '暂不可用';
}
// MCP adapters can wrap structuredContent more than once. Never consult hidden payloads.
export function toolOutput(event: SessionEvent): unknown {
  if (event.output_state !== 'available') return undefined;
  let value = event.output;
  for (let i = 0; i < 5; i++) {
    const object = record(value);
    if (object.structuredContent !== undefined) { value = object.structuredContent; continue; }
    const content = object.content;
    if (Array.isArray(content) && content.length === 1 && record(content[0]).type === 'text') {
      const body = text(record(content[0]).text);
      try { value = JSON.parse(body); continue; } catch { return body; }
    }
    break;
  }
  return value;
}
const taskActions: Record<string, string> = {
  task_delegation_create_session: '触发任务', task_delegation_list_targets: '查看可用员工',
  task_delegation_list_sessions: '查看任务列表', task_delegation_get_session: '查看任务进展',
  task_delegation_send_message: '补充任务要求', task_delegation_stop_session: '停止任务',
};
export function taskAction(event: SessionEvent): string { return taskActions[event.name || ''] || ''; }
export function toolName(event: SessionEvent): string {
  return taskAction(event) || ({ read: '读取文件', write: '写入文件', edit: '编辑文件', ls: '浏览目录', find: '查找文件', grep: '搜索内容', bash_start: '执行命令', bash_poll: '查看命令输出', bash_write: '输入命令内容', bash_stop: '停止命令', bash_list: '查看后台进程' } as Record<string, string>)[event.name || ''] || event.name || '工具调用';
}
export function toolFailed(event: SessionEvent): boolean {
  if (event.output_state === 'available') {
    let value = event.output;
    for (let i = 0; i < 5; i++) {
      const wrapper = record(value);
      if (wrapper.isError === true || wrapper.error) return true;
      if (wrapper.structuredContent === undefined) break;
      value = wrapper.structuredContent;
    }
  }
  const output = record(toolOutput(event));
  return event.status === 'failed' || !!event.error || output.isError === true || !!output.error || (typeof output.exit_code === 'number' && output.exit_code !== 0);
}
export function toolStatus(event: SessionEvent): string {
  if (toolFailed(event)) return '执行失败';
  if (event.status !== 'completed') return sessionStatus(event.status);
  if (event.output_state !== 'available') return '调用结束';
  const output = record(toolOutput(event));
  if (event.name === 'task_delegation_create_session') return text(output.session_id) ? '已触发' : '调用结束';
  if (['task_delegation_send_message', 'task_delegation_stop_session'].includes(event.name || '')) return '已受理';
  if (output.state === 'running') return '后台运行中';
  return '执行成功';
}
export function toolTarget(event: SessionEvent): string {
  if (event.input_state !== 'available') return '';
  const input = record(event.input);
  return text(input.title) || text(input.path) || text(input.file_path) || text(input.command).split('\n')[0] || text(input.pattern);
}
export function duration(event: SessionEvent): string {
  const start = Date.parse(text(event.started_at)), end = Date.parse(text(event.finished_at));
  if (!Number.isFinite(start) || !Number.isFinite(end) || end < start) return '';
  const ms = end - start;
  return ms < 1000 ? `${ms} ms` : `${(ms / 1000).toFixed(1)} s`;
}
export function jsonText(value: unknown): string { return typeof value === 'string' ? value : JSON.stringify(value, null, 2) ?? ''; }
export function outputText(event: SessionEvent): string {
  const output = toolOutput(event), object = record(output);
  if (Array.isArray(object.chunks)) return object.chunks.map(c => `${record(c).stream === 'stderr' ? '[stderr] ' : ''}${text(record(c).text)}`).join('');
  if (Array.isArray(object.content)) return object.content.filter(c => record(c).type === 'text').map(c => text(record(c).text)).join('\n');
  return jsonText(output);
}
export function taskSessionID(event: SessionEvent): string {
  if (!taskAction(event) || toolFailed(event)) return '';
  const output = record(toolOutput(event));
  return text(output.session_id) || (event.input_state === 'available' ? text(record(event.input).session_id) : '');
}
export function taskReport(event: SessionEvent) {
  if (event.source_kind !== 'task_report') return null;
  const body = eventText(event);
  try {
    const start = body.indexOf('{');
    if (start < 0) return null;
    const value = record(JSON.parse(body.slice(start)));
    if (typeof value.result !== 'string' || typeof value.outcome !== 'string' || typeof value.session_id !== 'string') return null;
    return { sessionID: value.session_id, turnID: text(value.turn_id), outcome: value.outcome, result: value.result, unknown: value.has_unknown_tool_results === true, background: typeof value.background_process_count === 'number' ? value.background_process_count : null, queued: typeof value.queued_input_count === 'number' ? value.queued_input_count : 0 };
  } catch { return null; }
}
export function clockTime(value: string): string {
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? '' : date.toLocaleTimeString('zh-CN', { hour: '2-digit', minute: '2-digit', hour12: false });
}

// Only fold replies whose input and turn identities both match. A new input,
// tool call or unrelated reply ends the group, preserving conversation order.
export function presentationRows(events: SessionEvent[]): { event: SessionEvent; replies: SessionEvent[] }[] {
  const rows: { event: SessionEvent; replies: SessionEvent[] }[] = [];
  let current: { event: SessionEvent; replies: SessionEvent[] } | undefined;
  for (const event of events) {
    const matches = current && event.input_id && event.turn_id && event.input_id === current.event.input_id && event.turn_id === current.event.turn_id;
    if (matches && event.type === 'agent.message' && eventText(event)) { current!.replies.push(event); continue; }
    if (event.type !== 'turn.updated' || !matches) current = undefined;
    const row = { event, replies: [] as SessionEvent[] };
    rows.push(row);
    if (taskReport(event)) current = row;
  }
  return rows;
}

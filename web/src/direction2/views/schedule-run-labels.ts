const reasons: Record<string, string> = {
  missed_while_offline: '调度中断期间错过',
  agent_disabled: '员工已停用',
  task_space_archived: '任务空间已归档',
};
const states: Record<string, string> = {
  queued: '排队中', started: '执行中', running: '执行中', completed: '执行完成',
  failed: '执行失败', rejected: '未能开始执行', canceled: '已取消', interrupted: '执行中断',
};
export function scheduleRunReason(code: string) {
  return reasons[code] ?? (code.includes('archived') ? '来源已归档' : code.includes('revoked') ? '来源或权限已失效' : code);
}
export function scheduleExecutionStatus(code: string) {
  return states[code] ?? code;
}

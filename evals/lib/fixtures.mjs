import { createHash } from 'node:crypto';

export const prefix = 'mcp__task_delegation__';
export const names = Object.freeze({
  get: 'task_delegation_get_session',
  list: 'task_delegation_list_sessions',
  send: 'task_delegation_supplement_task',
  stop: 'task_delegation_stop_session',
  cancel: 'task_delegation_cancel_todo',
  create: 'task_delegation_create_session',
  work: 'work_item_get_current',
  artifact: 'artifact_select',
});
export const writeNames = new Set([
  names.send, names.stop, names.cancel, names.create, names.artifact, 'send_task_detail_link',
  'task_delegation_create_todo', 'task_delegation_start_todo', 'task_delegation_supplement_todo',
  'work_item_amend_requirements',
]);
export function idFor(c, tag) {
  const s = createHash('sha256').update(c.id + ':' + tag).digest('hex');
  return [s.slice(0, 8), s.slice(8, 12), '4' + s.slice(13, 16), '8' + s.slice(17, 20), s.slice(20, 32)].join('-');
}
export function ids(c) {
  return Object.fromEntries(
    ['task', 'session', 'turn', 'input', 'new_input', 'new_turn', 'artifact'].map(x => [x + '_id', idFor(c, x.replace('_', '-'))]),
  );
}
export const reject = (code, message, recovery) => ({
  error: { code, message }, ...(recovery ? { recovery } : {}),
});

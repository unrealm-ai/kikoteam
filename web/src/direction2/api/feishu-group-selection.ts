import { platformRequest, post } from './platform';
import type { SelectionCandidate } from './selectors';

type GroupPage = {
  source: 'provider' | 'observed';
  items: { selection?: SelectionCandidate; selection_token?: string }[];
  next_cursor: string;
};
function groups(workspace: string, employee: string, signal: AbortSignal, body: { cursor?: string; selection_token?: string }) {
  return platformRequest<GroupPage>(`/workspaces/${encodeURIComponent(workspace)}/agents/${encodeURIComponent(employee)}/channels/feishu/groups/discover`, { ...post(body), signal });
}
export async function loadLiveFeishuGroups(workspace: string, employee: string, cursor: string, signal: AbortSignal) {
  const page = await groups(workspace, employee, signal, { cursor });
  if (!['provider', 'observed'].includes(page.source) || !Array.isArray(page.items)) throw new Error('飞书群列表响应无效');
  const rows = page.items.flatMap(row => row.selection && row.selection_token ? [{ ...row.selection, selection_token: row.selection_token }] : []);
  return { source: page.source, rows, cursor: page.next_cursor || '' };
}
export async function selectLiveFeishuGroup(workspace: string, employee: string, token: string, signal: AbortSignal) {
  const page = await groups(workspace, employee, signal, { selection_token: token });
  const result = page.items[0]?.selection;
  if (!result?.selectable || !result.ref.id) throw new Error('飞书群候选已失效，请重新刷新');
  return result;
}
export function filterLiveGroupNames(rows: SelectionCandidate[], query: string) {
  const needle = query.trim().toLowerCase();
  return needle ? rows.filter(row => (row.name ?? '').toLowerCase().includes(needle) || row.ref.external_id.toLowerCase().includes(needle)) : rows;
}

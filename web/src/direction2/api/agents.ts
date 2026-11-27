import type { SelectionCandidate, SelectionRef } from './selectors';
import type { PlatformType } from '../types';
import { platformRequest, post } from './platform';
export interface AgentConfiguration {
  name: string; role: string; avatar: string; instruction: string; model_profile_id?: string; platform_model_id?: string;
}
export interface Agent extends AgentConfiguration {
  model_profile_id: string;
  can_manage?: boolean;
  can_chat?: boolean;
  payer?: {id: string; name: string};
  web_access?:{mode:'workspace'|'allowlist';allow_member_ids:string[];deny_member_ids:string[]};
  template_origin?:{template_id:string;template_version_id:string;parameters:Record<string,unknown>};
  id: string; model_name: string; model_available: boolean; enabled: boolean; version: number;
  config_version: number; admission_epoch: number; harness: 'pi'; created_at: string; updated_at: string;
}
export interface AgentStatusCounts { enabled: number; disabled: number }
export interface AgentDirectoryFilter { search?: string; enabled?: boolean }
interface AgentListItem extends Agent {
  card_resources: { channels: PlatformType[]; package_count: number; trigger_count: number };
}
interface AgentPage {
  items: AgentListItem[]; total: number; page: number; page_size: number; status_counts: AgentStatusCounts;
}
const base = (workspace: string) => `/workspaces/${encodeURIComponent(workspace)}/agents`;
const path = (workspace: string, id: string) => `${base(workspace)}/${encodeURIComponent(id)}`;
export interface EmployeeMaintainers { version: number; people: SelectionCandidate[] }
export const agentsApi = {
  maintainers: (workspace: string, id: string, signal: AbortSignal) => platformRequest<EmployeeMaintainers>(`${path(workspace, id)}/maintainers`, {signal}),
  saveMaintainers: (workspace: string, id: string, version: number, people: SelectionRef[], signal: AbortSignal) => platformRequest<EmployeeMaintainers>(`${path(workspace, id)}/maintainers`, {method:'PUT',body:JSON.stringify({version,people}),signal}),
  list(workspace: string, page = 1, signal?: AbortSignal, filter: AgentDirectoryFilter = {}) {
    const query = new URLSearchParams({page: String(page), page_size: '20'});
    if (filter.search) query.set('q', filter.search);
    if (filter.enabled !== undefined) query.set('enabled', String(filter.enabled));
    return platformRequest<AgentPage>(`${base(workspace)}?${query}`, signal ? {signal} : {});
  },
  get: (workspace: string, id: string, signal?: AbortSignal) => platformRequest<Agent>(path(workspace, id), { ...(signal ? { signal } : {}) }),
  create: (workspace: string, config: AgentConfiguration) => platformRequest<Agent>(base(workspace), post(config)),
  update: (workspace: string, agent: Agent, config: Partial<AgentConfiguration> & { confirm_platform_billing?: boolean }) => platformRequest<Agent>(path(workspace, agent.id), { method: 'PATCH', body: JSON.stringify({ ...config, version: agent.version }) }),
  setEnabled: (workspace: string, agent: Agent) => platformRequest<Agent>(path(workspace, agent.id), { method: 'PATCH', body: JSON.stringify({ enabled: !agent.enabled, version: agent.version }) })
};

import { platformRequest, post } from './platform';
import { workspacePath, type Skill, type Plugin, type Page } from './capabilities';

export type Kind = 'skill' | 'plugin';
export type Item = Skill | Plugin;
export const versionOf = (item: Item) => 'current_version_id' in item ? item.current_version_id : item.version_id;

type Target = {
  agent_id: string;
  package_id: string;
  package_version: number;
};
export type Use = {
  agent_id: string;
  agent_name: string;
  package_id: string;
  package_name: string;
  version_id: string;
  enabled: boolean;
  account_ready: boolean;
};
type Result = {agent_id: string; package_id: string; status: string};

const resource = (workspace: string, kind: Kind, id: string) =>
  `${workspacePath(workspace)}/${kind}s/${encodeURIComponent(id)}`;
const official = (kind: Kind) => `/official-${kind}s`;
const catalogPost = (body: unknown) => ({...post(body), signal: AbortSignal.timeout(65000)});

export const catalogApi = {
  uses(workspace: string, kind: Kind, id: string, page: number) {
    return platformRequest<Page<Use>>(`${resource(workspace, kind, id)}/agents?page=${page}&page_size=20`);
  },
  assign(workspace: string, kind: Kind, id: string, version_id: string, targets: Target[]) {
    return platformRequest<{items: Result[]}>(`${resource(workspace, kind, id)}/assign`, catalogPost({version_id, targets}));
  },
  copy(workspace: string, kind: Kind, id: string, version_id: string) {
    return platformRequest<Item>(`${resource(workspace, kind, id)}/copy`, catalogPost({version_id}));
  },
  archive(workspace: string, kind: Kind, item: Item) {
    return platformRequest<void>(resource(workspace, kind, item.id), {
      method: 'DELETE', body: JSON.stringify({version: item.version})
    });
  },
  publish(workspace: string, kind: Kind, item: Item, version_id: string, target?: Item, oauth?:import('./capabilities').OAuthConfig) {
    const path = official(kind) + (target ? `/${target.id}/versions` : '');
    return platformRequest<Item>(path, catalogPost({
      ...(oauth?{oauth}:{}),source_workspace_id: workspace, resource_id: item.id, version_id, ...(target ? {version: target.version} : {})
    }));
  },
  officials(kind: Kind, page: number) {
    return platformRequest<Page<Item>>(`${official(kind)}?page=${page}&page_size=100`);
  },
  state(kind: Kind, item: Item, change: {listed?: boolean; enabled?: boolean}) {
    return platformRequest<Item>(`${official(kind)}/${item.id}`, {
      method: 'PATCH', body: JSON.stringify({version: item.version, ...change})
    });
  }
};

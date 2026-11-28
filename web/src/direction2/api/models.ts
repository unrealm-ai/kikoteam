import { platformRequest, post } from './platform';
import type { ModelProtocol } from '../types';

export interface ModelPrices {
  input: string; output: string; cache_read: string | null; cache_write: string | null;
}
export interface ModelProfile {
  id: string; name: string; description: string; protocol: ModelProtocol;
  base_url: string; model_id: string; version: number; has_key: boolean;
  vision_status: 'passed' | 'unsupported' | 'unknown'; enabled: boolean;
  is_default: boolean; tested_at: string; validated?: boolean;
}
export type ModelChoice = (ModelProfile & {source?: 'workspace'; prices?: undefined}) | {
  id: string; name: string; protocol: ModelProtocol; source: 'platform';
  enabled: boolean; validated: boolean; is_default: boolean; prices: ModelPrices;
};
export const choiceValue = (m: ModelChoice) => `${m.source ?? 'workspace'}:${m.id}`;
export function modelSelection(value: string) {
  const [source, id] = value.split(':');
  return source === 'platform' ? { platform_model_id: id! } : { model_profile_id: id! };
}
export function modelOptions(models: ModelChoice[]) {
  return (['platform', 'workspace'] as const).flatMap(source => {
    const children = models.filter(m => (m.source ?? 'workspace') === source).map(m => ({
      label: `${m.name}${m.is_default ? '（默认）' : ''}${m.prices ? ` · 输入 ${m.prices.input} / 输出 ${m.prices.output} 积分 / 百万 Token` : ''}`,
      value: choiceValue(m), disabled: !m.enabled || !m.validated
    }));
    return children.length ? [{ type: 'group' as const, key: source, label: source === 'platform' ? '官方模型' : '自备模型', children }] : [];
  });
}
export interface ModelConfiguration {
  name: string; description: string; protocol: ModelProtocol;
  base_url: string; model_id: string; api_key?: string;
}
export interface ModelCaseResult {
  status: 'passed' | 'failed' | 'unsupported' | 'unknown';
  output: string; issue?: string; duration_ms: number; http_status?: number;
}
export interface ModelTestResult {
  receipt: string; expires_at: string; text: ModelCaseResult; image: ModelCaseResult;
}
const base = (workspace: string) => `/workspaces/${encodeURIComponent(workspace)}/model-profiles`;
const resource = (workspace: string, id: string) => `${base(workspace)}/${encodeURIComponent(id)}`;
export const modelProfilesApi = {
  choices: (workspace: string, page = 1, signal?: AbortSignal) => platformRequest<{ items: ModelChoice[]; total: number; page: number; page_size: number }>(
    `${base(workspace)}?include_platform=true&page=${page}&page_size=20`, signal ? { signal } : {}
  ),
  list: (workspace: string, page = 1, signal?: AbortSignal) => platformRequest<{ items: ModelProfile[]; total: number; page: number; page_size: number }>(`${base(workspace)}?page=${page}&page_size=20`, { ...(signal ? { signal } : {}) }),
  test: (workspace: string, config: ModelConfiguration, existing?: { id: string; version: number }, signal?: AbortSignal) => platformRequest<ModelTestResult>(`${base(workspace)}/test`, {
    ...post({ ...config, ...(existing ? { model_profile_id: existing.id, version: existing.version } : {}) }),
    signal: signal ? AbortSignal.any([signal, AbortSignal.timeout(50000)]) : AbortSignal.timeout(50000)
  }),
  save: (workspace: string, config: ModelConfiguration, receipt: string, existing?: { id: string; version: number }) => platformRequest<ModelProfile>(existing ? resource(workspace, existing.id) : base(workspace), {
    method: existing ? 'PATCH' : 'POST', body: JSON.stringify({ ...config, test_receipt: receipt, ...(existing ? { version: existing.version } : {}) })
  }),
  status: (workspace: string, profile: ModelProfile) => platformRequest<ModelProfile>(resource(workspace, profile.id), { method: 'PATCH', body: JSON.stringify({ version: profile.version, enabled: !profile.enabled }) }),
  setDefault: (workspace: string, profile: ModelProfile) => platformRequest<ModelProfile>(`${resource(workspace, profile.id)}/set-default`, post({ version: profile.version })),
  revokeKey: (workspace: string, profile: ModelProfile) => platformRequest<ModelProfile>(`${resource(workspace, profile.id)}/revoke-key`, post({ version: profile.version }))
};

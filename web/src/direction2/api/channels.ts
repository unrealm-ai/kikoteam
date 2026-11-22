import { platformRequest } from './platform';
import type { FeishuBinding, FeishuConfiguration } from './feishu';
export type ChannelProvider = 'feishu' | 'dingtalk' | 'wecom';
export const channelLabels: Record<ChannelProvider, string> = { feishu: '飞书', dingtalk: '钉钉', wecom: '企业微信' };
const path = (w: string, a: string, provider: ChannelProvider) => `/workspaces/${encodeURIComponent(w)}/agents/${encodeURIComponent(a)}/channels/${provider}`;
export const channelsApi = {
  get: (w: string, a: string, provider: ChannelProvider, signal: AbortSignal) => platformRequest<FeishuBinding>(path(w, a, provider), { signal }),
  rebindImpact: (w: string, a: string, provider: ChannelProvider, signal: AbortSignal) => platformRequest<FeishuBinding>(`${path(w, a, provider)}?include_rebind_impact=true`, { signal }),
  save: (w: string, a: string, provider: ChannelProvider, body: Pick<FeishuConfiguration, 'version'|'app_id'|'app_secret'|'enabled'> & { test_receipt?: string; confirmed_impact?: boolean }, signal: AbortSignal) => platformRequest<FeishuBinding>(path(w, a, provider), { method: 'PUT', body: JSON.stringify(body), signal })
};

import type { Plugin } from './capabilities';
import type { Item } from './resource-catalog';

export type SummaryLanguage = 'zh' | 'en';

export const categories: Record<string, string> = {
  office: '办公协作', market: '行情与数据', dev: '工程研发', research: '信息研究',
  sales: '销售服务', editor: '内容运营'
};

export function resourceSummary(item: Item, language: SummaryLanguage) {
  return (language === 'en' ? item.card?.summary_en : item.card?.summary_zh) || item.description;
}

export function pluginConnectionLabel(item: Plugin) {
  if (item.auth_type === 'none') return '免认证';
  switch (item.shared_connection_state) {
    case 'connected': return '空间共享已连接';
    case 'missing': return '空间共享待连接';
    case 'revoked': return '共享连接已撤销';
    case 'unvalidated': return '共享连接待验证';
    default: return '需要认证';
  }
}

export function pluginAuthLabel(item: Plugin) {
  const labels = {none: '免认证', bearer: 'Bearer Token', api_key: 'API Key', oauth: 'OAuth 2.0'};
  const methods = item.supported_auth_methods ?? [item.auth_type];
  return methods.map(method => method === 'bearer' && methods.length > 1 ? '个人访问令牌（PAT）' : labels[method]).join(' / ');
}

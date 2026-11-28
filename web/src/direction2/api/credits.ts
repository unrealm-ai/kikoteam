import { platformRequest, post } from './platform';

export interface Usage {
  id: string;
  agent_id: string;
  agent_name: string;
  model_id: string;
  price_snapshot: {model_name: string};
  input_tokens: number | null;
  output_tokens: number | null;
  cache_read_tokens: number | null;
  cache_write_tokens: number | null;
  status: 'running' | 'usage_recorded' | 'settled' | 'pending_review' | 'no_charge';
  charge: string | null;
  started_at: string;
  upstream_request_id: string | null;
}
export interface Ledger {
  id: string;
  kind: 'signup_bonus' | 'referral_bonus' | 'admin_grant' | 'model_charge' | 'image_charge';
  amount: string;
  balance_after: string;
  reason: string | null;
  created_at: string;
  usage?: Usage;
  image?: {generation_id:string;agent_id:string;agent_name:string;image_count:number;credits_per_image:string;started_at:string};
}
interface CreditPage<T> {
  items: T[];
  next_cursor: string;
  summary?: {spent: string; granted: string};
}
export interface Settings { signup_bonus: string; version: number }
export interface UsageTokens {
  input_tokens: number;
  output_tokens: number;
  cache_read_tokens: number;
  cache_write_tokens: number;
}
export const creditsApi = {
  wallet: (id: string, signal?: AbortSignal) => platformRequest<{balance: string}>(`/accounts/${id}/credit-wallet`, signal ? {signal} : {}),
  ledger: (id: string, query: URLSearchParams, signal?: AbortSignal) =>
    platformRequest<CreditPage<Ledger>>(`/accounts/${id}/credit-ledger?${query}`, signal ? {signal} : {}),
  usage: (id: string, query: URLSearchParams, signal?: AbortSignal) =>
    platformRequest<CreditPage<Usage>>(`/model-usage?${new URLSearchParams({...Object.fromEntries(query), account_id: id})}`, signal ? {signal} : {}),
  grant: (id: string, amount: string, reason: string, key: string) =>
    platformRequest<Ledger>(`/accounts/${id}/credit-grants`, {...post({amount, reason}), headers: {'Idempotency-Key': key}}),
  settings: () => platformRequest<Settings>('/credit-settings'),
  saveSettings: (settings: Settings) =>
    platformRequest<Settings>('/credit-settings', {method: 'PATCH', body: JSON.stringify(settings)}),
  resolve: (id: string, reason: string, usage?: UsageTokens) =>
    platformRequest<null>(`/model-usage/${id}/resolve`, post(usage ? {action:'settle', reason, usage} : {action:'no_charge', reason}))
};

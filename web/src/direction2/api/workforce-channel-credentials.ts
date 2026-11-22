import { channelsApi, type ChannelProvider } from './channels';
import type { FeishuBinding } from './feishu';
import { platformRequest } from './platform';

/** Credential-only transport for the original Workforce forms. It does not
 * emulate the prototype's test receipt, unbind, or employee-wide save. */
export interface WorkforceChannelCredentials {
  appId?: string;
  appSecret?: string;
  appKey?: string;
  botId?: string;
  botSecret?: string;
}
export function channelCredentialPayload(provider: ChannelProvider, credentials: WorkforceChannelCredentials) {
  const app_id = (provider === 'wecom' ? credentials.botId : provider === 'dingtalk' ? credentials.appKey : credentials.appId)?.trim() || '';
  const app_secret = (provider === 'wecom' ? credentials.botSecret : credentials.appSecret)?.trim() || '';
  if (!app_id || !app_secret || app_secret.includes('•') || /^\*{4,}$/.test(app_secret)) {
    throw new Error('请填写完整的渠道 ID 和 Secret，不能使用脱敏占位符');
  }
  return { app_id, app_secret };
}

export function createWorkforceChannelCredentialTransport(api: Pick<typeof channelsApi, 'save'> = channelsApi) {
  return {
    async replace(workspaceId: string, employeeId: string, provider: ChannelProvider,
      current: FeishuBinding, credentials: WorkforceChannelCredentials, signal: AbortSignal, testReceipt?: string): Promise<FeishuBinding> {
      // Use the displayed binding's own revision, not the employee version or a
      // freshly fetched revision: stale forms must fail instead of overwriting.
      if (current.agent_id !== employeeId || current.provider !== provider) throw new Error('渠道绑定与当前员工不匹配');
      return api.save(workspaceId, employeeId, provider, {
        ...channelCredentialPayload(provider, credentials),
        version: current.config_revision,
        enabled: current.enabled,
        ...(testReceipt ? { test_receipt: testReceipt } : {})
      }, signal);
    }
  };
}

export interface WorkforceCredentialTest {
  credential_status: 'verified' | 'format_only';
  receipt?: string; tested_at: string; expires_at?: string;
}
export const workforceCredentialTest = (workspaceId: string, employeeId: string, provider: ChannelProvider,
  version: number, credentials: WorkforceChannelCredentials, signal: AbortSignal) =>
  platformRequest<WorkforceCredentialTest>(`/workspaces/${encodeURIComponent(workspaceId)}/agents/${encodeURIComponent(employeeId)}/channels/${provider}/test-connection`, {
    method: 'POST', body: JSON.stringify({ version, ...channelCredentialPayload(provider, credentials) }), signal
  });

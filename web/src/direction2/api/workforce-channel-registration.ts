import type { ChannelQrRegistrationSession, EmployeeChannelBinding, PlatformType } from '../types';
import { feishuAuthorizationUrl, feishuVerificationURL, feishuPermissionReadiness, feishuRegistrationFailure, type FeishuRegistration } from './feishu-setup';
import { dingTalkVerificationURL, type DingTalkRegistration } from './dingtalk-setup';

/** Project provider state into the original channel component without treating
 * credential storage as a live connection or a full permission grant. */
export function registrationForWorkforce(workspaceId: string, employeeId: string,
  provider: Exclude<PlatformType, 'wecom'>, value: FeishuRegistration | DingTalkRegistration,
  binding?: EmployeeChannelBinding, now = Date.now()): ChannelQrRegistrationSession {
  const connected = binding?.status !== 'disabled' && binding?.connectionState === 'connected';
  const permissionReady = provider === 'feishu'
    ? feishuPermissionReadiness((value as FeishuRegistration).diagnostics)
    : binding?.permissionReady;
  const feishu = provider === 'feishu' ? value as FeishuRegistration : undefined;
  const failure = feishuRegistrationFailure(feishu);
  const authorizationUrl = feishu && feishuAuthorizationUrl(feishu.app_id,
    feishu.diagnostics?.permissions?.find(permission => permission.required && permission.status === 'missing')?.scope);
  const done = provider === 'feishu' ? value.state === 'connected' : value.state === 'consumed';
  const expiresAt = Date.parse(value.expires_at);
  // A restarted backend can keep reporting an interrupted attempt after its
  // QR expires. Credentials already acquired have a separate lifecycle.
  const state = value.state === 'interrupted' && !value.credential_saved && expiresAt <= now
    ? 'expired' : value.state;
  const ended = ['expired', 'cancelled', 'denied', 'interrupted', 'failed'].includes(state);
  const status: ChannelQrRegistrationSession['status'] = done && binding ? 'authorized'
    : ended ? 'expired' : value.credential_saved || value.state === 'ready' || value.state === 'checking' ? 'scanned_confirming' : 'waiting_scan';
  const verificationUrl = status === 'waiting_scan'
    ? (provider === 'feishu' ? feishuVerificationURL(value.verification_url) : dingTalkVerificationURL(value.verification_url)) || ''
    : '';
  return {
    sessionId: value.id, workspaceId, employeeId, platform: provider,
    qrPayload: verificationUrl, qrSvgDataUrl: '',
    verificationUrl, registrationState: state, registrationError: value.error_code,
    ...(failure ? {feishuFailure: failure} : {}),
    restartNeedsReview: state === 'interrupted' || (ended && state !== 'expired' && !!value.app_id && !value.credential_saved),
    ...(authorizationUrl ? {authorizationUrl} : {}),
    expiresInSeconds: Math.max(0, Math.ceil((expiresAt - now) / 1000)) || 0,
    status, qrStatus: status, credentialSaved: value.credential_saved,
    permissionReady, permissionStatus: permissionReady ? 'authorized' : 'unknown',
    connectionState: connected ? 'connected' : 'pending',
    ...(binding ? { binding } : {})
  };
}

import { feishuSetupApi } from './feishu-setup';
import { dingTalkSetupApi } from './dingtalk-setup';

export function createWorkforceRegistrationTransport(options: {
  workspace: () => string;
  binding: (workspace: string, employee: string, provider: Exclude<PlatformType,'wecom'>) => Promise<EmployeeChannelBinding | undefined>;
}) {
  type Context = { workspace: string; employee: string; provider: Exclude<PlatformType,'wecom'> };
  const contexts = new Map<string, Context>();
  const creationKeys = new Map<string, string>();
  const confirmations = new Set<string>();
  function providerOnly(provider: PlatformType): asserts provider is Exclude<PlatformType,'wecom'> {
    if (provider === 'wecom') throw new Error('企业微信通过官方授权窗口接入');
  }
  const signal = () => AbortSignal.timeout(75000);
  async function project(context: Context, registration: FeishuRegistration | DingTalkRegistration | null) {
    if (!registration) return null;
    contexts.set(registration.id, context);
    const complete = registration.state === 'connected' || registration.state === 'consumed';
    const binding = complete ? await options.binding(context.workspace, context.employee, context.provider) : undefined;
    return registrationForWorkforce(context.workspace, context.employee, context.provider, registration, binding);
  }
  function contextFor(id: string) {
    const context = contexts.get(id);
    if (!context || context.workspace !== options.workspace()) throw new Error('扫码会话已变更，请重新读取当前员工状态');
    return context;
  }
  return {
    async getLatestChannelQrRegistration(employee: string, provider: PlatformType) {
      providerOnly(provider); const context = { workspace: options.workspace(), employee, provider };
      const value = await (provider === 'feishu' ? feishuSetupApi : dingTalkSetupApi).get(context.workspace, employee, signal());
      return {session: await project(context, value.registration)};
    },
    async startChannelQrRegistration(employee: string, provider: PlatformType, workspace = options.workspace(), replaceId?: string) {
      providerOnly(provider); const context = { workspace: options.workspace(), employee, provider };
      if (workspace !== context.workspace) throw new Error('工作区已变更，请重新读取接入状态');
      if (replaceId) {
        const old = contextFor(replaceId);
        if (provider !== 'feishu' || old.provider !== provider || old.employee !== employee) throw new Error('扫码登记已更换，请重新读取');
      }
      const scope = JSON.stringify([context.workspace,employee,provider,replaceId]);
      let key = creationKeys.get(scope);
      if (!key) { key = crypto.randomUUID(); creationKeys.set(scope,key); }
      const value = provider === 'feishu'
        ? await feishuSetupApi.create(context.workspace,employee,key,signal(),replaceId)
        : await dingTalkSetupApi.create(context.workspace,employee,key,signal());
      const session = await project(context,value.registration);
      if (!session) throw new Error('服务端未返回扫码登记');
      // Only a known completed create can consume the client idempotency key.
      creationKeys.delete(scope); return session;
    },
    async pollChannelQrRegistration(id: string) {
      const context = contextFor(id); let registration: FeishuRegistration | DingTalkRegistration | null;
      if (context.provider === 'feishu') {
        registration = (await feishuSetupApi.get(context.workspace,context.employee,signal())).registration;
      } else {
        registration = (await dingTalkSetupApi.action(context.workspace,context.employee,id,'poll',signal())).registration;
        if (registration?.state === 'ready' && !confirmations.has(id) && !sessionStorage.getItem(`kiko-dingtalk-confirm:${context.workspace}:${context.employee}:${id}`)) {
          confirmations.add(id);
          sessionStorage.setItem(`kiko-dingtalk-confirm:${context.workspace}:${context.employee}:${id}`, 'attempted');
          registration = (await dingTalkSetupApi.action(context.workspace,context.employee,id,'confirm',signal())).registration;
        }
      }
      if (registration?.id !== id) throw new Error('扫码登记已更换，请重新读取');
      const session = await project(context,registration);
      if (!session) throw new Error('扫码登记不存在');
      return session;
    },
    async confirmChannelQrRegistration(id: string) {
      const context = contextFor(id);
      let registration:FeishuRegistration|DingTalkRegistration|null;
      if (context.provider === 'feishu') registration=(await feishuSetupApi.action(context.workspace,context.employee,id,'retry',signal())).registration;
      else {
        // A lost confirmation response is not permission to create/confirm again.
        // Re-read the durable result first, including after refresh or remount.
        registration=(await dingTalkSetupApi.get(context.workspace,context.employee,signal())).registration;
        if(registration?.id!==id)throw new Error('扫码登记已更换，请重新读取');
        if(registration.state==='ready') registration=(await dingTalkSetupApi.action(context.workspace,context.employee,id,'confirm',signal())).registration;
        else if(registration.state!=='consumed')throw new Error('授权结果尚未确定，请继续查询当前登记，不要重复创建应用');
      }
      const session = await project(context,registration);
      if (!session) throw new Error('扫码登记不存在');
      return session;
    },
    async cancelChannelQrRegistration(id: string) {
      const context = contextFor(id);
      return context.provider === 'feishu'
        ? feishuSetupApi.action(context.workspace,context.employee,id,'cancel',signal())
        : dingTalkSetupApi.action(context.workspace,context.employee,id,'cancel',signal());
    }
  };
}

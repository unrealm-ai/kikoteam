import type { ChannelBusinessCapability, EmployeeChannelBinding } from '../types';
import type { FeishuBinding } from './feishu';
import {feishuAuthorizationUrl, feishuPermissionReadiness, type FeishuDiagnostics, type FeishuPermissionGroup} from './feishu-setup';
import type { UsageResource } from './agent-usage';
import { channelLabels, type ChannelProvider } from './channels';

const channelErrors: Record<string, string> = {
  connection_failed: '长连接建立失败，系统会自动重试。',
  wecom_connection_failed: '无法连接企微服务器，系统会自动重试。',
  wecom_authentication_failed: '企微长连接认证失败，请核对 BotID 和 Secret。',
  wecom_status_update_failed: '企微已返回认证回执，但连接状态同步失败，系统会自动重试。',
};

export function bindingForWorkforce(binding: FeishuBinding, usage?: UsageResource, diagnostics: FeishuDiagnostics | undefined = binding.permission_diagnostics, groups: FeishuPermissionGroup[] | undefined = binding.permission_groups): EmployeeChannelBinding {
  const provider = binding.provider as ChannelProvider;
  if (!['feishu', 'wecom', 'dingtalk'].includes(provider)) throw new Error('未知渠道类型');
  const applied = binding.applied?.connection_generation === binding.connection_generation
    && binding.applied.config_revision === binding.config_revision ? binding.applied : undefined;
  const connected = binding.enabled && applied?.status === 'connected';
  const failed = binding.enabled && applied?.status === 'failed';
  const needsCredentials = applied?.error_code === 'wecom_authentication_failed';
  const access = usage?.usage_config.access;
  const selected = (side: 'allow'|'deny', kind: 'user'|'department'|'group') => (access?.[side] ?? [])
    .filter(value => value.channel === provider && value.kind === kind && value.binding_id === binding.id && value.identity_epoch === binding.identity_epoch).map(value => value.id);
  const groupMembersEnabled = binding.access?.group_members_enabled ?? access?.group_members_enabled;
  return {
    platform: provider, platformName: channelLabels[provider], connectionId: binding.id,
    status: !binding.enabled ? 'disabled' : connected ? 'connected' : 'pending_config',
    statusText: !binding.enabled ? '已停用' : connected ? '已连接' : needsCredentials ? '认证失败' : failed || applied?.status === 'disconnected' ? '自动重连中' : '正在连接',
    connectionGeneration: binding.connection_generation,
    identityEpoch: binding.identity_epoch,
    configRevision: binding.config_revision,
    credentialStatus: binding.has_secret ? 'saved' : 'unsaved',
    connectionState: connected ? 'connected' : failed ? 'error' : 'disconnected',
    connectionErrorCode: !connected && /^[a-z][a-z0-9_]{0,95}$/.test(applied?.error_code || '') ? applied?.error_code : undefined,
    errorDetail: failed ? channelErrors[applied.error_code || ''] || applied.error_code || '长连接失败' : undefined,
    permissionReady: provider === 'feishu' ? feishuPermissionReadiness(diagnostics) : undefined,
    businessCapabilities:provider==='feishu' ? groups?.map(group => businessCapability(group,binding.app_id)) : undefined,
    identityConfirmed: binding.identity_confirmed,
    directoryStatus: binding.directory_status,
    appId: provider === 'feishu' ? binding.app_id : '', appKey: provider === 'dingtalk' ? binding.app_id : '', botId: provider === 'wecom' ? binding.app_id : '',
    lastVerifiedAt: diagnostics?.checked_at || '',
    usageScope: {
      mode: (binding.access?.mode ?? access?.mode) === 'allowlist' ? 'whitelist' : 'all',
      ...(groupMembersEnabled === undefined ? {} : {groupMembersEnabled}),
      whitelistMembers: binding.access?.allow_ids ?? selected('allow','user'),
      whitelistDepartments: binding.access?.allow_department_ids ?? selected('allow','department'),
      blacklistMembers: binding.access?.deny_ids ?? selected('deny','user'),
      allowedGroupIds: binding.access?.allow_chat_ids ?? selected('allow','group'),
    },
    capabilities: { supportsOrdinaryGroup: provider === 'feishu', supportsKeywords: provider === 'feishu', supportsRecentContext: provider !== 'dingtalk',
      ordinaryGroupEnabled: binding.trigger
        ? Boolean(binding.trigger.keywords?.length || binding.trigger.regexes?.length)
        : Boolean(usage?.usage_config.trigger.keywords.some(x=>x.channels.includes(provider)) || usage?.usage_config.trigger.regexes.some(x=>x.channels.includes(provider))),
      recentContextEnabled: binding.history?.enabled ?? Boolean(usage?.usage_config.history.enabled && usage.usage_config.history.channels.includes(provider)),
      recentContextWindow: binding.history?.limit ?? usage?.usage_config.history.limit },
    sharingRules: [], boundGroups: []
  };
}

function businessCapability(group:FeishuPermissionGroup, appId:string):ChannelBusinessCapability {
  const states=group.scopeStates||[];
  const platformAuthStatus=group.status==='granted'?'authorized':group.status==='missing'?(states.some(p=>p.status==='granted')?'partial':'unauthorized'):'unknown';
  const missing = states.find(p => p.status === 'missing')?.scope;
  const authorizationUrl = feishuAuthorizationUrl(appId, missing);
  return {
    id:group.id,name:group.label,description:group.note||'',platformAuthStatus,
    ...(authorizationUrl ? {authorizationUrl} : {}),
    resourceAccessStatus:'unknown',
    statusDetail:[group.note,'具体资源访问尚未验证，接口授权不代表相关业务工具已接入。',
      ...states.map(p=>`${p.scope}：${p.status==='granted'?'已授权':p.status==='missing'?'缺失':'未确认'}`)
    ].filter(Boolean).join('；')
  };
}

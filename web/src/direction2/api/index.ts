import { platformAuth, realPlatform } from './platform';
import { apiRequest, activeWorkspaceId, D2ApiError } from './client';
import { normalizeTriggerAllowedActions } from '../utils/workforce';
import type {
  UserProfile, WorkspaceInfo, AuthSessionResponse, SendEmailCodeResponse, GoogleOAuthStartResponse,
  DashboardOverview, GlobalSearchResult,
  ModelItem, WorkspaceSkill, SkillZipParseResult, McpServerEndpoint, McpToolSchema,
  ChannelSyncSourceDetail, DirectorySyncStatus, DirectorySyncJob, ChannelDirectoryDepartment,
  ChannelDirectoryUser, ConnectionDirectoryQueryResult,
  WorkspaceEmailInvitation, UserEmployeePermissionSummary,
  WecomVerifySession, WorkforceOptions, CapabilityPackageBinding,
  DigitalEmployee, EmployeeChannelBinding, CrossGroupSharingRule,
  ChannelQrRegistrationSession, EmployeeTrigger, FeishuTriggerRule,
  HumanConfirmationRule, EmployeeChannelConfigInput, EmployeeTriggerConfigInput,
  EmployeeConfirmationRuleConfigInput, EmployeeSaveConfigInput,
  EmployeeConversationSession, ChatTurnSubmitResponse, ChatExecutionStatus, ChatEmployeeSummaryItem,
  PrincipalRef,
  AsyncOperationReceipt,
  TriggerSecretRegenerateResult, TriggerRunExecutionData,
  WorkspaceSettings, WorkspaceMember,
  PlatformType
} from '../types';

export function toChannelConfigInput(ch: EmployeeChannelBinding): EmployeeChannelConfigInput {
  return {
    platform: ch.platform,
    connectionId: ch.connectionId,
    usageScope: {
      mode: ch.usageScope.mode,
      whitelistMembers: [...ch.usageScope.whitelistMembers],
      whitelistDepartments: [...ch.usageScope.whitelistDepartments],
      blacklistMembers: [...ch.usageScope.blacklistMembers],
      allowedGroupIds: [...ch.usageScope.allowedGroupIds]
    },
    capabilities: {
      ordinaryGroupEnabled: ch.capabilities.ordinaryGroupEnabled,
      keywordTriggersEnabled: ch.capabilities.keywordTriggersEnabled,
      keywords: ch.capabilities.keywords ? [...ch.capabilities.keywords] : undefined,
      regexMatchEnabled: ch.capabilities.regexMatchEnabled,
      regexPattern: ch.capabilities.regexPattern,
      feishuRules: ch.capabilities.feishuRules ? JSON.parse(JSON.stringify(ch.capabilities.feishuRules)) : undefined,
      recentContextEnabled: ch.capabilities.recentContextEnabled,
      recentContextWindow: ch.capabilities.recentContextWindow
    },
    sharingRules: ch.sharingRules.map(sr => ({
      id: sr.id,
      name: sr.name,
      mode: sr.mode,
      sourceGroups: [...sr.sourceGroups],
      targetGroups: [...sr.targetGroups],
      targetUserId: sr.targetUserId,
      targetUserLabel: sr.targetUserLabel,
      targetUser: sr.targetUser ? { ...sr.targetUser } : undefined,
      status: sr.status
    }))
  };
}

export function toTriggerConfigInput(trg: EmployeeTrigger): EmployeeTriggerConfigInput {
  return {
    id: trg.id,
    name: trg.name,
    type: trg.type,
    enabled: trg.enabled,
    allowedActions: normalizeTriggerAllowedActions(trg.type, trg.allowedActions),
    cron: trg.cron,
    timezone: trg.timezone,
    description: trg.description,
    businessKeyMode: trg.businessKeyMode,
    businessKeyField: trg.businessKeyField,
    businessKeyPattern: trg.businessKeyPattern,
    githubEvents: trg.githubEvents ? [...trg.githubEvents] : undefined,
    githubRepo: trg.githubRepo,
    githubIssueSyncMode: trg.githubIssueSyncMode,
    historyPolicy: trg.historyPolicy,
    notificationTargets: trg.notificationTargets.map(nt => ({
      platform: nt.platform,
      connectionId: nt.connectionId,
      groupId: nt.groupId,
      groupName: nt.groupName
    })),
    ...(trg.type === 'webhook' && trg.draftSecretToken ? { draftSecretToken: trg.draftSecretToken } : {})
  };
}

export function toConfirmationRuleConfigInput(rule: HumanConfirmationRule): EmployeeConfirmationRuleConfigInput {
  return {
    id: rule.id,
    questionScope: rule.questionScope,
    target: {
      kind: rule.target.kind,
      targetType: rule.target.targetType || 'user',
      label: rule.target.label,
      userId: rule.target.userId,
      targetId: rule.target.targetId || rule.target.principal?.targetId || rule.target.userId,
      provider: rule.target.provider,
      channel: rule.target.channel || (rule.target.provider === 'platform' ? 'web' : rule.target.provider),
      connectionId: rule.target.connectionId,
      botId: rule.target.botId,
      principal: rule.target.principal ? { ...rule.target.principal } : undefined
    },
    fallbackChannel: rule.fallbackChannel
  };
}

function sanitizeAccessScopeForWrite<
  T extends {
    mode: string;
    whitelistMemberIds?: string[];
    blacklistMemberIds?: string[];
    whitelistTargets?: PrincipalRef[] | undefined;
    blacklistTargets?: PrincipalRef[] | undefined;
    allowGroupMembers?: boolean | undefined;
  }
>(scope: T): Record<string, unknown> {
  const hasCanonical = scope.whitelistTargets !== undefined || scope.blacklistTargets !== undefined;
  if (hasCanonical) {
    return {
      mode: scope.mode,
      whitelistTargets: (scope.whitelistTargets || []).map(t => ({ ...t })),
      blacklistTargets: (scope.blacklistTargets || []).map(t => ({ ...t })),
      ...(scope.allowGroupMembers !== undefined ? { allowGroupMembers: scope.allowGroupMembers } : {})
    };
  }
  return JSON.parse(JSON.stringify(scope)) as Record<string, unknown>;
}

function sanitizeCapabilityPackagesForWrite(pkgs: CapabilityPackageBinding[]): CapabilityPackageBinding[] {
  return pkgs.map(pkg => {
    const cloned = JSON.parse(JSON.stringify(pkg)) as CapabilityPackageBinding;
    if (cloned.scope?.userScope) {
      cloned.scope.userScope = sanitizeAccessScopeForWrite(cloned.scope.userScope) as unknown as CapabilityPackageBinding['scope']['userScope'];
    }
    return cloned;
  });
}

export function toEmployeeSaveConfigInput(
  emp: DigitalEmployee,
  expectedVersion?: number | undefined
): EmployeeSaveConfigInput {
  return {
    expectedVersion: expectedVersion ?? emp.version,
    name: emp.name,
    role: emp.role,
    avatar: emp.avatar,
    executionEngine: emp.executionEngine || 'pi',
    modelId: emp.modelId,
    thinkingLevel: emp.thinkingLevel,
    prompt: emp.prompt,
    unifiedAccess: sanitizeAccessScopeForWrite(emp.unifiedAccess) as unknown as EmployeeSaveConfigInput['unifiedAccess'],
    handoffPolicy: JSON.parse(JSON.stringify(emp.handoffPolicy)),
    webChatAccess: JSON.parse(JSON.stringify(emp.webChatAccess)),
    humanConfirmationRules: emp.humanConfirmationRules.map(toConfirmationRuleConfigInput),
    capabilityPackages: sanitizeCapabilityPackagesForWrite(emp.capabilityPackages),
    channels: emp.channels.map(toChannelConfigInput),
    triggers: emp.triggers.map(toTriggerConfigInput)
  };
}

function buildDirQueryString(params?: {
  q?: string | undefined;
  page?: number | undefined;
  pageSize?: number | undefined;
  snapshotVersion?: string | undefined;
  cursor?: string | undefined;
}): string {
  const sp = new URLSearchParams();
  if (params?.q) sp.set('q', params.q);
  if (params?.page) sp.set('page', String(params.page));
  if (params?.pageSize) sp.set('pageSize', String(params.pageSize));
  if (params?.snapshotVersion) sp.set('snapshotVersion', params.snapshotVersion);
  if (params?.cursor) sp.set('cursor', params.cursor);
  const s = sp.toString();
  return s ? `?${s}` : '';
}

function wsBase(wsId = activeWorkspaceId.value): string {
  return `/workspaces/${encodeURIComponent(wsId)}`;
}

const mockAuthApi = {
  getMe: () => apiRequest<UserProfile>('/auth/me'),
  getWorkspaces: () => apiRequest<WorkspaceInfo[]>('/workspaces'),
  loginWithEmail: (payload: { email: string; password: string }) =>
    apiRequest<AuthSessionResponse>('/auth/login', {
      method: 'POST',
      body: JSON.stringify(payload)
    }),
  loginWithEmailCode: (payload: { email: string; code: string }) =>
    apiRequest<AuthSessionResponse>('/auth/login/code', {
      method: 'POST',
      body: JSON.stringify(payload)
    }),
  sendEmailCode: (payload: { email: string; purpose: 'login' | 'register' | 'reset_password' }) =>
    apiRequest<SendEmailCodeResponse>('/auth/email/send-code', {
      method: 'POST',
      body: JSON.stringify(payload)
    }),
  registerWithEmail: (payload: { name: string; email: string; code: string; password: string }) =>
    apiRequest<AuthSessionResponse>('/auth/register', {
      method: 'POST',
      body: JSON.stringify(payload)
    }),
  resetPassword: (payload: { email: string; code: string; newPassword: string }) =>
    apiRequest<{ ok: boolean; email: string }>('/auth/password/reset', {
      method: 'POST',
      body: JSON.stringify(payload)
    }),
  getGoogleOAuthUrl: (redirectUri: string) =>
    apiRequest<GoogleOAuthStartResponse>(`/auth/oauth/google/url?redirectUri=${encodeURIComponent(redirectUri)}`),
  exchangeGoogleOAuthCallback: (payload: {
    code: string;
    state: string;
    redirectUri: string;
    mockAccount?: { email: string; name?: string | undefined } | undefined;
  }) =>
    apiRequest<AuthSessionResponse>('/auth/oauth/google/callback', {
      method: 'POST',
      body: JSON.stringify(payload)
    }),
  logout: () =>
    apiRequest<{ ok: boolean }>('/auth/logout', {
      method: 'POST'
    })
};

export const authApi = realPlatform ? { ...mockAuthApi, ...platformAuth } : mockAuthApi;

export const dashboardApi = {
  getOverview: (wsId = activeWorkspaceId.value) =>
    apiRequest<DashboardOverview>(`${wsBase(wsId)}/dashboard/overview`),
  search: (query: string, wsId = activeWorkspaceId.value) =>
    apiRequest<GlobalSearchResult>(`${wsBase(wsId)}/search?q=${encodeURIComponent(query)}`)
};

export const resourcesApi = {
  listModels: (wsId = activeWorkspaceId.value) =>
    apiRequest<ModelItem[]>(`${wsBase(wsId)}/models`),
  createModel: (payload: Record<string, unknown>, wsId = activeWorkspaceId.value) =>
    apiRequest<ModelItem>(`${wsBase(wsId)}/models`, { method: 'POST', body: JSON.stringify(payload) }),
  updateModel: (id: string, payload: Record<string, unknown>, wsId = activeWorkspaceId.value) =>
    apiRequest<ModelItem>(`${wsBase(wsId)}/models/${encodeURIComponent(id)}`, { method: 'PUT', body: JSON.stringify(payload) }),
  deleteModel: (id: string, wsId = activeWorkspaceId.value) =>
    apiRequest<{ deleted: boolean; id: string }>(`${wsBase(wsId)}/models/${encodeURIComponent(id)}`, { method: 'DELETE' }),
  testModelConnection: (
    payload: {
      profileId?: string | undefined;
      name?: string | undefined;
      protocol: string;
      endpoint: string;
      modelCode: string;
      apiKey?: string | undefined;
      supportsThinking?: boolean | undefined;
    },
    wsId = activeWorkspaceId.value
  ) =>
    apiRequest<{ ok: boolean; protocol: string; modelCode: string; latency: string; receipt: string; connectionTestReceipt: string; expiresAt: string }>(
      payload.profileId
        ? `${wsBase(wsId)}/models/${encodeURIComponent(payload.profileId)}/test-connection`
        : `${wsBase(wsId)}/models/test-connection`,
      { method: 'POST', body: JSON.stringify(payload) }
    ),
  toggleModelStatus: (id: string, enabled: boolean, wsId = activeWorkspaceId.value) =>
    apiRequest<ModelItem>(`${wsBase(wsId)}/models/${encodeURIComponent(id)}/status`, { method: 'PATCH', body: JSON.stringify({ enabled }) }),
  setDefaultModel: (id: string, wsId = activeWorkspaceId.value) =>
    apiRequest<ModelItem>(`${wsBase(wsId)}/models/${encodeURIComponent(id)}/set-default`, { method: 'POST' }),
  revokeModelKey: (id: string, wsId = activeWorkspaceId.value) =>
    apiRequest<ModelItem>(`${wsBase(wsId)}/models/${encodeURIComponent(id)}/revoke-key`, { method: 'POST' }),

  listSkills: (wsId = activeWorkspaceId.value) =>
    apiRequest<WorkspaceSkill[]>(`${wsBase(wsId)}/skills`),
  parseSkillZip: (payload: { fileName: string; base64Content: string }, wsId = activeWorkspaceId.value) =>
    apiRequest<SkillZipParseResult>(`${wsBase(wsId)}/skills/parse-zip`, { method: 'POST', body: JSON.stringify(payload) }),
  createSkill: (payload: Record<string, unknown>, wsId = activeWorkspaceId.value) =>
    apiRequest<WorkspaceSkill>(`${wsBase(wsId)}/skills`, { method: 'POST', body: JSON.stringify(payload) }),
  updateSkill: (id: string, payload: Record<string, unknown>, wsId = activeWorkspaceId.value) =>
    apiRequest<WorkspaceSkill>(`${wsBase(wsId)}/skills/${encodeURIComponent(id)}`, { method: 'PATCH', body: JSON.stringify(payload) }),
  syncSkillGit: (id: string, wsId = activeWorkspaceId.value) =>
    apiRequest<WorkspaceSkill>(`${wsBase(wsId)}/skills/${encodeURIComponent(id)}/sync-git`, { method: 'POST' }),

  listMcpServers: (wsId = activeWorkspaceId.value) =>
    apiRequest<McpServerEndpoint[]>(`${wsBase(wsId)}/mcp-servers`),
  createMcpServer: (payload: { receipt: string; allowedTools: string[]; enabled?: boolean }, wsId = activeWorkspaceId.value) =>
    apiRequest<McpServerEndpoint>(`${wsBase(wsId)}/mcp-servers`, { method: 'POST', body: JSON.stringify(payload) }),
  updateMcpServer: (id: string, payload: { receipt: string; allowedTools: string[]; enabled?: boolean }, wsId = activeWorkspaceId.value) =>
    apiRequest<McpServerEndpoint>(`${wsBase(wsId)}/mcp-servers/${encodeURIComponent(id)}`, { method: 'PUT', body: JSON.stringify(payload) }),
  updateMcpAllowedTools: (id: string, allowedTools: string[], wsId = activeWorkspaceId.value) =>
    apiRequest<McpServerEndpoint>(`${wsBase(wsId)}/mcp-servers/${encodeURIComponent(id)}/tools`, { method: 'PATCH', body: JSON.stringify({ allowedTools }) }),
  toggleMcpStatus: (id: string, enabled: boolean, wsId = activeWorkspaceId.value) =>
    apiRequest<McpServerEndpoint>(`${wsBase(wsId)}/mcp-servers/${encodeURIComponent(id)}/status`, { method: 'PATCH', body: JSON.stringify({ enabled }) }),
  discoverMcpTools: (payload: Record<string, unknown>, wsId = activeWorkspaceId.value) =>
    apiRequest<{ ok: boolean; receipt: string; tools: McpToolSchema[]; expiresAt: string; discoveredAt: string }>(`${wsBase(wsId)}/mcp-servers/discover`, { method: 'POST', body: JSON.stringify(payload) }),

  getDirectorySyncSources: (wsId = activeWorkspaceId.value) =>
    apiRequest<{ syncStatus: DirectorySyncStatus; sources: ChannelSyncSourceDetail[]; departments: ChannelDirectoryDepartment[] }>(`${wsBase(wsId)}/directory/sync-sources`),
  triggerDirectorySync: (
    arg?: string | { sourceId?: string | undefined; connectionId?: string | undefined; simulateFailure?: boolean | undefined },
    wsId = activeWorkspaceId.value
  ) => {
    const body = typeof arg === 'string' ? { sourceId: arg } : (arg || {});
    return apiRequest<{
      syncJobId: string;
      jobs: DirectorySyncJob[];
      syncStatus: DirectorySyncStatus;
      sources: ChannelSyncSourceDetail[];
      preservedPreviousSnapshot?: boolean;
    }>(
      `${wsBase(wsId)}/directory/sync`,
      { method: 'POST', body: JSON.stringify(body) }
    );
  },
  getDirectorySyncJob: (jobId: string, wsId = activeWorkspaceId.value) =>
    apiRequest<DirectorySyncJob>(`${wsBase(wsId)}/directory/sync-jobs/${encodeURIComponent(jobId)}`),
  queryChannelSnapshotDirectory: (
    platform: 'feishu' | 'dingtalk',
    connectionId: string,
    params?: {
      q?: string | undefined;
      page?: number | undefined;
      pageSize?: number | undefined;
      snapshotVersion?: string | undefined;
      cursor?: string | undefined;
    },
    wsId = activeWorkspaceId.value
  ) =>
    apiRequest<ConnectionDirectoryQueryResult>(
      `${wsBase(wsId)}/channels/${platform}/${encodeURIComponent(connectionId)}/directory/snapshot${buildDirQueryString(params)}`
    ),
  searchWecomConnectionDirectory: (
    connectionId: string,
    params?: { q?: string | undefined; page?: number | undefined; pageSize?: number | undefined },
    wsId = activeWorkspaceId.value
  ) =>
    apiRequest<ConnectionDirectoryQueryResult>(
      `${wsBase(wsId)}/channels/wecom/${encodeURIComponent(connectionId)}/directory/search${buildDirQueryString(params)}`
    ),
  queryWebDirectory: (
    params?: { q?: string | undefined; page?: number | undefined; pageSize?: number | undefined },
    wsId = activeWorkspaceId.value
  ) =>
    apiRequest<ConnectionDirectoryQueryResult>(`${wsBase(wsId)}/directory/web/members${buildDirQueryString(params)}`),
  listDirectoryUsers: (wsId = activeWorkspaceId.value) =>
    apiRequest<ChannelDirectoryUser[]>(`${wsBase(wsId)}/directory/users`),
  getUserEmployeePermissions: (userId: string, wsId = activeWorkspaceId.value) =>
    apiRequest<UserEmployeePermissionSummary[]>(`${wsBase(wsId)}/directory/users/${encodeURIComponent(userId)}/permissions`),

  startWecomVerifyCode: (
    payload: {
      userId: string;
      employeeId?: string | undefined;
      connectionId: string;
      botId?: string | undefined;
    },
    wsId = activeWorkspaceId.value
  ) =>
    apiRequest<WecomVerifySession>(`${wsBase(wsId)}/wecom/verify-code`, { method: 'POST', body: JSON.stringify(payload) }),
  pollWecomVerifySession: (verifySessionId: string, wsId = activeWorkspaceId.value) =>
    apiRequest<WecomVerifySession>(`${wsBase(wsId)}/wecom/verify-code/sessions/${encodeURIComponent(verifySessionId)}`),
  pollWecomVerifyCode: (userId: string, connectionId: string, wsId = activeWorkspaceId.value) =>
    apiRequest<WecomVerifySession>(
      `${wsBase(wsId)}/wecom/verify-code/${encodeURIComponent(userId)}?connectionId=${encodeURIComponent(connectionId)}`
    ),
  simulateWecomDmCallback: (
    payload: { verifySessionId?: string | undefined; userId?: string | undefined; connectionId?: string | undefined },
    wsId = activeWorkspaceId.value
  ) =>
    payload.verifySessionId
      ? apiRequest<{ ok: boolean; session: WecomVerifySession | null; user: ChannelDirectoryUser | null }>(
          `${wsBase(wsId)}/wecom/verify-code/sessions/${encodeURIComponent(payload.verifySessionId)}/simulate`,
          { method: 'POST', body: JSON.stringify(payload) }
        )
      : apiRequest<{ ok: boolean; session: WecomVerifySession | null; user: ChannelDirectoryUser | null }>(
          `${wsBase(wsId)}/wecom/verify-code/${encodeURIComponent(payload.userId || '')}/simulate`,
          { method: 'POST', body: JSON.stringify({ connectionId: payload.connectionId }) }
        ),

  listEmailInvitations: (wsId = activeWorkspaceId.value) =>
    apiRequest<WorkspaceEmailInvitation[]>(`${wsBase(wsId)}/invitations/email`),
  createEmailInvitation: (payload: { email: string; name: string; targetEmployeeIds: string[] }, wsId = activeWorkspaceId.value) =>
    apiRequest<WorkspaceEmailInvitation>(`${wsBase(wsId)}/invitations/email`, { method: 'POST', body: JSON.stringify(payload) }),
  revokeEmailInvitation: (id: string, wsId = activeWorkspaceId.value) =>
    apiRequest<WorkspaceEmailInvitation>(`${wsBase(wsId)}/invitations/email/${encodeURIComponent(id)}/revoke`, { method: 'POST' })
};

export const workforceApi = {
  getOptions: (wsId = activeWorkspaceId.value) =>
    apiRequest<WorkforceOptions>(`${wsBase(wsId)}/workforce/options`),
  getCapabilityPackagesCatalog: (wsId = activeWorkspaceId.value) =>
    apiRequest<CapabilityPackageBinding[]>(`${wsBase(wsId)}/capability-packages`),
  listEmployees: (wsId = activeWorkspaceId.value) =>
    apiRequest<DigitalEmployee[]>(`${wsBase(wsId)}/entities`),
  getEmployee: (id: string, wsId = activeWorkspaceId.value) =>
    apiRequest<DigitalEmployee>(`${wsBase(wsId)}/entities/${encodeURIComponent(id)}`),
  createEmployee: (payload: Record<string, unknown>, wsId = activeWorkspaceId.value) =>
    apiRequest<DigitalEmployee>(`${wsBase(wsId)}/entities`, { method: 'POST', body: JSON.stringify(payload) }),
  saveFullEmployee: (id: string, draft: DigitalEmployee, expectedVersion?: number | undefined, wsId = activeWorkspaceId.value) => {
    const ver = expectedVersion ?? draft.version;
    return apiRequest<DigitalEmployee>(`${wsBase(wsId)}/entities/${encodeURIComponent(id)}`, {
      method: 'PUT',
      body: JSON.stringify(toEmployeeSaveConfigInput(draft, ver))
    });
  },
  updateEmployeeBasics: (id: string, payload: Record<string, unknown>, expectedVersion?: number | undefined, wsId = activeWorkspaceId.value) =>
    apiRequest<DigitalEmployee>(`${wsBase(wsId)}/entities/${encodeURIComponent(id)}/basics`, {
      method: 'PATCH',
      body: JSON.stringify(expectedVersion !== undefined ? { ...payload, expectedVersion } : payload)
    }),
  updateEmployeeStatus: (id: string, status: DigitalEmployee['status'], expectedVersion?: number | undefined, wsId = activeWorkspaceId.value) =>
    apiRequest<DigitalEmployee>(`${wsBase(wsId)}/entities/${encodeURIComponent(id)}/status`, {
      method: 'PATCH',
      body: JSON.stringify(expectedVersion !== undefined ? { status, expectedVersion } : { status })
    }),
  saveEmployeePackages: (id: string, capabilityPackages: CapabilityPackageBinding[], expectedVersion?: number | undefined, wsId = activeWorkspaceId.value) => {
    const sanitizedPackages = sanitizeCapabilityPackagesForWrite(capabilityPackages);
    return apiRequest<DigitalEmployee>(`${wsBase(wsId)}/entities/${encodeURIComponent(id)}/capability-packages`, {
      method: 'PUT',
      body: JSON.stringify(expectedVersion !== undefined ? { capabilityPackages: sanitizedPackages, expectedVersion } : { capabilityPackages: sanitizedPackages })
    });
  },
  saveEmployeeChannels: (id: string, channels: EmployeeChannelBinding[], expectedVersion?: number | undefined, wsId = activeWorkspaceId.value, _resourceVersions?: DigitalEmployee['resourceVersions']) => {
    const configPayload = channels.map(toChannelConfigInput);
    return apiRequest<DigitalEmployee>(`${wsBase(wsId)}/entities/${encodeURIComponent(id)}/channels`, {
      method: 'PUT',
      body: JSON.stringify(expectedVersion !== undefined ? { channels: configPayload, expectedVersion } : { channels: configPayload })
    });
  },
  toggleEmployeeChannelStatus: (id: string, platform: PlatformType, enabled: boolean, expectedVersion?: number | undefined, wsId = activeWorkspaceId.value) =>
    apiRequest<DigitalEmployee>(`${wsBase(wsId)}/entities/${encodeURIComponent(id)}/channels/${platform}/status`, {
      method: 'PATCH',
      body: JSON.stringify(expectedVersion !== undefined ? { enabled, expectedVersion } : { enabled })
    }),
  unbindEmployeeChannel: (id: string, platform: PlatformType, expectedVersion?: number | undefined, wsId = activeWorkspaceId.value) =>
    apiRequest<DigitalEmployee>(`${wsBase(wsId)}/entities/${encodeURIComponent(id)}/channels/${platform}`, {
      method: 'DELETE',
      body: JSON.stringify(expectedVersion !== undefined ? { expectedVersion } : {})
    }),
  saveGroupSharingRule: (
    id: string,
    platform: PlatformType,
    rule: CrossGroupSharingRule,
    expectedVersion?: number | undefined,
    wsId = activeWorkspaceId.value
  ) =>
    apiRequest<DigitalEmployee>(`${wsBase(wsId)}/entities/${encodeURIComponent(id)}/channels/group-sharing`, {
      method: 'PUT',
      body: JSON.stringify(expectedVersion !== undefined ? { platform, rule, expectedVersion } : { platform, rule })
    }),
  refreshGroupSharingStatus: (id: string, platform?: PlatformType, wsId = activeWorkspaceId.value) =>
    apiRequest<DigitalEmployee>(`${wsBase(wsId)}/entities/${encodeURIComponent(id)}/channels/group-sharing/refresh-status`, { method: 'POST', body: JSON.stringify({ platform }) }),
  testFeishuRules: (sampleText: string, rules: FeishuTriggerRule[], wsId = activeWorkspaceId.value, _employeeId?: string) =>
    apiRequest<{ matchedCount: number; matched: { ruleId: string; ruleName: string; type: string; matchedText: string }[] }>(`${wsBase(wsId)}/channels/feishu/test-rules`, { method: 'POST', body: JSON.stringify({ sampleText, rules }) }),
  startChannelQrRegistration: (id: string, platform: PlatformType, wsId = activeWorkspaceId.value, _replaceId?: string) =>
    apiRequest<ChannelQrRegistrationSession>(`${wsBase(wsId)}/entities/${encodeURIComponent(id)}/channels/${platform}/qr-registration`, { method: 'POST' }),
  getLatestChannelQrRegistration: (id: string, platform: PlatformType, wsId = activeWorkspaceId.value) =>
    apiRequest<{ session: ChannelQrRegistrationSession | null }>(`${wsBase(wsId)}/entities/${encodeURIComponent(id)}/channels/${platform}/qr-registration/latest`),
  pollChannelQrRegistration: (sessionId: string, wsId = activeWorkspaceId.value) =>
    apiRequest<ChannelQrRegistrationSession>(`${wsBase(wsId)}/channels/qr-registration/${encodeURIComponent(sessionId)}`),
  confirmChannelQrRegistration: (sessionId: string, wsId = activeWorkspaceId.value) =>
    apiRequest<ChannelQrRegistrationSession>(`${wsBase(wsId)}/channels/qr-registration/${encodeURIComponent(sessionId)}/confirm`, { method: 'POST' }),
  testChannelConnection: (
    id: string,
    platform: PlatformType,
    payload: { appId?: string; appSecret?: string; botId?: string; botSecret?: string; appKey?: string; useStored?: boolean; configVersion?: number },
    wsId = activeWorkspaceId.value
  ) =>
    apiRequest<{ ok: boolean; platform: PlatformType; latency: string; channelTestReceipt: string; testedAt: string; credentialStatus?: 'verified' | 'format_only' }>(
      `${wsBase(wsId)}/entities/${encodeURIComponent(id)}/channels/${platform}/test-connection`,
      { method: 'POST', body: JSON.stringify(payload) }
    ),
  bindChannelCredentials: (
    id: string,
    platform: PlatformType,
    payload: { appId?: string; appSecret?: string; botId?: string; botSecret?: string; appKey?: string; channelTestReceipt: string; confirmedImpact?: boolean; expectedVersion?: number | undefined },
    wsId = activeWorkspaceId.value
  ) =>
    apiRequest<{ employee: DigitalEmployee; channel: EmployeeChannelBinding; switchingRobot: boolean }>(
      `${wsBase(wsId)}/entities/${encodeURIComponent(id)}/channels/${platform}/bind`,
      {
        method: 'POST',
        body: JSON.stringify(payload)
      }
    ),
  rebindChannelCredentials: (
    id: string,
    platform: PlatformType,
    payload: { appId?: string; appSecret?: string; botId?: string; botSecret?: string; appKey?: string; channelTestReceipt: string; confirmedImpact?: boolean; expectedVersion?: number | undefined },
    wsId = activeWorkspaceId.value
  ) =>
    apiRequest<{ employee: DigitalEmployee; channel: EmployeeChannelBinding; switchingRobot: boolean }>(
      `${wsBase(wsId)}/entities/${encodeURIComponent(id)}/channels/${platform}/rebind`,
      {
        method: 'POST',
        body: JSON.stringify(payload)
      }
    ),
  saveEmployeeTriggers: (id: string, triggers: EmployeeTrigger[], expectedVersion?: number | undefined, wsId = activeWorkspaceId.value) => {
    const configPayload = triggers.map(toTriggerConfigInput);
    return apiRequest<DigitalEmployee>(`${wsBase(wsId)}/entities/${encodeURIComponent(id)}/triggers`, {
      method: 'PUT',
      body: JSON.stringify(expectedVersion !== undefined ? { triggers: configPayload, expectedVersion } : { triggers: configPayload })
    });
  },
  regenerateTriggerSecret: (
    id: string,
    triggerId: string,
    optsOrAllowDraft: boolean | { allowDraft?: boolean | undefined; expectedVersion?: number | undefined } = false,
    wsId = activeWorkspaceId.value
  ) => {
    const opts = typeof optsOrAllowDraft === 'boolean' ? { allowDraft: optsOrAllowDraft } : optsOrAllowDraft;
    return apiRequest<TriggerSecretRegenerateResult>(
      `${wsBase(wsId)}/entities/${encodeURIComponent(id)}/triggers/${encodeURIComponent(triggerId)}/regenerate-secret`,
      {
        method: 'POST',
        headers: buildActionHeaders(opts.expectedVersion),
        body: JSON.stringify({ allowDraft: Boolean(opts.allowDraft), expectedVersion: opts.expectedVersion })
      }
    );
  },
  runTriggerNow: (
    id: string,
    triggerId: string,
    opts?: { expectedVersion?: number | undefined; idempotencyKey?: string | undefined },
    wsId = activeWorkspaceId.value
  ) =>
    runWithReusableIdempotencyKey(
      'trigger_run',
      `${id}::${triggerId}`,
      opts?.expectedVersion,
      { employeeId: id, triggerId },
      opts?.idempotencyKey,
      key =>
        apiRequest<AsyncOperationReceipt<TriggerRunExecutionData>>(
          `${wsBase(wsId)}/entities/${encodeURIComponent(id)}/triggers/${encodeURIComponent(triggerId)}/run`,
          {
            method: 'POST',
            headers: buildActionHeaders(opts?.expectedVersion, key),
            body: JSON.stringify({ expectedVersion: opts?.expectedVersion, idempotencyKey: key })
          }
        )
    ),
  getEmployeeChannelStatuses: (id: string, wsId = activeWorkspaceId.value) =>
    apiRequest<{
      employeeId: string;
      status: DigitalEmployee['status'];
      checkedAt: string;
      addedBindings?: {channel:EmployeeChannelBinding;revision:number}[];
      channelReadErrors?: Partial<Record<PlatformType, string | null>>;
      channels: (Pick<
        EmployeeChannelBinding,
        'platform' | 'connectionId' | 'status' | 'statusText' | 'credentialStatus' | 'connectionState' | 'permissionReady' | 'lastVerifiedAt' | 'errorDetail' | 'boundGroups' | 'businessCapabilities'
      > & { observedConfigRevision?: number; bindingMissing?: true })[];
    }>(`${wsBase(wsId)}/entities/${encodeURIComponent(id)}/channels/status`),
  previewTriggerCron: (id: string, cron: string, timezone: string, wsId = activeWorkspaceId.value) =>
    apiRequest<{ cron: string; timezone: string; previews: string[] }>(`${wsBase(wsId)}/entities/${encodeURIComponent(id)}/triggers/preview-cron`, { method: 'POST', body: JSON.stringify({ cron, timezone }) }),
  saveEmployeeRules: (id: string, payload: Record<string, unknown>, expectedVersion?: number | undefined, wsId = activeWorkspaceId.value) => {
    const sanitized: Record<string, unknown> = { ...payload };
    if (Array.isArray(payload['humanConfirmationRules'])) {
      sanitized['humanConfirmationRules'] = (payload['humanConfirmationRules'] as HumanConfirmationRule[]).map(toConfirmationRuleConfigInput);
    }
    if (payload['unifiedAccess'] && typeof payload['unifiedAccess'] === 'object') {
      sanitized['unifiedAccess'] = sanitizeAccessScopeForWrite(
        payload['unifiedAccess'] as Parameters<typeof sanitizeAccessScopeForWrite>[0]
      );
    }
    if (expectedVersion !== undefined) {
      sanitized['expectedVersion'] = expectedVersion;
    }
    return apiRequest<DigitalEmployee>(`${wsBase(wsId)}/entities/${encodeURIComponent(id)}/rules`, {
      method: 'PUT',
      body: JSON.stringify(sanitized)
    });
  }
};

export const chatApi = {
  listChatEmployees: (wsId = activeWorkspaceId.value) =>
    apiRequest<{ employee: DigitalEmployee; session: EmployeeConversationSession }[]>(`${wsBase(wsId)}/chat-employees`),
  listChatEmployeesSummary: (wsId = activeWorkspaceId.value) =>
    apiRequest<ChatEmployeeSummaryItem[]>(`${wsBase(wsId)}/chat-employees?summary=true`),
  getEmployeeConversation: (
    empId: string,
    opts?: { limit?: number | undefined; before?: string | undefined },
    wsId = activeWorkspaceId.value
  ) => {
    const sp = new URLSearchParams();
    if (opts?.limit !== undefined) sp.set('limit', String(opts.limit));
    if (opts?.before) sp.set('before', opts.before);
    const qs = sp.toString();
    return apiRequest<EmployeeConversationSession>(
      `${wsBase(wsId)}/entities/${encodeURIComponent(empId)}/conversation${qs ? `?${qs}` : ''}`
    );
  },
  sendTurn: (
    empId: string,
    content: string,
    opts?: { clientMessageId?: string | undefined; idempotencyKey?: string | undefined },
    wsId = activeWorkspaceId.value
  ) =>
    apiRequest<ChatTurnSubmitResponse>(`${wsBase(wsId)}/entities/${encodeURIComponent(empId)}/conversation/turns`, {
      method: 'POST',
      headers: opts?.idempotencyKey ? { 'Idempotency-Key': opts.idempotencyKey } : {},
      body: JSON.stringify({ content, clientMessageId: opts?.clientMessageId })
    }),
  getExecutionStatus: (empId: string, executionId: string, wsId = activeWorkspaceId.value) =>
    apiRequest<ChatExecutionStatus>(
      `${wsBase(wsId)}/entities/${encodeURIComponent(empId)}/conversation/executions/${encodeURIComponent(executionId)}`
    ),
  interruptExecution: (empId: string, executionId: string, wsId = activeWorkspaceId.value) =>
    apiRequest<{ execution: ChatExecutionStatus; session: EmployeeConversationSession }>(
      `${wsBase(wsId)}/entities/${encodeURIComponent(empId)}/conversation/executions/${encodeURIComponent(executionId)}/interrupt`,
      { method: 'POST' }
    ),
  interruptConversation: (empId: string, executionId?: string | undefined, wsId = activeWorkspaceId.value) =>
    apiRequest<EmployeeConversationSession>(`${wsBase(wsId)}/entities/${encodeURIComponent(empId)}/conversation/interrupt`, {
      method: 'POST',
      body: JSON.stringify(executionId ? { executionId } : {})
    })
};

function buildActionHeaders(
  _expectedVersion?: number | undefined,
  idempotencyKey?: string | undefined
): Record<string, string> {
  const hdrs: Record<string, string> = {};
  if (idempotencyKey) hdrs['Idempotency-Key'] = idempotencyKey;
  return hdrs;
}

function canonicalStringify(val: unknown): string {
  if (val === null || val === undefined) return 'null';
  if (typeof val !== 'object') return JSON.stringify(val);
  if (Array.isArray(val)) {
    return `[${val.map(v => canonicalStringify(v)).join(',')}]`;
  }
  const obj = val as Record<string, unknown>;
  const keys = Object.keys(obj)
    .filter(k => obj[k] !== undefined)
    .sort();
  return `{${keys.map(k => `${JSON.stringify(k)}:${canonicalStringify(obj[k])}`).join(',')}}`;
}

function computePayloadDigest(payload: unknown): string {
  const serialized = canonicalStringify(payload);
  let hash = 2166136261;
  for (let i = 0; i < serialized.length; i += 1) {
    hash ^= serialized.charCodeAt(i);
    hash = Math.imul(hash, 16777619);
  }
  return `${(hash >>> 0).toString(36)}_${serialized.length}`;
}

const pendingActionIdemKeys = new Map<string, string>();

function buildIdemScope(action: string, targetId: string, versionTag?: number | string, payload?: unknown): { prefix: string; scope: string } {
  const prefix = `${action}::${targetId}::${versionTag ?? 'latest'}::`;
  const digest = payload !== undefined ? computePayloadDigest(payload) : 'default';
  return { prefix, scope: `${prefix}${digest}` };
}

export function getOrCreateActionIdempotencyKey(
  action: string,
  targetId: string,
  versionTag?: number | string,
  payload?: unknown
): string {
  const { prefix, scope } = buildIdemScope(action, targetId, versionTag, payload);
  const existing = pendingActionIdemKeys.get(scope);
  if (existing) return existing;

  // 若用户在同一对象/版本上修改了操作内容（例如修改消息正文或更换改派人），销毁旧内容的幂等键并生成新键
  for (const k of Array.from(pendingActionIdemKeys.keys())) {
    if (k.startsWith(prefix)) {
      pendingActionIdemKeys.delete(k);
    }
  }
  const fresh = `idem_${action}_${targetId}_${Date.now().toString(36)}_${Math.random().toString(36).slice(2, 6)}`;
  pendingActionIdemKeys.set(scope, fresh);
  return fresh;
}

export function getPendingActionIdempotencyKey(
  action: string,
  targetId: string,
  versionTag?: number | string,
  payload?: unknown
): string | undefined {
  const { prefix, scope } = buildIdemScope(action, targetId, versionTag, payload);
  if (payload !== undefined) {
    return pendingActionIdemKeys.get(scope);
  }
  for (const [k, v] of pendingActionIdemKeys.entries()) {
    if (k.startsWith(prefix)) return v;
  }
  return undefined;
}

export function clearActionIdempotencyKey(
  action: string,
  targetId: string,
  versionTag?: number | string,
  payload?: unknown
): void {
  const { prefix, scope } = buildIdemScope(action, targetId, versionTag, payload);
  if (payload !== undefined) {
    pendingActionIdemKeys.delete(scope);
    return;
  }
  for (const k of Array.from(pendingActionIdemKeys.keys())) {
    if (k.startsWith(prefix)) pendingActionIdemKeys.delete(k);
  }
}

async function runWithReusableIdempotencyKey<T>(
  action: string,
  targetId: string,
  versionTag: number | string | undefined,
  payloadForFingerprint: unknown,
  explicitKey: string | undefined,
  fn: (idemKey: string) => Promise<T>
): Promise<T> {
  const key = explicitKey || getOrCreateActionIdempotencyKey(action, targetId, versionTag, payloadForFingerprint);
  try {
    const res = await fn(key);
    clearActionIdempotencyKey(action, targetId, versionTag, payloadForFingerprint);
    return res;
  } catch (err) {
    // Only clear key on deterministic client validation/conflict (400/404/409).
    // Preserve key across 5xx / timeout / dropped response ONLY for identical payload retries.
    if (err instanceof D2ApiError && err.status >= 400 && err.status < 500) {
      clearActionIdempotencyKey(action, targetId, versionTag, payloadForFingerprint);
    }
    throw err;
  }
}

// Legacy trigger configuration still polls its existing Mock operation receipt.
export const tasksApi = {
  getOperation: (opId: string, wsId = activeWorkspaceId.value) =>
    apiRequest<AsyncOperationReceipt>(`${wsBase(wsId)}/operations/${encodeURIComponent(opId)}`)
};

export const settingsApi = {
  getSettings: (wsId = activeWorkspaceId.value) =>
    apiRequest<WorkspaceSettings>(`${wsBase(wsId)}/settings`),
  saveSettings: (payload: Pick<WorkspaceSettings, 'name' | 'desc'> & Partial<WorkspaceSettings>, wsId = activeWorkspaceId.value) =>
    apiRequest<WorkspaceSettings>(`${wsBase(wsId)}/settings`, { method: 'PUT', body: JSON.stringify(payload) }),
  listMembers: (wsId = activeWorkspaceId.value) =>
    apiRequest<WorkspaceMember[]>(`${wsBase(wsId)}/members`),
  inviteMember: (payload: { email: string; role: 'admin' | 'member' }, wsId = activeWorkspaceId.value) =>
    apiRequest<{ inviteLink: string; member: WorkspaceMember }>(`${wsBase(wsId)}/invitations`, { method: 'POST', body: JSON.stringify(payload) }),
  updateMember: (id: string, payload: Partial<Pick<WorkspaceMember, 'role' | 'status'>>, wsId = activeWorkspaceId.value) =>
    apiRequest<WorkspaceMember>(`${wsBase(wsId)}/members/${encodeURIComponent(id)}`, { method: 'PATCH', body: JSON.stringify(payload) })
};

// Direction 2 shared domain contracts, API DTO types, and avatar preset utilities.

export const PORTRAIT_AVATAR_PRESETS = Array.from({ length: 27 }, (_, i) => `cn-v2-${String(i + 1).padStart(2, '0')}`);
export const ALL_AVATAR_PRESETS = [...PORTRAIT_AVATAR_PRESETS];

export function resolveD2AvatarPreset(preset: string | null | undefined, identityKey = ''): string {
  if (preset && ALL_AVATAR_PRESETS.includes(preset)) return preset;
  const hash = Array.from(identityKey || 'emp').reduce((n, c) => ((n * 31) + (c.codePointAt(0) || 0)) >>> 0, 0);
  return PORTRAIT_AVATAR_PRESETS[hash % PORTRAIT_AVATAR_PRESETS.length] || 'cn-v2-01';
}

export type PlatformType = 'feishu' | 'dingtalk' | 'wecom';
export type MemberProviderType = 'web' | 'feishu' | 'dingtalk' | 'wecom';
export type ModelProtocol = 'chat_completions' | 'openai_responses' | 'anthropic_messages';
export type ExecutionEngine = 'pi' | 'codex' | 'claudecode';

export interface PrincipalRef {
  targetType: 'user' | 'department';
  channel: MemberProviderType;
  connectionId: string;
  targetId: string;
  displayName?: string | undefined;
  canonicalKey?: string | undefined;
}

export function buildCanonicalPrincipalKey(ref: Pick<PrincipalRef, 'targetType' | 'channel' | 'connectionId' | 'targetId'>): string {
  return `${ref.channel}::${ref.connectionId}::${ref.targetType}::${ref.targetId}`;
}

export interface FeishuTriggerRule {
  id: string;
  type: 'keyword' | 'regex';
  name: string;
  pattern: string;
  enabled: boolean;
  description?: string | undefined;
}

export interface ChannelBusinessCapability {
  authorizationUrl?: string;
  id: string;
  name: string;
  description: string;
  platformAuthStatus: 'authorized' | 'partial' | 'unauthorized' | 'not_detected' | 'unknown';
  resourceAccessStatus: 'ready' | 'pending_collaborator' | 'not_configured' | 'unknown';
  statusDetail: string;
}

export interface UnifiedAccessPolicy {
  mode: 'all_with_blacklist' | 'whitelist_with_blacklist';
  whitelistMemberIds: string[];
  blacklistMemberIds: string[];
  whitelistTargets?: PrincipalRef[] | undefined;
  blacklistTargets?: PrincipalRef[] | undefined;
  /** Whether members inside a group chat outside the configured access scope can still use the AI employee in that group. Defaults to true. */
  allowGroupMembers?: boolean | undefined;
}

export interface WebChatAccessPolicy {
  mode: 'all' | 'custom';
  allowMemberIds: string[];
  denyMemberIds: string[];
  allowTargets?: PrincipalRef[] | undefined;
  denyTargets?: PrincipalRef[] | undefined;
}

export interface HandoffAccessPolicy {
  allowAll?: boolean | undefined;
  mode: 'all_with_blacklist' | 'whitelist';
  allowEmployeeIds: string[];
  denyEmployeeIds: string[];
}

export interface CrossGroupSharingRule {
  id: string;
  name: string;
  mode: 'interconnect' | 'directed';
  sourceGroups: string[];
  targetGroups: string[];
  targetUserId?: string | undefined;
  targetUserLabel?: string | undefined;
  targetUser?: PrincipalRef | undefined;
  status: 'active' | 'disabled';
  syncStatus: 'saved' | 'pending_apply' | 'applied' | 'auth_error';
  authErrorDetail?: string | undefined;
  savedAt: string;
}

export interface EmployeeChannelBinding {
  platform: PlatformType;
  platformName: string;
  connectionId?: string | undefined;
  connectionGeneration?: number | undefined;
  identityEpoch?: number | undefined;
  configRevision?: number | undefined;
  status: 'connected' | 'pending_config' | 'error' | 'disabled';
  statusText: string;
  configurationChanged?: boolean | undefined;
  credentialStatus?: 'saved' | 'unsaved' | undefined;
  connectionState?: 'connected' | 'disconnected' | 'error' | undefined;
  permissionReady?: boolean | undefined;
  identityConfirmed?: boolean | undefined;
  directoryStatus?: import('./api/feishu').FeishuDirectoryStatus | undefined;
  dingtalkProactiveSendPermissionMissing?: boolean | undefined;
  connectionMethod?: 'qrcode' | 'existing_app' | undefined;
  appId?: string | undefined;
  corpId?: string | undefined;
  agentId?: string | undefined;
  botId?: string | undefined;
  botSecretMasked?: string | undefined;
  appKey?: string | undefined;
  appSecretMasked?: string | undefined;
  adminConsoleUrl?: string | undefined;
  errorDetail?: string | undefined;
  connectionErrorCode?: string | undefined;
  lastVerifiedAt?: string | undefined;
  businessCapabilities?: ChannelBusinessCapability[] | undefined;
  usageScope: {
    mode: 'all' | 'whitelist';
    groupMembersEnabled?: boolean;
    whitelistMembers: string[];
    whitelistDepartments: string[];
    blacklistMembers: string[];
    allowedGroupIds: string[];
  };
  capabilities: {
    supportsOrdinaryGroup: boolean;
    supportsKeywords: boolean;
    supportsRecentContext: boolean;
    ordinaryGroupEnabled?: boolean | undefined;
    keywordTriggersEnabled?: boolean | undefined;
    keywords?: string[] | undefined;
    regexMatchEnabled?: boolean | undefined;
    regexPattern?: string | undefined;
    feishuRules?: FeishuTriggerRule[] | undefined;
    recentContextEnabled?: boolean | undefined;
    recentContextWindow?: number | undefined;
  };
  sharingRules: CrossGroupSharingRule[];
  boundGroups: {
    id: string;
    name: string;
    memberCount: number;
    activeAt: string;
  }[];
}

export interface ChannelDirectoryDepartment {
  id: string;
  refId?: string | undefined;
  name: string;
  platform: MemberProviderType;
  sourceType?: MemberProviderType | undefined;
  connectionId?: string | undefined;
  memberCount: number;
  principal?: PrincipalRef | undefined;
}

export interface CapabilityPackageUserScope {
  mode: 'inherit_with_blacklist' | 'whitelist_with_blacklist';
  whitelistMemberIds: string[];
  blacklistMemberIds: string[];
  whitelistTargets?: PrincipalRef[] | undefined;
  blacklistTargets?: PrincipalRef[] | undefined;
}

export interface CapabilityPackageHandoffScope {
  mode: 'inherit_with_blacklist' | 'whitelist';
  allowEmployeeIds: string[];
  denyEmployeeIds: string[];
}

export interface CapabilityPackageBinding {
  id: string;
  name: string;
  description: string;
  enabled: boolean;
  isBound: boolean;
  resources: {
    skills: string[];
    skillVersionPins?: Record<string, string> | undefined;
    mcp: string[];
    mcpTools: string[];
    bash: boolean;
    sandboxNetwork?: boolean | undefined;
    sandboxFilesystem?: 'readonly' | 'readwrite' | undefined;
  };
  scope: {
    userScope: CapabilityPackageUserScope;
    userMode?: 'inherit' | 'workspace_members' | 'custom_whitelist' | undefined;
    allowedMemberIds?: string[] | undefined;
    channelPolicies: {
      platform: PlatformType;
      enabled: boolean;
      allowPrivate: boolean;
      allowGroup: boolean;
    }[];
    conversationKinds: ('private' | 'group')[];
    triggerUsageScope: {
      mode: 'all' | 'allowlist' | 'denylist';
      allowTriggerIds: string[];
      denyTriggerIds: string[];
    };
    handoffUsageScope: CapabilityPackageHandoffScope;
  };
}

export type TriggerAllowedAction = 'create_task' | 'supplement_task' | 'cancel_task';

export interface EmployeeTrigger {
  id: string;
  name: string;
  type: 'schedule' | 'webhook' | 'github_issue';
  enabled: boolean;
  allowedActions: TriggerAllowedAction[];
  cron?: string | undefined;
  timezone?: string | undefined;
  description: string;
  businessKeyMode?: 'empty' | 'fixed' | 'period' | undefined;
  businessKeyField?: string | undefined;
  businessKeyPattern?: string | undefined;
  nextExecutionTimes?: string[] | undefined;
  githubEvents?: string[] | undefined;
  webhookEndpoint?: string | undefined;
  keyId?: string | undefined;
  secretMasked?: string | undefined;
  draftSecretToken?: string | undefined;
  githubRepo?: string | undefined;
  githubIssueSyncMode?: 'on_opened' | 'on_updated' | undefined;
  historyPolicy: 'isolated' | 'employee_shared' | 'business_key';
  notificationTargets: {
    platform: PlatformType;
    connectionId?: string | undefined;
    groupId?: string | undefined;
    groupName: string;
  }[];
  executionLogs: {
    id: string;
    executionId?: string | undefined;
    triggeredAt: string;
    status: 'succeeded' | 'failed' | 'running';
    duration: string;
    deliveryStatus: string;
    summary: string;
  }[];
}

export interface TriggerSecretRegenerateResult {
  trigger: EmployeeTrigger;
  secret: string;
  draftSecretToken?: string | undefined;
  version?: number | undefined;
  etag?: string | undefined;
  isDraft?: boolean | undefined;
}

export interface TriggerRunExecutionData {
  executionId: string;
  executionLog: EmployeeTrigger['executionLogs'][number];
  trigger: EmployeeTrigger;
  task: TaskRecord;
}

export interface HumanConfirmationRule {
  id: string;
  questionScope: string;
  target: {
    kind: 'channel_user' | 'platform_member';
    targetType?: 'user' | 'department' | undefined;
    label: string;
    userId: string;
    targetId?: string | undefined;
    provider: MemberProviderType | 'platform';
    channel?: MemberProviderType | undefined;
    connectionId?: string | undefined;
    botId?: string | undefined;
    principal?: PrincipalRef | undefined;
    identityVerified?: boolean | undefined;
    bindingStatus?: 'verified' | 'pending_binding' | undefined;
  };
  fallbackChannel: string;
}

export interface DigitalEmployee {
  isAssistant?: boolean | undefined;
  canManage?: boolean | undefined;
  canChat?: boolean | undefined;
  cardResources?: { channels: PlatformType[]; packageCount: number; triggerCount: number };
  configVersion?: number;
  modelAvailable?: boolean;
  id: string;
  code: string;
  version: number;
  resourceVersions?: {usageAccess?:number;usageHistory?:number;triggerRules?:number;channels:Partial<Record<PlatformType,number>>};
  etag?: string | undefined;
  name: string;
  role: string;
  avatar: string;
  status: 'active' | 'pending_setup' | 'warning' | 'disabled';
  executionEngine: ExecutionEngine;
  modelId: string;
  modelName: string;
  thinkingLevel: 'none' | 'low' | 'medium' | 'high' | null;
  prompt: string;
  unifiedAccess: UnifiedAccessPolicy;
  channels: EmployeeChannelBinding[];
  channelReadErrors?: Partial<Record<PlatformType, string>> | undefined;
  capabilityPackages: CapabilityPackageBinding[];
  triggers: EmployeeTrigger[];
  humanConfirmationRules: HumanConfirmationRule[];
  webChatAccess: WebChatAccessPolicy;
  handoffPolicy: HandoffAccessPolicy;
  stats: {
    todayTasks: number | null;
    todayConversations: number | null;
    successRate: string | null;
  };
}

export interface WorkItem {
  id: string;
  code: string;
  version?: number | undefined;
  activeOperationId?: string | undefined;
  title: string;
  status: 'pending' | 'in_progress' | 'waiting_input' | 'waiting_approval' | 'awaiting_acceptance' | 'completed' | 'failed' | 'canceling' | 'canceled' | 'unknown';
  originPlatform: string;
  originGroup: string;
  originEmployeeId: string;
  ownerEmployeeId: string;
  goal: string;
  criteria: string;
  currentSummary: string;
  nextStep?: string | undefined;
  waitReason?: string | undefined;
  createdAt: string;
  updatedAt: string;
  linkedTaskIds: string[];
  supplementAdopted?: boolean | undefined;
  supplementAdoptionDetail?: {
    status: 'pending' | 'adopted' | 'unadoptable';
    stage: string;
    intent: 'execute' | 'record';
    note: string;
  } | undefined;
  assignments: {
    id: string;
    employeeName: string;
    sourceEmployeeName: string;
    action: string;
    timestamp: string;
  }[];
  events: {
    id: string;
    type: string;
    title: string;
    time: string;
    detail: string;
    adoptionStatus?: 'pending' | 'adopted' | 'unadoptable' | 'record_only' | undefined;
    adoptionStage?: string | undefined;
  }[];
  deliveryStatus: string;
  deliveryCardPreview?: {
    title: string;
    fields: { label: string; value: string }[];
  } | undefined;
}

export interface SkillVersionRecord {
  version: string;
  commitHash?: string | undefined;
  sourceType: 'manual' | 'zip' | 'markdown' | 'git';
  changeLog: string;
  promptTemplate: string;
  updatedAt: string;
}

export interface WorkspaceSkill {
  id: string;
  name: string;
  code: string;
  version: string;
  source: 'builtin' | 'local_import' | 'git_synced' | 'zip_archive' | 'markdown_file';
  gitRepoUrl?: string | undefined;
  gitBranch?: string | undefined;
  gitCommit?: string | undefined;
  importedFileName?: string | undefined;
  description: string;
  promptTemplate: string;
  requiredToolTags: string[];
  boundEmployeesCount: number;
  enabled: boolean;
  createdAt: string;
  historyVersions: SkillVersionRecord[];
}

export interface McpToolSchema {
  name: string;
  description: string;
  riskLevel: 'safe' | 'medium' | 'high';
}

export interface McpHeader {
  key: string;
  value: string;
}

export interface McpServerEndpoint {
  id: string;
  name: string;
  endpoint: string;
  protocol: 'Streamable HTTP' | 'SSE';
  authType: 'no_auth' | 'bearer_token' | 'api_key' | 'custom_headers';
  tokenMasked?: string | undefined;
  apiKeyName?: string | undefined;
  apiKeyValue?: string | undefined;
  customHeaders?: McpHeader[] | undefined;
  status: 'connected' | 'error' | 'testing' | 'needs_discovery';
  tools: McpToolSchema[];
  allowedTools: string[];
  whitelistMatched: boolean;
  enabled: boolean;
}

export interface TaskToolExecution {
  id: string;
  toolName: string;
  inputArgs: string;
  outputResult: string;
  duration: string;
  status: 'success' | 'failed';
}

export interface TaskRunRecord {
  runIndex: number;
  runId?: string | undefined;
  parentTaskId?: string | undefined;
  operationId?: string | undefined;
  startedAt: string;
  duration: string;
  status: 'succeeded' | 'failed' | 'interrupted' | 'running';
  triggerBy: string;
  toolExecutions: TaskToolExecution[];
  logs: string[];
  outputArtifact?: string | undefined;
}

export interface MaterialAuthorization {
  id: string;
  name: string;
  resourceType: 'repository' | 'database' | 'document' | 'api';
  authStatus: 'preparing' | 'authorized' | 'insufficient_scope' | 'expired';
  authScope: string;
  grantedBy: string;
  statusReason?: string | undefined;
  expiresAt?: string | undefined;
}

export interface TaskInputRevision {
  id: string;
  submittedAt: string;
  author: string;
  content: string;
  intent: 'execute' | 'record';
  adoptionStatus: 'pending' | 'adopted' | 'unadoptable';
  effectiveStage: string;
  notes?: string | undefined;
}

export interface DeliveryRecord {
  id: string;
  targetChannel: string;
  recipientType: 'group_webhook' | 'user_private';
  recipientName: string;
  status: 'sending' | 'delivered' | 'send_blocked' | 'unknown' | 'failed';
  sentAt: string;
  statusCode: string;
  statusDetail: string;
  resendCount: number;
}

export interface ChannelSyncSourceDetail {
  id: string;
  channel: PlatformType;
  connectionId: string;
  channelName: string;
  appName: string;
  appIdOrBotId: string;
  syncInterval: string;
  status: 'synced' | 'syncing' | 'error';
  progressPercent: number;
  lastSyncedAt: string;
  snapshotVersion: string;
  snapshotNodeCount: number;
  snapshotGeneratedAt: string;
  lastSuccessfulSnapshot?: {
    snapshotVersion: string;
    syncedAt: string;
    totalMembers: number;
    totalDepartments: number;
  } | undefined;
  totalMembers: number;
  totalDepartments: number;
  lastError?: string | undefined;
  history: {
    syncId: string;
    trigger: 'periodic' | 'webhook' | 'manual';
    time: string;
    duration: string;
    nodesSynced: number;
    status: 'success' | 'partial_fail' | 'failed';
    detail: string;
  }[];
}

export interface WorkspaceEmailInvitation {
  id: string;
  email: string;
  name: string;
  targetEmployeeIds: string[];
  invitedAt: string;
  expiresAt: string;
  status: 'pending' | 'activated' | 'expired' | 'revoked';
}

export interface DirectorySyncStatus {
  lastSyncedAt: string;
  syncInterval: 'daily_0200' | 'realtime_webhook' | 'manual';
  status: 'synced' | 'syncing' | 'error';
  totalSyncedCount: number;
  errorCount: number;
  errorLogs: { time: string; message: string; channel: PlatformType }[];
}

export interface TaskRecord {
  id: string;
  taskId: string;
  version?: number | undefined;
  activeOperationId?: string | undefined;
  workItemId?: string | undefined;
  employeeName: string;
  employeeId: string;
  channelGroup: string;
  platform: PlatformType | 'web';
  toolUsed: string;
  status: 'succeeded' | 'running' | 'waiting_input' | 'waiting_approval' | 'failed' | 'canceling' | 'canceled' | 'unknown' | 'rejected';
  delivery: 'sent' | 'pending' | 'failed' | 'delivered';
  duration: string;
  createdAt: string;
  outputArtifact?: string | undefined;
  rejectReason?: string | undefined;
  runs?: TaskRunRecord[] | undefined;
  materials?: MaterialAuthorization[] | undefined;
  inputRevisions?: TaskInputRevision[] | undefined;
  deliveries?: DeliveryRecord[] | undefined;
  retryHistory: {
    runIndex: number;
    time: string;
    duration: string;
    result: string;
  }[];
  supplementAdopted?: boolean | undefined;
  supplementAdoptionDetail?: {
    status: 'pending' | 'adopted' | 'unadoptable';
    stage: string;
    note: string;
  } | undefined;
  logs: string[];
}

export type ApprovalKind = 'risk_command' | 'business_decision' | 'missing_info';

export interface HumanApproval {
  id: string;
  version?: number | undefined;
  decisionOperationId?: string | undefined;
  taskId: string;
  workItemId?: string | undefined;
  employeeId: string;
  employeeName: string;
  channelGroup: string;
  initiator: string;
  kind: ApprovalKind;
  riskLevel: 'high' | 'medium' | 'low';
  questionScope: string;
  title: string;
  content: string;
  contextReason: string;
  options?: string[] | undefined;
  optionImpacts?: Record<string, { impact: string; consequence: string }> | undefined;
  selectedOption?: string | undefined;
  rejectReason?: string | undefined;
  createdAt: string;
  status: 'pending' | 'approved' | 'rejected';
  notificationStatus: 'sent' | 'blocked' | 'failed';
  deliveryStatus?: 'delivered' | 'awaiting_binding' | 'pending' | 'failed' | undefined;
  reassignSaved?: boolean | undefined;
  blockedReason?: string | undefined;
  recipientLabel: string;
  assignee?: PrincipalRef | undefined;
}

export type AsyncOperationStatus = 'accepted' | 'processing' | 'succeeded' | 'failed';

export interface AsyncOperationReceipt<T = unknown> {
  operationId: string;
  idempotencyKey?: string | undefined;
  action: string;
  targetId: string;
  status: AsyncOperationStatus;
  outcome: string;
  message: string;
  replayed?: boolean | undefined;
  resourceVersion?: number | undefined;
  approvalStatus?: 'pending' | 'approved' | 'rejected' | undefined;
  taskResumeStatus?: 'not_applicable' | 'resuming' | 'running' | 'succeeded' | 'failed' | 'rejected' | undefined;
  deliveryStatus?: 'not_applicable' | 'pending' | 'delivered' | 'awaiting_binding' | 'failed' | undefined;
  reassignSaved?: boolean | undefined;
  errorCode?: string | undefined;
  errorMessage?: string | undefined;
  data: T;
}

export interface ApprovalActionResult {
  operationId: string;
  idempotencyKey?: string | undefined;
  status: AsyncOperationStatus;
  outcome: string;
  message: string;
  replayed?: boolean | undefined;
  approvalStatus?: 'pending' | 'approved' | 'rejected' | undefined;
  taskResumeStatus?: AsyncOperationReceipt['taskResumeStatus'];
  deliveryStatus?: AsyncOperationReceipt['deliveryStatus'];
  reassignSaved?: boolean | undefined;
  errorCode?: string | undefined;
  errorMessage?: string | undefined;
  approval: HumanApproval;
  badgeRefreshed: boolean;
  badgeRefreshError?: string | undefined;
}

export interface ConversationMessage {
  id: string;
  sender: 'user' | 'assistant';
  content: string;
  time: string;
  thinking?: string | undefined;
  generatedTaskId?: string | undefined;
  executionId?: string | undefined;
  status?: 'accepted' | 'running' | 'completed' | 'interrupted' | 'failed' | undefined;
  errorMessage?: string | undefined;
}

export interface EmployeeConversationSession {
  id: string;
  employeeId: string;
  title: string;
  createdAt: string;
  updatedAt: string;
  activeExecutionId?: string | undefined;
  totalMessages?: number | undefined;
  hasMoreMessages?: boolean | undefined;
  nextBeforeCursor?: string | undefined;
  messages: ConversationMessage[];
  sessionTasks: TaskRecord[];
}

export interface ChatTurnSubmitResponse {
  sessionId: string;
  messageId: string;
  executionId: string;
  status: 'accepted' | 'running' | 'completed' | 'interrupted' | 'failed';
  userMessage: ConversationMessage;
  session: EmployeeConversationSession;
}

export interface ChatExecutionStatus {
  executionId: string;
  employeeId: string;
  sessionId: string;
  messageId: string;
  status: 'accepted' | 'running' | 'completed' | 'interrupted' | 'failed';
  thinking?: string | undefined;
  partialContent?: string | undefined;
  assistantMessage?: ConversationMessage | undefined;
  generatedTask?: TaskRecord | null | undefined;
  errorMessage?: string | undefined;
  updatedAt: string;
}

export interface ChatEmployeeSummaryItem {
  employee: DigitalEmployee;
  sessionSummary: {
    id: string;
    employeeId: string;
    title: string;
    updatedAt: string;
    lastMessagePreview: string;
    lastMessageTime: string;
    totalMessages: number;
    sessionTasksCount: number;
    activeExecutionId?: string | undefined;
  };
}

export interface ChannelDirectoryUser {
  id: string;
  refId?: string | undefined;
  name: string;
  department: string;
  email: string;
  platform: MemberProviderType;
  sourceType?: MemberProviderType | undefined;
  connectionId?: string | undefined;
  avatar: string;
  botAuthorized: boolean;
  corpUserId?: string | undefined;
  isWecomVerified?: boolean | undefined;
  bindingStatus?: 'verified' | 'pending_binding' | undefined;
  principal?: PrincipalRef | undefined;
}

export interface DirectorySyncJob {
  syncJobId: string;
  sourceId: string;
  connectionId: string;
  channel: PlatformType;
  status: 'accepted' | 'running' | 'succeeded' | 'failed';
  progressPercent: number;
  processedNodes: number;
  totalNodes: number;
  snapshotVersion?: string | undefined;
  preservedPreviousSnapshot?: boolean | undefined;
  error?: string | undefined;
  startedAt: string;
  finishedAt?: string | undefined;
}

export interface ConnectionDirectoryQueryResult {
  sourceType: MemberProviderType;
  connectionId: string;
  channelName: string;
  queryMode: 'snapshot' | 'live_search' | 'workspace_directory';
  snapshotVersion?: string | undefined;
  snapshotSyncedAt?: string | undefined;
  snapshotFallbackUsed?: boolean | undefined;
  lastSyncError?: string | undefined;
  page: number;
  pageSize: number;
  cursor?: string | undefined;
  nextCursor?: string | undefined;
  totalUsers: number;
  totalDepartments: number;
  users: ChannelDirectoryUser[];
  departments: ChannelDirectoryDepartment[];
}

export interface PendingApprovalsSummary {
  count: number;
  version: number;
  latestUpdatedAt: string;
}

export interface PaginatedResponse<T> {
  items: T[];
  page: number;
  pageSize: number;
  total: number;
  hasMore: boolean;
  nextCursor?: string | undefined;
}

export interface EmployeeChannelConfigInput {
  platform: PlatformType;
  connectionId?: string | undefined;
  usageScope: EmployeeChannelBinding['usageScope'];
  capabilities: {
    ordinaryGroupEnabled?: boolean | undefined;
    keywordTriggersEnabled?: boolean | undefined;
    keywords?: string[] | undefined;
    regexMatchEnabled?: boolean | undefined;
    regexPattern?: string | undefined;
    feishuRules?: FeishuTriggerRule[] | undefined;
    recentContextEnabled?: boolean | undefined;
    recentContextWindow?: number | undefined;
  };
  sharingRules: {
    id: string;
    name: string;
    mode: CrossGroupSharingRule['mode'];
    sourceGroups: string[];
    targetGroups: string[];
    targetUserId?: string | undefined;
    targetUserLabel?: string | undefined;
    targetUser?: PrincipalRef | undefined;
    status: CrossGroupSharingRule['status'];
  }[];
}

export interface EmployeeTriggerConfigInput {
  id: string;
  name: string;
  type: EmployeeTrigger['type'];
  enabled: boolean;
  allowedActions: TriggerAllowedAction[];
  cron?: string | undefined;
  timezone?: string | undefined;
  description: string;
  businessKeyMode?: EmployeeTrigger['businessKeyMode'];
  businessKeyField?: string | undefined;
  businessKeyPattern?: string | undefined;
  githubEvents?: string[] | undefined;
  githubRepo?: string | undefined;
  githubIssueSyncMode?: EmployeeTrigger['githubIssueSyncMode'];
  historyPolicy: EmployeeTrigger['historyPolicy'];
  notificationTargets: EmployeeTrigger['notificationTargets'];
  draftSecretToken?: string | undefined;
}

export interface EmployeeConfirmationRuleConfigInput {
  id: string;
  questionScope: string;
  target: {
    kind: HumanConfirmationRule['target']['kind'];
    targetType?: 'user' | 'department' | undefined;
    label: string;
    userId: string;
    targetId?: string | undefined;
    provider: HumanConfirmationRule['target']['provider'];
    channel?: MemberProviderType | undefined;
    connectionId?: string | undefined;
    botId?: string | undefined;
    principal?: PrincipalRef | undefined;
  };
  fallbackChannel: string;
}

export interface AccessTargetsWriteInput {
  mode: UnifiedAccessPolicy['mode'];
  whitelistTargets: PrincipalRef[];
  blacklistTargets: PrincipalRef[];
  allowGroupMembers?: boolean | undefined;
}

export interface EmployeeSaveConfigInput {
  expectedVersion?: number | undefined;
  name: string;
  role: string;
  avatar: string;
  executionEngine?: ExecutionEngine | undefined;
  modelId: string;
  thinkingLevel: DigitalEmployee['thinkingLevel'];
  prompt: string;
  unifiedAccess: UnifiedAccessPolicy | AccessTargetsWriteInput;
  handoffPolicy: HandoffAccessPolicy;
  webChatAccess: WebChatAccessPolicy;
  humanConfirmationRules: EmployeeConfirmationRuleConfigInput[];
  capabilityPackages: CapabilityPackageBinding[];
  channels: EmployeeChannelConfigInput[];
  triggers: EmployeeTriggerConfigInput[];
}

// ================= 新增前后端 API 契约模型 =================

export type AuthProviderType = 'email' | 'google';

export interface UserProfile {
	 isPlatformAdmin?: boolean;
  id: string;
  name: string;
  email: string;
  role: string;
  avatarText: string;
  authProvider?: AuthProviderType | undefined;
  emailVerified?: boolean | undefined;
}

export interface WorkspaceInfo {
  id: string;
  name: string;
  role: string;
  desc: string;
}

export interface AuthSessionResponse {
  token?: string | undefined;
  user: UserProfile;
  workspaces: WorkspaceInfo[];
}

export interface SendEmailCodeResponse {
  sent: boolean;
  email: string;
  purpose: 'login' | 'register' | 'reset_password';
  expiresInSeconds: number;
  resendAfterSeconds: number;
  debugCode?: string | undefined;
}

export interface GoogleOAuthStartResponse {
  authorizeUrl: string;
  state: string;
  provider: 'google';
  isMock?: boolean | undefined;
}

export interface ModelItem {
  id: string;
  name: string;
  modelCode: string;
  protocol: ModelProtocol;
  endpoint: string;
  apiKeyMasked: string;
  boundEmployeesCount: number;
  supportsThinking: boolean;
  enabled: boolean;
  latency?: string | undefined;
  isDefault: boolean;
  connectionTestReceipt?: string | undefined;
}

export interface WorkspaceSettings {
  name: string;
  desc: string;
  autoTrackingWorkItems: boolean;
  defaultApprovalTimeoutHours: string;
  notificationGroup: string;
}

export interface WorkspaceMember {
  id: string;
  name: string;
  email: string;
  role: 'owner' | 'admin' | 'member';
  status: 'active' | 'disabled';
  joinedAt: string;
}

export interface ActivityEvent {
  id: string;
  time: string;
  category: 'tasks' | 'policy_changes' | 'alerts';
  content: string;
  tone: 'warning' | 'error' | 'info' | 'success';
  badge: string;
}

export interface DashboardOverview {
  metrics: {
    totalEmployees: number;
    runningEmployees: number;
    warningEmployees: number;
    errorEmployees: number;
    totalChannels: number;
    avgLatency: string;
    healthRate: number;
    todayTasksCount: number;
    weeklyNewEmployees: number;
  };
  pendingApprovals: HumanApproval[];
  unconfiguredEmployees: DigitalEmployee[];
  activityEvents: ActivityEvent[];
  trendDates: string[];
  trendPath: string;
}

export interface ChannelHealthMonitorItem {
  name: string;
  status: 'online' | 'degraded' | 'offline';
  latency: string;
  lastHeartbeat: string;
  groupsActive: number;
}

export interface CronJobMonitorItem {
  id: string;
  name: string;
  schedule: string;
  targetEmployee: string;
  nextRun: string;
  lastStatus: string;
}

export interface RuntimeMonitorData {
  healthRate: number;
  totalEmployees: number;
  runningEmployees: number;
  warningEmployees: number;
  errorEmployees: number;
  totalChannels: number;
  channels: ChannelHealthMonitorItem[];
  runtimeHost: {
    name: string;
    status: string;
    cpuUsage: string;
    memoryUsage: string;
    activeSlots: string;
    storageUsed: string;
  };
  cronJobs: CronJobMonitorItem[];
}

export interface UserEmployeePermissionSummary {
  employeeId: string;
  employeeName: string;
  permissionType: string;
  isApprover: boolean;
}

export interface FeishuRegexTemplateItem {
  label: string;
  pattern: string;
  name: string;
  description: string;
}

export interface ChannelGroupOption {
  groupId: string;
  connectionId: string;
  platform: PlatformType;
  name: string;
  memberCount?: number | undefined;
}

export interface WorkforceOptions {
  platformCandidateGroups: Record<PlatformType, string[]>;
  platformCandidateGroupItems: Record<PlatformType, ChannelGroupOption[]>;
  feishuRegexTemplates: FeishuRegexTemplateItem[];
}

export interface WecomVerifySession {
  verifySessionId: string;
  workspaceId?: string | undefined;
  employeeId?: string | undefined;
  connectionId: string;
  botId?: string | undefined;
  userId: string;
  userName: string;
  corpUserId: string;
  code: string;
  expiresInSeconds: number;
  expiresAt: string;
  status: 'pending' | 'verified' | 'expired';
  verifiedAt?: string | undefined;
}

export interface ChannelQrRegistrationSession {
  verificationUrl?: string;
  registrationState?: string;
  registrationError?: string;
  feishuFailure?: { reason: string; detail: string; automaticRetry: boolean; canReauthorize: boolean };
  restartNeedsReview?: boolean;
  sessionId: string;
  workspaceId: string;
  employeeId: string;
  platform: PlatformType;
  qrPayload: string;
  qrSvgDataUrl: string;
  expiresInSeconds: number;
  status: 'waiting_scan' | 'scanned_confirming' | 'authorized' | 'expired';
  qrStatus: 'waiting_scan' | 'scanned_confirming' | 'authorized' | 'expired';
  credentialSaved: boolean;
  permissionReady: boolean | undefined;
  authorizationUrl?: string;
  permissionStatus: 'authorized' | 'pending_admin_approval' | 'unknown';
  connectionState: 'pending' | 'connected' | 'error';
  suggestedAppId?: string | undefined;
  suggestedBotId?: string | undefined;
  binding?: EmployeeChannelBinding | undefined;
}

export interface SkillZipParseResult {
  importedFileName: string;
  name: string;
  code: string;
  version: string;
  description: string;
  promptTemplate: string;
  requiredToolTags: string[];
  extractedFiles: string[];
}

export interface GlobalSearchResultItem {
  id: string;
  kind: 'employee' | 'work_item' | 'task' | 'skill' | 'mcp' | 'conversation';
  title: string;
  subtitle: string;
  agent_id?: string;
  targetMenu?: string;
  targetId?: string | undefined;
}

export interface GlobalSearchResult {
  query: string;
  items: GlobalSearchResultItem[];
  unavailable?: GlobalSearchResultItem['kind'][];
}

import type {
  ChannelDirectoryDepartment,
  ChannelDirectoryUser,
  ConnectionDirectoryQueryResult,
  DigitalEmployee,
  EmployeeChannelBinding,
  EmployeeTrigger,
  PlatformType,
  PrincipalRef,
  TriggerAllowedAction
} from '../types';

function formatCanonicalPrincipalKey(
  ref: Pick<PrincipalRef, 'targetType' | 'channel' | 'connectionId' | 'targetId'>
): string {
  return `${ref.channel}::${ref.connectionId}::${ref.targetType}::${ref.targetId}`;
}

export function parseCanonicalPrincipalKey(raw: string): PrincipalRef | null {
  if (!raw || typeof raw !== 'string' || !raw.includes('::')) return null;
  const parts = raw.split('::');
  if (parts.length < 4) return null;
  const [channel, connectionId, targetType, ...rest] = parts;
  const targetId = rest.join('::');
  if (!channel || (!connectionId && channel !== 'web') || !targetId) return null;
  if (channel !== 'web' && channel !== 'feishu' && channel !== 'wecom' && channel !== 'dingtalk') return null;
  if (targetType !== 'user' && targetType !== 'department') return null;
  return {
    targetType,
    channel,
    connectionId: connectionId || '',
    targetId,
    canonicalKey: raw
  };
}

export function getDirectoryUserCanonicalKey(user: ChannelDirectoryUser): string {
  const channel = user.principal?.channel || user.platform;
  const connectionId = user.principal?.connectionId || user.connectionId || (channel === 'web' ? 'ws-web-directory' : `conn-${channel}`);
  const targetId = user.principal?.targetId || user.refId || user.id;
  return user.principal?.canonicalKey || formatCanonicalPrincipalKey({ targetType: 'user', channel, connectionId, targetId });
}

export function getDirectoryDeptCanonicalKey(dept: ChannelDirectoryDepartment): string {
  const channel = dept.principal?.channel || dept.platform;
  const connectionId = dept.principal?.connectionId || dept.connectionId || (channel === 'web' ? 'ws-web-directory' : `conn-${channel}`);
  const targetId = dept.principal?.targetId || dept.refId || dept.id;
  return dept.principal?.canonicalKey || formatCanonicalPrincipalKey({ targetType: 'department', channel, connectionId, targetId });
}

function findUserForRef(ref: PrincipalRef, directoryUsers: ChannelDirectoryUser[]): ChannelDirectoryUser | undefined {
  return directoryUsers.find(item => {
    if (ref.canonicalKey && item.principal?.canonicalKey && item.principal.canonicalKey === ref.canonicalKey) {
      return true;
    }
    if (item.platform !== ref.channel) return false;
    if (ref.connectionId && item.connectionId && item.connectionId !== ref.connectionId) return false;
    return item.refId === ref.targetId || item.id === ref.targetId;
  });
}

function findDeptForRef(ref: PrincipalRef, directoryDepartments: ChannelDirectoryDepartment[]): ChannelDirectoryDepartment | undefined {
  return directoryDepartments.find(item => {
    if (ref.canonicalKey && item.principal?.canonicalKey && item.principal.canonicalKey === ref.canonicalKey) {
      return true;
    }
    if (item.platform !== ref.channel) return false;
    if (ref.connectionId && item.connectionId && item.connectionId !== ref.connectionId) return false;
    return item.refId === ref.targetId || item.id === ref.targetId;
  });
}

export interface MemberOptionGroup {
  type: 'group';
  label: string;
  key: string;
  children: Array<{ label: string; value: string; disabled: boolean; [key: string]: unknown }>;
  [key: string]: unknown;
}

export function resolveMemberKeyToOptionId(
  id: string,
  targets: PrincipalRef[] = [],
  directoryUsers: ChannelDirectoryUser[] = [],
  directoryDepartments: ChannelDirectoryDepartment[] = []
): string {
  const parsed = parseCanonicalPrincipalKey(id);
  if (parsed) {
    if (parsed.targetType === 'user') {
      const u = findUserForRef(parsed, directoryUsers);
      if (u) return u.id;
    } else {
      const d = findDeptForRef(parsed, directoryDepartments);
      if (d) return d.id;
    }
    return parsed.targetId;
  }
  const t = targets.find(item => item.canonicalKey === id || item.targetId === id);
  if (t) {
    if (t.targetType === 'user') {
      const u = findUserForRef(t, directoryUsers);
      if (u) return u.id;
    } else {
      const d = findDeptForRef(t, directoryDepartments);
      if (d) return d.id;
    }
  }
  if (directoryUsers.some(u => u.id === id) || directoryDepartments.some(d => d.id === id)) {
    return id;
  }
  const byUserRef = directoryUsers.find(u => u.refId === id);
  if (byUserRef) return byUserRef.id;
  const byDeptRef = directoryDepartments.find(d => d.refId === id);
  if (byDeptRef) return byDeptRef.id;
  return id;
}

export function resolveMemberKeyToCanonicalKey(
  id: string,
  targets: PrincipalRef[] = [],
  directoryUsers: ChannelDirectoryUser[] = [],
  directoryDepartments: ChannelDirectoryDepartment[] = []
): string {
  const parsed = parseCanonicalPrincipalKey(id);
  if (parsed) return id;
  const targetMatch = targets.find(item => {
    if (item.canonicalKey === id || item.targetId === id) return true;
    if (item.targetType === 'user') {
      return findUserForRef(item, directoryUsers)?.id === id;
    }
    return findDeptForRef(item, directoryDepartments)?.id === id;
  });
  if (targetMatch) {
    return targetMatch.canonicalKey || formatCanonicalPrincipalKey(targetMatch);
  }
  const u = directoryUsers.find(item => item.id === id || item.refId === id);
  if (u) return getDirectoryUserCanonicalKey(u);
  const d = directoryDepartments.find(item => item.id === id || item.refId === id);
  if (d) return getDirectoryDeptCanonicalKey(d);
  return id;
}

function normalizeScopeIdsToCanonical(
  memberIds: string[] | undefined,
  targets: PrincipalRef[] | undefined,
  directoryUsers: ChannelDirectoryUser[],
  directoryDepartments: ChannelDirectoryDepartment[]
): { ids: string[]; targets: PrincipalRef[] } {
  if (Array.isArray(targets)) {
    const normalizedTargets = targets.map(t => ({
      ...t,
      canonicalKey: t.canonicalKey || formatCanonicalPrincipalKey(t)
    }));
    const ids = normalizedTargets.map(t => t.canonicalKey!);
    return { ids, targets: normalizedTargets };
  }
  const rawIds = memberIds || [];
  const canonicalIds = rawIds.map(id =>
    resolveMemberKeyToCanonicalKey(id, [], directoryUsers, directoryDepartments)
  );
  const builtTargets = buildPrincipalRefsFromIds(canonicalIds, [], directoryUsers, directoryDepartments);
  return {
    ids: builtTargets.map(t => t.canonicalKey || formatCanonicalPrincipalKey(t)),
    targets: builtTargets
  };
}

export function normalizeEmployeeAccessSelections(
  emp: DigitalEmployee,
  directoryUsers: ChannelDirectoryUser[] = [],
  directoryDepartments: ChannelDirectoryDepartment[] = []
): DigitalEmployee {
  const wl = normalizeScopeIdsToCanonical(
    emp.unifiedAccess.whitelistMemberIds,
    emp.unifiedAccess.whitelistTargets,
    directoryUsers,
    directoryDepartments
  );
  const bl = normalizeScopeIdsToCanonical(
    emp.unifiedAccess.blacklistMemberIds,
    emp.unifiedAccess.blacklistTargets,
    directoryUsers,
    directoryDepartments
  );
  emp.unifiedAccess = {
    ...emp.unifiedAccess,
    whitelistMemberIds: wl.ids,
    whitelistTargets: wl.targets,
    blacklistMemberIds: bl.ids,
    blacklistTargets: bl.targets,
    allowGroupMembers: emp.unifiedAccess.allowGroupMembers ?? true
  };
  for (const pkg of emp.capabilityPackages || []) {
    if (pkg.scope?.userScope) {
      const pkgWl = normalizeScopeIdsToCanonical(
        pkg.scope.userScope.whitelistMemberIds,
        pkg.scope.userScope.whitelistTargets,
        directoryUsers,
        directoryDepartments
      );
      const pkgBl = normalizeScopeIdsToCanonical(
        pkg.scope.userScope.blacklistMemberIds,
        pkg.scope.userScope.blacklistTargets,
        directoryUsers,
        directoryDepartments
      );
      pkg.scope.userScope.whitelistMemberIds = pkgWl.ids;
      pkg.scope.userScope.whitelistTargets = pkgWl.targets;
      pkg.scope.userScope.blacklistMemberIds = pkgBl.ids;
      pkg.scope.userScope.blacklistTargets = pkgBl.targets;
    }
  }
  return emp;
}

export function buildPrincipalRefsFromIds(
  ids: string[],
  existing: PrincipalRef[] = [],
  directoryUsers: ChannelDirectoryUser[] = [],
  directoryDepartments: ChannelDirectoryDepartment[] = []
): PrincipalRef[] {
  return ids.map(id => {
    const parsed = parseCanonicalPrincipalKey(id);
    if (parsed) {
      const exactPrev = existing.find(
        t =>
          t.canonicalKey === id ||
          (t.targetType === parsed.targetType &&
            t.channel === parsed.channel &&
            t.connectionId === parsed.connectionId &&
            t.targetId === parsed.targetId)
      );
      if (exactPrev) {
        return {
          ...exactPrev,
          canonicalKey: exactPrev.canonicalKey || id
        };
      }
      if (parsed.targetType === 'user') {
        const matchedUser = findUserForRef(parsed, directoryUsers);
        return {
          ...matchedUser?.principal,
          ...parsed,
          displayName: matchedUser?.name || parsed.targetId,
          canonicalKey: id
        };
      }
      const matchedDept = findDeptForRef(parsed, directoryDepartments);
      return {
        ...matchedDept?.principal,
        ...parsed,
        displayName: matchedDept?.name || parsed.targetId,
        canonicalKey: id
      };
    }

    const prevDirect = existing.find(t => t.canonicalKey === id || t.targetId === id);
    if (prevDirect) {
      return {
        ...prevDirect,
        canonicalKey: prevDirect.canonicalKey || formatCanonicalPrincipalKey(prevDirect)
      };
    }

    const u = directoryUsers.find(item => item.id === id || item.refId === id);
    const d = !u ? directoryDepartments.find(item => item.id === id || item.refId === id) : undefined;
    const prev = existing.find(
      t =>
        (u && t.targetType === 'user' && t.channel === u.platform && (!t.connectionId || !u.connectionId || t.connectionId === u.connectionId) && (t.targetId === u.refId || t.targetId === u.id)) ||
        (d && t.targetType === 'department' && t.channel === d.platform && (!t.connectionId || !d.connectionId || t.connectionId === d.connectionId) && (t.targetId === d.refId || t.targetId === d.id))
    );
    if (prev) {
      return {
        ...prev,
        canonicalKey: prev.canonicalKey || formatCanonicalPrincipalKey(prev)
      };
    }
    if (u) {
      const connectionId = u.connectionId || (u.platform === 'web' ? 'ws-web-directory' : `conn-${u.platform}`);
      const targetId = u.refId || u.id;
      return {
        ...u.principal,
        targetType: 'user',
        channel: u.platform,
        connectionId,
        targetId,
        displayName: u.name,
        canonicalKey: formatCanonicalPrincipalKey({ targetType: 'user', channel: u.platform, connectionId, targetId })
      };
    }
    const channel = d?.platform || 'web';
    const connectionId = d?.connectionId || (channel === 'web' ? 'ws-web-directory' : `conn-${channel}`);
    const targetId = d?.refId || id;
    return {
      ...d?.principal,
      targetType: 'department',
      channel,
      connectionId,
      targetId,
      displayName: d?.name || id,
      canonicalKey: formatCanonicalPrincipalKey({ targetType: 'department', channel, connectionId, targetId })
    };
  });
}

export function isEmployeeSourceConnected(
  emp: DigitalEmployee | null | undefined,
  platform: string,
  connectionId?: string
): boolean {
  if (platform === 'web') return true;
  if (!emp) return false;
  const effectiveConnId = connectionId || `conn-${platform}`;
  return emp.channels.some(
    c => c.status === 'connected' && c.platform === platform && (c.connectionId || `conn-${c.platform}`) === effectiveConnId
  );
}

function unavailablePrincipal(ref?: PrincipalRef): boolean {
  return !!ref && (ref as PrincipalRef & {selectionCandidate?:{selectable:boolean}}).selectionCandidate?.selectable === false;
}
function departmentCountLabel(dept: ChannelDirectoryDepartment): string {
  return dept.principal && 'usageSelector' in dept.principal ? '' : ` (${dept.memberCount}人)`;
}

export function buildUnifiedMemberAndDeptOptions(
  emp: DigitalEmployee | null,
  directoryUsers: ChannelDirectoryUser[],
  directoryDepartments: ChannelDirectoryDepartment[],
  extraRefs: PrincipalRef[] = [],
  extraIds: string[] = []
): MemberOptionGroup[] {
  if (!emp) return [];
  const connectedChannelConnKeys = new Set(
    emp.channels
      .filter(c => c.status === 'connected')
      .map(c => `${c.platform}::${c.connectionId || `conn-${c.platform}`}`)
  );
  const platformNameMap: Record<string, string> = {
    web: '网页',
    feishu: '飞书',
    wecom: '企业微信',
    dingtalk: '钉钉'
  };

  const countConnectionsPerPlatform = new Map<string, Set<string>>();
  for (const item of [...directoryUsers, ...directoryDepartments]) {
    const conn = item.connectionId || '';
    if (!conn) continue;
    const set = countConnectionsPerPlatform.get(item.platform) || new Set<string>();
    set.add(conn);
    countConnectionsPerPlatform.set(item.platform, set);
  }

  const isSourceConnected = (platform: string, connectionId?: string): boolean => {
    if (platform === 'web') return true;
    const effectiveConnId = connectionId || `conn-${platform}`;
    return connectedChannelConnKeys.has(`${platform}::${effectiveConnId}`);
  };

  const formatConnSuffix = (platform: string, connectionId?: string): string => {
    if (!connectionId || platform === 'web') return '';
    const multiConn = (countConnectionsPerPlatform.get(platform)?.size || 0) > 1;
    return multiConn ? ` · ${connectionId}` : '';
  };

  const seenValues = new Set<string>();
  const deptChildren: MemberOptionGroup['children'] = [];
  for (const dept of directoryDepartments) {
    const value = getDirectoryDeptCanonicalKey(dept);
    if (seenValues.has(value)) continue;
    seenValues.add(value);
    const isChannelConnected = isSourceConnected(dept.platform, dept.connectionId);
    const platformLabel = dept.platform === 'web' ? '网页' : dept.platform === 'feishu' ? '飞书' : dept.platform === 'wecom' ? '企微' : '钉钉';
    const connTag = formatConnSuffix(dept.platform, dept.connectionId);
    deptChildren.push({
      label: `[部门] ${platformLabel}${connTag} · ${dept.name}${departmentCountLabel(dept)}${!isChannelConnected ? ' [未接入该渠道，不可选]' : ''}`,
      value,
      disabled: !isChannelConnected || unavailablePrincipal(dept.principal)
    });
  }

  const userChildren: MemberOptionGroup['children'] = [];
  for (const user of directoryUsers) {
    const value = getDirectoryUserCanonicalKey(user);
    if (seenValues.has(value)) continue;
    seenValues.add(value);
    const isChannelConnected = isSourceConnected(user.platform, user.connectionId);
    const platformLabel = platformNameMap[user.platform] || user.platform;
    const connTag = formatConnSuffix(user.platform, user.connectionId);
    userChildren.push({
      label: `${user.name} (${platformLabel}${connTag} · ${user.department})${!isChannelConnected ? ' [未接入该渠道，不可选]' : ''}`,
      value,
      disabled: !isChannelConnected || unavailablePrincipal(user.principal)
    });
  }

  const activeRefs: PrincipalRef[] = [
    ...(emp.unifiedAccess.whitelistTargets || []),
    ...(emp.unifiedAccess.blacklistTargets || []),
    ...extraRefs
  ];
  for (const ref of activeRefs) {
    const val = ref.canonicalKey || formatCanonicalPrincipalKey(ref);
    if (seenValues.has(val)) continue;
    const isConnected = isSourceConnected(ref.channel, ref.connectionId);
    const disabledSuffix = !isConnected ? ' [未接入该渠道，不可选]' : '';
    const pLabel = platformNameMap[ref.channel] || ref.channel;
    const connTag = ref.connectionId && ref.channel !== 'web' ? ` · ${ref.connectionId}` : '';
    if (ref.targetType === 'department') {
      const matchedDept = findDeptForRef(ref, directoryDepartments);
      deptChildren.push({
        label: (matchedDept
          ? `[部门] ${pLabel}${connTag} · ${matchedDept.name}${departmentCountLabel(matchedDept)}`
          : `[部门] ${pLabel}${connTag} · ${ref.displayName || ref.targetId}`) + disabledSuffix,
        value: val,
        disabled: !isConnected
      });
    } else {
      const matchedUser = findUserForRef(ref, directoryUsers);
      userChildren.push({
        label: (matchedUser
          ? `${matchedUser.name} (${pLabel}${connTag} · ${matchedUser.department})`
          : `${ref.displayName || ref.targetId} (${pLabel}${connTag})`) + disabledSuffix,
        value: val,
        disabled: !isConnected
      });
    }
    seenValues.add(val);
  }

  const activeIds = new Set<string>([
    ...(emp.unifiedAccess.whitelistMemberIds || []),
    ...(emp.unifiedAccess.blacklistMemberIds || []),
    ...extraIds
  ]);

  for (const id of activeIds) {
    if (seenValues.has(id)) continue;
    const parsed = parseCanonicalPrincipalKey(id);
    if (parsed) {
      const isConnected = isSourceConnected(parsed.channel, parsed.connectionId);
      const disabledSuffix = !isConnected ? ' [未接入该渠道，不可选]' : '';
      const pLabel = platformNameMap[parsed.channel] || parsed.channel;
      const connTag = parsed.connectionId && parsed.channel !== 'web' ? ` · ${parsed.connectionId}` : '';
      if (parsed.targetType === 'department') {
        deptChildren.push({
          label: `[部门] ${pLabel}${connTag} · ${parsed.targetId}${disabledSuffix}`,
          value: id,
          disabled: !isConnected
        });
      } else {
        userChildren.push({
          label: `${parsed.targetId} (${pLabel}${connTag})${disabledSuffix}`,
          value: id,
          disabled: !isConnected
        });
      }
      seenValues.add(id);
      continue;
    }
    const matchedUser = directoryUsers.find(u => u.id === id || u.refId === id);
    const matchedDept = directoryDepartments.find(d => d.id === id || d.refId === id);
    if (matchedDept) {
      const isConnected = isSourceConnected(matchedDept.platform, matchedDept.connectionId);
      const disabledSuffix = !isConnected ? ' [未接入该渠道，不可选]' : '';
      const pLabel = matchedDept.platform === 'web' ? '网页' : matchedDept.platform === 'feishu' ? '飞书' : matchedDept.platform === 'wecom' ? '企微' : '钉钉';
      deptChildren.push({
        label: `[部门] ${pLabel} · ${matchedDept.name}${departmentCountLabel(matchedDept)}${disabledSuffix}`,
        value: id,
        disabled: !isConnected
      });
      seenValues.add(id);
    } else if (matchedUser) {
      const isConnected = isSourceConnected(matchedUser.platform, matchedUser.connectionId);
      const disabledSuffix = !isConnected ? ' [未接入该渠道，不可选]' : '';
      const pLabel = platformNameMap[matchedUser.platform] || matchedUser.platform;
      userChildren.push({
        label: `${matchedUser.name} (${pLabel} · ${matchedUser.department})${disabledSuffix}`,
        value: id,
        disabled: !isConnected
      });
      seenValues.add(id);
    }
  }

  return [
    {
      type: 'group' as const,
      label: '组织部门架构',
      key: 'grp-depts',
      children: deptChildren
    },
    {
      type: 'group' as const,
      label: '组织成员',
      key: 'grp-users',
      children: userChildren
    }
  ];
}

export function mergeConnectionDirectoryData(
  existingUsers: ChannelDirectoryUser[],
  existingDepts: ChannelDirectoryDepartment[],
  res: ConnectionDirectoryQueryResult
): { users: ChannelDirectoryUser[]; departments: ChannelDirectoryDepartment[] } {
  const nextUsers = [...existingUsers];
  for (const u of res.users) {
    const connId = u.connectionId || res.connectionId;
    const idx = nextUsers.findIndex(item => item.id === u.id && (item.connectionId || '') === connId);
    const withMeta: ChannelDirectoryUser = {
      ...u,
      sourceType: res.sourceType,
      connectionId: connId
    };
    if (idx >= 0) nextUsers[idx] = withMeta;
    else nextUsers.push(withMeta);
  }

  const nextDepts = [...existingDepts];
  for (const d of res.departments) {
    const connId = d.connectionId || res.connectionId;
    const idx = nextDepts.findIndex(item => item.id === d.id && (item.connectionId || '') === connId);
    const withMeta: ChannelDirectoryDepartment = {
      ...d,
      sourceType: res.sourceType,
      connectionId: connId
    };
    if (idx >= 0) nextDepts[idx] = withMeta;
    else nextDepts.push(withMeta);
  }

  return { users: nextUsers, departments: nextDepts };
}

export interface ManualCredFormInput {
  appId: string;
  appSecret: string;
  botId: string;
  botSecret: string;
  appKey: string;
}

export function buildManualCredPayload(platform: PlatformType, form: ManualCredFormInput) {
  if (platform === 'wecom') {
    return { botId: form.botId.trim(), botSecret: form.botSecret.trim() };
  }
  if (platform === 'dingtalk') {
    return { appKey: form.appKey.trim(), appSecret: form.appSecret.trim() };
  }
  return { appId: form.appId.trim(), appSecret: form.appSecret.trim() };
}

export function validateManualCredentials(platform: PlatformType, form: ManualCredFormInput): string | null {
  const isMasked = (val: string) => val.includes('•') || /^\*{4,}$/.test(val.trim());
  if (platform === 'wecom') {
    if (!form.botId.trim()) return '请填写企业微信智能机器人 BotID';
    if (!form.botSecret.trim()) return '请填写智能机器人 Bot Secret';
    if (isMasked(form.botSecret)) return '请输入完整的 Bot Secret，请勿提交脱敏占位符';
  } else if (platform === 'dingtalk') {
    if (!form.appKey.trim()) return '请填写钉钉开放平台 AppKey';
    if (!form.appSecret.trim()) return '请填写钉钉应用 AppSecret';
    if (isMasked(form.appSecret)) return '请输入完整的 AppSecret，请勿提交脱敏占位符';
  } else {
    if (!form.appId.trim()) return '请填写飞书开放平台 App ID';
    if (!form.appSecret.trim()) return '请填写飞书应用 App Secret';
    if (isMasked(form.appSecret)) return '请输入完整的 App Secret，请勿提交脱敏占位符';
  }
  return null;
}

const VALID_WEBHOOK_TRIGGER_ACTIONS: TriggerAllowedAction[] = ['create_task', 'supplement_task', 'cancel_task'];

/**
 * Normalize trigger allowedActions according to trigger type:
 * schedule and github_issue triggers only allow 'create_task'.
 */
export function normalizeTriggerAllowedActions(
  type: EmployeeTrigger['type'],
  actions?: TriggerAllowedAction[]
): TriggerAllowedAction[] {
  if (type === 'schedule' || type === 'github_issue') {
    return ['create_task'];
  }
  const filtered = Array.isArray(actions)
    ? Array.from(new Set(actions.filter((a): a is TriggerAllowedAction => VALID_WEBHOOK_TRIGGER_ACTIONS.includes(a))))
    : [];
  return filtered.length > 0 ? filtered : ['create_task'];
}

export function computeBusinessKeyPreview(mode?: string, fixed?: string, pattern?: string, dateOverride?: Date): string {
  if (mode === 'empty') return '（无业务键，每次触发独立）';
  if (mode === 'fixed') return fixed || 'audit-default';
  const pat = pattern || 'audit-${YYYYMMDD}';
  const now = dateOverride || new Date();
  const yyyy = now.getFullYear();
  const mm = String(now.getMonth() + 1).padStart(2, '0');
  const dd = String(now.getDate()).padStart(2, '0');
  return pat
    .replace('${YYYYMMDD}', `${yyyy}${mm}${dd}`)
    .replace('${YYYY-MM-DD}', `${yyyy}-${mm}-${dd}`)
    .replace('${DATE}', `${yyyy}${mm}${dd}`);
}

export function appendObservedChannelBindings(
  employee: DigitalEmployee,
  bindings: {channel:EmployeeChannelBinding;revision:number}[] = [],
) {
  for (const {channel,revision} of bindings) {
    if (employee.channels.some(item => item.platform === channel.platform)) continue;
    employee.channels.push(structuredClone(channel));
    if (employee.resourceVersions) employee.resourceVersions.channels[channel.platform] = revision;
  }
}

export function patchChannelRuntimeStatusFields(
  targetCh: EmployeeChannelBinding,
  src: {
    connectionId?: string | undefined;
    status: EmployeeChannelBinding['status'];
    statusText: string;
    credentialStatus?: EmployeeChannelBinding['credentialStatus'];
    connectionState?: EmployeeChannelBinding['connectionState'];
    permissionReady?: boolean | undefined;
    identityConfirmed?: boolean | undefined;
    directoryStatus?: EmployeeChannelBinding['directoryStatus'];
    dingtalkProactiveSendPermissionMissing?: EmployeeChannelBinding['dingtalkProactiveSendPermissionMissing'];
    businessCapabilities?: EmployeeChannelBinding['businessCapabilities'];
    lastVerifiedAt?: string | undefined;
    errorDetail?: string | undefined;
    connectionErrorCode?: string | undefined;
  }
) {
  targetCh.status = src.status;
  targetCh.statusText = src.statusText;
  if (src.credentialStatus !== undefined) targetCh.credentialStatus = src.credentialStatus;
  if (src.connectionState !== undefined) targetCh.connectionState = src.connectionState;
  if ('permissionReady' in src) targetCh.permissionReady = src.permissionReady;
  if ('identityConfirmed' in src) targetCh.identityConfirmed = src.identityConfirmed;
  if ('directoryStatus' in src) targetCh.directoryStatus = src.directoryStatus;
  if ('dingtalkProactiveSendPermissionMissing' in src) targetCh.dingtalkProactiveSendPermissionMissing = src.dingtalkProactiveSendPermissionMissing;
  if ('businessCapabilities' in src) targetCh.businessCapabilities = src.businessCapabilities;
  if (src.lastVerifiedAt !== undefined) targetCh.lastVerifiedAt = src.lastVerifiedAt;
  targetCh.errorDetail = src.errorDetail;
  targetCh.connectionErrorCode = src.connectionErrorCode;
  if (!targetCh.connectionId && src.connectionId) {
    targetCh.connectionId = src.connectionId;
  }
}

export function patchVerifiedWecomOnEmployee(
  empObj: DigitalEmployee,
  verifiedUserId: string,
  verifiedConnId?: string,
  verifiedBotId?: string
) {
  for (const r of empObj.humanConfirmationRules) {
    const ruleConn = r.target.connectionId || '';
    const sameUser = r.target.userId === verifiedUserId || r.target.targetId === verifiedUserId;
    const sameScope = !verifiedConnId || !ruleConn || ruleConn === verifiedConnId;
    if (r.target.provider === 'wecom' && sameUser && sameScope) {
      r.target.identityVerified = true;
      r.target.bindingStatus = 'verified';
      if (verifiedConnId) r.target.connectionId = verifiedConnId;
      if (verifiedBotId) r.target.botId = verifiedBotId;
    }
  }
  for (const ch of empObj.channels) {
    if (ch.platform === 'wecom' && (!verifiedConnId || !ch.connectionId || ch.connectionId === verifiedConnId)) {
      for (const sr of ch.sharingRules) {
        const targetId = sr.targetUser?.targetId || sr.targetUserId;
        if (targetId === verifiedUserId && sr.syncStatus === 'auth_error') {
          sr.syncStatus = 'applied';
          sr.authErrorDetail = undefined;
        }
      }
    }
  }
}

export const EMPLOYEE_ROLE_PLACEHOLDER = [
  '例如',
  '【服务对象】研发团队',
  '【负责事项】收集故障线索与日志',
  '【交付成果】故障摘要和跟进清单'
].join('\n');

export const EMPLOYEE_ROLE_PRESETS = [
  {
    key: 'target',
    label: '+ 服务对象',
    tag: '【服务对象】',
    example: '【服务对象】研发团队'
  },
  {
    key: 'duty',
    label: '+ 负责事项',
    tag: '【负责事项】',
    example: '【负责事项】收集故障线索与日志'
  },
  {
    key: 'deliverable',
    label: '+ 交付成果',
    tag: '【交付成果】',
    example: '【交付成果】故障摘要和跟进清单'
  }
] as const;

export const PERSONA_CONSTRAINT_PRESETS = [
  {
    key: 'scope',
    label: '+ 超范围处理',
    snippet: '【超范围处理】遇到职责范围外的请求，说明当前可以协助的部分。'
  },
  {
    key: 'concise',
    label: '+ 结论先行',
    snippet: '【回答风格】语言精炼、结论先行，先给出明确判断与建议，再补充关键依据与步骤。'
  },
  {
    key: 'friendly',
    label: '+ 亲和耐心',
    snippet: '【沟通语气】保持友好、专业、耐心的服务态度，避免使用生硬或晦涩的技术黑话。'
  },
  {
    key: 'confirm',
    label: '+ 高风险先确认',
    snippet: '【行为红线】涉及线上变更、数据修改或对外承诺等高风险事项时，必须先向负责人确认后再执行。'
  }
] as const;

export const PERSONA_CONSTRAINT_PLACEHOLDER = [
  '例如',
  '【回复方式】先说结论，再列依据和下一步。',
  '【信息不足】说明缺什么，先问清关键条件。',
  '【确认前提】修改生产配置前，先请值班负责人确认。'
].join('\n');

function hasTextSnippet(current: string | undefined, snippet: string): boolean {
  return (current || '').includes(snippet);
}

function appendTextSnippet(current: string | undefined, snippet: string): string {
  if (hasTextSnippet(current, snippet)) return current || '';
  const trimmed = (current || '').trimEnd();
  return trimmed ? `${trimmed}\n${snippet}` : snippet;
}

/** Return whether a role structure tag is already present in the responsibility text. */
export function isEmployeeRolePresetApplied(current: string | undefined, tag: string): boolean {
  return hasTextSnippet(current, tag);
}

/** Append a role structure tag onto a new line when not yet present. */
export function appendEmployeeRolePreset(current: string | undefined, tag: string): string {
  return appendTextSnippet(current, tag);
}

export function isPersonaConstraintApplied(current: string | undefined, snippet: string): boolean {
  return hasTextSnippet(current, snippet);
}

export function appendPersonaConstraint(current: string | undefined, snippet: string): string {
  return appendTextSnippet(current, snippet);
}

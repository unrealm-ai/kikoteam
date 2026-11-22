import { usageApi, type UsageResource, type UsageSelector } from './agent-usage';
import { D2ApiError, activeWorkspaceId } from './client';
import { buildCanonicalPrincipalKey, type PrincipalRef, type UnifiedAccessPolicy } from '../types';
import { getWorkforcePlatformSnapshot, getWorkforcePlatformEmployee, invalidateWorkforcePlatformSnapshot } from './workforce-platform-data';

export type WorkforceAccessPrincipal = PrincipalRef & { usageSelector?: UsageSelector };
const selectorKey = (value: UsageSelector) => buildCanonicalPrincipalKey({channel:value.channel,connectionId:value.binding_id || '',targetType:value.kind === 'department' ? 'department' : 'user',targetId:value.id});
/** Keep the authority-bearing provider identity alongside the original UI DTO.
 * Never infer identity_epoch from the employee's current channel binding. */
export function accessSelectors(ids: string[], refs: PrincipalRef[] | undefined, existing: UsageSelector[]): UsageSelector[] {
  const values = ids.map(id => {
    const ref = (refs || []).find(item => (item.canonicalKey || buildCanonicalPrincipalKey(item)) === id) as WorkforceAccessPrincipal | undefined;
    const saved = existing.find(item => item.kind !== 'group' && selectorKey(item) === id);
    const selector = ref?.usageSelector || saved;
    if (!selector || !selector.id || selector.kind === 'group') throw new Error('所选身份缺少真实授权信息，请从通讯录重新选择，不能仅按名称或 ID 授权');
    if (selectorKey(selector) !== id || (ref && (ref.channel !== selector.channel || ref.targetType !== selector.kind || ref.targetId !== selector.id || ref.connectionId !== (selector.binding_id || '')))) throw new Error('所选身份与渠道授权信息不一致，请重新选择');
    if (selector.channel !== 'web' && (!selector.binding_id || !Number.isSafeInteger(selector.identity_epoch) || selector.identity_epoch! <= 0)) throw new Error('渠道身份版本缺失，请重新选择');
    return JSON.parse(JSON.stringify(selector));
  });
  return values;
}
/** Ignore presentation metadata, but include all authority-bearing selector fields for real policies. */
export function workforceAccessIsDirty(current: UnifiedAccessPolicy, saved: UnifiedAccessPolicy | undefined, real: boolean): boolean {
  if (!saved) return false;
  const persistedSelectors = (policy: UnifiedAccessPolicy) =>
    [policy.whitelistTargets, policy.blacklistTargets].map(list => (list || []).map(ref => {
      const v = (ref as WorkforceAccessPrincipal).usageSelector;
      return v ? [v.channel, v.kind, v.id, v.binding_id, v.identity_epoch, v.include_descendants] : null;
    }));
  return current.mode !== saved.mode ||
    (current.allowGroupMembers ?? true) !== (saved.allowGroupMembers ?? true) ||
    JSON.stringify(current.whitelistMemberIds || []) !== JSON.stringify(saved.whitelistMemberIds || []) ||
    JSON.stringify(current.blacklistMemberIds || []) !== JSON.stringify(saved.blacklistMemberIds || []) ||
    (real && JSON.stringify(persistedSelectors(current)) !== JSON.stringify(persistedSelectors(saved)));
}

export function workforceAccessRequiresReview(access: UsageResource['usage_config']['access']): boolean {
  return [...access.allow, ...access.deny].some(item => item.kind === 'group') ||
    (access.mode === 'allowlist' && access.deny.length > 0) ||
    (access.mode === 'all' && access.allow.length > 0);
}
export function replaceWorkforceAccess(current: UsageResource, access: UnifiedAccessPolicy, replaceLegacyAccess = false): UsageResource {
  if (!['all_with_blacklist','whitelist_with_blacklist'].includes(access.mode)) throw new Error('不支持的使用范围模式');
  if (workforceAccessRequiresReview(current.usage_config.access) && !replaceLegacyAccess) throw new D2ApiError(409, 'usage_policy_requires_review', '既有使用范围包含新模式无法表示的规则，需复核；本次未保存或转换');
  const next = structuredClone(current);
  next.usage_config.access = {
    mode: access.mode === 'all_with_blacklist' ? 'all' : 'allowlist',
    group_members_enabled: access.allowGroupMembers ?? true,
    allow: access.mode === 'all_with_blacklist' ? [] : accessSelectors(access.whitelistMemberIds, access.whitelistTargets, current.usage_config.access.allow),
    deny: access.mode === 'all_with_blacklist' ? accessSelectors(access.blacklistMemberIds, access.blacklistTargets, current.usage_config.access.deny) : []
  };
  if (replaceLegacyAccess && workforceAccessRequiresReview(current.usage_config.access)) Object.assign(next, {replace_legacy_access:true});
  return next;
}
const defaultUsageDependencies = {snapshot:getWorkforcePlatformSnapshot,save:usageApi.save,invalidate:invalidateWorkforcePlatformSnapshot,reload:getWorkforcePlatformEmployee};
export function createWorkforceUsageActions(workspace = () => activeWorkspaceId.value, dependencies = defaultUsageDependencies) {
  return {
    async saveEmployeeRules(id: string, payload: Record<string, unknown>, expectedVersion?: number) {
      if (Object.keys(payload).some(key => !['unifiedAccess','_usageVersion','_replaceLegacyAccess'].includes(key)) || !payload.unifiedAccess) throw new Error('人工确认、交接和其他规则尚未接入真实后端，本次未保存');
      if (!Number.isSafeInteger(payload._usageVersion) || Number(payload._usageVersion) <= 0) throw new Error('使用范围草稿版本缺失，请重新加载后再保存');
      const w = workspace(), current = dependencies.snapshot(w, id);
      if (!current) throw new Error('请先重新加载员工使用范围');
      if (expectedVersion !== undefined && expectedVersion !== current.employee.version) throw new D2ApiError(409,'version_conflict','员工已更新，请重新读取后核对');
      const usage = current.usage ?? await usageApi.get(w, id, AbortSignal.timeout(20000));
      const next = replaceWorkforceAccess(usage, payload.unifiedAccess as UnifiedAccessPolicy, payload._replaceLegacyAccess === true);
      // Other cards may refresh the cache. Only the version captured with this
      // access draft can authorize its write; never silently rebase on cache.
      next.version = payload._usageVersion as number;
      const saved = await dependencies.save(w, id, next, AbortSignal.timeout(20000));
      dependencies.invalidate(w, id);
      try { return await dependencies.reload(w, id, undefined, {usage:saved}); }
      catch { throw new Error('使用范围已保存，但重新读取员工失败；请刷新核对，不要重复提交'); }
    }
  };
}

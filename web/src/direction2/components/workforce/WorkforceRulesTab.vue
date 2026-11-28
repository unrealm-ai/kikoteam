<script setup lang="ts">
import { computed, onUnmounted, ref, watch } from 'vue';
import {
  NAlert, NButton, NCheckbox, NForm, NFormItem, NInput, NModal, NRadio, NRadioGroup,
  NSelect, NSpace, NSwitch, NTag, NTooltip, useMessage
} from 'naive-ui';
import WorkforceSharingRules from './WorkforceSharingRules.vue';
import WorkReceiverPolicy from '../WorkReceiverPolicy.vue';
import { realPlatform } from '../../api/platform';
import { useWorkforceAccessLabels, workforceAccessIdentityKey } from '../../composables/useWorkforceAccessLabels';
import { workforceAccessIsDirty, workforceAccessRequiresReview, type WorkforceAccessPrincipal } from '../../api/workforce-usage';
import { getWorkforcePlatformSnapshot } from '../../api/workforce-platform-data';
import { activeWorkspaceId } from '../../api/client';
import { useWorkforcePorts } from '../../api/workforce-ports';
import type { DirectoryPickItem } from '../UnifiedDirectoryPicker.vue';
import {
  buildPrincipalRefsFromIds,
  buildUnifiedMemberAndDeptOptions,
  getDirectoryUserCanonicalKey,
  isEmployeeSourceConnected,
  parseCanonicalPrincipalKey,
  patchVerifiedWecomOnEmployee,
  resolveMemberKeyToCanonicalKey
} from '../../utils/workforce';
import {
  buildCanonicalPrincipalKey,
  type ChannelDirectoryDepartment, type ChannelDirectoryUser, type DigitalEmployee,
  type HumanConfirmationRule, type PrincipalRef, type WecomVerifySession
} from '../../types';

const { resourcesApi, workforceApi } = useWorkforcePorts();

const props = withDefaults(defineProps<{
  canManage?: boolean;
  isActiveTab?: boolean;
  employee: DigitalEmployee;
  savedEmployee: DigitalEmployee | null;
  employees: DigitalEmployee[];
  directoryUsers: ChannelDirectoryUser[];
  directoryDepartments: ChannelDirectoryDepartment[];
  isDevMode: boolean;
  syncServerFieldIntoSnapshot: <K extends keyof DigitalEmployee>(
    empId: string,
    savedRaw: DigitalEmployee,
    fields: K[]
  ) => void;
  handleOptimisticConflictError: (err: unknown) => boolean;
}>(), {canManage:true});

const emit = defineEmits<{
  dirty: [];
  'open-picker': [
    purpose: 'access_blacklist' | 'access_whitelist' | 'confirmation_rule',
    selectedIds: string[],
    selectedTargets: PrincipalRef[]
  ];
  'wecom-verified': [payload: {
    empId: string;
    verifiedUserId: string;
    verifiedConnId?: string | undefined;
    verifiedBotId?: string | undefined;
  }];
}>();

const message = useMessage();
const usageLoading = ref(false);
const usageError = ref('');
const usageRetry = ref(0);
const usageReady = computed(() => !realPlatform || (
  !usageLoading.value && !usageError.value && props.employee.resourceVersions?.usageAccess !== undefined
));
const legacyAccess = computed(() => {
  if (!realPlatform) return undefined;
  void props.savedEmployee;
  const access = getWorkforcePlatformSnapshot(activeWorkspaceId.value, props.employee.id)?.usage?.usage_config.access;
  return access && workforceAccessRequiresReview(access) ? access : undefined;
});


const reviewingLegacyAccess = ref(false);
const legacyReplacementConfirmed = ref(false);
watch(() => [props.employee.id, activeWorkspaceId.value, props.savedEmployee?.resourceVersions?.usageAccess], () => { reviewingLegacyAccess.value = false; legacyReplacementConfirmed.value = false; });
watch(() => JSON.stringify(props.employee.unifiedAccess), () => { legacyReplacementConfirmed.value = false; });

const usageRead = ref(0);
const scopedAccessRefs = computed(() => [
  ...(props.employee.unifiedAccess.whitelistTargets || []),
  ...(props.employee.unifiedAccess.blacklistTargets || []),
] as WorkforceAccessPrincipal[]);
const bindingRevision = computed(() => JSON.stringify(props.employee.channels.map(c => [
  c.platform, c.connectionId, c.connectionGeneration, c.identityEpoch, c.configRevision,
  c.configurationChanged, c.identityConfirmed, c.status, c.directoryStatus?.status, c.directoryStatus?.snapshot_version,
])));
const labelScope = computed(() => {
  const workspace = activeWorkspaceId.value;
  if (!realPlatform || props.isActiveTab === false || !props.canManage || !workspace || !props.employee.id) return null;
  const changed = new Set<string>(props.employee.channels.filter(c => c.configurationChanged).map(c => c.platform));
  return {
    workspace, employee: props.employee.id, bindingRevision: bindingRevision.value,
    usageRevision: props.employee.resourceVersions?.usageAccess, usageRead: usageRead.value,
    selectors: scopedAccessRefs.value.flatMap(ref => ref.usageSelector && !changed.has(ref.usageSelector.channel) ? [ref.usageSelector] : []),
  };
});
const resolvedTargetNames = useWorkforceAccessLabels(() => labelScope.value);

const unifiedMemberAndDeptOptions = computed(() => {
  const groups = buildUnifiedMemberAndDeptOptions(props.employee, props.directoryUsers, props.directoryDepartments);
  const names = resolvedTargetNames.value;
  if (!realPlatform || !scopedAccessRefs.value.length) return groups;
  // The select's legacy value omits the epoch. Only overlay a resolved name
  // when this visible value has exactly one scoped identity.
  const scopedKeys = new Map<string, string | null>();
  for (const ref of scopedAccessRefs.value) {
    if (!ref.usageSelector) continue;
    const visibleKey = ref.canonicalKey || buildCanonicalPrincipalKey(ref);
    const identityKey = workforceAccessIdentityKey(ref.usageSelector);
    scopedKeys.set(visibleKey, scopedKeys.has(visibleKey) && scopedKeys.get(visibleKey) !== identityKey ? null : identityKey);
  }
  const pNames: Record<string, string> = { web: '网页', feishu: '飞书', wecom: '企业微信', dingtalk: '钉钉' };
  return groups.map(group => ({
    ...group,
    children: group.children.map(opt => {
      if (!scopedKeys.has(opt.value)) return opt;
      const identityKey = scopedKeys.get(opt.value);
      const parsed = parseCanonicalPrincipalKey(opt.value);
      if (!parsed) return opt;
      // An unscoped directory label can refer to a different epoch of this ID.
      const resolved = (identityKey ? names.get(identityKey) : undefined) || parsed.targetId;
      const pLabel = pNames[parsed.channel] || parsed.channel;
      const connTag = parsed.connectionId && parsed.channel !== 'web' ? ` · ${parsed.connectionId}` : '';
      const suffix = opt.disabled ? ' [未接入该渠道，不可选]' : '';
      return {
        ...opt,
        label: parsed.targetType === 'department'
          ? `[部门] ${pLabel}${connTag} · ${resolved}${suffix}`
          : `${resolved} (${pLabel}${connTag})${suffix}`
      };
    })
  }));
});

function onAccessMemberIdsChange(field: 'whitelist' | 'blacklist', ids: string[]) {
  if (!props.canManage || !usageReady.value || (legacyAccess.value && !reviewingLegacyAccess.value)) return;
  const resolved = ids.map(id =>
    resolveMemberKeyToCanonicalKey(
      id,
      field === 'whitelist' ? props.employee.unifiedAccess.whitelistTargets : props.employee.unifiedAccess.blacklistTargets,
      props.directoryUsers,
      props.directoryDepartments
    )
  );
  if (field === 'whitelist') {
    props.employee.unifiedAccess.whitelistMemberIds = resolved;
    props.employee.unifiedAccess.whitelistTargets = buildPrincipalRefsFromIds(
      resolved,
      props.employee.unifiedAccess.whitelistTargets || [],
      props.directoryUsers,
      props.directoryDepartments
    );
  } else {
    props.employee.unifiedAccess.blacklistMemberIds = resolved;
    props.employee.unifiedAccess.blacklistTargets = buildPrincipalRefsFromIds(
      resolved,
      props.employee.unifiedAccess.blacklistTargets || [],
      props.directoryUsers,
      props.directoryDepartments
    );
  }
  emit('dirty');
}

const isSavingAccessScope = ref(false);
const isSavingHandoffScope = ref(false);
const isSavingConfirmationRules = ref(false);

async function persistRulesSection<K extends 'unifiedAccess' | 'handoffPolicy' | 'humanConfirmationRules'>(
  loadingFlag: { value: boolean },
  payload: Record<string, unknown>,
  field: K,
  successMsg: string,
  errorMsg: string
): Promise<boolean> {
  const draft = props.employee;
  if (!props.canManage || loadingFlag.value) return false;
  loadingFlag.value = true;
  try {
    const saved = await workforceApi.saveEmployeeRules(draft.id, payload, draft.version);
    props.syncServerFieldIntoSnapshot(draft.id, saved, [field]);
    message.success(successMsg);
    return true;
  } catch (err) {
    if (!props.handleOptimisticConflictError(err)) {
      message.error(err instanceof Error ? err.message : errorMsg);
    }
    return false;
  } finally {
    loadingFlag.value = false;
  }
}

const isAccessScopeDirty = computed(() =>
  workforceAccessIsDirty(props.employee.unifiedAccess, props.savedEmployee?.unifiedAccess, realPlatform),
);

watch(
  () => [props.isActiveTab, activeWorkspaceId.value, props.employee.id, props.canManage, usageRetry.value] as const,
  async ([active, workspace, id, canManage], _, onCleanup) => {
    usageLoading.value = false;
    usageError.value = '';
    if (!realPlatform || !active || !canManage || !workforceApi.getEmployeeUsage || isAccessScopeDirty.value) return;
    const controller = new AbortController();
    onCleanup(() => controller.abort());
    usageLoading.value = true;
    try {
      const saved = await workforceApi.getEmployeeUsage(id, workspace, controller.signal);
      if (!controller.signal.aborted) {
        props.syncServerFieldIntoSnapshot(id, saved, ['unifiedAccess', 'webChatAccess']);
        usageRead.value++;
      }
    } catch (error) {
      if (!controller.signal.aborted) usageError.value = error instanceof Error ? error.message : '读取使用范围失败';
    } finally {
      if (!controller.signal.aborted) usageLoading.value = false;
    }
  },
  {immediate: true},
);

function cancelAccessScope() {
  reviewingLegacyAccess.value = false;
  legacyReplacementConfirmed.value = false;
  if (!props.savedEmployee?.unifiedAccess) return;
  props.employee.unifiedAccess = JSON.parse(JSON.stringify(props.savedEmployee.unifiedAccess));
  emit('dirty');
}

async function saveAccessScope() {
  if (!props.canManage || !usageReady.value || (legacyAccess.value && !legacyReplacementConfirmed.value)) return;
  const draft = props.employee;
  const normalizedAccess = {
    ...draft.unifiedAccess,
    allowGroupMembers: draft.unifiedAccess.allowGroupMembers ?? true,
    whitelistTargets: buildPrincipalRefsFromIds(
      draft.unifiedAccess.whitelistMemberIds,
      draft.unifiedAccess.whitelistTargets || [],
      props.directoryUsers,
      props.directoryDepartments
    ),
    blacklistTargets: buildPrincipalRefsFromIds(
      draft.unifiedAccess.blacklistMemberIds,
      draft.unifiedAccess.blacklistTargets || [],
      props.directoryUsers,
      props.directoryDepartments
    )
  };
  await persistRulesSection(
    isSavingAccessScope,
    { unifiedAccess: normalizedAccess, ...(realPlatform ? {_usageVersion:draft.resourceVersions?.usageAccess, ...(legacyAccess.value && legacyReplacementConfirmed.value ? {_replaceLegacyAccess:true} : {})} : {}) },
    'unifiedAccess',
    'AI 员工使用范围已保存！',
    '保存使用范围失败，当前修改已保留'
  );
}

const isHandoffScopeDirty = computed(() => {
  const snap = props.savedEmployee?.handoffPolicy;
  if (!snap) return false;
  const cur = props.employee.handoffPolicy;
  return cur.mode !== snap.mode ||
    Boolean(cur.allowAll) !== Boolean(snap.allowAll) ||
    JSON.stringify(cur.allowEmployeeIds || []) !== JSON.stringify(snap.allowEmployeeIds || []) ||
    JSON.stringify(cur.denyEmployeeIds || []) !== JSON.stringify(snap.denyEmployeeIds || []);
});

function cancelHandoffScope() {
  if (!props.savedEmployee?.handoffPolicy) return;
  props.employee.handoffPolicy = JSON.parse(JSON.stringify(props.savedEmployee.handoffPolicy));
  emit('dirty');
}

async function saveHandoffScope() {
  if (!props.canManage || realPlatform) return;
  await persistRulesSection(
    isSavingHandoffScope,
    { handoffPolicy: props.employee.handoffPolicy },
    'handoffPolicy',
    'AI 员工被交接范围已保存！',
    '保存交接范围失败，当前修改已保留'
  );
}

// 人工确认规则
const showAddRuleModal = ref(false);
const newRuleForm = ref<{
  questionScope: string;
  selectedUserId: string;
  selectedTarget: PrincipalRef | null;
  fallbackChannel: string;
}>({
  questionScope: '',
  selectedUserId: '',
  selectedTarget: null,
  fallbackChannel: '平台对话'
});

const confirmationUserOptions = computed(() => {
  const seen = new Set<string>();
  const list: Array<{ label: string; value: string; disabled?: boolean }> = props.directoryUsers.map(u => {
    const value = getDirectoryUserCanonicalKey(u);
    seen.add(value);
    const platLabel = u.platform === 'feishu' ? '飞书' : u.platform === 'wecom' ? '企业微信' : u.platform === 'dingtalk' ? '钉钉' : '工作区网页';
    const connTag = u.connectionId && u.platform !== 'web' ? ` · ${u.connectionId}` : '';
    const verifyTag = u.bindingStatus === 'pending_binding' || u.isWecomVerified === false ? ' · 待核验' : '';
    const connected = isEmployeeSourceConnected(props.employee, u.platform, u.connectionId);
    const unboundTag = !connected ? ' [未接入该渠道，不可选]' : '';
    return {
      label: `${u.name} · ${u.department} (${platLabel}${connTag}${verifyTag})${unboundTag}`,
      value,
      disabled: !connected
    };
  });
  const selTarget = newRuleForm.value.selectedTarget;
  if (selTarget) {
    const selKey = selTarget.canonicalKey || buildCanonicalPrincipalKey(selTarget);
    if (!seen.has(selKey)) {
      const platLabel = selTarget.channel === 'feishu' ? '飞书' : selTarget.channel === 'wecom' ? '企业微信' : selTarget.channel === 'dingtalk' ? '钉钉' : '工作区网页';
      const connTag = selTarget.connectionId && selTarget.channel !== 'web' ? ` · ${selTarget.connectionId}` : '';
      const connected = isEmployeeSourceConnected(props.employee, selTarget.channel, selTarget.connectionId);
      list.unshift({
        label: `${selTarget.displayName || selTarget.targetId} (${platLabel}${connTag})${!connected ? ' [未接入该渠道，不可选]' : ''}`,
        value: selKey,
        disabled: !connected
      });
    }
  }
  return list;
});

function buildPrincipalFromUser(u: ChannelDirectoryUser): PrincipalRef {
  const connectionId = u.connectionId || (u.platform === 'web' ? 'ws-web-directory' : `conn-${u.platform}`);
  const targetId = u.refId || u.id;
  return {
    targetType: 'user',
    channel: u.platform,
    connectionId,
    targetId,
    displayName: u.name,
    canonicalKey: buildCanonicalPrincipalKey({ targetType: 'user', channel: u.platform, connectionId, targetId })
  };
}

function handleConfirmationUserSelect(val: string) {
  if (!props.canManage || realPlatform) return;
  newRuleForm.value.selectedUserId = val;
  const matchedUser = props.directoryUsers.find(u => getDirectoryUserCanonicalKey(u) === val);
  if (matchedUser) {
    newRuleForm.value.selectedTarget = buildPrincipalFromUser(matchedUser);
    return;
  }
  const parsed = parseCanonicalPrincipalKey(val);
  if (parsed) {
    newRuleForm.value.selectedTarget = parsed;
  }
}

function openAddConfirmationRule() {
  if (!props.canManage || realPlatform) return;
  const firstU = props.directoryUsers.find(u => isEmployeeSourceConnected(props.employee, u.platform, u.connectionId));
  newRuleForm.value = {
    questionScope: '',
    selectedUserId: firstU ? getDirectoryUserCanonicalKey(firstU) : '',
    selectedTarget: firstU ? (firstU.principal ? { ...firstU.principal } : buildPrincipalFromUser(firstU)) : null,
    fallbackChannel: '平台对话'
  };
  showAddRuleModal.value = true;
}

async function handleAddConfirmationRule() {
  if (!props.canManage || realPlatform) return;
  if (isSavingConfirmationRules.value) return;
  if (!newRuleForm.value.questionScope.trim()) {
    message.warning('请输入问题范围（如 deploy-prod 或 *）');
    return;
  }
  const selectedKey = newRuleForm.value.selectedUserId;
  const parsedSel = parseCanonicalPrincipalKey(selectedKey);
  const targetHint = newRuleForm.value.selectedTarget || parsedSel;
  const user =
    props.directoryUsers.find(u => getDirectoryUserCanonicalKey(u) === selectedKey) ||
    (targetHint
      ? props.directoryUsers.find(
          u =>
            u.platform === targetHint.channel &&
            (!targetHint.connectionId || !u.connectionId || u.connectionId === targetHint.connectionId) &&
            (u.refId === targetHint.targetId || u.id === targetHint.targetId)
        )
      : props.directoryUsers.find(u => u.id === selectedKey));
  if (!user && !targetHint) {
    message.warning('请先从企业通讯录选择一位确认人员');
    return;
  }
  const channel = targetHint?.channel || user!.platform;
  const targetConnId = targetHint?.connectionId || user?.connectionId;
  if (!isEmployeeSourceConnected(props.employee, channel, targetConnId)) {
    message.warning('当前员工尚未接入该成员所在渠道，无法选为确认人');
    return;
  }
  const matchedCh = props.employee.channels.find(c => c.platform === channel && (!targetHint?.connectionId || c.connectionId === targetHint.connectionId)) ||
    props.employee.channels.find(c => c.platform === channel);
  const connectionId = targetHint?.connectionId || user?.connectionId || matchedCh?.connectionId || (channel === 'web' ? 'ws-web-directory' : `conn-${channel}`);
  const targetId = targetHint?.targetId || user?.refId || user?.id || '';
  const displayName = user?.name || targetHint?.displayName || targetId;
  const canonicalKey = buildCanonicalPrincipalKey({ targetType: 'user', channel, connectionId, targetId });
  const principal: PrincipalRef = {
    targetType: 'user',
    channel,
    connectionId,
    targetId,
    displayName,
    canonicalKey
  };
  const verified = channel === 'wecom' ? (user?.bindingStatus === 'verified' || user?.isWecomVerified === true) : true;

  const newRule: HumanConfirmationRule = {
    id: `hcr-${Date.now()}`,
    questionScope: newRuleForm.value.questionScope.trim(),
    target: {
      kind: channel === 'feishu' || channel === 'dingtalk' || channel === 'wecom' ? 'channel_user' : 'platform_member',
      targetType: 'user',
      label: `${displayName} (${channel === 'feishu' ? '飞书' : channel === 'wecom' ? '企业微信' : channel === 'dingtalk' ? '钉钉' : '网页'})`,
      userId: user?.id || targetId,
      targetId,
      provider: channel,
      channel,
      connectionId,
      botId: channel === 'wecom' ? matchedCh?.botId : undefined,
      principal,
      identityVerified: verified,
      bindingStatus: verified ? 'verified' : 'pending_binding'
    },
    fallbackChannel: newRuleForm.value.fallbackChannel
  };
  const nextRules = [...props.employee.humanConfirmationRules, newRule];
  const ok = await persistRulesSection(
    isSavingConfirmationRules,
    { humanConfirmationRules: nextRules },
    'humanConfirmationRules',
    '已添加人工确认规则！',
    '添加人工确认规则失败'
  );
  if (ok) {
    showAddRuleModal.value = false;
  }
}

async function handleRemoveConfirmationRule(ruleId: string) {
  if (!props.canManage || realPlatform) return;
  const nextRules = props.employee.humanConfirmationRules.filter(item => item.id !== ruleId);
  await persistRulesSection(
    isSavingConfirmationRules,
    { humanConfirmationRules: nextRules },
    'humanConfirmationRules',
    '已移除该人工确认规则',
    '移除人工确认规则失败'
  );
}

function applyPickedConfirmationTarget(item: DirectoryPickItem) {
  if (!props.canManage || realPlatform) return;
  if (item.kind === 'user') {
    const key = item.principal.canonicalKey || buildCanonicalPrincipalKey(item.principal);
    newRuleForm.value.selectedUserId = key;
    newRuleForm.value.selectedTarget = { ...item.principal, canonicalKey: key };
  }
}

function setConfirmationTargetFromTargets(targets: PrincipalRef[]) {
  if (!props.canManage || realPlatform) return;
  if (targets[0]) {
    const key = targets[0].canonicalKey || buildCanonicalPrincipalKey(targets[0]);
    newRuleForm.value.selectedUserId = key;
    newRuleForm.value.selectedTarget = { ...targets[0], canonicalKey: key };
  }
}

// 企微身份核验
const showWecomVerifyModal = ref(false);
const currentVerifyingRule = ref<HumanConfirmationRule | null>(null);
const wecomVerifySession = ref<WecomVerifySession | null>(null);
const wecomVerifyDynamicCode = ref('');
const wecomVerifyStatus = ref<'waiting' | 'verified' | 'expired'>('waiting');
const wecomVerifySimulated = ref(false);
const wecomVerifyLoading = ref(false);
const wecomVerifyError = ref('');
let wecomVerifyPollTimer: ReturnType<typeof setInterval> | null = null;

function stopWecomVerifyPoll() {
  if (wecomVerifyPollTimer) {
    clearInterval(wecomVerifyPollTimer);
    wecomVerifyPollTimer = null;
  }
}

onUnmounted(() => {
  stopWecomVerifyPoll();
});

async function applyVerifiedWecomScope(empId: string, rule: HumanConfirmationRule, polled: WecomVerifySession) {
  if (!props.canManage || realPlatform) return;
  const verifiedUserId = polled.userId || rule.target.userId;
  const verifiedConnId = polled.connectionId || rule.target.connectionId;
  const verifiedBotId = polled.botId || rule.target.botId;

  rule.target.identityVerified = true;
  rule.target.bindingStatus = 'verified';
  rule.target.connectionId = verifiedConnId;
  rule.target.botId = verifiedBotId;

  patchVerifiedWecomOnEmployee(props.employee, verifiedUserId, verifiedConnId, verifiedBotId);
  const masterEmp = props.employees.find(e => e.id === empId);
  if (masterEmp) {
    patchVerifiedWecomOnEmployee(masterEmp, verifiedUserId, verifiedConnId, verifiedBotId);
  }
  const user = props.directoryUsers.find(
    u =>
      (!verifiedConnId || !u.connectionId || u.connectionId === verifiedConnId) &&
      (u.id === verifiedUserId || u.refId === verifiedUserId)
  );
  if (user) {
    user.isWecomVerified = true;
    user.botAuthorized = true;
    user.bindingStatus = 'verified';
  }
  emit('wecom-verified', { empId, verifiedUserId, verifiedConnId, verifiedBotId });
  try {
    const fresh = await workforceApi.getEmployee(empId);
    props.syncServerFieldIntoSnapshot(empId, fresh, []);
  } catch {
    // keep verified marker if already updated on server
  }
}

async function openWecomVerifyModal(rule: HumanConfirmationRule) {
  if (!props.canManage || realPlatform) return;
  stopWecomVerifyPoll();
  const empId = props.employee.id;
  const wecomCh = props.employee.channels.find(c => c.platform === 'wecom');
  const connectionId = rule.target.connectionId || wecomCh?.connectionId || '';
  const botId = rule.target.botId || wecomCh?.botId;

  if (!connectionId) {
    message.error('当前尚未完成企业微信机器人配置，无法发起核验');
    return;
  }

  currentVerifyingRule.value = rule;
  wecomVerifySession.value = null;
  wecomVerifyDynamicCode.value = '';
  wecomVerifyStatus.value = 'waiting';
  wecomVerifySimulated.value = false;
  wecomVerifyError.value = '';
  wecomVerifyLoading.value = true;
  showWecomVerifyModal.value = true;

  try {
    const sess = await resourcesApi.startWecomVerifyCode({
      userId: rule.target.userId,
      employeeId: empId,
      connectionId,
      botId
    });
    if (props.employee.id !== empId || currentVerifyingRule.value?.id !== rule.id) return;
    wecomVerifySession.value = sess;
    wecomVerifyDynamicCode.value = sess.code;
    wecomVerifyStatus.value = sess.status === 'verified' ? 'verified' : 'waiting';
    if (sess.status === 'verified') {
      await applyVerifiedWecomScope(empId, rule, sess);
      return;
    }

    wecomVerifyPollTimer = setInterval(async () => {
      if (!showWecomVerifyModal.value || !currentVerifyingRule.value || props.employee.id !== empId) {
        stopWecomVerifyPoll();
        return;
      }
      try {
        const polled = await resourcesApi.pollWecomVerifySession(sess.verifySessionId);
        if (!showWecomVerifyModal.value || currentVerifyingRule.value?.id !== rule.id || props.employee.id !== empId) {
          stopWecomVerifyPoll();
          return;
        }
        wecomVerifySession.value = polled;
        if (polled.status === 'verified') {
          wecomVerifyStatus.value = 'verified';
          wecomVerifySimulated.value = true;
          stopWecomVerifyPoll();
          await applyVerifiedWecomScope(empId, rule, polled);
          message.success('企业微信成员身份绑定已生效！');
        } else if (polled.status === 'expired') {
          wecomVerifyStatus.value = 'expired';
          stopWecomVerifyPoll();
        }
      } catch {
        // ignore transient poll hiccup
      }
    }, 1500);
  } catch (err) {
    const errMsg = err instanceof Error ? err.message : '申请企微验证码失败';
    wecomVerifyError.value = errMsg;
    message.error(errMsg);
  } finally {
    wecomVerifyLoading.value = false;
  }
}

async function simulateMemberSendCode() {
  if (!props.canManage || realPlatform) return;
  if (!currentVerifyingRule.value || !wecomVerifySession.value) return;
  const empId = props.employee.id;
  const rule = currentVerifyingRule.value;
  try {
    await resourcesApi.simulateWecomDmCallback({
      verifySessionId: wecomVerifySession.value.verifySessionId,
      userId: rule.target.userId,
      connectionId: wecomVerifySession.value.connectionId
    });
    const polled = await resourcesApi.pollWecomVerifySession(wecomVerifySession.value.verifySessionId);
    wecomVerifySession.value = polled;
    if (polled.status === 'verified') {
      wecomVerifyStatus.value = 'verified';
      wecomVerifySimulated.value = true;
      stopWecomVerifyPoll();
      await applyVerifiedWecomScope(empId, rule, polled);
      message.success('企微身份核验已通过！');
    }
  } catch (err) {
    message.error(err instanceof Error ? err.message : '模拟企微私聊验证失败');
  }
}

function verifyWecomIdentity(rule: HumanConfirmationRule) {
  if (!props.canManage || realPlatform) return;
  openWecomVerifyModal(rule);
}

watch(() => props.canManage, allowed => {
  if (allowed) return;
  stopWecomVerifyPoll(); showWecomVerifyModal.value=false; showAddRuleModal.value=false;
});

defineExpose({
  applyPickedConfirmationTarget,
  setConfirmationTargetFromTargets,
  stopWecomVerifyPoll
});
</script>

<template>
  <div class="tab-panel-inner">
    <!-- 1. AI 员工使用范围 -->
    <div class="settings-card-block" :class="{ 'is-dirty-card': usageReady && (isAccessScopeDirty || reviewingLegacyAccess) && (!legacyAccess || reviewingLegacyAccess) }">
      <div class="card-edit-header">
        <h4 class="cb-title">1. AI 员工使用范围</h4>
        <div v-if="usageReady && (isAccessScopeDirty || reviewingLegacyAccess) && (!legacyAccess || reviewingLegacyAccess)" class="card-edit-actions">
          <span class="card-dirty-pill">未保存</span>
          <NButton size="tiny" :disabled="isSavingAccessScope" @click="cancelAccessScope">取消</NButton>
          <NButton :disabled="!canManage || (!!legacyAccess && !legacyReplacementConfirmed)" size="tiny" type="primary" :loading="isSavingAccessScope" @click="saveAccessScope">保存</NButton>
        </div>
      </div>
      <p class="cb-sub">
        统一管理可在 Web 网页、飞书、企业微信、钉钉与该 AI 员工交互的人员与部门范围。
      </p>
      <p v-if="usageLoading" role="status">正在读取使用范围…</p>
      <NAlert v-else-if="usageError" type="error">
        {{ usageError }}
        <NButton size="small" @click="usageRetry++">重试读取使用范围</NButton>
      </NAlert>
      <p v-else-if="!usageReady">需要当前工作区管理权限读取使用范围。</p>
      <NAlert v-if="usageReady && legacyAccess" type="warning" class="legacy-access-review">
        既有使用范围需复核：包含新模式无法表示的规则。原有规则继续保留，不会自动转换或删除。需明确审阅并保存替换。
        <div>原模式：{{ legacyAccess.mode === 'all' ? '全员' : '白名单' }}；群内可用：{{ legacyAccess.group_members_enabled ? '开启' : '关闭' }}</div>
        <div>允许列表（{{ legacyAccess.allow.length }} 项）</div>
        <ul><li v-for="(item, index) in legacyAccess.allow" :key="'allow-' + index">{{ item.channel }} / {{ item.kind }} / {{ item.id }}<span v-if="item.binding_id"> / 渠道绑定 {{ item.binding_id }}</span></li></ul>
        <div>排除列表（{{ legacyAccess.deny.length }} 项）</div>
        <ul><li v-for="(item, index) in legacyAccess.deny" :key="'deny-' + index">{{ item.channel }} / {{ item.kind }} / {{ item.id }}<span v-if="item.binding_id"> / 渠道绑定 {{ item.binding_id }}</span></li></ul>
        <NButton v-if="!reviewingLegacyAccess" :disabled="!canManage" size="small" @click="reviewingLegacyAccess = true">审阅并转换使用范围</NButton>
        <template v-else>
          <p>保存差异：全部旧群主体规则将移除；选择全员模式将移除旧允许列表，选择纯白名单将移除旧排除列表。群内可用开关单独生效，可能改变实际可用人员范围。请核对下方新人员/部门名单。</p>
          <NCheckbox v-model:checked="legacyReplacementConfirmed" :disabled="!canManage || isSavingAccessScope">我已审阅原规则和新范围，确认替换并接受上述权限变化</NCheckbox>
        </template>
      </NAlert>
      <div v-if="usageReady && (!legacyAccess || reviewingLegacyAccess)" class="cb-form">
        <div class="p-row">
          <div class="p-info">
            <strong>使用范围模式</strong>
            <p>支持面向全员开放（可排除黑名单），或向白名单成员/部门开放。</p>
          </div>
          <NRadioGroup
            :disabled="!canManage || (isSavingAccessScope)"
            v-model:value="employee.unifiedAccess.mode"
            @update:value="emit('dirty')"
          >
            <NSpace wrap>
              <NRadio value="all_with_blacklist">全员可用 + 黑名单排除</NRadio>
              <NRadio value="whitelist_with_blacklist">白名单</NRadio>
            </NSpace>
          </NRadioGroup>
        </div>

        <div class="sub-scope-selectors">
          <div v-if="employee.unifiedAccess.mode === 'all_with_blacklist'" class="scope-row">
            <div class="scope-header-line">
              <label style="margin: 0;">禁止使用的人员与部门黑名单（可选）</label>
              <NButton
                size="tiny"
                secondary
                type="primary"
                :disabled="!canManage || (isSavingAccessScope)"
                @click="emit('open-picker', 'access_blacklist', employee.unifiedAccess.blacklistMemberIds, employee.unifiedAccess.blacklistTargets || [])"
              >
                从通讯录选择
              </NButton>
            </div>
            <NSelect
              :disabled="!canManage || (isSavingAccessScope)"
              :value="employee.unifiedAccess.blacklistMemberIds"
              multiple
              filterable
              :options="unifiedMemberAndDeptOptions"
              placeholder="选择禁止访问的成员或部门（可选）"
              @update:value="ids => onAccessMemberIdsChange('blacklist', ids)"
            >
              <template #empty>
                <span class="field-note">{{ realPlatform ? '请点击右上角「从通讯录选择」添加人员或部门' : '暂无可选人员或部门' }}</span>
              </template>
            </NSelect>
          </div>

          <div v-else class="scope-row-dual">
            <div class="dual-item">
              <div class="scope-header-line">
                <label style="margin: 0;">允许使用的人员或部门白名单</label>
                <NButton
                  size="tiny"
                  secondary
                  type="primary"
                  :disabled="!canManage || (isSavingAccessScope)"
                @click="emit('open-picker', 'access_whitelist', employee.unifiedAccess.whitelistMemberIds, employee.unifiedAccess.whitelistTargets || [])"
                >
                  从通讯录选择
                </NButton>
              </div>
              <NSelect
              :disabled="!canManage || (isSavingAccessScope)"
                :value="employee.unifiedAccess.whitelistMemberIds"
                multiple
                filterable
                :options="unifiedMemberAndDeptOptions"
                placeholder="选择允许使用该 AI 员工的人员或部门 (白名单)"
                @update:value="ids => onAccessMemberIdsChange('whitelist', ids)"
              >
                <template #empty>
                  <span class="field-note">{{ realPlatform ? '请点击右上角「从通讯录选择」添加人员或部门' : '暂无可选人员或部门' }}</span>
                </template>
              </NSelect>
            </div>

          </div>

          <div class="p-row scope-group-toggle-row">
            <div class="p-info">
              <div class="scope-toggle-title">
                <strong>群内可用</strong>
                <NTooltip placement="top" :style="{ maxWidth: '340px' }">
                  <template #trigger>
                    <span class="scope-help-badge" tabindex="0" aria-label="群聊可用范围说明">?</span>
                  </template>
                  <div class="scope-tooltip-body">
                    <div><strong>开启：</strong>在群聊中，除上方使用范围配置的人员/部门外，同群内的其他成员也可 @ 或触发该 AI 员工。</div>
                    <div style="margin-top: 6px;"><strong>关闭：</strong>群聊中仅限上方使用范围配置的人员/部门可使用。</div>
                  </div>
                </NTooltip>
              </div>
              <p>
                开启后，同群内的其他成员也可在群聊中使用该 AI 员工；关闭则仅限上方授权成员可用。
              </p>
            </div>
            <NSwitch
              :disabled="!canManage || (isSavingAccessScope)"
              :value="employee.unifiedAccess.allowGroupMembers ?? true"
              @update:value="(val: boolean) => {
                employee.unifiedAccess.allowGroupMembers = val;
                emit('dirty');
              }"
            />
          </div>
        </div>
      </div>
    </div>

    <!-- 2. AI 员工交接接收范围 -->
    <div v-if="!realPlatform" class="settings-card-block" :class="{ 'is-dirty-card': isHandoffScopeDirty }">
      <div class="card-edit-header">
        <h4 class="cb-title">2. AI 员工被交接限制或范围</h4>
        <div v-if="isHandoffScopeDirty" class="card-edit-actions">
          <span class="card-dirty-pill">未保存</span>
          <NButton size="tiny" :disabled="isSavingHandoffScope" @click="cancelHandoffScope">取消</NButton>
          <NButton :disabled="realPlatform || !canManage" size="tiny" type="primary" :loading="isSavingHandoffScope" @click="saveHandoffScope">保存</NButton>
        </div>
      </div>
      <p class="cb-sub">决定哪些同工作区的其他员工可以把业务事项交接给本员工。</p>
      <div class="cb-form">
        <div class="p-row">
          <div class="p-info">
            <strong>交接权限策略</strong>
            <p>支持「全部 + 黑名单」或「指定白名单」两种交接管控模式。</p>
          </div>
          <NRadioGroup :disabled="realPlatform || !canManage"
            v-model:value="employee.handoffPolicy.mode"
            @update:value="v => { employee.handoffPolicy.allowAll = (v === 'all_with_blacklist'); emit('dirty'); }"
          >
            <NSpace wrap>
              <NRadio value="all_with_blacklist">全部员工 + 黑名单排除</NRadio>
              <NRadio value="whitelist">白名单</NRadio>
            </NSpace>
          </NRadioGroup>
        </div>

        <div v-if="employee.handoffPolicy.mode === 'all_with_blacklist'" class="handoff-whitelist-row">
          <label>禁止交接的 AI 员工黑名单（可选）</label>
          <NSelect :disabled="realPlatform || !canManage"
            v-model:value="employee.handoffPolicy.denyEmployeeIds"
            multiple
            :options="employees.filter(e => e.id !== employee.id).map(e => ({ label: `${e.avatar} ${e.name} (${e.code})`, value: e.id }))"
            placeholder="选择禁止交接的员工（可选）"
            @update:value="emit('dirty')"
          />
          <p class="field-note" style="margin-top: 6px;">
            未列出的同工作区 AI 员工均可将事项交接给本员工。
          </p>
        </div>

        <div v-else class="handoff-whitelist-row">
          <label>允许交接的来源 AI 员工白名单 (仅名单内员工可发起交接)</label>
          <NSelect :disabled="realPlatform || !canManage"
            v-model:value="employee.handoffPolicy.allowEmployeeIds"
            multiple
            :options="employees.filter(e => e.id !== employee.id).map(e => ({ label: `${e.avatar} ${e.name} (${e.code})`, value: e.id }))"
            placeholder="选择允许交接的 AI 员工 (白名单)"
            @update:value="emit('dirty')"
          />
          <p class="field-note" style="margin-top: 6px;">
            仅名单内勾选的 AI 员工在遇到超出范围事项时，才有权限将任务流转给本员工。
          </p>
        </div>
      </div>
    </div>
    <template v-else>
      <WorkReceiverPolicy v-if="canManage && isActiveTab !== false && activeWorkspaceId"
        :workspace="activeWorkspaceId" :employee="employee.id" v-slot="state">
        <section class="settings-card-block" :class="{ 'is-dirty-card': state.dirty }" aria-labelledby="employee-receiver-heading">
          <div class="card-edit-header">
            <h4 id="employee-receiver-heading" class="cb-title">2. AI 员工接手范围</h4>
            <div v-if="state.dirty" class="card-edit-actions">
              <span class="card-dirty-pill">未保存</span>
              <NButton size="tiny" :disabled="state.busy" @click="state.discard()">取消</NButton>
              <NButton size="tiny" type="primary" :loading="state.busy" @click="state.save()">保存</NButton>
            </div>
          </div>
          <p class="cb-sub">决定哪些同工作区的其他 AI 员工可以把业务事项交接给本员工。</p>
          <NAlert v-if="state.error" type="error">
            {{ state.error }}
            <NButton size="tiny" :disabled="state.busy" @click="state.reload()">重新读取</NButton>
          </NAlert>
          <p v-if="state.saved" class="field-note" role="status">接手规则已保存</p>
          <p v-if="state.busy && !state.draft" class="field-note" role="status">正在读取接手范围…</p>
          <div v-if="state.draft" class="cb-form">
            <div class="p-row">
              <div class="p-info">
                <strong>交接权限策略</strong>
                <p>支持「全部 + 黑名单」或「指定白名单」两种交接管控模式。</p>
              </div>
              <NRadioGroup :value="state.mode" :disabled="state.busy" aria-label="交接权限策略"
                @update:value="v => state.updateMode(v === 'allowlist' ? 'allowlist' : 'denylist')">
                <NSpace wrap>
                  <NRadio value="denylist">全部员工 + 黑名单排除</NRadio>
                  <NRadio value="allowlist">白名单</NRadio>
                </NSpace>
              </NRadioGroup>
            </div>
            <div class="handoff-whitelist-row">
              <label>{{ state.mode === 'denylist' ? '禁止交接的来源员工（可选）' : '允许交接的来源员工' }}</label>
              <NSelect multiple filterable aria-label="交接来源员工" placeholder="选择同工作区的来源员工"
                :value="state.draft.employee_ids" :options="state.options" :loading="state.optionsBusy" :disabled="state.busy"
                @update:value="state.updateIds" />
              <p v-if="state.optionsError" role="alert">{{ state.optionsError }} <NButton size="tiny" @click="state.loadOptions()">重试</NButton></p>
              <p class="field-note">{{ state.mode === 'denylist' ? '未列出的员工可以交接。黑名单留空时允许全部员工。' : '只有名单内的员工可以交接。白名单留空时不接受任何员工的交接。' }}</p>
            </div>
          </div>
        </section>
      </WorkReceiverPolicy>
      <div v-else-if="!canManage" class="settings-card-block">
        <h4 class="cb-title">2. AI 员工接手范围</h4>
        <p class="cb-sub">仅工作区管理员可查看和配置接手范围。</p>
      </div>
    </template>

    <!-- Mock 人工确认示例 -->
    <div v-if="!realPlatform" class="settings-card-block">
      <div class="cs-header-flex">
        <div>
          <h4 class="cb-title">3. 人工确认接收人与问题范围规则 <NTag v-if="realPlatform" size="tiny">尚未接入</NTag></h4>
          <p class="cb-sub">按问题范围指定确认人，* 表示匹配全部问题。企微确认人需先完成身份核验。</p>
        </div>
        <NButton :disabled="realPlatform || !canManage" size="tiny" type="primary" secondary @click="openAddConfirmationRule">
          + 添加确认规则
        </NButton>
      </div>

      <div class="rules-table-box">
        <table class="custom-d2-table">
          <thead>
            <tr>
              <th>问题范围</th>
              <th>指定确认人</th>
              <th>渠道与身份核验状态</th>
              <th>未回复/失联处置策略</th>
              <th>操作</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="r in employee.humanConfirmationRules" :key="r.id">
              <td><code>{{ r.questionScope }}</code></td>
              <td><strong>{{ r.target.label }}</strong></td>
              <td>
                <span v-if="r.target.provider === 'wecom'">
                  <NTag
                    size="tiny"
                    :type="(r.target.bindingStatus === 'verified' || r.target.identityVerified) ? 'success' : 'warning'"
                    round
                  >
                    {{ (r.target.bindingStatus === 'verified' || r.target.identityVerified) ? '企微身份已核验' : '待企微私聊绑定' }}
                  </NTag>
                  <span v-if="r.target.connectionId || r.target.botId" style="font-size: 12px; color: var(--id-text-secondary); margin-left: 6px;">
                    <code>{{ r.target.botId || r.target.connectionId }}</code>
                  </span>
                  <NButton :disabled="realPlatform || !canManage"
                    v-if="!(r.target.bindingStatus === 'verified' || r.target.identityVerified)"
                    size="tiny"
                    type="warning"
                    quaternary
                    @click="verifyWecomIdentity(r)"
                  >
                    核验并绑定
                  </NButton>
                </span>
                <span v-else>
                  <NTag size="tiny" type="info">平台成员已认证</NTag>
                </span>
              </td>
              <td><span class="text-muted" style="font-size: 12px;">转入待人工确认中心由管理员处理</span></td>
              <td>
                <NButton
                  size="tiny"
                  quaternary
                  type="error"
                  :disabled="realPlatform || !canManage || (isSavingConfirmationRules)"
                  @click="handleRemoveConfirmationRule(r.id)"
                >
                  移除
                </NButton>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
    </div>

    <!-- 群消息共享 -->
    <WorkforceSharingRules v-if="realPlatform" :workspace="activeWorkspaceId" :employee-id="employee.id" :can-manage="canManage" />

    <!-- 添加人工确认规则弹窗 -->
    <NModal v-model:show="showAddRuleModal" preset="card" title="添加人工确认规则" style="width: min(500px, 94vw);">
      <NForm label-placement="top">
        <NFormItem label="确认问题范围（如 deploy-prod，或 * 表示全部）">
          <NInput :disabled="realPlatform || !canManage" v-model:value="newRuleForm.questionScope" placeholder="例如：deploy-prod" />
        </NFormItem>

        <NFormItem label="指定确认人员">
          <div style="width: 100%; display: flex; flex-direction: column; gap: 6px;">
            <NSelect :disabled="realPlatform || !canManage"
              :value="newRuleForm.selectedUserId || null"
              filterable
              placeholder="从企业通讯录中选择确认人员"
              :options="confirmationUserOptions"
              @update:value="handleConfirmationUserSelect"
            />
            <div>
              <NButton :disabled="realPlatform || !canManage"
                size="tiny"
                secondary
                type="primary"
                @click="emit('open-picker', 'confirmation_rule', newRuleForm.selectedUserId ? [newRuleForm.selectedUserId] : [], newRuleForm.selectedTarget ? [newRuleForm.selectedTarget] : [])"
              >
                从企业通讯录筛选人员
              </NButton>
            </div>
          </div>
        </NFormItem>

        <NFormItem label="备用通知渠道">
          <NSelect :disabled="realPlatform || !canManage"
            v-model:value="newRuleForm.fallbackChannel"
            :options="[
              { label: '平台对话', value: '平台对话' },
              { label: '飞书群卡片', value: '飞书群卡片' },
              { label: '企业微信私聊卡片', value: '企业微信私聊卡片' },
              { label: '钉钉群工作通知', value: '钉钉群工作通知' }
            ]"
          />
        </NFormItem>
      </NForm>
      <template #footer>
        <div style="display: flex; justify-content: flex-end; gap: 8px; flex-wrap: wrap;">
          <NButton :disabled="isSavingConfirmationRules" @click="showAddRuleModal = false">取消</NButton>
          <NButton :disabled="realPlatform || !canManage" type="primary" :loading="isSavingConfirmationRules" @click="handleAddConfirmationRule">添加规则</NButton>
        </div>
      </template>
    </NModal>

    <!-- 企业微信成员身份核验弹窗 -->
    <NModal v-model:show="showWecomVerifyModal" preset="card" title="企业微信成员身份核验" style="width: min(540px, 94vw);">
      <div class="wecom-verify-body">
        <NAlert type="info" :show-icon="true" style="margin-bottom: 14px;">
          请由该成员本人在企业微信中向对应机器人私聊发送下方 6 位验证码，发送后将自动完成身份核验。
        </NAlert>

        <div class="wv-user-card" style="background: var(--id-bg-subtle); border: 1px solid var(--id-border-subtle); padding: 12px; border-radius: var(--id-radius-md); margin-bottom: 14px;">
          <div style="display: flex; justify-content: space-between; gap: 8px; flex-wrap: wrap; margin-bottom: 4px;">
            <span style="color: var(--id-text-secondary); font-size: 12px;">待核验成员：</span>
            <strong style="color: var(--id-text-primary);">{{ currentVerifyingRule?.target.label }}</strong>
          </div>
          <div style="display: flex; justify-content: space-between; gap: 8px; flex-wrap: wrap;">
            <span style="color: var(--id-text-secondary); font-size: 12px;">对应 AI 员工：</span>
            <span style="color: var(--id-accent-primary); font-weight: 500;">{{ employee.name }}</span>
          </div>
        </div>

        <NAlert v-if="wecomVerifyError" type="error" :show-icon="true" style="margin-bottom: 14px;">
          {{ wecomVerifyError }}
        </NAlert>

        <div v-else class="wv-code-box" style="background: var(--id-accent-primary-soft); border: 1px dashed var(--id-accent-primary); padding: 16px; border-radius: var(--id-radius-lg); text-align: center; margin-bottom: 14px;">
          <div style="font-size: 12px; color: var(--id-accent-primary); margin-bottom: 6px;">请通知该成员在企业微信中向该机器人私聊发送 6 位验证码：</div>
          <div style="font-size: 26px; font-weight: 700; letter-spacing: 3px; color: var(--id-accent-primary); font-family: var(--id-font-mono); font-variant-numeric: tabular-nums;">
            {{ wecomVerifyLoading ? '申请中...' : wecomVerifyDynamicCode }}
          </div>
          <div style="font-size: 12px; margin-top: 8px;">
            <NTag size="small" :type="wecomVerifyStatus === 'verified' ? 'success' : wecomVerifyStatus === 'expired' ? 'error' : 'info'">
              {{ wecomVerifyStatus === 'verified' ? '身份核验已通过' : wecomVerifyStatus === 'expired' ? '验证码已过期' : '等待成员发送验证码...' }}
            </NTag>
          </div>
        </div>

        <div v-if="isDevMode && wecomVerifyStatus === 'waiting' && wecomVerifyDynamicCode" style="display: flex; justify-content: space-between; align-items: center; gap: 8px; flex-wrap: wrap; padding: 8px 12px; background: var(--id-status-warning-soft); border: 1px solid var(--id-status-warning-border); border-radius: var(--id-radius-md);">
          <span style="font-size: 12px; color: var(--id-status-warning);">开发联调辅助（模拟企微网关入站消息）：</span>
          <NButton size="tiny" type="warning" secondary :disabled="realPlatform || !canManage || (wecomVerifyLoading)" @click="simulateMemberSendCode">
            调试：模拟企微私聊发送验证码回调
          </NButton>
        </div>
      </div>
      <template #footer>
        <div style="display: flex; justify-content: flex-end; gap: 8px;">
          <NButton @click="showWecomVerifyModal = false">{{ wecomVerifyStatus === 'verified' ? '完成并关闭' : '关闭' }}</NButton>
        </div>
      </template>
    </NModal>
  </div>
</template>

<style scoped>
@import './workforce-shared.css';

.settings-card-block {
  background: var(--id-bg-surface);
  border: 1px solid var(--id-border-subtle);
  border-radius: var(--id-radius-lg);
  padding: 16px;
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.cb-title {
  font-size: 14px;
  font-weight: 600;
  color: var(--id-text-primary);
  margin: 0;
}

.cb-sub {
  font-size: 12px;
  color: var(--id-text-secondary);
  margin: 0;
}

.sub-scope-selectors {
  background: var(--id-bg-subtle);
  border: 1px solid var(--id-border-subtle);
  border-radius: var(--id-radius-md);
  padding: 12px;
  display: flex;
  flex-direction: column;
  margin-top: 14px;
}

.scope-header-line {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
  margin-bottom: 6px;
}

.scope-row label,
.dual-item label,
.handoff-whitelist-row label {
  font-size: 12px;
  color: var(--id-text-secondary);
  font-weight: 600;
  margin-bottom: 4px;
  display: block;
}

.unified-scope-callout {
  margin-top: 12px;
  background: var(--id-bg-muted);
  border: 1px solid var(--id-border-subtle);
  border-radius: var(--id-radius-md);
  padding: 10px 14px;
  display: flex;
  gap: 10px;
  font-size: 12px;
  color: var(--id-text-primary);
}

.scope-group-toggle-row {
  margin-top: 12px;
  padding: 12px 0 4px 0;
  border-top: 1px solid var(--id-border-hairline);
  border-bottom: none;
}

.scope-toggle-title {
  display: inline-flex;
  align-items: center;
  gap: 6px;
}

.scope-toggle-title strong {
  font-size: 13px;
  font-weight: 500;
  color: var(--id-text-primary);
}

.scope-help-badge {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 15px;
  height: 15px;
  border-radius: 50%;
  font-size: 11px;
  font-weight: 600;
  line-height: 1;
  color: var(--id-text-secondary);
  background: var(--id-bg-muted);
  border: 1px solid var(--id-border-strong);
  cursor: help;
}

.scope-tooltip-body {
  font-size: 12px;
  line-height: 1.55;
}

.handoff-whitelist-row {
  background: var(--id-bg-subtle);
  padding: 10px;
  border-radius: var(--id-radius-md);
  border: 1px solid var(--id-border-subtle);
  margin-top: 12px;
}

.rules-table-box {
  overflow-x: auto;
  -webkit-overflow-scrolling: touch;
}

.rules-table-box .custom-d2-table {
  min-width: 640px;
}

.legacy-access-review ul {
  margin: 4px 0 8px 18px;
  padding: 0;
  font-size: 12px;
  font-family: var(--id-font-mono);
}

.legacy-access-review li {
  line-height: 1.6;
}

@media (max-width: 760px) {
  .settings-card-block {
    padding: 12px;
  }
}
</style>

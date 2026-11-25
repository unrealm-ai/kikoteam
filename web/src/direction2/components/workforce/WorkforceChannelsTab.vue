<script setup lang="ts">
import { copyText } from '../../utils/clipboard';
import WorkforceConnectionStatus from './WorkforceConnectionStatus.vue';
import WorkforceStatusHelp from './WorkforceStatusHelp.vue';
import WorkforceWecomContactPermission from './WorkforceWecomContactPermission.vue';
import { computed, onUnmounted, ref, watch } from 'vue';
import {
  NAlert, NButton, NCheckbox, NForm, NFormItem, NInput, NModal,
  NQrCode, NRadio, NRadioGroup, NSlider, NSpace, NSwitch,
  NTab, NTabs, NTag, useMessage
} from 'naive-ui';
import D2ChannelLogo from '../D2ChannelLogo.vue';
import FeishuRegistrationQr from '../FeishuRegistrationQr.vue';
import WorkforceGroupPolicies from './WorkforceGroupPolicies.vue';
import { useWorkforcePorts } from '../../api/workforce-ports';
import { activeWorkspaceId } from '../../api/client';
import { realPlatform } from '../../api/platform';
import type { ChannelRebindImpact } from '../../api/feishu';
import { beginWeComRegistration } from '../../api/wecom-registration';
import { buildManualCredPayload, validateManualCredentials } from '../../utils/workforce';
import {
  type ChannelDirectoryUser,
  type ChannelQrRegistrationSession,
  type DigitalEmployee,
  type EmployeeChannelBinding,
  type FeishuRegexTemplateItem,
  type FeishuTriggerRule,
  type PlatformType
} from '../../types';

const { workforceApi } = useWorkforcePorts();

const props = withDefaults(defineProps<{
  canManage?: boolean;
  employee: DigitalEmployee;
  savedEmployee: DigitalEmployee | null;
  activeChannelPlatform: PlatformType;
  isActiveTab: boolean;
  retryingChannel?: PlatformType | undefined;
  directoryUsers: ChannelDirectoryUser[];
  platformCandidateGroups: Record<PlatformType, string[]>;
  feishuRegexTemplates: FeishuRegexTemplateItem[];
  isDevMode: boolean;
  syncModuleField: <K extends keyof DigitalEmployee>(field: K, value: DigitalEmployee[K]) => Promise<boolean>;
  syncServerFieldIntoSnapshot: <K extends keyof DigitalEmployee>(
    empId: string,
    savedRaw: DigitalEmployee,
    fields: K[]
  ) => void;
  handleOptimisticConflictError: (err: unknown) => boolean;
}>(), {canManage:true});

const emit = defineEmits<{
  'update:activeChannelPlatform': [platform: PlatformType];
  'configure-access': [];
  'retry-channel': [];
  dirty: [];
}>();

const message = useMessage();

const currentPlatform = computed({
  get: () => props.activeChannelPlatform,
  set: (v: PlatformType) => emit('update:activeChannelPlatform', v)
});

const currentChannel = computed(() =>
  props.employee.channels.find(c => c.platform === currentPlatform.value) || null
);
const channelReadError = computed(() => realPlatform ? props.employee.channelReadErrors?.[currentPlatform.value] : undefined);
const channelNeedsWhitelist = computed(() => {
  const channel = currentChannel.value;
  if (!realPlatform || !channel || channel.status === 'disabled') return false;
  const scope = channel.usageScope;
  return scope.mode === 'whitelist' && scope.whitelistMembers.length === 0 && scope.whitelistDepartments.length === 0;
});
const groupMembersAllowed = computed(() => currentChannel.value?.usageScope.groupMembersEnabled
  ?? ((props.savedEmployee ?? props.employee).unifiedAccess?.allowGroupMembers === true));

const directoryAvailability = computed(() => {
  const channel = currentChannel.value;
  if (channel?.status === 'disabled') return { label: '通讯录不可用', type: 'default' } as const;
  const directory = channel?.directoryStatus;
  if (!directory) return { label: '通讯录未确认', type: 'default' } as const;
  return directory.complete
    ? { label: '通讯录可用', type: 'success' } as const
    : { label: '通讯录不可用', type: 'default' } as const;
});

function directoryStatusText(channel: EmployeeChannelBinding) {
  const directory = channel.directoryStatus;
  if (channel.status === 'disabled') return '通讯录同步已暂停';
  if (directory?.status === 'syncing') return '正在同步通讯录';
  if (directory?.complete) return `通讯录已同步 · ${directory.user_count} 人 · ${directory.department_count} 个部门`;
  if (channel.platform === 'dingtalk' && directory?.error_code === 'directory_authorization_failed') {
    return '通讯录缺少权限或可见范围。机器人可以先接入，补齐后将自动复查。';
  }
  if (channel.platform === 'dingtalk' && directory?.error_code === 'directory_authentication_failed') {
    return '通讯录认证失败，请核对应用凭据。系统会自动复查。';
  }
  if (directory?.status === 'invalidated' || directory?.status === 'failed') return '通讯录暂不可用，系统会自动复查。';
  return '等待自动同步通讯录';
}
const feishuChannel = computed(() =>
  props.employee.channels.find(c => c.platform === 'feishu') || null
);
const wecomChannel = computed(() =>
  props.employee.channels.find(c => c.platform === 'wecom') || null
);
const dingtalkChannel = computed(() =>
  props.employee.channels.find(c => c.platform === 'dingtalk') || null
);
const dingtalkSendPermissionMissing = computed(() => {
  const channel = dingtalkChannel.value;
  return realPlatform && channel?.status !== 'disabled' && !channel?.configurationChanged
    && channel?.dingtalkProactiveSendPermissionMissing === true;
});
const dingtalkSendHelp = [
  '钉钉权限不足，请在钉钉开放平台开通「企业内机器人发送消息权限」。',
];
const dingtalkDirectoryAuthorizationFailed = computed(() => {
  const channel = dingtalkChannel.value;
  return realPlatform && channel?.status !== 'disabled'
    && channel?.directoryStatus?.error_code === 'directory_authorization_failed';
});
const dingtalkDirectoryHelp = [
  '请检查「通讯录部门成员读权限」和「成员信息读权限」，并确认应用的通讯录授权范围包含需要同步的部门和人员。',
];
const savedFeishuChannel = computed(() =>
  props.savedEmployee?.channels.find(c => c.platform === 'feishu') || null
);
const savedWecomChannel = computed(() =>
  props.savedEmployee?.channels.find(c => c.platform === 'wecom') || null
);

type ChannelPatchTarget = 'feishuPolicy' | 'wecomPolicy' | 'feishuRules' | 'sharingRules';

const isSavingChannelSection = ref(false);

const isFeishuPolicyDirty = computed(() => {
  const cur = feishuChannel.value;
  const saved = savedFeishuChannel.value;
  if (!cur || !saved) return false;
  return (
    Boolean(cur.capabilities.ordinaryGroupEnabled) !== Boolean(saved.capabilities.ordinaryGroupEnabled) ||
    Boolean(cur.capabilities.recentContextEnabled) !== Boolean(saved.capabilities.recentContextEnabled) ||
    (cur.capabilities.recentContextWindow ?? 20) !== (saved.capabilities.recentContextWindow ?? 20)
  );
});

const isWecomPolicyDirty = computed(() => {
  const cur = wecomChannel.value;
  const saved = savedWecomChannel.value;
  if (!cur || !saved) return false;
  return (
    Boolean(cur.capabilities.recentContextEnabled) !== Boolean(saved.capabilities.recentContextEnabled) ||
    (cur.capabilities.recentContextWindow ?? 20) !== (saved.capabilities.recentContextWindow ?? 20)
  );
});

async function persistChannelPatch(
  target: ChannelPatchTarget,
  successMsg: string,
  mutator: (channels: EmployeeChannelBinding[]) => void
): Promise<boolean> {
  if (isSavingChannelSection.value) return false;

  // Preserve unsaved inline drafts on other cards so saving one card or modal does not commit or wipe them.
  const feishuCur = feishuChannel.value;
  const wecomCur = wecomChannel.value;
  const draftFeishuPolicy = target !== 'feishuPolicy' && isFeishuPolicyDirty.value && feishuCur
    ? {
        ordinaryGroupEnabled: feishuCur.capabilities.ordinaryGroupEnabled,
        recentContextEnabled: feishuCur.capabilities.recentContextEnabled,
        recentContextWindow: feishuCur.capabilities.recentContextWindow
      }
    : null;
  const draftWecomPolicy = target !== 'wecomPolicy' && isWecomPolicyDirty.value && wecomCur
    ? {
        recentContextEnabled: wecomCur.capabilities.recentContextEnabled,
        recentContextWindow: wecomCur.capabilities.recentContextWindow
      }
    : null;

  const baseChannels = JSON.parse(
    JSON.stringify(props.savedEmployee?.channels ?? props.employee.channels)
  ) as EmployeeChannelBinding[];
  mutator(baseChannels);

  isSavingChannelSection.value = true;
  try {
    const ok = await props.syncModuleField('channels', baseChannels);
    if (!ok) return false;

    if (draftFeishuPolicy && feishuChannel.value) {
      feishuChannel.value.capabilities.ordinaryGroupEnabled = draftFeishuPolicy.ordinaryGroupEnabled;
      feishuChannel.value.capabilities.recentContextEnabled = draftFeishuPolicy.recentContextEnabled;
      feishuChannel.value.capabilities.recentContextWindow = draftFeishuPolicy.recentContextWindow;
    }
    if (draftWecomPolicy && wecomChannel.value) {
      wecomChannel.value.capabilities.recentContextEnabled = draftWecomPolicy.recentContextEnabled;
      wecomChannel.value.capabilities.recentContextWindow = draftWecomPolicy.recentContextWindow;
    }

    emit('dirty');
    message.success(successMsg);
    return true;
  } finally {
    isSavingChannelSection.value = false;
  }
}

function cancelFeishuPolicy() {
  const cur = feishuChannel.value;
  const saved = savedFeishuChannel.value;
  if (!cur || !saved) return;
  cur.capabilities.ordinaryGroupEnabled = saved.capabilities.ordinaryGroupEnabled;
  cur.capabilities.recentContextEnabled = saved.capabilities.recentContextEnabled;
  cur.capabilities.recentContextWindow = saved.capabilities.recentContextWindow;
  emit('dirty');
}

async function saveFeishuPolicy() {
  const cur = feishuChannel.value;
  if (!cur) return;
  const nextOrdinary = cur.capabilities.ordinaryGroupEnabled;
  const nextRecentEnabled = cur.capabilities.recentContextEnabled;
  const nextRecentWindow = cur.capabilities.recentContextWindow;
  await persistChannelPatch('feishuPolicy', '飞书群触发与上下文策略已保存！', (channels) => {
    const ch = channels.find(c => c.platform === 'feishu');
    if (!ch) return;
    ch.capabilities.ordinaryGroupEnabled = nextOrdinary;
    ch.capabilities.recentContextEnabled = nextRecentEnabled;
    ch.capabilities.recentContextWindow = nextRecentWindow;
  });
}

function cancelWecomPolicy() {
  const cur = wecomChannel.value;
  const saved = savedWecomChannel.value;
  if (!cur || !saved) return;
  cur.capabilities.recentContextEnabled = saved.capabilities.recentContextEnabled;
  cur.capabilities.recentContextWindow = saved.capabilities.recentContextWindow;
  emit('dirty');
}

async function saveWecomPolicy() {
  const cur = wecomChannel.value;
  if (!cur) return;
  const nextRecentEnabled = cur.capabilities.recentContextEnabled;
  const nextRecentWindow = cur.capabilities.recentContextWindow;
  await persistChannelPatch('wecomPolicy', '企微群聊上下文策略已保存！', (channels) => {
    const ch = channels.find(c => c.platform === 'wecom');
    if (!ch) return;
    ch.capabilities.recentContextEnabled = nextRecentEnabled;
    ch.capabilities.recentContextWindow = nextRecentWindow;
  });
}

// ================= QR 扫码接入与轮询 =================
const channelOnboardingMode = ref<Record<PlatformType, 'choice' | 'qrcode' | 'existing_app'>>({
  feishu: 'choice',
  wecom: 'choice',
  dingtalk: 'choice'
});

const qrSession = ref<ChannelQrRegistrationSession | null>(null);
const qrSecondsLeft = ref(0);
const activeQrSessionId = ref<string | null>(null);
const qrLoading = ref(false);
const qrError = ref('');
const qrQueryTimedOut = ref(false);
const qrConfirming = ref(false);
const wecomOpening = ref(false);
const usingWecomPopup = computed(() => realPlatform && currentPlatform.value === 'wecom');
let wecomAttempt: ReturnType<typeof beginWeComRegistration> | undefined;
const qrOutcomeUnknown = computed(() => qrSession.value?.restartNeedsReview
  || ['interrupted', 'unknown', 'uncertain'].includes(qrSession.value?.registrationState || ''));
const qrCanRecheck = computed(() => qrSession.value?.credentialSaved || qrSession.value?.registrationState === 'ready');
const pendingWecomCredentials = ref<{botId:string;secret:string} | null>(null);
const qrLink = computed(() => qrSession.value?.status === 'waiting_scan' && qrSecondsLeft.value > 0
  ? qrSession.value.verificationUrl : undefined);
const qrPayload = computed(() => qrSession.value?.qrPayload || qrLink.value || '');

const qrFormattedCountdown = computed(() => {
  const m = Math.floor(qrSecondsLeft.value / 60);
  const s = qrSecondsLeft.value % 60;
  return `${String(m).padStart(2, '0')}:${String(s).padStart(2, '0')}`;
});

let qrCountdownTimer: ReturnType<typeof setInterval> | null = null;
let qrPollTimer: ReturnType<typeof setInterval> | null = null;
let qrLoadSeq = 0;
let qrLoopSeq = 0;
let qrDisposed = false;
let qrPollFailureCount = 0;

function stopQrTimers() {
  qrLoopSeq += 1;
  if (qrCountdownTimer) {
    clearInterval(qrCountdownTimer);
    qrCountdownTimer = null;
  }
  if (qrPollTimer) {
    clearInterval(qrPollTimer);
    qrPollTimer = null;
  }
}

function resetQrState() {
  qrLoadSeq += 1;
  pendingWecomCredentials.value = null;
  wecomAttempt?.cancel();
  wecomAttempt = undefined;
  wecomOpening.value = false;
  stopQrTimers();
  qrSession.value = null;
  activeQrSessionId.value = null;
  qrSecondsLeft.value = 0;
  qrLoading.value = false;
  qrConfirming.value = false;
  qrQueryTimedOut.value = false;
  qrError.value = '';
}

function isQrContextStillActive(
  expectedWsId: string,
  expectedEmpId: string,
  expectedPlatform: PlatformType,
  expectedSessionId?: string | null
): boolean {
  if (qrDisposed || props.canManage === false || activeWorkspaceId.value !== expectedWsId) return false;
  if (props.employee.id !== expectedEmpId) return false;
  if (!props.isActiveTab || channelOnboardingMode.value[expectedPlatform] !== 'qrcode') return false;
  if (currentPlatform.value !== expectedPlatform) return false;
  if (expectedSessionId && activeQrSessionId.value !== expectedSessionId) return false;
  return true;
}

async function applyAuthorizedQrRefresh(expectedWsId: string, expectedEmpId: string, expectedPlatform: PlatformType) {
  try {
    const fresh = await workforceApi.getEmployee(expectedEmpId);
    if (!isQrContextStillActive(expectedWsId,expectedEmpId,expectedPlatform)) return;
    props.syncServerFieldIntoSnapshot(expectedEmpId, fresh, ['channels', 'status']);
  } catch (err) {
    message.error(err instanceof Error ? err.message : `刷新「${expectedPlatform}」渠道状态失败`);
  }
}

function registrationTerminalMessage(session: ChannelQrRegistrationSession) {
  if(session.restartNeedsReview || session.registrationState==='interrupted')return '上次扫码未完成，请先在官方后台核对是否已创建机器人，再绑定已有机器人或重新扫码。';
  if(session.registrationState==='cancelled')return '已停止等待，可重新开始接入。';
  if(session.registrationState==='denied')return '扫码授权被拒绝，可重新发起授权。';
  if(session.registrationState==='failed')return `接入未完成${session.registrationError ? `（${session.registrationError}）` : ''}，请先核对应用状态。`;
  return '授权二维码已过期，请点击刷新重新获取';
}

function shouldReplaceExpiredQr(session: ChannelQrRegistrationSession) {
  return realPlatform && session.platform !== 'wecom' && session.registrationState === 'expired' && !session.credentialSaved;
}

function startQrLoops(wsId: string, empId: string, platform: PlatformType, sessionId: string) {
  stopQrTimers();
  if (qrSession.value?.feishuFailure?.automaticRetry === false) return;
  const loop = qrLoopSeq;
  const current = () => loop === qrLoopSeq && isQrContextStillActive(wsId, empId, platform, sessionId);
  let inFlight = false;
  let displayedQr = Boolean(qrLink.value);
  qrPollFailureCount = 0;
  qrCountdownTimer = setInterval(() => {
    if (qrSecondsLeft.value > 0) {
      qrSecondsLeft.value -= 1;
    } else if (!realPlatform && qrSession.value && qrSession.value.status === 'waiting_scan') {
      qrSession.value.status = 'expired';
      qrError.value = '授权二维码已过期，请点击刷新重新获取';
      stopQrTimers();
    }
  }, 1000);

  qrPollTimer = setInterval(async () => {
    if (inFlight || !current()) return;
    try {
      inFlight = true;
      const polled = await workforceApi.pollChannelQrRegistration(sessionId);
      if (!current()) return;
      qrPollFailureCount = 0;
      qrError.value = '';
      qrSession.value = polled;
      qrSecondsLeft.value = polled.expiresInSeconds;
      displayedQr ||= Boolean(qrLink.value);
      if (polled.feishuFailure?.automaticRetry === false) {
        stopQrTimers();
      } else if (polled.status === 'authorized' && polled.binding) {
        stopQrTimers();
        await applyAuthorizedQrRefresh(wsId, empId, platform);
        message.success(`「${polled.binding.platformName}」渠道配置已保存。`);
      } else if (polled.status === 'expired') {
        stopQrTimers();
        if (displayedQr && shouldReplaceExpiredQr(polled)) {
          await loadQrCodeForPlatform(platform);
        } else {
          qrError.value = registrationTerminalMessage(polled);
        }
      }
    } catch (err) {
      if (!current()) return;
      qrPollFailureCount += 1;
      const errMsg = err instanceof Error ? err.message : '查询扫码状态失败';
      if (qrPollFailureCount >= 3) {
        qrQueryTimedOut.value = true;
        qrError.value = `${errMsg}，请点击「继续查询状态」或刷新二维码`;
        stopQrTimers();
      } else {
        qrError.value = `${errMsg}（正在第 ${qrPollFailureCount}/3 次自动重试...）`;
      }
    } finally { inFlight = false; }
  }, 2000);
}

async function loadQrCodeForPlatform(platform: PlatformType, forceNew = false, replaceId?: string) {
 if(qrLoading.value)return;
  if(props.canManage===false){resetQrState();qrError.value='扫码或修改接入需要工作区管理员权限';return;}
  if (qrSession.value && (qrSession.value.platform !== platform || qrSession.value.employeeId !== props.employee.id || qrSession.value.workspaceId !== activeWorkspaceId.value)) resetQrState();
  const empId = props.employee.id;
  const wsId = activeWorkspaceId.value;
  const seq = ++qrLoadSeq;

  stopQrTimers();
  qrLoading.value = true;
  qrError.value = '';
  qrQueryTimedOut.value = false;
  if (forceNew && !replaceId) {
    qrSession.value = null;
    activeQrSessionId.value = null;
    qrSecondsLeft.value = 0;
  }

  try {
    let sess: ChannelQrRegistrationSession | null = null;
    if (!forceNew) {
      const latest = await workforceApi.getLatestChannelQrRegistration(empId, platform);
      if (seq !== qrLoadSeq || !isQrContextStillActive(wsId, empId, platform)) return;
      const previous = latest.session;
      if (previous) {
        const dismissed = realPlatform && !previous.credentialSaved && !previous.restartNeedsReview
          && ['cancelled', 'denied'].includes(previous.registrationState || '');
        if (!dismissed && !shouldReplaceExpiredQr(previous)) sess = previous;
      }
    }

    if (!sess) {
      sess = replaceId ? await workforceApi.startChannelQrRegistration(empId, platform, wsId, replaceId)
        : await workforceApi.startChannelQrRegistration(empId, platform, wsId);
    }
    if (seq !== qrLoadSeq || !isQrContextStillActive(wsId, empId, platform)) return;
    if ((sess.workspaceId && sess.workspaceId !== wsId) || (sess.employeeId && sess.employeeId !== empId) || sess.platform !== platform) {
      return;
    }

    qrSession.value = sess;
    activeQrSessionId.value = sess.sessionId;
    qrSecondsLeft.value = sess.expiresInSeconds;

    if (sess.status === 'authorized' && sess.binding) {
      await applyAuthorizedQrRefresh(wsId, empId, platform);
    } else if (sess.status === 'expired') {
      qrError.value = registrationTerminalMessage(sess);
    } else if (sess.status === 'waiting_scan' || sess.status === 'scanned_confirming') {
      startQrLoops(wsId, empId, platform, sess.sessionId);
    }
  } catch (err) {
    if (seq !== qrLoadSeq || !isQrContextStillActive(wsId, empId, platform)) return;
    qrQueryTimedOut.value = true;
    const baseMsg = err instanceof Error ? err.message : '查询授权状态失败';
    qrError.value = `${baseMsg}，请点击「继续查询状态」或刷新二维码`;
    message.error(qrError.value);
  } finally {
    if (seq === qrLoadSeq) {
      qrLoading.value = false;
    }
  }
}

function resumeQrPolling() {
  if (!activeQrSessionId.value || qrSession.value?.feishuFailure) {
    return loadQrCodeForPlatform(currentPlatform.value, false);
  }
  qrQueryTimedOut.value = false;
  qrError.value = '';
  startQrLoops(
    activeWorkspaceId.value,
    props.employee.id,
    currentPlatform.value,
    activeQrSessionId.value
  );
}

async function refreshQrCode(confirmedPreviousOutcome = false) {
  const workspaceId=activeWorkspaceId.value, employeeId=props.employee.id, platform=currentPlatform.value, sessionId=activeQrSessionId.value;
  if (qrLoading.value || qrConfirming.value || (qrOutcomeUnknown.value && !confirmedPreviousOutcome)) return;
  if (realPlatform && sessionId && qrCanRecheck.value) {
    message.warning('已取得凭据，请继续检查当前接入，不重复创建应用'); return;
  }
  try {
    if (realPlatform && sessionId && qrSession.value?.status === 'waiting_scan' && workforceApi.cancelChannelQrRegistration) {
      await workforceApi.cancelChannelQrRegistration(sessionId);
      if (!isQrContextStillActive(workspaceId,employeeId,platform,sessionId)) return;
      const latest = (await workforceApi.getLatestChannelQrRegistration(employeeId, platform)).session;
      if (!isQrContextStillActive(workspaceId,employeeId,platform,sessionId)) return;
      if (latest && (latest.sessionId !== sessionId || latest.credentialSaved || latest.status !== 'expired')) {
        await loadQrCodeForPlatform(platform);
        return;
      }
    }
    if (!isQrContextStillActive(workspaceId,employeeId,platform,sessionId)) return;
    await loadQrCodeForPlatform(platform, true);
  } catch(error) { if(isQrContextStillActive(workspaceId,employeeId,platform,sessionId))qrError.value=error instanceof Error?error.message:'刷新失败'; }
}
async function cancelQrWaiting() {
  const workspaceId=activeWorkspaceId.value, employeeId=props.employee.id, platform=currentPlatform.value, sessionId=activeQrSessionId.value;
  if (!sessionId || !workforceApi.cancelChannelQrRegistration) return;
  try {
    await workforceApi.cancelChannelQrRegistration(sessionId);
    if (!isQrContextStillActive(workspaceId,employeeId,platform,sessionId)) return;
    stopQrTimers();
    if (qrSession.value && !qrSession.value.credentialSaved) qrSession.value.status = 'expired';
    qrError.value = qrSession.value?.credentialSaved ? '凭据已取得，停止等待不会撤销接入，稍后可继续检查。' : '已停止等待，可重新扫码。';
  } catch(error) { if(isQrContextStillActive(workspaceId,employeeId,platform,sessionId))qrError.value=error instanceof Error?error.message:'停止等待失败'; }
}
async function retryQrCheck() {
  const workspaceId=activeWorkspaceId.value, employeeId=props.employee.id, platform=currentPlatform.value, sessionId=activeQrSessionId.value;
  if (!sessionId || qrLoading.value || qrConfirming.value) return;
  stopQrTimers();
  qrError.value = '';
  qrQueryTimedOut.value = false;
  qrConfirming.value = true;
  try {
    const value = await workforceApi.confirmChannelQrRegistration(sessionId);
    if (!isQrContextStillActive(workspaceId,employeeId,platform,sessionId)) return;
    qrSession.value = value;
    if (value.status === 'authorized' && value.binding) await applyAuthorizedQrRefresh(workspaceId,employeeId,platform);
    else startQrLoops(workspaceId,employeeId,platform,value.sessionId);
  } catch(error) { if(isQrContextStillActive(workspaceId,employeeId,platform,sessionId))qrError.value=error instanceof Error?error.message:'检查失败'; }
  finally {if(isQrContextStillActive(workspaceId,employeeId,platform,sessionId))qrConfirming.value=false;}
}

// Navigation never initiates registration.
watch([activeWorkspaceId, () => props.employee.id, () => props.isActiveTab, currentPlatform], () => {
  channelOnboardingMode.value = {feishu: 'choice', wecom: 'choice', dingtalk: 'choice'};
  resetQrState();
  onAppBindFieldChange();
}, {flush: 'sync'});

function chooseCreateRobot() {
  if (props.canManage === false || appBindForm.value.isSaving) return;
  if (channelOnboardingMode.value[currentPlatform.value] === 'qrcode') return;
  onAppBindFieldChange();
  resetQrState();
  channelOnboardingMode.value[currentPlatform.value] = 'qrcode';
  if (usingWecomPopup.value) {
    openWecomRegistration();
    return;
  }
  void loadQrCodeForPlatform(currentPlatform.value);
}

function chooseExistingRobot() {
  if (props.canManage === false) return;
  resetQrState();
  channelOnboardingMode.value[currentPlatform.value] = 'existing_app';
}

onUnmounted(() => {
  qrDisposed = true;
  resetQrState();
});

async function copyEmployeeName() {
  try { await copyText(props.employee.name); message.success('已复制员工名称'); }
  catch { message.warning('无法自动复制，请手动选择员工名称'); }
}

function openWecomRegistration() {
  if (props.canManage === false || wecomOpening.value || qrConfirming.value || pendingWecomCredentials.value || !usingWecomPopup.value) return;
  const employeeId = props.employee.id;
  const workspaceId = activeWorkspaceId.value;
  const generation = ++qrLoadSeq;
  const current = () => generation === qrLoadSeq && isQrContextStillActive(workspaceId, employeeId, 'wecom');
  qrError.value = '';
  wecomAttempt = beginWeComRegistration({
    host: window,
    onStatus(status) {
      if (!current()) return;
      wecomOpening.value = status === 'waiting' || status === 'ready';
      if (status === 'blocked') qrError.value = '浏览器拦截了企微扫码窗口。允许弹窗后可重新打开。';
      if (status === 'expired') qrError.value = '企业微信扫码已超时，请重新打开扫码页。';
      if (status === 'failed') qrError.value = '企微授权未完成，请检查官方扫码页。';
    },
    onCredentials(credentials) {
      if (!current()) return;
      pendingWecomCredentials.value = credentials;
      void saveWecomRegistration();
    },
  });
}

async function saveWecomRegistration(recover = false) {
  const credentials = pendingWecomCredentials.value;
  const employeeId = props.employee.id;
  const workspaceId = activeWorkspaceId.value;
  const generation = qrLoadSeq;
  const current = () => generation === qrLoadSeq && isQrContextStillActive(workspaceId, employeeId, 'wecom');
  if (!credentials || !current() || qrConfirming.value) return;
  qrConfirming.value = true;
  qrError.value = '';
  try {
    if (recover) {
      const employee = await workforceApi.getEmployee(employeeId);
      if (!current()) return;
      const binding = employee.channels.find(channel => channel.platform === 'wecom');
      if (binding) {
        pendingWecomCredentials.value = null;
        props.syncServerFieldIntoSnapshot(employeeId, employee, ['channels', 'status']);
        if (binding.botId !== credentials.botId) message.warning('企微绑定已变化，请核对当前机器人。');
        return;
      }
    }
    const payload = {botId: credentials.botId, botSecret: credentials.secret};
    await workforceApi.testChannelConnection(employeeId, 'wecom', {...payload, configVersion: 0});
    if (!current()) return;
    const result = await workforceApi.bindChannelCredentials(employeeId, 'wecom', {
      ...payload, channelTestReceipt: '', expectedVersion: props.employee.version,
    });
    if (!current()) return;
    pendingWecomCredentials.value = null;
    props.syncServerFieldIntoSnapshot(employeeId, result.employee, ['channels', 'status']);
    message.success('企微凭据已保存，等待正式连接验证。');
  } catch (error) {
    if (current()) qrError.value = error instanceof Error ? error.message : '企微凭据保存失败';
  } finally {
    if (current()) qrConfirming.value = false;
  }
}

async function handleChannelQrSuccess(platform: PlatformType) {
  const empId = props.employee.id;
  const wsId = activeWorkspaceId.value;
  const sessionId = activeQrSessionId.value;
  if (!sessionId) {
    message.error('当前二维码无效，请先刷新获取二维码');
    return;
  }
  qrConfirming.value = true;
  qrError.value = '';
  try {
    const res = await workforceApi.confirmChannelQrRegistration(sessionId);
    if (!isQrContextStillActive(wsId, empId, platform, sessionId)) return;
    if (res.status !== 'authorized' || !res.binding) {
      throw new Error('尚未完成渠道绑定，请重试');
    }
    stopQrTimers();
    qrSession.value = res;
    await applyAuthorizedQrRefresh(wsId, empId, platform);
    message.success(`管理员扫码授权成功！已接入「${res.binding.platformName}」。`);
  } catch (err) {
    if (isQrContextStillActive(wsId, empId, platform, sessionId)) {
      const errMsg = err instanceof Error ? err.message : '确认扫码授权失败';
      qrError.value = errMsg;
      message.error(errMsg);
    }
  } finally {
    qrConfirming.value = false;
  }
}

// ================= 手动凭据接入 =================
const appBindForm = ref({
  appId: '',
  appSecret: '',
  botId: '',
  botSecret: '',
  corpId: '',
  agentId: '',
  appKey: '',
  encryptKey: '',
  verificationToken: '',
  isTesting: false,
  isSaving: false,
  testReceipt: '',
  formatOnly: false,
  testedSignature: '',
  testLatency: '',
  errorMsg: ''
});

let appCredentialTestSequence = 0;
function onAppBindFieldChange() {
  appCredentialTestSequence += 1;
  appBindForm.value.isTesting = false;
  appBindForm.value.testReceipt = '';
  appBindForm.value.formatOnly = false;
  appBindForm.value.testedSignature = '';
  appBindForm.value.testLatency = '';
  appBindForm.value.errorMsg = '';
}

watch(currentPlatform, () => {
  appBindForm.value.appSecret = '';
  appBindForm.value.botSecret = '';
});

async function runChannelConnectivityTest(platformOverride?: PlatformType) {
  if(props.canManage===false){message.warning('修改接入需要工作区管理员权限');return;}
  const sequence = ++appCredentialTestSequence;
  const employeeId = props.employee.id;
  const workspaceId = activeWorkspaceId.value;
  const platform = platformOverride || currentPlatform.value;
  const current = () => sequence === appCredentialTestSequence && props.employee.id === employeeId
    && activeWorkspaceId.value === workspaceId && currentPlatform.value === platform
    && props.isActiveTab && channelOnboardingMode.value[platform] === 'existing_app';
  const validationErr = validateManualCredentials(platform, appBindForm.value);
  if (validationErr) {
    appBindForm.value.errorMsg = validationErr;
    message.error(validationErr);
    return;
  }
  const creds = buildManualCredPayload(platform, appBindForm.value);
  appBindForm.value.isTesting = true;
  appBindForm.value.errorMsg = '';
  appBindForm.value.testReceipt = '';
  appBindForm.value.formatOnly = false;
  appBindForm.value.testedSignature = '';
  try {
    const res = await workforceApi.testChannelConnection(props.employee.id, platform, {...creds,...(realPlatform?{configVersion:props.employee.resourceVersions?.channels[platform]??0}:{})});
    if (!current()) return;
    appBindForm.value.testReceipt = res.channelTestReceipt;
    appBindForm.value.formatOnly = res.credentialStatus === 'format_only';
    appBindForm.value.testedSignature = `${platform}::${JSON.stringify(creds)}`;
    appBindForm.value.testLatency = res.latency;
    message.success(res.credentialStatus === 'format_only' ? '凭据格式检查通过，保存后由正式连接验证。' : `凭据验证通过（延迟 ${res.latency}）`);
  } catch (err) {
    if (!current()) return;
    const errMsg = err instanceof Error ? err.message : '渠道凭证连接测试失败';
    appBindForm.value.errorMsg = errMsg;
    message.error(errMsg);
  } finally {
    if (current()) appBindForm.value.isTesting = false;
  }
}

async function handleSaveAppBinding(platformOverride?: PlatformType) {
  if (props.canManage === false || appBindForm.value.isSaving) return;
  const empId = props.employee.id;
  const workspaceId = activeWorkspaceId.value;
  const platform = platformOverride || currentPlatform.value;
  const validationErr = validateManualCredentials(platform, appBindForm.value);
  if (validationErr) {
    appBindForm.value.errorMsg = validationErr;
    message.error(validationErr);
    return;
  }
  const creds = buildManualCredPayload(platform, appBindForm.value);
  const currentSig = `${platform}::${JSON.stringify(creds)}`;
  if (!realPlatform && ((!appBindForm.value.testReceipt && !(platform === 'wecom' && appBindForm.value.formatOnly)) || appBindForm.value.testedSignature !== currentSig)) {
    const errMsg = platform === 'wecom' ? '请先完成凭据格式检查，再保存等待正式连接验证' : '请先点击「验证连接」通过测试后再保存';
    appBindForm.value.errorMsg = errMsg;
    message.error(errMsg);
    return;
  }

  appBindForm.value.isSaving = true;
  appBindForm.value.errorMsg = '';
  const sequence = ++appCredentialTestSequence;
  const current = () => !qrDisposed && props.canManage !== false && props.employee.id === empId && activeWorkspaceId.value === workspaceId
    && currentPlatform.value === platform && appCredentialTestSequence === sequence && props.isActiveTab
    && channelOnboardingMode.value[platform] === 'existing_app';
  try {
    const receipt = realPlatform
      ? (await workforceApi.testChannelConnection(empId, platform, {...creds, configVersion: 0})).channelTestReceipt
      : appBindForm.value.testReceipt;
    if (!current()) return;
    const res = await workforceApi.bindChannelCredentials(empId, platform, {
      ...creds,
      channelTestReceipt: receipt,
      expectedVersion: props.employee.version
    });
    if (!current()) return;
    props.syncServerFieldIntoSnapshot(empId, res.employee, ['channels', 'status']);
    appBindForm.value.appSecret = '';
    appBindForm.value.botSecret = '';
    appBindForm.value.testReceipt = '';
    appBindForm.value.formatOnly = false;
    appBindForm.value.testedSignature = '';
    message.success(platform === 'wecom' ? '凭据已保存，等待正式连接验证。' : `「${res.channel.platformName}」凭据已保存，等待连接。`);
  } catch (err) {
    if (current() && !props.handleOptimisticConflictError(err)) {
      const errMsg = err instanceof Error ? err.message : '保存渠道凭证失败';
      appBindForm.value.errorMsg = errMsg;
      message.error(errMsg);
    }
  } finally {
    appBindForm.value.isSaving = false;
  }
}

watch([() => props.employee.id, () => activeWorkspaceId.value], () => {
  onAppBindFieldChange();
  onRebindFieldChange();
  appBindForm.value.appSecret = '';
  appBindForm.value.botSecret = '';
  rebindForm.value.appSecret = '';
  rebindForm.value.botSecret = '';
  showRebindModal.value = false;
});

// ================= 换绑流程 & 渠道启停 =================
const showRebindModal = ref(false);
const rebindPlatform = ref<PlatformType>('feishu');
const rebindConfigurationChanged = computed(() => realPlatform && props.employee.channels
  .some(channel => channel.platform === rebindPlatform.value && channel.configurationChanged));
const rebindForm = ref({
  appId: '',
  appSecret: '',
  botId: '',
  botSecret: '',
  appKey: '',
  confirmedImpact: false,
  isTesting: false,
  isSaving: false,
  testReceipt: '',
  formatOnly: false,
  testedSignature: '',
  testLatency: '',
  errorMsg: ''
});

const isSwitchingRobot = computed(() => {
  const ch = props.employee.channels.find(c => c.platform === rebindPlatform.value);
  if (!ch) return false;
  if (rebindPlatform.value === 'wecom') {
    return !!rebindForm.value.botId && rebindForm.value.botId.trim() !== (ch.botId || '');
  }
  if (rebindPlatform.value === 'dingtalk') {
    return !!rebindForm.value.appKey && rebindForm.value.appKey.trim() !== (ch.appKey || '');
  }
  return !!rebindForm.value.appId && rebindForm.value.appId.trim() !== (ch.appId || '');
});

const rebindImpact = ref<ChannelRebindImpact>();
const rebindImpactError = ref('');
const rebindImpactRetry = ref(0);
watch([showRebindModal, isSwitchingRobot, activeWorkspaceId, () => props.employee.id, rebindPlatform, rebindImpactRetry], async ([open, replacing, workspace, employee, provider], _, cleanup) => {
  rebindImpact.value = undefined;
  rebindImpactError.value = '';
  if (!realPlatform || !open || !replacing) return;
  const controller = new AbortController();
  cleanup(() => controller.abort());
  try {
    const result = await workforceApi.getChannelRebindImpact?.(workspace, employee, provider, controller.signal);
    if (controller.signal.aborted) return;
    if (!result?.rebind_impact) throw new Error('无法读取换绑影响');
    const current = props.employee.channels.find(channel => channel.platform === provider);
    const version = props.employee.resourceVersions?.channels[provider];
    if (result.id !== current?.connectionId || (version !== undefined && result.config_revision !== version)) {
      rebindImpactError.value = '渠道配置已变更，请关闭窗口并重新加载。';
      return;
    }
    rebindImpact.value = result.rebind_impact;
  } catch (error) {
    if (!controller.signal.aborted) rebindImpactError.value = error instanceof Error ? error.message : '无法读取换绑影响';
  }
});

let rebindCredentialTestSequence = 0;
function onRebindFieldChange() {
  rebindCredentialTestSequence += 1;
  rebindForm.value.confirmedImpact = false;
  rebindForm.value.testReceipt = '';
  rebindForm.value.formatOnly = false;
  rebindForm.value.testedSignature = '';
  rebindForm.value.testLatency = '';
  rebindForm.value.errorMsg = '';
}

function openRebindModal(platform: PlatformType) {
  rebindCredentialTestSequence += 1;
  rebindPlatform.value = platform;
  const ch = props.employee.channels.find(c => c.platform === platform);
  rebindForm.value = {
    appId: ch?.appId || '',
    appSecret: '',
    botId: ch?.botId || '',
    botSecret: '',
    appKey: ch?.appKey || '',
    confirmedImpact: false,
    isTesting: false,
    isSaving: false,
    testReceipt: '',
  formatOnly: false,
    testedSignature: '',
    testLatency: '',
    errorMsg: ''
  };
  showRebindModal.value = true;
}

async function runRebindConnectivityTest() {
  if(props.canManage===false){message.warning('修改接入需要工作区管理员权限');return;}
  const sequence = ++rebindCredentialTestSequence;
  const employeeId = props.employee.id;
  const workspaceId = activeWorkspaceId.value;
  const platform = rebindPlatform.value;
  const validationErr = validateManualCredentials(platform, rebindForm.value);
  if (validationErr) {
    rebindForm.value.errorMsg = validationErr;
    message.error(validationErr);
    return;
  }
  const creds = buildManualCredPayload(platform, rebindForm.value);
  rebindForm.value.isTesting = true;
  rebindForm.value.errorMsg = '';
  rebindForm.value.testReceipt = '';
  rebindForm.value.formatOnly = false;
  rebindForm.value.testedSignature = '';
  try {
    const res = await workforceApi.testChannelConnection(props.employee.id, platform, {...creds,...(realPlatform?{configVersion:props.employee.resourceVersions?.channels[platform]??0}:{})});
    if (sequence !== rebindCredentialTestSequence || props.employee.id !== employeeId || activeWorkspaceId.value !== workspaceId) return;
    rebindForm.value.testReceipt = res.channelTestReceipt;
    rebindForm.value.formatOnly = res.credentialStatus === 'format_only';
    rebindForm.value.testedSignature = `${platform}::${JSON.stringify(creds)}`;
    rebindForm.value.testLatency = res.latency;
    message.success(res.credentialStatus === 'format_only' ? '凭据格式检查通过，换绑后由正式连接验证。' : `新凭据验证通过（延迟 ${res.latency}），可提交换绑。`);
  } catch (err) {
    const errMsg = err instanceof Error ? err.message : '换绑凭证测试失败';
    rebindForm.value.errorMsg = errMsg;
    message.error(errMsg);
  } finally {
    rebindForm.value.isTesting = false;
  }
}

async function handleExecuteRebind() {
  if (props.canManage === false || rebindForm.value.isSaving || rebindConfigurationChanged.value) return;
  const empId = props.employee.id;
  const workspaceId = activeWorkspaceId.value;
  const platform = rebindPlatform.value;
  const validationErr = validateManualCredentials(platform, rebindForm.value);
  if (validationErr) {
    rebindForm.value.errorMsg = validationErr;
    message.error(validationErr);
    return;
  }
  if (isSwitchingRobot.value && (!rebindForm.value.confirmedImpact || (realPlatform && !rebindImpact.value))) {
    message.warning('请先勾选确认已了解换绑影响');
    return;
  }
  const creds = buildManualCredPayload(platform, rebindForm.value);
  const currentSig = `${platform}::${JSON.stringify(creds)}`;
  if (!realPlatform && ((!rebindForm.value.testReceipt && !(platform === 'wecom' && rebindForm.value.formatOnly)) || rebindForm.value.testedSignature !== currentSig)) {
    const errMsg = platform === 'wecom' ? '换绑前请先检查新凭据格式' : '换绑前必须对新凭证完成「测试连接」验证';
    rebindForm.value.errorMsg = errMsg;
    message.error(errMsg);
    return;
  }

  rebindForm.value.isSaving = true;
  rebindForm.value.errorMsg = '';
  const sequence = ++rebindCredentialTestSequence;
  const confirmedImpact = rebindForm.value.confirmedImpact;
  const current = () => !qrDisposed && props.canManage !== false && props.employee.id === empId && activeWorkspaceId.value === workspaceId
    && rebindPlatform.value === platform && rebindCredentialTestSequence === sequence && showRebindModal.value;
  try {
    const receipt = realPlatform
      ? (await workforceApi.testChannelConnection(empId, platform, {...creds, configVersion: props.employee.resourceVersions?.channels[platform] ?? 0})).channelTestReceipt
      : rebindForm.value.testReceipt;
    if (!current() || rebindConfigurationChanged.value) return;
    const res = await workforceApi.rebindChannelCredentials(empId, platform, {
      ...creds,
      channelTestReceipt: receipt,
      confirmedImpact,
      expectedVersion: props.employee.version
    });
    if (!current()) return;
    props.syncServerFieldIntoSnapshot(empId, res.employee, ['channels', 'humanConfirmationRules', 'status']);
    showRebindModal.value = false;
    message.success(
      res.switchingRobot
        ? '已保存新机器人。旧绑定的人员、部门和群引用已失效。'
        : `「${res.channel.platformName}」凭证已更新，原有工作群与策略保持不变。`
    );
  } catch (err) {
    if (current() && !props.handleOptimisticConflictError(err)) {
      const errMsg = err instanceof Error ? err.message : '换绑提交失败';
      rebindForm.value.errorMsg = errMsg;
      message.error(errMsg);
    }
  } finally {
    rebindForm.value.isSaving = false;
  }
}

async function retestChannel(channel: EmployeeChannelBinding) {
  message.loading(`正在检查「${channel.platformName}」连接状态...`);
  try {
    const res = await workforceApi.testChannelConnection(
      props.employee.id,
      channel.platform,
      { useStored: true }
    );
    const fresh = await workforceApi.getEmployee(props.employee.id);
    props.syncServerFieldIntoSnapshot(props.employee.id, fresh, ['channels']);
    if (res.ok) message.success(`「${channel.platformName}」连接正常。`);
    else message.warning(`「${channel.platformName}」尚未连接，请查看渠道状态。`);
  } catch (err) {
    message.error(err instanceof Error ? err.message : '渠道连接检查失败');
  }
}

async function toggleChannelStatus(channel: EmployeeChannelBinding) {
  if (realPlatform && channel.configurationChanged) return;
  if(props.canManage===false){message.warning('修改接入需要工作区管理员权限');return;}
  const empId = props.employee.id;
  const nextEnabled = channel.status === 'disabled';
  try {
    const saved = await workforceApi.toggleEmployeeChannelStatus(
      empId,
      channel.platform,
      nextEnabled,
      props.employee.version
    );
    props.syncServerFieldIntoSnapshot(empId, saved, ['channels', 'status']);
    if (nextEnabled) {
      message.success(`已启用「${channel.platformName}」`);
    } else {
      message.warning(`已停用「${channel.platformName}」`);
    }
  } catch (err) {
    if (!props.handleOptimisticConflictError(err)) {
      message.error(err instanceof Error ? err.message : '更新渠道启用状态失败');
    }
  }
}

// ================= 飞书多规则触发器 =================
const showFeishuRuleModal = ref(false);
const showFeishuRuleTesterModal = ref(false);
const editingFeishuRule = ref<FeishuTriggerRule | null>(null);
const feishuRuleTestInput = ref('');
const feishuRuleTestResult = ref<{ matched: boolean; ruleName?: string; reason?: string } | null>(null);

function openAddFeishuRule() {
  editingFeishuRule.value = {
    id: `fs-r-${Date.now()}`,
    type: 'keyword',
    name: '',
    pattern: '',
    enabled: true,
    description: ''
  };
  showFeishuRuleModal.value = true;
}

function openEditFeishuRule(rule: FeishuTriggerRule) {
  editingFeishuRule.value = JSON.parse(JSON.stringify(rule));
  showFeishuRuleModal.value = true;
}

function applyFeishuRegexTemplate(templatePattern: string, templateDesc: string) {
  if (editingFeishuRule.value) {
    editingFeishuRule.value.type = 'regex';
    editingFeishuRule.value.pattern = templatePattern;
    if (!editingFeishuRule.value.description) {
      editingFeishuRule.value.description = templateDesc;
    }
  }
}

async function saveFeishuRule() {
  if (!editingFeishuRule.value || !editingFeishuRule.value.name.trim() || !editingFeishuRule.value.pattern.trim()) {
    message.warning('请输入规则名称与匹配内容');
    return;
  }
  if (!feishuChannel.value) return;
  const draftRule = { ...editingFeishuRule.value };
  const ok = await persistChannelPatch('feishuRules', '已保存飞书触发规则！', (channels) => {
    const ch = channels.find(c => c.platform === 'feishu');
    if (!ch) return;
    if (!ch.capabilities.feishuRules) {
      ch.capabilities.feishuRules = [];
    }
    const idx = ch.capabilities.feishuRules.findIndex(r => r.id === draftRule.id);
    if (idx >= 0) {
      ch.capabilities.feishuRules[idx] = draftRule;
    } else {
      ch.capabilities.feishuRules.push(draftRule);
    }
  });
  if (ok) {
    showFeishuRuleModal.value = false;
  }
}

async function deleteFeishuRule(ruleId: string) {
  await persistChannelPatch('feishuRules', '已删除该触发规则', (channels) => {
    const ch = channels.find(c => c.platform === 'feishu');
    if (ch?.capabilities.feishuRules) {
      ch.capabilities.feishuRules = ch.capabilities.feishuRules.filter(r => r.id !== ruleId);
    }
  });
}

async function toggleFeishuRuleEnabled(ruleId: string, enabled: boolean) {
  await persistChannelPatch('feishuRules', enabled ? '已启用该飞书触发规则' : '已停用该飞书触发规则', (channels) => {
    const target = channels.find(c => c.platform === 'feishu')?.capabilities.feishuRules?.find(r => r.id === ruleId);
    if (target) {
      target.enabled = enabled;
    }
  });
}

async function testFeishuRulesMatch() {
  if (!feishuRuleTestInput.value.trim()) {
    message.warning('请输入待测试的群消息文本');
    return;
  }
  const rules = feishuChannel.value?.capabilities.feishuRules || [];
  try {
    const res = await workforceApi.testFeishuRules(feishuRuleTestInput.value.trim(), rules, activeWorkspaceId.value, props.employee.id);
    if (res.matchedCount > 0 && res.matched[0]) {
      const hit = res.matched[0];
      feishuRuleTestResult.value = {
        matched: true,
        ruleName: hit.ruleName,
        reason: hit.type === 'keyword' ? `命中了关键词「${hit.matchedText}」` : `命中了高级正则匹配文本「${hit.matchedText}」`
      };
    } else {
      feishuRuleTestResult.value = {
        matched: false,
        reason: '未命中任何已启用的触发规则'
      };
    }
  } catch (err) {
    message.error(err instanceof Error ? err.message : '规则模拟匹配失败');
  }
}

const channelOnboarding = {
  wecom: {
    title: '接入企业微信智能机器人',
    subtitle: '支持在群聊中被 @ 时响应、按需回顾近期群消息及跨群按需读取。',
    qrAlt: '企微授权二维码',
    qrGuide: '使用企业微信扫一扫，按提示创建机器人。',
    scanLinkLabel: '打开企微扫码页',
    consoleUrl: 'https://work.weixin.qq.com/wework_admin/frame#/aiHelper/list?from=manage_tools',
    consoleLabel: '前往企微后台创建机器人',
    idField: 'botId', idLabel: 'BotID',
    secretField: 'botSecret', secretLabel: 'Bot Secret',
  },
  dingtalk: {
    title: '接入钉钉群机器人',
    subtitle: '在工作群中响应成员 @ 提问与指令。',
    qrAlt: '钉钉授权二维码',
    qrGuide: '使用钉钉扫一扫，按提示创建机器人。',
    scanLinkLabel: '打开钉钉扫码页',
    consoleUrl: 'https://open-dev.dingtalk.com/fe/app#/corp/app',
    consoleLabel: '前往钉钉后台创建应用',
    idField: 'appKey', idLabel: 'Client ID（AppKey）',
    secretField: 'appSecret', secretLabel: 'Client Secret（AppSecret）',
  },
  feishu: {
    title: '接入飞书企业应用',
    subtitle: '支持响应普通群消息、多规则关键词/正则匹配、近期群消息上下文及跨群按需读取。',
    qrAlt: '飞书授权二维码',
    qrGuide: '使用飞书扫一扫，按提示创建企业自建应用。',
    scanLinkLabel: '打开飞书扫码页',
    consoleUrl: 'https://open.feishu.cn/app',
    consoleLabel: '前往飞书后台创建应用',
    idField: 'appId', idLabel: 'App ID',
    secretField: 'appSecret', secretLabel: 'App Secret',
  }
} as const;
const onboardingMeta = computed(() => channelOnboarding[currentPlatform.value]);

function channelDotClass(platform: PlatformType): string {
  const ch = props.employee.channels.find(c => c.platform === platform);
  if (!ch || ch.status === 'disabled') return 'is-muted';
  return ch.connectionState === 'connected' ? 'is-connected' : 'is-pending';
}

defineExpose({ resetQrState, stopQrTimers });
</script>

<template>
  <div class="tab-panel-inner">
    <NTabs v-model:value="currentPlatform" type="segment" size="small" class="channel-platform-tabs">
      <NTab name="feishu">
        <span class="ch-tab-label">
          <D2ChannelLogo platform="feishu" :size="17" show-label label-text="飞书机器人" />
          <span class="ch-tab-dot" :class="channelDotClass('feishu')" aria-hidden="true" />
        </span>
      </NTab>
      <NTab name="wecom">
        <span class="ch-tab-label">
          <D2ChannelLogo platform="wecom" :size="17" show-label label-text="企业微信智能机器人" />
          <span class="ch-tab-dot" :class="channelDotClass('wecom')" aria-hidden="true" />
        </span>
      </NTab>
      <NTab name="dingtalk">
        <span class="ch-tab-label">
          <D2ChannelLogo platform="dingtalk" :size="17" show-label label-text="钉钉机器人" />
          <span class="ch-tab-dot" :class="channelDotClass('dingtalk')" aria-hidden="true" />
        </span>
      </NTab>
    </NTabs>
    <div class="channel-config-box">
      <!-- 已接入状态：飞书 -->
      <div v-if="currentPlatform === 'feishu' && feishuChannel" class="ch-form-sections">
        <div class="channel-lifecycle-bar">
          <div class="clb-left">
            <D2ChannelLogo platform="feishu" :size="20" />
            <span class="clb-title">飞书智能机器人状态：</span>
            <WorkforceConnectionStatus :connected="feishuChannel.connectionState === 'connected'" :status-text="feishuChannel.statusText" :error-code="feishuChannel.connectionErrorCode" :real-backend="realPlatform"/>
            <NTag size="small" :type="feishuChannel.permissionReady === true ? 'success' : feishuChannel.permissionReady === false ? 'warning' : 'default'">
              {{ feishuChannel.permissionReady === true ? '权限就绪' : feishuChannel.permissionReady === false ? '权限未就绪' : '权限未确认' }}
            </NTag>
            <NTag v-if="realPlatform" size="small" :type="directoryAvailability.type">{{ directoryAvailability.label }}</NTag>
            <span class="clb-appid">App ID: <code>{{ feishuChannel.appId }}</code></span>
          </div>
          <div class="clb-actions">
            <NButton v-if="!realPlatform" size="tiny" secondary @click="retestChannel(feishuChannel)">检查连接</NButton>
            <NButton size="tiny" secondary :disabled="feishuChannel.configurationChanged === true" @click="openRebindModal('feishu')">更新凭据 / 换绑</NButton>
            <NButton size="tiny" :type="feishuChannel.status === 'disabled' ? 'success' : 'default'" secondary :disabled="feishuChannel.configurationChanged === true" @click="toggleChannelStatus(feishuChannel)">
              {{ feishuChannel.status === 'disabled' ? '启用' : '停用' }}
            </NButton>
          </div>
        </div>

        <div v-if="realPlatform && feishuChannel.directoryStatus" class="ch-section">
          <p class="field-note" role="status">{{ directoryStatusText(feishuChannel) }}</p>
        </div>
      </div>

      <!-- 已接入状态：企业微信 -->
      <div v-else-if="currentPlatform === 'wecom' && wecomChannel" class="ch-form-sections">
        <div class="channel-lifecycle-bar">
          <div class="clb-left">
            <D2ChannelLogo platform="wecom" :size="20" />
            <span class="clb-title">企业微信智能机器人状态：</span>
            <WorkforceConnectionStatus :connected="wecomChannel.connectionState === 'connected'" :status-text="wecomChannel.statusText" :error-code="wecomChannel.connectionErrorCode" :real-backend="realPlatform"/>
            <WorkforceWecomContactPermission v-if="realPlatform && canManage && isActiveTab"
              :workspace="activeWorkspaceId" :employee="employee.id" :binding="wecomChannel.connectionId"
              :generation="wecomChannel.connectionGeneration" :revision="wecomChannel.configRevision"
              :eligible="wecomChannel.status !== 'disabled' && wecomChannel.identityConfirmed === true && !wecomChannel.configurationChanged" />
            <span class="clb-appid">BotID: <code>{{ wecomChannel.botId || '未设置' }}</code></span>
          </div>
          <div class="clb-actions">
            <NButton v-if="!realPlatform" size="tiny" secondary @click="retestChannel(wecomChannel)">检查连接</NButton>
            <NButton size="tiny" secondary :disabled="wecomChannel.configurationChanged === true" @click="openRebindModal('wecom')">更新凭据 / 换绑</NButton>
            <NButton size="tiny" :type="wecomChannel.status === 'disabled' ? 'success' : 'default'" secondary :disabled="wecomChannel.configurationChanged === true" @click="toggleChannelStatus(wecomChannel)">
              {{ wecomChannel.status === 'disabled' ? '启用' : '停用' }}
            </NButton>
          </div>
        </div>
        <p v-if="realPlatform && wecomChannel.connectionState === 'connected' && wecomChannel.identityConfirmed === false" class="field-note">
          请向机器人发送一条私聊以确认身份，随后可以核验联系人。
        </p>
      </div>

      <!-- 已接入状态：钉钉 -->
      <div v-else-if="currentPlatform === 'dingtalk' && dingtalkChannel" class="ch-form-sections">
        <div class="channel-lifecycle-bar">
          <div class="clb-left">
            <D2ChannelLogo platform="dingtalk" :size="20" />
            <span class="clb-title">钉钉智能机器人状态：</span>
            <WorkforceConnectionStatus :connected="dingtalkChannel.connectionState === 'connected'" :status-text="dingtalkChannel.statusText" :error-code="dingtalkChannel.connectionErrorCode" :real-backend="realPlatform"/>
            <NTag v-if="!realPlatform" size="small" :type="dingtalkChannel.permissionReady === true ? 'success' : dingtalkChannel.permissionReady === false ? 'warning' : 'default'">
              {{ dingtalkChannel.permissionReady === true ? '权限就绪' : dingtalkChannel.permissionReady === false ? '权限未就绪' : '权限未确认' }}
            </NTag>
            <WorkforceStatusHelp v-if="dingtalkDirectoryAuthorizationFailed"
              :label="directoryAvailability.label" :lines="dingtalkDirectoryHelp" />
            <NTag v-else-if="realPlatform" size="small" :type="directoryAvailability.type">{{ directoryAvailability.label }}</NTag>
            <WorkforceStatusHelp v-if="dingtalkSendPermissionMissing"
              label="主动发送权限" :lines="dingtalkSendHelp" />
            <span class="clb-appid">Client ID（AppKey）: <code>{{ dingtalkChannel.appKey }}</code></span>
          </div>
          <div class="clb-actions">
            <NButton v-if="!realPlatform" size="tiny" secondary @click="retestChannel(dingtalkChannel)">检查连接</NButton>
            <NButton size="tiny" secondary :disabled="dingtalkChannel.configurationChanged === true" @click="openRebindModal('dingtalk')">更新凭据 / 换绑</NButton>
            <NButton size="tiny" :type="dingtalkChannel.status === 'disabled' ? 'success' : 'default'" secondary :disabled="dingtalkChannel.configurationChanged === true" @click="toggleChannelStatus(dingtalkChannel)">
              {{ dingtalkChannel.status === 'disabled' ? '启用' : '停用' }}
            </NButton>
          </div>
        </div>

        <div v-if="realPlatform && dingtalkChannel.directoryStatus" class="ch-section">
          <p class="field-note" role="status">{{ directoryStatusText(dingtalkChannel) }}</p>
          <p v-if="canManage !== false && dingtalkDirectoryAuthorizationFailed" class="field-note">
            <a :href="channelOnboarding.dingtalk.consoleUrl" target="_blank" rel="noopener noreferrer">去钉钉补充通讯录权限 ↗</a>
          </p>
        </div>
      </div>

      <div v-if="currentPlatform === 'wecom'" class="ch-notice dingtalk-notice">
        <strong>企业微信平台能力限制：</strong>
        <span>企微群机器人仅支持在群聊被 @ 后响应（暂不支持普通群消息监听与关键词触发）；支持被 @ 后按需读取近期群消息。</span>
      </div>
      <div v-else-if="currentPlatform === 'dingtalk'" class="ch-notice dingtalk-notice">
        <strong>钉钉平台能力限制：</strong>
        <span>钉钉群机器人当前仅支持在群聊中被 @ 后响应，暂不支持普通群消息监听、关键词触发、近期群消息读取及群消息共享。</span>
      </div>

      <NAlert v-if="channelReadError" type="error" role="alert" aria-label="渠道配置读取失败">
        <p>渠道配置读取失败。{{ channelReadError }}</p>
        <NButton
          size="small"
          :loading="retryingChannel === currentPlatform"
          :disabled="!!retryingChannel"
          @click="emit('retry-channel')"
        >重试读取渠道</NButton>
      </NAlert>

      <div v-if="!currentChannel && !channelReadError" class="unconnected-channel-card">
        <div class="ucc-header-row">
          <div class="ucc-title-col">
            <h3 class="ucc-logo-heading"><D2ChannelLogo :platform="currentPlatform" :size="24" /> {{ onboardingMeta.title }}</h3>
            <p>{{ onboardingMeta.subtitle }}</p>
          </div>
          <NButton
            v-if="channelOnboardingMode[currentPlatform] !== 'choice'"
            class="onboarding-mode-link"
            text
            type="primary"
            :disabled="canManage === false || appBindForm.isSaving"
            @click="channelOnboardingMode[currentPlatform] === 'qrcode' ? chooseExistingRobot() : chooseCreateRobot()"
          >
            {{ channelOnboardingMode[currentPlatform] === 'qrcode' ? '绑定已有机器人' : '创建智能机器人' }}
          </NButton>
        </div>

        <div v-if="channelOnboardingMode[currentPlatform] === 'choice'" class="onboarding-choice-actions">
          <NButton type="primary" :disabled="canManage === false" @click="chooseCreateRobot">创建智能机器人</NButton>
          <NButton secondary :disabled="canManage === false" @click="chooseExistingRobot">绑定已有机器人</NButton>
        </div>
        <div v-else-if="channelOnboardingMode[currentPlatform] === 'qrcode' && usingWecomPopup" class="onboarding-qr-pane">
          <p v-if="qrConfirming" class="qr-note" role="status">正在保存企微机器人凭据…</p>
          <p v-else-if="wecomOpening" class="qr-note" role="status">请在企业微信官方窗口扫码，完成后此处会自动保存。</p>
          <p v-else class="qr-note">在企业微信官方窗口扫码创建并授权机器人。</p>
          <NAlert v-if="qrError" type="warning" role="status">{{ qrError }}</NAlert>
          <div class="qr-actions">
            <NButton v-if="pendingWecomCredentials && !qrConfirming" text type="primary" @click="saveWecomRegistration(true)">重试保存</NButton>
            <NButton v-else-if="!wecomOpening && !qrConfirming" text type="primary" @click="openWecomRegistration">
              {{ onboardingMeta.scanLinkLabel }} <span aria-hidden="true">↗</span>
            </NButton>
            <NButton v-if="wecomOpening" text size="tiny" @click="wecomAttempt?.cancel()">停止等待</NButton>
            <span class="qr-name">机器人名称：{{ employee.name }} <NButton text size="tiny" @click="copyEmployeeName">复制</NButton></span>
          </div>
        </div>

        <div v-else-if="channelOnboardingMode[currentPlatform] === 'qrcode'" class="onboarding-qr-pane">
          <div v-if="qrLoading" role="status">正在获取二维码…</div>
          <div v-else-if="qrLink || (qrSession?.qrSvgDataUrl && qrSession.status === 'waiting_scan')" class="qr-pane-body">
            <div class="qr-code-art">
              <FeishuRegistrationQr
                v-if="qrLink && currentPlatform === 'feishu'"
                :value="qrPayload"
                :compact-size="168"
                role="img"
                :aria-label="onboardingMeta.qrAlt"
              />
              <NQrCode v-else-if="qrLink" :value="qrPayload" :size="168" type="svg" role="img" :aria-label="onboardingMeta.qrAlt" />
              <img v-else :src="qrSession?.qrSvgDataUrl" :alt="onboardingMeta.qrAlt" class="qr-svg-img" />
            </div>
            <div class="qr-details">
              <p>{{ onboardingMeta.qrGuide }}</p>
              <a v-if="qrLink" class="onboarding-link" :href="qrLink" target="_blank" rel="noopener noreferrer">
                {{ onboardingMeta.scanLinkLabel }} <span aria-hidden="true">↗</span>
              </a>
              <span v-if="qrSecondsLeft > 0" class="qr-countdown">有效期剩余 {{ qrFormattedCountdown }}</span>
              <p v-if="realPlatform && currentPlatform !== 'feishu'" class="qr-name">
                机器人名称：{{ employee.name }} <NButton text size="tiny" @click="copyEmployeeName">复制</NButton>
              </p>
            </div>
          </div>
          <NAlert v-else-if="qrSession?.feishuFailure" type="warning" role="status" :title="qrSession.feishuFailure.reason">
            <p v-if="qrSession.feishuFailure.detail">{{ qrSession.feishuFailure.detail }}</p>
            <p>{{ qrSession.feishuFailure.automaticRetry ? '后台将自动重试。' : '自动检查已停止。' }}</p>
            <a v-if="qrSession.authorizationUrl" :href="qrSession.authorizationUrl" target="_blank" rel="noopener noreferrer">去飞书补授权 ↗</a>
            <p v-if="qrSession.feishuFailure.canReauthorize">重新授权会结束这次接入。再次扫码后，飞书将创建新的应用。</p>
          </NAlert>
          <div v-else-if="qrSession?.status === 'scanned_confirming'" role="status">
            {{ qrSession.credentialSaved ? '已取得凭据，后台会继续验证。' : '正在确认接入…' }}
            <a v-if="qrSession.authorizationUrl" :href="qrSession.authorizationUrl" target="_blank" rel="noopener noreferrer">去飞书补授权 ↗</a>
          </div>
          <div v-else-if="!qrError" role="status">
            {{ qrSession?.status === 'authorized' ? '渠道配置已保存。' : '正在查询扫码状态…' }}
          </div>

          <NAlert v-if="qrError" type="warning" role="status">{{ qrError }}</NAlert>
          <div class="qr-actions">
            <NButton v-if="qrQueryTimedOut" size="tiny" text type="primary" @click="resumeQrPolling">继续查询状态</NButton>
            <NButton v-if="qrOutcomeUnknown && !qrSession?.credentialSaved" size="tiny" text type="primary" :disabled="qrLoading" @click="refreshQrCode(true)">重新发起扫码</NButton>
            <NButton v-else-if="!qrCanRecheck && !qrLoading && (qrError || !qrLink) && qrSession?.status !== 'authorized'" size="tiny" text type="primary" @click="refreshQrCode()">刷新二维码</NButton>
            <NButton
              v-if="realPlatform && activeQrSessionId && !qrSession?.feishuFailure && qrSession?.status !== 'expired' && qrSession?.status !== 'authorized' && qrSession?.registrationState !== 'ready'"
              size="tiny" text @click="cancelQrWaiting"
            >停止等待</NButton>
            <NButton v-if="realPlatform && qrCanRecheck" size="tiny" text type="primary" :disabled="qrLoading" :loading="qrConfirming" @click="retryQrCheck">
              {{ currentPlatform === 'dingtalk' ? '重新检查并保存连接' : '重新检查' }}
            </NButton>
            <NButton v-if="realPlatform && qrSession?.feishuFailure?.canReauthorize" size="tiny" type="primary" :disabled="qrLoading || qrConfirming" @click="loadQrCodeForPlatform('feishu', true, qrSession.sessionId)">重新授权</NButton>
            <NButton
              v-if="isDevMode && !realPlatform"
              size="tiny" :loading="qrConfirming"
              :disabled="qrLoading || !activeQrSessionId || qrSession?.status === 'expired'"
              @click="handleChannelQrSuccess(currentPlatform)"
            >调试：模拟移动端管理员确认授权</NButton>
          </div>
        </div>

        <div v-else class="onboarding-form-pane">
          <div class="ofp-form">
            <NForm label-placement="top">
              <NFormItem :label="onboardingMeta.idLabel">
                <NInput
                  v-model:value="appBindForm[onboardingMeta.idField]"
                  :input-props="{'aria-label': onboardingMeta.idLabel}"
                  @update:value="onAppBindFieldChange"
                />
              </NFormItem>
              <NFormItem :label="onboardingMeta.secretLabel">
                <NInput
                  v-model:value="appBindForm[onboardingMeta.secretField]"
                  type="password" show-password-on="click"
                  :input-props="{'aria-label': onboardingMeta.secretLabel, autocomplete: 'off'}"
                  @update:value="onAppBindFieldChange"
                />
              </NFormItem>
            </NForm>

            <NAlert v-if="appBindForm.errorMsg" type="error" role="alert">{{ appBindForm.errorMsg }}</NAlert>
            <div class="form-test-actions">
              <NButton v-if="!realPlatform" size="small" secondary :loading="appBindForm.isTesting" :disabled="appBindForm.isSaving" @click="runChannelConnectivityTest()">
                {{ currentPlatform === 'wecom' ? '检查格式' : '验证连接' }}
              </NButton>
              <NButton
                type="primary" size="small" :loading="appBindForm.isSaving"
                :disabled="appBindForm.isTesting || (!realPlatform && !appBindForm.testReceipt && !appBindForm.formatOnly) || !appBindForm[onboardingMeta.idField].trim() || !appBindForm[onboardingMeta.secretField].trim()"
                @click="handleSaveAppBinding()"
              >{{ realPlatform ? '验证并保存' : '确认绑定并启用' }}</NButton>
            </div>
            <p v-if="appBindForm.testReceipt || appBindForm.formatOnly" class="tr-receipt-msg" role="status">
              {{ appBindForm.formatOnly ? '格式检查通过，保存后验证连接' : `凭据验证通过（延迟 ${appBindForm.testLatency}）` }}
            </p>
          </div>
          <a class="onboarding-link" :href="onboardingMeta.consoleUrl" target="_blank" rel="noopener noreferrer">
            {{ onboardingMeta.consoleLabel }} <span aria-hidden="true">↗</span>
          </a>
        </div>
      </div>

      <template v-else-if="!realPlatform && currentPlatform === 'feishu' && feishuChannel">
        <div class="ch-section">
          <div class="cs-header-flex">
            <div>
              <h4 class="cs-title">群触发与规则策略</h4>
              <p class="cs-sub">
                支持全量监听、关键词与正则表达式匹配，命中任一规则即可触发员工。
              </p>
            </div>
            <div style="display: flex; gap: 8px; flex-wrap: wrap; align-items: center;">
              <template v-if="isFeishuPolicyDirty">
                <NButton size="tiny" :disabled="isSavingChannelSection" @click="cancelFeishuPolicy">取消</NButton>
                <NButton size="tiny" type="primary" :loading="isSavingChannelSection" @click="saveFeishuPolicy">保存</NButton>
              </template>
              <NButton size="tiny" type="primary" secondary @click="openAddFeishuRule">
                + 新增匹配规则
              </NButton>
              <NButton size="tiny" secondary @click="feishuRuleTestInput = ''; feishuRuleTestResult = null; showFeishuRuleTesterModal = true">
                规则命中测试器
              </NButton>
            </div>
          </div>

          <div class="policy-rows">
            <div class="feishu-rules-list-box">
              <div class="frl-header">
                <span class="frl-title">已配置的群触发规则列表 (命中任一即触发)：</span>
              </div>
              <div class="rules-table-scroll">
                <table class="feishu-rules-table">
                  <thead>
                    <tr>
                      <th style="width: 140px;">规则名称</th>
                      <th style="width: 100px;">匹配类型</th>
                      <th>匹配模式 / 关键词 / 正则表达式</th>
                      <th style="width: 90px;">状态</th>
                      <th style="width: 110px;">操作</th>
                    </tr>
                  </thead>
                  <tbody>
                    <tr v-for="rule in (feishuChannel.capabilities.feishuRules || [])" :key="rule.id">
                      <td><strong>{{ rule.name }}</strong></td>
                      <td>
                        <NTag size="tiny" :type="rule.type === 'keyword' ? 'info' : 'warning'" round>
                          {{ rule.type === 'keyword' ? '关键词' : '高级正则' }}
                        </NTag>
                      </td>
                      <td>
                        <code>{{ rule.pattern }}</code>
                        <span v-if="rule.description" class="rule-desc-inline">({{ rule.description }})</span>
                      </td>
                      <td>
                        <NSwitch
                          size="small"
                          :value="rule.enabled"
                          :disabled="isSavingChannelSection"
                          @update:value="v => toggleFeishuRuleEnabled(rule.id, v)"
                        />
                      </td>
                      <td>
                        <NButton size="tiny" quaternary @click="openEditFeishuRule(rule)">编辑</NButton>
                        <NButton size="tiny" type="error" quaternary :disabled="isSavingChannelSection" @click="deleteFeishuRule(rule.id)">删除</NButton>
                      </td>
                    </tr>
                  </tbody>
                </table>
              </div>
            </div>

            <div class="p-row">
              <div class="p-info">
                <strong>保留近期群聊历史消息作为上下文</strong>
                <p>当机器人被 @ 提及或命中规则时，自动回顾前 N 条群历史消息以理解背景语境。</p>
              </div>
              <NSwitch
                v-model:value="feishuChannel.capabilities.recentContextEnabled"
                @update:value="emit('dirty')"
              />
            </div>

            <div v-if="feishuChannel.capabilities.recentContextEnabled" class="slider-row">
              <span class="slider-lbl">保留历史消息条数：</span>
              <NSlider
                :value="feishuChannel.capabilities.recentContextWindow ?? 20"
                :min="5"
                :max="100"
                :step="5"
                class="context-window-slider"
                @update:value="(v: number) => { feishuChannel!.capabilities.recentContextWindow = v; emit('dirty'); }"
              />
              <span class="slider-val">{{ feishuChannel.capabilities.recentContextWindow }} 条近期消息</span>
            </div>
            <p style="margin: 4px 0 0 0; font-size: 12px; color: var(--id-text-secondary);">
              支持设置 5–100 条近期群消息。
            </p>
          </div>
        </div>
      </template>

      <template v-else-if="!realPlatform && currentPlatform === 'wecom' && wecomChannel">
        <div class="ch-section">
          <div class="card-edit-header">
            <h4 class="cs-title">群聊 @ 交互与近期群消息补充</h4>
            <div v-if="isWecomPolicyDirty" class="card-edit-actions">
              <NButton size="small" :disabled="isSavingChannelSection" @click="cancelWecomPolicy">取消</NButton>
              <NButton size="small" type="primary" :loading="isSavingChannelSection" @click="saveWecomPolicy">保存</NButton>
            </div>
          </div>
          <div class="policy-rows">
            <div class="p-row disabled-row">
              <div class="p-info">
                <strong>普通群消息全量监听 & 关键词触发</strong>
                <p class="text-muted">企业微信暂不支持群内非 @ 消息的监听与关键词触发。</p>
              </div>
              <NTag size="tiny" type="default">平台不支持</NTag>
            </div>

            <div class="p-row">
              <div class="p-info">
                <strong>被 @ 时按条件回顾近期群消息</strong>
                <p>被 @ 唤起时，自动回顾本群前 N 条消息以理解语境。</p>
              </div>
              <NSwitch
                v-model:value="wecomChannel.capabilities.recentContextEnabled"
                @update:value="emit('dirty')"
              />
            </div>

            <div v-if="wecomChannel.capabilities.recentContextEnabled" class="slider-row">
              <span class="slider-lbl">回顾消息条数：</span>
              <NSlider
                :value="wecomChannel.capabilities.recentContextWindow ?? 20"
                :min="5"
                :max="100"
                :step="5"
                class="context-window-slider"
                @update:value="(v: number) => { wecomChannel!.capabilities.recentContextWindow = v; emit('dirty'); }"
              />
              <span class="slider-val">{{ wecomChannel.capabilities.recentContextWindow }} 条近期消息</span>
            </div>
            <p style="margin: 4px 0 0 0; font-size: 12px; color: var(--id-text-secondary);">
              支持设置 5–100 条近期群消息。
            </p>
          </div>
        </div>
      </template>

      <template v-else-if="currentPlatform === 'dingtalk' && dingtalkChannel">
        <div class="ch-section">
          <h4 class="cs-title">{{ realPlatform ? '钉钉群配置' : '群聊 @ 交互支持' }}</h4>
          <div class="policy-rows">
            <div class="p-row">
              <div class="p-info">
                <strong>群聊 @ 消息响应</strong>
                <p>支持接收钉钉工作群中 @ 机器人的指令。Stream 已连接不代表实际消息收发成功，仍需在群内验证。</p>
              </div>
              <NTag size="small" type="default">尚未验证</NTag>
            </div>

            <div v-if="!realPlatform" class="p-row disabled-row">
              <div class="p-info">
                <strong>普通消息监听、关键词触发、群历史读取</strong>
                <p class="text-muted">钉钉平台暂不支持上述高级群消息能力。</p>
              </div>
              <NTag size="tiny" type="default">平台不支持</NTag>
            </div>
          </div>
        </div>
      </template>

      <WorkforceGroupPolicies
        v-else-if="realPlatform && currentChannel && isActiveTab"
        :channel="currentPlatform"
        :workspace="activeWorkspaceId"
        :employee-id="employee.id"
        :can-manage="canManage"
      />

      <NAlert v-if="channelNeedsWhitelist" type="info" aria-label="渠道使用范围提示">
        <p>员工使用白名单模式。当前渠道还没有允许的人员或部门，需配置白名单后才能私聊。{{ groupMembersAllowed ? '已开启群内可用，获准群内仍可按渠道支持的方式使用。' : '群聊也按白名单判断。' }}</p>
        <NButton v-if="canManage" text type="primary" @click="emit('configure-access')">配置使用范围</NButton>
      </NAlert>

      <div v-if="currentPlatform === 'feishu' && feishuChannel" class="ch-section">
        <details>
          <summary class="cs-title" style="cursor: pointer;">
            业务能力授权与具体资源访问状态
            <span class="cs-sub">· {{ feishuChannel.businessCapabilities?.length ? feishuChannel.businessCapabilities.length + ' 项业务模块检查结果' : '尚无权限检查结果' }}</span>
          </summary>
          <p class="cs-sub" style="margin: 12px 0;">
            清晰区分开放平台接口权限与具体业务资源授权状态。
            <span class="text-note">（重要说明：开放平台接口权限申请成功 ≠ 具体业务资源访问权。例如：云文档 API 权限已获批，但具体文档仍需在飞书文档内将机器人添加为协作者）</span>
          </p>
          <div class="business-capabilities-table">
            <table class="cap-matrix-table">
              <thead>
                <tr>
                  <th style="width: 140px;">业务模块</th>
                  <th style="width: 140px;">开放平台接口权限</th>
                  <th style="width: 150px;">实际资源访问状态</th>
                  <th>授权明细与操作指引</th>
                </tr>
              </thead>
              <tbody>
                <tr v-if="!feishuChannel.businessCapabilities?.length"><td colspan="4">尚无权限检查结果</td></tr>
                <tr v-for="cap in (feishuChannel.businessCapabilities || [])" :key="cap.id">
                  <td><strong>{{ cap.name }}</strong></td>
                  <td>
                    <NTag size="tiny" :type="cap.platformAuthStatus === 'authorized' ? 'success' : cap.platformAuthStatus === 'partial' ? 'warning' : 'default'">
                      {{ cap.platformAuthStatus === 'authorized' ? '已授权' : cap.platformAuthStatus === 'partial' ? '部分授权' : cap.platformAuthStatus === 'unauthorized' ? '未授权' : cap.platformAuthStatus === 'not_detected' ? '未检测到授权' : '状态未知' }}
                    </NTag>
                  </td>
                  <td>
                    <NTag size="tiny" :type="cap.resourceAccessStatus === 'ready' ? 'success' : cap.resourceAccessStatus === 'pending_collaborator' ? 'warning' : 'default'">
                      {{ cap.resourceAccessStatus === 'ready' ? '已就绪' : cap.resourceAccessStatus === 'pending_collaborator' ? '待添加协作人' : cap.resourceAccessStatus === 'not_configured' ? '未配置' : '未确认' }}
                    </NTag>
                  </td>
                  <td class="cap-detail-cell">
                    <span>{{ cap.statusDetail }}</span>
                    <a v-if="realPlatform && cap.id === 'feishu-card-callback'" href="https://open.feishu.cn/document/event-subscription-guide/callback-subscription/add-callback"
                      target="_blank" rel="noopener noreferrer">飞书配置说明 ↗</a>
                    <a v-if="realPlatform && canManage !== false && cap.authorizationUrl" :href="cap.authorizationUrl"
                      target="_blank" rel="noopener noreferrer">去飞书补授权 ↗</a>
                  </td>
                </tr>
              </tbody>
            </table>
          </div>
        </details>
      </div>
    </div>

    <!-- 智能换绑判定弹窗 -->
    <NModal v-model:show="showRebindModal" preset="card" :title="`渠道凭据维护与换绑 · ${rebindPlatform === 'feishu' ? '飞书自建机器人' : rebindPlatform === 'wecom' ? '企业微信智能机器人' : '钉钉 Stream 机器人'}`" style="width: min(580px, 94vw);">
      <div class="rebind-modal-content">
        <div style="margin-bottom: 14px;">
          <NAlert v-if="!isSwitchingRobot" type="info" :show-icon="true">
            更新凭据后，平台会重新验证当前机器人连接。
          </NAlert>
          <NAlert v-else type="warning" :show-icon="true" aria-label="换绑权限影响">
            <p>换绑后，旧绑定的人员、部门和群引用将失效。新机器人将按剩余配置鉴权，原工作群需要添加新机器人。</p>
            <template v-if="rebindImpact">
              <ul style="margin: 6px 0 0 16px; padding: 0; line-height: 1.6; font-size: 12px;">
                <li>使用范围将失效 {{ rebindImpact.allow_entries }} 条允许项、{{ rebindImpact.deny_entries }} 条拒绝项。</li>
                <li v-if="rebindImpact.deny_entries">旧名单中的人或部门将失去对应的拒绝限制，使用范围可能扩大。</li>
                <li v-if="rebindImpact.package_names.length">能力包按剩余名单重新鉴权，范围可能扩大或缩小（{{ rebindImpact.package_names.join('、') }}）。</li>
                <li v-if="rebindImpact.package_deny_names.length">这些能力包将失去旧拒绝项，权限可能扩大（{{ rebindImpact.package_deny_names.join('、') }}）。</li>
                <li v-if="rebindImpact.sharing_names.length">含旧群或人员的共享方向将失效（{{ rebindImpact.sharing_names.join('、') }}）。</li>
                <li v-if="rebindImpact.notification_names.length">这些触发器引用的旧群通知目标将失效（{{ rebindImpact.notification_names.join('、') }}）。</li>
              </ul>
            </template>
            <template v-else-if="rebindImpactError">
              <p role="alert">{{ rebindImpactError }}</p>
              <NButton size="small" @click="rebindImpactRetry++">重新读取换绑影响</NButton>
            </template>
            <p v-else-if="realPlatform" role="status">正在检查换绑影响…</p>
          </NAlert>
        </div>

        <NForm label-placement="top">
          <template v-if="rebindPlatform === 'wecom'">
            <NFormItem label="企业微信智能机器人 BotID (修改将触发换绑)">
              <NInput v-model:value="rebindForm.botId" placeholder="例如：bot_kiko_8829" @update:value="onRebindFieldChange" />
            </NFormItem>
            <NFormItem label="Secret">
              <NInput v-model:value="rebindForm.botSecret" type="password" show-password-on="click" placeholder="输入新的 Bot Secret" @update:value="onRebindFieldChange" />
            </NFormItem>
          </template>
          <template v-else-if="rebindPlatform === 'dingtalk'">
            <NFormItem label="Client ID（AppKey，修改将触发换绑）">
              <NInput v-model:value="rebindForm.appKey" placeholder="例如：ding_stream_client" @update:value="onRebindFieldChange" />
            </NFormItem>
            <NFormItem label="Client Secret（AppSecret）">
              <NInput v-model:value="rebindForm.appSecret" type="password" show-password-on="click" placeholder="输入新的 AppSecret" @update:value="onRebindFieldChange" />
            </NFormItem>
          </template>
          <template v-else>
            <NFormItem label="飞书 App ID (修改将触发换绑)">
              <NInput v-model:value="rebindForm.appId" placeholder="例如：cli_a891024fe" @update:value="onRebindFieldChange" />
            </NFormItem>
            <NFormItem label="更新 App Secret">
              <NInput v-model:value="rebindForm.appSecret" type="password" show-password-on="click" placeholder="输入新的 App Secret" @update:value="onRebindFieldChange" />
            </NFormItem>
          </template>

          <NAlert v-if="rebindForm.errorMsg" type="error" :show-icon="true" style="margin-bottom: 10px;">
            {{ rebindForm.errorMsg }}
          </NAlert>

          <div style="display: flex; align-items: center; gap: 10px; flex-wrap: wrap; margin-bottom: 10px;">
            <NButton v-if="!realPlatform" size="small" secondary :loading="rebindForm.isTesting" @click="runRebindConnectivityTest">
              {{ rebindPlatform === 'wecom' ? '检查新凭据格式' : '验证新凭证连接' }}
            </NButton>
            <span v-if="rebindForm.testReceipt || rebindForm.formatOnly" style="font-size: 12px; color: var(--id-status-success);">
              {{ rebindForm.formatOnly ? '格式检查通过，换绑后验证连接' : `新凭据验证通过（延迟 ${rebindForm.testLatency}）` }}
            </span>
          </div>

          <div v-if="isSwitchingRobot" style="margin-top: 12px; padding: 10px; background: var(--id-status-danger-soft); border: 1px solid var(--id-status-danger-border); border-radius: var(--id-radius-md);">
            <NCheckbox v-model:checked="rebindForm.confirmedImpact" :disabled="realPlatform && !rebindImpact">
              <strong style="color: var(--id-status-danger);">我已了解权限变化，确认换绑为新机器人</strong>
            </NCheckbox>
          </div>
        </NForm>
      </div>
      <template #footer>
        <div style="display: flex; justify-content: flex-end; gap: 8px; flex-wrap: wrap;">
          <span v-if="rebindConfigurationChanged" role="status">渠道配置已变更，请关闭窗口并重新加载配置。</span>
          <NButton @click="showRebindModal = false">取消</NButton>
          <NButton
            type="primary"
            :loading="rebindForm.isSaving"
            :disabled="rebindConfigurationChanged || (!realPlatform && !rebindForm.testReceipt && !(rebindPlatform === 'wecom' && rebindForm.formatOnly)) || (isSwitchingRobot && (!rebindForm.confirmedImpact || (realPlatform && !rebindImpact)))"
            @click="handleExecuteRebind"
          >
            {{ isSwitchingRobot ? '确认换绑新机器人' : realPlatform ? '验证并更新凭据' : '保存更新凭据' }}
          </NButton>
        </div>
      </template>
    </NModal>

    <!-- 飞书多规则匹配创建/编辑弹窗 -->
    <NModal v-model:show="showFeishuRuleModal" preset="card" :title="editingFeishuRule?.id ? '编辑飞书群触发规则' : '新增飞书群触发规则'" style="width: min(540px, 94vw);">
      <NForm v-if="editingFeishuRule" label-placement="top">
        <NFormItem label="规则名称">
          <NInput v-model:value="editingFeishuRule.name" placeholder="例如：PR/MR 合并审查、缺陷报警" />
        </NFormItem>
        <NFormItem label="匹配方式">
          <NRadioGroup v-model:value="editingFeishuRule.type">
            <NSpace wrap>
              <NRadio value="keyword">关键词匹配 (逗号分隔多个关键词)</NRadio>
              <NRadio value="regex">高级正则表达式匹配</NRadio>
            </NSpace>
          </NRadioGroup>
        </NFormItem>

        <div v-if="editingFeishuRule.type === 'regex'" style="margin-bottom: 12px; background: var(--id-bg-subtle); border: 1px solid var(--id-border-subtle); border-radius: var(--id-radius-md); padding: 10px;">
          <span style="font-size: 12px; font-weight: 600; color: var(--id-text-primary);">常见正则模板快捷填入：</span>
          <div style="display: flex; flex-wrap: wrap; gap: 6px; margin-top: 6px;">
            <NButton
              v-for="tpl in feishuRegexTemplates"
              :key="tpl.pattern"
              size="tiny"
              secondary
              @click="applyFeishuRegexTemplate(tpl.pattern, tpl.description)"
            >
              {{ tpl.label }}
            </NButton>
          </div>
        </div>

        <NFormItem :label="editingFeishuRule.type === 'keyword' ? '匹配关键词 (支持逗号分隔多个)' : '正则表达式 (含斜杠与修饰符)'">
          <NInput v-model:value="editingFeishuRule.pattern" :placeholder="editingFeishuRule.type === 'keyword' ? '例如：/review, /fix, 前端报错' : '例如：/(?:review|pr|mr)\\s*#?\\d+/i'" />
        </NFormItem>
        <NFormItem label="规则说明 (可选)">
          <NInput :value="editingFeishuRule.description || ''" placeholder="说明该规则生效场景" @update:value="v => editingFeishuRule!.description = v" />
        </NFormItem>
        <NFormItem label="启用该规则">
          <NSwitch v-model:value="editingFeishuRule.enabled" />
        </NFormItem>
      </NForm>
      <template #footer>
        <div style="display: flex; justify-content: flex-end; gap: 8px; flex-wrap: wrap;">
          <NButton :disabled="isSavingChannelSection" @click="showFeishuRuleModal = false">取消</NButton>
          <NButton type="primary" :loading="isSavingChannelSection" @click="saveFeishuRule">保存规则</NButton>
        </div>
      </template>
    </NModal>

    <!-- 飞书规则命中实时测试器 -->
    <NModal v-model:show="showFeishuRuleTesterModal" preset="card" title="飞书群消息规则命中实时测试器" style="width: min(520px, 94vw);">
      <div style="display: flex; flex-direction: column; gap: 12px;">
        <p style="font-size: 13px; color: var(--id-text-secondary); margin: 0;">输入一段模拟群消息文本，测试当前所有已启用的多规则命中结果：</p>
        <NInput
          v-model:value="feishuRuleTestInput"
          type="textarea"
          :rows="3"
          placeholder="例如：请帮我 review 一下 PR #214 的前端首屏优化方案"
        />
        <div style="display: flex; justify-content: flex-end;">
          <NButton type="primary" size="small" @click="testFeishuRulesMatch">运行匹配测试</NButton>
        </div>

        <div v-if="feishuRuleTestResult" style="padding: 12px; border-radius: var(--id-radius-md);" :style="{ background: feishuRuleTestResult.matched ? 'var(--id-status-success-soft)' : 'var(--id-status-danger-soft)', border: feishuRuleTestResult.matched ? '1px solid var(--id-status-success-border)' : '1px solid var(--id-status-danger-border)' }">
          <div style="font-weight: 600; font-size: 13px;" :style="{ color: feishuRuleTestResult.matched ? 'var(--id-status-success)' : 'var(--id-status-danger)' }">
            {{ feishuRuleTestResult.matched ? `成功触发！命中规则：「${feishuRuleTestResult.ruleName}」` : '未命中任何规则，员工将静默忽略该群消息' }}
          </div>
          <p style="margin: 4px 0 0 0; font-size: 12px; color: var(--id-text-secondary);">{{ feishuRuleTestResult.reason }}</p>
        </div>
      </div>
    </NModal>

    <!-- 群消息共享规则新建/编辑弹窗 -->

  </div>
</template>

<style scoped>
@import './workforce-shared.css';

.channel-config-box {
  display: flex;
  flex-direction: column;
  gap: 16px;
  padding-top: 10px;
}

.ch-tab-label {
  display: inline-flex;
  align-items: center;
  gap: 6px;
}

.ch-tab-dot {
  width: 7px;
  height: 7px;
  border-radius: 50%;
  flex-shrink: 0;
  background: var(--id-text-placeholder);
}

.ch-tab-dot.is-connected {
  background: var(--id-status-success-dot);
}

.ch-tab-dot.is-pending {
  background: var(--id-status-warning-dot);
}

.ch-form-sections {
  display: flex;
  flex-direction: column;
  gap: 16px;
}

.unconnected-channel-card {
  background: var(--id-bg-surface);
  border: 1px solid var(--id-border-subtle);
  border-radius: var(--id-radius-lg);
  padding: 20px;
  display: flex;
  flex-direction: column;
  gap: 16px;
}

.ucc-header-row {
  display: grid;
  grid-template-columns: minmax(0, 1fr) auto;
  align-items: start;
  gap: 12px;
  padding-bottom: 12px;
  border-bottom: 1px solid var(--id-border-hairline);
}

.ucc-logo-heading {
  display: inline-flex;
  align-items: center;
  gap: 8px;
}

.ucc-title-col h3 {
  font-size: 15px;
  font-weight: 600;
  color: var(--id-text-primary);
  margin: 0 0 4px 0;
}

.ucc-title-col p {
  font-size: 12px;
  color: var(--id-text-secondary);
  margin: 0;
}

.onboarding-choice-actions, .qr-actions, .form-test-actions {
  display: flex;
  gap: 10px;
  flex-wrap: wrap;
}

.onboarding-qr-pane {
  display: grid;
  gap: 12px;
}

.qr-actions:empty {
  display: none;
}

.qr-pane-body, .onboarding-form-pane {
  display: grid;
  grid-template-columns: auto minmax(0, 1fr);
  gap: 24px;
  align-items: center;
  width: fit-content;
  max-width: 100%;
}

.qr-code-art {
  display: grid;
  place-items: center;
  min-width: 184px;
  min-height: 184px;
  padding: 8px;
  background: #fff;
  border: 1px solid var(--id-border-subtle);
  border-radius: var(--id-radius-md);
}

.qr-code-art:has(> .feishu-qr-expanded) {
  grid-template-columns: minmax(0, 1fr);
}

.qr-svg-img {
  display: block;
  width: 168px;
  height: 168px;
}

.qr-details {
  display: grid;
  gap: 8px;
  justify-items: start;
}

.qr-details p, .qr-note {
  margin: 0;
}

.qr-countdown, .qr-name {
  color: var(--id-text-secondary);
  font-size: 12px;
  font-variant-numeric: tabular-nums;
}

.onboarding-link {
  width: fit-content;
  color: var(--id-text-link, var(--id-accent-primary));
  font-size: 13px;
  font-weight: 500;
  text-decoration: underline;
  text-underline-offset: 3px;
}

.onboarding-form-pane {
  grid-template-columns: minmax(220px, 340px) minmax(0, 1fr);
  align-items: start;
}

.onboarding-form-pane > .onboarding-link {
  margin-top: 30px;
}

.form-test-actions {
  margin-top: 4px;
}

.tr-receipt-msg {
  margin: 8px 0 0;
}

.business-capabilities-table,
.rules-table-scroll {
  overflow-x: auto;
  -webkit-overflow-scrolling: touch;
}

.cap-matrix-table,
.feishu-rules-table {
  width: 100%;
  min-width: 580px;
  border-collapse: collapse;
  font-size: 12px;
  font-variant-numeric: tabular-nums;
}

.cap-matrix-table th,
.cap-matrix-table td,
.feishu-rules-table th,
.feishu-rules-table td {
  padding: 8px 10px;
  text-align: left;
  border-bottom: 1px solid var(--id-border-hairline);
}

.cap-matrix-table th,
.feishu-rules-table th {
  background: var(--id-bg-subtle);
  font-weight: 600;
  color: var(--id-text-secondary);
}

.cap-detail-cell {
  color: var(--id-text-secondary);
  line-height: 1.5;
}

.feishu-rules-list-box {
  background: var(--id-bg-subtle);
  border: 1px solid var(--id-border-subtle);
  border-radius: var(--id-radius-md);
  padding: 12px;
  margin: 8px 0;
}

.frl-header {
  font-size: 13px;
  font-weight: 600;
  color: var(--id-text-primary);
  margin-bottom: 8px;
}

.rule-desc-inline {
  color: var(--id-text-muted);
  font-size: 12px;
  margin-left: 6px;
}

.channel-lifecycle-bar {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 10px;
  flex-wrap: wrap;
  background: var(--id-bg-subtle);
  border: 1px solid var(--id-border-subtle);
  border-radius: var(--id-radius-md);
  padding: 10px 14px;
}

.clb-left {
  display: flex;
  align-items: center;
  gap: 10px;
  flex-wrap: wrap;
  font-size: 12px;
}

.clb-title { font-weight: 600; color: var(--id-text-primary); }
.clb-appid { color: var(--id-text-secondary); font-family: var(--id-font-mono); word-break: break-all; }
.clb-time { color: var(--id-text-muted); font-size: 12px; font-variant-numeric: tabular-nums; }
.clb-actions { display: flex; gap: 8px; flex-wrap: wrap; }

.policy-rows {
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.slider-row {
  display: flex;
  align-items: center;
  gap: 12px;
  flex-wrap: wrap;
  font-size: 12px;
}

.context-window-slider {
  width: min(240px, 100%);
}

.slider-lbl { color: var(--id-text-secondary); }
.slider-val { font-weight: 600; font-family: var(--id-font-mono); font-variant-numeric: tabular-nums; color: var(--id-text-primary); }

.sharing-rules-grid {
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.sharing-rule-card {
  background: var(--id-bg-subtle);
  border: 1px solid var(--id-border-subtle);
  border-radius: var(--id-radius-md);
  padding: 12px;
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.src-top {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
}

.src-name-row {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
}

.src-name-row strong {
  font-size: 13px;
  color: var(--id-text-primary);
}

.src-actions {
  display: flex;
  align-items: center;
  gap: 8px;
}

.src-details {
  display: flex;
  flex-direction: column;
  gap: 4px;
  font-size: 12px;
}

.src-row {
  display: flex;
  gap: 6px;
  flex-wrap: wrap;
}

.sr-lbl { color: var(--id-text-secondary); min-width: 110px; }
.sr-val { color: var(--id-text-primary); font-weight: 500; word-break: break-word; }

.src-error-note {
  font-size: 12px;
  color: var(--id-status-danger);
  background: var(--id-status-danger-soft);
  padding: 4px 8px;
  border-radius: var(--id-radius-sm);
}

.ch-notice {
  font-size: 12px;
  padding: 10px 14px;
  border-radius: var(--id-radius-md);
  line-height: 1.5;
  background: var(--id-bg-subtle);
  border: 1px solid var(--id-border-subtle);
  color: var(--id-text-secondary);
}

.feishu-notice { background: var(--id-status-info-soft); border: 1px solid var(--id-status-info-border); color: var(--id-status-info); }
.dingtalk-notice { background: var(--id-status-warning-soft); border: 1px solid var(--id-status-warning-border); color: var(--id-status-warning); }

:deep(.n-tabs.n-tabs--segment-type .n-tabs-tab .n-tabs-tab__label) {
  color: var(--id-text-secondary) !important;
  font-weight: 500 !important;
}

:deep(.n-tabs.n-tabs--segment-type .n-tabs-tab.n-tabs-tab--active .n-tabs-tab__label) {
  color: var(--id-text-primary) !important;
  font-weight: 600 !important;
}

@media (max-width: 860px) {
  .unconnected-channel-card {
    padding: 14px;
  }
}

@media (max-width: 760px) {
  .qr-pane-body,
  .onboarding-form-pane {
    grid-template-columns: minmax(0, 1fr);
  }

  .onboarding-form-pane > .onboarding-link {
    margin-top: 0;
  }

  :deep(.channel-platform-tabs .n-tabs-rail) {
    flex-wrap: wrap;
    height: auto;
    padding: 4px;
    gap: 4px;
  }

  :deep(.channel-platform-tabs .n-tabs-capsule) {
    display: none;
  }

  :deep(.channel-platform-tabs .n-tabs-tab-wrapper) {
    flex: 1 1 130px;
  }

  :deep(.channel-platform-tabs .n-tabs-tab) {
    padding: 6px 8px;
    border-radius: 6px;
  }

  :deep(.channel-platform-tabs .n-tabs-tab--active) {
    background: var(--id-bg-surface);
    box-shadow: var(--id-shadow-sm);
  }
}
</style>

<script setup lang="ts">
import { copyText } from '../../utils/clipboard';
import { computed, onUnmounted, ref, watch } from 'vue';
import {
  NAlert, NButton, NCheckbox, NCheckboxGroup, NDrawer, NDrawerContent,
  NForm, NFormItem, NInput, NModal, NRadio, NRadioGroup, NSelect, NSpace, NTag, useMessage
} from 'naive-ui';
import { getPendingActionIdempotencyKey } from '../../api';
import { useWorkforcePorts } from '../../api/workforce-ports';
import { computeBusinessKeyPreview, normalizeTriggerAllowedActions } from '../../utils/workforce';
import type {
  ChannelGroupOption, DigitalEmployee, EmployeeTrigger, PlatformType, TriggerAllowedAction
} from '../../types';

const { tasksApi, workforceApi } = useWorkforcePorts();

const props = defineProps<{
  employee: DigitalEmployee;
  employees: DigitalEmployee[];
  platformCandidateGroupItems: Record<PlatformType, ChannelGroupOption[]>;
  syncModuleField: <K extends keyof DigitalEmployee>(field: K, value: DigitalEmployee[K]) => Promise<boolean>;
  syncServerFieldIntoSnapshot?: <K extends keyof DigitalEmployee>(
    empId: string,
    savedRaw: DigitalEmployee,
    fields: K[]
  ) => void;
}>();

const message = useMessage();

const showTriggerDrawer = ref(false);
const showTriggerHistoryModal = ref(false);
const editingTrigger = ref<EmployeeTrigger | null>(null);
const viewingTriggerHistory = ref<EmployeeTrigger | null>(null);
const isRunningTrigger = ref(false);
const isPollingTrigger = ref(false);
const generatedPlaintextSecret = ref<string | null>(null);
const selectedNotifyChannelKey = ref<string>('');
const runningTriggerState = ref<{
  triggerId: string;
  executionId: string;
  operationId: string;
  status: string;
  message: string;
  pollError?: boolean | undefined;
  pollTimedOut?: boolean | undefined;
  retryIdempotencyKey?: string | undefined;
} | null>(null);
let triggerRunPollTimer: ReturnType<typeof setTimeout> | null = null;

const cronPreviewTimes = ref<string[]>([]);
const cronPreviewLoading = ref(false);
const cronPreviewError = ref('');
let cronPreviewSeq = 0;
let cronPreviewTimer: ReturnType<typeof setTimeout> | null = null;

async function refreshCronPreview() {
  const empId = props.employee.id;
  const trg = editingTrigger.value;
  if (!showTriggerDrawer.value || !empId || !trg || trg.type !== 'schedule') {
    cronPreviewTimes.value = [];
    cronPreviewError.value = '';
    cronPreviewLoading.value = false;
    return;
  }
  const cron = (trg.cron || '').trim();
  const timezone = trg.timezone || 'Asia/Shanghai';
  if (!cron) {
    cronPreviewTimes.value = [];
    cronPreviewError.value = '请输入 5 段式 Cron 表达式（分 时 日 月 周）以计算执行时间';
    cronPreviewLoading.value = false;
    return;
  }
  const seq = ++cronPreviewSeq;
  cronPreviewLoading.value = true;
  cronPreviewError.value = '';
  try {
    const res = await workforceApi.previewTriggerCron(empId, cron, timezone);
    if (seq !== cronPreviewSeq) return;
    cronPreviewTimes.value = res.previews;
  } catch (err) {
    if (seq !== cronPreviewSeq) return;
    cronPreviewTimes.value = [];
    cronPreviewError.value = err instanceof Error ? err.message : 'Cron 表达式或时区解析失败';
  } finally {
    if (seq === cronPreviewSeq) cronPreviewLoading.value = false;
  }
}

watch(
  () => [
    showTriggerDrawer.value,
    editingTrigger.value?.type,
    editingTrigger.value?.cron,
    editingTrigger.value?.timezone
  ] as const,
  ([open, type], prev) => {
    if (cronPreviewTimer) {
      clearTimeout(cronPreviewTimer);
      cronPreviewTimer = null;
    }
    if (!open || type !== 'schedule') {
      cronPreviewTimes.value = [];
      cronPreviewError.value = '';
      cronPreviewLoading.value = false;
      return;
    }
    const openedNow = !prev || !prev[0] || prev[1] !== 'schedule';
    if (openedNow) {
      void refreshCronPreview();
    } else {
      cronPreviewTimer = setTimeout(() => {
        void refreshCronPreview();
      }, 180);
    }
  }
);

onUnmounted(() => {
  if (cronPreviewTimer) {
    clearTimeout(cronPreviewTimer);
    cronPreviewTimer = null;
  }
  if (triggerRunPollTimer) {
    clearTimeout(triggerRunPollTimer);
    triggerRunPollTimer = null;
  }
});

const platformLabelMap: Record<PlatformType, string> = {
  feishu: '飞书',
  wecom: '企业微信',
  dingtalk: '钉钉'
};

const connectedChannelOptions = computed(() =>
  props.employee.channels
    .filter(c => c.status === 'connected')
    .map(c => {
      const connId = c.connectionId || `conn-${c.platform}`;
      return {
        label: `${platformLabelMap[c.platform] || c.platform} · ${connId}`,
        value: `${c.platform}::${connId}`,
        channel: c
      };
    })
);

function formatTargetGroupKey(platform: PlatformType, connectionId: string | undefined, groupIdOrName: string): string {
  return `${platform}::${connectionId || `conn-${platform}`}::${groupIdOrName}`;
}

const triggerGroupSelectOptions = computed(() => {
  const options: Array<{
    label: string;
    value: string;
    groupItem: { platform: PlatformType; connectionId?: string | undefined; groupId: string; name: string };
  }> = [];
  const seen = new Set<string>();

  const selectedCh = connectedChannelOptions.value.find(o => o.value === selectedNotifyChannelKey.value)?.channel;
  if (selectedCh) {
    const connId = selectedCh.connectionId || `conn-${selectedCh.platform}`;
    for (const bg of selectedCh.boundGroups || []) {
      const val = formatTargetGroupKey(selectedCh.platform, connId, bg.id);
      if (seen.has(val)) continue;
      seen.add(val);
      options.push({
        label: `${bg.name} (${bg.memberCount}人 · 已绑定)`,
        value: val,
        groupItem: { platform: selectedCh.platform, connectionId: connId, groupId: bg.id, name: bg.name }
      });
    }
    const items = (props.platformCandidateGroupItems[selectedCh.platform] || []).filter(
      g => !g.connectionId || !selectedCh.connectionId || g.connectionId === selectedCh.connectionId
    );
    for (const g of items) {
      const val = formatTargetGroupKey(selectedCh.platform, connId, g.groupId);
      if (seen.has(val)) continue;
      seen.add(val);
      options.push({
        label: `${g.name} (${g.memberCount}人)`,
        value: val,
        groupItem: { platform: selectedCh.platform, connectionId: connId, groupId: g.groupId, name: g.name }
      });
    }
  }

  for (const nt of editingTrigger.value?.notificationTargets || []) {
    const connId = nt.connectionId || props.employee.channels.find(c => c.platform === nt.platform)?.connectionId || `conn-${nt.platform}`;
    const gid = nt.groupId || nt.groupName;
    const val = formatTargetGroupKey(nt.platform, connId, gid);
    if (seen.has(val)) continue;
    seen.add(val);
    options.push({
      label: `${nt.groupName} (${platformLabelMap[nt.platform] || nt.platform} · ${connId})`,
      value: val,
      groupItem: { platform: nt.platform, connectionId: connId, groupId: gid, name: nt.groupName }
    });
  }

  return options;
});

function handleTriggerNotificationTargetsUpdate(selectedKeys: string[]) {
  if (!editingTrigger.value) return;
  const optMap = new Map(triggerGroupSelectOptions.value.map(o => [o.value, o.groupItem]));
  editingTrigger.value.notificationTargets = selectedKeys.map(val => {
    const found = optMap.get(val);
    if (found) {
      return {
        platform: found.platform,
        connectionId: found.connectionId,
        groupId: found.groupId,
        groupName: found.name
      };
    }
    const [plat, connId, ...rest] = val.split('::');
    const gid = rest.join('::') || val;
    return {
      platform: (plat as PlatformType) || 'feishu',
      connectionId: connId,
      groupId: gid,
      groupName: gid
    };
  });
}

function openAddTrigger() {
  generatedPlaintextSecret.value = null;
  selectedNotifyChannelKey.value = '';
  editingTrigger.value = {
    id: `trg-${Date.now()}`,
    name: '',
    type: 'schedule',
    enabled: true,
    allowedActions: ['create_task'],
    cron: '0 9 * * 1-5',
    timezone: 'Asia/Shanghai',
    businessKeyMode: 'period',
    businessKeyPattern: 'audit-${YYYYMMDD}',
    githubRepo: '',
    githubIssueSyncMode: 'on_opened',
    description: '',
    historyPolicy: 'isolated',
    notificationTargets: [],
    executionLogs: []
  };
  showTriggerDrawer.value = true;
}

function openEditTrigger(trg: EmployeeTrigger) {
  generatedPlaintextSecret.value = null;
  const cloned = JSON.parse(JSON.stringify(trg)) as EmployeeTrigger;
  cloned.allowedActions = normalizeTriggerAllowedActions(cloned.type, cloned.allowedActions);
  if (!cloned.businessKeyMode) cloned.businessKeyMode = 'empty';
  if (!cloned.businessKeyPattern) cloned.businessKeyPattern = 'audit-${YYYYMMDD}';
  const firstTarget = cloned.notificationTargets[0];
  if (firstTarget) {
    const matchedCh = props.employee.channels.find(
      c => c.status === 'connected' && c.platform === firstTarget.platform && (!firstTarget.connectionId || c.connectionId === firstTarget.connectionId)
    );
    selectedNotifyChannelKey.value = matchedCh
      ? `${matchedCh.platform}::${matchedCh.connectionId || `conn-${matchedCh.platform}`}`
      : '';
  } else {
    selectedNotifyChannelKey.value = '';
  }
  editingTrigger.value = cloned;
  showTriggerDrawer.value = true;
}

watch(
  () => editingTrigger.value?.type,
  nextType => {
    if (!editingTrigger.value || !nextType) return;
    editingTrigger.value.allowedActions = normalizeTriggerAllowedActions(
      nextType,
      editingTrigger.value.allowedActions
    );
  }
);

async function copyGeneratedSecret() {
  if (!generatedPlaintextSecret.value) return;
  try {
    await copyText(generatedPlaintextSecret.value);
    message.success('已复制一次性完整 Webhook Secret！');
  } catch {
    message.info('请手动选中并复制上方完整 Webhook Secret');
  }
}

async function regenerateWebhookSecret() {
  if (!editingTrigger.value) return;
  const isUnsavedDraftTrigger = !props.employee.triggers.some(t => t.id === editingTrigger.value!.id);
  try {
    const res = await workforceApi.regenerateTriggerSecret(
      props.employee.id,
      editingTrigger.value.id,
      {
        allowDraft: isUnsavedDraftTrigger,
        expectedVersion: isUnsavedDraftTrigger ? undefined : props.employee.version
      }
    );
    editingTrigger.value.keyId = res.trigger.keyId;
    editingTrigger.value.secretMasked = res.trigger.secretMasked;
    editingTrigger.value.webhookEndpoint = res.trigger.webhookEndpoint || editingTrigger.value.webhookEndpoint;
    editingTrigger.value.draftSecretToken = res.isDraft ? res.draftSecretToken : undefined;
    generatedPlaintextSecret.value = res.secret;

    if (!res.isDraft && res.version !== undefined) {
      const nextTriggers = JSON.parse(JSON.stringify(props.employee.triggers)) as EmployeeTrigger[];
      const tIdx = nextTriggers.findIndex(t => t.id === editingTrigger.value!.id);
      if (tIdx >= 0) {
        nextTriggers[tIdx] = JSON.parse(JSON.stringify(res.trigger));
      }
      if (props.syncServerFieldIntoSnapshot) {
        props.syncServerFieldIntoSnapshot(
          props.employee.id,
          {
            ...props.employee,
            version: res.version,
            etag: res.etag || props.employee.etag,
            triggers: nextTriggers
          },
          ['triggers']
        );
      } else {
        props.employee.version = res.version;
        if (res.etag) props.employee.etag = res.etag;
        if (tIdx >= 0) props.employee.triggers[tIdx] = JSON.parse(JSON.stringify(res.trigger));
      }
    }
    message.success(
      res.isDraft
        ? '已生成草稿 Webhook HMAC 密钥，请立即复制保存完整密钥（仅展示一次），并点击「保存触发器」完成绑定！'
        : '已重新生成 Webhook HMAC 密钥并同步最新配置版本，请立即复制保存完整密钥（仅展示一次）！'
    );
  } catch (err) {
    message.error(err instanceof Error ? err.message : '重新生成 Webhook 密钥失败');
  }
}

const isScheduleCronInvalid = computed(() => {
  if (!editingTrigger.value || editingTrigger.value.type !== 'schedule') return false;
  return !editingTrigger.value.cron?.trim() || cronPreviewLoading.value || Boolean(cronPreviewError.value);
});

const isSavingTriggers = ref(false);

async function saveTriggerForm() {
  if (isSavingTriggers.value) return;
  if (!editingTrigger.value?.name?.trim()) {
    message.warning('请输入自动化任务名称');
    return;
  }
  editingTrigger.value.allowedActions = normalizeTriggerAllowedActions(
    editingTrigger.value.type,
    editingTrigger.value.allowedActions
  );
  if (editingTrigger.value.type === 'schedule') {
    if (!editingTrigger.value.cron?.trim()) {
      message.error('定时触发器必须填写有效的 5 段式 Cron 表达式');
      return;
    }
    if (cronPreviewTimer) {
      clearTimeout(cronPreviewTimer);
      cronPreviewTimer = null;
      await refreshCronPreview();
    } else if (!cronPreviewTimes.value.length && !cronPreviewError.value) {
      await refreshCronPreview();
    }
    if (cronPreviewError.value || !cronPreviewTimes.value.length) {
      message.error(cronPreviewError.value || 'Cron 表达式格式错误，请修正后再保存');
      return;
    }
  }
  const clonedTrg = JSON.parse(JSON.stringify(editingTrigger.value)) as EmployeeTrigger;
  clonedTrg.name = clonedTrg.name.trim();
  const nextTriggers = JSON.parse(JSON.stringify(props.employee.triggers)) as EmployeeTrigger[];
  const idx = nextTriggers.findIndex(t => t.id === clonedTrg.id);
  if (idx >= 0) {
    nextTriggers[idx] = clonedTrg;
  } else {
    nextTriggers.push(clonedTrg);
  }
  isSavingTriggers.value = true;
  try {
    const ok = await props.syncModuleField('triggers', nextTriggers);
    if (ok) {
      for (const t of props.employee.triggers) {
        t.draftSecretToken = undefined;
      }
      generatedPlaintextSecret.value = null;
      showTriggerDrawer.value = false;
      message.success(`自动化触发器「${clonedTrg.name}」已保存并正式生效！`);
    }
  } finally {
    isSavingTriggers.value = false;
  }
}

function openTriggerHistory(trg: EmployeeTrigger) {
  viewingTriggerHistory.value = trg;
  showTriggerHistoryModal.value = true;
}

function syncTriggerLogsToState(trg: EmployeeTrigger, nextLogs: EmployeeTrigger['executionLogs']) {
  trg.executionLogs = JSON.parse(JSON.stringify(nextLogs));
  if (viewingTriggerHistory.value?.id === trg.id) {
    viewingTriggerHistory.value = trg;
  }
  const masterEmp = props.employees.find(e => e.id === props.employee.id);
  const masterTrg = masterEmp?.triggers.find(t => t.id === trg.id);
  if (masterTrg) {
    masterTrg.executionLogs = JSON.parse(JSON.stringify(nextLogs));
  }
}

function isTriggerOperationInFlight(triggerId: string): boolean {
  const st = runningTriggerState.value;
  if (!st || st.triggerId !== triggerId || !st.operationId) return false;
  return st.status === 'accepted' || st.status === 'processing' || st.status === 'poll_error' || Boolean(st.pollError) || Boolean(st.pollTimedOut);
}

async function pollTriggerRunOperation(trg: EmployeeTrigger, operationId: string, executionId: string) {
  if (triggerRunPollTimer) {
    clearTimeout(triggerRunPollTimer);
    triggerRunPollTimer = null;
  }
  isPollingTrigger.value = true;
  try {
    const maxAttempts = 25;
    for (let attempt = 0; attempt < maxAttempts; attempt++) {
      await new Promise(resolve => {
        triggerRunPollTimer = setTimeout(resolve, 450);
      });
      let op;
      try {
        op = await tasksApi.getOperation(operationId);
      } catch (pollErr) {
        const errText = pollErr instanceof Error ? pollErr.message : '状态查询网络异常';
        runningTriggerState.value = {
          triggerId: trg.id,
          executionId,
          operationId,
          status: 'poll_error',
          pollError: true,
          pollTimedOut: false,
          message: `任务已提交，状态暂时获取失败：${errText}。请点击「继续查询状态」，请勿重复提交。`
        };
        message.warning('触发器运行已提交，状态暂时获取失败，请点击「继续查询状态」');
        return;
      }

      const opData = op.data as { trigger?: EmployeeTrigger; executionLog?: EmployeeTrigger['executionLogs'][number]; task?: { taskId?: string }; executionId?: string } | undefined;
      if (opData?.trigger?.executionLogs) {
        syncTriggerLogsToState(trg, opData.trigger.executionLogs);
      }
      if (op.status === 'succeeded') {
        runningTriggerState.value = {
          triggerId: trg.id,
          executionId,
          operationId,
          status: 'succeeded',
          pollError: false,
          pollTimedOut: false,
          message: op.message || '触发器执行完成'
        };
        message.success(`触发器执行完成${opData?.task?.taskId ? `（任务 ${opData.task.taskId}）` : ''}！`);
        return;
      }
      if (op.status === 'failed') {
        runningTriggerState.value = {
          triggerId: trg.id,
          executionId,
          operationId,
          status: 'failed',
          pollError: false,
          pollTimedOut: false,
          message: op.errorMessage || op.message || '触发器运行失败'
        };
        message.error(op.errorMessage || op.message || '触发器执行失败');
        return;
      }
      runningTriggerState.value = {
        triggerId: trg.id,
        executionId,
        operationId,
        status: op.status,
        pollError: false,
        pollTimedOut: false,
        message: op.message || '正在执行中...'
      };
    }

    runningTriggerState.value = {
      triggerId: trg.id,
      executionId,
      operationId,
      status: runningTriggerState.value?.status === 'accepted' ? 'accepted' : 'processing',
      pollError: false,
      pollTimedOut: true,
      message: '任务仍在执行中，可点击「继续查询状态」查看结果。'
    };
    message.info('任务仍在执行中，可点击「继续查询状态」继续跟踪');
  } finally {
    isPollingTrigger.value = false;
  }
}

async function resumePollTriggerOperation(trg: EmployeeTrigger) {
  if (isPollingTrigger.value) return;
  const st = runningTriggerState.value;
  if (!st || st.triggerId !== trg.id || !st.operationId) return;
  st.pollError = false;
  st.pollTimedOut = false;
  st.message = '正在继续查询状态...';
  await pollTriggerRunOperation(trg, st.operationId, st.executionId);
}

async function simulateRunTrigger(trg: EmployeeTrigger, retryWithKey?: string) {
  if (isRunningTrigger.value || isPollingTrigger.value) return;
  if (!retryWithKey && isTriggerOperationInFlight(trg.id)) {
    await resumePollTriggerOperation(trg);
    return;
  }
  isRunningTrigger.value = true;
  let acceptedReceipt: Awaited<ReturnType<typeof workforceApi.runTriggerNow>> | null = null;
  try {
    acceptedReceipt = await workforceApi.runTriggerNow(props.employee.id, trg.id, {
      expectedVersion: props.employee.version,
      idempotencyKey: retryWithKey
    });
  } catch (err) {
    const preservedKey = getPendingActionIdempotencyKey('trigger_run', `${props.employee.id}::${trg.id}`, props.employee.version);
    if (preservedKey) {
      runningTriggerState.value = {
        triggerId: trg.id,
        executionId: '',
        operationId: '',
        status: 'retryable_error',
        message: `${err instanceof Error ? err.message : '请求超时或网络异常'}（请点击重试，不会重复创建任务）`,
        retryIdempotencyKey: preservedKey
      };
    }
    message.error(err instanceof Error ? err.message : '触发器运行提交失败');
    isRunningTrigger.value = false;
    return;
  } finally {
    isRunningTrigger.value = false;
  }

  syncTriggerLogsToState(trg, acceptedReceipt.data.trigger.executionLogs);
  const executionId = acceptedReceipt.data.executionId || acceptedReceipt.targetId;
  runningTriggerState.value = {
    triggerId: trg.id,
    executionId,
    operationId: acceptedReceipt.operationId,
    status: acceptedReceipt.status,
    pollError: false,
    pollTimedOut: false,
    message: acceptedReceipt.message
  };
  if (acceptedReceipt.status === 'accepted' || acceptedReceipt.status === 'processing') {
    message.info('已提交运行请求，正在查询执行结果...');
    await pollTriggerRunOperation(trg, acceptedReceipt.operationId, executionId);
  } else if (acceptedReceipt.status === 'succeeded') {
    message.success(`触发器运行完成（任务 ${acceptedReceipt.data.task.taskId}）！`);
  } else {
    message.error(acceptedReceipt.errorMessage || acceptedReceipt.message || '触发器执行失败');
  }
}
</script>

<template>
  <div class="tab-panel-inner">
    <div class="panel-desc-bar">
      <span><strong>自动触发器</strong> 允许通过定时 Cron 巡检、外部 Webhook 签名事件或 GitHub Issue 触发员工执行任务。</span>
      <NButton type="primary" size="small" @click="openAddTrigger">+ 新增自动化任务</NButton>
    </div>

    <div class="triggers-list">
      <div
        v-for="trg in employee.triggers"
        :key="trg.id"
        class="trg-card"
      >
        <div class="trg-top">
          <div class="trg-title-row">
            <h4 class="trg-name">{{ trg.name }}</h4>
            <NTag size="tiny" :type="trg.type === 'schedule' ? 'info' : trg.type === 'github_issue' ? 'success' : 'warning'" round>
              {{ trg.type === 'schedule' ? '定时 Cron 调度' : trg.type === 'github_issue' ? 'GitHub Issue 联动' : 'Webhook HMAC 调用' }}
            </NTag>
            <NTag v-for="act in (trg.allowedActions || ['create_task'])" :key="act" size="tiny" type="info">
              {{ act === 'create_task' ? '创建任务' : act === 'supplement_task' ? '补充输入' : '取消任务' }}
            </NTag>
            <NTag size="tiny" :type="trg.enabled ? 'success' : 'default'">
              {{ trg.enabled ? '已启用' : '已停用' }}
            </NTag>
          </div>
          <div class="trg-actions">
            <NButton
              v-if="runningTriggerState?.triggerId === trg.id && (runningTriggerState.pollError || runningTriggerState.pollTimedOut) && runningTriggerState.operationId"
              size="tiny"
              type="warning"
              secondary
              :loading="isPollingTrigger"
              @click="resumePollTriggerOperation(trg)"
            >
              继续查询状态
            </NButton>
            <NButton
              v-else-if="runningTriggerState?.triggerId === trg.id && runningTriggerState.retryIdempotencyKey"
              size="tiny"
              type="warning"
              secondary
              :loading="isRunningTrigger"
              @click="simulateRunTrigger(trg, runningTriggerState.retryIdempotencyKey)"
            >
              重试运行
            </NButton>
            <NButton
              size="tiny"
              secondary
              :loading="isRunningTrigger && runningTriggerState?.triggerId === trg.id"
              :disabled="isTriggerOperationInFlight(trg.id)"
              @click="simulateRunTrigger(trg)"
            >
              {{ isTriggerOperationInFlight(trg.id) ? '运行中...' : '立即试运行' }}
            </NButton>
            <NButton size="tiny" secondary @click="openTriggerHistory(trg)">
              触发历史 ({{ trg.executionLogs.length }})
            </NButton>
            <NButton size="tiny" @click="openEditTrigger(trg)">
              编辑配置
            </NButton>
          </div>
        </div>

        <NAlert
          v-if="runningTriggerState && runningTriggerState.triggerId === trg.id"
          :type="runningTriggerState.status === 'failed' || runningTriggerState.status === 'retryable_error' ? 'error' : runningTriggerState.pollError || runningTriggerState.pollTimedOut ? 'warning' : runningTriggerState.status === 'succeeded' ? 'success' : 'info'"
          :bordered="false"
          style="font-size: 12px;"
        >
          <div style="display: flex; justify-content: space-between; align-items: center; gap: 8px; flex-wrap: wrap;">
            <span>
              {{ runningTriggerState.message }}
            </span>
            <NButton
              v-if="(runningTriggerState.pollError || runningTriggerState.pollTimedOut) && runningTriggerState.operationId"
              size="tiny"
              type="warning"
              secondary
              :loading="isPollingTrigger"
              @click="resumePollTriggerOperation(trg)"
            >
              继续查询状态
            </NButton>
          </div>
        </NAlert>

        <div class="trg-details">
          <div v-if="trg.type === 'schedule'" class="trg-field">
            <span>Cron 计划: <code>{{ trg.cron }}</code> (时区: {{ trg.timezone }})</span>
          </div>
          <div v-else-if="trg.type === 'github_issue'" class="trg-field">
            <span>关联仓库: <code>{{ trg.githubRepo }}</code></span>
            <span>触发时机: <code>{{ trg.githubIssueSyncMode === 'on_opened' ? '仅 Issue 打开时触发' : 'Issue 打开与更新均触发' }}</code></span>
          </div>
          <div v-else class="trg-field">
            <span>Endpoint: <code>{{ trg.webhookEndpoint }}</code></span>
            <span>Key ID: <code>{{ trg.keyId }}</code></span>
          </div>

          <p class="trg-desc">执行内容：{{ trg.description }}</p>

          <div class="trg-notify-targets">
            <span class="tnt-lbl">结果回告真实工作群：</span>
            <span v-if="trg.notificationTargets.length">
              {{ trg.notificationTargets.map(n => n.groupName).join('、') }}
            </span>
            <span v-else class="text-muted">未配置回告群</span>
          </div>
        </div>
      </div>

      <div v-if="!employee.triggers.length" class="empty-channel-box">
        <p>员工暂未配置自动化触发器。可配置早晚定时健康检查、CI/CD 构建失败 Webhook 或 GitHub Issue 联动。</p>
        <NButton type="primary" size="small" @click="openAddTrigger">+ 创建第一个自动化触发器</NButton>
      </div>
    </div>

    <!-- 自动化触发器配置抽屉 -->
    <NDrawer
      v-model:show="showTriggerDrawer"
      :width="'min(600px, 95vw)'"
      placement="right"
      @update:show="v => { if (!v) generatedPlaintextSecret = null; }"
    >
      <NDrawerContent v-if="editingTrigger" :title="`配置自动化触发器 · ${editingTrigger.name || '新建'}`" closable>
        <div class="drawer-inner-flow">
          <NForm label-placement="top">
            <NFormItem label="任务标题">
              <NInput v-model:value="editingTrigger.name" placeholder="例如：每日早 9 点 master 巡检" />
            </NFormItem>
            <NFormItem label="触发机制">
              <NRadioGroup v-model:value="editingTrigger.type">
                <NSpace wrap>
                  <NRadio value="schedule">定时 Cron 调度</NRadio>
                  <NRadio value="github_issue">GitHub Issue 联动</NRadio>
                  <NRadio value="webhook">Webhook HMAC 签名调用</NRadio>
                </NSpace>
              </NRadioGroup>
            </NFormItem>

            <template v-if="editingTrigger.type === 'schedule'">
              <NFormItem label="Cron 表达式">
                <NInput :value="editingTrigger.cron || ''" placeholder="0 9 * * 1-5" @update:value="v => editingTrigger!.cron = v" />
              </NFormItem>
              <NFormItem label="时区">
                <NSelect
                  :value="editingTrigger.timezone || 'Asia/Shanghai'"
                  :options="[
                    { label: 'Asia/Shanghai (中国标准时间 UTC+8)', value: 'Asia/Shanghai' },
                    { label: 'UTC (世界协调时间)', value: 'UTC' },
                    { label: 'America/New_York (美东时间)', value: 'America/New_York' }
                  ]"
                  @update:value="v => editingTrigger!.timezone = v"
                />
              </NFormItem>

              <div class="cron-preview-box" style="margin-bottom: 14px; background: var(--id-bg-subtle); border: 1px solid var(--id-border-subtle); border-radius: var(--id-radius-md); padding: 10px 12px;">
                <label style="font-size: 12px; font-weight: 600; color: var(--id-text-primary); display: block; margin-bottom: 6px;">
                  未来 5 次计划执行时刻预测 (基于 {{ editingTrigger.timezone || 'Asia/Shanghai' }} 计算)：
                </label>
                <div v-if="cronPreviewLoading" style="font-size: 12px; color: var(--id-text-secondary);">
                  正在计算 {{ editingTrigger.timezone || 'Asia/Shanghai' }} 时区下的触发时刻...
                </div>
                <NAlert v-else-if="cronPreviewError" type="error" :bordered="false" style="padding: 6px 10px; font-size: 12px;">
                  {{ cronPreviewError }}
                </NAlert>
                <div v-else style="display: flex; flex-direction: column; gap: 4px;">
                  <div
                    v-for="(timeStr, idx) in cronPreviewTimes"
                    :key="idx"
                    class="cron-preview-row"
                    style="font-family: var(--id-font-mono); font-variant-numeric: tabular-nums; font-size: 12px; color: var(--id-text-primary); display: flex; align-items: center; gap: 6px;"
                  >
                    <span style="color: var(--id-text-muted);">#{{ idx + 1 }}</span>
                    <span>{{ timeStr }}</span>
                  </div>
                </div>
              </div>

              <div style="margin-bottom: 14px; background: var(--id-bg-subtle); border: 1px solid var(--id-border-subtle); border-radius: var(--id-radius-md); padding: 10px 12px;">
                <label style="font-size: 12px; font-weight: 600; color: var(--id-text-primary); display: block; margin-bottom: 4px;">
                  定时任务业务键 (Business Key - 用于任务归集、状态防重与回告聚合)：
                </label>
                <NRadioGroup
                  :value="editingTrigger.businessKeyMode || 'period'"
                  @update:value="v => editingTrigger!.businessKeyMode = v as 'empty' | 'fixed' | 'period'"
                >
                  <NSpace wrap>
                    <NRadio value="empty">不使用业务键 (每次执行独立)</NRadio>
                    <NRadio value="fixed">固定业务键</NRadio>
                    <NRadio value="period">周期业务键模板</NRadio>
                  </NSpace>
                </NRadioGroup>

                <div v-if="editingTrigger.businessKeyMode === 'fixed'" style="margin-top: 8px;">
                  <NInput
                    :value="editingTrigger.businessKeyField || ''"
                    placeholder="输入固定业务键，例如：nightly-master-audit"
                    @update:value="v => editingTrigger!.businessKeyField = v"
                  />
                </div>
                <div v-else-if="editingTrigger.businessKeyMode === 'period'" style="margin-top: 8px;">
                  <NInput
                    :value="editingTrigger.businessKeyPattern || 'audit-${YYYYMMDD}'"
                    placeholder="周期表达式，如 audit-${YYYYMMDD} 或 check-${YYYY-MM-DD}"
                    @update:value="v => editingTrigger!.businessKeyPattern = v"
                  />
                  <div style="margin-top: 6px; font-size: 12px; color: var(--id-text-secondary);">
                    <span>当前实时计算预览：</span>
                    <NTag size="tiny" type="info">{{ computeBusinessKeyPreview(editingTrigger.businessKeyMode, editingTrigger.businessKeyField, editingTrigger.businessKeyPattern) }}</NTag>
                  </div>
                </div>
              </div>
            </template>

            <template v-else-if="editingTrigger.type === 'github_issue'">
              <NFormItem label="GitHub 仓库名">
                <NInput :value="editingTrigger.githubRepo || ''" placeholder="例如：org/frontend-core" @update:value="v => editingTrigger!.githubRepo = v" />
              </NFormItem>
              <NFormItem label="触发时机模式">
                <NRadioGroup :value="editingTrigger.githubIssueSyncMode || 'on_opened'" @update:value="v => editingTrigger!.githubIssueSyncMode = v as 'on_opened' | 'on_updated'">
                  <NSpace wrap>
                    <NRadio value="on_opened">仅在 Issue 打开时触发</NRadio>
                    <NRadio value="on_updated">在 Issue 打开或更新时触发</NRadio>
                  </NSpace>
                </NRadioGroup>
              </NFormItem>
            </template>

            <template v-else>
              <NFormItem label="Webhook 调用端点 (系统生成，只读)">
                <NInput :value="editingTrigger.webhookEndpoint || '保存后由系统自动分配调用端点'" readonly />
              </NFormItem>
              <NFormItem label="HMAC 签名密钥 Key ID (系统托管，只读)">
                <NInput :value="editingTrigger.keyId || '生成密钥后自动分配'" readonly />
              </NFormItem>
              <NFormItem label="Secret 签名密钥 (仅展示脱敏摘要，重生成后一次性显示明文)">
                <div style="display: flex; flex-direction: column; gap: 8px; width: 100%;">
                  <div style="display: flex; gap: 8px; flex-wrap: wrap; width: 100%;">
                    <NInput :value="editingTrigger.secretMasked || '保存或点击生成后生成密钥'" disabled style="flex: 1; min-width: 180px;" />
                    <NButton size="small" secondary @click="regenerateWebhookSecret">
                      {{ editingTrigger.secretMasked ? '重新生成密钥' : '生成密钥' }}
                    </NButton>
                  </div>
                  <NAlert v-if="generatedPlaintextSecret" type="warning" :bordered="true" style="font-size: 12px;">
                    <div style="display: flex; flex-direction: column; gap: 6px;">
                      <strong>一次性完整 HMAC 签名密钥（仅此次展示，关闭或离开后将无法再次查看）：</strong>
                      <div style="display: flex; gap: 8px; align-items: center; flex-wrap: wrap;">
                        <code style="word-break: break-all; font-size: 12px; background: rgba(0,0,0,0.06); padding: 3px 6px; border-radius: 4px;">{{ generatedPlaintextSecret }}</code>
                        <NButton size="tiny" type="primary" secondary @click="copyGeneratedSecret">复制明文密钥</NButton>
                      </div>
                    </div>
                  </NAlert>
                </div>
              </NFormItem>
            </template>

            <NFormItem label="允许的任务动作 (安全策略)">
              <div v-if="editingTrigger.type === 'schedule'">
                <NTag size="small" type="info">创建执行任务</NTag>
                <p style="margin: 4px 0 0 0; font-size: 12px; color: var(--id-text-secondary);">定时调度按计划独立创建并执行任务。</p>
              </div>
              <div v-else-if="editingTrigger.type === 'github_issue'">
                <NTag size="small" type="info">创建执行任务</NTag>
                <p style="margin: 4px 0 0 0; font-size: 12px; color: var(--id-text-secondary);">GitHub Issue 事件到达时自动创建分析任务。</p>
              </div>
              <div v-else style="width: 100%;">
                <NCheckboxGroup :value="editingTrigger.allowedActions || ['create_task']" @update:value="v => editingTrigger!.allowedActions = v as TriggerAllowedAction[]">
                  <NSpace wrap>
                    <NCheckbox value="create_task">创建执行任务</NCheckbox>
                    <NCheckbox value="supplement_task">补充任务输入</NCheckbox>
                    <NCheckbox value="cancel_task">取消正在运行任务</NCheckbox>
                  </NSpace>
                </NCheckboxGroup>

                <div style="margin-top: 10px; background: var(--id-bg-subtle); padding: 8px 12px; border-radius: var(--id-radius-md); border: 1px solid var(--id-border-subtle);">
                  <label style="font-size: 12px; color: var(--id-text-secondary); display: block; margin-bottom: 4px;">业务键映射选项 (用于定位补充或取消的具体任务)</label>
                  <NInput
                    :value="editingTrigger.businessKeyField || ''"
                    placeholder="例如：payload.task_id 或 issue_number"
                    @update:value="v => editingTrigger!.businessKeyField = v"
                  />
                </div>
              </div>
            </NFormItem>

            <NFormItem label="任务指令内容">
              <NInput
                v-model:value="editingTrigger.description"
                type="textarea"
                :rows="3"
                placeholder="详细说明每次自动触发时，员工应执行的步骤与生成的产物..."
              />
            </NFormItem>

            <NFormItem label="① 选择回告渠道与连接 (仅已接入渠道)">
              <div style="display: flex; flex-direction: column; gap: 6px; width: 100%;">
                <NSelect
                  :value="selectedNotifyChannelKey || null"
                  clearable
                  :disabled="!connectedChannelOptions.length"
                  :options="connectedChannelOptions"
                  placeholder="请先选择员工已接入的渠道与连接"
                  @update:value="val => selectedNotifyChannelKey = val || ''"
                />
                <NAlert v-if="!connectedChannelOptions.length" type="warning" :bordered="false" style="font-size: 12px;">
                  当前员工尚未接入任何渠道连接，请先在「渠道绑定」接入渠道后再配置回告群。
                </NAlert>
              </div>
            </NFormItem>

            <NFormItem label="② 选择回告工作群（可选）">
              <NSelect
                :value="editingTrigger.notificationTargets.map(n => formatTargetGroupKey(n.platform, n.connectionId || employee.channels.find(c => c.platform === n.platform)?.connectionId, n.groupId || n.groupName))"
                multiple
                :disabled="!selectedNotifyChannelKey && !editingTrigger.notificationTargets.length"
                :options="triggerGroupSelectOptions"
                :placeholder="selectedNotifyChannelKey ? '从当前连接可访问的群中选择（可留空不选）' : '请先在上方选择回告渠道与连接'"
                @update:value="handleTriggerNotificationTargetsUpdate"
              />
            </NFormItem>

            <NFormItem label="历史任务参考策略">
              <NSelect
                v-model:value="editingTrigger.historyPolicy"
                :options="[
                  { label: '独立执行 - 不参考聊天与其他任务历史', value: 'isolated' },
                  { label: '参考员工历史 - 结合本员工最近任务结果', value: 'employee_shared' }
                ]"
              />
            </NFormItem>
          </NForm>

          <div class="drawer-actions">
            <NButton :disabled="isSavingTriggers" @click="showTriggerDrawer = false">取消</NButton>
            <NButton type="primary" :loading="isSavingTriggers" :disabled="isScheduleCronInvalid" @click="saveTriggerForm">保存触发器</NButton>
          </div>
        </div>
      </NDrawerContent>
    </NDrawer>

    <!-- 触发器历史记录弹窗 -->
    <NModal v-model:show="showTriggerHistoryModal" preset="card" title="触发器历史执行记录" style="width: min(600px, 94vw);">
      <div v-if="viewingTriggerHistory" class="history-modal-body">
        <div class="hm-header">
          <h4 class="hm-title">{{ viewingTriggerHistory.name }}</h4>
          <div style="display: flex; gap: 6px; align-items: center; flex-wrap: wrap;">
            <NButton
              v-if="runningTriggerState?.triggerId === viewingTriggerHistory.id && (runningTriggerState.pollError || runningTriggerState.pollTimedOut) && runningTriggerState.operationId"
              size="tiny"
              type="warning"
              secondary
              :loading="isPollingTrigger"
              @click="resumePollTriggerOperation(viewingTriggerHistory)"
            >
              继续查询状态
            </NButton>
            <NButton
              v-if="runningTriggerState?.triggerId === viewingTriggerHistory.id && runningTriggerState.retryIdempotencyKey"
              size="tiny"
              type="warning"
              secondary
              :loading="isRunningTrigger"
              @click="simulateRunTrigger(viewingTriggerHistory, runningTriggerState.retryIdempotencyKey)"
            >
              重试运行
            </NButton>
            <NButton
              size="tiny"
              type="primary"
              secondary
              :loading="isRunningTrigger"
              :disabled="isTriggerOperationInFlight(viewingTriggerHistory.id)"
              @click="simulateRunTrigger(viewingTriggerHistory)"
            >
              立即测试触发一次
            </NButton>
          </div>
        </div>
        <NAlert
          v-if="runningTriggerState && runningTriggerState.triggerId === viewingTriggerHistory.id"
          :type="runningTriggerState.status === 'failed' || runningTriggerState.status === 'retryable_error' ? 'error' : runningTriggerState.pollError || runningTriggerState.pollTimedOut ? 'warning' : runningTriggerState.status === 'succeeded' ? 'success' : 'info'"
          :bordered="false"
          style="margin-bottom: 10px; font-size: 12px;"
        >
          <div style="display: flex; justify-content: space-between; align-items: center; gap: 8px; flex-wrap: wrap;">
            <span>
              {{ runningTriggerState.message }}
            </span>
            <NButton
              v-if="(runningTriggerState.pollError || runningTriggerState.pollTimedOut) && runningTriggerState.operationId"
              size="tiny"
              type="warning"
              secondary
              :loading="isPollingTrigger"
              @click="resumePollTriggerOperation(viewingTriggerHistory)"
            >
              继续查询状态
            </NButton>
          </div>
        </NAlert>
        <div class="hm-logs-list">
          <div
            v-for="log in viewingTriggerHistory.executionLogs"
            :key="log.id"
            class="hm-log-item"
          >
            <div class="hml-top">
              <span class="hml-time">{{ log.triggeredAt }}</span>
              <NTag
                size="tiny"
                :type="log.status === 'running' ? 'info' : log.status === 'failed' ? 'error' : 'success'"
                :bordered="false"
              >
                {{ log.status === 'running' ? '运行中' : log.status === 'failed' ? '失败' : log.deliveryStatus }}
              </NTag>
            </div>
            <div class="hml-summary">
              {{ log.summary }}
              <span v-if="log.executionId" style="font-size: 11px; color: var(--id-text-muted);"> ({{ log.executionId }})</span>
            </div>
            <span class="hml-dur">耗时: {{ log.duration }}</span>
          </div>
          <div v-if="!viewingTriggerHistory.executionLogs.length" class="empty-note">
            暂无触发记录
          </div>
        </div>
      </div>
    </NModal>
  </div>
</template>

<style scoped>
@import './workforce-shared.css';

.triggers-list {
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.trg-card {
  background: var(--id-bg-surface);
  border: 1px solid var(--id-border-subtle);
  border-radius: var(--id-radius-lg);
  padding: 14px;
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.trg-top {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 10px;
  flex-wrap: wrap;
}

.trg-title-row {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
}

.trg-name {
  font-size: 14px;
  font-weight: 600;
  color: var(--id-text-primary);
  margin: 0;
}

.trg-actions {
  display: flex;
  gap: 8px;
  flex-wrap: wrap;
}

.trg-details {
  display: flex;
  flex-direction: column;
  gap: 6px;
  font-size: 12px;
}

.trg-field {
  display: flex;
  gap: 14px;
  flex-wrap: wrap;
  color: var(--id-text-secondary);
  font-variant-numeric: tabular-nums;
  word-break: break-all;
}

.trg-desc {
  margin: 0;
  color: var(--id-text-primary);
}

.trg-notify-targets {
  font-size: 12px;
  font-variant-numeric: tabular-nums;
  background: var(--id-bg-muted);
  padding: 4px 8px;
  border-radius: var(--id-radius-sm);
  display: flex;
  gap: 6px;
  flex-wrap: wrap;
}

.tnt-lbl {
  color: var(--id-text-secondary);
}

.history-modal-body {
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.hm-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
}

.hm-title {
  font-size: 13px;
  font-weight: 600;
  color: var(--id-text-primary);
  margin: 0;
}

.hm-logs-list {
  display: flex;
  flex-direction: column;
  gap: 8px;
  max-height: 340px;
  overflow-y: auto;
}

.hm-log-item {
  background: var(--id-bg-subtle);
  border: 1px solid var(--id-border-subtle);
  border-radius: var(--id-radius-md);
  padding: 10px 12px;
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.hml-top {
  display: flex;
  align-items: center;
  gap: 10px;
  font-size: 12px;
}

.hml-time {
  font-weight: 600;
  font-family: var(--id-font-mono);
  font-variant-numeric: tabular-nums;
  color: var(--id-text-primary);
}

.hml-dur {
  color: var(--id-text-secondary);
  font-size: 12px;
  font-family: var(--id-font-mono);
  font-variant-numeric: tabular-nums;
}

.hml-summary {
  margin: 0;
  font-size: 12px;
  color: var(--id-text-secondary);
}
</style>

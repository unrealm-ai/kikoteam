<script setup lang="ts">
import { ref, computed, watch, onUnmounted } from 'vue';
import { NButton, NInput, NTag, NAlert, NCheckbox } from 'naive-ui';
import D2ChannelLogo from './D2ChannelLogo.vue';
import { resourcesApi } from '../api';
import {createWorkforceDirectoryReader, type WorkforceDirectoryContext, type DirectoryPrincipal} from '../api/workforce-directory';
import {feishuDirectoryApi, type FeishuDirectoryStatus} from '../api/feishu';
import {wecomContactsApi, type WecomVerification} from '../api/wecom-contacts';
import {
  buildCanonicalPrincipalKey,
  type MemberProviderType,
  type ChannelDirectoryUser,
  type ChannelDirectoryDepartment,
  type ConnectionDirectoryQueryResult,
  type PrincipalRef
} from '../types';

export interface DirectorySourceOption {
  sourceType: MemberProviderType;
  connectionId: string;
  label: string;
  connected: boolean;
}

export interface DirectoryPickItem {
  kind: 'user' | 'department';
  id: string;
  refId?: string | undefined;
  name: string;
  department?: string | undefined;
  sourceType: MemberProviderType;
  connectionId: string;
  bindingStatus?: 'verified' | 'pending_binding' | undefined;
  principal: PrincipalRef;
  user?: ChannelDirectoryUser | undefined;
  dept?: ChannelDirectoryDepartment | undefined;
}

const props = withDefaults(defineProps<{
  sources: DirectorySourceOption[];
  realContext?: WorkforceDirectoryContext | undefined;
  multiple?: boolean;
  includeDepartments?: boolean;
  selectedIds?: string[];
  selectedTargets?: PrincipalRef[];
  pageSize?: number;
}>(), {
  multiple: true,
  includeDepartments: true,
  selectedIds: () => [],
  selectedTargets: () => [],
  pageSize: 8
});

const emit = defineEmits<{
  pick: [item: DirectoryPickItem];
  'update:selectedIds': [ids: string[]];
  'update:selectedTargets': [targets: PrincipalRef[]];
  loaded: [result: ConnectionDirectoryQueryResult];
}>();

const reader = createWorkforceDirectoryReader();
let realController = new AbortController();
const verification = ref<WecomVerification>();
const verifying = ref(false);
const verificationRequests = new Map<string,string>();
const activeKey = ref('');
const searchQuery = ref('');
const departmentTrail=ref<{id:string;name:string}[]>([]);
const directoryStatus=ref<FeishuDirectoryStatus>();
const statusError=ref('');
const now=ref(Date.now());
const clock=setInterval(()=>{now.value=Date.now();},1000);
const verificationPoll = setInterval(() => {
  if (!document.hidden && verification.value?.status === 'pending') void checkVerification();
}, 4000);
const previousSnapshotValid=computed(()=>directoryStatus.value?.complete===true && !!directoryStatus.value.expires_at && Date.parse(directoryStatus.value.expires_at)>now.value && directoryStatus.value.status!=='invalidated');
function enterDepartment(d:ChannelDirectoryDepartment){departmentTrail.value.push({id:d.refId||d.id,name:d.name});searchQuery.value='';page.value=1;reader.reset();void fetchDirectoryPage();}
function breadcrumb(index:number){departmentTrail.value=departmentTrail.value.slice(0,index);searchQuery.value='';page.value=1;reader.reset();void fetchDirectoryPage();}

const page = ref(1);
const loading = ref(false);
const syncing = ref(false);
const activeSyncJobText = ref('');
const errorMsg = ref('');
const result = ref<ConnectionDirectoryQueryResult | null>(null);
const pinnedSnapshotVersion = ref<string>('');

const activeSource = computed(() => {
  const byKey = props.sources.find(s => `${s.sourceType}::${s.connectionId}` === activeKey.value);
  return byKey || props.sources[0] || null;
});

watch(
  () => props.sources,
  (list) => {
    if (!list.length) return;
    const stillValid = list.some(s => `${s.sourceType}::${s.connectionId}` === activeKey.value);
    if (!stillValid) {
      const firstConnected = list.find(s => s.connected) || list[0];
      if (firstConnected) {
        activeKey.value = `${firstConnected.sourceType}::${firstConnected.connectionId}`;
      }
    }
  },
  { immediate: true, deep: true }
);

const totalPages = computed(() => {
  if (!result.value) return 1;
  return Math.max(1, Math.ceil(result.value.totalUsers / result.value.pageSize));
});

let dirFetchSeq = 0;
let syncJobPollTimer: ReturnType<typeof setInterval> | null = null;

function stopSyncJobPoll() {
  if (syncJobPollTimer) {
    clearInterval(syncJobPollTimer);
    syncJobPollTimer = null;
  }
}

onUnmounted(() => {
  clearInterval(clock);
  clearInterval(verificationPoll);
  stopSyncJobPoll(); realController.abort(); ++dirFetchSeq;
});

async function fetchDirectoryPage() {
  const src = activeSource.value;
  const reqSeq = ++dirFetchSeq;
  if (!src) {
    result.value = null;
    loading.value = false;
    return;
  }
  const expectedKey = `${src.sourceType}::${src.connectionId}`;
  const expectedPage = page.value;
  loading.value = true;
  errorMsg.value = '';
  try {
    let res: ConnectionDirectoryQueryResult;
    const params = {
      q: searchQuery.value.trim() || undefined,
      page: expectedPage,
      pageSize: props.pageSize,
      snapshotVersion: expectedPage > 1 && pinnedSnapshotVersion.value ? pinnedSnapshotVersion.value : undefined
    };
    if (props.realContext) {
      realController.abort(); realController = new AbortController();
      res = await reader.page(props.realContext, src.sourceType, src.connectionId, searchQuery.value.trim(), expectedPage, props.includeDepartments || ['feishu','dingtalk'].includes(src.sourceType), realController.signal,departmentTrail.value.at(-1)?.id);
      if(src.sourceType==='feishu'||src.sourceType==='dingtalk'){
        try {const status=await feishuDirectoryApi.status(props.realContext.workspace,props.realContext.employeeId,realController.signal,src.sourceType);if(reqSeq===dirFetchSeq){directoryStatus.value=status;statusError.value='';}}
        catch(e){if(reqSeq===dirFetchSeq){directoryStatus.value=undefined;statusError.value=e instanceof Error?e.message:'同步状态读取失败';}}
      }
    } else if (src.sourceType === 'feishu' || src.sourceType === 'dingtalk') {
      res = await resourcesApi.queryChannelSnapshotDirectory(src.sourceType, src.connectionId, params);
    } else if (src.sourceType === 'wecom') {
      res = await resourcesApi.searchWecomConnectionDirectory(src.connectionId, params);
    } else {
      res = await resourcesApi.queryWebDirectory(params);
    }
    const currentKey = activeSource.value ? `${activeSource.value.sourceType}::${activeSource.value.connectionId}` : '';
    if (reqSeq !== dirFetchSeq || currentKey !== expectedKey) {
      // 丢弃切换渠道或重新搜索后晚返回的过期响应，避免覆盖新渠道数据
      return;
    }
    if ((src.sourceType === 'feishu' || src.sourceType === 'dingtalk') && expectedPage === 1 && res.snapshotVersion) {
      pinnedSnapshotVersion.value = res.snapshotVersion;
    }
    result.value = res;
    emit('loaded', res);
  } catch (err) {
    const currentKey = activeSource.value ? `${activeSource.value.sourceType}::${activeSource.value.connectionId}` : '';
    if (reqSeq !== dirFetchSeq || currentKey !== expectedKey) return;
    errorMsg.value = err instanceof Error ? err.message : '加载通讯录数据源失败';
    result.value = null;
  } finally {
    if (reqSeq === dirFetchSeq) {
      loading.value = false;
    }
  }
}

watch(() => [props.realContext?.workspace, props.realContext?.employeeId], () => {
  departmentTrail.value=[];directoryStatus.value=undefined;statusError.value='';reader.reset(); realController.abort(); page.value=1; verification.value=undefined; verifying.value=false; verificationRequests.clear(); stopSyncJobPoll(); syncing.value=false;
});

watch([activeSource, page, () => props.realContext?.workspace, () => props.realContext?.employeeId], () => {
  fetchDirectoryPage();
}, { immediate: true });

function selectSource(src: DirectorySourceOption) {
  stopSyncJobPoll(); realController.abort(); verification.value = undefined; verifying.value=false; verificationRequests.clear();
  syncing.value = false;
  departmentTrail.value=[];directoryStatus.value=undefined;statusError.value='';
  activeKey.value = `${src.sourceType}::${src.connectionId}`;
  searchQuery.value = '';
  pinnedSnapshotVersion.value = '';
  page.value = 1;
}

function handleSearchTrigger() {
  pinnedSnapshotVersion.value = '';
  page.value = 1;
  fetchDirectoryPage();
}

async function handleManualSync(simulateFailure = false) {
  const src = activeSource.value;
  if (!src || (src.sourceType !== 'feishu' && src.sourceType !== 'dingtalk')) return;
  stopSyncJobPoll();
  syncing.value = true;
  errorMsg.value = '';
  activeSyncJobText.value = '';
  const syncTargetKey = `${src.sourceType}::${src.connectionId}`;
  try {
    if (props.realContext) {
      const context = {...props.realContext};
      const status = await feishuDirectoryApi.sync(context.workspace, context.employeeId, realController.signal, src.sourceType);
      activeSyncJobText.value = `同步状态：${status.status}`;
      const poll = async () => {
        if (props.realContext?.workspace !== context.workspace || props.realContext?.employeeId !== context.employeeId || activeSource.value?.connectionId !== src.connectionId) { stopSyncJobPoll(); return; }
        try {
          const latest = await feishuDirectoryApi.status(context.workspace,context.employeeId,realController.signal,src.sourceType as 'feishu'|'dingtalk');
          activeSyncJobText.value = `同步状态：${latest.status}`;
          if (latest.status !== 'syncing') { stopSyncJobPoll(); syncing.value=false; page.value=1; reader.reset(); await fetchDirectoryPage(); }
        } catch(err) { stopSyncJobPoll(); syncing.value=false; errorMsg.value=err instanceof Error?err.message:'同步状态读取失败'; }
      };
      if (status.status === 'syncing') syncJobPollTimer=setInterval(() => { void poll(); },1500);
      else { syncing.value=false; page.value=1; reader.reset(); await fetchDirectoryPage(); }
      return;
    }
    const startRes = await resourcesApi.triggerDirectorySync({
      connectionId: src.connectionId,
      simulateFailure
    });
    const firstJob = startRes.jobs[0];
    if (firstJob?.status === 'failed' || simulateFailure) {
      activeSyncJobText.value = `同步失败 (${firstJob?.error || '已保留上次数据'})`;
      syncing.value = false;
      pinnedSnapshotVersion.value = '';
      page.value = 1;
      await fetchDirectoryPage();
      return;
    }

    activeSyncJobText.value = `正在同步通讯录 (${firstJob?.progressPercent ?? 25}%)`;
    syncJobPollTimer = setInterval(async () => {
      try {
        const job = await resourcesApi.getDirectorySyncJob(startRes.syncJobId);
        activeSyncJobText.value = `${
          job.status === 'succeeded' ? '同步完成' : job.status === 'failed' ? '同步失败' : '正在同步'
        } (${job.progressPercent}%)`;
        if (job.status === 'succeeded' || job.status === 'failed') {
          stopSyncJobPoll();
          syncing.value = false;
          const currentKey = activeSource.value ? `${activeSource.value.sourceType}::${activeSource.value.connectionId}` : '';
          if (currentKey === syncTargetKey) {
            pinnedSnapshotVersion.value = '';
            page.value = 1;
            await fetchDirectoryPage();
          }
          if (job.status === 'failed') {
            errorMsg.value = job.error || '同步失败，当前展示的是上次同步的通讯录数据';
          }
        }
      } catch (err) {
        stopSyncJobPoll();
        syncing.value = false;
        errorMsg.value = err instanceof Error ? err.message : '查询同步状态失败';
      }
    }, 650);
  } catch (err) {
    syncing.value = false;
    errorMsg.value = err instanceof Error ? err.message : '同步通讯录失败';
  }
}

function makePrincipalRef(
  targetType: 'user' | 'department',
  channel: MemberProviderType,
  connectionId: string,
  targetId: string,
  displayName: string
): PrincipalRef {
  return {
    targetType,
    channel,
    connectionId,
    targetId,
    displayName,
    canonicalKey: buildCanonicalPrincipalKey({ targetType, channel, connectionId, targetId })
  };
}

function getCanonicalKey(principal: PrincipalRef): string {
  return principal.canonicalKey || buildCanonicalPrincipalKey(principal);
}

function isSamePrincipal(a: PrincipalRef, b: PrincipalRef): boolean {
  if (a.canonicalKey && b.canonicalKey) return a.canonicalKey === b.canonicalKey;
  return (
    a.targetType === b.targetType &&
    a.channel === b.channel &&
    a.connectionId === b.connectionId &&
    a.targetId === b.targetId
  );
}

function isTargetSelected(principal: PrincipalRef, fallbackId?: string): boolean {
  const key = getCanonicalKey(principal);
  if (props.selectedTargets && props.selectedTargets.length > 0) {
    return props.selectedTargets.some(t => isSamePrincipal(t, principal));
  }
  if (props.selectedIds.includes(key)) return true;
  if (fallbackId && !props.selectedIds.some(id => id.includes('::'))) {
    return props.selectedIds.includes(fallbackId);
  }
  return false;
}

function isRowDisabled(principal: PrincipalRef, fallbackId?: string): boolean {
  if (props.realContext) {
    const candidate = (principal as Partial<DirectoryPrincipal>).selectionCandidate;
    if (!candidate?.selectable && !isTargetSelected(principal, fallbackId)) return true;
  }
  if (!activeSource.value || activeSource.value.connected) return false;
  if (!props.multiple) return true;
  return !isTargetSelected(principal, fallbackId);
}

async function toggleSelect(item: DirectoryPickItem) {
  if(item.kind==='department' && !props.includeDepartments){if(props.realContext&&item.dept)enterDepartment(item.dept);return;}
  if (props.realContext && !isTargetSelected(item.principal, item.id)) {
    if (loading.value || isRowDisabled(item.principal, item.id)) return;
    const seq = dirFetchSeq; loading.value = true; errorMsg.value = '';
    try {
      const principal = await reader.verify(props.realContext,item.principal as DirectoryPrincipal,realController.signal);
      if (seq !== dirFetchSeq) return;
      item = {...item,principal};
    } catch (e) { if (seq === dirFetchSeq) errorMsg.value = e instanceof Error ? e.message : '身份核验失败'; return; }
    finally { if(seq===dirFetchSeq) loading.value=false; }
  }
  const canonicalKey = getCanonicalKey(item.principal);
  const principal: PrincipalRef = {
    ...item.principal,
    canonicalKey
  };
  const nextItem: DirectoryPickItem = {
    ...item,
    id: canonicalKey,
    principal
  };
  const already = isTargetSelected(principal, item.id);
  if (activeSource.value && !activeSource.value.connected && (!props.multiple || !already)) {
    return;
  }
  if (!props.multiple) {
    emit('update:selectedIds', [canonicalKey]);
    emit('update:selectedTargets', [principal]);
    emit('pick', nextItem);
    return;
  }
  const nextIds = already
    ? props.selectedIds.filter(x => x !== canonicalKey)
    : Array.from(new Set([...props.selectedIds.filter(x => x !== canonicalKey), canonicalKey]));
  const existingTargets = props.selectedTargets || [];
  const nextTargets = already
    ? existingTargets.filter(t => !isSamePrincipal(t, principal))
    : [...existingTargets.filter(t => !isSamePrincipal(t, principal)), principal];
  emit('update:selectedIds', nextIds);
  emit('update:selectedTargets', nextTargets);
  emit('pick', nextItem);
}


async function startVerification(user: ChannelDirectoryUser) {
  const context=props.realContext, row=(user.principal as Partial<DirectoryPrincipal> | undefined)?.selectionCandidate;
  if(!context||!row?.search_user_id||verifying.value)return;
  const seq=dirFetchSeq;verifying.value=true;errorMsg.value='';
  let request=verificationRequests.get(row.search_user_id);if(!request){request=crypto.randomUUID();verificationRequests.set(row.search_user_id,request);}
  try { const result=row.verification_id && row.verification_status==='pending'
    ? await wecomContactsApi.status(context.workspace,context.employeeId,row.verification_id,realController.signal)
    : await wecomContactsApi.start(context.workspace,context.employeeId,row.search_user_id,request,realController.signal);if(seq===dirFetchSeq)verification.value=result; }
  catch(e){if(seq===dirFetchSeq)errorMsg.value=e instanceof Error?e.message:'绑定验证失败';}
  finally{if(seq===dirFetchSeq)verifying.value=false;}
}
async function checkVerification(cancel=false) {
  const context=props.realContext, current=verification.value;
  if(!context||!current||verifying.value)return;
  const controller=realController;
  const stillCurrent=()=>!controller.signal.aborted && verification.value?.id===current.id;
  verifying.value=true;
  try {const result=await(cancel?wecomContactsApi.cancel:wecomContactsApi.status)(context.workspace,context.employeeId,current.id,controller.signal);if(!stillCurrent())return;verification.value=result;if(['expired','cancelled','conflict'].includes(result.status))verificationRequests.delete(result.search_user_id);if(result.status==='verified'){verifying.value=false;page.value=1;await fetchDirectoryPage();}}
  catch(e){if(stillCurrent())errorMsg.value=e instanceof Error?e.message:'绑定状态读取失败';}
  finally{if(stillCurrent())verifying.value=false;}
}

function modeLabel(mode: ConnectionDirectoryQueryResult['queryMode']): string {
  if (mode === 'snapshot') return '已同步通讯录';
  if (mode === 'live_search') return '实时成员查询';
  return '工作区成员目录';
}
</script>

<template>
  <div class="unified-dir-picker">
    <div class="udp-source-tabs">
      <button
        v-for="src in sources"
        :key="`${src.sourceType}::${src.connectionId}`"
        type="button"
        class="udp-tab-btn"
        :class="{ active: activeSource && activeSource.connectionId === src.connectionId && activeSource.sourceType === src.sourceType, unbound: !src.connected }"
        @click="selectSource(src)"
      >
        <D2ChannelLogo :platform="src.sourceType" :size="14" />
        <span>{{ src.label }}</span>
        <NTag v-if="!src.connected" size="tiny" type="default" :bordered="false">未绑定</NTag>
      </button>
    </div>

    <NAlert v-if="activeSource && !activeSource.connected" type="warning" :show-icon="true" style="margin-bottom: 10px;">
      当前员工尚未接入此渠道，仅支持查看或取消已勾选的旧配置，不可新增选择。
    </NAlert>

    <div v-if="activeSource" class="udp-toolbar">
      <NInput
        v-model:value="searchQuery"
        size="small"
        clearable
        :placeholder="activeSource.sourceType === 'wecom' ? '按姓名搜索企业微信成员...' : '按姓名或部门搜索成员...'"
        style="flex: 1;"
        @keyup.enter="handleSearchTrigger"
      />
      <NButton size="small" type="primary" secondary :loading="loading" @click="handleSearchTrigger">
        搜索
      </NButton>
      <NButton
        v-if="activeSource.sourceType === 'feishu' || activeSource.sourceType === 'dingtalk'"
        size="small"
        secondary
        :loading="syncing"
        @click="handleManualSync(false)"
      >
        同步通讯录
      </NButton>
    </div>

    <div v-if="realContext && activeSource && ['feishu','dingtalk'].includes(activeSource.sourceType)" class="udp-meta-bar" aria-label="部门路径">
      <NButton size="tiny" @click="breadcrumb(0)">全部部门</NButton>
      <NButton v-for="(d,i) in departmentTrail" :key="d.id" size="tiny" @click="breadcrumb(i+1)">{{d.name}}</NButton>
    </div>
    <NAlert v-if="realContext && directoryStatus" :type="directoryStatus.error_code ? 'warning':'info'" style="margin-bottom:10px">
      快照版本：{{directoryStatus.snapshot_version}} · 最近同步：{{directoryStatus.synced_at || '暂无同步记录'}} · 有效期至：{{directoryStatus.expires_at || '未知'}} · 同步状态：{{directoryStatus.status}}
      <template v-if="directoryStatus.error_code">；最近同步错误：{{directoryStatus.error_code}}。{{previousSnapshotValid ? '上次完整快照仍在有效期内，可继续使用。':'没有可确认有效的旧快照，请重新同步。'}}</template>
    </NAlert>
    <NAlert v-if="statusError" type="warning">同步状态未确认：{{statusError}}</NAlert>
    <div v-if="result" class="udp-meta-bar">
      <NTag size="tiny" :type="result.queryMode === 'snapshot' ? 'info' : result.queryMode === 'live_search' ? 'warning' : 'default'">
        {{ modeLabel(result.queryMode) }}
      </NTag>
      <span v-if="result.snapshotSyncedAt" class="udp-meta-text">
        最近同步：{{ result.snapshotSyncedAt }}
      </span>
      <span v-if="activeSyncJobText" class="udp-meta-text">
        {{ activeSyncJobText }}
      </span>
    </div>

    <NAlert v-if="result?.snapshotFallbackUsed" type="warning" :show-icon="true" style="margin-bottom: 10px;">
      {{ result.lastSyncError || '最近一次同步失败，当前展示的是上次同步的通讯录数据。' }}
    </NAlert>
    <NAlert v-if="errorMsg" type="error" :show-icon="true" style="margin-bottom: 10px;">
      {{ errorMsg }}
    </NAlert>

    <div v-if="result && (includeDepartments || realContext) && result.departments.length" class="udp-section">
      <div class="udp-sec-title">部门 ({{ result.departments.length }})</div>
      <div class="udp-list">
        <div
          v-for="d in result.departments"
          :key="`${result.connectionId}::dept::${d.id}`"
          class="udp-row"
          :class="{
            selected: isTargetSelected(d.principal || makePrincipalRef('department', result.sourceType, result.connectionId, d.refId || d.id, d.name), d.id),
            disabled: isRowDisabled(d.principal || makePrincipalRef('department', result.sourceType, result.connectionId, d.refId || d.id, d.name), d.id)
          }"
          @click="toggleSelect({ kind: 'department', id: d.id, refId: d.refId, name: d.name, sourceType: result.sourceType, connectionId: result.connectionId, principal: d.principal || makePrincipalRef('department', result.sourceType, result.connectionId, d.refId || d.id, d.name), dept: d })"
        >
          <NCheckbox
            v-if="multiple && includeDepartments"
            :checked="isTargetSelected(d.principal || makePrincipalRef('department', result.sourceType, result.connectionId, d.refId || d.id, d.name), d.id)"
            :disabled="isRowDisabled(d.principal || makePrincipalRef('department', result.sourceType, result.connectionId, d.refId || d.id, d.name), d.id)"
            @click.stop
            @update:checked="() => toggleSelect({ kind: 'department', id: d.id, refId: d.refId, name: d.name, sourceType: result!.sourceType, connectionId: result!.connectionId, principal: d.principal || makePrincipalRef('department', result!.sourceType, result!.connectionId, d.refId || d.id, d.name), dept: d })"
          />
          <NTag size="tiny" type="info">部门</NTag>
          <strong class="udp-name">{{ d.name }}</strong>
          <span v-if="!realContext" class="udp-sub">{{ d.memberCount }} 人</span>
          <NButton v-if="realContext" size="tiny" @click.stop="enterDepartment(d)">查看子部门和人员</NButton>
        </div>
      </div>
    </div>

    <div v-if="result" class="udp-section">
      <div class="udp-sec-title">
        人员列表 (本页 {{ result.users.length }}<template v-if="!realContext"> / 共 {{ result.totalUsers }} 人</template>)
      </div>
      <div v-if="!result.users.length" class="udp-empty">
        未匹配到成员记录
      </div>
      <div v-else class="udp-list">
        <div
          v-for="u in result.users"
          :key="`${result.connectionId}::user::${u.id}`"
          class="udp-row"
          :class="{
            selected: isTargetSelected(u.principal || makePrincipalRef('user', result.sourceType, result.connectionId, u.refId || u.id, u.name), u.id),
            disabled: isRowDisabled(u.principal || makePrincipalRef('user', result.sourceType, result.connectionId, u.refId || u.id, u.name), u.id)
          }"
          @click="toggleSelect({ kind: 'user', id: u.id, refId: u.refId, name: u.name, department: u.department, sourceType: result.sourceType, connectionId: result.connectionId, bindingStatus: u.bindingStatus, principal: u.principal || makePrincipalRef('user', result.sourceType, result.connectionId, u.refId || u.id, u.name), user: u })"
        >
          <NCheckbox
            v-if="multiple"
            :checked="isTargetSelected(u.principal || makePrincipalRef('user', result.sourceType, result.connectionId, u.refId || u.id, u.name), u.id)"
            :disabled="isRowDisabled(u.principal || makePrincipalRef('user', result.sourceType, result.connectionId, u.refId || u.id, u.name), u.id)"
            @click.stop
            @update:checked="() => toggleSelect({ kind: 'user', id: u.id, refId: u.refId, name: u.name, department: u.department, sourceType: result!.sourceType, connectionId: result!.connectionId, bindingStatus: u.bindingStatus, principal: u.principal || makePrincipalRef('user', result!.sourceType, result!.connectionId, u.refId || u.id, u.name), user: u })"
          />
          <strong class="udp-name">{{ u.name }}</strong>
          <NButton v-if="realContext && result.sourceType === 'wecom' && u.bindingStatus !== 'verified'" size="tiny" :loading="verifying" @click.stop="startVerification(u)">绑定验证</NButton>
          <span class="udp-sub">{{ u.department }}</span>
          <span v-if="realContext && result.sourceType==='wecom'" class="udp-sub">搜索 ID：{{ (u.principal as DirectoryPrincipal)?.selectionCandidate.search_user_id || '未返回' }} · 消息／回调 ID：{{u.bindingStatus==='verified' ? u.refId : '待验证后确认'}}</span>
          <NTag
            v-if="result.sourceType === 'wecom'"
            size="tiny"
            :type="u.bindingStatus === 'verified' ? 'success' : 'warning'"
            round
          >
            {{ u.bindingStatus === 'verified' ? '已核验' : '待核验' }}
          </NTag>
        </div>
      </div>
    </div>

    <NAlert v-if="realContext && verification" type="info">
      绑定验证：{{ verification.status }} · 有效期至：{{verification.expires_at}}
      <template v-if="verification.status === 'pending' && Date.parse(verification.expires_at) > now">，核对联系人后将验证码私下交给本人，让本人私聊本机器人发送：{{ verification.code }}。不要在群里公开；验证完成不会自动授予权限，仍需选择并保存配置。</template>
      <template v-if="verification.status === 'pending' && Date.parse(verification.expires_at) <= now">，验证码已过期，正在更新状态。</template>
      <NButton size="tiny" :disabled="verifying" @click="checkVerification()">查询状态</NButton>
      <NButton v-if="verification.status === 'pending'" size="tiny" :disabled="verifying" @click="checkVerification(true)">取消验证</NButton>
    </NAlert>
    <div v-if="realContext && result && (result.page > 1 || result.nextCursor)" class="udp-pagination">
      <span class="udp-page-info">第 {{ result.page }} 页</span>
      <div class="udp-page-btns"><NButton size="tiny" secondary :disabled="loading || result.page <= 1" @click="page--">上一页</NButton><NButton size="tiny" secondary :disabled="loading || !result.nextCursor" @click="page++">下一页</NButton></div>
    </div>
    <div v-if="!realContext && result && totalPages > 1" class="udp-pagination">
      <span class="udp-page-info">第 {{ result.page }} / {{ totalPages }} 页 (共 {{ result.totalUsers }} 人)</span>
      <div class="udp-page-btns">
        <NButton size="tiny" secondary :disabled="result.page <= 1" @click="page = Math.max(1, page - 1)">上一页</NButton>
        <NButton size="tiny" secondary :disabled="result.page >= totalPages" @click="page = Math.min(totalPages, page + 1)">下一页</NButton>
      </div>
    </div>
  </div>
</template>

<style scoped>
.unified-dir-picker {
  border: 1px solid var(--id-border-subtle);
  border-radius: var(--id-radius-lg);
  padding: 12px;
  background: var(--id-bg-subtle);
}
.udp-source-tabs {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
  margin-bottom: 10px;
}
.udp-tab-btn {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  padding: 5px 10px;
  border-radius: var(--id-radius-md);
  border: 1px solid var(--id-border-subtle);
  background: var(--id-bg-surface);
  color: var(--id-text-secondary);
  font-size: 12px;
  cursor: pointer;
  transition: all var(--id-transition-fast);
}
.udp-tab-btn:hover {
  border-color: var(--id-border-strong);
  color: var(--id-text-primary);
}
.udp-tab-btn.unbound {
  opacity: 0.78;
}
.udp-tab-btn.active {
  border-color: var(--id-accent-primary);
  background: var(--id-accent-primary-soft);
  color: var(--id-accent-primary);
  font-weight: 600;
}
.udp-conn-id {
  font-size: 12px;
  font-family: var(--id-font-mono);
  opacity: 0.75;
}
.udp-toolbar {
  display: flex;
  gap: 8px;
  align-items: center;
  margin-bottom: 8px;
}
.udp-meta-bar {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
  align-items: center;
  margin-bottom: 10px;
  font-size: 12px;
  color: var(--id-text-secondary);
  font-variant-numeric: tabular-nums;
}
.udp-meta-text code {
  font-size: 12px;
  font-family: var(--id-font-mono);
  background: var(--id-bg-muted);
  padding: 1px 4px;
  border-radius: var(--id-radius-sm);
}
.udp-section {
  margin-top: 8px;
}
.udp-sec-title {
  font-size: 12px;
  font-weight: 600;
  color: var(--id-text-secondary);
  margin-bottom: 6px;
}
.udp-list {
  display: flex;
  flex-direction: column;
  gap: 4px;
}
.udp-row {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 6px 10px;
  background: var(--id-bg-surface);
  border: 1px solid var(--id-border-subtle);
  border-radius: var(--id-radius-md);
  cursor: pointer;
  font-size: 12px;
  transition: border-color var(--id-transition-fast), background var(--id-transition-fast);
}
.udp-row:hover {
  border-color: var(--id-border-strong);
}
.udp-row.selected {
  border-color: var(--id-accent-primary);
  background: var(--id-accent-primary-soft);
}
.udp-row.disabled {
  opacity: 0.58;
  cursor: not-allowed;
}
.udp-row.disabled:hover {
  border-color: var(--id-border-subtle);
}
.udp-name {
  color: var(--id-text-primary);
}
.udp-sub {
  flex: 1;
  color: var(--id-text-secondary);
  font-size: 12px;
}
.udp-empty {
  padding: 14px;
  text-align: center;
  font-size: 12px;
  color: var(--id-text-muted);
  background: var(--id-bg-surface);
  border-radius: var(--id-radius-md);
}
.udp-pagination {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-top: 10px;
  padding-top: 8px;
  border-top: 1px solid var(--id-border-subtle);
}
.udp-page-info {
  font-size: 12px;
  font-variant-numeric: tabular-nums;
  color: var(--id-text-secondary);
}
.udp-page-btns {
  display: flex;
  gap: 6px;
}

@media (max-width: 640px) {
  .udp-toolbar {
    flex-wrap: wrap;
  }
  .udp-row {
    flex-wrap: wrap;
  }
  .udp-pagination {
    flex-direction: column;
    align-items: flex-start;
    gap: 8px;
  }
}
</style>

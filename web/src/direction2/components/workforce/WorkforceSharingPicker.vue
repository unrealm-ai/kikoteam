<script setup lang="ts">
import { computed, onUnmounted, ref, watch } from 'vue';
import { NAlert, NButton, NSelect } from 'naive-ui';
import { selectorsApi, selectorKey, type SelectionCandidate } from '../../api/selectors';
import { wecomContactsApi, type WecomVerification } from '../../api/wecom-contacts';
import { filterLiveGroupNames, loadLiveFeishuGroups, selectLiveFeishuGroup } from '../../api/feishu-group-selection';
import { D2ApiError } from '../../api/client';
import type { UsageChannel } from '../../api/agent-usage';

const props = defineProps<{workspace: string; employeeId: string; kind: 'groups' | 'people'; disabled?: boolean}>();
const emit = defineEmits<{pick: [candidate: SelectionCandidate]; revoked: []}>();
const channel = ref<UsageChannel>('feishu'), query = ref(''), rows = ref<SelectionCandidate[]>([]);
const snapshotVersion = ref<number>();
const next = ref<string | null>(null), loading = ref(false), error = ref(''), limited = ref(false), loaded = ref(false);
const verification = ref<WecomVerification>(), job = ref(''), jobStatus = ref(''), discoveryCursor = ref('');
const labels = {feishu: '飞书', wecom: '企微'};
const feishuGroupSource = ref<'provider' | 'observed'>();
const visibleRows = computed(() => feishuGroupSource.value === 'provider' ? filterLiveGroupNames(rows.value, query.value) : rows.value);
const channelOptions = Object.entries(labels).map(([value, label]) => ({value, label}));
const options = computed(() => visibleRows.value.filter(row => row.ref.provider === channel.value && row.ref.provider in labels && row.ref.kind === (props.kind === 'groups' ? 'group' : 'user')).map(row => ({
  value: selectorKey(row.ref), label: row.display_name || row.name || row.ref.external_id || row.search_user_id || '未命名',
  disabled: !row.selectable
})));
const pendingPeople = computed(() => props.kind === 'people' && channel.value === 'wecom' ? rows.value.filter(row => !row.selectable && row.search_user_id) : []);
let generation = 0, read = 0, controller = new AbortController(), seen = new Set<string>(), discovered = new Set<string>();
const requests = new Map<string, string>();
let searchTimer: ReturnType<typeof setTimeout> | undefined;
const available = computed(() => !props.disabled && !loading.value);
function fail(e: unknown) {
  error.value = e instanceof Error ? e.message : '读取候选失败';
  if (e instanceof D2ApiError && [401, 403].includes(e.status)) { rows.value = []; emit('revoked'); }
}
async function search(more = false) {
  if (props.disabled || (more && loading.value)) return;
  clearTimeout(searchTimer);
  query.value = query.value.trim();
  if (props.kind === 'people' && channel.value === 'wecom' && !query.value) { rows.value = []; return; }
  if (props.kind === 'groups' && channel.value === 'feishu') {
    if (!feishuGroupSource.value) { await refresh(); return; }
    if (feishuGroupSource.value === 'provider') return; // Search loaded live results locally.
  }
  const cursor = more ? next.value || '' : '';
  if (more && !cursor) return;
  const g = generation, r = ++read; loading.value = true; error.value = '';
  if (!more) { rows.value = []; next.value = null; seen = new Set(); snapshotVersion.value = undefined; }
  try {
    const result = await selectorsApi.search(props.workspace, props.employeeId, props.kind, channel.value, query.value, cursor, 'sharing', controller.signal, more ? snapshotVersion.value : undefined);
    if (g !== generation || r !== read) return;
    if (more && snapshotVersion.value !== undefined && result.snapshot_version !== snapshotVersion.value) throw new Error('通讯录快照已变化，请重新搜索');
    snapshotVersion.value = result.snapshot_version;
    if (result.next_cursor && (result.next_cursor === cursor || seen.has(result.next_cursor))) throw new Error('候选分页异常，请重新搜索');
    seen.add(cursor);
    rows.value = [...rows.value.filter(old => !result.items.some(row => selectorKey(old.ref) === selectorKey(row.ref))), ...result.items];
    next.value = result.next_cursor; limited.value = !!result.limited; loaded.value = true;
  } catch (e) { if (g === generation && r === read) { next.value = null; fail(e); } }
  finally { if (g === generation && r === read) loading.value = false; }
}
function changeQuery(value: string) {
  query.value = value; clearTimeout(searchTimer);
  searchTimer = setTimeout(() => { void search(); }, 250);
}
async function pick(key: string) {
  if (!available.value) return;
  const row = rows.value.find(item => selectorKey(item.ref) === key);
  if (!row?.selectable || !(row.ref.provider in labels) || row.ref.provider !== channel.value) return;
  const g = generation; loading.value = true; error.value = '';
  try {
    if (row.selection_token) {
      const selected = await selectLiveFeishuGroup(props.workspace, props.employeeId, row.selection_token, controller.signal);
      if (g === generation && selectorKey(selected.ref) === selectorKey(row.ref)) { emit('pick', selected); }
      return;
    }
    const result = await selectorsApi.resolve(props.workspace, props.employeeId, props.kind, [row.ref], 'sharing', controller.signal);
    if (g !== generation) return;
    const current = result.items.find(item => selectorKey(item.ref) === key);
    if (!current?.selectable) throw new Error(current?.disabled_reason || '候选已失效，请重新搜索');
    emit('pick', current);
  } catch (e) { if (g === generation) fail(e); }
  finally { if (g === generation) loading.value = false; }
}
async function verify(row: SelectionCandidate) {
  if (!available.value || !row.search_user_id) return;
  const g = generation; loading.value = true; error.value = '';
  let request = requests.get(row.search_user_id);
  if (!request) { request = crypto.randomUUID(); requests.set(row.search_user_id, request); }
  try {
    const result = row.verification_id && row.verification_status === 'pending'
      ? await wecomContactsApi.status(props.workspace, props.employeeId, row.verification_id, controller.signal)
      : await wecomContactsApi.start(props.workspace, props.employeeId, row.search_user_id, request, controller.signal);
    if (g === generation) verification.value = result;
  } catch (e) { if (g === generation) fail(e); }
  finally { if (g === generation) loading.value = false; }
}
async function verifyStatus(cancel = false) {
  if (!available.value || !verification.value) return;
  const g = generation; loading.value = true; error.value = '';
  try {
    const result = await (cancel ? wecomContactsApi.cancel : wecomContactsApi.status)(props.workspace, props.employeeId, verification.value.id, controller.signal);
    if (g !== generation) return;
    verification.value = result;
    if (['cancelled', 'expired', 'conflict'].includes(result.status)) requests.delete(result.search_user_id);
    if (result.status === 'verified') { loading.value = false; await search(); }
  } catch (e) { if (g === generation) fail(e); }
  finally { if (g === generation) loading.value = false; }
}
async function refresh(more = false) {
  if (!available.value) return;
  const g = generation; loading.value = true; error.value = '';
  try {
    if (channel.value === 'feishu') {
      const cursor = more ? discoveryCursor.value : '';
      const result = await loadLiveFeishuGroups(props.workspace, props.employeeId, cursor, controller.signal);
      if (g !== generation) return;
      feishuGroupSource.value = result.source;
      if (result.source === 'observed') {
        discoveryCursor.value = ''; discovered = new Set(); loading.value = false; await search(); return;
      }
      if (result.cursor && (result.cursor === cursor || discovered.has(result.cursor))) throw new Error('群刷新游标重复，请重新搜索');
      if (!more) discovered = new Set();
      discovered.add(cursor); discoveryCursor.value = result.cursor; next.value = null;
      const kept = more ? rows.value.filter(old => !result.rows.some(row => selectorKey(row.ref) === selectorKey(old.ref))) : [];
      rows.value = [...kept, ...result.rows]; loaded.value = true; limited.value = false;
    } else if (channel.value === 'wecom') {
      const result = job.value
        ? await selectorsApi.wecomRefreshStatus(props.workspace, props.employeeId, job.value, controller.signal)
        : await selectorsApi.refreshWecom(props.workspace, props.employeeId, controller.signal);
      if (g !== generation) return;
      job.value = result.job_id; jobStatus.value = result.status;
      if (result.status === 'completed') { job.value = ''; loading.value = false; await search(); }
      else if (result.status === 'failed') { job.value = ''; throw new Error('企微近期群刷新失败'); }
    }
  } catch (e) { if (g === generation) fail(e); }
  finally { if (g === generation) loading.value = false; }
}
function reset() {
  clearTimeout(searchTimer);
  ++generation; ++read; controller.abort(); controller = new AbortController(); rows.value = []; next.value = null;
  loading.value = false; error.value = ''; loaded.value = false; limited.value = false; query.value = '';
  verification.value = undefined; requests.clear(); job.value = ''; jobStatus.value = ''; discoveryCursor.value = ''; discovered = new Set(); feishuGroupSource.value = undefined;
}
watch(() => [props.workspace, props.employeeId, props.kind, channel.value], () => { reset(); void search(); }, {immediate: true});
const verificationPoll = setInterval(() => {
  if (!document.hidden && verification.value?.status === 'pending') void verifyStatus();
}, 4000);
onUnmounted(() => { clearTimeout(searchTimer); clearInterval(verificationPoll); ++generation; controller.abort(); });
</script>

<template>
  <div class="sharing-picker">
    <div class="sharing-picker-search">
      <NSelect :virtual-scroll="false" v-model:value="channel" :options="channelOptions" :disabled="disabled || loading" style="width: 95px; flex-shrink: 0;" />
      <NSelect :virtual-scroll="false" :value="null" filterable remote clearable :options="options" :loading="loading" :disabled="disabled" :placeholder="kind === 'groups' ? '搜索 AI 员工所在群' : '搜索对应渠道的真人'" @search="changeQuery" @update:value="pick" />
      <NButton :disabled="!available" @click="search()">搜索</NButton>
    </div>
    <div v-if="next || (kind === 'groups' && ['feishu', 'wecom'].includes(channel))" class="sharing-picker-actions">
      <NButton v-if="next" size="tiny" secondary :disabled="!available" @click="search(true)">加载更多候选</NButton>
      <template v-if="kind === 'groups' && ['feishu', 'wecom'].includes(channel)">
        <NButton size="tiny" secondary :disabled="!available" @click="refresh()">{{ channel === 'feishu' ? '刷新飞书群' : job ? '查询刷新状态' : '刷新企微近期群' }}</NButton>
        <NButton v-if="discoveryCursor" size="tiny" secondary :disabled="!available" @click="refresh(true)">继续刷新飞书群</NButton>
        <small v-if="jobStatus">{{ jobStatus }}</small>
        <small v-if="feishuGroupSource === 'observed'">飞书接口暂不可用，显示历史观察到的群，可能不全。</small>
      </template>
    </div>
    <small v-if="loaded && !loading && !rows.length && !error">没有匹配的候选。</small>
    <small v-if="limited">仅返回部分结果，请缩小搜索范围。</small>
    <NAlert v-if="error" type="error">{{ error }}</NAlert>
    <template v-if="kind === 'people' && channel === 'wecom'">
      <small>企微仅支持搜索与绑定验证，不提供完整通讯录或部门查询。</small>
      <div v-for="row in pendingPeople" :key="row.search_user_id" class="pending-person-row">
        <span>{{ row.display_name || row.name || row.search_user_id }} · {{ row.disabled_reason || '待绑定验证' }}</span>
        <NButton size="tiny" secondary :disabled="!available" @click="verify(row)">绑定验证</NButton>
      </div>
      <NAlert v-if="verification" type="info">
        <div>
          验证状态：{{ verification.status }}
          <template v-if="verification.status === 'pending' && Date.parse(verification.expires_at) > Date.now()">，请让该用户私聊此机器人发送：{{ verification.code }}</template>
        </div>
        <div class="sharing-picker-actions" style="margin-top: 6px;">
          <NButton size="tiny" secondary :disabled="!available" @click="verifyStatus()">查询状态</NButton>
          <NButton v-if="verification.status === 'pending'" size="tiny" quaternary type="error" :disabled="!available" @click="verifyStatus(true)">取消验证</NButton>
        </div>
      </NAlert>
    </template>
  </div>
</template>
<style scoped>
.sharing-picker { width: 100%; display: flex; flex-direction: column; gap: 8px; }
.sharing-picker-search { display: flex; gap: 8px; width: 100%; }
.sharing-picker-actions { display: flex; flex-wrap: wrap; align-items: center; gap: 8px; }
.pending-person-row {
  display: flex; align-items: center; justify-content: space-between; gap: 8px; flex-wrap: wrap;
  padding: 6px 10px; background: var(--id-bg-subtle); border: 1px solid var(--id-border-subtle);
  border-radius: var(--id-radius-sm); font-size: 12px; color: var(--id-text-primary);
}
.sharing-picker small { color: var(--id-text-secondary); font-size: 12px; }
</style>

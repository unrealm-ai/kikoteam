<script setup lang="ts">
import { computed, onUnmounted, ref, watch } from 'vue';
import { NAlert, NButton, NCheckbox, NInput, NSelect } from 'naive-ui';
import D2ChannelLogo from './D2ChannelLogo.vue';
import { selectorsApi, selectorKey, type SelectionCandidate, type SelectorPurpose, type PeopleSource } from '../api/selectors';
import { wecomContactsApi, type WecomVerification } from '../api/wecom-contacts';
import { feishuDirectoryApi, type FeishuDirectoryStatus } from '../api/feishu';
import { filterLiveGroupNames, loadLiveFeishuGroups, selectLiveFeishuGroup } from '../api/feishu-group-selection';
import { D2ApiError } from '../api/client';
import type { UsageChannel } from '../api/agent-usage';

const props = withDefaults(defineProps<{
  workspace: string;
  employeeId: string;
  kind: 'people' | 'groups';
  purpose: SelectorPurpose;
  disabled?: boolean;
  includeDepartments?: boolean;
  providers?: UsageChannel[];
}>(), { disabled: false, includeDepartments: false });
const emit = defineEmits<{ pick: [candidate: SelectionCandidate, includeDescendants: boolean]; revoked: [] }>();
const channel = ref<UsageChannel>('web'), query = ref(''), departments = ref(false), descendants = ref(false);
const rows = ref<SelectionCandidate[]>([]), next = ref<string | null>(null), busy = ref(false), error = ref(''), loaded = ref(false), limited = ref(false);
const verification = ref<WecomVerification>(), refreshJob = ref(''), refreshStatus = ref('');
const now = ref(Date.now());
const timer = setInterval(() => { now.value = Date.now(); }, 1000);
const verificationPending = computed(() => verification.value?.status === 'pending' && Date.parse(verification.value.expires_at) > now.value);
let generation = 0, read = 0, controller = new AbortController(), seen = new Set<string>();
const requests = new Map<string, string>();
const labels = { web: 'Web', feishu: '飞书', dingtalk: '钉钉', wecom: '企微' };
const directoryStatus = ref<FeishuDirectoryStatus>();
const trail = ref<SelectionCandidate[]>([]);
const feishuGroupSource = ref<'provider' | 'observed'>();
const visibleRows = computed(() => feishuGroupSource.value === 'provider' ? filterLiveGroupNames(rows.value, query.value) : rows.value);
const sources = ref<PeopleSource[]>([]), snapshotVersion = ref<number>();
const options = computed(() => Object.entries(labels).filter(([value]) => !props.providers || props.providers.includes(value as UsageChannel)).map(([value, label]) => ({ value, label })));
const source = computed(() => sources.value.find(s => s.provider === channel.value));
const departmentAvailable = computed(() => props.kind === 'people' && props.includeDepartments && !!source.value?.supports_department_tree);
const writable = computed(() => !props.disabled && !busy.value);
function reset() {
  ++generation; ++read; controller.abort(); controller = new AbortController();
  rows.value = []; next.value = null; seen = new Set(); error.value = ''; busy.value = false; loaded.value = false; limited.value = false;
  directoryStatus.value = undefined; snapshotVersion.value = undefined; verification.value = undefined;
  refreshJob.value = ''; refreshStatus.value = ''; discoveryCursor.value = ''; discoverySeen = new Set(); feishuGroupSource.value = undefined; requests.clear();
}
function fail(e: unknown) {
  error.value = e instanceof Error ? e.message : '候选读取失败';
  if (e instanceof D2ApiError && [401, 403].includes(e.status)) { rows.value = []; next.value = null; emit('revoked'); }
}
async function search(more = false) {
  if (!writable.value || (props.kind === 'people' && source.value?.available === false) || (channel.value === 'wecom' && props.kind === 'people' && !query.value.trim())) return;
  if (props.kind === 'groups' && channel.value === 'feishu') {
    if (!feishuGroupSource.value) { await refresh(); return; }
    if (feishuGroupSource.value === 'provider') return; // Search loaded live results locally.
  }
  const cursor = more ? next.value || '' : '';
  if (more && !cursor) return;
  const g = generation, r = ++read; busy.value = true; error.value = '';
  if (!more) { rows.value = []; next.value = null; snapshotVersion.value = undefined; seen = new Set(); }
  try {
    const result = await selectorsApi.search(
      props.workspace, props.employeeId,
      props.kind === 'groups' ? 'groups' : departments.value && departmentAvailable.value ? 'departments' : 'people',
      channel.value, query.value.trim(), cursor, props.purpose, controller.signal,
      more ? snapshotVersion.value : undefined,
      departments.value && departmentAvailable.value ? trail.value.at(-1)?.ref.external_id || '' : undefined
    );
    if (g !== generation || r !== read) return;
    if (!Array.isArray(result.items)) throw new Error('候选响应无效');
    if (more && snapshotVersion.value !== undefined && result.snapshot_version !== snapshotVersion.value) { rows.value = []; throw new Error('通讯录快照发生变化，请重新搜索'); }
    if (result.next_cursor && (result.next_cursor === cursor || seen.has(result.next_cursor))) throw new Error('候选分页游标重复，请重新搜索');
    seen.add(cursor); snapshotVersion.value = result.snapshot_version;
    rows.value = [...rows.value.filter(v => !result.items.some(i => selectorKey(i.ref) === selectorKey(v.ref))), ...result.items];
    next.value = result.next_cursor || null; loaded.value = true; limited.value = !!result.limited;
  } catch (e) { if (g === generation && r === read) { next.value = null; rows.value = []; fail(e); } }
  finally { if (g === generation && r === read) busy.value = false; }
}
async function pick(row: SelectionCandidate) {
  if (!writable.value || !row.selectable) return;
  const g = generation; busy.value = true; error.value = '';
  try {
    if (row.selection_token) {
      const selected = await selectLiveFeishuGroup(props.workspace, props.employeeId, row.selection_token, controller.signal);
      if (g === generation && selectorKey(selected.ref) === selectorKey(row.ref)) { emit('pick', selected, false); }
      return;
    }
    const result = await selectorsApi.resolve(props.workspace, props.employeeId, props.kind, [row.ref], props.purpose, controller.signal);
    if (g !== generation) return;
    if (!Array.isArray(result.items)) throw new Error('候选核验响应无效');
    const verified = result.items.find(v => selectorKey(v.ref) === selectorKey(row.ref));
    if (!verified?.selectable) throw new Error(verified?.disabled_reason || '候选已失效，请重新搜索');
    emit('pick', verified, descendants.value);
  } catch (e) { if (g === generation) fail(e); }
  finally { if (g === generation) busy.value = false; }
}
function setVerification(value: WecomVerification) {
  if (value.status === 'pending' && Date.parse(value.expires_at) <= Date.now()) value = { ...value, status: 'expired', code: undefined };
  verification.value = value;
  if (['cancelled', 'expired', 'conflict'].includes(value.status)) {
    requests.delete(value.search_user_id);
    rows.value = rows.value.map(row => {
      if (row.search_user_id !== value.search_user_id) return row;
      const nextRow = { ...row, verification_status: value.status };
      delete nextRow.verification_id;
      return nextRow;
    });
  }
}
async function verify(row: SelectionCandidate) {
  if (!writable.value || !row.search_user_id) return;
  const g = generation; busy.value = true; error.value = '';
  let request = requests.get(row.search_user_id);
  if (!request) { request = crypto.randomUUID(); requests.set(row.search_user_id, request); }
  try {
    const value = row.verification_id && row.verification_status === 'pending'
      ? await wecomContactsApi.status(props.workspace, props.employeeId, row.verification_id, controller.signal)
      : await wecomContactsApi.start(props.workspace, props.employeeId, row.search_user_id, request, controller.signal);
    if (g === generation) setVerification(value);
  } catch (e) { if (g === generation) fail(e); }
  finally { if (g === generation) busy.value = false; }
}
async function verifyStatus(cancel = false) {
  if (!writable.value || !verification.value) return;
  const g = generation; busy.value = true; error.value = '';
  try {
    const result = await (cancel ? wecomContactsApi.cancel : wecomContactsApi.status)(props.workspace, props.employeeId, verification.value.id, controller.signal);
    if (g !== generation) return;
    setVerification(result);
    if (result.status === 'verified') { busy.value = false; await search(); }
  } catch (e) { if (g === generation) fail(e); }
  finally { if (g === generation) busy.value = false; }
}
async function enterDepartment(row?: SelectionCandidate) {
  if (!writable.value) return;
  if (row) trail.value.push(row); else trail.value.pop();
  query.value = ''; await search();
}
async function refreshDirectory(sync = false) {
  if (!writable.value || !['feishu', 'dingtalk'].includes(channel.value)) return;
  const g = generation; busy.value = true; error.value = ''; rows.value = []; next.value = null;
  try {
    const value = await (sync ? feishuDirectoryApi.sync : feishuDirectoryApi.status)(props.workspace, props.employeeId, controller.signal, channel.value as 'feishu' | 'dingtalk');
    if (g !== generation) return;
    directoryStatus.value = value;
    if (value.complete && value.expires_at && Date.parse(value.expires_at) > Date.now()) { busy.value = false; await search(); }
  } catch (e) { if (g === generation) fail(e); }
  finally { if (g === generation) busy.value = false; }
}
const discoveryCursor = ref('');
let discoverySeen = new Set<string>();
async function refresh(more = false) {
  if (!writable.value) return;
  const g = generation; busy.value = true; error.value = '';
  try {
    if (channel.value === 'feishu') {
      const cursor = more ? discoveryCursor.value : '';
      const result = await loadLiveFeishuGroups(props.workspace, props.employeeId, cursor, controller.signal);
      if (g !== generation) return;
      feishuGroupSource.value = result.source;
      if (result.source === 'observed') {
        discoveryCursor.value = ''; discoverySeen = new Set(); busy.value = false; await search(); return;
      }
      if (result.cursor && (result.cursor === cursor || discoverySeen.has(result.cursor))) throw new Error('群刷新游标重复，请重新搜索');
      if (!more) discoverySeen = new Set();
      discoverySeen.add(cursor); discoveryCursor.value = result.cursor; next.value = null;
      const kept = more ? rows.value.filter(old => !result.rows.some(row => selectorKey(row.ref) === selectorKey(old.ref))) : [];
      rows.value = [...kept, ...result.rows]; loaded.value = true; limited.value = false;
    } else if (channel.value === 'wecom') {
      const result = refreshJob.value
        ? await selectorsApi.wecomRefreshStatus(props.workspace, props.employeeId, refreshJob.value, controller.signal)
        : await selectorsApi.refreshWecom(props.workspace, props.employeeId, controller.signal);
      if (g !== generation) return;
      refreshJob.value = result.job_id; refreshStatus.value = result.status;
      if (result.status === 'completed') { refreshJob.value = ''; busy.value = false; await search(); }
      else if (result.status === 'failed') { refreshJob.value = ''; throw new Error('企微近期群刷新失败，请重试'); }
    }
  } catch (e) { if (g === generation) fail(e); }
  finally { if (g === generation) busy.value = false; }
}
async function initialize() {
  reset(); sources.value = []; trail.value = [];
  if (props.disabled) return;
  const g = generation;
  if (props.kind === 'people') {
    try {
      const result = await selectorsApi.sources(props.workspace, props.employeeId, props.purpose, controller.signal);
      if (g !== generation) return;
      if (!Array.isArray(result.items)) throw new Error('渠道能力响应无效');
      sources.value = result.items;
    } catch (e) { if (g === generation) fail(e); return; }
  }
  await search();
}
watch(() => [props.workspace, props.employeeId, props.kind, props.purpose], () => void initialize(), { immediate: true });
watch(() => props.disabled, disabled => {
  // Parent operations briefly disable picking; do not re-fetch the live
  // Feishu page if it was already loaded for this employee.
  if (props.kind === 'groups' && channel.value === 'feishu' && (disabled || feishuGroupSource.value)) return;
  void initialize();
});
watch([channel, departments], () => { query.value = ''; void initialize(); });
watch(verificationPending, v => { if (!v && verification.value?.status === 'pending') setVerification({ ...verification.value, status: 'expired', code: undefined }); });
onUnmounted(() => { reset(); clearInterval(timer); });
</script>

<template>
  <section class="identity-picker" :aria-label="kind === 'groups' ? '统一选群' : '统一选人'">
    <div class="actions">
      <NSelect :virtual-scroll="false" v-model:value="channel" :options="options" :disabled="disabled" size="small" />
      <NInput
        v-model:value="query"
        :disabled="disabled"
        size="small"
        :placeholder="kind === 'groups' ? '搜索员工所在群名称或 ID' : '搜索真人姓名或关键词'"
        @keyup.enter="search()"
      />
      <NButton size="small" type="primary" secondary :disabled="!writable || (kind === 'people' && channel === 'wecom' && !query.trim())" @click="search()">搜索候选</NButton>
    </div>

    <NAlert v-if="kind === 'people' && source?.available === false" type="warning" class="picker-alert">
      此渠道当前不可用：{{ source.unavailable_reason }}
    </NAlert>

    <div v-if="departmentAvailable" class="picker-subbar">
      <div class="actions">
        <NButton size="tiny" secondary :disabled="!writable" @click="refreshDirectory(true)">同步通讯录</NButton>
        <NButton size="tiny" secondary :disabled="!writable" @click="refreshDirectory()">刷新通讯录状态</NButton>
        <NCheckbox v-model:checked="departments" :disabled="!writable">选择部门</NCheckbox>
        <NCheckbox v-if="departments" v-model:checked="descendants" :disabled="!writable">包含子部门</NCheckbox>
      </div>
      <small v-if="directoryStatus" class="picker-note">
        通讯录状态：{{ directoryStatus.status }} · 最近同步 {{ directoryStatus.synced_at || '尚无快照' }} · 有效期至 {{ directoryStatus.expires_at || '未知' }}。同步中只表示请求已受理。
      </small>
    </div>

    <p v-if="kind === 'people' && channel === 'wecom'" class="picker-note">
      企微仅支持搜索选人和绑定验证，不提供部门或完整通讯录查询。验证是管理员转交确认，不是提供方独立身份证明。
    </p>
    <p v-if="kind === 'groups'" class="picker-note">
      按 AI 员工所在或已发现的群查询，不按配置人所在群过滤。取得群元数据不授予读取群消息的权限。钉钉仅覆盖已观察群；企微为已观察或近期群，均不保证完整列表。
    </p>

    <div v-if="kind === 'groups' && ['feishu', 'wecom'].includes(channel)" class="actions picker-subbar">
      <NButton size="tiny" secondary :disabled="!writable" @click="refresh()">{{ channel === 'feishu' ? '刷新飞书群' : refreshJob ? '查询企微刷新状态' : '刷新企微近期群' }}</NButton>
      <NButton v-if="channel === 'feishu' && discoveryCursor" size="tiny" secondary :disabled="!writable" @click="refresh(true)">继续刷新飞书群</NButton>
      <small v-if="feishuGroupSource === 'observed'" class="picker-note">飞书接口暂不可用，显示历史观察到的群，可能不全。</small>
      <small v-if="refreshStatus" class="picker-note">刷新状态：{{ refreshStatus }}</small>
    </div>

    <div v-if="departments && departmentAvailable" class="picker-breadcrumb">
      <small>部门路径：根目录 {{ trail.map(v => v.display_name).join(' / ') }}</small>
      <NButton v-if="trail.length" size="tiny" secondary :disabled="!writable" @click="enterDepartment()">返回上级部门</NButton>
    </div>

    <p v-if="busy" role="status" class="picker-note">正在读取并核验…</p>
    <NAlert v-if="error" type="error" class="picker-alert">{{ error }}</NAlert>
    <p v-if="limited" class="picker-note" style="color: var(--id-status-warning);">提供方仅返回部分结果，请缩小关键词范围。</p>
    <p v-if="loaded && !busy && !visibleRows.length && !error" class="picker-empty">没有匹配的可见候选。</p>

    <ul v-if="visibleRows.length" class="picker-list">
      <li v-for="row in visibleRows" :key="row.search_user_id || selectorKey(row.ref)" class="picker-row">
        <div class="picker-row-info">
          <div class="picker-row-title">
            <D2ChannelLogo :platform="row.ref.provider" :size="15" />
            <strong>{{ row.display_name || row.name || row.ref.external_id }}</strong>
          </div>
          <small>
            {{ labels[row.ref.provider] }} · {{ row.ref.external_id || row.search_user_id }}
            <template v-if="row.evidence"> · {{ row.evidence }}</template>
            <template v-if="row.coverage"> · {{ row.coverage }}</template>
          </small>
          <small v-if="!row.selectable" class="picker-unselectable">{{ row.disabled_reason || '当前不可选' }} {{ row.verification_status }}</small>
        </div>
        <div class="picker-row-actions">
          <NButton v-if="row.ref.kind === 'department'" size="tiny" secondary :disabled="!writable" @click="enterDepartment(row)">查看子部门</NButton>
          <NButton v-if="row.selectable" size="tiny" type="primary" secondary :disabled="!writable" @click="pick(row)">选择</NButton>
          <NButton v-else-if="channel === 'wecom' && kind === 'people' && row.search_user_id" size="tiny" type="warning" secondary :disabled="!writable" @click="verify(row)">绑定验证</NButton>
        </div>
      </li>
    </ul>

    <div v-if="next" class="picker-more">
      <NButton size="small" secondary :disabled="!writable" @click="search(true)">继续读取候选</NButton>
    </div>

    <section v-if="verification" aria-label="企微绑定验证" class="wecom-verify-box">
      <p class="picker-note">验证状态：<strong>{{ verification.status }}</strong> · 有效期至 {{ verification.expires_at }}</p>
      <template v-if="verificationPending && verification.code">
        <NAlert type="warning">核对所选联系人后，将验证码私下交给本人，让本人发给本机器人；不要在群里公开。完成验证不会自动授予权限，需重新选择并保存配置。</NAlert>
        <div class="verify-code-card">
          <code>{{ verification.code }}</code>
        </div>
      </template>
      <div class="actions">
        <NButton size="tiny" secondary :disabled="!writable" @click="verifyStatus()">刷新绑定状态</NButton>
        <NButton v-if="verificationPending" size="tiny" quaternary type="error" :disabled="!writable" @click="verifyStatus(true)">取消绑定验证</NButton>
      </div>
    </section>
  </section>
</template>

<style scoped>
.identity-picker {
  border: 1px solid var(--id-border-subtle);
  border-radius: var(--id-radius-lg);
  background: var(--id-bg-subtle);
  padding: 12px;
  margin: 8px 0;
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.actions {
  display: flex;
  gap: 8px;
  align-items: center;
  flex-wrap: wrap;
}

.actions .n-select {
  width: 112px;
}

.actions .n-input {
  flex: 1;
  min-width: 160px;
}

.picker-subbar {
  display: flex;
  flex-direction: column;
  gap: 6px;
  padding-top: 6px;
  border-top: 1px solid var(--id-border-hairline);
}

.picker-breadcrumb {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  padding: 6px 10px;
  background: var(--id-bg-surface);
  border: 1px solid var(--id-border-subtle);
  border-radius: var(--id-radius-md);
}

.picker-alert {
  margin: 2px 0;
}

.picker-note {
  margin: 0;
  color: var(--id-text-muted);
  font-size: 12px;
  line-height: 1.55;
}

.picker-empty {
  margin: 0;
  padding: 14px;
  text-align: center;
  background: var(--id-bg-surface);
  border: 1px dashed var(--id-border-strong);
  border-radius: var(--id-radius-md);
  color: var(--id-text-muted);
  font-size: 12px;
}

.picker-list {
  list-style: none;
  margin: 0;
  padding: 0;
  max-height: 320px;
  overflow: auto;
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.picker-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  padding: 8px 12px;
  background: var(--id-bg-surface);
  border: 1px solid var(--id-border-subtle);
  border-radius: var(--id-radius-md);
}

.picker-row:hover {
  border-color: var(--id-border-strong);
}

.picker-row-info {
  min-width: 0;
  flex: 1;
}

.picker-row-title {
  display: flex;
  align-items: center;
  gap: 6px;
}

.picker-row-title strong {
  font-size: 13px;
  color: var(--id-text-primary);
}

.picker-row-info small {
  display: block;
  color: var(--id-text-muted);
  font-size: 12px;
  line-height: 1.5;
  overflow-wrap: anywhere;
}

.picker-unselectable {
  color: var(--id-status-warning) !important;
}

.picker-row-actions {
  display: flex;
  gap: 6px;
  flex-shrink: 0;
}

.picker-more {
  display: flex;
  justify-content: center;
}

.wecom-verify-box {
  background: var(--id-bg-surface);
  border: 1px solid var(--id-border-subtle);
  border-radius: var(--id-radius-md);
  padding: 12px;
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.verify-code-card {
  background: var(--id-bg-terminal);
  border: 1px solid var(--id-border-terminal);
  border-radius: var(--id-radius-md);
  padding: 12px;
  text-align: center;
}

.verify-code-card code {
  display: block;
  font-family: var(--id-font-mono);
  font-size: 26px;
  font-weight: 700;
  letter-spacing: 4px;
  color: var(--id-text-inverse);
}
</style>

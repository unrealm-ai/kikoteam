<script setup lang="ts">
import { computed, onUnmounted, ref, watch } from 'vue';
import { NAlert, NButton, NInput, NCheckbox } from 'naive-ui';
import { D2ApiError } from '../api/client';
import { feishuDirectoryApi, type FeishuDirectoryStatus, type FeishuDirectoryUser, type FeishuDirectoryDepartment, type FeishuRules } from '../api/feishu';
const props = defineProps<{ workspace: string; employeeId: string; identity: string; disabled: boolean; access: FeishuRules['access']; provider?: 'feishu'|'dingtalk' }>();
const channelName = computed(() => props.provider === 'dingtalk' ? '钉钉' : '飞书');
const emit = defineEmits<{ 'update:access': [value: FeishuRules['access']]; revoked: [] }>();
type Row = FeishuDirectoryUser | FeishuDirectoryDepartment;
type Selection = 'allow_ids' | 'deny_ids' | 'allow_department_ids' | 'deny_department_ids';
const opened = ref(false), loading = ref(false), syncing = ref(false), error = ref(''), denied = ref(false);
const status = ref<FeishuDirectoryStatus | null>(null), rows = ref<Row[]>([]), cursor = ref(''), query = ref(''), searched = ref('');
const kind = ref<'users' | 'departments'>('users'), names = ref<Record<string, string>>({}), now = ref(Date.now());
const trail = ref<FeishuDirectoryDepartment[]>([]);
let generation = 0, controller = new AbortController();
const timer = setInterval(() => { now.value = Date.now(); }, 1000);
const valid = computed(() => !!status.value && ['ready','syncing','failed'].includes(status.value.status) && status.value.complete && !!status.value.expires_at && Date.parse(status.value.expires_at) > now.value);
watch(valid, value => { if (!value) { rows.value = []; cursor.value = ''; names.value = {}; } });
const labels: Record<string, string> = { never: '尚未同步', syncing: '同步中', ready: '快照已发布', failed: '同步失败', invalidated: '快照已失效' };
const selections: { key: Selection; label: string; type: string }[] = [
  { key: 'allow_ids', label: '允许人员', type: 'users' }, { key: 'deny_ids', label: '拒绝人员', type: 'users' },
  { key: 'allow_department_ids', label: '允许部门', type: 'departments' }, { key: 'deny_department_ids', label: '拒绝部门', type: 'departments' }
];
function reset() { ++generation; controller.abort(); controller = new AbortController(); opened.value = false; loading.value = false; syncing.value = false; status.value = null; rows.value = []; cursor.value = ''; query.value = ''; searched.value = ''; names.value = {}; trail.value = []; error.value = ''; denied.value = false; }
watch(() => [props.workspace, props.employeeId, props.identity, props.provider], reset);
function fail(e: unknown) {
  rows.value = []; cursor.value = ''; names.value = {}; status.value = null;
  error.value = e instanceof Error ? e.message : '读取通讯录失败';
  if (e instanceof D2ApiError && [401, 403].includes(e.status)) { denied.value = true; emit('revoked'); }
}
async function refresh(sync = false) {
  if (props.disabled || loading.value || syncing.value || denied.value) return;
  const g = ++generation; controller.abort(); controller = new AbortController();
  opened.value = true; loading.value = !sync; syncing.value = sync; error.value = ''; rows.value = []; cursor.value = ''; names.value = {};
  try {
    const value = await (sync ? feishuDirectoryApi.sync : feishuDirectoryApi.status)(props.workspace, props.employeeId, controller.signal, props.provider);
    if (g !== generation) return;
    status.value = value; now.value = Date.now();
    if (valid.value) await readPage(false, g);
  } catch (e) { if (g === generation) { fail(e); if (sync) error.value += '；同步可能已受理，请刷新状态核实，不会自动重试。'; } }
  finally { if (g === generation) { loading.value = false; syncing.value = false; } }
}
async function readPage(more: boolean, g: number) {
  const previous = more ? cursor.value : '';
  const result = await feishuDirectoryApi.list(props.workspace, props.employeeId, kind.value, { q: searched.value, cursor: previous, snapshot_version: status.value!.snapshot_version, ...(kind.value === 'departments' ? { parent_id: trail.value.at(-1)?.id || '' } : {}) }, controller.signal, props.provider);
  if (g !== generation) return;
  if (result.snapshot_version !== status.value?.snapshot_version || (previous && result.next_cursor === previous)) throw new Error('通讯录快照或分页发生变化，请刷新通讯录状态后重新查询');
  rows.value = more ? [...rows.value, ...result.items.filter(v => !rows.value.some(r => r.id === v.id))] : result.items;
  for (const row of result.items) names.value[`${kind.value}:${row.id}`] = row.display_name;
  cursor.value = result.next_cursor || '';
}
async function search(more = false) {
  if (props.disabled || !valid.value || loading.value || syncing.value || denied.value) return;
  const g = ++generation; controller.abort(); controller = new AbortController(); loading.value = true; error.value = '';
  if (!more) { searched.value = query.value.trim(); rows.value = []; cursor.value = ''; }
  try { await readPage(more, g); } catch (e) { if (g === generation) fail(e); } finally { if (g === generation) loading.value = false; }
}
function changeKind(value: 'users' | 'departments') { trail.value = []; kind.value = value; query.value = ''; searched.value = ''; rows.value = []; cursor.value = ''; void search(); }
function departmentKey(key: Selection) { return key === 'allow_department_ids' ? 'allow_department_rules' : 'deny_department_rules'; }
function selectedIds(key: Selection) {
  const rules = key.includes('department') ? props.access[departmentKey(key)] : undefined;
  return rules ? rules.map(r => r.department_id) : (props.access[key] || []);
}
function includesDescendants(key: Selection, id: string) { return props.access[departmentKey(key)]?.find(r => r.department_id === id)?.include_descendants ?? false; }
function setDescendants(key: Selection, id: string, value: boolean) {
  if (props.disabled || denied.value || !valid.value || Date.parse(status.value!.expires_at!) <= Date.now()) return;
  const ids = selectedIds(key);
  emit('update:access', { ...props.access, [key]: ids, [departmentKey(key)]: ids.map(department_id => ({ department_id, include_descendants: department_id === id ? value : includesDescendants(key, department_id) })) });
}
function descend(row: Row) {
  if (!('parent_id' in row) || props.disabled || loading.value || syncing.value || !valid.value || trail.value.some(v => v.id === row.id)) return;
  trail.value.push(row); query.value = ''; void search();
}
function ascend(index: number) {
  if (props.disabled || loading.value || syncing.value || !valid.value) return;
  trail.value = trail.value.slice(0, index + 1); query.value = ''; void search();
}
function add(row: Row, allow: boolean) {
  if (props.disabled || !valid.value || Date.parse(status.value!.expires_at!) <= Date.now() || denied.value || (allow && props.access.mode !== 'allowlist') || ('status' in row && row.status !== 'active')) return;
  const key: Selection = kind.value === 'users' ? (allow ? 'allow_ids' : 'deny_ids') : (allow ? 'allow_department_ids' : 'deny_department_ids');
  const ids = [...new Set([...selectedIds(key), row.id])];
  emit('update:access', { ...props.access, [key]: ids, ...(kind.value === 'departments' ? { [departmentKey(key)]: ids.map(department_id => ({ department_id, include_descendants: includesDescendants(key, department_id) })) } : {}) });
}
function remove(key: Selection, id: string) { if (!props.disabled && !denied.value) emit('update:access', { ...props.access, [key]: selectedIds(key).filter(v => v !== id), ...(key.includes('department') ? { [departmentKey(key)]: selectedIds(key).filter(v => v !== id).map(department_id => ({ department_id, include_descendants: includesDescendants(key, department_id) })) } : {}) }); }
onUnmounted(() => { reset(); clearInterval(timer); });
</script>
<template>
  <section class="directory-picker" :aria-label="channelName + '通讯录选择'">
    <header><strong>从{{ channelName }}通讯录选择</strong><NButton :disabled="disabled || loading || syncing || denied" @click="refresh()">{{ opened ? '刷新通讯录状态' : '读取通讯录' }}</NButton></header>
    <p>仅当前员工已保存的{{ channelName }}应用可见范围；选择只修改草稿，保存员工统一配置后才生效。不使用 Web 成员身份。</p>
    <template v-if="opened">
      <p v-if="loading" role="status">正在读取通讯录…</p><p v-if="syncing" role="status">正在请求同步{{ channelName }}通讯录…</p>
      <NAlert v-if="error" type="error">{{ error }}</NAlert>
      <NAlert v-if="denied" type="warning">没有通讯录管理权限，已清除显示的数据。</NAlert>
      <template v-else>
        <p v-if="status">{{ labels[status.status] }} · 快照 v{{ status.snapshot_version }} · {{ status.user_count }} 人 / {{ status.department_count }} 个部门<span v-if="status.synced_at"> · 最近同步 {{ status.synced_at }}</span></p>
        <p v-if="status?.expires_at">有效期至 {{ status.expires_at }}（新快照有效期 24 小时）</p>
        <p v-if="status?.error_code">同步错误：{{ status.error_code }}</p><p v-if="valid && status?.status !== 'ready'">最近一次完整快照仍在有效期内（至 {{ status?.expires_at }}），当前更新尚未成功；权限由服务端重新核验。</p>
        <p v-if="status?.status === 'syncing'">同步请求已受理，请稍后刷新通讯录状态；这不表示同步已成功。</p>
        <NAlert v-if="status && !valid && !syncing" type="warning">当前没有有效完整快照，不能新增通讯录选择。请同步后重试；已有选择保留，不代表当前仍有权限。</NAlert>
        <NButton :disabled="disabled || loading || syncing || status?.status === 'syncing'" @click="refresh(true)">同步{{ channelName }}通讯录</NButton>
        <div v-if="valid" class="directory-search"><NButton :type="kind === 'users' ? 'primary' : 'default'" :disabled="disabled || loading || syncing" @click="changeKind('users')">人员</NButton><NButton :type="kind === 'departments' ? 'primary' : 'default'" :disabled="disabled || loading || syncing" @click="changeKind('departments')">部门</NButton><NInput v-model:value="query" placeholder="按名称或 ID 搜索通讯录" :disabled="disabled || loading || syncing" @keyup.enter="search()" /><NButton :disabled="disabled || loading || syncing" @click="search()">查询通讯录</NButton></div>
        <nav v-if="valid && kind === 'departments'" aria-label="部门路径" class="directory-search"><NButton size="small" :disabled="disabled || loading || syncing" @click="ascend(-1)">可见范围根部门</NButton><template v-for="(item,index) in trail" :key="item.id"><span>/</span><NButton size="small" :disabled="disabled || loading || syncing" @click="ascend(index)">{{ item.display_name || item.id }}</NButton></template></nav>
        <p v-if="valid && kind === 'departments'">{{ trail.length ? '当前仅显示此部门的直接子部门；搜索也限定在此层。' : (searched ? '按名称或 ID 在全部可见部门中搜索；可进入匹配部门查看子部门。' : '显示当前可见范围根部门；可逐层查看子部门，搜索覆盖全部可见部门。') }}</p>
        <p v-if="valid && !loading && !syncing && !error && !rows.length">未找到匹配的人员或部门。</p>
        <ul v-if="valid && !error" class="directory-results"><li v-for="row in rows" :key="row.id"><div><strong>{{ row.display_name || row.id }}</strong><small>{{ row.id }}<span v-if="'status' in row && row.status !== 'active'"> · 非有效在职状态</span></small></div><div class="directory-actions"><NButton v-if="'parent_id' in row" size="small" :disabled="disabled || loading || syncing || trail.some(v => v.id === row.id)" @click="descend(row)">查看子部门</NButton><NButton size="small" :disabled="disabled || loading || syncing || access.mode !== 'allowlist' || ('status' in row && row.status !== 'active')" @click="add(row,true)">加入允许</NButton><NButton size="small" :disabled="disabled || loading || syncing || ('status' in row && row.status !== 'active')" @click="add(row,false)">加入拒绝</NButton></div></li></ul>
        <NButton v-if="cursor && valid" :disabled="disabled || loading || syncing" @click="search(true)">加载更多通讯录</NButton>
      </template>
    </template>
    <div v-for="selection in selections.filter(s => selectedIds(s.key).length)" :key="selection.key" class="directory-selected"><strong>{{ selection.label }}</strong><div v-for="id in selectedIds(selection.key)" :key="id"><span>{{ names[`${selection.type}:${id}`] || '未解析对象' }} · {{ id }}</span><NCheckbox v-if="selection.type === 'departments'" :checked="includesDescendants(selection.key,id)" :disabled="disabled || denied || !valid" @update:checked="value => setDescendants(selection.key,id,value)">包含子部门</NCheckbox><NButton size="tiny" :disabled="disabled || denied" @click="remove(selection.key,id)">移除</NButton></div></div>
    <p>黑名单优先。每条部门规则默认仅包含直接成员；勾选“包含子部门”后覆盖该部门及其后代部门。已有选择与包含子部门设置不会因快照失效而删除，未解析不代表当前可用。服务端依据有效完整快照判断，快照失效或关系不明时不会自动放宽权限。</p>
  </section>
</template>
<style scoped>
.directory-picker{border-top:1px solid var(--id-border-subtle);padding-top:16px;margin-top:16px}header,.directory-search,.directory-actions,.directory-results li,.directory-selected>div{display:flex;gap:10px;align-items:center;justify-content:space-between}.directory-search{margin-top:12px}.directory-search .n-select{max-width:100px}.directory-results{list-style:none;padding:0;max-height:320px;overflow:auto}.directory-results li{padding:10px 0;border-bottom:1px solid var(--id-border-subtle)}small{display:block;color:var(--id-text-muted);overflow-wrap:anywhere}.directory-selected{margin-top:12px}.directory-selected>div{margin-top:6px;overflow-wrap:anywhere}p{font-size:12px;color:var(--id-text-muted);line-height:1.7}@media(max-width:600px){.directory-results li{align-items:flex-start;flex-direction:column}.directory-search{flex-wrap:wrap}.directory-search .n-input{min-width:150px;flex:1}}
</style>

<script setup lang="ts">
import { computed, onUnmounted, ref, watch } from 'vue';
import { NAlert, NButton, NEmpty, NInput } from 'naive-ui';
import { allPages, workspacePath, type Page } from '../api/capabilities';
import { platformRequest } from '../api/platform';
import { feishuDirectoryApi, type FeishuDirectoryStatus, type FeishuDirectoryUser, type FeishuDirectoryDepartment } from '../api/feishu';

const props = defineProps<{
  workspace: string; employeeId: string; provider: 'web'|'feishu'|'dingtalk'; snapshot?: FeishuDirectoryStatus | undefined
}>();
interface Row { id: string; name: string; detail: string }
interface Member { id: string; name: string; email: string; status: string }
const kind = ref<'users'|'departments'>('users');
const query = ref(''), search = ref('');
const trail = ref<Row[]>([]);
const rows = ref<Row[]>([]), cursors = ref<string[]>(['']), next = ref(''), page = ref(0);
const loading = ref(false), error = ref('');
const clock = ref(Date.now());
const timer = setInterval(() => { clock.value = Date.now(); }, 1000);
const valid = computed(() => props.provider === 'web' || (!!props.snapshot?.complete &&
  (!props.snapshot.expires_at || Date.parse(props.snapshot.expires_at) > clock.value)));
let request: AbortController | undefined;
let debounce: ReturnType<typeof setTimeout> | undefined;
// The workspace member endpoint supports numbered pages and has no search parameter.
let webMatches: {workspace: string; term: string; items: Member[]} | undefined;
async function webPage(signal: AbortSignal) {
  const { workspace } = props;
  const term = search.value.toLocaleLowerCase();
  const endpoint = workspacePath(workspace) + '/members';
  const read = (n: number) => platformRequest<Page<Member>>(endpoint + '?page=' + n + '&page_size=20', {signal});
  if (!term) {
    webMatches = undefined;
    return read(page.value + 1);
  }
  if (webMatches?.workspace !== workspace || webMatches.term !== term) {
    const members = await allPages(read);
    signal.throwIfAborted();
    webMatches = {workspace, term, items: members.filter(m =>
      [m.name, m.email].some(value => value.toLocaleLowerCase().includes(term)))};
  }
  return {items:webMatches.items.slice(page.value * 20, (page.value + 1) * 20), total:webMatches.items.length};
}
async function load() {
  request?.abort();
  const controller = new AbortController();
  request = controller;
  const { signal } = controller;
  rows.value = []; next.value = ''; error.value = ''; loading.value = false;
  if (!valid.value) return;
  loading.value = true;
  try {
    if (props.provider === 'web') {
      const result = await webPage(signal);
      if (signal.aborted) return;
      rows.value = result.items.map(m => ({id:m.id, name:m.name, detail:m.email}));
      if ((page.value + 1) * 20 < result.total) next.value = String(page.value + 1);
    } else {
      const version = props.snapshot!.snapshot_version;
      const department = trail.value.at(-1)?.id;
      const result = await feishuDirectoryApi.list(props.workspace, props.employeeId, kind.value, {
        q: search.value, cursor: cursors.value[page.value] || '', snapshot_version: version,
        ...(department ? kind.value === 'departments' ? {parent_id:department} : {department_id:department} : {})
      }, signal, props.provider);
      if (signal.aborted) return;
      if (result.snapshot_version !== version || (result.next_cursor && cursors.value.slice(0, page.value+1).includes(result.next_cursor))) {
        throw new Error('通讯录快照或分页已变化，请刷新来源后重试。');
      }
      rows.value = result.items.map((r:FeishuDirectoryUser | FeishuDirectoryDepartment) => ({
        id:r.id, name:r.display_name || r.id, detail:'department_ids' in r ? r.department_ids.join('、') : r.id
      }));
      next.value = result.next_cursor || '';
    }
  } catch (e) {
    if (!signal.aborted) error.value = e instanceof Error ? e.message : '通讯录读取失败';
  } finally { if (!signal.aborted) loading.value = false; }
}
function resetPages() { cursors.value = ['']; page.value = 0; void load(); }
function forward() {
  if (!next.value) return;
  cursors.value[page.value+1] = next.value; page.value++; void load();
}
function setDepartment(path:Row[], tab:'users'|'departments') {
  trail.value = path; kind.value = tab; query.value = ''; search.value = '';
}
watch(query, value => { clearTimeout(debounce); debounce = setTimeout(() => { search.value = value.trim(); }, 250); });
watch([kind, search, trail, valid, () => props.snapshot?.snapshot_version, () => props.workspace], resetPages, {immediate:true});
onUnmounted(() => { clearTimeout(debounce); clearInterval(timer); request?.abort(); });
</script>

<template>
  <div class="directory-table-container">
    <div class="table-search-bar">
      <div v-if="provider !== 'web'" class="table-action-btns">
        <NButton size="small" :type="kind === 'users' ? 'primary' : 'default'" @click="kind = 'users'">成员</NButton>
        <NButton size="small" :type="kind === 'departments' ? 'primary' : 'default'" @click="kind = 'departments'">部门</NButton>
      </div>
      <NInput v-model:value="query" class="dir-search-input" clearable size="small" :placeholder="provider === 'web' ? '搜索成员姓名、邮箱…' : kind === 'users' ? '搜索成员姓名…' : '搜索部门名称…'" />
    </div>
    <nav v-if="trail.length" class="table-action-btns" aria-label="部门路径">
      <NButton text @click="setDepartment([], 'departments')">全部部门</NButton>
      <NButton v-for="(d,i) in trail" :key="d.id" text @click="setDepartment(trail.slice(0,i+1), 'departments')">{{ d.name }}</NButton>
    </nav>
    <NAlert v-if="!valid" type="info">当前来源暂无有效通讯录快照。</NAlert>
    <NAlert v-else-if="error" type="error">{{ error }} <NButton size="tiny" @click="resetPages">重试</NButton></NAlert>
    <p v-else-if="loading" role="status">正在读取通讯录…</p>
    <div v-else class="table-scroll-wrap">
      <table class="custom-d2-table">
        <thead><tr><th>{{ kind === 'users' ? '成员姓名' : '部门名称' }}</th>
          <th>{{ provider === 'web' ? '邮箱' : kind === 'users' ? '所属部门 ID' : '部门 ID' }}</th>
          <th v-if="kind === 'departments'">操作</th></tr></thead>
        <tbody><tr v-if="!rows.length"><td :colspan="kind === 'departments' ? 3 : 2"><NEmpty description="未找到匹配记录" /></td></tr>
          <tr v-for="row in rows" :key="row.id"><td><div class="user-cell"><strong class="member-strong">{{ row.name }}</strong></div></td>
            <td>{{ row.detail || '—' }}</td><td v-if="kind === 'departments'" class="table-action-btns">
              <NButton size="tiny" @click="setDepartment([...trail,row], 'departments')">子部门</NButton>
              <NButton size="tiny" @click="setDepartment([...trail,row], 'users')">部门成员</NButton></td></tr>
        </tbody>
      </table>
    </div>
    <div v-if="valid && (page || next)" class="dir-pagination-bar">
      <span>第 {{ page + 1 }} 页</span><div class="table-action-btns">
        <NButton size="tiny" :disabled="!page || loading" @click="page--; load()">上一页</NButton>
        <NButton size="tiny" :disabled="!next || loading" @click="forward">下一页</NButton>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, onUnmounted, ref, watch } from 'vue';
import { NAlert, NButton, NEmpty, NTab, NTabs, useMessage } from 'naive-ui';
import D2ChannelLogo from '../components/D2ChannelLogo.vue';
import { activeWorkspaceId, D2ApiError } from '../api/client';
import { platformRequest } from '../api/platform';
import { agentsApi } from '../api/agents';
import { allPages, workspacePath } from '../api/capabilities';
import { channelsApi, channelLabels, type ChannelProvider } from '../api/channels';
import { feishuDirectoryApi, type FeishuBinding, type FeishuDirectoryStatus } from '../api/feishu';
import DirectoryMembers from './ResourceDirectoryMembers.vue';
import PlatformWecomContacts from './PlatformWecomContacts.vue';

type Provider = ChannelProvider | 'web';
interface Source {
  key: string; provider: Provider; employeeId: string; employeeName: string;
  binding?: FeishuBinding; snapshot?: FeishuDirectoryStatus; error?: string; pollError?: string;
}
const labels = { ...channelLabels, web: '工作区网页' };
const states = { never: '尚未同步', syncing: '同步中', ready: '已同步', failed: '同步失败', invalidated: '快照已失效' };
const providers: Provider[] = ['feishu', 'dingtalk', 'wecom', 'web'];
const sources = ref<Source[]>([]);
const filter = ref<Provider | 'all'>('all');
const selected = ref('');
const loading = ref(false);
const error = ref('');
const submitting = ref('');
const message = useMessage();
const visible = computed(() => sources.value.filter(s => filter.value === 'all' || s.provider === filter.value));
const current = computed(() => visible.value.find(s => s.key === selected.value));
let request: AbortController | undefined;
let poll: ReturnType<typeof setTimeout> | undefined;
function selectDefault() {
  const available = visible.value.filter(s => !s.error);
  selected.value = available.length === 1 ? available[0]!.key : '';
}
watch(filter, selectDefault);
async function load() {
  const prior = current.value;
  request?.abort();
  clearTimeout(poll);
  const workspace = activeWorkspaceId.value;
  const controller = new AbortController();
  request = controller;
  error.value = ''; loading.value = true; sources.value = []; selected.value = ''; submitting.value = '';
  if (!workspace) { loading.value = false; return; }
  const signal = controller.signal;
  try {
    const info = await platformRequest<{role:string}>(workspacePath(workspace), {signal});
    if (signal.aborted) return;
    const catalog: Source[] = [];
    if (info.role === 'owner' || info.role === 'admin') {
      const agents = await allPages(page => agentsApi.list(workspace, page, signal));
      const channels = agents.flatMap(a => a.card_resources.channels.map(provider =>
        ({key: a.id + '/' + provider, provider, employeeId: a.id, employeeName: a.name})));
      // Bound parallel reads across large employee directories.
      for (let start = 0; start < channels.length && !signal.aborted; start += 6) {
        const batch = await Promise.all(channels.slice(start, start + 6).map(async (s): Promise<Source> => {
          try {
            const binding = await channelsApi.get(workspace, s.employeeId, s.provider, signal);
            return {...s, binding, ...(binding.directory_status ? {snapshot: binding.directory_status} : {})};
          } catch (e) {
            return {...s, error: e instanceof Error ? e.message : '来源读取失败'};
          }
        }));
        catalog.push(...batch);
      }
    }
    if (signal.aborted) return;
    sources.value = [...catalog, {key: 'web', provider: 'web', employeeId: '', employeeName: ''}];
    const same = visible.value.find(s => !s.error && s.key === prior?.key && s.binding?.id === prior.binding?.id);
    if (same) selected.value = same.key;
    else selectDefault();
    schedulePoll();
  } catch (e) {
    if (!signal.aborted) error.value = e instanceof Error ? e.message : '读取通讯录来源失败';
  } finally {
    if (!signal.aborted) loading.value = false;
  }
}
function schedulePoll() {
  clearTimeout(poll);
  if (sources.value.some(s => s.snapshot?.status === 'syncing' && !s.error)) {
    poll = setTimeout(async () => {
      const signal = request?.signal;
      if (!signal || signal.aborted) return;
      const workspace = activeWorkspaceId.value;
      for (const s of sources.value.filter(row => row.snapshot?.status === 'syncing' && !row.error)) {
        if (signal.aborted) return;
        if (s.provider === 'wecom' || s.provider === 'web') continue;
        try {
          const status = await feishuDirectoryApi.status(workspace, s.employeeId, signal, s.provider);
          if (!signal.aborted && workspace === activeWorkspaceId.value) { s.snapshot = status; delete s.pollError; }
        } catch (e) {
          if (signal.aborted || workspace !== activeWorkspaceId.value) return;
          const detail = e instanceof Error ? e.message : '刷新同步状态失败';
          if (e instanceof TypeError || e instanceof DOMException && e.name === 'TimeoutError' ||
              e instanceof D2ApiError && (e.status >= 500 || e.status === 408 || e.status === 429)) {
            s.pollError = detail;
          } else {
            delete s.snapshot; delete s.pollError; s.error = detail;
            if (e instanceof D2ApiError && [401, 403].includes(e.status)) {
              // A status denial may be workspace-wide; recheck all visible sources.
              void load();
              return;
            }
          }
        }
      }
      if (!signal.aborted) schedulePoll();
    }, 2500);
  }
}
async function sync(s: Source) {
  const signal = request?.signal;
  if (!signal || signal.aborted || submitting.value || !s.binding?.enabled || s.provider === 'wecom' || s.provider === 'web') return;
  submitting.value = s.key;
  try {
    const status = await feishuDirectoryApi.sync(activeWorkspaceId.value, s.employeeId, signal, s.provider);
    if (!signal.aborted) { s.snapshot = status; delete s.pollError; message.success('已提交该来源的同步请求'); schedulePoll(); }
  } catch (e) {
    if (!signal.aborted) message.error(e instanceof Error ? e.message : '提交同步失败');
  } finally { if (!signal.aborted) submitting.value = ''; }
}
watch(activeWorkspaceId, () => { filter.value = 'all'; selected.value = ''; void load(); }, {immediate:true});
onUnmounted(() => { request?.abort(); clearTimeout(poll); });
</script>

<template>
  <section class="tab-content-panel">
    <div class="directory-sync-banner">
      <div class="sync-info"><strong>渠道通讯录与成员核验</strong><p>选择已绑定的应用查看通讯录快照，或搜索指定企微机器人的联系人。</p></div>
      <NButton size="small" secondary :loading="loading" @click="load">刷新来源</NButton>
    </div>
    <div class="source-snapshot-section">
      <div class="section-title-bar">
        <span class="sec-title">通讯录来源</span>
        <NTabs v-model:value="filter" type="segment" size="small" class="source-filter-tabs">
          <NTab name="all">全部渠道</NTab><NTab v-for="provider in providers" :key="provider" :name="provider">{{ labels[provider] }}</NTab>
        </NTabs>
      </div>
      <p v-if="loading" role="status">正在读取通讯录来源…</p>
      <NAlert v-else-if="error" type="error">{{ error }}</NAlert>
      <div v-else-if="visible.length" class="sync-sources-grid">
        <article v-for="s in visible" :key="s.key" class="sync-source-card" :class="{selected: s.key === selected}">
          <button class="source-choice" :aria-pressed="s.key === selected" @click="selected = s.key">
            <span class="ssc-top"><strong class="ssc-title">{{ s.employeeName || labels[s.provider] }}</strong><D2ChannelLogo :platform="s.provider" :size="18" /></span>
            <span class="ssc-id">{{ labels[s.provider] }}<template v-if="s.binding"> · {{ s.binding.app_id }}</template></span>
            <span v-if="s.binding && !s.binding.enabled" class="field-note">绑定已停用</span>
            <span v-if="s.snapshot" class="ssc-snapshot-info">
              <span class="ssc-row"><span class="lbl">同步状态</span>{{ states[s.snapshot.status] }}</span>
              <span class="ssc-row"><span class="lbl">最近同步</span>{{ s.snapshot.synced_at ? new Date(s.snapshot.synced_at).toLocaleString() : '—' }}</span>
              <span v-if="s.snapshot.complete" class="ssc-row"><span class="lbl">组织规模</span>{{ s.snapshot.department_count }} 部门 · {{ s.snapshot.user_count }} 成员</span>
            </span>
            <span v-else-if="s.provider === 'wecom'" class="ssc-row">关键词搜索与验证码核验</span>
            <span v-if="s.pollError" class="field-note" role="status">暂时无法刷新同步状态，将继续重试：{{ s.pollError }}</span>
            <span v-if="s.error || s.snapshot?.error_code" class="field-note">{{ s.error || s.snapshot?.error_code }}</span>
          </button>
          <div v-if="s.provider === 'feishu' || s.provider === 'dingtalk'" class="ssc-footer">
            <NButton size="tiny" secondary :loading="submitting === s.key" :disabled="!!submitting || !s.binding?.enabled || s.snapshot?.status === 'syncing'" @click="sync(s)">立即同步该来源</NButton>
          </div>
        </article>
      </div>
      <NEmpty v-else description="没有可查看的通讯录来源" />
    </div>
    <template v-if="current && !current.error && current.snapshot?.status !== 'invalidated'">
      <PlatformWecomContacts v-if="current.provider === 'wecom' && current.binding" :key="current.key"
        directory-view :workspace="activeWorkspaceId" :employee-id="current.employeeId" :identity="current.binding.id + ':' + current.binding.connection_generation"
        :disabled="!current.binding.enabled" :confirmed="!!current.binding.identity_confirmed && current.binding.identity_verified_generation === current.binding.connection_generation" @revoked="load" />
      <DirectoryMembers v-else-if="current.provider !== 'wecom'" :key="current.key" :workspace="activeWorkspaceId"
        :employee-id="current.employeeId" :provider="current.provider" :snapshot="current.snapshot" />
    </template>
    <p v-else-if="!loading && !error && visible.length > 1" class="field-note">选择上方来源查看成员。</p>
  </section>
</template>

<style scoped>
.source-choice { display:grid; gap:8px; width:100%; cursor:pointer; text-align:left; background:transparent; border:0; font:inherit; color:inherit; padding:0; }
.source-choice .ssc-id { overflow-wrap:anywhere; }
.sync-source-card.selected { border-color:var(--id-accent-primary); }
</style>

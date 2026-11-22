<script setup lang="ts">
import { computed, inject, onUnmounted, ref, watch } from 'vue';
import { NAlert, NButton, NInput, NSelect, NSkeleton, NSwitch, useDialog } from 'naive-ui';
import PlatformIdentityPicker from '../components/PlatformIdentityPicker.vue';
import D2ChannelLogo from '../components/D2ChannelLogo.vue';
import { sharingApi, type SharingDraft, type SharingPolicy, type SharingEdge } from '../api/sharing';
import { selectorsApi, selectorKey, type SelectionRef, type SelectionCandidate } from '../api/selectors';
import { D2ApiError } from '../api/client';

const props = defineProps<{ workspace: string; employeeId: string; canManage: boolean }>();
const emit = defineEmits<{ close: [] }>(), dialog = useDialog();
const rows = ref<SharingPolicy[]>([]), total = ref(0), page = ref(1), query = ref(''), appliedQuery = ref('');
const draft = ref<SharingDraft>(), editing = ref<SharingPolicy>(), baseline = ref(''), side = ref<'sources' | 'targets'>('sources');
const uncertain = ref(false);
const error = ref(''), notice = ref(''), loading = ref(false), busy = ref(false), denied = ref(false), edges = ref<SharingEdge[]>(), names = ref<SelectionCandidate[]>([]), nameError = ref('');
let generation = 0, nameGeneration = 0, previewGeneration = 0, controller = new AbortController();
const writable = computed(() => props.canManage && !denied.value && !busy.value && !loading.value && !uncertain.value);
const dirty = computed(() => !!draft.value && JSON.stringify(draft.value) !== baseline.value);
const historyLabels: Record<string, string> = {
  local_history: 'Web 本地历史',
  provider_history: '飞书历史接口（外部权限未验证）',
  recent_window: '企微近 7 天历史（外部权限未验证）',
  unsupported: '当前渠道不支持历史读取'
};
const valid = computed(() => !!draft.value && !!draft.value.name.trim() && draft.value.sources.length >= (draft.value.mode === 'mutual' ? 2 : 1) && (draft.value.mode === 'mutual' ? draft.value.targets.length === 0 : draft.value.targets.length > 0));
const labels = { web: 'Web', feishu: '飞书', dingtalk: '钉钉', wecom: '企微' };
const selectedKey = (r: SelectionRef) => selectorKey(r);
function name(r: SelectionRef) { const row = names.value.find(v => selectedKey(v.ref) === selectedKey(r)); return row?.name || row?.display_name || r.external_id; }
function status(r: SelectionRef) { const row = names.value.find(v => selectedKey(v.ref) === selectedKey(r)); return row ? (row.selectable ? '候选可用，读取时另行核验' : row.disabled_reason || '当前不可用') : '未解析，原选择保留'; }
function failure(e: unknown) {
  error.value = e instanceof Error ? e.message : '请求失败';
  if (e instanceof D2ApiError && [401, 403].includes(e.status)) { denied.value = true; rows.value = []; draft.value = undefined; edges.value = undefined; }
}
async function list() {
  const g = generation; loading.value = true; error.value = '';
  try {
    const r = await sharingApi.list(props.workspace, props.employeeId, appliedQuery.value, page.value, controller.signal);
    if (g !== generation) return;
    rows.value = r.items; total.value = r.total;
  } catch (e) { if (g === generation) failure(e); }
  finally { if (g === generation) loading.value = false; }
}
function confirm(action: () => void) {
  const g = generation; if (busy.value) return;
  if (!dirty.value) { action(); return; }
  dialog.warning({
    title: '群共享策略尚未保存',
    content: '继续将放弃当前策略草稿。',
    positiveText: '放弃修改',
    negativeText: '继续编辑',
    onPositiveClick: () => { if (g === generation && !busy.value) action(); }
  });
}
function edit(policy?: SharingPolicy) {
  confirm(() => {
    editing.value = policy;
    draft.value = policy ? JSON.parse(JSON.stringify({ name: policy.name, mode: policy.mode, enabled: policy.enabled, sources: policy.sources, targets: policy.targets })) : { name: '', mode: 'directed', enabled: true, sources: [], targets: [] };
    baseline.value = JSON.stringify(draft.value); side.value = 'sources'; error.value = ''; notice.value = ''; edges.value = undefined;
  });
}
function changeMode(mode: 'directed' | 'mutual') {
  if (!writable.value || !draft.value) return;
  if (mode === 'mutual' && draft.value.targets.length) {
    error.value = '请先移除全部目标。项目组仅将来源群两两互通，不会自动把指定人或目标群扩大为双向共享。';
    return;
  }
  draft.value.mode = mode; side.value = 'sources';
}
function pick(candidate: SelectionCandidate) {
  if (!writable.value || !draft.value || !candidate.selectable) return;
  const r = candidate.ref;
  if (r.kind === 'department' || (side.value === 'sources' && r.kind !== 'group') || (draft.value.mode === 'mutual' && side.value !== 'sources')) return;
  const listArr = draft.value[side.value];
  if (!listArr.some(v => selectedKey(v) === selectedKey(r))) listArr.push(r);
}
async function preview() {
  if (!writable.value || !draft.value) return;
  const g = generation, p = ++previewGeneration; busy.value = true; error.value = '';
  try {
    const r = await sharingApi.preview(props.workspace, props.employeeId, JSON.parse(JSON.stringify(draft.value)), controller.signal);
    if (g === generation && p === previewGeneration) edges.value = r.edges;
  } catch (e) { if (g === generation) failure(e); }
  finally { if (g === generation) busy.value = false; }
}
async function save() {
  if (!writable.value || !draft.value || !valid.value) return false;
  const g = generation; busy.value = true; error.value = '';
  try {
    const r = await sharingApi.save(props.workspace, props.employeeId, JSON.parse(JSON.stringify(draft.value)), editing.value?.id, editing.value?.version, controller.signal);
    if (g !== generation) return false;
    editing.value = r; baseline.value = JSON.stringify(draft.value); notice.value = '策略已保存；实际读取仍核验当前身份、方向与渠道能力。';
    await list(); return true;
  } catch (e) {
    if (g === generation) {
      failure(e);
      if (!(e instanceof D2ApiError) || e.status >= 500) {
        uncertain.value = true;
        error.value += '；保存结果未知，请重新读取策略列表核对，不要重复创建。';
      }
    }
    return false;
  } finally { if (g === generation) busy.value = false; }
}
function remove(policy: SharingPolicy) {
  const opened = generation; if (!writable.value) return;
  dialog.warning({
    title: '删除群共享策略',
    content: `删除“${policy.name}”将撤销本策略授权，不影响其他独立策略。`,
    positiveText: '删除策略',
    negativeText: '取消',
    onPositiveClick: async () => {
      if (opened !== generation || !writable.value) return;
      const g = generation; busy.value = true; error.value = '';
      try {
        await sharingApi.remove(props.workspace, props.employeeId, policy.id, policy.version, controller.signal);
        if (g !== generation) return;
        if (editing.value?.id === policy.id) { draft.value = undefined; editing.value = undefined; }
        notice.value = '策略已删除。'; await list();
      } catch (e) { if (g === generation) failure(e); }
      finally { if (g === generation) busy.value = false; }
    }
  });
}
watch(() => JSON.stringify(draft.value), async () => {
  edges.value = undefined; ++previewGeneration;
  const n = ++nameGeneration; names.value = []; nameError.value = '';
  if (!draft.value) return;
  const all = [...draft.value.sources, ...draft.value.targets];
  await Promise.all((['groups', 'people'] as const).map(async kind => {
    const refs = all.filter(v => kind === 'groups' ? v.kind === 'group' : v.kind !== 'group');
    if (!refs.length) return;
    try {
      const r = await selectorsApi.resolve(props.workspace, props.employeeId, kind, refs, 'sharing', controller.signal);
      if (n === nameGeneration) names.value.push(...r.items);
    } catch (e) {
      if (n === nameGeneration) nameError.value = '名称暂不可解析，原有选择未删除；' + (e instanceof Error ? e.message : '读取失败');
    }
  }));
});
watch(() => [props.workspace, props.employeeId, props.canManage], () => {
  ++generation; ++nameGeneration; controller.abort(); controller = new AbortController();
  draft.value = undefined; editing.value = undefined; rows.value = []; names.value = []; edges.value = undefined;
  busy.value = false; uncertain.value = false; loading.value = false; page.value = 1; query.value = ''; appliedQuery.value = '';
  error.value = ''; notice.value = ''; denied.value = !props.canManage;
  if (props.canManage) void list();
}, { immediate: true });
const guard = inject<{ register: (v: unknown) => void; unregister: (v: unknown) => void }>('d2DraftGuard');
const draftGuard = { hasUnsaved: () => dirty.value || busy.value, getLabel: () => '群共享策略', save, discard: () => emit('close') };
guard?.register(draftGuard);
onUnmounted(() => { ++generation; ++nameGeneration; controller.abort(); guard?.unregister(draftGuard); });
const sides = ['sources', 'targets'] as const;
</script>

<template>
  <section class="sharing">
    <header class="sharing-header">
      <div>
        <h2>群共享策略</h2>
        <p class="field-note">策略独立、不传递，不合并对话或自动转发消息。选择群仅表示员工关联群可被识别，不等于可以读取其历史。</p>
      </div>
      <NButton size="small" secondary :disabled="busy" @click="confirm(() => emit('close'))">返回员工列表</NButton>
    </header>

    <NAlert v-if="error" type="error">{{ error }}</NAlert>
    <NAlert v-if="notice" type="success">{{ notice }}</NAlert>
    <NAlert v-if="denied" type="warning">当前权限不足，策略内容已隐藏。</NAlert>

    <template v-else>
      <div v-if="uncertain">
        <NButton size="small" type="warning" :disabled="busy || loading" @click="confirm(() => { draft = undefined; editing = undefined; uncertain = false; void list(); })">重新读取并核对策略</NButton>
      </div>

      <div class="panel-desc-bar">
        <NInput v-model:value="query" size="small" placeholder="搜索策略名称" :disabled="busy" style="flex: 1; min-width: 180px;" />
        <div class="btn-group">
          <NButton size="small" secondary :disabled="busy || loading" @click="confirm(() => { appliedQuery = query; page = 1; void list(); })">搜索策略</NButton>
          <NButton size="small" type="primary" :disabled="!writable" @click="edit()">新增策略</NButton>
        </div>
      </div>

      <NSkeleton v-if="loading" text :repeat="3" />
      <div v-else-if="!rows.length && !error" class="empty-channel-box">
        <p>暂无策略</p>
      </div>
      <div v-else class="sharing-list">
        <article v-for="policy in rows" :key="policy.id" class="ch-section sharing-policy-card">
          <div class="cs-header-flex">
            <div class="policy-title-group">
              <strong>{{ policy.name }}</strong>
              <span class="policy-mode-pill">{{ policy.mode === 'mutual' ? '项目组' : '信息同步' }}</span>
              <span class="wf-status-pill" :class="policy.enabled ? 'is-success' : ''">
                <span class="wf-status-dot" />
                {{ policy.enabled ? '已启用' : '已停用' }}
              </span>
              <span class="policy-ver-pill tabular-num">v{{ policy.version }}</span>
            </div>
            <div class="btn-group">
              <NButton size="tiny" secondary :disabled="!writable" @click="edit(policy)">编辑策略</NButton>
              <NButton size="tiny" quaternary type="error" :disabled="!writable" @click="remove(policy)">删除策略</NButton>
            </div>
          </div>
        </article>
      </div>

      <div class="sharing-pagination">
        <NButton size="small" secondary :disabled="busy || loading || page <= 1" @click="confirm(() => { page--; void list(); })">上一页策略</NButton>
        <NButton size="small" secondary :disabled="busy || loading || page * 20 >= total" @click="confirm(() => { page++; void list(); })">下一页策略</NButton>
      </div>

      <fieldset v-if="draft" class="ch-section sharing-editor">
        <legend class="fs-legend">{{ editing ? '编辑策略' : '新策略' }}</legend>
        <NInput v-model:value="draft.name" placeholder="策略名称" :maxlength="100" :disabled="!writable" />
        <NSelect
          :virtual-scroll="false"
          :value="draft.mode"
          :disabled="!writable"
          :options="[{ label: '信息同步 · 来源到目标单向', value: 'directed' }, { label: '项目组 · 所选群两两双向', value: 'mutual' }]"
          @update:value="changeMode"
        />
        <div class="p-row">
          <div class="p-info">
            <strong>启用策略</strong>
            <p>{{ draft.mode === 'directed' ? '目标群或指定人可以读取所选来源群，不授予反向访问。' : '来源中至少选择两个群，群间两两互通；暂不支持指定人目标。' }}</p>
          </div>
          <NSwitch v-model:value="draft.enabled" :disabled="!writable" />
        </div>

        <NSelect
          :virtual-scroll="false"
          v-if="draft.mode === 'directed'"
          v-model:value="side"
          :disabled="!writable"
          :options="[{ label: '添加来源群', value: 'sources' }, { label: '添加目标群或指定人', value: 'targets' }]"
        />
        <PlatformIdentityPicker
          :key="`group:${side}:${draft.mode}`"
          :workspace="workspace"
          :employee-id="employeeId"
          kind="groups"
          purpose="sharing"
          :disabled="!writable || draft[side].length >= 20"
          @pick="pick"
          @revoked="denied = true"
        />
        <PlatformIdentityPicker
          v-if="side === 'targets' && draft.mode === 'directed'"
          key="people"
          :workspace="workspace"
          :employee-id="employeeId"
          kind="people"
          purpose="sharing"
          :disabled="!writable || draft.targets.length >= 20"
          @pick="pick"
          @revoked="denied = true"
        />
        <NAlert v-if="nameError" type="warning">{{ nameError }}</NAlert>

        <template v-for="slot in sides" :key="slot">
          <section v-if="slot === 'sources' || draft.mode === 'directed'" class="flow-section">
            <h3 class="fs-title">{{ slot === 'sources' ? '来源群' : '目标群 / 指定人' }}</h3>
            <article v-for="(r, i) in draft[slot]" :key="selectedKey(r)" class="subj-card">
              <div class="subj-main">
                <div class="subj-title">
                  <D2ChannelLogo :platform="r.provider" :size="14" />
                  <span>{{ labels[r.provider] }} · {{ name(r) }}</span>
                </div>
                <small>{{ r.external_id }} · {{ status(r) }}</small>
              </div>
              <NButton size="tiny" quaternary type="error" :disabled="!writable" @click="draft[slot].splice(i, 1)">移除选择</NButton>
            </article>
          </section>
        </template>

        <div class="btn-group">
          <NButton size="small" secondary :disabled="!writable" @click="preview">预览方向与读取能力</NButton>
          <NButton size="small" type="primary" :disabled="!writable || !dirty || !valid" @click="save">保存策略</NButton>
          <NButton size="small" :disabled="busy" @click="confirm(() => { draft = undefined; editing = undefined; })">关闭编辑</NButton>
        </div>

        <section v-if="edges" class="flow-section preview-edges-box">
          <h3 class="fs-title">本策略直接授权方向</h3>
          <p class="field-note">预览不读取消息，不保存策略；执行时仍会重新校验。</p>
          <article v-for="(edge, i) in edges" :key="i" class="subj-card">
            <div class="subj-main">
              <span class="edge-route">{{ labels[edge.source.provider] }} · {{ name(edge.source) }} → {{ labels[edge.target.provider] }} · {{ name(edge.target) }}</span>
              <small>{{ historyLabels[edge.history_capability] || edge.history_capability }}<template v-if="edge.unavailable_reason"> · {{ edge.unavailable_reason }}</template></small>
            </div>
          </article>
        </section>
      </fieldset>
    </template>
  </section>
</template>

<style scoped>
@import '../components/workforce/workforce-shared.css';

.sharing {
  display: flex;
  flex-direction: column;
  gap: 16px;
}

.sharing-header {
  display: flex;
  justify-content: space-between;
  align-items: flex-start;
  gap: 12px;
}

.sharing-header h2 {
  margin: 0 0 4px 0;
  font-size: 18px;
  font-weight: 600;
}

.sharing-list {
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.sharing-policy-card {
  padding: 12px 16px;
}

.policy-title-group {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
}

.policy-title-group strong {
  font-size: 14px;
  color: var(--id-text-primary);
}

.policy-mode-pill,
.policy-ver-pill {
  font-size: 12px;
  font-family: var(--id-font-mono);
  background: var(--id-bg-muted);
  border: 1px solid var(--id-border-subtle);
  color: var(--id-text-secondary);
  padding: 1px 7px;
  border-radius: var(--id-radius-sm);
}

.sharing-pagination {
  display: flex;
  justify-content: flex-end;
  gap: 8px;
}

.fs-legend {
  font-size: 14px;
  font-weight: 600;
  color: var(--id-text-primary);
  padding: 0 8px;
}

.subj-card {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  padding: 8px 12px;
  background: var(--id-bg-subtle);
  border: 1px solid var(--id-border-subtle);
  border-radius: var(--id-radius-md);
}

.subj-main {
  min-width: 0;
  flex: 1;
}

.subj-title {
  display: flex;
  align-items: center;
  gap: 6px;
  font-size: 13px;
  font-weight: 500;
  color: var(--id-text-primary);
}

.subj-main small {
  display: block;
  margin-top: 2px;
  font-size: 12px;
  color: var(--id-text-muted);
  overflow-wrap: anywhere;
}

.preview-edges-box {
  background: var(--id-bg-subtle);
  border: 1px solid var(--id-border-subtle);
  border-radius: var(--id-radius-md);
  padding: 12px;
}

.edge-route {
  font-size: 13px;
  font-weight: 500;
  color: var(--id-text-primary);
}
</style>

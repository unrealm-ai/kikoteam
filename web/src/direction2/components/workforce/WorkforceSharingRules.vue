<script setup lang="ts">
import { computed, inject, onUnmounted, ref, watch } from 'vue';
import { NAlert, NButton, NForm, NFormItem, NInput, NModal, NPagination, NRadio, NRadioGroup, NSpace, NSwitch, NTag, useDialog } from 'naive-ui';
import WorkforceSharingPicker from './WorkforceSharingPicker.vue';
import { sharingApi, type SharingDraft, type SharingPolicy, type SharingEdge } from '../../api/sharing';
import { selectorsApi, selectorKey, type SelectionCandidate, type SelectionRef } from '../../api/selectors';
import { D2ApiError } from '../../api/client';

const props = withDefaults(defineProps<{workspace: string; employeeId: string; canManage?: boolean}>(), {canManage: true});
const dialog = useDialog();
const rows = ref<SharingPolicy[]>([]), total = ref(0), page = ref(1), loading = ref(false), busy = ref(false);
const searchText = ref(''), searchQuery = ref('');
const error = ref(''), notice = ref(''), denied = ref(false), uncertain = ref(false), open = ref(false);
const draft = ref<SharingDraft>(), editing = ref<SharingPolicy>(), baseline = ref('');
const previewEdges = ref<SharingEdge[]>(), previewLoading = ref(false), previewError = ref('');
let previewRequest = 0, previewController = new AbortController();
const historyLabels: Record<string, string> = {local_history: 'Web 本地历史', provider_history: '飞书历史接口（外部权限未验证）', recent_window: '企微近 7 天历史（外部权限未验证）', unsupported: '当前渠道不支持历史读取'};
function clearPreview() { ++previewRequest; previewController.abort(); previewController = new AbortController(); previewEdges.value = undefined; previewLoading.value = false; previewError.value = ''; }
async function preview() {
  if (!writable.value || !valid.value || !draft.value) return;
  clearPreview(); const g = generation, request = previewRequest;
  previewLoading.value = true;
  try {
    const result = await sharingApi.preview(props.workspace, props.employeeId, JSON.parse(JSON.stringify(draft.value)), previewController.signal);
    if (g === generation && request === previewRequest) previewEdges.value = result.edges;
  } catch (e) {
    if (g !== generation || request !== previewRequest) return;
    if (e instanceof D2ApiError && [401, 403].includes(e.status)) fail(e);
    else previewError.value = e instanceof Error ? e.message : '预览失败，请重试';
  } finally { if (g === generation && request === previewRequest) previewLoading.value = false; }
}
watch(draft, clearPreview, {deep: true, flush: 'sync'});
const targetKind = ref<'group' | 'user'>('group'), names = ref<SelectionCandidate[]>([]);
const labels = {web: 'Web', feishu: '飞书', dingtalk: '钉钉', wecom: '企微'};
let generation = 0, read = 0, resolution = 0, controller = new AbortController();
const writable = computed(() => props.canManage && !busy.value && !loading.value && !denied.value && !uncertain.value);
const hasHiddenChannels = computed(() => !!draft.value && [...draft.value.sources, ...draft.value.targets].some(item => !['feishu', 'wecom'].includes(item.provider)));
const dirty = computed(() => open.value && !!draft.value && JSON.stringify(draft.value) !== baseline.value);
const valid = computed(() => !!draft.value?.name.trim() && !!draft.value && draft.value.sources.length >= (draft.value.mode === 'mutual' ? 2 : 1) && (draft.value.mode === 'mutual' ? !draft.value.targets.length : !!draft.value.targets.length));
function name(ref: SelectionRef) { const row = names.value.find(item => selectorKey(item.ref) === selectorKey(ref)); return `${labels[ref.provider]} · ${row?.display_name || row?.name || ref.external_id}`; }
function fail(e: unknown) {
  error.value = e instanceof Error ? e.message : '请求失败';
  if (e instanceof D2ApiError && [401, 403].includes(e.status)) revoke();
}
function revoke() { denied.value = true; rows.value = []; open.value = false; draft.value = undefined; names.value = []; }
async function resolveNames() {
  const g = generation, n = ++resolution;
  const all = [...rows.value.flatMap(row => [...row.sources, ...row.targets]), ...(draft.value ? [...draft.value.sources, ...draft.value.targets] : [])];
  await Promise.all((['groups', 'people'] as const).map(async kind => {
    const refs = [...new Map(all.filter(item => (kind === 'groups') === (item.kind === 'group')).map(item => [selectorKey(item), item])).values()];
    if (!refs.length) return;
    try {
      const result = await selectorsApi.resolve(props.workspace, props.employeeId, kind, refs, 'sharing', controller.signal);
      if (g === generation && n === resolution) names.value = [...names.value.filter(old => !result.items.some(item => selectorKey(item.ref) === selectorKey(old.ref))), ...result.items];
    } catch (e) {
      if (g !== generation || n !== resolution) return;
      if (e instanceof D2ApiError && [401, 403].includes(e.status)) fail(e);
      else notice.value = '部分名称暂不可解析，已保留原身份标识，请核对后保存。';
    }
  }));
}
async function list() {
  const g = generation, r = ++read; loading.value = true; error.value = '';
  try {
    const result = await sharingApi.list(props.workspace, props.employeeId, searchQuery.value, page.value, controller.signal);
    if (g !== generation || r !== read) return;
    rows.value = result.items; total.value = result.total; void resolveNames();
  } catch (e) { if (g === generation && r === read) fail(e); }
  finally { if (g === generation && r === read) loading.value = false; }
}
function search(clear = false) {
  if (busy.value || denied.value || !props.canManage) return;
  if (clear) searchText.value = '';
  searchQuery.value = searchText.value.trim(); page.value = 1;
  ++resolution; rows.value = []; total.value = 0;
  void list();
}
function edit(row?: SharingPolicy) {
  if (!writable.value) return;
  editing.value = row;
  draft.value = row ? JSON.parse(JSON.stringify({name: row.name, mode: row.mode, enabled: row.enabled, sources: row.sources, targets: row.targets})) : {name: '', mode: 'directed', enabled: true, sources: [], targets: []};
  baseline.value = JSON.stringify(draft.value); targetKind.value = row?.targets.some(item => item.kind === 'user') ? 'user' : 'group';
  open.value = true; error.value = ''; void resolveNames();
}
function dismiss() { if (!busy.value) { open.value = false; draft.value = undefined; editing.value = undefined; } }
function close() {
  if (busy.value) return;
  const g = generation;
  if (!dirty.value) { dismiss(); return; }
  dialog.warning({title: '群共享策略尚未保存', content: '关闭将放弃当前修改。', positiveText: '放弃修改', negativeText: '继续编辑', onPositiveClick: () => { if (g === generation) dismiss(); }});
}
function mode(value: 'directed' | 'mutual') {
  if (!draft.value || !writable.value) return;
  if (value === 'mutual' && draft.value.targets.length) { error.value = '请先移除授权目标，再切换项目组，避免将单向授权扩大为双向共享。'; return; }
  draft.value.mode = value; error.value = '';
}
function pick(side: 'sources' | 'targets', candidate: SelectionCandidate) {
  if (!writable.value || !draft.value || !candidate.selectable || candidate.ref.kind === 'department') return;
  if (!['feishu', 'wecom'].includes(candidate.ref.provider)) return;
  if (side === 'sources' && candidate.ref.kind !== 'group') return;
  if (side === 'targets' && draft.value.mode === 'mutual') return;
  if (!draft.value[side].some(item => selectorKey(item) === selectorKey(candidate.ref))) draft.value[side].push(candidate.ref);
  names.value = [...names.value.filter(item => selectorKey(item.ref) !== selectorKey(candidate.ref)), candidate];
}
function unpick(side: 'sources' | 'targets', ref: SelectionRef) { if (writable.value && draft.value) draft.value[side] = draft.value[side].filter(item => selectorKey(item) !== selectorKey(ref)); }
async function persist(value: SharingDraft, row?: SharingPolicy) {
  if (!writable.value) return false;
  const g = generation; busy.value = true; error.value = ''; notice.value = '';
  try {
    const saved = await sharingApi.save(props.workspace, props.employeeId, value, row?.id, row?.version, controller.signal);
    if (g !== generation) return false;
    if (open.value) { editing.value = saved; baseline.value = JSON.stringify(draft.value); open.value = false; draft.value = undefined; }
    notice.value = '策略已保存。实际读取时仍按当前身份、授权方向和渠道能力校验。';
    await list(); return true;
  } catch (e) {
    if (g === generation) {
      fail(e);
      if (!(e instanceof D2ApiError) || e.status >= 500) { uncertain.value = true; error.value += '；保存结果未知，请重新读取核对，不要重复创建。'; }
    }
    return false;
  } finally { if (g === generation) busy.value = false; }
}
function save() { return valid.value && draft.value ? persist(JSON.parse(JSON.stringify(draft.value)), editing.value) : Promise.resolve(false); }
function toggle(row: SharingPolicy, enabled: boolean) { void persist({name: row.name, mode: row.mode, enabled, sources: row.sources, targets: row.targets}, row); }
function remove(row: SharingPolicy) {
  if (!writable.value) return;
  const g = generation;
  dialog.warning({title: '删除群共享规则', content: `删除“${row.name}”将撤销本规则授权，不影响其他独立规则。`, positiveText: '删除', negativeText: '取消', onPositiveClick: async () => {
    if (g !== generation || !writable.value) return;
    busy.value = true; error.value = '';
    try { await sharingApi.remove(props.workspace, props.employeeId, row.id, row.version, controller.signal); if (g === generation) { if (rows.value.length === 1 && page.value > 1) page.value--; await list(); } }
    catch (e) { if (g === generation) fail(e); }
    finally { if (g === generation) busy.value = false; }
  }});
}
function reload() { if (busy.value) return; open.value = false; draft.value = undefined; uncertain.value = false; void list(); }
watch(() => [props.workspace, props.employeeId, props.canManage], () => {
  clearPreview(); ++generation; ++read; ++resolution; controller.abort(); controller = new AbortController();
  rows.value = []; names.value = []; draft.value = undefined; editing.value = undefined; open.value = false; page.value = 1; searchText.value = ''; searchQuery.value = '';
  error.value = ''; notice.value = ''; busy.value = false; uncertain.value = false; denied.value = !props.canManage;
  if (props.canManage) void list();
}, {immediate: true});
const guard = inject<{register: (value: unknown) => void; unregister: (value: unknown) => void} | null>('d2DraftGuard', null);
const draftGuard = {hasUnsaved: () => dirty.value || busy.value, getLabel: () => '群消息共享规则', save, discard: dismiss};
guard?.register(draftGuard);
onUnmounted(() => { clearPreview(); ++generation; controller.abort(); guard?.unregister(draftGuard); });
</script>

<template>
  <div class="settings-card-block">
    <div class="cs-header-flex">
      <div><h4 class="cb-title">3. 群消息共享</h4><p class="cb-sub">统一配置跨渠道项目组和信息同步策略，授权任务按需<strong>读取来源群历史</strong>，不会自动转发消息。</p></div>
      <NButton size="tiny" type="primary" secondary :disabled="!writable" @click="edit()">+ 新建共享规则</NButton>
    </div>
    <form v-if="!denied" class="sharing-search" role="search" aria-label="按规则名称搜索群共享策略" @submit.prevent="search()">
      <NInput v-model:value="searchText" placeholder="搜索共享规则名称" aria-label="共享规则名称" maxlength="120" :disabled="busy"/>
      <NButton attr-type="submit" :disabled="busy">搜索</NButton>
      <NButton :disabled="busy || (!searchText && !searchQuery)" @click="search(true)">清空搜索</NButton>
    </form>
    <NAlert v-if="error" type="error">{{ error }}</NAlert>
    <NAlert v-if="notice" type="info">{{ notice }}</NAlert>
    <NAlert v-if="denied" type="warning">当前无权管理该员工的群共享策略。</NAlert>
    <NButton v-if="uncertain || (error && !denied)" size="small" style="align-self: flex-start;" :disabled="busy || loading" @click="reload">重新读取并核对策略</NButton>
    <p v-if="loading" role="status">正在读取共享规则…</p>
    <div v-else-if="!denied" class="sharing-rules-grid">
      <div v-for="rule in rows" :key="rule.id" class="sharing-rule-card">
        <div class="src-top"><div class="src-name-row"><strong>{{ rule.name }}</strong><NTag size="tiny" :type="rule.mode === 'mutual' ? 'info' : 'warning'" round>{{ rule.mode === 'mutual' ? '项目组 (双向互读)' : '信息同步 (定向共享)' }}</NTag><NTag size="tiny" :type="rule.enabled ? 'success' : 'default'">{{ rule.enabled ? '已启用' : '已停用' }}</NTag></div><div class="src-actions"><NSwitch size="small" :value="rule.enabled" :disabled="!writable" @update:value="value => toggle(rule, value)"/><NButton size="tiny" quaternary :disabled="!writable" @click="edit(rule)">编辑</NButton><NButton size="tiny" type="error" quaternary :disabled="!writable" @click="remove(rule)">删除</NButton></div></div>
        <div class="src-details"><div class="src-row"><span class="sr-lbl">读取来源群历史：</span><span class="sr-val">{{ rule.sources.map(name).join('、') }}</span></div><div v-if="rule.mode === 'directed'" class="src-row"><span class="sr-lbl">授权任务目标：</span><span class="sr-val">{{ rule.targets.map(name).join('、') }}</span></div></div>
      </div>
      <div v-if="!rows.length && !error" class="empty-note">{{ searchQuery ? '没有匹配名称的共享规则。' : '暂无群消息共享规则，点击上方「+ 新建共享规则」配置。' }}</div>
      <NPagination v-if="total > 20" :page="page" :item-count="total" :page-size="20" :disabled="busy || loading" @update:page="value => { page = value; list(); }"/>
    </div>
    <NModal v-if="!denied" :show="open" preset="card" title="配置群消息共享规则 (读取来源群历史)" style="width: min(540px, 94vw);" :mask-closable="!busy" :closable="!busy" @update:show="value => { if (!value) close(); }">
      <NAlert v-if="error" type="error">{{ error }}</NAlert>
      <NAlert v-if="hasHiddenChannels" type="info">此策略包含暂不开放选择的钉钉或 Web 配置，仍会原样保留。移除已选项将撤销相应授权，当前无法重新选入。</NAlert>
      <div v-if="draft" class="sharing-rule-form"><NForm label-placement="top">
        <NFormItem label="规则名称"><NInput v-model:value="draft.name" :disabled="!writable" maxlength="120" placeholder="例如：研发排查按需读取客服群历史"/></NFormItem>
        <NFormItem label="共享模式"><NRadioGroup :value="draft.mode" :disabled="!writable" @update:value="mode"><NSpace wrap><NRadio value="directed">信息同步 (定向共享)</NRadio><NRadio value="mutual">项目组 (双向互读)</NRadio></NSpace></NRadioGroup></NFormItem>
        <NFormItem :label="draft.mode === 'mutual' ? '选择互通群聊 (至少选择 2 个群双向互读)' : '选择来源群 (任务按需读取这些群的历史)'"><div class="selection-field"><NSpace wrap><NTag v-for="item in draft.sources" :key="selectorKey(item)" :closable="writable" @close="unpick('sources', item)">{{ name(item) }}</NTag></NSpace><WorkforceSharingPicker :workspace="workspace" :employee-id="employeeId" kind="groups" :disabled="!writable" @pick="candidate => pick('sources', candidate)" @revoked="revoke"/></div></NFormItem>
        <template v-if="draft.mode === 'directed'"><NFormItem label="授权任务目标类型"><NRadioGroup v-model:value="targetKind" :disabled="!writable"><NSpace wrap><NRadio value="group">授权目标群聊</NRadio><NRadio value="user">授权指定成员私聊</NRadio></NSpace></NRadioGroup></NFormItem><NFormItem label="授权目标 (可跨渠道选择)"><div class="selection-field"><NSpace wrap><NTag v-for="item in draft.targets" :key="selectorKey(item)" :closable="writable" @close="unpick('targets', item)">{{ name(item) }}</NTag></NSpace><WorkforceSharingPicker :workspace="workspace" :employee-id="employeeId" :kind="targetKind === 'group' ? 'groups' : 'people'" :disabled="!writable" @pick="candidate => pick('targets', candidate)" @revoked="revoke"/></div></NFormItem></template>
        <NAlert type="info" :bordered="false" style="margin-top: 10px;">各策略独立、不传递；授权按需读取历史，<strong>不会将消息自动转发到目标群</strong>。钉钉目前不支持群历史读取。</NAlert>
      </NForm></div>
      <NAlert v-if="previewError" type="error">{{ previewError }}</NAlert>
      <section v-if="previewEdges" class="sharing-preview" aria-label="共享方向与来源历史能力预览">
        <strong>本策略直接授权方向</strong><p>预览不保存策略、不读取消息；实际执行时仍会重新校验。</p>
        <p v-if="!previewEdges.length">当前没有直接授权方向。</p>
        <div v-for="(edge, index) in previewEdges" :key="index" class="preview-edge">
          <div>来源：{{ name(edge.source) }} → 授权目标：{{ name(edge.target) }}</div>
          <small>来源历史能力：{{ historyLabels[edge.history_capability] || '能力暂不可识别' }}<template v-if="edge.unavailable_reason"> · {{ edge.unavailable_reason }}</template></small>
        </div>
      </section>
      <template #footer><div style="display:flex;justify-content:flex-end;gap:8px;flex-wrap:wrap;"><NButton :disabled="busy" @click="close">取消</NButton><NButton :loading="previewLoading" :disabled="!writable || !valid || previewLoading" @click="preview">预览方向与读取能力</NButton><NButton type="primary" :loading="busy" :disabled="!writable || !valid" @click="save">保存共享规则</NButton></div></template>
    </NModal>
  </div>
</template>
<style scoped>
@import './workforce-shared.css';
.settings-card-block { background:var(--id-bg-surface);border:1px solid var(--id-border-subtle);border-radius:var(--id-radius-lg);padding:16px;display:flex;flex-direction:column;gap:10px; }
.cb-title { font-size:14px;font-weight:600;color:var(--id-text-primary);margin:0; }
.cb-sub { font-size:12px;color:var(--id-text-secondary);margin:0; }
@media(max-width:760px) { .settings-card-block { padding:12px; } }
.sharing-search { display:flex;gap:8px;align-items:center; }
.sharing-search .n-input { flex:1;min-width:0; }
.sharing-preview { margin-top:12px;font-size:12px;overflow-wrap:anywhere; }
.preview-edge { padding:8px 0;border-top:1px solid var(--id-border-subtle); }
.selection-field { width:100%;display:flex;flex-direction:column;gap:8px; }
.sharing-rules-grid { display:flex;flex-direction:column;gap:10px; }
.sharing-rule-card { background:var(--id-bg-subtle);border:1px solid var(--id-border-subtle);border-radius:var(--id-radius-md);padding:12px;display:flex;flex-direction:column;gap:8px; }
.src-top { display:flex;justify-content:space-between;align-items:center;gap:8px;flex-wrap:wrap; }
.src-name-row { display:flex;align-items:center;gap:8px;flex-wrap:wrap; }
.src-name-row strong { font-size:13px;color:var(--id-text-primary); }
.src-actions { display:flex;align-items:center;gap:8px; }
.src-details { display:flex;flex-direction:column;gap:4px;font-size:12px; }
.src-row { display:flex;gap:6px;flex-wrap:wrap; }
.sr-lbl { color:var(--id-text-secondary);min-width:110px; }
.sr-val { color:var(--id-text-primary);font-weight:500;word-break:break-word; }
</style>

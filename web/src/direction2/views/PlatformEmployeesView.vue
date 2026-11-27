<script setup lang="ts">
import { computed, inject, provide, onUnmounted, ref, watch } from 'vue';
import {
  NAlert, NButton, NCheckbox, NEmpty, NForm, NFormItem,
  NInput, NSelect, NPagination, NPopconfirm, NSkeleton, NTag, useDialog, useMessage
} from 'naive-ui';
import PlatformPackagesPanel from './PlatformPackagesPanel.vue';
import PlatformTriggersPanel from './PlatformTriggersPanel.vue';
import PlatformUsagePanel from './PlatformUsagePanel.vue';
import PlatformSharingPanel from './PlatformSharingPanel.vue';
import PlatformFeishuPanel from './PlatformFeishuPanel.vue';
import { channelLabels, type ChannelProvider } from '../api/channels';
import AgentSetupPanel from './AgentSetupPanel.vue';
import D2EmployeeAvatar from '../components/D2EmployeeAvatar.vue';
import D2ChannelLogo from '../components/D2ChannelLogo.vue';
import { PORTRAIT_AVATAR_PRESETS } from '../types';
import { activeWorkspaceId } from '../api/client';
import { platformRequest } from '../api/platform';
import { agentsApi, type Agent, type AgentConfiguration } from '../api/agents';
import { modelProfilesApi, modelOptions, modelSelection, choiceValue, type ModelChoice } from '../api/models';

const props = defineProps<{ selectedEmployeeId?: string | undefined }>();
const emit = defineEmits<{ navigate: [target: string, param?: string, targetId?: string] }>();
const message = useMessage();
const dialog = useDialog();
const items = ref<Agent[]>([]);
const models = ref<ModelChoice[]>([]);
const role = ref('');
const statusFilter = ref<'all' | 'enabled' | 'disabled'>('all');
const loading = ref(false);
const error = ref('');
const modelError = ref('');
const detailError = ref('');
const saving = ref(false);
const actionBusy = ref(false);
const detailLoading = ref(false);
const detailFailed = ref(false);
const setupDirty = ref(false), setupBusy = ref(false);
const avatarPicker = ref(false);
type DetailTab = 'overview'|'persona'|'packages'|'channels'|'triggers'|'rules';
const activeDetailTab = ref<DetailTab>('persona');
const detailTabs: {key: DetailTab; label: string}[] = [
  {key:'overview',label:'概览与就绪检查'}, {key:'persona',label:'设定与模型'},
  {key:'packages',label:'能力包与授权'}, {key:'channels',label:'渠道接入与群策略'},
  {key:'triggers',label:'自动触发器'}, {key:'rules',label:'权限与确认规则'}
];
const sharingOpen = ref(false);
const selectedChannel = ref<ChannelProvider>('feishu');
type DraftHandler = {hasUnsaved:()=>boolean;save:()=>Promise<boolean>;discard:()=>void;getLabel?:()=>string};
const childDrafts = new Set<DraftHandler>();
let discardingChildren = false;
function discardChildren(handlers:DraftHandler[]) { discardingChildren=true; try {handlers.forEach(h=>h.discard());} finally {discardingChildren=false;} }
provide('d2DraftGuard', {register:(handler:DraftHandler)=>childDrafts.add(handler),unregister:(handler:DraftHandler)=>childDrafts.delete(handler)});
function navigateDetail(action:()=>void) {
  if (saving.value || setupBusy.value || actionBusy.value) return;
  if (packagesPanel.value?.hasOpenEditor()) { message.warning('请先保存或关闭能力包编辑窗口。'); return; }
  const pending=[...childDrafts].filter(h=>h.hasUnsaved());
  const current=detailGeneration, workspace=activeWorkspaceId.value;
  if (dirty.value || pending.length) {
    dialog.warning({title:'存在未保存的配置草稿',content:'切换页面将放弃本次未保存的修改。',positiveText:'放弃修改',negativeText:'继续编辑',onPositiveClick:()=>{
      if(current!==detailGeneration || workspace!==activeWorkspaceId.value)return;
      discardChildren(pending); if(selected.value) reset(selected.value); setupDirty.value=false; action();
    }}); return;
  }
  action();
}
function switchTab(tab:DetailTab) { if(tab!==activeDetailTab.value) navigateDetail(()=>{activeDetailTab.value=tab;sharingOpen.value=false;}); }
function switchChannel(channel:ChannelProvider) { if(channel!==selectedChannel.value || sharingOpen.value) navigateDetail(()=>{selectedChannel.value=channel;sharingOpen.value=false;}); }
async function openTab(agent:Agent,tab:DetailTab,channel?:ChannelProvider,sharing=false) {
  const expected=detailGeneration+1; await open(agent.id); if(expected!==detailGeneration || selected.value?.id!==agent.id)return;
  activeDetailTab.value=tab; sharingOpen.value=sharing; if(channel)selectedChannel.value=channel;
}
const channelProviders: ChannelProvider[] = ['feishu', 'dingtalk', 'wecom'];
const packagesPanel = ref<InstanceType<typeof PlatformPackagesPanel>>();
const page = ref(1);
const total = ref(0);
const show = ref(false);
const selected = ref<Agent>();
const fresh = (): AgentConfiguration & { model_profile_id: string } => ({
  name: '', role: '', avatar: 'cn-v2-01', instruction: '', model_profile_id: ''
});
const form = ref(fresh());
const baseline = ref('');
let generation = 0;
let detailGeneration = 0;
let controller: AbortController | undefined;
const canManage = computed(() => ['owner', 'admin'].includes(role.value));
const filteredItems = computed(() => {
  if (statusFilter.value === 'enabled') return items.value.filter(a => a.enabled);
  if (statusFilter.value === 'disabled') return items.value.filter(a => !a.enabled);
  return items.value;
});
const dirty = computed(() => show.value && canManage.value && (JSON.stringify(form.value) !== baseline.value || setupDirty.value));
const options = computed(() => modelOptions(models.value));
const initialChoice = (agent: Agent) => agent.platform_model_id ? `platform:${agent.platform_model_id}` : `workspace:${agent.model_profile_id}`;
const officialApplies = computed(() => form.value.model_profile_id.startsWith('platform:') || models.value.some(m => m.source === 'platform' && m.is_default));
const firstBinding = computed(() => !!selected.value && !selected.value.payer && officialApplies.value);
const confirmBilling = ref(false);
const validModel = computed(() => models.value.some(m => choiceValue(m) === form.value.model_profile_id && m.enabled && m.validated));
const canSave = computed(() => canManage.value && !!form.value.name.trim() && (validModel.value || (!!selected.value && form.value.model_profile_id === initialChoice(selected.value))) && (!firstBinding.value || confirmBilling.value || form.value.model_profile_id === initialChoice(selected.value!)) && !saving.value && !detailLoading.value && !modelError.value && !detailFailed.value);
function reset(agent?: Agent) {
  selected.value = agent;
  confirmBilling.value = false;
  const preferred = models.value.find(m => m.enabled && m.validated && m.is_default) || models.value.find(m => m.enabled && m.validated);
  form.value = agent ? { name: agent.name, role: agent.role, avatar: agent.avatar, instruction: agent.instruction, model_profile_id: initialChoice(agent) } : { ...fresh(), model_profile_id: preferred ? choiceValue(preferred) : '' };
  baseline.value = JSON.stringify(form.value);
  detailError.value = ''; avatarPicker.value = false;
}
async function loadModels(workspace: string, current: number, signal: AbortSignal) {
  const all: ModelChoice[] = [];
  const seen = new Set<string>();
  try {
    for (let p = 1; ; p++) {
      const result = await modelProfilesApi.choices(workspace, p, signal);
      if (current !== generation) return;
      if (result.page !== p || result.items.some(item => seen.has(choiceValue(item)))) throw new Error('模型分页响应重复或页码错误，请重试或联系管理员。');
      result.items.forEach(item => seen.add(choiceValue(item)));
      all.push(...result.items);
      if (!result.items.length || all.length >= result.total) break;
    }
    if (current === generation) models.value = all;
  } catch (err) { if (current === generation) modelError.value = err instanceof Error ? err.message : '模型读取失败'; }
}
async function load() {
  const current = ++generation;
  controller?.abort(); controller = new AbortController();
  const workspace = activeWorkspaceId.value;
  loading.value = true; error.value = ''; modelError.value = ''; models.value = [];
  if (!workspace) { loading.value = false; return; }
  const signal = controller.signal;
  const modelLoad = loadModels(workspace, current, signal);
  try {
    const [list, info] = await Promise.all([agentsApi.list(workspace, page.value, signal), platformRequest<{ role: string }>(`/workspaces/${encodeURIComponent(workspace)}`, { signal })]);
    if (current !== generation) return;
    items.value = list.items; total.value = list.total; role.value = info.role;
  } catch (err) { if (current === generation) error.value = err instanceof Error ? err.message : '员工列表读取失败'; }
  finally { await modelLoad; if (current === generation) loading.value = false; }
}
async function open(id?: string) {
  const current = ++detailGeneration;
  const workspace = activeWorkspaceId.value;
  reset(); activeDetailTab.value='persona'; sharingOpen.value=false; detailFailed.value = false; show.value = true; detailLoading.value = !!id;
  if (!id) return;
  try {
    const agent = await agentsApi.get(workspace, id);
    if (current === detailGeneration && workspace === activeWorkspaceId.value) reset(agent);
  } catch (err) { if (current === detailGeneration) { detailFailed.value = true; detailError.value = err instanceof Error ? err.message : '员工详情读取失败'; } }
  finally { if (current === detailGeneration) detailLoading.value = false; }
}
function close() { if(discardingChildren)return; navigateDetail(() => { show.value = false; ++detailGeneration; }); }
// Child panels already confirm their own drafts before emitting close.
function closeFromPanel() { if(discardingChildren)return; show.value=false; ++detailGeneration; }
async function save(): Promise<boolean> {
  if (setupDirty.value || setupBusy.value) { detailError.value = '请先保存接入或使用范围设置，再保存并离开。'; return false; }
  if (!canSave.value) return false;
  const workspace = activeWorkspaceId.value; const current = detailGeneration;
  const { model_profile_id: choice, ...fields } = form.value;
  const include = !selected.value || choice.startsWith('workspace:') || choice !== initialChoice(selected.value) || confirmBilling.value;
  const config = { ...fields, name: fields.name.trim(), role: fields.role.trim(), ...(include ? modelSelection(choice) : {}) };
  saving.value = true; detailError.value = '';
  try {
    const saved = selected.value ? await agentsApi.update(workspace, selected.value, { ...config, ...(confirmBilling.value ? { confirm_platform_billing: true } : {}) }) : await agentsApi.create(workspace, config);
    if (workspace !== activeWorkspaceId.value || current !== detailGeneration) return false;
    reset(saved); message.success('员工配置已保存');
    await load(); // list errors never overwrite successful write.
    return true;
  } catch (err) {
    if (workspace === activeWorkspaceId.value && current === detailGeneration) detailError.value = err instanceof Error ? err.message : '保存失败';
    return false;
  } finally { if (current === detailGeneration) saving.value = false; }
}
async function toggle(agent: Agent) {
  if (!canManage.value || actionBusy.value) return;
  const workspace = activeWorkspaceId.value;
  actionBusy.value = true;
  try {
    const updated = await agentsApi.setEnabled(workspace, agent);
    if (workspace !== activeWorkspaceId.value) return;
    if(selected.value?.id===agent.id) reset(updated);
    message.success(agent.enabled ? '员工已停用' : '员工已启用'); await load();
  } catch (err) { if (workspace === activeWorkspaceId.value) message.error(err instanceof Error ? err.message : '操作失败'); }
  finally { actionBusy.value = false; }
}
const draft = { hasUnsaved: () => dirty.value || saving.value || setupBusy.value || [...childDrafts].some(h=>h.hasUnsaved()), getLabel: () => '员工配置',
  save: async()=>{for(const h of childDrafts)if(h.hasUnsaved()&&!await h.save())return false;return dirty.value?save():true;},
  discard: () => { discardChildren([...childDrafts]); show.value = false; ++detailGeneration; } };
const guard = inject<{ register: (handler: typeof draft) => void; unregister: (handler: typeof draft) => void }>('d2DraftGuard');
guard?.register(draft);
watch(activeWorkspaceId, async (workspace) => {
  ++detailGeneration; show.value = false; childDrafts.clear();
  saving.value = false; role.value = ''; items.value = []; statusFilter.value = 'all'; page.value = 1;
  await load();
  if (workspace === activeWorkspaceId.value && props.selectedEmployeeId) void open(props.selectedEmployeeId);
}, { immediate: true });
watch(page, () => { void load(); });
watch(() => props.selectedEmployeeId, id => { if (id) void open(id); });
onUnmounted(() => { ++generation; ++detailGeneration; controller?.abort(); guard?.unregister(draft); });
</script>

<template>
  <section class="platform-employees">
    <template v-if="!show">
    <header class="list-header">
      <div class="header-titles">
        <h1 class="view-title">AI 员工</h1>
        <p class="view-subtitle">配置员工职责与执行模型。保存即生效，无需单独发布。</p>
      </div>
      <div v-if="canManage" class="btn-group">
        <NButton size="small" secondary @click="emit('navigate', 'market')">从模板创建</NButton>
        <NButton size="small" type="primary" :disabled="loading || !!modelError" @click="open()">新建 AI 员工</NButton>
      </div>
    </header>

    <NAlert type="info" class="scope-notice">Web 对话已接入；渠道配置保存与实际连接分别展示；定时与 Webhook 可在自动化任务中管理；人工确认尚未接入。</NAlert>

    <div v-if="loading" class="wf-skeleton-wrap">
      <div class="wf-skeleton-bar">
        <NSkeleton text style="width: 240px; height: 22px;" />
        <NSkeleton text style="width: 112px; height: 24px;" />
      </div>
      <div class="wf-skeleton-grid">
        <div v-for="n in 3" :key="'emp-sk-' + n" class="wf-skeleton-card">
          <NSkeleton text style="width: 55%; height: 20px;" />
          <NSkeleton text :repeat="2" />
          <NSkeleton text style="width: 40%;" />
        </div>
      </div>
    </div>

    <div v-else-if="error" class="wf-error-card" role="alert">
      <p class="wf-error-title">无法读取员工列表</p>
      <p class="wf-error-desc">{{ error }}</p>
      <NButton type="primary" size="small" @click="load">重新加载</NButton>
    </div>

    <template v-else>
      <NAlert v-if="modelError" type="warning" class="scope-notice">
        模型列表读取失败：{{ modelError }}
        <NButton size="small" style="margin-left: 8px;" @click="load">重试</NButton>
      </NAlert>
      <NAlert v-if="!canManage" type="info" class="scope-notice">当前仅可查看，修改需要工作区管理员权限。</NAlert>

      <div v-if="items.length" class="list-filter-bar">
        <div class="filter-pills">
          <button type="button" class="pill-btn" :class="{ active: statusFilter === 'all' }" @click="statusFilter = 'all'">
            全部员工 ({{ items.length }})
          </button>
          <button type="button" class="pill-btn" :class="{ active: statusFilter === 'enabled' }" @click="statusFilter = 'enabled'">
            已启用 ({{ items.filter(a => a.enabled).length }})
          </button>
          <button type="button" class="pill-btn" :class="{ active: statusFilter === 'disabled' }" @click="statusFilter = 'disabled'">
            已停用 ({{ items.filter(a => !a.enabled).length }})
          </button>
        </div>
        <span class="filter-count-summary tabular-num">本页 {{ filteredItems.length }} / 共 {{ total }} 位</span>
      </div>

      <div v-if="!items.length" class="empty-filter-card">
        <NEmpty description="还没有 AI 员工">
          <template #extra>
            <div style="display: flex; flex-direction: column; align-items: center; gap: 10px;">
              <p class="field-note">为员工起个名字，选择已通过测试的模型，即可保存基础配置。</p>
              <div v-if="canManage" class="btn-group">
                <NButton size="small" secondary @click="emit('navigate', 'market')">从模板创建</NButton>
                <NButton size="small" type="primary" :disabled="loading || !!modelError" @click="open()">新建 AI 员工</NButton>
              </div>
            </div>
          </template>
        </NEmpty>
      </div>

      <div v-else-if="!filteredItems.length" class="empty-filter-card">
        <NEmpty description="当前筛选条件下暂无 AI 员工">
          <template #extra>
            <NButton size="small" @click="statusFilter = 'all'">重置筛选条件</NButton>
          </template>
        </NEmpty>
      </div>

      <div v-else class="employee-cards-grid">
        <article
          v-for="agent in filteredItems"
          :key="agent.id"
          class="emp-card"
          :class="{ 'emp-disabled': !agent.enabled }"
        >
          <div class="ec-body">
            <div
              class="ec-card-hit"
              tabindex="0"
              @click="open(agent.id)"
              @keydown.enter.self.prevent="open(agent.id)"
            >
              <div class="ec-top">
                <D2EmployeeAvatar :name="agent.name" :preset="agent.avatar" :identity-key="agent.id" size="md" />
                <div class="ec-meta">
                  <div class="ec-title-row">
                    <h2 class="ec-name" :title="agent.name">{{ agent.name }}</h2>
                    <span class="ai-label">AI</span>
                    <span class="engine-label">Pi</span>
                  </div>
                  <span class="ec-code tabular-num">v{{ agent.config_version }}</span>
                </div>
                <span class="wf-status-pill" :class="agent.enabled ? 'is-success' : ''">
                  <span class="wf-status-dot" />
                  {{ agent.enabled ? '已启用' : '已停用' }}
                </span>
              </div>

              <p class="ec-role">{{ agent.role || '未填写岗位职责' }}</p>
            </div>

            <div class="ec-resources-row">
              <span class="ec-model-pill" :title="agent.model_name">
                {{ agent.model_name }}
                <span v-if="agent.model_available === false" class="ec-model-warn">（当前不可用）</span>
              </span>
            </div>

            <div class="ec-config-shortcuts">
              <div class="ec-shortcut-row">
                <NButton size="tiny" secondary @click="openTab(agent, 'packages')">能力包与授权</NButton>
                <template v-if="canManage">
                  <NButton size="tiny" secondary @click="openTab(agent, 'rules')">统一使用配置</NButton>
                  <NButton size="tiny" secondary @click="openTab(agent, 'channels', undefined, true)">群共享策略</NButton>
                  <NButton size="tiny" secondary @click="openTab(agent, 'triggers')">自动化任务</NButton>
                </template>
              </div>
              <div v-if="canManage" class="ec-shortcut-row">
                <NButton
                  v-for="channel in channelProviders"
                  :key="channel"
                  size="tiny"
                  tertiary
                  @click="openTab(agent, 'channels', channel)"
                >
                  <D2ChannelLogo :platform="channel" :size="13" />
                  <span style="margin-left: 4px;">{{ channelLabels[channel] }}渠道</span>
                </NButton>
              </div>
            </div>
          </div>

          <footer class="ec-card-footer">
            <p class="ec-context" :title="`Pi · ${agent.model_name}`">
              <span class="ec-context-dot" :class="agent.enabled ? 'is-active' : ''" aria-hidden="true" />
              Pi · {{ agent.model_name }}
            </p>
            <div class="ec-footer-actions">
              <NButton size="small" color="#18181B" @click="emit('navigate', 'chat', undefined, agent.id)">开始对话</NButton>
              <NButton size="small" secondary @click="open(agent.id)">{{ canManage ? '配置员工' : '查看详情' }}</NButton>
              <NPopconfirm v-if="canManage" @positive-click="toggle(agent)">
                <template #trigger>
                  <NButton size="small" quaternary :type="agent.enabled ? 'error' : 'primary'" :disabled="actionBusy || (!agent.enabled && agent.model_available === false)">{{ agent.enabled ? '停用' : '启用' }}</NButton>
                </template>
                {{ agent.enabled ? '停用后停止受理新输入和新的官方模型请求，已发出的模型请求可以完成。' : '重新启用不会恢复此前被拒绝的输入。' }}
              </NPopconfirm>
            </div>
          </footer>
        </article>
      </div>

      <div v-if="total > 20" class="emp-pagination">
        <NPagination v-model:page="page" :page-size="20" :item-count="total" />
      </div>
    </template>

    </template>
    <div v-else class="employee-detail-container">
      <NAlert v-if="error || modelError" type="warning">配置操作结果不受列表刷新影响；刷新失败：{{error || modelError}} <NButton text @click="load">重新读取列表</NButton></NAlert>
      <div class="detail-top-bar">
        <div class="detail-hero-left">
          <button class="back-link-btn" :disabled="saving || setupBusy" @click="close">← 返回员工名录</button>
          <div class="emp-profile-summary">
            <D2EmployeeAvatar :name="form.name || 'AI 员工'" :preset="form.avatar" :identity-key="selected?.id" size="lg" />
            <div class="profile-titles"><div class="pt-row"><h2>{{ selected?.name || '新建 AI 员工' }}</h2><span class="engine-label">Pi</span><span v-if="selected" class="ec-code">v{{ selected.config_version }}</span><NTag v-if="dirty" size="small" type="warning">未保存</NTag></div><p>{{ selected?.role || '配置员工职责、模型与工作渠道' }}</p></div>
          </div>
        </div>
        <div v-if="selected" class="detail-hero-actions">
          <NButton size="small" type="primary" @click="navigateDetail(()=>emit('navigate','chat',undefined,selected?.id))">{{ selected.enabled ? '开始对话' : '查看历史对话' }}</NButton>
          <NPopconfirm v-if="canManage" @positive-click="toggle(selected)"><template #trigger><NButton size="small" :disabled="actionBusy || dirty || setupBusy" :type="selected.enabled?'error':'primary'" secondary>{{ selected.enabled ? '停用员工' : '恢复在岗' }}</NButton></template>{{ selected.enabled ? '停用后停止受理新输入；已发出的模型请求可以完成。' : '重新启用不会恢复此前被拒绝的输入。' }}</NPopconfirm>
        </div>
      </div>
      <NAlert v-if="selected && !selected.enabled" type="warning">该 AI 员工已被管理员停用，不再受理新输入。</NAlert>
      <nav v-if="selected" class="detail-tabs" aria-label="员工配置模块">
        <button v-for="tab in detailTabs" :key="tab.key" type="button" :class="{active:activeDetailTab===tab.key}" :aria-current="activeDetailTab===tab.key?'page':undefined" @click="switchTab(tab.key)">{{tab.label}}</button>
      </nav>
        <p v-if="detailLoading" class="field-note">正在读取详情…</p>
        <template v-else>
          <NAlert v-if="detailError" type="error" class="scope-notice">
            {{ detailError }}
            <p v-if="selected" style="margin: 4px 0 0 0;">如配置已被他人修改，请关闭后重新打开，核对最新版本再编辑。</p>
          </NAlert>

          <NForm v-if="!detailFailed && activeDetailTab === 'persona'" :disabled="!canManage || saving" label-placement="top" class="emp-drawer-form">
            <div class="form-block">
              <h4 class="fb-title">1. 员工档案与头像</h4>
              <NFormItem label="员工名称（必填）">
                <NInput v-model:value="form.name" :maxlength="100" placeholder="例如：研发助手" />
              </NFormItem>
              <NFormItem label="岗位职责（选填）">
                <NInput v-model:value="form.role" :maxlength="2000" placeholder="例如：协助研发团队排查问题" />
              </NFormItem>
              <NFormItem label="员工头像">
                <div style="width: 100%;">
                  <div class="avatar-current">
                    <D2EmployeeAvatar :name="form.name || 'AI 员工'" :preset="form.avatar" size="lg" />
                    <NButton size="small" secondary @click="avatarPicker = !avatarPicker">选择头像</NButton>
                  </div>
                  <div v-if="avatarPicker" class="avatar-picker">
                    <button
                      v-for="preset in PORTRAIT_AVATAR_PRESETS"
                      :key="preset"
                      type="button"
                      class="avatar-preset-btn"
                      :class="{ selected: form.avatar === preset }"
                      :aria-label="`选择头像 ${preset}`"
                      :aria-pressed="form.avatar === preset"
                      :disabled="!canManage || saving"
                      @click="form.avatar = preset; avatarPicker = false"
                    >
                      <D2EmployeeAvatar :name="form.name || 'AI 员工'" :preset="preset" size="sm" />
                    </button>
                  </div>
                </div>
              </NFormItem>
            </div>

            <div class="form-block">
              <h4 class="fb-title">2. 执行引擎与基座模型</h4>
              <NFormItem label="执行引擎">
                <div class="engine-readonly-row">
                  <span class="engine-label">Pi</span>
                  <span class="field-note">生成参数由执行引擎管理</span>
                </div>
              </NFormItem>
              <NFormItem label="员工模型（必填）">
                <NSelect v-model:value="form.model_profile_id" :options="options" placeholder="选择已通过测试且已启用的模型" />
              </NFormItem>
              <NAlert v-if="!modelError && !models.some(m => m.enabled && m.validated)" type="warning" style="margin-bottom: 8px;">
                尚无可用模型，请先在资源库添加并测试模型。
                <NButton text type="primary" style="margin-left: 6px;" @click="emit('navigate', 'resources', 'models')">前往模型配置</NButton>
              </NAlert>
              <p class="field-hint">员工对话和任务执行都使用所选模型，不会回退到工作区默认模型。</p>
              <p v-if="officialApplies && selected?.payer" class="field-hint">官方模型费用由 {{ selected.payer.name }} 的积分账户支付。</p>
              <p v-if="officialApplies && !selected" class="field-hint">该员工使用官方模型产生的费用将从工作区所有者的积分账户扣除。</p>
              <div v-if="firstBinding" style="margin-top: 4px;">
                <NCheckbox v-model:checked="confirmBilling">确认该员工使用官方模型的费用从工作区所有者的积分账户扣除</NCheckbox>
              </div>
            </div>

            <div v-if="selected" class="form-block">
              <h4 class="fb-title">3. 岗位行为规范</h4>
              <NFormItem label="岗位行为规范（选填）">
                <NInput v-model:value="form.instruction" type="textarea" :autosize="{ minRows: 5, maxRows: 16 }" :maxlength="8000" placeholder="说明员工该做什么、如何协作及应遵循的限制。可以稍后补充。" />
              </NFormItem>
              <p class="field-hint">当前配置版本 v{{ selected.config_version }}。更新从主对话下一轮起生效，新任务和首次启动的待办使用最新配置。已启动的任务继续使用启动时的配置。</p>
            </div>
          </NForm>

          <AgentSetupPanel
            v-if="selected && canManage && activeDetailTab === 'overview'"
            :key="selected.id"
            :agent="selected"
            :can-manage="canManage"
            @dirty="v => setupDirty = v"
            @busy="v => setupBusy = v"
            @updated="v => { selected = v; load(); }"
            @navigate="(view, param, id) => emit('navigate', view, param, id)"
          />
        </template>

        <div v-if="activeDetailTab === 'persona'" class="detail-save-footer">
          <div class="drawer-footer-actions">
            <NButton :disabled="saving || setupBusy" @click="close">取消</NButton>
            <NButton v-if="canManage" type="primary" :disabled="!canSave" :loading="saving" @click="save">{{ selected ? '保存配置' : '创建员工' }}</NButton>
          </div>
        </div>


      <template v-if="selected && !detailLoading && !detailFailed">
        <section v-if="activeDetailTab==='overview'" class="overview-facts">
          <h3>员工状态</h3><p>{{selected.enabled?'已启用':'已停用'}} · {{selected.model_name}} · {{selected.model_available?'模型可用':'模型当前不可用'}}</p>
          <p>员工配置已保存不代表渠道已连接；请在渠道接入中核对实时状态。</p>
          <NButton @click="switchTab('channels')">查看渠道接入</NButton>
          <NButton @click="switchTab('packages')">查看能力包与授权</NButton>
        </section>
        <PlatformPackagesPanel v-if="activeDetailTab==='packages'" ref="packagesPanel" :employee-id="selected.id" />
        <section v-if="activeDetailTab==='channels'">
          <nav class="channel-tabs" aria-label="渠道选择">
            <NButton v-for="channel in channelProviders" :key="channel" :type="selectedChannel===channel&&!sharingOpen?'primary':'default'" @click="switchChannel(channel)"><D2ChannelLogo :platform="channel" :size="17" /> {{channelLabels[channel]}}</NButton>
            <NButton :type="sharingOpen?'primary':'default'" @click="navigateDetail(()=>{sharingOpen=true})">群共享策略</NButton>
          </nav>
          <PlatformSharingPanel v-if="sharingOpen" :workspace="activeWorkspaceId" :employee-id="selected.id" :can-manage="canManage" @close="closeFromPanel" />
          <PlatformFeishuPanel v-else :key="`${selected.id}:${selectedChannel}`" :provider="selectedChannel" :workspace="activeWorkspaceId" :employee-id="selected.id" :employee-name="selected.name" :can-manage="canManage" @close="closeFromPanel" />
        </section>
        <PlatformTriggersPanel v-if="activeDetailTab==='triggers'" :workspace="activeWorkspaceId" :employee-id="selected.id" :can-manage="canManage" @close="closeFromPanel" />
        <section v-if="activeDetailTab==='rules'">
          <PlatformUsagePanel :workspace="activeWorkspaceId" :employee-id="selected.id" :can-manage="canManage" @close="closeFromPanel" />
          <NAlert type="info">事项交接与人工确认尚未开放，当前仅配置员工统一使用范围与群触发规则。</NAlert>
        </section>
      </template>
    </div>
  </section>
</template>

<style scoped>
@import '../components/workforce/workforce-shared.css';
.employee-detail-container { display:flex;flex-direction:column;gap:20px;min-width:0; }
.detail-top-bar { display:flex;justify-content:space-between;gap:20px;align-items:center;flex-wrap:wrap; }
.detail-hero-left { display:flex;flex-direction:column;gap:16px;min-width:0; }
.back-link-btn { border:0;background:none;text-align:left;color:var(--id-text-secondary);cursor:pointer;padding:0;font:inherit; }
.emp-profile-summary,.pt-row,.detail-hero-actions,.channel-tabs { display:flex;align-items:center;gap:12px;flex-wrap:wrap; }
.profile-titles { min-width:0; }.profile-titles h2 {margin:0;font-size:24px;}.profile-titles p {margin:6px 0 0;color:var(--id-text-secondary);}
.detail-tabs {display:flex;gap:24px;border-bottom:1px solid var(--id-border-subtle);overflow-x:auto;}
.detail-tabs button {border:0;border-bottom:2px solid transparent;padding:14px 0;white-space:nowrap;background:none;color:var(--id-text-secondary);font:inherit;cursor:pointer;}
.detail-tabs button.active {color:var(--id-accent-primary);border-bottom-color:var(--id-accent-primary);font-weight:600;}
.channel-tabs {margin-bottom:20px;}.detail-save-footer {border-top:1px solid var(--id-border-subtle);padding-top:16px;}
.overview-facts {padding:20px;border:1px solid var(--id-border-subtle);border-radius:12px;}.overview-facts .n-button {margin-right:8px;}


.platform-employees {
  display: flex;
  flex-direction: column;
  gap: 16px;
}

.list-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 12px;
  flex-wrap: wrap;
  padding: 4px 0;
}

.header-titles {
  display: flex;
  flex-direction: column;
  gap: 2px;
}

.view-title {
  font-size: 20px;
  font-weight: 600;
  letter-spacing: -0.02em;
  color: var(--id-text-primary);
  margin: 0 0 4px 0;
}

.view-subtitle {
  font-size: 13px;
  color: var(--id-text-secondary);
  margin: 0;
}

.scope-notice {
  margin: 0;
}

.list-filter-bar {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 12px;
  flex-wrap: wrap;
  background: var(--id-bg-surface);
  padding: 10px 16px;
  border: 1px solid var(--id-border-subtle);
  border-radius: var(--id-radius-lg);
}

.filter-pills {
  display: flex;
  gap: 8px;
  flex-wrap: wrap;
}

.pill-btn {
  background: var(--id-bg-subtle);
  border: 1px solid var(--id-border-subtle);
  padding: 4px 12px;
  border-radius: var(--id-radius-pill);
  font-size: 12px;
  font-weight: 500;
  font-variant-numeric: tabular-nums;
  color: var(--id-text-secondary);
  cursor: pointer;
  transition: all var(--id-transition-fast);
}

.pill-btn:hover {
  background: var(--id-bg-muted);
  color: var(--id-text-primary);
}

.pill-btn.active {
  background: var(--id-text-primary);
  color: var(--id-bg-surface);
  border-color: var(--id-text-primary);
}

.filter-count-summary {
  font-size: 12px;
  color: var(--id-text-muted);
}

.empty-filter-card {
  padding: 48px 16px;
  background: var(--id-bg-surface);
  border: 1px solid var(--id-border-subtle);
  border-radius: var(--id-radius-lg);
}

.employee-cards-grid {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: 16px;
}

.emp-card {
  min-width: 0;
  border: 1px solid var(--id-border-subtle);
  border-radius: var(--id-radius-lg);
  background: var(--id-bg-surface);
  overflow: hidden;
  display: flex;
  flex-direction: column;
  transition: border-color var(--id-transition-fast), box-shadow var(--id-transition-fast);
}

.emp-card:hover {
  border-color: var(--id-border-strong);
  box-shadow: var(--id-shadow-md);
}

.ec-card-hit {
  cursor: pointer;
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.ec-card-hit:focus-visible {
  outline: none;
  box-shadow: var(--id-focus-ring);
  border-radius: var(--id-radius-sm);
}

.emp-card.emp-disabled {
  opacity: 0.78;
}

.ec-body {
  padding: 18px 18px 14px;
  flex: 1;
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.ec-top {
  display: flex;
  align-items: flex-start;
  gap: 10px;
}

.ec-meta {
  flex: 1;
  min-width: 0;
}

.ec-title-row {
  display: flex;
  align-items: center;
  gap: 6px;
  min-width: 0;
}

.ec-name {
  font-size: 14px;
  line-height: 20px;
  font-weight: 600;
  color: var(--id-text-primary);
  margin: 0;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.ai-label {
  flex-shrink: 0;
  font-size: 12px;
  line-height: 16px;
  font-weight: 600;
  border-radius: var(--id-radius-sm);
  background: var(--id-bg-subtle);
  border: 1px solid var(--id-border-subtle);
  color: var(--id-text-secondary);
  padding: 1px 6px;
}

.engine-label {
  flex-shrink: 0;
  font-size: 11px;
  line-height: 16px;
  font-weight: 500;
  font-family: var(--id-font-mono);
  border-radius: var(--id-radius-sm);
  background: var(--id-bg-muted);
  border: 1px solid var(--id-border-hairline);
  color: var(--id-text-primary);
  padding: 1px 6px;
}

.ec-code {
  display: inline-block;
  margin-top: 3px;
  font-size: 12px;
  color: var(--id-text-secondary);
  background: var(--id-bg-subtle);
  border: 1px solid var(--id-border-hairline);
  padding: 1px 6px;
  border-radius: var(--id-radius-sm);
  font-family: var(--id-font-mono);
}

.ec-role {
  min-height: 40px;
  margin: 0;
  color: var(--id-text-secondary);
  font-size: 13px;
  line-height: 20px;
  display: -webkit-box;
  -webkit-line-clamp: 2;
  -webkit-box-orient: vertical;
  overflow: hidden;
}

.ec-resources-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  flex-wrap: wrap;
  gap: 8px;
  padding-top: 10px;
  border-top: 1px solid var(--id-border-hairline);
}

.ec-model-pill {
  background: var(--id-bg-subtle);
  border: 1px solid var(--id-border-hairline);
  border-radius: var(--id-radius-sm);
  padding: 2px 8px;
  font-size: 12px;
  color: var(--id-text-secondary);
  overflow-wrap: anywhere;
}

.ec-model-warn {
  color: var(--id-status-warning);
  font-weight: 500;
}

.ec-config-shortcuts {
  display: flex;
  flex-direction: column;
  gap: 6px;
  padding-top: 6px;
}

.ec-shortcut-row {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
}

.ec-card-footer {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  padding: 12px 14px;
  background: var(--id-bg-subtle);
  border-top: 1px solid var(--id-border-hairline);
  flex-wrap: wrap;
}

.ec-context {
  margin: 0;
  display: inline-flex;
  align-items: center;
  gap: 6px;
  color: var(--id-text-secondary);
  font-size: 12px;
  line-height: 18px;
  flex: 1;
  min-width: 0;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.ec-context-dot {
  width: 6px;
  height: 6px;
  border-radius: 50%;
  background: var(--id-text-muted);
  flex-shrink: 0;
}

.ec-context-dot.is-active {
  background: var(--id-status-success-dot);
}

.ec-footer-actions {
  display: flex;
  align-items: center;
  gap: 6px;
  margin-left: auto;
  flex-wrap: wrap;
}

.emp-pagination {
  display: flex;
  justify-content: flex-end;
}

.drawer-heading {
  display: flex;
  justify-content: space-between;
  align-items: center;
  width: 100%;
  gap: 16px;
}

.drawer-title-group {
  display: flex;
  align-items: center;
  gap: 10px;
  min-width: 0;
}

.drawer-title-group strong {
  display: block;
  font-size: 15px;
  font-weight: 600;
  color: var(--id-text-primary);
}

.drawer-title-meta {
  display: flex;
  align-items: center;
  gap: 6px;
  margin-top: 2px;
}

.detail-sections {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
  margin-bottom: 16px;
}

.emp-drawer-form {
  display: flex;
  flex-direction: column;
  gap: 14px;
}

.form-block {
  background: var(--id-bg-surface);
  border: 1px solid var(--id-border-subtle);
  border-radius: var(--id-radius-lg);
  padding: 16px;
  display: flex;
  flex-direction: column;
  gap: 8px;
  min-width: 0;
}

.fb-title {
  font-size: 14px;
  font-weight: 600;
  color: var(--id-text-primary);
  margin: 0 0 6px 0;
}

.engine-readonly-row {
  display: flex;
  align-items: center;
  gap: 8px;
}

.avatar-current {
  display: flex;
  align-items: center;
  gap: 14px;
}

.avatar-picker {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(46px, 1fr));
  gap: 8px;
  margin-top: 12px;
  padding: 12px;
  background: var(--id-bg-subtle);
  border: 1px solid var(--id-border-subtle);
  border-radius: var(--id-radius-md);
}

.field-hint {
  font-size: 12px;
  color: var(--id-text-muted);
  margin: 0;
  line-height: 1.5;
}

.drawer-footer-actions {
  display: flex;
  justify-content: flex-end;
  gap: 8px;
  width: 100%;
}

@media (max-width: 1180px) {
  .employee-cards-grid {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }
}

@media (max-width: 760px) {
  .employee-cards-grid {
    grid-template-columns: 1fr;
    gap: 12px;
  }
  .list-filter-bar {
    padding: 10px 12px;
  }
  .form-block {
    padding: 12px;
  }
}
</style>

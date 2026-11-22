<script setup lang="ts">
import { computed, onUnmounted, ref, watch } from 'vue';
import {
  NAlert, NButton, NCheckbox, NDrawer, NDrawerContent,
  NForm, NFormItem, NInput, NSelect, NPagination, NSkeleton, NTag, useMessage
} from 'naive-ui';
import { activeWorkspaceId, D2ApiError } from '../api/client';
import { platformRequest } from '../api/platform';
import {
  packagesApi, defaultTriggerScope, defaultConversationScope, skillsApi, pluginsApi, allPages, workspacePath,
  type CapabilityPackage, type PackageInput, type Skill, type Plugin, type PluginAccount, type Page
} from '../api/capabilities';
import { useResourcePanel, useDraftClose } from '../composables/useResourcePanel';
import { triggersApi, triggerLabel, type Trigger } from '../api/triggers';
import PlatformSelectedSubjects from '../components/PlatformSelectedSubjects.vue';
import WorkScopeFields from '../components/WorkScopeFields.vue';
import PluginToolList from '../components/PluginToolList.vue';
import PackageEditorSections from '../components/PackageEditorSections.vue';
import PackageSceneRules from '../components/PackageSceneRules.vue';
import type { UsageSelector } from '../api/agent-usage';
import PlatformPluginAccount from './PlatformPluginAccount.vue';

const props = withDefaults(defineProps<{ employeeId?: string }>(), { employeeId: '' });
const message = useMessage();
const { items, loading, error, page, total, canManage, load } = useResourcePanel('能力包', (w, p, s) => packagesApi.list(w, props.employeeId, p, s));
const show = ref(false), saving = ref(false), busyAccounts = ref<Record<string, boolean>>({}), resourcesLoading = ref(false), formError = ref(''), resourcesError = ref('');
const existing = ref<CapabilityPackage>();
const dirtyAccounts = ref<Record<string, boolean>>({});
const fresh = (): PackageInput => ({
  name: '',
  description: '',
  enabled: true,
  trigger_scope: {...defaultTriggerScope(),enabled:true},
  conversation_scope: defaultConversationScope(true),
  scope: { mode: 'inherit_with_blacklist', allow_member_ids: [], deny_member_ids: [], allow: [], deny: [] },
  skills: [],
  plugins: []
});
const form = ref(fresh());
const baseline = ref('');
const dirty = computed(() => JSON.stringify(form.value) !== baseline.value || Object.values(dirtyAccounts.value).some(Boolean));
const busy = computed(() => saving.value || Object.values(busyAccounts.value).some(Boolean));
const selectorDenied = ref(false);
function scopeList(side: 'allow' | 'deny'): UsageSelector[] {
  return [...(form.value.scope[side] || []), ...form.value.scope[`${side}_member_ids`].map(id => ({ channel: 'web' as const, kind: 'user' as const, id }))];
}
const triggers = ref<Trigger[]>([]);
const triggerOptions = computed(() => {
  const opts = triggers.value.map(t => ({ label: triggerLabel(t), value: t.id }));
  for (const id of [...form.value.trigger_scope.allow_trigger_ids, ...form.value.trigger_scope.deny_trigger_ids]) {
    if (!opts.some(o => o.value === id)) opts.push({ label: `不可见或已失效：${id}`, value: id });
  }
  return opts;
});
const skills = ref<Skill[]>([]), plugins = ref<Plugin[]>([]), members = ref<{ id: string; name: string; email: string; status: string }[]>([]), templates = ref<CapabilityPackage[]>([]);
const templateID = ref<string | null>(null);
const accounts = ref<Record<string, PluginAccount | undefined>>({});
const skillPins = ref<Record<string,string>>({}), pluginPins = ref<Record<string,string>>({});
const skillNumbers = ref<Record<string,number>>({}), pluginVersions = ref<Record<string,Plugin>>({});
const preparing = ref('');
const upgradeReady = computed(() => !busy.value && !dirty.value && canManage.value && !!existing.value && !!props.employeeId);
function selectedPlugin(id:string, version:string) {
  return preparing.value === id ? pluginDefinition(id) : pluginVersions.value[version];
}
async function boundResource<T>(request:Promise<T>) {
  try {return await request;} catch(e) {if(e instanceof D2ApiError && e.status===404)return; throw e;}
}
let epoch = 0;
let controller: AbortController | undefined;
function discard() { preparing.value = ''; show.value = false; ++epoch; controller?.abort(); formError.value = ''; accounts.value = {}; busyAccounts.value = {}; dirtyAccounts.value = {}; }
async function loadResources() {
  const current = epoch, w = activeWorkspaceId.value;
  controller?.abort(); controller = new AbortController(); const signal = controller.signal;
  resourcesLoading.value = true; resourcesError.value = '';
  skills.value = []; plugins.value = []; members.value = []; triggers.value = [];
  try {
    const [s, p, m, t, tr, packs] = await Promise.all([
      allPages(n => skillsApi.list(w, n, signal)),
      allPages(n => pluginsApi.list(w, n, signal)),
      props.employeeId ? Promise.resolve([]) : allPages(n => platformRequest<Page<{ id: string; name: string; email: string; status: string }>>(`${workspacePath(w)}/members?page=${n}&page_size=20`, { signal })),
      props.employeeId ? allPages(n => packagesApi.list(w, '', n, signal)) : Promise.resolve([]),
      props.employeeId && canManage.value ? triggersApi.list(w, props.employeeId, signal) : Promise.resolve({ items: [] }),
      props.employeeId ? allPages(n => packagesApi.list(w, props.employeeId, n, signal)) : Promise.resolve([])
    ]);
    if (current !== epoch || w !== activeWorkspaceId.value) return;
    const boundSkills = form.value.skills.filter(b => !s.some(def => def.id===b.skill_id));
    const boundPlugins = form.value.plugins.filter(b => !p.some(def => def.id===b.plugin_id));
    const [extraSkills,extraPlugins]=await Promise.all([
      Promise.all(boundSkills.map(b=>boundResource(skillsApi.get(w,b.skill_id,signal)))),
      Promise.all(boundPlugins.map(b=>boundResource(pluginsApi.get(w,b.plugin_id,signal))))
    ]);
    for(const def of extraSkills) if(def) s.push(def);
    for(const def of extraPlugins) if(def) p.push(def);
    const skillBindings=Object.fromEntries(packs.flatMap(pack=>pack.skills.map(b=>[b.skill_id,b.version_id])));
    const pluginBindings=Object.fromEntries(packs.flatMap(pack=>pack.plugins.map(b=>[b.plugin_id,b.version_id])));
    const numbers:Record<string,number>={}, versions:Record<string,Plugin>={};
    await Promise.all([
      ...s.map(async def=>{
        numbers[def.current_version_id]=def.current_version.sequence;
        const pin=skillBindings[def.id] || form.value.skills.find(b=>b.skill_id===def.id)?.version_id;
        if(pin && pin!==def.current_version_id) for(const v of (await skillsApi.versions(w,def.id)).items) numbers[v.id]=v.sequence;
      }),
      ...p.map(async def=>{
        versions[def.version_id]=def;
        const pin=pluginBindings[def.id] || form.value.plugins.find(b=>b.plugin_id===def.id)?.version_id;
        if(pin && pin!==def.version_id) for(const v of (await pluginsApi.versions(w,def.id)).items) versions[v.version_id]=v;
      })
    ]);
    if(current!==epoch || signal.aborted)return;
    skillPins.value=skillBindings; pluginPins.value=pluginBindings; skillNumbers.value=numbers; pluginVersions.value=versions;
    skills.value = s; plugins.value = p; members.value = m; templates.value = t; triggers.value = tr.items;
  } catch (e) { if (current === epoch) resourcesError.value = e instanceof Error ? e.message : '资源读取失败'; }
  finally { if (current === epoch) resourcesLoading.value = false; }
}
function open(item?: CapabilityPackage) {
  discard(); selectorDenied.value = false; existing.value = item;
  form.value = item ? JSON.parse(JSON.stringify({ name: item.name, description: item.description, enabled: item.enabled, scope: item.scope, work_scope:item.work_scope, conversation_scope:item.conversation_scope, trigger_scope: item.trigger_scope || defaultTriggerScope(), skills: item.skills, plugins: item.plugins })) : fresh();
  baseline.value = JSON.stringify(form.value); show.value = true; void loadResources();
}
const skillDefinition = (id: string) => skills.value.find(s => s.id === id);
const pluginDefinition = (id: string) => plugins.value.find(p => p.id === id);
async function upgrade(kind:'skill'|'plugin',id:string,from:string,to:string) {
  const pack=existing.value;
  if(!upgradeReady.value || !pack)return;
  const current=epoch,w=activeWorkspaceId.value,a=props.employeeId;
  saving.value=true; formError.value='';
  try {
    await packagesApi.upgrade(w,a,kind,id,from,to);
    const updated=await packagesApi.get(w,a,pack.id);
    if(current!==epoch)return;
    open(updated);
    message.success('员工版本已升级，下次执行时生效');
    await load();
  }catch(e){if(current===epoch)formError.value=e instanceof Error?e.message:'升级失败';}
  finally{saving.value=false;}
}
const addSkillID = ref<string | null>(null), addPluginID = ref<string | null>(null);
function addSkill(id: string | null) {
  if (!id) return;
  const s = skills.value.find(i => i.id === id);
  if (!s || form.value.skills.some(i => i.skill_id === id)) return;
  form.value.skills.push({ skill_id: id, version_id: skillPins.value[id] || s.current_version_id });
  addSkillID.value = null;
}
function addPlugin(id: string | null) {
  if (!id) return;
  const p = plugins.value.find(i => i.id === id);
  if (!p || form.value.plugins.some(i => i.plugin_id === id)) return;
  form.value.plugins.push({ plugin_id: id, version_id: pluginPins.value[id] || p.version_id });
  addPluginID.value = null;
}
const invalidBindings = computed(() => form.value.skills.some(b => !skills.value.some(s => s.id === b.skill_id)) || form.value.plugins.some(b => !plugins.value.some(p => p.id === b.plugin_id)));
const referencesChanged = computed(() => !existing.value || JSON.stringify(form.value.skills) !== JSON.stringify(existing.value.skills) || JSON.stringify(form.value.plugins) !== JSON.stringify(existing.value.plugins) || (!existing.value.enabled && form.value.enabled));
const canSave = computed(() => canManage.value && !selectorDenied.value && (!existing.value || dirty.value) && !busy.value && !Object.values(dirtyAccounts.value).some(Boolean) && !resourcesLoading.value && !resourcesError.value && (!referencesChanged.value || !invalidBindings.value) && !!form.value.name.trim());
async function save() {
  if (!canSave.value) return false;
  const current = epoch, w = activeWorkspaceId.value;
  saving.value = true; formError.value = '';
  try {
    await packagesApi.save(w, props.employeeId, { ...form.value, name: form.value.name.trim() }, existing.value);
    if (current !== epoch || w !== activeWorkspaceId.value) return false;
    discard(); message.success('能力包配置已保存；实际使用仍受运行时权限校验'); await load(); return true;
  } catch (e) { if (current === epoch) formError.value = e instanceof Error ? e.message : '保存失败'; return false; }
  finally { saving.value = false; }
}
const close = useDraftClose('能力包配置', show, dirty, busy, discard, save);
function copyTemplate() {
  const t = templates.value.find(t => t.id === templateID.value);
  if (!t || busy.value) return;
  form.value = {
    name: t.name,
    description: t.description,
    enabled: t.enabled,
    scope: JSON.parse(JSON.stringify(t.scope)),
    ...(t.work_scope ? {work_scope: JSON.parse(JSON.stringify(t.work_scope))} : {}),
    conversation_scope: t.conversation_scope ? JSON.parse(JSON.stringify(t.conversation_scope)) : null,
    trigger_scope: JSON.parse(JSON.stringify(t.trigger_scope || defaultTriggerScope())),
    skills: t.skills.map(b => ({ skill_id: b.skill_id, version_id: skillPins.value[b.skill_id] || skillDefinition(b.skill_id)?.current_version_id || b.version_id })),
    plugins: t.plugins.map(b => ({ plugin_id: b.plugin_id, version_id: pluginPins.value[b.plugin_id] || pluginDefinition(b.plugin_id)?.version_id || b.version_id }))
  };
  accounts.value = {};
  void loadResources();
  message.info('模板资源已填入草稿。');
}
defineExpose({ hasOpenEditor: () => show.value });

watch(() => props.employeeId, () => { discard(); page.value = 1; void load(); });
watch(activeWorkspaceId, () => { discard(); saving.value = false; });
onUnmounted(discard);
</script>

<template>
  <section class="platform-resources" :class="{ 'employee-packages': employeeId }">
    <div class="resource-toolbar">
      <div class="resource-toolbar-copy">
        <p class="resource-hint">
          <template v-if="employeeId"><strong>能力包</strong> 包含技能、插件，以及会话、触发器与事项交接的使用范围。首次添加时使用最新版。手动升级后，该员工的所有能力包会一起更新，正在执行的一轮会继续用原版本。</template>
          <template v-else>工作区模板复制到员工后独立，不随模板修改自动变化。</template>
        </p>
        <p v-if="employeeId" class="resource-hint-sm">运行时仍会核对来源范围、资源版本和账号状态。</p>
      </div>
      <NButton v-if="canManage" type="primary" size="small" @click="open()">{{ employeeId ? '新建能力包' : '新建能力包模板' }}</NButton>
    </div>

    <NAlert v-if="!employeeId" type="info">能力包启用与配置保存不代表每个任务都可使用；运行时仍核对来源范围、资源版本及账号状态。</NAlert>

    <div v-if="loading" class="resource-loading">
      <div v-for="n in 3" :key="'pkg-sk-' + n" class="resource-skeleton-card">
        <NSkeleton text style="width: 55%; height: 18px;" />
        <NSkeleton style="height: 60px; border-radius: 6px;" />
        <NSkeleton text style="width: 80%; height: 22px;" />
      </div>
    </div>
    <NAlert v-else-if="error" type="error">
      {{ error }}
      <NButton size="small" style="margin-left: 8px;" @click="load">重试</NButton>
    </NAlert>
    <template v-else>
      <p v-if="!items.length" class="resource-empty">尚未配置能力包。</p>
      <div v-else class="resource-grid">
        <article
          v-for="item in items"
          :key="item.id"
          class="resource-card"
          :class="{ 'is-disabled': !item.enabled }"
        >
          <template v-if="employeeId">
            <header class="package-card-header">
              <div class="package-card-title">
                <h4 class="resource-card-title">{{ item.name }}</h4>
                <NTag size="tiny" :type="item.enabled ? 'success' : 'default'">{{ item.enabled ? '已启用' : '已停用' }}</NTag>
              </div>
              <NButton size="tiny" secondary @click="open(item)">{{ canManage ? '配置能力包' : '查看能力包' }}</NButton>
            </header>
            <p class="resource-desc">{{ item.description || '暂无能力包描述' }}</p>
            <div class="package-card-chips">
              <span>技能 {{ item.skills.length }} 个</span>
              <span>插件 {{ item.plugins.length }} 个</span>
              <span>配置 v{{ item.version }}</span>
              <span v-if="item.conversation_scope">场景：{{ [item.conversation_scope.private.enabled && '私聊', item.conversation_scope.group.enabled && '群聊', item.trigger_scope?.enabled !== false && '触发器'].filter(Boolean).join(' / ') || '未开启' }}</span>
              <span v-else>旧版范围</span>
            </div>
          </template>
          <template v-else>
            <header class="resource-card-header">
              <div class="resource-title-group">
                <div class="resource-title-row">
                  <h4 class="resource-card-title" :title="item.name">{{ item.name }}</h4>
                  <span class="mono-pill tabular-num">v{{ item.version }}</span>
                </div>
                <div class="resource-badges-row">
                  <span class="neutral-pill tabular-num">{{ item.skills.length }} 个 Skill</span>
                  <span class="neutral-pill tabular-num">{{ item.plugins.length }} 个插件</span>
                </div>
              </div>
              <span class="status-pill" :class="item.enabled ? 'status-pill--success' : 'status-pill--muted'">
                <span class="status-dot" :class="item.enabled ? 'dot--success' : 'dot--muted'"></span>
                <span>{{ item.enabled ? '已启用' : '已停用' }}</span>
              </span>
            </header>

            <p class="resource-desc">{{ item.description || '暂无能力包描述' }}</p>

            <footer class="resource-card-footer">
              <span class="resource-hint-sm tabular-num">配置 v{{ item.version }}</span>
              <div class="resource-actions">
                <NButton size="tiny" secondary @click="open(item)">{{ canManage ? '配置能力包' : '查看能力包' }}</NButton>
              </div>
            </footer>
          </template>
        </article>
      </div>
      <div v-if="total > 20" class="resource-pagination">
        <NPagination v-model:page="page" :page-size="20" :item-count="total" />
      </div>
    </template>

    <NDrawer :show="show" :width="employeeId ? 'min(640px, 95vw)' : 'min(820px,96vw)'" :mask-closable="false" :close-on-esc="false">
      <NDrawerContent :class="{ 'employee-package-drawer': employeeId }">
        <template #header>
          <div class="resource-heading">
            <strong>{{ existing ? '配置能力包' : employeeId ? '新建能力包' : '新建能力包模板' }}<template v-if="employeeId && existing"> · {{ existing.name }}</template></strong>
            <NButton size="small" quaternary :disabled="busy" @click="close">关闭</NButton>
          </div>
        </template>

        <p v-if="resourcesLoading" class="resource-hint-sm" style="margin-bottom: 12px;">正在读取资源与成员…</p>
        <NAlert v-if="resourcesError" type="error" class="resource-alert">
          {{ resourcesError }}
          <NButton size="small" style="margin-left: 8px;" @click="loadResources">重试</NButton>
        </NAlert>
        <NAlert v-if="!resourcesLoading && invalidBindings" type="error" class="resource-alert">部分已绑定资源不可见或不存在，请核对授权和资源状态；不会自动移除。</NAlert>

        <div v-if="employeeId && !existing && templates.length && canManage" class="resource-fieldset package-template">
          <div class="resource-fieldset-head">
            <strong>从工作区能力包模板快速填入</strong>
          </div>
          <div class="resource-inline-row">
            <NSelect v-model:value="templateID" :disabled="busy" :options="templates.map(t => ({ label: t.name, value: t.id }))" placeholder="选择工作区能力包模板" style="flex: 1; min-width: 200px;" />
            <NButton size="small" secondary :disabled="!templateID || busy" @click="copyTemplate">从模板填写</NButton>
          </div>
          <p class="resource-hint-sm">使用模板的资源和使用范围创建员工能力包。</p>
        </div>

        <NForm label-placement="top" :disabled="!canManage || busy || resourcesLoading">
          <PackageEditorSections :employee="!!employeeId">
            <template #basics>
              <NFormItem label="能力包名称"><NInput v-model:value="form.name" :maxlength="100" /></NFormItem>
              <NFormItem label="说明"><NInput v-model:value="form.description" :maxlength="2000" /></NFormItem>
              <div class="package-enabled">
                <NCheckbox v-model:checked="form.enabled">启用能力包</NCheckbox>
              </div>
            </template>

            <template #scope>
              <PackageSceneRules :workspace="activeWorkspaceId" :employee-id="employeeId" :conversation="form.conversation_scope" :trigger="form.trigger_scope" :disabled="busy || !canManage || selectorDenied || resourcesLoading" :trigger-options="triggerOptions" :members="members" @update:conversation="v=>form.conversation_scope=v" @update:trigger="v=>form.trigger_scope=v" @revoked="selectorDenied=true; formError='选择权限已失效，请重新打开配置。'" />
              <WorkScopeFields :workspace="activeWorkspaceId" :employee="employeeId" :scope="form.work_scope" :disabled="busy || !canManage" @update="scope=>form.work_scope=scope"/>
              <fieldset v-if="!form.conversation_scope" class="resource-fieldset">
                <legend>旧版统一使用范围（只读保留）</legend>
                <p class="resource-hint-sm">模式：{{form.scope.mode==='inherit_with_blacklist'?'继承员工范围 + 黑名单':'白名单 + 黑名单'}}。四渠道身份独立，旧规则不会因编辑名称或资源而丢失。</p>
                <template v-for="side in (['allow','deny'] as const)" :key="side">
                  <h4 class="resource-subhead">{{side==='allow'?'原允许名单':'原拒绝名单'}}</h4>
                  <PlatformSelectedSubjects v-if="employeeId" :workspace="activeWorkspaceId" :employee-id="employeeId" :items="scopeList(side)" purpose="capability" disabled />
                  <pre v-else>{{scopeList(side)}}</pre>
                </template>
              </fieldset>
            </template>

            <template #resources>
              <fieldset class="resource-fieldset package-resource-set">
                <legend>Skill 技能</legend>
                <div v-for="(b, index) in form.skills" :key="b.skill_id" class="resource-fieldset">
                  <div class="resource-fieldset-head">
                    <strong>{{ skills.find(s => s.id === b.skill_id)?.name || b.skill_id }}</strong>
                    <NButton v-if="canManage" size="tiny" type="error" quaternary :disabled="busy" @click="form.skills.splice(index, 1)">移除 Skill</NButton>
                  </div>
                  <p class="resource-hint-sm">{{ skillNumbers[b.version_id] ? '当前使用版本 ' + skillNumbers[b.version_id] : '资源不可用' }} {{ skillDefinition(b.skill_id)?.listed === false ? ' · 已下架' : '' }}</p>
                  <NButton v-if="employeeId && existing && canManage && skillDefinition(b.skill_id)?.enabled && b.version_id !== skillDefinition(b.skill_id)?.current_version_id" size="tiny" secondary :disabled="!upgradeReady || !existing.skills.some(pin=>pin.skill_id===b.skill_id)" @click="upgrade('skill',b.skill_id,b.version_id,skillDefinition(b.skill_id)!.current_version_id)">升级至版本 {{ skillDefinition(b.skill_id)!.current_version.sequence }}</NButton>
                </div>
                <NSelect v-if="canManage" v-model:value="addSkillID" :options="skills.filter(s => s.enabled && s.listed !== false && !form.skills.some(b => b.skill_id === s.id)).map(s => ({ label: (s.scope === 'official' ? '官方 · ' : '空间 · ') + s.name, value: s.id }))" placeholder="添加 Skill" @update:value="addSkill" />
              </fieldset>

              <fieldset class="resource-fieldset package-resource-set">
                <legend>插件</legend>
                <div v-for="(b, index) in form.plugins" :key="b.plugin_id" class="resource-fieldset">
                  <div class="resource-fieldset-head">
                    <strong>{{ plugins.find(p => p.id === b.plugin_id)?.name || b.plugin_id }}</strong>
                    <NButton v-if="canManage" size="tiny" type="error" quaternary :disabled="busy" @click="form.plugins.splice(index, 1)">移除插件</NButton>
                  </div>
                  <p class="resource-hint-sm">{{ pluginVersions[b.version_id] ? '当前使用版本 ' + pluginVersions[b.version_id]?.definition_version : '资源不可用' }} {{ pluginDefinition(b.plugin_id)?.listed === false ? ' · 已下架' : '' }}</p>
                  <template v-if="employeeId && existing && canManage && pluginDefinition(b.plugin_id)?.enabled && b.version_id !== pluginDefinition(b.plugin_id)?.version_id">
                    <NButton v-if="preparing!==b.plugin_id" size="tiny" secondary :disabled="!upgradeReady || !existing.plugins.some(pin=>pin.plugin_id===b.plugin_id)" @click="preparing=b.plugin_id">升级至版本 {{ pluginDefinition(b.plugin_id)!.definition_version }}</NButton>
                    <div v-else>
                      <p>确认后，此员工使用该插件的所有能力包都会升级。{{ pluginDefinition(b.plugin_id)!.auth_type === 'none' ? '' : '请先连接新版本的账号。' }}</p>
                      <NButton size="small" type="primary" :disabled="!upgradeReady" @click="upgrade('plugin',b.plugin_id,b.version_id,pluginDefinition(b.plugin_id)!.version_id)">确认升级</NButton>
                      <NButton size="small" quaternary :disabled="busy || dirty" @click="preparing=''">取消升级</NButton>
                    </div>
                  </template>
                  <PlatformPluginAccount
                    v-if="employeeId && !resourcesLoading && selectedPlugin(b.plugin_id,b.version_id)"
                    :key="`${employeeId}:${b.plugin_id}`"
                    :employee-id="employeeId"
                    :plugin="selectedPlugin(b.plugin_id,b.version_id)!"
                    :allow-source-change="preparing!==b.plugin_id"
                    :version-id="selectedPlugin(b.plugin_id,b.version_id)!.version_id"
                    :can-manage="canManage && !saving"
                    @account="value => accounts[b.plugin_id] = value"
                    @busy="value => busyAccounts[b.plugin_id] = value"
                    @dirty="value => dirtyAccounts[b.plugin_id] = value"
                  />
                  <PluginToolList v-if="employeeId" :account="accounts[b.plugin_id]" />
                </div>
                <NSelect v-if="canManage" v-model:value="addPluginID" :options="plugins.filter(p => p.enabled && p.listed !== false && !form.plugins.some(b => b.plugin_id === p.id)).map(p => ({ label: (p.scope === 'official' ? '官方 · ' : '空间 · ') + p.name, value: p.id }))" placeholder="添加插件" @update:value="addPlugin" />
              </fieldset>
            </template>
          </PackageEditorSections>
        </NForm>

        <NAlert v-if="formError" class="resource-alert" type="error">
          {{ formError }}
          <p style="margin: 4px 0 0 0;">如配置发生冲突，请保留当前草稿，核对最新版本后再操作。</p>
        </NAlert>
        <template #footer>
          <div class="resource-drawer-footer" style="align-items: center;">
            <span v-if="canManage && Object.values(dirtyAccounts).some(Boolean)" class="resource-hint-sm" style="margin-right: auto; color: var(--id-status-warning);">
              请先在上方插件卡片中保存账号配置
            </span>
            <NButton v-if="canManage" type="primary" :loading="saving" :disabled="!canSave" @click="save">保存能力包</NButton>
          </div>
        </template>
      </NDrawerContent>
    </NDrawer>
  </section>
</template>

<style src="../styles/platform-resources.css"></style>

<style scoped>
.employee-packages {
  padding: 16px 0 0;
}
.employee-packages .resource-toolbar {
  flex-direction: row;
  align-items: center;
  gap: 12px;
  padding: 10px 14px;
  background: var(--id-bg-subtle);
  border-radius: var(--id-radius-md);
}
.employee-packages .resource-hint,
.employee-packages .resource-desc {
  font-size: 12px;
  line-height: 1.5;
}
.employee-packages .resource-grid,
.employee-packages .resource-loading {
  grid-template-columns: minmax(0, 1fr);
  gap: 12px;
}
.employee-packages .resource-card,
.employee-packages .resource-skeleton-card {
  padding: 14px 16px;
  gap: 8px;
}
.employee-packages .resource-card:hover { box-shadow: none; }
.employee-packages .resource-card.is-disabled {
  opacity: .68;
  background: var(--id-bg-subtle);
}
.package-card-header,
.package-card-title,
.package-card-chips {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 8px;
  min-width: 0;
}
.package-card-header {
  justify-content: space-between;
  gap: 12px;
}
.package-card-chips { margin-top: 2px; }
.package-card-chips span {
  padding: 3px 9px;
  background: var(--id-bg-muted);
  border: 1px solid var(--id-border-subtle);
  border-radius: var(--id-radius-sm);
  color: var(--id-text-secondary);
  font: 500 12px/1.5 var(--id-font-mono);
  font-variant-numeric: tabular-nums;
}
.employee-packages .resource-empty {
  padding: 24px;
  border: 1px dashed var(--id-border-strong);
  background: var(--id-bg-subtle);
  color: var(--id-text-secondary);
}
.package-enabled { margin-bottom: 16px; }
.employee-package-drawer .package-enabled { margin: 0; }
.employee-package-drawer .resource-heading strong {
  min-width: 0;
  overflow-wrap: anywhere;
}
.employee-package-drawer :deep(fieldset) { min-width: 0; }
.employee-package-drawer .resource-fieldset {
  margin-bottom: 0;
  padding: 10px 12px;
  border-radius: var(--id-radius-md);
}
.employee-package-drawer .resource-fieldset-head { overflow-wrap: anywhere; }
.employee-package-drawer .package-template {
  margin-bottom: 16px;
  background: var(--id-bg-subtle);
}
.employee-package-drawer .package-resource-set {
  padding: 0;
  border: 0;
}
.employee-package-drawer .package-resource-set > legend { padding: 0 0 6px; }
</style>

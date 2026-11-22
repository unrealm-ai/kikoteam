<script setup lang="ts">
import { computed, onUnmounted, ref, watch } from 'vue';
import {
  NAlert, NButton, NDrawer, NDrawerContent, NForm, NFormItem, NInput,
  NEmpty, NSelect, NPagination, NSkeleton, useMessage
} from 'naive-ui';
import { activeWorkspaceId } from '../api/client';
import { pluginsApi, type Plugin, type PluginConfig } from '../api/capabilities';
import { useResourcePanel, useDraftClose } from '../composables/useResourcePanel';

import PluginSharedConnections from './PluginSharedConnections.vue';
import ResourceCatalogToolbar from '../components/ResourceCatalogToolbar.vue';
import ResourceOverview from '../components/ResourceOverview.vue';
import ResourceCard from '../components/ResourceCard.vue';
import { pluginAuthLabel, pluginConnectionLabel, type SummaryLanguage } from '../api/resource-display';
import CatalogDrawer from './ResourceCatalogDrawer.vue';
const catalogItem=ref<Plugin>();
const catalogScope=ref('all');
const query = ref('');
const language = defineModel<SummaryLanguage>('language', { default: 'zh' });
const overview = ref<Plugin>();
const sharedOpen=ref(false),sharedPlugin=ref<Plugin>();
const oauthFields=ref({issuer:'',client_id:'',client_secret:'',scopes:'',resource:''});
const props = defineProps<{ platformAdmin?:boolean; targetId?: string | undefined }>();
const message = useMessage();
const { items, loading, error, page, total, canManage, load } = useResourcePanel('插件', (w,p,signal)=>pluginsApi.list(w,p,signal,catalogScope.value,query.value.trim()));
watch([catalogScope, query],()=>{if(page.value!==1)page.value=1;else void load()});
const show = ref(false), saving = ref(false), formError = ref('');
const existing = ref<Plugin>();
const fresh = (): PluginConfig => ({ name: '', description: '', endpoint: '', auth_type: 'none', header_name: '' });
const form = ref(fresh());
const baseline = ref('');
const dirty = computed(() => JSON.stringify(form.value) !== baseline.value);
let epoch = 0;
const authOptions = [
  { label: '无需认证', value: 'none' },
  { label: 'Bearer Token', value: 'bearer' },
  { label: 'API Key Header', value: 'api_key' },{label:'OAuth 2.0',value:'oauth'}
];
function discard() { show.value = false; ++epoch; formError.value = ''; oauthFields.value.client_secret=''; }
function open(p?: Plugin) {
  discard();
  existing.value = p;
  oauthFields.value={issuer:p?.oauth?.issuer??'',client_id:p?.oauth?.client_id??'',client_secret:'',scopes:p?.oauth?.scopes??'',resource:p?.oauth?.resource??''};
  form.value = p ? { name: p.name, description: p.description, endpoint: p.endpoint, auth_type: p.auth_type, header_name: p.header_name } : fresh();
  baseline.value = JSON.stringify(form.value);
  show.value = true;
}
const canSave = computed(() => canManage.value && !saving.value && !!form.value.name.trim() && /^https:\/\//i.test(form.value.endpoint.trim()) && (form.value.auth_type !== 'api_key' || !!form.value.header_name.trim()));
async function save() {
  if (!canSave.value) return false;
  const current = epoch, w = activeWorkspaceId.value;
  saving.value = true; formError.value = '';
  try {
    const o=oauthFields.value, prior=existing.value?.oauth;
    const changed=form.value.auth_type==='oauth' && (!prior || !!o.client_secret || o.issuer!==prior.issuer || o.client_id!==prior.client_id || o.scopes!==prior.scopes || o.resource!==prior.resource || existing.value?.endpoint!==form.value.endpoint);
    await pluginsApi.save(w, { ...form.value, ...(changed?{oauth:o}:{}), name: form.value.name.trim(), endpoint: form.value.endpoint.trim(), header_name: form.value.auth_type === 'api_key' ? form.value.header_name.trim() : '' }, existing.value);
    if (current !== epoch || w !== activeWorkspaceId.value) return false;
    discard(); message.success('插件定义已保存，账号请在员工能力包中接入'); await load(); return true;
  } catch (e) { if (current === epoch) formError.value = e instanceof Error ? e.message : '保存失败'; return false; }
  finally { saving.value = false; }
}
const close = useDraftClose('插件定义', show, dirty, saving, discard, save);
const actionBusy = ref(false);
async function toggle(p: Plugin) {
  if (!canManage.value || actionBusy.value) return;
  const w = activeWorkspaceId.value; actionBusy.value = true;
  try {
    await pluginsApi.status(w, p);
    if (w === activeWorkspaceId.value) { message.success('插件状态已更新'); await load(); }
  } catch (e) { if (w === activeWorkspaceId.value) message.error(e instanceof Error ? e.message : '更新失败'); }
  finally { actionBusy.value = false; }
}
const versionsLoading = ref(false);
const versions = ref<Plugin[]>([]), versionError = ref(''), viewing = ref(false);
let historyEpoch = 0;
async function history(pluginId: string) {
  const current = ++historyEpoch, w = activeWorkspaceId.value;
  viewing.value = true; versionsLoading.value = true; versions.value = []; versionError.value = '';
  try {
    const r = await pluginsApi.versions(w, pluginId);
    if (current === historyEpoch && w === activeWorkspaceId.value) versions.value = r.items;
  } catch (e) { if (current === historyEpoch) versionError.value = e instanceof Error ? e.message : '历史读取失败'; }
  finally { if (current === historyEpoch) versionsLoading.value = false; }
}
function closeHistory(value: boolean) { viewing.value = value; if (!value) ++historyEpoch; }
watch(activeWorkspaceId, () => { overview.value=undefined; sharedOpen.value=false;sharedPlugin.value=undefined; catalogItem.value=undefined; discard(); saving.value = false; viewing.value = false; ++historyEpoch; });
watch(() => [activeWorkspaceId.value, props.targetId] as const, ([w,id]) => {if (w && id) void history(id);}, {immediate:true});
onUnmounted(() => { ++epoch; ++historyEpoch; });
</script>

<template>
  <section class="tab-content-panel">
    <ResourceCatalogToolbar v-model:scope="catalogScope" v-model:query="query" v-model:language="language"
      kind="插件" :platform-admin="!!platformAdmin" :total="total" :loading="loading">
      <NButton v-if="canManage" size="small" @click="sharedPlugin=undefined; sharedOpen=true">空间连接</NButton>
      <NButton v-if="canManage" size="small" type="primary" @click="open()">+ 新增插件</NButton>
    </ResourceCatalogToolbar>
    <ResourceOverview v-if="overview" :item="overview" :language="language" @close="overview=undefined">
        <NButton v-if="canManage || platformAdmin" type="primary" size="small" @click="catalogItem=overview; overview=undefined">分配与管理</NButton>
        <template #connection>
          <section><h3>账号连接</h3><p>{{ pluginConnectionLabel(overview) }}</p>
            <NButton v-if="canManage && overview.auth_type !== 'none'" size="small" @click="sharedPlugin=overview; overview=undefined; sharedOpen=true">配置共享连接</NButton>
          </section>
        </template>
        <NButton size="small" @click="history(overview.id); overview=undefined">定义版本</NButton>
        <NButton v-if="canManage && overview.scope !== 'official'" size="small" @click="open(overview); overview=undefined">编辑插件</NButton>
        <NButton v-if="canManage && overview.scope !== 'official'" size="small" :disabled="actionBusy" @click="toggle(overview); overview=undefined">{{ overview.enabled ? '停用' : '启用' }}</NButton>
    </ResourceOverview>
    <PluginSharedConnections v-if="sharedOpen && canManage" :plugin="sharedPlugin" @close="sharedOpen=false; load()" />
    <CatalogDrawer v-if="catalogItem" :key="catalogItem.id" kind="plugin" :item="catalogItem" :platform-admin="!!platformAdmin" :can-manage="canManage" @close="catalogItem=undefined" @changed="load" />

    <div v-if="loading" class="resource-loading">
      <div v-for="n in 3" :key="'pl-sk-' + n" class="resource-skeleton-card">
        <NSkeleton text style="width: 55%; height: 18px;" />
        <NSkeleton style="height: 68px; border-radius: 6px;" />
        <NSkeleton text style="width: 80%; height: 22px;" />
      </div>
    </div>
    <NAlert v-else-if="error" type="error">
      {{ error }}
      <NButton size="small" style="margin-left: 8px;" @click="load">重试</NButton>
    </NAlert>
    <template v-else>
      <NAlert v-if="!canManage" type="info">当前仅可查看，修改需要管理员权限。</NAlert>
      <div v-if="!items.length" class="empty-panel-box">
        <NEmpty description="暂无匹配的插件">
          <template v-if="canManage" #extra><NButton size="small" type="primary" @click="open()">+ 新增插件</NButton></template>
        </NEmpty>
      </div>
      <div v-else class="catalog-card-grid">
        <ResourceCard v-for="srv in items" :key="srv.id" :item="srv" :language="language"
          :class="{ highlighted: targetId === srv.id }" :global="catalogScope === 'manage'" @detail="overview=srv" />
      </div>
      <div v-if="total > 20" class="resource-pagination">
        <NPagination v-model:page="page" :page-size="20" :item-count="total" />
      </div>
    </template>

    <NDrawer class="library-drawer" :show="show" :width="'min(600px,95vw)'" :mask-closable="false" :close-on-esc="false">
      <NDrawerContent>
        <template #header>
          <div class="resource-heading">
            <strong>{{ existing ? '编辑插件' : '新增空间插件' }}</strong>
            <NButton size="small" quaternary :disabled="saving" @click="close">关闭</NButton>
          </div>
        </template>
        <NAlert class="resource-alert" type="info">这里不填写账号密钥。定义保存不表示服务已连接；请在员工能力包中配置账号并测试。</NAlert>
        <NForm label-placement="top" :disabled="saving">
          <NFormItem label="插件名称"><NInput v-model:value="form.name" :maxlength="100" /></NFormItem>
          <NFormItem label="说明"><NInput v-model:value="form.description" :maxlength="2000" /></NFormItem>
          <NFormItem label="远程 MCP 地址（HTTPS）"><NInput v-model:value="form.endpoint" placeholder="https://example.com/mcp" /></NFormItem>
          <NFormItem label="认证方式"><NSelect v-model:value="form.auth_type" :options="authOptions" /></NFormItem>
          <template v-if="form.auth_type==='oauth'">
          <NFormItem label="OAuth 授权服务器（issuer）"><NInput v-model:value="oauthFields.issuer" /></NFormItem>
          <NFormItem label="客户端 ID"><NInput v-model:value="oauthFields.client_id" /></NFormItem>
          <NFormItem label="客户端密钥（机密客户端修改 OAuth 配置时填写）"><NInput v-model:value="oauthFields.client_secret" type="password" autocomplete="new-password" /></NFormItem>
          <NFormItem label="目标 resource（默认使用 MCP 地址）"><NInput v-model:value="oauthFields.resource" /></NFormItem>
          <NFormItem label="申请权限（空格分隔）"><NInput v-model:value="oauthFields.scopes" /></NFormItem></template>
          <NFormItem v-if="form.auth_type === 'api_key'" label="API Key 请求头名称"><NInput v-model:value="form.header_name" placeholder="X-API-Key" /></NFormItem>
        </NForm>
        <NAlert v-if="existing" type="warning" class="resource-alert">新会话和新任务使用更新后的定义。需要认证的插件请确认新版已有可用连接。</NAlert>
        <NAlert v-if="formError" class="resource-alert" type="error">{{ formError }}</NAlert>
        <template #footer>
          <div class="resource-drawer-footer">
            <NButton :disabled="saving" @click="close">取消</NButton>
            <NButton type="primary" :loading="saving" :disabled="!canSave" @click="save">保存定义</NButton>
          </div>
        </template>
      </NDrawerContent>
    </NDrawer>

    <NDrawer class="library-drawer" :show="viewing" :width="'min(620px,95vw)'" @update:show="closeHistory">
      <NDrawerContent title="插件定义版本" closable>
        <NAlert v-if="versionError" type="error" class="resource-alert">{{ versionError }}</NAlert>
        <p v-if="versionsLoading" role="status">正在读取定义版本…</p>
        <div class="history-version-cards">
          <article v-for="p in versions" :key="p.version_id" class="resource-card">
            <div class="resource-card-header">
              <strong class="resource-card-title">{{ p.name }}</strong>
              <span class="mono-pill">版本 {{ p.version }}</span>
            </div>
            <dl class="resource-details-box" style="margin: 0;">
              <div class="resource-detail-row">
                <dt class="resource-detail-lbl">端点地址</dt>
                <dd class="resource-detail-code" style="margin: 0;">{{ p.endpoint }}</dd>
              </div>
              <div class="resource-detail-row">
                <dt class="resource-detail-lbl">认证方式</dt>
                <dd class="resource-detail-val" style="margin: 0;">{{ pluginAuthLabel(p) }}</dd>
              </div>
            </dl>
          </article>
        </div>
      </NDrawerContent>
    </NDrawer>
  </section>
</template>

<style src="../styles/platform-resources.css"></style>

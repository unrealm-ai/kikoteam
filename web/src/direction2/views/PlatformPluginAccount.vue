<script setup lang="ts">
import { computed, onUnmounted, ref, watch } from 'vue';
import { NAlert, NButton, NCheckbox, NInput, NPopconfirm, NSelect, useMessage } from 'naive-ui';
import { activeWorkspaceId, D2ApiError } from '../api/client';
import { pluginsApi, type AuthType, type Plugin, type PluginAccount, type ConnectionOwner } from '../api/capabilities';

const props = defineProps<{ employeeId: string; plugin: Plugin; versionId: string; canManage: boolean; allowSourceChange?: boolean }>();
const emit = defineEmits<{ account: [value: PluginAccount | undefined]; busy: [value: boolean]; dirty: [value: boolean] }>();
const message = useMessage();
const baselineName = ref('');
const account = ref<PluginAccount>();
const name = ref('');
const secret = ref('');
const appID = ref('');
const tenantTools=ref(''), tenantScopes=ref('');
const tenantEndpoint = ref(''), tenantIssuer = ref(''), tenantClient = ref(''), tenantSecret = ref('');
const officialProvider = computed(() => props.plugin.scope === 'official' ? props.plugin.endpoint : '');
const feishu = computed(() => officialProvider.value === 'https://mcp.feishu.cn/mcp');
const crm = computed(() => officialProvider.value === 'https://open.fxiaoke.com/mcp');
const github = computed(() => officialProvider.value === 'https://api.githubcopilot.com/mcp/readonly');
const methods = computed(() => props.plugin.supported_auth_methods ?? [props.plugin.auth_type]);
const defaultMethod = computed(() => props.plugin.default_auth_method ?? props.plugin.auth_type);
const authMethod = ref<AuthType>(defaultMethod.value);
const methodChanged = computed(() => !!account.value && authMethod.value !== (account.value.auth_method ?? props.plugin.auth_type));
const connectionStatus = computed(() => account.value?.enabled && account.value.validated ? '已验证' : props.plugin.auth_type==='none' && account.value?.enabled ? '待获取工具' : '待重新授权');
const credentialLabel = computed(() => {
  if (github.value) return 'GitHub 个人访问令牌（PAT）';
  switch (officialProvider.value) {
    case 'https://mcp.feishu.cn/mcp': return '飞书 App Secret';
    case 'https://mcp-gw.dingtalk.com/mserver': return '钉钉待办专属服务 URL';
    case 'https://mcp.tavily.com/mcp': return 'Tavily API Key';
    default: return '账号密钥';
  }
});
const replace = ref(false);
const source = ref<ConnectionOwner>('workspace'), revision = ref(0);
const pendingPrivate = ref(false);
const owner = computed(() => source.value==='agent' ? props.employeeId : '');
const oauth = computed(() => authMethod.value === 'oauth');
watch(authMethod, () => { secret.value = ''; replace.value = false; });
let consent: {source: ConnectionOwner; version: number} | undefined;
function refreshConsent(){if(consent)void load()}
window.addEventListener('focus',refreshConsent);
async function changeSource(value:ConnectionOwner){
  const current=epoch;
  saving.value=true;emit('busy',true);
  try {
    if (pendingPrivate.value && value === 'workspace') { await load(); return; }
    if (value === 'agent' && source.value === 'workspace') {
      let privateAccount: PluginAccount | undefined;
      try { privateAccount = await pluginsApi.account(activeWorkspaceId.value,props.employeeId,props.plugin.id,props.versionId); }
      catch(e) { if (!(e instanceof D2ApiError && e.status === 404)) throw e; }
      if (current !== epoch) return;
      if (!privateAccount?.enabled || !privateAccount.validated) {
        pendingPrivate.value = true;
        source.value = 'agent';
        account.value = privateAccount;
        authMethod.value = privateAccount?.auth_method ?? defaultMethod.value;
        name.value = privateAccount?.name ?? props.plugin.name+'账号';
        secret.value = ''; replace.value = false; error.value = '';
        emit('account', undefined);
        return;
      }
    }
    await pluginsApi.setSource(activeWorkspaceId.value,props.employeeId,props.plugin.id,props.versionId,value,revision.value);
    if(current===epoch)await load();
  }
  catch(e){if(current===epoch)error.value=e instanceof Error?e.message:'切换账号失败'}
  finally{saving.value=false;emit('busy',false)}
}
const ready=ref(false);
const loading = ref(false), saving = ref(false), error = ref('');
let epoch = 0;
let controller: AbortController | undefined;
const mustReplace = computed(() => !account.value || account.value.plugin_version_id !== props.versionId || !account.value.enabled || methodChanged.value);
async function load() {
  pendingPrivate.value = false;
  authMethod.value = defaultMethod.value;
  appID.value='';tenantTools.value='';tenantScopes.value='';tenantEndpoint.value='';tenantIssuer.value='';tenantClient.value='';tenantSecret.value='';
  const current = ++epoch; ready.value=false; name.value=''; controller?.abort(); loading.value = true; error.value = ''; account.value = undefined; secret.value = ''; replace.value = false; emit('account', undefined);
  const w = activeWorkspaceId.value;
  try {
    const selected = props.employeeId ? await pluginsApi.source(w,props.employeeId,props.plugin.id,props.versionId) : undefined;
    const a = selected ? selected.account : await pluginsApi.account(w,'',props.plugin.id,props.versionId);
    if (current !== epoch) return;
    source.value=selected?.source ?? 'workspace';revision.value=selected?.revision ?? 0;
    authMethod.value=a ? (a.auth_method ?? props.plugin.auth_type) : defaultMethod.value;
    if(a && consent && source.value===consent.source && a.version>consent.version)consent=undefined;
    ready.value=true;account.value = a; name.value = a?.name ?? props.plugin.name+'账号'; emit('account', a);
  } catch (e) {
    if (current === epoch) {
      if (!props.employeeId && e instanceof D2ApiError && e.status === 404) {ready.value=true;name.value = `${props.plugin.name}账号`;}
      else error.value = e instanceof Error ? e.message : '账号读取失败';
    }
  } finally { if (current === epoch) { baselineName.value = name.value; loading.value = false; } }
}
watch(() => [props.employeeId, props.plugin.id, props.versionId, activeWorkspaceId.value], () => { consent=undefined; void load(); }, { immediate: true });
watch(() => props.versionId, () => { secret.value = ''; });
watch(() => pendingPrivate.value || !!secret.value || replace.value || methodChanged.value || (ready.value && !loading.value && name.value !== baselineName.value), value => emit('dirty', value));
const canSave = computed(() => props.canManage && ready.value && !loading.value && !saving.value && !!name.value.trim() && (oauth.value || authMethod.value === 'none' || (!(mustReplace.value || replace.value) || !!secret.value)));
async function save() {
  if (!canSave.value) return;
  const current = epoch; const w = activeWorkspaceId.value;
  saving.value = true; emit('busy', true); error.value = ''; controller = new AbortController();
  const timeout = setTimeout(() => controller?.abort(), 50000);
  try {
    if(oauth.value){
      const popup=window.open('about:blank','_blank');if(popup)popup.opener=null;
      try{const tenant=crm.value && tenantEndpoint.value.trim() ? { resource:tenantEndpoint.value.trim(), issuer:tenantIssuer.value.trim(), client_id:tenantClient.value.trim(),client_secret:tenantSecret.value, scopes:tenantScopes.value.trim(), read_tools:tenantTools.value.split(/[,，\s]+/).filter(Boolean) } : undefined;
      const {authorization_url}=await pluginsApi.authorize(w,owner.value,props.plugin.id,name.value.trim(),account.value?.version||0,props.versionId,tenant); tenantSecret.value='';
        if(current!==epoch){popup?.close();return}consent={source:source.value,version:account.value?.version ?? 0};if(popup)popup.location.replace(authorization_url);else window.location.assign(authorization_url)}
      catch(e){popup?.close();throw e}return;
    }
    const result = await pluginsApi.saveAccount(
      w, owner.value, props.plugin.id,
      { name: name.value.trim(), plugin_version_id: props.versionId, version: account.value?.version || 0, ...(methods.value.length > 1 ? {auth_method: authMethod.value} : {}), ...(authMethod.value !== 'none' && (mustReplace.value || replace.value) ? { secret: feishu.value ? JSON.stringify({app_id:appID.value.trim(),app_secret:secret.value}) : secret.value } : {}) },
      controller.signal
    );
    if (current !== epoch) return;
    if (pendingPrivate.value) {
      saving.value = false; emit('busy', false);
      await load();
    } else {
      account.value = result; baselineName.value = name.value; secret.value = ''; replace.value = false; emit('account', result);
    }
    message.success('账号已验证并保存');
  } catch (e) { if (current === epoch) error.value = e instanceof Error ? e.message : '账号探测失败'; }
  finally { clearTimeout(timeout); if (current === epoch) { saving.value = false; emit('busy', false); } }
}
async function revoke() {
  if (!account.value || saving.value || !props.canManage) return;
  const current = epoch; saving.value = true; emit('busy', true);
  try {
    await pluginsApi.revokeAccount(activeWorkspaceId.value, owner.value, props.plugin.id, account.value.version,props.versionId);
    if (current === epoch) { emit('account', undefined); await load(); message.success('账号授权已撤销'); }
  } catch (e) { if (current === epoch) { if(e instanceof D2ApiError && e.code==='upstream_revocation_unconfirmed') await load();error.value = e instanceof Error ? e.message : '撤销失败'; } }
  finally { saving.value = false; emit('busy', false); }
}
onUnmounted(() => { window.removeEventListener("focus",refreshConsent); ++epoch; controller?.abort(); secret.value = ''; emit('busy', false); emit('dirty', false); });
</script>

<template>
  <div class="resource-fieldset">
    <div class="resource-fieldset-head">
      <strong>{{ plugin.auth_type==='none'?'可用工具':employeeId ? '使用的账号' : '空间共享账号' }}</strong>
      <span v-if="account" class="status-pill" :class="account.enabled && account.validated ? 'status-pill--success' : 'status-pill--warning'">
        <span class="status-dot" :class="account.enabled && account.validated ? 'dot--success' : 'dot--warning'"></span>
        <span>{{ connectionStatus }}</span>
      </span>
    </div>
    <NSelect v-if="employeeId && plugin.auth_type!=='none'" :value="source" :virtual-scroll="false" :options="[{label:'空间共享账号（默认）',value:'workspace'},{label:'此数字人独享账号',value:'agent'}]" :disabled="allowSourceChange===false || !canManage || loading || saving" @update:value="changeSource" />
    <p v-if="plugin.auth_type!=='none'" class="resource-hint-sm">{{source==='workspace'?'配置了此插件的数字人可以通过各自获准的能力包使用这份连接。':'只有此数字人使用这份连接。撤销后需要重新连接，或手动切换账号来源。'}}</p>
    <p v-if="pendingPrivate" class="resource-hint-sm">独享账号验证成功后生效。验证期间，此 AI 员工继续使用空间共享账号。</p>
    <label v-if="methods.length > 1" class="resource-form-label">认证方式
      <select v-model="authMethod" aria-label="认证方式" :disabled="!canManage || !ready || loading || saving">
        <option v-for="method in methods" :key="method" :value="method">{{ method==='bearer' ? '个人访问令牌（PAT）' : '网页授权（OAuth）' }}</option>
      </select>
    </label>
    <p v-if="github && oauth" class="resource-hint-sm">前往 GitHub 授权，只读访问已安装应用且当前账号有权查看的仓库。</p>
    <p v-else-if="oauth && plugin.oauth">授权服务 {{plugin.oauth.issuer}} · 申请权限 {{plugin.oauth.scopes || "服务商默认范围"}}</p>
    <p v-if="methodChanged && source==='workspace'" class="resource-hint-sm">验证成功后将更新共享连接，影响使用此连接的 {{ account?.agent_count ?? 0 }} 位 AI 员工。</p>
    <p v-if="account?.external_account">第三方账号 {{account.external_account}}</p>
    <p v-if="account?.granted_scopes">已授权范围 {{account.granted_scopes}}</p>
    <p v-if="loading" class="resource-hint-sm">正在读取账号…</p>
    <NAlert v-if="error" type="error" class="resource-alert">
      {{ error }}
      <NButton v-if="!account && !saving" size="small" style="margin-left: 8px;" @click="load">重新读取</NButton>
    </NAlert>
    <div v-if="account" class="resource-details-box">
      <div class="resource-detail-row">
        <span class="resource-detail-lbl">账号名称</span>
        <span class="resource-detail-val">{{ account.name }}</span>
      </div>
      <div v-if="methods.length > 1" class="resource-detail-row">
        <span class="resource-detail-lbl">已保存的认证方式</span>
        <span class="resource-detail-val">{{ (account.auth_method ?? plugin.auth_type) === 'oauth' ? '网页授权（OAuth）' : '个人访问令牌（PAT）' }}</span>
      </div>
      <div class="resource-detail-row">
        <span class="resource-detail-lbl">探测结果</span>
        <span class="resource-detail-val tabular-num">{{ connectionStatus }}<template v-if="account.validated"> · {{ account.tools.length }} 个可见工具</template></span>
      </div>
    </div>
    <div v-if="canManage" style="display: flex; flex-direction: column; gap: 10px;">
      <label v-if="plugin.auth_type!=='none'" class="resource-form-label">
        账号名称
        <NInput v-model:value="name" :disabled="saving || loading" />
      </label>
      <div v-if="account && !mustReplace && !oauth && plugin.auth_type !== 'none'">
        <NCheckbox v-model:checked="replace" :disabled="saving">更新凭据</NCheckbox>
      </div>
      <label v-if="!oauth && plugin.auth_type !== 'none' && (mustReplace || replace) && feishu" class="resource-form-label">
        飞书 App ID
        <NInput v-model:value="appID" :disabled="saving" />
      </label>
      <label v-if="!oauth && plugin.auth_type !== 'none' && (mustReplace || replace)" class="resource-form-label">
        {{ credentialLabel }}（保存后不回显）
        <NInput v-model:value="secret" type="password" autocomplete="new-password" :disabled="saving" />
      </label>
      <NAlert v-if="mustReplace && account && !methodChanged" type="warning">{{ oauth ? '请重新授权以启用此连接。' : '请重新提供凭据以启用此连接。' }}</NAlert>
      <template v-if="crm">
        <p class="resource-hint-sm">首次连接请填写企业 MCP 地址和 OAuth 应用。已配置的空间可直接授权。</p>
        <label class="resource-form-label">企业 MCP 地址<NInput v-model:value="tenantEndpoint" placeholder="https://open.fxiaoke.com/mcp/企业标识/服务标识" /></label>
        <template v-if="tenantEndpoint">
          <label class="resource-form-label">OAuth 发行方地址<NInput v-model:value="tenantIssuer" /></label>
          <label class="resource-form-label">允许的只读查询工具（企业 MCP 工具名，逗号分隔）<NInput v-model:value="tenantTools" /></label>
          <label class="resource-form-label">客户端 ID<NInput v-model:value="tenantClient" /></label>
          <label class="resource-form-label">OAuth 权限范围（如有）<NInput v-model:value="tenantScopes" /></label>
          <label class="resource-form-label">客户端密钥（如有）<NInput v-model:value="tenantSecret" type="password" autocomplete="new-password" /></label>
        </template>
      </template>
      <div class="resource-actions">
        <NButton type="primary" secondary size="small" :loading="saving" :disabled="!canSave" @click="save">{{ github && oauth ? '前往 GitHub 授权' : oauth ? (source==='workspace'?'连接并在本空间共享':'连接此数字人的专属账号') : plugin.auth_type==='none'?'重新探测工具':(source==='workspace'?'测试并保存为空间共享账号':'测试并保存账号') }}</NButton>
        <NPopconfirm v-if="account?.enabled" @positive-click="revoke">
          <template #trigger>
            <NButton size="small" type="error" quaternary :disabled="saving">撤销账号</NButton>
          </template>
          {{source==='workspace'?'所有使用该共享连接的数字人将失去访问权限。':'此数字人的专属连接将被撤销。'}}确定撤销？
        </NPopconfirm>
      </div>
    </div>
  </div>
</template>

<style src="../styles/platform-resources.css"></style>

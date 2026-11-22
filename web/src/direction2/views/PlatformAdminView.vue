<script setup lang="ts">
import { onMounted, onUnmounted, ref, watch } from 'vue';
import { NAlert, NButton, NDrawer, NDrawerContent, NForm, NFormItem, NInput, NModal, NPagination, NPopconfirm, NTag } from 'naive-ui';
import { platformRequest } from '../api/platform';
import { D2ApiError } from '../api/client';
import { creditsApi, type Settings } from '../api/credits';
import OfficialModelsView from './OfficialModelsView.vue';
import MyCreditsView from './MyCreditsView.vue';
import PlatformMailSettings from './PlatformMailSettings.vue';
import PlatformGithubOAuthSettings from './PlatformGithubOAuthSettings.vue';

interface Account { id:string; name:string; email:string; is_platform_admin:boolean; platform_admin_version:number; credit_balance:string }
type AdminSection = 'accounts' | 'models' | 'mail' | 'integrations';
const tab = ref<AdminSection>('accounts');
const mailOpened = ref(false), integrationsOpened = ref(false);
const mailEditor = ref<InstanceType<typeof PlatformMailSettings>>();
const githubEditor = ref<InstanceType<typeof PlatformGithubOAuthSettings>>();
function selectSection(section: AdminSection) {
  if (section === 'mail') mailOpened.value = true;
  if (section === 'integrations') integrationsOpened.value = true;
  tab.value = section;
}
const hasUnsaved = () => mailEditor.value?.dirty === true || githubEditor.value?.dirty === true;
async function save() {
  for (const editor of [mailEditor.value, githubEditor.value]) {
    if (editor?.dirty && !await editor.save()) return false;
  }
  return true;
}
function discard() {
  mailEditor.value?.discard();
  githubEditor.value?.discard();
}
defineExpose({ hasUnsaved, save, discard });
const items = ref<Account[]>([]), page = ref(1), total = ref(0), q = ref('');
const busy = ref(false), loading = ref(false), error = ref('');
const settings = ref<Settings>(), settingsError = ref(''), settingsNotice = ref('');
const bill = ref<Account>(), grant = ref<Account>(), amount = ref(''), reason = ref(''), grantError = ref('');
let grantKey: string|undefined;
let controller: AbortController|undefined;
let mounted = true;
watch([amount,reason], () => {grantKey=undefined;});

async function load() {
  controller?.abort();
  const request = controller = new AbortController();
  error.value=''; loading.value=true;
  try {
    const result = await platformRequest<{items:Account[];total:number}>(`/accounts?page=${page.value}&page_size=20&q=${encodeURIComponent(q.value)}`, {signal:request.signal});
    if (request.signal.aborted) return;
    items.value=result.items;total.value=result.total;
  } catch(e) { if (!request.signal.aborted) error.value=e instanceof Error?e.message:'读取用户失败'; }
  finally { if (!request.signal.aborted) loading.value=false; }
}
async function readSettings() {
  settingsError.value='';
  try { const result=await creditsApi.settings(); if (mounted) settings.value=result; }
  catch(e) { if (mounted) settingsError.value=e instanceof Error?e.message:'读取设置失败'; }
}
async function set(a: Account) {
  if (busy.value) return;
  busy.value=true;
  try {
    await platformRequest(`/accounts/${a.id}/platform-admin`, {method:'PATCH',body:JSON.stringify({version:a.platform_admin_version,is_platform_admin:!a.is_platform_admin})});
    if (mounted) await load();
  } catch(e) {
    error.value=e instanceof D2ApiError && e.code==='platform_admin_password_required'
      ? '不能撤销此账号的平台管理员权限。邮件已关闭，请先设立另一位可用密码登录的平台管理员。'
      : e instanceof Error ? e.message : '授权失败';
  }
  finally { busy.value=false; }
}
async function saveSettings() {
  if (!settings.value || busy.value) return;
  busy.value=true;settingsError.value=settingsNotice.value='';
  try { settings.value=await creditsApi.saveSettings(settings.value);settingsNotice.value='注册赠送已保存'; }
  catch(e) { settingsError.value=e instanceof Error?e.message:'保存失败'; }
  finally { busy.value=false; }
}
function openGrant(a: Account) {
  grant.value=a;amount.value=reason.value=grantError.value='';grantKey=undefined;
}
async function sendGrant() {
  const a=grant.value;
  if (!a || busy.value) return;
  if (!reason.value.trim() || !/^\d+(\.\d{1,6})?$/.test(amount.value) || Number(amount.value)<=0) {
    grantError.value='请填写正数积分和赠送原因。';
    return;
  }
  busy.value=true;grantError.value='';
  try {
    grantKey ??= Array.from(crypto.getRandomValues(new Uint8Array(16)),v=>v.toString(16).padStart(2,'0')).join('');
    await creditsApi.grant(a.id,amount.value,reason.value.trim(),grantKey);
    if (mounted) {grant.value=undefined;await load();}
  } catch(e) { grantError.value=e instanceof Error?e.message:'赠送失败'; }
  finally { busy.value=false; }
}
onMounted(()=>{void load();void readSettings();});
onUnmounted(()=>{mounted=false;controller?.abort();});
</script>

<template>
  <section class="accounts">
    <h1>平台管理</h1>
    <nav>
      <NButton :type="tab==='accounts'?'primary':'default'" @click="selectSection('accounts')">用户管理</NButton>
      <NButton :type="tab==='models'?'primary':'default'" @click="selectSection('models')">官方模型</NButton>
      <NButton :type="tab==='mail'?'primary':'default'" @click="selectSection('mail')">邮件与注册</NButton>
      <NButton :type="tab==='integrations'?'primary':'default'" @click="selectSection('integrations')">集成设置</NButton>
    </nav>
    <OfficialModelsView v-if="tab==='models'"/>
    <PlatformMailSettings v-if="mailOpened" v-show="tab==='mail'" ref="mailEditor" />
    <PlatformGithubOAuthSettings v-if="integrationsOpened" v-show="tab==='integrations'" ref="githubEditor" />
    <section v-if="tab==='accounts'">
      <form v-if="settings" class="settings" @submit.prevent="saveSettings">
        <label for="signup-bonus">新用户注册赠送积分</label>
        <NInput v-model:value="settings.signup_bonus" :input-props="{id:'signup-bonus'}" :disabled="busy"/>
        <NButton attr-type="submit" :loading="busy">保存设置</NButton>
      </form>
      <NAlert v-if="settingsError" type="error">{{settingsError}} <NButton text @click="readSettings">重新读取设置</NButton></NAlert>
      <NAlert v-if="settingsNotice" type="success">{{settingsNotice}}</NAlert>
      <div class="search"><NInput v-model:value="q" placeholder="搜索名称或邮箱" @keyup.enter="page=1;load()"/><NButton @click="page=1;load()">搜索</NButton></div>
      <NAlert v-if="error" type="error">{{error}} <NButton text @click="load">重新加载</NButton></NAlert>
      <p v-if="loading">正在读取用户…</p>
      <p v-else-if="!items.length && !error">没有找到用户。</p>
      <article v-for="a in items" :key="a.id">
        <div><strong>{{a.name}}</strong><p>{{a.email}}</p></div>
        <NTag v-if="a.is_platform_admin" type="success">平台管理员</NTag>
        <span>{{a.credit_balance}} 积分</span>
        <NButton size="small" @click="openGrant(a)">赠送积分</NButton>
        <NButton size="small" @click="bill=a">查看账单</NButton>
        <NPopconfirm @positive-click="set(a)">
          <template #trigger><NButton size="small" :disabled="busy">{{a.is_platform_admin?'取消平台管理员':'设为平台管理员'}}</NButton></template>
          确定{{a.is_platform_admin?'撤销':'授予'}} {{a.name}} 的平台管理员资格？
        </NPopconfirm>
      </article>
      <NPagination v-if="total>20" :page="page" @update:page="v=>{page=v;load()}" :page-size="20" :item-count="total"/>
    </section>
    <NDrawer :show="!!bill" :width="'min(1080px, 96vw)'" @update:show="show=>{if(!show) bill=undefined}">
      <NDrawerContent title="账户账单" closable>
        <MyCreditsView v-if="bill" :key="bill.id" :account-id="bill.id" :name="bill.name" admin @changed="load()"/>
      </NDrawerContent>
    </NDrawer>
    <NModal :show="!!grant" preset="card" :title="'赠送积分 · '+(grant?.name??'')" style="width:min(460px,95vw)"
      :closable="!busy" :mask-closable="!busy" :close-on-esc="!busy" @update:show="show=>{if(!show) grant=undefined}">
      <NAlert v-if="grantError" type="error">{{grantError}}</NAlert>
      <NForm label-placement="top" :disabled="busy">
        <NFormItem label="积分数量"><NInput v-model:value="amount"/></NFormItem>
        <NFormItem label="赠送原因"><NInput v-model:value="reason" maxlength="300"/></NFormItem>
        <NButton type="primary" :loading="busy" @click="sendGrant">确认赠送</NButton>
      </NForm>
    </NModal>
  </section>
</template>

<style scoped>
.accounts{padding:24px;max-width:1100px;margin:auto}.accounts nav,.search,.settings,article{display:flex;align-items:center;gap:12px;flex-wrap:wrap}
.accounts nav,.search,.settings{margin:16px 0}.search :deep(.n-input){flex:1}.settings :deep(.n-input){width:160px}
article{border-bottom:1px solid #e5e7eb;padding:16px 0}article>div{flex:1;min-width:155px}article p{color:#71717a;margin:4px 0}
</style>

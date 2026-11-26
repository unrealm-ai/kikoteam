<script setup lang="ts">
import { onMounted, onUnmounted, ref, watch } from 'vue';
import { NAlert, NButton, NDrawer, NDrawerContent, NEmpty, NForm, NFormItem, NInput, NModal, NPagination, NPopconfirm, NTag } from 'naive-ui';
import { platformRequest } from '../api/platform';
import { D2ApiError } from '../api/client';
import { creditsApi, type Settings } from '../api/credits';
import OfficialModelsView from './OfficialModelsView.vue';
import MyCreditsView from './MyCreditsView.vue';
import PlatformMailSettings from './PlatformMailSettings.vue';
import PlatformGithubOAuthSettings from './PlatformGithubOAuthSettings.vue';
import PlatformTaskLinkSettings from './PlatformTaskLinkSettings.vue';
import PlatformChannelDownloadSettings from './PlatformChannelDownloadSettings.vue';

interface Account { id:string; name:string; email:string; is_platform_admin:boolean; platform_admin_version:number; credit_balance:string }
type AdminSection = 'accounts' | 'models' | 'mail' | 'integrations' | 'downloads' | 'task-links';
const tab = ref<AdminSection>('accounts');
const mailOpened = ref(false), integrationsOpened = ref(false);
const downloadsOpened = ref(false), taskLinksOpened = ref(false);
const mailEditor = ref<InstanceType<typeof PlatformMailSettings>>();
const githubEditor = ref<InstanceType<typeof PlatformGithubOAuthSettings>>();
const downloadsEditor = ref<InstanceType<typeof PlatformChannelDownloadSettings>>();
const taskLinksEditor = ref<InstanceType<typeof PlatformTaskLinkSettings>>();
function selectSection(section: AdminSection) {
  if (section === 'mail') mailOpened.value = true;
  if (section === 'integrations') integrationsOpened.value = true;
  if (section === 'downloads') downloadsOpened.value = true;
  if (section === 'task-links') taskLinksOpened.value = true;
  tab.value = section;
}
const hasUnsaved = () => mailEditor.value?.dirty === true || githubEditor.value?.dirty === true || downloadsEditor.value?.dirty === true || taskLinksEditor.value?.dirty === true;
async function save() {
  for (const editor of [mailEditor.value, githubEditor.value, downloadsEditor.value, taskLinksEditor.value]) {
    if (editor?.dirty && !await editor.save()) return false;
  }
  return true;
}
function discard() {
  mailEditor.value?.discard();
  githubEditor.value?.discard();
  downloadsEditor.value?.discard();
  taskLinksEditor.value?.discard();
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
    <header class="platform-admin-banner">
      <div class="admin-banner-copy">
        <h1>平台管理</h1>
        <p class="sub-desc">管理全平台注册账户、积分额度、官方托管模型与底层基础设施配置。</p>
      </div>
      <nav class="admin-subnav" aria-label="平台管理分区">
        <NButton size="small" :quaternary="tab !== 'accounts'" :type="tab==='accounts'?'primary':'default'" @click="selectSection('accounts')">用户管理</NButton>
        <NButton size="small" :quaternary="tab !== 'models'" :type="tab==='models'?'primary':'default'" @click="selectSection('models')">官方模型</NButton>
        <NButton size="small" :quaternary="tab !== 'mail'" :type="tab==='mail'?'primary':'default'" :class="{ 'has-unsaved-dot': mailEditor?.dirty }" @click="selectSection('mail')">邮件与注册</NButton>
        <NButton size="small" :quaternary="tab !== 'integrations'" :type="tab==='integrations'?'primary':'default'" :class="{ 'has-unsaved-dot': githubEditor?.dirty }" @click="selectSection('integrations')">集成设置</NButton>
        <NButton size="small" :quaternary="tab !== 'downloads'" :type="tab==='downloads'?'primary':'default'" :class="{ 'has-unsaved-dot': downloadsEditor?.dirty }" @click="selectSection('downloads')">附件下载</NButton>
        <NButton size="small" :quaternary="tab !== 'task-links'" :type="tab==='task-links'?'primary':'default'" :class="{ 'has-unsaved-dot': taskLinksEditor?.dirty }" @click="selectSection('task-links')">任务详情链接</NButton>
      </nav>
    </header>

    <OfficialModelsView v-if="tab==='models'"/>
    <PlatformMailSettings v-if="mailOpened" v-show="tab==='mail'" ref="mailEditor" />
    <PlatformGithubOAuthSettings v-if="integrationsOpened" v-show="tab==='integrations'" ref="githubEditor" />
    <PlatformChannelDownloadSettings v-if="downloadsOpened" v-show="tab==='downloads'" ref="downloadsEditor" />
    <PlatformTaskLinkSettings v-if="taskLinksOpened" v-show="tab==='task-links'" ref="taskLinksEditor" />

    <section v-if="tab==='accounts'" class="accounts-panel-stack">
      <form v-if="settings" class="settings-card settings signup-bonus-card" @submit.prevent="saveSettings">
        <div class="signup-bonus-copy">
          <label for="signup-bonus">新用户注册赠送积分</label>
          <span class="sub-desc">新用户首次注册成功后自动发放的官方模型初始积分额度</span>
        </div>
        <div class="signup-bonus-control">
          <NInput v-model:value="settings.signup_bonus" :input-props="{id:'signup-bonus'}" :disabled="busy" placeholder="0" />
          <span class="unit-label">积分</span>
          <NButton attr-type="submit" type="primary" size="small" :loading="busy">保存设置</NButton>
        </div>
      </form>
      <NAlert v-if="settingsError" type="error">{{settingsError}} <NButton text @click="readSettings">重新读取设置</NButton></NAlert>
      <NAlert v-if="settingsNotice" type="success">{{settingsNotice}}</NAlert>

      <div class="members-table-card">
        <div class="accounts-toolbar">
          <div class="search">
            <NInput v-model:value="q" size="small" clearable placeholder="搜索名称或邮箱" @keyup.enter="page=1;load()" @clear="q='';page=1;load()"/>
            <NButton size="small" secondary @click="page=1;load()">搜索</NButton>
          </div>
          <span class="neutral-pill tabular-num">共 {{ total }} 位用户</span>
        </div>

        <NAlert v-if="error" type="error" style="margin: 0 16px 12px;">{{error}} <NButton text @click="load">重新加载</NButton></NAlert>

        <div v-if="loading" class="accounts-empty-state">
          <p class="sub-desc">正在读取用户…</p>
        </div>
        <div v-else-if="!items.length && !error" class="accounts-empty-state">
          <NEmpty description="没有找到用户。" />
        </div>
        <template v-else-if="items.length">
          <div class="accounts-list-header">
            <span>用户</span>
            <span>平台角色</span>
            <span>积分余额</span>
            <span class="col-actions">操作</span>
          </div>
          <article v-for="a in items" :key="a.id" class="account-row">
            <div class="account-identity">
              <span class="account-avatar" aria-hidden="true">{{ (a.name || a.email || '?').slice(0, 1) }}</span>
              <div class="account-meta-text">
                <strong>{{a.name}}</strong>
                <p class="mono-text">{{a.email}}</p>
              </div>
            </div>
            <div class="account-role-col">
              <NTag v-if="a.is_platform_admin" type="success" size="small" round>平台管理员</NTag>
              <span v-else class="neutral-pill">普通用户</span>
            </div>
            <div class="account-credit-col">
              <span class="credit-pill tabular-num">{{a.credit_balance}} 积分</span>
            </div>
            <div class="table-action-btns">
              <NButton size="small" secondary @click="openGrant(a)">赠送积分</NButton>
              <NButton size="small" secondary @click="bill=a">查看账单</NButton>
              <NPopconfirm @positive-click="set(a)">
                <template #trigger>
                  <NButton size="small" :type="a.is_platform_admin ? 'warning' : 'default'" quaternary :disabled="busy">
                    {{a.is_platform_admin?'取消平台管理员':'设为平台管理员'}}
                  </NButton>
                </template>
                确定{{a.is_platform_admin?'撤销':'授予'}} {{a.name}} 的平台管理员资格？
              </NPopconfirm>
            </div>
          </article>
        </template>
      </div>
      <div v-if="total > 20" class="accounts-pagination">
        <NPagination :page="page" @update:page="v=>{page=v;load()}" :page-size="20" :item-count="total"/>
      </div>
    </section>

    <NDrawer :show="!!bill" :width="'min(1080px, 96vw)'" @update:show="show=>{if(!show) bill=undefined}">
      <NDrawerContent title="账户账单" closable>
        <MyCreditsView v-if="bill" :key="bill.id" :account-id="bill.id" :name="bill.name" admin @changed="load()"/>
      </NDrawerContent>
    </NDrawer>

    <NModal :show="!!grant" preset="card" :title="'赠送积分 · '+(grant?.name??'')" style="width:min(460px,95vw)"
      :closable="!busy" :mask-closable="!busy" :close-on-esc="!busy" @update:show="show=>{if(!show) grant=undefined}">
      <div v-if="grant" class="modal-body-stack">
        <div class="scope-notice-strip scope-notice-strip--subtle">
          <div class="grant-user-summary">
            <span class="mono-text">{{ grant.email }}</span>
            <span class="credit-pill tabular-num">当前余额 {{ grant.credit_balance }} 积分</span>
          </div>
        </div>
        <NAlert v-if="grantError" type="error">{{grantError}}</NAlert>
        <NForm label-placement="top" :disabled="busy">
          <NFormItem label="积分数量"><NInput v-model:value="amount" placeholder="例如：100" /></NFormItem>
          <NFormItem label="赠送原因"><NInput v-model:value="reason" maxlength="300" placeholder="填写本次积分调账或赠送说明" /></NFormItem>
        </NForm>
      </div>
      <template #footer>
        <div class="modal-footer-actions">
          <NButton :disabled="busy" @click="grant=undefined">取消</NButton>
          <NButton type="primary" :loading="busy" @click="sendGrant">确认赠送</NButton>
        </div>
      </template>
    </NModal>
  </section>
</template>

<style scoped>
.accounts {
  display: flex;
  flex-direction: column;
  gap: 16px;
  padding-top: 12px;
}
.platform-admin-banner {
  background: var(--id-bg-surface);
  border: 1px solid var(--id-border-subtle);
  border-radius: var(--id-radius-lg);
  padding: 16px 20px;
  display: flex;
  flex-direction: column;
  gap: 14px;
}
.admin-banner-copy {
  display: flex;
  flex-direction: column;
  gap: 4px;
}
.admin-banner-copy h1 {
  margin: 0;
  font-size: 16px;
  font-weight: 600;
  color: var(--id-text-primary);
}
.admin-subnav {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 6px;
  background: var(--id-bg-muted);
  border: 1px solid var(--id-border-subtle);
  border-radius: var(--id-radius-md);
  padding: 4px;
}
.admin-subnav :deep(.n-button) {
  position: relative;
}
.admin-subnav :deep(.has-unsaved-dot)::after {
  content: '';
  position: absolute;
  top: 4px;
  right: 4px;
  width: 6px;
  height: 6px;
  border-radius: 50%;
  background: var(--id-status-warning-dot);
}

.accounts-panel-stack {
  display: flex;
  flex-direction: column;
  gap: 16px;
}

.signup-bonus-card {
  flex-direction: row;
  align-items: center;
  justify-content: space-between;
  flex-wrap: wrap;
  gap: 14px 20px;
  padding: 16px 20px;
}
.signup-bonus-copy {
  display: flex;
  flex-direction: column;
  gap: 3px;
  min-width: 0;
  flex: 1 1 260px;
}
.signup-bonus-copy label {
  font-size: 14px;
  font-weight: 600;
  color: var(--id-text-primary);
}
.signup-bonus-control {
  display: flex;
  align-items: center;
  gap: 10px;
  flex-wrap: wrap;
}
.signup-bonus-control :deep(.n-input) {
  width: 148px;
}
.unit-label {
  font-size: 13px;
  color: var(--id-text-secondary);
}

.accounts-toolbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  flex-wrap: wrap;
  gap: 12px;
  padding: 12px 16px;
  border-bottom: 1px solid var(--id-border-subtle);
}
.search {
  display: flex;
  align-items: center;
  gap: 8px;
  flex: 1 1 260px;
  max-width: 420px;
}
.search :deep(.n-input) {
  flex: 1;
}

.accounts-empty-state {
  padding: 36px 16px;
  display: flex;
  justify-content: center;
  align-items: center;
}
.accounts-list-header {
  display: grid;
  grid-template-columns: minmax(220px, 1.5fr) 120px 140px minmax(240px, 1.2fr);
  gap: 12px;
  align-items: center;
  padding: 10px 16px;
  background: var(--id-bg-canvas);
  border-bottom: 1px solid var(--id-border-subtle);
  font-size: 12px;
  font-weight: 500;
  color: var(--id-text-muted);
}
.account-row {
  display: grid;
  grid-template-columns: minmax(220px, 1.5fr) 120px 140px minmax(240px, 1.2fr);
  gap: 12px;
  align-items: center;
  padding: 13px 16px;
  border-bottom: 1px solid var(--id-border-hairline);
  transition: background-color 0.15s ease;
}
.account-row:last-child {
  border-bottom: none;
}
.account-row:hover {
  background: var(--id-bg-canvas);
}
.account-identity {
  display: flex;
  align-items: center;
  gap: 10px;
  min-width: 0;
}
.account-avatar {
  width: 32px;
  height: 32px;
  border-radius: 50%;
  background: var(--id-bg-muted);
  border: 1px solid var(--id-border-subtle);
  color: var(--id-text-primary);
  font-size: 13px;
  font-weight: 600;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  flex-shrink: 0;
  text-transform: uppercase;
}
.account-meta-text {
  min-width: 0;
}
.account-meta-text strong {
  display: block;
  font-size: 13px;
  font-weight: 600;
  color: var(--id-text-primary);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.account-meta-text p {
  margin: 2px 0 0 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.credit-pill {
  display: inline-flex;
  align-items: center;
  padding: 2px 9px;
  border-radius: var(--id-radius-sm);
  font-size: 12px;
  font-weight: 600;
  font-family: var(--id-font-mono);
  color: var(--id-text-primary);
  background: var(--id-bg-subtle);
  border: 1px solid var(--id-border-subtle);
}
.accounts-pagination {
  display: flex;
  justify-content: flex-end;
}
.grant-user-summary {
  display: flex;
  align-items: center;
  justify-content: space-between;
  flex-wrap: wrap;
  gap: 8px;
  width: 100%;
}

@media (max-width: 900px) {
  .accounts-list-header {
    display: none;
  }
  .account-row {
    grid-template-columns: 1fr;
    gap: 10px;
  }
  .account-row .table-action-btns {
    justify-content: flex-start;
  }
}
@media (max-width: 760px) {
  .platform-admin-banner {
    padding: 14px 16px;
  }
  .signup-bonus-card {
    padding: 16px;
  }
}
</style>

<style scoped src="../components/workspace-settings-shared.css"></style>

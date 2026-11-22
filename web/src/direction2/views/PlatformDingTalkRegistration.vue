<script setup lang="ts">
import { copyText } from '../utils/clipboard';
import { computed, onMounted, onUnmounted, ref, watch } from 'vue';
import { NAlert, NButton, NQrCode } from 'naive-ui';
import { D2ApiError } from '../api/client';
import { dingTalkSetupApi, dingTalkVerificationURL, type DingTalkSetupStatus } from '../api/dingtalk-setup';

const props = defineProps<{ workspace: string; employeeId: string; identity: string; employeeName?: string; configured: boolean; autoCreate?: boolean; disabled: boolean }>();
const emit = defineEmits<{ revoked: []; changed: [state: string]; busy: [value: boolean] }>();
const data = ref<DingTalkSetupStatus | null>(null), loading = ref(false), polling = ref(false), error = ref(''), denied = ref(false), now = ref(Date.now());
const uncertain = ref(false), copyNotice = ref('');
let generation = 0, controller = new AbortController(), createKey = '';
const registration = computed(() => data.value?.registration);
const expired = computed(() => !!registration.value && Date.parse(registration.value.expires_at) <= now.value);
const active = computed(() => !!registration.value && !['consumed', 'cancelled', 'expired', 'failed'].includes(registration.value.state));
const url = computed(() => !expired.value && registration.value?.state === 'awaiting_confirmation' ? dingTalkVerificationURL(registration.value.verification_url) : null);
const labels: Record<string, string> = {
  starting: '正在准备授权',
  awaiting_confirmation: '等待钉钉管理员确认',
  polling: '正在读取授权结果',
  ready: '授权完成，等待保存连接',
  consumed: '应用凭据已保存',
  failed: '接入失败',
  expired: '授权已过期',
  cancelled: '已取消',
  interrupted: '接入结果需核实'
};
function reset() { uncertain.value = false; copyNotice.value = ''; ++generation; controller.abort(); controller = new AbortController(); data.value = null; loading.value = false; polling.value = false; error.value = ''; denied.value = false; createKey = ''; emit('busy', false); }
watch(() => [props.workspace, props.employeeId, props.identity], reset);
watch(() => props.disabled, v => { if (v) reset(); });
async function copyName() {
  const current = generation;
  try { await copyText(props.employeeName || ''); if (current === generation) copyNotice.value = '名称已复制，请在钉钉创建页填写'; }
  catch { if (current === generation) copyNotice.value = '无法复制，请手动填写名称'; }
}
// Persist only an attempt marker, never credentials. A lost confirm response must
// not cause another automatic write after switching tabs or reloading the page.
function claimAutomaticConfirmation(id: string): boolean {
  try {
    const key = `kiko:dingtalk-confirm:${props.workspace}:${props.employeeId}:${id}`;
    if (window.sessionStorage.getItem(key)) return false;
    window.sessionStorage.setItem(key, 'attempted');
    return true;
  } catch { return false; } // Storage unavailable: keep the explicit save fallback.
}
async function recoverConfirm() {
  const current = generation, id = registration.value?.id;
  await run('get');
  if (current === generation && id && registration.value?.id === id && registration.value.state === 'ready' && !error.value && !expired.value && !props.configured) await run('confirm');
}
async function run(action: 'get' | 'create' | 'poll' | 'confirm' | 'cancel') {
  if (props.disabled || loading.value || denied.value || (action === 'create' && props.configured)) return;
  const g = generation; loading.value = true; emit('busy', true); error.value = ''; polling.value = false;
  try {
    let result: DingTalkSetupStatus;
    if (action === 'get') result = await dingTalkSetupApi.get(props.workspace, props.employeeId, controller.signal);
    else if (action === 'create') { createKey ||= crypto.randomUUID(); result = await dingTalkSetupApi.create(props.workspace, props.employeeId, createKey, controller.signal); }
    else { if (!registration.value) return; result = await dingTalkSetupApi.action(props.workspace, props.employeeId, registration.value.id, action, controller.signal); }
    if (g !== generation) return;
    const wasSaved = registration.value?.credential_saved;
    data.value = result;
    if (result.registration?.credential_saved && !wasSaved) emit('changed', result.registration.state);
    if (action === 'confirm') uncertain.value = false;
    polling.value = result.registration?.state === 'awaiting_confirmation' && !expired.value;
    if (result.registration && ['consumed', 'cancelled', 'expired', 'failed'].includes(result.registration.state)) createKey = '';
  } catch (e) {
    if (g !== generation) return;
    error.value = e instanceof Error ? e.message : '钉钉接入请求失败';
    if (action !== 'get') uncertain.value = true;
    if (action !== 'get') error.value += '；结果尚未确认，请先读取状态，不会重复创建或自动重试写操作。';
    if (e instanceof D2ApiError && [401, 403].includes(e.status)) { denied.value = true; data.value = null; emit('revoked'); }
  } finally { if (g === generation) { loading.value = false; emit('busy', false); } }
  if (g === generation && action !== 'confirm' && action !== 'cancel' && !props.disabled && !props.configured && !denied.value && !error.value && !uncertain.value && !expired.value && registration.value?.state === 'ready' && claimAutomaticConfirmation(registration.value.id)) await run('confirm');
}
// Restore before creating; failed or uncertain attempts require an explicit action.
onMounted(async () => {
  if (props.configured || props.disabled) return;
  const g = generation;
  await run('get');
  if (g === generation && !props.configured && !props.disabled && props.autoCreate !== false && data.value?.registration === null && !error.value) await run('create');
});
const timer = setInterval(() => { now.value = Date.now(); if (polling.value && !expired.value && !loading.value && !props.disabled && registration.value && Date.parse(registration.value.next_poll_at) <= now.value) void run('poll'); }, 1000);
onUnmounted(() => { reset(); clearInterval(timer); });
</script>

<template>
  <section class="registration ch-section" aria-label="钉钉扫码接入">
    <div>
      <h3 class="cs-title">钉钉扫码接入</h3>
      <p class="cs-sub">在钉钉官方页面完成扫码创建后，自动保存连接。名称请在钉钉中填写。不会覆盖已保存的手动绑定，也不会自动扩大使用范围。取消本地流程不撤销已在钉钉创建的应用。</p>
    </div>

    <p v-if="employeeName" class="field-note">AI 员工名称：{{ employeeName }} <NButton size="tiny" text @click="copyName">复制名称</NButton>。请在钉钉创建页手动填写。<span role="status">{{ copyNotice }}</span></p>
    <div class="actions">
      <NButton size="small" secondary :disabled="disabled || loading || denied" @click="run('get')">读取钉钉接入状态</NButton>
      <NButton v-if="!configured" size="small" type="primary" :disabled="disabled || loading || denied || active || !!registration?.credential_saved" @click="run('create')">扫码创建钉钉应用</NButton>
    </div>

    <p v-if="disabled" class="field-note text-warning">请先保存或放弃当前配置草稿。</p>
    <p v-if="loading" role="status" class="field-note">正在处理钉钉接入…</p>
    <NAlert v-if="error" type="error">{{ error }}</NAlert>
    <p v-if="data && !registration" class="field-note">暂无当前管理员的钉钉接入记录。</p>

    <template v-if="registration">
      <div class="reg-state-bar">
        <span class="wf-status-pill" :class="registration.state === 'consumed' ? 'is-success' : ['failed', 'expired', 'interrupted'].includes(registration.state) ? 'is-danger' : 'is-info'">
          <span class="wf-status-dot" />
          <strong>{{ labels[registration.state] || registration.state }}</strong>
        </span>
        <span v-if="registration.app_id" class="app-id-pill">应用 {{ registration.app_id }}</span>
      </div>

      <div v-if="url" class="qr">
        <NQrCode :value="url" type="svg" :size="180" />
        <a :href="url" target="_blank" rel="noopener noreferrer" class="qr-auth-link">打开钉钉官方授权页面</a>
        <small class="field-note">有效期至 {{ registration.expires_at }}</small>
      </div>
      <NAlert v-else-if="registration.state === 'awaiting_confirmation'" type="warning">授权链接过期或不可信，已阻止展示；请读取最新状态。</NAlert>
      <NAlert v-if="registration.state === 'interrupted'" type="warning">可能已经创建应用，请先核实钉钉控制台，不要直接重新创建。</NAlert>
      <p v-if="registration.retry_status && registration.state === 'awaiting_confirmation'" class="field-note">
        正在等待同一次授权恢复：{{ registration.retry_status }}。不会重新创建应用。
      </p>
      <p v-if="registration.error_code" class="field-note" style="color: var(--id-status-danger);">接入错误：{{ registration.error_code }}</p>
      <NAlert v-if="registration.credential_saved" type="info">
        凭据已保存，请重新读取配置。实际连接状态以渠道页为准；首条可信消息自动确认租户身份，不需要额外验证。
      </NAlert>

      <div class="actions">
        <NButton v-if="registration.state === 'ready' && !expired" size="small" type="primary" :disabled="disabled || loading || denied || configured" @click="recoverConfirm">重新核对并保存连接</NButton>
        <NButton v-if="active" size="small" :disabled="disabled || loading || denied" @click="run('cancel')">取消本次钉钉接入</NButton>
      </div>
    </template>
  </section>
</template>

<style scoped>
@import '../components/workforce/workforce-shared.css';

.actions {
  display: flex;
  gap: 8px;
  flex-wrap: wrap;
}

.reg-state-bar {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
}

.app-id-pill {
  font-family: var(--id-font-mono);
  font-size: 12px;
  color: var(--id-text-secondary);
  background: var(--id-bg-muted);
  border: 1px solid var(--id-border-subtle);
  padding: 2px 8px;
  border-radius: var(--id-radius-sm);
}

.qr {
  display: flex;
  align-items: center;
  flex-direction: column;
  gap: 10px;
  padding: 16px;
  background: var(--id-bg-subtle);
  border: 1px solid var(--id-border-subtle);
  border-radius: var(--id-radius-md);
}

.qr-auth-link {
  color: var(--id-accent-primary);
  font-size: 13px;
  font-weight: 500;
  text-decoration: none;
}

.qr-auth-link:hover {
  text-decoration: underline;
}
</style>

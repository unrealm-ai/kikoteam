<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue';
import { NAlert, NButton } from 'naive-ui';
import { D2ApiError } from '../api/client';
import { feishuSetupApi, feishuRegistrationFailure, feishuVerificationURL, type FeishuSetupStatus, type FeishuDiagnostics } from '../api/feishu-setup';
import FeishuRegistrationQr from '../components/FeishuRegistrationQr.vue';

const props = defineProps<{ workspace: string; employeeId: string; disabled: boolean; configured: boolean; autoCreate?: boolean; identity: string }>();
const emit = defineEmits<{ revoked: []; changed: [state: string]; busy: [value: boolean] }>();
const data = ref<FeishuSetupStatus | null>(null), diagnostics = ref<FeishuDiagnostics | null>(null), loading = ref(false), error = ref(''), polling = ref(false), denied = ref(false), now = ref(Date.now());
let generation = 0, controller = new AbortController(), createKey = '', nextStatusRead = 0;
let replaceKey: { id: string; key: string } | undefined;
const registration = computed(() => data.value?.registration);
const terminal = new Set(['connected', 'cancelled', 'expired', 'denied', 'failed']);
const labels: Record<string, string> = {
  starting: '正在准备授权',
  awaiting_confirmation: '等待飞书管理员确认',
  polling: '正在查询授权结果',
  credentials_saved: '应用凭据已保存',
  checking: '正在检查配置',
  needs_attention: '需要处理配置问题',
  connected: '配置校验与保存完成',
  cancelled: '已取消',
  expired: '授权已过期',
  denied: '授权被拒绝',
  failed: '接入失败',
  interrupted: '接入已中断',
  unknown: '接入结果未知'
};
const resultLabels: Record<string, string> = { pass: '通过', failed: '失败', unknown: '尚未确认', granted: '已获批', missing: '缺少权限' };
const expired = computed(() => !!registration.value && Date.parse(registration.value.expires_at) <= now.value);
const verificationURL = computed(() => !expired.value && registration.value?.state === 'awaiting_confirmation' ? feishuVerificationURL(registration.value.verification_url) : null);
const checks = computed(() => diagnostics.value || registration.value?.diagnostics);
const permissions = computed(() => checks.value?.permissions || data.value?.permissions || []);
const active = computed(() => !!registration.value && !terminal.has(registration.value.state));
const failure = computed(() => feishuRegistrationFailure(registration.value));
function reset() { ++generation; controller.abort(); controller = new AbortController(); data.value = null; diagnostics.value = null; error.value = ''; loading.value = false; denied.value = false; polling.value = false; createKey = ''; emit('busy', false); }
watch(() => [props.workspace, props.employeeId, props.identity], reset);
watch(() => props.disabled, value => { if (value) reset(); });
function apply(value: FeishuSetupStatus) { const previous = registration.value; data.value = value; if (value.registration?.credential_saved && (!previous?.credential_saved || (value.registration.state === 'connected' && previous.state !== 'connected'))) emit('changed', value.registration.state); }
async function run(action: 'get' | 'create' | 'replace' | 'poll' | 'retry' | 'cancel' | 'diagnose') {
  if (props.disabled || denied.value || loading.value) return;
  const g = generation; loading.value = true; emit('busy', true); error.value = '';
  if (action !== 'poll') polling.value = false;
  try {
    if (action === 'diagnose') { const result = await feishuSetupApi.diagnose(props.workspace, props.employeeId, controller.signal); if (g === generation) diagnostics.value = result; }
    else {
      let result: FeishuSetupStatus;
      if (action === 'get') result = await feishuSetupApi.get(props.workspace, props.employeeId, controller.signal);
      else if (action === 'replace') {
        const id = registration.value?.id;
        if (!id || props.configured || !failure.value?.canReauthorize) return;
        if (replaceKey?.id !== id) replaceKey = {id, key:crypto.randomUUID()};
        result = await feishuSetupApi.create(props.workspace, props.employeeId, replaceKey.key, controller.signal, id);
      }
      else if (action === 'create') { createKey ||= crypto.randomUUID(); result = await feishuSetupApi.create(props.workspace, props.employeeId, createKey, controller.signal); }
      else { if (!registration.value) return; result = await feishuSetupApi.action(props.workspace, props.employeeId, registration.value.id, action, controller.signal); }
      if (g !== generation) return;
      diagnostics.value = null;
      apply(result);
      nextStatusRead = Date.now() + 3000;
      polling.value = !!result.registration && ['awaiting_confirmation', 'polling'].includes(result.registration.state) && !expired.value;
      if (result.registration && terminal.has(result.registration.state)) createKey = '';
    }
  } catch (e) {
    if (g !== generation) return;
    polling.value = false;
    error.value = e instanceof Error ? e.message : '接入请求失败';
    if (action !== 'get' && action !== 'diagnose') error.value += '；请求结果尚未确认，请先读取接入状态，不会自动重试。';
    if (e instanceof D2ApiError && [401, 403].includes(e.status)) { denied.value = true; data.value = null; diagnostics.value = null; emit('revoked'); }
  } finally { if (g === generation) { loading.value = false; emit('busy', false); } }
}
// Restore before creating; failed or uncertain attempts require an explicit action.
onMounted(async () => {
  if (props.configured || props.disabled) return;
  const g = generation;
  await run('get');
  if (g === generation && !props.configured && !props.disabled && props.autoCreate !== false && data.value?.registration === null && !error.value) await run('create');
});
const timer = setInterval(() => {
  now.value = Date.now();
  if (polling.value && !expired.value && !loading.value && !props.disabled && registration.value && Date.parse(registration.value.next_poll_at) <= now.value) void run('poll');
  else if (!error.value && !props.configured && !props.disabled && !loading.value && now.value >= nextStatusRead
    && (registration.value?.state === 'checking' || registration.value?.auto_check?.eligible)) void run('get');
}, 1000);
onUnmounted(() => { reset(); clearInterval(timer); });
</script>

<template>
  <section class="feishu-registration ch-section" aria-label="飞书扫码接入与权限检查">
    <div class="cs-header-flex">
      <div>
        <h3 class="cs-title">扫码接入与权限检查</h3>
        <p class="cs-sub">使用飞书官方授权页面创建应用并保存凭据。管理员需在飞书确认；取消本地流程不会删除已创建的飞书应用。权限不足时不会自动扩大到全员。</p>
      </div>
    </div>

    <div class="setup-actions">
      <NButton size="small" secondary :disabled="disabled || loading || denied" @click="run('get')">读取扫码接入状态</NButton>
      <NButton v-if="!configured" size="small" type="primary" :disabled="disabled || loading || denied || active" @click="run('create')">扫码创建飞书应用</NButton>
      <NButton v-if="configured" size="small" secondary :disabled="disabled || loading || denied" @click="run('diagnose')">检查已保存应用权限</NButton>
    </div>

    <p v-if="disabled" class="field-note text-warning">请先保存或放弃当前配置草稿，再操作接入与诊断。</p>
    <p v-if="loading" role="status" class="field-note">正在处理接入请求…</p>
    <NAlert v-if="error" type="error">{{ error }}</NAlert>
    <NAlert v-if="denied" type="warning">接入管理权限已失效。</NAlert>
    <p v-if="data && !registration" class="field-note">暂无当前管理员的扫码接入记录。</p>

    <template v-if="registration">
      <div class="reg-state-bar">
        <span class="wf-status-pill" :class="registration.state === 'connected' ? 'is-success' : ['failed', 'denied', 'expired', 'interrupted'].includes(registration.state) ? 'is-danger' : 'is-info'">
          <span class="wf-status-dot" />
          <strong>{{ labels[registration.state] || registration.state }}</strong>
        </span>
        <span v-if="registration.app_id" class="app-id-pill">应用 {{ registration.app_id }}</span>
      </div>

      <NAlert v-if="failure" type="warning" :title="failure.reason">
        <p>{{ failure.detail }}</p>
        <p>{{ failure.automaticRetry ? '后台将自动重试。' : '自动检查已停止。' }}</p>
        <p v-if="!configured && failure.canReauthorize">重新授权会结束这次接入。再次扫码后，飞书将创建新的应用。</p>
      </NAlert>
      <NAlert v-else-if="registration.credential_saved" type="info">应用凭据已保存，请核对应用的使用范围。</NAlert>

      <div v-if="verificationURL" class="setup-qr">
        <FeishuRegistrationQr :value="verificationURL" :compact-size="180" />
        <a :href="verificationURL" target="_blank" rel="noopener noreferrer" class="qr-auth-link">打开飞书官方授权页面</a>
        <small class="field-note">有效期至 {{ registration.expires_at }}</small>
      </div>
      <NAlert v-else-if="registration.state === 'awaiting_confirmation'" type="warning">
        {{ expired ? '授权链接已过期，请读取最新状态后重新创建。' : '授权链接不可用，已阻止展示。请重新读取接入状态。' }}
      </NAlert>
      <NAlert v-if="['interrupted', 'unknown'].includes(registration.state)" type="warning">
        此流程可能已在飞书创建应用。请先到飞书控制台核实，不要直接重复创建。
      </NAlert>
      <p v-if="registration.state === 'connected'" class="field-note">配置已验证并保存；Event Hub 是否连接仍以飞书配置中的 Hub 应用状态为准。</p>
      <p v-if="registration.error_code" class="field-note" style="color: var(--id-status-danger);">接入错误：{{ registration.error_code }}</p>

      <div class="setup-actions">
        <NButton
          v-if="registration.state === 'credentials_saved' || registration.state === 'needs_attention' || registration.state === 'interrupted'"
          size="small"
          type="primary"
          secondary
          :disabled="disabled || loading || denied"
          @click="run('retry')"
        >
          {{ registration.state === 'credentials_saved' ? '检查并连接' : '重新检查并继续接入' }}
        </NButton>
        <NButton v-if="!configured && failure?.canReauthorize" size="small" type="primary" :disabled="disabled || loading || denied" @click="run('replace')">重新授权</NButton>
        <NButton v-if="active && !registration.credential_saved" size="small" :disabled="disabled || loading || denied" @click="run('cancel')">取消本次接入</NButton>
      </div>
    </template>

    <section v-if="checks" class="setup-checks">
      <div class="setup-checks-head">
        <strong>{{ checks.ready ? '基础接入检查通过' : '基础接入检查未通过' }}</strong>
        <span class="field-note">检查时间 {{ checks.checked_at }}。检查结果不等于真实收发验收，也不保证所有业务资源可访问。</span>
      </div>
      <ul class="checks-grid">
        <li><span>应用凭据：</span><strong>{{ resultLabels[checks.credential_status] || checks.credential_status }}</strong></li>
        <li><span>机器人：</span><strong>{{ resultLabels[checks.bot_status] || checks.bot_status }}</strong></li>
        <li><span>通讯录访问：</span><strong>{{ resultLabels[checks.directory_status] || checks.directory_status }}</strong></li>
        <li><span>权限范围：</span><strong>{{ resultLabels[checks.scope_status] || checks.scope_status }}</strong></li>
      </ul>
      <p v-if="checks.error_code && !failure" class="field-note" style="color: var(--id-status-danger);">诊断错误：{{ checks.error_detail || checks.error_code }}</p>
    </section>

    <details v-if="permissions.length" class="perm-details">
      <summary>所需飞书权限与检查结果 ({{ permissions.length }})</summary>
      <p class="field-note" style="margin: 8px 0;">以下状态来自服务端诊断；未知不表示已授权，权限变更仍需飞书管理员操作。</p>
      <ul class="perm-list">
        <li v-for="permission in permissions" :key="permission.scope">
          <code>{{ permission.scope }}</code>
          <span>{{ permission.required ? '基础必需' : '按业务需要' }} · {{ resultLabels[permission.status] || permission.status }}</span>
        </li>
      </ul>
    </details>
  </section>
</template>

<style scoped>
@import '../components/workforce/workforce-shared.css';

.setup-actions {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
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

.setup-qr {
  display: flex;
  flex-direction: column;
  align-items: center;
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

.setup-checks {
  background: var(--id-bg-subtle);
  border: 1px solid var(--id-border-subtle);
  border-radius: var(--id-radius-md);
  padding: 12px;
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.setup-checks-head {
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.setup-checks-head strong {
  font-size: 13px;
  color: var(--id-text-primary);
}

.checks-grid {
  list-style: none;
  margin: 0;
  padding: 0;
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 8px;
}

.checks-grid li {
  background: var(--id-bg-surface);
  border: 1px solid var(--id-border-hairline);
  border-radius: var(--id-radius-sm);
  padding: 8px 10px;
  display: flex;
  justify-content: space-between;
  align-items: center;
  font-size: 12px;
  color: var(--id-text-secondary);
}

.checks-grid li strong {
  color: var(--id-text-primary);
}

.perm-details {
  font-size: 12px;
  color: var(--id-text-secondary);
}

.perm-details summary {
  cursor: pointer;
  font-weight: 500;
  color: var(--id-text-primary);
}

.perm-list {
  list-style: none;
  margin: 0;
  padding: 0;
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.perm-list li {
  display: flex;
  justify-content: space-between;
  gap: 8px;
  flex-wrap: wrap;
  padding: 6px 10px;
  background: var(--id-bg-subtle);
  border: 1px solid var(--id-border-hairline);
  border-radius: var(--id-radius-sm);
}

.perm-list code {
  font-family: var(--id-font-mono);
  color: var(--id-text-primary);
}
</style>

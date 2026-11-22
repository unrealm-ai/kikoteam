<script setup lang="ts">
import { computed, inject, onUnmounted, ref, watch } from 'vue';
import { NAlert, NButton, NForm, NFormItem, NInput, NSkeleton, NSwitch, useDialog } from 'naive-ui';
import PlatformFeishuRegistration from './PlatformFeishuRegistration.vue';
import PlatformDingTalkRegistration from './PlatformDingTalkRegistration.vue';
import PlatformWecomRegistration from './PlatformWecomRegistration.vue';
import type { WeComRegistrationCredentials } from '../api/wecom-registration';
import D2ChannelLogo from '../components/D2ChannelLogo.vue';
import { D2ApiError } from '../api/client';
import { type FeishuBinding, type FeishuConfiguration } from '../api/feishu';
import { channelsApi, channelLabels, type ChannelProvider } from '../api/channels';

const props = defineProps<{ workspace: string; employeeId: string; canManage: boolean; employeeName?: string; provider?: ChannelProvider }>();
const provider = computed(() => props.provider || 'feishu');
const channelName = computed(() => channelLabels[provider.value]);
const appLabel = computed(() => provider.value === 'dingtalk' ? 'Client ID' : provider.value === 'wecom' ? 'Bot ID' : 'App ID');
const secretLabel = computed(() => provider.value === 'dingtalk' ? 'Client Secret' : provider.value === 'wecom' ? 'Bot Secret' : 'App Secret');
const appPlaceholder = computed(() => provider.value === 'feishu' ? '飞书自建应用 App ID' : `${channelName.value} ${appLabel.value}`);
const connected = computed(() => !!saved.value && saved.value.enabled && saved.value.applied?.status === 'connected' && saved.value.applied.connection_generation === saved.value.connection_generation);
const emit = defineEmits<{ close: [] }>();
const dialog = useDialog();
type ConnectionForm = Pick<FeishuConfiguration, 'version' | 'app_id' | 'enabled'>;
const fresh = (): ConnectionForm => ({ version: 0, app_id: '', enabled: false });
const form = ref(fresh()), secret = ref(''), saved = ref<FeishuBinding | null>(null), baseline = ref('');
const connectionBusy = ref(false);
const autoCreateRegistration = ref(true);
const onboardingMode = ref<'qrcode' | 'existing_app'>('qrcode');
const loading = ref(false), busy = ref(false), denied = ref(false), loaded = ref(false), error = ref(''), statusError = ref(''), notice = ref('');
let generation = 0, controller = new AbortController();
const dirty = computed(() => JSON.stringify(form.value) !== baseline.value || !!secret.value);
const writable = computed(() => props.canManage && !denied.value && loaded.value && !loading.value && !busy.value && !connectionBusy.value);
const needsSecret = computed(() => !saved.value?.has_secret || form.value.app_id.trim() !== saved.value.app_id);
const applied = computed(() => !!saved.value && ['applied', 'connected'].includes(saved.value.applied?.status) && saved.value.applied.config_revision === saved.value.config_revision && saved.value.applied.rules_version === saved.value.rules_version && saved.value.applied.connection_generation === saved.value.connection_generation);
function adopt(value: FeishuBinding | null) {
  saved.value = value;
  form.value = value ? { version: value.config_revision, app_id: value.app_id, enabled: value.enabled } : fresh();
  baseline.value = JSON.stringify(form.value); secret.value = ''; loaded.value = true;
}
async function load() {
  const g = ++generation; controller.abort(); controller = new AbortController();
  error.value = ''; statusError.value = ''; notice.value = ''; loaded.value = false; denied.value = false; loading.value = true; adopt(null); loaded.value = false;
  if (!props.canManage) { denied.value = true; loading.value = false; return; }
  try { const result = await channelsApi.get(props.workspace, props.employeeId, provider.value, controller.signal); if (g === generation) adopt(result); }
  catch (e) { if (g !== generation) return; if (e instanceof D2ApiError && e.status === 404 && e.code === 'channel_not_configured') adopt(null); else { denied.value = e instanceof D2ApiError && [401, 403].includes(e.status); error.value = e instanceof Error ? e.message : '读取失败'; } }
  finally { if (g === generation) loading.value = false; }
}
async function refreshStatus() {
  if (!loaded.value || busy.value || loading.value || denied.value || !saved.value) return;
  const g = generation;
  try { const result = await channelsApi.get(props.workspace, props.employeeId, provider.value, controller.signal); if (g === generation) { saved.value = result; statusError.value = ''; } }
  catch (e) { if (g === generation) { statusError.value = e instanceof Error ? e.message : '状态读取失败'; if (e instanceof D2ApiError && [401, 403].includes(e.status)) { denied.value = true; secret.value = ''; saved.value = null; } } }
}
async function save(): Promise<boolean> {
  if (!writable.value || !form.value.app_id.trim() || (needsSecret.value && !secret.value)) return false;
  const g = generation; busy.value = true; error.value = ''; notice.value = '';
  const payload = { version: form.value.version, app_id: form.value.app_id.trim(), enabled: form.value.enabled, ...(secret.value ? { app_secret: secret.value } : {}) };
  try { const result = await channelsApi.save(props.workspace, props.employeeId, provider.value, payload, controller.signal); if (g !== generation) return false; adopt(result); notice.value = '配置已保存；凭据尚不代表已验证，等待 Hub 应用状态。'; return true; }
  catch (e) { if (g === generation) { error.value = e instanceof Error ? e.message : '保存失败'; if (e instanceof D2ApiError && [401, 403].includes(e.status)) { denied.value = true; secret.value = ''; saved.value = null; } } return false; }
  finally { if (g === generation) busy.value = false; }
}
function confirmDiscard(action: () => void) { if (busy.value) return; if (!dirty.value) { action(); return; } dialog.warning({ title: `有未保存的${channelName.value}配置`, content: '继续将放弃本次修改，包括尚未保存的密钥。', positiveText: '放弃修改', negativeText: '继续编辑', onPositiveClick: action }); }
function close() { confirmDiscard(() => emit('close')); }
function changeMode() { confirmDiscard(() => { secret.value = ''; form.value = saved.value ? { version: saved.value.config_revision, app_id: saved.value.app_id, enabled: saved.value.enabled } : fresh(); baseline.value = JSON.stringify(form.value); onboardingMode.value = onboardingMode.value === 'qrcode' ? 'existing_app' : 'qrcode'; }); }
async function acceptWecomCredentials(value: WeComRegistrationCredentials) {
  if (!writable.value || saved.value || provider.value !== 'wecom') return;
  form.value.app_id = value.botId; form.value.enabled = true; secret.value = value.secret;
  onboardingMode.value = 'existing_app';
  await save();
}
async function registrationChanged(state: string) {
  notice.value = '应用凭据已保存；实际连接状态以渠道配置为准。';
  // Feishu can save credentials before diagnostics finish. Preserve that attempt
  // and its check-and-connect action until completion, rather than unmounting it.
  if (!['connected', 'consumed'].includes(state) || dirty.value || busy.value || denied.value || !props.canManage) return;
  autoCreateRegistration.value = false;
  const refresh = load();
  const current = generation;
  await refresh;
  if (current === generation && !error.value) notice.value = saved.value ? '应用凭据已保存，配置已刷新；实际连接状态以渠道配置为准。' : '应用凭据已保存，但尚未读取到渠道配置，请重新读取配置核对。';
}
function reload() { confirmDiscard(() => { void load(); }); }
const draft = { hasUnsaved: () => dirty.value || busy.value, getLabel: () => `${channelName.value}渠道配置`, save, discard: () => { secret.value = ''; emit('close'); } };
const guard = inject<{ register: (handler: typeof draft) => void; unregister: (handler: typeof draft) => void }>('d2DraftGuard');
guard?.register(draft);
watch(() => [props.workspace, props.employeeId, props.canManage, provider.value], () => { busy.value = false; connectionBusy.value = false; autoCreateRegistration.value = true; onboardingMode.value = 'qrcode'; void load(); }, { immediate: true });
onUnmounted(() => { ++generation; controller.abort(); secret.value = ''; guard?.unregister(draft); });
defineExpose({ close });
</script>

<template>
  <section class="feishu-panel">
    <header class="channel-header">
      <div class="channel-title-row">
        <D2ChannelLogo :platform="provider" :size="24" />
        <div>
          <h2>{{ channelName }}渠道连接</h2>
          <p class="field-note">仅管理应用连接；业务规则由员工统一配置。</p>
        </div>
      </div>
      <NButton size="small" secondary :disabled="busy" @click="close">返回员工列表</NButton>
    </header>

    <NSkeleton v-if="loading" text :repeat="3" />
    <NAlert v-if="denied || !canManage" type="warning">{{ channelName }}连接仅工作区管理员可管理。</NAlert>
    <NAlert v-if="error" type="error">
      {{ error }}
      <NButton size="small" style="margin-left: 8px;" :disabled="busy" @click="reload">重新读取配置</NButton>
    </NAlert>
    <NAlert v-if="notice" type="success">{{ notice }}</NAlert>

    <template v-if="loaded && !denied && canManage">
      <section v-if="saved" class="status-card ch-section">
        <div class="cs-header-flex">
          <div class="status-badges">
            <strong class="status-rev-tag tabular-num">已保存 v{{ saved.config_revision }}</strong>
            <span class="wf-status-pill" :class="connected ? 'is-success' : saved.enabled ? 'is-warning' : ''">
              <span class="wf-status-dot" />
              <strong>{{ connected ? '已连接' : saved.enabled ? '等待连接' : '未启用' }}</strong>
            </span>
            <span class="wf-status-pill" :class="applied ? 'is-info' : ''">
              {{ applied ? 'Hub 已应用当前版本' : 'Hub 尚未确认应用当前版本' }}
            </span>
          </div>
          <NButton size="small" secondary :disabled="busy" @click="refreshStatus">刷新应用状态</NButton>
        </div>
        <p v-if="provider !== 'feishu' && connected && !saved.identity_confirmed" class="field-note">
          首次可信消息到达时自动确认身份并处理该消息，无需单独验证。
        </p>
        <div class="status-meta-row tabular-num">
          <span>连接版本 {{ saved.connection_generation }} · 规则 v{{ saved.rules_version }}</span>
          <span v-if="saved.applied">Hub 状态：{{ saved.applied.status || '未知' }} · 已应用配置 v{{ saved.applied.config_revision }}</span>
        </div>
        <span v-if="saved.applied?.error_code" class="status-err-text">错误：{{ saved.applied.error_code }}</span>
        <p v-if="statusError" role="alert" class="status-err-text">{{ statusError }}</p>
        <NAlert v-if="saved.config_revision !== form.version" type="warning">
          配置已被更新。当前草稿仍基于 v{{ form.version }}，请重新读取后核对，不会自动覆盖。
        </NAlert>
      </section>

      <div v-if="!saved" class="unconnected-channel-card">
        <div class="ucc-header-row">
          <div><h3>接入{{ channelName }}</h3><p class="field-note">扫码创建机器人应用并接入这位 AI 员工。</p></div>
          <NButton size="small" secondary :disabled="busy || connectionBusy" @click="changeMode">{{ onboardingMode === 'qrcode' ? '已有机器人应用' : '返回扫码接入' }}</NButton>
        </div>
      </div>
      <PlatformFeishuRegistration
        v-if="provider === 'feishu' && (saved || onboardingMode === 'qrcode')"
        :workspace="workspace"
        :employee-id="employeeId"
        :identity="saved ? `${saved.id}:${saved.connection_generation}` : 'new'"
        :configured="!!saved"
        :auto-create="autoCreateRegistration"
        :disabled="dirty || busy || loading || denied || !canManage"
        @busy="connectionBusy = $event"
        @changed="registrationChanged"
        @revoked="denied = true; secret = ''; saved = null"
      />
      <PlatformDingTalkRegistration
        :employee-name="employeeName || ''"
        v-if="provider === 'dingtalk' && (saved || onboardingMode === 'qrcode')"
        :workspace="workspace"
        :employee-id="employeeId"
        :identity="saved ? `${saved.id}:${saved.connection_generation}` : 'new'"
        :configured="!!saved"
        :auto-create="autoCreateRegistration"
        :disabled="dirty || busy || loading || denied || !canManage"
        @busy="connectionBusy = $event"
        @changed="registrationChanged"
        @revoked="denied = true; secret = ''; saved = null"
      />

      <PlatformWecomRegistration
        :employee-name="employeeName || ''"
        v-if="provider === 'wecom' && !saved && onboardingMode === 'qrcode'"
        :context="`${workspace}:${employeeId}:${provider}`"
        :disabled="dirty || busy || loading || denied || !canManage"
        @busy-changed="connectionBusy = $event"
        @credentials="acceptWecomCredentials"
      />

      <NForm v-if="saved || onboardingMode === 'existing_app'" :disabled="!writable" label-placement="top" @submit.prevent="save">
        <fieldset class="ch-section">
          <legend class="fs-legend">应用连接</legend>
          <NFormItem :label="appLabel"><NInput v-model:value="form.app_id" autocomplete="off" :placeholder="appPlaceholder" /></NFormItem>
          <NFormItem :label="saved?.has_secret ? secretLabel + '（留空保留已保存密钥）' : secretLabel">
            <NInput v-model:value="secret" type="password" show-password-on="click" autocomplete="new-password" placeholder="密钥仅写入，不回显" />
          </NFormItem>
          <p v-if="saved && needsSecret" class="text-warning field-note">更换 {{ appLabel }} 必须填写新密钥。提供新密钥会更新连接代际，并重新确认应用身份。</p>
          <NFormItem :label="'启用此员工的' + channelName + '渠道'"><NSwitch v-model:value="form.enabled" /></NFormItem>
          <p class="field-note">保存、Hub 应用、凭据有效和连接在线是不同事实。未收到连接状态时不表示机器人已连接。</p>
        </fieldset>
        <p class="field-note" style="margin: 8px 0 14px 0;">使用范围、群成员使用、触发规则与历史上下文在员工的“统一使用配置”中管理。更换应用后旧身份规则不会授予新应用权限。</p>
        <footer class="channel-footer">
          <NButton :disabled="busy" @click="reload">重新读取配置</NButton>
          <NButton type="primary" :loading="busy" :disabled="!writable || !form.app_id.trim() || (needsSecret && !secret)" @click="save">保存{{ channelName }}配置</NButton>
        </footer>
      </NForm>
    </template>
  </section>
</template>

<style scoped>
@import '../components/workforce/workforce-shared.css';

.feishu-panel {
  color: var(--id-text-primary);
  display: flex;
  flex-direction: column;
  gap: 16px;
}

.unconnected-channel-card { padding: 16px; border: 1px solid var(--id-border-subtle); border-radius: var(--id-radius-md); background: var(--id-bg-subtle); }
.ucc-header-row { display: flex; justify-content: space-between; align-items: center; gap: 12px; flex-wrap: wrap; }
.ucc-header-row h3 { margin: 0 0 6px; }

.channel-header {
  display: flex;
  justify-content: space-between;
  align-items: flex-start;
  gap: 16px;
}

.channel-title-row {
  display: flex;
  align-items: center;
  gap: 12px;
}

.channel-title-row h2 {
  font-size: 18px;
  font-weight: 600;
  margin: 0 0 4px 0;
}

.status-badges {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
}

.status-rev-tag {
  font-size: 13px;
  font-weight: 600;
  color: var(--id-text-primary);
}

.status-meta-row {
  display: flex;
  flex-wrap: wrap;
  gap: 12px;
  font-size: 12px;
  color: var(--id-text-secondary);
  background: var(--id-bg-subtle);
  border: 1px solid var(--id-border-hairline);
  border-radius: var(--id-radius-md);
  padding: 8px 12px;
}

.status-err-text {
  font-size: 12px;
  color: var(--id-status-danger);
  margin: 0;
}

.fs-legend {
  font-size: 14px;
  font-weight: 600;
  color: var(--id-text-primary);
  padding: 0 8px;
}

.channel-footer {
  display: flex;
  justify-content: flex-end;
  gap: 10px;
  flex-wrap: wrap;
}

@media (max-width: 600px) {
  .channel-header {
    flex-direction: column;
  }
}
</style>

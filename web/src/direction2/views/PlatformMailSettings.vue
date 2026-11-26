<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue';
import { NAlert, NButton, NCheckbox, NForm, NFormItem, NInput, NRadio, NRadioGroup, NTag } from 'naive-ui';
import { platformSettings, type MailSettings, type MailSettingsFields, type MailSettingsWrite } from '../api/platform-settings';
import { D2ApiError } from '../api/client';
import { useVersionedPlatformSettings } from '../composables/useVersionedPlatformSettings';

type Profile =
  | { provider: '163' | 'custom'; email: string; address: string }
  | { provider: 'legacy' };
type Draft = Pick<MailSettingsFields, 'enabled' | 'registration_verification_required'> & {
  profile: Profile;
  password: string;
};
const address163 = 'smtp.163.com:465';
const emailPattern = /^[^\s<>()@]+@[^\s<>()@]+\.[^\s<>()@]+$/;
const dnsLabel = /^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$/i;
function endpoint(address: string) {
  const match = /^([^:]+):(465|587)$/.exec(address.trim());
  const host = match?.[1];
  if (!host || host.length > 253) return undefined;
  const labels = host.split('.');
  if (labels.length < 2 || labels.some(label => !dnsLabel.test(label)) || /^\d+$/.test(labels.at(-1)!)) return undefined;
  return { address: address.trim(), host, implicit_tls: match[2] === '465' };
}
function noTransport(s: MailSettings) {
  return ![s.address, s.host, s.from, s.username].some(value => value.trim()) && !s.password_configured;
}
function isFirstSetup(s: MailSettings) {
  return s.source === 'environment' && s.version === 0 && !s.enabled &&
    !s.registration_verification_required && noTransport(s);
}
function toProfile(s: MailSettings): Profile {
  if (!s.enabled && noTransport(s)) {
    return { provider: '163', email: '', address: address163 };
  }
  const server = endpoint(s.address);
  if (!server || server.host.toLowerCase() !== s.host.trim().toLowerCase() ||
      server.implicit_tls !== s.implicit_tls || s.from.trim() !== s.username.trim() ||
      !emailPattern.test(s.from.trim())) return { provider: 'legacy' };
  return {
    provider: server.address === address163 && /@163\.com$/i.test(s.from.trim()) ? '163' : 'custom',
    email: s.from.trim(),
    address: server.address,
  };
}
const toDraft = (s: MailSettings): Draft => ({
  enabled: s.enabled || isFirstSetup(s),
  registration_verification_required: s.registration_verification_required || isFirstSetup(s),
  profile: toProfile(s),
  password: '',
});
function savedTarget(s: MailSettingsFields) {
  return { address: s.address.trim(), host: s.host.trim(), username: s.username.trim(), from: s.from.trim(), implicit_tls: s.implicit_tls };
}
function simpleTarget(profile: Profile) {
  if (profile.provider === 'legacy') return undefined;
  const server = endpoint(profile.provider === '163' ? address163 : profile.address);
  return server && { ...server, username: profile.email.trim(), from: profile.email.trim() };
}
function sameSmtpTarget(a: ReturnType<typeof simpleTarget>, b: ReturnType<typeof simpleTarget>) {
  return !!a && !!b && a.address === b.address && a.host.toLowerCase() === b.host.toLowerCase() &&
    a.username === b.username && a.implicit_tls === b.implicit_tls;
}
const candidate = (d: Draft, s: MailSettings): MailSettingsWrite => ({
  version: s.version,
  enabled: d.enabled,
  registration_verification_required: d.registration_verification_required,
  ...(d.enabled ? simpleTarget(d.profile)! : savedTarget(s)),
  ...(d.enabled && d.password ? { password: d.password } : {}),
});
function keepsStoredCode(d: Draft, s: MailSettings) {
  return s.password_configured && toProfile(s).provider !== 'legacy' && sameSmtpTarget(simpleTarget(d.profile), savedTarget(s));
}
const testReceipt = ref('');
let receiptExpiry: ReturnType<typeof setTimeout> | undefined;
function invalidateTestReceipt() {
  clearTimeout(receiptExpiry);
  testReceipt.value = '';
}
onUnmounted(invalidateTestReceipt);
const editor = useVersionedPlatformSettings<MailSettings, Draft>({
  read: platformSettings.mail,
  write: async (d, s, signal) => {
    try {
      return await platformSettings.saveMail({
        ...candidate(d, s),
        ...(d.enabled ? { test_receipt: testReceipt.value } : {}),
      }, signal);
    } catch (error) {
      if (error instanceof D2ApiError && error.code === 'smtp_test_required') invalidateTestReceipt();
      throw error;
    }
  },
  toDraft,
  validate(d, s, operation) {
    if (!d.enabled) {
      if (operation === 'test') return '请先在草稿中启用邮件，再发送测试邮件。';
      return d.registration_verification_required ? '要求注册邮箱验证前，必须启用邮件。' : undefined;
    }
    if (d.profile.provider === 'legacy') return '旧 SMTP 配置无法在此编辑，请选择服务商并填写新配置，或停用邮件。';
    const target = simpleTarget(d.profile);
    if (!target) return '请填写 DNS 主机名和端口，只支持 465 或 587，不支持 IP 地址。';
    if (!emailPattern.test(target.from)) return '请输入有效的发件邮箱。';
    if (d.profile.provider === '163' && !/@163\.com$/i.test(target.from)) return '请填写 @163.com 邮箱；其他邮箱可使用自定义 SMTP。';
    if (!d.password && !keepsStoredCode(d, s)) return d.profile.provider === '163'
      ? '请输入邮箱客户端授权码。更换服务器或账号时不能沿用旧授权码。'
      : '请输入 SMTP 密码或授权码。更换服务器或账号时不能沿用旧凭据。';
    if (operation === 'write' && !testReceipt.value) return '请先发测试邮件，测试成功后再保存。';
    return undefined;
  },
  // Rebase the whole connection profile or none of it. An SMTP destination
  // must not be reconstructed from individual local and concurrent fields.
  sameField(key, before, after) {
    return key === 'profile' ? JSON.stringify(before.profile) === JSON.stringify(after.profile) : before[key] === after[key];
  },
  reviewRebase(previous, merged) {
    return previous.password && !sameSmtpTarget(simpleTarget(previous.profile), simpleTarget(merged.profile))
      ? {draft: {...merged, password: ''}, notice: '已读取最新版本。SMTP 连接目标已变化，请核对后重新输入授权码。'}
      : {draft: merged};
  },
  errors: {
    invalid_mail_settings: '邮件配置无效。请核对地址、认证信息和注册验证策略。',
    smtp_test_required: '此次 SMTP 测试已失效，请重新发测试邮件再保存。',
    smtp_test_failed: 'SMTP 测试失败。请核对服务器、TLS、认证与收发件策略。',
    platform_admin_password_required: '不能关闭邮件：至少需要一位可用密码登录的平台管理员。请在邮件仍可用时为管理员设置密码，或先把已有密码的账号设为平台管理员。',
  },
});
const {snapshot, draft, busy, error, notice, conflict, dirty, edit, load, save, test, discard} = editor;
// A policy checkbox does not change SMTP authentication or delivery. Do
// invalidate immediately on a transport/credential change, even if reverted.
watch([
  () => snapshot.value?.version,
  () => snapshot.value?.source,
  () => draft.value?.profile.provider,
  () => draft.value?.profile.provider === 'legacy' ? undefined : draft.value?.profile.email.trim(),
  () => draft.value?.profile.provider === 'custom' ? draft.value.profile.address.trim() : undefined,
  () => draft.value?.password,
], invalidateTestReceipt, { flush: 'sync' });
const enabledSaveBlocked = computed(() => draft.value?.enabled === true && !testReceipt.value);
const importable = computed(() => snapshot.value?.source === 'environment' && snapshot.value.version === 0 &&
  snapshot.value.enabled && draft.value?.profile.provider !== 'legacy');
const canKeepCode = computed(() => !!draft.value && !!snapshot.value && keepsStoredCode(draft.value, snapshot.value));
const passwordLabel = computed(() => draft.value?.profile.provider === '163' ? '客户端授权码' : 'SMTP 密码或授权码');
function editProfile(profile: Profile) {
  if (!draft.value) return;
  const previous = draft.value.profile;
  const sameTarget = previous.provider === profile.provider && sameSmtpTarget(simpleTarget(previous), simpleTarget(profile));
  edit({ profile, ...(!sameTarget ? { password: '' } : {}) });
}
function switchProvider(provider: '163' | 'custom') {
  const previous = draft.value?.profile;
  if (!previous || previous.provider === provider) return;
  editProfile({ provider, email: previous.provider === 'legacy' ? '' : previous.email, address: provider === '163' ? address163 : '' });
}
function editEmail(email: string) {
  const profile = draft.value?.profile;
  if (profile && profile.provider !== 'legacy') editProfile({ ...profile, email });
}
function editAddress(address: string) {
  const profile = draft.value?.profile;
  if (profile?.provider === 'custom') editProfile({ ...profile, address });
}
function sendTest() {
  return test(async (d, s, signal) => {
    invalidateTestReceipt();
    const response = await platformSettings.testMail(candidate(d, s), signal);
    if (response?.ok !== true || typeof response.test_receipt !== 'string' || !response.test_receipt.trim()) {
      throw new Error('Unexpected SMTP test response');
    }
    if (signal.aborted) return;
    testReceipt.value = response.test_receipt;
    receiptExpiry = setTimeout(invalidateTestReceipt, 10 * 60 * 1000);
  },
    'SMTP 服务器已接受发往当前账号邮箱的测试邮件，请检查收件箱。测试不会保存配置。');
}
onMounted(() => void load());
defineExpose({ dirty, save, discard });
</script>

<template>
  <section class="platform-settings-editor" aria-label="邮件与注册设置">
    <header class="editor-card-head">
      <div class="editor-title-block">
        <h2>邮件与注册</h2>
        <p class="editor-desc">配置平台发信和注册验证。测试邮件只发送到当前管理员的登录邮箱，不保存草稿。</p>
      </div>
      <div v-if="snapshot || dirty" class="editor-meta-pills">
        <template v-if="snapshot">
          <NTag size="small">{{ snapshot.source === 'database' ? '平台已保存' : isFirstSetup(snapshot) ? '尚未配置' : '部署环境配置' }}</NTag>
          <span class="mono-pill tabular-num">版本 {{ snapshot.version }}</span>
        </template>
        <span v-if="dirty" class="status-pill status-pill--warning">
          <span class="status-dot dot--warning"></span>
          <span>未保存</span>
        </span>
      </div>
    </header>

    <NAlert v-if="error" type="error" role="alert">{{ error }}</NAlert>
    <NAlert v-if="notice" type="success" role="status">{{ notice }}</NAlert>

    <div v-if="!draft && busy === 'read'" class="editor-loading-state">
      <span>正在读取邮件设置…</span>
    </div>
    <div v-else-if="!draft" class="editor-loading-state">
      <span>尚未加载邮件设置</span>
      <NButton size="small" :loading="busy === 'read'" @click="load()">重新读取</NButton>
    </div>

    <NForm v-if="draft" :disabled="Boolean(busy)" label-placement="top" class="editor-form-stack" @submit.prevent="save()">
      <div class="setting-toggle-box">
        <div class="setting-toggle-row">
          <NCheckbox :checked="draft.enabled" @update:checked="v => edit({enabled: v, registration_verification_required: v && draft?.registration_verification_required === true})">启用邮件</NCheckbox>
          <NCheckbox :checked="draft.registration_verification_required" :disabled="!draft.enabled"
            @update:checked="v => edit({registration_verification_required: v})">注册时必须验证邮箱</NCheckbox>
        </div>
        <p v-if="!draft.enabled" class="setting-toggle-help">停用邮件时保留已保存的连接设置。</p>
      </div>

      <NAlert v-if="draft.profile.provider === 'legacy'" type="warning">
        现有 SMTP 配置不符合简化表单，仍保留原有配置。选择服务商并填写新连接和凭据后才能替换，也可直接停用邮件。
        <p v-if="snapshot?.address">现有地址：{{ snapshot.address }}；发件邮箱：{{ snapshot.from || '未设置' }}</p>
      </NAlert>

      <div v-if="draft.enabled" class="editor-subpanel">
        <NFormItem label="邮箱服务商">
          <NRadioGroup :value="draft.profile.provider" aria-label="邮箱服务商" @update:value="v => switchProvider(v as '163' | 'custom')">
            <NRadio value="163">网易 163 邮箱</NRadio>
            <NRadio value="custom">自定义 SMTP</NRadio>
          </NRadioGroup>
        </NFormItem>
        <div v-if="draft.profile.provider !== 'legacy'" class="mail-fields-grid">
          <NFormItem label="发件邮箱" for="platform-smtp-from">
            <NInput :value="draft.profile.email" :input-props="{id:'platform-smtp-from'}" :placeholder="draft.profile.provider === '163' ? 'name@163.com' : 'notify@example.com'" @update:value="editEmail"/>
          </NFormItem>
          <NFormItem v-if="draft.profile.provider === 'custom'" label="SMTP 服务器地址" for="platform-smtp-address">
            <NInput :value="draft.profile.address" :input-props="{id:'platform-smtp-address'}" placeholder="smtp.example.com:465 或 :587" @update:value="editAddress"/>
          </NFormItem>
          <NFormItem :label="passwordLabel" for="platform-smtp-password" class="mail-field-full">
            <NInput type="password" show-password-on="click" :value="draft.password" :input-props="{id:'platform-smtp-password', autocomplete:'new-password'}"
              :placeholder="canKeepCode ? '同一 SMTP 连接留空保留' : '输入' + passwordLabel" @update:value="v => edit({password: v})"/>
          </NFormItem>
        </div>
      </div>

      <div v-if="enabledSaveBlocked && draft.profile.provider !== 'legacy'" class="scope-notice-strip scope-notice-strip--warning">
        <p class="notice-text">启用邮件前，请先发测试邮件；测试成功后才能保存。</p>
      </div>
      <div v-else-if="draft.enabled && testReceipt && draft.profile.provider !== 'legacy'" class="scope-notice-strip scope-notice-strip--success">
        <p class="notice-text">当前配置已通过测试验证（10 分钟内有效），可点击保存使其生效。</p>
      </div>

      <footer class="editor-footer">
        <NButton size="small" secondary :loading="busy === 'read'" @click="load(dirty)">读取最新版本</NButton>
        <div class="editor-footer-primary">
          <NButton size="small" secondary :disabled="!draft.enabled || draft.profile.provider === 'legacy' || conflict" :loading="busy === 'test'" @click="sendTest">发测试邮件到我的邮箱</NButton>
          <NButton v-if="importable && !dirty" size="small" :disabled="conflict || enabledSaveBlocked" :loading="busy === 'write'" type="primary" @click="save(true)">迁入邮件设置</NButton>
          <NButton v-else size="small" :disabled="!dirty || conflict || enabledSaveBlocked" :loading="busy === 'write'" type="primary" attr-type="submit">保存邮件设置</NButton>
        </div>
      </footer>
    </NForm>
  </section>
</template>

<style scoped>
.mail-fields-grid {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 0 12px;
}
.mail-field-full {
  grid-column: span 2;
}
@media (max-width: 760px) {
  .mail-fields-grid {
    grid-template-columns: 1fr;
  }
  .mail-field-full {
    grid-column: span 1;
  }
}
</style>

<style scoped src="../components/workspace-settings-shared.css"></style>

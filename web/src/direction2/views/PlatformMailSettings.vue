<script setup lang="ts">
import { computed, onMounted } from 'vue';
import { NAlert, NButton, NCheckbox, NForm, NFormItem, NInput, NTag } from 'naive-ui';
import { platformSettings, type MailSettings, type MailSettingsFields, type MailSettingsWrite } from '../api/platform-settings';
import { useVersionedPlatformSettings } from '../composables/useVersionedPlatformSettings';

type Draft = MailSettingsFields & { password: string };
const toDraft = (s: MailSettings): Draft => ({
  enabled: s.enabled,
  registration_verification_required: s.registration_verification_required,
  address: s.address,
  host: s.host,
  username: s.username,
  from: s.from,
  implicit_tls: s.implicit_tls,
  password: '',
});
const candidate = (d: Draft, s: MailSettings): MailSettingsWrite => ({
  version: s.version,
  enabled: d.enabled,
  registration_verification_required: d.registration_verification_required,
  address: d.address.trim(),
  host: d.host.trim(),
  username: d.username.trim(),
  from: d.from.trim(),
  implicit_tls: d.implicit_tls,
  ...(d.password ? { password: d.password } : {}),
});
function sameSmtpTarget(a: MailSettingsFields, b: MailSettingsFields) {
  return a.address.trim() === b.address.trim() && a.host.trim().toLowerCase() === b.host.trim().toLowerCase() &&
    a.username.trim() === b.username.trim() && a.implicit_tls === b.implicit_tls;
}
const editor = useVersionedPlatformSettings<MailSettings, Draft>({
  read: platformSettings.mail,
  write: (d, s, signal) => platformSettings.saveMail(candidate(d, s), signal),
  toDraft,
  validate(d, s, operation) {
    if (!d.enabled) {
      if (operation === 'test') return '请先在草稿中启用邮件，再发送测试邮件。';
      return d.registration_verification_required ? '要求注册邮箱验证前，必须启用邮件。' : undefined;
    }
    if (!d.address.trim() || !d.host.trim() || !d.from.trim()) return '请填写 SMTP 服务器地址、TLS 服务器名称和发件邮箱。';
    const account = d.username.trim();
    if (!account && d.password) return '填写密码时请同时填写 SMTP 用户名。';
    if (account && !d.password && !(sameSmtpTarget(d, s) && s.password_configured)) return '请输入 SMTP 密码。更换服务器、TLS 模式或账号时不能沿用旧密码。';
    return undefined;
  },
  reviewRebase(previous, merged) {
    return previous.password && !sameSmtpTarget(previous, merged)
      ? {draft: {...merged, password: ''}, notice: '已保留非密码修改。SMTP 连接目标已变化，为避免密码被发给不同的服务器或账号，请确认后重新输入。'}
      : {draft: merged};
  },
  errors: {
    invalid_mail_settings: '邮件配置无效。请核对地址、认证信息和注册验证策略。',
    smtp_test_failed: 'SMTP 测试失败。请核对服务器、TLS、认证与收发件策略。',
    platform_admin_password_required: '不能关闭邮件：至少需要一位可用密码登录的平台管理员。请在邮件仍可用时为管理员设置密码，或先把已有密码的账号设为平台管理员。',
  },
});
const {snapshot, draft, busy, error, notice, conflict, dirty, edit, load, save, test, discard} = editor;
const smtpTargetChanged = computed(() => Boolean(draft.value && snapshot.value && !sameSmtpTarget(draft.value, snapshot.value)));
function editSmtpTarget(patch: Partial<MailSettingsFields>) {
  if (!draft.value) return;
  const next = {...draft.value, ...patch};
  edit({...patch, ...(!sameSmtpTarget(draft.value, next) ? {password: ''} : {})});
}
const importable = computed(() => snapshot.value?.source === 'environment' && snapshot.value.version === 0 && snapshot.value.enabled);
function sendTest() {
  return test(async (d, s, signal) => {
    const response = await platformSettings.testMail(candidate(d, s), signal);
    if (response?.ok !== true) throw new Error('Unexpected SMTP test response');
  },
    'SMTP 服务器已接受发往当前账号邮箱的测试邮件，请检查收件箱。测试不会保存配置。');
}
onMounted(() => void load());
defineExpose({ dirty, save, discard });
</script>

<template>
  <section class="platform-settings-editor" aria-label="邮件与注册设置">
    <h2>邮件与注册</h2>
    <p>配置平台发信和注册验证。测试邮件只发送到当前管理员的登录邮箱，不保存草稿。</p>
    <NAlert v-if="error" type="error" role="alert">{{ error }}</NAlert>
    <NAlert v-if="notice" type="success" role="status">{{ notice }}</NAlert>
    <p v-if="snapshot">
      <NTag size="small">{{ snapshot.source === 'database' ? '平台已保存' : '部署环境配置' }}</NTag>
      <span>　版本 {{ snapshot.version }}</span>
    </p>
    <p v-else-if="busy === 'read'">正在读取邮件设置…</p>
    <NButton v-if="!draft" :loading="busy === 'read'" @click="load()">重新读取</NButton>
    <NForm v-if="draft" :disabled="Boolean(busy)" label-placement="top" @submit.prevent="save()">
      <div class="checks">
        <NCheckbox :checked="draft.enabled" @update:checked="v => edit({enabled: v, registration_verification_required: v && draft?.registration_verification_required === true})">启用邮件</NCheckbox>
        <NCheckbox :checked="draft.registration_verification_required" :disabled="!draft.enabled"
          @update:checked="v => edit({registration_verification_required: v})">注册时必须验证邮箱</NCheckbox>
      </div>
      <NFormItem label="SMTP 服务器地址" for="platform-smtp-address">
        <NInput :value="draft.address" :input-props="{id:'platform-smtp-address'}" placeholder="smtp.example.com:587" @update:value="v => editSmtpTarget({address: v})"/>
      </NFormItem>
      <NFormItem label="TLS 服务器名称" for="platform-smtp-host">
        <NInput :value="draft.host" :input-props="{id:'platform-smtp-host'}" placeholder="smtp.example.com" @update:value="v => editSmtpTarget({host: v})"/>
      </NFormItem>
      <NFormItem label="发件邮箱" for="platform-smtp-from">
        <NInput :value="draft.from" :input-props="{id:'platform-smtp-from'}" placeholder="notify@example.com" @update:value="v => edit({from: v})"/>
      </NFormItem>
      <NFormItem label="SMTP 用户名" for="platform-smtp-username">
        <NInput :value="draft.username" :input-props="{id:'platform-smtp-username', autocomplete:'off'}" @update:value="v => editSmtpTarget({username: v})"/>
      </NFormItem>
      <NFormItem label="SMTP 密码" for="platform-smtp-password">
        <NInput type="password" :value="draft.password" :input-props="{id:'platform-smtp-password', autocomplete:'new-password'}"
          :placeholder="smtpTargetChanged && draft.username.trim() ? '连接目标已改变，请输入新密码' : snapshot?.password_configured ? '同一 SMTP 目标留空保留' : '未设置'" @update:value="v => edit({password: v})"/>
      </NFormItem>
      <p>同时留空用户名和密码会移除现有密码，改用无需认证的 SMTP 服务。</p>
      <NCheckbox :checked="draft.implicit_tls" @update:checked="v => editSmtpTarget({implicit_tls: v})">使用隐式 TLS（否则使用 STARTTLS）</NCheckbox>
      <footer>
        <NButton :loading="busy === 'read'" @click="load(dirty)">读取最新版本</NButton>
        <NButton :disabled="!draft.enabled || conflict" :loading="busy === 'test'" @click="sendTest">发测试邮件到我的邮箱</NButton>
        <NButton v-if="importable && !dirty" :disabled="conflict" :loading="busy === 'write'" type="primary" @click="save(true)">迁入邮件设置</NButton>
        <NButton v-else :disabled="!dirty || conflict" :loading="busy === 'write'" type="primary" attr-type="submit">保存邮件设置</NButton>
      </footer>
    </NForm>
  </section>
</template>

<style scoped>
.platform-settings-editor { max-width: 640px; }
.platform-settings-editor > p { color: var(--id-text-secondary); margin: 10px 0 14px; }
.platform-settings-editor > :deep(.n-alert) { margin: 10px 0; }
.checks, footer { display: flex; flex-wrap: wrap; gap: 12px; margin: 8px 0 16px; }
</style>

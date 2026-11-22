<script setup lang="ts">
import { computed, onMounted } from 'vue';
import { NAlert, NButton, NCheckbox, NForm, NFormItem, NInput, NTag } from 'naive-ui';
import { platformSettings, type GithubOAuthApp, type GithubOAuthWrite } from '../api/platform-settings';
import { useVersionedPlatformSettings } from '../composables/useVersionedPlatformSettings';

interface Draft {
  enabled: boolean;
  clientId: string;
  clientSecret: string;
  clearSecret: boolean;
}
function candidate(d: Draft, s: GithubOAuthApp): GithubOAuthWrite {
  const clientId = d.clientId.trim();
  return {
    expected_version: s.version,
    enabled: d.enabled,
    ...(clientId !== s.client_id ? { client_id: clientId } : {}),
    ...(d.clientSecret ? { client_secret: d.clientSecret } : {}),
    ...(d.clearSecret ? { clear_client_secret: true } : {}),
  };
}
const editor = useVersionedPlatformSettings<GithubOAuthApp, Draft>({
  read: platformSettings.github,
  write: (d, s, signal) => platformSettings.saveGithub(candidate(d, s), signal),
  toDraft: s => ({enabled: s.enabled, clientId: s.client_id, clientSecret: '', clearSecret: false}),
  validate(d, s) {
    if (d.clearSecret && (d.enabled || d.clientSecret)) return '清除密钥前必须关闭 GitHub 插件 OAuth，且不能同时填写新密钥。';
    if (d.enabled && d.clientId.trim() !== s.client_id && !d.clientSecret) return '更换 Client ID 时必须输入该应用的新 Client Secret。';
    if (d.enabled && (!d.clientId.trim() || !(d.clientSecret || s.has_client_secret))) return '启用需要填写 Client ID 并配置 Client Secret。';
    return undefined;
  },
  reviewRebase(previous, merged) {
    return previous.clientSecret && previous.clientId.trim() !== merged.clientId.trim()
      ? {draft: {...merged, clientSecret: ''}, notice: '已保留其他修改。Client ID 已变化，请核对 OAuth 应用并重新输入 Client Secret。'}
      : {draft: merged};
  },
  errors: {
    invalid_oauth_app_config: 'GitHub OAuth 配置无效。请核对 Client ID、Client Secret 和启停状态。',
    oauth_app_client_secret_required: '更换 Client ID 时必须输入该应用的新 Client Secret。',
  },
});
const {snapshot, draft, busy, error, notice, conflict, dirty, edit, load, save, discard} = editor;
const importable = computed(() => snapshot.value?.source === 'legacy_env' && snapshot.value.version === 0);
const clientIdChanged = computed(() => draft.value?.clientId.trim() !== snapshot.value?.client_id);
function editClientId(value: string) {
  const reset = value.trim() !== draft.value?.clientId.trim();
  edit({clientId: value, ...(reset ? {clientSecret: '', clearSecret: false} : {})});
}
const sourceLabel = computed(() => snapshot.value?.source === 'database' ? '平台已保存'
  : snapshot.value?.source === 'legacy_env' ? '部署环境配置' : '未配置');
onMounted(() => void load());
defineExpose({ dirty, save, discard });
</script>

<template>
  <section class="platform-settings-editor" aria-label="集成设置">
    <h2>集成设置</h2>
    <h3>GitHub 插件 OAuth</h3>
    <p>供 AI 员工的 GitHub 插件账号授权使用，不用于登录 KIKO 网站。</p>
    <NAlert v-if="error" type="error" role="alert">{{ error }}</NAlert>
    <NAlert v-if="notice" type="success" role="status">{{ notice }}</NAlert>
    <p v-if="snapshot"><NTag size="small">{{ sourceLabel }}</NTag><span>　版本 {{ snapshot.version }}</span></p>
    <p v-else-if="busy === 'read'">正在读取集成设置…</p>
    <NButton v-if="!draft" :loading="busy === 'read'" @click="load()">重新读取</NButton>
    <NForm v-if="draft" :disabled="Boolean(busy)" label-placement="top" @submit.prevent="save()">
      <NCheckbox :checked="draft.enabled" @update:checked="v => edit({enabled: v, ...(v ? {clearSecret: false} : {})})">启用 GitHub 插件 OAuth</NCheckbox>
      <NFormItem label="GitHub 应用回调地址" for="platform-github-oauth-redirect">
        <NInput :value="snapshot?.redirect_uri || ''" :input-props="{id:'platform-github-oauth-redirect'}" readonly />
      </NFormItem>
      <NFormItem label="Client ID" for="platform-github-oauth-id">
        <NInput :value="draft.clientId" :input-props="{id:'platform-github-oauth-id', autocomplete:'off'}" @update:value="editClientId"/>
      </NFormItem>
      <NFormItem label="Client Secret" for="platform-github-oauth-secret">
        <NInput type="password" :value="draft.clientSecret" :input-props="{id:'platform-github-oauth-secret', autocomplete:'new-password'}"
          :placeholder="clientIdChanged ? '已更换应用，请输入新密钥' : snapshot?.has_client_secret ? '已设置，留空保留' : '未设置'" :disabled="draft.clearSecret" @update:value="v => edit({clientSecret: v})"/>
      </NFormItem>
      <p v-if="clientIdChanged">更换 Client ID 不会沿用旧应用的 Client Secret。停用状态下不填新密钥并保存，会清除旧密钥。</p>
      <NCheckbox v-if="snapshot?.has_client_secret && !clientIdChanged" :checked="draft.clearSecret" :disabled="draft.enabled || Boolean(draft.clientSecret)"
        @update:checked="v => edit({clearSecret: v})">保存时清除原 Client Secret（仅停用时可选）</NCheckbox>
      <footer>
        <NButton :loading="busy === 'read'" @click="load(dirty)">读取最新版本</NButton>
        <NButton v-if="importable && !dirty" :disabled="conflict" :loading="busy === 'write'" type="primary" @click="save(true)">迁入 GitHub OAuth 设置</NButton>
        <NButton v-else :disabled="!dirty || conflict" :loading="busy === 'write'" type="primary" attr-type="submit">保存 GitHub OAuth 设置</NButton>
      </footer>
    </NForm>
  </section>
</template>

<style scoped>
.platform-settings-editor { max-width: 640px; }
.platform-settings-editor > p { color: var(--id-text-secondary); margin: 10px 0 14px; }
.platform-settings-editor > :deep(.n-alert), .platform-settings-editor :deep(.n-form > .n-checkbox) { margin: 10px 0; }
footer { display: flex; flex-wrap: wrap; gap: 12px; margin: 12px 0; }
</style>

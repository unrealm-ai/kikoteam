<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue';
import { NAlert, NButton, NCheckbox, NForm, NFormItem, NInput, NTag } from 'naive-ui';
import { platformSettings, type GithubOAuthApp, type GithubOAuthWrite } from '../api/platform-settings';
import { useVersionedPlatformSettings } from '../composables/useVersionedPlatformSettings';
import { copyText } from '../utils/clipboard';

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
const clientIdChanged = computed(() => draft.value?.clientId.trim() !== snapshot.value?.client_id);
function editClientId(value: string) {
  const reset = value.trim() !== draft.value?.clientId.trim();
  edit({clientId: value, ...(reset ? {clientSecret: '', clearSecret: false} : {})});
}
const sourceLabel = computed(() => snapshot.value?.source === 'database' ? '平台已保存'
  : '未配置');

const copyState = ref<'idle' | 'copied' | 'failed'>('idle');
let copyTimer: ReturnType<typeof setTimeout> | undefined;
async function copyRedirectUri() {
  const uri = snapshot.value?.redirect_uri || '';
  if (!uri) return;
  clearTimeout(copyTimer);
  const inputEl = document.querySelector<HTMLInputElement>('#platform-github-oauth-redirect');
  try {
    await copyText(uri, inputEl);
    copyState.value = 'copied';
  } catch {
    copyState.value = 'failed';
  }
  copyTimer = setTimeout(() => { copyState.value = 'idle'; }, 2400);
}
onMounted(() => void load());
onUnmounted(() => clearTimeout(copyTimer));
defineExpose({ dirty, save, discard });
</script>

<template>
  <section class="platform-settings-editor" aria-label="集成设置">
    <header class="editor-card-head">
      <div class="editor-title-block">
        <h2>集成设置</h2>
        <h3>GitHub 插件 OAuth</h3>
        <p class="editor-desc">供 AI 员工的 GitHub 插件账号授权使用，不用于登录 KIKO 网站。</p>
      </div>
      <div v-if="snapshot || dirty" class="editor-meta-pills">
        <template v-if="snapshot">
          <NTag size="small">{{ sourceLabel }}</NTag>
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
      <span>正在读取集成设置…</span>
    </div>
    <div v-else-if="!draft" class="editor-loading-state">
      <span>尚未加载集成设置</span>
      <NButton size="small" :loading="busy === 'read'" @click="load()">重新读取</NButton>
    </div>

    <NForm v-if="draft" :disabled="Boolean(busy)" label-placement="top" class="editor-form-stack" @submit.prevent="save()">
      <div class="setting-toggle-box">
        <NCheckbox :checked="draft.enabled" @update:checked="v => edit({enabled: v, ...(v ? {clearSecret: false} : {})})">启用 GitHub 插件 OAuth</NCheckbox>
        <p class="setting-toggle-help">开启后，工作区管理员或员工配置可通过 OAuth 授权绑定 GitHub 插件账号。</p>
      </div>

      <div class="editor-subpanel">
        <NFormItem label="GitHub 应用回调地址" for="platform-github-oauth-redirect">
          <div class="inline-input-copy">
            <NInput :value="snapshot?.redirect_uri || ''" :input-props="{id:'platform-github-oauth-redirect'}" readonly class="mono-input" />
            <NButton size="small" secondary :disabled="!snapshot?.redirect_uri" @click="copyRedirectUri">复制回调地址</NButton>
          </div>
        </NFormItem>
        <p v-if="copyState === 'copied'" class="copy-feedback copy-feedback--ok">已复制回调地址到剪贴板</p>
        <p v-else-if="copyState === 'failed'" class="copy-feedback copy-feedback--err">复制失败，请手动选中复制</p>

        <div class="github-fields-grid">
          <NFormItem label="Client ID" for="platform-github-oauth-id">
            <NInput :value="draft.clientId" :input-props="{id:'platform-github-oauth-id', autocomplete:'off'}" placeholder="Ov23li..." @update:value="editClientId"/>
          </NFormItem>
          <NFormItem label="Client Secret" for="platform-github-oauth-secret">
            <NInput type="password" show-password-on="click" :value="draft.clientSecret" :input-props="{id:'platform-github-oauth-secret', autocomplete:'new-password'}"
              :placeholder="clientIdChanged ? '已更换应用，请输入新密钥' : snapshot?.has_client_secret ? '已设置，留空保留' : '未设置'" :disabled="draft.clearSecret" @update:value="v => edit({clientSecret: v})"/>
          </NFormItem>
        </div>
        <div v-if="clientIdChanged" class="scope-notice-strip scope-notice-strip--warning">
          <p class="notice-text">更换 Client ID 不会沿用旧应用的 Client Secret。停用状态下不填新密钥并保存，会清除旧密钥。</p>
        </div>
      </div>

      <div v-if="snapshot?.has_client_secret && !clientIdChanged" class="setting-toggle-box setting-toggle-box--danger">
        <NCheckbox :checked="draft.clearSecret" :disabled="draft.enabled || Boolean(draft.clientSecret)"
          @update:checked="v => edit({clearSecret: v})">保存时清除原 Client Secret（仅停用时可选）</NCheckbox>
      </div>

      <footer class="editor-footer">
        <NButton size="small" secondary :loading="busy === 'read'" @click="load(dirty)">读取最新版本</NButton>
        <div class="editor-footer-primary">
          <NButton size="small" :disabled="!dirty || conflict" :loading="busy === 'write'" type="primary" attr-type="submit">保存 GitHub OAuth 设置</NButton>
        </div>
      </footer>
    </NForm>
  </section>
</template>

<style scoped>
.mono-input :deep(input) {
  font-family: var(--id-font-mono);
}
.github-fields-grid {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 0 12px;
}
.copy-feedback {
  margin: -6px 0 8px 0;
  font-size: 12px;
}
.copy-feedback--ok {
  color: var(--id-status-success);
}
.copy-feedback--err {
  color: var(--id-status-danger);
}
@media (max-width: 760px) {
  .github-fields-grid {
    grid-template-columns: 1fr;
  }
}
</style>

<style scoped src="../components/workspace-settings-shared.css"></style>

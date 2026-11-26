<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue';
import { NAlert, NButton, NModal } from 'naive-ui';
import { referralsApi, referralSettingsChanged } from '../api/referrals';

const enabled = ref(false), open = ref(false), loading = ref(false);
const code = ref(''), error = ref(''), notice = ref('');
const linkInput = ref<HTMLInputElement>();
const url = computed(() => code.value ? new URL(`/r/${code.value}`, location.origin).href : '');
let active = true;
let refreshRequest: AbortController | undefined;

async function refresh() {
  refreshRequest?.abort();
  const request = refreshRequest = new AbortController();
  try {
    const result = await referralsApi.link({ signal: request.signal });
    if (!active || request.signal.aborted) return;
    enabled.value = result.enabled;
    code.value = result.code || '';
    if (!enabled.value) open.value = false;
  } catch {
    // This optional feature must not block the workbench.
  }
}
function onVisibility() {
  if (!document.hidden) void refresh();
}
async function showInvite() {
  open.value = true;
  error.value = notice.value = '';
  if (loading.value) return;
  loading.value = true;
  try {
    const result = await referralsApi.create();
    if (!active) return;
    enabled.value = result.enabled;
    code.value = result.code || '';
    if (!result.enabled) open.value = false;
  } catch (e) {
    if (active) error.value = e instanceof Error ? e.message : '无法创建邀请链接，请重试';
  } finally {
    loading.value = false;
  }
}
async function copyInvite() {
  if (!url.value) return;
  try {
    await navigator.clipboard.writeText(url.value);
    notice.value = '邀请链接已复制';
  } catch {
    linkInput.value?.focus();
    linkInput.value?.select();
    notice.value = '请手动复制选中的链接';
  }
}
onMounted(() => {
  void refresh();
  window.addEventListener(referralSettingsChanged, refresh);
  document.addEventListener('visibilitychange', onVisibility);
});
onUnmounted(() => {
  active = false;
  refreshRequest?.abort();
  window.removeEventListener(referralSettingsChanged, refresh);
  document.removeEventListener('visibilitychange', onVisibility);
});
</script>

<template>
  <NButton v-if="enabled" type="primary" size="small" aria-label="邀请得积分" @click="showInvite">
    <template #icon>
      <svg viewBox="0 0 24 24" width="17" height="17" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linejoin="round" aria-hidden="true">
        <circle cx="12" cy="12" r="9" />
        <path d="M12 7.5v9m-4.5-4.5h9" />
      </svg>
    </template>
    邀请得积分
  </NButton>
  <NModal v-model:show="open" preset="card" title="邀请得积分" class="referral-invite-dialog">
    <p>每成功邀请 1 位新用户注册并验证邮箱，你可获得 <strong>100 积分</strong>。</p>
    <p>链接长期有效。朋友会进入自己的工作区，不会加入你的工作区。</p>
    <NAlert v-if="error" type="error">{{ error }} <NButton text @click="showInvite">重试</NButton></NAlert>
    <div v-else class="referral-copy">
      <input ref="linkInput" :value="url" readonly aria-label="你的邀请链接" placeholder="正在生成邀请链接…" />
      <NButton type="primary" :loading="loading" :disabled="!url" @click="copyInvite">复制链接</NButton>
    </div>
    <p v-if="notice" role="status">{{ notice }}</p>
  </NModal>
</template>

<style scoped>
.referral-invite-dialog { width: min(480px, calc(100vw - 32px)); }
.referral-copy { display: flex; gap: 8px; align-items: center; }
.referral-copy input { flex: 1; width: 0; border: 1px solid var(--n-border-color, #d9d9df); color: inherit; background: transparent; border-radius: 6px; padding: 6px 8px; }
</style>

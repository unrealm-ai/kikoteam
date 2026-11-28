<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue';
import { NAlert, NButton, NModal } from 'naive-ui';
import { referralsApi, referralSettingsChanged } from '../api/referrals';

defineProps<{ muted?: boolean }>();

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
  <button
    v-if="enabled"
    type="button"
    class="referral-trigger"
    :class="{ 'is-muted': muted }"
    aria-label="邀请得积分"
    aria-haspopup="dialog"
    :aria-expanded="open"
    title="邀请得积分"
    @click="showInvite"
  >
    <span class="referral-trigger-icon" aria-hidden="true">🎁</span>
    <span class="referral-trigger-label">邀请得积分</span>
    <span class="referral-trigger-reward" aria-hidden="true">+100</span>
  </button>
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
.referral-trigger {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  flex-shrink: 0;
  height: 34px;
  padding: 0 6px 0 5px;
  border: 1px solid #FCD34D;
  border-radius: var(--id-radius-pill);
  background: linear-gradient(135deg, #FFFBEB 0%, #FEF3C7 100%);
  color: #92400E;
  font: 600 12.5px/1 var(--id-font-sans);
  white-space: nowrap;
  cursor: pointer;
  box-shadow: 0 1px 4px rgba(245, 158, 11, 0.14);
  transition: background 0.15s ease, border-color 0.15s ease, color 0.15s ease, box-shadow 0.15s ease;
}
.referral-trigger:hover {
  background: linear-gradient(135deg, #FEF3C7 0%, #FDE68A 100%);
  border-color: #F59E0B;
}
.referral-trigger:focus-visible {
  outline: 2px solid #D97706;
  outline-offset: 2px;
}
.referral-trigger-icon {
  display: grid;
  place-items: center;
  width: 22px;
  height: 22px;
  flex-shrink: 0;
  border-radius: 50%;
  background: #FFFFFF;
  font-size: 14px;
  line-height: 1;
  box-shadow: 0 1px 2px rgba(28, 25, 23, 0.06);
}
.referral-trigger-reward {
  padding: 3px 6px;
  border-radius: var(--id-radius-pill);
  background: rgba(245, 158, 11, 0.18);
  color: #92400E;
  font-size: 11px;
  font-weight: 700;
  font-variant-numeric: tabular-nums;
}
.referral-trigger.is-muted {
  background: #FFFFFF;
  border-color: #DFDDD6;
  color: #57534E;
  font-weight: 500;
  box-shadow: 0 1px 2px rgba(28, 25, 23, 0.03);
}
.referral-trigger.is-muted:hover {
  background: var(--id-bg-subtle);
  border-color: var(--id-border-strong);
  color: var(--id-text-primary);
}
.referral-trigger.is-muted .referral-trigger-icon {
  background: var(--id-bg-muted);
}
.referral-trigger.is-muted .referral-trigger-reward {
  background: var(--id-bg-muted);
  color: var(--id-text-secondary);
}
@media (max-width: 760px) {
  .referral-trigger-label { display: none; }
}

.referral-invite-dialog { width: min(480px, calc(100vw - 32px)); }
.referral-copy { display: flex; gap: 8px; align-items: center; }
.referral-copy input { flex: 1; width: 0; border: 1px solid var(--n-border-color, #d9d9df); color: inherit; background: transparent; border-radius: 6px; padding: 6px 8px; }
</style>

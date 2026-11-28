<script setup lang="ts">
import { onMounted, ref } from 'vue';
import { NAlert, NButton, NCheckbox } from 'naive-ui';
import { referralsApi, referralSettingsChanged } from '../api/referrals';

const settings = ref<Awaited<ReturnType<typeof referralsApi.settings>>>();
const enabled = ref(false), busy = ref(false), error = ref(''), saved = ref(false);

async function load() {
  busy.value = true;
  error.value = '';
  saved.value = false;
  try {
    settings.value = await referralsApi.settings();
    enabled.value = settings.value.enabled;
  } catch (e) {
    error.value = e instanceof Error ? e.message : '无法读取邀请奖励设置';
  } finally {
    busy.value = false;
  }
}
async function save() {
  if (busy.value || !settings.value) return;
  busy.value = true;
  error.value = '';
  saved.value = false;
  try {
    settings.value = await referralsApi.saveSettings({ version: settings.value.version, enabled: enabled.value });
    enabled.value = settings.value.enabled;
    saved.value = true;
    window.dispatchEvent(new window.Event(referralSettingsChanged));
  } catch (e) {
    error.value = e instanceof Error ? e.message : '保存失败';
  } finally {
    busy.value = false;
  }
}
onMounted(load);
</script>

<template>
  <form class="registration-setting-card" @submit.prevent="save">
    <div class="signup-bonus-copy">
      <strong>邀请拉新奖励</strong>
      <span class="sub-desc">每位通过邀请链接注册并验证邮箱的新用户，为邀请人带来 100 积分。关闭后旧链接仍可注册或登录，但不发奖励。</span>
      <NAlert v-if="error" type="error">{{ error }} <NButton text :disabled="busy" @click="load">重新读取</NButton></NAlert>
      <span v-if="saved" class="sub-desc" role="status">邀请奖励设置已保存</span>
    </div>
    <div class="signup-bonus-control">
      <NCheckbox v-model:checked="enabled" :disabled="busy || !settings" @update:checked="saved = false">开启奖励</NCheckbox>
      <NButton attr-type="submit" type="primary" size="small" :loading="busy" :disabled="!settings || enabled === settings.enabled">保存邀请设置</NButton>
    </div>
  </form>
</template>

<style scoped src="../components/registration-settings-cards.css"></style>

<script setup lang="ts">
import { onMounted } from 'vue';
import { NAlert, NButton, NForm, NFormItem, NInput } from 'naive-ui';
import { platformSettings, type TimezoneSettings } from '../api/platform-settings';
import { useVersionedPlatformSettings } from '../composables/useVersionedPlatformSettings';

const { draft, busy, error, notice, conflict, dirty, edit, load, save, discard } =
  useVersionedPlatformSettings<TimezoneSettings, { timezone: string }>({
    read: platformSettings.timezone,
    write: (d, snapshot, signal) => platformSettings.saveTimezone({
      expected_version: snapshot.version, timezone: d.timezone.trim(),
    }, signal),
    toDraft: s => ({ timezone: s.timezone }),
    validate: d => d.timezone.trim() ? undefined : '请输入 IANA 时区。',
    errors: {},
  });
onMounted(() => void load());
defineExpose({ dirty, save, discard });
</script>

<template>
  <section class="platform-settings-editor" aria-label="平台定时时区">
    <header class="editor-card-head">
      <div class="editor-title-block">
        <h2>平台时区</h2>
        <p class="editor-desc">默认北京时间。更改会重新安排所有定时触发的下次时间，不改已有会话定时或运行中的任务。</p>
      </div>
    </header>
    <NAlert v-if="error" type="error">{{ error }}</NAlert>
    <NAlert v-if="notice" type="success">{{ notice }}</NAlert>
    <NForm v-if="draft" :disabled="Boolean(busy)" @submit.prevent="save()">
      <NFormItem label="IANA 时区">
        <NInput :value="draft.timezone" :input-props="{ id: 'platform-schedule-timezone' }"
          placeholder="Asia/Shanghai" maxlength="128" @update:value="v => edit({ timezone: v })" />
      </NFormItem>
      <div class="editor-footer">
        <NButton size="small" secondary :loading="busy === 'read'" @click="load(dirty)">读取最新版本</NButton>
        <NButton size="small" :disabled="!dirty || conflict" :loading="busy === 'write'" type="primary" attr-type="submit">保存平台时区</NButton>
      </div>
    </NForm>
    <NButton v-else size="small" :loading="busy === 'read'" @click="load()">读取时区</NButton>
  </section>
</template>

<style scoped src="../components/workspace-settings-shared.css"></style>

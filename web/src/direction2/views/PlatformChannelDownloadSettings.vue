<script setup lang="ts">
import { onMounted } from 'vue';
import { NAlert, NButton, NCheckbox, NForm } from 'naive-ui';
import { platformSettings, type ChannelDownloadSettings } from '../api/platform-settings';
import { useVersionedPlatformSettings } from '../composables/useVersionedPlatformSettings';

type Draft = Pick<ChannelDownloadSettings, 'validate_public_ip'>;
const {draft, busy, error, notice, conflict, dirty, edit, load, save, discard} =
  useVersionedPlatformSettings<ChannelDownloadSettings, Draft>({
    read: platformSettings.channelDownloads,
    write: (d, snapshot, signal) => platformSettings.saveChannelDownloads({
      expected_version: snapshot.version,
      validate_public_ip: d.validate_public_ip,
    }, signal),
    toDraft: s => ({validate_public_ip: s.validate_public_ip}),
    validate: () => undefined,
    errors: {},
  });

onMounted(() => void load());
defineExpose({ dirty, save, discard });
</script>

<template>
  <section class="platform-settings-editor" aria-label="附件下载设置">
    <h2>附件下载</h2>
    <p>适用于钉钉和企业微信的入站附件，保存后从下一次下载尝试起生效。</p>
    <NAlert v-if="error" type="error" role="alert">{{ error }}</NAlert>
    <NAlert v-if="notice" type="success" role="status">{{ notice }}</NAlert>
    <p v-if="!draft && busy === 'read'">正在读取附件下载设置…</p>
    <NButton v-else-if="!draft" @click="load()">重新读取</NButton>
    <NForm v-if="draft" :disabled="Boolean(busy)" @submit.prevent="save()">
      <NCheckbox :checked="draft.validate_public_ip" aria-describedby="channel-download-public-ip-help"
        @update:checked="v => edit({validate_public_ip: v})">校验公网 IP</NCheckbox>
      <p id="channel-download-public-ip-help">
        开启时仅连接解析到的公网 IP。关闭后允许连接 DNS 返回的内网地址，适用于通过可信内网网关下载附件的部署。
      </p>
      <footer>
        <NButton :loading="busy === 'read'" @click="load(dirty)">读取最新版本</NButton>
        <NButton :disabled="!dirty || conflict" :loading="busy === 'write'" type="primary" attr-type="submit">保存附件下载设置</NButton>
      </footer>
    </NForm>
  </section>
</template>

<style scoped>
.platform-settings-editor { max-width: 640px; }
.platform-settings-editor p { color: var(--id-text-secondary); margin: 10px 0 14px; }
.platform-settings-editor > :deep(.n-alert) { margin: 10px 0; }
footer { display: flex; flex-wrap: wrap; gap: 12px; margin: 12px 0; }
</style>

<script setup lang="ts">
import { onMounted } from 'vue';
import { NAlert, NButton, NCheckbox, NForm } from 'naive-ui';
import { platformSettings, type ChannelDownloadSettings } from '../api/platform-settings';
import { useVersionedPlatformSettings } from '../composables/useVersionedPlatformSettings';

type Draft = Pick<ChannelDownloadSettings, 'validate_public_ip'>;
const {snapshot, draft, busy, error, notice, conflict, dirty, edit, load, save, discard} =
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
    <header class="editor-card-head">
      <div class="editor-title-block">
        <h2>附件下载</h2>
        <p class="editor-desc">适用于钉钉和企业微信的入站附件，保存后从下一次下载尝试起生效。</p>
      </div>
      <div v-if="snapshot || dirty" class="editor-meta-pills">
        <span v-if="snapshot" class="mono-pill tabular-num">版本 {{ snapshot.version }}</span>
        <span v-if="dirty" class="status-pill status-pill--warning">
          <span class="status-dot dot--warning"></span>
          <span>未保存</span>
        </span>
      </div>
    </header>

    <NAlert v-if="error" type="error" role="alert">{{ error }}</NAlert>
    <NAlert v-if="notice" type="success" role="status">{{ notice }}</NAlert>

    <div v-if="!draft && busy === 'read'" class="editor-loading-state">
      <span>正在读取附件下载设置…</span>
    </div>
    <div v-else-if="!draft" class="editor-loading-state">
      <span>尚未加载附件下载设置</span>
      <NButton size="small" @click="load()">重新读取</NButton>
    </div>

    <NForm v-if="draft" :disabled="Boolean(busy)" class="editor-form-stack" @submit.prevent="save()">
      <div class="setting-toggle-box">
        <NCheckbox :checked="draft.validate_public_ip" aria-describedby="channel-download-public-ip-help"
          @update:checked="v => edit({validate_public_ip: v})">校验公网 IP</NCheckbox>
        <p id="channel-download-public-ip-help" class="setting-toggle-help">
          开启时仅连接解析到的公网 IP。关闭后允许连接 DNS 返回的内网地址，适用于通过可信内网网关下载附件的部署。
        </p>
      </div>

      <footer class="editor-footer">
        <NButton size="small" secondary :loading="busy === 'read'" @click="load(dirty)">读取最新版本</NButton>
        <div class="editor-footer-primary">
          <NButton size="small" :disabled="!dirty || conflict" :loading="busy === 'write'" type="primary" attr-type="submit">保存附件下载设置</NButton>
        </div>
      </footer>
    </NForm>
  </section>
</template>

<style scoped src="../components/workspace-settings-shared.css"></style>

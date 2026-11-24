<script setup lang="ts">
import { onMounted } from 'vue';
import { NAlert, NButton, NCheckbox, NForm, NFormItem, NInput } from 'naive-ui';
import { platformSettings, type TaskLinkSettings } from '../api/platform-settings';
import { useVersionedPlatformSettings } from '../composables/useVersionedPlatformSettings';

type Draft = Pick<TaskLinkSettings, 'origin' | 'enabled'>;

function validate(d: Draft) {
  if (!d.origin.trim()) return undefined;
  try {
    const source = d.origin.trim(), url = new URL(source);
    if (['https:', 'http:'].includes(url.protocol) && url.hostname && !url.username && !url.password &&
        url.pathname === '/' && !/[?#]/.test(source)) return undefined;
  } catch { /* report validation below */ }
  return '请填写 HTTP 或 HTTPS 地址，可使用域名、IP 和端口，不要包含路径、参数或凭据。';
}
const { draft, busy, error, notice, conflict, dirty, edit, load, save, discard } =
  useVersionedPlatformSettings<TaskLinkSettings, Draft>({
    read: platformSettings.taskLinks,
    write: (d, snapshot, signal) => platformSettings.saveTaskLinks({
      expected_version: snapshot.version, origin: d.origin.trim(), enabled: d.enabled,
    }, signal),
    toDraft: s => ({ origin: s.origin, enabled: s.enabled }),
    validate,
    errors: { task_link_settings_version_conflict: '设置已被更新，请先读取最新版本。' },
  });
function editOrigin(origin: string) { edit({ origin }); }
function editEnabled(enabled: boolean) { edit({ enabled }); }
onMounted(() => void load());
defineExpose({ dirty, save, discard });
</script>

<template>
  <section class="platform-settings-editor" aria-label="任务详情链接">
    <h2>任务详情链接</h2>
    <p>同时配置任务链接地址并勾选此设置，普通主对话才能发送任务详情卡片。关闭不会撤销已发出的链接。</p>
    <NAlert v-if="error" type="error" role="alert">{{ error }}</NAlert>
    <NAlert v-if="notice" type="success" role="status">{{ notice }}</NAlert>
    <p v-if="!draft && busy === 'read'">正在读取任务详情链接设置…</p>
    <NButton v-else-if="!draft" @click="load()">重新读取</NButton>
    <NForm v-if="draft" :disabled="Boolean(busy)" @submit.prevent="save()">
      <NFormItem label="任务链接地址" for="platform-task-origin">
        <NInput :value="draft?.origin || ''" :input-props="{ id: 'platform-task-origin' }" placeholder="https://kikoteam.com/ 或 http://192.168.1.10:8080/"
          @update:value="editOrigin" />
      </NFormItem>
      <NCheckbox :checked="draft?.enabled" :disabled="!draft?.origin.trim()" :aria-disabled="!draft?.origin.trim()" aria-describedby="task-link-help"
        @update:checked="editEnabled">允许发送任务详情卡片</NCheckbox>
      <p id="task-link-help">打开链接的设备须能直接访问此地址。使用 HTTP 时链接和任务数据会明文传输，请只在可信内网使用；公网使用 HTTPS。未配置地址时不能发卡，但会保留勾选状态。新卡片链接默认有效 3 天。</p>
      <footer>
        <NButton :loading="busy === 'read'" @click="load(dirty)">读取最新版本</NButton>
        <NButton :disabled="!dirty || conflict" :loading="busy === 'write'" type="primary" attr-type="submit">保存任务详情链接设置</NButton>
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

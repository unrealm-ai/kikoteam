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
const { snapshot, draft, busy, error, notice, conflict, dirty, edit, load, save, discard } =
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
    <header class="editor-card-head">
      <div class="editor-title-block">
        <h2>任务详情链接</h2>
        <p class="editor-desc">同时配置任务链接地址并勾选此设置，普通主对话才能发送任务详情卡片。关闭不会撤销已发出的链接。</p>
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
      <span>正在读取任务详情链接设置…</span>
    </div>
    <div v-else-if="!draft" class="editor-loading-state">
      <span>尚未加载任务详情链接设置</span>
      <NButton size="small" @click="load()">重新读取</NButton>
    </div>

    <NForm v-if="draft" :disabled="Boolean(busy)" label-placement="top" class="editor-form-stack" @submit.prevent="save()">
      <div class="editor-subpanel">
        <NFormItem label="任务链接地址" for="platform-task-origin">
          <NInput :value="draft?.origin || ''" :input-props="{ id: 'platform-task-origin' }" placeholder="https://kikoteam.com/ 或 http://192.168.1.10:8080/"
            @update:value="editOrigin" />
        </NFormItem>
        <div class="setting-toggle-box">
          <NCheckbox :checked="draft?.enabled" :disabled="!draft?.origin.trim()" :aria-disabled="!draft?.origin.trim()" aria-describedby="task-link-help"
            @update:checked="editEnabled">允许发送任务详情卡片</NCheckbox>
          <p id="task-link-help" class="setting-toggle-help">打开链接的设备须能直接访问此地址。使用 HTTP 时链接和任务数据会明文传输，请只在可信内网使用；公网使用 HTTPS。未配置地址时不能发卡，但会保留勾选状态。新链接默认有效 3 天。持有者可以查看授权任务的执行过程和产物；请勿把链接发给不应查看这些数据的人。</p>
        </div>
      </div>

      <footer class="editor-footer">
        <NButton size="small" secondary :loading="busy === 'read'" @click="load(dirty)">读取最新版本</NButton>
        <div class="editor-footer-primary">
          <NButton size="small" :disabled="!dirty || conflict" :loading="busy === 'write'" type="primary" attr-type="submit">保存任务详情链接设置</NButton>
        </div>
      </footer>
    </NForm>
  </section>
</template>

<style scoped src="../components/workspace-settings-shared.css"></style>

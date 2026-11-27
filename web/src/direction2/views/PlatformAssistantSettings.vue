<script setup lang="ts">
import {computed, onMounted, onUnmounted, ref} from 'vue';
import {NAlert, NButton, NCheckbox, NSkeleton} from 'naive-ui';
import {assistantApi, type AssistantSettings} from '../api/assistant';
import type { DefaultOfficialModelStatus } from '../api/models';
defineProps<{modelStatus: DefaultOfficialModelStatus}>();
const saved = ref<AssistantSettings>(), enabled = ref(false), error = ref(''), notice = ref('');
const busy = ref(false);
let controller = new AbortController();
const dirty = computed(() => !!saved.value && enabled.value !== saved.value.enabled);
function discard() { enabled.value = saved.value?.enabled ?? false; error.value = ''; }
async function load() {
  controller.abort();
  controller = new AbortController();
  const {signal} = controller;
  busy.value = true; error.value = '';
  try { const value = await assistantApi.settings(signal); if (!signal.aborted) { saved.value = value; enabled.value = value.enabled; } }
  catch (e) { if (!signal.aborted) error.value = e instanceof Error ? e.message : '读取助手开关失败'; }
  finally { if (!signal.aborted) busy.value = false; }
}
async function save() {
  if (!saved.value || busy.value || !dirty.value) return !dirty.value;
  const {signal} = controller;
  busy.value = true; notice.value = ''; error.value = '';
  try {
    const value = await assistantApi.save({...saved.value, enabled: enabled.value}, signal);
    if (signal.aborted) return false;
    saved.value = value; enabled.value = value.enabled; notice.value = '助手发放设置已保存。'; return true;
  } catch (e) {
    if (!signal.aborted) error.value = e instanceof Error ? e.message : '保存助手设置失败';
    return false;
  } finally { if (!signal.aborted) busy.value = false; }
}
onMounted(load);
onUnmounted(() => controller.abort());
defineExpose({dirty, save, discard});
</script>

<template>
  <section class="settings-card assistant-settings">
    <h3>KIKO 助手</h3>
    <p>仅当平台默认模型已验证并启用时，才为新注册或手动新建的工作区发放助手。不会补发给旧工作区，关闭也不影响已经发放的助手。</p>
    <p v-if="modelStatus.state === 'ready'">当前默认模型：{{ modelStatus.name }}（已验证、已启用）</p>
    <p v-else-if="modelStatus.state === 'not-ready'">当前默认模型「{{ modelStatus.name }}」不可用，请先验证并启用。</p>
    <p v-else-if="modelStatus.state === 'missing'">还没有默认模型。请在上方添加模型，验证并启用后设为默认。</p>
    <p v-else-if="modelStatus.state === 'unavailable'">暂时无法读取默认模型状态，请在上方重试。</p>
    <p v-else>正在读取默认模型状态…</p>
    <NSkeleton v-if="busy && !saved" text />
    <NAlert v-if="error" type="error">{{ error }} <NButton size="small" @click="load">重新读取</NButton></NAlert>
    <template v-if="saved">
      <NCheckbox v-model:checked="enabled" :disabled="busy">为新工作区发放 KIKO 助手</NCheckbox>
      <NButton type="primary" size="small" :loading="busy" :disabled="!dirty" @click="save">保存</NButton>
    </template>
    <p v-if="notice" role="status">{{notice}}</p>
  </section>
</template>

<style scoped>
.assistant-settings{padding:16px}.assistant-settings :deep(.n-button){margin-inline-start:12px}
</style>

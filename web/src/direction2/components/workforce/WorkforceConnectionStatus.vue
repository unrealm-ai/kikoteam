<script setup lang="ts">
import { computed, ref, watch } from 'vue';
import { NTag } from 'naive-ui';
const props = defineProps<{ connected: boolean; errorCode?: string | undefined; realBackend: boolean; statusText?: string | undefined }>();
const hovered = ref(false), focused = ref(false);
const code = computed(() => !props.connected && props.realBackend && /^[a-z][a-z0-9_]{0,95}$/.test(props.errorCode || '') ? props.errorCode : undefined);
const label = computed(() => props.realBackend && props.statusText || (props.connected ? '已连接' : '未连接'));
watch(() => [props.connected, props.errorCode, props.realBackend], () => { hovered.value = false; focused.value = false; });
</script>
<template>
  <span class="connection-status" :tabindex="code ? 0 : undefined" :aria-label="code ? `${label}，错误码：${code}` : undefined" @mouseenter="hovered = true" @mouseleave="hovered = false" @focus="focused = true" @blur="focused = false" @keydown.esc="hovered = false; focused = false">
    <NTag size="small" :type="connected ? 'success' : code ? 'warning' : 'default'">{{ label }}</NTag>
    <span v-if="code && (hovered || focused)" role="tooltip" class="connection-tooltip">错误码：{{ code }}</span>
  </span>
</template>
<style scoped>
.connection-status { position:relative;display:inline-flex; }
.connection-status:focus-visible { outline:2px solid var(--id-primary,#406cff);outline-offset:3px;border-radius:4px; }
.connection-tooltip { position:absolute;bottom:calc(100% + 6px);left:0;z-index:100;max-width:280px;width:max-content;overflow-wrap:anywhere;padding:6px 8px;border-radius:4px;background:var(--id-bg-surface,#fff);color:var(--id-text-primary,#222);border:1px solid var(--id-border-subtle,#ddd);box-shadow:0 2px 8px #0002;font-size:12px; }
</style>

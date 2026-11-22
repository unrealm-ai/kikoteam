<script setup lang="ts">
import { computed } from 'vue';
import { renderSessionMarkdown } from '../utils/session-markdown';
const props = defineProps<{ text: string }>();
const html = computed(() => renderSessionMarkdown(props.text));
</script>
<template><div class="session-markdown" v-html="html" /></template>
<style scoped>
.session-markdown{min-width:0;overflow-wrap:anywhere;font-size:13px;line-height:1.8;color:var(--id-text-primary)}
.session-markdown :deep(> :first-child){margin-top:0}.session-markdown :deep(> :last-child){margin-bottom:0}
.session-markdown :deep(p){margin:10px 0}.session-markdown :deep(h1),.session-markdown :deep(h2),.session-markdown :deep(h3),.session-markdown :deep(h4){margin:20px 0 10px;line-height:1.5;font-weight:600}.session-markdown :deep(h1){font-size:20px}.session-markdown :deep(h2){font-size:17px}.session-markdown :deep(h3){font-size:15px}.session-markdown :deep(h4){font-size:14px}
.session-markdown :deep(ul),.session-markdown :deep(ol){padding-left:22px;margin:10px 0}.session-markdown :deep(li){margin:4px 0}.session-markdown :deep(li>p){margin:4px 0}
.session-markdown :deep(blockquote){margin:14px 0;padding:8px 12px;border-left:3px solid var(--id-accent-primary-border);background:var(--id-bg-muted);color:var(--id-text-secondary);border-radius:0 6px 6px 0}.session-markdown :deep(blockquote p){margin:0}
.session-markdown :deep(code){font-family:var(--id-font-mono);font-size:12px;padding:2px 5px;border-radius:4px;background:var(--id-bg-muted)}.session-markdown :deep(pre){max-width:100%;overflow-x:auto;padding:14px;margin:12px 0;border:1px solid var(--id-border-subtle);background:var(--id-bg-muted);border-radius:8px;line-height:1.7;white-space:pre}.session-markdown :deep(pre code){padding:0;background:transparent;overflow-wrap:normal}
.session-markdown :deep(table){display:block;max-width:100%;width:max-content;overflow-x:auto;border-collapse:collapse;margin:14px 0;font-size:12px;font-variant-numeric:tabular-nums}.session-markdown :deep(th),.session-markdown :deep(td){padding:9px 14px;border-bottom:1px solid var(--id-border-subtle);min-width:64px}.session-markdown :deep(th){font-weight:500;background:var(--id-bg-muted);color:var(--id-text-secondary)}.session-markdown :deep(tr:last-child td){border-bottom:0}
.session-markdown :deep(a){color:var(--id-accent-primary);text-decoration:underline;text-underline-offset:3px}.session-markdown :deep(hr){border:0;border-top:1px solid var(--id-border-subtle);margin:18px 0}.session-markdown :deep(.markdown-image-label){color:var(--id-text-muted)}
</style>

<script setup lang="ts">
import { ref } from 'vue';
import type { useProjectDraft } from '../composables/useProjectDraft';
defineProps<{ draft: ReturnType<typeof useProjectDraft>; disabled: boolean; directory?: string }>();
const picker = ref<HTMLInputElement>();
</script>
<template>
  <div class="attachment-bar">
    <button
      type="button"
      class="pick"
      :aria-label="directory === undefined ? '添加附件' : '上传文件'"
      :title="directory === undefined ? '添加附件' : '上传到 project/' + directory"
      :disabled="disabled || (directory !== undefined && draft.pending.value)"
      @click="picker?.click()"
    >
      {{ directory === undefined ? '＋ 附件' : '↑ 上传文件' }}
    </button>
    <input
      ref="picker"
      type="file"
      multiple
      hidden
      @change="
        draft.enqueue(Array.from(picker?.files || []), directory);
        if (picker) picker.value = '';
      "
    />
    <div v-for="f in [...draft.items.value, ...draft.uploads.value]" :key="f.key" class="chip" :title="f.path || f.name">
      <span>{{ f.name }}</span>
      <span v-if="f.status === 'reserving'" role="status">正在准备附件</span>
      <span v-if="f.status === 'uploading'" role="status">{{ f.percent }}%</span>
      <template v-if="f.status === 'failed'">
        <span role="alert">{{ f.error }}</span>
        <button v-if="!f.terminal" type="button" :disabled="disabled" @click="draft.retry(f)">重试</button>
      </template>
      <button
        v-if="draft.items.value.some((item) => item.key === f.key)"
        type="button"
        :disabled="disabled"
        :aria-label="'移除 ' + f.name"
        @click="draft.remove(f.key)"
      >
        ×
      </button>
    </div>
  </div>
</template>
<style scoped>
.attachment-bar,
.chip {
  display: flex;
  align-items: center;
  gap: 6px;
  flex-wrap: wrap;
}
.attachment-bar {
  margin: 0 0 6px;
}
.pick {
  border: 1px solid var(--id-border-subtle);
  background: var(--id-bg-subtle);
  color: var(--id-text-secondary);
  border-radius: var(--id-radius-md);
  padding: 4px 10px;
  font-size: 12px;
  font-weight: 500;
  cursor: pointer;
  transition: background-color var(--id-transition-fast), border-color var(--id-transition-fast), color var(--id-transition-fast);
}
.pick:hover:not(:disabled) {
  background: var(--id-bg-hover);
  border-color: var(--id-border-strong);
  color: var(--id-text-primary);
}
.pick:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}
.chip {
  border: 1px solid var(--id-border-subtle);
  background: var(--id-bg-muted);
  color: var(--id-text-primary);
  border-radius: var(--id-radius-md);
  padding: 4px 8px;
  font-size: 12px;
  overflow-wrap: anywhere;
}
.chip [role='alert'] {
  color: var(--id-status-danger);
}
.chip [role='status'] {
  color: var(--id-accent-primary);
  font-family: var(--id-font-mono);
  font-variant-numeric: tabular-nums;
}
.chip button {
  background: transparent;
  border: 0;
  color: var(--id-text-muted);
  cursor: pointer;
  padding: 0 2px;
  border-radius: 4px;
}
.chip button:hover:not(:disabled) {
  color: var(--id-text-primary);
}
</style>

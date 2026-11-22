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
    <div v-for="f in draft.items.value" :key="f.key" class="chip" :title="f.path || f.name">
      <span>{{ f.name }}</span>
      <span v-if="f.status === 'uploading'" role="status">{{ f.percent }}%</span>
      <template v-if="f.status === 'failed'">
        <span role="alert">{{ f.error }}</span>
        <button type="button" :disabled="disabled" @click="draft.retry(f)">重试</button>
      </template>
      <button
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
  gap: 8px;
  flex-wrap: wrap;
}
.attachment-bar {
  margin: 6px 0;
}
.chip,
.pick {
  border: 1px solid var(--id-border-subtle);
  background: var(--id-bg-subtle);
  border-radius: 6px;
  padding: 5px 8px;
  font-size: 12px;
  overflow-wrap: anywhere;
}
.chip button {
  background: transparent;
  border: 0;
  color: inherit;
  cursor: pointer;
}
</style>

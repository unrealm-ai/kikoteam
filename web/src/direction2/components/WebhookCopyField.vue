<script setup lang="ts">
import { ref } from 'vue';
import { NButton } from 'naive-ui';
import { copyText } from '../utils/clipboard';

const props = defineProps<{ value: string; label: string; copyLabel: string; compact?: boolean }>();
const field = ref<HTMLTextAreaElement>();
const feedback = ref('');
const copying = ref(false);

async function copy() {
  if (!field.value || copying.value) return;
  copying.value = true;
  feedback.value = '';
  try {
    await copyText(props.value, field.value);
    feedback.value = props.label + '已复制。';
  } catch {
    feedback.value = '请手动复制（⌘C 或 Ctrl+C）。';
  } finally {
    copying.value = false;
  }
}
</script>

<template>
  <div class="address" :class="{ compact }">
    <label>{{ label }}
      <textarea ref="field" :value="value" readonly rows="2" spellcheck="false" @click="field?.select()" />
    </label>
    <NButton :size="compact ? 'tiny' : 'small'" :disabled="copying" @click="copy">{{ copyLabel }}</NButton>
    <span role="status">{{ feedback }}</span>
  </div>
</template>

<style scoped>
.address, label { display: grid; gap: 6px; min-width: 0; }
.compact { grid-template-columns: minmax(0, 1fr) auto; align-items: end; }
.compact span { grid-column: 1 / -1; }
textarea {
  box-sizing: border-box;
  width: 100%;
  resize: none;
  color: inherit;
  font: 12px/1.5 var(--id-font-mono);
  padding: 6px 8px;
  border: 1px solid var(--id-border-subtle);
  border-radius: var(--id-radius-sm);
  background: var(--id-bg-surface);
}
span:empty { display: none; }
</style>

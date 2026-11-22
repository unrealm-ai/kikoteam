<script setup lang="ts">
import { computed } from 'vue';
import { NQrCode } from 'naive-ui';

const props = defineProps<{ value: string; compactSize: number }>();
const expandedSize = 256;
const padding = 12;
const expanded = computed(() => props.value.length > 500);
</script>

<template>
  <NQrCode
    :value="value"
    :size="expanded ? expandedSize : compactSize"
    :padding="padding"
    :class="{ 'feishu-qr-expanded': expanded }"
    :style="expanded ? { width: `${expandedSize + 2 * padding}px`, height: 'auto' } : undefined"
    type="svg"
  />
</template>

<style scoped>
.feishu-qr-expanded {
  max-width: 100%;
}

.feishu-qr-expanded :deep(svg) {
  display: block;
  width: 100%;
  height: auto;
}
</style>

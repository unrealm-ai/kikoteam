<script setup lang="ts">
import { ref, useId } from 'vue';
import { NTag } from 'naive-ui';

defineProps<{ label: string; lines: string[] }>();
const id = useId();
const hovered = ref(false);
const focused = ref(false);
const dismissed = ref(false);
</script>

<template>
  <span class="status-help" @mouseenter="hovered = true; dismissed = false" @mouseleave="hovered = false">
    <button type="button" class="status-help-trigger" :aria-describedby="id"
      @focus="focused = true; dismissed = false" @blur="focused = false"
      @click="dismissed = false" @keydown.esc="dismissed = true">
      <NTag size="small" type="warning">{{ label }}</NTag>
    </button>
    <span v-show="(hovered || focused) && !dismissed" :id="id" role="tooltip" class="status-help-tooltip">
      <span v-for="line in lines" :key="line">{{ line }}</span>
    </span>
  </span>
</template>

<style scoped>
.status-help { position: relative; display: inline-flex; }
.status-help-trigger { background: none; border: 0; padding: 0; cursor: help; }
.status-help-trigger:focus-visible { outline: 2px solid var(--id-primary, #406cff); outline-offset: 3px; border-radius: 4px; }
.status-help-tooltip {
  position: absolute;
  top: 100%;
  left: 0;
  z-index: 100;
  display: grid;
  gap: 4px;
  width: max-content;
  max-width: min(250px, 60vw);
  overflow-wrap: anywhere;
  padding: 6px 8px;
  border-radius: 4px;
  background: var(--id-bg-surface, #fff);
  color: var(--id-text-primary, #222);
  border: 1px solid var(--id-border-subtle, #ddd);
  box-shadow: 0 2px 8px #0002;
  font-size: 12px;
}
</style>

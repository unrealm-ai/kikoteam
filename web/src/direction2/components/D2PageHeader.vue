<script setup lang="ts">
defineProps<{ title: string }>();
</script>

<template>
  <header class="d2-page-header">
    <h1>{{ title }}</h1>
    <div v-if="$slots.actions" class="page-actions"><slot name="actions" /></div>
  </header>
</template>

<style scoped>
.d2-page-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  flex-wrap: wrap;
  gap: 8px 12px;
  min-height: 32px;
}
.d2-page-header h1 {
  margin: 0;
  color: var(--id-text-primary);
  font-size: 20px;
  font-weight: 600;
  line-height: 1.4;
  letter-spacing: -0.02em;
}
.page-actions {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 8px;
}
</style>

<script setup lang="ts">
defineProps<{ employee: boolean }>();
</script>

<template>
  <div v-if="employee" class="package-sections">
    <section>
      <h4>1. 基本信息</h4>
      <slot name="basics" />
    </section>
    <section>
      <h4>2. 资源组合</h4>
      <slot name="resources" />
    </section>
    <section>
      <h4>3. 使用范围授权</h4>
      <slot name="scope" />
    </section>
  </div>
  <template v-else>
    <slot name="basics" />
    <slot name="scope" />
    <slot name="resources" />
  </template>
</template>

<style scoped>
.package-sections,
.package-sections > section {
  display: flex;
  flex-direction: column;
  gap: 16px;
  min-width: 0;
}
.package-sections > section { gap: 10px; }
.package-sections h4 {
  margin: 0;
  padding-bottom: 6px;
  border-bottom: 1px solid var(--id-border-hairline);
  color: var(--id-text-primary);
  font-size: 13px;
  font-weight: 600;
}
</style>

<script setup lang="ts">
import D2EmployeeAvatar from './D2EmployeeAvatar.vue';
import type { AgentTemplate } from '../api/templates';

defineProps<{ template: AgentTemplate; category?: string | undefined }>();
</script>

<template>
  <div class="identity" :data-category="template.content.category">
    <D2EmployeeAvatar
      aria-hidden="true"
      :name="template.content.name"
      :preset="template.content.avatar"
      :identity-key="template.id"
      size="xl"
    />
    <div class="identity-text">
      <span v-if="category" class="category">{{ category }}</span>
      <h2>{{ template.content.name || '未命名模板' }}</h2>
    </div>
  </div>
</template>

<style scoped>
.identity {
  --tint: #edf1f6;
  --ink: #425773;
  display: flex;
  align-items: center;
  gap: 16px;
  padding: 20px;
  background: linear-gradient(115deg, var(--tint), #fff 120%);
}
.identity[data-category="analytics"] { --tint: #e3f3ef; --ink: #286454; }
.identity[data-category="office"] { --tint: #eaeefb; --ink: #455e99; }
.identity[data-category="sales"] { --tint: #faeee3; --ink: #82552b; }
.identity[data-category="marketing"] { --tint: #f7e9f1; --ink: #86446f; }
.identity[data-category="engineering"] { --tint: #eeebfa; --ink: #63518d; }
.identity :deep(.d2-emp-avatar) { border: 3px solid white; box-shadow: 0 2px 7px #20364014; }
.identity-text { min-width: 0; }
.category { color: var(--ink); font-size: 12px; font-weight: 600; }
h2 { color: #1d2939; font-size: 20px; line-height: 1.35; margin: 4px 0 0; overflow-wrap: anywhere; }
</style>

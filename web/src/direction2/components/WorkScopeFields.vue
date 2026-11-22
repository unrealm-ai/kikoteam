<script setup lang="ts">
import { computed, ref, watch } from 'vue';
import { NSelect } from 'naive-ui';
import { agentsApi } from '../api/agents';
import { allPages } from '../api/capabilities';
import type { WorkScope } from '../api/work-items';
const props = defineProps<{
  workspace: string;
  employee: string;
  scope?: WorkScope | undefined;
  receiver?: boolean;
  disabled?: boolean;
}>();
const emit = defineEmits<{ update: [scope: WorkScope] }>();
const agents = ref<{ label: string; value: string }[]>([]),
  error = ref('');
const modes = computed(() => [
  ...(props.receiver
    ? [{ label: '全部员工', value: 'all' }]
    : [
        ...(!props.scope ? [{ label: '沿用已有能力包范围', value: 'legacy' }] : []),
        { label: '继承员工接手范围', value: 'inherit' },
      ]),
  { label: '仅允许所选来源', value: 'allowlist' },
  { label: '排除所选来源', value: 'denylist' },
]);
watch(
  () => [props.workspace, props.employee],
  async (_, __, cleanup) => {
    const controller = new AbortController();
    cleanup(() => controller.abort());
    agents.value = [];
    error.value = '';
    try {
      const list = await allPages((n) => agentsApi.list(props.workspace, n, controller.signal));
      if (!controller.signal.aborted)
        agents.value = list
          .filter((a) => a.id !== props.employee)
          .map((a) => ({ label: a.name, value: a.id }));
    } catch (e) {
      if (!controller.signal.aborted) error.value = e instanceof Error ? e.message : '员工读取失败';
    }
  },
  { immediate: true },
);
</script>
<template>
  <fieldset>
    <legend>{{ receiver ? '允许向此员工交接的来源' : '事项交接时允许使用此能力包的来源' }}</legend>
    <NSelect
      :value="scope?.mode || 'legacy'"
      :disabled="disabled"
      :options="modes"
      aria-label="交接来源规则"
      @update:value="(mode) => emit('update', { mode, employee_ids: [] })"
    />
    <NSelect
      v-if="scope?.mode === 'allowlist' || scope?.mode === 'denylist'"
      multiple
      filterable
      :value="scope.employee_ids || []"
      :disabled="disabled"
      :options="agents"
      placeholder="选择来源员工"
      @update:value="(ids) => scope && emit('update', { ...scope, employee_ids: ids })"
    />
    <p v-if="error" role="alert">{{ error }}</p>
  </fieldset>
</template>
<style scoped>
fieldset {
  display: grid;
  gap: 10px;
  margin: 0;
  padding: 12px;
  background: var(--id-bg-surface);
  border: 1px solid var(--id-border-subtle);
  border-radius: var(--id-radius-md);
  min-width: 0;
}
legend {
  padding: 0 8px;
  font-size: 13px;
  font-weight: 600;
  color: var(--id-text-primary);
}
</style>

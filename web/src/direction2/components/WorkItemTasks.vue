<script setup lang="ts">
import { onMounted, onUnmounted, ref } from 'vue';
import { NButton } from 'naive-ui';
import { taskApi, type TaskRecord } from '../api/tasks';
const props = defineProps<{ workspace: string; itemId: string }>();
const emit = defineEmits<{ open: [id: string] }>();
const rows = ref<TaskRecord[]>([]),
  next = ref<string | null>(null),
  loading = ref(false),
  error = ref('');
const controller = new AbortController();
async function load() {
  if (loading.value) return;
  loading.value = true;
  error.value = '';
  try {
    const page = await taskApi.list(
      props.workspace,
      { work_item_id: props.itemId },
      next.value || '',
      controller.signal,
    );
    if (controller.signal.aborted) return;
    if (page.next_cursor && page.next_cursor === next.value)
      throw new Error('关联任务分页游标重复');
    rows.value = [
      ...new Map(
        [...rows.value, ...page.items].map((t) => [t.task_id, t]),
      ).values(),
    ];
    next.value = page.next_cursor;
  } catch (e) {
    if (!controller.signal.aborted) {
      rows.value = [];
      next.value = null;
      error.value = e instanceof Error ? e.message : '关联任务读取失败';
    }
  } finally {
    loading.value = false;
  }
}
onMounted(load);
onUnmounted(() => controller.abort());
</script>
<template>
  <section>
    <strong class="d-label">关联执行任务</strong>
    <p v-if="error" role="alert">
      {{ error }} <NButton size="tiny" @click="load">重试</NButton>
    </p>
    <p v-else-if="!loading && !rows.length">暂无可见的关联任务。</p>
    <p v-for="row in rows" :key="row.task_id">
      <NButton text @click="emit('open', row.task_id)"
        >{{ row.title || row.task_id }} ↗</NButton
      >
    </p>
    <NButton v-if="next" size="small" :loading="loading" @click="load"
      >更多关联任务</NButton
    >
  </section>
</template>
<style scoped>
section {
  font-size: 13px;
  color: var(--id-text-secondary);
}
p {
  overflow-wrap: anywhere;
}
</style>

<script setup lang="ts">
import { computed, ref, watch } from 'vue';
import { NTag } from 'naive-ui';
import { wecomContactsApi, type WecomContactPermissionStatus } from '../../api/wecom-contacts';

const props = defineProps<{
  workspace: string;
  employee: string;
  binding?: string | undefined;
  generation?: number | undefined;
  revision?: number | undefined;
  eligible: boolean;
}>();
const state = ref<WecomContactPermissionStatus | 'checking'>('not_checked');
const labels: Record<typeof state.value, string> = {
  not_checked: '联系人搜索未检测',
  checking: '正在检测联系人搜索',
  available: '联系人搜索可用',
  denied: '联系人搜索未获授权',
  authentication_failed: '联系人搜索认证失败',
  unsupported: '当前企微连接不支持联系人搜索',
  unavailable: '联系人搜索暂无法检测',
};
const color = computed(() => {
  if (state.value === 'available') return 'success';
  if (state.value === 'denied' || state.value === 'authentication_failed') return 'warning';
  return 'default';
});

watch(() => [props.workspace, props.employee, props.binding, props.generation, props.revision, props.eligible] as const,
  async ([workspace, employee, binding, generation, revision, eligible], _, onCleanup) => {
    if (!workspace || !employee || !binding || generation === undefined || revision === undefined || !eligible) {
      state.value = 'not_checked';
      return;
    }
    const ctrl = new AbortController();
    onCleanup(() => ctrl.abort());
    state.value = 'checking';
    try {
      const outcome = await wecomContactsApi.permission(workspace, employee, ctrl.signal);
      if (ctrl.signal.aborted) return;
      state.value = outcome.binding_id === binding && outcome.connection_generation === generation && outcome.config_revision === revision
        ? outcome.status : 'not_checked';
    } catch {
      if (!ctrl.signal.aborted) state.value = 'unavailable';
    }
  }, {immediate:true});
</script>

<template>
  <NTag size="small" :type="color" role="status">{{ labels[state] ?? labels.not_checked }}</NTag>
</template>

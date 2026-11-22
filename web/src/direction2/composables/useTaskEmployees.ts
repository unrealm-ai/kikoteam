import { onUnmounted, ref, watch, type Ref } from 'vue';
import { agentsApi } from '../api/agents';

export function useTaskEmployees(workspace: Ref<string>) {
  const agents = ref<{ label: string; value: string }[]>([]);
  const agentError = ref('');
  let controller: AbortController;
  async function loadAgents() {
    controller?.abort();
    controller = new AbortController();
    const { signal } = controller;
    const options: typeof agents.value = [];
    const seen = new Set<string>();
    agentError.value = '';
    try {
      for (let page = 1; ; page++) {
        const result = await agentsApi.list(workspace.value, page, signal);
        if (signal.aborted) return;
        if (result.page !== page || result.items.some((a) => seen.has(a.id)))
          throw new Error('员工分页响应重复');
        result.items.forEach((a) => {
          seen.add(a.id);
          options.push({ label: a.name, value: a.id });
        });
        if (!result.items.length || options.length >= result.total) break;
      }
      agents.value = options;
    } catch (error) {
      if (!signal.aborted) {
        agents.value = [];
        agentError.value =
          error instanceof Error ? error.message : '员工筛选加载失败';
      }
    }
  }
  watch(
    workspace,
    () => {
      agents.value = [];
      void loadAgents();
    },
    { immediate: true },
  );
  onUnmounted(() => controller?.abort());
  return { agents, agentError, loadAgents };
}

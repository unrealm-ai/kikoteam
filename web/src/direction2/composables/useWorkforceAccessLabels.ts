import { computed, shallowRef, watch } from 'vue';
import type { UsageSelector } from '../api/agent-usage';
import { selectorsApi, toUsageSelector } from '../api/selectors';

const resolveLimit = 200;
export const workforceAccessIdentityKey = (v: UsageSelector) =>
  JSON.stringify([v.channel, v.kind, v.id, v.binding_id || '', v.identity_epoch || 0]);

interface AccessLabelScope {
  workspace: string;
  employee: string;
  selectors: UsageSelector[];
  bindingRevision: string;
  usageRevision: number | undefined;
  usageRead: number;
}

export function useWorkforceAccessLabels(
  scope: () => AccessLabelScope | null,
  resolve = selectorsApi.resolveSaved,
) {
  const empty: ReadonlyMap<string, string> = new Map();
  const result = shallowRef<{ scope: AccessLabelScope; names: ReadonlyMap<string, string> }>();
  watch(scope, async (current, _, onCleanup) => {
    result.value = undefined;
    if (!current?.selectors.length) return;
    const controller = new AbortController();
    onCleanup(() => controller.abort());
    const expired = () => controller.signal.aborted || scope() !== current;
    const sources = new Map<string, Map<string, UsageSelector>>();
    for (const v of current.selectors) {
      const sourceKey = JSON.stringify([v.channel, v.binding_id]);
      const source = sources.get(sourceKey) ?? new Map<string, UsageSelector>();
      source.set(workforceAccessIdentityKey(v), v);
      sources.set(sourceKey, source);
    }
    const resolution = new Map<string, string>();
    for (const source of sources.values()) {
      const requested = [...source.entries()];
      for (let offset = 0; offset < requested.length; offset += resolveLimit) {
        if (expired()) return;
        const batch = requested.slice(offset, offset + resolveLimit);
        const batchKeys = new Set(batch.map(([key]) => key));
        try {
          const response = await resolve(current.workspace, current.employee, batch.map(([, value]) => value), 'usage', controller.signal);
          if (expired()) return;
          for (const row of response.items) {
            const label = row.display_name || row.name;
            if (!label || row.status === 'unavailable' || row.ref.workspace_id !== current.workspace ||
              row.ref.agent_id !== current.employee) continue;
            const key = workforceAccessIdentityKey(toUsageSelector(row.ref));
            if (batchKeys.has(key)) resolution.set(key, label);
          }
        } catch {
          if (expired()) return;
          // Other sources and batches are independent; retain raw fallback here.
        }
      }
    }
    if (!expired()) result.value = { scope: current, names: resolution };
  }, {immediate: true});
  return computed(() => {
    const current = scope();
    const resolved = result.value;
    return resolved && resolved.scope === current ? resolved.names : empty;
  });
}

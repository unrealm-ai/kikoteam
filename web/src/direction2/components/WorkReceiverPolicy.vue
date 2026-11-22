<script setup lang="ts">
import { computed, inject, onUnmounted, ref, watch } from 'vue';
import { agentsApi } from '../api/agents';
import { allPages } from '../api/capabilities';
import { workApi, type ReceiverPolicy, type WorkScope } from '../api/work-items';

const props = defineProps<{ workspace: string; employee: string }>();
const policy = ref<ReceiverPolicy>();
const draft = ref<WorkScope>();
const busy = ref(false);
const error = ref('');
const saved = ref(false);
const options = ref<{ label: string; value: string }[]>([]);
const optionsBusy = ref(false);
const optionsError = ref('');
const dirty = computed(() => JSON.stringify(draft.value) !== JSON.stringify(policy.value?.scope));
const mode = computed(() => draft.value?.mode === 'allowlist' ? 'allowlist' : 'denylist');
let request: AbortController | undefined;
let directory: AbortController | undefined;

function apply(value: ReceiverPolicy) {
  policy.value = value;
  draft.value = { ...value.scope, employee_ids: [...value.scope.employee_ids] };
}
async function run(save = false): Promise<boolean> {
  if (save && (!policy.value || !draft.value || !dirty.value || busy.value)) return false;
  request?.abort();
  request = new AbortController();
  const { signal } = request;
  busy.value = true;
  error.value = '';
  saved.value = false;
  try {
    const value = save && policy.value && draft.value
      ? await workApi.saveReceiver(props.workspace, props.employee, { version: policy.value.version, scope: draft.value }, signal)
      : await workApi.receiver(props.workspace, props.employee, signal);
    if (signal.aborted) return false;
    apply(value);
    saved.value = save;
    return true;
  } catch (e) {
    if (!signal.aborted) error.value = e instanceof Error ? e.message : save ? '保存接手规则失败' : '读取接手规则失败';
    return false;
  } finally {
    if (!signal.aborted) busy.value = false;
  }
}
async function loadOptions() {
  directory?.abort();
  directory = new AbortController();
  const { signal } = directory;
  optionsBusy.value = true;
  optionsError.value = '';
  try {
    const list = await allPages(page => agentsApi.list(props.workspace, page, signal));
    if (!signal.aborted) options.value = list.filter(a => a.id !== props.employee).map(a => ({ label: a.name, value: a.id }));
  } catch (e) {
    if (!signal.aborted) optionsError.value = e instanceof Error ? e.message : '读取来源员工失败';
  } finally {
    if (!signal.aborted) optionsBusy.value = false;
  }
}
function updateMode(value: 'allowlist' | 'denylist') {
  if (!draft.value || busy.value || value === mode.value) return;
  const previous = policy.value?.scope;
  const restored = previous && (previous.mode === value || previous.mode === 'all' && value === 'denylist');
  draft.value = restored ? { ...previous, employee_ids: [...previous.employee_ids] } : { mode: value, employee_ids: [] };
  saved.value = false;
}
function updateIds(ids: string[]) {
  if (!draft.value || busy.value) return;
  draft.value = { mode: mode.value === 'denylist' && ids.length === 0 && policy.value?.scope.mode === 'all' ? 'all' : mode.value, employee_ids: ids };
  saved.value = false;
}
function discard() {
  if (busy.value) return;
  if (policy.value) apply(policy.value);
  error.value = '';
  saved.value = false;
}
const guard = { hasUnsaved: () => dirty.value, save: () => run(true), discard, getLabel: () => 'AI 员工交接范围' };
const draftGuard = inject<{ register: (item: typeof guard) => void; unregister: (item: typeof guard) => void }>('d2DraftGuard');
draftGuard?.register(guard);
watch(() => [props.workspace, props.employee], () => {
  policy.value = undefined;
  draft.value = undefined;
  options.value = [];
  void run();
  void loadOptions();
}, { immediate: true });
onUnmounted(() => {
  request?.abort();
  directory?.abort();
  draftGuard?.unregister(guard);
});
</script>

<template>
  <slot :draft="draft" :busy="busy" :mode="mode" :dirty="dirty" :saved="saved" :error="error"
    :options="options" :options-busy="optionsBusy" :options-error="optionsError"
    :update-mode="updateMode" :update-ids="updateIds" :save="() => run(true)" :reload="() => run()"
    :load-options="loadOptions" :discard="discard" />
</template>

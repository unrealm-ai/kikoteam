<script setup lang="ts">
import { onUnmounted, ref, watch } from 'vue';
import { NAlert, NButton } from 'naive-ui';
import D2ChannelLogo from './D2ChannelLogo.vue';
import { selectorsApi, type SelectorPurpose, type SelectionCandidate } from '../api/selectors';
import type { UsageSelector } from '../api/agent-usage';

const props = defineProps<{ workspace: string; employeeId: string; items: UsageSelector[]; purpose: SelectorPurpose; disabled: boolean }>();
const emit = defineEmits<{ remove: [index: number] }>();
const rows = ref<SelectionCandidate[]>([]), error = ref('');
let generation = 0, controller = new AbortController();
const labels = { web: 'Web', feishu: '飞书', dingtalk: '钉钉', wecom: '企微' };
function resolved(s: UsageSelector) {
  return rows.value.find(v =>
    v.ref.provider === s.channel &&
    v.ref.kind === s.kind &&
    v.ref.external_id === s.id &&
    (s.channel === 'web' || (v.ref.binding_id === s.binding_id && v.ref.identity_epoch === s.identity_epoch))
  );
}
watch(() => [props.workspace, props.employeeId, props.purpose, JSON.stringify(props.items)], async () => {
  const g = ++generation; controller.abort(); controller = new AbortController(); rows.value = []; error.value = '';
  const people = props.items.filter(s => s.kind !== 'group'), groups = props.items.filter(s => s.kind === 'group');
  const resolve = async (kind: 'people' | 'groups', selectors: UsageSelector[]) => {
    if (!selectors.length) return;
    try {
      const result = kind === 'groups'
        ? await selectorsApi.resolveSavedGroups(props.workspace, props.employeeId, selectors, controller.signal)
        : await selectorsApi.resolveSaved(props.workspace, props.employeeId, selectors, props.purpose, controller.signal);
      if (g !== generation) return;
      if (!Array.isArray(result.items)) throw new Error('名称解析响应无效');
      rows.value.push(...result.items);
    } catch (e) {
      if (g === generation) error.value += [kind === 'groups' ? '群' : '人员', e instanceof Error ? e.message : '名称解析失败'].join('：') + '；';
    }
  };
  await Promise.all([resolve('people', people), resolve('groups', groups)]);
}, { immediate: true });
onUnmounted(() => { ++generation; controller.abort(); });
</script>

<template>
  <div class="selected-subjects">
    <NAlert v-if="error" type="warning" style="margin-bottom: 8px;">名称暂时无法解析，原有选择仍保留：{{ error }}</NAlert>
    <p v-if="!items.length" class="subjects-empty">暂无规则</p>
    <div v-else class="subjects-list">
      <article v-for="(s, i) in items" :key="JSON.stringify(s)" class="subj-card">
        <div class="subj-main">
          <div class="subj-title">
            <D2ChannelLogo :platform="s.channel" :size="14" />
            <span class="subj-pill">{{ labels[s.channel] }} · {{ s.kind }}</span>
            <strong>{{ resolved(s)?.display_name || resolved(s)?.name || s.id }} {{ s.include_descendants ? '（包含子部门）' : '' }}</strong>
          </div>
          <small class="subj-meta">
            {{ s.id }}
            <template v-if="s.binding_id"> · {{ s.binding_id }} / {{ s.identity_epoch }}</template>
            · {{ resolved(s)?.selectable ? '当前候选可用' : resolved(s)?.disabled_reason || (resolved(s)?.status === 'unavailable' ? '当前不可用' : '未解析，保存及使用时重新核验') }}
          </small>
        </div>
        <NButton size="tiny" quaternary type="error" :disabled="disabled" @click="emit('remove', i)">移除主体</NButton>
      </article>
    </div>
  </div>
</template>

<style scoped>
.selected-subjects {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.subjects-empty {
  color: var(--id-text-muted);
  font-size: 12px;
  padding: 10px 12px;
  margin: 0;
  background: var(--id-bg-subtle);
  border: 1px dashed var(--id-border-strong);
  border-radius: var(--id-radius-md);
}

.subjects-list {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.subj-card {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  padding: 8px 12px;
  background: var(--id-bg-surface);
  border: 1px solid var(--id-border-subtle);
  border-radius: var(--id-radius-md);
}

.subj-main {
  min-width: 0;
  flex: 1;
  display: flex;
  flex-direction: column;
  gap: 3px;
}

.subj-title {
  display: flex;
  align-items: center;
  gap: 6px;
  flex-wrap: wrap;
}

.subj-title strong {
  font-size: 13px;
  color: var(--id-text-primary);
  overflow-wrap: anywhere;
}

.subj-pill {
  font-size: 11px;
  font-family: var(--id-font-mono);
  background: var(--id-bg-muted);
  border: 1px solid var(--id-border-subtle);
  color: var(--id-text-secondary);
  padding: 1px 6px;
  border-radius: var(--id-radius-sm);
}

.subj-meta {
  font-size: 12px;
  color: var(--id-text-muted);
  overflow-wrap: anywhere;
}
</style>

<script setup lang="ts">
import { computed, inject, onUnmounted, ref, watch } from 'vue';
import { NAlert, NButton, NModal, NSkeleton } from 'naive-ui';
import { agentsApi, type EmployeeMaintainers } from '../../api/agents';
import { selectorKey, type SelectionCandidate } from '../../api/selectors';
import D2ChannelLogo from '../D2ChannelLogo.vue';
import PlatformIdentityPicker from '../PlatformIdentityPicker.vue';

const props = defineProps<{ workspace: string; employeeId: string }>();
const emit = defineEmits<{ saved: [] }>();
const saved = ref<EmployeeMaintainers>();
const people = ref<SelectionCandidate[]>([]);
const loading = ref(false), saving = ref(false), picking = ref(false);
const error = ref(''), notice = ref('');
let controller = new AbortController();
const dirty = computed(() => !!saved.value && people.value.map(p => selectorKey(p.ref)).join('\n') !== saved.value.people.map(p => selectorKey(p.ref)).join('\n'));
function apply(value: EmployeeMaintainers) {
  saved.value = value;
  people.value = [...value.people];
}
function discard() {
  if (saved.value) apply(saved.value);
  picking.value = false;
  error.value = '';
}
async function load() {
  controller.abort();
  controller = new AbortController();
  const { signal } = controller;
  loading.value = true;
  saving.value = false;
  saved.value = undefined;
  people.value = [];
  error.value = '';
  notice.value = '';
  picking.value = false;
  try {
    const value = await agentsApi.maintainers(props.workspace, props.employeeId, signal);
    if (!signal.aborted) apply(value);
  } catch (cause) {
    if (!signal.aborted) error.value = cause instanceof Error ? cause.message : '维护人名单加载失败';
  } finally {
    if (!signal.aborted) loading.value = false;
  }
}
function add(candidate: SelectionCandidate) {
  if (candidate.ref.kind !== 'user' || !candidate.selectable || people.value.length >= 100) return;
  if (!people.value.some(person => selectorKey(person.ref) === selectorKey(candidate.ref))) people.value.push(candidate);
  picking.value = false;
  notice.value = '';
}
async function save() {
  if (!saved.value || !dirty.value || saving.value) return !dirty.value;
  const { signal } = controller;
  saving.value = true;
  error.value = '';
  notice.value = '';
  try {
    const value = await agentsApi.saveMaintainers(props.workspace, props.employeeId, saved.value.version, people.value.map(p => p.ref), signal);
    if (signal.aborted) return false;
    apply(value);
    notice.value = '维护人名单已保存';
    emit('saved');
    return true;
  } catch (cause) {
    if (!signal.aborted) error.value = cause instanceof Error ? cause.message : '维护人名单保存失败';
    return false;
  } finally {
    if (!signal.aborted) saving.value = false;
  }
}
const guard = { hasUnsaved: () => dirty.value, save, discard, getLabel: () => '维护人' };
const draftGuard = inject<{ register: (v: typeof guard) => void; unregister: (v: typeof guard) => void }>('d2DraftGuard');
draftGuard?.register(guard);
watch(() => [props.workspace, props.employeeId], load, { immediate: true });
onUnmounted(() => { controller.abort(); draftGuard?.unregister(guard); });
</script>

<template>
  <section class="form-block" :class="{ 'is-dirty-card': dirty }" aria-labelledby="workforce-maintainers-title">
    <div class="card-edit-header">
      <h4 id="workforce-maintainers-title" class="fb-title">维护人</h4>
      <div v-if="saved" class="card-edit-actions">
        <span v-if="dirty" class="card-dirty-pill">未保存</span>
        <NButton size="tiny" :disabled="saving || people.length >= 100" @click="picking = true">添加维护人</NButton>
        <template v-if="dirty">
          <NButton size="tiny" :disabled="saving" @click="discard">取消</NButton>
          <NButton size="tiny" type="primary" :loading="saving" @click="save">保存</NButton>
        </template>
      </div>
    </div>
    <p class="fb-sub">仅允许名单中的个人维护此员工。维护人可修改员工配置和维护名单。创建人和工作区管理员默认有管理权限，无需添加。</p>
    <p class="fb-sub">渠道名单独立于 Web 账号；在渠道内修改员工配置需通过后续接入的管理工具。</p>
    <p v-if="notice && !dirty" class="tr-receipt-msg" role="status">{{ notice }}</p>
    <NAlert v-if="error" type="error">{{ error }} <NButton v-if="!saved" size="tiny" @click="load">重试</NButton></NAlert>
    <NSkeleton v-if="loading" text :repeat="2" />
    <p v-else-if="saved && !people.length" class="fb-sub">未添加其他维护人。</p>
    <div v-for="person in people" :key="selectorKey(person.ref)" class="maintainer-row">
      <D2ChannelLogo :platform="person.ref.provider" :size="16" show-label />
      <span>{{ person.display_name || person.ref.external_id }}</span>
      <span v-if="!person.selectable" class="fb-sub">当前不可用</span>
      <NButton tertiary size="tiny" :disabled="saving" :aria-label="'移除 ' + (person.display_name || person.ref.external_id)"
        @click="people = people.filter(p => p !== person)">移除</NButton>
    </div>
    <NModal v-model:show="picking" preset="card" title="选择维护人" style="width: min(620px, 94vw)">
      <PlatformIdentityPicker v-if="picking" :workspace="workspace" :employee-id="employeeId" kind="people" purpose="maintenance" @pick="add" />
    </NModal>
  </section>
</template>

<style scoped>
@import './workforce-shared.css';
.maintainer-row { display: flex; flex-wrap: wrap; align-items: center; gap: 10px; margin-top: 10px; overflow-wrap: anywhere; }
</style>

<script setup lang="ts">
import { computed, inject, onUnmounted, ref, watch } from 'vue';
import { NAlert, NButton, NInput, NModal, NSkeleton } from 'naive-ui';
import { humanRequestsApi, type HumanRecipient, type HumanRecipients } from '../../api/human-requests';
import { selectorKey, type SelectionCandidate } from '../../api/selectors';
import D2ChannelLogo from '../D2ChannelLogo.vue';
import PlatformIdentityPicker from '../PlatformIdentityPicker.vue';

const props = defineProps<{ workspace: string; employeeId: string }>();
const emit = defineEmits<{ loaded: [count: number] }>();
const saved = ref<HumanRecipients>();
const draft = ref<HumanRecipient[]>([]);
const picking = ref<'specialist' | 'default' | null>(null);
const loading = ref(false);
const saving = ref(false);
const error = ref('');
const savedNotice = ref(false);
let controller = new AbortController();

const dirty = computed(() => !!saved.value && JSON.stringify(saved.value.recipients) !== JSON.stringify(draft.value));
const defaultRecipient = computed(() => draft.value.find(recipient => recipient.is_default));
const specialists = computed(() => draft.value.filter(recipient => !recipient.is_default));
const valid = computed(() => specialists.value.every(recipient => recipient.scope.trim()) && draft.value.length <= 40);
function apply(value: HumanRecipients) {
  saved.value = value;
  draft.value = value.recipients.map(recipient => ({ ...recipient }));
  emit('loaded', value.recipients.length);
}
function discard() {
  if (saved.value) apply(saved.value);
  picking.value = null;
  error.value = '';
  savedNotice.value = false;
}
async function load() {
  controller.abort();
  controller = new AbortController();
  const { signal } = controller;
  saved.value = undefined;
  draft.value = [];
  picking.value = null;
  loading.value = true;
  saving.value = false;
  error.value = '';
  savedNotice.value = false;
  try {
    const value = await humanRequestsApi.recipients(props.workspace, props.employeeId, signal);
    if (!signal.aborted) apply(value);
  } catch (cause) {
    if (!signal.aborted) error.value = cause instanceof Error ? cause.message : '确认人加载失败';
  } finally {
    if (!signal.aborted) loading.value = false;
  }
}
function remove(recipient: HumanRecipient) {
  draft.value = draft.value.filter(item => item !== recipient);
}
function clearDefault() {
  const recipient = defaultRecipient.value;
  if (!recipient) return;
  if (recipient.scope.trim()) delete recipient.is_default;
  else remove(recipient);
}
function add(candidate: SelectionCandidate) {
  const existing = draft.value.find(recipient => selectorKey(recipient.ref) === selectorKey(candidate.ref));
  if (existing) {
    existing.ref = candidate.ref;
    existing.name = candidate.display_name || candidate.ref.external_id;
  }
  if (picking.value === 'default' && (!existing || existing !== defaultRecipient.value)) {
    if (!existing && draft.value.length >= 40 && (!defaultRecipient.value || defaultRecipient.value.scope.trim())) {
      error.value = '最多可配置 40 名确认人，请选择已有成员或移除一条规则。';
      return;
    }
    clearDefault();
    const selected: HumanRecipient = existing ?? { ref: candidate.ref, name: candidate.display_name || candidate.ref.external_id, scope: '' };
    selected.is_default = true;
    if (!existing) draft.value.push(selected);
  } else if (picking.value === 'specialist' && !existing) {
    draft.value.push({ ref: candidate.ref, name: candidate.display_name || candidate.ref.external_id, scope: '' });
  }
  error.value = '';
  picking.value = null;
}
async function save() {
  if (!saved.value || !dirty.value || saving.value || !valid.value) return !dirty.value;
  saving.value = true;
  error.value = '';
  savedNotice.value = false;
  const { signal } = controller;
  try {
    const value = await humanRequestsApi.saveRecipients(props.workspace, props.employeeId, { version: saved.value.version, recipients: draft.value }, signal);
    if (signal.aborted) return false;
    apply(value);
    savedNotice.value = true;
    return true;
  } catch (cause) {
    if (!signal.aborted) error.value = cause instanceof Error ? cause.message : '确认人保存失败';
    return false;
  } finally {
    if (!signal.aborted) saving.value = false;
  }
}
const guard = { hasUnsaved: () => dirty.value, save, discard, getLabel: () => '人工确认规则' };
const draftGuard = inject<{ register: (value: typeof guard) => void; unregister: (value: typeof guard) => void }>('d2DraftGuard');
draftGuard?.register(guard);
watch(() => [props.workspace, props.employeeId], load, { immediate: true });
onUnmounted(() => { controller.abort(); draftGuard?.unregister(guard); });
</script>

<template>
  <section class="form-block" :class="{ 'is-dirty-card': dirty }" aria-labelledby="workforce-human-recipients-title">
    <div class="card-edit-header">
      <h4 id="workforce-human-recipients-title" class="fb-title">2. 人工确认规则</h4>
      <div v-if="saved" class="card-edit-actions">
        <span v-if="dirty" class="card-dirty-pill">未保存</span>
        <NButton size="tiny" :disabled="saving || draft.length >= 40" @click="picking='specialist'">添加确认人</NButton>
        <template v-if="dirty">
          <NButton size="tiny" :disabled="saving" @click="discard">取消</NButton>
          <NButton size="tiny" type="primary" :disabled="!valid" :loading="saving" @click="save">保存</NButton>
        </template>
      </div>
    </div>
    <p class="fb-sub">从 Web 工作区、飞书、钉钉或企业微信选择确认人。AI 员工按负责范围匹配。渠道确认人可私聊该员工，询问「有哪些待确认问题」并答复。</p>
    <p v-if="savedNotice && !dirty" class="tr-receipt-msg" role="status">人工确认规则已保存</p>
    <p v-if="dirty && !valid" class="field-note text-warning" role="status">请为每位确认人填写负责范围后再保存（最多 40 名）。</p>
    <NAlert v-if="error" type="error">
      {{ error }}
      <NButton v-if="!saved" size="tiny" @click="load">重试</NButton>
    </NAlert>
    <NSkeleton v-if="loading" text :repeat="2" />
    <p v-else-if="saved && !draft.length" class="fb-sub">尚未配置确认人</p>
    <div v-if="saved" class="default-recipient">
      <div>
        <strong>默认确认人</strong>
        <p class="fb-sub">问题没有匹配的负责范围时，由默认确认人兜底。</p>
      </div>
      <NButton size="small" :disabled="saving" @click="picking='default'">{{ defaultRecipient ? '更换默认确认人' : '选择默认确认人' }}</NButton>
      <div v-if="defaultRecipient" class="recipient default-recipient-row">
        <strong class="recipient-name"><D2ChannelLogo :platform="defaultRecipient.ref.provider" :size="16" show-label />{{ defaultRecipient.name }}</strong>
        <NInput v-model:value="defaultRecipient.scope" :disabled="saving" maxlength="1000" aria-label="默认确认人负责范围" placeholder="可选，填写后也可按负责范围匹配" />
        <NButton tertiary size="small" :disabled="saving" @click="clearDefault">取消默认</NButton>
      </div>
    </div>
    <div v-for="recipient in specialists" :key="selectorKey(recipient.ref)" class="recipient">
      <strong class="recipient-name"><D2ChannelLogo :platform="recipient.ref.provider" :size="16" show-label />{{ recipient.name }}</strong>
      <NInput v-model:value="recipient.scope" :disabled="saving" maxlength="1000"
        :aria-label="`${recipient.name}负责范围`" placeholder="例如财务数据口径、账期和退款归属" />
      <NButton tertiary size="small" :disabled="saving" @click="remove(recipient)">移除</NButton>
    </div>
    <NModal :show="picking !== null" @update:show="show => { if (!show) picking = null }" preset="card" :title="picking === 'default' ? '选择默认确认人' : '选择确认人'" style="width: min(620px, 94vw)">
      <PlatformIdentityPicker v-if="picking" :workspace="workspace" :employee-id="employeeId"
        kind="people" purpose="confirmation" @pick="add" />
    </NModal>
    <div v-if="$slots.default" class="delivery-tools"><slot /></div>
  </section>
</template>

<style scoped>
@import './workforce-shared.css';
.delivery-tools { margin-top: 16px; border-top: 1px solid var(--id-border-subtle); }
.recipient { display: grid; grid-template-columns: minmax(6rem, 12rem) 1fr auto; align-items: center; gap: 12px; margin-top: 12px; }
.recipient-name { display: flex; flex-wrap: wrap; align-items: center; gap: 6px; overflow-wrap: anywhere; }
.default-recipient { display: grid; grid-template-columns: 1fr auto; align-items: start; gap: 8px 12px; margin-top: 12px; }
.default-recipient-row { grid-column: 1 / -1; margin-top: 0; }
@media (max-width: 620px) {
  .recipient { grid-template-columns: 1fr auto; }
  .recipient :deep(.n-input) { grid-column: 1 / -1; grid-row: 2; }
}
</style>

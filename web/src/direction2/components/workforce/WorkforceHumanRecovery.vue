<script setup lang="ts">
import { computed, onUnmounted, ref, watch } from 'vue';
import { NAlert, NButton, NInput, NSelect, NTag } from 'naive-ui';
import { platformRequest, post } from '../../api/platform';

const props = defineProps<{ workspace: string; employeeId: string }>();
type Item = { id: string; version: string; provider: string; status: string; error_code?: string; can_retry: boolean; can_reassign: boolean };
type Recipient = { id?: string; name?: string; ref: { provider: string } };
type Recovery = { items: Item[]; recipients: Recipient[] };
const data = ref<Recovery>({ items: [], recipients: [] });
const target = ref<Item>();
const reason = ref('');
const recipientId = ref<string | null>(null);
const action = ref<'retry' | 'reassign'>('retry');
const busy = ref(false);
const opened = ref(false);
const error = ref('');
const status = ref('');
let controller = new AbortController();
const endpoint = computed(() =>
  '/workspaces/' + encodeURIComponent(props.workspace) + '/agents/' + encodeURIComponent(props.employeeId) + '/human-recovery');
const names: Record<string, string> = { web: 'Web', feishu: '飞书', dingtalk: '钉钉', wecom: '企微' };
const states: Record<string, string> = {
  unknown: '送达未知', failed: '投递失败', sent: '已送达', sending: '正在发送', accepted: '确认送达中',
  cancelled: '投递已取消', leased: '等待发送', pending: '等待发送', not_sent: '尚未发送',
  web_inbox: '工作台待确认', blocked: '任务停稳待核对',
};
const options = computed(() => data.value.recipients.flatMap(r =>
  r.id ? [{ value: r.id, label: (r.name || '确认人') + ' · ' + (names[r.ref.provider] || r.ref.provider) }] : []));
async function load() {
  const { signal } = controller;
  busy.value = true;
  error.value = '';
  try {
    const result = await platformRequest<Recovery>(endpoint.value, { signal });
    if (!signal.aborted) data.value = result;
  } catch (cause) {
    if (!signal.aborted) error.value = cause instanceof Error ? cause.message : '读取待确认投递状态失败';
  } finally {
    if (!signal.aborted) busy.value = false;
  }
}
function begin(item: Item, operation: 'retry' | 'reassign') {
  target.value = item;
  action.value = operation;
  reason.value = '';
  recipientId.value = null;
  status.value = '';
}
async function submit() {
  const item = target.value;
  if (busy.value || !item || !reason.value.trim() || action.value === 'reassign' && !recipientId.value) return;
  const { signal } = controller;
  busy.value = true;
  try {
    await platformRequest(endpoint.value + '/' + encodeURIComponent(item.id), {
      ...post({ version: item.version, action: action.value, reason: reason.value.trim(),
        ...(action.value === 'reassign' ? { recipient_id: recipientId.value } : {}) }), signal,
    });
    if (signal.aborted) return;
    target.value = undefined;
    status.value = '补救操作已记录。请以实际投递状态为准。';
    await load();
  } catch (cause) {
    if (signal.aborted) return;
    target.value = undefined;
    await load();
    if (!signal.aborted) error.value = cause instanceof Error ? cause.message : '补救操作失败';
  } finally {
    if (!signal.aborted) busy.value = false;
  }
}
watch(() => [props.workspace, props.employeeId], () => {
  controller.abort();
  controller = new AbortController();
  data.value = { items: [], recipients: [] };
  busy.value = false;
  opened.value = false;
  target.value = undefined;
  error.value = '';
  status.value = '';
});
onUnmounted(() => controller.abort());
</script>

<template>
  <section class="recovery">
    <h5>投递状态与补救</h5>
    <p>只有确认未发送、且无人开始答复时，才可重试或改派。送达未知不会自动重发，管理员不能代答。</p>
    <NButton size="small" :loading="busy" @click="opened = true; load()">{{ opened ? '刷新状态' : '查看状态' }}</NButton>
    <NAlert v-if="status" type="success" role="status">{{ status }}</NAlert>
    <NAlert v-if="error" type="error" role="alert">{{ error }}</NAlert>
    <p v-if="opened && !busy && !data.items.length && !error">没有待处理的确认请求。</p>
    <article v-for="item in data.items" :key="item.id" class="recovery-row">
      <span>{{ names[item.provider] || item.provider }} · {{ item.id }} · v{{ item.version }}</span>
      <NTag size="small" :title="item.error_code">{{ states[item.status] || item.status }}</NTag>
      <NButton v-if="item.can_retry" size="small" :disabled="busy" @click="begin(item, 'retry')">重试通知</NButton>
      <NButton v-if="item.can_reassign" size="small" :disabled="busy" @click="begin(item, 'reassign')">改派</NButton>
    </article>
    <div v-if="target" class="recovery-form-card">
      <h6>{{ action === 'retry' ? '重试通知' : '改派确认人' }}</h6>
      <NSelect v-if="action === 'reassign'" v-model:value="recipientId" :options="options" placeholder="选择当前已验证的确认人" />
      <NInput v-model:value="reason" type="textarea" maxlength="1000" placeholder="填写补救原因" aria-label="补救原因" />
      <div class="recovery-row">
        <NButton :disabled="busy" @click="target = undefined">取消</NButton>
        <NButton type="primary" :disabled="busy || !reason.trim() || action === 'reassign' && !recipientId" @click="submit">确认提交</NButton>
      </div>
    </div>
  </section>
</template>

<style scoped>
.recovery { display: grid; justify-items: start; gap: 10px; padding: 12px 0; }
.recovery :deep(.n-alert) { width: 100%; box-sizing: border-box; }
.recovery-form-card { width: 100%; box-sizing: border-box; display: grid; gap: 10px; padding: 12px; background: var(--id-bg-subtle); border: 1px solid var(--id-border-subtle); border-radius: var(--id-radius-md); }
h5, h6, p { margin: 0; }
.recovery > p { color: var(--id-text-muted); }
.recovery-row { display: flex; flex-wrap: wrap; gap: 8px; align-items: center; overflow-wrap: anywhere; }
</style>

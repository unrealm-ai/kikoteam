<script setup lang="ts">
import { computed, onUnmounted, ref, watch } from 'vue';
import { NAlert, NButton, NCheckbox, NCheckboxGroup, NDrawer, NDrawerContent, NForm, NFormItem, NInput, NInputNumber, NModal, NSelect, NSkeleton, NTag, useDialog } from 'naive-ui';
import { D2ApiError } from '../api/client';
import { triggersApi, triggerLabel, type Trigger, type TriggerWrite, type TriggerCredential, type TriggerEvent, type TriggerTarget, type TriggerNotification } from '../api/triggers';
import PlatformIdentityPicker from '../components/PlatformIdentityPicker.vue';
import WebhookCopyField from '../components/WebhookCopyField.vue';
import type { EventSource } from '../api/triggers';
import type { SelectionCandidate } from '../api/selectors';
import { useDraftClose } from '../composables/useResourcePanel';

const props = defineProps<{ workspace: string; employeeId: string; canManage: boolean; hideBackButton?: boolean }>();
const emit = defineEmits<{ close: [] }>(), dialog = useDialog();
const items = ref<Trigger[]>([]), loading = ref(false), busy = ref(false), denied = ref(false), error = ref(''), notice = ref('');
const editor = ref(false), existing = ref<Trigger>(), baseline = ref('');
const fresh = (): TriggerWrite => ({
  name: '自动化任务',
  notification_targets: [],
  kind: 'schedule',
  enabled: true,
  config: { cron: '0 9 * * *', timezone: 'Asia/Shanghai', description: '', businessKey: { mode: 'empty' } },
  allowed_actions: ['create_task']
});
const form = ref<TriggerWrite>(fresh());
const needsGitLabSetup = computed(() => !existing.value && form.value.config.source === 'gitlab');
const saveEnabled = computed(() => form.value.enabled && !needsGitLabSetup.value);
const sources = [
  { label: 'GitHub', value: 'github' }, { label: 'GitLab · Signing token', value: 'gitlab' },
  { label: 'GitLab · 旧版 Secret token', value: 'gitlab_token' }, { label: '其他系统 · Bearer token', value: 'custom' }
];
function sourceChanged(source: EventSource) {
  form.value.config = { adapter: 'event', source, prompt: form.value.config.prompt ?? '' };
}
const eventSample = ref(''), sampleEventName = ref('push');
const eventPreview = computed(() => {
  const bytes = new TextEncoder().encode(eventSample.value).length;
  const metadata = { source: form.value.config.source, event: form.value.config.source === 'custom' ? '' : sampleEventName.value, content_type: 'application/json', method: 'POST', size_bytes: bytes };
  let body = JSON.stringify(eventSample.value);
  try { JSON.parse(eventSample.value); body = eventSample.value; } catch { /* Plain text stays a JSON string. */ }
  const data = bytes <= 8192
    ? '{"metadata":' + JSON.stringify(metadata) + ',"body":' + body + '}'
    : JSON.stringify({ metadata: { ...metadata, file: 'webhook-〈事件编号〉.body' } });
  return '本次事件（以下 JSON 是外部数据，预览）\n' + data + '\n\n请执行管理员为此 Webhook 配置的处理说明\n' + (form.value.config.prompt || '');
});
const selected = ref<Trigger>(), events = ref<TriggerEvent[]>([]), credentials = ref<TriggerCredential[]>([]), secret = ref<TriggerCredential>();
const eventError = ref(''), recordsLoading = ref(false), detailLoading = ref(false), cursor = ref(''), next = ref<string | null>(null), previous = ref<string[]>([]);
const credentialConfirming = ref(false);
const hasActiveCredential = computed(() => credentials.value.some(c => c.status === 'active'));
const selectedSource = computed(() => sources.find(s => s.value === selected.value?.config.source)?.label);
const signingToken = ref('');
watch(() => selected.value?.id, () => { signingToken.value = ''; });
async function importSigningToken() {
  const hook = selected.value, d = detailGeneration;
  if (!allowed.value || busy.value || !hook || !signingToken.value) return;
  busy.value = true;
  try {
    const saved = await triggersApi.importCredential(props.workspace, props.employeeId, hook.id, signingToken.value.trim(), detailController.signal);
    if (d !== detailGeneration) return;
    credentials.value = [saved, ...credentials.value];
    signingToken.value = '';
    notice.value = '签名凭据已保存。';
  } catch (e) { if (d === detailGeneration) fail(e); }
  finally { if (d === detailGeneration) busy.value = false; }
}
async function enableEventHook(token?: string) {
  const hook = selected.value, d = detailGeneration;
  if (!hook || hook.enabled || !allowed.value || busy.value || detailLoading.value) return;
  const { name, kind, config, allowed_actions, notification_targets } = hook;
  busy.value = true;
  try {
    const updated = await triggersApi.save(props.workspace, props.employeeId, {
      name, kind, config, allowed_actions, notification_targets, enabled: true,
      ...(token ? { signing_token: token } : {})
    }, detailController.signal, hook);
    if (d !== detailGeneration) return;
    selected.value = updated;
    signingToken.value = '';
    notice.value = '触发器已启用。';
    busy.value = false;
    void load();
    void detail(updated);
  } catch (e) { if (d === detailGeneration) fail(e); }
  finally { if (d === detailGeneration) busy.value = false; }
}
function isLocalAddress(endpoint: string) {
  const url = new URL(endpoint, window.location.origin);
  return ['localhost', '127.0.0.1', '[::1]'].includes(url.hostname);
}
let credentialDialogEpoch = 0;
let editGeneration = 0, targetRead = 0, eventRead = 0, generation = 0, read = 0, detailGeneration = 0;
let controller = new AbortController(), detailController = new AbortController();
const targets = ref<TriggerTarget[]>([]), targetsLoading = ref(false), targetsError = ref(''), targetsTruncated = ref(false);
const notifications = ref<TriggerNotification[]>([]), notificationsTruncated = ref(false);
const targetIDs = computed({
  get: () => form.value.notification_targets.map(t => t.conversation_id),
  set: (ids: string[]) => { form.value.notification_targets = ids.map(conversation_id => ({ conversation_id })); }
});
const targetOptions = computed(() => {
  const opts = targets.value.map(t => ({
    value: t.conversation_id,
    label: `${({ web: 'Web', feishu: '飞书', dingtalk: '钉钉', wecom: '企微' } as Record<string, string>)[t.provider] || t.provider} · ${t.label || t.conversation_id}`
  }));
  for (const id of targetIDs.value) if (!opts.some(t => t.value === id)) opts.push({ value: id, label: `未解析/当前不可选：${id}` });
  return opts;
});
const deliveryLabel = (v: string) => ({
  sent: '发送已确认',
  unknown: '结果未知，不自动重发',
  failed: '发送失败',
  accepted: '渠道已受理，送达待核对',
  queued: '待发送',
  leased: '已领取',
  sending: '发送中',
  pending: '待处理',
  cancelled: '已取消'
} as Record<string, string>)[v] || v;
function notificationTargetLabel(conversationId: string) {
  const match = targets.value.find(t => t.conversation_id === conversationId);
  if (!match) return `群会话 ${conversationId}`;
  const providerLabel = ({ web: 'Web', feishu: '飞书', dingtalk: '钉钉', wecom: '企微' } as Record<string, string>)[match.provider] || match.provider;
  return `${providerLabel} · ${match.label || conversationId}（${conversationId}）`;
}
async function loadTargets() {
  const g = generation, e = editGeneration, rn = ++targetRead; targetsLoading.value = true; targetsError.value = '';
  try {
    const r = await triggersApi.targets(props.workspace, props.employeeId, controller.signal);
    if (g !== generation || e !== editGeneration || rn !== targetRead) return;
    targets.value = r.items; targetsTruncated.value = r.truncated;
  } catch (err) {
    if (g === generation && e === editGeneration && rn === targetRead) {
      targetsError.value = text(err);
      if (err instanceof D2ApiError && [401, 403].includes(err.status)) fail(err);
    }
  } finally { if (g === generation && e === editGeneration && rn === targetRead) targetsLoading.value = false; }
}
async function addCandidate(c: SelectionCandidate) {
  if (!allowed.value || busy.value || targetsLoading.value || targetsError.value || !c.selectable || !c.ref.id) return;
  const g = generation, e = editGeneration; busy.value = true; error.value = '';
  try {
    const r = await triggersApi.resolveCandidate(props.workspace, props.employeeId, c.ref.provider, c.ref.id, controller.signal);
    if (g !== generation || e !== editGeneration || !editor.value || denied.value) return;
    if (!r.conversation_id) throw new Error('服务器没有返回有效群会话');
    if (!targetIDs.value.includes(r.conversation_id)) form.value.notification_targets.push({ conversation_id: r.conversation_id });
    if (!targets.value.some(t => t.conversation_id === r.conversation_id)) {
      targets.value.push({ conversation_id: r.conversation_id, provider: c.ref.provider, label: c.name || c.ref.external_id });
    }
  } catch (err) { if (g === generation && e === editGeneration) fail(err); }
  finally { if (g === generation) busy.value = false; }
}

const dirty = computed(() => JSON.stringify(form.value) !== baseline.value);
const allowed = computed(() => props.canManage && !denied.value);
const actionLabels: Record<string, string> = {
  create_task: '创建任务',
  supplement_task: '补充输入',
  cancel_task: '取消任务',
  send_message: '固定文案提醒'
};
const modes = [{ label: '每次独立空间', value: 'empty' }, { label: '固定业务键', value: 'fixed' }, { label: '按日历周期', value: 'period' }];
const periods = ['minute', 'hour', 'day', 'week', 'month', 'year'].map((v, i) => ({ value: v, label: ['分钟', '小时', '日', '周（周一开始）', '月', '年'][i]! }));
function text(e: unknown) { return e instanceof Error ? e.message : '请求失败'; }
function fail(e: unknown) {
  error.value = text(e);
  if (e instanceof D2ApiError && [401, 403].includes(e.status)) {
    denied.value = true; ++generation; ++detailGeneration; controller.abort(); detailController.abort();
    items.value = []; selected.value = undefined; secret.value = undefined; credentials.value = []; events.value = [];
    editor.value = false; ++editGeneration; targets.value = []; notifications.value = [];
    busy.value = false; loading.value = false; detailLoading.value = false; recordsLoading.value = false;
  }
}
async function load() {
  if (!allowed.value) return;
  const g = generation, r = ++read; loading.value = true; error.value = '';
  try {
    const v = await triggersApi.list(props.workspace, props.employeeId, controller.signal);
    if (g !== generation || r !== read) return;
    items.value = v.items;
  } catch (e) { if (g === generation && r === read) fail(e); }
  finally { if (g === generation && r === read) loading.value = false; }
}
function discard() {
  ++editGeneration; editor.value = false; targetsLoading.value = false;
  error.value = '';
  existing.value = undefined; form.value = fresh(); baseline.value = JSON.stringify(form.value);
}
const closeEditor = useDraftClose('自动化任务配置', editor, dirty, busy, discard, save);
function open(t?: Trigger) {
  if (!allowed.value || busy.value) return;
  eventSample.value = '';
  sampleEventName.value = 'push';
  ++editGeneration; existing.value = t;
  form.value = t ? JSON.parse(JSON.stringify({ name: t.name || '触发器', kind: t.kind, enabled: t.enabled, config: t.config, allowed_actions: t.allowed_actions, notification_targets: t.notification_targets || [] })) : fresh();
  baseline.value = JSON.stringify(form.value); editor.value = true; error.value = ''; notice.value = ''; secret.value = undefined;
  void loadTargets();
}
function kind(v: 'schedule' | 'generic' | 'github_issue' | 'event') {
  form.value.kind = v === 'schedule' ? 'schedule' : 'webhook';
  if (v === 'event') {
    form.value.config = { adapter: v, source: 'github', prompt: '' };
  } else {
    form.value.config = v === 'schedule' ? fresh().config : { adapter: v };
  }
  form.value.allowed_actions = ['create_task'];
}
function scheduleAction(v: 'create_task' | 'send_message') {
  form.value.config.action = v; form.value.allowed_actions = [v];
  if (v === 'send_message') delete form.value.config.businessKey;
  else form.value.config.businessKey = { mode: 'empty' };
}
function keyMode(v: 'empty' | 'fixed' | 'period') {
  form.value.config.businessKey = v === 'fixed' ? { mode: v, value: '' } : v === 'period' ? { mode: v, period: 'day' } : { mode: v };
}
async function save() {
  if (!allowed.value || busy.value || targetsLoading.value || !!targetsError.value) return false;
  if (!form.value.name.trim()) { error.value = '请填写触发器名称'; return false; }
  if (form.value.config.adapter === 'event' && !form.value.config.prompt?.trim()) {
    error.value = '请填写员工处理说明'; return false;
  }
  if (form.value.config.action === 'send_message' && !form.value.notification_targets.length) { error.value = '固定提醒至少需要一个群目标'; return false; }
  const g = generation, w = props.workspace, a = props.employeeId, row = existing.value;
  busy.value = true; error.value = ''; notice.value = '';
  try {
    const { initial_credential, ...result } = await triggersApi.save(w, a, { ...form.value, enabled: saveEnabled.value, name: form.value.name.trim() }, controller.signal, row);
    if (g !== generation) return false;
    discard();
    notice.value = row ? '配置已保存。' : result.enabled ? '触发器已创建并启用。' : '触发器已创建，当前已停用。';
    void load();
    void detail(result, initial_credential);
    return true;
  } catch (e) {
    if (g === generation) {
      fail(e);
      if (!denied.value) error.value += '；不会自动重试。请求结果不确定时请先刷新列表核对，避免重复创建。';
    }
    return false;
  } finally { if (g === generation) busy.value = false; }
}
async function detail(t: Trigger, initialCredential?: TriggerCredential) {
  if ((busy.value && !notice.value) || !allowed.value) return;
  const g = generation, d = ++detailGeneration;
  detailController.abort(); detailController = new AbortController();
  if (selected.value?.id !== t.id || initialCredential) secret.value = initialCredential;
  selected.value = t; notifications.value = []; credentials.value = []; events.value = [];
  cursor.value = ''; next.value = null; previous.value = []; eventError.value = ''; detailLoading.value = true;
  try {
    const current = await triggersApi.get(props.workspace, props.employeeId, t.id, detailController.signal);
    if (g !== generation || d !== detailGeneration) return;
    selected.value = current;
    await records();
    if (g !== generation || d !== detailGeneration || denied.value) return;
    const ns = await triggersApi.notifications(props.workspace, props.employeeId, t.id, detailController.signal);
    if (g !== generation || d !== detailGeneration) return;
    notifications.value = ns.items; notificationsTruncated.value = ns.truncated;
    if (current.kind === 'webhook') {
      const r = await triggersApi.credentials(props.workspace, props.employeeId, t.id, detailController.signal);
      if (g !== generation || d !== detailGeneration) return;
      credentials.value = r.items;
    }
  } catch (e) { if (g === generation && d === detailGeneration) fail(e); }
  finally { if (g === generation && d === detailGeneration) detailLoading.value = false; }
}
async function records() {
  if (!selected.value || !allowed.value) return;
  const g = generation, d = detailGeneration, rn = ++eventRead, id = selected.value.id;
  eventError.value = ''; recordsLoading.value = true;
  try {
    const r = await triggersApi.events(props.workspace, props.employeeId, id, cursor.value, detailController.signal);
    if (g !== generation || d !== detailGeneration || rn !== eventRead) return;
    if (r.next_cursor && (r.next_cursor === cursor.value || previous.value.includes(r.next_cursor))) throw new Error('触发记录分页游标重复');
    events.value = r.items; next.value = r.next_cursor;
  } catch (e) {
    if (g === generation && d === detailGeneration) { if (rn !== eventRead) return; eventError.value = text(e); fail(e); }
  } finally { if (g === generation && d === detailGeneration && rn === eventRead) recordsLoading.value = false; }
}
function page(forward: boolean) {
  if (busy.value || detailLoading.value || recordsLoading.value) return;
  if (forward && next.value) { previous.value.push(cursor.value); cursor.value = next.value; }
  else if (!forward && previous.value.length) cursor.value = previous.value.pop() || '';
  void records();
}
function credential(key?: string) {
  if (!selected.value || busy.value || credentialConfirming.value || !allowed.value) return;
  credentialConfirming.value = true;
  const token = ++credentialDialogEpoch;
  const target = selected.value.id, g = generation, d = detailGeneration;
  dialog.warning({
    style: { width: 'min(446px, 94vw)' },
    onAfterLeave: () => { if (token === credentialDialogEpoch) credentialConfirming.value = false; },
    title: key ? '撤销签名密钥' : '生成签名密钥',
    content: key ? '使用此密钥的后续请求将被拒绝。历史记录保持。' : '新密钥仅在生成成功时显示一次。请妥善保存，不会写入浏览器存储。',
    positiveText: key ? '确认撤销' : '确认生成',
    negativeText: '取消',
    onPositiveClick: async () => {
      if (busy.value || token !== credentialDialogEpoch || g !== generation || d !== detailGeneration || selected.value?.id !== target || !allowed.value) return;
      busy.value = true; error.value = '';
      try {
        if (key) {
          await triggersApi.revokeCredential(props.workspace, props.employeeId, target, key, detailController.signal);
          if (d === detailGeneration && secret.value?.key_id === key) secret.value = undefined;
        }
        else {
          const result = await triggersApi.createCredential(props.workspace, props.employeeId, target, detailController.signal);
          if (g !== generation || d !== detailGeneration) return;
          secret.value = result;
        }
        if (g !== generation || d !== detailGeneration || denied.value) return;
        const r = await triggersApi.credentials(props.workspace, props.employeeId, target, detailController.signal);
        if (g !== generation || d !== detailGeneration) return;
        credentials.value = r.items; notice.value = key ? '密钥已撤销。' : '密钥已生成，请保存本次显示的密钥。';
      } catch (e) { if (g === generation && d === detailGeneration) fail(e); }
      finally { if (g === generation) busy.value = false; }
    }
  });
}
function closeDetail() {
  if (busy.value || credentialConfirming.value) return;
  ++detailGeneration;
  detailController.abort();
  selected.value = undefined;
  secret.value = undefined;
  error.value = '';
  notice.value = '';
  detailLoading.value = false;
  recordsLoading.value = false;
}
function close() {
  if (busy.value) return;
  if (editor.value) { closeEditor(); return; }
  secret.value = undefined; emit('close');
}
function reset() {
  ++credentialDialogEpoch; credentialConfirming.value = false; ++generation; ++detailGeneration;
  controller.abort(); detailController.abort(); controller = new AbortController(); detailController = new AbortController();
  items.value = []; selected.value = undefined; events.value = []; credentials.value = []; secret.value = undefined;
  denied.value = false; busy.value = false; loading.value = false; detailLoading.value = false;
  error.value = ''; notice.value = ''; targets.value = []; targetsError.value = ''; targetsLoading.value = false;
  notifications.value = []; discard(); void load();
}
watch(() => [props.workspace, props.employeeId, props.canManage], reset, { immediate: true });
onUnmounted(() => { ++generation; ++detailGeneration; controller.abort(); detailController.abort(); secret.value = undefined; });
</script>

<template>
  <section class="tab-panel-inner">
    <div class="panel-desc-bar">
      <span><strong>自动触发器</strong> 允许通过定时 Cron、外部 Webhook 或 GitHub Issue 触发员工执行任务。</span>
      <div class="btn-group">
        <NButton v-if="!hideBackButton" size="small" quaternary :disabled="busy" @click="close">返回员工列表</NButton>
        <template v-if="allowed">
          <NButton size="small" secondary :disabled="busy || editor || loading" @click="load">刷新触发器</NButton>
          <NButton size="small" type="primary" :disabled="busy || editor || items.length >= 100" @click="open()">新增自动化任务</NButton>
        </template>
      </div>
    </div>
    <NAlert v-if="!allowed" type="warning">需要当前工作区管理员权限；已撤权的内容不再展示。</NAlert>
    <NAlert v-if="error && (!allowed || (!editor && !selected))" type="error">{{ error }}</NAlert>
    <NAlert v-if="notice && allowed && !editor && !selected" type="success">{{ notice }}</NAlert>

    <template v-if="allowed">
      <NSkeleton v-if="loading" text :repeat="3" />
      <div v-else-if="!items.length && !error" class="empty-channel-box">
        <p>员工暂未配置自动触发器。</p>
        <NButton size="small" type="primary" :disabled="busy || editor" @click="open()">创建第一个自动化任务</NButton>
      </div>
      <div v-else class="trigger-list">
        <article v-for="t in items" :key="t.id" class="ch-section trigger-card">
          <div class="cs-header-flex">
            <div class="trigger-title-group">
              <h3 class="cs-title">{{ t.name || '触发器' }}</h3>
              <NTag v-if="t.kind === 'schedule'" size="tiny" type="info" round>定时 Cron 调度</NTag>
              <NTag v-else-if="t.config.adapter === 'github_issue'" size="tiny" type="success" round>GitHub Issue 联动</NTag>
              <NTag v-else-if="t.config.adapter === 'event'" size="tiny" type="success" round>接收外部事件</NTag>
              <NTag v-else size="tiny" type="warning" round>Webhook HMAC 调用</NTag>
              <NTag v-for="action in t.allowed_actions" :key="action" size="tiny" type="info">{{ actionLabels[action] || action }}</NTag>
              <NTag size="tiny" :type="t.enabled ? 'success' : 'default'">{{ t.enabled ? '已启用' : '已停用' }}</NTag>
            </div>
            <div class="btn-group">
              <NButton size="tiny" secondary :disabled="busy || editor" @click="detail(t)">查看触发记录与接入信息</NButton>
              <NButton size="tiny" :disabled="busy || editor" @click="open(t)">编辑配置</NButton>
            </div>
          </div>
          <div class="trigger-summary">
            <span v-if="t.kind === 'schedule'">Cron 计划：<code>{{ t.config.cron }}</code>（{{ t.config.timezone || 'Asia/Shanghai' }}）</span>
            <span v-else-if="t.config.adapter === 'github_issue'">关联仓库 ID：<code>{{ t.config.repositoryId }}</code></span>
            <WebhookCopyField v-else-if="t.endpoint" :key="t.endpoint" :value="t.endpoint" label="接收地址" copy-label="复制地址" compact />
            <span v-else>调用端点：<code>/api/triggers/webhook/{{ t.id }}</code></span>
            <p v-if="t.config.description" class="trigger-description">{{ t.config.action === 'send_message' ? '提醒正文' : '执行内容' }}：{{ t.config.description }}</p>
            <span v-if="t.next_due">下次计划时刻：{{ new Date(t.next_due).toLocaleString('zh-CN') }}（本地显示）</span>
            <small class="mono-sub">ID {{ t.id }} · v{{ t.version }}</small>
            <div class="trigger-target-summary">结果回告工作群：{{ t.notification_targets?.length ? t.notification_targets.length + ' 个群' : '未配置回告群' }}</div>
          </div>
        </article>
      </div>

      <NDrawer
        :show="editor" width="min(600px, 95vw)" placement="right"
        :mask-closable="!busy" :close-on-esc="!busy" @update:show="shown => { if (!shown) closeEditor(); }"
      >
        <NDrawerContent :title="existing ? '编辑自动化任务' : '新增自动化任务'" :closable="!busy">
          <NForm v-if="editor" label-placement="top" :disabled="busy" class="trigger-editor-form">
            <p class="field-note">定时与 Webhook 使用独立触发身份及能力包范围，不借用管理员的人工权限。</p>
            <NFormItem label="触发器名称"><NInput v-model:value="form.name" maxlength="100" placeholder="例如：客户日报" /></NFormItem>
            <NFormItem label="触发类型">
              <NSelect
                :virtual-scroll="false"
                :value="form.kind === 'schedule' ? 'schedule' : form.config.adapter || 'generic'"
                :disabled="!!existing || busy"
                :options="[{ label: '定时任务', value: 'schedule' }, { label: '接收外部事件', value: 'event' }, { label: '通用 Webhook', value: 'generic' }, { label: 'GitHub 新建 Issue', value: 'github_issue' }]"
                @update:value="kind"
              />
            </NFormItem>
            <div class="editor-check-block">
              <NCheckbox :checked="saveEnabled" :disabled="needsGitLabSetup" @update:checked="v => form.enabled = v">启用此触发器</NCheckbox>
              <p v-if="needsGitLabSetup" class="field-note">先创建并复制接收地址，再填入 GitLab 生成的 Signing token 即可启用。</p>
              <p v-else class="field-note">启用后将按配置执行，模型任务可能产生费用。</p>
            </div>

            <template v-if="form.kind === 'schedule'">
              <NFormItem label="定时动作">
                <NSelect
                  :virtual-scroll="false"
                  :value="form.config.action || 'create_task'"
                  :options="[{ label: '创建模型任务', value: 'create_task' }, { label: '发送固定文案提醒（不调用模型）', value: 'send_message' }]"
                  @update:value="scheduleAction"
                />
              </NFormItem>
              <NFormItem label="Cron（分 时 日 月 周）"><NInput :value="form.config.cron || ''" @update:value="v => form.config.cron = v" placeholder="0 9 * * *" /></NFormItem>
              <NFormItem label="IANA 时区"><NInput :value="form.config.timezone || ''" @update:value="v => form.config.timezone = v" placeholder="Asia/Shanghai" /></NFormItem>
              <NFormItem :label="form.config.action === 'send_message' ? '固定提醒正文' : '每次任务说明'">
                <NInput :value="form.config.description || ''" @update:value="v => form.config.description = v" type="textarea" placeholder="让员工完成什么工作" />
              </NFormItem>
              <template v-if="form.config.action !== 'send_message'">
                <NFormItem label="任务空间业务键">
                  <div class="business-key-stack">
                    <NSelect :virtual-scroll="false" :value="form.config.businessKey?.mode || 'empty'" :options="modes" @update:value="keyMode" />
                    <NInput v-if="form.config.businessKey?.mode === 'fixed'" :value="form.config.businessKey.value || ''" @update:value="v => { if (form.config.businessKey) form.config.businessKey.value = v; }" placeholder="固定业务键" />
                    <NSelect v-if="form.config.businessKey?.mode === 'period'" :virtual-scroll="false" :value="form.config.businessKey.period || 'day'" @update:value="v => { if (form.config.businessKey) form.config.businessKey.period = v; }" :options="periods" />
                    <p class="field-note">空键每次独立；固定键让同键任务归入同一空间；周期键按计划应触发时刻与所选 IANA 时区计算周期起止，周从周一开始，日/月/年按当地日历边界。服务器用周期类型和 UTC 起止时刻生成稳定业务键，重试不改变该键。分组不改变 Cron 频率，也不授予资料或能力包权限。</p>
                  </div>
                </NFormItem>
              </template>
              <p v-else class="field-note">固定文案提醒不创建 Task、Session 或任务空间，不调用模型。进入群上下文与是否发送成功分别展示。</p>
            </template>

            <template v-else-if="form.config.adapter === 'event'">
              <NFormItem label="谁会发通知"><NSelect :value="form.config.source ?? null" :options="sources" :disabled="!!existing" :virtual-scroll="false" @update:value="sourceChanged" /></NFormItem>
              <NFormItem label="员工处理说明（Prompt）">
                <NInput :value="form.config.prompt ?? ''" @update:value="v => form.config.prompt=v" type="textarea" maxlength="4000" show-count :autosize="{minRows:3,maxRows:8}"
                  placeholder="例如：main 有推送时概括代码变化；新建 Issue 时给出标题和链接。" />
              </NFormItem>
              <details class="sig-details event-preview-details"><summary>预览员工输入</summary>
                <div class="event-preview-body">
                  <NFormItem v-if="form.config.source !== 'custom'" label="样例事件名"><NInput v-model:value="sampleEventName" /></NFormItem>
                  <NFormItem label="样例正文"><NInput v-model:value="eventSample" type="textarea" placeholder="粘贴一段通知正文" /></NFormItem>
                  <pre v-if="eventSample" class="event-preview">{{ eventPreview }}</pre>
                </div>
              </details>
              <p class="field-note">保存后可复制接收地址。业务正文交给员工及其模型处理，单次最多 256 KiB。</p>
            </template>
            <template v-else-if="form.config.adapter === 'github_issue'">
              <NFormItem label="GitHub 仓库 ID">
                <NInputNumber :value="form.config.repositoryId || null" :min="1" :precision="0" @update:value="v => { if (v) form.config.repositoryId = v; else delete form.config.repositoryId; }" />
              </NFormItem>
              <NFormItem label="GitHub 安装 ID（选填）">
                <NInputNumber :value="form.config.installationId || null" :min="1" :precision="0" @update:value="v => { if (v) form.config.installationId = v; else delete form.config.installationId; }" />
              </NFormItem>
              <p class="field-note">仅处理指定仓库 issues opened，以 Issue 内容创建任务；其他 Issue 动作忽略，不处理 PR、push 或评论。</p>
            </template>

            <NFormItem v-else label="允许动作">
              <NCheckboxGroup v-model:value="form.allowed_actions">
                <NCheckbox value="create_task">创建任务</NCheckbox>
                <NCheckbox value="supplement_task">补充任务</NCheckbox>
                <NCheckbox value="cancel_task">取消任务</NCheckbox>
              </NCheckboxGroup>
            </NFormItem>

            <div class="flow-section">
              <h3 class="fs-title">结果同步到指定群</h3>
              <PlatformIdentityPicker :workspace="workspace" :employee-id="employeeId" kind="groups" purpose="notification" :disabled="busy || targetsLoading || !!targetsError" @pick="addCandidate" @revoked="denied = true" />
              <div v-for="id in targetIDs" :key="id" class="target-chip-row">
                <span>{{ targetOptions.find(t => t.value === id)?.label || id }}</span>
                <NButton size="tiny" quaternary type="error" :disabled="busy" @click="targetIDs = targetIDs.filter(v => v !== id)">移除此群</NButton>
              </div>
              <p class="field-note">Web 候选按 AI 员工当前所在群筛选；管理员无需本人入群即可配置必要群元数据，但这不授予读取群消息的权限。外部候选明确标注平台核验或可信历史群消息，不伪称完整群目录。添加时服务器重新验证，并可按需创建该员工的群会话；验证完成前不会选中。暂不支持群话题。保存和发送时仍重新校验权限。</p>
              <p v-if="targetsLoading" class="field-note">正在读取真实群目标…</p>
              <NAlert v-if="targetsError" type="error">{{ targetsError }} <NButton size="small" @click="loadTargets">重试群目标</NButton></NAlert>
              <NAlert v-if="targetsTruncated" type="warning">既有群会话名称只加载最近 100 个；未解析的已选 ID 仍保留。统一候选可按渠道搜索并继续分页。</NAlert>
            </div>

          </NForm>
          <template #footer>
            <div class="editor-footer">
              <NAlert v-if="error" type="error">{{ error }}</NAlert>
              <div class="btn-group">
                <NButton :disabled="busy" @click="closeEditor">关闭编辑</NButton>
                <NButton type="primary" :loading="busy" :disabled="busy || targetsLoading || !!targetsError" @click="save">{{ existing ? '保存触发器' : saveEnabled ? '创建并启用' : '仅创建' }}</NButton>
              </div>
            </div>
          </template>
        </NDrawerContent>
      </NDrawer>

      <NModal
        :show="!!selected" preset="card" title="触发记录与接入信息" aria-label="触发记录与接入信息"
        style="width: min(600px, 94vw); max-height: 90dvh"
        :closable="!busy && !credentialConfirming" :mask-closable="!busy && !credentialConfirming"
        :close-on-esc="!busy && !credentialConfirming" @update:show="shown => { if (!shown) closeDetail(); }"
        :content-style="{ overflowY: 'auto' }"
      >
        <section v-if="selected" class="trigger-detail">
          <NAlert v-if="error && error !== eventError" type="error">{{ error }}</NAlert>
          <NAlert v-if="notice" type="success">{{ notice }}</NAlert>
          <div v-if="selected.kind === 'webhook'" class="webhook-box">
            <p v-if="selectedSource" class="field-note">通知来源 · {{ selectedSource }}</p>
            <WebhookCopyField v-if="selected.endpoint" :key="selected.endpoint" :value="selected.endpoint" label="接收地址" copy-label="复制地址" />
            <NAlert v-if="secret?.secret" type="warning">
              <WebhookCopyField :key="secret.key_id" :value="secret.secret" label="Webhook 密钥" copy-label="复制密钥" />
              <p v-if="selected.config.adapter === 'generic'" class="field-note">X-Key-Id · {{ secret.key_id }}</p>
              <p class="field-note">请保存密钥，关闭后无法再次查看。</p>
              <NButton size="small" @click="secret = undefined">已保存，隐藏密钥</NButton>
            </NAlert>
            <template v-if="selected.config.source === 'gitlab'">
              <NInput v-model:value="signingToken" type="password" placeholder="GitLab Signing token" />
              <div class="btn-group">
                <NButton v-if="!selected.enabled" type="primary" size="small" :disabled="!signingToken.trim() || busy || detailLoading" @click="enableEventHook(signingToken.trim())">保存并启用</NButton>
                <NButton size="small" :disabled="!signingToken.trim() || busy || detailLoading" @click="importSigningToken">保存签名凭据</NButton>
              </div>
            </template>
            <NAlert v-if="selected.endpoint && isLocalAddress(selected.endpoint)" type="warning">这是本机地址。请将 KIKO_PUBLIC_ORIGIN 设为发送方可访问的站点地址。</NAlert>
            <p v-if="selected.config.adapter === 'event'" class="field-note">{{ selected.last_verified_at ? '最近验证通过：'+new Date(selected.last_verified_at).toLocaleString() : '尚未收到已验证的通知。' }}</p>
            <template v-if="!selected.enabled">
              <NButton v-if="hasActiveCredential" size="small" type="primary" :disabled="busy || detailLoading" @click="enableEventHook()">启用触发器</NButton>
            </template>
          </div>
          <div>
            <h3 class="cs-title">{{ triggerLabel(selected) }}</h3>
            <p class="cs-sub">已受理不等于执行完成；以下为真实接收回执，不推断任务运行或消息送达状态。</p>
          </div>
          <p v-if="detailLoading" class="field-note">正在读取接入信息…</p>

          <div class="flow-section">
            <div class="cs-header-flex">
              <h4 class="fs-title" style="border: 0; padding: 0;">触发事件回执</h4>
              <NButton size="tiny" secondary :disabled="busy || detailLoading || recordsLoading" @click="records">刷新触发记录</NButton>
            </div>
            <NAlert v-if="eventError" type="error">{{ eventError }}</NAlert>
            <p v-if="!detailLoading && !events.length && !eventError" class="field-note">暂无触发记录。</p>
            <div v-if="events.length" class="history-records">
              <article v-for="e in events" :key="e.id" class="detail-record-card">
                <strong>{{ actionLabels[e.action] || e.action }} · {{ e.status }}</strong>
                <p class="field-note">{{ new Date(e.received_at).toLocaleString('zh-CN') }} · 配置 v{{ e.binding_version }}</p>
                <small class="mono-sub">事件 {{ e.event_id }}</small>
                <p v-if="e.receipt" class="field-note" style="margin-top: 4px;">
                  <span v-if="e.event_name">{{ e.event_name }} · </span><span v-if="e.execution_status">执行 {{ e.execution_status }}<br /></span>
                  受理状态 {{ e.receipt.status }} · {{ e.receipt.duplicate ? '重复请求回执' : '首次受理' }}<br />
                  任务 {{ e.action === 'send_message' ? '固定提醒不创建任务' : e.receipt.task_id || '未创建' }}<br />
                  空间 {{ e.action === 'send_message' ? '固定提醒不创建空间' : e.receipt.task_space_id || '未创建' }}
                </p>
              </article>
            </div>
            <div class="btn-group" style="justify-content: flex-end;">
              <NButton size="small" secondary :disabled="busy || detailLoading || recordsLoading || !previous.length" @click="page(false)">上一页记录</NButton>
              <NButton size="small" secondary :disabled="busy || detailLoading || recordsLoading || !next" @click="page(true)">下一页记录</NButton>
            </div>
          </div>

          <div class="flow-section">
            <div class="cs-header-flex">
              <h4 class="fs-title" style="border: 0; padding: 0;">群结果投递</h4>
              <NButton size="tiny" secondary :disabled="busy || detailLoading" @click="detail(selected)">刷新接入与投递</NButton>
            </div>
            <p v-if="!notifications.length && !detailLoading" class="field-note">暂无投递记录。</p>
            <p v-if="notificationsTruncated" class="field-note">只显示最近 100 条投递，非完整历史。</p>
            <div v-if="notifications.length" class="history-records">
              <article v-for="(n, i) in notifications" :key="n.source_input_id + ':' + n.conversation_id + ':' + i" class="detail-record-card">
                <strong>{{ deliveryLabel(n.status) }}</strong>
                <p class="field-note">{{ n.reminder_event_id ? '固定提醒 ' + n.reminder_event_id : '任务 ' + (n.task_id || '未关联') }} · {{ notificationTargetLabel(n.conversation_id) }}</p>
                <small class="mono-sub">
                  上下文 {{ ({ pending: '等待入上下文', admitted: '已进入上下文队列', unavailable: '无法写入上下文', suppressed: '未写入上下文' } as Record<string, string>)[n.context_status] || n.context_status }}
                  <template v-if="n.error_code"> · {{ n.error_code }}</template>
                  <template v-if="n.context_error"> · {{ n.context_error }}</template>
                </small>
              </article>
            </div>
          </div>

          <template v-if="selected.kind === 'webhook'">
            <div class="webhook-box">
              <h4 class="cs-title">{{ selected.config.adapter === 'github_issue' ? 'GitHub 接入信息' : 'Webhook 接入信息' }}</h4>
              <code v-if="!selected.endpoint && selected.config.adapter !== 'event'" class="webhook-url">/api/triggers/{{ selected.config.adapter === 'github_issue' ? 'github' : 'webhook' }}/{{ selected.id }}</code>
              <template v-if="selected.config.adapter === 'event'">
                <p v-if="selected.config.source==='github'">在仓库 Settings → Webhooks 粘贴接收地址，将 Webhook 密钥填入 Secret，勾选 Pushes、Issues 等事件。GitHub 保存时会发送 ping 检查连接。</p>
                <p v-else-if="selected.config.source==='gitlab'">在 GitLab 项目 Settings → Webhooks 填入接收地址并选择事件，将项目生成的 Signing token 粘贴到本页。</p>
                <p v-else-if="selected.config.source==='gitlab_token'">在项目 Webhooks 粘贴接收地址，将 Webhook 密钥填入 Secret token，再选择事件。</p>
                <p v-else>向接收地址 POST 业务正文，使用 Authorization: Bearer 加 Webhook 密钥。重试同一事件可复用 Idempotency-Key，省略时每次请求独立建任务。</p>
                <p v-if="selected.config.source?.startsWith('gitlab')" class="field-note">在 GitLab 点击 Test 会发送业务事件，启用后会创建员工任务。</p>
              </template>
              <p v-else-if="selected.config.adapter === 'github_issue'" class="field-note">
                GitHub 配置此 HTTPS Webhook 地址与本次生成的 secret，选择 Issues 事件。服务验证 X-Hub-Signature-256，对原始 body 按 GitHub HMAC-SHA256 协议验签，不使用通用 Hook 的时间戳协议。
              </p>
              <template v-else>
                <p class="field-note">POST 请求使用 X-Key-Id、X-Timestamp（Unix 毫秒）、X-Signature。仅接受 HTTPS，拒绝浏览器 Cookie/Origin 身份。时间戳允许前后 5 分钟；不把密钥放进 URL。</p>
                <details class="sig-details">
                  <summary>通用签名与请求格式</summary>
                  <p class="field-note" style="margin: 8px 0;">对原始请求 body 计算 SHA-256 小写十六进制摘要，再以密钥对下列五行（用换行分隔，无尾部换行）计算 HMAC-SHA256 小写十六进制签名：</p>
                  <pre class="sig-pre">v1
POST
/api/triggers/webhook/{{ selected.id }}
X-Timestamp 的原值
body 的 SHA-256 摘要</pre>
                  <p class="field-note" style="margin-top: 8px;">body 包含 version:1、稳定 eventId、action；创建或补充使用 description。补充/取消指定 taskId；创建可选 businessKey。相同 eventId 必须保持相同原始 body，不以重发创建新任务。此页面不会发送测试 Hook。</p>
                </details>
              </template>
              <div class="btn-group">
                <NButton v-if="selected.config.source !== 'gitlab'" size="small" secondary :disabled="busy || detailLoading || credentialConfirming" @click="credential()">生成新签名密钥</NButton>
              </div>
              <div v-for="c in credentials" :key="c.key_id" class="cred-row">
                <span class="mono-sub">{{ c.key_id }} · {{ c.status }}</span>
                <NButton v-if="c.status === 'active'" size="tiny" quaternary type="error" :disabled="busy || credentialConfirming" @click="credential(c.key_id)">撤销密钥</NButton>
              </div>
            </div>
          </template>
        </section>
      </NModal>
    </template>
  </section>
</template>

<style scoped>
@import '../components/workforce/workforce-shared.css';

.event-preview { white-space: pre-wrap; overflow-wrap: anywhere; max-height: 18rem; overflow: auto; }

.trigger-list,
.trigger-detail,
.trigger-summary {
  display: flex;
  flex-direction: column;
}

.trigger-list,
.trigger-detail {
  gap: 12px;
}

.trigger-card {
  padding: 14px;
  gap: 10px;
  overflow-wrap: anywhere;
}

.trigger-card .cs-header-flex {
  align-items: center;
}

.trigger-title-group {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 8px;
}

.trigger-summary {
  gap: 6px;
  font-size: 12px;
  color: var(--id-text-secondary);
  font-variant-numeric: tabular-nums;
}

.trigger-description {
  display: -webkit-box;
  -webkit-box-orient: vertical;
  -webkit-line-clamp: 2;
  overflow: hidden;
  margin: 0;
  color: var(--id-text-primary);
}

.trigger-target-summary {
  padding: 4px 8px;
  border-radius: var(--id-radius-sm);
  background: var(--id-bg-muted);
}

.trigger-detail,
.target-chip-row {
  overflow-wrap: anywhere;
}

.editor-footer {
  width: 100%;
  display: grid;
  gap: 8px;
}

.editor-footer .btn-group {
  justify-content: flex-end;
}

.mono-sub {
  display: block;
  font-size: 12px;
  font-family: var(--id-font-mono);
  color: var(--id-text-muted);
  overflow-wrap: anywhere;
}

.trigger-editor-form {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.business-key-stack {
  width: 100%;
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.event-preview-details {
  border: 1px solid var(--id-border-subtle);
  border-radius: var(--id-radius-md);
  padding: 10px 12px;
  background: var(--id-bg-subtle);
}

.event-preview-body {
  display: flex;
  flex-direction: column;
  gap: 8px;
  margin-top: 10px;
}

.editor-check-block {
  display: flex;
  flex-direction: column;
  gap: 4px;
  margin-bottom: 8px;
}

.target-chip-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  padding: 6px 10px;
  background: var(--id-bg-subtle);
  border: 1px solid var(--id-border-subtle);
  border-radius: var(--id-radius-md);
  font-size: 12px;
}

.webhook-box {
  display: flex;
  flex-direction: column;
  gap: 10px;
  padding: 12px;
  background: var(--id-bg-subtle);
  border: 1px solid var(--id-border-subtle);
  border-radius: var(--id-radius-md);
}

.webhook-url {
  display: block;
  font-family: var(--id-font-mono);
  font-size: 12px;
  padding: 8px 10px;
  background: var(--id-bg-surface);
  border: 1px solid var(--id-border-subtle);
  border-radius: var(--id-radius-sm);
  overflow-wrap: anywhere;
}

.sig-details summary {
  cursor: pointer;
  font-size: 12px;
  font-weight: 500;
  color: var(--id-text-primary);
}

.sig-pre {
  margin: 0;
  padding: 10px 12px;
  background: var(--id-bg-terminal);
  color: var(--id-text-terminal);
  border-radius: var(--id-radius-md);
  font-family: var(--id-font-mono);
  font-size: 12px;
  overflow: auto;
}

.cred-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  padding: 6px 10px;
  background: var(--id-bg-surface);
  border: 1px solid var(--id-border-subtle);
  border-radius: var(--id-radius-sm);
}

.history-records {
  display: grid;
  gap: 8px;
  max-height: min(340px, 40dvh);
  overflow-y: auto;
}

.detail-record-card {
  padding: 10px 12px;
  background: var(--id-bg-subtle);
  border: 1px solid var(--id-border-subtle);
  border-radius: var(--id-radius-md);
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.detail-record-card strong {
  font-size: 13px;
  color: var(--id-text-primary);
}
</style>

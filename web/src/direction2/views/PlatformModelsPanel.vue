<script setup lang="ts">
import { ref, computed, watch, onUnmounted } from 'vue';
import {
  NButton, NInput, NSelect, NEmpty, NForm, NFormItem, NDrawer, NDrawerContent,
  NAlert, NPopconfirm, NPagination, NSkeleton, useMessage
} from 'naive-ui';
import { activeWorkspaceId } from '../api/client';
import { platformRequest } from '../api/platform';
import { modelProfilesApi, type ModelProfile, type ModelConfiguration, type ModelTestResult, type ModelCaseResult } from '../api/models';

const message = useMessage();
const profiles = ref<ModelProfile[]>([]);
const role = ref('');
const canManage = computed(() => ['owner', 'admin'].includes(role.value));
const loading = ref(false);
const error = ref('');
const page = ref(1);
const total = ref(0);
const show = ref(false);
const existing = ref<ModelProfile>();
const fresh = (): ModelConfiguration => ({ name: '', description: '', protocol: 'chat_completions', base_url: '', model_id: '' });
const form = ref(fresh());
const key = ref('');
const testing = ref(false);
const saving = ref(false);
const actionBusy = ref(false);
const result = ref<ModelTestResult>();
const testError = ref('');
const now = ref(Date.now());
let epoch = 0;
let testEpoch = 0;
let loadController: AbortController | undefined;
let testController: AbortController | undefined;
const clock = setInterval(() => { now.value = Date.now(); }, 1000);
const protocols = [
  { label: 'Chat Completions', value: 'chat_completions' },
  { label: 'OpenAI Responses', value: 'openai_responses' },
  { label: 'Anthropic Messages', value: 'anthropic_messages' }
];
function normalizedBaseURL(value: string): string {
  try {
    const url = new URL(value.trim());
    url.pathname = url.pathname.replace(/\/+$/, '') || '/v1';
    return url.href;
  } catch { return value.trim(); }
}
const needsExplicitKey = computed(() => !!existing.value?.has_key && normalizedBaseURL(form.value.base_url) !== normalizedBaseURL(existing.value.base_url));
const expiry = computed(() => result.value ? Date.parse(result.value.expires_at) : 0);
const canSave = computed(() => canManage.value && !testing.value && !saving.value && !!result.value?.receipt && result.value.text.status === 'passed' && expiry.value > now.value && !!form.value.name.trim());
function invalidate() {
  ++testEpoch;
  testController?.abort();
  testing.value = false;
  result.value = undefined;
  testError.value = '';
}
watch([form, key], invalidate, { deep: true, flush: 'sync' });
watch(show, value => { if (!value) { invalidate(); key.value = ''; } });
function open(profile?: ModelProfile) {
  invalidate();
  existing.value = profile;
  form.value = profile ? { name: profile.name, description: profile.description || '', protocol: profile.protocol, base_url: profile.base_url, model_id: profile.model_id } : fresh();
  key.value = '';
  show.value = true;
}
function config(): ModelConfiguration {
  return { ...form.value, name: form.value.name.trim(), description: form.value.description.trim(), base_url: form.value.base_url.trim(), model_id: form.value.model_id.trim(), ...(key.value.trim() ? { api_key: key.value.trim() } : {}) };
}
async function load() {
  const current = ++epoch;
  loadController?.abort();
  loadController = new AbortController();
  const workspace = activeWorkspaceId.value;
  profiles.value = []; role.value = ''; error.value = ''; loading.value = true;
  if (!workspace) { loading.value = false; return; }
  try {
    const [list, workspaceInfo] = await Promise.all([
      modelProfilesApi.list(workspace, page.value, loadController.signal),
      platformRequest<{ role: string }>(`/workspaces/${encodeURIComponent(workspace)}`, { signal: loadController.signal })
    ]);
    if (current !== epoch) return;
    profiles.value = list.items; total.value = list.total; role.value = workspaceInfo.role;
  } catch (err) {
    if (current === epoch) error.value = err instanceof Error ? err.message : '模型列表加载失败';
  } finally { if (current === epoch) loading.value = false; }
}
watch(activeWorkspaceId, () => { show.value = false; invalidate(); page.value = 1; void load(); }, { immediate: true });
watch(page, () => { void load(); });
async function runTest() {
  if (!canManage.value || testing.value || saving.value) return;
  if ((!existing.value?.has_key || needsExplicitKey.value) && !key.value.trim()) { testError.value = needsExplicitKey.value ? 'API 地址已修改，请重新填写 API Key，避免将旧密钥发往其他服务。' : '请填写 API Key。'; return; }
  if (!form.value.name.trim() || !form.value.base_url.trim() || !form.value.model_id.trim()) { testError.value = '请填写配置名称、API 地址和 Model ID。'; return; }
  invalidate();
  const current = testEpoch;
  const workspace = activeWorkspaceId.value;
  testController = new AbortController(); testing.value = true;
  try {
    const response = await modelProfilesApi.test(workspace, config(), existing.value, testController.signal);
    if (current !== testEpoch || workspace !== activeWorkspaceId.value || !show.value) return;
    result.value = response; now.value = Date.now();
  } catch (err) {
    if (current === testEpoch) testError.value = err instanceof Error && err.name === 'TimeoutError' ? '测试请求超时，请重试。未取得有效结果，不能保存。' : err instanceof Error ? err.message : '模型测试失败';
  } finally { if (current === testEpoch) testing.value = false; }
}
async function save() {
  now.value = Date.now();
  if (!canSave.value || !result.value) return;
  const workspace = activeWorkspaceId.value;
  const current = epoch;
  saving.value = true;
  try {
    await modelProfilesApi.save(workspace, config(), result.value.receipt, existing.value);
    if (workspace !== activeWorkspaceId.value || current !== epoch) return;
    show.value = false; message.success('模型配置已保存');
    await load(); // load has its own error state; never turn a successful save into failure.
  } catch (err) {
    if (workspace === activeWorkspaceId.value && current === epoch) testError.value = err instanceof Error ? err.message : '保存失败';
  } finally { saving.value = false; }
}
async function action(kind: 'status' | 'setDefault' | 'revokeKey', profile: ModelProfile) {
  if (!canManage.value || actionBusy.value) return;
  const workspace = activeWorkspaceId.value; const current = epoch; actionBusy.value = true;
  try {
    await modelProfilesApi[kind](workspace, profile);
    if (workspace !== activeWorkspaceId.value || current !== epoch) return;
    message.success('模型配置已更新'); await load();
  } catch (err) { if (workspace === activeWorkspaceId.value && current === epoch) message.error(err instanceof Error ? err.message : '操作失败'); }
  finally { actionBusy.value = false; }
}
function status(value: string) { return ({ passed: '通过', failed: '未通过', unsupported: '不支持', unknown: '不确定' } as Record<string, string>)[value] || '不确定'; }
function cases(value: ModelTestResult): { name: string; result: ModelCaseResult }[] { return [{ name: '文字测试', result: value.text }, { name: '图片测试', result: value.image }]; }
onUnmounted(() => { ++epoch; invalidate(); loadController?.abort(); clearInterval(clock); });
</script>

<template>
  <section class="tab-content-panel">
    <div class="panel-action-bar">
      <div class="resource-toolbar-copy">
        <p class="panel-hint"><strong>模型服务支持 3 种协议</strong>：文字和图片并行测试。文字通过后可保存，图片不支持或不确定不影响保存。</p>
      </div>
      <NButton v-if="canManage" type="primary" size="small" @click="open()">+ 添加模型配置</NButton>
    </div>

    <div v-if="loading" class="resource-loading">
      <div v-for="n in 3" :key="'m-sk-' + n" class="resource-skeleton-card">
        <NSkeleton text style="width: 55%; height: 18px;" />
        <NSkeleton style="height: 84px; border-radius: 6px;" />
        <NSkeleton text style="width: 80%; height: 22px;" />
      </div>
    </div>
    <NAlert v-else-if="error" type="error" title="无法加载模型">
      <p style="margin: 0 0 8px 0;">{{ error }}</p>
      <NButton size="small" @click="load">重试</NButton>
    </NAlert>
    <template v-else>
      <NAlert v-if="!canManage" type="info">当前仅可查看模型配置，修改需要工作区管理员权限。</NAlert>
      <div v-if="!profiles.length" class="empty-panel-box">
        <NEmpty description="暂无模型接入配置，请点击右上角添加并完成连通性测试">
          <template v-if="canManage" #extra>
            <NButton size="small" type="primary" @click="open()">+ 添加模型配置</NButton>
          </template>
        </NEmpty>
      </div>
      <div v-else class="models-grid">
        <div
          v-for="m in profiles"
          :key="m.id"
          class="model-card"
          :class="{ 'is-default': m.is_default, disabled: !m.enabled }"
        >
          <div class="m-card-header">
            <div class="m-title-group">
              <div class="m-title-row">
                <h4 class="m-name" :title="m.name">{{ m.name }}</h4>
                <span v-if="m.is_default" class="default-micro-badge">默认</span>
              </div>
              <div class="m-title-badges">
                <span class="protocol-micro-pill">
                  {{ m.protocol === 'chat_completions' ? 'Chat Completions' : m.protocol === 'openai_responses' ? 'OpenAI Responses' : 'Anthropic Messages' }}
                </span>
              </div>
            </div>
            <span class="status-pill" :class="m.validated === false ? 'status-pill--warning' : m.enabled ? 'status-pill--success' : 'status-pill--muted'">
              <span class="status-dot" :class="m.validated === false ? 'dot--warning' : m.enabled ? 'dot--success' : 'dot--muted'"></span>
              <span>{{ m.validated === false ? '待重新测试' : m.enabled ? '已启用' : '已停用' }}</span>
            </span>
          </div>

          <p v-if="m.description" class="field-note">{{ m.description }}</p>
          <div class="m-details-table">
            <div class="m-row">
              <span class="m-lbl">Model ID</span>
              <code class="m-code" :title="m.model_id">{{ m.model_id }}</code>
            </div>
            <div class="m-row">
              <span class="m-lbl">API 端点</span>
              <code class="m-code" :title="m.base_url">{{ m.base_url }}</code>
            </div>
            <div class="m-row">
              <span class="m-lbl">凭据状态</span>
              <span class="m-txt m-code">{{ m.has_key ? '已配置' : '未配置' }}</span>
            </div>
            <div class="m-row">
              <span class="m-lbl">图片能力</span>
              <span class="m-txt strong-txt tabular-num">{{ status(m.vision_status) }}</span>
            </div>
          </div>

          <div class="m-card-footer">
            <span class="latency-pill ok tabular-num">
              最近测试 {{ m.tested_at ? new Date(m.tested_at).toLocaleString() : '—' }}
            </span>
            <div v-if="canManage" class="m-btn-group">
              <NButton size="tiny" secondary :disabled="actionBusy" @click="open(m)">编辑配置</NButton>
              <NButton size="tiny" :disabled="actionBusy || (!m.enabled && m.validated === false)" @click="action('status', m)">
                {{ m.enabled ? '停用' : '启用' }}
              </NButton>
              <NButton
                v-if="!m.is_default && m.enabled && m.validated !== false"
                size="tiny"
                type="primary"
                quaternary
                :disabled="actionBusy"
                @click="action('setDefault', m)"
              >
                设为默认
              </NButton>
              <NPopconfirm v-if="m.has_key"
                positive-text="确认吊销"
                negative-text="取消"
                @positive-click="action('revokeKey', m)"
              >
                <template #trigger>
                  <NButton size="tiny" type="error" quaternary :disabled="actionBusy">
                    撤销凭据
                  </NButton>
                </template>
                吊销「{{ m.name }}」的 API Key 后，依赖该模型的 AI 员工将无法继续调用，确认吊销？
              </NPopconfirm>
            </div>
          </div>
        </div>
      </div>
      <div v-if="total > 20" class="resource-pagination">
        <NPagination v-model:page="page" :page-size="20" :item-count="total" />
      </div>
    </template>

    <NDrawer class="library-drawer" v-model:show="show" :width="'min(580px, 95vw)'" :mask-closable="!saving" :close-on-esc="!saving">
      <NDrawerContent :title="existing ? '编辑模型配置' : '添加模型配置'" :closable="!saving">
        <NForm label-placement="top" :disabled="saving">
          <NFormItem label="配置名称"><NInput v-model:value="form.name" :maxlength="100" placeholder="例如：研发模型" /></NFormItem>
          <NFormItem label="说明"><NInput v-model:value="form.description" :maxlength="2000" placeholder="用途说明（选填）" /></NFormItem>
          <NFormItem label="接口协议"><NSelect v-model:value="form.protocol" :options="protocols" /></NFormItem>
          <NFormItem label="Model ID"><NInput v-model:value="form.model_id" :maxlength="256" placeholder="模型标识" /></NFormItem>
          <NFormItem label="API Base URL"><NInput v-model:value="form.base_url" :maxlength="2048" placeholder="https://api.example.com/v1" /></NFormItem>
          <NFormItem label="API Key 凭据">
            <NInput v-model:value="key" type="password" show-password-on="click" autocomplete="new-password" :maxlength="8192"
              :placeholder="existing?.has_key && !needsExplicitKey ? '留空沿用已保存的凭据' : '请输入 API Key'" />
          </NFormItem>
          <NAlert v-if="needsExplicitKey" type="warning">API 地址已修改，请填写新凭据，避免将旧密钥发往其他服务。</NAlert>
          <div class="test-receipt-block">
            <div class="trb-header">
              <strong>连通性测试</strong>
              <NButton type="primary" secondary size="small" :loading="testing" :disabled="saving || testing" @click="runTest">测试连接</NButton>
            </div>
            <p v-if="testing" class="resource-hint-sm">正在并行测试，文字最多等待 20 秒，图片最多等待 40 秒…</p>
            <NAlert v-if="testError" type="error">{{ testError }}</NAlert>
            <template v-if="result">
              <article v-for="item in cases(result)" :key="item.name" class="case-result">
                <div class="case-result-head">
                  <strong>{{ item.name }} · {{ status(item.result.status) }}</strong>
                  <span class="tabular-num">{{ item.result.duration_ms }} ms</span>
                </div>
                <pre v-if="item.result.output">{{ item.result.output }}</pre>
                <p v-else>未返回文本输出。</p>
                <p v-if="item.result.issue" style="color: var(--id-status-warning);">{{ item.result.issue }}</p>
              </article>
              <NAlert v-if="result.text.status !== 'passed'" type="error">文字测试未通过，不能保存。请检查模型配置后重试。</NAlert>
              <NAlert v-else-if="expiry <= now" type="warning">测试结果已过期，请重新测试后保存。</NAlert>
              <NAlert v-else type="success">文字测试通过，可保存配置。修改配置后需要重新测试。</NAlert>
            </template>
            <p v-else-if="!testing" class="resource-hint-sm">使用测试用例检查实际响应，不根据模型名称推测图片能力。</p>
          </div>
        </NForm>
        <template #footer>
          <div class="m-btn-group">
            <NButton :disabled="saving" @click="show = false">取消</NButton>
            <NButton type="primary" :disabled="!canSave" :loading="saving" @click="save">保存配置</NButton>
          </div>
        </template>
      </NDrawerContent>
    </NDrawer>
  </section>
</template>

<style src="../styles/platform-resources.css"></style>

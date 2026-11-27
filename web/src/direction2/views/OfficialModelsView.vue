<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue';
import { NAlert, NButton, NCheckbox, NDrawer, NDrawerContent, NEmpty, NForm, NFormItem, NInput, NPagination, NSelect, NTable } from 'naive-ui';
import { platformRequest, post } from '../api/platform';
import type { DefaultOfficialModelStatus, ModelPrices } from '../api/models';
import type { ModelProtocol } from '../types';

interface OfficialModel {
  id: string; name: string; protocol: ModelProtocol; base_url: string; upstream_model_id: string;
  has_key: boolean; prices: ModelPrices; enabled: boolean; validated: boolean; is_default: boolean; version: number;
}
interface OfficialModelPage {items: OfficialModel[]; total: number}
const emit = defineEmits<{defaultModelStatus: [status: DefaultOfficialModelStatus]}>();
const protocols = [
  {label: 'Chat Completions', value:'chat_completions'}, {label:'Responses', value:'openai_responses'},
  {label:'Anthropic Messages', value:'anthropic_messages'}
];
const blank = () => ({name:'', protocol:'chat_completions' as ModelProtocol, base_url:'', upstream_model_id:'',
  api_key:'', omitKey:false, enabled:true, is_default:false, input:'', output:'', cache_read:'', cache_write:''});
const form = ref(blank()), items = ref<OfficialModel[]>([]), existing = ref<OfficialModel>();
const page = ref(1), total = ref(0), loading = ref(false), busy = ref(false), editing = ref(false);
const error = ref(''), formError = ref(''), probe = ref(''), notice = ref('');
const receipt = ref<{token:string; stamp:string; expires:string}>();
let listRequest: AbortController | undefined;
let editRequest: AbortController | undefined;

const fields = computed(() => ({
  name: form.value.name, protocol: form.value.protocol, base_url: form.value.base_url, upstream_model_id: form.value.upstream_model_id,
  ...(form.value.omitKey ? {api_key: ''} : form.value.api_key ? {api_key: form.value.api_key} : {}),
  ...(existing.value ? {version: existing.value.version} : {})
}));
const stamp = computed(() => JSON.stringify(fields.value));

function reportDefaultModel(model?: OfficialModel) {
  emit('defaultModelStatus', model
    ? {state:model.enabled && model.validated ? 'ready' : 'not-ready', name:model.name}
    : {state:'missing'});
}

async function refreshDefaultModel(result: OfficialModelPage, signal: AbortSignal) {
  const onPage = result.items.find(model => model.is_default);
  if (onPage || result.total <= result.items.length) {
    reportDefaultModel(onPage);
    return;
  }
  // The existing admin list is sorted newest first, not by default status.
  // Search other pages so we don't report "missing" for an older default.
  try {
    for (let p = 1; ; p++) {
      const batch = await platformRequest<OfficialModelPage>(`/platform-models?page=${p}&page_size=100`, {signal});
      if (signal.aborted) return;
      const found = batch.items.find(model => model.is_default);
      if (found || batch.items.length === 0 || p * 100 >= batch.total) {
        reportDefaultModel(found);
        return;
      }
    }
  } catch {
    if (!signal.aborted) emit('defaultModelStatus', {state:'unavailable'});
  }
}

async function load() {
  listRequest?.abort();
  const request = listRequest = new AbortController();
  loading.value = true;
  error.value = '';
  emit('defaultModelStatus', {state:'loading'});
  try {
    const result = await platformRequest<OfficialModelPage>(`/platform-models?page=${page.value}`, {signal:request.signal});
    if (request.signal.aborted) return;
    items.value = result.items;
    total.value = result.total;
    void refreshDefaultModel(result, request.signal);
  } catch (e) {
    if (!request.signal.aborted) {
      error.value = e instanceof Error ? e.message : '读取模型失败';
      emit('defaultModelStatus', {state:'unavailable'});
    }
  } finally { if (!request.signal.aborted) loading.value = false; }
}
function open(model?: OfficialModel) {
  editRequest?.abort();
  existing.value = model;
  form.value = model ? {...blank(), name:model.name, protocol:model.protocol, base_url:model.base_url,
    upstream_model_id:model.upstream_model_id, enabled:model.enabled, is_default:model.is_default,
    input:model.prices.input, output:model.prices.output,
    cache_read:model.prices.cache_read ?? '', cache_write:model.prices.cache_write ?? ''} : blank();
  receipt.value = undefined;
  formError.value = probe.value = '';
  editing.value = true;
}
async function test() {
  const request = editRequest = new AbortController();
  const tested = stamp.value;
  busy.value = true;
  formError.value = probe.value = '';
  receipt.value = undefined;
  try {
    const result = await platformRequest<{connected:boolean; usage_available:boolean; receipt?:string; expires_at?:string}>(
      '/platform-models/test', {...post({...fields.value, ...(existing.value ? {id:existing.value.id} : {})}), signal:request.signal});
    if (request.signal.aborted || tested !== stamp.value) return;
    if (result.receipt && result.expires_at) {
      receipt.value = {token:result.receipt, stamp:tested, expires:result.expires_at};
      probe.value = '连接正常，已收到流式计费用量。';
    } else formError.value = result.connected ? '连接成功，但未取得可计费的流式用量。' : '连接测试失败，请检查地址、模型和凭据。';
  } catch (e) {
    if (!request.signal.aborted) formError.value = e instanceof Error ? e.message : '测试失败';
  } finally { if (!request.signal.aborted) busy.value = false; }
}
async function save() {
  const request = editRequest = new AbortController();
  busy.value = true;
  formError.value = '';
  try {
    const f = form.value;
    const tested = receipt.value;
    const body = {...fields.value, enabled:f.enabled, is_default:f.enabled && f.is_default,
      prices:{input:f.input, output:f.output, cache_read:f.cache_read || null, cache_write:f.cache_write || null},
      ...(tested?.stamp === stamp.value && Date.parse(tested.expires) > Date.now() ? {test_receipt:tested.token} : {})};
    await platformRequest(existing.value ? `/platform-models/${existing.value.id}` : '/platform-models',
      {method:existing.value ? 'PATCH' : 'POST', body:JSON.stringify(body), signal:request.signal});
    if (request.signal.aborted) return;
    editing.value = false;
    form.value = blank();
    notice.value = '官方模型已保存';
    await load();
  } catch (e) {
    if (!request.signal.aborted) formError.value = e instanceof Error ? e.message : '保存失败';
  } finally { if (!request.signal.aborted) busy.value = false; }
}
onMounted(() => void load());
onUnmounted(() => {listRequest?.abort(); editRequest?.abort();});
</script>

<template>
  <section class="official">
    <header class="panel-top-actions">
      <div class="panel-top-copy">
        <h2>官方模型</h2>
        <p class="sub-desc">管理平台统一托管的官方模型与 Token 计费单价，工作区成员可使用积分直接调用。</p>
      </div>
      <NButton type="primary" size="small" @click="open()">添加官方模型</NButton>
    </header>

    <NAlert v-if="error" type="error">{{error}} <NButton text @click="load">重试</NButton></NAlert>
    <NAlert v-if="notice" type="success">{{notice}}</NAlert>

    <div v-if="loading" class="settings-card official-state-card">
      <p class="sub-desc">正在读取模型…</p>
    </div>
    <div v-else-if="!items.length && !error" class="settings-card official-state-card">
      <NEmpty description="尚未配置官方模型。">
        <template #extra>
          <NButton size="small" type="primary" @click="open()">添加官方模型</NButton>
        </template>
      </NEmpty>
    </div>
    <div v-else class="members-table-card table-wrap">
      <NTable :bordered="false" :single-line="false">
        <thead>
          <tr>
            <th>名称</th>
            <th>协议</th>
            <th>输入 / 输出价格</th>
            <th>状态</th>
            <th class="col-actions">操作</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="m in items" :key="m.id" :class="{ 'row-disabled': !m.enabled }">
            <td>
              <div class="model-name-cell">
                <div class="model-title-row">
                  <strong>{{m.name}}</strong>
                  <span v-if="m.is_default" class="default-micro-badge">默认</span>
                </div>
                <code class="mono-text model-sub-code" :title="m.upstream_model_id">{{m.upstream_model_id}}</code>
              </div>
            </td>
            <td>
              <div class="model-name-cell">
                <span class="protocol-micro-pill">{{protocols.find(p=>p.value===m.protocol)?.label}}</span>
                <code class="mono-text model-sub-code" :title="m.base_url">{{m.base_url}}</code>
              </div>
            </td>
            <td>
              <div class="model-name-cell">
                <span class="tabular-num price-main">{{m.prices.input}} / {{m.prices.output}} 积分 / 百万 Token</span>
                <span v-if="m.prices.cache_read || m.prices.cache_write" class="muted-text tabular-num price-sub">
                  缓存读 {{m.prices.cache_read ?? '同输入'}} · 写 {{m.prices.cache_write ?? '同输入'}}
                </span>
              </div>
            </td>
            <td>
              <div class="model-status-cell">
                <span class="status-pill" :class="m.enabled ? 'status-pill--success' : 'status-pill--muted'">
                  <span class="status-dot" :class="m.enabled ? 'dot--success' : 'dot--muted'"></span>
                  <span>{{m.enabled ? (m.is_default ? '默认 · 已启用' : '已启用') : '已停用'}}</span>
                </span>
                <span class="neutral-pill">{{ m.has_key ? '已配凭据' : '免认证' }}</span>
              </div>
            </td>
            <td class="col-actions">
              <div class="table-action-btns">
                <NButton size="small" secondary @click="open(m)">编辑</NButton>
              </div>
            </td>
          </tr>
        </tbody>
      </NTable>
    </div>

    <div v-if="total > 20" class="official-pagination">
      <NPagination :page="page" :page-size="20" :item-count="total" @update:page="p=>{page=p;load()}"/>
    </div>

    <NDrawer v-model:show="editing" :width="'min(580px, 96vw)'" :mask-closable="!busy" :close-on-esc="!busy">
      <NDrawerContent :title="existing ? '编辑官方模型' : '添加官方模型'" :closable="!busy">
        <div class="drawer-stack">
          <NAlert v-if="formError" type="error">{{formError}}</NAlert>
          <NAlert v-if="probe && receipt?.stamp === stamp" type="success">{{probe}}</NAlert>
          <NForm :disabled="busy" label-placement="top">
            <div class="drawer-section">
              <h3 class="drawer-section-title">连接与协议</h3>
              <NFormItem label="模型名称"><NInput v-model:value="form.name" placeholder="例如：DeepSeek-V3 官方托管" /></NFormItem>
              <div class="prices">
                <NFormItem label="接口协议"><NSelect v-model:value="form.protocol" :options="protocols"/></NFormItem>
                <NFormItem label="上游模型 ID"><NInput v-model:value="form.upstream_model_id" placeholder="例如：deepseek-chat" /></NFormItem>
              </div>
              <NFormItem label="服务地址"><NInput v-model:value="form.base_url" placeholder="https://provider.example/v1"/></NFormItem>
            </div>

            <div class="drawer-section">
              <h3 class="drawer-section-title">认证凭据</h3>
              <NFormItem label="供应商 API Key">
                <NInput v-model:value="form.api_key" type="password" show-password-on="click" autocomplete="new-password"
                  :disabled="form.omitKey" :placeholder="existing?.has_key ? '留空保留现有凭据' : '输入供应商凭据'"/>
              </NFormItem>
              <div class="setting-toggle-box">
                <NCheckbox v-model:checked="form.omitKey">明确使用无需认证的服务</NCheckbox>
              </div>
            </div>

            <div class="drawer-section">
              <h3 class="drawer-section-title">计费单价（积分 / 百万 Token）</h3>
              <div class="prices">
                <NFormItem label="输入价格"><NInput v-model:value="form.input" placeholder="积分 / 百万 Token"/></NFormItem>
                <NFormItem label="输出价格"><NInput v-model:value="form.output" placeholder="积分 / 百万 Token"/></NFormItem>
                <NFormItem label="缓存读取价格"><NInput v-model:value="form.cache_read" placeholder="沿用输入价格"/></NFormItem>
                <NFormItem label="缓存写入价格"><NInput v-model:value="form.cache_write" placeholder="沿用输入价格"/></NFormItem>
              </div>
            </div>

            <div class="drawer-section">
              <h3 class="drawer-section-title">启用与默认策略</h3>
              <div class="setting-toggle-box">
                <div class="setting-toggle-row">
                  <NCheckbox v-model:checked="form.enabled">启用</NCheckbox>
                  <NCheckbox v-model:checked="form.is_default" :disabled="!form.enabled">设为默认</NCheckbox>
                </div>
                <p class="setting-toggle-help">保存前需先点击「测试连接」并取得有效的流式用量回执。</p>
              </div>
            </div>
          </NForm>
        </div>
        <template #footer>
          <div class="modal-footer-actions">
            <NButton :disabled="busy" @click="editing = false">取消</NButton>
            <NButton :disabled="busy" secondary @click="test">测试连接</NButton>
            <NButton type="primary" :loading="busy" @click="save">保存</NButton>
          </div>
        </template>
      </NDrawerContent>
    </NDrawer>
  </section>
</template>

<style scoped>
.official {
  display: flex;
  flex-direction: column;
  gap: 16px;
}
.official-state-card {
  padding: 36px 20px;
  align-items: center;
  justify-content: center;
}
.table-wrap {
  overflow-x: auto;
}
.table-wrap :deep(.n-table th) {
  background: var(--id-bg-canvas);
  font-size: 12px;
  font-weight: 500;
  color: var(--id-text-muted);
}
.row-disabled td {
  opacity: 0.6;
}
.model-name-cell {
  display: flex;
  flex-direction: column;
  gap: 4px;
  min-width: 0;
}
.model-title-row {
  display: flex;
  align-items: center;
  gap: 6px;
  flex-wrap: wrap;
}
.model-sub-code {
  display: inline-block;
  max-width: 260px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.model-status-cell {
  display: flex;
  align-items: center;
  gap: 6px;
  flex-wrap: wrap;
}
.price-main {
  font-size: 13px;
  color: var(--id-text-primary);
}
.price-sub {
  font-size: 11px;
}
.official-pagination {
  display: flex;
  justify-content: flex-end;
}
.drawer-stack {
  display: flex;
  flex-direction: column;
  gap: 14px;
}
.drawer-section {
  background: var(--id-bg-subtle);
  border: 1px solid var(--id-border-subtle);
  border-radius: var(--id-radius-md);
  padding: 14px 16px;
  margin-bottom: 14px;
}
.drawer-section:last-child {
  margin-bottom: 0;
}
.drawer-section-title {
  margin: 0 0 12px 0;
  font-size: 13px;
  font-weight: 600;
  color: var(--id-text-primary);
}
.prices {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 0 12px;
}
@media (max-width: 760px) {
  .prices {
    grid-template-columns: 1fr;
  }
}
</style>

<style scoped src="../components/workspace-settings-shared.css"></style>

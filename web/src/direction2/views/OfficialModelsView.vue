<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue';
import { NAlert, NButton, NCheckbox, NDrawer, NDrawerContent, NForm, NFormItem, NInput, NPagination, NSelect, NTable } from 'naive-ui';
import { platformRequest, post } from '../api/platform';
import type { ModelPrices } from '../api/models';
import type { ModelProtocol } from '../types';

interface OfficialModel {
  id: string; name: string; protocol: ModelProtocol; base_url: string; upstream_model_id: string;
  has_key: boolean; prices: ModelPrices; enabled: boolean; validated: boolean; is_default: boolean; version: number;
}
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

async function load() {
  listRequest?.abort();
  const request = listRequest = new AbortController();
  loading.value = true;
  error.value = '';
  try {
    const result = await platformRequest<{items: OfficialModel[]; total:number}>(`/platform-models?page=${page.value}`, {signal:request.signal});
    if (request.signal.aborted) return;
    items.value = result.items;
    total.value = result.total;
  } catch (e) {
    if (!request.signal.aborted) error.value = e instanceof Error ? e.message : '读取模型失败';
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
    <header><h2>官方模型</h2><NButton type="primary" @click="open()">添加官方模型</NButton></header>
    <NAlert v-if="error" type="error">{{error}} <NButton text @click="load">重试</NButton></NAlert>
    <NAlert v-if="notice" type="success">{{notice}}</NAlert>
    <p v-if="loading">正在读取模型…</p>
    <p v-else-if="!items.length && !error">尚未配置官方模型。</p>
    <div v-else class="table-wrap"><NTable>
      <thead><tr><th>名称</th><th>协议</th><th>输入 / 输出价格</th><th>状态</th><th>操作</th></tr></thead>
      <tbody><tr v-for="m in items" :key="m.id">
        <td>{{m.name}}</td><td>{{protocols.find(p=>p.value===m.protocol)?.label}}</td>
        <td>{{m.prices.input}} / {{m.prices.output}} 积分 / 百万 Token</td>
        <td>{{m.enabled ? (m.is_default ? '默认 · 已启用' : '已启用') : '已停用'}}</td>
        <td><NButton size="small" @click="open(m)">编辑</NButton></td>
      </tr></tbody>
    </NTable></div>
    <NPagination v-if="total>20" :page="page" :page-size="20" :item-count="total" @update:page="p=>{page=p;load()}"/>
    <NDrawer v-model:show="editing" :width="'min(560px, 96vw)'" :mask-closable="!busy" :close-on-esc="!busy">
      <NDrawerContent :title="existing ? '编辑官方模型' : '添加官方模型'" :closable="!busy">
        <NAlert v-if="formError" type="error">{{formError}}</NAlert>
        <NAlert v-if="probe && receipt?.stamp === stamp" type="success">{{probe}}</NAlert>
        <NForm :disabled="busy" label-placement="top">
          <NFormItem label="模型名称"><NInput v-model:value="form.name" /></NFormItem>
          <NFormItem label="接口协议"><NSelect v-model:value="form.protocol" :options="protocols"/></NFormItem>
          <NFormItem label="服务地址"><NInput v-model:value="form.base_url" placeholder="https://provider.example/v1"/></NFormItem>
          <NFormItem label="上游模型 ID"><NInput v-model:value="form.upstream_model_id"/></NFormItem>
          <NFormItem label="供应商 API Key"><NInput v-model:value="form.api_key" type="password" autocomplete="new-password"
            :disabled="form.omitKey" :placeholder="existing?.has_key ? '留空保留现有凭据' : '输入供应商凭据'"/></NFormItem>
          <NCheckbox v-model:checked="form.omitKey">明确使用无需认证的服务</NCheckbox>
          <div class="prices">
            <NFormItem label="输入价格"><NInput v-model:value="form.input" placeholder="积分 / 百万 Token"/></NFormItem>
            <NFormItem label="输出价格"><NInput v-model:value="form.output" placeholder="积分 / 百万 Token"/></NFormItem>
            <NFormItem label="缓存读取价格"><NInput v-model:value="form.cache_read" placeholder="沿用输入价格"/></NFormItem>
            <NFormItem label="缓存写入价格"><NInput v-model:value="form.cache_write" placeholder="沿用输入价格"/></NFormItem>
          </div>
          <NCheckbox v-model:checked="form.enabled">启用</NCheckbox>
          <NCheckbox v-model:checked="form.is_default" :disabled="!form.enabled">设为默认</NCheckbox>
        </NForm>
        <template #footer><NButton :disabled="busy" @click="test">测试连接</NButton> <NButton type="primary" :loading="busy" @click="save">保存</NButton></template>
      </NDrawerContent>
    </NDrawer>
  </section>
</template>

<style scoped>
.official header{display:flex;align-items:center;justify-content:space-between;gap:12px;margin-bottom:16px}
.table-wrap{overflow-x:auto}.prices{display:grid;grid-template-columns:repeat(2,minmax(0,1fr));gap:0 12px;margin-top:12px}
</style>

<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue';
import { NAlert, NButton, NCheckbox, NForm, NFormItem, NInput, NInputNumber, NTag } from 'naive-ui';
import { platformRequest, post } from '../api/platform';

interface Config {
  name: string;
  release_version: number;
  version: number;
  credential_configured: boolean;
  enabled: boolean;
  provider: string;
  base_url: string;
  upstream_model_id: string;
  credits_per_image: string;
  workspace_daily_image_limit: number;
  max_concurrent_generations: number;
  timeout_seconds: number;
}
interface Usage {
  generation_id: string;
  agent_name: string;
  status: string;
  billing_status: string;
  started_at: string;
  credits_per_image: string;
  charged_credits: string | null;
}
const base = '/platform/default-plugins/image-generation';
const online = ref<Config>(),
  draft = ref<Config>(),
  key = ref(''),
  receipt = ref(''),
  preview = ref(''),
  busy = ref(false),
  error = ref(''),
  notice = ref('');
const items = ref<Usage[]>([]),
  cursor = ref(''),
  reason = ref(''),
  selected = ref(''),
  usageError = ref(''),
  usageBusy = ref(false);
const dirty = computed(
  () => !!key.value || JSON.stringify(draft.value) !== JSON.stringify(online.value),
);
const status = computed(() =>
  !online.value?.credential_configured ? '待配置' : online.value.enabled ? '已启用' : '已停用',
);
const labels: Record<string, string> = {
  admitted: '已受理',
  dispatched: '生成中',
  ready: '已保存',
  failed: '失败',
  pending_review: '待核对',
  settled: '已扣分',
  pending: '待结算',
  no_charge: '零收费',
};
function discard() {
  draft.value = online.value && { ...online.value };
  key.value = receipt.value = preview.value = error.value = notice.value = '';
}
async function load() {
  try {
    online.value = await platformRequest<Config>(base);
    discard();
  } catch (e) {
    error.value = String(e);
  }
}
watch(
  () => [key.value, draft.value?.base_url, draft.value?.upstream_model_id],
  () => {
    receipt.value = preview.value = '';
  },
);
function changes() {
  const v = draft.value,
    old = online.value;
  if (!v || !old) throw new Error('请刷新当前配置');
  const fields = [
    'enabled',
    'provider',
    'base_url',
    'upstream_model_id',
    'credits_per_image',
    'workspace_daily_image_limit',
    'max_concurrent_generations',
    'timeout_seconds',
  ] as const;
  return Object.fromEntries([
    ['version', old.version],
    ...fields.filter((k) => v[k] !== old[k]).map((k) => [k, v[k]]),
    ...(key.value ? [['api_key', key.value]] : []),
  ]);
}
async function test() {
  if (busy.value) return;
  busy.value = true;
  error.value = notice.value = receipt.value = preview.value = '';
  try {
    const result = await platformRequest<{
      receipt: string;
      preview_png: string;
      expires_at: string;
    }>(base + '/test', { ...post(changes()), signal: AbortSignal.timeout(315000) });
    receipt.value = result.receipt;
    preview.value = 'data:image/png;base64,' + result.preview_png;
    notice.value = '测试成功，回执有效至 ' + new Date(result.expires_at).toLocaleTimeString();
  } catch (e) {
    error.value = e instanceof Error ? e.message : '测试失败';
  } finally {
    busy.value = false;
  }
}
async function save() {
  if (busy.value || !draft.value) return false;
  busy.value = true;
  error.value = notice.value = '';
  try {
    online.value = await platformRequest<Config>(base, {
      method: 'PATCH',
      body: JSON.stringify({ ...changes(), receipt: receipt.value }),
    });
    discard();
    notice.value = '已保存平台配置';
    return true;
  } catch (e) {
    error.value = e instanceof Error ? e.message : '保存失败';
    return false;
  } finally {
    busy.value = false;
  }
}
async function usage(more = false) {
  if (usageBusy.value) return;
  usageBusy.value = true;
  usageError.value = '';
  try {
    const q = new URLSearchParams({ scope: 'all', limit: '20' });
    if (more && cursor.value) q.set('cursor', cursor.value);
    const v = await platformRequest<{ items: Usage[]; next_cursor: string }>(
      '/image-generations?' + q,
    );
    items.value = more ? [...items.value, ...v.items] : v.items;
    cursor.value = v.next_cursor;
  } catch (e) {
    usageError.value = String(e);
  } finally {
    usageBusy.value = false;
  }
}
async function resolve(action: 'reconcile' | 'close_no_charge') {
  if (!selected.value || !reason.value.trim() || busy.value) return;
  busy.value = true;
  usageError.value = '';
  try {
    await platformRequest(
      '/image-generations/' + selected.value + '/resolve',
      post({ action, reason: reason.value.trim() }),
    );
    selected.value = reason.value = '';
    await usage();
  } catch (e) {
    usageError.value = String(e);
  } finally {
    busy.value = false;
  }
}
onMounted(() => {
  void load();
  void usage();
});
defineExpose({ dirty, save, discard });
</script>
<template>
  <section class="image-settings">
    <header>
      <h2>官方文生图</h2>
      <NTag v-if="online">发行版本 {{ online.release_version }} · {{ status }}</NTag>
    </header>
    <NAlert v-if="error" type="error"
      >{{ error }} <NButton text :disabled="busy" @click="load">重新读取</NButton></NAlert
    >
    <NForm v-if="draft" :disabled="busy" @submit.prevent="save">
      <NFormItem label="供应商协议"><span>OpenAI Images 兼容接口</span></NFormItem>
      <NFormItem label="服务地址"><NInput v-model:value="draft.base_url" /></NFormItem>
      <NFormItem label="模型 ID"
        ><NInput v-model:value="draft.upstream_model_id" :input-props="{ id: 'image-model-id' }"
      /></NFormItem>
      <NFormItem label="API Key"
        ><NInput
          v-model:value="key"
          :input-props="{ id: 'image-api-key' }"
          type="password"
          :placeholder="
            online?.credential_configured ? '已配置，留空保留当前凭据' : '输入供应商 API Key'
          "
      /></NFormItem>
      <NFormItem label="每张图的积分"
        ><NInput v-model:value="draft.credits_per_image" :input-props="{ id: 'image-unit-price' }"
      /></NFormItem>
      <NFormItem label="每工作区每日上限（UTC 00:00 重置）"
        ><NInputNumber
          :value="draft.workspace_daily_image_limit"
          :min="1"
          @update:value="
            (v) => {
              if (v) draft!.workspace_daily_image_limit = v;
            }
          "
      /></NFormItem>
      <NFormItem label="全平台并发上限"
        ><NInputNumber
          :value="draft.max_concurrent_generations"
          :min="1"
          @update:value="
            (v) => {
              if (v) draft!.max_concurrent_generations = v;
            }
          "
      /></NFormItem>
      <NFormItem label="供应商超时（秒）"
        ><NInputNumber
          :value="draft.timeout_seconds"
          :min="1"
          :max="300"
          @update:value="
            (v) => {
              if (v) draft!.timeout_seconds = v;
            }
          "
      /></NFormItem>
      <NCheckbox v-model:checked="draft.enabled">为全体数字员工启用</NCheckbox>
      <p>测试会实际生成一张样图，供应商费用由平台承担。</p>
      <div class="actions">
        <NButton :disabled="busy" @click="test">测试出图</NButton
        ><NButton type="primary" attr-type="submit" :disabled="busy || !dirty">保存配置</NButton>
      </div>
    </NForm>
    <p v-if="notice" role="status">{{ notice }}</p>
    <img v-if="preview" :src="preview" alt="供应商测试样图" width="160" />
    <header>
      <h3>全平台出图记录</h3>
      <NButton :disabled="usageBusy" @click="usage()">刷新</NButton>
    </header>
    <NAlert v-if="usageError" type="error">{{ usageError }}</NAlert>
    <p v-if="!items.length && !usageBusy">暂无出图记录</p>
    <article v-for="u in items" :key="u.generation_id">
      <strong>{{ u.agent_name }}</strong> · {{ new Date(u.started_at).toLocaleString() }} ·
      {{ labels[u.status] }} · {{ labels[u.billing_status] }} · 单价 {{ u.credits_per_image }} ·
      实扣 {{ u.charged_credits ?? '待确认' }}
      <div>
        <code>{{ u.generation_id }}</code>
        <NButton
          v-if="u.status === 'pending_review'"
          text
          @click="
            selected = u.generation_id;
            reason = '';
          "
          >处理核对</NButton
        >
      </div>
      <div v-if="selected === u.generation_id" class="actions">
        <NInput v-model:value="reason" placeholder="填写核对依据" /><NButton
          :disabled="busy || !reason.trim()"
          @click="resolve('reconcile')"
          >核对原图</NButton
        ><NButton :disabled="busy || !reason.trim()" @click="resolve('close_no_charge')"
          >确认无原图，零收费关闭</NButton
        >
      </div>
    </article>
    <NButton v-if="cursor" :disabled="usageBusy" @click="usage(true)">加载更多</NButton>
  </section>
</template>
<style scoped>
.image-settings {
  max-width: 52rem;
}
.image-settings header,
.actions {
  display: flex;
  align-items: center;
  gap: 12px;
  flex-wrap: wrap;
}
.image-settings form {
  max-width: 38rem;
}
.image-settings article {
  padding: 12px 0;
  border-bottom: 1px solid var(--id-border-subtle);
}
.image-settings code {
  overflow-wrap: anywhere;
}
</style>

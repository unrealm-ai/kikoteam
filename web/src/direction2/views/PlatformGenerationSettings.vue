<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue';
import { NButton, NCheckbox, NSwitch } from 'naive-ui';
import { platformRequest, post } from '../api/platform';
import { D2ApiError } from '../api/client';
import GenerationRecords from './GenerationRecords.vue';

const props = defineProps<{ kind: 'image' | 'video' }>();
const video = props.kind === 'video';
const base = '/platform/default-plugins/' + props.kind + '-generation';
const priceField = video ? 'credits_per_second' : 'credits_per_image';
const limitField = video ? 'workspace_daily_seconds' : 'workspace_daily_image_limit';
interface Config {
  version: number;
  enabled: boolean;
  credential_configured: boolean;
  base_url: string;
  upstream_model_id: string;
  max_concurrent_generations: number;
  credits_per_image?: string;
  credits_per_second?: string;
  workspace_daily_image_limit?: number;
  workspace_daily_seconds?: number;
  timeout_seconds?: number;
  provider?: string;
  release_version?: number;
}
interface TestResult {
  receipt: string;
  expires_at: string;
  preview_mp4?: string;
  preview_png?: string;
}
const live = ref<Config>(),
  draft = ref<Config>(),
  key = ref('');
const form = ref<HTMLFormElement>(),
  action = ref<'' | 'loading' | 'testing' | 'saving'>('');
const error = ref(''),
  notice = ref(''),
  preview = ref(''),
  receipt = ref(''),
  expires = ref(0),
  playable = ref(false);
const lifetime = new AbortController();
let expiryTimer: ReturnType<typeof setTimeout> | undefined;
let revision = 0;
const dirty = computed(() => !!key.value || JSON.stringify(draft.value) !== JSON.stringify(live.value));
const needsTest = computed(
  () =>
    !!draft.value &&
    (!live.value?.credential_configured ||
      !!key.value ||
      draft.value.base_url !== live.value.base_url ||
      draft.value.upstream_model_id !== live.value.upstream_model_id ||
      draft.value.provider !== live.value.provider),
);
const tested = computed(() => !!receipt.value && playable.value);
const savedStatus = computed(() =>
  !live.value?.credential_configured ? '待配置' : live.value.enabled ? '已启用' : '已停用',
);
const modelOptions = computed(() => [
  ...new Set([live.value?.upstream_model_id, video ? 'MiniMax-H3' : ''].filter((v): v is string => !!v)),
]);
const testLabel = computed(() =>
  action.value === 'testing'
    ? '生成测试中…'
    : preview.value
      ? '重新测试'
      : video
        ? '测试视频生成'
        : '测试出图',
);
function invalidate() {
  revision++;
  clearTimeout(expiryTimer);
  preview.value = receipt.value = notice.value = '';
  playable.value = false;
  expires.value = 0;
}
watch(
  () => [key.value, draft.value?.base_url, draft.value?.upstream_model_id, draft.value?.provider],
  invalidate,
  { flush: 'sync' },
);
function discard() {
  invalidate();
  draft.value = live.value && { ...live.value };
  key.value = error.value = notice.value = '';
}
function adopt(config: Config) {
  const price = config[priceField];
  live.value = {
    ...config,
    ...(price ? { [priceField]: price.replace(/(\.\d*?[1-9])0+$|\.0+$/, '$1') } : {}),
  };
  discard();
}
async function load() {
  if (action.value) return;
  action.value = 'loading';
  error.value = '';
  try {
    const result = await platformRequest<Config>(base, { signal: signal(20000) });
    if (!lifetime.signal.aborted) adopt(result);
  } catch (e) {
    if (!lifetime.signal.aborted) error.value = String(e);
  } finally {
    action.value = '';
  }
}
function signal(timeout: number) {
  return AbortSignal.any([lifetime.signal, AbortSignal.timeout(timeout)]);
}
function changes() {
  const d = draft.value,
    old = live.value;
  if (!d || !old) throw new Error('请先读取配置');
  const fields: (keyof Config)[] = [
    'enabled',
    'base_url',
    'upstream_model_id',
    priceField,
    limitField,
    'max_concurrent_generations',
  ];
  if (!video) fields.push('provider', 'timeout_seconds');
  return {
    version: old.version,
    ...Object.fromEntries(fields.filter((k) => d[k] !== old[k]).map((k) => [k, d[k]])),
    ...(key.value ? { api_key: key.value } : {}),
  };
}
async function test() {
  if (action.value || !form.value?.reportValidity()) return;
  invalidate();
  error.value = '';
  action.value = 'testing';
  const candidate = revision;
  try {
    const result = await platformRequest<TestResult>(base + '/test', {
      ...post(changes()),
      signal: signal(315000),
    });
    if (candidate !== revision || lifetime.signal.aborted) return;
    const bytes = video ? result.preview_mp4 : result.preview_png;
    const end = Date.parse(result.expires_at);
    if (!bytes || !result.receipt || !Number.isFinite(end) || end <= Date.now())
      throw new Error('测试未返回有效预览或回执，请重新测试');
    preview.value = 'data:' + (video ? 'video/mp4' : 'image/png') + ';base64,' + bytes;
    receipt.value = result.receipt;
    expires.value = end;
    expiryTimer = setTimeout(() => {
      receipt.value = '';
    }, end - Date.now());
  } catch (e) {
    if (candidate === revision && !lifetime.signal.aborted)
      error.value = e instanceof Error ? e.message : '生成测试失败';
  } finally {
    action.value = '';
  }
}
function previewError() {
  receipt.value = '';
  playable.value = false;
  error.value = video
    ? '浏览器无法播放测试短片，请检查模型输出后重新测试。'
    : '浏览器无法显示测试样图，请重新测试。';
}
async function save() {
  if (action.value || !form.value?.reportValidity()) return false;
  if (!dirty.value) return true;
  if (needsTest.value && (!tested.value || expires.value <= Date.now())) {
    error.value = '请先测试当前模型连接并加载预览，再保存配置。';
    return false;
  }
  error.value = notice.value = '';
  action.value = 'saving';
  try {
    const cfg = await platformRequest<Config>(base, {
      method: 'PATCH',
      body: JSON.stringify({
        ...changes(),
        ...(receipt.value ? { receipt: receipt.value } : {}),
      }),
      signal: signal(20000),
    });
    if (lifetime.signal.aborted) return false;
    adopt(cfg);
    notice.value = '配置已保存';
    return true;
  } catch (e) {
    if (
      e instanceof D2ApiError &&
      ['image_test_required', 'video_test_required', 'version_conflict'].includes(e.code)
    )
      receipt.value = '';
    if (!lifetime.signal.aborted) error.value = e instanceof Error ? e.message : '保存失败';
    return false;
  } finally {
    action.value = '';
  }
}
onMounted(load);
onUnmounted(() => {
  lifetime.abort();
  clearTimeout(expiryTimer);
});
defineExpose({ dirty, save, discard });
</script>

<template>
  <section :class="['generation-settings', kind + '-settings']">
    <header class="heading">
      <h2>
        <svg
          aria-hidden="true"
          viewBox="0 0 24 24"
          fill="none"
          stroke="currentColor"
          stroke-width="1.7"
          stroke-linecap="round"
          stroke-linejoin="round"
        >
          <template v-if="video">
            <rect x="2" y="5" width="14" height="14" rx="3" />
            <path d="m16 10 6-4v12l-6-4z" />
          </template>
          <template v-else>
            <rect x="3" y="3" width="18" height="18" rx="3" />
            <circle cx="9" cy="9" r="1.4" />
            <path d="m4 18 5-5 3 3 3-4 5 6" />
          </template></svg
        >{{ video ? '官方文生视频' : '官方文生图' }}
      </h2>
      <span v-if="live" class="status" :class="{ pending: dirty, enabled: !dirty && live.enabled }">{{
        dirty ? '待保存' : savedStatus
      }}</span>
    </header>
    <div v-if="error" class="error" role="alert">
      {{ error }}
      <button type="button" :disabled="!!action" @click="load">
        {{ dirty ? '放弃草稿并重新读取' : '重新读取配置' }}
      </button>
    </div>
    <p v-if="action === 'loading'" role="status">正在读取配置…</p>
    <form v-if="draft" ref="form" @submit.prevent="save">
      <div class="layout">
        <fieldset class="panel connection" :disabled="!!action" aria-label="生成配置">
          <h3>模型连接</h3>
          <label
            >服务{{ video ? '根地址' : '地址'
            }}<input v-model.trim="draft.base_url" type="url" required :id="kind + '-service-url'"
          /></label>
          <label
            >API Key<input
              v-model="key"
              :id="kind + '-api-key'"
              type="password"
              autocomplete="new-password"
              :required="!live?.credential_configured || draft.base_url !== live.base_url"
              :placeholder="
                live?.credential_configured && draft.base_url === live.base_url
                  ? '已配置，留空保留当前凭据'
                  : '输入当前服务的 API Key'
              "
          /></label>
          <label
            >模型 ID<input
              v-model="draft.upstream_model_id"
              :id="kind + '-model-id'"
              :list="kind + '-model-options'"
              maxlength="200"
              required
              spellcheck="false"
              autocomplete="off"
              placeholder="输入模型 ID"
          /></label>
          <datalist :id="kind + '-model-options'">
            <option v-for="model in modelOptions" :key="model" :value="model" />
          </datalist>
          <p class="hint">
            {{ video ? '可选择默认模型，也可输入兼容的视频模型 ID。' : '输入图片服务支持的模型 ID。' }}
          </p>
          <div v-if="!video" class="protocol">
            <NCheckbox
              :checked="draft.provider === 'openai_images_b64_json'"
              :disabled="!!action"
              @update:checked="(v) => (draft!.provider = v ? 'openai_images_b64_json' : 'openai_images')"
              >要求网关直接返回图片（Base64）</NCheckbox
            >
          </div>
          <div class="limits">
            <h3>计费与限额</h3>
            <div class="numbers">
              <label
                >{{ video ? '每秒积分' : '每张积分'
                }}<span class="with-unit"
                  ><input
                    v-model="draft[priceField]"
                    :id="kind + '-unit-price'"
                    inputmode="decimal"
                    required
                  /><span>{{ video ? '积分/秒' : '积分/张' }}</span></span
                ></label
              >
              <label
                >工作区每日额度<span class="with-unit"
                  ><input
                    v-model.number="draft[limitField]"
                    type="number"
                    min="1"
                    max="1000000"
                    required
                  /><span>{{ video ? '秒' : '张' }}</span></span
                ></label
              >
              <label
                >平台并发上限<span class="with-unit"
                  ><input
                    v-model.number="draft.max_concurrent_generations"
                    type="number"
                    min="1"
                    max="1000"
                    required
                  /><span>个</span></span
                ></label
              >
            </div>
            <p class="hint">每日额度在 UTC 00:00 重置</p>
            <label v-if="!video" class="timeout"
              >供应商超时<span class="with-unit"
                ><input
                  v-model.number="draft.timeout_seconds"
                  type="number"
                  min="1"
                  max="300"
                  required
                /><span>秒</span></span
              ></label
            >
          </div>
          <div class="enable">
            <label :id="kind + '-enable-label'">为全体数字员工启用</label
            ><NSwitch
              v-model:value="draft.enabled"
              :aria-labelledby="kind + '-enable-label'"
              :disabled="!!action"
            /><span class="hint">保存后生效</span>
          </div>
        </fieldset>
        <aside>
          <section class="panel test-panel">
            <header>
              <h3>生成测试</h3>
              <span v-if="tested" class="verified">✓ 测试通过</span>
            </header>
            <div :class="['media', { filled: preview }]">
              <video
                v-if="preview && video"
                :src="preview"
                aria-label="供应商测试短片"
                controls
                playsinline
                preload="auto"
                @loadeddata="playable = true"
                @error="previewError"
              />
              <img
                v-else-if="preview"
                :src="preview"
                alt="供应商测试样图"
                @load="playable = true"
                @error="previewError"
              />
              <span v-else role="status">{{
                action === 'testing'
                  ? '正在等待供应商生成…'
                  : video
                    ? '测试后在此播放短片'
                    : '测试后在此预览样图'
              }}</span>
            </div>
            <p v-if="preview" class="hint preview-model">
              {{ draft.upstream_model_id }} · {{ video ? '1 秒 · 768P' : 'PNG'
              }}<span v-if="!receipt && !error"> · 回执已过期，请重新测试</span>
            </p>
            <div class="test-actions">
              <NButton :disabled="!!action" @click="test">{{ testLabel }}</NButton>
              <p class="hint">{{ video ? '每次生成 1 秒短片' : '每次生成 1 张样图' }}，费用由平台承担。</p>
            </div>
          </section>
          <GenerationRecords :kind="kind" />
        </aside>
      </div>
      <footer class="panel actions">
        <span role="status">{{
          notice ||
          (dirty && needsTest && !tested
            ? '模型连接有更改，请先完成生成测试'
            : dirty
              ? '有未保存的更改'
              : savedStatus)
        }}</span>
        <div>
          <NButton :disabled="!!action || !dirty" @click="discard">放弃更改</NButton>
          <NButton attr-type="submit" type="primary" :disabled="!!action || !dirty || (needsTest && !tested)"
            >保存配置</NButton
          >
        </div>
      </footer>
    </form>
  </section>
</template>

<style scoped src="./generation-settings.css"></style>

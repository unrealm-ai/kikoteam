<script setup lang="ts">
import { computed, onMounted, ref, watch } from "vue";
import {
  NAlert,
  NButton,
  NCheckbox,
  NForm,
  NFormItem,
  NInput,
  NInputNumber,
} from "naive-ui";
import { platformRequest, post } from "../api/platform";
interface Config {
  version: number;
  enabled: boolean;
  credential_configured: boolean;
  base_url: string;
  upstream_model_id: string;
  credits_per_second: string;
  workspace_daily_seconds: number;
  max_concurrent_generations: number;
}
interface Review {
  generation_id: string;
  error_code: string;
  has_upstream_id: boolean;
}
const base = "/platform/default-plugins/video-generation";
const active = ref<Config>(),
  draft = ref<Config>(),
  key = ref(""),
  receipt = ref(""),
  busy = ref(false),
  error = ref(""),
  notice = ref("");
const reviews = ref<Review[]>([]),
  reason = ref(""),
  selected = ref("");
const dirty = computed(
  () =>
    !!key.value || JSON.stringify(draft.value) !== JSON.stringify(active.value),
);
function discard() {
  draft.value = active.value && { ...active.value };
  key.value = receipt.value = error.value = notice.value = "";
}
watch(
  () => [key.value, draft.value?.base_url, draft.value?.upstream_model_id],
  () => {
    receipt.value = "";
  },
);
async function load() {
  try {
    active.value = await platformRequest<Config>(base);
    discard();
    await refresh();
  } catch (e) {
    error.value = String(e);
  }
}
async function refresh() {
  try {
    reviews.value = (
      await platformRequest<{ items: Review[] }>(base + "/reviews")
    ).items;
  } catch (e) {
    error.value = String(e);
  }
}
function changes() {
  if (!draft.value || !active.value) throw new Error("请先读取配置");
  const fields = [
    "enabled",
    "base_url",
    "upstream_model_id",
    "credits_per_second",
    "workspace_daily_seconds",
    "max_concurrent_generations",
  ] as const;
  return {
    version: active.value.version,
    ...Object.fromEntries(
      fields
        .filter((k) => draft.value![k] !== active.value![k])
        .map((k) => [k, draft.value![k]]),
    ),
    ...(key.value ? { api_key: key.value } : {}),
  };
}
async function test() {
  if (busy.value) return;
  busy.value = true;
  error.value = notice.value = receipt.value = "";
  try {
    const r = await platformRequest<{ receipt: string }>(base + "/test", {
      ...post(changes()),
      signal: AbortSignal.timeout(315000),
    });
    receipt.value = r.receipt;
    notice.value = "已生成并验证测试短片，可保存配置。";
  } catch (e) {
    error.value = String(e);
  } finally {
    busy.value = false;
  }
}
async function save() {
  if (busy.value) return false;
  busy.value = true;
  error.value = "";
  try {
    active.value = await platformRequest<Config>(base, {
      method: "PATCH",
      body: JSON.stringify({ ...changes(), receipt: receipt.value }),
    });
    discard();
    notice.value = "视频配置已保存";
    return true;
  } catch (e) {
    error.value = String(e);
    return false;
  } finally {
    busy.value = false;
  }
}
async function resolve(action: string) {
  if (!selected.value || !reason.value.trim() || busy.value) return;
  busy.value = true;
  try {
    await platformRequest(
      base + "/reviews/" + selected.value,
      post({ action, reason: reason.value }),
    );
    selected.value = reason.value = "";
    await refresh();
  } catch (e) {
    error.value = String(e);
  } finally {
    busy.value = false;
  }
}
onMounted(load);
defineExpose({ dirty, save, discard });
</script>
<template>
  <section class="video-settings">
    <h2>官方文生视频</h2>
    <p v-if="active">
      {{
        !active.credential_configured
          ? "待配置"
          : active.enabled
            ? "已启用"
            : "已停用"
      }}
      · Nbility MiniMax-H3
    </p>
    <NAlert v-if="error" type="error"
      >{{ error }} <NButton text @click="load">重新读取</NButton></NAlert
    >
    <NForm v-if="draft" :disabled="busy" @submit.prevent="save">
      <NFormItem label="服务根地址"
        ><NInput v-model:value="draft.base_url"
      /></NFormItem>
      <NFormItem label="模型"
        ><span>{{ draft.upstream_model_id }}</span></NFormItem
      >
      <NFormItem label="API Key"
        ><NInput
          v-model:value="key"
          type="password"
          :placeholder="
            active?.credential_configured
              ? '已配置，留空保留'
              : '输入供应商 API Key'
          "
      /></NFormItem>
      <NFormItem label="每秒积分"
        ><NInput v-model:value="draft.credits_per_second"
      /></NFormItem>
      <NFormItem label="每工作区每日秒数（UTC 00:00 重置）"
        ><NInputNumber
          :value="draft.workspace_daily_seconds"
          :min="1"
          @update:value="
            (v) => {
              if (v) draft!.workspace_daily_seconds = v;
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
      <NCheckbox v-model:checked="draft.enabled">为全体数字员工启用</NCheckbox>
      <p>测试会实际生成 1 秒短片，供应商费用由平台承担。</p>
      <NButton :disabled="busy" @click="test">测试视频生成</NButton>
      <NButton type="primary" attr-type="submit" :disabled="busy || !dirty"
        >保存视频配置</NButton
      >
    </NForm>
    <p role="status">{{ notice }}</p>
    <h3>待核对的视频</h3>
    <NButton size="small" @click="refresh">刷新</NButton>
    <p v-if="!reviews.length">暂无待核对记录</p>
    <div v-for="r in reviews" :key="r.generation_id">
      <NButton
        text
        @click="
          selected = r.generation_id;
          reason = '';
        "
        >{{ r.generation_id }} · {{ r.error_code }}</NButton
      >
      <div v-if="selected === r.generation_id">
        <NInput v-model:value="reason" placeholder="填写核对依据" /><NButton
          :disabled="busy || !reason.trim()"
          @click="resolve('reconcile')"
          >恢复原生成记录</NButton
        >
        <NButton
          :disabled="busy || !reason.trim()"
          @click="resolve('close_no_charge')"
          >确认无成片，零收费关闭</NButton
        >
      </div>
    </div>
  </section>
</template>
<style scoped>
.video-settings {
  max-width: 44rem;
  padding: 22px 0;
}
.video-settings h2,
.video-settings h3 {
  margin: 1rem 0;
}
.video-settings p {
  margin: 0.6rem 0;
}
</style>

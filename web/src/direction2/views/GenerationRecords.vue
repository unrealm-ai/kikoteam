<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue';
import { platformRequest, post } from '../api/platform';

const props = defineProps<{ kind: 'image' | 'video' }>();
interface Entry {
  generation_id: string;
  status?: string;
  billing_status?: string;
  error_code?: string;
  started_at?: string;
  agent_name?: string;
  credits_per_image?: string;
  charged_credits?: string | null;
}
const video = computed(() => props.kind === 'video');
const records = ref<Entry[]>([]),
  loaded = ref(false),
  cursor = ref(''),
  selected = ref(''),
  reason = ref('');
const busy = ref(false),
  error = ref('');
const lifetime = new AbortController();
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
async function refresh(more = false) {
  if (busy.value) return;
  busy.value = true;
  error.value = '';
  try {
    const q = new URLSearchParams({ scope: 'all', limit: '20' });
    if (more) q.set('cursor', cursor.value);
    const path = video.value
      ? '/platform/default-plugins/video-generation/reviews'
      : '/image-generations?' + q;
    const result = await platformRequest<{ items: Entry[]; next_cursor?: string }>(path, {
      signal: AbortSignal.any([lifetime.signal, AbortSignal.timeout(20000)]),
    });
    if (lifetime.signal.aborted) return;
    if (more && result.next_cursor && result.next_cursor === cursor.value)
      throw new Error('出图记录游标未更新，请刷新');
    records.value = more ? [...records.value, ...result.items] : result.items;
    cursor.value = result.next_cursor || '';
    loaded.value = true;
  } catch (e) {
    if (!lifetime.signal.aborted) error.value = String(e);
  } finally {
    busy.value = false;
  }
}
async function resolve(action: 'reconcile' | 'close_no_charge') {
  if (!selected.value || !reason.value.trim() || busy.value) return;
  busy.value = true;
  error.value = '';
  try {
    const id = encodeURIComponent(selected.value);
    const path = video.value
      ? '/platform/default-plugins/video-generation/reviews/' + id
      : '/image-generations/' + id + '/resolve';
    await platformRequest(path, {
      ...post({ action, reason: reason.value.trim() }),
      signal: AbortSignal.any([lifetime.signal, AbortSignal.timeout(20000)]),
    });
    selected.value = reason.value = '';
  } catch (e) {
    if (!lifetime.signal.aborted) error.value = String(e);
    return;
  } finally {
    busy.value = false;
  }
  if (!lifetime.signal.aborted) await refresh();
}
onMounted(() => {
  void refresh();
});
onUnmounted(() => lifetime.abort());
</script>

<template>
  <section class="records">
    <header>
      <h3>{{ video ? '待核对的视频' : '全平台出图记录' }}</h3>
      <span v-if="video && loaded && !error" class="count">{{
        records.length === 100 ? '100+' : records.length
      }}</span>
      <button type="button" class="refresh" :disabled="busy" aria-label="刷新记录" @click="refresh()">
        刷新
      </button>
    </header>
    <p v-if="error" role="alert">{{ error }}</p>
    <p v-else-if="!loaded" class="empty">正在读取记录…</p>
    <p v-else-if="!records.length" class="empty">{{ video ? '暂无待核对记录' : '暂无出图记录' }}</p>
    <div v-if="records.length" class="list">
      <article v-for="item in records" :key="item.generation_id">
        <template v-if="!video"
          ><strong>{{ item.agent_name }}</strong> · {{ labels[item.status || ''] }} ·
          {{ labels[item.billing_status || ''] }}
          <p>
            {{ item.started_at && new Date(item.started_at).toLocaleString() }} · 单价
            {{ item.credits_per_image }} · 实扣 {{ item.charged_credits ?? '待确认' }}
          </p></template
        >
        <code>{{ item.generation_id }}</code>
        <p v-if="video">{{ item.error_code }}</p>
        <button
          v-if="video || item.status === 'pending_review'"
          type="button"
          :disabled="busy"
          @click="
            selected = item.generation_id;
            reason = '';
          "
        >
          处理核对
        </button>
        <div v-if="selected === item.generation_id" class="resolve">
          <input v-model="reason" aria-label="核对依据" placeholder="填写核对依据" maxlength="1024" />
          <button type="button" :disabled="busy || !reason.trim()" @click="resolve('reconcile')">
            {{ video ? '恢复原生成记录' : '核对原图' }}
          </button>
          <button type="button" :disabled="busy || !reason.trim()" @click="resolve('close_no_charge')">
            确认无产物，零收费关闭
          </button>
        </div>
      </article>
      <button v-if="cursor" type="button" :disabled="busy" @click="refresh(true)">加载更多</button>
    </div>
  </section>
</template>
<style scoped>
.records {
  padding: 20px;
  background: var(--id-bg-surface);
  border: 1px solid var(--id-border-subtle);
  border-radius: 10px;
  min-width: 0;
}
header,
.resolve {
  display: flex;
  gap: 8px;
  align-items: center;
  flex-wrap: wrap;
}
h3 {
  margin: 0;
  font-size: 16px;
}
.count {
  border-radius: 1em;
  background: var(--id-bg-muted);
  padding: 0 7px;
  font-size: 12px;
}
button {
  font: inherit;
  font-size: 13px;
  cursor: pointer;
  color: var(--id-accent-primary);
  background: none;
  padding: 3px 0;
  border: 0;
}
button:disabled {
  opacity: 0.5;
  cursor: default;
}
.refresh {
  margin-left: auto;
}
.empty {
  text-align: center;
  color: var(--id-text-muted);
  padding: 20px 0 10px;
}
.list {
  max-height: 22rem;
  overflow: auto;
}
article {
  border-top: 1px solid var(--id-border-subtle);
  padding: 12px 0;
  margin-top: 12px;
  font-size: 13px;
}
code,
p {
  overflow-wrap: anywhere;
}
article p {
  color: var(--id-text-secondary);
  margin: 4px 0;
}
.resolve input {
  width: 100%;
  min-width: 0;
  font: inherit;
  padding: 6px;
  border: 1px solid var(--id-border-subtle);
  border-radius: 5px;
}
[role='alert'] {
  color: var(--id-status-danger);
}
</style>

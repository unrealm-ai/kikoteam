<script setup lang="ts">
import { computed, onUnmounted, ref, watch } from 'vue';
import { NAlert, NButton, NEmpty, NForm, NFormItem, NInput, NModal, NSelect, NTable } from 'naive-ui';
import { creditsApi, type Ledger, type Usage, type UsageTokens } from '../api/credits';

const props = defineProps<{accountId: string; admin?: boolean; name?: string}>();
const emit = defineEmits<{changed: []}>();
const tab = ref<'ledger'|'usage'>('ledger');
const balance = ref(''), spent = ref(''), error = ref(''), notice = ref('');
const entries = ref<Ledger[]>([]), calls = ref<Usage[]>([]);
const next = ref(''), loading = ref(false);
const employee = ref<string|null>(null), model = ref<string|null>(null), status = ref<string|null>(null);
const from = ref(''), to = ref('');
const employees = ref(new Map<string,string>()), models = ref(new Map<string,string>());
let request: AbortController | undefined;
const statuses = {running:'进行中',usage_recorded:'待结算',settled:'已结算',pending_review:'待核对',no_charge:'不收费'};
const kinds = {signup_bonus:'注册赠送',referral_bonus:'邀请奖励',admin_grant:'管理员赠送',model_charge:'模型消耗'};
const statusTone = (value: Usage['status']) => {
  if (value === 'settled') return 'success';
  if (value === 'pending_review') return 'warning';
  if (value === 'running' || value === 'usage_recorded') return 'info';
  return 'muted';
};
const kindTone = (value: Ledger['kind']) => {
  if (value === 'signup_bonus' || value === 'referral_bonus' || value === 'admin_grant') return 'success';
  return 'muted';
};
const formatter = new Intl.DateTimeFormat(undefined,{dateStyle:'medium',timeStyle:'short'});
const rows = computed(() => tab.value==='usage' ? calls.value : entries.value);

function monthQuery() {
  const now = new Date();
  return new URLSearchParams({
    from: new Date(now.getFullYear(),now.getMonth(),1).toISOString(),
    to: new Date(now.getFullYear(),now.getMonth()+1,1).toISOString(), limit:'1'
  });
}
function filters(cursor: string) {
  const q = new URLSearchParams();
  if (employee.value) q.set('agent_id',employee.value);
  if (model.value) q.set('model_id',model.value);
  if (tab.value==='usage' && status.value) q.set('status',status.value);
  if (from.value) q.set('from',new Date(from.value+'T00:00:00').toISOString());
  if (to.value) {
    const end = new Date(to.value+'T00:00:00');
    end.setDate(end.getDate()+1);
    q.set('to',end.toISOString());
  }
  if (cursor) q.set('cursor',cursor);
  return q;
}
function capture(u?: Usage) {
  if (!u) return;
  employees.value.set(u.agent_id,u.agent_name);
  models.value.set(u.model_id,u.price_snapshot.model_name);
}
async function load(cursor = '') {
  request?.abort();
  const active = request = new AbortController();
  const signal = active.signal;
  const {accountId} = props;
  const mode = tab.value;
  loading.value = true;
  error.value = '';
  if (!cursor) {next.value='';entries.value=[];calls.value=[];}
  try {
    const q = filters(cursor);
    if (mode==='ledger') {
      const page = await creditsApi.ledger(accountId,q,signal);
      if (signal.aborted) return;
      entries.value = [...entries.value,...page.items];
      page.items.forEach(e=>capture(e.usage));
      next.value = page.next_cursor;
    } else {
      const page = await creditsApi.usage(accountId,q,signal);
      if (signal.aborted) return;
      calls.value = [...calls.value,...page.items];
      page.items.forEach(capture);
      next.value = page.next_cursor;
    }
    if (!cursor) {
      const [wallet,month] = await Promise.all([creditsApi.wallet(accountId,signal),creditsApi.ledger(accountId,monthQuery(),signal)]);
      if (signal.aborted) return;
      balance.value = wallet.balance;
      spent.value = month.summary?.spent ?? '0';
    }
  } catch (e) {
    if (!signal.aborted) error.value = e instanceof Error ? e.message : '读取积分失败';
  } finally { if (!signal.aborted) loading.value = false; }
}

const resolving = ref<Usage>(), resolveMode = ref('no_charge'), reason = ref(''), busy = ref(false), resolveError=ref('');
const tokenFields = [
  {key:'input_tokens',label:'输入 Token'}, {key:'output_tokens',label:'输出 Token'},
  {key:'cache_read_tokens',label:'缓存读取 Token'}, {key:'cache_write_tokens',label:'缓存写入 Token'}
] as const;
function blankTokens() {return {input_tokens:'',output_tokens:'',cache_read_tokens:'0',cache_write_tokens:'0'};}
const tokenValues = ref(blankTokens());
function openResolve(usage: Usage) {
  resolving.value=usage;resolveMode.value='no_charge';reason.value='';resolveError.value='';tokenValues.value=blankTokens();
}
async function resolve() {
  const call = resolving.value;
  if (!call || busy.value) return;
  if (!reason.value.trim()) {resolveError.value='请填写核对原因。';return;}
  const keys = tokenFields.map(f=>f.key);
  const provided = keys.map(k=>tokenValues.value[k]);
  if (resolveMode.value==='settle' && provided.some(v=>!/^\d+$/.test(v) || !Number.isSafeInteger(Number(v)))) {
    resolveError.value='请输入实际的非负整数用量。';return;
  }
  const usage = resolveMode.value==='settle' ? Object.fromEntries(keys.map((k,i)=>[k,Number(provided[i])])) as unknown as UsageTokens : undefined;
  busy.value=true;resolveError.value='';
  try {
    await creditsApi.resolve(call.id,reason.value.trim(),usage);
    if (resolving.value?.id!==call.id) return;
    resolving.value=undefined;notice.value='调用已核对';emit('changed');await load();
  } catch(e) { if (resolving.value?.id===call.id) resolveError.value=e instanceof Error?e.message:'处理失败'; }
  finally {busy.value=false;}
}
watch(()=>props.accountId,()=>{
  resolving.value=undefined;balance.value=spent.value=notice.value='';
  employee.value=model.value=status.value=null;
  employees.value.clear();models.value.clear();
  void load();
},{immediate:true});
watch(tab,()=>void load());
onUnmounted(()=>{request?.abort();resolving.value=undefined;});
</script>

<template>
  <section class="credits">
    <header class="credits-header">
      <div class="credits-title-copy">
        <h2 class="credits-title">{{ name ? name + ' · 积分' : '我的积分' }}</h2>
        <p class="sub-desc">查看官方模型调用消耗、积分变动流水与当月消费统计。</p>
      </div>
      <NButton size="small" secondary :loading="loading" @click="load()">刷新</NButton>
    </header>

    <div class="balances" v-if="balance">
      <div class="balance-stat-card balance-stat-card--primary">
        <span class="balance-label">当前余额</span>
        <strong class="balance-value tabular-num">{{balance}} 积分</strong>
      </div>
      <div class="balance-stat-card">
        <span class="balance-label">本月消耗</span>
        <strong class="balance-value tabular-num">{{spent}} 积分</strong>
      </div>
    </div>

    <NAlert v-if="balance && Number(balance)<=0" type="warning">积分不足，新的官方模型请求暂不可用。请联系平台管理员补充积分。</NAlert>
    <NAlert v-if="error" type="error">{{error}}</NAlert>
    <NAlert v-if="notice" type="success">{{notice}}</NAlert>

    <div class="settings-card credits-toolbar-card">
      <div class="tabs-bar">
        <div class="tabs">
          <NButton size="small" :quaternary="tab !== 'ledger'" :type="tab==='ledger'?'primary':'default'" @click="tab='ledger'">积分流水</NButton>
          <NButton size="small" :quaternary="tab !== 'usage'" :type="tab==='usage'?'primary':'default'" @click="tab='usage'">模型调用</NButton>
        </div>
      </div>

      <div class="filters">
        <NSelect v-model:value="employee" size="small" clearable placeholder="全部员工" :options="Array.from(employees, ([value,label])=>({value,label}))" />
        <NSelect v-model:value="model" size="small" clearable placeholder="全部模型" :options="Array.from(models, ([value,label])=>({value,label}))" />
        <NSelect v-if="tab==='usage'" v-model:value="status" size="small" clearable placeholder="全部状态" :options="Object.entries(statuses).map(([value,label])=>({value,label}))" />
        <label class="date-filter-field">
          <span>开始日期</span>
          <input v-model="from" type="date" class="date-input">
        </label>
        <label class="date-filter-field">
          <span>结束日期</span>
          <input v-model="to" type="date" class="date-input">
        </label>
        <NButton size="small" secondary @click="load()">筛选</NButton>
      </div>
    </div>

    <div v-if="loading && !rows.length" class="settings-card credits-empty-card">
      <p class="sub-desc">正在读取记录…</p>
    </div>
    <div v-else-if="!rows.length && !error" class="settings-card credits-empty-card">
      <NEmpty description="暂无符合条件的记录。" />
    </div>
    <div v-else-if="rows.length" class="members-table-card table-wrap">
      <NTable v-if="tab==='ledger'" :bordered="false" :single-line="false">
        <thead>
          <tr>
            <th>时间</th>
            <th>变动</th>
            <th>积分</th>
            <th>变动后余额</th>
            <th>详情</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="e in entries" :key="e.id">
            <td><span class="mono-text tabular-num muted-text">{{formatter.format(new Date(e.created_at))}}</span></td>
            <td>
              <span class="status-pill" :class="'status-pill--' + kindTone(e.kind)">
                <span class="status-dot" :class="'dot--' + kindTone(e.kind)"></span>
                <span>{{kinds[e.kind]}}</span>
              </span>
            </td>
            <td>
              <span class="mono-text tabular-num amount-cell" :class="{ 'amount-positive': e.kind !== 'model_charge' }">
                {{e.amount}}
              </span>
            </td>
            <td><span class="mono-text tabular-num">{{e.balance_after}}</span></td>
            <td>
              <details v-if="e.usage" class="usage-details">
                <summary class="usage-summary">{{e.usage.agent_name}} · {{e.usage.price_snapshot.model_name}}</summary>
                <div class="usage-detail-panel">
                  <div class="usage-token-grid">
                    <div v-for="field in tokenFields" :key="field.key" class="usage-token-item">
                      <span>{{field.label}}</span>
                      <strong class="tabular-num">{{e.usage[field.key] ?? '—'}}</strong>
                    </div>
                  </div>
                  <code class="mono-text usage-id-code">{{e.usage.id}}</code>
                </div>
              </details>
              <span v-else class="sub-desc">{{e.reason || '—'}}</span>
            </td>
          </tr>
        </tbody>
      </NTable>
      <NTable v-else :bordered="false" :single-line="false">
        <thead>
          <tr>
            <th>时间</th>
            <th>员工 / 模型</th>
            <th>消耗积分</th>
            <th>状态</th>
            <th>详情</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="u in calls" :key="u.id">
            <td><span class="mono-text tabular-num muted-text">{{formatter.format(new Date(u.started_at))}}</span></td>
            <td><span class="usage-model-title">{{u.agent_name}} · {{u.price_snapshot.model_name}}</span></td>
            <td><span class="mono-text tabular-num amount-cell">{{u.charge ?? '—'}}</span></td>
            <td>
              <span class="status-pill" :class="'status-pill--' + statusTone(u.status)">
                <span class="status-dot" :class="'dot--' + statusTone(u.status)"></span>
                <span>{{statuses[u.status]}}</span>
              </span>
            </td>
            <td>
              <div class="usage-action-cell">
                <details class="usage-details">
                  <summary class="usage-summary">用量明细</summary>
                  <div class="usage-detail-panel">
                    <div class="usage-token-grid">
                      <div v-for="field in tokenFields" :key="field.key" class="usage-token-item">
                        <span>{{field.label}}</span>
                        <strong class="tabular-num">{{u[field.key] ?? '—'}}</strong>
                      </div>
                    </div>
                    <code class="mono-text usage-id-code">{{u.id}}</code>
                    <p v-if="u.upstream_request_id" class="mono-text muted-text" style="margin: 4px 0 0 0;">上游 {{u.upstream_request_id}}</p>
                  </div>
                </details>
                <NButton v-if="admin && u.status==='pending_review'" size="small" secondary @click="openResolve(u)">核对</NButton>
              </div>
            </td>
          </tr>
        </tbody>
      </NTable>
    </div>

    <div v-if="next" class="credits-load-more">
      <NButton secondary :loading="loading" @click="load(next)">加载更多</NButton>
    </div>

    <NModal :show="!!resolving" preset="card" title="核对模型调用" style="width:min(480px, 95vw)"
      :closable="!busy" :mask-closable="!busy" :close-on-esc="!busy" @update:show="show=>{if(!show) resolving=undefined}">
      <div class="modal-body-stack">
        <NAlert v-if="resolveError" type="error">{{resolveError}}</NAlert>
        <NForm label-placement="top" :disabled="busy">
          <NFormItem label="处理结果"><NSelect v-model:value="resolveMode" :options="[{value:'no_charge',label:'确认不收费'},{value:'settle',label:'补录实际用量'}]"/></NFormItem>
          <template v-if="resolveMode==='settle'">
            <div class="resolve-tokens-grid">
              <NFormItem v-for="field in tokenFields" :key="field.key" :label="field.label">
                <NInput v-model:value="tokenValues[field.key]" />
              </NFormItem>
            </div>
          </template>
          <NFormItem label="核对原因"><NInput v-model:value="reason" maxlength="300" placeholder="填写核对处理说明" /></NFormItem>
        </NForm>
      </div>
      <template #footer>
        <div class="modal-footer-actions">
          <NButton :disabled="busy" @click="resolving = undefined">取消</NButton>
          <NButton type="primary" :loading="busy" @click="resolve">确认处理</NButton>
        </div>
      </template>
    </NModal>
  </section>
</template>

<style scoped>
.credits {
  display: flex;
  flex-direction: column;
  gap: 16px;
  max-width: 1200px;
}
.credits-header {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  flex-wrap: wrap;
  gap: 12px 16px;
  padding-top: 4px;
}
.credits-title-copy {
  display: flex;
  flex-direction: column;
  gap: 4px;
  min-width: 0;
}
.credits-title {
  margin: 0;
  font-size: 20px;
  font-weight: 600;
  letter-spacing: -0.02em;
  color: var(--id-text-primary);
}

.balances {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 14px;
}
.balance-stat-card {
  background: var(--id-bg-surface);
  border: 1px solid var(--id-border-subtle);
  border-radius: var(--id-radius-lg);
  padding: 16px 20px;
  display: flex;
  flex-direction: column;
  gap: 6px;
}
.balance-stat-card--primary {
  border-color: var(--id-border-strong);
}
.balance-label {
  font-size: 12px;
  font-weight: 500;
  color: var(--id-text-muted);
}
.balance-value {
  font-size: 22px;
  font-weight: 700;
  font-family: var(--id-font-mono);
  color: var(--id-text-primary);
  letter-spacing: -0.02em;
}

.credits-toolbar-card {
  padding: 14px 16px;
  gap: 12px;
}
.tabs-bar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  flex-wrap: wrap;
  gap: 10px;
}
.tabs {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  background: var(--id-bg-muted);
  border: 1px solid var(--id-border-subtle);
  border-radius: var(--id-radius-md);
  padding: 3px;
}
.filters {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 10px;
}
.filters :deep(.n-select) {
  width: 164px;
}
.date-filter-field {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  font-size: 12px;
  color: var(--id-text-secondary);
}
.date-input {
  height: 28px;
  padding: 0 8px;
  border: 1px solid var(--id-border-subtle);
  border-radius: var(--id-radius-md);
  background: var(--id-bg-surface);
  color: var(--id-text-primary);
  font-size: 12px;
  font-family: var(--id-font-sans);
  outline: none;
  transition: border-color 0.15s ease;
}
.date-input:focus {
  border-color: var(--id-accent-primary);
  box-shadow: var(--id-focus-ring);
}

.credits-empty-card {
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
  white-space: nowrap;
}
.amount-cell {
  font-weight: 600;
  color: var(--id-text-primary);
}
.amount-positive {
  color: var(--id-status-success);
}
.usage-model-title {
  font-weight: 500;
  color: var(--id-text-primary);
}
.usage-action-cell {
  display: flex;
  align-items: flex-start;
  gap: 8px;
  flex-wrap: wrap;
}
.usage-details {
  font-size: 12px;
}
.usage-summary {
  cursor: pointer;
  color: var(--id-accent-primary);
  font-weight: 500;
  user-select: none;
}
.usage-detail-panel {
  margin-top: 8px;
  padding: 10px 12px;
  background: var(--id-bg-subtle);
  border: 1px solid var(--id-border-subtle);
  border-radius: var(--id-radius-md);
  display: flex;
  flex-direction: column;
  gap: 6px;
  min-width: 220px;
}
.usage-token-grid {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 6px 12px;
}
.usage-token-item {
  display: flex;
  justify-content: space-between;
  gap: 8px;
  color: var(--id-text-secondary);
}
.usage-token-item strong {
  color: var(--id-text-primary);
  font-family: var(--id-font-mono);
}
.usage-id-code {
  word-break: break-all;
  color: var(--id-text-muted);
}
.credits-load-more {
  display: flex;
  justify-content: center;
}
.resolve-tokens-grid {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 0 12px;
}

@media (max-width: 760px) {
  .balances,
  .resolve-tokens-grid {
    grid-template-columns: 1fr;
  }
  .filters {
    align-items: stretch;
  }
  .filters :deep(.n-select),
  .date-filter-field {
    width: 100%;
  }
  .date-input {
    flex: 1;
  }
}
</style>

<style scoped src="../components/workspace-settings-shared.css"></style>

<script setup lang="ts">
import { computed, onUnmounted, ref, watch } from 'vue';
import { NAlert, NButton, NForm, NFormItem, NInput, NModal, NSelect, NTable } from 'naive-ui';
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
const kinds = {signup_bonus:'注册赠送',admin_grant:'管理员赠送',model_charge:'模型消耗'};
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
    <header><h2>{{ name ? name + ' · 积分' : '我的积分' }}</h2><NButton @click="load()">刷新</NButton></header>
    <div class="balances" v-if="balance">
      <div>当前余额<strong>{{balance}} 积分</strong></div>
      <div>本月消耗<strong>{{spent}} 积分</strong></div>
    </div>
    <NAlert v-if="balance && Number(balance)<=0" type="warning">积分不足，新的官方模型请求暂不可用。请联系平台管理员补充积分。</NAlert>
    <NAlert v-if="error" type="error">{{error}}</NAlert>
    <NAlert v-if="notice" type="success">{{notice}}</NAlert>
    <div class="filters">
      <NSelect v-model:value="employee" clearable placeholder="全部员工" :options="Array.from(employees, ([value,label])=>({value,label}))" />
      <NSelect v-model:value="model" clearable placeholder="全部模型" :options="Array.from(models, ([value,label])=>({value,label}))" />
      <NSelect v-if="tab==='usage'" v-model:value="status" clearable placeholder="全部状态" :options="Object.entries(statuses).map(([value,label])=>({value,label}))" />
      <label>开始日期 <input v-model="from" type="date"></label>
      <label>结束日期 <input v-model="to" type="date"></label>
      <NButton @click="load()">筛选</NButton>
    </div>
    <div class="tabs"><NButton :type="tab==='ledger'?'primary':'default'" @click="tab='ledger'">积分流水</NButton>
      <NButton :type="tab==='usage'?'primary':'default'" @click="tab='usage'">模型调用</NButton></div>
    <p v-if="loading && !rows.length">正在读取记录…</p>
    <p v-else-if="!rows.length && !error">暂无符合条件的记录。</p>
    <div v-else-if="rows.length" class="table-wrap">
      <NTable v-if="tab==='ledger'">
        <thead><tr><th>时间</th><th>变动</th><th>积分</th><th>变动后余额</th><th>详情</th></tr></thead>
        <tbody><tr v-for="e in entries" :key="e.id"><td>{{formatter.format(new Date(e.created_at))}}</td>
          <td>{{kinds[e.kind]}}</td><td>{{e.amount}}</td><td>{{e.balance_after}}</td>
          <td><details v-if="e.usage"><summary>{{e.usage.agent_name}} · {{e.usage.price_snapshot.model_name}}</summary>
            <p v-for="field in tokenFields" :key="field.key">{{field.label}} {{e.usage[field.key] ?? '—'}}</p>
            <code>{{e.usage.id}}</code>
          </details><span v-else>{{e.reason || '—'}}</span></td>
        </tr></tbody>
      </NTable>
      <NTable v-else>
        <thead><tr><th>时间</th><th>员工 / 模型</th><th>消耗积分</th><th>状态</th><th>详情</th></tr></thead>
        <tbody><tr v-for="u in calls" :key="u.id"><td>{{formatter.format(new Date(u.started_at))}}</td>
          <td>{{u.agent_name}} · {{u.price_snapshot.model_name}}</td><td>{{u.charge ?? '—'}}</td><td>{{statuses[u.status]}}</td>
          <td><details><summary>用量明细</summary><p v-for="field in tokenFields" :key="field.key">{{field.label}} {{u[field.key] ?? '—'}}</p>
            <code>{{u.id}}</code><p v-if="u.upstream_request_id">上游 {{u.upstream_request_id}}</p>
          </details><NButton v-if="admin && u.status==='pending_review'" size="small" @click="openResolve(u)">核对</NButton></td>
        </tr></tbody>
      </NTable>
    </div>
    <NButton v-if="next" :loading="loading" @click="load(next)">加载更多</NButton>
    <NModal :show="!!resolving" preset="card" title="核对模型调用" style="width:min(480px, 95vw)"
      :closable="!busy" :mask-closable="!busy" :close-on-esc="!busy" @update:show="show=>{if(!show) resolving=undefined}">
      <NAlert v-if="resolveError" type="error">{{resolveError}}</NAlert>
      <NForm label-placement="top" :disabled="busy">
        <NFormItem label="处理结果"><NSelect v-model:value="resolveMode" :options="[{value:'no_charge',label:'确认不收费'},{value:'settle',label:'补录实际用量'}]"/></NFormItem>
        <template v-if="resolveMode==='settle'"><NFormItem v-for="field in tokenFields" :key="field.key" :label="field.label">
          <NInput v-model:value="tokenValues[field.key]" />
        </NFormItem></template>
        <NFormItem label="核对原因"><NInput v-model:value="reason" maxlength="300" /></NFormItem>
        <NButton type="primary" :loading="busy" @click="resolve">确认处理</NButton>
      </NForm>
    </NModal>
  </section>
</template>

<style scoped>
.credits{padding:24px;max-width:1140px;margin:auto}.credits header,.balances,.filters,.tabs{display:flex;align-items:center;gap:12px;flex-wrap:wrap}.credits header{justify-content:space-between}
.balances{margin:12px 0}.balances div{flex:1;min-width:180px;padding:16px;border:1px solid #e5e7eb;border-radius:8px}.balances strong{display:block;font-size:22px;margin-top:8px}
.filters,.tabs{margin:14px 0}.filters :deep(.n-select){width:152px}.filters input{border:1px solid #e5e7eb;border-radius:4px;padding:5px}.table-wrap{overflow-x:auto;margin-bottom:12px}
</style>

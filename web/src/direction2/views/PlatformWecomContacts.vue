<script setup lang="ts">
import { computed, onUnmounted, ref, watch } from 'vue';
import { NAlert, NButton, NEmpty, NInput, NModal } from 'naive-ui';
import { D2ApiError } from '../api/client';
import { wecomContactsApi, type WecomContact, type WecomVerification } from '../api/wecom-contacts';
import type { FeishuRules } from '../api/feishu';
const props=defineProps<{workspace:string;employeeId:string;identity:string;disabled:boolean;confirmed:boolean;directoryView?:boolean;access?:FeishuRules['access']}>();
const emit=defineEmits<{'update:access':[value:FeishuRules['access']];revoked:[]}>();
const query=ref(''),error=ref(''),pollError=ref(''),busy=ref(false),searched=ref(false),limited=ref(false),rows=ref<WecomContact[]>([]),verification=ref<WecomVerification|null>(null),now=ref(Date.now());
let generation=0,controller=new AbortController();const requests=new Map<string,string>();
const timer=setInterval(()=>now.value=Date.now(),1000);
let pollTimer: ReturnType<typeof setInterval> | undefined;
watch(() => props.directoryView && verification.value?.status === 'pending', active => {
  clearInterval(pollTimer);
  if (active) pollTimer = setInterval(() => { if (!busy.value) void refresh(); }, 3000);
});
function closeVerification() {
  ++generation;
  controller.abort();
  controller = new AbortController();
  verification.value = null;
  pollError.value = '';
  busy.value = false;
}
const pending=computed(()=>verification.value?.status==='pending'&&Date.parse(verification.value.expires_at)>now.value);
const writable=computed(()=>!props.disabled&&props.confirmed&&!busy.value);
function reset(){++generation;controller.abort();controller=new AbortController();rows.value=[];verification.value=null;error.value='';pollError.value='';query.value='';busy.value=false;searched.value=false;limited.value=false;requests.clear();}
watch(()=>[props.workspace,props.employeeId,props.identity,props.confirmed,props.disabled],reset);
function applyVerification(value: WecomVerification) {
  pollError.value = '';
  if (value.status === 'pending' && !(Date.parse(value.expires_at) > Date.now())) {
    value = {...value, status:'expired', code:undefined};
  }
  verification.value = value;
  rows.value = rows.value.map(row => row.search_user_id === value.search_user_id ? {
    ...row, verification_status: value.status, verification_id: value.id,
    callback_user_id: value.status === 'verified' ? value.callback_user_id : undefined
  } : row);
  if (value.status !== 'pending') requests.delete(value.search_user_id);
}
watch([verification, now], () => {
  const value = verification.value;
  if (value?.status === 'pending' && !pending.value) applyVerification({...value, status:'expired', code:undefined});
});
function fail(e:unknown){error.value=e instanceof Error?e.message:'联系人操作失败';rows.value=[];verification.value=null;pollError.value='';if(e instanceof D2ApiError&&[401,403].includes(e.status))emit('revoked');}
async function search(){if(!writable.value||!query.value.trim())return;const g=++generation;controller.abort();controller=new AbortController();busy.value=true;error.value='';rows.value=[];verification.value=null;try{const v=await wecomContactsApi.search(props.workspace,props.employeeId,query.value.trim(),controller.signal);if(g!==generation)return;rows.value=v.items;limited.value=v.limited;searched.value=true;}catch(e){if(g===generation)fail(e);}finally{if(g===generation)busy.value=false;}}
async function verify(row:WecomContact){if(!writable.value)return;const g=generation;busy.value=true;error.value='';try{let value:WecomVerification;if(row.verification_id&&row.verification_status==='pending')value=await wecomContactsApi.status(props.workspace,props.employeeId,row.verification_id,controller.signal);else{let id=requests.get(row.search_user_id);if(!id){id=crypto.randomUUID();requests.set(row.search_user_id,id);}value=await wecomContactsApi.start(props.workspace,props.employeeId,row.search_user_id,id,controller.signal);}if(g===generation)applyVerification(value);}catch(e){if(g===generation){error.value=e instanceof Error?e.message:'创建验证码失败';error.value+='；请求可能已受理，重试将沿用同一请求标识。';if(e instanceof D2ApiError&&[401,403].includes(e.status))fail(e);}}finally{if(g===generation)busy.value=false;}}
async function refresh(cancel=false) {
  const previous=verification.value;
  if(!writable.value || !previous)return;
  const g=generation;
  busy.value=true;error.value='';
  try {
    const value=await (cancel?wecomContactsApi.cancel:wecomContactsApi.status)(props.workspace,props.employeeId,previous.id,controller.signal);
    if(g!==generation)return;
    if(value.id!==previous.id || value.search_user_id!==previous.search_user_id) {
      fail(new Error('验证码身份已变更，请重新搜索联系人。'));
      return;
    }
    applyVerification(value);
  } catch(e) {
    if(g!==generation)return;
    const detail=e instanceof Error?e.message:'请求未完成';
    if(e instanceof TypeError || e instanceof DOMException && e.name==='TimeoutError' ||
       e instanceof D2ApiError && (e.status>=500 || e.status===408 || e.status===429)) {
      pollError.value=cancel ? '暂时无法确认是否取消'+(props.directoryView?'，将继续查询状态':'，请查询状态')+'：'+detail : '暂时无法查询验证状态'+(props.directoryView?'，将继续查询':'，请重试')+'：'+detail;
    } else fail(e);
  } finally {
    if(g===generation)busy.value=false;
  }
}
function add(row:WecomContact,allow:boolean){if(!props.access||!writable.value||row.verification_status!=='verified'||!row.callback_user_id||(allow&&props.access?.mode!=='allowlist'))return;const key=allow?'allow_ids':'deny_ids';emit('update:access',{...props.access,[key]:[...new Set([...props.access[key],row.callback_user_id])]});}
onUnmounted(()=>{reset();clearInterval(timer);clearInterval(pollTimer);});
</script>
<template>
<div v-if="directoryView" class="directory-table-container">
  <div class="table-search-bar">
    <NInput v-model:value="query" class="dir-search-input" size="small" clearable placeholder="搜索企业微信联系人姓名…" :disabled="!confirmed || disabled" @keyup.enter="search" />
    <NButton size="small" secondary :disabled="!writable || !query.trim()" @click="search">搜索联系人</NButton>
  </div>
  <NAlert v-if="!confirmed || disabled" type="info">请先启用并确认当前机器人身份。</NAlert>
  <NAlert v-if="error" type="error">{{ error }}</NAlert>
  <p v-if="limited" class="field-note">仅返回部分匹配结果，可细化关键词。</p>
  <div class="table-scroll-wrap"><table class="custom-d2-table">
    <thead><tr><th>成员姓名</th><th>所属部门</th><th>企业微信身份核验</th><th>操作</th></tr></thead>
    <tbody><tr v-if="!rows.length"><td colspan="4"><NEmpty :description="busy ? '正在查询…' : searched ? '未找到匹配的联系人' : '输入关键词查找联系人'" /></td></tr>
      <tr v-for="row in rows" :key="row.search_user_id"><td><strong class="member-strong">{{ row.display_name }}</strong></td>
        <td>{{ row.department_paths?.join(' / ') || '—' }}</td><td>{{ row.verification_status === 'verified' ? '已核验' : '待核验' }}</td>
        <td><NButton v-if="row.verification_status !== 'verified'" size="tiny" secondary type="warning" :disabled="!writable" @click="verify(row)">核验绑定身份</NButton></td></tr>
    </tbody></table></div>
  <NModal :show="!!verification" class="library-modal" preset="card" title="企业微信成员身份核验" style="width:min(520px,94vw)" @update:show="v => !v && closeVerification()">
    <div v-if="verification" class="wecom-verify-flow">
      <NAlert v-if="pollError" type="warning">{{pollError}}</NAlert>
      <strong class="user-verify-header">联系人 · {{ rows.find(r => r.search_user_id === verification?.search_user_id)?.display_name || verification.search_user_id }}</strong>
      <template v-if="pending && verification.code">
        <p class="verify-instruction-box">请私下将验证码交给这位联系人，由本人私聊发送给当前机器人。</p>
        <div class="verify-code-card"><span class="v-label">绑定验证码</span><code class="v-code">{{ verification.code }}</code><span class="v-timer">有效期至 {{ new Date(verification.expires_at).toLocaleString() }}</span></div>
      </template>
      <p role="status" class="wecom-poll-status-box" :class="{waiting:pending, verified:verification.status === 'verified', expired:verification.status === 'expired', error:verification.status === 'conflict'}">{{ verification.status === 'verified' ? '已核验' : verification.status === 'pending' ? '等待成员私聊验证码' : verification.status === 'expired' ? '验证码已过期' : verification.status === 'conflict' ? '身份映射冲突' : '已取消' }}</p>
      <div class="table-action-btns"><NButton size="small" :disabled="!writable" @click="refresh()">刷新核验状态</NButton><NButton v-if="pending" size="small" :disabled="busy" @click="refresh(true)">取消验证码</NButton><NButton size="small" @click="closeVerification">关闭</NButton></div>
    </div>
  </NModal>
</div>
<section v-else aria-label="企业微信联系人搜索" class="contacts">
 <strong>搜索企业微信联系人</strong><p>搜索 ID 与消息中的用户 ID 可能不同。仅已完成管理员转交验证码映射的联系人可加入使用范围；不把搜索 ID 当作消息身份。此处不提供部门树或全量同步。</p>
 <NAlert v-if="!confirmed" type="warning">请等待机器人收到首条可信消息后再搜索联系人；这不是额外的机器人验证操作。</NAlert>
 <div class="actions"><NInput v-model:value="query" placeholder="搜索企微联系人姓名或关键词" :disabled="!writable" @keyup.enter="search"/><NButton :disabled="!writable||!query.trim()" @click="search">搜索联系人</NButton></div>
 <p v-if="busy" role="status">正在处理联系人请求…</p><NAlert v-if="error" type="error">{{error}}</NAlert><p v-if="limited">提供方仅返回部分匹配结果，请细化搜索关键词。</p><p v-if="searched&&!busy&&!rows.length&&!error">未找到匹配的联系人。</p>
 <ul><li v-for="row in rows" :key="row.search_user_id"><strong>{{row.display_name}}</strong><small>搜索 ID：{{row.search_user_id}} · {{row.department_paths?.join(' / ')}}</small><p>{{row.verification_status==='verified'?'已完成身份映射':'尚未完成身份映射'}}<span v-if="row.callback_user_id"> · 消息用户 ID：{{row.callback_user_id}}</span></p><div class="actions"><template v-if="row.verification_status==='verified'&&row.callback_user_id"><NButton :disabled="!writable||access?.mode!=='allowlist'" @click="add(row,true)">加入允许</NButton><NButton :disabled="!writable" @click="add(row,false)">加入拒绝</NButton></template><NButton v-else :disabled="!writable" @click="verify(row)">{{row.verification_status==='pending'?'查看验证码':'生成转交验证码'}}</NButton></div></li></ul>
 <section v-if="verification" aria-label="联系人身份映射"><NAlert v-if="pollError" type="warning">{{pollError}}</NAlert><p>验证状态：{{verification.status}} · 有效期至 {{verification.expires_at}}</p><template v-if="pending&&verification.code"><NAlert type="warning">请管理员核对所选联系人后，将下方验证码私下交给本人，让本人发给本机器人。持有码代表管理员转交确认，不是提供方独立身份证明；不要发到群里或公开分享。</NAlert><p>所选联系人搜索 ID：{{verification.search_user_id}}</p><code>{{verification.code}}</code></template><p v-if="verification.status==='verified'">映射已完成，请在上方加入允许或拒绝并保存员工统一配置；验证码不会自动授予使用权限。</p><div class="actions"><NButton :disabled="!writable" @click="refresh()">刷新映射状态</NButton><NButton v-if="pending" :disabled="!writable" @click="refresh(true)">取消验证码</NButton></div></section>
</section>
</template>
<style scoped>.contacts{border-top:1px solid var(--id-border-subtle);padding-top:16px;margin-top:16px}.actions{display:flex;gap:8px;align-items:center;flex-wrap:wrap}.actions .n-input{flex:1;min-width:180px}.contacts ul{list-style:none;padding:0;max-height:320px;overflow:auto}.contacts li{padding:12px 0;border-bottom:1px solid var(--id-border-subtle)}.contacts p,.contacts small{font-size:12px;line-height:1.7;overflow-wrap:anywhere;color:var(--id-text-muted)}.contacts small{display:block}.contacts code{display:block;font-size:24px;letter-spacing:4px;padding:12px 0}</style>

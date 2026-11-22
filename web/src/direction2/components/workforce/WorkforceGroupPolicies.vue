<script setup lang="ts">
import {computed,inject,onUnmounted,ref,watch} from 'vue';
import {NAlert,NButton,NCheckbox,NCheckboxGroup,NInput,NModal,NRadio,NRadioGroup,NSlider,NSpace,NSwitch,NSelect} from 'naive-ui';
import {useWorkforcePorts} from '../../api/workforce-ports';
import {groupPolicyDraft,replaceGroupPolicy,replaceChannelHistory,type GroupPolicyDraft} from '../../api/workforce-group-policies';
import type {UsageResource} from '../../api/agent-usage';
import type {WorkforceNamedRule,WorkforceTriggerRules} from '../../api/workforce-trigger-rules';
const copy=<T,>(value:T):T=>JSON.parse(JSON.stringify(value));
const props=defineProps<{workspace:string;employeeId:string;canManage:boolean;channel?:'web'|'feishu'|'wecom'|'dingtalk'}>();
const channelName=computed(()=>({web:'Web',feishu:'飞书',wecom:'企微',dingtalk:'钉钉'}[props.channel||'web']));
const hasHistory=computed(()=>props.channel!=='dingtalk');
const testChannelOptions=[{label:'Web 群消息',value:'web'},{label:'飞书群消息',value:'feishu'}];
const hasRules=computed(()=>!props.channel||props.channel==='web'||props.channel==='feishu');
function policyDraft(u:UsageResource){const d=groupPolicyDraft(u);if(props.channel)d.historyEnabled=u.usage_config.history.enabled&&u.usage_config.history.channels.includes(props.channel);return d;}
const {groupPoliciesApi:api}=useWorkforcePorts();
const usage=ref<UsageResource>(),rules=ref<WorkforceTriggerRules>();
const usageStale=ref(false);
const draft=ref<GroupPolicyDraft>(),error=ref(''),notice=ref(''),busy=ref(false),loading=ref(false),denied=ref(false);
const editor=ref<WorkforceNamedRule>(),editIndex=ref(-1),showRule=ref(false),testChannel=ref<'web'|'feishu'>('web'),testText=ref(''),testResult=ref('');
let epoch=0,controller=new AbortController();
const dirty=computed(()=>!!usage.value&&!!draft.value&&JSON.stringify(draft.value)!==JSON.stringify(policyDraft(usage.value)));
const policyBlocked=computed(()=>blocked.value||usageStale.value);
const blocked=computed(()=>busy.value||loading.value||denied.value||!props.canManage||!api);
function fail(e:unknown){error.value=e instanceof Error?e.message:'请求失败';if([401,403].includes(Number((e as {status?:number})?.status))){denied.value=true;usage.value=undefined;rules.value=undefined;draft.value=undefined;showRule.value=false;controller.abort();}}
async function load(){
 const current=++epoch;controller.abort();controller=new AbortController();
 usage.value=undefined;rules.value=undefined;draft.value=undefined;showRule.value=false;editor.value=undefined;testResult.value='';error.value='';notice.value='';denied.value=false;busy.value=false;loading.value=false;usageStale.value=false;
 if(!api||!props.canManage)return;
 loading.value=true;
 try{
  const [u,r]=await Promise.all([
   api.getUsage(props.workspace,props.employeeId,controller.signal),
   hasRules.value ? api.getRules(props.workspace,props.employeeId,controller.signal) : undefined,
  ]);
  if(current!==epoch)return;
  usage.value=u;draft.value=policyDraft(u);rules.value=r;
 }
 catch(e){if(current===epoch)fail(e);}finally{if(current===epoch)loading.value=false;}
}
async function savePolicy(){
 if(policyBlocked.value||!api||!usage.value||!draft.value)return;const current=epoch;busy.value=true;error.value='';notice.value='';
 try{const saved=await api.saveUsage(props.workspace,props.employeeId,props.channel?replaceChannelHistory(usage.value,props.channel,draft.value.historyEnabled,draft.value.historyLimit):replaceGroupPolicy(usage.value,draft.value),controller.signal);if(current!==epoch)return;usage.value=saved;draft.value=policyDraft(saved);notice.value='群历史设置已保存。';}
 catch(e){if(current===epoch)fail(e);}finally{if(current===epoch)busy.value=false;}
}
function edit(rule?:WorkforceNamedRule,index=-1){if(blocked.value)return;editIndex.value=index;editor.value=rule?copy(rule):{id:'',name:'',type:'keyword',pattern:'',enabled:true,channels:[props.channel==='feishu'?'feishu':'web'],description:''};if(props.channel==='feishu')editor.value.channels=['feishu'];showRule.value=true;testResult.value='';}
async function saveRules(next:WorkforceNamedRule[]){
 if(blocked.value||!api||!rules.value)return;const current=epoch;busy.value=true;error.value='';notice.value='';
 try{
  const saved=await api.saveRules(props.workspace,props.employeeId,{version:rules.value.version,rules:next},controller.signal);
  if(current!==epoch)return;
  rules.value=saved;showRule.value=false;editor.value=undefined;notice.value='群触发规则已保存。';usageStale.value=true;
 }catch(e){if(current===epoch){fail(e);busy.value=false;}return;}
 try{
  // A confirmed write must not be represented as failed because a later read fails.
  if(dirty.value){notice.value+=' 历史设置有未保存修改，请取消这些修改后刷新群配置，再继续编辑。';return;}
  const u=await api.getUsage(props.workspace,props.employeeId,controller.signal);
  if(current===epoch){usage.value=u;draft.value=policyDraft(u);usageStale.value=false;}
 }catch(e){if(current===epoch){fail(e);if(!denied.value)error.value='群触发规则已保存，但历史设置刷新失败。请刷新群配置，不要重复保存规则。';}}
 finally{if(current===epoch)busy.value=false;}
}
function currentEditedRules(){
 const all=copy(rules.value?.rules||[]);
 if(editor.value&&showRule.value){
  const edited=copy(editor.value);
  if(editIndex.value<0)all.push(edited);
  else {const old=all[editIndex.value];
   if(props.channel==='feishu'&&old?.channels.includes('web')){all[editIndex.value]={...old,channels:['web']};all.push({...edited,id:'',channels:['feishu']});}
   else all[editIndex.value]=edited;
  }
 }
 return all;
}
function removeRule(index:number){const all=copy(rules.value?.rules||[]);const old=all[index];if(props.channel==='feishu'&&old?.channels.includes('web'))all[index]={...old,channels:['web']};else all.splice(index,1);void saveRules(all);}
function saveRule(){if(!editor.value?.name.trim()||!editor.value.pattern.trim()||!editor.value.channels.length){error.value='请填写名称、匹配内容，并选择至少一个渠道';return;}void saveRules(currentEditedRules());}
async function test(){if(blocked.value||!api||!rules.value||!testText.value.trim())return;const current=epoch;busy.value=true;testResult.value='';error.value='';try{const result=await api.testRules(props.workspace,props.employeeId,testText.value,currentEditedRules(),props.channel==='feishu'?'feishu':props.channel==='web'?'web':testChannel.value);if(current!==epoch)return;testResult.value=result.matched_count?result.matched.map(v=>`${v.rule_name}：${v.matched_text}`).join('；'):'未命中已启用规则';}catch(e){if(current===epoch)fail(e);}finally{if(current===epoch)busy.value=false;}}
const draftGuard={
 hasUnsaved:()=>dirty.value||showRule.value,
 getLabel:()=>`${channelName.value}群配置`,
 save:async()=>{if(showRule.value)return false;if(!dirty.value||policyBlocked.value)return !dirty.value;await savePolicy();return !dirty.value;},
 discard:()=>{showRule.value=false;editor.value=undefined;error.value='';if(usage.value)draft.value=policyDraft(usage.value);}
};
const d2DraftGuard=inject<{register:(v:typeof draftGuard)=>void;unregister:(v:typeof draftGuard)=>void}|null>('d2DraftGuard',null);
d2DraftGuard?.register(draftGuard);
watch(()=>[props.workspace,props.employeeId,props.canManage,props.channel],()=>{testChannel.value=props.channel==='feishu'?'feishu':'web';void load();},{immediate:true});
onUnmounted(()=>{++epoch;controller.abort();d2DraftGuard?.unregister(draftGuard);});
</script>
<template>
 <section class="ch-section group-policies" :class="{'is-dirty-card':dirty}">
  <div class="cs-header-flex">
   <h4 class="cs-title">{{props.channel?channelName+'群配置':'群聊触发与近期上下文（员工统一配置）'}}</h4>
   <div class="card-edit-actions">
    <span v-if="dirty" class="card-dirty-pill">未保存</span>
    <NButton size="tiny" :disabled="busy||dirty||showRule" @click="load">刷新群配置</NButton>
   </div>
  </div>
  <p class="cs-sub">支持群 @ 触发，此处仅展示能力，不修改已有 @ 配置。历史按渠道启用，最大条数为所有渠道共用。</p>
  <NAlert v-if="error" type="error">{{error}}</NAlert><NAlert v-if="notice" type="success">{{notice}}</NAlert><NAlert v-if="usageStale&&!denied" type="warning">历史设置版本待刷新，暂不可修改；请先取消未保存修改，再刷新群配置。</NAlert>
  <p v-if="!api" class="field-note">当前配置接口未提供。</p><p v-if="loading" class="field-note">正在读取群配置…</p><p v-if="!canManage||denied" class="field-note">需要当前工作区管理权限。</p>
  <template v-if="draft&&!denied">
   <div class="p-row"><strong>{{props.channel?channelName:'Web／飞书'}}群 @ 触发</strong><span class="wf-status-pill is-info"><span class="wf-status-dot" />支持</span></div>
   <p v-if="usage && (!props.channel||props.channel==='web'||props.channel==='feishu') && !usage.usage_config.trigger.mention" class="cs-sub">当前员工已有 @ 配置为关闭，本页保留该配置，不自动启用。</p>
   <template v-if="hasHistory">
    <div class="p-row"><strong>保留近期群聊历史消息作为上下文</strong><NSwitch v-model:value="draft.historyEnabled" :disabled="policyBlocked" aria-label="启用近期群历史" /></div>
    <NCheckboxGroup v-if="!props.channel" v-model:value="draft.historyChannels" :disabled="policyBlocked"><NSpace wrap><NCheckbox value="web">Web 群</NCheckbox><NCheckbox value="feishu">飞书群</NCheckbox><NCheckbox value="wecom">企微群</NCheckbox></NSpace></NCheckboxGroup>
    <p class="cs-sub">仅修改当前渠道的启用状态；最大条数与其他渠道共用。钉钉暂无群历史能力。</p>
    <div class="slider-row"><span>共同历史条数：<strong>{{draft.historyLimit}}</strong></span><NSlider v-model:value="draft.historyLimit" :min="5" :max="100" :step="1" :disabled="policyBlocked" aria-label="共同历史条数" /></div>
    <div class="card-edit-actions" style="justify-content:flex-end;">
     <NButton size="small" :disabled="blocked||!dirty" @click="draft=policyDraft(usage!)">取消修改</NButton>
     <NButton size="small" type="primary" :disabled="policyBlocked||!dirty" @click="savePolicy">保存历史设置</NButton>
    </div>
   </template>
   <template v-if="hasRules && rules">
    <div class="cs-header-flex rules-subhead">
     <h4 class="cs-title">群关键词／正则规则</h4>
     <NButton size="small" secondary type="primary" :disabled="blocked" @click="edit()">新增群匹配规则</NButton>
    </div>
    <div v-if="rules.rules.length" class="rules-table-scroll">
     <table class="feishu-rules-table">
      <thead><tr><th>规则</th><th>适用渠道</th><th>匹配内容</th><th style="width: 120px;">操作</th></tr></thead>
      <tbody>
       <tr v-for="(rule,index) in rules.rules" v-show="!props.channel||rule.channels.includes(props.channel as 'web'|'feishu')" :key="rule.id">
        <td><strong>{{rule.name}}</strong> · <span :class="rule.enabled ? 'text-success' : 'text-muted'">{{rule.enabled?'启用':'停用'}}</span></td>
        <td>{{rule.channels.map(v=>v==='web'?'Web':'飞书').join('、')}}</td>
        <td><code>{{rule.type==='regex'?'正则':'关键词'}}：{{rule.pattern}}</code></td>
        <td><div class="rule-row-actions"><NButton size="tiny" quaternary @click="edit(rule,index)" :disabled="blocked">编辑</NButton><NButton size="tiny" quaternary type="error" :disabled="blocked" @click="removeRule(index)">删除</NButton></div></td>
       </tr>
      </tbody>
     </table>
    </div>
    <p v-else class="empty-note">暂无群匹配规则。</p>
    <div class="rule-test-box">
     <p class="cs-sub" style="margin:0;">只模拟匹配，不发送群消息，不触发员工回复。</p>
     <div class="rule-test-bar">
      <NSelect v-if="!props.channel" v-model:value="testChannel" :disabled="blocked" aria-label="模拟消息渠道" :options="testChannelOptions" style="width:150px;flex-shrink:0;" />
      <NInput v-model:value="testText" :disabled="blocked" placeholder="输入一条群消息，测试是否命中规则" />
      <NButton size="small" secondary :disabled="blocked||!testText.trim()" @click="test">测试命中</NButton>
     </div>
     <p v-if="testResult" role="status" class="rule-test-result">{{testResult}}</p>
    </div>
    <NModal v-model:show="showRule" :mask-closable="false" :close-on-esc="false" preset="card" title="编辑群匹配规则" style="width:min(600px,94vw)" :closable="!busy">
     <div v-if="editor" class="rule-modal-flow">
      <label class="rule-field"><span>规则名称</span><NInput v-model:value="editor.name" :disabled="blocked" placeholder="规则名称" /></label>
      <div class="rule-field"><span>匹配方式</span><NRadioGroup v-model:value="editor.type" :disabled="blocked"><NSpace wrap><NRadio value="keyword">关键词</NRadio><NRadio value="regex">正则表达式</NRadio></NSpace></NRadioGroup></div>
      <label class="rule-field"><span>匹配内容</span><NInput v-model:value="editor.pattern" :disabled="blocked" placeholder="匹配内容" /></label>
      <div v-if="!props.channel" class="rule-field"><span>适用渠道</span><NCheckboxGroup v-model:value="editor.channels" :disabled="blocked"><NSpace wrap><NCheckbox value="web">Web 群</NCheckbox><NCheckbox value="feishu">飞书群</NCheckbox></NSpace></NCheckboxGroup></div>
      <label class="rule-field"><span>规则说明</span><NInput :value="editor.description || ''" @update:value="v => {if(editor) editor.description=v;}" :disabled="blocked" placeholder="规则说明" /></label>
      <NCheckbox v-model:checked="editor.enabled" :disabled="blocked">启用此规则</NCheckbox>
      <p class="field-note">{{props.channel==='feishu'?'仅修改飞书规则；已有共享规则会拆分并保留原 Web 配置。':'编辑共享规则会同时更新所选渠道；其他规则保持不变。'}}测试由后端使用实际同一匹配器执行。</p>
      <div class="rule-modal-actions">
       <NButton type="primary" :disabled="blocked" @click="saveRule">保存群规则</NButton>
      </div>
      <div class="rule-test-box">
       <p class="cs-sub" style="margin:0;">只模拟匹配，不发送群消息，不触发员工回复。</p>
       <div class="rule-test-bar">
        <NSelect v-if="!props.channel" v-model:value="testChannel" :disabled="blocked" aria-label="模拟消息渠道" :options="testChannelOptions" style="width:150px;flex-shrink:0;" />
        <NInput v-model:value="testText" :disabled="blocked" placeholder="输入一条群消息，测试是否命中规则" />
        <NButton size="small" secondary :disabled="blocked||!testText.trim()" @click="test">测试草稿命中</NButton>
       </div>
       <p v-if="testResult" role="status" class="rule-test-result">{{testResult}}</p>
      </div>
      <NAlert v-if="error" type="error">{{error}}</NAlert>
     </div>
    </NModal>
   </template>
  </template>
 </section>
</template>
<style scoped>
@import './workforce-shared.css';
.group-policies{display:flex;flex-direction:column;gap:12px}
.slider-row{display:flex;gap:16px;align-items:center;font-size:12px;color:var(--id-text-secondary)}
.slider-row .n-slider{flex:1}
.p-row,.cs-header-flex{display:flex;justify-content:space-between;align-items:center;gap:12px}
.rules-subhead{padding-top:8px;border-top:1px solid var(--id-border-hairline)}
.rules-table-scroll{overflow-x:auto;-webkit-overflow-scrolling:touch;border:1px solid var(--id-border-subtle);border-radius:var(--id-radius-md)}
.feishu-rules-table{width:100%;min-width:540px;border-collapse:collapse;font-size:12px;font-variant-numeric:tabular-nums}
.feishu-rules-table th,.feishu-rules-table td{padding:8px 10px;text-align:left;border-bottom:1px solid var(--id-border-hairline)}
.feishu-rules-table th{background:var(--id-bg-subtle);font-weight:600;color:var(--id-text-secondary);border-bottom:1px solid var(--id-border-subtle)}
.feishu-rules-table tbody tr:last-child td{border-bottom:none}
.rule-row-actions{display:flex;gap:6px;align-items:center}
.rule-test-box{display:flex;flex-direction:column;gap:8px;padding:10px 12px;background:var(--id-bg-subtle);border:1px solid var(--id-border-subtle);border-radius:var(--id-radius-md)}
.rule-test-bar{display:flex;gap:8px;align-items:center;flex-wrap:wrap}
.rule-test-bar :deep(.n-input){flex:1;min-width:180px}
.rule-test-result{margin:0;font-size:12px;color:var(--id-text-primary);background:var(--id-bg-surface);border:1px solid var(--id-border-subtle);padding:6px 10px;border-radius:var(--id-radius-sm)}
.rule-modal-flow{display:flex;flex-direction:column;gap:12px}
.rule-field{display:flex;flex-direction:column;gap:6px;font-size:12px;font-weight:500;color:var(--id-text-secondary)}
.rule-modal-actions{display:flex;justify-content:flex-end;gap:8px}
</style>

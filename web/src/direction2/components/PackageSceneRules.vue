<script setup lang="ts">
import { computed } from 'vue';
import { NAlert, NCheckbox, NSelect, NButton } from 'naive-ui';
import type { PackageConversationScope, PackageSceneScope, PackageTriggerScope } from '../api/capabilities';
import { defaultConversationScope } from '../api/capabilities';
import { toUsageSelector, type SelectionCandidate } from '../api/selectors';
import PlatformIdentityPicker from './PlatformIdentityPicker.vue';
import PlatformSelectedSubjects from './PlatformSelectedSubjects.vue';
const props=defineProps<{workspace:string;employeeId:string;conversation?:PackageConversationScope|null|undefined;trigger:PackageTriggerScope;disabled:boolean;triggerOptions:{label:string;value:string}[];members:{id:string;name:string;email:string;status:string}[]}>();
const emit=defineEmits<{ 'update:conversation':[value:PackageConversationScope];'update:trigger':[value:PackageTriggerScope];revoked:[] }>();
const scenes=[{key:'private',label:'私聊'},{key:'group',label:'群聊'}] as const;
const triggerEnabled=computed(()=>props.trigger.enabled!==false);
function scene(key:'private'|'group',patch:Partial<PackageSceneScope>){if(props.disabled||!props.conversation)return;emit('update:conversation',{...props.conversation,[key]:{...props.conversation[key],...patch}});}
function mode(key:'private'|'group',value:PackageSceneScope['mode']){const s=props.conversation?.[key];if(!s)return; // Never silently discard a populated opposite list.
 if((value==='allowlist'?s.deny:s.allow).length)return;
 scene(key,{mode:value});}
function picked(key:'private'|'group',candidate:SelectionCandidate,descendants:boolean){const s=props.conversation?.[key];if(!s||!s.enabled||key==='private'&&candidate.ref.kind==='group')return;const side=s.mode==='allowlist'?'allow':'deny',ref=toUsageSelector(candidate.ref,descendants);if(!s[side].some(x=>JSON.stringify(x)===JSON.stringify(ref)))scene(key,{[side]:[...s[side],ref]});}
function remove(key:'private'|'group',index:number){const s=props.conversation?.[key];if(!s)return;const side=s.mode==='allowlist'?'allow':'deny';scene(key,{[side]:s[side].filter((_,i)=>i!==index)});}
function templateValues(key:'private'|'group'){const scope=props.conversation![key];return scope[scope.mode==='allowlist'?'allow':'deny'];}
function templateOptions(key:'private'|'group'){const result=props.members.map(m=>({label:`${m.name} (${m.email})`,value:m.id,disabled:m.status!=='active'}));for(const s of templateValues(key))if(!result.some(m=>m.value===s.id))result.push({label:`不可见或已失效：${s.id}`,value:s.id,disabled:true});return result;}
function templateSelect(key:'private'|'group',ids:string[]){const s=props.conversation?.[key];if(!s)return;const side=s.mode==='allowlist'?'allow':'deny';scene(key,{[side]:ids.map(id=>templateValues(key).find(v=>v.id===id)||{channel:'web',kind:'user',id})});}
function triggerMode(mode:PackageTriggerScope['mode']) {
 if(mode===props.trigger.mode || (mode==='allowlist'?props.trigger.deny_trigger_ids:props.trigger.allow_trigger_ids).length)return;
 updateTrigger({mode,allow_trigger_ids:[],deny_trigger_ids:[]});
}
function updateTrigger(patch:Partial<PackageTriggerScope>){if(!props.disabled)emit('update:trigger',{...props.trigger,...patch});}
</script>
<template>
 <section class="package-scene-rules resource-fieldset">
  <strong>使用场景</strong>
  <template v-if="conversation">
   <div class="scene-switches">
    <NCheckbox v-for="s in scenes" :key="s.key" :checked="conversation[s.key].enabled" :disabled="disabled" @update:checked="v=>scene(s.key,{enabled:v})">{{s.label}}</NCheckbox>
    <NCheckbox :checked="triggerEnabled" :disabled="disabled" @update:checked="v=>updateTrigger({enabled:v})">触发器</NCheckbox>
   </div>
   <p class="resource-hint-sm">三个场景独立启停；关闭后保留草稿规则，但不允许在该场景使用此包。能力包不能扩大员工本身的使用权限。</p>
   <fieldset v-for="s in scenes.filter(s=>conversation![s.key].enabled)" :key="s.key" class="resource-fieldset" :data-scene="s.key">
    <legend>{{s.label}}使用范围</legend>
    <NSelect :virtual-scroll="false" :value="conversation[s.key].mode" :disabled="disabled" :options="[{label:'继承员工使用范围 + 黑名单',value:'inherit_with_blacklist',disabled:conversation[s.key].allow.length>0},{label:'白名单',value:'allowlist',disabled:conversation[s.key].deny.length>0}]" @update:value="v=>mode(s.key,v)" />
    <p class="resource-hint-sm">切换模式前请明确移除原名单；不会自动丢弃已有授权规则。</p>
    <p v-if="s.key==='group'" class="resource-hint-sm">群聊白名单同时配置人员（含部门）和群时，必须同时满足人员范围与群范围；同类别内部任一匹配即可。</p>
    <PlatformIdentityPicker v-if="employeeId" :key="`${s.key}:${conversation[s.key].mode}:people`" :workspace="workspace" :employee-id="employeeId" kind="people" purpose="capability" include-departments :disabled="disabled" @pick="(c,d)=>picked(s.key,c,d)" @revoked="emit('revoked')" />
    <PlatformIdentityPicker v-if="employeeId && s.key==='group'" :key="`${s.key}:${conversation[s.key].mode}:groups`" :workspace="workspace" :employee-id="employeeId" kind="groups" purpose="capability" :disabled="disabled" @pick="(c,d)=>picked(s.key,c,d)" @revoked="emit('revoked')" />
    <p v-if="!employeeId" class="resource-hint-sm">模板可选择当前工作区 Web 成员；渠道人员、部门和群请复制给员工后配置。空白名单不允许任何人。</p>
    <NSelect :virtual-scroll="false" v-if="!employeeId" :value="templateValues(s.key).map(v=>v.id)" :disabled="disabled" multiple filterable :options="templateOptions(s.key)" @update:value="v=>templateSelect(s.key,v)" />
    <h4 class="resource-subhead">{{conversation[s.key].mode==='allowlist'?'允许名单':'拒绝名单'}}</h4>
    <PlatformSelectedSubjects v-if="employeeId" :workspace="workspace" :employee-id="employeeId" :items="conversation[s.key][conversation[s.key].mode==='allowlist'?'allow':'deny']" purpose="capability" :disabled="disabled" @remove="i=>remove(s.key,i)" />
   </fieldset>
  </template>
  <NAlert v-else type="warning">此包仍使用旧版统一使用范围，不能自动转换为三个独立场景。旧规则会保留。请核对下方旧规则后，明确重新配置私聊和群聊；触发器范围保持不变。重新配置后旧私聊/群聊限制不再生效，可能扩大权限；请逐项核对后保存，取消编辑可恢复旧规则。
   <NButton :disabled="disabled" size="small" @click="emit('update:conversation',defaultConversationScope())">确认重新配置私聊和群聊（初始关闭）</NButton>
  </NAlert>
  <fieldset v-if="triggerEnabled" class="resource-fieldset" data-scene="trigger">
   <legend>自动化任务使用范围</legend><p v-if="!employeeId && trigger.mode==='allowlist' && !trigger.allow_trigger_ids.length" class="resource-hint-sm">模板空白名单复制后仍禁止全部自动化任务。</p>
   <NSelect :virtual-scroll="false" :value="trigger.mode" :disabled="disabled" :options="[{label:'全部触发器 + 黑名单',value:'all',disabled:trigger.allow_trigger_ids.length>0},{label:'白名单',value:'allowlist',disabled:trigger.deny_trigger_ids.length>0}]" @update:value="triggerMode" />
   <p v-if="trigger.allow_trigger_ids.length || trigger.deny_trigger_ids.length" class="resource-hint-sm">切换模式前，请先清空当前名单。</p>
   <NSelect :virtual-scroll="false" v-if="trigger.mode==='allowlist'" :value="trigger.allow_trigger_ids" :disabled="disabled" multiple filterable :options="triggerOptions" placeholder="允许的触发器" @update:value="v=>updateTrigger({allow_trigger_ids:v})" />
   <NSelect v-else :virtual-scroll="false" :value="trigger.deny_trigger_ids" :disabled="disabled" multiple filterable :options="triggerOptions" placeholder="排除的触发器" @update:value="v=>updateTrigger({deny_trigger_ids:v})" />
  </fieldset>
  <NCheckbox v-if="!conversation" :checked="triggerEnabled" :disabled="disabled" @update:checked="v=>updateTrigger({enabled:v})">启用触发器场景</NCheckbox>
 </section>
</template>
<style scoped>
.scene-switches{display:flex;gap:24px;margin:12px 0;flex-wrap:wrap}.package-scene-rules{border-radius:var(--id-radius-md);background:var(--id-bg-surface);color:var(--id-text-primary)}
</style>

<script setup lang="ts">
import {inject,onUnmounted,ref,watch} from 'vue';
import {NAlert,NButton} from 'naive-ui';
import {agentsApi,type Agent} from '../../api/agents';
import AgentSetupPanel from '../../views/AgentSetupPanel.vue';
const props=defineProps<{workspace:string;employeeId:string;revision?:number;canManage:boolean}>();
const emit=defineEmits<{navigate:[target:string,param?:string,targetId?:string];dirty:[value:boolean];busy:[value:boolean]}>();
type Guard={hasUnsaved:()=>boolean;save:()=>Promise<boolean>;discard:()=>void;getLabel?:()=>string};
const guard=inject<{register:(g:Guard)=>void;unregister:(g:Guard)=>void}>('d2DraftGuard');
const dirty=ref(false),busy=ref(false);
const draftGuard:Guard={hasUnsaved:()=>dirty.value||busy.value,save:async()=>false,discard:()=>{dirty.value=false;busy.value=false;void load();},getLabel:()=>'插件账号配置'};guard?.register(draftGuard);
const agent=ref<Agent>(),error=ref('');let generation=0;let controller:AbortController|undefined;
async function load(){controller?.abort();controller=new AbortController();const g=++generation;agent.value=undefined;error.value='';try{const a=await agentsApi.get(props.workspace,props.employeeId,controller.signal);if(g===generation)agent.value=a;}catch(e){if(g===generation)error.value=e instanceof Error?e.message:'员工详情读取失败';}}
watch(()=>[props.workspace,props.employeeId,props.revision],()=>void load(),{immediate:true});
onUnmounted(()=>{++generation;controller?.abort();guard?.unregister(draftGuard);});
</script>
<template>
  <NAlert v-if="error" type="error">{{error}} <NButton size="small" @click="load">重新读取员工详情</NButton></NAlert>
  <template v-if="agent">
    <p v-if="agent.payer" class="reuse-payer-note">官方模型费用由 {{agent.payer.name}} 的积分账户支付。</p>
    <AgentSetupPanel :key="workspace+':'+agent.id" :agent="agent" :can-manage="canManage" @navigate="(t,p,id)=>emit('navigate',t,p,id)" @dirty="v=>{dirty=v;emit('dirty',v)}" @busy="v=>{busy=v;emit('busy',v)}" />
  </template>
</template>
<style scoped>
@import './workforce-shared.css';
.reuse-payer-note { margin: 4px 0 0; padding: 8px 12px; background: var(--id-bg-subtle); border: 1px solid var(--id-border-subtle); border-radius: var(--id-radius-md); font-size: 12px; color: var(--id-text-secondary); }
</style>

<script setup lang="ts">
import {computed, ref, watch} from 'vue';
import {NAlert, NButton, NInput, NSelect} from 'naive-ui';
import {allPages, defaultConversationScope, defaultTriggerScope, packagesApi, pluginsApi, type CapabilityPackage, type Plugin, type PluginAccount} from '../api/capabilities';
import type {Agent} from '../api/agents';
import type {Kind} from '../api/resource-catalog';
import PluginToolList from '../components/PluginToolList.vue';
import PlatformPluginAccount from './PlatformPluginAccount.vue';

const props=defineProps<{workspace:string; agents:Agent[]; kind:Kind; plugin:Plugin|undefined; pin:string; locked:boolean}>();
const emit=defineEmits<{
  target:[value:{agent_id:string;package_id:string;package_version:number}];busy:[boolean];dirty:[boolean]
}>();
const employee=ref(''), selected=ref(''), packages=ref<CapabilityPackage[]>([]);
const creating=ref(false), name=ref(''), loading=ref(false), error=ref('');
const selectedDefinition=ref<Plugin>();
const account=ref<PluginAccount>(), accountBusy=ref(false), dirty=ref(false);
let request=0;
async function load() {
  const current=++request;
  const agent=employee.value;
  selected.value=''; packages.value=[]; selectedDefinition.value=undefined; error.value='';
  if (!agent) {loading.value=false;return;}
  loading.value=true;
  try {
    const rows=await allPages(n=>packagesApi.list(props.workspace,agent,n));
    let definition=props.plugin;
    const bound=rows.flatMap(p=>p.plugins).find(b=>b.plugin_id===definition?.id);
    if(definition && bound && bound.version_id!==definition.version_id) {
      const history=await pluginsApi.versions(props.workspace,definition.id);
      definition=history.items.find(v=>v.version_id===bound.version_id);
    }
    if(current===request){packages.value=rows;selectedDefinition.value=definition;}
  }
  catch(e) {if(current===request) error.value=e instanceof Error?e.message:'读取失败';}
  finally {if(current===request) loading.value=false;}
}
watch(employee,()=>{account.value=undefined;name.value='';void load();});
watch([employee,selected,packages],()=>{
  const pack=packages.value.find(p=>p.id===selected.value);
  emit('target',{agent_id:employee.value,package_id:pack?.id || '',package_version:pack?.version || 0});
});
async function create() {
  if(!name.value.trim() || creating.value) return;
  creating.value=true; emit('busy',true); error.value='';
  try {
    const pack=await packagesApi.save(props.workspace,employee.value,{name:name.value.trim(),description:'',enabled:false,
      scope:{mode:'inherit_with_blacklist',allow_member_ids:[],deny_member_ids:[]},conversation_scope:defaultConversationScope(false),
      trigger_scope:{...defaultTriggerScope(),enabled:false},skills:[],plugins:[]});
    packages.value=[...packages.value,pack];selected.value=pack.id;name.value='';
  } catch(e) {error.value=e instanceof Error?e.message:'创建失败';}
  finally {creating.value=false;emit('busy',false);}
}
const locked=computed(()=>props.locked || creating.value || accountBusy.value || dirty.value);
</script>
<template>
  <label class="resource-form-label">员工
    <NSelect v-model:value="employee" :virtual-scroll="false" filterable :disabled="locked" :options="agents.map(a=>({label:a.name,value:a.id}))"/>
  </label>
  <template v-if="employee">
    <label class="resource-form-label">能力包
      <NSelect v-model:value="selected" :virtual-scroll="false" :disabled="locked || loading" :loading="loading" :options="packages.map(p=>({label:p.name+(p.enabled?'':'（已停用）'),value:p.id}))"/>
    </label>
    <NButton size="tiny" :disabled="locked || loading" @click="load">刷新能力包版本</NButton>
    <details><summary>新建能力包</summary>
      <p>新包默认停用。分配后可在员工的能力配置中设置使用范围并启用。</p>
      <NInput v-model:value="name" placeholder="能力包名称" :disabled="locked"/>
      <NButton :disabled="locked || !name.trim()" :loading="creating" @click="create">创建并选中</NButton>
    </details>
    <PlatformPluginAccount v-if="kind==='plugin' && selectedDefinition" :key="employee+selectedDefinition.version_id" :employee-id="employee" :plugin="selectedDefinition" :version-id="selectedDefinition.version_id" :can-manage="!props.locked"
      @account="value => account=value" @dirty="v=>{dirty=v;emit('dirty',v)}" @busy="v=>{accountBusy=v;emit('busy',v)}"/>
    <PluginToolList v-if="kind==='plugin'" :account="account" />
    <NAlert v-if="error" type="error">{{error}}</NAlert>
  </template>
</template>

<script setup lang="ts">
import {ref,watch} from 'vue';
import {NAlert,NButton,NDrawer,NDrawerContent} from 'naive-ui';
import {activeWorkspaceId} from '../api/client';
import {pluginsApi,type Plugin,type PluginAccount} from '../api/capabilities';
import PlatformPluginAccount from './PlatformPluginAccount.vue';
const props=defineProps<{plugin?:Plugin|undefined}>();
const emit=defineEmits<{close:[]}>();
const items=ref<PluginAccount[]>([]), target=ref<Plugin>(),error=ref(''),loading=ref(true);
let epoch=0;
async function load(){
 const current=++epoch;loading.value=true;error.value='';
 try{const result=await pluginsApi.shared(activeWorkspaceId.value);if(current===epoch)items.value=result.items}
 catch(e){if(current===epoch)error.value=e instanceof Error?e.message:'连接列表读取失败'}
 finally{if(current===epoch)loading.value=false}
}
async function edit(a:PluginAccount){
 const current=epoch;
 try{const versions=await pluginsApi.versions(activeWorkspaceId.value,a.plugin_id);if(current===epoch)target.value=versions.items.find(p=>p.version_id===a.plugin_version_id)}
 catch(e){if(current===epoch)error.value=e instanceof Error?e.message:'无法读取插件版本'}
}
watch(()=>[activeWorkspaceId.value,props.plugin],()=>{target.value=props.plugin;items.value=[];void load()},{immediate:true});
</script>
<template>
<NDrawer :show="true" :width="'min(580px,95vw)'" @update:show="emit('close')">
<NDrawerContent title="空间插件连接" closable>
<NAlert v-if="error" type="error">{{error}}</NAlert>
<template v-if="target"><h3>{{target.name}} · v{{target.version}}</h3>
<PlatformPluginAccount :key="target.version_id" employee-id="" :plugin="target" :version-id="target.version_id" :can-manage="true" @account="load"/>
<NButton @click="target=undefined">返回连接列表</NButton></template>
<template v-else><p v-if="loading">正在读取连接…</p><p v-else-if="!items.length">可在插件目录中选择「配置共享连接」。</p>
<div v-for="a in items" :key="a.id" class="resource-detail-row">
<strong>{{a.name}}</strong><span>{{a.enabled && a.validated?'已连接':'待连接'}} · {{a.agent_count || 0}} 位数字人</span><NButton size="small" @click="edit(a)">管理</NButton></div></template>
</NDrawerContent></NDrawer></template>

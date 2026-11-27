<script setup lang="ts">
import {computed,inject,onUnmounted,ref,watch} from 'vue';
import {NAlert,NButton,NInput,NSelect,NForm,NFormItem,NDrawer,NDrawerContent,NSteps,NStep,NPagination,NTag,NCheckbox,NPopconfirm,useMessage} from 'naive-ui';
import {activeWorkspaceId,D2ApiError} from '../api/client';
import {platformRequest} from '../api/platform';
import {templatesApi,emptyContent,renderInstruction,type AgentTemplate,type Content,type TemplateVersion} from '../api/templates';
import {allPages,packagesApi,skillsApi,pluginsApi,type Skill,type Plugin,type CapabilityPackage} from '../api/capabilities';
import {modelProfilesApi,choiceValue,modelOptions,modelSelection,type ModelChoice} from '../api/models';
import type {Agent} from '../api/agents';
import AssistantDemoCards from '../components/AssistantDemoCards.vue';
import type { AssistantCard } from '../api/assistant';
import TemplateIdentity from '../components/TemplateIdentity.vue';
const props=defineProps<{platformAdmin:boolean;assistantCards?:AssistantCard[] | undefined}>();
const emit=defineEmits<{navigate:[target:string,param?:string,targetId?:string]; assistantDemo:[card:AssistantCard]}>();
const message=useMessage();
const official=ref(true),manage=ref(false),q=ref(''),category=ref(''),sort=ref('recommended'),page=ref(1),total=ref(0),role=ref('');
const items=ref<AgentTemplate[]>([]),selected=ref<AgentTemplate>(),content=ref<Content>(emptyContent()),history=ref<TemplateVersion[]>([]);
const loading=ref(false),busy=ref(false),error=ref(''),panel=ref<'detail'|'edit'|'create'|''>(''),step=ref(1);
const models=ref<ModelChoice[]>([]),packages=ref<CapabilityPackage[]>([]),packageChoice=ref<string|null>(null);
const skills=ref<Skill[]>([]), plugins=ref<Plugin[]>([]);
const name=ref(''),model=ref(''),params=ref<Record<string,unknown>>({}),override=ref(''),editedInstruction=ref(false),baseline=ref(''),formError=ref('');
let epoch=0;let abort:AbortController|undefined;
const canManage=computed(()=>official.value?props.platformAdmin:['owner','admin'].includes(role.value));
const canCreate=computed(()=>['owner','admin','member'].includes(role.value));
const canManageWorkspaceTemplates=computed(()=>['owner','admin'].includes(role.value));
const categories=[{label:'全部分类',value:''},{label:'研发协作',value:'engineering'},{label:'运营营销',value:'marketing'},{label:'销售服务',value:'sales'},{label:'业务分析',value:'analytics'},{label:'通用办公',value:'office'}];
function categoryLabel(value: string) { return value ? categories.find(c => c.value === value)?.label : undefined; }
const rendered=computed(()=>selected.value?renderInstruction(selected.value.content,params.value):'');
const dirty=computed(()=>panel.value==='edit'&&JSON.stringify(content.value)!==baseline.value||panel.value==='create');
const guard=inject<{register:(v:typeof draft)=>void;unregister:(v:typeof draft)=>void}>('d2DraftGuard');
const draft={hasUnsaved:()=>dirty.value||busy.value,getLabel:()=> 'AI 员工模板',save:async()=>{if(panel.value==='edit'){await save();return !dirty.value}return false},discard:()=>{panel.value='';formError.value=''}};guard?.register(draft);
function close(){if(dirty.value&&!confirm('离开会丢弃尚未提交的内容，确定离开？'))return;panel.value='';formError.value=''}
async function load(){
 const current=++epoch;abort?.abort();abort=new AbortController();loading.value=true;error.value='';items.value=[];
 const w=activeWorkspaceId.value;
 try{
  const [ws,result]=await Promise.all([platformRequest<{role:string}>(`/workspaces/${w}`,{signal:abort.signal}),templatesApi.list(w,official.value,new URLSearchParams({q:q.value,category:category.value,sort_by:sort.value,page:String(page.value),page_size:'20',...(manage.value?{view:'manage'}:{})}),abort.signal)]);
  if(current!==epoch)return;role.value=ws.role;items.value=result.items;total.value=result.total;
 }catch(e){if(current===epoch)error.value=e instanceof Error?e.message:'读取失败'}finally{if(current===epoch)loading.value=false}
}
async function action(fn:()=>Promise<void>){if(busy.value)return;busy.value=true;formError.value='';const context=activeWorkspaceId.value+':'+official.value;try{await fn()}catch(e){if(context===activeWorkspaceId.value+':'+official.value)formError.value=e instanceof Error?e.message:'操作失败'}finally{if(context===activeWorkspaceId.value+':'+official.value)busy.value=false}}
function detail(t:AgentTemplate){selected.value=t;panel.value='detail';formError.value=''}
async function edit(t?:AgentTemplate){
 await action(async()=>{const current=epoch,w=activeWorkspaceId.value;
 const [v,p,s,m]=await Promise.all([t?templatesApi.get(w,official.value,t.id,true):Promise.resolve(undefined),allPages(n=>packagesApi.list(w,'',n)),allPages(n=>skillsApi.list(w,n)),allPages(n=>pluginsApi.list(w,n))]);
 if(current!==epoch)return;skills.value=s;plugins.value=m;packageChoice.value=null;selected.value=v;content.value=v?structuredClone(v.content):emptyContent();baseline.value=JSON.stringify(content.value);packages.value=p;history.value=v?(await templatesApi.versions(w,official.value,v.id)).items:[];if(current!==epoch)return;panel.value='edit';
 });
}
async function copyToWorkspace() {
 const source = selected.value;
 if (!source?.builtin_key || !canCreate.value) return;
 await action(async () => {
  const current = epoch;
  const workspace = activeWorkspaceId.value;
  await templatesApi.create(workspace, false, source.content);
  if (current !== epoch) return;
  panel.value = '';
  official.value = false;
  manage.value = true;
  q.value = '';
  category.value = '';
  message.success('已复制到工作区草稿，可编辑后发布');
 });
}
async function save(){await action(async()=>{const current=epoch;const v=selected.value?await templatesApi.update(activeWorkspaceId.value,official.value,selected.value,{content:content.value}):await templatesApi.create(activeWorkspaceId.value,official.value,content.value);if(current!==epoch)return;selected.value=v;content.value=structuredClone(v.content);baseline.value=JSON.stringify(content.value);message.success('已保存草稿，市场仍展示已发布版本');await load()})}
type Preset = Content['packages'][number];
type ResourceKind = 'skill'|'plugin';
function addResource(kind:ResourceKind,id:string,version:string) {
 const c=content.value;
 const existing=c.resource_refs.find(r=>r.kind===kind && r.id===id);
 if(existing)return existing.resource_key;
 const def=(kind==='skill'?skills.value:plugins.value).find(r=>r.id===id);
 const key='resource_'+crypto.randomUUID().replaceAll('-','');
 c.resource_refs.push({resource_key:key,kind,id,version_id:version,...(def?.scope==='official'?{}:{workspace_id:activeWorkspaceId.value})});
 return key;
}
function newPackage():Preset {
 const pack:Preset={package_key:'package_'+crypto.randomUUID().replaceAll('-',''),name:'',description:'',onboarding_requirement:'required',skills:[],plugins:[]};
 content.value.packages.push(pack);
 return pack;
}
function addPackage(){
 const p=packages.value.find(x=>x.id===packageChoice.value);if(!p)return;
 const pack=newPackage();
 pack.name=p.name;pack.description=p.description;
 pack.skills=p.skills.map(s=>({resource_key:addResource('skill',s.skill_id,s.version_id)}));
 pack.plugins=p.plugins.map(s=>({resource_key:addResource('plugin',s.plugin_id,s.version_id)}));
 packageChoice.value=null;
}
function selectedIds(pack:Preset, kind:ResourceKind) {
 const refs=content.value.resource_refs;
 return (kind==='skill'?pack.skills:pack.plugins).map(pin=>refs.find(r=>r.resource_key===pin.resource_key)?.id || pin.resource_key);
}
function options(pack:Preset,kind:ResourceKind) {
 const defs=kind==='skill'?skills.value:plugins.value;
 const selected=selectedIds(pack,kind);
 const items=defs.filter(d=>d.enabled && d.listed!==false || selected.includes(d.id)).map(d=>({value:d.id,label:d.name}));
 for(const id of selected) if(!items.some(item=>item.value===id))items.push({value:id,label:'已绑定资源（不可添加）'});
 return items;
}
function pruneResources() {
 const used=new Set(content.value.packages.flatMap(p=>[...p.skills,...p.plugins].map(pin=>pin.resource_key)));
 content.value.resource_refs=content.value.resource_refs.filter(r=>used.has(r.resource_key));
}
function setResources(pack:Preset,kind:ResourceKind,ids:string[]) {
 const keys=ids.map(id=>{
  const existing=content.value.resource_refs.find(r=>r.kind===kind && (r.id===id || r.resource_key===id));
  if(existing)return existing.resource_key;
  const def=kind==='skill'?skills.value.find(s=>s.id===id):plugins.value.find(p=>p.id===id);
  if(!def)throw new Error('资源信息已变更，请重新打开模板');
  return addResource(kind,id,'current_version_id' in def?def.current_version_id:def.version_id);
 });
 const pins=keys.map(resource_key=>({resource_key}));
 if(kind==='skill')pack.skills=pins;
 else pack.plugins=pins;
 pruneResources();
}
function removePackage(pack:Preset) {
 content.value.packages=content.value.packages.filter(p=>p.package_key!==pack.package_key);
 for(const ex of content.value.examples)ex.package_keys=ex.package_keys.filter(k=>k!==pack.package_key);
 pruneResources();
}
async function start(){
 if(!selected.value||!canCreate.value)return;
 await action(async()=>{const current=epoch;const result=await allPages(n=>modelProfilesApi.choices(activeWorkspaceId.value,n));if(current!==epoch)return;models.value=result;const preferred=result.find(m=>m.enabled&&m.validated&&m.is_default)||result.find(m=>m.enabled&&m.validated);model.value=preferred?choiceValue(preferred):'';name.value=selected.value!.content.name;params.value=Object.fromEntries(selected.value!.content.parameters.map(p=>[p.key,p.default??(p.type==='multi_select'?[]:'')]));editedInstruction.value=false;override.value='';step.value=1;panel.value='create'})
}
function next(){
 formError.value='';
 if(step.value===1){if(!name.value.trim()){formError.value='请填写员工名称';return};for(const p of selected.value?.content.parameters||[]){const v=params.value[p.key];if(p.required&&(!v||Array.isArray(v)&&!v.length)){formError.value='请填写：'+(p.label||p.key);return}}}
 if(step.value===2&&!models.value.some(m=>choiceValue(m)===model.value&&m.enabled&&m.validated)){formError.value='请选择可用模型';return}
 step.value++;
}
async function publish(){await action(async()=>{if(!selected.value)return;const current=epoch;const v=await templatesApi.publish(activeWorkspaceId.value,official.value,selected.value);if(current!==epoch)return;selected.value=v;content.value=structuredClone(v.content);baseline.value=JSON.stringify(content.value);message.success('已发布');await load()})}
async function listing(){await action(async()=>{if(!selected.value)return;const current=epoch;const v=await templatesApi.update(activeWorkspaceId.value,official.value,selected.value,{is_listed:!selected.value.is_listed});if(current!==epoch)return;selected.value=v;await load()})}
async function remove(){await action(async()=>{if(!selected.value)return;const current=epoch;await templatesApi.remove(activeWorkspaceId.value,official.value,selected.value);if(current!==epoch)return;panel.value='';await load()})}
async function tryMarket(){
 const workspace=activeWorkspaceId.value, template=selected.value?.id;
 await start();
 if(workspace!==activeWorkspaceId.value || !template || template!==selected.value?.id || panel.value!=='create')return;
 if(!model.value){step.value=2;formError.value='请先配置一个可用模型';return}
 await create(true);
}
async function create(requiredPackagesOnly=false){
 if(!selected.value)return;
 await action(async()=>{const current=epoch,w=activeWorkspaceId.value,t=selected.value!;
 let employee:Agent;
 try{employee=await templatesApi.createAgent(w,{...(requiredPackagesOnly===true?{required_packages_only:true}:{}),template_id:t.id,template_version_id:t.template_version_id,name:name.value,...modelSelection(model.value),parameters:params.value,...(editedInstruction.value?{instruction_override:override.value}:{})},AbortSignal.timeout(60000))}
 catch(e){if(!(e instanceof D2ApiError)){throw new Error('创建结果暂未确认，请先到 AI 员工列表核对，避免重复创建。')}throw e}
 if(current!==epoch)return;panel.value='';busy.value=false;message.success('员工已创建，请完成账号接入并试用');emit('navigate','employees',undefined,employee.id)
 })
}
watch([official,manage,category,sort],()=>{page.value=1;panel.value='';void load()});
watch(page,()=>void load());
watch(activeWorkspaceId,()=>{++epoch;panel.value='';manage.value=false;role.value='';busy.value=false;page.value=1;void load()},{immediate:true});
onUnmounted(()=>{++epoch;abort?.abort();guard?.unregister(draft)});
</script>
<template>
<section class="market">
<header><div><h1>AI 员工市场</h1><p>选择岗位模板，查看交付示例，创建团队需要的 AI 员工。</p></div><div><NButton v-if="canManage" @click="manage=!manage">{{manage?'返回市场':'管理模板'}}</NButton><NButton v-if="manage&&canManage" type="primary" @click="edit()">新建模板</NButton></div></header>
<section v-if="assistantCards?.length && !manage" aria-label="体验 AI 员工">
  <h2>体验 AI 员工</h2>
  <AssistantDemoCards :cards="assistantCards" @choose="card => emit('assistantDemo', card)" />
</section>
<nav><NButton :type="official?'primary':'default'" @click="official=true;manage=false">官方精选</NButton><NButton :type="!official?'primary':'default'" @click="official=false;manage=false">工作区模板</NButton></nav>
<div class="filters"><NInput v-model:value="q" placeholder="搜索岗位、职责或交付物" @keyup.enter="page=1;load()" /><NButton @click="page=1;load()">搜索</NButton><NSelect v-model:value="category" :options="categories"/><NSelect v-model:value="sort" :options="[{label:'推荐排序',value:'recommended'},{label:'最近发布',value:'published_at'}]"/></div>
<NAlert v-if="error" type="error">{{error}}<NButton @click="load">重试</NButton></NAlert><p v-else-if="loading">正在读取模板…</p><div v-else-if="!items.length" class="empty"><h2>{{manage?'还没有模板':'暂无匹配的岗位'}}</h2><p>可以调整筛选，或由管理员制作工作区模板。</p></div>
<div v-else class="grid">
  <article v-for="t in items" :key="t.id" class="template-card">
    <TemplateIdentity :template="t" :category="categoryLabel(t.content.category)" />
    <div class="card-body">
      <p class="role">{{ t.content.role || '岗位介绍待完善' }}</p>
      <div v-if="t.content.deliverables" class="deliverables">
        <h3>主要交付物</h3>
        <p>{{ t.content.deliverables }}</p>
      </div>
      <div v-if="t.content.packages.length" class="chips">
        <NTag v-for="p in t.content.packages" :key="p.package_key" size="small">{{ p.name }}</NTag>
      </div>
      <div v-if="manage" class="status">
        <span v-if="t.builtin_key">随 KIKO 版本维护 · </span>{{ t.status==='draft'?'未发布':t.is_listed?'已发布':'已下架' }}<span v-if="t.has_unpublished_changes"> · 有未发布修改</span>
      </div>
    </div>
    <div class="card-actions">
      <span>{{ t.scope==='official'?'官方模板':'工作区模板' }} · v{{ t.sequence }}</span>
      <NButton secondary size="small" @click="manage && !t.builtin_key ? edit(t) : detail(t)">{{ manage && !t.builtin_key ? '编辑模板' : '查看详情' }}</NButton>
    </div>
  </article>
</div>
<NPagination v-if="total>20" v-model:page="page" :item-count="total" :page-size="20"/>
<NDrawer :show="!!panel" :width="'min(900px,96vw)'" :mask-closable="false" :close-on-esc="false"><NDrawerContent :title="panel==='edit'?'制作 AI 员工模板':panel==='create'?'创建 AI 员工':(selected?.content.name||'模板详情')"><template #header><strong>{{panel==='edit'?'制作模板':panel==='create'?'创建员工':'模板详情'}}</strong><NButton quaternary :disabled="busy" @click="close">关闭</NButton></template>
<NAlert v-if="formError" type="error">{{formError}}</NAlert>
<template v-if="panel==='detail'&&selected">
  <TemplateIdentity class="detail-identity" :template="selected" :category="categoryLabel(selected.content.category)" />
  <p class="detail-role">{{ selected.content.role }}</p><p v-if="selected.builtin_key">随 KIKO 版本维护 <NButton v-if="canManageWorkspaceTemplates" size="small" :disabled="busy" @click="copyToWorkspace">复制为工作区模板</NButton></p><div v-if="selected.content.deliverables" class="deliverables"><h3>主要交付物</h3><p>{{ selected.content.deliverables }}</p></div>
  <h3 v-if="selected.content.packages.length">预设能力</h3><article v-for="p in selected.content.packages" :key="p.package_key"><h4>{{p.name}} <NTag size="small">模板预设</NTag></h4><p>{{p.description}}</p><small>{{p.onboarding_requirement==='deferred'?'相关系统可稍后接入':'使用此能力前需完成相关接入'}}</small></article><h3>创建前需要</h3><p>{{selected.content.model_requirements||'选择工作区中已验证且启用的模型。'}}</p><h3 v-if="selected.content.examples.length">试着交给它</h3><article v-for="ex in selected.content.examples" :key="ex.key"><h4>{{ex.title}}</h4><pre>{{ex.text}}</pre><h4 v-if="ex.output">交付示例</h4><pre v-if="ex.output" class="example-output">{{ex.output}}</pre></article><details><summary>查看岗位指令</summary><pre>{{selected.content.instruction_template}}</pre></details><NAlert type="info">创建后独立维护，模板更新不会自动覆盖。能力已全部预设，账号在创建后连接。</NAlert><NButton v-if="selected.builtin_key==='market-reporter' && canCreate && selected.is_listed" :disabled="busy" @click="tryMarket">一键体验免认证行情</NButton><NButton type="primary" :disabled="!canCreate||!selected.is_listed||busy" @click="start">{{canCreate?'用此模板创建':'创建员工需要有效工作区成员身份'}}</NButton></template>
<template v-if="panel==='edit'"><p v-if="selected">当前编辑 v{{selected.sequence}} · {{selected.published_version_id?'已有线上版本':'尚未发布'}}，保存后仍需点击发布。</p><NForm :disabled="busy" label-placement="top"><NFormItem label="岗位名称"><NInput v-model:value="content.name" maxlength="100"/></NFormItem><NFormItem label="职责简介"><NInput v-model:value="content.role" type="textarea"/></NFormItem><NFormItem label="分类"><NSelect v-model:value="content.category" :options="categories.slice(1)"/></NFormItem><NFormItem label="适用对象"><NInput v-model:value="content.audience"/></NFormItem><NFormItem label="主要交付物"><NInput v-model:value="content.deliverables"/></NFormItem><NFormItem label="岗位指令"><NInput v-model:value="content.instruction_template" type="textarea" :autosize="{minRows:6}"/></NFormItem>
<h3>岗位参数</h3><p>参数标识可在岗位指令中作为双花括号占位符使用。</p><article v-for="(p,i) in content.parameters" :key="i"><NInput v-model:value="p.key" placeholder="参数标识，例如 project_scope"/><NInput v-model:value="p.label" placeholder="显示名称"/><NInput v-model:value="p.description" placeholder="填写说明"/><NSelect v-model:value="p.type" :options="[{label:'短文本',value:'short_text'},{label:'长文本',value:'long_text'},{label:'单选',value:'single_select'},{label:'多选',value:'multi_select'}]"/><NSelect v-if="p.type.includes('select')" :value="p.options||[]" @update:value="v=>p.options=v" multiple filterable tag placeholder="输入可选值"/><NInput v-if="!p.type.includes('select')" :value="typeof p.default==='string'?p.default:''" @update:value="v=>{if(v)p.default=v;else delete p.default}" placeholder="默认值（可选）"/><NCheckbox v-model:checked="p.required">必填</NCheckbox><NButton size="small" @click="content.parameters.splice(i,1)">移除参数</NButton></article><NButton @click="content.parameters.push({key:'',label:'',description:'',type:'short_text',required:true})">添加参数</NButton>
<h3>模板预设能力</h3>
<p>新建能力包并添加技能或插件，也可以复用工作区能力包模板。</p>
<div class="filters">
  <NButton @click="newPackage()">新建能力包</NButton>
  <template v-if="packages.length">
    <NSelect v-model:value="packageChoice" :options="packages.map(p=>({label:p.name,value:p.id}))" placeholder="选择工作区能力包模板"/>
    <NButton :disabled="!packageChoice" @click="addPackage">加入能力</NButton>
  </template>
</div>
<article v-for="p in content.packages" :key="p.package_key">
  <NInput v-model:value="p.name" placeholder="能力名称" maxlength="100"/>
  <NInput v-model:value="p.description" type="textarea" placeholder="用途和输入产出"/>
  <NSelect v-model:value="p.onboarding_requirement" :options="[{label:'使用前需接入',value:'required'},{label:'可稍后接入',value:'deferred'}]"/>
  <NFormItem label="技能">
    <NSelect multiple filterable :virtual-scroll="skills.length>50" :value="selectedIds(p,'skill')" :options="options(p,'skill')"
      @update:value="ids=>setResources(p,'skill',ids)" placeholder="选择技能"/>
  </NFormItem>
  <NFormItem label="MCP 插件">
    <NSelect multiple filterable :virtual-scroll="plugins.length>50" :value="selectedIds(p,'plugin')" :options="options(p,'plugin')"
      @update:value="ids=>setResources(p,'plugin',ids)" placeholder="选择插件"/>
  </NFormItem>
  <NButton size="small" @click="removePackage(p)">移除此能力</NButton>
</article>
<NFormItem label="模型与接入要求"><NInput v-model:value="content.model_requirements" type="textarea"/></NFormItem><h3>示例与交付</h3><article v-for="(ex,i) in content.examples" :key="i"><NInput v-model:value="ex.title" placeholder="示例标题"/><NInput v-model:value="ex.text" type="textarea" placeholder="可直接委派的任务"/><NInput v-model:value="ex.output" type="textarea" placeholder="脱敏或虚构交付样例"/><NSelect v-model:value="ex.package_keys" multiple :options="content.packages.map(p=>({label:p.name,value:p.package_key}))" placeholder="此示例需要哪些能力"/><NButton @click="content.examples.splice(i,1)">移除示例</NButton></article><NButton @click="content.examples.push({key:'example_'+Date.now(),title:'',text:'',output:'',package_keys:[]})">添加示例</NButton>
</NForm><footer><NButton type="primary" :loading="busy" @click="save">保存草稿</NButton><NButton v-if="selected" :disabled="dirty||busy" @click="publish">{{selected.is_listed?'发布新版本':'发布并上架'}}</NButton><NButton v-if="selected?.published_version_id" :disabled="dirty||busy" @click="listing">{{selected.is_listed?'下架':'重新上架原发布版'}}</NButton><NPopconfirm v-if="selected&&!selected.published_version_id" @positive-click="remove"><template #trigger><NButton :disabled="busy">删除未发布模板</NButton></template>删除这个未发布模板及保存历史？</NPopconfirm></footer><details v-if="history.length"><summary>保存历史</summary><p v-for="v in history" :key="v.id">v{{v.sequence}} · {{v.published_at?'曾发布':'未发布'}} {{v.change_note}}</p></details></template>
<template v-if="panel==='create'&&selected"><NSteps :current="step" size="small"><NStep title="岗位设定"/><NStep title="模型与接入"/><NStep title="确认创建"/></NSteps><NForm :disabled="busy" label-placement="top"><div v-if="step===1"><h3>{{selected.content.name}} · v{{selected.sequence}}</h3><NFormItem label="员工名称"><NInput v-model:value="name" maxlength="100"/></NFormItem><NFormItem v-for="p in selected.content.parameters" :key="p.key" :label="p.label||p.key" :required="p.required"><NSelect v-if="p.type.includes('select')" :value="params[p.key] as string|string[]" @update:value="v=>params[p.key]=v" :multiple="p.type==='multi_select'" :options="(p.options||[]).map(x=>({label:x,value:x}))"/><NInput v-else :value="String(params[p.key]??'')" @update:value="v=>params[p.key]=v" :type="p.type==='long_text'?'textarea':'text'" :placeholder="p.description"/></NFormItem><details><summary>查看岗位指令</summary><pre>{{rendered}}</pre><NCheckbox :checked="editedInstruction" @update:checked="v=>{editedInstruction=v;if(v)override=rendered}">自定义这位员工的岗位指令</NCheckbox><NInput v-if="editedInstruction" v-model:value="override" type="textarea" :autosize="{minRows:6}"/></details></div><div v-if="step===2"><h3>选择模型，了解接入要求</h3><NSelect v-model:value="model" :options="modelOptions(models)" placeholder="选择已验证并启用的模型"/><p>{{selected.content.model_requirements}}</p><p v-if="model.startsWith('platform:')">该员工使用官方模型产生的费用将从工作区所有者的积分账户扣除。</p><h3>模板已预设的能力</h3><article v-for="p in selected.content.packages" :key="p.package_key"><NTag>模板预设</NTag><h4>{{p.name}}</h4><p>{{p.description}}</p><p>{{p.plugins.length?'创建后连接相关业务系统':'无额外业务账号要求'}} · {{p.onboarding_requirement==='deferred'?'可稍后接入':'使用此能力前完成接入'}}</p></article><NAlert v-if="!models.some(m=>m.enabled&&m.validated)" type="warning">请先在资源库配置一个可用模型。表单内容会保留。</NAlert></div><div v-if="step===3"><h3>确认创建 {{name}}</h3><p>来源：{{selected.content.name}} v{{selected.sequence}}</p><p>模型：{{models.find(m=>choiceValue(m)===model)?.name}}</p><p>能力：{{selected.content.packages.map(p=>p.name).join('、')}}</p><NAlert type="info">使用范围默认全员可用，黑名单为空。创建后可在员工设置中调整范围和连接业务账号。</NAlert><pre>{{editedInstruction?override:rendered}}</pre></div></NForm><footer><NButton v-if="step>1" :disabled="busy" @click="step--">上一步</NButton><NButton v-if="step<3" type="primary" @click="next">下一步</NButton><NButton v-else type="primary" :loading="busy" :disabled="busy" @click="create()">创建员工</NButton></footer></template>
</NDrawerContent></NDrawer>
</section>
</template>
<style scoped>
.market { padding: 28px; max-width: 1350px; margin: auto; }
.market header, .card-actions { display: flex; align-items: center; justify-content: space-between; gap: 12px; }
.market header h1 { margin: 0; font-size: 28px; }
.market header p { margin: 4px 0 0; }
p { color: #526071; line-height: 1.65; }
nav, .filters, footer { display: flex; gap: 12px; margin: 20px 0; }
.filters .n-select { min-width: 150px; max-width: 230px; }
.grid { display: grid; grid-template-columns: repeat(auto-fill, minmax(min(100%, 280px), 1fr)); gap: 18px; }
article { border: 1px solid #dfe5ec; border-radius: 14px; background: white; padding: 20px; margin: 12px 0; }
.template-card { display: flex; flex-direction: column; min-width: 0; margin: 0; padding: 0; overflow: hidden; box-shadow: 0 2px 6px #19304808; }
.card-body { flex: 1; padding: 16px 20px; overflow-wrap: anywhere; }
.role, .deliverables p { margin: 0; }
.deliverables { border-left: 2px solid #bbcbd3; padding-left: 11px; margin: 14px 0 0; overflow-wrap: anywhere; }
.deliverables h3 { margin: 0 0 3px; font-size: 12px; color: #576576; }
.deliverables p { color: #28394a; }
.chips { display: flex; flex-wrap: wrap; gap: 6px; margin-top: 12px; }
.status, .card-actions { color: #596575; font-size: 12px; }
.status { margin-top: 12px; }
.card-actions { border-top: 1px solid #edf0f4; padding: 10px 20px; }
.detail-identity { border-radius: 12px; }
.detail-role { font-size: 16px; }
.empty { text-align: center; padding: 48px 14px; }
pre { white-space: pre-wrap; overflow-wrap: anywhere; background: #f5f7fa; padding: 15px; border-radius: 9px; }
.example-output { background: #f0f6f4; }
footer { flex-wrap: wrap; position: sticky; bottom: 0; background: white; padding: 16px 0; }
.n-drawer-content article .n-input, .n-drawer-content article .n-select { margin-bottom: 10px; }
@media (max-width: 700px) {
  .market { padding: 16px; }
  .market header { flex-wrap: wrap; }
  .market > .filters { display: grid; grid-template-columns: minmax(0, 1fr) auto; gap: 8px; }
  .market > .filters .n-select { min-width: 0; max-width: none; }
  .market > .filters .n-select:last-child { min-width: 104px; }
  .grid { gap: 14px; }
}
</style>

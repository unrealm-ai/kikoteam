<script setup lang="ts">
import {computed, onMounted, ref, watch} from 'vue';
import {NInput, NAlert, NButton, NDrawer, NDrawerContent, NSelect, NPagination, NPopconfirm, useMessage} from 'naive-ui';
import {activeWorkspaceId} from '../api/client';
import {agentsApi, type Agent} from '../api/agents';
import {allPages} from '../api/capabilities';
import {catalogApi, versionOf, type Kind, type Item, type Use} from '../api/resource-catalog';
import AssignmentTarget from './ResourceAssignmentTarget.vue';

const props = defineProps<{kind:Kind; item:Item; canManage:boolean; platformAdmin:boolean}>();
const emit = defineEmits<{close:[]; changed:[]}>();
const workspace = activeWorkspaceId.value;
const official = computed(() => props.item.scope === 'official');
const pin = computed(() => versionOf(props.item));
const officialClient=ref(""),officialSecret=ref("");
const pinnedPlugin = computed(() => 'version_id' in props.item ? props.item : undefined);
const pinnedSkill = computed(() => 'current_version' in props.item ? props.item.current_version : undefined);
const agents = ref<Agent[]>([]), officials = ref<Item[]>([]), destination = ref('');
const uses = ref<Use[]>([]), page = ref(1), total = ref(0);
const busy = ref(false), loading = ref(true), error = ref(''), message = useMessage();
const statusLabels:Record<string,string> = {
  assigned:'已分配', already_assigned:'已分配过', plugin_authorization_required:'请验证该员工的插件账号',
  plugin_tools_required:'请选择允许使用的工具', package_version_conflict:'能力包已变更，请重新选择',
  plugin_tool_changed:'工具已变更，请重新验证', skill_unavailable:'Skill 不可用', plugin_unavailable:'插件不可用', not_found:'目标不存在'
};
type Target = {key:number; agent_id:string; package_id:string; package_version:number; status:string; pending:boolean; dirty:boolean};
let key = 0;
function newTarget():Target {return {key:++key,agent_id:'',package_id:'',package_version:0,status:'',pending:false,dirty:false};}
const targets = ref<Target[]>([newTarget()]);
const editingAccount = computed(() => targets.value.some(t => t.pending || t.dirty));
const submitted = (t:Target) => t.status === 'assigned' || t.status === 'already_assigned';
const pendingTargets = computed(() => targets.value.filter(t => !submitted(t)));
const assignable = computed(() => pendingTargets.value.length > 0 && pendingTargets.value.every(t => t.package_id && t.package_version)
  && new Set(pendingTargets.value.map(t => t.package_id)).size === pendingTargets.value.length);
let useRequest = 0;
async function loadUses() {
  if (!props.canManage) return;
  const request = ++useRequest;
  const result = await catalogApi.uses(workspace, props.kind, props.item.id, page.value);
  if (request === useRequest) { uses.value = result.items; total.value = result.total; }
}
function explain(e:unknown) {error.value = e instanceof Error ? e.message : '操作失败，请重试';}
async function load() {
  loading.value = true; error.value = '';
  try {
    await Promise.all([
      props.canManage && allPages(n => agentsApi.list(workspace,n)).then(v => {agents.value=v;}),
      props.platformAdmin && props.canManage && !official.value && allPages(n => catalogApi.officials(props.kind,n)).then(v => {officials.value=v;}),
      loadUses()
    ]);
  } catch (e) {explain(e);} finally {loading.value=false;}
}
onMounted(load);
watch(page, () => {void loadUses().catch(explain);});
watch(pin, () => {targets.value=[newTarget()];});
async function act(action:()=>Promise<unknown>, success:string) {
  if (busy.value) return;
  busy.value=true; error.value='';
  try {await action(); message.success(success); emit('changed'); emit('close');}
  catch(e) {explain(e);} finally {busy.value=false;}
}
const copy = () => act(() => catalogApi.copy(workspace,props.kind,props.item.id,pin.value),'已创建独立的空间副本');
const publish = () => act(() => catalogApi.publish(workspace,props.kind,props.item,pin.value,officials.value.find(i=>i.id===destination.value),pinnedPlugin.value?.oauth?{issuer:pinnedPlugin.value.oauth.issuer,resource:pinnedPlugin.value.oauth.resource ?? pinnedPlugin.value.endpoint,scopes:pinnedPlugin.value.oauth.scopes,client_id:officialClient.value,client_secret:officialSecret.value}:undefined),'已发布官方版本');
const listing = () => act(() => catalogApi.state(props.kind,props.item,{listed:props.item.listed===false}),'官方目录已更新');
const toggle = () => act(() => catalogApi.state(props.kind,props.item,{enabled:!props.item.enabled}),'官方状态已更新');
const archive = () => act(() => catalogApi.archive(workspace,props.kind,props.item),'空间资源已归档');
async function assign() {
  if (!assignable.value || busy.value || editingAccount.value) return;
  busy.value=true; error.value='';
  try {
    const payload=pendingTargets.value.map(({agent_id,package_id,package_version}) => ({agent_id,package_id,package_version}));
    const result=await catalogApi.assign(workspace,props.kind,props.item.id,pin.value,payload);
    for (const target of targets.value) {
      const match=result.items.find(r => r.package_id===target.package_id && r.agent_id===target.agent_id);
      if (match) target.status=match.status;
    }
    emit('changed'); await loadUses();
  } catch(e) {explain(e);} finally {busy.value=false;}
}
function close() {
  if (!busy.value && !targets.value.some(t=>t.pending) && (!editingAccount.value || window.confirm('放弃未保存的账号修改？'))) emit('close');
}
</script>
<template>
  <NDrawer :show="true" width="min(620px,95vw)" :mask-closable="!busy" @update:show="v => {if(!v)close()}">
    <NDrawerContent :title="item.name" closable>
      <div class="catalog-sections">
        <NAlert v-if="error" type="error">{{error}} <NButton size="tiny" @click="load">重新读取</NButton></NAlert>
        <p v-if="pinnedSkill || pinnedPlugin">最新版本：{{ pinnedSkill?.sequence ?? pinnedPlugin?.definition_version }}</p>
        <details v-if="pinnedSkill"><summary>版本文件（{{pinnedSkill.manifest.files.length}}）</summary>
          <ul><li v-for="file in pinnedSkill.manifest.files" :key="file.path">{{file.path}} · {{file.size}} B</li></ul>
        </details>
        <section v-if="canManage">
          <h4>本空间已绑定的能力包</h4>
          <p v-if="!uses.length && !loading">暂无绑定</p>
          <ul v-else><li v-for="use in uses" :key="use.package_id">{{use.agent_name}} · {{use.package_name}} · {{use.enabled?'已启用':'已停用'}}
            <small v-if="kind==='plugin'">{{use.account_ready?'已连接':'待连接'}}</small>
          </li></ul>
          <NPagination v-if="total>20" v-model:page="page" :item-count="total" :page-size="20"/>
        </section>
        <section v-if="canManage && item.enabled && item.listed !== false && !loading">
          <h4>分配给员工</h4>
          <fieldset v-for="target in targets" :key="target.key" :disabled="busy || submitted(target)">
            <AssignmentTarget :agents="agents" :kind="kind" :plugin="pinnedPlugin" :pin="pin" :workspace="workspace"
              :locked="busy || submitted(target)" @target="v=>Object.assign(target,v,{status:''})"
              @busy="v=>target.pending=v" @dirty="v=>target.dirty=v"/>
            <p v-if="target.status" role="status">{{statusLabels[target.status] || target.status}}</p>
            <NButton v-if="targets.length>1 && !submitted(target)" size="tiny" :disabled="busy || target.pending || target.dirty" @click="targets=targets.filter(t=>t.key!==target.key)">移除此目标</NButton>
          </fieldset>
          <div class="resource-actions"><NButton :disabled="busy || targets.length>=100" @click="targets.push(newTarget())">添加目标</NButton>
            <NButton type="primary" :loading="busy" :disabled="!assignable || editingAccount" @click="assign">确认分配</NButton></div>
        </section>
        <section v-if="!loading" class="catalog-sections">
          <NButton v-if="canManage && official && item.enabled && item.listed!==false" :disabled="busy" @click="copy">复制此版本为空间自建资源</NButton>
          <template v-if="platformAdmin && canManage && !official && item.enabled">
            <label class="resource-form-label">发布到官方目录
              <NSelect v-model:value="destination" :options="[{label:'新建官方资源',value:''},...officials.map(i=>({label:i.name,value:i.id}))]"/>
            </label>
            <template v-if="pinnedPlugin?.auth_type==='oauth'"><label>官方应用客户端 ID<NInput v-model:value="officialClient" /></label><label>官方应用客户端密钥<NInput v-model:value="officialSecret" type="password" autocomplete="new-password" /></label></template>
            <NPopconfirm @positive-click="publish">
              <template #trigger><NButton :disabled="busy">发布最新版本</NButton></template>此版本将供所有空间使用，确认发布？
            </NPopconfirm>
          </template>
          <template v-if="platformAdmin && official">
            <NButton :disabled="busy" @click="listing">{{item.listed===false?'上架':'下架'}}官方资源</NButton>
            <NPopconfirm @positive-click="toggle">
              <template #trigger><NButton :disabled="busy">{{item.enabled?'停用':'启用'}}官方资源</NButton></template>此状态会影响所有空间的既有绑定。
            </NPopconfirm>
          </template>
          <NPopconfirm v-if="canManage && !official" @positive-click="archive">
            <template #trigger><NButton :disabled="busy">归档空间资源</NButton></template>确认归档此资源？
          </NPopconfirm>
        </section>
      </div>
    </NDrawerContent>
  </NDrawer>
</template>
<style scoped>
.catalog-sections,fieldset{display:grid;gap:14px}fieldset{margin:10px 0;padding:12px;border:1px solid var(--border-subtle,#ddd);border-radius:8px}
h4{margin:0 0 8px}ul{padding-left:20px}small{display:block;overflow-wrap:anywhere}
</style>

<script setup lang="ts">
import { computed, onUnmounted, ref, watch } from 'vue';
import { NAlert, NButton, useMessage } from 'naive-ui';
import { activeWorkspaceId } from '../api/client';
import { platformRequest } from '../api/platform';
import type { Agent } from '../api/agents';
import { templatesApi, renderInstruction, type Setup, type Example } from '../api/templates';
import { pluginsApi, type Plugin } from '../api/capabilities';
import PlatformPluginAccount from './PlatformPluginAccount.vue';

const props = defineProps<{ agent: Agent; canManage: boolean; canManageTemplates: boolean }>();
const emit = defineEmits<{ updated: [value: Agent]; navigate: [target: string, param?: string, targetId?: string]; dirty: [value: boolean]; busy: [value: boolean] }>();
const message = useMessage(), setup = ref<Setup>(), error = ref(''), busy = ref(false), definitions = ref<{ plugin: Plugin; version: string }[]>([]);
let epoch = 0;
const accountDirty = ref<Record<string, boolean>>({});
const accountBusy = ref<Record<string, boolean>>({});
watch(() => Object.values(accountDirty.value).some(Boolean), v => emit('dirty', v));
watch(() => busy.value || Object.values(accountBusy.value).some(Boolean), v => emit('busy', v));
async function load() {
  const current = ++epoch; const w = activeWorkspaceId.value; error.value = ''; setup.value = undefined; definitions.value = [];
  try {
    const s = await templatesApi.setup(w, props.agent.id);
    const defs: { plugin: Plugin; version: string }[] = [];
    const seen = new Set<string>();
    for (const p of s.packages) {
      for (const pin of p.plugins) {
        if (seen.has(pin.plugin_id)) continue;
        seen.add(pin.plugin_id);
        const versions = await pluginsApi.versions(w, pin.plugin_id);
        const plugin = versions.items.find(x => x.version_id === pin.version_id);
        if (plugin) defs.push({ plugin, version: pin.version_id });
      }
    }
    if (current !== epoch) return;
    setup.value = s; definitions.value = defs;
  } catch (e) { if (current === epoch) error.value = e instanceof Error ? e.message : '接入状态读取失败'; }
}
async function bindSelf() {
  if (!props.canManage || busy.value) return;
  const current=epoch; busy.value=true;
  try {
    const updated=await platformRequest<Agent>('/workspaces/'+activeWorkspaceId.value+'/agents/'+props.agent.id+'/payer-binding',{method:'PUT',body:JSON.stringify({confirm_self:true,version:props.agent.version})});
    if(current===epoch) emit('updated',updated);
  } catch(e){if(current===epoch)error.value=e instanceof Error?e.message:'付款绑定失败';}
  finally {busy.value=false;}
}
async function saveAs() {
  if (busy.value || !props.canManageTemplates) return;
  busy.value = true; const current = epoch;
  try {
    await templatesApi.fromAgent(activeWorkspaceId.value, props.agent);
    if (current !== epoch) return;
    message.success('已另存为工作区模板草稿');
    busy.value = false; emit('busy', false); emit('navigate', 'market');
  } catch (e) { if (current === epoch) error.value = e instanceof Error ? e.message : '保存失败'; }
  finally { if(current === epoch) busy.value = false; }
}
const issues = computed(() => setup.value?.issues || []);
const labels: Record<string, string> = {
  available:'可用',unconfigured:'等待平台配置',payer_binding_required:'待绑定有效付款账户',needs_connection: '需要连接账号',
  missing_operation: '缺少预设操作',
  schema_changed: '工具定义已变化',
  resource_unavailable: '资源不可用',
  disabled: '能力已停用'
};
function canTry(ex: Example) { return !!setup.value && setup.value.access_allowed === true && setup.value.conversation_model.available === true && setup.value.task_model.available === true && ex.can_try === true; }
function tryTask(ex: Example) {
  if (!canTry(ex)) return;
  const text = ex.text;
  const p = props.agent.template_origin?.parameters || {};
  const rendered = renderInstruction({ instruction_template: text } as Parameters<typeof renderInstruction>[0], p);
  sessionStorage.setItem('kiko:template-trial:' + activeWorkspaceId.value + ':' + props.agent.id, rendered);
  emit('navigate', 'chat', undefined, props.agent.id);
}
watch(() => [activeWorkspaceId.value, props.agent.id, props.agent.version, props.agent.config_version], () => load(), { immediate: true });
onUnmounted(() => { ++epoch; emit('dirty', false); emit('busy', false); });
</script>

<template>
  <section class="setup-panel">
    <div class="setup-header">
      <h3>概览与接入</h3>
    </div>
    <NAlert v-if="error" type="error">
      {{ error }}
      <NButton size="small" style="margin-left: 8px;" @click="load()">重新读取</NButton>
    </NAlert>
    <template v-if="setup">
      <section v-if="setup.default_plugins?.length"><h4>平台默认能力</h4>
        <p v-for="p in setup.default_plugins" :key="p.key">{{ p.name }} · 官方预置 · {{ labels[p.status] || p.status }} <span v-if="p.credits_per_image">· {{ Number(p.credits_per_image) }} 积分/张</span></p>
        <template v-if="canManage"><p v-if="agent.payer">付款账户 · {{agent.payer.name}}</p>
          <NButton v-else-if="setup.can_bind_payer" size="small" :disabled="busy" @click="bindSelf">确认由我的积分支付</NButton></template>
      </section>
      <div class="setup-status-bar">
        <span class="wf-status-pill" :class="setup.conversation_model.available ? 'is-success' : 'is-warning'">
          <span class="wf-status-dot" />
          对话模型：{{ setup.conversation_model.available ? '可用' : '待配置' }}
        </span>
        <span class="wf-status-pill" :class="setup.task_model.available ? 'is-success' : 'is-warning'">
          <span class="wf-status-dot" />
          任务模型：{{ setup.task_model.available ? '可用' : '待配置' }}
        </span>
        <span class="wf-status-pill" :class="setup.access_allowed ? 'is-info' : ''">
          <span class="wf-status-dot" />
          当前用户{{ setup.access_allowed ? '有使用权' : '无使用权' }}
        </span>
      </div>

      <NAlert v-if="agent.template_origin" type="info">此员工从模板创建，配置已完整带入。来源模板更新不会自动覆盖。</NAlert>

      <div v-if="setup.packages.length" class="setup-list">
        <article v-for="p in setup.packages" :key="p.id" class="setup-card" :class="{ passed: !issues.some(i => i.package_id === p.id) }">
          <div class="setup-card-head">
            <strong>{{ p.name }}</strong>
            <span class="wf-status-pill" :class="issues.some(i => i.package_id === p.id) ? 'is-warning' : 'is-success'">
              <span class="wf-status-dot" />
              {{ issues.some(i => i.package_id === p.id) ? '待完善接入' : '当前接入条件已满足' }}
            </span>
          </div>
          <p v-if="p.description" class="field-note">{{ p.description }}</p>
          <p v-for="issue in issues.filter(i => i.package_id === p.id)" :key="issue.resource_id" class="setup-issue">
            {{ labels[issue.code] || issue.code }}
          </p>
        </article>
      </div>

      <article v-for="d in definitions" :key="d.plugin.id" class="setup-card">
        <h4 class="setup-sub-title">{{ d.plugin.name }}</h4>
        <PlatformPluginAccount
          :employee-id="agent.id"
          :plugin="d.plugin"
          :version-id="d.version"
          :can-manage="canManage"
          @account="() => load()"
          @dirty="v => accountDirty[d.plugin.id] = v"
          @busy="v => accountBusy[d.plugin.id] = v"
        />
      </article>

      <div v-if="setup.examples.length" class="setup-examples">
        <h3 class="setup-sub-heading">试用示例任务</h3>
        <article v-for="ex in setup.examples" :key="ex.key" class="setup-card">
          <div class="setup-card-head">
            <strong>{{ ex.title }}</strong>
            <NButton
              size="tiny"
              type="primary"
              secondary
              :disabled="!canTry(ex)"
              @click="tryTask(ex)"
            >
              填写到对话中试用
            </NButton>
          </div>
          <p class="field-note">{{ ex.text }}</p>
        </article>
      </div>
    </template>

    <div v-if="canManageTemplates" class="setup-reuse-box">
      <div class="setup-card-head">
        <div>
          <h3 class="setup-sub-heading" style="margin: 0 0 4px 0;">团队复用</h3>
          <p class="field-note">将当前已保存配置另存为工作区模板草稿，不会自动发布；未保存的修改不会带入。</p>
        </div>
        <NButton size="small" secondary :loading="busy" @click="saveAs">另存为工作区模板</NButton>
      </div>
    </div>
  </section>
</template>

<style scoped>
@import '../components/workforce/workforce-shared.css';

.setup-panel {
  margin-top: 20px;
  padding-top: 18px;
  border-top: 1px solid var(--id-border-subtle);
  display: flex;
  flex-direction: column;
  gap: 14px;
}

.setup-header h3 {
  margin: 0;
  font-size: 14px;
  font-weight: 600;
  color: var(--id-text-primary);
}

.setup-status-bar {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
}

.setup-list,
.setup-examples {
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.setup-card {
  background: var(--id-bg-subtle);
  border: 1px solid var(--id-border-subtle);
  border-radius: var(--id-radius-md);
  padding: 12px 14px;
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.setup-card.passed {
  border-color: var(--id-status-success-border);
}

.setup-card-head {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 10px;
  flex-wrap: wrap;
}

.setup-card-head strong {
  font-size: 13px;
  color: var(--id-text-primary);
}

.setup-sub-title {
  margin: 0 0 4px 0;
  font-size: 13px;
  font-weight: 600;
  color: var(--id-text-primary);
}

.setup-sub-heading {
  margin: 4px 0;
  font-size: 13px;
  font-weight: 600;
  color: var(--id-text-primary);
}

.setup-issue {
  margin: 0;
  font-size: 12px;
  color: var(--id-status-warning);
}

.setup-reuse-box {
  background: var(--id-bg-surface);
  border: 1px solid var(--id-border-subtle);
  border-radius: var(--id-radius-lg);
  padding: 14px;
}
</style>

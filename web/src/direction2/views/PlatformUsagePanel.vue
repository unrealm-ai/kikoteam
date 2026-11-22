<script setup lang="ts">
import { computed, inject, onUnmounted, ref, watch } from 'vue';
import { NAlert, NButton, NCheckbox, NInput, NInputNumber, NSelect, NSkeleton, NSwitch, useDialog } from 'naive-ui';
import { usageApi, type UsageResource, type UsageChannel } from '../api/agent-usage';
import { D2ApiError } from '../api/client';
import PlatformSelectedSubjects from '../components/PlatformSelectedSubjects.vue';
import PlatformIdentityPicker from '../components/PlatformIdentityPicker.vue';
import { toUsageSelector, type SelectionCandidate } from '../api/selectors';

const props = defineProps<{ workspace: string; employeeId: string; canManage: boolean }>();
const emit = defineEmits<{ close: [] }>(), dialog = useDialog();
const data = ref<UsageResource>(), baseline = ref(''), error = ref(''), notice = ref(''), loading = ref(false), busy = ref(false), denied = ref(false);
const side = ref<'allow' | 'deny'>('allow');
let generation = 0, controller = new AbortController();
const labels: Record<UsageChannel, string> = { web: 'Web', feishu: '飞书', dingtalk: '钉钉', wecom: '企业微信' };
const dirty = computed(() => !!data.value && JSON.stringify(data.value) !== baseline.value);
const writable = computed(() => props.canManage && !denied.value && !busy.value && !loading.value && !!data.value);
function picked(candidate: SelectionCandidate, includeDescendants: boolean) {
  if (!writable.value || !data.value) return;
  const subject = toUsageSelector(candidate.ref, includeDescendants);
  const list = data.value.usage_config.access[side.value];
  if (!list.some(s => JSON.stringify(s) === JSON.stringify(subject))) list.push(subject);
}
async function load() {
  const g = ++generation; controller.abort(); controller = new AbortController();
  loading.value = true; busy.value = false; data.value = undefined; error.value = ''; notice.value = ''; denied.value = !props.canManage;
  if (denied.value) { loading.value = false; return; }
  try {
    const r = await usageApi.get(props.workspace, props.employeeId, controller.signal);
    if (g !== generation) return;
    data.value = r; baseline.value = JSON.stringify(r);
  } catch (e) {
    if (g === generation) {
      error.value = e instanceof Error ? e.message : '读取失败';
      denied.value = e instanceof D2ApiError && [401, 403].includes(e.status);
    }
  } finally { if (g === generation) loading.value = false; }
}
async function save() {
  if (!writable.value || !data.value) return false;
  const g = generation; busy.value = true; error.value = '';
  try {
    const r = await usageApi.save(props.workspace, props.employeeId, JSON.parse(JSON.stringify(data.value)), controller.signal);
    if (g !== generation) return false;
    data.value = r; baseline.value = JSON.stringify(r); notice.value = '员工统一配置已保存。'; return true;
  } catch (e) {
    if (g === generation) {
      error.value = e instanceof Error ? e.message : '保存失败';
      if (e instanceof D2ApiError && [401, 403].includes(e.status)) { denied.value = true; data.value = undefined; }
    }
    return false;
  } finally { if (g === generation) busy.value = false; }
}
function confirm(action: () => void) {
  if (busy.value) return;
  if (!dirty.value) { action(); return; }
  dialog.warning({
    title: '统一配置尚未保存',
    content: '继续将放弃当前草稿。版本冲突时请重新读取后核对，不会自动覆盖。',
    positiveText: '放弃修改',
    negativeText: '继续编辑',
    onPositiveClick: action
  });
}
function close() { confirm(() => emit('close')); }
const draft = { hasUnsaved: () => dirty.value || busy.value, getLabel: () => '员工统一使用配置', save, discard: () => emit('close') };
const guard = inject<{ register: (v: typeof draft) => void; unregister: (v: typeof draft) => void }>('d2DraftGuard');
guard?.register(draft);
watch(() => [props.workspace, props.employeeId, props.canManage], () => void load(), { immediate: true });
onUnmounted(() => { ++generation; controller.abort(); guard?.unregister(draft); });
const sides = ['allow', 'deny'] as const, patterns = ['keywords', 'regexes'] as const, triggerChannels = ['web', 'feishu'] as const, historyChannels = ['web', 'feishu', 'wecom'] as const;
</script>

<template>
  <section class="usage">
    <header class="usage-header">
      <div>
        <h2>员工统一使用配置</h2>
        <p class="field-note">四渠道共用一份配置。主体保留渠道和应用身份，同名人员不会自动合并。</p>
      </div>
      <NButton size="small" secondary :disabled="busy" @click="close">返回员工列表</NButton>
    </header>

    <NSkeleton v-if="loading" text :repeat="4" />
    <NAlert v-if="error" type="error">{{ error }}</NAlert>
    <NAlert v-if="denied" type="warning">仅工作区管理员可管理。</NAlert>
    <NAlert v-if="notice" type="success">{{ notice }}</NAlert>

    <template v-if="data && !denied">
      <fieldset class="ch-section">
        <legend class="fs-legend">使用范围</legend>
        <div class="p-row">
          <div class="p-info">
            <strong>群成员免白名单</strong>
            <p>默认开启，群成员跳过允许名单，但拒绝规则始终优先。关闭不是禁用群聊，而是按统一使用范围判定。</p>
          </div>
          <NSwitch v-model:value="data.usage_config.access.group_members_enabled" :disabled="!writable" />
        </div>
        <div class="usage-field">
          <span class="usage-field-label">授权模式</span>
          <NSelect
            v-model:value="data.usage_config.access.mode"
            :disabled="!writable"
            :options="[{ label: '仅允许指定主体', value: 'allowlist' }, { label: '允许全部，拒绝规则优先', value: 'all' }]"
          />
        </div>
        <div class="usage-field">
          <h3 class="usage-sub-title">选择主体</h3>
          <NSelect
            v-model:value="side"
            :disabled="!writable"
            :options="[{ label: '加入允许名单', value: 'allow' }, { label: '加入拒绝名单', value: 'deny' }]"
          />
          <PlatformIdentityPicker
            :key="`people:${side}`"
            :workspace="workspace"
            :employee-id="employeeId"
            kind="people"
            purpose="usage"
            include-departments
            :disabled="!writable"
            @pick="picked"
            @revoked="denied = true"
          />
          <PlatformIdentityPicker
            :key="`groups:${side}`"
            :workspace="workspace"
            :employee-id="employeeId"
            kind="groups"
            purpose="usage"
            :disabled="!writable"
            @pick="picked"
            @revoked="denied = true"
          />
        </div>
        <div v-for="s in sides" :key="s" class="usage-field">
          <h3 class="usage-sub-title">全部{{ s === 'allow' ? '允许' : '拒绝' }}规则</h3>
          <PlatformSelectedSubjects
            :workspace="workspace"
            :employee-id="employeeId"
            :items="data.usage_config.access[s]"
            purpose="usage"
            :disabled="!writable"
            @remove="i => data!.usage_config.access[s].splice(i, 1)"
          />
        </div>
      </fieldset>

      <fieldset class="ch-section">
        <legend class="fs-legend">群触发规则</legend>
        <div class="p-row">
          <div class="p-info">
            <strong>Web / 飞书群内 @ 触发</strong>
            <p>钉钉、企业微信固定按平台 @ / 定向消息路由。私聊不经过群触发筛选。</p>
          </div>
          <NSwitch v-model:value="data.usage_config.trigger.mention" :disabled="!writable" />
        </div>
        <div v-for="kind in patterns" :key="kind" class="usage-field">
          <div class="cs-header-flex">
            <h3 class="usage-sub-title">{{ kind === 'keywords' ? '关键词' : '正则表达式' }}</h3>
            <NButton size="tiny" secondary :disabled="!writable || data.usage_config.trigger[kind].length >= 32" @click="data.usage_config.trigger[kind].push({ text: '', channels: ['feishu'] })">
              添加{{ kind === 'keywords' ? '关键词' : '正则' }}
            </NButton>
          </div>
          <article v-for="(r, i) in data.usage_config.trigger[kind]" :key="i" class="pattern-row">
            <NInput v-model:value="r.text" :disabled="!writable" :maxlength="256" size="small" style="flex: 1; min-width: 180px;" />
            <div class="pattern-channels">
              <NCheckbox
                v-for="c in triggerChannels"
                :key="c"
                :checked="r.channels.includes(c)"
                :disabled="!writable"
                @update:checked="v => r.channels = v ? [...r.channels, c] : r.channels.filter(x => x !== c)"
              >
                {{ labels[c] }}
              </NCheckbox>
            </div>
            <NButton size="tiny" quaternary type="error" :disabled="!writable" @click="data.usage_config.trigger[kind].splice(i, 1)">移除</NButton>
          </article>
        </div>
      </fieldset>

      <fieldset class="ch-section">
        <legend class="fs-legend">群历史上下文</legend>
        <div class="p-row">
          <div class="p-info">
            <strong>按需补拉群历史上下文</strong>
            <p>钉钉不补拉历史，企微通过 CLI 查询当前群近 7 天历史，按时间窗口与全文唯一锚点匹配，不支持话题。私聊不补拉。历史逐条进入 Input、不触发模型，由引擎合批。</p>
          </div>
          <NSwitch v-model:value="data.usage_config.history.enabled" :disabled="!writable" />
        </div>
        <div class="history-controls">
          <div class="pattern-channels">
            <NCheckbox
              v-for="c in historyChannels"
              :key="c"
              :checked="data.usage_config.history.channels.includes(c)"
              :disabled="!writable"
              @update:checked="v => data!.usage_config.history.channels = v ? [...data!.usage_config.history.channels, c] : data!.usage_config.history.channels.filter(x => x !== c)"
            >
              {{ labels[c] }}
            </NCheckbox>
          </div>
          <NInputNumber v-model:value="data.usage_config.history.limit" :min="1" :max="100" :clearable="false" :precision="0" :disabled="!writable" size="small" style="width: 140px;" />
        </div>
      </fieldset>
    </template>

    <footer class="usage-footer">
      <NButton :disabled="busy" @click="confirm(() => void load())">重新读取</NButton>
      <NButton type="primary" :loading="busy" :disabled="!writable || !dirty" @click="save">保存统一配置</NButton>
    </footer>
  </section>
</template>

<style scoped>
@import '../components/workforce/workforce-shared.css';

.usage {
  display: flex;
  flex-direction: column;
  gap: 16px;
}

.usage-header {
  display: flex;
  justify-content: space-between;
  align-items: flex-start;
  gap: 12px;
}

.usage-header h2 {
  margin: 0 0 4px 0;
  font-size: 18px;
  font-weight: 600;
}

.fs-legend {
  font-size: 14px;
  font-weight: 600;
  color: var(--id-text-primary);
  padding: 0 8px;
}

.usage-field {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.usage-field-label {
  font-size: 12px;
  font-weight: 600;
  color: var(--id-text-secondary);
}

.usage-sub-title {
  margin: 4px 0 0 0;
  font-size: 13px;
  font-weight: 600;
  color: var(--id-text-primary);
}

.pattern-row {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 10px;
  padding: 8px 10px;
  background: var(--id-bg-subtle);
  border: 1px solid var(--id-border-subtle);
  border-radius: var(--id-radius-md);
}

.pattern-channels {
  display: flex;
  align-items: center;
  gap: 10px;
  flex-wrap: wrap;
}

.history-controls {
  display: flex;
  align-items: center;
  justify-content: space-between;
  flex-wrap: wrap;
  gap: 12px;
  padding-top: 6px;
}

.usage-footer {
  display: flex;
  justify-content: flex-end;
  gap: 10px;
  flex-wrap: wrap;
}
</style>

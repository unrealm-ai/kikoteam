<script setup lang="ts">
import { computed } from 'vue';
import { NButton, NEmpty, NInput, NSpace, useMessage } from 'naive-ui';
import D2EmployeeAvatar from '../D2EmployeeAvatar.vue';
import D2ChannelLogo from '../D2ChannelLogo.vue';
import EmployeeCreateModal from './EmployeeCreateModal.vue';
import type { AgentStatusCounts } from '../../api/agents';
import { realPlatform } from '../../api/platform';
import { getEmployeeStatusMeta, getExecutionEngineMeta } from '../../utils/presentation';
import type { DigitalEmployee, ModelItem } from '../../types';

const props = withDefaults(defineProps<{
  page?: number; total?: number; statusCounts?: AgentStatusCounts | undefined; loading?: boolean;
  canManage?: boolean;
  employees: DigitalEmployee[];
  filteredEmployees: DigitalEmployee[];
  searchQuery: string;
  selectedFilter: string;
  modelsCatalog: Pick<ModelItem,'id'|'name'|'enabled'|'isDefault'|'protocol'>[];
  showCreateModal: boolean;
}>(), {canManage:true});

const emit = defineEmits<{
  'update:searchQuery': [value: string];
  'update:selectedFilter': [value: string];
  'update:showCreateModal': [value: boolean];
  'open-detail': [emp: DigitalEmployee, tab?: string];
  'start-chat': [emp: DigitalEmployee];
  created: [emp: DigitalEmployee];
  page:[page:number];
  'load-models':[];
  'from-template':[];
}>();

const filters = computed(() => {
  if (realPlatform) {
    const counts = props.statusCounts;
    return [
      {value: 'all', label: '全部员工', count: counts && counts.enabled + counts.disabled},
      {value: 'active', label: '已启用', count: counts?.enabled},
      {value: 'disabled', label: '已停用', count: counts?.disabled}
    ];
  }
  return [
    {value: 'all', label: '全部员工', count: props.employees.length},
    ...([
      ['active', '正常履职'], ['pending_setup', '入职待完善'], ['warning', '告警异常'], ['disabled', '已停用']
    ] as const).map(([value, label]) => ({
      value, label, count: props.employees.filter(emp => emp.status === value).length
    }))
  ];
});

const cards = computed(() => props.filteredEmployees.map(emp => {
  const summary = emp.cardResources;
  const channels = realPlatform
    ? summary?.channels?.map(platform => ({platform, title: undefined}))
    : emp.channels.map(ch => ({platform: ch.platform, title: `${ch.platformName} (${ch.statusText} · 已绑 ${ch.boundGroups.length} 群)`}));
  return {
    emp, channels,
    packageCount: realPlatform ? summary?.packageCount : emp.capabilityPackages.length,
    triggerCount: realPlatform ? summary?.triggerCount : emp.triggers.length
  };
}));

const message = useMessage();
function openCreateModal() {
  if (!props.canManage) {
    message.warning('创建员工需要有效工作区成员身份');
    return;
  }
  emit('update:showCreateModal', true);
}

defineExpose({ openCreateModal });
</script>

<template>
  <div class="workforce-list-container">
    <div class="list-header">
      <div class="header-titles">
        <h2 class="view-title">AI 员工</h2>
        <p class="view-subtitle">集中配置 AI 员工人设、绑定飞书/钉钉/企微渠道与群策略、授权技能包与自动触发器。</p>
      </div>
      <NSpace align="center" :size="8">
        <NButton v-if="realPlatform" size="small" secondary @click="emit('from-template')">从模板创建</NButton>
        <NButton type="primary" size="small" @click="openCreateModal">+ 新建 AI 员工</NButton>
      </NSpace>
    </div>

    <!-- 筛选与搜索 -->
    <div class="list-filter-bar">
      <div class="filter-pills">
        <button
          v-for="filter in filters" :key="filter.value" class="pill-btn"
          :class="{ active: selectedFilter === filter.value }"
          :aria-pressed="selectedFilter === filter.value"
          @click="emit('update:selectedFilter', filter.value)"
        >{{ filter.label }}<template v-if="filter.count !== undefined"> ({{ filter.count }})</template></button>
      </div>
      <NInput
        :value="searchQuery"
        placeholder="搜索员工岗位、职责关键字..."
        size="small"
        clearable
        class="list-search-input"
        v-bind="realPlatform ? {maxlength: 2000} : {}"
        @update:value="v => emit('update:searchQuery', v)"
      >
        <template #prefix>
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" style="opacity: 0.6;">
            <circle cx="11" cy="11" r="8"/><path d="m21 21-4.3-4.3"/>
          </svg>
        </template>
      </NInput>
    </div>

    <div v-if="filteredEmployees.length === 0" class="empty-filter-card">
      <NEmpty :description="loading ? '正在加载员工…' : '没有找到匹配的 AI 员工'">
        <template #extra>
          <NSpace v-if="!loading">
            <NButton
              v-if="selectedFilter !== 'all' || searchQuery"
              size="small"
              @click="emit('update:selectedFilter', 'all'); emit('update:searchQuery', '')"
            >
              重置筛选条件
            </NButton>
            <NButton type="primary" size="small" @click="openCreateModal">+ 新建 AI 员工</NButton>
          </NSpace>
        </template>
      </NEmpty>
    </div>

    <!-- 员工卡片列表 -->
    <div v-else class="employee-cards-grid">
      <article
        v-for="{ emp, channels, packageCount, triggerCount } in cards"
        :key="emp.id"
        class="emp-card"
        :class="{
          'needs-config': emp.status === 'pending_setup',
          'emp-disabled': emp.status === 'disabled'
        }"
        tabindex="0"
        @click="emit('open-detail', emp, 'overview')"
        @keydown.enter.self.prevent="emit('open-detail', emp, 'overview')"
      >
        <div class="ec-body">
          <div class="ec-top">
            <D2EmployeeAvatar :name="emp.name" :preset="emp.avatar" :identity-key="emp.id" :assistant="emp.isAssistant" size="md" />
            <div class="ec-meta">
              <div class="ec-title-row">
                <h3 class="ec-name" :title="emp.name">{{ emp.name }}</h3>
                <span class="ai-label">AI</span>
                <span
                  class="engine-label"
                  :title="`底层执行引擎：${getExecutionEngineMeta(emp.executionEngine).label}`"
                >
                  {{ getExecutionEngineMeta(emp.executionEngine).shortLabel }}
                </span>
              </div>
              <span class="ec-code" :title="emp.code">{{ realPlatform && emp.code.length > 16 ? emp.code.slice(0, 8) + '…' + emp.code.slice(-4) : emp.code }}</span>
            </div>
            <span
              class="wf-status-pill"
              :class="{
                'is-success': emp.status === 'active',
                'is-info': emp.status === 'pending_setup',
                'is-warning': emp.status === 'warning',
              }"
            >
              <span class="wf-status-dot" />
              {{ realPlatform && emp.status === 'active' ? '已启用' : getEmployeeStatusMeta(emp.status).label }}
            </span>
          </div>

          <p class="ec-role">{{ emp.role }}</p>

          <div class="ec-resources-row">
            <div class="ec-channel-logos">
              <D2ChannelLogo
                v-for="ch in channels"
                :key="ch.platform"
                :platform="ch.platform"
                :size="18"
                :title="ch.title"
              />
              <span v-if="channels?.length === 0" class="ch-empty-note">
                <svg class="ch-empty-icon" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
                  <path d="m18.84 12.25 1.72-1.71h-.02a5.004 5.004 0 0 0-.12-7.07 5.006 5.006 0 0 0-6.95 0l-1.72 1.71"/>
                  <path d="m5.17 11.75-1.71 1.71a5.004 5.004 0 0 0 .12 7.07 5.006 5.006 0 0 0 6.95 0l1.71-1.71"/>
                  <line x1="8" y1="2" x2="8" y2="5"/>
                  <line x1="2" y1="8" x2="5" y2="8"/>
                  <line x1="16" y1="19" x2="16" y2="22"/>
                  <line x1="19" y1="16" x2="22" y2="16"/>
                </svg>
                <span>未接入 IM 渠道</span>
              </span>
            </div>
            <span v-if="packageCount !== undefined" class="ec-package-pill">
              {{ packageCount }} 个能力包<template v-if="triggerCount != null"> · {{ triggerCount }} 触发器</template>
            </span>
            <span v-else class="ch-empty-note">资源摘要暂不可用</span>
          </div>
        </div>

        <div class="ec-card-footer">
          <p
            class="ec-context"
            :class="{
              'is-attention': emp.status === 'pending_setup',
              'is-warning': emp.status === 'warning',
            }"
            :title="emp.canManage === false ? undefined : emp.modelAvailable === false ? '模型当前不可用' : emp.status === 'pending_setup' ? '待接入飞书 / 钉钉 / 企微渠道' : `${getExecutionEngineMeta(emp.executionEngine).shortLabel} · ${emp.modelName}`"
          >
            <span
              class="ec-context-dot"
              :class="{
                'is-active': emp.status === 'active',
                'is-pending': emp.status === 'pending_setup',
                'is-warning': emp.status === 'warning',
              }"
              aria-hidden="true"
            />
            {{
              emp.canManage === false ? (emp.status === 'disabled' ? '已停用 · 仅查看历史' : 'AI 员工') : emp.modelAvailable === false ? '模型当前不可用' : emp.status === 'disabled'
                ? '已停用 · 仅查看历史'
                : emp.status === 'warning'
                  ? '渠道连接异常 · 请检查'
                  : emp.status === 'pending_setup'
                    ? '待接入 IM 渠道'
                    : `${getExecutionEngineMeta(emp.executionEngine).shortLabel} · ${emp.modelName.replace(/\s*\(.*?\)\s*/g, '')}`
            }}
          </p>
          <div class="ec-footer-actions">
            <NButton
              size="small"
              v-if="emp.status === 'disabled' || emp.canChat !== false"
              v-bind="emp.status === 'disabled' ? {} : { color: '#18181B' }"
              @click.stop="emit('start-chat', emp)"
            >
              {{ emp.status === 'disabled' ? '查看历史对话' : '开始对话' }}
            </NButton>
            <NButton size="small" secondary @click.stop="emit('open-detail', emp, 'overview')">
              {{ emp.canManage === false ? '查看概览' : '管理配置' }}
            </NButton>
          </div>
        </div>
      </article>
    </div>

    <NSpace v-if="realPlatform && total && total > 20" align="center" justify="end" aria-label="员工列表分页">
      <NButton size="small" :disabled="loading || (page || 1) <= 1" @click="emit('page', (page || 1) - 1)">上一页</NButton>
      <span>第 {{ page }} 页 · 共 {{ total }} 位员工</span>
      <NButton size="small" :disabled="loading || (page || 1) * 20 >= total" @click="emit('page', (page || 1) + 1)">下一页</NButton>
    </NSpace>

    <EmployeeCreateModal
      :show-create-modal="showCreateModal" :models-catalog="modelsCatalog" :can-manage="canManage"
      @update:show-create-modal="v => emit('update:showCreateModal', v)"
      @load-models="emit('load-models')" @created="emp => emit('created', emp)"
    />
  </div>
</template>

<style scoped>
@import './workforce-shared.css';

.workforce-list-container {
  display: flex;
  flex-direction: column;
  gap: 16px;
}

.list-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 12px;
  flex-wrap: wrap;
  padding: 8px 0;
}

.header-titles {
  display: flex;
  flex-direction: column;
  gap: 2px;
}

.view-title {
  font-size: 20px;
  font-weight: 600;
  letter-spacing: -0.02em;
  color: var(--id-text-primary);
  margin: 0 0 4px 0;
}

.view-subtitle {
  font-size: 13px;
  color: var(--id-text-secondary);
  margin: 0;
}

.list-filter-bar {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 12px;
  flex-wrap: wrap;
  background: var(--id-bg-surface);
  padding: 10px 16px;
  border: 1px solid var(--id-border-subtle);
  border-radius: var(--id-radius-lg);
}

.filter-pills {
  display: flex;
  gap: 8px;
  flex-wrap: wrap;
}

.list-search-input {
  max-width: 280px;
  width: 100%;
}

.pill-btn {
  background: var(--id-bg-subtle);
  border: 1px solid var(--id-border-subtle);
  padding: 4px 12px;
  border-radius: var(--id-radius-pill);
  font-size: 12px;
  font-weight: 500;
  font-variant-numeric: tabular-nums;
  color: var(--id-text-secondary);
  cursor: pointer;
  transition: all var(--id-transition-fast);
}

.pill-btn:hover {
  background: var(--id-bg-muted);
  color: var(--id-text-primary);
}

.pill-btn:focus-visible {
  outline: none;
  box-shadow: var(--id-focus-ring);
}

.pill-btn.active {
  background: var(--id-text-primary);
  color: var(--id-bg-surface);
  border-color: var(--id-text-primary);
  font-weight: 500;
}

.empty-filter-card {
  padding: 48px 16px;
  background: var(--id-bg-surface);
  border: 1px solid var(--id-border-subtle);
  border-radius: var(--id-radius-lg);
}

.employee-cards-grid {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: 16px;
}

@media (max-width: 1180px) {
  .employee-cards-grid {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }
}

@media (max-width: 760px) {
  .employee-cards-grid {
    grid-template-columns: 1fr;
    gap: 12px;
  }

  .list-filter-bar {
    padding: 10px 12px;
  }

  .list-search-input {
    max-width: none;
  }

  .ec-card-footer {
    flex-wrap: wrap;
  }
}

.emp-card {
  min-width: 0;
  border: 1px solid var(--id-border-subtle);
  border-radius: var(--id-radius-lg);
  background: var(--id-bg-surface);
  overflow: hidden;
  display: flex;
  flex-direction: column;
  cursor: pointer;
  transition: border-color var(--id-transition-fast), box-shadow var(--id-transition-fast);
}

.emp-card:hover {
  border-color: var(--id-border-strong);
  box-shadow: var(--id-shadow-md);
}

.emp-card:focus-visible {
  outline: none;
  box-shadow: var(--id-focus-ring);
}

.emp-card.emp-disabled {
  opacity: 0.78;
}

.ec-body {
  padding: 20px 20px 16px;
  flex: 1;
  display: flex;
  flex-direction: column;
}

.ec-top {
  display: flex;
  align-items: flex-start;
  gap: 10px;
  margin-bottom: 12px;
}

.ec-meta {
  flex: 1;
  min-width: 0;
}

.ec-title-row {
  display: flex;
  align-items: center;
  gap: 6px;
  flex-wrap: nowrap;
  min-width: 0;
}

.ec-name {
  font-size: 14px;
  line-height: 20px;
  font-weight: 600;
  color: var(--id-text-primary);
  margin: 0;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.ai-label {
  flex-shrink: 0;
  font-size: 12px;
  line-height: 16px;
  font-weight: 600;
  border-radius: var(--id-radius-sm);
  background: var(--id-bg-subtle);
  border: 1px solid var(--id-border-subtle);
  color: var(--id-text-secondary);
  padding: 1px 6px;
}

.engine-label {
  flex-shrink: 0;
  font-size: 11px;
  line-height: 16px;
  font-weight: 500;
  font-family: var(--id-font-mono);
  border-radius: var(--id-radius-sm);
  background: var(--id-bg-muted);
  border: 1px solid var(--id-border-hairline);
  color: var(--id-text-primary);
  padding: 1px 6px;
}

.ec-code {
  font-size: 12px;
  color: var(--id-text-secondary);
  background: var(--id-bg-subtle);
  border: 1px solid var(--id-border-hairline);
  padding: 1px 6px;
  border-radius: var(--id-radius-sm);
  font-family: var(--id-font-mono);
  font-variant-numeric: tabular-nums;
}

.ec-role {
  min-height: 40px;
  margin: 0 0 16px 0;
  color: var(--id-text-secondary);
  font-size: 13px;
  line-height: 20px;
  display: -webkit-box;
  -webkit-line-clamp: 2;
  -webkit-box-orient: vertical;
  overflow: hidden;
}

.ec-resources-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  flex-wrap: wrap;
  gap: 8px;
  margin-top: auto;
  padding-top: 12px;
  border-top: 1px solid var(--id-border-hairline);
  font-size: 12px;
  color: var(--id-text-muted);
}

.ec-channel-logos {
  display: inline-flex;
  align-items: center;
  gap: 7px;
}

.ch-empty-note {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  font-size: 12px;
  line-height: 1;
  color: var(--id-status-warning);
}

.ch-empty-icon {
  flex-shrink: 0;
  display: block;
}

.ec-package-pill {
  background: var(--id-bg-subtle);
  border: 1px solid var(--id-border-hairline);
  border-radius: var(--id-radius-sm);
  padding: 2px 8px;
  font-size: 12px;
  color: var(--id-text-secondary);
  font-variant-numeric: tabular-nums;
}

.ec-card-footer {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  padding: 12px 14px;
  background: var(--id-bg-subtle);
  border-top: 1px solid var(--id-border-hairline);
  flex-wrap: nowrap;
}

.ec-context {
  margin: 0;
  display: inline-flex;
  align-items: center;
  gap: 6px;
  color: var(--id-text-secondary);
  font-size: 12px;
  line-height: 18px;
  flex: 1;
  min-width: 0;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.ec-context.is-attention {
  color: var(--id-status-info);
  font-weight: 500;
}

.ec-context.is-warning {
  color: var(--id-status-warning);
  font-weight: 500;
}

.ec-context-dot {
  width: 6px;
  height: 6px;
  border-radius: 50%;
  background: var(--id-text-muted);
  flex-shrink: 0;
}

.ec-context-dot.is-active { background: var(--id-status-success-dot); }
.ec-context-dot.is-pending { background: var(--id-status-info-dot); }
.ec-context-dot.is-warning { background: var(--id-status-warning-dot); }

.ec-footer-actions {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-left: auto;
}

</style>

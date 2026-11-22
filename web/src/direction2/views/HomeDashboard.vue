<script setup lang="ts">
import { ref, computed, onMounted, onUnmounted, watch } from 'vue';
import { NButton, NSkeleton, NEmpty } from 'naive-ui';
import { dashboardApi } from '../api';
import { activeWorkspaceId, D2ApiError } from '../api/client';
import type { DashboardOverview, HumanApproval } from '../types';
import D2EmployeeAvatar from '../components/D2EmployeeAvatar.vue';
import D2ChannelLogo from '../components/D2ChannelLogo.vue';
import { parseTrendPathPoints } from '../utils/presentation';

const emit = defineEmits<{
  navigate: [target: string, param?: string, targetId?: string];
  openApproval: [id: string];
}>();

const activeActivityTab = ref<'all' | 'tasks' | 'policy_changes' | 'alerts'>('all');
const isLoading = ref(true);
const hasLoadedOnce = ref(false);
const loadError = ref<string | null>(null);
const overviewUnavailable = ref(false);

const overview = ref<DashboardOverview>({
  metrics: {
    totalEmployees: 0,
    runningEmployees: 0,
    warningEmployees: 0,
    errorEmployees: 0,
    totalChannels: 0,
    avgLatency: '--',
    healthRate: 0,
    todayTasksCount: 0,
    weeklyNewEmployees: 0
  },
  pendingApprovals: [],
  unconfiguredEmployees: [],
  activityEvents: [],
  trendDates: [],
  trendPath: ''
});

async function loadOverview(silent = false) {
  if (!silent) {
    isLoading.value = true;
  }
  try {
    overview.value = await dashboardApi.getOverview(activeWorkspaceId.value);
    loadError.value = null;
    overviewUnavailable.value = false;
    hasLoadedOnce.value = true;
  } catch (err) {
    overviewUnavailable.value = err instanceof D2ApiError && err.status === 404 && err.code === 'not_found' && err.message === '接口不存在';
    loadError.value = err instanceof Error ? err.message : '工作台概览数据加载失败';
  } finally {
    isLoading.value = false;
  }
}

let refreshTimer: ReturnType<typeof setInterval> | null = null;

watch(activeWorkspaceId, () => {
  hasLoadedOnce.value = false;
  loadOverview(false);
});

onMounted(() => {
  loadOverview(false);
  refreshTimer = setInterval(() => {
    loadOverview(true);
  }, 15000);
});

onUnmounted(() => {
  if (refreshTimer) clearInterval(refreshTimer);
});

const firstApproval = computed(() => overview.value.pendingApprovals[0]);
const secondApproval = computed(() => overview.value.pendingApprovals[1]);
const topPendingApprovals = computed(() => overview.value.pendingApprovals.slice(0, 2));
const firstUnconfigured = computed(() => overview.value.unconfiguredEmployees[0]);
const hasPriorityItems = computed(() => Boolean(firstApproval.value || secondApproval.value || firstUnconfigured.value));
const totalPendingApprovals = computed(() => overview.value.pendingApprovals.length);
const totalUnconfigured = computed(() => overview.value.unconfiguredEmployees.length);

function getApprovalCardMeta(appr: HumanApproval) {
  if (appr.kind === 'risk_command' || appr.riskLevel === 'high') {
    return {
      tone: 'danger' as const,
      badgeLabel: '高风险指令 · 待人工确认',
      btnType: 'error' as const,
      btnSecondary: true,
      btnText: '查看指令并人工确认'
    };
  }
  if (appr.kind === 'business_decision') {
    return {
      tone: 'warning' as const,
      badgeLabel: '业务决策 · 待人工确认',
      btnType: 'default' as const,
      btnSecondary: false,
      btnText: '前往人工确认'
    };
  }
  return {
    tone: 'info' as const,
    badgeLabel: '信息补充 · 待人工确认',
    btnType: 'default' as const,
    btnSecondary: false,
    btnText: '补充信息并人工确认'
  };
}

interface ChartPlotNode {
  xPct: number;
  yPct: number;
  date: string;
}

const chartPlotNodes = computed<ChartPlotNode[]>(() => {
  const path = overview.value.trendPath;
  const dates = overview.value.trendDates;
  if (!path || !path.trim()) return [];

  const coords: Array<{ x: number; y: number }> = [];
  if (!/[Cc]/.test(path)) {
    for (const pt of parseTrendPathPoints(path, dates)) {
      coords.push({ x: pt.x, y: pt.y });
    }
  } else {
    const startMatch = /M\s*(-?\d+(?:\.\d+)?)[\s,]+(-?\d+(?:\.\d+)?)/i.exec(path);
    if (startMatch) {
      coords.push({ x: Number(startMatch[1]), y: Number(startMatch[2]) });
    }
    const curveRe = /C\s*-?\d+(?:\.\d+)?[\s,]+-?\d+(?:\.\d+)?[\s,]+-?\d+(?:\.\d+)?[\s,]+-?\d+(?:\.\d+)?[\s,]+(-?\d+(?:\.\d+)?)[\s,]+(-?\d+(?:\.\d+)?)/gi;
    let m: RegExpExecArray | null;
    while ((m = curveRe.exec(path)) !== null) {
      coords.push({ x: Number(m[1]), y: Number(m[2]) });
    }
  }

  return coords.map((pt, idx) => {
    const ratio = Math.min(1, Math.max(0, pt.x / 500));
    const dateIdx = dates.length > 1 ? Math.round(ratio * (dates.length - 1)) : idx;
    return {
      xPct: ratio * 100,
      yPct: Math.min(100, Math.max(0, (pt.y / 130) * 100)),
      date: dates[dateIdx] ?? `节点 ${idx + 1}`
    };
  });
});

const gatewayStatusMeta = computed(() => {
  const { warningEmployees, errorEmployees } = overview.value.metrics;
  if (errorEmployees > 0) {
    return { label: `${errorEmployees} 名员工异常`, tone: 'danger' as const };
  }
  if (warningEmployees > 0) {
    return { label: `${warningEmployees} 名员工告警`, tone: 'warning' as const };
  }
  return { label: '运行正常', tone: 'success' as const };
});

const filteredActivities = computed(() => {
  if (activeActivityTab.value === 'all') return overview.value.activityEvents;
  return overview.value.activityEvents.filter(a => a.category === activeActivityTab.value);
});

const activityTabs: Array<{ key: 'all' | 'tasks' | 'policy_changes' | 'alerts'; label: string }> = [
  { key: 'all', label: '全部' },
  { key: 'tasks', label: '任务' },
  { key: 'policy_changes', label: '群策略' },
  { key: 'alerts', label: '告警' }
];
</script>

<template>
  <div class="d2-dashboard">
    <!-- 顶部页面标题栏：极简平铺，保留唯一主操作按钮 -->
    <header class="page-header">
      <div class="page-header-meta">
        <h1 class="page-title">协同工作台概览</h1>
        <p class="page-subtitle">
          实时汇总当前工作区的待人工确认项、在岗 AI 员工与协同任务动态。
        </p>
      </div>
      <div class="page-header-actions">
        <NButton size="small" @click="emit('navigate', 'tasks')">
          事项看板
        </NButton>
        <NButton type="primary" size="small" @click="emit('navigate', 'employees', 'create')">
          + 新建 AI 员工
        </NButton>
      </div>
    </header>

    <!-- 四态闭环 1：首次加载结构化骨架屏（防止数据跳变与 CLS） -->
    <template v-if="isLoading && !hasLoadedOnce">
      <div class="kpi-strip kpi-strip--skeleton">
        <div v-for="n in 5" :key="'sk-kpi-' + n" class="kpi-cell">
          <NSkeleton text style="width: 56%; height: 14px;" />
          <NSkeleton text style="width: 42%; height: 24px; margin-top: 8px;" />
        </div>
      </div>

      <div class="priority-grid">
        <div v-for="n in 3" :key="'sk-pri-' + n" class="priority-card">
          <div class="pc-top">
            <NSkeleton text style="width: 88px; height: 20px;" />
            <NSkeleton text style="width: 96px; height: 14px;" />
          </div>
          <NSkeleton text style="width: 82%; height: 18px; margin-top: 4px;" />
          <NSkeleton text :repeat="2" style="height: 14px;" />
          <div class="pc-actions">
            <NSkeleton text style="width: 112px; height: 28px;" />
          </div>
        </div>
      </div>

      <div class="dashboard-grid-row">
        <div class="panel-surface">
          <div class="panel-header">
            <NSkeleton text style="width: 140px; height: 16px;" />
            <NSkeleton text style="width: 90px; height: 14px;" />
          </div>
          <div class="panel-body">
            <NSkeleton style="width: 100%; height: 148px; border-radius: 6px;" />
          </div>
        </div>
        <div class="panel-surface">
          <div class="panel-header">
            <NSkeleton text style="width: 150px; height: 16px;" />
            <NSkeleton text style="width: 140px; height: 16px;" />
          </div>
          <div class="panel-divide-list">
            <div v-for="n in 4" :key="'sk-act-' + n" class="activity-row">
              <NSkeleton text style="width: 44px; height: 14px;" />
              <NSkeleton text style="flex: 1; height: 14px;" />
              <NSkeleton text style="width: 68px; height: 20px;" />
            </div>
          </div>
        </div>
      </div>
    </template>

    <!-- 四态闭环 2：首次加载失败可恢复错误卡片 -->
    <section v-else-if="overviewUnavailable && !hasLoadedOnce" class="error-state-card" role="status">
      <div class="error-state-main">
        <div>
          <strong class="error-state-title">工作台概览尚未接入</strong>
          <p class="error-state-desc">当前已支持账号、工作区、模型与员工基础配置；任务统计、渠道及协同动态尚未接入真实后端，这里不展示模拟数据。</p>
        </div>
      </div>
      <NButton size="small" type="primary" @click="emit('navigate', 'employees')">管理 AI 员工</NButton>
    </section>
    <section v-else-if="loadError && !hasLoadedOnce" class="error-state-card" role="alert">
      <div class="error-state-main">
        <span class="status-dot status-dot--danger" aria-hidden="true"></span>
        <div>
          <strong class="error-state-title">工作台概览数据加载失败</strong>
          <p class="error-state-desc">{{ loadError }}</p>
        </div>
      </div>
      <NButton size="small" type="primary" @click="loadOverview(false)">重试加载</NButton>
    </section>

    <!-- 正常数据态 -->
    <template v-else>
      <!-- 后台静默刷新失败的非阻断内联提示 -->
      <div v-if="loadError" class="stale-inline-strip" role="status">
        <span>
          <span class="status-dot status-dot--warning" aria-hidden="true"></span>
          状态刷新失败（{{ loadError }}），当前显示的可能不是最新数据。
        </span>
        <button type="button" class="inline-link-btn" @click="loadOverview(false)">立即重试</button>
      </div>

      <!-- 1. 全域监控指标条：5 列一体式 1px 细线分段，数值统一深墨色 + tabular-nums -->
      <section class="kpi-strip" aria-label="全域运行指标">
        <button type="button" class="kpi-cell" @click="emit('navigate', 'employees')">
          <span class="kpi-label">AI 员工总数</span>
          <div class="kpi-value-row">
            <span class="kpi-value">{{ overview.metrics.totalEmployees }}</span>
            <span v-if="overview.metrics.weeklyNewEmployees > 0" class="micro-badge micro-badge--success tabular-num">
              +{{ overview.metrics.weeklyNewEmployees }} 本周
            </span>
          </div>
        </button>

        <button type="button" class="kpi-cell" @click="emit('navigate', 'employees')">
          <span class="kpi-label">正常履职在岗</span>
          <div class="kpi-value-row">
            <span class="kpi-value">{{ overview.metrics.runningEmployees }}</span>
            <span class="kpi-meta-inline">
              <span class="status-dot status-dot--success" aria-hidden="true"></span>
              <span>运行中</span>
            </span>
          </div>
        </button>

        <button type="button" class="kpi-cell" @click="emit('navigate', 'employees')">
          <span class="kpi-label">关联覆盖工作群</span>
          <div class="kpi-value-row">
            <span class="kpi-value">{{ overview.metrics.totalChannels }}</span>
            <span class="kpi-meta-inline">个业务群</span>
          </div>
        </button>

        <button type="button" class="kpi-cell" @click="emit('navigate', 'settings', 'operations')">
          <span class="kpi-label">平均响应延迟</span>
          <div class="kpi-value-row">
            <span class="kpi-value">{{ overview.metrics.avgLatency }}</span>
            <span class="kpi-meta-inline" :class="`is-${gatewayStatusMeta.tone}`">
              <span class="status-dot" :class="`status-dot--${gatewayStatusMeta.tone}`" aria-hidden="true"></span>
              <span class="truncate" :title="gatewayStatusMeta.label">{{ gatewayStatusMeta.label }}</span>
            </span>
          </div>
        </button>

        <button type="button" class="kpi-cell" @click="emit('navigate', 'tasks')">
          <span class="kpi-label">今日执行成功率</span>
          <div class="kpi-value-row">
            <span class="kpi-value">{{ overview.metrics.healthRate }}%</span>
            <span class="kpi-meta-inline tabular-num">{{ overview.metrics.todayTasksCount }} 项任务</span>
          </div>
        </button>
      </section>

      <!-- 2. 优先关注与待办事项（边框优先 + 微语义圆点，按真实 kind 与 riskLevel 动态渲染） -->
      <section v-if="hasPriorityItems" class="action-priority-section">
        <div class="section-header-row">
          <div class="section-header-left">
            <h2 class="section-title">优先关注与待办事项</h2>
            <span class="section-sub">
              需管理员人工确认或完善接入的事项
              <template v-if="totalPendingApprovals > 2">
                · 共 <strong class="tabular-num">{{ totalPendingApprovals }}</strong> 项待人工确认
              </template>
              <template v-if="totalUnconfigured > 1">
                · 另有 <strong class="tabular-num">{{ totalUnconfigured - 1 }}</strong> 名待配置
              </template>
            </span>
          </div>
          <button
            v-if="totalPendingApprovals > 0"
            type="button"
            class="inline-link-btn"
            @click="emit('navigate', 'tasks', 'approvals')"
          >
            查看全部待人工确认
          </button>
        </div>

        <div class="priority-grid">
          <article v-for="appr in topPendingApprovals" :key="appr.id" class="priority-card">
            <div class="pc-top">
              <span class="micro-badge" :class="`micro-badge--${getApprovalCardMeta(appr).tone}`">
                <span class="status-dot" :class="`status-dot--${getApprovalCardMeta(appr).tone}`" aria-hidden="true"></span>
                {{ getApprovalCardMeta(appr).badgeLabel }}
              </span>
              <span class="pc-time tabular-num">{{ appr.createdAt }}</span>
            </div>
            <h3 class="pc-title" :title="appr.title">{{ appr.title }}</h3>
            <p class="pc-desc" :title="appr.contextReason">
              来自 <strong>{{ appr.employeeName }}</strong> ({{ appr.channelGroup }})：{{ appr.contextReason }}
            </p>
            <div class="pc-actions">
              <NButton
                size="small"
                :type="getApprovalCardMeta(appr).btnType"
                :secondary="getApprovalCardMeta(appr).btnSecondary"
                @click="emit('openApproval', appr.id)"
              >
                {{ getApprovalCardMeta(appr).btnText }}
              </NButton>
            </div>
          </article>

          <!-- 待完善配置的员工提示 -->
          <article v-if="firstUnconfigured" class="priority-card">
            <div class="pc-top">
              <span class="micro-badge micro-badge--info">
                <span class="status-dot status-dot--info" aria-hidden="true"></span>
                入职待配置
              </span>
              <span class="pc-time truncate" :title="firstUnconfigured.role || '待完成接入'">
                {{ firstUnconfigured.role || '待完成接入' }}
              </span>
            </div>
            <div class="pc-employee-row">
              <D2EmployeeAvatar :name="firstUnconfigured.name" :preset="firstUnconfigured.avatar" :identity-key="firstUnconfigured.id" size="sm" />
              <h3 class="pc-title pc-title--inline" :title="`${firstUnconfigured.name} 尚未接入渠道`">
                {{ firstUnconfigured.name }} 尚未接入渠道
              </h3>
            </div>
            <p class="pc-desc pc-desc--channels">
              <template v-if="firstUnconfigured.channels.length > 0">
                <span>已关联：</span>
                <D2ChannelLogo v-for="ch in firstUnconfigured.channels" :key="ch.platform" :platform="ch.platform" :size="15" />
                <span>，请完成激活确认</span>
              </template>
              <template v-else>
                <span>尚未绑定飞书、钉钉或企微群聊渠道，配置后即可开启协同通知</span>
              </template>
            </p>
            <div class="pc-actions">
              <NButton size="small" @click="emit('navigate', 'employees', 'configure_pending', firstUnconfigured.id)">
                去完成渠道与群配置
              </NButton>
            </div>
          </article>
        </div>
      </section>

      <!-- 3. 下半部分：执行趋势 vs 最近业务事件与活动流 -->
      <div class="dashboard-grid-row">
        <!-- 执行趋势简报 -->
        <section class="panel-surface">
          <div class="panel-header">
            <h2 class="section-title">任务执行与流转趋势</h2>
            <span class="panel-meta tabular-num">最近 7 天任务分布</span>
          </div>
          <div class="panel-body">
            <div v-if="!overview.trendPath" class="panel-empty-box">
              <NEmpty size="small" description="暂无最近 7 天执行趋势数据" />
            </div>
            <template v-else>
              <div class="chart-stage">
                <svg viewBox="0 0 500 130" class="svg-trend" preserveAspectRatio="none" aria-hidden="true">
                  <defs>
                    <linearGradient id="d2TrendFill" x1="0" y1="0" x2="0" y2="1">
                      <stop offset="0%" stop-color="#2563EB" stop-opacity="0.14" />
                      <stop offset="100%" stop-color="#2563EB" stop-opacity="0.0" />
                    </linearGradient>
                  </defs>
                  <line x1="0" y1="28" x2="500" y2="28" stroke="#E4E4E7" stroke-width="1" stroke-dasharray="3 3" vector-effect="non-scaling-stroke" />
                  <line x1="0" y1="64" x2="500" y2="64" stroke="#E4E4E7" stroke-width="1" stroke-dasharray="3 3" vector-effect="non-scaling-stroke" />
                  <line x1="0" y1="100" x2="500" y2="100" stroke="#F4F4F5" stroke-width="1" vector-effect="non-scaling-stroke" />
                  <path :d="`${overview.trendPath} L500,130 L0,130 Z`" fill="url(#d2TrendFill)" />
                  <path :d="overview.trendPath" fill="none" stroke="#2563EB" stroke-width="2" vector-effect="non-scaling-stroke" />
                </svg>
                <!-- HTML 叠加圆形数据节点：不受 preserveAspectRatio 拉伸变扁 -->
                <span
                  v-for="(node, idx) in chartPlotNodes"
                  :key="node.date + ':' + idx"
                  class="chart-node-dot"
                  :class="{ 'is-latest': idx === chartPlotNodes.length - 1 }"
                  :style="{ left: `${node.xPct}%`, top: `${node.yPct}%` }"
                  :title="`${node.date}`"
                />
              </div>
              <div class="chart-x-axis tabular-num">
                <span v-for="d in overview.trendDates" :key="d">{{ d }}</span>
              </div>
            </template>
          </div>
        </section>

        <!-- 最近动态：无边框下划线 Tab + 1px divide-y 扁平列表 -->
        <section class="panel-surface">
          <div class="panel-header panel-header--tabs">
            <h2 class="section-title">最近动态</h2>
            <div class="underline-tabs" role="tablist" aria-label="业务事件筛选">
              <button
                v-for="tab in activityTabs"
                :key="tab.key"
                type="button"
                role="tab"
                class="u-tab-btn"
                :class="{ active: activeActivityTab === tab.key }"
                :aria-selected="activeActivityTab === tab.key"
                @click="activeActivityTab = tab.key"
              >
                {{ tab.label }}
              </button>
            </div>
          </div>

          <div v-if="filteredActivities.length === 0" class="panel-empty-box">
            <NEmpty size="small" description="当前筛选分类下暂无业务事件记录">
              <template v-if="activeActivityTab !== 'all'" #extra>
                <NButton size="tiny" @click="activeActivityTab = 'all'">查看全部事件</NButton>
              </template>
            </NEmpty>
          </div>
          <div v-else class="panel-divide-list">
            <div v-for="act in filteredActivities" :key="act.id" class="activity-row">
              <span class="act-time tabular-num">{{ act.time }}</span>
              <span class="act-content truncate" :title="act.content">{{ act.content }}</span>
              <span
                class="micro-badge act-badge"
                :class="`micro-badge--${act.tone === 'error' ? 'danger' : act.tone}`"
              >
                <span
                  class="status-dot"
                  :class="`status-dot--${act.tone === 'error' ? 'danger' : act.tone}`"
                  aria-hidden="true"
                ></span>
                {{ act.badge }}
              </span>
            </div>
          </div>
        </section>
      </div>
    </template>
  </div>
</template>

<style scoped>
.d2-dashboard {
  display: flex;
  flex-direction: column;
  gap: var(--id-space-5, 20px);
}

/* 顶部工作台标题栏：极简无外框 */
.page-header {
  display: flex;
  justify-content: space-between;
  align-items: flex-end;
  gap: var(--id-space-4, 16px);
  flex-wrap: wrap;
}
.page-header-meta {
  display: flex;
  flex-direction: column;
  gap: var(--id-space-1, 4px);
  min-width: 0;
}
.page-title {
  margin: 0;
  font-size: 20px;
  line-height: 28px;
  font-weight: 600;
  letter-spacing: -0.02em;
  color: var(--id-text-primary);
}
.page-subtitle {
  margin: 0;
  font-size: 13px;
  line-height: 20px;
  color: var(--id-text-secondary);
}
.page-header-actions {
  display: flex;
  align-items: center;
  gap: var(--id-space-2);
  flex-shrink: 0;
}

/* 通用字体/微语义工具类 */
.tabular-num {
  font-variant-numeric: tabular-nums;
}
.truncate {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.status-dot {
  width: 6px;
  height: 6px;
  border-radius: 50%;
  flex-shrink: 0;
  display: inline-block;
}
.status-dot--success { background: var(--id-status-success-dot); }
.status-dot--warning { background: var(--id-status-warning-dot); }
.status-dot--danger { background: var(--id-status-danger-dot); }
.status-dot--info { background: var(--id-status-info-dot); }

.micro-badge {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  padding: 2px 8px;
  border-radius: var(--id-radius-sm);
  font-size: 12px;
  line-height: 18px;
  font-weight: 500;
  border: 1px solid transparent;
  white-space: nowrap;
}
.micro-badge--success {
  color: var(--id-status-success);
  background: var(--id-status-success-soft);
  border-color: var(--id-status-success-border);
}
.micro-badge--warning {
  color: var(--id-status-warning);
  background: var(--id-status-warning-soft);
  border-color: var(--id-status-warning-border);
}
.micro-badge--danger {
  color: var(--id-status-danger);
  background: var(--id-status-danger-soft);
  border-color: var(--id-status-danger-border);
}
.micro-badge--info {
  color: var(--id-status-info);
  background: var(--id-status-info-soft);
  border-color: var(--id-status-info-border);
}

/* 错误态与陈旧提示条 */
.error-state-card {
  background: var(--id-bg-surface);
  border: 1px solid var(--id-status-danger-border);
  border-radius: var(--id-radius-lg);
  padding: var(--id-space-4) var(--id-space-5);
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: var(--id-space-4);
  flex-wrap: wrap;
}
.error-state-main {
  display: flex;
  align-items: flex-start;
  gap: var(--id-space-3);
}
.error-state-main .status-dot {
  margin-top: 7px;
}
.error-state-title {
  font-size: 14px;
  font-weight: 600;
  color: var(--id-text-primary);
  display: block;
}
.error-state-desc {
  margin: 2px 0 0 0;
  font-size: 13px;
  color: var(--id-text-secondary);
}
.stale-inline-strip {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: var(--id-space-3);
  padding: var(--id-space-2) var(--id-space-3);
  background: var(--id-status-warning-soft);
  border: 1px solid var(--id-status-warning-border);
  border-radius: var(--id-radius-md);
  font-size: 12px;
  color: var(--id-status-warning);
}
.stale-inline-strip > span {
  display: inline-flex;
  align-items: center;
  gap: 8px;
}

/* 1. 一体式 5 列 KPI 分段条 */
.kpi-strip {
  display: grid;
  grid-template-columns: repeat(5, minmax(0, 1fr));
  background: var(--id-bg-surface);
  border: 1px solid var(--id-border-subtle);
  border-radius: var(--id-radius-lg);
  overflow: hidden;
}
.kpi-cell {
  appearance: none;
  border: none;
  border-right: 1px solid var(--id-border-hairline);
  background: transparent;
  padding: 14px var(--id-space-4);
  text-align: left;
  display: flex;
  flex-direction: column;
  gap: 6px;
  cursor: pointer;
  transition: background-color var(--id-transition-fast);
  min-width: 0;
}
.kpi-cell:last-child {
  border-right: none;
}
.kpi-cell:hover {
  background: var(--id-bg-subtle);
}
.kpi-cell:focus-visible {
  outline: none;
  box-shadow: var(--id-focus-ring);
}
.kpi-strip--skeleton .kpi-cell {
  cursor: default;
}
.kpi-strip--skeleton .kpi-cell:hover {
  background: transparent;
}
.kpi-label {
  font-size: 12px;
  line-height: 16px;
  font-weight: 500;
  color: var(--id-text-muted);
}
.kpi-value-row {
  display: flex;
  align-items: baseline;
  justify-content: space-between;
  gap: var(--id-space-2);
  flex-wrap: wrap;
}
.kpi-value {
  font-size: 20px;
  line-height: 26px;
  font-weight: 600;
  letter-spacing: -0.02em;
  color: var(--id-text-primary);
  font-family: var(--id-font-mono);
  font-variant-numeric: tabular-nums;
}
.kpi-meta-inline {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  font-size: 12px;
  line-height: 16px;
  color: var(--id-text-muted);
  min-width: 0;
  max-width: 100%;
}
.kpi-meta-inline.is-danger {
  color: var(--id-status-danger);
  font-weight: 500;
}
.kpi-meta-inline.is-warning {
  color: var(--id-status-warning);
  font-weight: 500;
}

/* 2. 优先关注与待办事项 */
.action-priority-section {
  display: flex;
  flex-direction: column;
  gap: var(--id-space-3);
}
.section-header-row {
  display: flex;
  justify-content: space-between;
  align-items: baseline;
  gap: var(--id-space-3);
  flex-wrap: wrap;
}
.section-header-left {
  display: flex;
  align-items: baseline;
  gap: var(--id-space-3);
  flex-wrap: wrap;
}
.section-title {
  margin: 0;
  font-size: 14px;
  line-height: 20px;
  font-weight: 600;
  color: var(--id-text-primary);
}
.section-sub {
  font-size: 12px;
  line-height: 18px;
  color: var(--id-text-muted);
}
.inline-link-btn {
  border: none;
  background: transparent;
  color: var(--id-accent-primary);
  font-size: 12px;
  font-weight: 500;
  padding: 2px 4px;
  border-radius: var(--id-radius-sm);
  cursor: pointer;
  transition: background-color var(--id-transition-fast);
}
.inline-link-btn:hover {
  background: var(--id-accent-primary-soft);
}

.priority-grid {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: var(--id-space-3);
}
.priority-card {
  background: var(--id-bg-surface);
  border: 1px solid var(--id-border-subtle);
  border-radius: var(--id-radius-lg);
  padding: var(--id-space-4);
  display: flex;
  flex-direction: column;
  gap: var(--id-space-2);
  min-width: 0;
  transition: border-color var(--id-transition-fast);
}
.priority-card:hover {
  border-color: var(--id-border-strong);
}
.pc-top {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: var(--id-space-2);
}
.pc-time {
  font-size: 12px;
  line-height: 16px;
  color: var(--id-text-muted);
  font-family: var(--id-font-mono);
  min-width: 0;
}
.pc-employee-row {
  display: flex;
  align-items: center;
  gap: var(--id-space-2);
  margin-top: 2px;
  min-width: 0;
}
.pc-title {
  margin: 4px 0 0 0;
  font-size: 14px;
  line-height: 20px;
  font-weight: 600;
  color: var(--id-text-primary);
  display: -webkit-box;
  -webkit-line-clamp: 2;
  -webkit-box-orient: vertical;
  overflow: hidden;
}
.pc-title--inline {
  margin-top: 0;
  -webkit-line-clamp: 1;
}
.pc-desc {
  margin: 0;
  font-size: 13px;
  line-height: 20px;
  color: var(--id-text-secondary);
  flex: 1;
  display: -webkit-box;
  -webkit-line-clamp: 3;
  -webkit-box-orient: vertical;
  overflow: hidden;
}
.pc-desc--channels {
  display: flex;
  align-items: center;
  gap: 6px;
  flex-wrap: wrap;
}
.pc-actions {
  display: flex;
  justify-content: flex-end;
  padding-top: var(--id-space-2);
  margin-top: var(--id-space-1);
  border-top: 1px solid var(--id-border-hairline);
}

/* 3. 底部双栏：趋势图 + 活动审计流 */
.dashboard-grid-row {
  display: grid;
  grid-template-columns: 1fr 1.45fr;
  gap: var(--id-space-4);
  align-items: stretch;
}
.panel-surface {
  background: var(--id-bg-surface);
  border: 1px solid var(--id-border-subtle);
  border-radius: var(--id-radius-lg);
  display: flex;
  flex-direction: column;
  min-width: 0;
}
.panel-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: var(--id-space-3);
  padding: var(--id-space-3) var(--id-space-4);
  border-bottom: 1px solid var(--id-border-hairline);
  flex-wrap: wrap;
}
.panel-header--tabs {
  padding-top: 8px;
  padding-bottom: 0;
  min-height: 44px;
}
.panel-meta {
  font-size: 12px;
  line-height: 16px;
  color: var(--id-text-muted);
}
.panel-body {
  padding: var(--id-space-4);
  flex: 1;
  display: flex;
  flex-direction: column;
  justify-content: space-between;
}
.panel-empty-box {
  padding: 36px var(--id-space-4);
  display: flex;
  align-items: center;
  justify-content: center;
  flex: 1;
}

/* SVG 趋势图 + 非变形圆点叠加 */
.chart-stage {
  position: relative;
  height: 148px;
  margin-top: var(--id-space-2);
}
.svg-trend {
  width: 100%;
  height: 100%;
  display: block;
}
.chart-node-dot {
  position: absolute;
  width: 8px;
  height: 8px;
  border-radius: 50%;
  background: var(--id-bg-surface);
  border: 2px solid var(--id-accent-primary);
  transform: translate(-50%, -50%);
  pointer-events: auto;
  transition: transform var(--id-transition-fast);
}
.chart-node-dot:hover {
  transform: translate(-50%, -50%) scale(1.25);
}
.chart-node-dot.is-latest {
  width: 9px;
  height: 9px;
  background: var(--id-accent-primary);
  border: 2px solid var(--id-bg-surface);
  box-shadow: var(--id-focus-ring);
}
.chart-x-axis {
  display: flex;
  justify-content: space-between;
  margin-top: var(--id-space-3);
  font-size: 12px;
  line-height: 16px;
  color: var(--id-text-muted);
  font-family: var(--id-font-mono);
}

/* 无边框下划线 Tab */
.underline-tabs {
  display: inline-flex;
  align-items: center;
  gap: var(--id-space-4);
}
.u-tab-btn {
  border: none;
  border-bottom: 2px solid transparent;
  background: transparent;
  color: var(--id-text-muted);
  font-size: 13px;
  line-height: 20px;
  font-weight: 500;
  padding: 6px 2px 10px 2px;
  cursor: pointer;
  white-space: nowrap;
  transition: color var(--id-transition-fast), border-color var(--id-transition-fast);
}
.u-tab-btn:hover {
  color: var(--id-text-primary);
}
.u-tab-btn.active {
  color: var(--id-text-primary);
  font-weight: 600;
  border-bottom-color: var(--id-text-primary);
}

/* 活动审计流：1px divide-y 扁平列表 */
.panel-divide-list {
  display: flex;
  flex-direction: column;
}
.activity-row {
  display: flex;
  align-items: center;
  gap: var(--id-space-3);
  padding: 12px var(--id-space-4);
  border-bottom: 1px solid var(--id-border-hairline);
  transition: background-color var(--id-transition-fast);
  min-width: 0;
}
.activity-row:last-child {
  border-bottom: none;
}
.activity-row:hover {
  background: var(--id-bg-subtle);
}
.act-time {
  width: 48px;
  font-size: 12px;
  line-height: 18px;
  color: var(--id-text-muted);
  font-family: var(--id-font-mono);
  flex-shrink: 0;
}
.act-content {
  flex: 1;
  min-width: 0;
  font-size: 13px;
  line-height: 20px;
  color: var(--id-text-primary);
}
.act-badge {
  flex-shrink: 0;
}

@media (max-width: 1120px) {
  .kpi-strip {
    grid-template-columns: repeat(3, minmax(0, 1fr));
  }
  .kpi-cell {
    border-bottom: 1px solid var(--id-border-hairline);
  }
  .priority-grid {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }
  .dashboard-grid-row {
    grid-template-columns: 1fr;
  }
}

@media (max-width: 760px) {
  .page-header {
    flex-direction: column;
    align-items: flex-start;
  }
  .page-header-actions {
    width: 100%;
    flex-wrap: wrap;
  }
  .kpi-strip {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }
  .priority-grid {
    grid-template-columns: 1fr;
  }
  .activity-row {
    flex-wrap: wrap;
    gap: 6px;
  }
  .act-time {
    width: auto;
  }
}

@media (max-width: 480px) {
  .kpi-strip {
    grid-template-columns: 1fr;
  }
  .kpi-cell {
    border-right: none;
  }
}
</style>

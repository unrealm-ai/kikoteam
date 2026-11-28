<script setup lang="ts">
import { ref, computed, onMounted, onUnmounted, watch, provide } from 'vue';
import {
  NConfigProvider, zhCN, dateZhCN, NMessageProvider, NDialogProvider, NButton,
  NInput, NPopover, NAlert, NModal, NSkeleton, NTooltip,
  type InputInst
} from 'naive-ui';
import HomeDashboard from './views/HomeDashboard.vue';
import ConversationsView from './views/ConversationsView.vue';
import PlatformConversationsView from './views/PlatformConversationsView.vue';
import WorkforceView from './views/WorkforceView.vue';
import { assistantApi, type AssistantEntry, type AssistantCard } from './api/assistant';
import AgentMarketView from './views/AgentMarketView.vue';
import MyCreditsView from './views/MyCreditsView.vue';

import { humanRequestsApi } from './api/human-requests';
import TasksAndCollaborationView from './views/TasksAndCollaborationView.vue';
import ResourcesView from './views/ResourcesView.vue';
import SettingsView from './views/SettingsView.vue';
import AuthView from './views/AuthView.vue';
import InvitationView from './views/InvitationView.vue';
import ReferralInvite from './views/ReferralInvite.vue';
import { referralCodeFromPath } from './api/referrals';
import { readInvitationToken, realPlatform, platformRequest, post, workspaceFromWire, type WorkspaceWire } from './api/platform';
import { activeWorkspaceId, pendingApprovalsCount, clearAuthToken, D2ApiError } from './api/client';
import { authApi, dashboardApi } from './api';
import type { UserProfile, WorkspaceInfo, GlobalSearchResultItem, AuthSessionResponse } from './types';
import { productName, productDescriptor } from './product-brand';
import { d2ThemeOverrides } from './styles/theme-overrides';

// 导航视图状态
type RailView = 'home' | 'chat' | 'employees' | 'tasks' | 'resources' | 'settings' | 'market' | 'platform-admin' | 'credits';
interface RailNavItem {
  view: RailView;
  label: string;
  paths?: string[];
  polylines?: string[];
  rects?: Array<{ width: number; height: number; x: number; y: number; rx?: number }>;
  circles?: Array<{ cx: number; cy: number; r: number; fill?: string }>;
}

const railNavItems: RailNavItem[] = [
  {view:'market',label:'首页',paths:['M3 10h18v11H3z','M2 3h20v7H2z','M10 14h4v7']},
  {
    view: 'employees',
    label: 'AI 员工',
    rects: [{ width: 18, height: 12, x: 3, y: 6, rx: 2 }],
    circles: [
      { cx: 8, cy: 12, r: 1.2, fill: 'currentColor' },
      { cx: 16, cy: 12, r: 1.2, fill: 'currentColor' }
    ],
    paths: ['M12 2v4', 'M9 15h6']
  },
  {
    view: 'chat',
    label: '对话',
    paths: ['M21 15a2 2 0 0 1-2 2H7l-4 4V5a2 2 0 0 1 2-2h14a2 2 0 0 1 2 2z']
  },
  {
    view: 'tasks',
    label: '任务',
    paths: ['M9 11l3 3L22 4', 'M21 12v7a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h11']
  },
  {
    view: 'resources',
    label: '资源库',
    paths: [
      'm12.83 2.18a2 2 0 0 0-1.66 0L2.6 6.08a1 1 0 0 0 0 1.83l8.58 3.91a2 2 0 0 0 1.66 0l8.58-3.9a1 1 0 0 0 0-1.83Z',
      'm22 12.5-8.58 3.91a2 2 0 0 1-1.66 0L3.18 12.5',
      'm22 17.5-8.58 3.91a2 2 0 0 1-1.66 0L3.18 17.5'
    ]
  },
  {
    view: 'settings',
    label: '设置',
    circles: [{ cx: 12, cy: 12, r: 3 }],
    paths: [
      'M19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06a2 2 0 0 1 0 2.83 2 2 0 0 1-2.83 0l-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V21a2 2 0 0 1-2 2 2 2 0 0 1-2-2v-.09A1.65 1.65 0 0 0 9 19.4a1.65 1.65 0 0 0-1.82.33l-.06.06a2 2 0 0 1-2.83 0 2 2 0 0 1 0-2.83l.06-.06a1.65 1.65 0 0 0 .33-1.82 1.65 1.65 0 0 0-1.51-1H3a2 2 0 0 1-2-2 2 2 0 0 1 2-2h.09A1.65 1.65 0 0 0 4.6 9a1.65 1.65 0 0 0-.33-1.82l-.06-.06a2 2 0 0 1 0-2.83 2 2 0 0 1 2.83 0l.06.06a1.65 1.65 0 0 0 1.82.33H9a1.65 1.65 0 0 0 1-1.51V3a2 2 0 0 1 2-2 2 2 0 0 1 2 2v.09a1.65 1.65 0 0 0 1 1.51 1.65 1.65 0 0 0 1.82-.33l.06-.06a2 2 0 0 1 2.83 0 2 2 0 0 1 0 2.83l-.06.06a1.65 1.65 0 0 0-.33 1.82V9a1.65 1.65 0 0 0 1.51 1H21a2 2 0 0 1 2 2 2 2 0 0 1-2 2h-.09a1.65 1.65 0 0 0-1.51 1z'
    ]
  }
];

const defaultView: RailView = realPlatform ? 'market' : 'employees';
const currentView = ref<RailView>(defaultView);
const referralEntry = ref(realPlatform && window.location.pathname.startsWith('/r/'));
const referralCode = computed(() => referralEntry.value ? referralCodeFromPath(window.location.pathname) : '');
const invitationToken = ref(referralEntry.value ? '' : readInvitationToken());
function leaveReferralEntry() {
  if (!referralEntry.value) return;
  window.history.replaceState(null, '', '/');
  referralEntry.value = false;
}
const showCreateWorkspace = ref(false);
const newWorkspaceName = ref('');
const creatingWorkspace = ref(false);
const workspaceCreateError = ref('');
async function createWorkspace() {
  if (creatingWorkspace.value || !newWorkspaceName.value.trim()) return;
  creatingWorkspace.value = true; workspaceCreateError.value = '';
  try {
    const ws = workspaceFromWire(await platformRequest<WorkspaceWire>('/workspaces', post({name: newWorkspaceName.value.trim(), description: ''})));
    workspaces.value.push(ws);
    activeWorkspaceId.value = ws.id;
    applyNavigation(defaultView);
    showCreateWorkspace.value = false;
    newWorkspaceName.value = '';
  } catch (error) { workspaceCreateError.value = error instanceof Error ? error.message : '创建失败'; }
  finally { creatingWorkspace.value = false; }
}
async function invitationCompleted(workspaceId?: string) {
  invitationToken.value = '';
  const url = new URL(window.location.href); url.searchParams.delete('invitation_token'); if (url.pathname.startsWith('/invite/')) url.pathname = '/'; window.history.replaceState(null, '', url);
  await bootstrapShell(workspaceId);
}
const subParam = ref<string | undefined>(undefined);
const isMobileSidebarOpen = ref(false);
const showAccountPopover = ref(false);
const isMobileViewport = ref(
  typeof window !== 'undefined' && typeof window.matchMedia === 'function'
    ? window.matchMedia('(max-width: 960px)').matches
    : false
);
const isRailCollapsed = computed(() => !isMobileViewport.value);

// 全局工作区与用户状态 (严格由后端 /api/auth/me 与 /api/workspaces 驱动，无本地写死兜底)
const shellStatus = ref<'loading' | 'unauthenticated' | 'ready' | 'error'>('loading');
const shellError = ref('');
const currentUser = ref<UserProfile | null>(null);
const canOpenPlatformAdminTab = computed(() => realPlatform && currentUser.value?.isPlatformAdmin === true);
const assistantEntry = ref<AssistantEntry>();
const assistantOpenRequest = ref(0);
const assistantDemo = ref<{card: AssistantCard; workspace: string; agent: string; key: string}>();
const assistantNavActive = computed(() => !!assistantEntry.value?.agent_id && currentView.value === 'chat' && !subParam.value && targetEmployeeId.value === assistantEntry.value.agent_id);
const visibleRailNavItems = computed(() => railNavItems.filter(item => item.view !== 'market' || realPlatform));
watch([activeWorkspaceId, shellStatus, () => currentUser.value?.id, currentView], ([workspace, status], _old, onCleanup) => {
  const controller = new AbortController();
  let retry: ReturnType<typeof setTimeout> | undefined;
  onCleanup(() => { controller.abort(); if (retry) clearTimeout(retry); });
  if (!realPlatform || !workspace || status !== 'ready') { assistantEntry.value = undefined; return; }
  if (_old?.[0] !== workspace || _old?.[2] !== currentUser.value?.id) assistantEntry.value = undefined;
  async function refresh() {
    try {
      const value = await assistantApi.entry(workspace, controller.signal);
      if (controller.signal.aborted) return;
      assistantEntry.value = value;
      if (value.pending) retry = setTimeout(refresh, 2500);
    } catch {
      if (!controller.signal.aborted) assistantEntry.value = undefined;
    }
  }
  void refresh();
}, {immediate:true});
const workspaces = ref<WorkspaceInfo[]>([]);
const currentWorkspace = computed(() => workspaces.value.find(w => w.id === activeWorkspaceId.value) ?? workspaces.value[0] ?? null);
const isLoggingOut = ref(false);

function workspaceRoleLabel(role?: string) {
  if (!role) return '成员';
  if (role === 'owner' || role.includes('所有者')) return '所有者';
  if (role === 'admin' || role.includes('管理员')) return '管理员';
  if (role === 'member' || role.includes('成员')) return '成员';
  return role.replace(/\s*\(.*?\)\s*/g, '').trim() || '成员';
}

// 全域搜索：请求与结果都绑定当前工作区。
const globalSearchInputRef = ref<InputInst | null>(null);
const globalSearchQuery = ref('');
const searchResults = ref<GlobalSearchResultItem[]>([]);
type SearchKind = GlobalSearchResultItem['kind'];
const searchKindLabels: Record<SearchKind, string> = {
  employee: 'AI 员工', task: '任务', conversation: '对话', work_item: '事项', skill: '技能', mcp: '插件',
};
const searchGroups = computed(() => (Object.keys(searchKindLabels) as SearchKind[])
  .map(kind => ({ kind, items: searchResults.value.filter(item => item.kind === kind) }))
  .filter(group => group.items.length));
const searchLoading = ref(false);
const searchError = ref('');
const searchUnavailable = ref<SearchKind[]>([]);
let searchRequest: AbortController | undefined;
const showSearchPopover = ref(false);
let searchDebounce: ReturnType<typeof setTimeout> | null = null;
let searchReqSeq = 0;
let pollTimer: ReturnType<typeof setInterval> | null = null;

function cancelPendingSearch() {
  searchReqSeq += 1;
  searchRequest?.abort();
  searchRequest = undefined;
  searchLoading.value = false;
  if (searchDebounce) {
    clearTimeout(searchDebounce);
    searchDebounce = null;
  }
}

function closeSearchPopover(clearQuery = false) {
  cancelPendingSearch();
  showSearchPopover.value = false;
  if (clearQuery) {
    globalSearchQuery.value = '';
    searchResults.value = [];
    searchError.value = '';
    searchUnavailable.value = [];
  }
}

function handleGlobalKeydown(e: KeyboardEvent) {
  if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === 'k') {
    e.preventDefault();
    globalSearchInputRef.value?.focus();
    if (!showSearchPopover.value && globalSearchInputRef.value) onSearchInput(globalSearchQuery.value);
  } else if (e.key === 'Escape') {
    if (showSearchPopover.value) {
      closeSearchPopover(false);
      globalSearchInputRef.value?.blur();
    } else if (isMobileSidebarOpen.value) {
      isMobileSidebarOpen.value = false;
    }
  }
}

async function enterAuthenticatedShell(me: UserProfile, wsList: WorkspaceInfo[], invitedWorkspaceId?: string) {
  if (!me?.id || !Array.isArray(wsList)) {
    throw new Error('未获取到有效登录信息或可用工作区列表');
  }
  const routeState = invitedWorkspaceId && wsList.some(w => w.id === invitedWorkspaceId)
    ? { ws: invitedWorkspaceId, view: defaultView }
    : readStateFromHash();
  currentUser.value = me;
  if (me) leaveReferralEntry();
  workspaces.value = wsList;
  if (routeState.ws && wsList.some(w => w.id === routeState.ws)) {
    activeWorkspaceId.value = routeState.ws;
  } else if (!wsList.some(w => w.id === activeWorkspaceId.value)) {
    activeWorkspaceId.value = wsList[0]?.id || '';
  }
  isApplyingHash = true;
  try {
    applyNavigation(routeState.view, routeState.sub, routeState.targetId, routeState.extraType, false);
  } finally {
    isApplyingHash = false;
  }
  shellStatus.value = 'ready';
  void refreshHumanCount();
  syncUrlHash(true);
}

async function bootstrapShell(invitedWorkspaceId?: string) {
  shellStatus.value = 'loading';
  shellError.value = '';
  try {
    const me = await authApi.getMe();
    const wsList = await authApi.getWorkspaces();
    await enterAuthenticatedShell(me, wsList, invitedWorkspaceId);
  } catch (err) {
    currentUser.value = null;
    workspaces.value = [];
    activeWorkspaceId.value = '';
    if (err instanceof D2ApiError && err.status === 401) {
      clearAuthToken();
      shellStatus.value = 'unauthenticated';
      return;
    }
    shellStatus.value = 'error';
    shellError.value = err instanceof Error ? err.message : '账号或工作区信息加载失败';
  }
}

async function handleAuthenticated(session: AuthSessionResponse) {
  shellStatus.value = 'loading';
  shellError.value = '';
  try {
    const wsList = session.workspaces?.length ? session.workspaces : await authApi.getWorkspaces();
    await enterAuthenticatedShell(session.user, wsList);
  } catch (err) {
    currentUser.value = null;
    workspaces.value = [];
    activeWorkspaceId.value = '';
    if (err instanceof D2ApiError && err.status === 401) {
      clearAuthToken();
      shellStatus.value = 'unauthenticated';
      return;
    }
    shellStatus.value = 'error';
    shellError.value = err instanceof Error ? err.message : '工作区信息加载失败';
  }
}

function invalidateLocalSession() {
  clearAuthToken(); currentUser.value = null; workspaces.value = []; activeWorkspaceId.value = ''; pendingApprovalsCount.value = 0; shellStatus.value = 'unauthenticated';
}
async function performLogout() {
  if (isLoggingOut.value) return;
  isLoggingOut.value = true;
  showAccountPopover.value = false;
  isMobileSidebarOpen.value = false;
  try {
    await authApi.logout();
  } catch (error) {
    if (realPlatform) {
      isLoggingOut.value = false;
      shellError.value = error instanceof Error ? error.message : '退出登录失败，请重试';
      return;
    }
  }
  {
    clearAuthToken();
    currentUser.value = null;
    workspaces.value = [];
    activeWorkspaceId.value = '';
    pendingApprovalsCount.value = 0;
    shellStatus.value = 'unauthenticated';
    isLoggingOut.value = false;
  }
}

function requestLogout() {
  runWithUnsavedGuard(() => {
    performLogout();
  });
}

function onSearchInput(val: string) {
  globalSearchQuery.value = val;
  cancelPendingSearch();
  searchResults.value = [];
  searchError.value = '';
  searchUnavailable.value = [];
  showSearchPopover.value = true;
  const q = val.trim();
  if (!q) return;
  const seq = searchReqSeq;
  const wsId = activeWorkspaceId.value;
  const request = searchRequest = new AbortController();
  searchLoading.value = true;
  const current = () => seq === searchReqSeq && !request.signal.aborted && activeWorkspaceId.value === wsId;
  searchDebounce = setTimeout(async () => {
    searchDebounce = null;
    try {
      const res = await dashboardApi.search(q, wsId, request.signal);
      if (!current()) return;
      searchResults.value = res.items;
      searchUnavailable.value = res.unavailable ?? [];
    } catch (error) {
      if (current()) searchError.value = error instanceof D2ApiError && [401,403,404].includes(error.status)
        ? '当前无法搜索这个工作区' : '搜索失败，请稍后重试';
    } finally {
      if (current()) searchLoading.value = false;
    }
  }, 180);
}

function openSearch() {
  if (!showSearchPopover.value) onSearchInput(globalSearchQuery.value);
}

function selectSearchItem(item: GlobalSearchResultItem) {
  closeSearchPopover(true);
  globalSearchInputRef.value?.blur();
  if (item.kind === 'work_item') navigate('tasks', 'work-items', item.targetId || item.id, 'work-item');
  else if (item.kind === 'task') navigate('tasks', 'tasks', item.targetId || item.id, 'task');
  else if (item.kind === 'conversation' && item.agent_id) navigate('chat', item.id, item.agent_id);
  else if (item.kind === 'employee') navigate('employees', undefined, item.targetId || item.id);
  else if (item.kind === 'skill') navigate('resources', 'skills', item.targetId || item.id);
  else if (item.kind === 'mcp') navigate('resources', 'mcp', item.targetId || item.id);
}

const d2ViewTitleMap: Record<string, string> = {
  home: '首页',
  chat: '对话',
  employees: 'AI 员工',
  market: '首页',
  tasks: '任务',
  resources: '资源库',
  credits: '我的积分',
  settings: '设置'
};

watch(
  [currentView, () => currentWorkspace.value?.name, shellStatus],
  ([view, wsName, status]) => {
    if (status === 'unauthenticated') {
      document.title = `登录与注册 · ${productName}`;
      return;
    }
    if (status === 'error') {
      document.title = `连接异常 · ${productName}`;
      return;
    }
    const label = d2ViewTitleMap[view] || '管理台';
    document.title = wsName ? `${label} · ${wsName} · ${productName}` : `${label} · ${productName}`;
  },
  { immediate: true }
);

let humanCountRequest = 0;
async function refreshHumanCount() {
  const request = ++humanCountRequest, workspace = activeWorkspaceId.value;
  if (!realPlatform || !workspace || shellStatus.value !== 'ready') return;
  try {
    const result = await humanRequestsApi.summary(workspace);
    if (request === humanCountRequest && workspace === activeWorkspaceId.value && shellStatus.value === 'ready') {
      pendingApprovalsCount.value = typeof result?.pending === 'number' && Number.isFinite(result.pending) ? result.pending : 0;
    }
  } catch {
    // The inbox owns load errors. Keep the last successful count.
  }
}


function updateHumanCount(count: number) {
  ++humanCountRequest;
  pendingApprovalsCount.value = count;
}

watch(activeWorkspaceId, wsId => {
  closeSearchPopover(true);
  pendingApprovalsCount.value = 0;
  void refreshHumanCount();
  if (!realPlatform && wsId) syncUrlHash(false);
});

let mobileMq: MediaQueryList | null = null;
function handleViewportChange(e: MediaQueryListEvent) {
  isMobileViewport.value = e.matches;
  if (!e.matches) {
    isMobileSidebarOpen.value = false;
  }
}

onMounted(() => {
  bootstrapShell();
  if (typeof window !== 'undefined' && typeof window.matchMedia === 'function') {
    mobileMq = window.matchMedia('(max-width: 960px)');
    isMobileViewport.value = mobileMq.matches;
    mobileMq.addEventListener('change', handleViewportChange);
  }
  window.addEventListener('beforeunload', handleBeforeUnload);
  window.addEventListener('keydown', handleGlobalKeydown);
  window.addEventListener('hashchange', handleHashChange);
  if (realPlatform) {
    pollTimer = setInterval(() => {
      if (!document.hidden) void refreshHumanCount();
    }, 10000);
  }
});

onUnmounted(() => {
  mobileMq?.removeEventListener('change', handleViewportChange);
  window.removeEventListener('beforeunload', handleBeforeUnload);
  window.removeEventListener('keydown', handleGlobalKeydown);
  window.removeEventListener('hashchange', handleHashChange);
  if (pollTimer) clearInterval(pollTimer);
  if (searchDebounce) clearTimeout(searchDebounce);
});

export interface DraftLeaveGuardHandler {
  hasUnsaved: () => boolean;
  save: () => Promise<boolean>;
  discard: () => void;
  getLabel?: () => string;
}

const activeDraftGuard = ref<DraftLeaveGuardHandler | null>(null);
const showShellUnsavedModal = ref(false);
const shellSavingLeave = ref(false);
const pendingLeaveAction = ref<(() => void) | null>(null);

provide('d2DraftGuard', {
  register(handler: DraftLeaveGuardHandler) {
    activeDraftGuard.value = handler;
  },
  unregister(handler: DraftLeaveGuardHandler) {
    if (activeDraftGuard.value === handler) {
      activeDraftGuard.value = null;
    }
  }
});

function handleBeforeUnload(e: BeforeUnloadEvent) {
  if (activeDraftGuard.value?.hasUnsaved()) {
    e.preventDefault();
    e.returnValue = '';
  }
}

function runWithUnsavedGuard(action: () => void) {
  if (activeDraftGuard.value?.hasUnsaved()) {
    pendingLeaveAction.value = action;
    showShellUnsavedModal.value = true;
    return;
  }
  action();
}

function cancelShellLeave() {
  showShellUnsavedModal.value = false;
  pendingLeaveAction.value = null;
}

function discardAndContinueLeave() {
  activeDraftGuard.value?.discard();
  showShellUnsavedModal.value = false;
  const act = pendingLeaveAction.value;
  pendingLeaveAction.value = null;
  act?.();
}

async function saveAndContinueLeave() {
  if (!activeDraftGuard.value || shellSavingLeave.value) return;
  shellSavingLeave.value = true;
  try {
    const ok = await activeDraftGuard.value.save();
    if (ok) {
      showShellUnsavedModal.value = false;
      const act = pendingLeaveAction.value;
      pendingLeaveAction.value = null;
      act?.();
    }
  } finally {
    shellSavingLeave.value = false;
  }
}

const targetEmployeeId = ref<string | undefined>(undefined);
const targetTaskId = ref<string | undefined>(undefined);
const targetWorkItemId = ref<string | undefined>(undefined);
const targetApprovalId = ref<string | undefined>(undefined);
const targetResourceId = ref<string | undefined>(undefined);

const validViews = new Set(['home', 'chat', 'employees', 'tasks', 'resources', 'settings', 'market', 'platform-admin', 'credits']);
let isApplyingHash = false;

function readStateFromHash(): {
  ws?: string | undefined;
  view: RailView;
  sub?: string | undefined;
  targetId?: string | undefined;
  extraType?: string | undefined;
} {
  const raw = window.location.hash.replace(/^#\/?/, '');
  const queryIdx = raw.indexOf('?');
  const params = new URLSearchParams(queryIdx >= 0 ? raw.slice(queryIdx + 1) : raw);
  const rawView = params.get('view') || defaultView;
  const view = (validViews.has(rawView) ? rawView : defaultView) as RailView;
  const sub = params.get('sub') || undefined;
  const ws = params.get('ws') || undefined;
  let targetId: string | undefined;
  let extraType: string | undefined;
  if (params.get('appr')) {
    targetId = params.get('appr') || undefined;
    extraType = 'approval';
  } else if (params.get('wi')) {
    targetId = params.get('wi') || undefined;
    extraType = 'work-item';
  } else if (params.get('task')) {
    targetId = params.get('task') || undefined;
    extraType = 'task';
  } else if (params.get('emp')) {
    targetId = params.get('emp') || undefined;
    extraType = 'employee';
  } else if (params.get('res')) {
    targetId = params.get('res') || undefined;
  }
  return { ws, view, sub, targetId, extraType };
}

function syncUrlHash(replace = false) {
  if (isApplyingHash || shellStatus.value !== 'ready') return;
  const params = new URLSearchParams();
  if (activeWorkspaceId.value) params.set('ws', activeWorkspaceId.value);
  params.set('view', currentView.value);
  if (subParam.value && subParam.value !== 'create') params.set('sub', subParam.value);
  if (targetEmployeeId.value) params.set('emp', targetEmployeeId.value);
  if (targetTaskId.value) params.set('task', targetTaskId.value);
  if (targetWorkItemId.value) params.set('wi', targetWorkItemId.value);
  if (targetApprovalId.value) params.set('appr', targetApprovalId.value);
  if (targetResourceId.value) params.set('res', targetResourceId.value);
  const qs = params.toString();
  const nextHash = qs ? `#/?${qs}` : '#/';
  if (window.location.hash === nextHash) return;
  if (replace) {
    window.history.replaceState(null, '', nextHash);
  } else {
    window.history.pushState(null, '', nextHash);
  }
}

function handleHashChange() {
  if (shellStatus.value !== 'ready') return;
  const routeState = readStateFromHash();
  const applyFromHash = () => {
    isApplyingHash = true;
    try {
      if (routeState.ws && workspaces.value.some(w => w.id === routeState.ws) && routeState.ws !== activeWorkspaceId.value) {
        activeWorkspaceId.value = routeState.ws;
      }
      applyNavigation(routeState.view, routeState.sub, routeState.targetId, routeState.extraType, false);
    } finally {
      isApplyingHash = false;
    }
    if (routeState.view === 'platform-admin' || (routeState.view === 'settings' && routeState.sub === 'platform-admin' && !subParam.value)) syncUrlHash(true);
  };
  if (activeDraftGuard.value?.hasUnsaved()) {
    syncUrlHash(true);
    runWithUnsavedGuard(applyFromHash);
  } else {
    applyFromHash();
  }
}

function applyNavigation(view: string, param?: string, targetId?: string, extraType?: string, updateUrl = true) {
  if (view === 'platform-admin') {
    view = 'settings';
    param = 'platform-admin';
  }
  if (view === 'settings' && param === 'platform-admin' && !canOpenPlatformAdminTab.value) param = undefined;
  closeSearchPopover(false);
  isMobileSidebarOpen.value = false;
  showAccountPopover.value = false;
  if (view === 'chat') currentView.value = 'chat';
  else if (view === 'employees') currentView.value = 'employees';
  else if (view === 'tasks') currentView.value = 'tasks';
  else if (view === 'resources') currentView.value = 'resources';
  else if (view === 'settings') currentView.value = 'settings';
  else if (view === 'market') currentView.value = 'market';
  else if (view === 'credits') currentView.value = 'credits';
  else if (view === 'home') currentView.value = 'home';
  else currentView.value = defaultView;
  subParam.value = param;

  targetResourceId.value = view === 'resources' ? targetId : undefined;
  targetEmployeeId.value = view === 'employees' || view === 'chat' || (view === 'tasks' && extraType === 'employee') ? targetId : undefined;
  if (view === 'tasks') {
    if (param === 'approvals' || extraType === 'approval') {
      targetApprovalId.value = targetId;
      targetTaskId.value = undefined;
      targetWorkItemId.value = undefined;
    } else if (param === 'work-items' || extraType === 'work-item') {
      targetWorkItemId.value = targetId;
      targetTaskId.value = undefined;
      targetApprovalId.value = undefined;
    } else if (extraType === 'employee') {
      targetTaskId.value = undefined;
      targetWorkItemId.value = undefined;
      targetApprovalId.value = undefined;
    } else {
      targetTaskId.value = targetId;
      targetWorkItemId.value = undefined;
      targetApprovalId.value = undefined;
    }
  } else {
    targetApprovalId.value = undefined;
    targetTaskId.value = undefined;
    targetWorkItemId.value = undefined;
  }
  if (updateUrl) {
    syncUrlHash(false);
  }
}

function syncChatEmployee(id: string, direct = false) {
  if (currentView.value !== 'chat' || (!direct && targetEmployeeId.value === id)) return;
  subParam.value = undefined;
  targetEmployeeId.value = id;
  syncUrlHash(true);
}

function openAssistant(card?: AssistantCard) {
  const agent = assistantEntry.value?.agent_id;
  if (!agent || card && !assistantEntry.value?.cards.some(item => item.key === card.key)) return;
  runWithUnsavedGuard(() => {
    if (agent !== assistantEntry.value?.agent_id) return;
    if (card) assistantDemo.value = {card, workspace: activeWorkspaceId.value, agent, key: crypto.randomUUID()};
    assistantOpenRequest.value++;
    applyNavigation('chat', undefined, agent);
  });
}

function navigate(view: string, param?: string, targetId?: string, extraType?: string) {
  const isSameEmpDetailSwitch =
    currentView.value === 'employees' &&
    view === 'employees' &&
    Boolean(targetId) &&
    targetEmployeeId.value !== targetId;
  const isLeavingCurrentView = view !== currentView.value || isSameEmpDetailSwitch;
  if (isLeavingCurrentView) {
    runWithUnsavedGuard(() => applyNavigation(view, param, targetId, extraType));
  } else {
    applyNavigation(view, param, targetId, extraType);
  }
}

function requestSwitchWorkspace(wsId: string) {
  isMobileSidebarOpen.value = false;
  showAccountPopover.value = false;
  if (wsId === activeWorkspaceId.value) return;
  runWithUnsavedGuard(() => {
    activeWorkspaceId.value = wsId;
  });
}

function openApproval(id: string) {
  // 统一人工确认入口：直接跳转至任务与协同中心的人在回路待确认看板并定位到具体审批项
  navigate('tasks', 'approvals', id, 'approval');
}

</script>

<template>
  <NConfigProvider :locale="zhCN" :date-locale="dateZhCN" :theme-overrides="d2ThemeOverrides">
    <NDialogProvider>
    <NMessageProvider placement="top-right" :container-style="{ top: '56px' }">
      <InvitationView v-if="realPlatform && invitationToken" :token="invitationToken" :user="currentUser" @authenticated="handleAuthenticated" @session-invalidated="invalidateLocalSession" @completed="invitationCompleted" @signout="performLogout" />
      <AuthView
        v-else-if="shellStatus === 'unauthenticated'"
        :initial-tab="referralEntry ? 'register' : 'login'"
        :referral-code="referralCode"
        @authenticated="handleAuthenticated"
      />
      <div v-else class="d2-shell">
        <!-- 1. 顶部栏 (Top Bar - 轻量全局导航：Logo、全域搜索、待人工确认) -->
        <header class="d2-topbar">
          <div class="topbar-left">
            <button
              type="button"
              class="mobile-menu-btn"
              :aria-expanded="isMobileSidebarOpen"
              :aria-label="isMobileSidebarOpen ? '关闭导航菜单' : '打开导航菜单'"
              @click="isMobileSidebarOpen = !isMobileSidebarOpen"
            >
              <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
                <line x1="3" y1="6" x2="21" y2="6"/><line x1="3" y1="12" x2="21" y2="12"/><line x1="3" y1="18" x2="21" y2="18"/>
              </svg>
            </button>
            <div class="brand-logo" :title="productName" @click="navigate(defaultView)">
              <img src="/brand/mark.svg" class="brand-logo-mark" alt="" aria-hidden="true" />
              <span class="brand-kiko-text">KIKO</span>
              <span class="platform-tag">{{ productDescriptor }}</span>
            </div>
          </div>

          <div class="topbar-center">
            <div class="omnibar-shell">
              <NPopover
                :show="showSearchPopover"
                trigger="manual"
                placement="bottom-start"
                :show-arrow="false"
                style="width: min(440px, 92vw);"
                @update:show="(v: boolean) => { if (!v) closeSearchPopover(); }"
                @clickoutside="closeSearchPopover()"
              >
                <template #trigger>
                  <div class="global-search">
                    <NInput
                      ref="globalSearchInputRef"
                      :value="globalSearchQuery"
                      placeholder="搜索员工、任务或对话…"
                      :maxlength="120"
                      size="small"
                      clearable
                      @update:value="onSearchInput"
                      @focus="openSearch"
                      @click="openSearch"
                    >
                      <template #prefix>
                        <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" class="search-prefix-icon">
                          <circle cx="11" cy="11" r="8"/><path d="m21 21-4.3-4.3"/>
                        </svg>
                      </template>
                      <template v-if="!globalSearchQuery" #suffix>
                        <kbd class="search-kbd" title="快捷聚焦搜索 (Cmd/Ctrl + K)">⌘K</kbd>
                      </template>
                    </NInput>
                  </div>
                </template>
                <div class="search-popover-list">
                  <div v-if="!globalSearchQuery.trim()" class="search-empty-hint">输入员工名称、任务标题或对话名称</div>
                  <div v-else-if="searchLoading" class="search-empty-hint" role="status">正在搜索…</div>
                  <div v-else-if="searchError" class="search-empty-hint" role="alert">{{ searchError }}</div>
                  <template v-else>
                    <div v-if="searchUnavailable.length" class="search-empty-hint" role="status">
                      {{ searchUnavailable.map(kind => searchKindLabels[kind]).join('、') }}暂时无法搜索
                    </div>
                    <div v-if="searchResults.length === 0 && !searchUnavailable.length" class="search-empty-hint" role="status">
                      未找到与「{{ globalSearchQuery }}」匹配的记录
                    </div>
                    <section v-for="group in searchGroups" :key="group.kind" :aria-label="searchKindLabels[group.kind]">
                      <div class="search-group-label">{{ searchKindLabels[group.kind] }}</div>
                      <button
                        v-for="item in group.items" :key="item.id" type="button"
                        class="ws-item-choice ws-item-choice--btn" @click="selectSearchItem(item)"
                      >
                        <div class="ws-item-info"><strong>{{ item.title }}</strong><span v-if="item.subtitle">{{ item.subtitle }}</span></div>
                      </button>
                    </section>
                  </template>
                </div>
              </NPopover>

              <template v-if="realPlatform && assistantEntry?.agent_id">
                <span class="omnibar-divider" aria-hidden="true"></span>
                <button
                  type="button"
                  class="assistant-pill-btn"
                  :class="{ 'is-active': assistantNavActive }"
                  aria-label="KIKO 助手"
                  @click="openAssistant()"
                >
                  <span class="assistant-pill-icon" aria-hidden="true">
                    <img src="/brand/kiko-assistant-nav.svg" alt="" width="18" height="18" />
                  </span>
                  <span class="assistant-pill-text">KIKO 助手</span>
                  <span class="assistant-pill-sub">· 随时提问</span>
                </button>
              </template>
            </div>
          </div>

          <div class="topbar-right">
            <ReferralInvite v-if="realPlatform && currentUser" :key="currentUser.id" :muted="pendingApprovalsCount > 0" />
            <!-- 待处理提醒入口 (人在回路全局统一入口：直达任务中心待确认审批流) -->
            <button
              type="button"
              v-if="realPlatform"
              class="reminder-pill-btn"
              :aria-label="`当前工作区待人工确认 ${pendingApprovalsCount} 项`"
              :class="{ 'has-pending': pendingApprovalsCount > 0 }"
              @click="navigate('tasks', 'approvals')"
            >
              <span v-if="pendingApprovalsCount > 0" class="reminder-dot" aria-hidden="true"></span>
              <span v-else class="bell-icon" aria-hidden="true">
                <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
                  <path d="M6 8a6 6 0 0 1 12 0c0 7 3 9 3 9H3s3-2 3-9"/><path d="M10.3 21a1.94 1.94 0 0 0 3.4 0"/>
                </svg>
              </span>
              <span class="reminder-text">待人工确认</span>
              <span v-if="pendingApprovalsCount > 0" class="badge-count tabular-num">{{ pendingApprovalsCount }}</span>
            </button>
          </div>
        </header>

        <!-- 2. 主体框架：左侧 RailNav + 右侧工作区 -->
        <div class="d2-body">
          <div
            v-if="isMobileSidebarOpen"
            class="mobile-nav-backdrop"
            @click="isMobileSidebarOpen = false"
          ></div>
          <aside class="d2-rail-nav" :class="{ 'mobile-open': isMobileSidebarOpen, collapsed: isRailCollapsed }">
            <div class="mobile-rail-header">
              <span>导航菜单</span>
              <button type="button" class="mobile-close-btn" aria-label="关闭导航菜单" @click="isMobileSidebarOpen = false">✕</button>
            </div>

            <!-- 上部：置顶 KIKO 助手座舱 + 主导航项目 -->
            <div class="rail-nav-scroll">
              <template v-if="realPlatform && assistantEntry?.agent_id">
                <div class="rail-kiko-wrap">
                  <NTooltip placement="right" :disabled="!isRailCollapsed" :delay="80" :duration="80">
                    <template #trigger>
                      <button
                        type="button"
                        class="rail-kiko-anchor"
                        :class="{ active: assistantNavActive }"
                        aria-label="快速打开 KIKO 助手"
                        @click="openAssistant()"
                      >
                        <img src="/brand/kiko-assistant-nav.svg" alt="" width="26" height="26" />
                        <span class="rail-kiko-dot" aria-hidden="true"></span>
                        <span class="rail-label">KIKO 助手</span>
                      </button>
                    </template>
                    KIKO 助手
                  </NTooltip>
                </div>
                <div class="rail-top-divider" aria-hidden="true"></div>
              </template>

              <nav class="nav-items-group" aria-label="主导航">
                <NTooltip
                  v-for="item in visibleRailNavItems"
                  :key="item.view"
                  placement="right"
                  :disabled="!isRailCollapsed"
                  :delay="80"
                  :duration="80"
                >
                  <template #trigger>
                    <button
                      type="button"
                      class="rail-item"
                      :class="{ active: currentView === item.view }"
                      :aria-label="item.label"
                      :aria-current="currentView === item.view ? 'page' : undefined"
                      @click="navigate(item.view)"
                    >
                      <span class="rail-icon" aria-hidden="true">
                        <svg width="17" height="17" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
                          <rect v-for="(r, idx) in (item.rects ?? [])" :key="'r-' + idx" :width="r.width" :height="r.height" :x="r.x" :y="r.y" :rx="r.rx" />
                          <circle v-for="(c, idx) in (item.circles ?? [])" :key="'c-' + idx" :cx="c.cx" :cy="c.cy" :r="c.r" :fill="c.fill || 'none'" />
                          <polyline v-for="(p, idx) in (item.polylines ?? [])" :key="'pl-' + idx" :points="p" />
                          <path v-for="(d, idx) in (item.paths ?? [])" :key="'p-' + idx" :d="d" />
                        </svg>
                      </span>
                      <span class="rail-label">{{ item.label }}</span>
                    </button>
                  </template>
                  {{ item.label }}
                </NTooltip>
              </nav>
            </div>

            <!-- 下部：账号与工作区切换 -->
            <div class="rail-footer">
                <NPopover
                  v-if="shellStatus === 'ready' && currentUser && currentWorkspace"
                  v-model:show="showAccountPopover"
                  trigger="click"
                  :placement="isMobileViewport ? 'top-start' : 'right-end'"
                  :show-arrow="false"
                  raw
                >
                  <template #trigger>
                    <button
                      type="button"
                      class="unified-account-card"
                      :aria-label="`${currentWorkspace.name} · ${currentUser.name}`"
                    >
                      <NTooltip placement="right" :disabled="!isRailCollapsed" :delay="80" :duration="80">
                        <template #trigger>
                          <span class="account-avatar-slot">
                            <span class="user-avatar-circle">{{ currentUser.name.slice(0, 1) }}</span>
                          </span>
                        </template>
                        {{ currentWorkspace.name }} · {{ currentUser.name }}
                      </NTooltip>
                      <div class="account-meta">
                        <div class="ws-row">
                          <span class="ws-title" :title="currentWorkspace.name">{{ currentWorkspace.name }}</span>
                          <span class="caret-icon">⌄</span>
                        </div>
                        <span class="user-role-label" :title="`${currentUser.name} · ${workspaceRoleLabel(currentWorkspace.role)}`">
                          {{ currentUser.name }} · {{ workspaceRoleLabel(currentWorkspace.role) }}
                        </span>
                      </div>
                    </button>
                  </template>

                  <!-- 点击弹出的统一组织与个人设置 Popover -->
                  <div class="account-popover-menu">
                    <section class="popover-workspaces" aria-label="切换工作区">
                      <div class="popover-workspaces-header">
                        <span class="popover-section-label">切换工作区</span>
                        <button
                          v-if="realPlatform"
                          type="button"
                          class="popover-workspace-create"
                          aria-label="创建工作区"
                          title="创建工作区"
                          @click="runWithUnsavedGuard(() => { showCreateWorkspace = true; showAccountPopover = false })"
                        >
                          <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" aria-hidden="true"><path d="M12 5v14M5 12h14"/></svg>
                        </button>
                      </div>
                      <div class="workspace-list">
                        <button
                          v-for="ws in workspaces"
                          :key="ws.id"
                          type="button"
                          class="ws-item-choice ws-item-choice--btn"
                          :class="{ selected: ws.id === activeWorkspaceId }"
                          :aria-current="ws.id === activeWorkspaceId ? 'true' : undefined"
                          @click="requestSwitchWorkspace(ws.id)"
                        >
                          <span class="ws-item-info"><strong :title="ws.name">{{ ws.name }}</strong></span>
                          <span v-if="ws.id === activeWorkspaceId" class="check-mark" aria-hidden="true">✓</span>
                        </button>
                      </div>
                    </section>

                    <div class="popover-divider" aria-hidden="true"></div>

                    <div class="popover-nav-list" role="group" aria-label="账号与工作区操作">
                      <button type="button" class="pop-nav-item pop-nav-item--btn" @click="navigate('settings')">工作区设置</button>
                      <button v-if="realPlatform" type="button" class="pop-nav-item pop-nav-item--btn" @click="navigate('credits')">我的积分</button>
                      <button
                        type="button"
                        class="pop-nav-item pop-nav-item--btn pop-nav-item--danger"
                        :disabled="isLoggingOut"
                        @click="requestLogout"
                      >
                        <span>{{ isLoggingOut ? '正在退出...' : '退出登录' }}</span>
                      </button>
                    </div>
                  </div>
                </NPopover>
                <NTooltip v-else-if="shellStatus === 'error'" placement="right" :disabled="!isRailCollapsed" :delay="80" :duration="80">
                  <template #trigger>
                    <button
                      type="button"
                      class="unified-account-card unified-account-card--error"
                      aria-label="登录信息加载失败，点击重试"
                      @click="bootstrapShell()"
                    >
                      <span class="account-avatar-slot">
                        <span class="user-avatar-circle user-avatar-circle--error">!</span>
                      </span>
                      <div class="account-meta">
                        <span class="ws-title">登录信息加载失败</span>
                        <span class="user-role-label">点击重试</span>
                      </div>
                    </button>
                  </template>
                  登录信息加载失败 · 点击重试
                </NTooltip>
                <div v-else class="unified-account-card unified-account-card--loading">
                  <NSkeleton circle size="small" style="width: 28px; height: 28px; flex-shrink: 0;" />
                  <div class="account-meta" style="gap: 4px;">
                    <NSkeleton text style="width: 80%; height: 12px;" />
                    <NSkeleton text style="width: 60%; height: 12px;" />
                  </div>
                </div>
            </div>
          </aside>

          <!-- 3. 内容区容器（中间立体突出白色画布） -->
          <main class="d2-main-content" :class="{ 'is-chat-sheet': currentView === 'chat' && shellStatus === 'ready' && !!currentWorkspace }">
            <NAlert v-if="shellError && shellStatus === 'ready'" type="error">{{ shellError }}</NAlert>
            <div v-if="shellStatus === 'loading'" class="shell-loading-skeleton">
              <div class="shell-sk-header">
                <div style="display: flex; flex-direction: column; gap: 6px; width: 280px;">
                  <NSkeleton text style="width: 60%; height: 24px;" />
                  <NSkeleton text style="width: 100%; height: 14px;" />
                </div>
                <NSkeleton style="width: 120px; height: 30px; border-radius: 6px;" />
              </div>
              <NSkeleton style="width: 100%; height: 76px; border-radius: 8px;" />
              <div class="shell-sk-grid">
                <NSkeleton v-for="n in 3" :key="'sh-sk-' + n" style="height: 140px; border-radius: 8px;" />
              </div>
            </div>
            <div v-else-if="shellStatus === 'error'" style="max-width: 560px; margin: 48px auto;">
              <NAlert type="error" title="无法加载当前用户与工作区信息">
                <p style="margin: 6px 0 12px 0;">{{ shellError }}</p>
                <NButton size="small" type="primary" @click="bootstrapShell()">重新加载</NButton>
              </NAlert>
            </div>
            <div v-else-if="!currentWorkspace" style="max-width: 560px; margin: 48px auto;">
              <h2>欢迎，{{ currentUser?.name }}</h2><p>创建工作区，或通过邀请链接加入团队。</p>
              <NButton type="primary" @click="showCreateWorkspace = true">创建工作区</NButton>
              <NButton quaternary @click="performLogout">退出登录</NButton>
            </div>
            <template v-else>
              <HomeDashboard
                v-if="currentView === 'home'"
                :key="'home-' + activeWorkspaceId"
                @navigate="navigate"
                @open-approval="openApproval"
              />
              <PlatformConversationsView v-else-if="realPlatform && currentView === 'chat'" :key="'manager-chat-' + currentUser?.id + '-' + activeWorkspaceId" :account-id="currentUser?.id" :selected-employee-id="targetEmployeeId" :selected-conversation-id="subParam" :assistant-entry="assistantEntry" :can-configure-models="['owner', 'admin'].includes(currentWorkspace.role)" @configure-models="navigate('resources', 'models')" :assistant-open-request="assistantOpenRequest" :assistant-demo="assistantDemo" @select-employee="syncChatEmployee" @demo-consumed="key => { if (assistantDemo?.key === key) assistantDemo = undefined }" />
              <ConversationsView
                v-else-if="currentView === 'chat'"
                :key="'chat-' + activeWorkspaceId"
                :selected-employee-id="targetEmployeeId"
                @navigate="navigate"
              />
              <AgentMarketView v-else-if="realPlatform && currentView === 'market'" :key="'market-'+activeWorkspaceId" :initial-tab="subParam" :platform-admin="currentUser?.isPlatformAdmin===true" :assistant-agent-id="assistantEntry?.agent_id" :assistant-cards="assistantEntry?.cards" @assistant-demo="openAssistant" @open-assistant="openAssistant()" @navigate="navigate"/>
              <MyCreditsView v-else-if="realPlatform && currentView === 'credits' && currentUser" :key="currentUser.id" :account-id="currentUser.id" />
              <WorkforceView
                v-else-if="currentView === 'employees'"
                :key="'emp-' + activeWorkspaceId"
                :initial-tab="subParam"
                :selected-employee-id="targetEmployeeId"
                @navigate="navigate"
              />
              <TasksAndCollaborationView v-else-if="realPlatform && currentView === 'tasks'" :key="'tasks-' + activeWorkspaceId"
                :workspace="activeWorkspaceId" :workspace-pending="pendingApprovalsCount" :initial-tab="subParam"
                :selected-work-id="targetWorkItemId" :selected-task-id="targetTaskId" :selected-approval-id="targetApprovalId"
                :selected-employee-id="targetEmployeeId" @count="updateHumanCount" @navigate="navigate" />
              <NAlert v-else-if="currentView === 'tasks'" type="info" title="任务与协同中心需要真实后端">
                本地演示不提供任务、事项、人工确认或任务空间数据。请连接真实后端后重新打开此页面。
              </NAlert>
              <ResourcesView
                :platform-admin="currentUser?.isPlatformAdmin === true"
                v-else-if="currentView === 'resources'"
                :key="'res-' + activeWorkspaceId"
                :initial-tab="subParam"
                :target-id="targetResourceId"
                @navigate="navigate"
              />
              <SettingsView
                v-else-if="currentView === 'settings'"
                :key="'set-' + activeWorkspaceId"
                :initial-tab="subParam"
                :workspace-id="activeWorkspaceId"
                :user="currentUser"
                @tab-changed="tab => { subParam = tab === 'workspace' ? undefined : tab; syncUrlHash() }"
                @renamed="ws => { workspaces = workspaces.map(w => w.id === ws.id ? workspaceFromWire(ws) : w) }"
              />
            </template>
          </main>
        </div>

        <NModal v-model:show="showCreateWorkspace" preset="card" title="创建工作区" style="width: min(460px, 94vw)">
          <NInput v-model:value="newWorkspaceName" maxlength="100" placeholder="工作区名称" />
          <NAlert v-if="workspaceCreateError" type="error">{{ workspaceCreateError }}</NAlert>
          <template #footer><NButton type="primary" :loading="creatingWorkspace" :disabled="!newWorkspaceName.trim()" @click="createWorkspace">创建</NButton></template>
        </NModal>
        <!-- 全局统一未保存草稿离开守卫弹窗 -->
        <NModal v-model:show="showShellUnsavedModal" preset="card" title="存在未保存的配置" style="width: min(480px, 94vw);">
          <div>
            <p style="margin-bottom: 12px; color: #334155; line-height: 1.6;">
              您对{{ activeDraftGuard?.getLabel?.() ? `「${activeDraftGuard.getLabel()}」` : '当前配置' }}进行了修改但尚未保存，离开当前页面将丢失未保存的内容。
            </p>
          </div>
          <template #footer>
            <div style="display: flex; justify-content: flex-end; gap: 8px; flex-wrap: wrap;">
              <NButton @click="cancelShellLeave">取消 (继续编辑)</NButton>
              <NButton type="error" secondary @click="discardAndContinueLeave">放弃修改并离开</NButton>
              <NButton type="primary" :loading="shellSavingLeave" @click="saveAndContinueLeave">保存并离开</NButton>
            </div>
          </template>
        </NModal>
      </div>
    </NMessageProvider>
    </NDialogProvider>
  </NConfigProvider>
</template>

<style scoped>
.d2-shell {
  min-height: 100vh;
  display: flex;
  flex-direction: column;
  background: var(--id-bg-canvas);
  color: var(--id-text-primary);
  font-family: var(--id-font-sans);
}

/* 顶栏：融入暖瓷白底壳 + 中央全能指令舱 */
.d2-topbar {
  height: 58px;
  background: var(--id-bg-canvas);
  display: flex;
  justify-content: space-between;
  align-items: center;
  padding: 0 18px;
  gap: 16px;
  position: sticky;
  top: 0;
  z-index: 100;
}
.topbar-left {
  display: flex;
  align-items: center;
  gap: 10px;
}
.mobile-menu-btn {
  display: none;
  align-items: center;
  justify-content: center;
  width: 32px;
  height: 32px;
  border-radius: var(--id-radius-md);
  border: 1px solid var(--id-border-subtle);
  background: var(--id-bg-surface);
  color: var(--id-text-secondary);
  cursor: pointer;
  flex-shrink: 0;
  transition: background-color 0.15s ease, color 0.15s ease, border-color 0.15s ease;
}
.mobile-menu-btn:hover {
  background: var(--id-bg-muted);
  color: var(--id-text-primary);
  border-color: var(--id-border-strong);
}
.mobile-menu-btn:focus-visible {
  outline: none;
  box-shadow: var(--id-focus-ring);
}
.mobile-rail-header {
  display: none;
  align-items: center;
  justify-content: space-between;
  padding: 2px 4px 10px 4px;
  margin-bottom: 8px;
  border-bottom: 1px solid var(--id-border-subtle);
  font-size: 13px;
  font-weight: 600;
  color: var(--id-text-primary);
}
.mobile-close-btn {
  width: 28px;
  height: 28px;
  border-radius: var(--id-radius-md);
  border: 1px solid var(--id-border-subtle);
  background: var(--id-bg-muted);
  color: var(--id-text-secondary);
  cursor: pointer;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  font-size: 13px;
}
.mobile-close-btn:hover {
  color: var(--id-text-primary);
  border-color: var(--id-border-strong);
}
.mobile-nav-backdrop {
  display: none;
}
.brand-logo {
  display: flex;
  align-items: center;
  gap: 8px;
  cursor: pointer;
  user-select: none;
}
.brand-logo-mark {
  height: 24px;
  width: 24px;
  display: block;
  object-fit: contain;
  flex-shrink: 0;
}
.brand-kiko-text {
  font-size: 16px;
  font-weight: 700;
  letter-spacing: -0.02em;
  line-height: 1;
  color: var(--id-text-primary);
}
.platform-tag {
  font-size: 11.5px;
  font-weight: 500;
  background: #ECEAE4;
  color: var(--id-text-secondary);
  padding: 2px 8px;
  border-radius: var(--id-radius-md);
  margin-left: 2px;
}

.topbar-center {
  flex: 1;
  min-width: 0;
  max-width: 650px;
  display: flex;
  justify-content: center;
}
.omnibar-shell {
  width: 100%;
  height: 40px;
  background: var(--id-bg-surface);
  border: 1px solid #DFDDD6;
  border-radius: var(--id-radius-pill);
  box-shadow: 0 2px 10px rgba(28, 25, 23, 0.04);
  display: flex;
  align-items: center;
  padding: 3px 4px 3px 8px;
  gap: 8px;
  transition: border-color 0.15s ease, box-shadow 0.15s ease;
}
.omnibar-shell:focus-within {
  border-color: var(--id-border-strong);
  box-shadow: 0 3px 14px rgba(28, 25, 23, 0.07);
}
.global-search {
  flex: 1;
  min-width: 0;
}
.global-search :deep(.n-input) {
  background: transparent !important;
  border: none !important;
  --n-border: none !important;
  --n-border-hover: none !important;
  --n-border-focus: none !important;
  --n-box-shadow-focus: none !important;
  --n-color: transparent !important;
  --n-color-focus: transparent !important;
}
.search-prefix-icon {
  color: var(--id-text-muted);
  flex-shrink: 0;
}
.search-kbd {
  font-family: var(--id-font-mono);
  font-size: 11px;
  line-height: 1.1;
  color: var(--id-text-muted);
  background: #F5F4F0;
  border: 1px solid var(--id-border-subtle);
  border-radius: 5px;
  padding: 2px 6px;
  user-select: none;
}
.omnibar-divider {
  width: 1px;
  height: 20px;
  background: var(--id-border-subtle);
  flex-shrink: 0;
}
.search-group-label { padding: 6px 8px; font-size: 12px; font-weight: 600; color: var(--id-text-secondary); }
.search-popover-list {
  display: flex;
  flex-direction: column;
  gap: 2px;
  max-height: min(320px, 60vh);
  overflow-y: auto;
}
.search-empty-hint {
  padding: 12px 10px;
  text-align: center;
  font-size: 13px;
  color: var(--id-text-muted);
}

.topbar-right {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-shrink: 0;
}
.assistant-pill-btn {
  display: inline-flex;
  align-items: center;
  gap: 7px;
  height: 32px;
  padding: 0 13px 0 5px;
  border-radius: 999px;
  border: none;
  background: linear-gradient(135deg, #10B981 0%, #059669 100%);
  color: #FFFFFF;
  cursor: pointer;
  white-space: nowrap;
  flex-shrink: 0;
  box-shadow: 0 2px 8px rgba(16, 185, 129, 0.28);
  transition: transform 0.15s ease, box-shadow 0.15s ease, filter 0.15s ease;
}
.assistant-pill-btn:hover {
  filter: brightness(1.03);
  box-shadow: 0 3px 12px rgba(16, 185, 129, 0.36);
}
.assistant-pill-btn:focus-visible {
  outline: none;
  box-shadow: 0 0 0 2px #FFFFFF, 0 0 0 4px #10B981;
}
.assistant-pill-btn.is-active {
  box-shadow: 0 0 0 2px rgba(16, 185, 129, 0.28), 0 3px 12px rgba(5, 150, 105, 0.36);
}
.assistant-pill-icon {
  width: 24px;
  height: 24px;
  border-radius: 50%;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  background: #FFFFFF;
  flex-shrink: 0;
}
.assistant-pill-text {
  font-size: 12.5px;
  font-weight: 600;
  line-height: 1;
}
.assistant-pill-sub {
  font-size: 11px;
  font-weight: 500;
  color: #D1FAE5;
  line-height: 1;
}
.reminder-pill-btn {
  display: inline-flex;
  align-items: center;
  gap: 7px;
  height: 34px;
  background: var(--id-bg-surface);
  border: 1px solid #DFDDD6;
  padding: 0 12px;
  border-radius: 999px;
  cursor: pointer;
  box-shadow: 0 1px 2px rgba(28, 25, 23, 0.03);
  transition: background 0.15s ease, border-color 0.15s ease, box-shadow 0.15s ease;
}
.reminder-pill-btn:hover {
  background: var(--id-bg-subtle);
  border-color: var(--id-border-strong);
}
.reminder-pill-btn:focus-visible {
  outline: none;
  box-shadow: var(--id-focus-ring);
}
.reminder-pill-btn.has-pending {
  padding: 0 8px 0 12px;
  background: linear-gradient(135deg, #FFFBEB 0%, #FEF3C7 100%);
  border-color: #F59E0B;
  box-shadow: 0 2px 8px rgba(245, 158, 11, 0.16);
}
.reminder-pill-btn.has-pending:hover {
  background: linear-gradient(135deg, #FEF3C7 0%, #FDE68A 100%);
}
.reminder-dot {
  width: 7px;
  height: 7px;
  border-radius: 50%;
  background: #F59E0B;
  box-shadow: 0 0 0 3px rgba(245, 158, 11, 0.22);
  flex-shrink: 0;
}
.bell-icon {
  display: inline-flex;
  align-items: center;
  color: var(--id-text-muted);
}
.reminder-text {
  font-size: 12.5px;
  font-weight: 500;
  color: var(--id-text-secondary);
}
.reminder-pill-btn.has-pending .reminder-text {
  color: #92400E;
  font-weight: 600;
}
.badge-count {
  background: #F59E0B;
  color: #FFFFFF;
  font-size: 11px;
  font-weight: 700;
  line-height: 1.2;
  padding: 1px 6px;
  border-radius: 999px;
  font-family: var(--id-font-mono);
}
.tabular-num {
  font-variant-numeric: tabular-nums;
}

/* 主体与固定折叠左侧主导航 */
.d2-body {
  display: flex;
  flex: 1;
  min-height: 0;
  padding: 0 14px 14px 8px;
  gap: 10px;
}
.d2-rail-nav {
  width: 60px;
  background: transparent;
  display: flex;
  flex-direction: column;
  justify-content: space-between;
  padding: 6px 0 4px;
  flex-shrink: 0;
  position: sticky;
  top: 58px;
  height: calc(100vh - 72px);
  align-self: flex-start;
  overflow: visible;
  z-index: 50;
}
.rail-nav-scroll {
  flex: 1;
  min-height: 0;
  overflow-y: auto;
  overflow-x: hidden;
  display: flex;
  flex-direction: column;
  align-items: center;
}
.rail-kiko-wrap {
  display: flex;
  justify-content: center;
  width: 100%;
}
.rail-kiko-anchor {
  appearance: none;
  width: 42px;
  height: 42px;
  border-radius: 13px;
  background: linear-gradient(145deg, #ECFDF5, #D1FAE5);
  border: 1.5px solid #34D399;
  box-shadow: 0 3px 10px rgba(16, 185, 129, 0.18);
  display: inline-flex;
  align-items: center;
  justify-content: center;
  position: relative;
  cursor: pointer;
  padding: 0;
  transition: transform 0.15s ease, box-shadow 0.15s ease, border-color 0.15s ease;
}
.rail-kiko-anchor:hover {
  transform: translateY(-1px);
  box-shadow: 0 4px 14px rgba(16, 185, 129, 0.25);
}
.rail-kiko-anchor.active {
  border-color: #059669;
  box-shadow: 0 0 0 2px rgba(16, 185, 129, 0.22), 0 4px 14px rgba(16, 185, 129, 0.22);
}
.rail-kiko-anchor:focus-visible {
  outline: none;
  box-shadow: var(--id-focus-ring);
}
.rail-kiko-dot {
  position: absolute;
  right: -2px;
  top: -2px;
  width: 9px;
  height: 9px;
  border-radius: 50%;
  background: #10B981;
  border: 2px solid var(--id-bg-canvas);
}
.rail-top-divider {
  width: 24px;
  height: 1px;
  background: #DFDDD6;
  margin: 10px 0;
  flex-shrink: 0;
}
.d2-rail-nav.collapsed {
  width: 58px;
}
.d2-rail-nav.collapsed .nav-items-group {
  align-items: center;
  justify-content: center;
}
.d2-rail-nav.collapsed .rail-footer {
  display: flex;
  justify-content: center;
}
.nav-items-group {
  display: flex;
  flex-direction: column;
  gap: 5px;
  width: 100%;
}
.rail-item {
  appearance: none;
  border: none;
  background: transparent;
  width: 100%;
  text-align: left;
  font-family: inherit;
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 8px 10px;
  border-radius: 10px;
  font-size: 13px;
  font-weight: 500;
  color: var(--id-text-muted);
  cursor: pointer;
  transition: background-color 0.15s ease, color 0.15s ease, box-shadow 0.15s ease;
}
.rail-label {
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.d2-rail-nav.collapsed .rail-item,
.d2-rail-nav.collapsed .rail-kiko-anchor {
  justify-content: center;
}
.d2-rail-nav.collapsed .rail-item {
  width: 40px;
  height: 38px;
  padding: 0;
  gap: 0;
}
.d2-rail-nav.collapsed .rail-label {
  display: none;
}
.rail-item:hover {
  background: rgba(255, 255, 255, 0.72);
  color: var(--id-text-primary);
}
.rail-item:focus-visible {
  outline: none;
  box-shadow: var(--id-focus-ring);
}
.rail-item.active {
  background: var(--id-bg-surface);
  color: var(--id-text-primary);
  font-weight: 600;
  box-shadow: 0 1px 4px rgba(28, 25, 23, 0.08);
}
.rail-item.active .rail-icon {
  color: var(--id-text-primary);
}
.rail-icon {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 18px;
  height: 18px;
  flex-shrink: 0;
  color: var(--id-text-muted);
  transition: color 0.15s ease;
}
.rail-item:hover .rail-icon {
  color: var(--id-text-primary);
}
.rail-icon svg {
  display: block;
}

/* 左下角整合卡片 */
.rail-footer {
  margin-top: 8px;
  padding-top: 8px;
  flex-shrink: 0;
}
.unified-account-card {
  appearance: none;
  width: 100%;
  text-align: left;
  font-family: inherit;
  display: flex;
  align-items: center;
  gap: 7px;
  padding: 7px 8px;
  border-radius: var(--id-radius-lg);
  background: var(--id-bg-subtle);
  border: 1px solid var(--id-border-subtle);
  cursor: pointer;
  transition: background-color 0.15s ease, border-color 0.15s ease;
}
.unified-account-card:focus-visible {
  outline: none;
  box-shadow: var(--id-focus-ring);
}
.account-avatar-slot {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  flex-shrink: 0;
}
.d2-rail-nav.collapsed .unified-account-card {
  width: 36px;
  height: 36px;
  padding: 0;
  gap: 0;
  justify-content: center;
  border-radius: 50%;
  border-color: transparent;
  background: transparent;
}
.d2-rail-nav.collapsed .account-avatar-slot {
  width: 100%;
  height: 100%;
}
.d2-rail-nav.collapsed .account-meta {
  display: none;
}
.unified-account-card:hover {
  background: var(--id-bg-surface);
  border-color: var(--id-border-strong);
}
.d2-rail-nav.collapsed .unified-account-card:hover {
  background: rgba(255, 255, 255, 0.72);
  border-color: transparent;
}
.unified-account-card--error {
  width: 100%;
  text-align: left;
  font-family: inherit;
  border-color: var(--id-status-danger-border);
  background: var(--id-status-danger-soft);
}
.unified-account-card--error:hover {
  border-color: var(--id-status-danger);
  background: var(--id-status-danger-soft);
}
.d2-rail-nav.collapsed .unified-account-card--error,
.d2-rail-nav.collapsed .unified-account-card--error:hover {
  background: transparent;
  border-color: transparent;
}
.unified-account-card--loading {
  cursor: default;
}
.unified-account-card--loading:hover {
  background: var(--id-bg-subtle);
  border-color: var(--id-border-subtle);
}
.d2-rail-nav.collapsed .unified-account-card--loading:hover {
  background: transparent;
  border-color: transparent;
}
.user-avatar-circle {
  width: 26px;
  height: 26px;
  border-radius: 50%;
  background: var(--id-text-primary);
  color: var(--id-text-inverse);
  display: flex;
  align-items: center;
  justify-content: center;
  font-size: 12px;
  font-weight: 600;
  flex-shrink: 0;
}
.user-avatar-circle--error {
  background: var(--id-status-danger);
}
.account-meta {
  display: flex;
  flex-direction: column;
  min-width: 0;
  flex: 1;
}
.ws-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 4px;
}
.ws-title {
  font-size: 12px;
  font-weight: 600;
  color: var(--id-text-primary);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.caret-icon {
  font-size: 12px;
  color: var(--id-text-muted);
  flex-shrink: 0;
}
.user-role-label {
  font-size: 12px;
  color: var(--id-text-muted);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

/* 账号与工作区浮层菜单 */
.account-popover-menu {
  width: min(276px, 84vw);
  background: var(--id-bg-surface);
  border: 1px solid var(--id-border-subtle);
  border-radius: var(--id-radius-lg);
  box-shadow: var(--id-shadow-lg);
  padding: 12px;
  display: flex;
  flex-direction: column;
  gap: 10px;
}
.popover-workspaces-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  margin-bottom: 4px;
  padding-inline-start: 10px;
}
.popover-workspace-create {
  display: grid;
  place-items: center;
  width: 28px;
  height: 28px;
  border: 1px solid var(--id-border-subtle);
  border-radius: var(--id-radius-md);
  background: var(--id-bg-surface);
  color: var(--id-text-secondary);
  cursor: pointer;
}
.popover-workspace-create:hover {
  background: var(--id-bg-muted);
  color: var(--id-text-primary);
}
.popover-workspace-create:focus-visible {
  outline: 2px solid var(--id-accent-primary);
  outline-offset: 2px;
}

.popover-divider {
  height: 1px;
  background: var(--id-border-hairline);
  margin: 2px 0;
}
.popover-section-label {
  font-size: 12px;
  font-weight: 600;
  color: var(--id-text-muted);
}
.workspace-list {
  max-height: min(240px, 38dvh);
  overflow: auto;
  display: flex;
  flex-direction: column;
  gap: 2px;
}
.ws-item-choice {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 8px;
  padding: 8px 10px;
  border-radius: var(--id-radius-md);
  cursor: pointer;
  transition: background-color 0.15s ease;
}
.ws-item-choice--btn {
  appearance: none;
  border: none;
  background: transparent;
  width: 100%;
  text-align: left;
  font-family: inherit;
}
.ws-item-choice:hover {
  background: var(--id-bg-muted);
}
.ws-item-choice.selected {
  background: var(--id-bg-muted);
}
.ws-item-info {
  display: flex;
  flex-direction: column;
  min-width: 0;
}
.ws-item-info strong {
  font-size: 13px;
  font-weight: 600;
  color: var(--id-text-primary);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.ws-item-info span {
  font-size: 12px;
  color: var(--id-text-muted);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.check-mark {
  color: var(--id-accent-primary);
  font-weight: 700;
  flex-shrink: 0;
}

.popover-nav-list {
  display: flex;
  flex-direction: column;
  gap: 2px;
}
.pop-nav-item {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 8px 10px;
  border-radius: var(--id-radius-md);
  font-size: 13px;
  color: var(--id-text-secondary);
  cursor: pointer;
  transition: background-color 0.15s ease, color 0.15s ease;
}
.pop-nav-item--btn {
  appearance: none;
  border: none;
  background: transparent;
  width: 100%;
  text-align: left;
  font-family: inherit;
}
.pop-nav-item:hover {
  background: var(--id-bg-muted);
  color: var(--id-text-primary);
}
.pop-nav-item--danger {
  color: var(--id-status-danger);
}
.pop-nav-item--danger:hover {
  background: var(--id-status-danger-soft);
  color: var(--id-status-danger);
}

/* 右侧主视窗（中间立体突出白色画布）与骨架屏 */
.d2-main-content {
  flex: 1;
  min-width: 0;
  background: var(--id-bg-surface);
  border: 1px solid #E4E2DC;
  border-radius: 16px;
  box-shadow: 0 8px 28px rgba(28, 25, 23, 0.05), 0 1px 3px rgba(28, 25, 23, 0.03);
  padding: 22px 28px;
  overflow-x: hidden;
}
.d2-main-content.is-chat-sheet {
  padding: 0;
  height: calc(100vh - 72px);
  min-height: 540px;
  overflow: hidden;
  display: flex;
  flex-direction: column;
}
.shell-loading-skeleton {
  display: flex;
  flex-direction: column;
  gap: 20px;
  padding: 4px 0;
}
.shell-sk-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 16px;
}
.shell-sk-grid {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: 16px;
}

/* 全局 Tabs 规范化：去重边框与高对比度中性态 */
:deep(.n-tabs.n-tabs--segment-type .n-tabs-rail) {
  background-color: var(--id-bg-muted) !important;
  padding: 3px !important;
  border-radius: var(--id-radius-md) !important;
  border: 1px solid var(--id-border-subtle) !important;
}
:deep(.n-tabs.n-tabs--segment-type .n-tabs-tab) {
  font-weight: 500 !important;
  color: var(--id-text-secondary) !important;
  transition: all 0.15s ease !important;
}
:deep(.n-tabs.n-tabs--segment-type .n-tabs-tab .n-tabs-tab__label) {
  color: var(--id-text-secondary) !important;
  font-weight: 500 !important;
  opacity: 1 !important;
  visibility: visible !important;
  display: inline-block !important;
}
:deep(.n-tabs.n-tabs--segment-type .n-tabs-tab.n-tabs-tab--active) {
  font-weight: 600 !important;
  color: var(--id-text-primary) !important;
  background-color: var(--id-bg-surface) !important;
  border-radius: 4px !important;
  box-shadow: var(--id-shadow-xs) !important;
}
:deep(.n-tabs.n-tabs--segment-type .n-tabs-tab.n-tabs-tab--active .n-tabs-tab__label) {
  color: var(--id-text-primary) !important;
  font-weight: 600 !important;
}
:deep(.n-tabs.n-tabs--line-type .n-tabs-tab) {
  color: var(--id-text-muted);
  font-weight: 500;
}
:deep(.n-tabs.n-tabs--line-type .n-tabs-tab:hover) {
  color: var(--id-text-primary);
}
:deep(.n-tabs.n-tabs--line-type .n-tabs-tab.n-tabs-tab--active) {
  color: var(--id-text-primary);
  font-weight: 600;
}

@media (max-width: 960px) {
  .d2-topbar {
    padding: 0 14px;
    gap: 10px;
  }
  .mobile-menu-btn {
    display: inline-flex;
  }
  .platform-tag,
  .assistant-pill-sub {
    display: none;
  }
  .d2-body {
    padding: 0 10px 10px;
    gap: 0;
  }
  .mobile-nav-backdrop {
    display: block;
    position: fixed;
    inset: 0;
    background: rgba(28, 25, 23, 0.35);
    z-index: 110;
  }
  .d2-rail-nav,
  .d2-rail-nav.collapsed {
    position: fixed;
    top: 0;
    left: 0;
    bottom: 0;
    height: 100vh;
    width: 236px;
    padding: 12px 10px;
    background: var(--id-bg-surface);
    border-right: 1px solid var(--id-border-subtle);
    z-index: 120;
    transform: translateX(-100%);
    transition: transform 0.2s ease;
    box-shadow: var(--id-shadow-drawer);
  }
  .d2-rail-nav.mobile-open {
    transform: translateX(0);
  }
  .d2-rail-nav.collapsed .rail-nav-scroll {
    align-items: stretch;
  }
  .d2-rail-nav.collapsed .rail-kiko-wrap {
    justify-content: stretch;
  }
  .d2-rail-nav.collapsed .rail-kiko-anchor {
    width: 100%;
    height: auto;
    padding: 8px 10px;
    gap: 10px;
    justify-content: flex-start;
  }
  .d2-rail-nav.collapsed .rail-top-divider {
    width: 100%;
  }
  .d2-rail-nav.collapsed .nav-items-group {
    align-items: stretch;
  }
  .d2-rail-nav.collapsed .rail-footer {
    display: block;
  }
  .d2-rail-nav.collapsed .rail-item {
    width: 100%;
    height: auto;
    padding: 8px 10px;
    gap: 10px;
    justify-content: flex-start;
  }
  .d2-rail-nav.collapsed .rail-label {
    display: inline;
  }
  .d2-rail-nav.collapsed .unified-account-card {
    width: 100%;
    height: auto;
    padding: 8px 10px;
    gap: 10px;
    justify-content: flex-start;
    border-radius: var(--id-radius-lg);
    border-color: var(--id-border-subtle);
    background: var(--id-bg-subtle);
  }
  .d2-rail-nav.collapsed .account-avatar-slot {
    width: auto;
    height: auto;
  }
  .d2-rail-nav.collapsed .account-meta {
    display: flex;
  }
  .mobile-rail-header {
    display: flex;
  }
  .d2-main-content {
    padding: 16px;
    border-radius: 12px;
  }
  .d2-main-content.is-chat-sheet {
    padding: 0;
    height: calc(100vh - 68px);
  }
  .shell-sk-grid {
    grid-template-columns: 1fr;
  }
}

@media (max-width: 760px) {
  .d2-topbar {
    height: auto;
    min-height: 54px;
    flex-wrap: wrap;
    padding: 8px 12px;
    gap: 8px;
  }
  .topbar-left {
    order: 1;
  }
  .topbar-right {
    order: 2;
    flex-wrap: wrap;
    justify-content: flex-end;
    gap: 6px;
  }
  .topbar-center {
    order: 3;
    flex: 1 1 100%;
    max-width: none;
    width: 100%;
  }
  .omnibar-shell {
    height: 38px;
  }
  .brand-logo-mark {
    width: 22px;
    height: 22px;
  }
  .brand-kiko-text {
    font-size: 15px;
  }
  .assistant-pill-btn,
  .reminder-pill-btn {
    padding: 0 10px;
    height: 30px;
  }
  .assistant-pill-btn {
    padding-left: 5px;
  }
  .search-kbd {
    display: none;
  }
  .d2-main-content {
    padding: 14px 12px;
  }
  .d2-main-content.is-chat-sheet {
    padding: 0;
    height: calc(100vh - 108px);
  }
}
</style>

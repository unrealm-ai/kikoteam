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
import AgentMarketView from './views/AgentMarketView.vue';
import MyCreditsView from './views/MyCreditsView.vue';

import { humanRequestsApi } from './api/human-requests';
import TasksAndCollaborationView from './views/TasksAndCollaborationView.vue';
import ResourcesView from './views/ResourcesView.vue';
import SettingsView from './views/SettingsView.vue';
import AuthView from './views/AuthView.vue';
import InvitationView from './views/InvitationView.vue';
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
  {view:'market',label:'AI 员工市场',paths:['M3 10h18v11H3z','M2 3h20v7H2z','M10 14h4v7']},
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

const SIDEBAR_COLLAPSE_STORAGE_KEY = 'kiko:d2:sidebar-collapsed';

function readInitialSidebarCollapsed(): boolean {
  try {
    return typeof window !== 'undefined' && window.localStorage.getItem(SIDEBAR_COLLAPSE_STORAGE_KEY) === '1';
  } catch {
    return false;
  }
}

const defaultView: RailView = realPlatform ? 'market' : 'employees';
const currentView = ref<RailView>(defaultView);
const invitationToken = ref(readInvitationToken());
const showCreateWorkspace = ref(false);
const newWorkspaceName = ref('');
const creatingWorkspace = ref(false);
const workspaceCreateError = ref('');
async function createWorkspace() {
  if (creatingWorkspace.value || !newWorkspaceName.value.trim()) return;
  creatingWorkspace.value = true; workspaceCreateError.value = '';
  try {
    const ws = workspaceFromWire(await platformRequest<WorkspaceWire>('/workspaces', post({name: newWorkspaceName.value.trim(), description: ''})));
    workspaces.value.push(ws); activeWorkspaceId.value = ws.id; currentView.value = 'settings'; showCreateWorkspace.value = false; newWorkspaceName.value = '';
  } catch (error) { workspaceCreateError.value = error instanceof Error ? error.message : '创建失败'; }
  finally { creatingWorkspace.value = false; }
}
async function invitationCompleted(workspaceId?: string) {
  invitationToken.value = '';
  const url = new URL(window.location.href); url.searchParams.delete('invitation_token'); if (url.pathname.startsWith('/invite/')) url.pathname = '/'; window.history.replaceState(null, '', url);
  await bootstrapShell();
  if (workspaceId && workspaces.value.some(w => w.id === workspaceId)) {
    activeWorkspaceId.value = workspaceId; currentView.value = 'settings'; syncUrlHash(true);
  }
}
const subParam = ref<string | undefined>(undefined);
const isMobileSidebarOpen = ref(false);
const showAccountPopover = ref(false);
const isSidebarCollapsed = ref(readInitialSidebarCollapsed());
const isMobileViewport = ref(
  typeof window !== 'undefined' && typeof window.matchMedia === 'function'
    ? window.matchMedia('(max-width: 960px)').matches
    : false
);
const isRailCollapsed = computed(() => !isMobileViewport.value && isSidebarCollapsed.value);

function toggleSidebarCollapse() {
  isSidebarCollapsed.value = !isSidebarCollapsed.value;
  try {
    window.localStorage.setItem(SIDEBAR_COLLAPSE_STORAGE_KEY, isSidebarCollapsed.value ? '1' : '0');
  } catch {
    // ignore storage errors
  }
}

// 全局工作区与用户状态 (严格由后端 /api/auth/me 与 /api/workspaces 驱动，无本地写死兜底)
const shellStatus = ref<'loading' | 'unauthenticated' | 'ready' | 'error'>('loading');
const shellError = ref('');
const currentUser = ref<UserProfile | null>(null);
const canOpenPlatformAdminTab = computed(() => realPlatform && currentUser.value?.isPlatformAdmin === true);
const visibleRailNavItems=computed(()=>railNavItems.filter(item=>item.view!=='market'||realPlatform));
const workspaces = ref<WorkspaceInfo[]>([]);
const currentWorkspace = computed(() => workspaces.value.find(w => w.id === activeWorkspaceId.value) ?? workspaces.value[0] ?? null);
const isLoggingOut = ref(false);

function authProviderLabel(provider?: UserProfile['authProvider']) {
  if (provider === 'google') return 'Google OAuth 登录';
  return '邮箱账号登录';
}

function workspaceRoleLabel(role?: string) {
  if (!role) return '成员';
  if (role === 'owner' || role.includes('所有者')) return '所有者';
  if (role === 'admin' || role.includes('管理员')) return '管理员';
  if (role === 'member' || role.includes('成员')) return '成员';
  return role.replace(/\s*\(.*?\)\s*/g, '').trim() || '成员';
}

// 全域搜索状态 (调用 /api/workspaces/:wsId/search)
const globalSearchInputRef = ref<InputInst | null>(null);
const globalSearchQuery = ref('');
const searchResults = ref<GlobalSearchResultItem[]>([]);
const showSearchPopover = ref(false);
let searchDebounce: ReturnType<typeof setTimeout> | null = null;
let searchReqSeq = 0;
let pollTimer: ReturnType<typeof setInterval> | null = null;

function cancelPendingSearch() {
  searchReqSeq += 1;
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
  }
}

function handleGlobalKeydown(e: KeyboardEvent) {
  if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === 'k') {
    e.preventDefault();
    globalSearchInputRef.value?.focus();
    if (globalSearchQuery.value.trim() && searchResults.value.length > 0) {
      showSearchPopover.value = true;
    }
  } else if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === 'b' && !isMobileViewport.value) {
    e.preventDefault();
    toggleSidebarCollapse();
  } else if (e.key === 'Escape') {
    if (showSearchPopover.value) {
      closeSearchPopover(false);
      globalSearchInputRef.value?.blur();
    } else if (isMobileSidebarOpen.value) {
      isMobileSidebarOpen.value = false;
    }
  }
}

async function enterAuthenticatedShell(me: UserProfile, wsList: WorkspaceInfo[]) {
  if (!me?.id || !Array.isArray(wsList)) {
    throw new Error('未获取到有效登录信息或可用工作区列表');
  }
  const routeState = readStateFromHash();
  currentUser.value = me;
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

async function bootstrapShell() {
  shellStatus.value = 'loading';
  shellError.value = '';
  try {
    const me = await authApi.getMe();
    const wsList = await authApi.getWorkspaces();
    await enterAuthenticatedShell(me, wsList);
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
  const q = val.trim();
  if (!q) {
    searchResults.value = [];
    showSearchPopover.value = false;
    return;
  }
  const seq = searchReqSeq;
  const wsId = activeWorkspaceId.value;
  searchDebounce = setTimeout(async () => {
    try {
      const res = await dashboardApi.search(q, wsId);
      if (seq !== searchReqSeq || globalSearchQuery.value.trim() !== q || activeWorkspaceId.value !== wsId) {
        return;
      }
      searchResults.value = res.items;
      showSearchPopover.value = true;
    } catch {
      if (seq === searchReqSeq) {
        searchResults.value = [];
      }
    }
  }, 180);
}

function selectSearchItem(item: GlobalSearchResultItem) {
  closeSearchPopover(true);
  globalSearchInputRef.value?.blur();
  if (item.kind === 'work_item') navigate('tasks', 'work-items', item.targetId, 'work-item');
  else if (item.kind === 'task') navigate('tasks', 'tasks', item.targetId, 'task');
  else if (item.kind === 'skill') navigate('resources', 'skills', item.targetId);
  else if (item.kind === 'mcp') navigate('resources', 'mcp', item.targetId);
  else navigate(item.targetMenu, undefined, item.targetId);
}

const d2ViewTitleMap: Record<string, string> = {
  home: '首页',
  chat: '对话',
  employees: 'AI 员工',
  market: 'AI 员工市场',
  tasks: '任务',
  resources: '资源库',
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
      pendingApprovalsCount.value = result.pending;
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
            <NPopover
              :show="showSearchPopover && Boolean(globalSearchQuery.trim())"
              trigger="manual"
              placement="bottom-start"
              :show-arrow="false"
              style="width: min(440px, 92vw);"
              @update:show="(v: boolean) => { if (!v) showSearchPopover = false; }"
              @clickoutside="showSearchPopover = false"
            >
              <template #trigger>
                <div class="global-search">
                  <NInput
                    ref="globalSearchInputRef"
                    :value="globalSearchQuery"
                    placeholder="搜索：员工名称、飞书/钉钉群、任务或事项..."
                    size="small"
                    clearable
                    @update:value="onSearchInput"
                    @focus="() => { if (globalSearchQuery.trim() && searchResults.length > 0) showSearchPopover = true; }"
                    @keydown.esc="showSearchPopover = false"
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
                <div v-if="globalSearchQuery.trim() && searchResults.length === 0" class="search-empty-hint">
                  未找到与「{{ globalSearchQuery }}」匹配的记录
                </div>
                <button
                  v-for="item in searchResults"
                  :key="item.kind + ':' + item.id"
                  type="button"
                  class="ws-item-choice ws-item-choice--btn"
                  @click="selectSearchItem(item)"
                >
                  <div class="ws-item-info">
                    <strong>{{ item.title }}</strong>
                    <span>{{ item.subtitle }}</span>
                  </div>
                </button>
              </div>
            </NPopover>
          </div>

          <div class="topbar-right">
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
            <!-- PC 端右边框悬浮折叠圆钮 -->
            <NTooltip placement="right" :delay="120" :duration="80">
              <template #trigger>
                <button
                  type="button"
                  class="rail-edge-toggle rail-collapse-btn"
                  :aria-expanded="!isSidebarCollapsed"
                  :aria-label="isSidebarCollapsed ? '展开左侧导航' : '收起左侧导航'"
                  @click="toggleSidebarCollapse"
                >
                  <svg v-if="isRailCollapsed" width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
                    <path d="m9 18 6-6-6-6"/>
                  </svg>
                  <svg v-else width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
                    <path d="m15 18-6-6 6-6"/>
                  </svg>
                </button>
              </template>
              {{ isRailCollapsed ? '展开导航 (⌘B)' : '收起导航 (⌘B)' }}
            </NTooltip>

            <div class="mobile-rail-header">
              <span>导航菜单</span>
              <button type="button" class="mobile-close-btn" aria-label="关闭导航菜单" @click="isMobileSidebarOpen = false">✕</button>
            </div>

            <!-- 上部主导航项目（独立纵向滚动，避免裁切右侧悬浮圆钮） -->
            <div class="rail-nav-scroll">
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
                    <div class="popover-user-section">
                      <div class="p-avatar">{{ currentUser.name.slice(0, 1) }}</div>
                      <div class="p-meta">
                        <strong class="p-name">{{ currentUser.name }}</strong>
                        <span class="p-email">{{ currentUser.email }}</span>
                      </div>
                    </div>

                    <div class="popover-divider"></div>

                    <div class="popover-section-label">切换工作区</div>
                    <div class="workspace-list">
                      <div
                        v-for="ws in workspaces"
                        :key="ws.id"
                        class="ws-item-choice"
                        :class="{ selected: ws.id === activeWorkspaceId }"
                        @click="requestSwitchWorkspace(ws.id)"
                      >
                        <div class="ws-item-info">
                          <strong>{{ ws.name }}</strong>
                          <span>{{ ws.desc }}</span>
                        </div>
                        <span v-if="ws.id === activeWorkspaceId" class="check-mark">✓</span>
                      </div>
                    </div>

                    <div class="workspace-quick-actions popover-ws-hint">
                      <NButton v-if="realPlatform" text @click="runWithUnsavedGuard(() => { showCreateWorkspace = true; showAccountPopover = false })">创建工作区</NButton><span v-else>工作区创建与团队加入由企业组织管理员统一分配</span>
                    </div>

                    <div class="popover-divider"></div>

                    <div class="popover-nav-list">
                      <button v-if="realPlatform" type="button" class="pop-nav-item pop-nav-item--btn" @click="showAccountPopover=false;navigate('credits')">我的积分</button>
                      <button type="button" class="pop-nav-item pop-nav-item--btn" @click="navigate('settings')">
                        <span>工作区与成员设置</span>
                      </button>
                      <div class="pop-nav-item pop-nav-item--sso">
                        <span>{{ authProviderLabel(currentUser.authProvider) }}</span>
                      </div>
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
                      @click="bootstrapShell"
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

          <!-- 3. 内容区容器 -->
          <main class="d2-main-content">
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
                <NButton size="small" type="primary" @click="bootstrapShell">重新加载</NButton>
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
              <PlatformConversationsView v-else-if="realPlatform && currentView === 'chat'" :key="'manager-chat-' + activeWorkspaceId" :selected-employee-id="targetEmployeeId" :selected-conversation-id="subParam" />
              <ConversationsView
                v-else-if="currentView === 'chat'"
                :key="'chat-' + activeWorkspaceId"
                :selected-employee-id="targetEmployeeId"
                @navigate="navigate"
              />
              <AgentMarketView v-else-if="realPlatform && currentView === 'market'" :key="'market-'+activeWorkspaceId" :platform-admin="currentUser?.isPlatformAdmin===true" @navigate="navigate"/>
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

/* 顶栏：轻量极简、1px 发丝线层级 */
.d2-topbar {
  height: 52px;
  background: var(--id-bg-surface);
  border-bottom: 1px solid var(--id-border-subtle);
  display: flex;
  justify-content: space-between;
  align-items: center;
  padding: 0 20px;
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
  font-size: 12px;
  font-weight: 500;
  background: var(--id-bg-muted);
  color: var(--id-text-secondary);
  padding: 2px 8px;
  border-radius: var(--id-radius-sm);
  margin-left: 2px;
  border: 1px solid var(--id-border-subtle);
}

.topbar-center {
  flex: 1;
  min-width: 0;
  max-width: 440px;
}
.global-search :deep(.n-input) {
  background: var(--id-bg-muted);
  border: 1px solid var(--id-border-subtle);
  transition: background-color 0.15s ease, border-color 0.15s ease;
}
.global-search :deep(.n-input:hover),
.global-search :deep(.n-input--focus) {
  background: var(--id-bg-surface);
  border-color: var(--id-border-strong);
}
.search-prefix-icon {
  color: var(--id-text-placeholder);
  flex-shrink: 0;
}
.search-kbd {
  font-family: var(--id-font-mono);
  font-size: 12px;
  line-height: 1;
  color: var(--id-text-muted);
  background: var(--id-bg-surface);
  border: 1px solid var(--id-border-subtle);
  border-radius: 4px;
  padding: 2px 5px;
  user-select: none;
}
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
  gap: 12px;
}
.reminder-pill-btn {
  display: inline-flex;
  align-items: center;
  gap: 8px;
  height: 32px;
  background: var(--id-bg-surface);
  border: 1px solid var(--id-border-subtle);
  padding: 0 12px;
  border-radius: 999px;
  cursor: pointer;
  transition: background-color 0.15s ease, border-color 0.15s ease;
}
.reminder-pill-btn:hover {
  background: var(--id-bg-muted);
  border-color: var(--id-border-strong);
}
.reminder-pill-btn:focus-visible {
  outline: none;
  box-shadow: var(--id-focus-ring);
}
.reminder-pill-btn.has-pending {
  background: var(--id-bg-surface);
  border-color: var(--id-border-strong);
}
.reminder-pill-btn.has-pending:hover {
  background: var(--id-bg-muted);
}
.reminder-dot {
  width: 6px;
  height: 6px;
  border-radius: 50%;
  background: var(--id-status-warning-dot);
  flex-shrink: 0;
}
.bell-icon {
  display: inline-flex;
  align-items: center;
  color: var(--id-text-muted);
}
.reminder-text {
  font-size: 13px;
  font-weight: 500;
  color: var(--id-text-primary);
}
.badge-count {
  background: var(--id-status-warning-soft);
  color: var(--id-status-warning);
  border: 1px solid var(--id-status-warning-border);
  font-size: 12px;
  font-weight: 600;
  line-height: 1;
  padding: 2px 6px;
  border-radius: 999px;
  font-family: var(--id-font-mono);
}
.tabular-num {
  font-variant-numeric: tabular-nums;
}

/* 主体与左侧主导航 */
.d2-body {
  display: flex;
  flex: 1;
}
.d2-rail-nav {
  width: var(--id-sidebar-width, 228px);
  background: var(--id-bg-surface);
  border-right: 1px solid var(--id-border-subtle);
  display: flex;
  flex-direction: column;
  justify-content: space-between;
  padding: 12px 8px;
  flex-shrink: 0;
  position: sticky;
  top: 52px;
  height: calc(100vh - 52px);
  align-self: flex-start;
  overflow: visible;
  transition: width 0.2s cubic-bezier(0.16, 1, 0.3, 1);
  z-index: 50;
}
.rail-edge-toggle {
  position: absolute;
  top: 50%;
  right: -11px;
  transform: translateY(-50%);
  z-index: 60;
  width: 22px;
  height: 22px;
  padding: 0;
  border-radius: 50%;
  border: 1px solid var(--id-border-subtle);
  background: var(--id-bg-surface);
  color: var(--id-text-muted);
  box-shadow: var(--id-shadow-sm);
  display: inline-flex;
  align-items: center;
  justify-content: center;
  cursor: pointer;
  opacity: 0.85;
  transition: opacity 0.15s ease, color 0.15s ease, border-color 0.15s ease, box-shadow 0.15s ease, transform 0.15s ease, background-color 0.15s ease;
}
.rail-edge-toggle:hover {
  opacity: 1;
  color: var(--id-text-primary);
  border-color: var(--id-border-strong);
  background: var(--id-bg-surface);
  box-shadow: var(--id-shadow-md);
  transform: translateY(-50%) scale(1.06);
}
.rail-edge-toggle:active {
  transform: translateY(-50%) scale(0.96);
}
.rail-edge-toggle:focus-visible {
  opacity: 1;
  outline: none;
  box-shadow: var(--id-focus-ring);
}
.rail-nav-scroll {
  flex: 1;
  min-height: 0;
  overflow-y: auto;
  overflow-x: hidden;
  margin: 0 -2px;
  padding: 0 2px;
}
.d2-rail-nav.collapsed {
  width: 64px;
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
  gap: 2px;
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
  border-radius: var(--id-radius-md);
  font-size: 13px;
  font-weight: 500;
  color: var(--id-text-secondary);
  cursor: pointer;
  transition: background-color 0.15s ease, color 0.15s ease;
}
.rail-label {
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.d2-rail-nav.collapsed .rail-item {
  width: 42px;
  height: 36px;
  padding: 0;
  gap: 0;
  justify-content: center;
}
.d2-rail-nav.collapsed .rail-label {
  display: none;
}
.rail-item:hover {
  background: var(--id-bg-muted);
  color: var(--id-text-primary);
}
.rail-item:focus-visible {
  outline: none;
  box-shadow: var(--id-focus-ring);
}
.rail-item.active {
  background: var(--id-bg-muted);
  color: var(--id-text-primary);
  font-weight: 600;
}
.rail-item.active .rail-icon {
  color: var(--id-accent-primary);
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
  padding-top: 10px;
  border-top: 1px solid var(--id-border-subtle);
  flex-shrink: 0;
  background: var(--id-bg-surface);
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
  background: var(--id-bg-muted);
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
.popover-user-section {
  display: flex;
  align-items: center;
  gap: 10px;
  padding-bottom: 6px;
}
.p-avatar {
  width: 36px;
  height: 36px;
  border-radius: 50%;
  background: var(--id-text-primary);
  color: var(--id-text-inverse);
  display: flex;
  align-items: center;
  justify-content: center;
  font-size: 14px;
  font-weight: 600;
  flex-shrink: 0;
}
.p-meta {
  display: flex;
  flex-direction: column;
  min-width: 0;
}
.p-name {
  font-size: 13px;
  font-weight: 600;
  color: var(--id-text-primary);
}
.p-email {
  font-size: 12px;
  color: var(--id-text-muted);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
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

.workspace-quick-actions {
  display: flex;
  gap: 8px;
}
.popover-ws-hint {
  font-size: 12px;
  line-height: 1.45;
  color: var(--id-text-muted);
  padding: 2px 2px 0;
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
.pop-nav-item--sso {
  color: var(--id-text-muted);
  font-size: 12px;
  cursor: default;
}
.pop-nav-item--sso:hover {
  background: transparent;
  color: var(--id-text-muted);
}
.pop-nav-item--danger {
  color: var(--id-status-danger);
}
.pop-nav-item--danger:hover {
  background: var(--id-status-danger-soft);
  color: var(--id-status-danger);
}

/* 右侧主视窗与骨架屏 */
.d2-main-content {
  flex: 1;
  padding: 20px 28px;
  max-width: 1600px;
  min-width: 0;
  overflow-x: hidden;
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
  .platform-tag {
    display: none;
  }
  .mobile-nav-backdrop {
    display: block;
    position: fixed;
    inset: 0;
    background: rgba(9, 9, 11, 0.35);
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
    z-index: 120;
    transform: translateX(-100%);
    transition: transform 0.2s ease;
    box-shadow: var(--id-shadow-drawer);
  }
  .d2-rail-nav.mobile-open {
    transform: translateX(0);
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
  .rail-edge-toggle {
    display: none;
  }
  .mobile-rail-header {
    display: flex;
  }
  .d2-main-content {
    padding: 16px;
  }
  .shell-sk-grid {
    grid-template-columns: 1fr;
  }
}

@media (max-width: 760px) {
  .d2-topbar {
    height: auto;
    min-height: 52px;
    flex-wrap: wrap;
    padding: 8px 12px;
    gap: 8px;
  }
  .topbar-left {
    order: 1;
  }
  .topbar-right {
    order: 2;
  }
  .topbar-center {
    order: 3;
    flex: 1 1 100%;
    max-width: none;
    width: 100%;
  }
  .brand-logo-mark {
    width: 22px;
    height: 22px;
  }
  .brand-kiko-text {
    font-size: 15px;
  }
  .reminder-pill-btn {
    padding: 0 10px;
    height: 30px;
  }
  .search-kbd {
    display: none;
  }
  .d2-main-content {
    padding: 14px 12px;
  }
}
</style>

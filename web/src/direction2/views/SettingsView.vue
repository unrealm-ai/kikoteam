<script setup lang="ts">
import { copyText } from '../utils/clipboard';
import { ref, computed, watch, inject, markRaw, onMounted, onUnmounted } from 'vue';
import {
  NTabs, NTabPane, NButton, NInput,
  NPagination, NModal, NAlert, NForm, NFormItem, NSelect, NEmpty, NSkeleton, useMessage
} from 'naive-ui';
import { settingsApi } from '../api';
import { D2ApiError } from '../api/client';
import WorkspaceInvitations from '../components/WorkspaceInvitations.vue';
import PlatformAdminView from './PlatformAdminView.vue';
import {workspaceSettings, memberFromWire} from '../api/workspace-settings';
import {realPlatform, type WorkspaceWire} from '../api/platform';
import type {UserProfile} from '../types';
import type { WorkspaceMember } from '../types';

const props = defineProps<{
  initialTab?: string | undefined;
  workspaceId: string;
  user: UserProfile | null;
}>();

type SettingsTab = 'workspace' | 'members' | 'platform-admin';
const emit = defineEmits<{
  (e: 'renamed', ws: WorkspaceWire): void;
  (e: 'tab-changed', tab: SettingsTab): void;
}>();
const workspace = ref<WorkspaceWire|null>(null);
const owner = computed(() => workspace.value?.role === 'owner');
const canManage = computed(() => !realPlatform || owner.value || workspace.value?.role === 'admin');
const controller = new AbortController();
const signal = controller.signal;
const message = useMessage();
const canManagePlatform = computed(() => realPlatform && props.user?.isPlatformAdmin === true);
function visibleTab(tab: string | number | undefined): SettingsTab {
  if (tab === 'members') return 'members';
  if (tab === 'platform-admin' && canManagePlatform.value) return 'platform-admin';
  return 'workspace';
}
const activeTab = ref<SettingsTab>(visibleTab(props.initialTab));
watch([() => props.initialTab, canManagePlatform], ([tab]) => { activeTab.value = visibleTab(tab); });
const platformAdmin = ref<InstanceType<typeof PlatformAdminView>>();
const pendingSettingsTab = ref<SettingsTab>();
function applyTab(tab: SettingsTab) {
  activeTab.value = tab;
  emit('tab-changed', tab);
}
function selectTab(tab: string | number) {
  const next = visibleTab(tab);
  if (activeTab.value === 'platform-admin' && next !== 'platform-admin' && platformAdmin.value?.hasUnsaved()) {
    pendingSettingsTab.value = next;
    return;
  }
  applyTab(next);
}
function discardAdminAndSwitch() {
  const next = pendingSettingsTab.value;
  pendingSettingsTab.value = undefined;
  platformAdmin.value?.discard();
  if (next) applyTab(next);
}

// 1. 工作区设置状态
const wsName = ref('');
const wsDesc = ref('');
const isSavingSettings = ref(false);

// 2. 成员与权限状态
type MemberRow = WorkspaceMember & {version?: number};
const members = ref<MemberRow[]>([]);
const memberPage = ref(1);
const memberTotal = ref(0);
const memberError = ref('');
const memberLoading = ref(false);
const memberOptions = [{label:'管理员',value:'admin'},{label:'普通成员',value:'member'}];
const statusOptions = [{label:'启用',value:'active'},{label:'停用',value:'disabled'}];
const canEdit = (m: MemberRow) => m.role !== 'owner' && (!realPlatform || (canManage.value && m.id !== props.user?.id && (owner.value || m.role === 'member')));
let memberRequest = 0;
async function loadMembers() {
  const seq = ++memberRequest;
  memberError.value = ''; memberLoading.value = true;
  try {
    const result = await workspaceSettings.members(props.workspaceId, memberPage.value, signal);
    if (seq !== memberRequest || signal.aborted) return;
    members.value = result.items.map(memberFromWire);
    memberTotal.value = result.total;
  } catch (e) {
    if (seq === memberRequest && !signal.aborted) memberError.value = e instanceof Error ? e.message : '成员加载失败';
  } finally { if (seq === memberRequest) memberLoading.value = false; }
}
watch([activeTab, workspace, memberPage], () => {
  if (realPlatform && workspace.value && activeTab.value === 'members') void loadMembers();
});
const showInviteModal = ref(false);
const inviteEmail = ref('');
const inviteRole = ref<'admin' | 'member'>('member');
const generatedInviteLink = ref('');

// 加载、错误与局部异步提交状态
const isLoading = ref(true);
const hasLoadedOnce = ref(false);
const loadError = ref('');

async function loadAllSettingsData() {
  isLoading.value = true;
  loadError.value = '';
  try {
    if (realPlatform) {
      const info = await workspaceSettings.get(props.workspaceId, signal);
      if (signal.aborted) return;
      workspace.value = info;
      resetFields();
      hasLoadedOnce.value = true;
      return;
    }
    const [settings, memberList] = await Promise.all([
      settingsApi.getSettings(),
      settingsApi.listMembers()
    ]);
    wsName.value = settings.name;
    wsDesc.value = settings.desc;
    members.value = memberList;
    hasLoadedOnce.value = true;
  } catch (err) {
    if (signal.aborted) return;
    const msg = err instanceof Error ? err.message : '加载工作区设置失败';
    loadError.value = msg;
    message.error(msg);
  } finally {
    isLoading.value = false;
  }
}

onMounted(loadAllSettingsData);
onUnmounted(() => controller.abort());

const dirty = computed(() => !!workspace.value && (wsName.value.trim() !== workspace.value.name || wsDesc.value !== (workspace.value.description || '')));
const validFields = computed(() => !!wsName.value.trim() && [...wsName.value.trim()].length <= 100 && [...wsDesc.value].length <= 2000);
const conflict = ref(false);
function resetFields() {
  wsName.value = workspace.value?.name || '';
  wsDesc.value = workspace.value?.description || '';
  conflict.value = false;
}
async function refreshConflict() {
  try { workspace.value = await workspaceSettings.get(props.workspaceId, signal); conflict.value = true; }
  catch (e) { if (!signal.aborted) message.error(e instanceof Error ? e.message : '最新配置加载失败'); }
}
async function saveWorkspaceSettings() {
  if (isSavingSettings.value || (realPlatform && (!workspace.value || !validFields.value || !canManage.value))) return false;
  isSavingSettings.value = true;
  try {
    if (realPlatform && workspace.value) {
      const result = await workspaceSettings.save(props.workspaceId, wsName.value.trim(), wsDesc.value, workspace.value.version, signal);
      if (signal.aborted) return false;
      workspace.value = result;
      resetFields();
      emit('renamed',result);
      message.success('工作区已保存');
      return true;
    }
    await settingsApi.saveSettings({name: wsName.value, desc: wsDesc.value});
    message.success('工作区已保存');
    return true;
  } catch (err) {
    if (signal.aborted) return false;
    if (err instanceof D2ApiError && err.status === 409) await refreshConflict();
    message.error(err instanceof Error ? err.message : '保存设置失败');
    return false;
  } finally {
    isSavingSettings.value = false;
  }
}

const draft = markRaw({
  hasUnsaved: () => dirty.value || platformAdmin.value?.hasUnsaved() === true,
  getLabel: () => platformAdmin.value?.hasUnsaved() ? '工作区或平台设置' : '工作区设置',
  async save() {
    if (dirty.value && await saveWorkspaceSettings() !== true) return false;
    return platformAdmin.value?.save() ?? true;
  },
  discard() {
    resetFields();
    platformAdmin.value?.discard();
  },
});
const guard = inject<{register:(value:typeof draft)=>void;unregister:(value:typeof draft)=>void}>('d2DraftGuard');
if (realPlatform) guard?.register(draft);
onUnmounted(() => { if (realPlatform) guard?.unregister(draft); });

function openInviteModal() {
  inviteEmail.value = '';
  inviteRole.value = 'member';
  generatedInviteLink.value = '';
  showInviteModal.value = true;
}

const isGeneratingInvite = ref(false);

async function generateInviteLink() {
  const email = inviteEmail.value.trim();
  if (!email || !/^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(email)) {
    message.warning('请输入格式正确的企业邮箱地址');
    return;
  }
  isGeneratingInvite.value = true;
  try {
    const res = await settingsApi.inviteMember({
      email,
      role: inviteRole.value
    });
    generatedInviteLink.value = res.inviteLink;
    members.value.push(res.member);
    message.success('邀请链接已生成！复制后发送给团队成员即可直接加入工作区。');
  } catch (err) {
    message.error(err instanceof Error ? err.message : '生成邀请失败');
  } finally {
    isGeneratingInvite.value = false;
  }
}

async function copyInviteLink() {
  try {
    await copyText(generatedInviteLink.value);
    message.success('邀请链接已复制到剪贴板！');
  } catch {
    message.error('复制失败，请手动复制邀请链接');
  }
}

const showMemberConfirmModal = ref(false);
const isUpdatingMember = ref(false);
const pendingMemberAction = ref<{
  kind: 'status' | 'role';
  member: MemberRow;
  nextStatus: 'active' | 'disabled';
  nextRole: 'owner' | 'admin' | 'member';
} | null>(null);

function toggleMemberStatus(m: MemberRow) {
  const nextStatus = m.status === 'active' ? 'disabled' : 'active';
  pendingMemberAction.value = {
    kind: 'status',
    member: m,
    nextRole: m.role, nextStatus
  };
  showMemberConfirmModal.value = true;
}

function onRoleChange(m: MemberRow, newRole: 'owner' | 'admin' | 'member') {
  if (m.role === newRole) return;
  pendingMemberAction.value = {
    kind: 'role',
    member: m,
    nextStatus: m.status, nextRole: newRole
  };
  showMemberConfirmModal.value = true;
}

async function confirmMemberAction() {
  const act = pendingMemberAction.value;
  if (!act || isUpdatingMember.value) return;
  isUpdatingMember.value = true;
  try {
    if (realPlatform) {
      if (!canEdit(act.member) || act.member.version === undefined) return;
      if (act.nextRole === act.member.role && act.nextStatus === act.member.status) return;
      const changes = {
        version:act.member.version,
        ...(act.nextRole && act.nextRole !== act.member.role ? {role:act.nextRole} : {}),
        ...(act.nextStatus && act.nextStatus !== act.member.status ? {status:act.nextStatus} : {})
      };
      const updated = await workspaceSettings.member(props.workspaceId, act.member.id, changes, signal);
      if (signal.aborted) return;
      members.value = members.value.map(row => row.id === updated.id ? memberFromWire(updated) : row);
      message.success('成员设置已保存');
    } else if (act.kind === 'status' && act.nextStatus) {
      const updated = await settingsApi.updateMember(act.member.id, { status: act.nextStatus });
      act.member.status = updated.status;
      message.info(`已${act.member.status === 'active' ? '恢复' : '禁用'}成员「${act.member.name}」的工作区使用权限。`);
    } else if (act.kind === 'role' && act.nextRole) {
      const updated = await settingsApi.updateMember(act.member.id, { role: act.nextRole });
      act.member.role = updated.role;
      message.success(`已将「${act.member.name}」的角色调整为：${act.nextRole === 'admin' ? '管理员' : '普通成员'}`);
    }
    showMemberConfirmModal.value = false;
    pendingMemberAction.value = null;
  } catch (err) {
    if (signal.aborted) return;
    if (realPlatform && err instanceof D2ApiError && err.status === 409) {
      showMemberConfirmModal.value = false;
      await loadMembers();
    }
    message.error(err instanceof Error ? err.message : '更新成员权限失败');
  } finally {
    isUpdatingMember.value = false;
  }
}

</script>

<template>
  <div class="d2-settings">
    <div class="settings-header">
      <div>
        <h2 class="view-title">工作区设置</h2>
        <p class="view-subtitle">管理当前工作区的基本信息、团队成员和邀请。</p>
      </div>
    </div>

    <!-- 1. 首次加载骨架屏 -->
    <div v-if="activeTab !== 'platform-admin' && isLoading && !hasLoadedOnce" class="settings-skeleton">
      <div class="sk-card">
        <NSkeleton text style="width: 160px; height: 18px;" />
        <div class="sk-grid-2">
          <NSkeleton style="height: 36px; border-radius: 6px;" />
          <NSkeleton style="height: 36px; border-radius: 6px;" />
        </div>
        <NSkeleton style="height: 36px; border-radius: 6px;" />
      </div>
      <div class="sk-card">
        <NSkeleton text style="width: 240px; height: 18px;" />
        <NSkeleton v-for="n in 3" :key="'set-sk-' + n" style="height: 52px; border-radius: 6px;" />
      </div>
    </div>

    <!-- 2. 首次加载失败恢复卡片 -->
    <div v-else-if="activeTab !== 'platform-admin' && !hasLoadedOnce && loadError" class="settings-error-card">
      <div class="err-copy">
        <strong>无法加载工作区设置</strong>
        <span>{{ loadError }}</span>
      </div>
      <NButton size="small" type="primary" @click="loadAllSettingsData">重试</NButton>
    </div>

    <!-- 3. 正常态内容 -->
    <NTabs :value="activeTab" @update:value="selectTab" type="line" class="settings-main-tabs">
      <!-- 1. 工作区设置 -->
      <NTabPane name="workspace" tab="工作区设置">
        <div v-if="hasLoadedOnce || (!isLoading && !loadError)" class="tab-panel-body">
          <div v-if="loadError" class="stale-banner">
            <span>部分配置刷新失败：{{ loadError }}</span>
            <NButton size="tiny" secondary @click="loadAllSettingsData">重试刷新</NButton>
          </div>

          <section class="settings-card">
            <div class="card-head">
              <h3 class="card-title">基本信息</h3>
            </div>
            <div class="form-grid">
              <div class="field-item">
                <label>工作区名称</label>
                <NInput v-model:value="wsName" :disabled="!canManage || isSavingSettings" placeholder="输入工作区名称" />
              </div>
              <div class="field-item">
                <label>工作区 ID</label>
                <NInput :value="workspaceId" disabled class="mono-input" />
              </div>
              <div class="field-item full-width">
                <label>工作区描述</label>
                <NInput v-model:value="wsDesc" :type="realPlatform ? 'textarea' : 'text'" :autosize="{minRows:2,maxRows:8}" :disabled="!canManage || isSavingSettings" placeholder="简述工作区用途" />
              </div>
            </div>
          </section>

          <NAlert v-if="realPlatform && canManage && !validFields" type="warning">名称须为 1–100 字，描述最多 2000 字。</NAlert>
          <NAlert v-if="conflict" type="warning" title="工作区已被其他管理员更新，当前输入已保留">
            <p>服务器名称：{{ workspace?.name }}</p><p class="conflict-description">服务器描述：{{ workspace?.description || '（空）' }}</p>
            <NButton size="small" @click="resetFields">采用服务器版本</NButton>
          </NAlert>
          <div v-if="canManage" class="workspace-save-bar">
            <div class="ws-save-info">
              <strong>保存工作区设置</strong>
            </div>
            <NButton type="primary" size="small" :disabled="realPlatform && (!dirty || !validFields)" :loading="isSavingSettings" @click="saveWorkspaceSettings">
              {{ conflict ? '确认用当前输入保存' : '保存工作区设置' }}
            </NButton>
          </div>
        </div>
      </NTabPane>

      <!-- 2. 团队成员与权限 (支持角色调整、禁用恢复与邀请链接) -->
      <NTabPane name="members" tab="成员与权限">
        <div v-if="hasLoadedOnce || (!isLoading && !loadError)" class="tab-panel-body">
          <div class="scope-notice-strip">
            <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" class="notice-icon" aria-hidden="true">
              <circle cx="12" cy="12" r="10"/><path d="M12 16v-4"/><path d="M12 8h.01"/>
            </svg>
            <p class="notice-text">
              工作区成员仅控制管理后台访问权限；AI 员工在群内的响应范围与可用能力需在各员工设置中单独配置。
            </p>
          </div>

          <div class="panel-top-actions">
            <span class="sub-desc">查看团队成员，管理工作区角色和访问权限。</span>
            <NButton v-if="canManage" type="primary" size="small" @click="openInviteModal">+ 邀请成员</NButton>
          </div>

          <NAlert v-if="memberError" type="error">{{ memberError }} <NButton text @click="loadMembers">重试</NButton></NAlert>
          <div class="members-table-card">
            <table class="custom-d2-table">
              <thead>
                <tr>
                  <th>成员</th>
                  <th>企业邮箱</th>
                  <th>工作区角色</th>
                  <th>使用状态</th>
                  <th>加入时间</th>
                  <th class="col-actions">操作</th>
                </tr>
              </thead>
              <tbody>
                <tr v-if="members.length === 0 && !memberError">
                  <td colspan="6" style="padding: 32px 0;">
                    <NEmpty :description="memberLoading ? '正在加载成员…' : '当前工作区暂无成员记录'" />
                  </td>
                </tr>
                <tr v-for="m in members" :key="m.id" :class="{ disabled: m.status === 'disabled' }">
                  <td>
                    <span class="member-name" :title="m.name">{{ m.name }}</span>
                  </td>
                  <td><code class="mono-text" :title="m.email">{{ m.email }}</code></td>
                  <td>
                    <span v-if="m.role === 'owner'" class="role-badge-owner">
                      所有者
                    </span>
                    <span v-else-if="realPlatform && (!owner || !canEdit(m))">{{ m.role === 'admin' ? '管理员' : '普通成员' }}</span>
                    <div v-else class="role-select-wrap">
                      <NSelect
                        :value="m.role"
                        size="small"
                        :options="memberOptions"
                        @update:value="(v: any) => onRoleChange(m, v)"
                      />
                    </div>
                  </td>
                  <td>
                    <span class="status-pill" :class="m.status === 'active' ? 'status-pill--success' : 'status-pill--muted'">
                      <span class="status-dot" :class="m.status === 'active' ? 'dot--success' : 'dot--muted'"></span>
                      <span>{{ m.status === 'active' ? '正常启用' : '已禁用' }}</span>
                    </span>
                  </td>
                  <td><span class="mono-text tabular-num muted-text">{{ m.joinedAt }}</span></td>
                  <td class="col-actions">
                    <div class="table-action-btns">
                      <NButton
                        v-if="canEdit(m)"
                        size="small"
                        :type="m.status === 'active' ? 'error' : 'primary'"
                        quaternary
                        @click="toggleMemberStatus(m)"
                      >
                        {{ m.status === 'active' ? '停用成员' : '恢复成员' }}
                      </NButton>
                      <span v-else class="muted-text">-</span>
                    </div>
                  </td>
                </tr>
              </tbody>
            </table>
          </div>
          <NPagination v-if="memberTotal > 20" v-model:page="memberPage" :page-size="20" :item-count="memberTotal" />
          <WorkspaceInvitations v-if="realPlatform && canManage" v-model:open="showInviteModal" :workspace="workspaceId" :owner="owner" />
        </div>
      </NTabPane>

      <NTabPane v-if="canManagePlatform" name="platform-admin" tab="平台管理">
        <PlatformAdminView v-if="activeTab === 'platform-admin'" ref="platformAdmin" />
      </NTabPane>
    </NTabs>
    <NModal :show="Boolean(pendingSettingsTab)" preset="card" title="平台配置尚未保存"
      style="width: min(440px, 94vw)" @update:show="show => { if (!show) pendingSettingsTab = undefined }">
      请在平台管理内保存，或放弃本次修改后再切换页签。
      <template #footer>
        <NButton @click="pendingSettingsTab = undefined">继续编辑</NButton>
        <NButton type="error" @click="discardAdminAndSwitch">放弃并切换</NButton>
      </template>
    </NModal>

    <!-- 邀请成员弹窗 (生成专属邀请 Token 链接) -->
    <NModal v-if="!realPlatform" v-model:show="showInviteModal" preset="card" title="邀请团队成员加入工作区" style="width: min(520px, 94vw);">
      <NForm label-placement="top">
        <NFormItem label="被邀请人企业邮箱">
          <NInput v-model:value="inviteEmail" placeholder="colleague@example.com" />
        </NFormItem>
        <NFormItem label="分配初始角色">
          <NSelect
            v-model:value="inviteRole"
            :options="[
              { label: '管理员 (可配置员工、技能与通道)', value: 'admin' },
              { label: '普通成员 (可查看事项与发起协同)', value: 'member' }
            ]"
          />
        </NFormItem>

        <div v-if="generatedInviteLink" class="invite-link-box">
          <span class="il-label">专属邀请链接与 Token</span>
          <code class="il-code">{{ generatedInviteLink }}</code>
          <div style="display: flex; justify-content: flex-end;">
            <NButton size="tiny" type="primary" secondary @click="copyInviteLink">复制链接</NButton>
          </div>
        </div>
      </NForm>
      <template #footer>
        <div style="display: flex; justify-content: flex-end; gap: 8px;">
          <NButton @click="showInviteModal = false">关闭</NButton>
          <NButton type="primary" :loading="isGeneratingInvite" @click="generateInviteLink">生成邀请链接</NButton>
        </div>
      </template>
    </NModal>

    <!-- 成员角色变更与账号禁用二次确认弹窗 -->
    <NModal
      v-model:show="showMemberConfirmModal"
      preset="card"
      :title="realPlatform ? '确认修改成员权限' : pendingMemberAction?.kind === 'status' ? (pendingMemberAction.nextStatus === 'disabled' ? '确认禁用成员账号' : '确认恢复成员账号') : '确认变更成员权限角色'"
      style="width: min(460px, 94vw);"
    >
      <div v-if="pendingMemberAction" style="display: flex; flex-direction: column; gap: 10px;">
        <template v-if="realPlatform">
          <strong>{{ pendingMemberAction.member.name }} · {{ pendingMemberAction.member.email }}</strong>
          <NForm label-placement="top">
            <NFormItem v-if="owner" label="工作区角色"><NSelect :virtual-scroll="false" v-model:value="pendingMemberAction.nextRole" :options="memberOptions" :disabled="isUpdatingMember" /></NFormItem>
            <NFormItem label="使用状态"><NSelect v-model:value="pendingMemberAction.nextStatus" :options="statusOptions" :disabled="isUpdatingMember" /></NFormItem>
          </NForm>
          <p v-if="pendingMemberAction.nextStatus === 'disabled'">停用后，该成员将无法访问本工作区。</p>
        </template>
        <p v-else style="margin: 0; font-size: 13px; color: var(--id-text-primary); line-height: 1.6;">
          <template v-if="pendingMemberAction.kind === 'status'">
            确定要<strong>{{ pendingMemberAction.nextStatus === 'disabled' ? '禁用' : '恢复' }}</strong>成员「<strong>{{ pendingMemberAction.member.name }}</strong> ({{ pendingMemberAction.member.email }})」的工作区访问权限吗？
          </template>
          <template v-else>
            确定要将成员「<strong>{{ pendingMemberAction.member.name }}</strong>」的角色调整为
            <strong>{{ pendingMemberAction.nextRole === 'admin' ? '管理员' : '普通成员' }}</strong> 吗？
          </template>
        </p>
        <NAlert v-if="!realPlatform"
          :type="pendingMemberAction.kind === 'status' && pendingMemberAction.nextStatus === 'disabled' ? 'warning' : 'info'"
          :show-icon="true"
        >
          <template v-if="pendingMemberAction.kind === 'status' && pendingMemberAction.nextStatus === 'disabled'">
            禁用后，该成员将无法进入本工作区配置 AI 员工或处理待人工确认事项。
          </template>
          <template v-else-if="pendingMemberAction.kind === 'status'">
            恢复后，该成员将立即重新获得对应角色的工作区操作权限。
          </template>
          <template v-else>
            {{ pendingMemberAction.nextRole === 'admin' ? '管理员可配置 AI 员工、渠道群策略、模型凭据与处理待人工确认事项。' : '普通成员仅可查看事项看板、参与工作区对话，无法修改 AI 员工安全策略。' }}
          </template>
        </NAlert>
      </div>
      <template #footer>
        <div style="display: flex; justify-content: flex-end; gap: 8px;">
          <NButton @click="showMemberConfirmModal = false">取消</NButton>
          <NButton
            :type="pendingMemberAction?.kind === 'status' && pendingMemberAction?.nextStatus === 'disabled' ? 'error' : 'primary'"
            :loading="isUpdatingMember"
            :disabled="realPlatform && pendingMemberAction?.nextRole === pendingMemberAction?.member.role && pendingMemberAction?.nextStatus === pendingMemberAction?.member.status"
            @click="confirmMemberAction"
          >
            确认执行
          </NButton>
        </div>
      </template>
    </NModal>
  </div>
</template>

<style scoped>
.conflict-description { white-space: pre-wrap; overflow-wrap: anywhere; }

.d2-settings {
  display: flex;
  flex-direction: column;
  gap: 16px;
}
.settings-header {
  padding: 4px 0 8px 0;
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
  color: var(--id-text-muted);
  margin: 0;
}

/* 骨架屏与错误恢复态 */
.settings-skeleton {
  display: flex;
  flex-direction: column;
  gap: 16px;
}
.sk-card {
  background: var(--id-bg-surface);
  border: 1px solid var(--id-border-subtle);
  border-radius: var(--id-radius-lg);
  padding: 20px;
  display: flex;
  flex-direction: column;
  gap: 14px;
}
.sk-grid-2 {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 12px;
}
.settings-error-card {
  background: var(--id-bg-surface);
  border: 1px solid var(--id-status-danger-border);
  border-radius: var(--id-radius-lg);
  padding: 16px 20px;
  display: flex;
  align-items: center;
  justify-content: space-between;
  flex-wrap: wrap;
  gap: 16px;
}
.err-copy {
  display: flex;
  flex-direction: column;
  gap: 4px;
}
.err-copy strong {
  font-size: 14px;
  color: var(--id-text-primary);
}
.err-copy span {
  font-size: 13px;
  color: var(--id-status-danger);
}
.stale-banner {
  background: var(--id-status-warning-soft);
  border: 1px solid var(--id-status-warning-border);
  border-radius: var(--id-radius-md);
  padding: 8px 14px;
  display: flex;
  align-items: center;
  justify-content: space-between;
  flex-wrap: wrap;
  gap: 12px;
  font-size: 13px;
  color: var(--id-status-warning);
}

.tab-panel-body {
  display: flex;
  flex-direction: column;
  gap: 16px;
  padding-top: 12px;
}

.sub-desc {
  font-size: 13px;
  color: var(--id-text-secondary);
}

/* 通知提示条（替代饱和蓝 NAlert） */
.scope-notice-strip {
  display: flex;
  align-items: flex-start;
  gap: 10px;
  padding: 12px 16px;
  background: var(--id-bg-surface);
  border: 1px solid var(--id-border-subtle);
  border-radius: var(--id-radius-lg);
}
.notice-icon {
  color: var(--id-text-muted);
  flex-shrink: 0;
  margin-top: 2px;
}
.notice-text {
  margin: 0;
  font-size: 13px;
  line-height: 1.55;
  color: var(--id-text-secondary);
}
.notice-text strong {
  color: var(--id-text-primary);
  font-weight: 600;
}

/* 1. 工作区卡片 */
.settings-card {
  background: var(--id-bg-surface);
  border: 1px solid var(--id-border-subtle);
  border-radius: var(--id-radius-lg);
  padding: 20px;
  display: flex;
  flex-direction: column;
  gap: 16px;
}
.card-head {
  display: flex;
  align-items: baseline;
  justify-content: space-between;
  gap: 12px;
  flex-wrap: wrap;
}
.card-title {
  font-size: 14px;
  font-weight: 600;
  color: var(--id-text-primary);
  margin: 0;
}
.card-meta {
  font-size: 12px;
  color: var(--id-text-muted);
}
.form-grid {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 16px;
}
.field-item {
  display: flex;
  flex-direction: column;
  gap: 6px;
}
.field-item.full-width {
  grid-column: span 2;
}
.field-item label {
  font-size: 12px;
  font-weight: 500;
  color: var(--id-text-secondary);
}
.mono-input :deep(input) {
  font-family: var(--id-font-mono);
}
.tabular-num {
  font-variant-numeric: tabular-nums;
}

/* 保存栏 */
.workspace-save-bar {
  display: flex;
  justify-content: space-between;
  align-items: center;
  flex-wrap: wrap;
  gap: 16px;
  padding: 14px 18px;
  background: var(--id-bg-surface);
  border: 1px solid var(--id-border-subtle);
  border-radius: var(--id-radius-lg);
}
.ws-save-info {
  display: flex;
  flex-direction: column;
  gap: 2px;
}
.ws-save-info strong {
  font-size: 13px;
  color: var(--id-text-primary);
}
.ws-save-info span {
  font-size: 12px;
  color: var(--id-text-secondary);
}

/* 2. 成员表格 */

.col-actions {
  text-align: right;
}
.member-name {
  font-weight: 600;
  color: var(--id-text-primary);
}
.mono-text {
  font-family: var(--id-font-mono);
  font-size: 12px;
  color: var(--id-text-secondary);
}
.muted-text {
  color: var(--id-text-muted);
}
.role-select-wrap {
  width: 176px;
  min-width: 176px;
}
.role-badge-owner {
  display: inline-flex;
  align-items: center;
  padding: 3px 10px;
  border-radius: 999px;
  font-size: 12px;
  font-weight: 500;
  background: var(--id-bg-muted);
  color: var(--id-text-primary);
  border: 1px solid var(--id-border-subtle);
}

/* 6px 语义状态点微标签 */
.status-pill {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  font-size: 12px;
  font-weight: 500;
  padding: 2px 8px;
  border-radius: 999px;
  border: 1px solid transparent;
  white-space: nowrap;
}
.status-pill--success {
  background: var(--id-status-success-soft);
  color: var(--id-status-success);
  border-color: var(--id-status-success-border);
}
.status-pill--warning {
  background: var(--id-status-warning-soft);
  color: var(--id-status-warning);
  border-color: var(--id-status-warning-border);
}
.status-pill--error {
  background: var(--id-status-danger-soft);
  color: var(--id-status-danger);
  border-color: var(--id-status-danger-border);
}
.status-pill--muted {
  background: var(--id-bg-muted);
  color: var(--id-text-muted);
  border-color: var(--id-border-subtle);
}
.status-dot {
  width: 6px;
  height: 6px;
  border-radius: 50%;
  flex-shrink: 0;
}
.dot--success { background: var(--id-status-success-dot); }
.dot--warning { background: var(--id-status-warning-dot); }
.dot--error { background: var(--id-status-danger-dot); }
.dot--muted { background: var(--id-text-placeholder); }

/* 邀请链接 */
.il-label {
  font-size: 12px;
  color: var(--id-text-primary);
  font-weight: 600;
}
.il-code {
  font-family: var(--id-font-mono);
  font-size: 12px;
  color: var(--id-text-primary);
  word-break: break-all;
  background: var(--id-bg-surface);
  border: 1px solid var(--id-border-subtle);
  padding: 6px 8px;
  border-radius: var(--id-radius-sm);
}
@media (max-width: 760px) {
  .form-grid,
  .sk-grid-2 {
    grid-template-columns: 1fr;
  }
  .field-item.full-width {
    grid-column: span 1;
  }
  .panel-top-actions {
    flex-direction: column;
    align-items: flex-start;
    gap: 10px;
  }
  .settings-error-card,
  .stale-banner {
    flex-direction: column;
    align-items: flex-start;
  }
  .workspace-save-bar {
    flex-direction: column;
    align-items: stretch;
    gap: 12px;
  }
  .workspace-save-bar :deep(.n-button) {
    width: 100%;
  }
  .custom-d2-table {
    min-width: 680px;
  }
}
</style>

<style scoped src="../components/workspace-settings-shared.css"></style>

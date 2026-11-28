<script setup lang="ts">
import { computed, ref } from 'vue';
import {
  NButton, NCheckbox, NCheckboxGroup, NDrawer, NDrawerContent,
  NForm, NFormItem, NInput, NModal, NRadio, NRadioGroup, NSelect, NSpace, NSwitch, NTag, useMessage
} from 'naive-ui';
import D2ChannelLogo from '../D2ChannelLogo.vue';
import {
  buildPrincipalRefsFromIds,
  buildUnifiedMemberAndDeptOptions,
  resolveMemberKeyToCanonicalKey
} from '../../utils/workforce';
import type {
  CapabilityPackageBinding, ChannelDirectoryDepartment, ChannelDirectoryUser,
  DigitalEmployee, McpServerEndpoint, WorkspaceSkill
} from '../../types';

const props = defineProps<{
  employee: DigitalEmployee;
  employees: DigitalEmployee[];
  catalogPackages: CapabilityPackageBinding[];
  workspaceSkillsCatalog: WorkspaceSkill[];
  workspaceMcpCatalog: McpServerEndpoint[];
  directoryUsers: ChannelDirectoryUser[];
  directoryDepartments: ChannelDirectoryDepartment[];
  syncModuleField: <K extends keyof DigitalEmployee>(field: K, value: DigitalEmployee[K]) => Promise<boolean>;
}>();

const message = useMessage();

const showBindCatalogModal = ref(false);
const showPackageEditorDrawer = ref(false);
const editingPackage = ref<CapabilityPackageBinding | null>(null);

const unifiedMemberAndDeptOptions = computed(() =>
  buildUnifiedMemberAndDeptOptions(
    props.employee,
    props.directoryUsers,
    props.directoryDepartments,
    [
      ...(editingPackage.value?.scope?.userScope?.whitelistTargets || []),
      ...(editingPackage.value?.scope?.userScope?.blacklistTargets || [])
    ],
    [
      ...(editingPackage.value?.scope?.userScope?.whitelistMemberIds || []),
      ...(editingPackage.value?.scope?.userScope?.blacklistMemberIds || [])
    ]
  )
);

function openBindCatalogModal() {
  showBindCatalogModal.value = true;
}

const isSavingPackages = ref(false);

async function persistPackages(nextPkgs: CapabilityPackageBinding[], successMsg: string): Promise<boolean> {
  if (isSavingPackages.value) return false;
  isSavingPackages.value = true;
  try {
    const sanitized = JSON.parse(JSON.stringify(nextPkgs)) as CapabilityPackageBinding[];
    for (const pkg of sanitized) {
      if (pkg.scope?.userScope) {
        pkg.scope.userScope.whitelistTargets = buildPrincipalRefsFromIds(
          pkg.scope.userScope.whitelistMemberIds || [],
          pkg.scope.userScope.whitelistTargets || [],
          props.directoryUsers,
          props.directoryDepartments
        );
        pkg.scope.userScope.blacklistTargets = buildPrincipalRefsFromIds(
          pkg.scope.userScope.blacklistMemberIds || [],
          pkg.scope.userScope.blacklistTargets || [],
          props.directoryUsers,
          props.directoryDepartments
        );
      }
    }
    const ok = await props.syncModuleField('capabilityPackages', sanitized);
    if (ok) {
      message.success(successMsg);
    }
    return ok;
  } finally {
    isSavingPackages.value = false;
  }
}

async function copyPackageFromTemplate(catalogPkg: CapabilityPackageBinding) {
  const newBinding: CapabilityPackageBinding = JSON.parse(JSON.stringify(catalogPkg));
  newBinding.id = `pkg-custom-${Date.now()}`;
  newBinding.name = catalogPkg.name;
  newBinding.isBound = true;
  newBinding.enabled = true;
  const nextPkgs = [...props.employee.capabilityPackages, newBinding];
  const ok = await persistPackages(nextPkgs, `已绑定能力包「${catalogPkg.name}」`);
  if (ok) {
    showBindCatalogModal.value = false;
  }
}

function openCreateAndBindPackage() {
  editingPackage.value = {
    id: `pkg-${Date.now()}`,
    name: '',
    description: '',
    enabled: true,
    isBound: true,
    resources: {
      skills: ['skill-fe-diagnose'],
      skillVersionPins: { 'skill-fe-diagnose': 'latest' },
      mcp: ['mcp-gitlab'],
      mcpTools: ['get_diff'],
      bash: false,
      sandboxNetwork: false,
      sandboxFilesystem: 'readonly'
    },
    scope: {
      userScope: {
        mode: 'inherit_with_blacklist',
        whitelistMemberIds: [],
        blacklistMemberIds: []
      },
      channelPolicies: [
        { platform: 'feishu', enabled: true, allowPrivate: true, allowGroup: true },
        { platform: 'wecom', enabled: true, allowPrivate: true, allowGroup: true },
        { platform: 'dingtalk', enabled: true, allowPrivate: true, allowGroup: true }
      ],
      conversationKinds: ['private', 'group'],
      triggerUsageScope: { mode: 'all', allowTriggerIds: [], denyTriggerIds: [] },
      handoffUsageScope: {
        mode: 'inherit_with_blacklist',
        allowEmployeeIds: [],
        denyEmployeeIds: []
      }
    }
  };
  showPackageEditorDrawer.value = true;
}

function openEditPackage(pkg: CapabilityPackageBinding) {
  const cloned = JSON.parse(JSON.stringify(pkg)) as CapabilityPackageBinding;
  if (!cloned.resources.skillVersionPins) cloned.resources.skillVersionPins = {};
  if (!cloned.resources.mcpTools) cloned.resources.mcpTools = [];
  if (cloned.resources.sandboxNetwork === undefined) cloned.resources.sandboxNetwork = false;
  if (!cloned.resources.sandboxFilesystem) cloned.resources.sandboxFilesystem = 'readonly';
  if (!cloned.scope.triggerUsageScope) {
    cloned.scope.triggerUsageScope = { mode: 'all', allowTriggerIds: [], denyTriggerIds: [] };
  } else if (!cloned.scope.triggerUsageScope.denyTriggerIds) {
    cloned.scope.triggerUsageScope.denyTriggerIds = [];
  }
  const trigger = cloned.scope.triggerUsageScope;
  if (trigger.mode === 'allowlist') {
    trigger.allowTriggerIds = trigger.allowTriggerIds.filter(id => !trigger.denyTriggerIds?.includes(id));
    trigger.denyTriggerIds = [];
  } else {
    trigger.mode = 'all';
    trigger.allowTriggerIds = [];
  }
  if (!cloned.scope.userScope) {
    cloned.scope.userScope = {
      mode: cloned.scope.userMode === 'custom_whitelist' ? 'whitelist_with_blacklist' : 'inherit_with_blacklist',
      whitelistMemberIds: cloned.scope.allowedMemberIds || [],
      blacklistMemberIds: []
    };
  }
  if (!cloned.scope.handoffUsageScope) {
    cloned.scope.handoffUsageScope = {
      mode: 'inherit_with_blacklist',
      allowEmployeeIds: [],
      denyEmployeeIds: []
    };
  } else if ((cloned.scope.handoffUsageScope.mode as string) === 'inherit') {
    cloned.scope.handoffUsageScope.mode = 'inherit_with_blacklist';
  } else if ((cloned.scope.handoffUsageScope.mode as string) === 'custom') {
    cloned.scope.handoffUsageScope.mode = 'whitelist';
  }
  editingPackage.value = cloned;
  showPackageEditorDrawer.value = true;
}

async function togglePackageBinding(pkg: CapabilityPackageBinding) {
  const nextPkgs = JSON.parse(JSON.stringify(props.employee.capabilityPackages)) as CapabilityPackageBinding[];
  const target = nextPkgs.find(p => p.id === pkg.id);
  if (!target) return;
  target.isBound = !target.isBound;
  await persistPackages(
    nextPkgs,
    `已${target.isBound ? '重新绑定' : '解绑'}能力包「${target.name}」`
  );
}

function onPkgScopeMemberIdsChange(field: 'whitelist' | 'blacklist', ids: string[]) {
  if (!editingPackage.value?.scope?.userScope) return;
  const resolved = ids.map(id =>
    resolveMemberKeyToCanonicalKey(
      id,
      field === 'whitelist'
        ? editingPackage.value!.scope.userScope.whitelistTargets
        : editingPackage.value!.scope.userScope.blacklistTargets,
      props.directoryUsers,
      props.directoryDepartments
    )
  );
  if (field === 'whitelist') {
    editingPackage.value.scope.userScope.whitelistMemberIds = resolved;
    editingPackage.value.scope.userScope.whitelistTargets = buildPrincipalRefsFromIds(
      resolved,
      editingPackage.value.scope.userScope.whitelistTargets || [],
      props.directoryUsers,
      props.directoryDepartments
    );
  } else {
    editingPackage.value.scope.userScope.blacklistMemberIds = resolved;
    editingPackage.value.scope.userScope.blacklistTargets = buildPrincipalRefsFromIds(
      resolved,
      editingPackage.value.scope.userScope.blacklistTargets || [],
      props.directoryUsers,
      props.directoryDepartments
    );
  }
}

async function savePackageConfig() {
  if (!editingPackage.value) return;
  if (!editingPackage.value.name.trim()) {
    message.warning('请输入能力包名称');
    return;
  }
  const clonedPkg = JSON.parse(JSON.stringify(editingPackage.value)) as CapabilityPackageBinding;
  clonedPkg.name = clonedPkg.name.trim();
  const nextPkgs = JSON.parse(JSON.stringify(props.employee.capabilityPackages)) as CapabilityPackageBinding[];
  const idx = nextPkgs.findIndex(p => p.id === clonedPkg.id);
  if (idx >= 0) {
    nextPkgs[idx] = clonedPkg;
  } else {
    nextPkgs.push(clonedPkg);
  }
  const ok = await persistPackages(nextPkgs, `能力包「${clonedPkg.name}」配置已保存`);
  if (ok) {
    showPackageEditorDrawer.value = false;
  }
}
</script>

<template>
  <div class="tab-panel-inner">
    <div class="panel-desc-bar">
      <span><strong>能力包</strong> 包含技能、MCP 工具授权及人员、渠道、会话类型、触发器与交接使用范围。</span>
      <div class="btn-group">
        <NButton size="small" secondary @click="openBindCatalogModal">+ 绑定工作区已有能力包</NButton>
        <NButton size="small" type="primary" @click="openCreateAndBindPackage">+ 创建新能力包</NButton>
      </div>
    </div>

    <div class="packages-list">
      <div
        v-for="pkg in employee.capabilityPackages"
        :key="pkg.id"
        class="pkg-card"
        :class="{ disabled: !pkg.enabled || !pkg.isBound }"
      >
        <div class="pkg-header">
          <div class="pkg-title-row">
            <h4 class="pkg-name">{{ pkg.name }}</h4>
            <NTag size="tiny" :type="pkg.enabled ? 'success' : 'default'">
              {{ pkg.enabled ? '已启用' : '已停用' }}
            </NTag>
          </div>
          <div class="pkg-actions">
            <NButton size="tiny" secondary @click="openEditPackage(pkg)">
              编辑配置与 5 维授权范围
            </NButton>
            <NButton
              size="tiny"
              :type="pkg.isBound ? 'error' : 'primary'"
              quaternary
              :disabled="isSavingPackages"
              @click="togglePackageBinding(pkg)"
            >
              {{ pkg.isBound ? '从该员工解绑' : '重新绑定' }}
            </NButton>
          </div>
        </div>
        <p class="pkg-desc">{{ pkg.description }}</p>
        <div class="pkg-chips">
          <span class="chip">技能: {{ pkg.resources.skills.length }} 个</span>
          <span class="chip">MCP 服务: {{ pkg.resources.mcp.length }} 个 (授权工具: {{ pkg.resources.mcpTools.length }})</span>
          <span class="chip" :class="{ 'chip-active': pkg.resources.bash }">终端命令: {{ pkg.resources.bash ? '已开启' : '关闭' }}</span>
        </div>
      </div>

      <div v-if="!employee.capabilityPackages.length" class="empty-channel-box">
        <p>员工尚未绑定任何能力包，暂无法调用技能、MCP 工具或执行终端指令。</p>
        <NButton type="primary" size="small" @click="openBindCatalogModal">+ 浏览工作区能力包目录并绑定</NButton>
      </div>
    </div>

    <!-- 工作区能力包目录绑定弹窗 -->
    <NModal v-model:show="showBindCatalogModal" preset="card" title="绑定工作区能力包" style="width: min(600px, 94vw);">
      <div class="catalog-modal-list">
        <div
          v-for="catPkg in catalogPackages"
          :key="catPkg.id"
          class="cat-item"
        >
          <div class="cat-left">
            <h4 class="cat-name">{{ catPkg.name }}</h4>
            <p class="cat-desc">{{ catPkg.description }}</p>
            <div class="cat-tags">
              <span>技能: {{ catPkg.resources.skills.length }}</span>
              <span>MCP 服务: {{ catPkg.resources.mcp.length }}</span>
              <span v-if="catPkg.resources.bash" class="bash-tag">终端命令</span>
            </div>
          </div>
          <div class="cat-right">
            <NButton
              v-if="!employee.capabilityPackages.find(p => p.id === catPkg.id && p.isBound)"
              type="primary"
              size="small"
              :loading="isSavingPackages"
              @click="copyPackageFromTemplate(catPkg)"
            >
              + 绑定为员工专属能力包
            </NButton>
            <NTag v-else size="small" type="success">已绑定该员工</NTag>
          </div>
        </div>
      </div>
    </NModal>

    <!-- 能力包配置与 5 维授权范围抽屉 -->
    <NDrawer v-model:show="showPackageEditorDrawer" :width="'min(640px, 95vw)'" placement="right">
      <NDrawerContent v-if="editingPackage" :title="`配置能力包 · ${editingPackage.name || '新建能力包'}`" closable>
        <div class="drawer-inner-flow">
          <div class="flow-section">
            <h4 class="fs-title">1. 基本信息</h4>
            <NForm label-placement="top">
              <NFormItem label="能力包名称">
                <NInput v-model:value="editingPackage.name" placeholder="例如：GitLab 代码审查包" />
              </NFormItem>
              <NFormItem label="描述说明">
                <NInput v-model:value="editingPackage.description" placeholder="说明该能力包封装的业务场景" />
              </NFormItem>
            </NForm>
          </div>

          <div class="flow-section">
            <h4 class="fs-title">2. 资源组合</h4>

            <!-- A. Skill 技能与版本锁定 -->
            <div style="margin-bottom: 14px;">
              <label style="font-weight: 600; font-size: 13px; color: var(--id-text-primary); display: block; margin-bottom: 6px;">
                ① 关联 Prompt 技能 (从技能资源库选择，支持固定历史版本或跟随最新)
              </label>
              <div style="display: flex; flex-direction: column; gap: 8px;">
                <div
                  v-for="skill in workspaceSkillsCatalog"
                  :key="skill.id"
                  style="border: 1px solid var(--id-border-subtle); border-radius: var(--id-radius-md); padding: 10px 12px; background: var(--id-bg-subtle);"
                >
                  <div style="display: flex; justify-content: space-between; align-items: center; flex-wrap: wrap; gap: 8px;">
                    <NCheckbox
                      :checked="editingPackage.resources.skills.includes(skill.id)"
                      @update:checked="c => {
                        if (c) {
                          if (!editingPackage!.resources.skills.includes(skill.id)) editingPackage!.resources.skills.push(skill.id);
                          if (!editingPackage!.resources.skillVersionPins) editingPackage!.resources.skillVersionPins = {};
                          if (!editingPackage!.resources.skillVersionPins[skill.id]) editingPackage!.resources.skillVersionPins[skill.id] = 'latest';
                        } else {
                          editingPackage!.resources.skills = editingPackage!.resources.skills.filter(s => s !== skill.id);
                        }
                      }"
                    >
                      <strong style="color: var(--id-text-primary);">{{ skill.name }}</strong>
                      <code style="margin-left: 6px; font-size: 12px; font-family: var(--id-font-mono);">{{ skill.code }}</code>
                      <NTag size="tiny" :type="skill.source === 'git_synced' ? 'info' : skill.source === 'zip_archive' ? 'warning' : 'default'" style="margin-left: 6px;">
                        {{ skill.source === 'git_synced' ? 'Git同步' : skill.source === 'zip_archive' ? 'ZIP导入' : '系统内置' }}
                      </NTag>
                    </NCheckbox>

                    <div v-if="editingPackage.resources.skills.includes(skill.id)" style="display: flex; align-items: center; gap: 6px; margin-left: auto; flex-shrink: 0; flex-wrap: wrap;">
                      <span style="font-size: 12px; color: var(--id-text-secondary); white-space: nowrap; flex-shrink: 0;">版本策略:</span>
                      <NSelect
                        size="tiny"
                        style="width: min(220px, 70vw);"
                        :value="editingPackage.resources.skillVersionPins?.[skill.id] || 'latest'"
                        :options="[
                          { label: `跟随最新版本 (${skill.version})`, value: 'latest' },
                          ...(skill.historyVersions || []).map(hv => ({
                            label: `锁定版本: ${hv.version} (${hv.commitHash ? 'commit ' + hv.commitHash.slice(0, 7) : hv.sourceType})`,
                            value: hv.version
                          }))
                        ]"
                        @update:value="v => {
                          if (!editingPackage!.resources.skillVersionPins) editingPackage!.resources.skillVersionPins = {};
                          editingPackage!.resources.skillVersionPins[skill.id] = v;
                        }"
                      />
                    </div>
                  </div>
                  <p style="margin: 4px 0 0 24px; font-size: 12px; color: var(--id-text-secondary);">
                    {{ skill.description }}
                  </p>
                </div>
              </div>
            </div>

            <!-- B. MCP 端点与细粒度工具函数授权 -->
            <div style="margin-bottom: 14px;">
              <label style="font-weight: 600; font-size: 13px; color: var(--id-text-primary); display: block; margin-bottom: 6px;">
                ② MCP 服务与工具授权
              </label>
              <div style="display: flex; flex-direction: column; gap: 8px;">
                <div
                  v-for="mcp in workspaceMcpCatalog"
                  :key="mcp.id"
                  style="border: 1px solid var(--id-border-subtle); border-radius: var(--id-radius-md); padding: 10px 12px; background: var(--id-bg-subtle);"
                >
                  <div style="display: flex; justify-content: space-between; align-items: center; gap: 8px; flex-wrap: wrap;">
                    <NCheckbox
                      :checked="editingPackage.resources.mcp.includes(mcp.id)"
                      @update:checked="c => {
                        if (c) {
                          if (!editingPackage!.resources.mcp.includes(mcp.id)) editingPackage!.resources.mcp.push(mcp.id);
                          mcp.tools.forEach(t => {
                            if (!editingPackage!.resources.mcpTools.includes(t.name)) {
                              editingPackage!.resources.mcpTools.push(t.name);
                            }
                          });
                        } else {
                          editingPackage!.resources.mcp = editingPackage!.resources.mcp.filter(m => m !== mcp.id);
                        }
                      }"
                    >
                      <strong style="color: var(--id-text-primary);">{{ mcp.name }}</strong>
                      <span style="font-size: 12px; color: var(--id-text-secondary); margin-left: 6px;">({{ mcp.endpoint }})</span>
                    </NCheckbox>
                    <NTag size="tiny" :type="mcp.status === 'connected' ? 'success' : 'warning'">
                      {{ mcp.status === 'connected' ? '已联通' : '待测试' }}
                    </NTag>
                  </div>

                  <div v-if="editingPackage.resources.mcp.includes(mcp.id)" style="margin-top: 8px; margin-left: 24px; padding: 8px 10px; background: var(--id-bg-surface); border: 1px dashed var(--id-border-strong); border-radius: var(--id-radius-sm);">
                    <span style="font-size: 12px; color: var(--id-text-secondary); font-weight: 600; display: block; margin-bottom: 6px;">
                      授权调用的工具：
                    </span>
                    <div style="display: flex; flex-wrap: wrap; gap: 10px;">
                      <NCheckbox
                        v-for="tool in mcp.tools"
                        :key="tool.name"
                        :checked="editingPackage.resources.mcpTools.includes(tool.name)"
                        @update:checked="c => {
                          if (c) {
                            if (!editingPackage!.resources.mcpTools.includes(tool.name)) editingPackage!.resources.mcpTools.push(tool.name);
                          } else {
                            editingPackage!.resources.mcpTools = editingPackage!.resources.mcpTools.filter(t => t !== tool.name);
                          }
                        }"
                      >
                        <code>{{ tool.name }}</code>
                        <span style="font-size: 12px; color: var(--id-text-secondary); margin-left: 4px;">({{ tool.description }})</span>
                      </NCheckbox>
                    </div>
                  </div>
                </div>
              </div>
            </div>

            <!-- C. 受控沙箱运行环境与权限 -->
            <div style="border: 1px solid var(--id-border-subtle); border-radius: var(--id-radius-md); padding: 10px 12px; background: var(--id-bg-subtle);">
              <div style="display: flex; justify-content: space-between; align-items: center; gap: 8px; flex-wrap: wrap;">
                <NCheckbox v-model:checked="editingPackage.resources.bash">
                  <strong style="color: var(--id-text-primary);">本地终端命令执行</strong>
                </NCheckbox>
                <NTag size="tiny" type="info">隔离运行</NTag>
              </div>
              <div v-if="editingPackage.resources.bash" style="margin-top: 8px; margin-left: 24px; display: flex; gap: 16px; align-items: center; flex-wrap: wrap;">
                <div style="display: flex; align-items: center; gap: 6px;">
                  <span style="font-size: 12px; color: var(--id-text-secondary);">外网连接权限:</span>
                  <NSwitch
                    size="small"
                    :value="editingPackage.resources.sandboxNetwork ?? false"
                    @update:value="v => editingPackage!.resources.sandboxNetwork = v"
                  />
                  <span style="font-size: 12px; color: var(--id-text-secondary);">{{ editingPackage.resources.sandboxNetwork ? '允许受控外网' : '仅内网/离线' }}</span>
                </div>
                <div style="display: flex; align-items: center; gap: 6px;">
                  <span style="font-size: 12px; color: var(--id-text-secondary);">文件访问权限:</span>
                  <NSelect
                    size="tiny"
                    style="width: 140px;"
                    :value="editingPackage.resources.sandboxFilesystem || 'readonly'"
                    :options="[
                      { label: '仅只读', value: 'readonly' },
                      { label: '允许读写', value: 'readwrite' }
                    ]"
                    @update:value="v => editingPackage!.resources.sandboxFilesystem = v"
                  />
                </div>
              </div>
            </div>
          </div>

          <div class="flow-section">
            <h4 class="fs-title">3. 使用范围授权</h4>
            <div class="scope-fields">
              <div class="scope-block">
                <label style="font-weight: 600; font-size: 13px; color: var(--id-text-primary);">① 人员使用范围</label>
                <div style="margin-top: 6px;">
                  <NRadioGroup v-model:value="editingPackage.scope.userScope.mode">
                    <NSpace vertical>
                      <NRadio value="inherit_with_blacklist">
                        <strong>沿用员工使用范围 + 黑名单</strong>
                      </NRadio>
                      <NRadio value="whitelist_with_blacklist">
                        <strong>白名单 + 黑名单</strong>
                        <span style="font-size: 12px; color: var(--id-text-secondary);">（仅员工可用范围与本白名单的交集人员可用）</span>
                      </NRadio>
                    </NSpace>
                  </NRadioGroup>
                </div>

                <div v-if="editingPackage.scope.userScope.mode === 'inherit_with_blacklist'" style="margin-top: 10px; background: var(--id-bg-subtle); border: 1px solid var(--id-border-subtle); border-radius: var(--id-radius-md); padding: 10px 12px;">
                  <label style="font-size: 12px; color: var(--id-text-secondary); display: block; margin-bottom: 4px;">禁用人员/部门黑名单（可选）</label>
                  <NSelect
                    :value="editingPackage.scope.userScope.blacklistMemberIds"
                    multiple
                    filterable
                    :options="unifiedMemberAndDeptOptions"
                    placeholder="选择禁止使用此能力包的人员或部门 (黑名单)"
                    @update:value="ids => onPkgScopeMemberIdsChange('blacklist', ids)"
                  />
                </div>

                <div v-else style="margin-top: 10px; background: var(--id-bg-subtle); border: 1px solid var(--id-border-subtle); border-radius: var(--id-radius-md); padding: 10px 12px;">
                  <label style="font-size: 12px; color: var(--id-text-secondary); display: block; margin-bottom: 4px;">① 允许使用的人员/部门白名单</label>
                  <NSelect
                    :value="editingPackage.scope.userScope.whitelistMemberIds"
                    multiple
                    filterable
                    :options="unifiedMemberAndDeptOptions"
                    placeholder="选择允许使用此能力包的人员或部门 (白名单)"
                    @update:value="ids => onPkgScopeMemberIdsChange('whitelist', ids)"
                  />
                  <label style="font-size: 12px; color: var(--id-text-secondary); display: block; margin: 8px 0 4px 0;">② 额外排除的人员/部门黑名单（可选）</label>
                  <NSelect
                    :value="editingPackage.scope.userScope.blacklistMemberIds"
                    multiple
                    filterable
                    :options="unifiedMemberAndDeptOptions"
                    placeholder="从白名单中额外排除的人员或部门 (黑名单，无需排除可留空)"
                    @update:value="ids => onPkgScopeMemberIdsChange('blacklist', ids)"
                  />
                </div>
              </div>

              <div style="margin-top: 14px;">
                <label style="font-weight: 600; font-size: 13px; color: var(--id-text-primary);">② 渠道范围与私聊/群聊权限</label>
                <div class="channel-policies-matrix" style="margin-top: 6px;">
                  <div v-for="cp in editingPackage.scope.channelPolicies" :key="cp.platform" class="cpm-row">
                    <span class="cpm-plat"><D2ChannelLogo :platform="cp.platform" :size="16" show-label /></span>
                    <NSwitch v-model:value="cp.enabled" size="small" />
                    <NCheckbox v-model:checked="cp.allowPrivate" :disabled="!cp.enabled">允许私聊</NCheckbox>
                    <NCheckbox v-model:checked="cp.allowGroup" :disabled="!cp.enabled">允许群聊</NCheckbox>
                  </div>
                </div>
              </div>

              <div style="margin-top: 14px;">
                <label style="font-weight: 600; font-size: 13px; color: var(--id-text-primary);">③ 会话形态限制</label>
                <div style="margin-top: 6px;">
                  <NCheckboxGroup v-model:value="editingPackage.scope.conversationKinds">
                    <NSpace>
                      <NCheckbox value="private">私聊会话</NCheckbox>
                      <NCheckbox value="group">群聊会话</NCheckbox>
                    </NSpace>
                  </NCheckboxGroup>
                </div>
              </div>

              <div style="margin-top: 14px;">
                <label style="font-weight: 600; font-size: 13px; color: var(--id-text-primary);">④ 自动触发器调用策略</label>
                <div style="margin-top: 6px;">
                  <NRadioGroup :value="editingPackage.scope.triggerUsageScope.mode" @update:value="mode => { if (editingPackage && (mode === 'all' || mode === 'allowlist')) editingPackage.scope.triggerUsageScope = { mode, allowTriggerIds: [], denyTriggerIds: [] }; }">
                    <NSpace wrap>
                      <NRadio value="all">全部触发器 + 黑名单</NRadio>
                      <NRadio value="allowlist">白名单</NRadio>
                    </NSpace>
                  </NRadioGroup>
                </div>
                <div v-if="editingPackage.scope.triggerUsageScope.mode === 'allowlist'" style="margin-top: 8px;">
                  <label style="font-size: 12px; color: var(--id-text-secondary); display: block; margin-bottom: 4px;">允许调用此能力包的触发器白名单</label>
                  <NSelect
                    v-model:value="editingPackage.scope.triggerUsageScope.allowTriggerIds"
                    multiple
                    :options="employee.triggers.map(t => ({ label: `${t.name} (${t.type === 'schedule' ? 'Cron' : t.type === 'github_issue' ? 'GitHub' : 'Webhook'})`, value: t.id }))"
                    placeholder="选择允许调用该能力包的触发器"
                  />
                </div>
                <div v-else style="margin-top: 8px;">
                  <label style="font-size: 12px; color: var(--id-text-secondary); display: block; margin-bottom: 4px;">排除触发器黑名单 (此触发器不得调用本包)</label>
                  <NSelect
                    v-model:value="editingPackage.scope.triggerUsageScope.denyTriggerIds"
                    multiple
                    :options="employee.triggers.map(t => ({ label: `${t.name} (${t.type === 'schedule' ? 'Cron' : t.type === 'github_issue' ? 'GitHub' : 'Webhook'})`, value: t.id }))"
                    placeholder="选择禁止调用该能力包的触发器 (留空表示不排除)"
                  />
                </div>
              </div>

              <div style="margin-top: 14px;">
                <label style="font-weight: 600; font-size: 13px; color: var(--id-text-primary);">⑤ AI 员工交接使用限制</label>
                <div style="margin-top: 6px;">
                  <NRadioGroup v-model:value="editingPackage.scope.handoffUsageScope.mode">
                    <NSpace vertical>
                      <NRadio value="inherit_with_blacklist">
                        <strong>继承员工交接策略 + 黑名单</strong>
                        <span style="font-size: 12px; color: var(--id-text-secondary);">（继承员工交接范围，可配置包专属黑名单排除指定员工）</span>
                      </NRadio>
                      <NRadio value="whitelist">
                        <strong>指定白名单</strong>
                        <span style="font-size: 12px; color: var(--id-text-secondary);">（白名单内的员工可在交接时使用此能力包）</span>
                      </NRadio>
                    </NSpace>
                  </NRadioGroup>
                </div>

                <div v-if="editingPackage.scope.handoffUsageScope.mode === 'inherit_with_blacklist'" style="margin-top: 8px; background: var(--id-bg-subtle); border: 1px solid var(--id-border-subtle); border-radius: var(--id-radius-md); padding: 10px 12px;">
                  <label style="font-size: 12px; color: var(--id-text-secondary); display: block; margin-bottom: 4px;">禁止在交接中使用此包的员工黑名单（可选）</label>
                  <NSelect
                    v-model:value="editingPackage.scope.handoffUsageScope.denyEmployeeIds"
                    multiple
                    :options="employees.filter(e => e.id !== employee.id).map(e => ({ label: `${e.avatar} ${e.name} (${e.code})`, value: e.id }))"
                    placeholder="选择禁止在交接中使用该包的员工"
                  />
                </div>
                <div v-else style="margin-top: 8px; background: var(--id-bg-subtle); border: 1px solid var(--id-border-subtle); border-radius: var(--id-radius-md); padding: 10px 12px;">
                  <label style="font-size: 12px; color: var(--id-text-secondary); display: block; margin-bottom: 4px;">允许在交接中使用此包的员工白名单 (必须同时具备员工交接权限)</label>
                  <NSelect
                    v-model:value="editingPackage.scope.handoffUsageScope.allowEmployeeIds"
                    multiple
                    :options="employees.filter(e => e.id !== employee.id).map(e => ({ label: `${e.avatar} ${e.name} (${e.code})`, value: e.id }))"
                    placeholder="选择允许在交接中使用该包的员工"
                  />
                  <p style="margin: 4px 0 0 0; font-size: 12px; color: var(--id-text-secondary);">仅指定的交接员工可在事项交接中调用此包，且不可超出员工基础交接权限。</p>
                </div>
              </div>
            </div>
          </div>

          <div class="drawer-actions">
            <NButton :disabled="isSavingPackages" @click="showPackageEditorDrawer = false">取消</NButton>
            <NButton type="primary" :loading="isSavingPackages" @click="savePackageConfig">保存并生效</NButton>
          </div>
        </div>
      </NDrawerContent>
    </NDrawer>
  </div>
</template>

<style scoped>
@import './workforce-shared.css';

.packages-list {
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.pkg-card {
  background: var(--id-bg-surface);
  border: 1px solid var(--id-border-subtle);
  border-radius: var(--id-radius-lg);
  padding: 14px 16px;
  display: flex;
  flex-direction: column;
  gap: 8px;
  transition: border-color var(--id-transition-fast);
}

.pkg-card:hover {
  border-color: var(--id-border-strong);
}

.pkg-card.disabled {
  opacity: 0.68;
  background: var(--id-bg-subtle);
}

.pkg-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 12px;
  flex-wrap: wrap;
}

.pkg-title-row {
  display: flex;
  align-items: center;
  gap: 8px;
}

.pkg-name {
  font-size: 14px;
  font-weight: 600;
  color: var(--id-text-primary);
  margin: 0;
}

.pkg-actions {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
}

.pkg-desc {
  font-size: 12px;
  color: var(--id-text-secondary);
  margin: 0;
  line-height: 1.5;
}

.pkg-chips {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
  margin-top: 2px;
}

.chip {
  font-size: 12px;
  font-family: var(--id-font-mono);
  font-variant-numeric: tabular-nums;
  background: var(--id-bg-muted);
  border: 1px solid var(--id-border-subtle);
  color: var(--id-text-secondary);
  padding: 3px 9px;
  border-radius: var(--id-radius-sm);
  font-weight: 500;
}

.chip.chip-active {
  background: var(--id-accent-primary-soft);
  border-color: var(--id-accent-primary-border);
  color: var(--id-accent-primary);
}

.catalog-modal-list {
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.cat-item {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 12px;
  flex-wrap: wrap;
  background: var(--id-bg-subtle);
  border: 1px solid var(--id-border-subtle);
  border-radius: var(--id-radius-md);
  padding: 12px 14px;
}

.cat-left {
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.cat-name {
  font-size: 14px;
  font-weight: 600;
  color: var(--id-text-primary);
  margin: 0;
}

.cat-desc {
  font-size: 12px;
  color: var(--id-text-secondary);
  margin: 0;
}

.cat-tags {
  display: flex;
  gap: 8px;
  flex-wrap: wrap;
  font-size: 12px;
  font-variant-numeric: tabular-nums;
  color: var(--id-text-secondary);
}

.bash-tag {
  color: var(--id-status-warning);
  font-weight: 600;
}

.scope-fields {
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.scope-block {
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.cpm-row {
  display: flex;
  align-items: center;
  gap: 12px;
  flex-wrap: wrap;
  padding: 4px 0;
  font-size: 12px;
}

.cpm-plat {
  width: 105px;
  font-weight: 600;
  display: inline-flex;
  align-items: center;
}
</style>

<script setup lang="ts">
import { computed, inject, provide, onMounted, onUnmounted, reactive, ref, watch } from 'vue';
import {
  NAlert, NButton, NCheckbox, NDrawer, NDrawerContent, NInput, NModal, NSelect, NSkeleton, NTabs, NTabPane, NTag, useDialog, useMessage
} from 'naive-ui';
import D2EmployeeAvatar from '../components/D2EmployeeAvatar.vue';
import D2ChannelLogo from '../components/D2ChannelLogo.vue';
import UnifiedDirectoryPicker, { type DirectoryPickItem, type DirectorySourceOption } from '../components/UnifiedDirectoryPicker.vue';
import WorkforceEmployeeReuse from '../components/workforce/WorkforceEmployeeReuse.vue';
import WorkforceDirectoryList from '../components/workforce/WorkforceDirectoryList.vue';
import WorkforcePackagesTab from '../components/workforce/WorkforcePackagesTab.vue';
import WorkforceChannelsTab from '../components/workforce/WorkforceChannelsTab.vue';
import WorkforceTriggersTab from '../components/workforce/WorkforceTriggersTab.vue';
import WorkforceHumanRecovery from '../components/workforce/WorkforceHumanRecovery.vue';
import WorkforceHumanRecipients from '../components/workforce/WorkforceHumanRecipients.vue';
import WorkforceRulesTab from '../components/workforce/WorkforceRulesTab.vue';
import { workforceAccessIsDirty } from '../api/workforce-usage';
import PlatformPackagesPanel from './PlatformPackagesPanel.vue';
import PlatformTriggersPanel from './PlatformTriggersPanel.vue';
import { useWorkforcePorts } from '../api/workforce-ports';
import { useWorkforceChannelStatuses } from '../composables/useWorkforceChannelStatuses';
import { D2ApiError, activeWorkspaceId } from '../api/client';
import { realPlatform, platformAuth } from '../api/platform';
import type { AgentStatusCounts } from '../api/agents';
import { getWorkforceEmployeePage } from '../api/workforce-platform-data';
import { workforcePlatformResources } from '../api/workforce-platform-resources';
import {
  EXECUTION_ENGINE_OPTIONS,
  filterModelsByEngine,
  getEmployeeStatusMeta,
  getExecutionEngineMeta,
  isModelCompatibleWithEngine,
  normalizeExecutionEngine,
  resolveCompatibleModelId
} from '../utils/presentation';
import {
  EMPLOYEE_ROLE_PLACEHOLDER,
  EMPLOYEE_ROLE_PRESETS,
  PERSONA_CONSTRAINT_PLACEHOLDER,
  PERSONA_CONSTRAINT_PRESETS,
  appendEmployeeRolePreset,
  appendPersonaConstraint,
  buildPrincipalRefsFromIds,
  isEmployeeRolePresetApplied,
  isPersonaConstraintApplied,
  mergeConnectionDirectoryData,
  normalizeEmployeeAccessSelections,
  patchVerifiedWecomOnEmployee
} from '../utils/workforce';
import {
  ALL_AVATAR_PRESETS,
  PORTRAIT_AVATAR_PRESETS,
  type CapabilityPackageBinding,
  type ChannelDirectoryDepartment,
  type ChannelDirectoryUser,
  type ChannelGroupOption,
  type ChannelSyncSourceDetail,
  type ConnectionDirectoryQueryResult,
  type DigitalEmployee,
  type EmployeeChannelBinding,
  type EmployeeTrigger,
  type ExecutionEngine,
  type FeishuRegexTemplateItem,
  type McpServerEndpoint,
  type ModelItem,
  type PlatformType,
  type PrincipalRef,
  type WorkspaceSkill
} from '../types';

const { workforceApi, resourcesApi } = useWorkforcePorts();

const props = defineProps<{
  initialTab?: string | undefined;
  selectedEmployeeId?: string | undefined;
}>();

const emit = defineEmits<{
  navigate: [target: string, param?: string, targetId?: string, extraType?: string];
}>();

const message = useMessage();
const dialog = useDialog();
type ChildDraft = {hasUnsaved:()=>boolean;save:()=>Promise<boolean>;discard:()=>void;getLabel?:()=>string};
const childDrafts=reactive(new Set<ChildDraft>());
let discardingChildren=false;
const hasChildDrafts=()=>[...childDrafts].some(child=>child.hasUnsaved());
function discardChildDrafts(){discardingChildren=true;try{for(const child of childDrafts)child.discard();}finally{discardingChildren=false;}}
provide('d2DraftGuard',{register:(child:ChildDraft)=>childDrafts.add(child),unregister:(child:ChildDraft)=>childDrafts.delete(child)});
const realPackagesRef=ref<InstanceType<typeof PlatformPackagesPanel>>();
function beforeDetailTabLeave(){
  if(!realPlatform)return true;
  if(realPackagesRef.value?.hasOpenEditor()){message.warning('请先保存或关闭能力包编辑窗口');return false;}
  if(!hasChildDrafts())return true;
  const dirtyLabels=Array.from(new Set([...childDrafts].filter(c=>c.hasUnsaved()).map(c=>c.getLabel?.()||'当前模块配置')));
  const content=dirtyLabels.length?`当前页签的「${dirtyLabels.join('、')}」存在未保存的修改。请分项保存；切换将放弃当前模块未保存的修改。`:'请分项保存；切换将放弃当前模块未保存的修改。';
  return new Promise<boolean>(resolve=>dialog.warning({title:'存在未保存的配置草稿',content,positiveText:'放弃修改并切换',negativeText:'继续编辑',onPositiveClick:()=>{discardChildDrafts();resolve(true);},onNegativeClick:()=>resolve(false),onClose:()=>resolve(false)}));
}
function closeFromChild(){if(!discardingChildren)handleBackToList();}
const isDevMode = import.meta.env.DEV;
const currentRole=ref('');
const canManage=computed(()=>!realPlatform || ['owner','admin'].includes(currentRole.value));

// 远程目录与名录状态
const employees = ref<DigitalEmployee[]>([]);
const employeePage=ref(1),employeeTotal=ref(0);
const statusCounts = ref<AgentStatusCounts>();
let directoryRequest: AbortController | undefined;
const detailLoading=ref(false),detailError=ref('');
let detailSequence=0;
async function loadCreateModels(){
 if(!realPlatform)return;const w=activeWorkspaceId.value;
 try{const rows=await workforcePlatformResources.listModels(w);if(w===activeWorkspaceId.value)modelsCatalog.value=rows.map(m=>({...m,enabled:m.selectable}));}
 catch(e){if(w===activeWorkspaceId.value)message.warning(e instanceof Error?e.message:'模型选项加载失败，请重试');}
}
function changeEmployeePage(page:number){employeePage.value=page;void loadWorkforceData(false);}
const directoryUsers = ref<ChannelDirectoryUser[]>([]);
const directoryDepartments = ref<ChannelDirectoryDepartment[]>([]);
const channelSyncSources = ref<ChannelSyncSourceDetail[]>([]);
const versionConflictAlert = ref<string | null>(null);
const reloadingEmployee = ref(false);
const channelEditorVersion = ref(0);
const platformCandidateGroups = ref<Record<PlatformType, string[]>>({
  feishu: [],
  wecom: [],
  dingtalk: []
});
const platformCandidateGroupItems = ref<Record<PlatformType, ChannelGroupOption[]>>({
  feishu: [],
  wecom: [],
  dingtalk: []
});
const feishuRegexTemplates = ref<FeishuRegexTemplateItem[]>([]);
const workspaceCapabilityPackagesCatalog = ref<CapabilityPackageBinding[]>([]);
const workspaceSkillsCatalog = ref<WorkspaceSkill[]>([]);
const workspaceMcpCatalog = ref<McpServerEndpoint[]>([]);
const modelsCatalog = ref<Pick<ModelItem,'id'|'name'|'enabled'|'isDefault'|'protocol'>[]>([]);

const selectedFilter = ref('all');
const confirmPlatformBilling = ref(false);
const searchQuery = ref('');
watch([() => searchQuery.value.trim(), selectedFilter], () => {
  if (realPlatform) {
    employees.value = [];
    statusCounts.value = undefined;
    employeeTotal.value = 0;
    changeEmployeePage(1);
  }
});
const currentEditingEmp = ref<DigitalEmployee | null>(null);
const channelConfigurationChanged = computed(() => realPlatform
  && currentEditingEmp.value?.channels.some(channel => channel.configurationChanged));
const showCreateModal = ref(false);
const activeChannelPlatform = ref<PlatformType>('feishu');
const channelReadIncomplete = computed(() => Object.keys(currentEditingEmp.value?.channelReadErrors ?? {}).length > 0);

const DETAIL_TABS = new Set(['overview', 'persona', 'packages', 'channels', 'triggers', 'rules']);

function normalizeDetailTab(tab?: string): string {
  if (tab === 'configure_pending' || tab === 'policies') return 'channels';
  if (tab && DETAIL_TABS.has(tab)) return tab;
  return 'overview';
}

const activeDetailTab = ref(normalizeDetailTab(props.initialTab));
const isDirty = ref(false);
const originalEmpSnapshot = ref<string>('');
const showUnsavedDraftModal = ref<boolean>(false);

const parsedSnapshot = computed<DigitalEmployee | null>(() => {
  if (!originalEmpSnapshot.value) return null;
  try {
    return JSON.parse(originalEmpSnapshot.value) as DigitalEmployee;
  } catch {
    return null;
  }
});

function recomputeDirty() {
  if (!currentEditingEmp.value || !originalEmpSnapshot.value) {
    isDirty.value = false;
    return;
  }
  isDirty.value = JSON.stringify(currentEditingEmp.value) !== originalEmpSnapshot.value;
}

watch(() => currentEditingEmp.value?.modelId, () => { confirmPlatformBilling.value=false; });

const hasUnsavedChanges = computed(() => {
  if (!currentEditingEmp.value || !originalEmpSnapshot.value) return false;
  return isDirty.value || JSON.stringify(currentEditingEmp.value) !== originalEmpSnapshot.value;
});

const filteredEmployees = computed(() => {
  if (realPlatform) return employees.value;
  const query = searchQuery.value.trim().toLowerCase();
  return employees.value.filter(emp =>
    (selectedFilter.value === 'all' || emp.status === selectedFilter.value) &&
    (emp.name.toLowerCase().includes(query) || emp.role.toLowerCase().includes(query)));
});

const channelsTabRef = ref<InstanceType<typeof WorkforceChannelsTab> | null>(null);
const rulesTabRef = ref<InstanceType<typeof WorkforceRulesTab> | null>(null);

const avatarPresets = ALL_AVATAR_PRESETS;
const showAvatarPickerModal = ref(false);

function normalizeEmp(emp: DigitalEmployee): DigitalEmployee {
  emp.executionEngine = normalizeExecutionEngine(emp.executionEngine);
  return normalizeEmployeeAccessSelections(emp, directoryUsers.value, directoryDepartments.value);
}

// 将服务端返回的规范化字段同步回员工名录基线与快照
function syncServerFieldIntoSnapshot<K extends keyof DigitalEmployee>(
  empId: string,
  savedRaw: DigitalEmployee,
  fields: K[]
) {
  const saved = normalizeEmp(JSON.parse(JSON.stringify(savedRaw)) as DigitalEmployee);
  const editsAgent = fields.some(field => ['name','role','avatar','prompt','modelId','executionEngine'].includes(field)) || (fields.length === 1 && fields[0] === 'status');
  // Channel/usage resources have their own CAS revision. Their refresh must not
  // silently authorize a stale basic-profile draft against a newer Agent version.
  const syncKeys = Array.from(new Set<keyof DigitalEmployee>([...fields,
    ...(realPlatform && fields.includes('channels' as K) ? ['channelReadErrors'] as const : []),
    ...(!realPlatform || editsAgent ? ['version','etag','configVersion','modelAvailable'] as const : [])]));
  const masterEmp = employees.value.find(e => e.id === empId);
  if (masterEmp) {
    for (const f of syncKeys) {
      (masterEmp as unknown as Record<string, unknown>)[f as string] = structuredClone(saved[f]);
    }
  }
  if (currentEditingEmp.value?.id === empId) {
    for (const f of syncKeys) {
      (currentEditingEmp.value as unknown as Record<string, unknown>)[f as string] = structuredClone(saved[f]);
    }
    try {
      const snap = JSON.parse(originalEmpSnapshot.value || '{}') as DigitalEmployee;
      for (const f of syncKeys) {
        (snap as unknown as Record<string, unknown>)[f as string] = structuredClone(saved[f]);
      }
      originalEmpSnapshot.value = JSON.stringify(snap);
      isDirty.value = JSON.stringify(currentEditingEmp.value) !== originalEmpSnapshot.value;
    } catch {
      // ignore snapshot parse errors
    }
  }
  if (realPlatform && saved.resourceVersions) {
    const keys: (keyof NonNullable<DigitalEmployee['resourceVersions']>)[] = [];
    if (fields.includes('channels' as K)) keys.push('channels','triggerRules','usageHistory');
    if (fields.includes('unifiedAccess' as K)) keys.push('usageAccess');
    const patchVersions=(target:DigitalEmployee)=>{
      if(!target.resourceVersions)return;
      for(const key of keys) {
        const version = saved.resourceVersions![key];
        if (version !== undefined) Object.assign(target.resourceVersions,{[key]:structuredClone(version)});
      }
    };
    if(masterEmp)patchVersions(masterEmp);
    if(currentEditingEmp.value?.id===empId){
      patchVersions(currentEditingEmp.value);
      try{const snap=JSON.parse(originalEmpSnapshot.value) as DigitalEmployee;patchVersions(snap);originalEmpSnapshot.value=JSON.stringify(snap);}catch{/* retain existing snapshot */}
    }
  }
  versionConflictAlert.value = null;
}

function handleOptimisticConflictError(err: unknown): boolean {
  if (err instanceof D2ApiError && (err.status === 409 || err.code === 'CONFLICT_STALE_VERSION')) {
    versionConflictAlert.value = err.message;
    message.error(err.message);
    return true;
  }
  return false;
}

async function reloadLatestEditingEmployee() {
  const employeeId = currentEditingEmp.value?.id;
  if (!employeeId || reloadingEmployee.value) return;
  const workspaceId = activeWorkspaceId.value;
  const seq = detailSequence;
  const isCurrent = () => seq === detailSequence && workspaceId === activeWorkspaceId.value
    && currentEditingEmp.value?.id === employeeId && !workforceDisposed;
  reloadingEmployee.value = true;
  try {
    const fresh = normalizeEmp(await workforceApi.getEmployee(employeeId));
    if (!isCurrent()) return;
    if (realPlatform) {
      discardChildDrafts();
      channelEditorVersion.value++;
    }
    const idx = employees.value.findIndex(e => e.id === fresh.id);
    if (idx !== -1) employees.value[idx] = JSON.parse(JSON.stringify(fresh));
    currentEditingEmp.value = JSON.parse(JSON.stringify(fresh));
    originalEmpSnapshot.value = JSON.stringify(fresh);
    isDirty.value = false;
    versionConflictAlert.value = null;
    startChannelStatusPolling();
    message.success('已同步最新配置');
  } catch (err) {
    if (isCurrent()) message.error(err instanceof Error ? err.message : '拉取最新配置失败');
  } finally {
    reloadingEmployee.value = false;
  }
}

function confirmReloadLatestEditingEmployee() {
  const seq = detailSequence;
  const reload = () => {
    if (seq === detailSequence) void reloadLatestEditingEmployee();
  };
  if (realPlatform && (channelConfigurationChanged.value || hasUnsavedChanges.value || hasChildDrafts())) {
    dialog.warning({
      title: '重新加载员工配置',
      content: '重新加载成功后会放弃当前员工的未保存修改。',
      positiveText: '重新加载',
      negativeText: '保留草稿',
      onPositiveClick: reload
    });
    return;
  }
  reload();
}

async function syncModuleField<K extends keyof DigitalEmployee>(field: K, value: DigitalEmployee[K]): Promise<boolean> {
  if (!currentEditingEmp.value) return false;
  const empId = currentEditingEmp.value.id;
  const expectedVer = currentEditingEmp.value.version;

  try {
    let saved: DigitalEmployee | null = null;
    if (field === 'status') {
      saved = await workforceApi.updateEmployeeStatus(empId, value as DigitalEmployee['status'], expectedVer);
    } else if (field === 'capabilityPackages') {
      saved = await workforceApi.saveEmployeePackages(empId, value as CapabilityPackageBinding[], expectedVer);
    } else if (field === 'channels') {
      saved = await workforceApi.saveEmployeeChannels(empId, value as EmployeeChannelBinding[], expectedVer, activeWorkspaceId.value, currentEditingEmp.value.resourceVersions);
    } else if (field === 'triggers') {
      saved = await workforceApi.saveEmployeeTriggers(empId, value as EmployeeTrigger[], expectedVer);
    } else if (field === 'unifiedAccess' || field === 'handoffPolicy' || field === 'humanConfirmationRules' || field === 'webChatAccess') {
      saved = await workforceApi.saveEmployeeRules(empId, { [field]: value }, expectedVer);
    }
    if (saved) {
      syncServerFieldIntoSnapshot(empId, saved, [field]);
      return true;
    }
    return false;
  } catch (err) {
    if (!handleOptimisticConflictError(err)) {
      message.error(err instanceof Error ? err.message : '配置保存失败');
    }
    return false;
  }
}

type PersonaSectionKey = 'basic' | 'model' | 'prompt';
type BasicsSectionKey = PersonaSectionKey | 'avatar';
const savingBasicsSection = ref<BasicsSectionKey | null>(null);

async function patchBasicsSection<K extends keyof DigitalEmployee>(
  section: BasicsSectionKey,
  payload: Record<string, unknown>,
  fields: K[],
  successText: string,
  errorText: string
): Promise<boolean> {
  const draft = currentEditingEmp.value;
  if (!draft || savingBasicsSection.value) return false;
  const w=activeWorkspaceId.value,seq=detailSequence;
  savingBasicsSection.value = section;
  try {
    const saved = await workforceApi.updateEmployeeBasics(draft.id, payload, draft.version);
    if(w!==activeWorkspaceId.value||seq!==detailSequence||currentEditingEmp.value?.id!==draft.id)return true;
    syncServerFieldIntoSnapshot(draft.id, saved, fields);
    message.success(successText);
    return true;
  } catch (err) {
    if(w!==activeWorkspaceId.value||seq!==detailSequence)return false;
    if (!handleOptimisticConflictError(err)) {
      message.error(err instanceof Error ? err.message : errorText);
    }
    return false;
  } finally {
    if(w===activeWorkspaceId.value&&seq===detailSequence)savingBasicsSection.value = null;
  }
}

async function selectAvatar(av: string) {
  if(!canManage.value){message.warning('修改需要工作区管理员权限');return;}
  const emp = currentEditingEmp.value;
  if (!emp) return;
  if (emp.avatar === av) {
    showAvatarPickerModal.value = false;
    return;
  }
  const ok = await patchBasicsSection('avatar', { avatar: av }, ['avatar'], '员工头像已更新', '更新员工头像失败');
  if (ok) showAvatarPickerModal.value = false;
}

const personaExecutionEngine = computed<ExecutionEngine>(() =>
  normalizeExecutionEngine(currentEditingEmp.value?.executionEngine)
);

const personaEngineMeta = computed(() =>
  getExecutionEngineMeta(personaExecutionEngine.value)
);

const personaCompatibleModels = computed(() =>
  filterModelsByEngine(modelsCatalog.value, personaExecutionEngine.value)
);

const personaModelSelectOptions = computed(() =>
  personaCompatibleModels.value.map(m => ({
    label: m.isDefault ? `${m.name} (工作区默认)` : m.name,
    value: m.id
  }))
);

function handlePersonaEngineChange(nextEngineRaw: ExecutionEngine) {
  if (!currentEditingEmp.value) return;
  const nextEngine = normalizeExecutionEngine(nextEngineRaw);
  currentEditingEmp.value.executionEngine = nextEngine;
  recomputeDirty();

  const currentModel = modelsCatalog.value.find(m => m.id === currentEditingEmp.value?.modelId);
  if (currentModel && currentModel.enabled && isModelCompatibleWithEngine(currentModel.protocol, nextEngine)) {
    return;
  }

  const nextModelId = resolveCompatibleModelId(
    modelsCatalog.value,
    nextEngine,
    currentEditingEmp.value.modelId
  );
  if (nextModelId) {
    const nextModel = modelsCatalog.value.find(m => m.id === nextModelId);
    currentEditingEmp.value.modelId = nextModelId;
    if (nextModel) {
      currentEditingEmp.value.modelName = nextModel.name;
    }
    message.info(
      `已切换至「${getExecutionEngineMeta(nextEngine).shortLabel}」引擎，并自动切换为兼容模型「${nextModel?.name || nextModelId}」`
    );
  } else {
    currentEditingEmp.value.modelId = '';
    currentEditingEmp.value.modelName = '';
    message.warning(
      `「${getExecutionEngineMeta(nextEngine).shortLabel}」引擎仅支持 ${getExecutionEngineMeta(nextEngine).requiredProtocolLabel} 协议，当前工作区暂无兼容可用模型`
    );
  }
}

function validatePersonaEngineAndModel(draft: DigitalEmployee): { ok: true; engine: ExecutionEngine; modelName: string } | { ok: false } {
  const engine = normalizeExecutionEngine(draft.executionEngine);
  const engineMeta = getExecutionEngineMeta(engine);
  const selectedModel = modelsCatalog.value.find(m => m.id === draft.modelId && m.enabled);
  if (!draft.modelId || !selectedModel) {
    message.warning(`当前「${engineMeta.shortLabel}」引擎下尚未选择有效的已启用基座模型，请先选择兼容模型`);
    return { ok: false };
  }
  if (!isModelCompatibleWithEngine(selectedModel.protocol, engine)) {
    message.warning(
      `所选模型「${selectedModel.name}」与「${engineMeta.shortLabel}」引擎不兼容，请选择 ${engineMeta.requiredProtocolLabel} 协议模型`
    );
    return { ok: false };
  }
  return { ok: true, engine, modelName: selectedModel.name };
}

const personaDirty = computed(() => {
  const emp = currentEditingEmp.value;
  const snap = parsedSnapshot.value;
  return {
    basic: Boolean(emp && snap && (emp.name !== snap.name || emp.role !== snap.role)),
    model: Boolean(emp && snap && (
      normalizeExecutionEngine(emp.executionEngine) !== normalizeExecutionEngine(snap.executionEngine) ||
      emp.modelId !== snap.modelId ||
      emp.thinkingLevel !== snap.thinkingLevel
    )),
    prompt: Boolean(emp && snap && (emp.prompt || '') !== (snap.prompt || ''))
  };
});
const needsBillingConfirm = computed(() => realPlatform && !!currentEditingEmp.value?.modelId.startsWith('platform:') && currentEditingEmp.value.modelId !== parsedSnapshot.value?.modelId && !confirmPlatformBilling.value);
const accessDirty = computed(() => {
  const current = currentEditingEmp.value?.unifiedAccess;
  return !!current && workforceAccessIsDirty(current, parsedSnapshot.value?.unifiedAccess, realPlatform);
});
const unsavedCardLabels = computed(() => {
  const labels: string[] = [];
  if (personaDirty.value.basic) labels.push('员工档案基本信息');
  if (personaDirty.value.prompt) labels.push('岗位行为规范与人设约束');
  if (personaDirty.value.model) labels.push('底层执行引擎与基座模型');
  if (accessDirty.value) labels.push('AI 员工使用范围');
  if (!realPlatform && currentEditingEmp.value && parsedSnapshot.value) {
    if (JSON.stringify(currentEditingEmp.value.channels) !== JSON.stringify(parsedSnapshot.value.channels)) labels.push('渠道与群策略');
    if (JSON.stringify(currentEditingEmp.value.handoffPolicy) !== JSON.stringify(parsedSnapshot.value.handoffPolicy)) labels.push('AI 员工接手范围');
  }
  for (const child of childDrafts) {
    if (child.hasUnsaved()) { const lbl = child.getLabel?.() || '子模块配置'; if (!labels.includes(lbl)) labels.push(lbl); }
  }
  return labels;
});

const PERSONA_RESTORE_FIELDS: Record<PersonaSectionKey, (keyof DigitalEmployee)[]> = {
  basic: ['name', 'role'],
  model: ['executionEngine', 'modelId', 'modelName', 'thinkingLevel'],
  prompt: ['prompt']
};

function cancelPersonaSection(section: PersonaSectionKey) {
  const emp = currentEditingEmp.value;
  const snap = parsedSnapshot.value;
  if (!emp || !snap) return;
  const restoreField = <K extends keyof DigitalEmployee>(field: K) => { emp[field] = snap[field]; };
  PERSONA_RESTORE_FIELDS[section].forEach(restoreField);
  recomputeDirty();
}

async function savePersonaSection(section: PersonaSectionKey) {
  if(!canManage.value){message.warning('修改需要工作区管理员权限');return;}
  const draft = currentEditingEmp.value;
  if (!draft) return;
  if (section === 'basic') {
    if (!draft.name.trim()) {
      message.warning('请输入员工显示名称');
      return;
    }
    await patchBasicsSection('basic', { name: draft.name.trim(), role: draft.role.trim() }, ['name', 'role'], '员工基本信息已保存', '保存基本信息失败');
    return;
  }
  if (section === 'model') {
    const validated = validatePersonaEngineAndModel(draft);
    if (!validated.ok) return;
    await patchBasicsSection('model', { executionEngine: validated.engine, modelId: draft.modelId, thinkingLevel: draft.thinkingLevel, ...(confirmPlatformBilling.value ? {confirm_platform_billing:true} : {}) }, ['executionEngine', 'modelId', 'modelName', 'thinkingLevel'], '执行引擎与基座模型配置已保存', '保存模型配置失败');
    return;
  }
  await patchBasicsSection('prompt', { prompt: draft.prompt }, ['prompt'], '岗位行为规范与人设约束已保存', '保存岗位行为规范失败');
}

function appendEmployeeTextPreset(
  field: 'role' | 'prompt',
  snippet: string,
  isApplied: (current: string | undefined, text: string) => boolean,
  append: (current: string | undefined, text: string) => string
) {
  const emp = currentEditingEmp.value;
  if (!emp || isApplied(emp[field], snippet)) return;
  emp[field] = append(emp[field], snippet);
  recomputeDirty();
}

function handleAppendRolePreset(tag: string) {
  appendEmployeeTextPreset('role', tag, isEmployeeRolePresetApplied, appendEmployeeRolePreset);
}

function handleAppendPersonaPreset(snippet: string) {
  appendEmployeeTextPreset('prompt', snippet, isPersonaConstraintApplied, appendPersonaConstraint);
}

async function activateEmployeeOnboarding() {
  if(currentEditingEmp.value?.modelAvailable===false){message.warning('模型当前不可用，无法启用员工');return;}
  if(!canManage.value){message.warning('修改需要工作区管理员权限');return;}
  if (!currentEditingEmp.value) return;
  const ok = await syncModuleField('status', 'active');
  if (ok && currentEditingEmp.value) {
    message.success(`员工「${currentEditingEmp.value.name}」已正式启用上岗履职！`);
  }
}

const showDisableEmpModal = ref(false);
const isTogglingEmpStatus = ref(false);

function handleToggleEmployeeStatusClick() {
  if(!canManage.value){message.warning('修改需要工作区管理员权限');return;}
  if (!currentEditingEmp.value) return;
  if (currentEditingEmp.value.status === 'disabled') {
    void executeToggleEmployeeStatus('active');
  } else {
    showDisableEmpModal.value = true;
  }
}

async function executeToggleEmployeeStatus(newStatus: 'active' | 'disabled') {
  if (!currentEditingEmp.value || isTogglingEmpStatus.value) return;
  if(newStatus==='active'&&currentEditingEmp.value.modelAvailable===false){message.warning('模型当前不可用，无法启用员工');return;}
  isTogglingEmpStatus.value = true;
  try {
    const ok = await syncModuleField('status', newStatus);
    if (!ok || !currentEditingEmp.value) return;
    showDisableEmpModal.value = false;
    if (newStatus === 'active') {
      message.success(`已恢复员工「${currentEditingEmp.value.name}」在岗履职状态`);
    } else {
      message.warning(`已停用员工「${currentEditingEmp.value.name}」，已下线无法对话与处理任务`);
    }
  } finally {
    isTogglingEmpStatus.value = false;
  }
}

function handleEmployeeCreated(newEmp: DigitalEmployee) {
  if(realPlatform){employeePage.value=1;void loadWorkforceData(false);}
  else employees.value.unshift(newEmp);
  void openEmployeeDetail(newEmp, 'channels');
}

function mergeConnectionDirectoryResult(res: ConnectionDirectoryQueryResult) {
  const merged = mergeConnectionDirectoryData(directoryUsers.value, directoryDepartments.value, res);
  directoryUsers.value = merged.users;
  directoryDepartments.value = merged.departments;
}

async function preloadConnectionScopedDirectory(sources: ChannelSyncSourceDetail[]) {
  directoryUsers.value = [];
  const connTasks: Promise<ConnectionDirectoryQueryResult>[] = [
    resourcesApi.queryWebDirectory({ page: 1, pageSize: 20 })
  ];
  const seenConns = new Set<string>();
  const enqueueConn = (channel: PlatformType, connectionId?: string | undefined) => {
    const cleanConn = connectionId?.trim();
    if (!cleanConn) return;
    const key = `${channel}::${cleanConn}`;
    if (seenConns.has(key)) return;
    seenConns.add(key);
    if (channel === 'feishu' || channel === 'dingtalk') {
      connTasks.push(resourcesApi.queryChannelSnapshotDirectory(channel, cleanConn, { page: 1, pageSize: 20 }));
    } else if (channel === 'wecom') {
      connTasks.push(resourcesApi.searchWecomConnectionDirectory(cleanConn, { page: 1, pageSize: 20 }));
    }
  };

  for (const src of sources) {
    enqueueConn(src.channel, src.connectionId);
  }
  for (const emp of employees.value) {
    for (const ch of emp.channels) {
      enqueueConn(ch.platform, ch.connectionId);
    }
  }

  const results = await Promise.allSettled(connTasks);
  for (const r of results) {
    if (r.status === 'fulfilled') {
      mergeConnectionDirectoryResult(r.value);
    }
  }
}

const isLoading = ref(false);
const hasLoadedOnce = ref(false);
watch(showCreateModal,open=>{if(open)void loadCreateModels();});
const loadError = ref('');
let workforceLoadSequence = 0;
let workforceDisposed = false;

async function loadWorkforceData(openInitial=true) {
  const sequence=++workforceLoadSequence, workspaceId=activeWorkspaceId.value;
  const current=()=>!workforceDisposed&&sequence===workforceLoadSequence&&workspaceId===activeWorkspaceId.value;
  isLoading.value = true;
  loadError.value = '';
  try {
    if (realPlatform) {
      directoryRequest?.abort();
      directoryRequest = new AbortController();
      const filter = selectedFilter.value;
      const page = await getWorkforceEmployeePage(workspaceId, employeePage.value, directoryRequest.signal, {
        search: searchQuery.value.trim(),
        ...(filter === 'all' ? {} : {enabled: filter === 'active'})
      });
      if (!current()) return;
      const lastPage = Math.max(1, Math.ceil(page.total / page.page_size));
      if (employeePage.value > lastPage) {
        changeEmployeePage(lastPage);
        return;
      }
      employees.value = page.items;
      employeeTotal.value = page.total;
      statusCounts.value = page.status_counts;
    } else {
    const [empList, options, pkgCatalog, skList, mcpList, modelList, dirSync] = await Promise.all([
      workforceApi.listEmployees(),
      workforceApi.getOptions(),
      workforceApi.getCapabilityPackagesCatalog(),
      resourcesApi.listSkills(),
      resourcesApi.listMcpServers(),
      resourcesApi.listModels(),
      resourcesApi.getDirectorySyncSources()
    ]);
    if(!current())return;
    employees.value = empList;
    platformCandidateGroups.value = options.platformCandidateGroups;
    platformCandidateGroupItems.value = options.platformCandidateGroupItems || { feishu: [], wecom: [], dingtalk: [] };
    feishuRegexTemplates.value = options.feishuRegexTemplates;
    workspaceCapabilityPackagesCatalog.value = pkgCatalog;
    workspaceSkillsCatalog.value = skList;
    workspaceMcpCatalog.value = mcpList;
    modelsCatalog.value = modelList;
    directoryDepartments.value = dirSync.departments;
    channelSyncSources.value = dirSync.sources;
    await preloadConnectionScopedDirectory(dirSync.sources);

    }

    if(!current())return;
    if (openInitial && props.selectedEmployeeId) {
      const target = employees.value.find(e => e.id === props.selectedEmployeeId);
      if (target || realPlatform) {
        const initTab = props.initialTab ? normalizeDetailTab(props.initialTab) : undefined;
        void openEmployeeDetail(target || {id:props.selectedEmployeeId} as DigitalEmployee, initTab);
      }
    } else if (openInitial && props.initialTab === 'create') {
      currentEditingEmp.value = null;
      showCreateModal.value = true;
    } else if (openInitial && (props.initialTab === 'configure_pending' || props.initialTab === 'policies')) {
      const targetEmp = props.initialTab === 'configure_pending'
        ? (employees.value.find(e => e.status === 'pending_setup') || employees.value[0])
        : (employees.value.find(e => e.status === 'active') || employees.value[0]);
      if (targetEmp) {
        openEmployeeDetail(targetEmp, 'channels');
      }
    }
    hasLoadedOnce.value = true;
  } catch (err) {
    if(!current())return;
    const msg = err instanceof Error ? err.message : '加载 AI 员工配置数据失败';
    loadError.value = msg;
    message.error(msg);
  } finally {
    if(current())isLoading.value = false;
  }
}

const d2DraftGuard = inject<{
  register: (handler: {
    hasUnsaved: () => boolean;
    save: () => Promise<boolean>;
    discard: () => void;
    getLabel?: () => string;
  }) => void;
  unregister: (handler: {
    hasUnsaved: () => boolean;
    save: () => Promise<boolean>;
    discard: () => void;
    getLabel?: () => string;
  }) => void;
} | null>('d2DraftGuard', null);

const draftGuardHandler = {
  hasUnsaved: () => hasUnsavedChanges.value || hasChildDrafts(),
  save: async () => {if(realPlatform){const hint=unsavedCardLabels.value.length?`（${unsavedCardLabels.value.join('、')}）`:'';message.warning(`请返回各配置卡片右上角分别保存${hint}，再离开页面`);return false;}return saveEmployeeChanges();},
  discard: () => {
    discardChildDrafts();
    showUnsavedDraftModal.value = false;
    channelsTabRef.value?.stopQrTimers();
    rulesTabRef.value?.stopWecomVerifyPoll();
    stopChannelStatusPolling();
    currentEditingEmp.value = null;
    originalEmpSnapshot.value = '';
    isDirty.value = false;
  },
  getLabel: () => {const name=currentEditingEmp.value?.name||'AI 员工配置';return unsavedCardLabels.value.length?`${name}（未保存：${unsavedCardLabels.value.join('、')}）`:name;}
};

const { startChannelStatusPolling, stopChannelStatusPolling, retryChannel, retryingChannel } = useWorkforceChannelStatuses({
  employee: currentEditingEmp,
  employees,
  snapshot: originalEmpSnapshot,
  getStatuses: workforceApi.getEmployeeChannelStatuses,
  isDingTalkTabActive: () => activeDetailTab.value === 'channels' && activeChannelPlatform.value === 'dingtalk',
  onSnapshotPatched: recomputeDirty
});

watch(activeWorkspaceId, async (workspace, _, onCleanup) => {
  if (!realPlatform) return;
  let current = true;
  onCleanup(() => { current = false; });
  currentRole.value = '';
  try {
    const rows = await platformAuth.getWorkspaces();
    if (current) currentRole.value = rows.find(row => row.id === workspace)?.role || '';
  } catch { /* The directory stays readable if role lookup fails. */ }
}, {immediate: true});

onMounted(() => {
  d2DraftGuard?.register(draftGuardHandler);
  void loadWorkforceData();
});

onUnmounted(() => {
  directoryRequest?.abort();
  workforceDisposed=true; workforceLoadSequence+=1;detailSequence+=1;
  d2DraftGuard?.unregister(draftGuardHandler);
  channelsTabRef.value?.stopQrTimers();
  rulesTabRef.value?.stopWecomVerifyPoll();
});

watch(activeWorkspaceId, () => {
  if(!realPlatform)return;
  workforceLoadSequence+=1;detailSequence+=1;employeePage.value=1;detailLoading.value=false;detailError.value='';showCreateModal.value=false;modelsCatalog.value=[];savingBasicsSection.value=null;
  statusCounts.value = undefined; employeeTotal.value = 0; hasLoadedOnce.value = false;
  employees.value=[]; currentEditingEmp.value=null; originalEmpSnapshot.value='';
  channelsTabRef.value?.resetQrState(); stopChannelStatusPolling();
  void loadWorkforceData();
});

watch(
  () => [props.selectedEmployeeId, props.initialTab],
  ([empId, tab]) => {
    if (tab === 'create') {
      currentEditingEmp.value = null;
      showCreateModal.value = true;
      return;
    }
    if (empId) {
      const normalizedTab = normalizeDetailTab(tab);
      if (currentEditingEmp.value?.id === empId) {
        if (tab) activeDetailTab.value = normalizedTab;
        return;
      }
      const target = employees.value.find(e => e.id === empId);
      if (target || realPlatform) {
        void openEmployeeDetail(target || {id:empId} as DigitalEmployee, normalizedTab);
      }
    } else if (tab === 'configure_pending' || tab === 'policies') {
      const targetEmp = tab === 'configure_pending'
        ? (employees.value.find(e => e.status === 'pending_setup') || employees.value[0])
        : (employees.value.find(e => e.status === 'active') || employees.value[0]);
      if (targetEmp) {
        openEmployeeDetail(targetEmp, 'channels');
      }
    }
  }
);

function startExternalChat(emp: DigitalEmployee) {
  emit('navigate', 'chat', undefined, emp.id);
}

async function openEmployeeDetail(emp: DigitalEmployee, tab = 'overview') {
  const seq=++detailSequence,w=activeWorkspaceId.value;
  detailError.value='';
  if(realPlatform){
    detailLoading.value=true;
    try{emp=await workforceApi.getEmployee(emp.id);if(seq!==detailSequence||w!==activeWorkspaceId.value||workforceDisposed)return;}
    catch(e){if(seq===detailSequence&&w===activeWorkspaceId.value){detailError.value=e instanceof Error?e.message:'员工配置加载失败';}return;}
    finally{if(seq===detailSequence)detailLoading.value=false;}
    void loadCreateModels();
  }
  channelsTabRef.value?.resetQrState();
  versionConflictAlert.value = null;
  const normalized = normalizeEmp(JSON.parse(JSON.stringify(emp)) as DigitalEmployee);
  currentEditingEmp.value = normalized;
  originalEmpSnapshot.value = JSON.stringify(normalized);
  activeDetailTab.value = tab;
  isDirty.value = false;
  startChannelStatusPolling();
}

watch(() => currentEditingEmp.value?.id, (id, previous) => {
  if (realPlatform && previous && !id && hasLoadedOnce.value) void loadWorkforceData(false);
});

function handleBackToList() {
  detailSequence+=1;detailLoading.value=false;
  if (hasUnsavedChanges.value || hasChildDrafts()) {
    showUnsavedDraftModal.value = true;
  } else {
    stopChannelStatusPolling();
    currentEditingEmp.value = null;
    originalEmpSnapshot.value = '';
    isDirty.value = false;
  }
}

function confirmDiscardDraft() {
  discardChildDrafts();
  showUnsavedDraftModal.value = false;
  stopChannelStatusPolling();
  currentEditingEmp.value = null;
  originalEmpSnapshot.value = '';
  isDirty.value = false;
  message.info('已放弃草稿修改，员工名录保持原配置生效状态。');
}

async function saveAndClose() {
  const ok = await saveEmployeeChanges();
  if (!ok) return;
  showUnsavedDraftModal.value = false;
  stopChannelStatusPolling();
  currentEditingEmp.value = null;
  originalEmpSnapshot.value = '';
}

async function saveEmployeeChanges(): Promise<boolean> {
  if(!canManage.value){message.warning('修改需要工作区管理员权限');return false;}
  if (!currentEditingEmp.value) return false;
  const draft = JSON.parse(JSON.stringify(currentEditingEmp.value)) as DigitalEmployee;
  const validated = validatePersonaEngineAndModel(draft);
  if (!validated.ok) return false;
  draft.executionEngine = validated.engine;
  draft.modelName = validated.modelName;
  draft.unifiedAccess = {
    ...draft.unifiedAccess,
    whitelistTargets: buildPrincipalRefsFromIds(
      draft.unifiedAccess.whitelistMemberIds,
      draft.unifiedAccess.whitelistTargets || [],
      directoryUsers.value,
      directoryDepartments.value
    ),
    blacklistTargets: buildPrincipalRefsFromIds(
      draft.unifiedAccess.blacklistMemberIds,
      draft.unifiedAccess.blacklistTargets || [],
      directoryUsers.value,
      directoryDepartments.value
    )
  };
  for (const pkg of draft.capabilityPackages) {
    if (pkg.scope?.userScope) {
      pkg.scope.userScope.whitelistTargets = buildPrincipalRefsFromIds(
        pkg.scope.userScope.whitelistMemberIds || [],
        pkg.scope.userScope.whitelistTargets || [],
        directoryUsers.value,
        directoryDepartments.value
      );
      pkg.scope.userScope.blacklistTargets = buildPrincipalRefsFromIds(
        pkg.scope.userScope.blacklistMemberIds || [],
        pkg.scope.userScope.blacklistTargets || [],
        directoryUsers.value,
        directoryDepartments.value
      );
    }
  }
  try {
    const synced = normalizeEmp(
      await workforceApi.saveFullEmployee(draft.id, draft, draft.version)
    );
    const idx = employees.value.findIndex(e => e.id === synced.id);
    if (idx !== -1) {
      employees.value[idx] = JSON.parse(JSON.stringify(synced));
    }
    currentEditingEmp.value = JSON.parse(JSON.stringify(synced));
    originalEmpSnapshot.value = JSON.stringify(synced);
    isDirty.value = false;
    versionConflictAlert.value = null;
    message.success(`员工「${synced.name}」配置已保存！`);
    return true;
  } catch (err) {
    if (!handleOptimisticConflictError(err)) {
      message.error(err instanceof Error ? err.message : '保存员工配置失败，草稿已保留');
    }
    return false;
  }
}

function jumpToEmployeeTasks() {
  if (!currentEditingEmp.value) return;
  emit('navigate', 'tasks', 'tasks', currentEditingEmp.value.id, 'employee');
}

function handleWecomVerified(payload: {
  empId: string;
  verifiedUserId: string;
  verifiedConnId?: string | undefined;
  verifiedBotId?: string | undefined;
}) {
  if (currentEditingEmp.value?.id === payload.empId && originalEmpSnapshot.value) {
    try {
      const snap = JSON.parse(originalEmpSnapshot.value) as DigitalEmployee;
      patchVerifiedWecomOnEmployee(snap, payload.verifiedUserId, payload.verifiedConnId, payload.verifiedBotId);
      originalEmpSnapshot.value = JSON.stringify(snap);
      isDirty.value = JSON.stringify(currentEditingEmp.value) !== originalEmpSnapshot.value;
    } catch {
      // ignore snapshot patch errors
    }
  }
}

// 统一分源通讯录选择器桥接
const showUnifiedPickerModal = ref(false);
const unifiedPickerPurpose = ref<'access_blacklist' | 'access_whitelist' | 'confirmation_rule'>('access_whitelist');
const pickerConfirmationIds = ref<string[]>([]);
const pickerConfirmationTargets = ref<PrincipalRef[]>([]);

const unifiedDirectoryPickerSources = computed<DirectorySourceOption[]>(() => {
  const list: DirectorySourceOption[] = [
    { sourceType: 'web', connectionId: 'ws-web-directory', label: '工作区网页', connected: true }
  ];
  const channels = currentEditingEmp.value?.channels || [];
  const labelMap: Record<PlatformType, string> = {
    feishu: '飞书',
    wecom: '企业微信',
    dingtalk: '钉钉'
  };
  const addedConn = new Set<string>(['web::ws-web-directory']);
  for (const ch of channels) {
    const connId = ch.connectionId?.trim();
    if (!connId) continue;
    const key = `${ch.platform}::${connId}`;
    if (!addedConn.has(key)) {
      addedConn.add(key);
      list.push({
        sourceType: ch.platform,
        connectionId: connId,
        label: labelMap[ch.platform],
        connected: ch.status === 'connected'
      });
    }
  }
  for (const src of channelSyncSources.value) {
    if (!src.connectionId?.trim()) continue;
    const key = `${src.channel}::${src.connectionId}`;
    if (!addedConn.has(key)) {
      addedConn.add(key);
      list.push({
        sourceType: src.channel,
        connectionId: src.connectionId,
        label: labelMap[src.channel],
        connected: false
      });
    }
  }
  return list;
});

const unifiedPickerSelectedIds = computed<string[]>(() => {
  if (!currentEditingEmp.value) return [];
  if (unifiedPickerPurpose.value === 'access_blacklist') {
    return currentEditingEmp.value.unifiedAccess.blacklistMemberIds;
  }
  if (unifiedPickerPurpose.value === 'access_whitelist') {
    return currentEditingEmp.value.unifiedAccess.whitelistMemberIds;
  }
  return pickerConfirmationIds.value;
});

const unifiedPickerSelectedTargets = computed<PrincipalRef[]>(() => {
  if (!currentEditingEmp.value) return [];
  if (unifiedPickerPurpose.value === 'access_blacklist') {
    return currentEditingEmp.value.unifiedAccess.blacklistTargets || [];
  }
  if (unifiedPickerPurpose.value === 'access_whitelist') {
    return currentEditingEmp.value.unifiedAccess.whitelistTargets || [];
  }
  return pickerConfirmationTargets.value;
});

function openUnifiedPickerModal(
  purpose: 'access_blacklist' | 'access_whitelist' | 'confirmation_rule',
  selectedIds: string[] = [],
  selectedTargets: PrincipalRef[] = []
) {
  unifiedPickerPurpose.value = purpose;
  if (purpose === 'confirmation_rule') {
    pickerConfirmationIds.value = [...selectedIds];
    pickerConfirmationTargets.value = selectedTargets.map(t => ({ ...t }));
  }
  showUnifiedPickerModal.value = true;
}

function handleUnifiedPickerIdsUpdate(ids: string[]) {
  if (!currentEditingEmp.value) return;
  if (unifiedPickerPurpose.value === 'access_blacklist') {
    currentEditingEmp.value.unifiedAccess.blacklistMemberIds = [...ids];
    recomputeDirty();
  } else if (unifiedPickerPurpose.value === 'access_whitelist') {
    currentEditingEmp.value.unifiedAccess.whitelistMemberIds = [...ids];
    recomputeDirty();
  }
}

function handleUnifiedPickerTargetsUpdate(targets: PrincipalRef[]) {
  if (!currentEditingEmp.value) return;
  if (unifiedPickerPurpose.value === 'access_blacklist') {
    currentEditingEmp.value.unifiedAccess.blacklistTargets = targets.map(t => ({ ...t }));
    recomputeDirty();
  } else if (unifiedPickerPurpose.value === 'access_whitelist') {
    currentEditingEmp.value.unifiedAccess.whitelistTargets = targets.map(t => ({ ...t }));
    recomputeDirty();
  } else if (unifiedPickerPurpose.value === 'confirmation_rule') {
    rulesTabRef.value?.setConfirmationTargetFromTargets(targets);
  }
}

function handleUnifiedPickerPick(item: DirectoryPickItem) {
  if (unifiedPickerPurpose.value === 'confirmation_rule' && item.kind === 'user') {
    rulesTabRef.value?.applyPickedConfirmationTarget(item);
    showUnifiedPickerModal.value = false;
    message.success(`已选中确认人：${item.name}`);
  }
}
</script>

<template>
  <div class="d2-workforce">
    <div v-if="isLoading && !hasLoadedOnce" class="wf-skeleton-wrap" aria-label="正在加载 AI 员工">
      <div class="wf-skeleton-bar">
        <NSkeleton text style="width: 240px; height: 22px;" />
        <NSkeleton text style="width: 112px; height: 28px;" />
      </div>
      <div class="wf-skeleton-grid">
        <div v-for="n in 3" :key="n" class="wf-skeleton-card">
          <NSkeleton text style="width: 55%; height: 20px;" />
          <NSkeleton text :repeat="2" />
          <NSkeleton text style="width: 40%;" />
        </div>
      </div>
    </div>

    <div v-else-if="loadError && !hasLoadedOnce" class="wf-error-card" role="alert">
      <p class="wf-error-title">无法加载 AI 员工配置</p>
      <p class="wf-error-desc">{{ loadError }}</p>
      <NButton type="primary" size="small" @click="loadWorkforceData()">重试加载</NButton>
    </div>

    <template v-else>
      <div v-if="loadError" class="wf-inline-error" role="alert">
        <span>刷新数据失败：{{ loadError }}（当前显示的可能不是最新数据）</span>
        <NButton size="tiny" secondary @click="loadWorkforceData()">重试</NButton>
      </div>

      <NAlert v-if="detailError" type="warning">员工配置读取失败：{{ detailError }}。名录仍可使用，请重新点击员工重试。</NAlert>
      <p v-if="detailLoading" role="status">正在加载所选员工配置…</p>
      <!-- 1. 员工名录列表视图 -->
      <WorkforceDirectoryList
        :can-manage="canManage"
        :page="employeePage" :total="employeeTotal" :status-counts="statusCounts" :loading="isLoading"
        @page="changeEmployeePage" @load-models="loadCreateModels" @from-template="emit('navigate','market')"
        v-if="!currentEditingEmp"
      v-model:search-query="searchQuery"
      v-model:selected-filter="selectedFilter"
      v-model:show-create-modal="showCreateModal"
      :employees="employees"
      :filtered-employees="filteredEmployees"
      :models-catalog="modelsCatalog"
      @open-detail="openEmployeeDetail"
      @start-chat="startExternalChat"
      @created="handleEmployeeCreated"
    />

    <!-- 2. 员工详情配置视图 -->
    <div v-else class="employee-detail-container" :inert="reloadingEmployee">
      <NAlert v-if="channelConfigurationChanged" type="warning" class="status-alert-banner" style="margin-bottom: 10px;">
        <div class="alert-flex-row">
          <span>渠道配置已变更，请重新加载。未保存的草稿仍在当前页面。</span>
          <NButton size="tiny" type="primary" secondary :loading="reloadingEmployee" @click="confirmReloadLatestEditingEmployee">
            重新加载配置
          </NButton>
        </div>
      </NAlert>
      <NAlert v-if="versionConflictAlert" type="error" :show-icon="true" class="status-alert-banner" style="margin-bottom: 10px;">
        <div class="alert-flex-row">
          <span><strong>配置已更新：</strong>{{ versionConflictAlert }}</span>
          <NButton size="tiny" type="primary" secondary @click="confirmReloadLatestEditingEmployee">
            刷新最新配置
          </NButton>
        </div>
      </NAlert>

      <NAlert v-if="currentEditingEmp.status === 'disabled'" type="warning" class="status-alert-banner">
        <strong>该 AI 员工已被管理员停用</strong>。停用期间无法在网页对话、群聊或自动触发器中响应。点击右上角「恢复在岗」可重新启用。
      </NAlert>

      <div class="detail-top-bar">
        <div class="detail-hero-left">
          <button class="back-link-btn" @click="handleBackToList">← 返回员工名录</button>
          <div class="emp-profile-summary">
            <div class="profile-avatar-wrap" :class="{ 'avatar-clickable': canManage }" :title="canManage ? '点击更换员工头像' : undefined" @click="canManage && (showAvatarPickerModal = true)">
              <D2EmployeeAvatar :name="currentEditingEmp.name" :preset="currentEditingEmp.avatar" :identity-key="currentEditingEmp.id" size="lg" />
              <span v-if="canManage" class="avatar-edit-badge">更换</span>
            </div>
            <div class="profile-titles">
              <div class="pt-row">
                <h2 class="pt-name">{{ currentEditingEmp.name }}</h2>
                <span class="ai-label">AI</span>
                <span class="engine-pill" :title="`底层执行引擎：${personaEngineMeta.label}`">
                  {{ personaEngineMeta.shortLabel }}
                </span>
                <code class="pt-code">{{ currentEditingEmp.code }}</code>
                <span v-if="realPlatform && currentEditingEmp.configVersion !== undefined" class="pt-code">v{{ currentEditingEmp.configVersion }}</span>
                <span
                  class="wf-status-pill"
                  :class="{
                    'is-success': currentEditingEmp.status === 'active',
                    'is-info': currentEditingEmp.status === 'pending_setup',
                    'is-warning': currentEditingEmp.status === 'warning',
                  }"
                >
                  <span class="wf-status-dot" />
                  {{ getEmployeeStatusMeta(currentEditingEmp.status).label }}
                </span>
                <div v-if="currentEditingEmp.channels.length" class="hero-channel-logos">
                  <D2ChannelLogo
                    v-for="ch in currentEditingEmp.channels"
                    :key="ch.platform"
                    :platform="ch.platform"
                    :size="18"
                    :title="`${ch.platformName} (${ch.statusText})`"
                  />
                </div>
              </div>
              <p class="pt-role" :title="currentEditingEmp.role || undefined">{{ currentEditingEmp.role }}</p>
            </div>
          </div>
        </div>

        <div class="detail-top-actions">
          <NTag v-if="hasUnsavedChanges || hasChildDrafts()" type="warning" size="small" round :bordered="false">
            存在未保存修改{{ unsavedCardLabels.length ? `：${unsavedCardLabels.join('、')}` : '' }}
          </NTag>
          <NButton
            size="small"
            secondary
            type="primary"
            @click="startExternalChat(currentEditingEmp)"
          >
            <template #icon>
              <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
                <path d="M21 15a2 2 0 0 1-2 2H7l-4 4V5a2 2 0 0 1 2-2h14a2 2 0 0 1 2 2z"/>
              </svg>
            </template>
            {{ currentEditingEmp.status === 'disabled' ? '查看历史对话' : '开始对话' }}
          </NButton>
          <NButton
            size="small"
            :type="currentEditingEmp.status === 'disabled' ? 'success' : 'error'"
            secondary
            :loading="isTogglingEmpStatus"
            :disabled="!canManage || (currentEditingEmp.status === 'disabled' && currentEditingEmp.modelAvailable === false)"
            @click="handleToggleEmployeeStatusClick"
          >
            {{ currentEditingEmp.status === 'disabled' ? '恢复在岗' : '停用员工' }}
          </NButton>
        </div>
      </div>

      <NAlert v-if="realPlatform && !canManage" type="info">当前仅可查看，修改需要工作区管理员权限。</NAlert>
      <!-- 6 大深度配置 Tab -->
      <NTabs v-model:value="activeDetailTab" :on-before-leave="beforeDetailTabLeave" type="line" class="detail-tabs">
        <!-- Tab 1: 就绪检查概览 -->
        <NTabPane name="overview" tab="概览与就绪检查">
          <div class="tab-panel-inner">
            <NAlert
              v-if="currentEditingEmp.channels.length === 0 && !channelReadIncomplete"
              type="warning"
              style="margin-bottom: 16px;"
            >
              <div class="alert-flex-row">
                <span><strong>新员工尚未接入任何通讯渠道</strong>：员工目前无法接收飞书、企业微信或钉钉的群聊与私聊消息。建议优先完成渠道接入。</span>
                <NButton size="tiny" type="warning" @click="activeDetailTab = 'channels'">立即接入渠道</NButton>
              </div>
            </NAlert>

            <div class="readiness-checklist-card">
              <div class="alert-flex-row" style="margin-bottom: 12px;">
                <h4 class="card-inner-title" style="margin: 0;">AI 员工上岗就绪检查清单</h4>
                <NButton v-if="currentEditingEmp.status !== 'active'" size="tiny" type="primary" :disabled="!canManage || currentEditingEmp.modelAvailable === false" @click="activateEmployeeOnboarding">
                  一键完成入职并上岗
                </NButton>
              </div>
              <div class="checklist-items">
                <div class="cl-item" :class="{ passed: Boolean(currentEditingEmp.modelId && currentEditingEmp.prompt?.trim()) }">
                  <span class="cl-icon">{{ currentEditingEmp.modelId && currentEditingEmp.prompt?.trim() ? '✓' : '!' }}</span>
                  <div class="cl-info">
                    <strong>人设、执行引擎与基座模型配置</strong>
                    <span>{{ currentEditingEmp.modelId && currentEditingEmp.prompt?.trim() ? `已配置 ${personaEngineMeta.shortLabel} 引擎 · 绑定 ${currentEditingEmp.modelName} 且已配置岗位行为规范` : '请完善执行引擎、基座模型绑定与岗位行为规范' }}</span>
                  </div>
                </div>
                <div class="cl-item" :class="{ passed: currentEditingEmp.channels.length > 0 }">
                  <span class="cl-icon">{{ currentEditingEmp.channels.length > 0 ? '✓' : '!' }}</span>
                  <div class="cl-info">
                    <strong>飞书 / 钉钉 / 企微渠道接入</strong>
                    <span v-if="currentEditingEmp.channels.length">已接入 {{ currentEditingEmp.channels.length }} 个平台</span>
                    <span v-if="channelReadIncomplete">部分渠道配置读取失败，请前往渠道接入重试。</span>
                    <span v-else-if="!currentEditingEmp.channels.length">尚未接入任何渠道，无法在群聊中使用</span>
                  </div>
                  <div v-if="currentEditingEmp.channels.length" class="cl-channels">
                    <D2ChannelLogo
                      v-for="ch in currentEditingEmp.channels"
                      :key="ch.platform"
                      :platform="ch.platform"
                      :size="18"
                      show-label
                    />
                  </div>
                </div>
                <div v-if="!realPlatform" class="cl-item" :class="{ passed: currentEditingEmp.humanConfirmationRules.length > 0 }">
                  <span class="cl-icon">{{ currentEditingEmp.humanConfirmationRules.length > 0 ? '✓' : '' }}</span>
                  <div class="cl-info">
                    <strong>安全拦截与待人工确认策略</strong>
                    <span>{{ realPlatform ? '人工确认尚未接入，当前不提供自动暂停与通知承诺。' : currentEditingEmp.humanConfirmationRules.length > 0 ? `已配置 ${currentEditingEmp.humanConfirmationRules.length} 条待人工确认规则` : '未指定确认人时，高风险指令将暂停并通知管理员' }}</span>
                  </div>
                </div>
                <div v-if="realPlatform && canManage" class="cl-item">
                  <span class="cl-icon">i</span>
                  <div class="cl-info">
                    <strong>人工确认规则与兜底确认人</strong>
                    <span>配置跨渠道确认人与默认确认人，处理任务中的关键确认请求</span>
                  </div>
                  <NButton text type="primary" @click="activeDetailTab = 'persona'">设置人工确认规则</NButton>
                </div>
              </div>
            </div>

            <div class="overview-stats-grid">
              <div class="stat-box">
                <span class="s-label">今日执行任务</span>
                <strong class="s-val">{{ currentEditingEmp.stats.todayTasks ?? '暂无数据' }}</strong>
              </div>
              <div class="stat-box">
                <span class="s-label">今日交互对话</span>
                <strong class="s-val">{{ currentEditingEmp.stats.todayConversations ?? '暂无数据' }}</strong>
              </div>
              <div class="stat-box">
                <span class="s-label">任务成功率</span>
                <strong class="s-val" :class="{ 'text-success': currentEditingEmp.stats.successRate != null }">{{ currentEditingEmp.stats.successRate ?? '暂无数据' }}</strong>
              </div>
            </div>

            <div class="overview-quick-links">
              <NButton secondary size="small" @click="startExternalChat(currentEditingEmp)">
                {{ currentEditingEmp.status === 'disabled' ? '查看历史对话' : '前往工作区对话' }}
              </NButton>
              <NButton secondary size="small" @click="activeDetailTab = 'channels'">
                渠道与群策略配置
              </NButton>
              <NButton secondary size="small" @click="jumpToEmployeeTasks">
                查看该员工的历史任务流水
              </NButton>
            </div>
            <WorkforceEmployeeReuse v-if="realPlatform" :workspace="activeWorkspaceId" :employee-id="currentEditingEmp.id" :revision="currentEditingEmp.version" :can-manage="canManage" @navigate="(t,p,id)=>emit('navigate',t,p,id)" />
          </div>
        </NTabPane>

        <!-- Tab 2: 设定与模型 -->
        <NTabPane name="persona" tab="设定与模型">
          <div class="tab-panel-inner">
            <div class="form-block" :class="{ 'is-dirty-card': personaDirty.basic }">
              <div class="card-edit-header">
                <h4 class="fb-title">1. 员工档案基本信息</h4>
                <div v-if="personaDirty.basic" class="card-edit-actions">
                  <span class="card-dirty-pill">未保存</span>
                  <NButton size="tiny" :disabled="savingBasicsSection !== null" @click="cancelPersonaSection('basic')">取消</NButton>
                  <NButton size="tiny" type="primary" :loading="savingBasicsSection === 'basic'" :disabled="!canManage || (savingBasicsSection !== null && savingBasicsSection !== 'basic')" @click="savePersonaSection('basic')">保存</NButton>
                </div>
              </div>
              <div class="persona-basic-stack">
                <div class="fb-item persona-name-item">
                  <label>员工显示名称</label>
                  <NInput
                    v-model:value="currentEditingEmp.name"
                    :disabled="!canManage"
                    placeholder="输入 AI 员工名称，例如：研发效能助手 AI"
                    class="emp-name-input"
                    @update:value="recomputeDirty"
                  />
                </div>
                <div class="fb-item">
                  <div class="fb-field-head">
                    <label>岗位职责</label>
                    <span class="fb-sub">明确该员工的服务对象、日常负责事项与预期交付成果。</span>
                  </div>
                  <div class="persona-preset-bar">
                    <span class="persona-preset-label">常用维度：</span>
                    <button
                      v-for="preset in EMPLOYEE_ROLE_PRESETS"
                      :key="preset.key"
                      type="button"
                      class="persona-preset-chip"
                      :class="{ applied: isEmployeeRolePresetApplied(currentEditingEmp.role, preset.tag) }"
                      :disabled="!canManage || isEmployeeRolePresetApplied(currentEditingEmp.role, preset.tag)"
                      :title="`点击插入 ${preset.tag}（例如：${preset.example}）`"
                      @click="handleAppendRolePreset(preset.tag)"
                    >
                      {{ isEmployeeRolePresetApplied(currentEditingEmp.role, preset.tag) ? preset.label.replace(/^\+\s*/, '✓ ') : preset.label }}
                    </button>
                  </div>
                  <NInput
                    v-model:value="currentEditingEmp.role"
                    :disabled="!canManage"
                    type="textarea"
                    :autosize="{ minRows: 4, maxRows: 10 }"
                    :maxlength="2000"
                    :placeholder="EMPLOYEE_ROLE_PLACEHOLDER"
                    :input-props="{ 'aria-label': '岗位职责' }"
                    @update:value="recomputeDirty"
                  />
                </div>
              </div>
              <div class="persona-profile-guidance" :class="{ 'is-dirty-card': personaDirty.prompt }">
                <div class="card-edit-header">
                  <h5 class="fb-title">岗位行为规范与人设约束</h5>
                  <div v-if="personaDirty.prompt" class="card-edit-actions">
                    <span class="card-dirty-pill">未保存</span>
                    <NButton size="tiny" :disabled="savingBasicsSection !== null" @click="cancelPersonaSection('prompt')">取消</NButton>
                    <NButton size="tiny" type="primary" :loading="savingBasicsSection === 'prompt'" :disabled="!canManage || (savingBasicsSection !== null && savingBasicsSection !== 'prompt')" @click="savePersonaSection('prompt')">保存</NButton>
                  </div>
                </div>
                <p class="fb-sub">约定员工如何回复、信息不足时如何处理，以及哪些操作需要先确认。</p>
                <div class="persona-preset-bar">
                  <span class="persona-preset-label">常用约束：</span>
                  <button
                    v-for="preset in PERSONA_CONSTRAINT_PRESETS"
                    :key="preset.key"
                    type="button"
                    class="persona-preset-chip"
                    :class="{ applied: isPersonaConstraintApplied(currentEditingEmp.prompt, preset.snippet) }"
                    :disabled="!canManage || isPersonaConstraintApplied(currentEditingEmp.prompt, preset.snippet)"
                    :title="preset.snippet"
                    @click="handleAppendPersonaPreset(preset.snippet)"
                  >
                    {{ isPersonaConstraintApplied(currentEditingEmp.prompt, preset.snippet) ? preset.label.replace(/^\+\s*/, '✓ ') : preset.label }}
                  </button>
                </div>
                <NInput
                  v-model:value="currentEditingEmp.prompt"
                  :disabled="!canManage"
                  type="textarea"
                  :autosize="{ minRows: 5, maxRows: 12 }"
                  :placeholder="PERSONA_CONSTRAINT_PLACEHOLDER"
                  :input-props="{ 'aria-label': '岗位行为规范与人设约束' }"
                  @update:value="recomputeDirty"
                />
              </div>
            </div>

            <WorkforceHumanRecipients v-if="realPlatform && canManage && activeDetailTab === 'persona'"
              :workspace="activeWorkspaceId" :employee-id="currentEditingEmp.id">
              <WorkforceHumanRecovery :workspace="activeWorkspaceId" :employee-id="currentEditingEmp.id" />
            </WorkforceHumanRecipients>
            <div class="form-block" :class="{ 'is-dirty-card': personaDirty.model }">
              <div class="card-edit-header">
                <h4 class="fb-title">{{ realPlatform && canManage ? '3.' : '2.' }} 底层执行引擎与基座模型</h4>
                <div v-if="personaDirty.model" class="card-edit-actions">
                  <span class="card-dirty-pill">未保存</span>
                  <NButton size="tiny" :disabled="savingBasicsSection !== null" @click="cancelPersonaSection('model')">取消</NButton>
                  <NButton size="tiny" type="primary" :loading="savingBasicsSection === 'model'" :disabled="!canManage || needsBillingConfirm || (savingBasicsSection !== null && savingBasicsSection !== 'model')" @click="savePersonaSection('model')">保存</NButton>
                </div>
              </div>
              <p class="fb-sub">{{ personaEngineMeta.protocolHint }}</p>
              <NAlert v-if="realPlatform && currentEditingEmp.modelId.startsWith('platform:') && currentEditingEmp.modelId !== parsedSnapshot?.modelId" type="warning" style="margin-bottom:12px">
                <NCheckbox v-model:checked="confirmPlatformBilling" :disabled="!canManage">确认切换到官方计费模型，由当前操作账号承担后续模型用量费用。</NCheckbox>
              </NAlert>
              <div class="fb-grid fb-grid-3">
                <div class="fb-item">
                  <label>底层执行引擎</label>
                  <NSelect
                    :value="personaExecutionEngine"
                    :disabled="!canManage"
                    :options="realPlatform ? EXECUTION_ENGINE_OPTIONS.filter(option => option.value === 'pi') : EXECUTION_ENGINE_OPTIONS"
                    @update:value="v => handlePersonaEngineChange((v as ExecutionEngine) || 'pi')"
                  />
                </div>
                <div class="fb-item">
                  <label>绑定基座大模型</label>
                  <NSelect
                    :value="currentEditingEmp.modelId || null"
                    :disabled="!canManage || !personaModelSelectOptions.length"
                    :options="personaModelSelectOptions"
                    :placeholder="personaModelSelectOptions.length ? '请选择兼容的已启用模型' : `暂无兼容 ${personaEngineMeta.shortLabel} 的可用模型`"
                    @update:value="v => { if (currentEditingEmp) { currentEditingEmp.modelId = v || ''; const m = modelsCatalog.find(item => item.id === v); if (m) currentEditingEmp.modelName = m.name; recomputeDirty(); } }"
                  />
                  <p v-if="realPlatform" class="field-note">员工对话和任务执行都使用所选模型，不会回退到工作区默认模型。</p>
                </div>
                <div class="fb-item">
                  <label>深度思考等级</label>
                  <NSelect
                    v-model:value="currentEditingEmp.thinkingLevel"
                    :disabled="realPlatform"
                    :placeholder="realPlatform ? '暂无数据' : '请选择思考等级'"
                    :options="[
                      { label: '不开启思考 (极速响应)', value: 'none' },
                      { label: '低度思考 (基础上下文归纳)', value: 'low' },
                      { label: '中度思考 (逻辑多步推理)', value: 'medium' },
                      { label: '深度思考 (复杂代码审计与深度排障)', value: 'high' }
                    ]"
                    @update:value="recomputeDirty"
                  />
                </div>
              </div>
              <NAlert
                v-if="!personaCompatibleModels.length"
                type="warning"
                :bordered="false"
                style="margin-top: 4px;"
              >
                当前「{{ personaEngineMeta.shortLabel }}」引擎仅支持 {{ personaEngineMeta.requiredProtocolLabel }} 协议模型，工作区暂无可用兼容模型。请切换其他引擎，或前往「资源中心 -&gt; 模型管理」添加并启用对应协议模型。
              </NAlert>
            </div>
          </div>
        </NTabPane>

        <!-- Tab 4: 能力包与授权 -->
        <NTabPane name="packages" tab="能力包与授权">
          <PlatformPackagesPanel v-if="realPlatform && activeDetailTab === 'packages'" ref="realPackagesRef" :employee-id="currentEditingEmp.id" />
          <WorkforcePackagesTab v-else-if="!realPlatform"
            :employee="currentEditingEmp"
            :employees="employees"
            :catalog-packages="workspaceCapabilityPackagesCatalog"
            :workspace-skills-catalog="workspaceSkillsCatalog"
            :workspace-mcp-catalog="workspaceMcpCatalog"
            :directory-users="directoryUsers"
            :directory-departments="directoryDepartments"
            :sync-module-field="syncModuleField"
          />
        </NTabPane>

        <!-- Tab 5: 渠道接入与群策略 -->
        <NTabPane name="channels" tab="渠道接入与群策略">
          <WorkforceChannelsTab
            :key="channelEditorVersion"
            ref="channelsTabRef"
            :can-manage="canManage"
            v-model:active-channel-platform="activeChannelPlatform"
            :employee="currentEditingEmp"
            :saved-employee="parsedSnapshot"
            :is-active-tab="activeDetailTab === 'channels'"
            :retrying-channel="retryingChannel"
            :directory-users="directoryUsers"
            :platform-candidate-groups="platformCandidateGroups"
            :feishu-regex-templates="feishuRegexTemplates"
            :is-dev-mode="isDevMode"
            :sync-module-field="syncModuleField"
            :sync-server-field-into-snapshot="syncServerFieldIntoSnapshot"
            :handle-optimistic-conflict-error="handleOptimisticConflictError"
            @dirty="recomputeDirty"
            @retry-channel="retryChannel(activeChannelPlatform)"
            @configure-access="async () => { if (await beforeDetailTabLeave()) activeDetailTab = 'rules' }"
          />
        </NTabPane>

        <!-- Tab 6: 自动触发器 -->
        <NTabPane name="triggers" tab="自动触发器">
          <PlatformTriggersPanel v-if="realPlatform && activeDetailTab === 'triggers'" :workspace="activeWorkspaceId" :employee-id="currentEditingEmp.id" :can-manage="canManage" hide-back-button @close="closeFromChild" />
          <WorkforceTriggersTab v-else-if="!realPlatform"
            :employee="currentEditingEmp"
            :employees="employees"
            :platform-candidate-group-items="platformCandidateGroupItems"
            :sync-module-field="syncModuleField"
            :sync-server-field-into-snapshot="syncServerFieldIntoSnapshot"
          />
        </NTabPane>

        <!-- 权限和共享 -->
        <NTabPane name="rules" :tab="realPlatform ? '权限和共享' : '权限与确认规则'">
          <WorkforceRulesTab
            :key="channelEditorVersion"
            ref="rulesTabRef"
            :can-manage="canManage"
            :is-active-tab="activeDetailTab === 'rules'"
            :employee="currentEditingEmp"
            :saved-employee="parsedSnapshot"
            :employees="employees"
            :directory-users="directoryUsers"
            :directory-departments="directoryDepartments"
            :is-dev-mode="isDevMode"
            :sync-server-field-into-snapshot="syncServerFieldIntoSnapshot"
            :handle-optimistic-conflict-error="handleOptimisticConflictError"
            @dirty="recomputeDirty"
            @open-picker="openUnifiedPickerModal"
            @wecom-verified="handleWecomVerified"
          />
        </NTabPane>
      </NTabs>
    </div>
    </template>

    <!-- 更换头像弹窗 -->
    <NModal v-model:show="showAvatarPickerModal" preset="card" title="选择员工头像" style="width: min(520px, 94vw);">
      <div class="avatar-picker-header">
        <div class="avatar-picker-intro">
          <D2EmployeeAvatar :name="currentEditingEmp?.name || ''" :preset="currentEditingEmp?.avatar" :identity-key="currentEditingEmp?.id" size="lg" />
          <span class="avatar-picker-hint">从平台内置 AI 员工人像库中选择</span>
        </div>
        <NButton size="small" secondary :loading="savingBasicsSection === 'avatar'" @click="selectAvatar(PORTRAIT_AVATAR_PRESETS[Math.floor(Math.random() * PORTRAIT_AVATAR_PRESETS.length)]!)">
          换一个
        </NButton>
      </div>
      <div class="avatar-presets-grid">
        <button
          v-for="(av, idx) in avatarPresets"
          :key="av"
          class="avatar-preset-btn"
          :class="{ selected: currentEditingEmp?.avatar === av }"
          :disabled="savingBasicsSection === 'avatar'"
          :title="`头像方案 #${idx + 1}`"
          @click="selectAvatar(av)"
        >
          <D2EmployeeAvatar :name="currentEditingEmp?.name || 'AI'" :preset="av" size="md" />
        </button>
      </div>
    </NModal>

    <!-- 停用员工二次确认弹窗 -->
    <NModal v-model:show="showDisableEmpModal" preset="card" title="确认停用 AI 员工" style="width: min(460px, 94vw);">
      <div class="draft-modal-body">
        <p class="draft-modal-lead">
          确定要停用 AI 员工「<strong>{{ currentEditingEmp?.name }}</strong>」吗？
        </p>
        <NAlert type="warning" :show-icon="true" class="draft-modal-alert">
          停用后，该员工将立即下线，无法在工作区对话、已绑定的 {{ currentEditingEmp?.channels.length || 0 }} 个渠道工作群及 {{ currentEditingEmp?.triggers.length || 0 }} 个自动触发器中响应任务。
        </NAlert>
      </div>
      <template #footer>
        <div class="modal-footer-actions">
          <NButton @click="showDisableEmpModal = false">取消</NButton>
          <NButton type="error" :loading="isTogglingEmpStatus" @click="executeToggleEmployeeStatus('disabled')">确认停用</NButton>
        </div>
      </template>
    </NModal>

    <!-- 未保存草稿确认弹窗 -->
    <NModal v-model:show="showUnsavedDraftModal" preset="card" title="存在未保存的配置草稿" style="width: min(480px, 94vw);">
      <div class="draft-modal-body">
        <p class="draft-modal-lead">
          您对 AI 员工「<strong>{{ currentEditingEmp?.name }}</strong>」的以下配置进行了修改，尚未保存：
        </p>
        <NAlert type="warning" :show-icon="true" class="draft-modal-alert">
          <div v-if="unsavedCardLabels.length" style="margin-bottom: 4px;">
            <strong>未保存卡片：</strong>{{ unsavedCardLabels.join('、') }}
          </div>
          {{ realPlatform ? '不同卡片独立保存，请返回对应卡片右上角分项保存；直接退出将放弃未保存修改。' : '如果直接退出，未保存的修改将会丢失。' }}
        </NAlert>
      </div>
      <template #footer>
        <div class="modal-footer-actions">
          <NButton @click="showUnsavedDraftModal = false">继续编辑</NButton>
          <NButton type="error" secondary @click="confirmDiscardDraft">放弃修改并退出</NButton>
          <NButton v-if="realPlatform" type="primary" @click="showUnsavedDraftModal = false">返回分项保存</NButton>
          <NButton v-else type="primary" @click="saveAndClose">保存并退出</NButton>
        </div>
      </template>
    </NModal>

    <!-- 统一分源通讯录选择器 -->
    <NDrawer v-model:show="showUnifiedPickerModal" :width="'min(680px, 95vw)'" placement="right">
      <NDrawerContent title="选择通讯录成员与部门" closable>
        <UnifiedDirectoryPicker
      v-bind="realPlatform && currentEditingEmp ? {realContext:{workspace:activeWorkspaceId,employeeId:currentEditingEmp.id,purpose:'usage' as const}} : {}"
          :sources="unifiedDirectoryPickerSources"
          :multiple="unifiedPickerPurpose !== 'confirmation_rule'"
          :include-departments="unifiedPickerPurpose !== 'confirmation_rule'"
          :selected-ids="unifiedPickerSelectedIds"
          :selected-targets="unifiedPickerSelectedTargets"
          :page-size="8"
          @update:selected-ids="handleUnifiedPickerIdsUpdate"
          @update:selected-targets="handleUnifiedPickerTargetsUpdate"
          @pick="handleUnifiedPickerPick"
          @loaded="mergeConnectionDirectoryResult"
        />
        <template #footer>
          <div class="drawer-actions" style="padding-top: 0;">
            <NButton type="primary" size="small" @click="showUnifiedPickerModal = false">确认选择</NButton>
          </div>
        </template>
      </NDrawerContent>
    </NDrawer>
  </div>
</template>

<style scoped>
@import '../components/workforce/workforce-shared.css';

.d2-workforce { display: flex; flex-direction: column; gap: 16px; }
.employee-detail-container {
  background: var(--id-bg-surface); border: 1px solid var(--id-border-subtle);
  border-radius: var(--id-radius-lg); padding: 20px; display: flex; flex-direction: column; gap: 16px;
}
.status-alert-banner { margin-bottom: 8px; }
.alert-flex-row { display: flex; justify-content: space-between; align-items: center; gap: 12px; flex-wrap: wrap; width: 100%; }
.detail-top-bar {
  display: flex; justify-content: space-between; align-items: center; gap: 16px;
  flex-wrap: wrap; padding-bottom: 16px; border-bottom: 1px solid var(--id-border-subtle);
}
.detail-hero-left { display: flex; flex-direction: column; gap: 8px; min-width: 0; flex: 1 1 280px; }
.back-link-btn {
  background: none; border: none; color: var(--id-accent-primary);
  font-size: 13px; font-weight: 500; cursor: pointer; padding: 0; text-align: left;
}
.back-link-btn:hover { text-decoration: underline; }
.detail-top-actions { display: flex; align-items: center; gap: 8px; flex-wrap: wrap; }
.detail-tabs { margin-top: -8px; }
.fb-item :deep(.n-select), .fb-item :deep(.n-base-selection) { width: 100%; max-width: 100%; }

@media (max-width: 760px) {
  .employee-detail-container { padding: 14px; }
  .detail-top-bar { align-items: flex-start; gap: 12px; }
  .detail-hero-left { flex-basis: 100%; width: 100%; }
  .detail-top-actions { width: 100%; display: flex; flex-wrap: wrap; justify-content: flex-start; gap: 8px; }
}
</style>

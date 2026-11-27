<script setup lang="ts">
import { computed, onUnmounted, ref, watch } from 'vue';
import {
  NAlert, NButton, NForm, NFormItem, NInput, NModal, NSelect, useMessage,
  type FormInst, type FormRules
} from 'naive-ui';
import D2EmployeeAvatar from '../D2EmployeeAvatar.vue';
import { useWorkforcePorts } from '../../api/workforce-ports';
import { activeWorkspaceId } from '../../api/client';
import { realPlatform } from '../../api/platform';
import { EMPLOYEE_ROLE_PLACEHOLDER } from '../../utils/workforce';
import {
  EXECUTION_ENGINE_OPTIONS,
  filterModelsByEngine,
  getExecutionEngineMeta,
  resolveCompatibleModelId
} from '../../utils/presentation';
import { PORTRAIT_AVATAR_PRESETS, type DigitalEmployee, type ExecutionEngine, type ModelItem } from '../../types';

const props = defineProps<{
  canManage: boolean;
  modelsCatalog: Pick<ModelItem, 'id' | 'name' | 'enabled' | 'isDefault' | 'protocol'>[];
  showCreateModal: boolean;
}>();

const emit = defineEmits<{
  'update:showCreateModal': [value: boolean];
  created: [emp: DigitalEmployee];
  'load-models': [];
}>();

const { workforceApi } = useWorkforcePorts();
const message = useMessage();
const creating=ref(false);
let alive = true;
onUnmounted(() => { alive = false; });
const createFormRef = ref<FormInst | null>(null);

const enabledModels = computed(() => props.modelsCatalog.filter(m => m.enabled));

const CREATE_AVATAR_BATCH_SIZE = 12;

function shuffleList(items: string[]): string[] {
  const arr = [...items];
  for (let i = arr.length - 1; i > 0; i -= 1) {
    const j = Math.floor(Math.random() * (i + 1));
    [arr[i], arr[j]] = [arr[j]!, arr[i]!];
  }
  return arr;
}

function buildNextAvatarBatch(excludeList: string[] = []): string[] {
  const excluded = new Set(excludeList);
  const unseen = shuffleList(PORTRAIT_AVATAR_PRESETS.filter(av => !excluded.has(av)));
  if (unseen.length >= CREATE_AVATAR_BATCH_SIZE) {
    return unseen.slice(0, CREATE_AVATAR_BATCH_SIZE);
  }
  const rest = shuffleList(PORTRAIT_AVATAR_PRESETS.filter(av => excluded.has(av)));
  return [...unseen, ...rest].slice(0, CREATE_AVATAR_BATCH_SIZE);
}

const createAvatarBatch = ref<string[]>(PORTRAIT_AVATAR_PRESETS.slice(0, CREATE_AVATAR_BATCH_SIZE));

const createForm = ref({
  name: '',
  role: '',
  avatar: PORTRAIT_AVATAR_PRESETS[0] || 'cn-v2-01',
  executionEngine: 'pi' as ExecutionEngine,
  modelId: resolveCompatibleModelId(props.modelsCatalog, 'pi'),
  thinkingLevel: 'medium' as const
});

const compatibleModelsForCreate = computed(() =>
  filterModelsByEngine(props.modelsCatalog, createForm.value.executionEngine)
);

const createModelSelectOptions = computed(() =>
  compatibleModelsForCreate.value.map(m => ({
    label: m.isDefault ? `${m.name} (工作区默认)` : m.name,
    value: m.id
  }))
);

const createEngineMeta = computed(() =>
  getExecutionEngineMeta(createForm.value.executionEngine)
);

function handleCreateEngineChange(engine: ExecutionEngine) {
  createForm.value.executionEngine = engine;
  createForm.value.modelId = resolveCompatibleModelId(
    props.modelsCatalog,
    engine,
    createForm.value.modelId
  );
}

const createRules: FormRules = {
  name: [
    {
      required: true,
      whitespace: true,
      message: '请输入员工岗位名称',
      trigger: ['blur', 'input']
    }
  ],
  modelId: [
    {
      required: true,
      message: '请选择兼容当前执行引擎的基座模型',
      trigger: ['change', 'blur']
    }
  ]
};

function shuffleCreateAvatarBatch() {
  const nextBatch = buildNextAvatarBatch(createAvatarBatch.value);
  createAvatarBatch.value = nextBatch;
  createForm.value.avatar = nextBatch[0] || 'cn-v2-01';
}

function resetCreateFormState() {
  const initialBatch = buildNextAvatarBatch();
  createAvatarBatch.value = initialBatch;
  createForm.value = {
    name: '',
    role: '',
    avatar: initialBatch[0] || 'cn-v2-01',
    executionEngine: 'pi',
    modelId: resolveCompatibleModelId(props.modelsCatalog, 'pi'),
    thinkingLevel: 'medium'
  };
  createFormRef.value?.restoreValidation();
}

watch(
  () => props.showCreateModal,
  (open) => {
    if (open) {
      resetCreateFormState();
    }
  },
  { immediate: true }
);

watch(
  compatibleModelsForCreate,
  () => {
    if (props.showCreateModal && !compatibleModelsForCreate.value.some(m => m.id === createForm.value.modelId)) {
      createForm.value.modelId = resolveCompatibleModelId(
        props.modelsCatalog,
        createForm.value.executionEngine
      );
    }
  }
);

async function handleCreateEmployee() {
  if(creating.value)return;
  const workspace=activeWorkspaceId.value;
  if(props.canManage===false){message.warning('创建员工需要有效工作区成员身份');return;}
  if (!enabledModels.value.length) {
    message.warning('当前工作区暂无可用已启用模型，请先前往「资源中心 -> 模型管理」添加并启用模型');
    return;
  }
  if (!compatibleModelsForCreate.value.length) {
    message.warning(
      `当前工作区暂无兼容 ${createEngineMeta.value.requiredProtocolLabel} 的已启用模型，请切换执行引擎或前往「资源中心 -> 模型管理」添加对应协议模型`
    );
    return;
  }
  if (!createForm.value.name.trim()) {
    message.warning('请输入员工岗位名称');
    return;
  }
  const foundModel = compatibleModelsForCreate.value.find(m => m.id === createForm.value.modelId);
  if (!foundModel) {
    message.warning('请选择与当前执行引擎兼容的已启用基座模型');
    return;
  }
  creating.value=true;
  try {
    try {
      await createFormRef.value?.validate();
    } catch {
      return;
    }
    if(!alive || workspace!==activeWorkspaceId.value || !props.showCreateModal)return;
    const newEmp = await workforceApi.createEmployee({
      name: createForm.value.name.trim(),
      role: createForm.value.role.trim() || '通用业务协作者',
      avatar: createForm.value.avatar,
      executionEngine: createForm.value.executionEngine,
      modelId: foundModel.id,
      modelName: foundModel.name,
      ...(realPlatform ? {} : {thinkingLevel: createForm.value.thinkingLevel})
    });
    if(!alive || workspace!==activeWorkspaceId.value)return;
    emit('update:showCreateModal', false);
    emit('created', newEmp);
    message.success(`员工「${newEmp.name}」已创建！请继续为其接入飞书、钉钉或企微渠道并配置群策略。`);
  } catch (err) {
    if(alive && workspace===activeWorkspaceId.value)message.error(err instanceof Error ? err.message : '创建 AI 员工失败');
  }finally{creating.value=false;}
}

function close() {
  if (!creating.value) emit('update:showCreateModal', false);
}
</script>

<template>
  <NModal :show="showCreateModal" preset="card" title="新建 AI 员工" style="width: min(540px, 94vw);" :closable="!creating" :mask-closable="!creating" :close-on-esc="!creating" @update:show="v => { if (!v) close() }">
    <NAlert v-if="!enabledModels.length" type="warning" :bordered="false" style="margin-bottom: 12px;">
      当前工作区尚未配置可用模型，请先前往「资源中心 -&gt; 模型管理」添加并启用模型后再创建 AI 员工。
    </NAlert>
    <NForm ref="createFormRef" :model="createForm" :rules="createRules" label-placement="top" :disabled="creating">
      <NFormItem label="员工头像">
        <div class="create-avatar-select">
          <D2EmployeeAvatar :name="createForm.name || '新 AI 员工'" :preset="createForm.avatar" size="lg" />
          <div class="create-avatar-picker-body">
            <div class="av-mini-list">
              <button
                v-for="av in createAvatarBatch"
                :key="av"
                type="button"
                class="av-mini-btn"
                :class="{ active: createForm.avatar === av }"
                :title="`选择头像 ${av}`"
                @click="createForm.avatar = av"
              >
                <D2EmployeeAvatar :name="createForm.name || 'AI'" :preset="av" size="sm" />
              </button>
            </div>
            <div class="av-mini-actions">
              <span class="av-batch-hint">每次随机展示 12 款（共 {{ PORTRAIT_AVATAR_PRESETS.length }} 款国风人像）</span>
              <NButton size="tiny" secondary @click="shuffleCreateAvatarBatch">
                换一换
              </NButton>
            </div>
          </div>
        </div>
      </NFormItem>
      <NFormItem label="岗位名称" path="name">
        <NInput v-model:value="createForm.name" placeholder="例如：SRE 故障值班助手" />
      </NFormItem>
      <NFormItem label="岗位职责">
        <NInput
          v-model:value="createForm.role"
          type="textarea"
          :autosize="{ minRows: 5, maxRows: 12 }"
          :maxlength="2000"
          :placeholder="EMPLOYEE_ROLE_PLACEHOLDER"
          :input-props="{ 'aria-label': '岗位职责' }"
        />
      </NFormItem>
      <NFormItem label="底层执行引擎" path="executionEngine">
        <div class="create-engine-field">
          <NSelect
            :value="createForm.executionEngine"
            :options="realPlatform ? EXECUTION_ENGINE_OPTIONS.filter(option => option.value === 'pi') : EXECUTION_ENGINE_OPTIONS"
            @update:value="v => handleCreateEngineChange((v as ExecutionEngine) || 'pi')"
          />
          <span class="create-engine-hint">{{ createEngineMeta.protocolHint }}</span>
        </div>
      </NFormItem>
      <NFormItem label="基座模型" path="modelId">
        <div class="create-engine-field">
          <NSelect
            :value="createForm.modelId || null"
            :disabled="canManage === false || !compatibleModelsForCreate.length"
            :options="createModelSelectOptions"
            :placeholder="compatibleModelsForCreate.length ? '请选择与当前引擎兼容的已启用模型' : `暂无兼容 ${createEngineMeta.shortLabel} 的可用模型`"
            @update:value="v => createForm.modelId = v || ''"
          />
          <span v-if="realPlatform" class="create-engine-hint">所选模型用于该员工的对话和任务执行。</span>
          <NAlert v-if="realPlatform && createForm.modelId.startsWith('platform:')" type="warning" :bordered="false" style="margin-top:6px">选择官方模型创建员工后，官方模型费用将由工作区所有者的积分账户支付。</NAlert>
          <NAlert
            v-if="enabledModels.length && !compatibleModelsForCreate.length"
            type="warning"
            :bordered="false"
            style="margin-top: 6px;"
          >
            当前工作区暂无兼容 {{ createEngineMeta.requiredProtocolLabel }} 的已启用模型，请切换执行引擎或前往「资源中心 -&gt; 模型管理」添加对应协议模型。
          </NAlert>
        </div>
      </NFormItem>
    </NForm>
    <template #footer>
      <div style="display: flex; justify-content: flex-end; gap: 8px; flex-wrap: wrap;">
        <NButton v-if="realPlatform" :disabled="creating" @click="emit('load-models')">刷新模型选项</NButton>
        <NButton :disabled="creating" @click="close">取消</NButton>
        <NButton type="primary" :disabled="creating || canManage === false || !compatibleModelsForCreate.length" :loading="creating" @click="handleCreateEmployee">创建并进入渠道配置</NButton>
      </div>
    </template>
  </NModal>
</template>

<style scoped>
.create-engine-field {
  width: 100%;
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.create-engine-hint {
  font-size: 12px;
  line-height: 1.45;
  color: var(--id-text-secondary);
}

.create-avatar-select {
  display: flex;
  align-items: flex-start;
  gap: 14px;
  width: 100%;
  flex-wrap: wrap;
}

.create-avatar-picker-body {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.av-mini-list {
  display: grid;
  grid-template-columns: repeat(6, max-content);
  gap: 8px;
}

.av-mini-actions {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  flex-wrap: wrap;
}

.av-batch-hint {
  font-size: 12px;
  color: var(--id-text-muted);
}

@media (max-width: 520px) {
  .av-mini-list {
    grid-template-columns: repeat(4, max-content);
  }
}

.av-mini-btn {
  background: var(--id-bg-surface);
  border: 2px solid var(--id-border-subtle);
  border-radius: 50%;
  cursor: pointer;
  padding: 2px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  transition: border-color var(--id-transition-fast), background var(--id-transition-fast);
}

.av-mini-btn:hover {
  border-color: var(--id-border-strong);
}

.av-mini-btn:focus-visible {
  outline: none;
  box-shadow: var(--id-focus-ring);
}

.av-mini-btn.active {
  border-color: var(--id-accent-primary);
  background: var(--id-accent-primary-soft);
}
</style>

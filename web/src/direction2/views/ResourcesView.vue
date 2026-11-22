<script setup lang="ts">
import { ref, watch } from 'vue';
import { NTabs, NTabPane } from 'naive-ui';
import PlatformModelsPanel from './PlatformModelsPanel.vue';
import PlatformSkillsPanel from './PlatformSkillsPanel.vue';
import PlatformPluginsPanel from './PlatformPluginsPanel.vue';
import type { SummaryLanguage } from '../api/resource-display';
import ResourceDirectory from './ResourceDirectory.vue';

const language = ref<SummaryLanguage>('zh');

const props = defineProps<{ platformAdmin?:boolean; initialTab?: string | undefined; targetId?: string | undefined }>();
defineEmits<{ navigate: [view: string, tab: string, employeeId: string] }>();
const validResourceTabs = ['models', 'skills', 'mcp', 'directory'] as const;
type ResourceTabKey = (typeof validResourceTabs)[number];
function normalizeResourceTab(tab?: string): ResourceTabKey {
  if (tab === 'packages') return 'mcp';
  return (validResourceTabs as readonly string[]).includes(tab || '') ? (tab as ResourceTabKey) : 'models';
}
const activeTab = ref<ResourceTabKey>(normalizeResourceTab(props.initialTab));
watch(() => [props.initialTab, props.targetId], () => { activeTab.value = normalizeResourceTab(props.initialTab); });
</script>

<template>
  <div class="d2-resources">
    <div class="resources-header">
      <div>
        <h2 class="view-title">工作区资源库</h2>
        <p class="view-subtitle">浏览官方与空间自建资源，为 AI 员工配备工作所需的技能和插件。</p>
      </div>
    </div>

    <NTabs v-model:value="activeTab" type="line" class="resources-main-tabs">
      <NTabPane name="models" tab="共享模型池"><PlatformModelsPanel /></NTabPane>
      <NTabPane name="skills" tab="技能"><PlatformSkillsPanel v-model:language="language" :platform-admin="!!platformAdmin" :target-id="initialTab === 'skills' ? targetId : undefined" /></NTabPane>
      <NTabPane name="mcp" tab="插件"><PlatformPluginsPanel v-model:language="language" :platform-admin="!!platformAdmin" :target-id="initialTab === 'mcp' ? targetId : undefined" /></NTabPane>
      <NTabPane name="directory" tab="渠道通讯录与身份核验"><ResourceDirectory /></NTabPane>
    </NTabs>
  </div>
</template>

<style scoped>
.d2-resources {
  display: flex;
  flex-direction: column;
  gap: 16px;
}
.resources-header {
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

</style>
<style src="../styles/resource-library.css"></style>

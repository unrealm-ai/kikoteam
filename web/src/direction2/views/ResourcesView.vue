<script setup lang="ts">
import D2PageHeader from '../components/D2PageHeader.vue';
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
    <D2PageHeader title="资源库" />

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
</style>
<style src="../styles/resource-library.css"></style>

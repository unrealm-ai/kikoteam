<script setup lang="ts">
import { NConfigProvider, zhCN, dateZhCN } from 'naive-ui';
import { d2ThemeOverrides } from '../styles/theme-overrides';
import TaskDetailPanel from '../components/TaskDetailPanel.vue';
defineProps<{ token: string }>();
</script>

<template>
  <NConfigProvider :locale="zhCN" :date-locale="dateZhCN" :theme-overrides="d2ThemeOverrides">
    <main class="public-task">
      <TaskDetailPanel v-if="token" :public-token="token" presentation="page" />
      <p v-else role="alert">此任务详情链接无效。</p>
    </main>
  </NConfigProvider>
</template>

<style scoped>
.public-task { width: 100%; max-width: 1600px; height: 100vh; height: 100dvh; margin-inline: auto; }
</style>

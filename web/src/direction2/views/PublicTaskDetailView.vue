<script setup lang="ts">
import { NButton, NConfigProvider, zhCN, dateZhCN } from 'naive-ui';
import { d2ThemeOverrides } from '../styles/theme-overrides';
import TaskDetailPanel from '../components/TaskDetailPanel.vue';
defineProps<{ token: string }>();
</script>

<template>
  <NConfigProvider :locale="zhCN" :date-locale="dateZhCN" :theme-overrides="d2ThemeOverrides">
    <main class="public-task">
      <aside class="public-task-signin" aria-label="KIKO 登录引导">
        <img src="/brand/mark.svg" alt="" width="24" height="24" />
        <div class="signin-copy">
          <strong>在 KIKO 查看更多任务</strong>
          <span>登录后可在工作台查看你有权限的任务。</span>
        </div>
        <NButton tag="a" href="/" target="_blank" rel="noopener noreferrer" referrerpolicy="no-referrer" size="small" type="primary" secondary>
          登录 KIKO
        </NButton>
      </aside>
      <div class="public-task-content">
        <TaskDetailPanel v-if="token" :public-token="token" presentation="page" />
        <p v-else role="alert">此任务详情链接无效。</p>
      </div>
    </main>
  </NConfigProvider>
</template>

<style scoped>
.public-task { display: flex; flex-direction: column; width: 100%; max-width: 1600px; height: 100vh; height: 100dvh; margin-inline: auto; }
.public-task-content { flex: 1; min-height: 0; }
.public-task-signin {
  display: flex;
  align-items: center;
  flex: none;
  gap: 10px;
  padding: 8px 16px;
  border-bottom: 1px solid var(--id-border-subtle);
  background: var(--id-bg-subtle);
}
.public-task-signin img, .public-task-signin :deep(a) { flex: none; }
.signin-copy { flex: 1; min-width: 0; line-height: 1.45; }
.signin-copy strong { display: block; color: var(--id-text-primary); font-size: 13px; font-weight: 600; }
.signin-copy span { display: block; color: var(--id-text-secondary); font-size: 12px; }
@media (min-width: 640px) {
  .signin-copy strong, .signin-copy span { display: inline; }
  .signin-copy span { margin-inline-start: 12px; }
}
</style>

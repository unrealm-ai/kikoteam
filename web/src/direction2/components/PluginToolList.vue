<script setup lang="ts">
import type { PluginAccount } from '../api/capabilities';
defineProps<{ account: PluginAccount | undefined }>();
</script>

<template>
  <section aria-label="可用工具">
    <template v-if="account?.enabled && account.validated">
      <strong>可用工具（{{ account.tools.length }}）</strong>
      <p v-if="!account.tools.length">当前没有可用工具</p>
      <ul v-else>
        <li v-for="tool in account.tools" :key="tool.name">
          <code>{{ tool.name }}</code>
          <p v-if="tool.description" class="resource-hint-sm">{{ tool.description }}</p>
        </li>
      </ul>
    </template>
    <p v-else class="resource-hint-sm">使用时获取工具列表</p>
  </section>
</template>

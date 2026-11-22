<script setup lang="ts">
import { NDrawer, NDrawerContent } from 'naive-ui';
import type { Item } from '../api/resource-catalog';
import { categories, pluginAuthLabel, resourceSummary, type SummaryLanguage } from '../api/resource-display';
defineProps<{ item: Item; language: SummaryLanguage }>();
defineEmits<{ close: [] }>();
</script>
<template>
  <NDrawer class="library-drawer" :show="true" width="min(560px,95vw)" @update:show="open => { if (!open) $emit('close') }">
    <NDrawerContent :title="item.name" closable>
      <div class="overview">
        <small>{{ item.scope === 'official' ? '官方' : '空间自建' }} · {{ categories[item.card?.category ?? ''] || ('auth_type' in item ? '插件' : '技能') }}</small>
        <p :lang="item.card ? language : undefined">{{ resourceSummary(item, language) || '暂无简介' }}</p>
        <section v-if="item.card?.highlights"><h3>{{ 'auth_type' in item ? '插件内容' : '主要交付物' }}</h3><p>{{ item.card.highlights }}</p></section>
        <slot name="connection" />
        <details><summary>技术信息</summary><dl v-if="'auth_type' in item"><dt>最新版本</dt><dd>{{ item.definition_version }}</dd><dt>接入方式</dt><dd>MCP · Streamable HTTP</dd><dt>端点地址</dt><dd>{{ item.endpoint }}</dd><dt>认证方式</dt><dd>{{ pluginAuthLabel(item) }}</dd><template v-if="item.header_name"><dt>认证请求头</dt><dd>{{ item.header_name }}</dd></template></dl>
          <dl v-else><dt>技术名称</dt><dd>{{ item.current_version.technical_name }}</dd><dt>最新版本</dt><dd>{{ item.current_version.sequence }}</dd><dt>目录文件</dt><dd>{{ item.current_version.manifest.files.length }}</dd></dl>
        </details>
        <div class="overview-actions"><slot /></div>
      </div>
    </NDrawerContent>
  </NDrawer>
</template>
<style scoped>
.overview { display:grid; gap:16px; overflow-wrap:anywhere; color:var(--id-text-primary); }
.overview p { margin:0; line-height:1.7; }
.overview small,dt { color:var(--id-text-secondary); }
.overview h3 { margin:0 0 5px; font-size:14px; }
.overview-actions { display:flex; flex-wrap:wrap; gap:8px; padding-top:12px; border-top:1px solid var(--id-border-subtle); }
summary { cursor:pointer; } dl { display:grid; grid-template-columns:auto 1fr; gap:7px 12px; } dd { margin:0; }
</style>

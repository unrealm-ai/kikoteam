<script setup lang="ts">
import { NInput } from 'naive-ui';
import type { SummaryLanguage } from '../api/resource-display';
defineProps<{ kind: string; platformAdmin: boolean; total: number; loading: boolean }>();
const scope = defineModel<string>('scope', { required: true });
const query = defineModel<string>('query', { required: true });
const language = defineModel<SummaryLanguage>('language', { required: true });
const sources = [{ key: 'all', label: '全部' }, { key: 'official', label: '官方' }, { key: 'workspace', label: '空间自建' }];
</script>
<template>
  <div class="catalog-toolbar">
    <div class="source-switch" role="group" aria-label="资源来源">
      <button v-for="source in sources" :key="source.key" type="button" :aria-pressed="scope === source.key" @click="scope = source.key">{{ source.label }}</button>
      <button v-if="platformAdmin" type="button" :aria-pressed="scope === 'manage'" @click="scope = 'manage'">官方目录管理</button>
    </div>
    <div class="catalog-actions"><label>简介 <select v-model="language" aria-label="简介语言"><option value="zh">中文</option><option value="en">EN</option></select></label><slot /></div>
  </div>
  <div class="catalog-toolbar"><small aria-live="polite">{{ loading ? '正在读取…' : total + (kind === '插件' ? ' 个插件' : ' 项技能') }}</small>
    <NInput v-model:value="query" clearable size="small" :maxlength="200" :placeholder="'搜索' + kind + '名称或用途'" :input-props="{ 'aria-label': '搜索' + kind }" />
  </div>
</template>
<style scoped>
.catalog-toolbar,.catalog-actions,.source-switch { display:flex; flex-wrap:wrap; align-items:center; gap:8px; }
.catalog-toolbar { justify-content:space-between; gap:12px; }
.source-switch { padding:3px; gap:2px; border:1px solid var(--id-border-subtle); border-radius:8px; background:var(--id-bg-muted); }
.source-switch button,.catalog-actions select { font:inherit; cursor:pointer; background:transparent; color:var(--id-text-secondary); border:0; padding:4px 9px; border-radius:5px; }
.source-switch button[aria-pressed="true"] { background:var(--id-bg-surface); color:var(--id-text-primary); box-shadow:0 1px 3px #18263820; font-weight:600; }
.catalog-actions label,small { font-size:12px; color:var(--id-text-secondary); }
.catalog-actions select { background:var(--id-bg-surface); border:1px solid var(--id-border-subtle); }
.n-input { width:min(100%,240px); }
</style>

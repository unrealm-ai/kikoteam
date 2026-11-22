<script setup lang="ts">
import { computed } from 'vue';
import { NButton } from 'naive-ui';
import type { Item } from '../api/resource-catalog';
import { categories, pluginConnectionLabel, resourceSummary, type SummaryLanguage } from '../api/resource-display';

const props = defineProps<{ item: Item; language: SummaryLanguage; global?: boolean }>();
defineEmits<{ detail: [] }>();
const plugin = computed(() => 'auth_type' in props.item ? props.item : undefined);
const label = computed(() => categories[props.item.card?.category ?? ''] || (plugin.value ? '插件' : '技能'));
const mark = computed(() => props.item.name.match(/^[a-z]+/i)?.[0].slice(0, 2).toUpperCase() || props.item.name.charAt(0));
</script>

<template>
  <article class="catalog-card" :data-category="item.card?.category">
    <header><span class="catalog-mark" aria-hidden="true">{{ mark }}</span><div><small>{{ label }}</small><h3>{{ item.name }}</h3></div></header>
    <div class="catalog-body">
      <p class="catalog-summary" :lang="item.card ? language : undefined">{{ resourceSummary(item, language) || '暂无简介' }}</p>
      <div class="catalog-facts">
        <span v-if="!item.enabled" class="catalog-disabled">已停用</span>
        <span v-if="item.listed === false && item.scope === 'official'">未上架</span>
        <span v-if="plugin" class="catalog-connection" :class="{ ready: plugin.auth_type === 'none' || plugin.shared_connection_state === 'connected' }">{{ pluginConnectionLabel(plugin) }}</span>
        <span v-else-if="item.card?.highlights" class="catalog-output">交付物 · {{ item.card.highlights }}</span>
      </div>
    </div>
    <footer><span>{{ item.scope === 'official' ? '官方' : '空间自建' }}<template v-if="!global && item.assigned_agent_count != null"> · {{ item.assigned_agent_count ? '已分配 ' + item.assigned_agent_count + ' 位员工' : '尚未分配' }}</template></span>
      <NButton secondary size="small" @click="$emit('detail')">查看详情</NButton>
    </footer>
  </article>
</template>

<style scoped>
.catalog-card { --tint:#edf1f6; --ink:#425773; display:flex; flex-direction:column; min-width:0; overflow:hidden; background:var(--id-bg-surface); border:1px solid var(--id-border-subtle); border-radius:14px; box-shadow:0 2px 6px #19304808; }
.catalog-card.highlighted { outline:2px solid var(--id-color-primary, #2861df); outline-offset:1px; }
.catalog-card[data-category="market"] { --tint:#e3f3ef; --ink:#286454; }
.catalog-card[data-category="office"] { --tint:#eaeefb; --ink:#455e99; }
.catalog-card[data-category="dev"] { --tint:#eeebfa; --ink:#63518d; }
.catalog-card[data-category="sales"],.catalog-card[data-category="research"] { --tint:#faeee3; --ink:#82552b; }
.catalog-card[data-category="editor"] { --tint:#f7e9f1; --ink:#86446f; }
header { display:flex; align-items:center; gap:14px; min-height:84px; padding:16px 18px; background:linear-gradient(115deg,var(--tint),var(--id-bg-surface) 120%); }
header div { min-width:0; } header small { font-size:12px; font-weight:600; color:var(--ink); }
h3 { margin:2px 0 0; color:var(--id-text-primary); font-size:18px; line-height:1.4; overflow-wrap:anywhere; }
.catalog-mark { flex:none; display:grid; place-items:center; width:40px; height:40px; font-weight:650; color:var(--ink); background:var(--id-bg-surface); border-radius:12px; box-shadow:0 2px 6px #20364015; }
.catalog-body { flex:1; display:flex; flex-direction:column; gap:14px; padding:16px 18px; }
.catalog-summary { margin:0; color:var(--id-text-secondary); line-height:1.65; overflow-wrap:anywhere; display:-webkit-box; -webkit-line-clamp:3; -webkit-box-orient:vertical; overflow:hidden; }
.catalog-facts { margin-top:auto; display:flex; flex-wrap:wrap; align-items:center; gap:8px; font-size:12px; color:var(--id-text-secondary); }
.catalog-disabled { font-weight:600; }
.catalog-output { border-left:2px solid var(--id-border-subtle); padding-left:7px; }
.catalog-connection::before { content:''; display:inline-block; width:7px; height:7px; margin-right:6px; border-radius:50%; background:var(--id-status-warning); }
.catalog-connection.ready::before { background:var(--id-status-success); }
footer { display:flex; flex-wrap:wrap; justify-content:space-between; align-items:center; gap:6px; padding:10px 16px; border-top:1px solid var(--id-border-subtle); color:var(--id-text-muted); font-size:12px; }
</style>

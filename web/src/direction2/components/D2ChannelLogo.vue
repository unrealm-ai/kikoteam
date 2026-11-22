<script setup lang="ts">
import { computed } from 'vue';
import type { MemberProviderType } from '../types';

const props = withDefaults(
  defineProps<{
    platform: MemberProviderType | 'automation';
    size?: number | undefined;
    showLabel?: boolean | undefined;
    labelText?: string | undefined;
    title?: string | undefined;
  }>(),
  {
    size: 18,
    showLabel: false
  }
);

const platformNames: Record<string, string> = {
  feishu: '飞书',
  dingtalk: '钉钉',
  wecom: '企业微信',
  web: 'Web 工作区',
  automation: '自动触发器'
};

const resolvedLabel = computed(() => props.labelText || platformNames[props.platform] || props.platform);
const logoSrc = computed(() => {
  if (props.platform === 'automation') return '/channel-logos/automation.svg';
  if (props.platform === 'feishu' || props.platform === 'dingtalk' || props.platform === 'wecom') {
    return `/channel-logos/${props.platform}.png`;
  }
  return '';
});
</script>

<template>
  <span class="d2-channel-brand" :title="title || resolvedLabel">
    <span
      v-if="platform === 'web'"
      class="d2-web-badge"
      :style="{ width: `${size}px`, height: `${size}px`, fontSize: `${Math.max(10, Math.round(size * 0.58))}px` }"
    >W</span>
    <img
      v-else-if="logoSrc"
      :src="logoSrc"
      :alt="resolvedLabel"
      :width="size"
      :height="size"
      class="d2-channel-img"
    />
    <span v-if="showLabel" class="d2-channel-label">{{ resolvedLabel }}</span>
  </span>
</template>

<style scoped>
.d2-channel-brand {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  line-height: 1;
  vertical-align: middle;
}
.d2-channel-img {
  object-fit: contain;
  border-radius: 4px;
  flex-shrink: 0;
  display: block;
}
.d2-web-badge {
  display: inline-grid;
  place-items: center;
  border-radius: 4px;
  background: #ECEFF1;
  color: #182024;
  font-weight: 700;
  border: 1px solid #DFE4E7;
  flex-shrink: 0;
}
.d2-channel-label {
  font-size: inherit;
  font-weight: 500;
}
</style>

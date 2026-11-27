<script setup lang="ts">
import { NButton } from 'naive-ui';
import type { AssistantCard } from '../api/assistant';
defineProps<{cards: AssistantCard[]; disabled?: boolean}>();
defineEmits<{choose: [card: AssistantCard]}>();
</script>

<template>
  <div class="assistant-demo-cards" aria-label="体验场景">
    <p class="assistant-demo-note">使用样例数据，体验真实任务执行流程。</p>
    <NButton v-for="card in cards" :key="card.key" secondary :disabled="disabled" @click="$emit('choose', card)">
      <span class="assistant-demo-copy"><strong>{{ card.title }}</strong><small>{{ card.description }}</small></span>
    </NButton>
  </div>
</template>

<style scoped>
.assistant-demo-cards{display:flex;flex-wrap:wrap;gap:8px;margin-block:8px}
.assistant-demo-note{flex-basis:100%;margin:0;color:var(--id-text-muted);font-size:12px}
.assistant-demo-cards :deep(.n-button){height:auto;min-height:46px;white-space:normal;text-align:start;padding-block:8px}
.assistant-demo-copy{display:flex;flex-direction:column;align-items:flex-start;gap:3px}
.assistant-demo-copy small{color:var(--id-text-muted)}
</style>

<script setup lang="ts">
defineProps<{ kind: 'send' | 'stop' }>();
</script>

<template>
  <svg v-if="kind === 'stop'" viewBox="0 0 20 20" fill="currentColor" aria-hidden="true">
    <rect x="5" y="5" width="10" height="10" rx="1.5" />
  </svg>
  <svg v-else viewBox="0 0 20 20" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
    <path d="M10 15.5v-11M5.5 9l4.5-4.5L14.5 9" />
  </svg>
</template>

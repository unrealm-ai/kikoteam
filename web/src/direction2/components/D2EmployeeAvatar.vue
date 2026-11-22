<script setup lang="ts">
import { computed, ref, watch } from 'vue';
import { resolveD2AvatarPreset } from '../types';

const props = withDefaults(
  defineProps<{
    name: string;
    preset?: string | null | undefined;
    identityKey?: string | undefined;
    size?: 'sm' | 'md' | 'lg' | 'xl' | undefined;
    rounded?: 'circle' | 'square' | undefined;
  }>(),
  {
    size: 'md',
    rounded: 'circle'
  }
);

const failed = ref(false);
watch(() => [props.preset, props.identityKey], () => {
  failed.value = false;
});

const selected = computed(() => resolveD2AvatarPreset(props.preset, props.identityKey ?? props.name));
const src = computed(() => `/employee-avatars/${selected.value}.webp`);
const initial = computed(() => Array.from((props.name || '员').trim())[0] || '员');
</script>

<template>
  <span
    class="d2-emp-avatar"
    :class="[`d2-emp-avatar--${size}`, `d2-emp-avatar--${rounded}`]"
    :title="name"
  >
    <img v-if="!failed" :src="src" :alt="name" @error="failed = true" />
    <span v-else class="d2-avatar-fallback">{{ initial }}</span>
  </span>
</template>

<style scoped>
.d2-emp-avatar {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  flex-shrink: 0;
  overflow: hidden;
  background: #EFF6FF;
  color: #2563EB;
  font-weight: 700;
  border: 1px solid #E2E8F0;
  user-select: none;
}
.d2-emp-avatar--circle {
  border-radius: 50%;
}
.d2-emp-avatar--square {
  border-radius: 10px;
}
.d2-emp-avatar--sm {
  width: 36px;
  height: 36px;
  font-size: 13px;
}
.d2-emp-avatar--md {
  width: 44px;
  height: 44px;
  font-size: 15px;
}
.d2-emp-avatar--lg {
  width: 54px;
  height: 54px;
  font-size: 18px;
}
.d2-emp-avatar--xl {
  width: 72px;
  height: 72px;
  font-size: 24px;
}
.d2-emp-avatar img {
  width: 100%;
  height: 100%;
  object-fit: cover;
  display: block;
}
.d2-avatar-fallback {
  line-height: 1;
}
</style>

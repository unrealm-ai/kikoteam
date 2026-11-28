<script setup lang="ts">
import { ref } from 'vue';

const props = defineProps<{
  workspace: string;
  task: string;
  images: { id: string; name: string; size?: number | undefined }[];
  compact?: boolean;
}>();
const dimensions = ref<Record<string, string>>({});
function recordDimensions(id: string, event: Event) {
  const image = event.currentTarget as HTMLImageElement;
  if (image.naturalWidth && image.naturalHeight) {
    dimensions.value[id] = image.naturalWidth + ' × ' + image.naturalHeight;
  }
}
function sizeLabel(bytes?: number) {
  if (bytes === undefined) return '';
  if (bytes < 1024) return bytes + ' B';
  if (bytes < 1024 * 1024) return (bytes / 1024).toFixed(1) + ' KiB';
  return (bytes / (1024 * 1024)).toFixed(2) + ' MiB';
}
function contentURL(id: string) {
  return (
    '/api/v1/workspaces/' +
    encodeURIComponent(props.workspace) +
    '/tasks/' +
    encodeURIComponent(props.task) +
    '/artifacts/' +
    encodeURIComponent(id) +
    '/content'
  );
}
</script>
<template>
  <div v-if="compact" class="artifact-tiles">
    <a v-for="image in images" :key="image.id" class="artifact-tile" :href="contentURL(image.id) + '?download=1'" download :aria-label="'下载原图 ' + image.name">
      <img :src="contentURL(image.id)" alt="" loading="lazy" @load="recordDimensions(image.id, $event)" />
      <span><strong>{{ image.name }}</strong><small>{{ [dimensions[image.id], 'PNG', sizeLabel(image.size)].filter(Boolean).join(' · ') }}</small></span>
    </a>
  </div>
  <template v-else>
  <figure v-for="image in images" :key="image.id">
    <img :src="contentURL(image.id)" :alt="image.name" loading="lazy" />
    <figcaption><a :href="contentURL(image.id) + '?download=1'" download>下载原图</a></figcaption>
  </figure>
  </template>
</template>
<style scoped>
.artifact-tiles { display: grid; gap: 5px; margin: 8px 0; }
.artifact-tile { display: flex; align-items: center; gap: 12px; padding: 8px; border-radius: var(--id-radius-md); background: var(--id-bg-subtle); color: var(--id-text-primary); text-decoration: none; }
.artifact-tile:hover { background: var(--id-bg-hover); }
.artifact-tile img { width: 54px; height: 54px; flex: none; object-fit: cover; border-radius: 5px; background: var(--id-bg-muted); }
.artifact-tile span { min-width: 0; }
.artifact-tile strong, .artifact-tile small { display: block; overflow-wrap: anywhere; }
.artifact-tile small { margin-top: 3px; color: var(--id-text-secondary); font-size: 11px; }
figure {
  margin: 0 18px 16px;
}
img {
  display: block;
  max-width: 100%;
  max-height: 28rem;
}
figcaption {
  margin-top: 0.5rem;
}
</style>

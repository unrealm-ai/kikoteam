<script setup lang="ts">
const props = defineProps<{
  workspace: string;
  task: string;
  images: { id: string; name: string }[];
}>();
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
  <figure v-for="image in images" :key="image.id">
    <img :src="contentURL(image.id)" :alt="image.name" loading="lazy" />
    <figcaption><a :href="contentURL(image.id) + '?download=1'" download>下载原图</a></figcaption>
  </figure>
</template>
<style scoped>
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

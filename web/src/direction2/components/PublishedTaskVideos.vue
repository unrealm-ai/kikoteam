<script setup lang="ts">
const props = defineProps<{
  workspace: string;
  task: string;
  videos: { id: string; name: string }[];
}>();
function content(id: string) {
  return (
    "/api/v1/workspaces/" +
    encodeURIComponent(props.workspace) +
    "/tasks/" +
    encodeURIComponent(props.task) +
    "/artifacts/" +
    encodeURIComponent(id) +
    "/content"
  );
}
</script>
<template>
  <figure v-for="video in videos" :key="video.id" class="published-video">
    <video
      controls
      playsinline
      preload="metadata"
      :aria-label="video.name"
      :src="content(video.id)"
    />
    <figcaption>
      <a :href="content(video.id) + '?download=1'" :download="video.name"
        >下载视频 · {{ video.name }}</a
      >
    </figcaption>
  </figure>
</template>
<style scoped>
.published-video {
  margin: 12px 0;
  min-width: 0;
}
.published-video video {
  display: block;
  width: 100%;
  max-width: 40rem;
  max-height: 24rem;
  background: #111;
  border-radius: 8px;
}
.published-video figcaption {
  margin-top: 6px;
  overflow-wrap: anywhere;
}
</style>

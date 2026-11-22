<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue';
import {
  listProject,
  downloadProjectFile,
  type ProjectEntry
} from '../api/project-files';
import { useProjectDraft } from '../composables/useProjectDraft';
import ProjectDraftFiles from './ProjectDraftFiles.vue';
const props = defineProps<{ workspace: string; conversation: string; readOnly: boolean }>();
const emit = defineEmits<{ close: []; reference: [file: ProjectEntry] }>();
const children = ref<Record<string, ProjectEntry[]>>({}),
  expanded = ref(new Set([''])),
  selected = ref<ProjectEntry>(),
  current = ref('');
const error = ref(''),
  busy = ref(false);
const uploads = useProjectDraft(() => props, file => {
  uploads.remove(file.key);
  expanded.value.add(file.directory ?? '');
  void refresh();
});
const lifetime = new AbortController();
let loadVersion = 0;
const rows = computed(() => {
  const result: { entry: ProjectEntry; depth: number }[] = [];
  function walk(p: string, depth: number) {
    for (const entry of children.value[p] || []) {
      result.push({ entry, depth });
      if (expanded.value.has(entry.path)) walk(entry.path, depth + 1);
    }
  }
  if (expanded.value.has('')) walk('', 1);
  return result;
});
async function refresh() {
  const version = ++loadVersion;
  busy.value = true;
  error.value = '';
  const next: Record<string, ProjectEntry[]> = Object.create(null);
  try {
    for (const p of [...expanded.value].sort()) {
      if (p && !next[p.slice(0, Math.max(0, p.lastIndexOf('/')))]?.some((e) => e.path === p)) continue;
      next[p] = (await listProject(props.workspace, props.conversation, p, lifetime.signal)).entries;
    }
    if (version !== loadVersion || lifetime.signal.aborted) return;
    children.value = next;
    if (current.value && !next[current.value]) current.value = '';
    if (
      selected.value &&
      !Object.values(next)
        .flat()
        .some((e) => e.path === selected.value?.path)
    )
      selected.value = undefined;
  } catch (e) {
    if (version === loadVersion && !lifetime.signal.aborted) {
      error.value = e instanceof Error ? e.message : '目录读取失败';
      children.value = {};
      selected.value = undefined;
    }
  } finally {
    if (version === loadVersion) busy.value = false;
  }
}
function folder(p: string) {
  current.value = p;
  selected.value = undefined;
  if (expanded.value.has(p)) {
    expanded.value.delete(p);
  } else {
    expanded.value.add(p);
    void refresh();
  }
}
function select(entry: ProjectEntry) {
  if (entry.kind === 'directory') {
    folder(entry.path);
    return;
  }
  selected.value = entry;
  current.value = entry.path.slice(0, Math.max(0, entry.path.lastIndexOf('/')));
}
async function download() {
  if (!selected.value) return;
  try {
    await downloadProjectFile(props.workspace, props.conversation, selected.value.path, lifetime.signal);
  } catch (e) {
    if (!lifetime.signal.aborted) error.value = e instanceof Error ? e.message : '下载失败';
  }
}
onMounted(refresh);
onUnmounted(() => lifetime.abort());
defineExpose({ refresh });
</script>
<template>
  <aside class="file-tree" aria-label="项目文件">
    <header>
      <strong>项目文件</strong>
      <button :disabled="busy" @click="refresh" aria-label="刷新目录">↻</button>
      <button @click="emit('close')" aria-label="收起项目文件">›</button>
    </header>
    <ProjectDraftFiles :draft="uploads" :disabled="readOnly" :directory="current" />
    <p v-if="error" role="alert">{{ error }}</p>
    <nav>
      <button @click="folder('')" :aria-expanded="expanded.has('')">
        {{ expanded.has('') ? '▾' : '▸' }}　project
      </button>
      <button
        v-for="{ entry, depth } in rows"
        :key="entry.path"
        :style="{ paddingLeft: depth * 15 + 9 + 'px' }"
        :class="{ selected: selected?.path === entry.path }"
        @click="select(entry)"
        @dblclick="entry.kind === 'file' && download()"
      >
        <span>{{ entry.kind === 'directory' ? (expanded.has(entry.path) ? '▾' : '▸') : '▤' }}</span>
        {{ entry.name }}
      </button>
      <small v-if="busy">读取中…</small>
      <small v-else-if="!error && expanded.has('') && !rows.length">目录为空</small>
    </nav>
    <footer>
      <button
        class="wide"
        :disabled="!selected || readOnly"
        @click="selected && emit('reference', selected)"
      >
        ↗ 引用到对话
      </button>
      <button v-if="selected" @click="download">下载文件</button>
    </footer>
  </aside>
</template>
<style scoped>
.file-tree {
  width: clamp(235px, 23vw, 350px);
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 12px;
  padding: 14px;
  background: var(--id-bg-surface);
  border: 1px solid var(--id-border-subtle);
  border-radius: var(--id-radius-lg);
  overflow: auto;
}
.file-tree header {
  display: flex;
  align-items: center;
  gap: 8px;
}
.file-tree strong {
  flex: 1;
}
.file-tree button {
  background: transparent;
  border: 0;
  color: inherit;
  cursor: pointer;
  padding: 6px;
  border-radius: 5px;
}
.file-tree button:disabled {
  opacity: 0.45;
  cursor: default;
}
.file-tree .wide {
  background: #edf8f1;
  border: 1px solid #a2c7ae;
  color: #216943;
  width: 100%;
}
.file-tree nav {
  display: flex;
  flex-direction: column;
  min-height: 65px;
  overflow: auto;
}
.file-tree nav button {
  text-align: left;
  overflow-wrap: anywhere;
}
.file-tree nav span {
  margin-right: 8px;
}
.file-tree nav .selected {
  background: #ddf2e5;
}
.file-tree footer {
  border-top: 1px solid var(--id-border-subtle);
  padding-top: 12px;
}
.file-tree [role='alert'] {
  color: var(--id-status-danger);
}
@media (max-width: 1200px) {
  .file-tree {
    width: auto;
    min-height: 180px;
  }
}
</style>

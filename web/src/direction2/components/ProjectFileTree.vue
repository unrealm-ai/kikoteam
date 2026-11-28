<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue';
import {
  listProject,
  downloadProjectFile,
  readProjectFileText,
  type ProjectEntry
} from '../api/project-files';
import { useProjectDraft } from '../composables/useProjectDraft';
import ProjectDraftFiles from './ProjectDraftFiles.vue';
import SessionMarkdown from './SessionMarkdown.vue';
const props = defineProps<{ workspace: string; conversation: string; readOnly: boolean; embedded?: boolean }>();
const emit = defineEmits<{ close: []; reference: [file: ProjectEntry] }>();
const children = ref<Record<string, ProjectEntry[]>>({}),
  expanded = ref(new Set([''])),
  selected = ref<ProjectEntry>(),
  current = ref('');
const error = ref(''),
  busy = ref(false);
const previewText = ref(''),
  previewLoading = ref(false),
  previewError = ref('');
const uploads = useProjectDraft(() => props, file => {
  uploads.remove(file.key);
  expanded.value.add(file.directory ?? '');
  void refresh();
});
const lifetime = new AbortController();
let previewController: AbortController | undefined;
let loadVersion = 0;
let previewVersion = 0;
const PREVIEWABLE_EXTS = new Set(['md', 'markdown', 'txt', 'csv', 'json', 'log', 'yml', 'yaml', 'ts', 'tsx', 'js', 'jsx', 'py', 'go', 'sh', 'sql', 'xml']);
const MAX_PREVIEW_BYTES = 262144;

function fileExt(name: string) {
  const idx = name.lastIndexOf('.');
  return idx >= 0 ? name.slice(idx + 1).toLowerCase() : '';
}
function isMarkdownExt(ext: string) {
  return ext === 'md' || ext === 'markdown';
}
function formatFileSize(bytes?: number) {
  if (bytes == null) return '';
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1048576) return `${(bytes / 1024).toFixed(1)} KB`;
  return `${(bytes / 1048576).toFixed(1)} MB`;
}
const selectedExt = computed(() => (selected.value?.kind === 'file' ? fileExt(selected.value.name) : ''));
const selectedIsMarkdown = computed(() => isMarkdownExt(selectedExt.value));
const selectedPreviewable = computed(() => {
  const entry = selected.value;
  if (!entry || entry.kind !== 'file') return false;
  return PREVIEWABLE_EXTS.has(selectedExt.value) && (entry.size_bytes ?? 0) <= MAX_PREVIEW_BYTES;
});

async function loadPreview(entry?: ProjectEntry) {
  previewController?.abort();
  previewController = undefined;
  const ver = ++previewVersion;
  previewText.value = '';
  previewError.value = '';
  previewLoading.value = false;
  if (!entry || entry.kind !== 'file') return;
  const ext = fileExt(entry.name);
  if (!PREVIEWABLE_EXTS.has(ext)) return;
  if ((entry.size_bytes ?? 0) > MAX_PREVIEW_BYTES) {
    previewError.value = '文件超过 256 KB，请下载后查看完整内容';
    return;
  }
  const ctrl = new AbortController();
  previewController = ctrl;
  previewLoading.value = true;
  try {
    const content = await readProjectFileText(
      props.workspace,
      props.conversation,
      entry.path,
      ctrl.signal,
      MAX_PREVIEW_BYTES
    );
    if (ver !== previewVersion || ctrl.signal.aborted || lifetime.signal.aborted) return;
    previewText.value = content;
  } catch (e) {
    if (ver !== previewVersion || ctrl.signal.aborted || lifetime.signal.aborted) return;
    previewError.value = e instanceof Error ? e.message : '暂无法预览此文件';
  } finally {
    if (ver === previewVersion) previewLoading.value = false;
  }
}

watch(
  () => selected.value?.path,
  () => {
    void loadPreview(selected.value);
  }
);

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
    if (selected.value) {
      const latest = Object.values(next)
        .flat()
        .find((e) => e.path === selected.value?.path);
      selected.value = latest;
      if (latest) void loadPreview(latest);
    }
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
  const isSameFile = selected.value?.path === entry.path;
  selected.value = entry;
  current.value = entry.path.slice(0, Math.max(0, entry.path.lastIndexOf('/')));
  if (isSameFile) void loadPreview(entry);
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
onUnmounted(() => {
  previewController?.abort();
  lifetime.abort();
});
defineExpose({ refresh, busy });
</script>
<template>
  <aside class="file-tree" :class="{ embedded, 'has-selected': selected?.kind === 'file' }" aria-label="项目文件">
    <header v-if="!embedded">
      <strong>项目文件</strong>
      <button :disabled="busy" @click="refresh" aria-label="刷新目录">↻</button>
      <button @click="emit('close')" aria-label="收起项目文件">›</button>
    </header>
    <div class="tree-upload-row">
      <span class="tree-dir-hint">目录：project{{ current ? '/' + current : '' }}</span>
      <ProjectDraftFiles :draft="uploads" :disabled="readOnly" :directory="current" />
    </div>
    <p v-if="error" role="alert">{{ error }}</p>
    <nav>
      <button @click="folder('')" :aria-expanded="expanded.has('')">
        <span>{{ expanded.has('') ? '▾' : '▸' }}</span>
        <span class="entry-name">project</span>
      </button>
      <button
        v-for="{ entry, depth } in rows"
        :key="entry.path"
        :style="{ paddingLeft: depth * 14 + 10 + 'px' }"
        :class="{ selected: selected?.path === entry.path }"
        @click="select(entry)"
        @dblclick="entry.kind === 'file' && download()"
      >
        <span>{{ entry.kind === 'directory' ? (expanded.has(entry.path) ? '▾' : '▸') : '▤' }}</span>
        <span class="entry-name">{{ entry.name }}</span>
        <small v-if="entry.kind === 'file' && entry.size_bytes != null" class="entry-size">{{ formatFileSize(entry.size_bytes) }}</small>
      </button>
      <small v-if="busy" class="tree-empty">读取中…</small>
      <small v-else-if="!error && expanded.has('') && !rows.length" class="tree-empty">目录为空</small>
    </nav>
    <section v-if="selected?.kind === 'file'" class="live-preview" aria-label="文件预览">
      <div class="preview-bar">
        <span class="preview-bar-title">
          <span>文件预览</span>
          <span class="preview-pill">{{ selectedIsMarkdown ? 'Markdown' : (selectedExt ? selectedExt.toUpperCase() : 'FILE') }}</span>
        </span>
        <span class="preview-name" :title="selected.name">{{ selected.name }}</span>
      </div>
      <div class="preview-content">
        <small v-if="previewLoading" class="preview-state">读取预览内容…</small>
        <small v-else-if="previewError" class="preview-state">{{ previewError }}</small>
        <small v-else-if="!selectedPreviewable" class="preview-state">此文件类型暂不支持文本预览，可点击下方下载查看。</small>
        <SessionMarkdown v-else-if="selectedIsMarkdown" :text="previewText" />
        <pre v-else class="preview-plain">{{ previewText }}</pre>
      </div>
    </section>
    <footer>
      <button
        class="wide"
        :disabled="!selected || readOnly"
        @click="selected && emit('reference', selected)"
      >
        ↗ 引用到对话
      </button>
      <button v-if="selected" class="dl-btn" @click="download">↓ 下载文件</button>
    </footer>
  </aside>
</template>
<style scoped>
.file-tree {
  width: clamp(280px, 25vw, 382px);
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 10px;
  padding: 12px 14px;
  background: var(--id-bg-subtle);
  border: 1px solid var(--id-border-subtle);
  border-radius: var(--id-radius-lg);
  overflow: hidden;
}
.file-tree.embedded {
  width: 100%;
  flex: 1;
  min-height: 0;
  border: 0;
  border-radius: 0;
  padding: 10px 14px 12px;
}
.file-tree header {
  display: flex;
  align-items: center;
  gap: 6px;
  padding-bottom: 8px;
  border-bottom: 1px solid var(--id-border-hairline);
}
.file-tree strong {
  flex: 1;
  font-size: 13px;
  font-weight: 600;
  color: var(--id-text-primary);
}
.tree-upload-row {
  display: flex;
  flex-direction: column;
  gap: 6px;
}
.tree-dir-hint {
  font-size: 11px;
  color: var(--id-text-muted);
  font-family: var(--id-font-mono);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.file-tree button {
  background: transparent;
  border: 0;
  color: var(--id-text-secondary);
  cursor: pointer;
  padding: 6px 8px;
  border-radius: 8px;
  font-size: 12px;
  transition: background-color var(--id-transition-fast), color var(--id-transition-fast);
}
.file-tree button:hover:not(:disabled) {
  background: var(--id-bg-hover);
  color: var(--id-text-primary);
}
.file-tree button:disabled {
  opacity: 0.45;
  cursor: default;
}
.file-tree nav {
  display: flex;
  flex-direction: column;
  gap: 3px;
  flex: 1;
  min-height: 70px;
  overflow: auto;
  padding-bottom: 4px;
}
.file-tree.has-selected nav {
  flex: 0 1 auto;
  max-height: 196px;
  border-bottom: 1px solid var(--id-border-subtle);
  padding-bottom: 10px;
}
.file-tree nav button {
  display: flex;
  align-items: center;
  gap: 7px;
  text-align: left;
  color: var(--id-text-primary);
  line-height: 1.45;
  border: 1px solid transparent;
}
.file-tree nav button span:first-child {
  color: var(--id-text-muted);
  flex-shrink: 0;
}
.entry-name {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.entry-size {
  font-size: 11px;
  font-family: var(--id-font-mono);
  font-variant-numeric: tabular-nums;
  color: var(--id-text-muted);
  flex-shrink: 0;
}
.tree-empty {
  padding: 6px 10px;
  font-size: 12px;
  color: var(--id-text-muted);
}
.file-tree nav .selected {
  background: var(--id-bg-surface);
  border-color: var(--id-border-strong);
  box-shadow: 0 1px 3px rgba(28, 25, 23, 0.05);
  color: var(--id-text-primary);
  font-weight: 600;
}
.file-tree nav .selected span:first-child {
  color: var(--id-text-primary);
}
.live-preview {
  flex: 1;
  min-height: 140px;
  background: var(--id-bg-surface);
  border: 1px solid var(--id-border-subtle);
  border-radius: 12px;
  box-shadow: 0 2px 8px rgba(28, 25, 23, 0.03);
  display: flex;
  flex-direction: column;
  overflow: hidden;
}
.preview-bar {
  padding: 8px 12px;
  border-bottom: 1px solid var(--id-border-hairline);
  background: #FCFCFA;
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
}
.preview-bar-title {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  font-size: 12px;
  font-weight: 600;
  color: var(--id-text-primary);
  flex-shrink: 0;
}
.preview-pill {
  font-size: 10px;
  font-weight: 600;
  color: #059669;
  background: #ECFDF5;
  padding: 1px 6px;
  border-radius: 4px;
}
.preview-name {
  font-size: 11px;
  font-family: var(--id-font-mono);
  color: var(--id-text-muted);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.preview-content {
  flex: 1;
  min-height: 0;
  overflow: auto;
  padding: 12px;
  font-size: 12px;
}
.preview-content :deep(.session-markdown) {
  font-size: 12.5px;
  line-height: 1.65;
}
.preview-plain {
  margin: 0;
  font-family: var(--id-font-mono);
  font-size: 11.5px;
  line-height: 1.6;
  color: var(--id-text-secondary);
  white-space: pre-wrap;
  word-break: break-word;
}
.preview-state {
  color: var(--id-text-muted);
  font-size: 12px;
  line-height: 1.6;
}
.file-tree footer {
  padding-top: 2px;
  display: flex;
  flex-direction: row;
  gap: 8px;
}
.file-tree .wide {
  flex: 1;
  height: 34px;
  background: var(--id-text-primary);
  border: 1px solid var(--id-text-primary);
  color: var(--id-text-inverse);
  font-weight: 600;
  border-radius: 9px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  padding: 0 10px;
}
.file-tree .wide:hover:not(:disabled) {
  background: #292524;
  border-color: #292524;
  color: var(--id-text-inverse);
}
.file-tree .dl-btn {
  height: 34px;
  padding: 0 12px;
  background: var(--id-bg-surface);
  border: 1px solid var(--id-border-strong);
  color: var(--id-text-secondary);
  font-weight: 500;
  border-radius: 9px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  flex-shrink: 0;
}
.file-tree .dl-btn:hover {
  background: var(--id-bg-hover);
  color: var(--id-text-primary);
}
.file-tree [role='alert'] {
  color: var(--id-status-danger);
  font-size: 12px;
}
@media (max-width: 1200px) {
  .file-tree {
    width: auto;
    min-height: 180px;
  }
}
</style>

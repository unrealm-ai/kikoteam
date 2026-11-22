import { computed, ref, reactive, onScopeDispose } from 'vue';
import { uploadProjectFile, type ProjectEntry } from '../api/project-files';

interface DraftFile {
  key: string;
  name: string;
  path: string;
  status: 'uploading' | 'ready' | 'failed';
  percent: number;
  error: string;
  source?: File;
  directory?: string | undefined;
}
export function useProjectDraft(
  scope: () => { workspace: string; conversation: string },
  onUploaded: (file: DraftFile) => void
) {
  const items = ref<DraftFile[]>([]);
  const paths = computed(() => items.value.filter((i) => i.status === 'ready').map((i) => i.path));
  const pending = computed(() => items.value.some((i) => i.status !== 'ready'));
  let controller = new AbortController();
  function reset() {
    controller.abort();
    controller = new AbortController();
    items.value = [];
  }
  onScopeDispose(reset);
  function reference(file: ProjectEntry) {
    if (items.value.some((item) => item.path === file.path)) return;
    items.value.push({
      key: crypto.randomUUID(),
      name: file.name,
      path: file.path,
      status: 'ready',
      percent: 100,
      error: ''
    });
  }
  async function transfer(item: DraftFile) {
    const { workspace, conversation } = scope(),
      signal = controller.signal;
    if (!item.source || !conversation) return;
    item.status = 'uploading';
    item.error = '';
    item.percent = 0;
    try {
      const file = await uploadProjectFile(
        workspace,
        conversation,
        item.source,
        item.directory,
        item.key,
        signal,
        (p) => (item.percent = p)
      );
      if (signal.aborted) return;
      item.path = file.path;
      item.name = file.name;
      item.status = 'ready';
      delete item.source;
      onUploaded(item);
    } catch (e) {
      if (!signal.aborted) {
        item.status = 'failed';
        item.error = e instanceof Error ? e.message : '上传失败';
      }
    }
  }
  async function enqueue(files: File[], directory?: string) {
    const signal = controller.signal;
    const batch = files.map((file) =>
      reactive<DraftFile>({
        key: crypto.randomUUID(),
        source: file,
        directory,
        name: file.name,
        path: '',
        status: 'uploading',
        percent: 0,
        error: ''
      })
    );
    items.value.push(...batch);
    for (const item of batch) {
      if (signal.aborted) return;
      if (items.value.some((i) => i.key === item.key)) await transfer(item);
    }
  }
  function remove(key: string) {
    items.value = items.value.filter((i) => i.key !== key);
  }
  return { items, paths, pending, enqueue, reference, remove, retry: transfer, reset };
}

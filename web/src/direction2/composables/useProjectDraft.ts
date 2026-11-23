import { computed, ref, reactive, onScopeDispose } from 'vue';
import { getChatAttachment, reserveChatAttachment, uploadProjectFile, type ProjectAttachment, type ProjectEntry } from '../api/project-files';

interface UploadScope { workspace: string; conversation: string }
interface DraftFile {
  key: string;
  name: string;
  path: string;
  attachmentId?: string;
  terminal?: boolean;
  status: 'reserving' | 'uploading' | 'ready' | 'failed';
  percent: number;
  error: string;
  source?: File;
  directory?: string | undefined;
  scope?: UploadScope;
}

export function useProjectDraft(
  scope: () => UploadScope,
  onUploaded: (file: DraftFile) => void,
  asyncAttachments = false
) {
  const items = ref<DraftFile[]>([]);
  const submitted = ref<DraftFile[]>([]);
  const completed = ref<{ id: string; scope: UploadScope; file: ProjectAttachment }[]>([]);
  const attempts = new Map<string, AbortController>();
  function current(item: Pick<DraftFile, 'scope'>) {
    const cur = scope();
    return item.scope?.workspace === cur.workspace && item.scope?.conversation === cur.conversation;
  }
  const uploads = computed(() => submitted.value.filter(current));
  const uploadedAttachments = computed(() => Object.fromEntries(completed.value.filter(current).map(({ id, file }) => [id, file])));
  const paths = computed(() => items.value.filter((i) => i.status === 'ready' && !i.attachmentId).map((i) => i.path));
  const attachmentIds = computed(() => items.value.flatMap((i) => i.attachmentId ? [i.attachmentId] : []));
  const pending = computed(() => items.value.some((i) => i.status !== 'ready'));
  const unreserved = computed(() => items.value.some((i) => i.terminal || !i.path && !i.attachmentId));

  function reset() {
    for (const item of items.value) {
      // Changing conversations must not strand a message already received by the server:
      // after an unknown send result the browser may still have the file bytes.
      if (asyncAttachments && item.attachmentId && item.status !== 'ready') {
        submitted.value.push(item);
      } else {
        attempts.get(item.key)?.abort();
      }
    }
    items.value = [];
  }
  onScopeDispose(reset);

  function clearSent() {
    submitted.value.push(...items.value.filter((i) => i.attachmentId && i.status !== 'ready'));
    items.value = [];
  }

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
    const target = item.scope, source = item.source;
    if (!source || !target?.conversation || item.terminal || attempts.has(item.key)) return;
    const controller = new AbortController(), signal = controller.signal;
    attempts.set(item.key, controller);
    item.status = asyncAttachments && !item.attachmentId ? 'reserving' : 'uploading';
    item.error = '';
    item.percent = 0;
    const ready = (path: string, name: string) => {
      item.path = path;
      item.name = name;
      item.status = 'ready';
      if (item.attachmentId) {
        completed.value = [...completed.value, {
          id: item.attachmentId,
          scope: target,
          file: { path, name, size_bytes: source.size }
        }].slice(-100);
      }
      delete item.source;
      submitted.value = submitted.value.filter((i) => i.key !== item.key);
      if (current(item)) onUploaded(item);
    };
    try {
      if (asyncAttachments && !item.attachmentId) {
        const reservation = await reserveChatAttachment(target.workspace, target.conversation, source, item.key, signal);
        if (signal.aborted) return;
        if (typeof reservation.id !== 'string' || !reservation.id) throw new Error('无法预留附件，请重试');
        item.attachmentId = reservation.id;
      }
      item.status = 'uploading';
      const file = await uploadProjectFile(
        target.workspace,
        target.conversation,
        source,
        item.directory,
        item.key,
        signal,
        (p) => (item.percent = p),
        item.attachmentId
      );
      if (signal.aborted) return;
      ready(file.path, file.name);
    } catch (e) {
      if (!signal.aborted && item.attachmentId) {
        // A lost browser response can still mean that the server finished the upload.
        const state = await getChatAttachment(target.workspace, target.conversation, item.attachmentId, signal).catch(() => null);
        if (signal.aborted) return;
        if (state?.status === 'ready' && state.path) {
          ready(state.path, state.name);
          return;
        }
        if (state?.status === 'failed') {
          item.status = 'failed';
          item.terminal = true;
          item.error = '附件上传失败，请重新添加文件';
          delete item.source;
          return;
        }
      }
      if (!signal.aborted) {
        item.status = 'failed';
        item.error = e instanceof Error ? e.message : '上传失败';
      }
    } finally {
      attempts.delete(item.key);
    }
  }

  async function enqueue(files: File[], directory?: string) {
    const target = { ...scope() };
    if (!target.conversation) return;
    const batch = files.map((file) =>
      reactive<DraftFile>({
        key: crypto.randomUUID(),
        source: file,
        directory,
        scope: target,
        name: file.name,
        path: '',
        status: asyncAttachments ? 'reserving' : 'uploading',
        percent: 0,
        error: ''
      })
    );
    items.value.push(...batch);
    if (asyncAttachments) {
      // Reserve every ID immediately; a large earlier file must not delay admission of another.
      await Promise.all(batch.map(transfer));
      return;
    }
    for (const item of batch) {
      if (items.value.some((i) => i.key === item.key)) await transfer(item);
    }
  }

  function remove(key: string) {
    if (!asyncAttachments || items.value.some((i) => i.key === key)) attempts.get(key)?.abort();
    items.value = items.value.filter((i) => i.key !== key);
  }
  return { items, uploads, uploadedAttachments, paths, attachmentIds, pending, unreserved, enqueue, reference, remove, retry: transfer, reset, clearSent };
}

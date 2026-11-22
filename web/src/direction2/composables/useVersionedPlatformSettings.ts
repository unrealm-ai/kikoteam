import { computed, onUnmounted, ref, shallowRef } from 'vue';
import { D2ApiError } from '../api/client';

type EditorOperation = 'read' | 'write' | 'test';
type SettingsWriter<S, D> = (draft: D, baseline: S, signal: AbortSignal) => Promise<S>;
interface EditorOptions<S, D> {
  read: (signal: AbortSignal) => Promise<S>;
  write: SettingsWriter<S, D>;
  toDraft: (snapshot: S) => D;
  validate: (draft: D, baseline: S, operation: 'write' | 'test') => string | undefined;
  errors: Record<string, string>;
  reviewRebase?: (previousDraft: D, mergedDraft: D) => { draft: D; notice?: string };
}

function rebase<D extends object>(existing: D, changed: D, next: D): D {
  const merged = { ...next };
  for (const key of Object.keys(changed) as Array<keyof D>) {
    if (existing[key] !== changed[key]) merged[key] = changed[key];
  }
  return merged;
}

export function useVersionedPlatformSettings<S, D extends object>(options: EditorOptions<S, D>) {
  const snapshot = shallowRef<S>();
  const draft = shallowRef<D>();
  const busy = ref<EditorOperation>();
  const error = ref('');
  const notice = ref('');
  const conflict = ref(false);
  const controller = new AbortController();
  const { signal } = controller;
  const dirty = computed(() => Boolean(snapshot.value && draft.value &&
    JSON.stringify(draft.value) !== JSON.stringify(options.toDraft(snapshot.value))));
  onUnmounted(() => controller.abort());

  function edit(patch: Partial<D>) {
    if (!draft.value || busy.value) return;
    draft.value = { ...draft.value, ...patch };
    notice.value = '';
  }
  function errorMessage(e: unknown, fallback: string) {
    if (!(e instanceof D2ApiError)) return fallback;
    if (e.status === 401 || e.status === 403) return '当前账号没有管理平台设置的权限，请重新登录或联系管理员。';
    const message = options.errors[e.code];
    if (message) return message;
    if (e.status === 409) {
      conflict.value = true;
      return '配置已被其他管理员更新。请读取最新版本并核对草稿后，再手动提交。';
    }
    return fallback;
  }
  function discard() {
    if (snapshot.value) draft.value = options.toDraft(snapshot.value);
    error.value = '';
    notice.value = '';
  }
  async function load(preserveDraft = false) {
    if (busy.value) return;
    const previous = snapshot.value;
    const current = draft.value;
    busy.value = 'read';
    error.value = notice.value = '';
    try {
      const latest = await options.read(signal);
      if (signal.aborted) return;
      const next = options.toDraft(latest);
      if (preserveDraft && current && previous) {
        const merged = rebase(options.toDraft(previous), current, next);
        const reviewed = options.reviewRebase?.(current, merged);
        draft.value = reviewed?.draft ?? merged;
        notice.value = reviewed?.notice || '已读取最新版本并保留修改，请核对后提交。';
      } else draft.value = next;
      snapshot.value = latest;
      conflict.value = false;
    } catch (e) {
      if (!signal.aborted) error.value = errorMessage(e, '读取失败，请稍后重试。');
    } finally { busy.value = undefined; }
  }
  async function perform(kind: 'write' | 'test', action: (draft: D, snapshot: S) => Promise<void>) {
    if (!draft.value || !snapshot.value || busy.value || conflict.value) return false;
    error.value = options.validate(draft.value, snapshot.value, kind) || '';
    notice.value = '';
    if (error.value) return false;
    busy.value = kind;
    try {
      await action(draft.value, snapshot.value);
      return !signal.aborted;
    } catch (e) {
      if (!signal.aborted) error.value = errorMessage(e, kind === 'write'
        ? '未确认保存结果。请先读取最新配置，再决定是否重试。'
        : '未确认测试结果。请检查配置或收件箱，再决定是否重试。');
      return false;
    } finally { busy.value = undefined; }
  }
  function save(importLegacy = false) {
    if (!dirty.value && !importLegacy) return Promise.resolve(true);
    return perform('write', async (values, saved) => {
      const result = await options.write(values, saved, signal);
      if (signal.aborted) return;
      snapshot.value = result;
      draft.value = options.toDraft(result);
      notice.value = '已保存并生效。';
    });
  }
  function test(action: (draft: D, snapshot: S, signal: AbortSignal) => Promise<unknown>, message: string) {
    return perform('test', async (values, saved) => {
      await action(values, saved, signal);
      if (!signal.aborted) notice.value = message;
    });
  }
  return { snapshot, draft, busy, error, notice, conflict, dirty, edit, load, save, test, discard };
}

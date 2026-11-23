import { platformRequest, post } from './platform';
import { D2ApiError } from './client';

export interface ProjectAttachment {
  path: string;
  name: string;
  size_bytes: number;
}
export interface SessionAttachment {
  id?: string;
  path?: string;
  name: string;
  size_bytes: number;
  status?: string;
}
export interface ProjectEntry {
  path: string;
  name: string;
  kind: 'directory' | 'file';
  size_bytes?: number;
  modified_at?: string;
}
const base = (w: string, c: string) =>
  `/workspaces/${encodeURIComponent(w)}/conversations/${encodeURIComponent(c)}/files`;
const attachmentBase = (w: string, c: string) =>
  `/workspaces/${encodeURIComponent(w)}/conversations/${encodeURIComponent(c)}/attachments`;
export function reserveChatAttachment(w: string, c: string, file: File, key: string, signal: AbortSignal) {
  return platformRequest<{ id: string }>(attachmentBase(w, c), {
    ...post({ name: file.name, size: file.size }),
    headers: { 'Idempotency-Key': key },
    signal: AbortSignal.any([signal, AbortSignal.timeout(20000)])
  });
}
export function getChatAttachment(w: string, c: string, id: string, signal: AbortSignal) {
  return platformRequest<SessionAttachment>(attachmentBase(w, c) + '/' + encodeURIComponent(id), {
    signal: AbortSignal.any([signal, AbortSignal.timeout(5000)])
  });
}
export function listProject(w: string, c: string, path: string, signal: AbortSignal) {
  return platformRequest<{ path: string; entries: ProjectEntry[] }>(
    base(w, c) + '?' + new URLSearchParams({ path }),
    { signal }
  );
}
export async function downloadProjectFile(w: string, c: string, path: string, signal: AbortSignal) {
  const response = await fetch('/api/v1' + base(w, c) + '/content?' + new URLSearchParams({ path }), {
    credentials: 'include',
    signal
  });
  if (!response.ok)
    throw new Error(
      response.status === 404 ? '文件已移动或删除' : `无法下载文件（HTTP ${response.status}）`
    );
  const url = URL.createObjectURL(await response.blob());
  const a = document.createElement('a');
  a.href = url;
  a.download = path.split('/').pop() || '文件';
  a.click();
  setTimeout(() => URL.revokeObjectURL(url), 60000);
}
export function uploadProjectFile(
  w: string,
  c: string,
  file: File,
  directory: string | undefined,
  key: string,
  signal: AbortSignal,
  onProgress: (percent: number) => void,
  attachmentId?: string
) {
  return new Promise<ProjectAttachment>((resolve, reject) => {
    const xhr = new XMLHttpRequest();
    xhr.open('POST', '/api/v1' + (attachmentId
      ? attachmentBase(w, c) + '/' + encodeURIComponent(attachmentId) + '/upload'
      : base(w, c)));
    xhr.withCredentials = true;
    xhr.responseType = 'json';
    xhr.timeout = 130000;
    xhr.setRequestHeader('X-CSRF-Protection', '1');
    xhr.setRequestHeader('Idempotency-Key', key);
    xhr.upload.onprogress = (e) => {
      if (e.lengthComputable) onProgress(Math.round((e.loaded / e.total) * 100));
    };
    const abort = () => xhr.abort();
    if (signal.aborted) {
      reject(new DOMException('已取消', 'AbortError'));
      return;
    }
    signal.addEventListener('abort', abort, { once: true });
    xhr.onloadend = () => signal.removeEventListener('abort', abort);
    xhr.onload = () => {
      if (xhr.status >= 200 && xhr.status < 300 && typeof xhr.response?.path === 'string') {
        resolve(xhr.response);
        return;
      }
      reject(
        new D2ApiError(
          xhr.status,
          xhr.response?.error?.code || 'upload_failed',
          xhr.response?.error?.message || '上传失败'
        )
      );
    };
    xhr.onerror = xhr.ontimeout = () => reject(new Error('上传中断，可以重试'));
    xhr.onabort = () => reject(new DOMException('已取消', 'AbortError'));
    const form = new FormData();
    form.append('file', file);
    if (!attachmentId && directory !== undefined) form.append('directory', directory);
    xhr.send(form);
  });
}

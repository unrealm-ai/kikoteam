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
async function fetchProjectFileContent(w: string, c: string, path: string, signal: AbortSignal, verb: string) {
  const response = await fetch('/api/v1' + base(w, c) + '/content?' + new URLSearchParams({ path }), {
    credentials: 'include',
    signal
  });
  if (!response.ok)
    throw new Error(
      response.status === 404 ? '文件已移动或删除' : `无法${verb}文件（HTTP ${response.status}）`
    );
  return response;
}
export class ProjectFilePreviewTooLargeError extends Error {
  constructor(maxBytes: number) {
    super(`文件超过 ${Math.round(maxBytes / 1024)} KB，请下载后查看完整内容`);
  }
}
export async function readProjectFileText(
  w: string,
  c: string,
  path: string,
  signal: AbortSignal,
  maxBytes = 262144
) {
  const response = await fetchProjectFileContent(w, c, path, signal, '预览');
  const contentLength = Number(response.headers.get('content-length') || '');
  if (Number.isFinite(contentLength) && contentLength > maxBytes) {
    void response.body?.cancel();
    throw new ProjectFilePreviewTooLargeError(maxBytes);
  }
  if (!response.body?.getReader) {
    const buffer = await response.arrayBuffer();
    if (buffer.byteLength > maxBytes) {
      throw new ProjectFilePreviewTooLargeError(maxBytes);
    }
    return new TextDecoder().decode(buffer);
  }
  const reader = response.body.getReader();
  const chunks: Uint8Array[] = [];
  let total = 0;
  try {
    while (true) {
      const { done, value } = await reader.read();
      if (done) break;
      if (!value) continue;
      total += value.byteLength;
      if (total > maxBytes) {
        await reader.cancel().catch(() => undefined);
        throw new ProjectFilePreviewTooLargeError(maxBytes);
      }
      chunks.push(value);
    }
  } finally {
    reader.releaseLock();
  }
  const merged = new Uint8Array(total);
  let offset = 0;
  for (const chunk of chunks) {
    merged.set(chunk, offset);
    offset += chunk.byteLength;
  }
  return new TextDecoder().decode(merged);
}
export async function downloadProjectFile(w: string, c: string, path: string, signal: AbortSignal) {
  const response = await fetchProjectFileContent(w, c, path, signal, '下载');
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

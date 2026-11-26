import type { TaskArtifact } from '../api/public-task';

export type ArtifactKind = 'markdown' | 'csv' | 'code' | 'image' | 'binary';

export interface ArtifactMeta {
  kind: ArtifactKind;
  badge: string;
  label: string;
  previewable: boolean;
}

const markdownExt = new Set(['md', 'markdown', 'mdx']);
const csvExt = new Set(['csv', 'tsv']);
const imageExt = new Set(['png', 'jpg', 'jpeg', 'gif', 'webp']);
const codeExt = new Set([
  'json', 'txt', 'log', 'py', 'sh', 'bash', 'zsh', 'js', 'jsx', 'ts', 'tsx',
  'yaml', 'yml', 'xml', 'sql', 'go', 'html', 'css', 'scss', 'toml', 'ini', 'env',
]);

export function fileExtension(name: string): string {
  const base = name.split('/').pop() || name;
  const idx = base.lastIndexOf('.');
  return idx > 0 ? base.slice(idx + 1).toLowerCase() : '';
}

export function classifyArtifact(file: Pick<TaskArtifact, 'name' | 'size'>): ArtifactMeta {
  const ext = fileExtension(file.name);
  if (markdownExt.has(ext)) return { kind: 'markdown', badge: 'MD', label: 'Markdown 文档', previewable: true };
  if (csvExt.has(ext)) return { kind: 'csv', badge: ext.toUpperCase(), label: '表格数据', previewable: true };
  if (imageExt.has(ext)) return { kind: 'image', badge: 'IMG', label: '图像文件', previewable: true };
  if (codeExt.has(ext)) {
    const badge = ext === 'json' ? 'JSON' : ext === 'txt' || ext === 'log' ? 'TXT' : 'CODE';
    return { kind: 'code', badge, label: ext === 'json' ? 'JSON 数据' : ext === 'txt' ? '纯文本' : '代码 / 文本', previewable: true };
  }
  return { kind: 'binary', badge: (ext || 'FILE').toUpperCase().slice(0, 4), label: '附件文件', previewable: false };
}

export function formatArtifactSize(bytes?: number | null): string {
  if (bytes == null || !Number.isFinite(bytes)) return '';
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
  return `${(bytes / (1024 * 1024)).toFixed(2)} MB`;
}

export function parseCsvRows(raw: string, delimiter: string, maxRows = 200): string[][] {
  const rows: string[][] = [];
  let row: string[] = [];
  let field = '';
  let inQuotes = false;
  for (let i = 0; i < raw.length; i++) {
    const ch = raw[i]!;
    if (inQuotes) {
      if (ch === '"' && raw[i + 1] === '"') {
        field += '"';
        i++;
      } else if (ch === '"') {
        inQuotes = false;
      } else {
        field += ch;
      }
      continue;
    }
    if (ch === '"') {
      inQuotes = true;
    } else if (ch === delimiter) {
      row.push(field);
      field = '';
    } else if (ch === '\r' && raw[i + 1] === '\n') {
      row.push(field);
      rows.push(row);
      if (rows.length >= maxRows) return rows;
      row = [];
      field = '';
      i++;
    } else if (ch === '\n' || ch === '\r') {
      row.push(field);
      rows.push(row);
      if (rows.length >= maxRows) return rows;
      row = [];
      field = '';
    } else {
      field += ch;
    }
  }
  if (field.length > 0 || row.length > 0) {
    row.push(field);
    rows.push(row);
  }
  return rows.filter(r => r.some(cell => cell.length > 0));
}

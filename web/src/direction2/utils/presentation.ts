import type { ExecutionEngine, ModelItem, ModelProtocol } from '../types';

export interface ExecutionEngineMeta {
  value: ExecutionEngine;
  label: string;
  shortLabel: string;
  supportedProtocols: ModelProtocol[];
  requiredProtocolLabel: string;
  protocolHint: string;
}

const EXECUTION_ENGINE_META_MAP: Record<ExecutionEngine, ExecutionEngineMeta> = {
  pi: {
    value: 'pi',
    label: 'PI 原生引擎 (默认 · 全协议兼容)',
    shortLabel: 'PI',
    supportedProtocols: ['chat_completions', 'openai_responses', 'anthropic_messages'],
    requiredProtocolLabel: 'Chat Completions / Responses / Anthropic Messages',
    protocolHint: 'PI 原生引擎支持全部三种模型协议（Chat Completions、OpenAI Responses、Anthropic Messages）。'
  },
  claudecode: {
    value: 'claudecode',
    label: 'Claude Code (Anthropic Messages 协议)',
    shortLabel: 'Claude Code',
    supportedProtocols: ['anthropic_messages'],
    requiredProtocolLabel: 'Anthropic Messages (/v1/messages)',
    protocolHint: 'Claude Code 引擎仅支持 Anthropic Messages (/v1/messages) 协议模型，已自动筛选兼容模型。'
  },
  codex: {
    value: 'codex',
    label: 'Codex (OpenAI Responses 协议)',
    shortLabel: 'Codex',
    supportedProtocols: ['openai_responses'],
    requiredProtocolLabel: 'OpenAI Responses (/v1/responses)',
    protocolHint: 'Codex 引擎仅支持 OpenAI Responses (/v1/responses) 协议模型，已自动筛选兼容模型。'
  }
};

export const EXECUTION_ENGINE_OPTIONS: Array<{ label: string; value: ExecutionEngine }> = [
  { label: EXECUTION_ENGINE_META_MAP.pi.label, value: 'pi' },
  { label: EXECUTION_ENGINE_META_MAP.claudecode.label, value: 'claudecode' },
  { label: EXECUTION_ENGINE_META_MAP.codex.label, value: 'codex' }
];

export function normalizeExecutionEngine(engine?: string | null): ExecutionEngine {
  if (engine === 'codex' || engine === 'claudecode' || engine === 'pi') {
    return engine;
  }
  return 'pi';
}

export function getExecutionEngineMeta(engine?: ExecutionEngine | string | null): ExecutionEngineMeta {
  return EXECUTION_ENGINE_META_MAP[normalizeExecutionEngine(engine)];
}

export function isModelCompatibleWithEngine(
  protocol: ModelProtocol,
  engine: ExecutionEngine = 'pi'
): boolean {
  return getExecutionEngineMeta(engine).supportedProtocols.includes(protocol);
}

export function filterModelsByEngine<T extends Pick<ModelItem, 'id'|'name'|'enabled'|'isDefault'|'protocol'>>(
  models: T[],
  engine: ExecutionEngine = 'pi'
): T[] {
  return models.filter(m => m.enabled && isModelCompatibleWithEngine(m.protocol, engine));
}

export function resolveCompatibleModelId(
  models: Pick<ModelItem, 'id'|'name'|'enabled'|'isDefault'|'protocol'>[],
  engine: ExecutionEngine = 'pi',
  currentModelId?: string
): string {
  const compatible = filterModelsByEngine(models, engine);
  if (!compatible.length) return '';
  if (currentModelId && compatible.some(m => m.id === currentModelId)) {
    return currentModelId;
  }
  return compatible.find(m => m.isDefault)?.id || compatible[0]?.id || '';
}

export interface StatusMeta {
  label: string;
  tagType: 'success' | 'info' | 'warning' | 'error' | 'default';
  dotColor?: string;
}

export function getEmployeeStatusMeta(status?: string): StatusMeta {
  switch (status) {
    case 'active':
      return { label: '运行中', tagType: 'success', dotColor: '#10B981' };
    case 'pending_setup':
      return { label: '待配置渠道', tagType: 'info', dotColor: '#3B82F6' };
    case 'warning':
      return { label: '告警异常', tagType: 'warning', dotColor: '#F59E0B' };
    case 'disabled':
    default:
      return { label: '已停用', tagType: 'default', dotColor: '#94A3B8' };
  }
}

export function getTaskStatusMeta(status?: string): StatusMeta {
  switch (status) {
    case 'succeeded':
      return { label: '已完成', tagType: 'success' };
    case 'running':
      return { label: '运行中', tagType: 'info' };
    case 'waiting_approval':
      return { label: '待人工确认', tagType: 'warning' };
    case 'waiting_input':
      return { label: '待补充信息', tagType: 'warning' };
    case 'canceling':
      return { label: '取消中', tagType: 'warning' };
    case 'canceled':
      return { label: '已取消', tagType: 'default' };
    case 'failed':
      return { label: '执行失败', tagType: 'error' };
    case 'rejected':
      return { label: '已拦截拒绝', tagType: 'error' };
    case 'unknown':
    default:
      return { label: '结果待核对', tagType: 'warning' };
  }
}

export function getGatewayChannelStatusMeta(status?: string): StatusMeta {
  switch (status) {
    case 'online':
      return { label: '在线', tagType: 'success' };
    case 'degraded':
      return { label: '延迟波动', tagType: 'warning' };
    case 'offline':
      return { label: '离线断开', tagType: 'error' };
    default:
      return { label: '状态未知', tagType: 'default' };
  }
}

export function formatTimeShort(raw?: string): string {
  if (!raw) return '--';
  const trimmed = raw.trim();
  if (!trimmed) return '--';
  const match = /(?:^|\s|T)(\d{2}:\d{2}(?::\d{2})?)/.exec(trimmed);
  if (match && match[1]) return match[1];
  return trimmed;
}

export function parsePercentValue(raw?: string, fallback = 0): number {
  if (!raw) return fallback;
  const pctMatch = /(\d+(?:\.\d+)?)\s*%/.exec(raw);
  if (pctMatch && pctMatch[1]) {
    return Math.min(100, Math.max(0, Math.round(Number(pctMatch[1]))));
  }
  const ratioMatch = /(\d+(?:\.\d+)?)\s*(?:[A-Za-z]*)\s*\/\s*(\d+(?:\.\d+)?)/.exec(raw);
  if (ratioMatch && ratioMatch[1] && ratioMatch[2]) {
    const num = Number(ratioMatch[1]);
    const den = Number(ratioMatch[2]);
    if (Number.isFinite(num) && Number.isFinite(den) && den > 0) {
      return Math.min(100, Math.max(0, Math.round((num / den) * 100)));
    }
  }
  return fallback;
}

export interface TrendPoint {
  x: number;
  y: number;
  date: string;
}

export function parseTrendPathPoints(trendPath: string, trendDates: string[] = []): TrendPoint[] {
  if (!trendPath || !trendPath.trim()) return [];
  const matches = [...trendPath.matchAll(/[ML]\s*(-?\d+(?:\.\d+)?)[\s,]+(-?\d+(?:\.\d+)?)/gi)];
  return matches.map((m, idx) => ({
    x: Number(m[1]),
    y: Number(m[2]),
    date: trendDates[idx] ?? `节点 ${idx + 1}`
  }));
}

export interface ParsedSkillMarkdown {
  name: string;
  code: string;
  version: string;
  description: string;
  requiredToolTagsStr: string;
  promptTemplate: string;
}

export function parseSkillMarkdown(rawMarkdown: string): ParsedSkillMarkdown {
  const normalized = rawMarkdown.replace(/\r\n/g, '\n').trim();
  const fmMatch = /^---\n([\s\S]*?)\n---\n?([\s\S]*)$/.exec(normalized);

  const meta: Record<string, string> = {};
  let body = normalized;
  if (fmMatch && fmMatch[1] !== undefined) {
    body = (fmMatch[2] || '').trim();
    for (const line of fmMatch[1].split('\n')) {
      const sepIdx = line.indexOf(':');
      if (sepIdx > 0) {
        const k = line.slice(0, sepIdx).trim().toLowerCase();
        const v = line.slice(sepIdx + 1).trim().replace(/^['"]|['"]$/g, '');
        meta[k] = v;
      }
    }
  }

  const headingMatch = /^#\s+(.+)$/m.exec(body);
  const title = meta['name'] || meta['title'] || (headingMatch?.[1]?.trim() || '自定义导入技能');
  const codeBase = meta['code'] || meta['slug'] || title
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, '_')
    .replace(/^_+|_+$/g, '');
  const code = codeBase || `custom_skill_${Date.now().toString(36)}`;
  const version = meta['version'] || 'v1.0.0';
  const description = meta['description'] || meta['desc'] || `从 Markdown 文档导入：${title}`;
  const rawTools = meta['tools'] || meta['requiredtooltags'] || meta['required_tools'] || '';
  const requiredToolTagsStr = rawTools.replace(/^\[|\]$/g, '').split(',').map(s => s.trim().replace(/^['"]|['"]$/g, '')).filter(Boolean).join(', ');

  return {
    name: title,
    code,
    version,
    description,
    requiredToolTagsStr,
    promptTemplate: body || normalized
  };
}

export function paginateItems<T>(items: T[], page: number, pageSize: number): T[] {
  const safeSize = Math.max(1, pageSize);
  const safePage = Math.max(1, page);
  const offset = (safePage - 1) * safeSize;
  return items.slice(offset, offset + safeSize);
}

// Pure TypeScript stateful REST API mock router for Direction 2.
import {
  buildCanonicalPrincipalKey,
  type PlatformType, type DigitalEmployee, type EmployeeChannelBinding,
  type CapabilityPackageBinding, type EmployeeTrigger, type CrossGroupSharingRule,
  type HumanConfirmationRule, type ModelItem, type WorkspaceSkill, type McpServerEndpoint,
  type McpToolSchema, type TaskRecord, type DeliveryRecord, type EmployeeConversationSession,
  type ActivityEvent, type UserEmployeePermissionSummary, type AsyncOperationReceipt,
  type PrincipalRef, type DirectorySyncJob, type ChatExecutionStatus, type MemberProviderType,
  type ExecutionEngine, type UserProfile, type WorkspaceInfo
} from '../types';
import {
  getExecutionEngineMeta,
  isModelCompatibleWithEngine
} from '../utils/presentation';
import {
  createInitialMockDatabase, buildMainWorkspaceState, platformCandidateGroups, platformCandidateGroupItems, feishuRegexTemplates,
  type MockServerDatabase, type WorkspaceState, type MockAuthAccount
} from './seed-data';

export interface MockHttpResponse {
  status: number;
  body: unknown;
  headers?: Record<string, string> | undefined;
}

const FORBIDDEN_CHANNEL_WRITE_KEYS = [
  'status', 'statusText', 'credentialStatus', 'connectionState', 'permissionReady',
  'connectionMethod', 'appId', 'corpId', 'agentId', 'botId', 'botSecretMasked',
  'appKey', 'appSecretMasked', 'adminConsoleUrl', 'errorDetail', 'lastVerifiedAt',
  'businessCapabilities', 'boundGroups'
];
const FORBIDDEN_CAP_WRITE_KEYS = ['supportsOrdinaryGroup', 'supportsKeywords', 'supportsRecentContext'];
const FORBIDDEN_SHARING_WRITE_KEYS = ['syncStatus', 'authErrorDetail', 'savedAt'];
const FORBIDDEN_TRIGGER_WRITE_KEYS = ['executionLogs', 'secretMasked', 'keyId', 'webhookEndpoint', 'nextExecutionTimes'];
const FORBIDDEN_EMP_WRITE_KEYS = ['id', 'code', 'version', 'etag', 'status', 'modelName', 'stats'];

interface PendingTimedTransition {
  kind:
    | 'wecom_verify'
    | 'qr_session'
    | 'work_item_note'
    | 'directory_sync'
    | 'wi_cancel'
    | 'wi_verify'
    | 'task_cancel'
    | 'task_verify'
    | 'task_retry'
    | 'approval_task_resume'
    | 'chat_execution'
    | 'trigger_run';
  wsId: string;
  targetId: string;
  createdAtMs: number;
  opId?: string | undefined;
  extra?: string | undefined;
}

function normalizePrincipalRef(
  raw: unknown,
  fallback?: { targetType?: 'user' | 'department' | undefined; channel?: MemberProviderType | undefined; connectionId?: string | undefined; displayName?: string | undefined }
): PrincipalRef | null {
  if (!raw || typeof raw !== 'object') return null;
  const obj = raw as Record<string, unknown>;
  const targetType = obj['targetType'] === 'department' ? 'department' : (fallback?.targetType || 'user');
  const channel = (typeof obj['channel'] === 'string' && obj['channel'] ? obj['channel'] : fallback?.channel || 'web') as MemberProviderType;
  const connectionId = (typeof obj['connectionId'] === 'string' && obj['connectionId'].trim())
    ? obj['connectionId'].trim()
    : (fallback?.connectionId || (channel === 'web' ? 'ws-web-directory' : ''));
  const targetId = (typeof obj['targetId'] === 'string' && obj['targetId'].trim())
    ? obj['targetId'].trim()
    : (typeof obj['id'] === 'string' && obj['id'].trim() ? obj['id'].trim() : '');
  if (!targetId || !connectionId) return null;
  const displayName = typeof obj['displayName'] === 'string' && obj['displayName'].trim() ? obj['displayName'].trim() : fallback?.displayName;
  const ref: PrincipalRef = {
    targetType,
    channel,
    connectionId,
    targetId,
    displayName
  };
  ref.canonicalKey = buildCanonicalPrincipalKey(ref);
  return ref;
}

function collectConnectionDirectoryUsers(ws: WorkspaceState, connectionId?: string) {
  if (connectionId && ws.directorySnapshots[connectionId]) {
    return [...ws.directorySnapshots[connectionId]!.users, ...ws.directoryUsers];
  }
  const snapUsers = Object.values(ws.directorySnapshots).flatMap(s => s.users);
  return [...snapUsers, ...ws.directoryUsers];
}

function collectConnectionDirectoryDepts(ws: WorkspaceState, connectionId?: string) {
  if (connectionId && ws.directorySnapshots[connectionId]) {
    return [...ws.directorySnapshots[connectionId]!.departments, ...ws.directoryDepartments];
  }
  const snapDepts = Object.values(ws.directorySnapshots).flatMap(s => s.departments);
  return [...snapDepts, ...ws.directoryDepartments];
}

function resolveUserByPrincipalOrId(
  ws: WorkspaceState,
  target: { channel?: string | undefined; connectionId?: string | undefined; targetId?: string | undefined; id?: string | undefined; canonicalKey?: string | undefined }
) {
  const pool = collectConnectionDirectoryUsers(ws, target.connectionId);
  const uId = target.targetId || target.id;
  return pool.find(u => {
    if (target.canonicalKey && u.principal?.canonicalKey && u.principal.canonicalKey === target.canonicalKey) {
      return true;
    }
    const idMatch = u.id === uId || u.refId === uId;
    if (!idMatch) return false;
    if (target.channel && u.platform !== target.channel) return false;
    if (target.connectionId) {
      return (u.connectionId || (u.platform === 'web' ? 'ws-web-directory' : '')) === target.connectionId;
    }
    return true;
  });
}

function getIdempotencyKey(
  body: Record<string, unknown>,
  headers?: Record<string, string | string[] | undefined>
): string | null {
  const hdr = headers?.['idempotency-key'];
  const raw = Array.isArray(hdr) ? hdr[0] : hdr;
  const fromHdr = typeof raw === 'string' ? raw.trim() : '';
  const fromBody = typeof body['idempotencyKey'] === 'string' ? body['idempotencyKey'].trim() : '';
  return fromHdr || fromBody || null;
}

function canonicalSerializeForIdempotency(val: unknown): string {
  if (val === null || val === undefined) return 'null';
  if (typeof val !== 'object') return JSON.stringify(val);
  if (Array.isArray(val)) return `[${val.map(v => canonicalSerializeForIdempotency(v)).join(',')}]`;
  const obj = val as Record<string, unknown>;
  const keys = Object.keys(obj)
    .filter(k => obj[k] !== undefined && k !== 'idempotencyKey' && k !== 'clientMessageId')
    .sort();
  return `{${keys.map(k => `${JSON.stringify(k)}:${canonicalSerializeForIdempotency(obj[k])}`).join(',')}}`;
}

function computeIdempotencyRequestSignature(method: string, subPath: string, body: Record<string, unknown>): string {
  return `${method.toUpperCase()}::${subPath}::${canonicalSerializeForIdempotency(body)}`;
}

function checkExpectedVersion(currentVersion: number, body: Record<string, unknown>): MockHttpResponse | null {
  if ('expectedVersion' in body && body['expectedVersion'] !== undefined && typeof body['expectedVersion'] !== 'number') {
    return errResp(400, 'INVALID_EXPECTED_VERSION', '配置版本参数格式不正确');
  }
  const expected = typeof body['expectedVersion'] === 'number' ? body['expectedVersion'] : undefined;
  if (expected !== undefined && expected !== currentVersion) {
    return errResp(
      409,
      'CONFLICT_STALE_VERSION',
      '当前配置已被更新，请先刷新获取最新配置后再试'
    );
  }
  return null;
}

function checkVersionConflict(
  currentVersion: number,
  _entityPrefix: string,
  body: Record<string, unknown>,
  _headers?: Record<string, string | string[] | undefined>
): MockHttpResponse | null {
  return checkExpectedVersion(currentVersion, body);
}

function nowTimeLabel(): string {
  return new Date().toLocaleTimeString('zh-CN', { hour: '2-digit', minute: '2-digit', hour12: false });
}

function nowFullTimestamp(): string {
  const d = new Date();
  const pad = (n: number) => String(n).padStart(2, '0');
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}`;
}

function clone<T>(v: T): T {
  return JSON.parse(JSON.stringify(v)) as T;
}

function errResp(status: number, code: string, message: string): MockHttpResponse {
  return { status, body: { error: { code, message } } };
}

function okResp(body: unknown, status = 200, headers?: Record<string, string>): MockHttpResponse {
  return headers ? { status, body, headers } : { status, body };
}

function okEmpResp(emp: DigitalEmployee, status = 200): MockHttpResponse {
  const ver = emp.version || 1;
  const etag = `W/"emp-${emp.id}-v${ver}"`;
  emp.version = ver;
  emp.etag = etag;
  return okResp(clone(emp), status, { ETag: etag });
}

function bumpEmployeeVersion(emp: DigitalEmployee): string {
  emp.version = (emp.version || 1) + 1;
  emp.etag = `W/"emp-${emp.id}-v${emp.version}"`;
  return emp.etag;
}

function checkEmployeeConcurrency(
  emp: DigitalEmployee,
  body: Record<string, unknown>,
  _headers?: Record<string, string | string[] | undefined>
): MockHttpResponse | null {
  return checkExpectedVersion(emp.version || 1, body);
}

function parseExecutionEngineParam(raw: unknown, fallback: ExecutionEngine = 'pi'): ExecutionEngine {
  return raw === 'pi' || raw === 'codex' || raw === 'claudecode' ? raw : fallback;
}

function validateModelEngineCompatibility(model: ModelItem, engine: ExecutionEngine): MockHttpResponse | null {
  if (isModelCompatibleWithEngine(model.protocol, engine)) return null;
  const engineMeta = getExecutionEngineMeta(engine);
  return errResp(
    400,
    'INCOMPATIBLE_ENGINE_MODEL',
    `执行引擎「${engineMeta.shortLabel}」仅支持 ${engineMeta.requiredProtocolLabel} 协议，所选模型「${model.name}」(${model.protocol}) 不兼容`
  );
}

function resolveEmployeeEngineAndModel(
  ws: WorkspaceState,
  emp: DigitalEmployee,
  body: Record<string, unknown>
): { ok: true; engine: ExecutionEngine; model: ModelItem } | { ok: false; error: MockHttpResponse } {
  const engine = parseExecutionEngineParam(body['executionEngine'], emp.executionEngine || 'pi');
  const modelId = typeof body['modelId'] === 'string' ? body['modelId'] : emp.modelId;
  const model = ws.models.find(m => m.id === modelId);
  if (!model) {
    return { ok: false, error: errResp(400, 'MODEL_NOT_FOUND', `所选基座模型 ${modelId} 不存在`) };
  }
  const compatErr = validateModelEngineCompatibility(model, engine);
  if (compatErr) return { ok: false, error: compatErr };
  return { ok: true, engine, model };
}

function parseCronField(field: string, min: number, max: number, normalizeDow = false): Set<number> | null {
  const result = new Set<number>();
  const parts = field.split(',');
  for (const part of parts) {
    const trimmed = part.trim();
    if (!trimmed) return null;
    const stepSplit = trimmed.split('/');
    if (stepSplit.length > 2) return null;
    const rangePart = stepSplit[0]!;
    const step = stepSplit[1] !== undefined ? Number(stepSplit[1]) : 1;
    if (!Number.isInteger(step) || step <= 0) return null;

    let start = min;
    let end = max;
    if (rangePart !== '*') {
      const bounds = rangePart.split('-');
      if (bounds.length > 2) return null;
      start = Number(bounds[0]);
      end = bounds.length === 2 ? Number(bounds[1]) : start;
      if (!Number.isInteger(start) || !Number.isInteger(end)) return null;
      if (normalizeDow && start === 7 && bounds.length === 1) start = 0;
      if (normalizeDow && end === 7 && start === 7) {
        start = 0;
        end = 0;
      }
      if (start < min || end > max + (normalizeDow ? 1 : 0) || start > end) return null;
    }
    for (let v = start; v <= end; v += step) {
      result.add(normalizeDow && v === 7 ? 0 : v);
    }
  }
  return result.size > 0 ? result : null;
}

function computeNextCronRuns(cronExpr: string, timezone: string, count = 5, fromDate = new Date()): { ok: true; previews: string[] } | { ok: false; message: string } {
  const fields = cronExpr.trim().split(/\s+/);
  if (fields.length !== 5) {
    return { ok: false, message: `Cron 表达式必须包含 5 个字段（分 时 日 月 周），当前收到 ${fields.length} 个` };
  }
  const mins = parseCronField(fields[0]!, 0, 59);
  const hours = parseCronField(fields[1]!, 0, 23);
  const doms = parseCronField(fields[2]!, 1, 31);
  const months = parseCronField(fields[3]!, 1, 12);
  const dows = parseCronField(fields[4]!, 0, 6, true);
  if (!mins || !hours || !doms || !months || !dows) {
    return { ok: false, message: `Cron 表达式语法或取值范围不合法：${cronExpr}` };
  }

  let fmt: Intl.DateTimeFormat;
  try {
    fmt = new Intl.DateTimeFormat('en-US', {
      timeZone: timezone || 'Asia/Shanghai',
      year: 'numeric',
      month: '2-digit',
      day: '2-digit',
      hour: '2-digit',
      minute: '2-digit',
      weekday: 'short',
      hour12: false
    });
  } catch {
    return { ok: false, message: `不支持的时区标识：${timezone}` };
  }

  const dowMap: Record<string, number> = { Sun: 0, Mon: 1, Tue: 2, Wed: 3, Thu: 4, Fri: 5, Sat: 6 };
  const domIsWild = fields[2]!.trim() === '*';
  const dowIsWild = fields[4]!.trim() === '*';

  // Start checking from the next whole minute
  let ts = Math.ceil((fromDate.getTime() + 1000) / 60000) * 60000;
  const maxIterations = 60 * 24 * 366;
  const previews: string[] = [];

  for (let i = 0; i < maxIterations && previews.length < count; i++) {
    const parts = fmt.formatToParts(new Date(ts));
    const get = (t: string) => parts.find(p => p.type === t)?.value || '';
    const year = get('year');
    const monthStr = get('month');
    const dayStr = get('day');
    let hourNum = Number(get('hour'));
    if (hourNum === 24) hourNum = 0;
    const minNum = Number(get('minute'));
    const monthNum = Number(monthStr);
    const domNum = Number(dayStr);
    const dowNum = dowMap[get('weekday')] ?? 0;

    if (!months.has(monthNum)) {
      ts += 60 * 60 * 1000; // skip by hour quickly if month mismatch
      continue;
    }
    const domMatch = doms.has(domNum);
    const dowMatch = dows.has(dowNum);
    const dayOk = (domIsWild && dowIsWild)
      ? true
      : domIsWild
        ? dowMatch
        : dowIsWild
          ? domMatch
          : (domMatch || dowMatch);
    if (!dayOk) {
      ts += 60 * 60 * 1000;
      continue;
    }
    if (!hours.has(hourNum)) {
      ts += (60 - minNum) * 60 * 1000;
      continue;
    }
    if (mins.has(minNum)) {
      const hPad = String(hourNum).padStart(2, '0');
      const mPad = String(minNum).padStart(2, '0');
      previews.push(`${year}-${monthStr}-${dayStr} ${hPad}:${mPad}:00 (${timezone || 'Asia/Shanghai'})`);
    }
    ts += 60 * 1000;
  }

  if (previews.length === 0) {
    return { ok: false, message: '未来 366 天内未匹配到有效触发时刻，请检查日期组合（如 2 月 30 日）' };
  }
  return { ok: true, previews };
}

function generateQrSvgDataUrl(payload: string): string {
  const size = 21;
  const grid: boolean[][] = Array.from({ length: size }, () => Array<boolean>(size).fill(false));
  const setFinder = (ox: number, oy: number) => {
    for (let y = 0; y < 7; y++) {
      for (let x = 0; x < 7; x++) {
        const border = x === 0 || x === 6 || y === 0 || y === 6;
        const inner = x >= 2 && x <= 4 && y >= 2 && y <= 4;
        grid[oy + y]![ox + x] = border || inner;
      }
    }
  };
  setFinder(0, 0);
  setFinder(size - 7, 0);
  setFinder(0, size - 7);
  for (let i = 8; i < size - 8; i++) {
    grid[6]![i] = i % 2 === 0;
    grid[i]![6] = i % 2 === 0;
  }
  let seed = 2166136261;
  for (let i = 0; i < payload.length; i++) {
    seed ^= payload.charCodeAt(i);
    seed = Math.imul(seed, 16777619) >>> 0;
  }
  for (let y = 0; y < size; y++) {
    for (let x = 0; x < size; x++) {
      const inFinder = (x < 8 && y < 8) || (x >= size - 8 && y < 8) || (x < 8 && y >= size - 8);
      if (inFinder || x === 6 || y === 6) continue;
      seed = (Math.imul(seed, 1664525) + 1013904223) >>> 0;
      grid[y]![x] = ((seed >>> 24) & 1) === 1;
    }
  }
  let rects = '';
  for (let y = 0; y < size; y++) {
    for (let x = 0; x < size; x++) {
      if (grid[y]![x]) {
        rects += `<rect x="${x + 2}" y="${y + 2}" width="1" height="1" fill="#0F172A"/>`;
      }
    }
  }
  const svg = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 ${size + 4} ${size + 4}" shape-rendering="crispEdges"><rect width="${size + 4}" height="${size + 4}" fill="#FFFFFF"/>${rects}</svg>`;
  return `data:image/svg+xml;charset=utf-8,${encodeURIComponent(svg)}`;
}

function decodeBase64ToBytes(b64: string): Uint8Array {
  const clean = b64.includes(',') ? b64.split(',').pop() || '' : b64;
  const bin = atob(clean.replace(/\s+/g, ''));
  const bytes = new Uint8Array(bin.length);
  for (let i = 0; i < bin.length; i++) {
    bytes[i] = bin.charCodeAt(i);
  }
  return bytes;
}

async function extractZipTextEntries(bytes: Uint8Array): Promise<{ names: string[]; texts: Record<string, string> }> {
  const view = new DataView(bytes.buffer, bytes.byteOffset, bytes.byteLength);
  const decoder = new TextDecoder('utf-8', { fatal: false });
  const names: string[] = [];
  const texts: Record<string, string> = {};
  let offset = 0;
  let foundHeader = false;

  while (offset + 30 <= bytes.length) {
    const sig = view.getUint32(offset, true);
    if (sig !== 0x04034b50) break;
    foundHeader = true;
    const method = view.getUint16(offset + 8, true);
    const compSize = view.getUint32(offset + 18, true);
    const nameLen = view.getUint16(offset + 26, true);
    const extraLen = view.getUint16(offset + 28, true);
    const nameStart = offset + 30;
    const dataStart = nameStart + nameLen + extraLen;
    if (dataStart > bytes.length) break;
    const entryName = decoder.decode(bytes.subarray(nameStart, nameStart + nameLen));
    const dataEnd = compSize > 0 && dataStart + compSize <= bytes.length ? dataStart + compSize : dataStart;
    if (!entryName.endsWith('/')) {
      names.push(entryName);
      const compressed = bytes.subarray(dataStart, dataEnd);
      if (/\.(md|txt|json|ya?ml)$/i.test(entryName) && compressed.length < 262144) {
        if (method === 0) {
          texts[entryName] = decoder.decode(compressed);
        } else if (method === 8 && typeof DecompressionStream === 'function') {
          try {
            const copy = new Uint8Array(compressed);
            const ds = new DecompressionStream('deflate-raw');
            const stream = new Blob([copy]).stream().pipeThrough(ds);
            const buf = await new Response(stream).arrayBuffer();
            texts[entryName] = decoder.decode(new Uint8Array(buf));
          } catch {
            // fallback skip malformed deflate entry
          }
        }
      }
    }
    if (compSize === 0 && method === 8) break;
    offset = dataEnd;
  }

  if (!foundHeader) {
    throw new Error('NOT_A_ZIP');
  }
  return { names, texts };
}

function parseMarkdownSkillContent(raw: string, fallbackName: string): {
  name?: string;
  code?: string;
  version?: string;
  description?: string;
  tools?: string[];
  body: string;
} {
  const fmMatch = /^---\r?\n([\s\S]*?)\r?\n---\r?\n?([\s\S]*)$/.exec(raw.trim());
  const meta: Record<string, string> = {};
  let body = raw.trim();
  if (fmMatch && fmMatch[1]) {
    body = (fmMatch[2] || '').trim();
    for (const line of fmMatch[1].split(/\r?\n/)) {
      const idx = line.indexOf(':');
      if (idx > 0) {
        const k = line.slice(0, idx).trim().toLowerCase();
        const v = line.slice(idx + 1).trim().replace(/^['"]|['"]$/g, '');
        meta[k] = v;
      }
    }
  } else {
    const h1 = /^#\s+(.+)$/m.exec(body);
    if (h1 && h1[1]) meta['name'] = h1[1].trim();
  }
  const toolsRaw = meta['tools'] || '';
  const tools = toolsRaw
    ? toolsRaw.replace(/^\[|\]$/g, '').split(/[,，]/).map(s => s.trim()).filter(Boolean)
    : [];
  const res: {
    name?: string;
    code?: string;
    version?: string;
    description?: string;
    tools?: string[];
    body: string;
  } = { body: body || `# ${meta['name'] || fallbackName}\n请按照技能说明严格执行。` };
  if (meta['name']) res.name = meta['name'];
  if (meta['code']) res.code = meta['code'];
  if (meta['version']) res.version = meta['version'];
  if (meta['description']) res.description = meta['description'];
  if (tools.length > 0) res.tools = tools;
  return res;
}

function buildServerChannelBinding(platform: PlatformType, method: 'qrcode' | 'existing_app', suggestedId?: string, connectionId?: string): EmployeeChannelBinding {
  const names = { feishu: '飞书机器人 (Lark)', wecom: '企业微信智能机器人', dingtalk: '钉钉机器人' };
  const connId = connectionId || `conn-${platform}-${Date.now().toString(36)}`;
  const candidateItems = platformCandidateGroupItems[platform] || [];
  return {
    platform,
    platformName: names[platform],
    connectionId: connId,
    status: 'connected',
    statusText: '凭证已保存 · 连接正常',
    credentialStatus: 'saved',
    connectionState: 'connected',
    permissionReady: platform !== 'dingtalk',
    connectionMethod: method,
    appId: platform === 'feishu' ? (suggestedId || 'cli_a891024feishu') : undefined,
    botId: platform === 'wecom' ? (suggestedId || 'bot_wecom_sec_002') : undefined,
    botSecretMasked: platform === 'wecom' ? 'bsec_••••••••812e' : undefined,
    appKey: platform === 'dingtalk' ? (suggestedId || 'ding_stream_client_99') : undefined,
    appSecretMasked: platform !== 'wecom' ? 'sec_••••••••8a9f' : undefined,
    adminConsoleUrl: platform === 'dingtalk' ? 'https://open-dev.dingtalk.com/fe/app#/corp/app' : undefined,
    lastVerifiedAt: nowTimeLabel(),
    businessCapabilities: platform === 'feishu' ? [
      { id: 'cap-chat', name: '聊天与群消息', description: '单聊消息收发、群聊消息监听、群卡片投递', platformAuthStatus: 'authorized', resourceAccessStatus: 'ready', statusDetail: '连接正常，已获 im:message、im:chat 读写权限' },
      { id: 'cap-contact', name: '组织通讯录', description: '读取部门架构、检索员工信息与组织关系', platformAuthStatus: 'partial', resourceAccessStatus: 'ready', statusDetail: '已获部分部门权限' },
      { id: 'cap-docs', name: '云文档与多维表格', description: '自动化读取排障文档、写入缺陷工单与表格', platformAuthStatus: 'authorized', resourceAccessStatus: 'pending_collaborator', statusDetail: '开放平台权限已获批；注意：操作具体文档仍需在飞书文档中添加机器人为协作者' },
      { id: 'cap-meeting', name: '视频会议与日程', description: '创建紧急排障会议、日历预约与会议纪要转录', platformAuthStatus: 'unauthorized', resourceAccessStatus: 'not_configured', statusDetail: '尚未申请 vc:room 权限' }
    ] : undefined,
    usageScope: {
      mode: 'all',
      whitelistMembers: [],
      whitelistDepartments: [],
      blacklistMembers: [],
      allowedGroupIds: candidateItems.map(g => g.groupId)
    },
    capabilities: {
      supportsOrdinaryGroup: platform === 'feishu',
      supportsKeywords: platform === 'feishu',
      supportsRecentContext: platform !== 'dingtalk',
      ordinaryGroupEnabled: platform === 'feishu',
      keywordTriggersEnabled: platform === 'feishu',
      keywords: platform === 'feishu' ? ['/review', '/fix'] : [],
      regexMatchEnabled: false,
      regexPattern: '/(?:review|pr|mr)\\s*#?\\d+/i',
      feishuRules: platform === 'feishu' ? [
        { id: 'fs-r1', type: 'keyword', name: '快捷指令匹配', pattern: '/review, /fix, /diagnose', enabled: true, description: '日常指令' },
        { id: 'fs-r2', type: 'regex', name: 'PR/MR合并请求正则', pattern: '/(?:review|pr|mr)\\s*#?\\d+/i', enabled: true, description: '匹配 PR #xxx' }
      ] : undefined,
      recentContextEnabled: platform !== 'dingtalk',
      recentContextWindow: platform === 'feishu' ? 20 : 10
    },
    sharingRules: [],
    boundGroups: candidateItems.slice(0, 2).map((g, i) => ({
      id: g.groupId,
      name: g.name,
      memberCount: g.memberCount || (20 + i * 15),
      activeAt: '刚刚'
    }))
  };
}

function isMaskedDisplayValue(val: string): boolean {
  return val.includes('•') || /\*{3,}/.test(val);
}

function computeModelConfigBinding(
  wsId: string,
  profileId: string | null,
  protocol: string,
  endpoint: string,
  modelCode: string,
  apiKey: string
): string {
  return JSON.stringify([wsId, profileId, protocol.trim(), endpoint.trim(), modelCode.trim(), apiKey]);
}

function computeChannelCredDigest(
  wsId: string,
  empId: string,
  platform: PlatformType,
  identifier: string,
  secret: string
): string {
  return JSON.stringify([wsId, empId, platform, identifier.trim(), secret]);
}

export class D2MockBackend {
  private db: MockServerDatabase = createInitialMockDatabase();
  private timers: PendingTimedTransition[] = [];
  private qrSessionMeta: Record<string, { wsId: string; empId: string; createdAtMs: number }> = {};
  private latestQrSessionByKey: Record<string, string> = {};
  private modelReceipts: Record<string, { wsId: string; profileId: string | null; binding: string; expiresAt: number }> = {};
  private mcpReceipts: Record<string, {
    wsId: string;
    id: string | null;
    record: {
      name: string;
      endpoint: string;
      protocol: McpServerEndpoint['protocol'];
      authType: McpServerEndpoint['authType'];
      apiKeyName?: string | undefined;
      customHeaders?: McpServerEndpoint['customHeaders'] | undefined;
      tools: McpToolSchema[];
    };
    secret: string;
    expiresAt: number;
  }> = {};
  private channelTestReceipts: Record<string, { wsId: string; empId: string; platform: PlatformType; digest: string; expiresAt: number }> = {};
  private draftTriggerSecrets: Record<string, {
    empId: string;
    triggerId: string;
    keyId: string;
    secretMasked: string;
    secret: string;
    webhookEndpoint: string;
    draftSecretToken: string;
  }> = {};
  private operations: Record<string, AsyncOperationReceipt> = {};
  private idempotencyCache: Record<string, { response: MockHttpResponse; requestSignature: string }> = {};
  private operationsByIdempotencyKey: Record<string, string> = {};
  private authSessions: Record<string, { token: string; userId: string; createdAtMs: number; expiresAtMs: number }> = {};
  private emailVerificationCodes: Record<string, {
    email: string;
    purpose: 'login' | 'register' | 'reset_password';
    code: string;
    createdAtMs: number;
    expiresAtMs: number;
    resendAvailableAtMs: number;
    attempts: number;
    consumed: boolean;
  }> = {};
  private oauthStates: Record<string, { state: string; redirectUri: string; createdAtMs: number; expiresAtMs: number; consumed: boolean }> = {};

  private isValidEmailFormat(email: string): boolean {
    return /^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(email);
  }

  private normalizeEmail(email: string): string {
    return email.trim().toLowerCase();
  }

  private buildAvatarText(name: string, email: string): string {
    const clean = name.trim();
    if (clean.length >= 2 && /^[A-Za-z0-9\s]+$/.test(clean)) {
      const parts = clean.split(/\s+/).filter(Boolean);
      if (parts.length >= 2) {
        return `${parts[0]![0] || ''}${parts[1]![0] || ''}`.toUpperCase();
      }
      return clean.slice(0, 2).toUpperCase();
    }
    if (clean.length >= 1) {
      return clean.slice(0, 1).toUpperCase();
    }
    return (email.trim()[0] || 'U').toUpperCase();
  }

  private extractHeaderValue(headers: Record<string, string | string[] | undefined> | undefined, key: string): string {
    if (!headers) return '';
    const foundKey = Object.keys(headers).find(k => k.toLowerCase() === key.toLowerCase());
    if (!foundKey) return '';
    const raw = headers[foundKey];
    return (Array.isArray(raw) ? raw[0] : raw) || '';
  }

  private extractAuthToken(headers?: Record<string, string | string[] | undefined>): string {
    const authHeader = this.extractHeaderValue(headers, 'authorization').trim();
    if (/^Bearer\s+(.+)$/i.test(authHeader)) {
      return authHeader.replace(/^Bearer\s+/i, '').trim();
    }
    const cookieHeader = this.extractHeaderValue(headers, 'cookie');
    if (cookieHeader) {
      const pairs = cookieHeader.split(';');
      for (const pair of pairs) {
        const eqIdx = pair.indexOf('=');
        if (eqIdx <= 0) continue;
        const k = pair.slice(0, eqIdx).trim();
        const v = pair.slice(eqIdx + 1).trim();
        if (k === 'kiko_session' && v) {
          return decodeURIComponent(v);
        }
      }
    }
    return '';
  }

  private resolveAuthenticatedAccount(headers?: Record<string, string | string[] | undefined>): MockAuthAccount | null {
    const token = this.extractAuthToken(headers);
    if (!token) return null;
    const sess = this.authSessions[token];
    if (!sess) return null;
    if (Date.now() > sess.expiresAtMs) {
      delete this.authSessions[token];
      return null;
    }
    return this.db.authUsers.find(u => u.id === sess.userId) || null;
  }

  private toUserProfile(account: MockAuthAccount): UserProfile {
    return {
      id: account.id,
      name: account.name,
      email: account.email,
      role: account.role,
      avatarText: account.avatarText,
      authProvider: account.authProvider,
      emailVerified: account.emailVerified
    };
  }

  private getAccountWorkspaces(account: MockAuthAccount): WorkspaceInfo[] {
    const list: WorkspaceInfo[] = [];
    for (const wsId of account.workspaceIds) {
      const ws = this.db.workspaces[wsId];
      if (ws) list.push(clone(ws.info));
    }
    return list;
  }

  private ensureWorkspacesForAccount(account: MockAuthAccount): void {
    const normalized = this.normalizeEmail(account.email);
    const today = new Date().toISOString().slice(0, 10);

    // Accept any pending workspace email invitation matching this user's email
    for (const [wsId, ws] of Object.entries(this.db.workspaces)) {
      const pendingInvites = ws.emailInvitations.filter(
        inv => inv.status === 'pending' && this.normalizeEmail(inv.email) === normalized
      );
      if (pendingInvites.length > 0) {
        for (const inv of pendingInvites) {
          inv.status = 'activated';
        }
        if (!account.workspaceIds.includes(wsId)) {
          account.workspaceIds.push(wsId);
        }
        if (!ws.members.some(m => this.normalizeEmail(m.email) === normalized)) {
          ws.members.push({
            id: account.id,
            name: account.name,
            email: account.email,
            role: 'member',
            status: 'active',
            joinedAt: today
          });
        }
      }
    }

    if (account.workspaceIds.length === 0) {
      const wsId = `ws-${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 6)}`;
      const wsInfo: WorkspaceInfo = {
        id: wsId,
        name: `${account.name} 的工作区`,
        role: '所有者 (Owner)',
        desc: '默认团队协同空间'
      };
      const wsState = buildMainWorkspaceState(wsInfo);
      wsState.members.unshift({
        id: account.id,
        name: account.name,
        email: account.email,
        role: 'owner',
        status: 'active',
        joinedAt: today
      });
      this.db.workspaces[wsId] = wsState;
      account.workspaceIds.push(wsId);
    }
  }

  private createAuthSessionResponse(account: MockAuthAccount, status = 200): MockHttpResponse {
    this.ensureWorkspacesForAccount(account);
    const token = `tok_${Date.now().toString(36)}_${Math.random().toString(36).slice(2, 10)}`;
    const now = Date.now();
    this.authSessions[token] = {
      token,
      userId: account.id,
      createdAtMs: now,
      expiresAtMs: now + 7 * 24 * 60 * 60 * 1000
    };
    const userProfile = this.toUserProfile(account);
    this.db.currentUser = clone(userProfile);
    const workspaces = this.getAccountWorkspaces(account);
    return okResp(
      {
        token,
        user: clone(userProfile),
        workspaces
      },
      status,
      {
        'Set-Cookie': `kiko_session=${encodeURIComponent(token)}; Path=/; HttpOnly; SameSite=Lax`
      }
    );
  }

  private resolveIdempotencyHitOrConflict(
    wsId: string,
    method: string,
    subPath: string,
    idemKey: string | null,
    body: Record<string, unknown>
  ): MockHttpResponse | null {
    if (!idemKey) return null;
    const cached = this.idempotencyCache[`${wsId}::${method}::${subPath}::${idemKey}`];
    if (!cached) return null;
    const incomingSignature = computeIdempotencyRequestSignature(method, subPath, body);
    if (cached.requestSignature !== incomingSignature) {
      return errResp(
        409,
        'IDEMPOTENCY_CONFLICT',
        `重复提交冲突 (${idemKey})：同一操作标识不能用于不同的消息内容或操作目标`
      );
    }
    const replayedBody =
      cached.response.body && typeof cached.response.body === 'object'
        ? { ...(clone(cached.response.body) as Record<string, unknown>), replayed: true }
        : clone(cached.response.body);
    return okResp(replayedBody, cached.response.status);
  }

  private storeIdempotencyHit(
    wsId: string,
    method: string,
    subPath: string,
    idemKey: string | null,
    body: Record<string, unknown>,
    resp: MockHttpResponse
  ) {
    if (!idemKey) return;
    this.idempotencyCache[`${wsId}::${method}::${subPath}::${idemKey}`] = {
      response: clone(resp),
      requestSignature: computeIdempotencyRequestSignature(method, subPath, body)
    };
  }

  private getWs(wsId: string): WorkspaceState | null {
    return this.db.workspaces[wsId] ?? null;
  }

  private pushActivity(
    ws: WorkspaceState,
    category: ActivityEvent['category'],
    content: string,
    tone: ActivityEvent['tone'],
    badge: string
  ) {
    ws.activityEvents.unshift({
      id: `act-${Date.now()}-${Math.random().toString(36).slice(2, 6)}`,
      time: nowTimeLabel(),
      category,
      content,
      tone,
      badge
    });
    if (ws.activityEvents.length > 20) {
      ws.activityEvents = ws.activityEvents.slice(0, 20);
    }
  }

  private markScopedWecomVerified(ws: WorkspaceState, scopeKey: string, userId: string, connectionId?: string) {
    ws.wecomScopedBindings[scopeKey] = true;
    // Also mark verifySession objects pointing to same connectionId + userId as verified
    for (const s of Object.values(ws.wecomVerifySessions)) {
      if (s.userId === userId && (!connectionId || s.connectionId === connectionId) && s.status === 'pending') {
        s.status = 'verified';
        s.verifiedAt = nowFullTimestamp();
      }
    }
    const u = ws.directoryUsers.find(item => item.id === userId || item.refId === userId);
    if (u) {
      u.isWecomVerified = true;
      u.botAuthorized = true;
      u.bindingStatus = 'verified';
    }
    for (const emp of ws.employees) {
      let changed = false;
      for (const r of emp.humanConfirmationRules) {
        const ruleConn = r.target.connectionId || 'default-wecom';
        if (r.target.provider === 'wecom' && (r.target.userId === userId || r.target.targetId === userId) && (!connectionId || ruleConn === connectionId)) {
          r.target.identityVerified = true;
          r.target.bindingStatus = 'verified';
          changed = true;
        }
      }
      for (const ch of emp.channels) {
        if (ch.platform === 'wecom' && (!connectionId || ch.connectionId === connectionId)) {
          for (const sr of ch.sharingRules) {
            const targetId = sr.targetUser?.targetId || sr.targetUserId;
            if (targetId === userId && sr.syncStatus === 'auth_error') {
              sr.syncStatus = 'applied';
              sr.authErrorDetail = undefined;
              changed = true;
            }
          }
        }
      }
      if (changed) bumpEmployeeVersion(emp);
    }
    // Unblock pending approvals awaiting WeCom binding on this scoped connection
    for (const appr of ws.humanApprovals) {
      if (
        appr.assignee?.channel === 'wecom' &&
        (appr.assignee.targetId === userId || appr.assignee.canonicalKey?.endsWith(`::${userId}`)) &&
        (!connectionId || appr.assignee.connectionId === connectionId) &&
        appr.deliveryStatus === 'awaiting_binding'
      ) {
        appr.deliveryStatus = 'delivered';
        appr.notificationStatus = 'sent';
        appr.blockedReason = undefined;
        appr.version = (appr.version || 1) + 1;
      }
    }
  }

  private applyTimedTransitions() {
    const now = Date.now();
    for (const t of this.timers) {
      const ws = this.getWs(t.wsId);
      if (!ws) continue;
      const elapsed = now - t.createdAtMs;
      if (t.opId && this.operations[t.opId] && this.operations[t.opId]!.status === 'accepted' && elapsed >= 350 && elapsed < 1800) {
        this.operations[t.opId]!.status = 'processing';
      }

      if (t.kind === 'wecom_verify' && elapsed >= 3500) {
        const sess = ws.wecomVerifySessions[t.targetId];
        if (sess && sess.status === 'pending') {
          sess.status = 'verified';
          sess.verifiedAt = nowFullTimestamp();
          const scopeKey = `${sess.connectionId}::${sess.userId}`;
          this.markScopedWecomVerified(ws, scopeKey, sess.userId, sess.connectionId);
        }
      } else if (t.kind === 'directory_sync') {
        const job = t.opId ? ws.directorySyncJobs[t.opId] : undefined;
        const src = ws.channelSyncSources.find(s => s.id === t.targetId);
        if (job && job.status === 'accepted' && elapsed >= 300 && elapsed < 1800) {
          job.status = 'running';
          job.progressPercent = 72;
          job.processedNodes = Math.floor((job.totalNodes || 100) * 0.72);
        }
        if (elapsed >= 1800 && src && src.status === 'syncing') {
          const snapVer = `snap_${nowFullTimestamp().slice(0, 10).replace(/-/g, '')}_${Math.random().toString(36).slice(2, 6)}`;
          const syncedAt = nowFullTimestamp();
          src.status = 'synced';
          src.progressPercent = 100;
          src.lastSyncedAt = `刚刚 (${syncedAt})`;
          src.lastError = undefined;
          src.snapshotVersion = snapVer;
          src.snapshotGeneratedAt = syncedAt;
          src.lastSuccessfulSnapshot = {
            snapshotVersion: snapVer,
            syncedAt,
            totalMembers: src.totalMembers,
            totalDepartments: src.totalDepartments
          };
          if (src.connectionId && ws.directorySnapshots[src.connectionId]) {
            const prev = ws.directorySnapshots[src.connectionId]!;
            const nextSnap = {
              snapshotVersion: snapVer,
              syncedAt,
              users: clone(prev.users),
              departments: clone(prev.departments)
            };
            // Save immutable versioned snapshot without mutating previous versioned snapshots
            ws.directorySnapshotVersions[src.connectionId] = ws.directorySnapshotVersions[src.connectionId] || {};
            ws.directorySnapshotVersions[src.connectionId]![snapVer] = clone(nextSnap);
            ws.directorySnapshots[src.connectionId] = nextSnap;
          }
          if (job) {
            job.status = 'succeeded';
            job.progressPercent = 100;
            job.processedNodes = job.totalNodes;
            job.snapshotVersion = snapVer;
            job.finishedAt = syncedAt;
          }
          src.history.unshift({
            syncId: job?.syncJobId || `sync-${Date.now().toString(36)}`,
            trigger: 'manual',
            time: syncedAt.slice(5, 16),
            duration: '1.8s',
            nodesSynced: src.snapshotNodeCount,
            status: 'success',
            detail: '通讯录同步成功，组织架构与成员信息已更新'
          });
        }
        if (elapsed >= 1800) {
          ws.directorySyncStatus.status = 'synced';
          ws.directorySyncStatus.lastSyncedAt = '刚刚';
        }
      } else if (t.kind === 'chat_execution') {
        const exec = ws.chatExecutions[t.targetId];
        if (!exec || exec.status === 'interrupted' || exec.status === 'completed' || exec.status === 'failed') continue;
        if (elapsed >= 250 && elapsed < 700) {
          exec.status = 'running';
          exec.partialContent = '正在分析需求与可用能力...';
        } else if (elapsed >= 700) {
          const emp = ws.employees.find(e => e.id === exec.employeeId);
          const sess = emp ? this.ensureSession(ws, emp) : undefined;
          const userPrompt = t.extra || '';
          const tLabel = nowTimeLabel();
          const taskIdNum = `task_web_${t.targetId.slice(-6)}`;
          const isReportTask = userPrompt.includes('报告') || userPrompt.includes('测试') || userPrompt.includes('审查') || userPrompt.includes('执行') || userPrompt.includes('排查');
          let createdTask: TaskRecord | null = null;
          if (isReportTask && emp && sess) {
            createdTask = {
              id: `t-${Date.now()}`,
              taskId: taskIdNum,
              version: 1,
              employeeName: emp.name,
              employeeId: emp.id,
              channelGroup: `Web 工作台对话 · ${emp.name}`,
              platform: 'web',
              toolUsed: 'workspace_sandbox_runner',
              status: 'succeeded',
              delivery: 'sent',
              duration: '0.7s',
              createdAt: tLabel,
              outputArtifact: `${taskIdNum}_summary.md`,
              retryHistory: [{ runIndex: 1, time: tLabel, duration: '0.7s', result: '成功生成协同分析输出' }],
              runs: [
                {
                  runIndex: 1,
                  runId: `run_${taskIdNum}_1`,
                  parentTaskId: taskIdNum,
                  operationId: exec.executionId,
                  startedAt: tLabel,
                  duration: '0.7s',
                  status: 'succeeded',
                  triggerBy: `Web 对话 · ${this.db.currentUser?.name ?? '未登录用户'}`,
                  toolExecutions: [
                    {
                      id: `te-${Date.now()}`,
                      toolName: 'workspace_sandbox_runner',
                      inputArgs: JSON.stringify({ prompt: userPrompt.slice(0, 80) }),
                      outputResult: `Exit 0: 已输出 ${taskIdNum}_summary.md`,
                      duration: '0.6s',
                      status: 'success'
                    }
                  ],
                  logs: [
                    `[${tLabel}:01] 收到工作区对话指令`,
                    `[${tLabel}:02] 任务执行完成，产物已保存`
                  ],
                  outputArtifact: `${taskIdNum}_summary.md`
                }
              ],
              logs: [
                `[${tLabel}:01] 收到工作区对话指令`,
                `[${tLabel}:02] 处理完成 (${emp.modelName})`,
                `[${tLabel}:03] 结果已发送至对话窗口`
              ]
            };
            sess.sessionTasks.unshift(createdTask);
            ws.tasks.unshift(clone(createdTask));
            emp.stats.todayTasks = (emp.stats.todayTasks ?? 0) + 1;
          }
          const replyContent = isReportTask
            ? `已收到您的指令：「${userPrompt.slice(0, 60)}」。已创建任务 #${taskIdNum} 并完成处理，可在任务中心或下方查看详情。`
            : `收到！我是 ${emp?.name || 'AI 员工'}，已记录您的诉求：「${userPrompt.slice(0, 80)}」，如需发起跨群协同请随时说明。`;
          const assistantMsg = {
            id: `msg-a-${exec.executionId}`,
            sender: 'assistant' as const,
            content: replyContent,
            time: tLabel,
            thinking: `理解需求 -> 调用模型 ${emp?.modelName || 'DeepSeek-V3'} -> ${isReportTask ? `创建任务 ${taskIdNum}` : '直接回复'}`,
            generatedTaskId: createdTask?.taskId,
            executionId: exec.executionId,
            status: 'completed' as const
          };
          if (sess) {
            sess.messages.push(assistantMsg);
            sess.updatedAt = '刚刚';
            if (sess.activeExecutionId === exec.executionId) {
              sess.activeExecutionId = undefined;
            }
          }
          if (emp) emp.stats.todayConversations = (emp.stats.todayConversations ?? 0) + 1;
          exec.status = 'completed';
          exec.assistantMessage = clone(assistantMsg);
          exec.generatedTask = createdTask ? clone(createdTask) : null;
          exec.updatedAt = nowFullTimestamp();
        }
      } else if (t.kind === 'work_item_note' && elapsed >= 2000) {
        const wi = ws.workItems.find(item => item.id === t.targetId);
        if (wi && wi.supplementAdoptionDetail?.status === 'pending') {
          wi.supplementAdoptionDetail.status = 'adopted';
          wi.supplementAdopted = true;
          const pendingEvt = wi.events.find(e => e.adoptionStatus === 'pending');
          if (pendingEvt) {
            pendingEvt.adoptionStatus = 'adopted';
            pendingEvt.adoptionStage = wi.supplementAdoptionDetail.stage;
          }
          for (const tid of wi.linkedTaskIds) {
            const task = ws.tasks.find(tk => tk.id === tid || tk.taskId === tid);
            if (task?.supplementAdoptionDetail?.status === 'pending') {
              task.supplementAdoptionDetail.status = 'adopted';
              task.supplementAdopted = true;
            }
          }
        }
      } else if (t.kind === 'wi_cancel' && elapsed >= 1800) {
        const wi = ws.workItems.find(item => item.id === t.targetId);
        if (wi && wi.status === 'canceling') {
          wi.status = 'canceled';
          wi.deliveryStatus = '已取消';
          wi.activeOperationId = undefined;
          wi.version = (wi.version || 1) + 1;
          wi.events.unshift({
            id: `evt-wi-canceled-${Date.now()}`,
            type: 'cancel',
            title: '任务已中止，事项状态变更为「已取消」',
            time: nowTimeLabel(),
            detail: t.extra || '所有相关子任务已停止'
          });
          for (const tid of wi.linkedTaskIds) {
            const tk = ws.tasks.find(item => item.id === tid || item.taskId === tid);
            if (tk && (tk.status === 'running' || tk.status === 'waiting_approval' || tk.status === 'waiting_input' || tk.status === 'canceling')) {
              tk.status = 'canceled';
              tk.version = (tk.version || 1) + 1;
            }
          }
          if (t.opId && this.operations[t.opId]) {
            this.operations[t.opId]!.status = 'succeeded';
            this.operations[t.opId]!.outcome = 'canceled';
            this.operations[t.opId]!.message = '取消动作已完成，状态已变为已取消';
            this.operations[t.opId]!.data = clone(wi);
          }
        }
      } else if (t.kind === 'wi_verify' && elapsed >= 1800) {
        const wi = ws.workItems.find(item => item.id === t.targetId);
        if (wi && wi.status === 'unknown') {
          if (t.extra === 'simulate_fail') {
            wi.activeOperationId = undefined;
            wi.version = (wi.version || 1) + 1;
            if (t.opId && this.operations[t.opId]) {
              this.operations[t.opId]!.status = 'failed';
              this.operations[t.opId]!.outcome = 'probe_failed';
              this.operations[t.opId]!.errorCode = 'REMOTE_PROBE_FAILED';
              this.operations[t.opId]!.errorMessage = '远端状态核对失败：节点无响应';
              this.operations[t.opId]!.message = '状态核对失败，请稍后重试';
              this.operations[t.opId]!.data = clone(wi);
            }
            continue;
          }
          wi.status = 'awaiting_acceptance';
          wi.deliveryStatus = '结果已核对 · 待人工验收';
          wi.activeOperationId = undefined;
          wi.currentSummary = `${wi.currentSummary}（已核对最新结果并回收产物）`;
          wi.version = (wi.version || 1) + 1;
          wi.events.unshift({
            id: `evt-wi-verified-${Date.now()}`,
            type: 'verify',
            title: '状态核对完成 -> 进入待验收阶段',
            time: nowTimeLabel(),
            detail: '已确认执行结果与产物就绪'
          });
          for (const tid of wi.linkedTaskIds) {
            const tk = ws.tasks.find(item => item.id === tid || item.taskId === tid);
            if (tk && tk.status === 'unknown') {
              tk.status = 'succeeded';
              tk.version = (tk.version || 1) + 1;
            }
          }
          if (t.opId && this.operations[t.opId]) {
            this.operations[t.opId]!.status = 'succeeded';
            this.operations[t.opId]!.outcome = 'awaiting_acceptance';
            this.operations[t.opId]!.message = '现场状态核对完成，已进入待验收';
            this.operations[t.opId]!.data = clone(wi);
          }
        }
      } else if (t.kind === 'task_cancel' && elapsed >= 1800) {
        const task = ws.tasks.find(item => item.id === t.targetId);
        if (task && task.status === 'canceling') {
          task.status = 'canceled';
          task.activeOperationId = undefined;
          task.version = (task.version || 1) + 1;
          task.logs.push(`[${nowTimeLabel()}] 任务已中止`);
          if (t.opId && this.operations[t.opId]) {
            this.operations[t.opId]!.status = 'succeeded';
            this.operations[t.opId]!.outcome = 'canceled';
            this.operations[t.opId]!.message = '任务已中止';
            this.operations[t.opId]!.data = clone(task);
          }
        }
      } else if (t.kind === 'task_verify' && elapsed >= 1800) {
        const task = ws.tasks.find(item => item.id === t.targetId);
        if (task && task.status === 'unknown') {
          if (t.extra === 'simulate_fail') {
            task.activeOperationId = undefined;
            task.version = (task.version || 1) + 1;
            task.logs.push(`[${nowTimeLabel()}] 状态核对失败：请求超时，结果仍待核对`);
            if (t.opId && this.operations[t.opId]) {
              this.operations[t.opId]!.status = 'failed';
              this.operations[t.opId]!.outcome = 'probe_failed';
              this.operations[t.opId]!.errorCode = 'REMOTE_PROBE_FAILED';
              this.operations[t.opId]!.errorMessage = '状态核对超时未返回';
              this.operations[t.opId]!.message = '状态核对失败';
              this.operations[t.opId]!.data = clone(task);
            }
            continue;
          }
          task.status = 'succeeded';
          task.activeOperationId = undefined;
          task.version = (task.version || 1) + 1;
          task.logs.push(`[${nowTimeLabel()}] 状态核对完成：已确认任务执行成功`);
          if (t.opId && this.operations[t.opId]) {
            this.operations[t.opId]!.status = 'succeeded';
            this.operations[t.opId]!.outcome = 'succeeded';
            this.operations[t.opId]!.message = '状态核对完成，任务已完成执行';
            this.operations[t.opId]!.data = clone(task);
          }
        }
      } else if (t.kind === 'task_retry' && elapsed >= 1800) {
        const task = ws.tasks.find(item => item.id === t.targetId);
        if (task && task.status === 'running') {
          const runRec = (task.runs || []).find(r => r.runId === t.extra || r.operationId === t.opId);
          const lbl = nowTimeLabel();
          if (runRec) {
            runRec.status = 'succeeded';
            runRec.duration = '1.6s';
            runRec.toolExecutions = [{
              id: `te-${Date.now()}`,
              toolName: task.toolUsed,
              inputArgs: `{"retryOf":"${task.taskId}","runId":"${runRec.runId}"}`,
              outputResult: 'Succeeded',
              duration: '1.4s',
              status: 'success'
            }];
            runRec.logs.push(`[${lbl}] 重新运行完成`);
          }
          task.status = 'succeeded';
          task.delivery = 'sent';
          task.duration = '1.6s';
          task.activeOperationId = undefined;
          task.version = (task.version || 1) + 1;
          task.retryHistory.push({
            runIndex: runRec?.runIndex || (task.retryHistory.length + 1),
            time: lbl,
            duration: '1.6s',
            result: '重新运行已完成'
          });
          task.logs.push(`[${lbl}] 重新运行成功`);
          if (t.opId && this.operations[t.opId]) {
            this.operations[t.opId]!.status = 'succeeded';
            this.operations[t.opId]!.outcome = 'succeeded';
            this.operations[t.opId]!.message = '任务重新运行成功';
            this.operations[t.opId]!.data = clone(task);
          }
        }
      } else if (t.kind === 'approval_task_resume' && elapsed >= 2200) {
        const task = ws.tasks.find(item => item.id === t.targetId || item.taskId === t.targetId);
        const isFail = t.extra === 'simulate_fail';
        if (task && task.status === 'running') {
          task.status = isFail ? 'failed' : 'succeeded';
          task.delivery = isFail ? 'failed' : 'sent';
          task.activeOperationId = undefined;
          task.version = (task.version || 1) + 1;
          task.logs.push(
            isFail
              ? `[${nowTimeLabel()}] 审批已通过，但后续执行失败`
              : `[${nowTimeLabel()}] 审批通过后继续执行完成`
          );
        }
        const appr = ws.humanApprovals.find(a => a.taskId === t.targetId);
        if (appr?.workItemId) {
          const wi = ws.workItems.find(w => w.id === appr.workItemId);
          if (wi && wi.status === 'in_progress') {
            wi.status = isFail ? 'failed' : 'awaiting_acceptance';
            wi.deliveryStatus = isFail ? '后续任务恢复执行失败' : '后续执行已完成 · 待验收';
            wi.version = (wi.version || 1) + 1;
          }
        }
        if (t.opId && this.operations[t.opId]) {
          this.operations[t.opId]!.status = isFail ? 'failed' : 'succeeded';
          this.operations[t.opId]!.approvalStatus = 'approved';
          this.operations[t.opId]!.taskResumeStatus = isFail ? 'failed' : 'succeeded';
          this.operations[t.opId]!.outcome = isFail ? 'resumed_task_failed' : 'resumed_task_succeeded';
          this.operations[t.opId]!.errorCode = isFail ? 'RESUME_EXECUTION_FAILED' : undefined;
          this.operations[t.opId]!.errorMessage = isFail ? '审批虽已通过，但恢复任务执行失败' : undefined;
          this.operations[t.opId]!.message = isFail ? '审批已完成，后续恢复任务执行失败' : '审批已完成，后续恢复任务执行成功';
        }
      } else if (t.kind === 'trigger_run' && elapsed >= 1200) {
        let meta: { empId?: string; triggerId?: string; logId?: string; taskId?: string; targetGroup?: string } = {};
        try {
          meta = t.extra ? (JSON.parse(t.extra) as typeof meta) : {};
        } catch {
          meta = {};
        }
        const emp = ws.employees.find(e => e.id === meta.empId);
        const trg = emp?.triggers.find(item => item.id === meta.triggerId);
        const log = trg?.executionLogs.find(item => item.id === meta.logId || item.executionId === t.targetId);
        const task = ws.tasks.find(item => item.id === meta.taskId);
        const deliveryStatus = meta.targetGroup ? `已投递至 ${meta.targetGroup}` : '未指定回告群';
        if (log && log.status === 'running') {
          log.status = 'succeeded';
          log.duration = '1.2s';
          log.deliveryStatus = deliveryStatus;
          log.summary = `触发器「${trg?.name || t.targetId}」执行成功`;
        }
        if (task && task.status === 'running') {
          task.status = 'succeeded';
          task.delivery = meta.targetGroup ? 'sent' : 'pending';
          task.duration = '1.2s';
          task.version = (task.version || 1) + 1;
          task.retryHistory = [{ runIndex: 1, time: nowTimeLabel(), duration: '1.2s', result: '触发器手动运行完成' }];
          task.logs.push(`[${nowTimeLabel()}] 执行完成 (${deliveryStatus})`);
        }
        if (trg && emp) {
          this.pushActivity(ws, 'tasks', `触发器「${trg.name}」运行完成（任务 ${task?.taskId || ''}）`, 'success', '触发器运行');
        }
        if (t.opId && this.operations[t.opId] && this.operations[t.opId]!.status !== 'succeeded') {
          this.operations[t.opId]!.status = 'succeeded';
          this.operations[t.opId]!.outcome = 'completed';
          this.operations[t.opId]!.message = `触发器「${trg?.name || t.targetId}」运行完成`;
          this.operations[t.opId]!.data = {
            executionId: t.targetId,
            executionLog: log ? clone(log) : undefined,
            trigger: trg ? clone(trg) : undefined,
            task: task ? clone(task) : undefined
          };
        }
      }
    }
  }

  public async handle(
    method: string,
    rawUrl: string,
    bodyText = '',
    headers?: Record<string, string | string[] | undefined>
  ): Promise<MockHttpResponse> {
    this.applyTimedTransitions();

    const parsedUrl = new URL(rawUrl, 'http://localhost');
    const path = parsedUrl.pathname.replace(/\/+$/, '') || '/';
    let body: Record<string, unknown> = {};
    if (bodyText && bodyText.trim().length > 0) {
      try {
        body = JSON.parse(bodyText) as Record<string, unknown>;
      } catch {
        return errResp(400, 'INVALID_JSON', '请求体不是合法的 JSON 格式');
      }
    }

    // 1. Auth & Workspaces
    if (method === 'GET' && path === '/api/auth/me') {
      const account = this.resolveAuthenticatedAccount(headers);
      if (!account) {
        return errResp(401, 'UNAUTHORIZED', '未登录或登录会话已失效，请重新登录');
      }
      return okResp(clone(this.toUserProfile(account)));
    }
    if (method === 'POST' && path === '/api/auth/email/send-code') {
      const rawEmail = typeof body['email'] === 'string' ? body['email'].trim() : '';
      const purpose = body['purpose'] === 'login'
        ? 'login'
        : body['purpose'] === 'register'
          ? 'register'
          : body['purpose'] === 'reset_password'
            ? 'reset_password'
            : '';
      if (!purpose) {
        return errResp(400, 'INVALID_PURPOSE', '验证码用途必须为 register、login 或 reset_password');
      }
      if (!rawEmail || !this.isValidEmailFormat(rawEmail)) {
        return errResp(400, 'INVALID_EMAIL', '请输入格式正确的邮箱地址');
      }
      const normalizedEmail = this.normalizeEmail(rawEmail);
      const existing = this.db.authUsers.find(u => this.normalizeEmail(u.email) === normalizedEmail);
      if (purpose === 'register' && existing) {
        return errResp(409, 'EMAIL_ALREADY_EXISTS', '该邮箱已注册，请直接登录或使用 Google 账号继续');
      }
      if ((purpose === 'login' || purpose === 'reset_password') && !existing) {
        return errResp(404, 'ACCOUNT_NOT_FOUND', '该邮箱尚未注册，请先切换至「注册」创建账号');
      }

      const key = `${purpose}:${normalizedEmail}`;
      const now = Date.now();
      const prev = this.emailVerificationCodes[key];
      if (prev && !prev.consumed && now < prev.resendAvailableAtMs) {
        const waitSec = Math.max(1, Math.ceil((prev.resendAvailableAtMs - now) / 1000));
        return errResp(429, 'RATE_LIMITED', `发送过于频繁，请 ${waitSec} 秒后再试`);
      }

      const code = String(Math.floor(100000 + Math.random() * 900000));
      this.emailVerificationCodes[key] = {
        email: normalizedEmail,
        purpose,
        code,
        createdAtMs: now,
        expiresAtMs: now + 5 * 60 * 1000,
        resendAvailableAtMs: now + 60 * 1000,
        attempts: 0,
        consumed: false
      };
      return okResp({
        sent: true,
        email: normalizedEmail,
        purpose,
        expiresInSeconds: 300,
        resendAfterSeconds: 60,
        debugCode: code
      });
    }
    if (method === 'POST' && path === '/api/auth/register') {
      const name = typeof body['name'] === 'string' ? body['name'].trim() : '';
      const rawEmail = typeof body['email'] === 'string' ? body['email'].trim() : '';
      const code = typeof body['code'] === 'string' ? body['code'].trim() : '';
      const password = typeof body['password'] === 'string' ? body['password'] : '';

      if (!name || name.length > 40) {
        return errResp(400, 'INVALID_NAME', '请输入 1-40 个字符的姓名或昵称');
      }
      if (!rawEmail || !this.isValidEmailFormat(rawEmail)) {
        return errResp(400, 'INVALID_EMAIL', '请输入格式正确的邮箱地址');
      }
      if (!code) {
        return errResp(400, 'INVALID_VERIFICATION_CODE', '请输入邮箱验证码');
      }
      if (password.length < 8) {
        return errResp(400, 'WEAK_PASSWORD', '密码长度至少为 8 位');
      }

      const normalizedEmail = this.normalizeEmail(rawEmail);
      if (this.db.authUsers.some(u => this.normalizeEmail(u.email) === normalizedEmail)) {
        return errResp(409, 'EMAIL_ALREADY_EXISTS', '该邮箱已注册，请直接登录');
      }

      const key = `register:${normalizedEmail}`;
      const record = this.emailVerificationCodes[key];
      if (!record || record.consumed) {
        return errResp(400, 'INVALID_VERIFICATION_CODE', '请先获取邮箱验证码');
      }
      if (Date.now() > record.expiresAtMs) {
        return errResp(400, 'VERIFICATION_CODE_EXPIRED', '验证码已过期，请重新发送');
      }
      if (record.attempts >= 5) {
        return errResp(429, 'TOO_MANY_ATTEMPTS', '验证码错误次数过多，请重新获取验证码');
      }
      if (record.code !== code) {
        record.attempts += 1;
        return errResp(400, 'INVALID_VERIFICATION_CODE', '邮箱验证码不正确，请核对后重试');
      }
      record.consumed = true;

      const newUser: MockAuthAccount = {
        id: `user-${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 6)}`,
        name,
        email: normalizedEmail,
        password,
        role: 'owner',
        avatarText: this.buildAvatarText(name, normalizedEmail),
        authProvider: 'email',
        emailVerified: true,
        workspaceIds: []
      };
      this.db.authUsers.push(newUser);
      return this.createAuthSessionResponse(newUser, 201);
    }
    if (method === 'POST' && path === '/api/auth/login') {
      const rawEmail = typeof body['email'] === 'string' ? body['email'].trim() : '';
      const password = typeof body['password'] === 'string' ? body['password'] : '';
      if (!rawEmail || !this.isValidEmailFormat(rawEmail)) {
        return errResp(400, 'INVALID_EMAIL', '请输入格式正确的邮箱地址');
      }
      if (!password) {
        return errResp(400, 'INVALID_PASSWORD', '请输入登录密码');
      }
      const normalizedEmail = this.normalizeEmail(rawEmail);
      const account = this.db.authUsers.find(u => this.normalizeEmail(u.email) === normalizedEmail);
      if (!account) {
        return errResp(401, 'INVALID_CREDENTIALS', '邮箱或密码不正确');
      }
      if (!account.password) {
        return errResp(400, 'OAUTH_ACCOUNT_NO_PASSWORD', '该账号通过 Google OAuth 注册，请使用 Google 登录或邮箱验证码登录');
      }
      if (account.password !== password) {
        return errResp(401, 'INVALID_CREDENTIALS', '邮箱或密码不正确');
      }
      return this.createAuthSessionResponse(account, 200);
    }
    if (method === 'POST' && path === '/api/auth/login/code') {
      const rawEmail = typeof body['email'] === 'string' ? body['email'].trim() : '';
      const code = typeof body['code'] === 'string' ? body['code'].trim() : '';
      if (!rawEmail || !this.isValidEmailFormat(rawEmail)) {
        return errResp(400, 'INVALID_EMAIL', '请输入格式正确的邮箱地址');
      }
      if (!code) {
        return errResp(400, 'INVALID_VERIFICATION_CODE', '请输入邮箱验证码');
      }
      const normalizedEmail = this.normalizeEmail(rawEmail);
      const account = this.db.authUsers.find(u => this.normalizeEmail(u.email) === normalizedEmail);
      if (!account) {
        return errResp(404, 'ACCOUNT_NOT_FOUND', '该邮箱尚未注册，请先创建账号');
      }
      const key = `login:${normalizedEmail}`;
      const record = this.emailVerificationCodes[key];
      if (!record || record.consumed) {
        return errResp(400, 'INVALID_VERIFICATION_CODE', '请先获取邮箱验证码');
      }
      if (Date.now() > record.expiresAtMs) {
        return errResp(400, 'VERIFICATION_CODE_EXPIRED', '验证码已过期，请重新发送');
      }
      if (record.attempts >= 5) {
        return errResp(429, 'TOO_MANY_ATTEMPTS', '验证码错误次数过多，请重新获取验证码');
      }
      if (record.code !== code) {
        record.attempts += 1;
        return errResp(400, 'INVALID_VERIFICATION_CODE', '邮箱验证码不正确');
      }
      record.consumed = true;
      account.emailVerified = true;
      return this.createAuthSessionResponse(account, 200);
    }
    if (method === 'POST' && path === '/api/auth/password/reset') {
      const rawEmail = typeof body['email'] === 'string' ? body['email'].trim() : '';
      const code = typeof body['code'] === 'string' ? body['code'].trim() : '';
      const newPassword = typeof body['newPassword'] === 'string' ? body['newPassword'] : '';

      if (!rawEmail || !this.isValidEmailFormat(rawEmail)) {
        return errResp(400, 'INVALID_EMAIL', '请输入格式正确的邮箱地址');
      }
      if (!code) {
        return errResp(400, 'INVALID_VERIFICATION_CODE', '请输入邮箱验证码');
      }
      if (newPassword.length < 8) {
        return errResp(400, 'WEAK_PASSWORD', '新密码长度至少为 8 位');
      }

      const normalizedEmail = this.normalizeEmail(rawEmail);
      const account = this.db.authUsers.find(u => this.normalizeEmail(u.email) === normalizedEmail);
      if (!account) {
        return errResp(404, 'ACCOUNT_NOT_FOUND', '该邮箱尚未注册，请先创建账号');
      }

      const key = `reset_password:${normalizedEmail}`;
      const record = this.emailVerificationCodes[key];
      if (!record || record.consumed) {
        return errResp(400, 'INVALID_VERIFICATION_CODE', '请先获取重置密码邮箱验证码');
      }
      if (Date.now() > record.expiresAtMs) {
        return errResp(400, 'VERIFICATION_CODE_EXPIRED', '验证码已过期，请重新发送验证码');
      }
      if (record.attempts >= 5) {
        return errResp(429, 'TOO_MANY_ATTEMPTS', '验证码错误次数过多，请重新获取验证码');
      }
      if (record.code !== code) {
        record.attempts += 1;
        return errResp(400, 'INVALID_VERIFICATION_CODE', '邮箱验证码不正确，请核对后重试');
      }

      record.consumed = true;
      account.password = newPassword;
      account.emailVerified = true;
      // Invalidate existing sessions for this user after password reset
      for (const [tok, sess] of Object.entries(this.authSessions)) {
        if (sess.userId === account.id) {
          delete this.authSessions[tok];
        }
      }
      return okResp({ ok: true, email: normalizedEmail });
    }
    if (method === 'GET' && path === '/api/auth/oauth/google/url') {
      const redirectUri = (parsedUrl.searchParams.get('redirectUri') || '').trim();
      if (!redirectUri) {
        return errResp(400, 'INVALID_REDIRECT_URI', '缺少 OAuth 回调地址 redirectUri');
      }
      const state = `gstate_${Date.now().toString(36)}_${Math.random().toString(36).slice(2, 10)}`;
      const now = Date.now();
      this.oauthStates[state] = {
        state,
        redirectUri,
        createdAtMs: now,
        expiresAtMs: now + 10 * 60 * 1000,
        consumed: false
      };
      const sep = redirectUri.includes('?') ? '&' : '?';
      return okResp({
        authorizeUrl: `${redirectUri}${sep}mock_google_oauth=1&state=${encodeURIComponent(state)}`,
        state,
        provider: 'google',
        isMock: true
      });
    }
    if (method === 'POST' && path === '/api/auth/oauth/google/callback') {
      const code = typeof body['code'] === 'string' ? body['code'].trim() : '';
      const state = typeof body['state'] === 'string' ? body['state'].trim() : '';
      const stateRec = state ? this.oauthStates[state] : undefined;
      if (!stateRec || stateRec.consumed || Date.now() > stateRec.expiresAtMs) {
        return errResp(400, 'INVALID_OAUTH_STATE', 'Google 授权状态已失效，请重新发起登录');
      }
      stateRec.consumed = true;

      if (!code) {
        return errResp(400, 'INVALID_OAUTH_CODE', '缺少 Google 授权码');
      }
      if (code === 'access_denied') {
        return errResp(400, 'OAUTH_CANCELLED', '已取消 Google 账号授权');
      }

      const mockAccRaw = body['mockAccount'];
      const mockObj = mockAccRaw && typeof mockAccRaw === 'object' ? (mockAccRaw as Record<string, unknown>) : null;
      const rawEmail = typeof mockObj?.['email'] === 'string' && mockObj['email'].trim()
        ? mockObj['email'].trim()
        : 'alex.chen@gmail.com';
      if (!this.isValidEmailFormat(rawEmail)) {
        return errResp(400, 'INVALID_EMAIL', 'Google 账号邮箱格式无效');
      }
      const normalizedEmail = this.normalizeEmail(rawEmail);
      const providedName = typeof mockObj?.['name'] === 'string' ? mockObj['name'].trim() : '';
      const derivedName = providedName || normalizedEmail.split('@')[0] || 'Google User';
      const googleSub = `google_sub_${normalizedEmail}`;

      let account = this.db.authUsers.find(
        u => u.googleSub === googleSub || this.normalizeEmail(u.email) === normalizedEmail
      );
      if (!account) {
        account = {
          id: `user-${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 6)}`,
          name: derivedName,
          email: normalizedEmail,
          role: 'owner',
          avatarText: this.buildAvatarText(derivedName, normalizedEmail),
          authProvider: 'google',
          emailVerified: true,
          googleSub,
          workspaceIds: []
        };
        this.db.authUsers.push(account);
      } else {
        account.emailVerified = true;
        if (!account.googleSub) {
          account.googleSub = googleSub;
        }
      }
      return this.createAuthSessionResponse(account, 200);
    }
    if (method === 'POST' && path === '/api/auth/logout') {
      const token = this.extractAuthToken(headers);
      if (token) {
        delete this.authSessions[token];
      }
      return okResp(
        { ok: true },
        200,
        { 'Set-Cookie': 'kiko_session=; Path=/; Max-Age=0; HttpOnly; SameSite=Lax' }
      );
    }
    if (method === 'GET' && path === '/api/workspaces') {
      const token = this.extractAuthToken(headers);
      if (token) {
        const account = this.resolveAuthenticatedAccount(headers);
        if (!account) {
          return errResp(401, 'UNAUTHORIZED', '未登录或登录会话已失效，请重新登录');
        }
        return okResp(this.getAccountWorkspaces(account));
      }
      return okResp(Object.values(this.db.workspaces).map(w => clone(w.info)));
    }
    if (method === 'POST' && path === '/api/mock/reset') {
      this.db = createInitialMockDatabase();
      this.timers = [];
      this.qrSessionMeta = {};
      this.latestQrSessionByKey = {};
      this.modelReceipts = {};
      this.mcpReceipts = {};
      this.channelTestReceipts = {};
      this.draftTriggerSecrets = {};
      this.operations = {};
      this.idempotencyCache = {};
      this.operationsByIdempotencyKey = {};
      this.authSessions = {};
      this.emailVerificationCodes = {};
      this.oauthStates = {};
      return okResp({ reset: true });
    }

    // Match /api/workspaces/:wsId/...
    const wsMatch = /^\/api\/workspaces\/([^/]+)(\/.*)?$/.exec(path);
    if (!wsMatch || !wsMatch[1]) {
      return errResp(404, 'NOT_FOUND', `未找到接口路由：${method} ${path}`);
    }
    const wsId = decodeURIComponent(wsMatch[1]);
    const subPath = wsMatch[2] || '/';
    const ws = this.getWs(wsId);
    if (!ws) {
      return errResp(404, 'WORKSPACE_NOT_FOUND', `工作区 ${wsId} 不存在`);
    }

    // Route to domain handlers
    if (subPath === '/dashboard/overview' && method === 'GET') {
      return this.handleDashboardOverview(ws);
    }
    if (subPath === '/search' && method === 'GET') {
      const q = (parsedUrl.searchParams.get('q') || '').trim().toLowerCase();
      return this.handleSearch(ws, q);
    }
    if (subPath === '/workforce/options' && method === 'GET') {
      return okResp({
        platformCandidateGroups: clone(platformCandidateGroups),
        platformCandidateGroupItems: clone(platformCandidateGroupItems),
        feishuRegexTemplates: clone(feishuRegexTemplates)
      });
    }

    const resResp = await this.routeResources(wsId, ws, method, subPath, body, parsedUrl);
    if (resResp) return resResp;

    const wfResp = this.routeWorkforce(wsId, ws, method, subPath, body, headers, parsedUrl);
    if (wfResp) return wfResp;

    const chatResp = this.routeChat(wsId, ws, method, subPath, body, parsedUrl, headers);
    if (chatResp) return chatResp;

    const tasksResp = this.routeTasksAndWorkItems(wsId, ws, method, subPath, body, parsedUrl, headers);
    if (tasksResp) return tasksResp;

    const settingsResp = this.routeSettings(ws, method, subPath, body);
    if (settingsResp) return settingsResp;

    return errResp(404, 'NOT_FOUND', `未定义的工作区接口：${method} ${path}`);
  }

  private handleDashboardOverview(ws: WorkspaceState): MockHttpResponse {
    const pendingApprovals = ws.humanApprovals.filter(a => a.status === 'pending');
    const unconfiguredEmployees = ws.employees.filter(e => e.status === 'pending_setup' || e.channels.length === 0);
    const activeCount = ws.employees.filter(e => e.status === 'active').length;
    const warningCount = ws.employees.filter(e => e.status === 'warning').length;
    const errorCount = ws.employees.filter(e => e.channels.some(ch => ch.status === 'error')).length;
    const weeklyNewCount = ws.employees.filter(
      e => e.status === 'pending_setup' || (e.id !== 'emp-01' && e.id !== 'emp-02')
    ).length;
    return okResp({
      metrics: {
        totalEmployees: ws.employees.length,
        runningEmployees: activeCount,
        warningEmployees: warningCount,
        errorEmployees: errorCount,
        totalChannels: ws.runtimeMonitor.totalChannels,
        avgLatency: ws.runtimeMonitor.channels[0]?.latency || '42ms',
        healthRate: ws.runtimeMonitor.healthRate,
        todayTasksCount: 185 + ws.tasks.length,
        weeklyNewEmployees: weeklyNewCount
      },
      pendingApprovals: clone(pendingApprovals),
      unconfiguredEmployees: clone(unconfiguredEmployees),
      activityEvents: clone(ws.activityEvents),
      trendDates: ['11-05', '11-06', '11-07', '11-08', '11-09', '11-10', '今天'],
      trendPath: 'M0,100 C70,95 130,55 200,65 C270,75 330,30 400,35 C450,40 480,15 500,20'
    });
  }

  private handleSearch(ws: WorkspaceState, q: string): MockHttpResponse {
    if (!q) return okResp({ query: q, items: [] });
    const items: { id: string; kind: string; title: string; subtitle: string; targetMenu: string; targetId?: string }[] = [];
    for (const emp of ws.employees) {
      if (emp.name.toLowerCase().includes(q) || emp.role.toLowerCase().includes(q) || emp.code.toLowerCase().includes(q)) {
        items.push({ id: emp.id, kind: 'employee', title: `${emp.name} (${emp.code})`, subtitle: emp.role, targetMenu: 'employees', targetId: emp.id });
      }
    }
    for (const wi of ws.workItems) {
      if (wi.title.toLowerCase().includes(q) || wi.code.toLowerCase().includes(q) || wi.originGroup.toLowerCase().includes(q)) {
        items.push({ id: wi.id, kind: 'work_item', title: `${wi.code} · ${wi.title}`, subtitle: wi.originGroup, targetMenu: 'tasks', targetId: wi.id });
      }
    }
    for (const tk of ws.tasks) {
      if (tk.taskId.toLowerCase().includes(q) || tk.toolUsed.toLowerCase().includes(q)) {
        items.push({ id: tk.id, kind: 'task', title: tk.taskId, subtitle: `${tk.employeeName} · ${tk.toolUsed}`, targetMenu: 'tasks', targetId: tk.id });
      }
    }
    for (const sk of ws.skills) {
      if (sk.name.toLowerCase().includes(q) || sk.code.toLowerCase().includes(q)) {
        items.push({ id: sk.id, kind: 'skill', title: sk.name, subtitle: `${sk.code} · ${sk.version}`, targetMenu: 'resources', targetId: sk.id });
      }
    }
    for (const mcp of ws.mcpServers) {
      if (mcp.name.toLowerCase().includes(q) || mcp.endpoint.toLowerCase().includes(q)) {
        items.push({ id: mcp.id, kind: 'mcp', title: mcp.name, subtitle: mcp.endpoint, targetMenu: 'resources', targetId: mcp.id });
      }
    }
    return okResp({ query: q, items: items.slice(0, 12) });
  }

  private async routeResources(
    wsId: string,
    ws: WorkspaceState,
    method: string,
    subPath: string,
    body: Record<string, unknown>,
    parsedUrl: URL
  ): Promise<MockHttpResponse | null> {
    // Models (aligned with control-plane/src/model-profiles.ts receipt binding)
    if (subPath === '/models' && method === 'GET') {
      return okResp(clone(ws.models));
    }
    const modelTestMatch = /^(?:\/models\/([^/]+))?\/models?\/test-connection$|^\/models\/(?:([^/]+)\/)?test-connection$/.exec(subPath);
    if (modelTestMatch && method === 'POST') {
      const profileIdRaw = modelTestMatch[1] || modelTestMatch[2];
      const profileId = profileIdRaw ? decodeURIComponent(profileIdRaw) : (typeof body['profileId'] === 'string' && body['profileId'] ? body['profileId'] : null);
      const endpoint = String(body['endpoint'] || '').trim();
      const protocol = String(body['protocol'] || 'chat_completions').trim();
      const modelCode = String(body['modelCode'] || '').trim();
      const providedKey = typeof body['apiKey'] === 'string' ? body['apiKey'].trim() : '';

      if (!modelCode || !endpoint) {
        return errResp(400, 'VALIDATION_ERROR', '请先完整填写模型标识 Model ID 与端点地址 Endpoint');
      }
      if (isMaskedDisplayValue(providedKey)) {
        return errResp(400, 'MASKED_SECRET_REJECTED', '请输入真实 API Key 或留空保留原密钥');
      }

      let parsed: URL;
      try {
        parsed = new URL(endpoint);
      } catch {
        return errResp(400, 'INVALID_ENDPOINT', `连接测试失败：无效的端点 URL 格式（${endpoint || '空'}）`);
      }
      if (parsed.protocol !== 'http:' && parsed.protocol !== 'https:') {
        return errResp(400, 'INVALID_PROTOCOL', `连接测试失败：仅支持 http/https 协议，当前为 ${parsed.protocol}`);
      }
      const hostLower = parsed.hostname.toLowerCase();
      if (!hostLower || hostLower.endsWith('.invalid') || hostLower.includes('unreachable') || hostLower.includes('badhost') || parsed.port === '9999') {
        return errResp(400, 'ENDPOINT_UNREACHABLE', `连接测试失败：目标主机 ${parsed.host} 不可达或域名解析超时`);
      }

      const storedSecret = profileId ? (ws.modelSecrets[profileId] || '') : '';
      const effectiveKey = providedKey || storedSecret;
      if (!effectiveKey && !hostLower.startsWith('192.168.') && hostLower !== 'localhost' && hostLower !== '127.0.0.1') {
        return errResp(400, 'API_KEY_REQUIRED', '连接测试失败：外部模型端点必须提供有效 API Key');
      }

      const binding = computeModelConfigBinding(wsId, profileId, protocol, endpoint, modelCode, effectiveKey);
      const expiresAt = Date.now() + 300_000;
      const connectionTestReceipt = `rcpt_model_${Date.now().toString(36)}_${Math.random().toString(36).slice(2, 8)}`;
      this.modelReceipts[connectionTestReceipt] = { wsId, profileId, binding, expiresAt };

      const latencyMs = hostLower.startsWith('192.168.') || hostLower === 'localhost' ? 42 : 178;
      return okResp({
        ok: true,
        protocol,
        modelCode,
        latency: `${latencyMs}ms`,
        receipt: connectionTestReceipt,
        connectionTestReceipt,
        expiresAt: new Date(expiresAt).toISOString()
      });
    }
    if (subPath === '/models' && method === 'POST') {
      const name = String(body['name'] || '').trim();
      const modelCode = String(body['modelCode'] || '').trim();
      const endpoint = String(body['endpoint'] || '').trim();
      const protocol = String(body['protocol'] || 'chat_completions').trim() as ModelItem['protocol'];
      const apiKey = typeof body['apiKey'] === 'string' ? body['apiKey'].trim() : '';
      const receiptId = typeof body['connectionTestReceipt'] === 'string' ? body['connectionTestReceipt'].trim() : '';
      if (!name || !modelCode || !endpoint) {
        return errResp(400, 'VALIDATION_ERROR', '模型名称、编码与端点地址均为必填项');
      }
      if (isMaskedDisplayValue(apiKey)) {
        return errResp(400, 'MASKED_SECRET_REJECTED', '请输入有效的 API Key');
      }
      const receipt = this.modelReceipts[receiptId];
      const expectedBinding = computeModelConfigBinding(wsId, null, protocol, endpoint, modelCode, apiKey);
      if (!receipt || receipt.wsId !== wsId || receipt.profileId !== null || receipt.expiresAt <= Date.now() || receipt.binding !== expectedBinding) {
        return errResp(409, 'MODEL_CONNECTION_TEST_REQUIRED', '当前配置尚未通过连通性测试或参数已修改，请重新测试连接后再保存');
      }
      delete this.modelReceipts[receiptId];

      const newId = `model-${Date.now()}`;
      ws.modelSecrets[newId] = apiKey || 'EMPTY';
      const created: ModelItem = {
        id: newId,
        name,
        modelCode,
        protocol,
        endpoint,
        apiKeyMasked: apiKey ? `sk-custom••••${apiKey.slice(-4)}` : 'EMPTY (内网免密)',
        boundEmployeesCount: 0,
        supportsThinking: Boolean(body['supportsThinking']),
        enabled: body['enabled'] !== undefined ? Boolean(body['enabled']) : true,
        latency: String(body['latency'] || '160ms'),
        isDefault: Boolean(body['isDefault']),
        connectionTestReceipt: receiptId
      };
      if (created.isDefault) {
        ws.models.forEach(m => { m.isDefault = false; });
      }
      ws.models.unshift(created);
      return okResp(clone(created), 201);
    }
    const modelActionMatch = /^\/models\/([^/]+)\/(status|set-default|revoke-key)$/.exec(subPath);
    if (modelActionMatch && modelActionMatch[1] && modelActionMatch[2] && (method === 'PATCH' || method === 'POST')) {
      const mId = decodeURIComponent(modelActionMatch[1]);
      const action = modelActionMatch[2];
      const target = ws.models.find(m => m.id === mId);
      if (!target) return errResp(404, 'MODEL_NOT_FOUND', '未找到指定模型配置');
      if (action === 'status') {
        target.enabled = Boolean(body['enabled']);
      } else if (action === 'set-default') {
        ws.models.forEach(m => { m.isDefault = false; });
        target.isDefault = true;
      } else if (action === 'revoke-key') {
        ws.modelSecrets[mId] = '';
        target.apiKeyMasked = '已吊销 (Revoked)';
        target.connectionTestReceipt = undefined;
      }
      return okResp(clone(target));
    }
    const modelMatch = /^\/models\/([^/]+)$/.exec(subPath);
    if (modelMatch && modelMatch[1]) {
      const mId = decodeURIComponent(modelMatch[1]);
      const idx = ws.models.findIndex(m => m.id === mId);
      if (idx === -1) return errResp(404, 'MODEL_NOT_FOUND', '未找到指定模型配置');
      const target = ws.models[idx]!;
      if (method === 'PUT' || method === 'PATCH') {
        const nextName = typeof body['name'] === 'string' ? body['name'].trim() : target.name;
        const nextModelCode = typeof body['modelCode'] === 'string' ? body['modelCode'].trim() : target.modelCode;
        const nextProtocol = (typeof body['protocol'] === 'string' ? body['protocol'].trim() : target.protocol) as ModelItem['protocol'];
        const nextEndpoint = typeof body['endpoint'] === 'string' ? body['endpoint'].trim() : target.endpoint;
        const nextApiKeyInput = typeof body['apiKey'] === 'string' ? body['apiKey'].trim() : '';

        if (!nextName || !nextModelCode || !nextEndpoint) {
          return errResp(400, 'VALIDATION_ERROR', '模型名称、Model ID 与 API 端点不可为空');
        }
        if (isMaskedDisplayValue(nextApiKeyInput)) {
          return errResp(400, 'MASKED_SECRET_REJECTED', '请输入有效的 API Key');
        }

        const effectiveKey = nextApiKeyInput || (ws.modelSecrets[mId] || '');
        const receiptId = typeof body['connectionTestReceipt'] === 'string' ? body['connectionTestReceipt'].trim() : '';
        const receipt = this.modelReceipts[receiptId];
        const expectedBinding = computeModelConfigBinding(wsId, mId, nextProtocol, nextEndpoint, nextModelCode, effectiveKey);
        const connFieldsUnchanged = nextProtocol === target.protocol && nextEndpoint === target.endpoint && nextModelCode === target.modelCode && !nextApiKeyInput && Boolean(target.connectionTestReceipt);

        if (!connFieldsUnchanged) {
          if (!receipt || receipt.wsId !== wsId || receipt.profileId !== mId || receipt.expiresAt <= Date.now() || receipt.binding !== expectedBinding) {
            return errResp(409, 'MODEL_CONNECTION_TEST_REQUIRED', '编辑了模型连接配置，必须针对最新参数重新通过连通性测试后再保存');
          }
          delete this.modelReceipts[receiptId];
        }

        target.name = nextName;
        target.modelCode = nextModelCode;
        target.protocol = nextProtocol;
        target.endpoint = nextEndpoint;
        if (nextApiKeyInput) {
          ws.modelSecrets[mId] = nextApiKeyInput;
          target.apiKeyMasked = `sk-custom••••${nextApiKeyInput.slice(-4)}`;
        }
        if (typeof body['supportsThinking'] === 'boolean') target.supportsThinking = body['supportsThinking'];
        if (typeof body['enabled'] === 'boolean') target.enabled = body['enabled'];
        if (receiptId) target.connectionTestReceipt = receiptId;
        return okResp(clone(target));
      }
      if (method === 'DELETE') {
        if (target.isDefault) return errResp(409, 'CANNOT_DELETE_DEFAULT', '默认工作区模型不可直接删除');
        ws.models.splice(idx, 1);
        delete ws.modelSecrets[mId];
        return okResp({ deleted: true, id: mId });
      }
    }

    // Skills
    if (subPath === '/skills/parse-zip' && method === 'POST') {
      const fileName = String(body['fileName'] || '').trim();
      const base64Content = String(body['base64Content'] || '').trim();
      if (!fileName || !base64Content) {
        return errResp(400, 'MISSING_FILE', '请上传有效的 .zip 压缩包文件');
      }
      if (!fileName.toLowerCase().endsWith('.zip')) {
        return errResp(400, 'INVALID_EXTENSION', `仅支持 .zip 格式文件，当前文件为：${fileName}`);
      }
      let zipData: { names: string[]; texts: Record<string, string> };
      try {
        const bytes = decodeBase64ToBytes(base64Content);
        zipData = await extractZipTextEntries(bytes);
      } catch {
        return errResp(400, 'INVALID_ZIP', `文件「${fileName}」不是合法的 ZIP 压缩包或已损坏`);
      }

      if (zipData.names.length === 0) {
        return errResp(400, 'EMPTY_ZIP', `压缩包「${fileName}」中不包含任何文件`);
      }

      const mdKey = Object.keys(zipData.texts).find(n => /(^|\/)SKILL\.md$/i.test(n))
        || Object.keys(zipData.texts).find(n => /\.md$/i.test(n))
        || Object.keys(zipData.texts).find(n => /(prompt\.txt|readme\.txt)$/i.test(n));
      const pkgKey = Object.keys(zipData.texts).find(n => /(^|\/)(skill|package)\.json$/i.test(n));

      const baseSlug = fileName.replace(/\.zip$/i, '').replace(/[^a-zA-Z0-9_-]+/g, '_').replace(/^_+|_+$/g, '') || 'zip_skill';
      let parsedName = fileName.replace(/\.zip$/i, '');
      let parsedCode = baseSlug.toLowerCase();
      let parsedVersion = 'v1.0.0';
      let parsedDesc = `从 ZIP 文件「${fileName}」解析（共 ${zipData.names.length} 个文件：${zipData.names.slice(0, 4).join(', ')}）`;
      let parsedPrompt = mdKey ? (zipData.texts[mdKey] || '') : '';
      let parsedTags: string[] = [];

      if (pkgKey && zipData.texts[pkgKey]) {
        try {
          const pkgJson = JSON.parse(zipData.texts[pkgKey]!) as Record<string, unknown>;
          if (typeof pkgJson['name'] === 'string' && pkgJson['name'].trim()) parsedName = pkgJson['name'].trim();
          if (typeof pkgJson['code'] === 'string' && pkgJson['code'].trim()) parsedCode = pkgJson['code'].trim();
          if (typeof pkgJson['version'] === 'string' && pkgJson['version'].trim()) {
            parsedVersion = pkgJson['version'].startsWith('v') ? pkgJson['version'] : `v${pkgJson['version']}`;
          }
          if (typeof pkgJson['description'] === 'string' && pkgJson['description'].trim()) parsedDesc = pkgJson['description'].trim();
          if (Array.isArray(pkgJson['tools'])) parsedTags = pkgJson['tools'].map(String);
        } catch {
          // ignore invalid package.json
        }
      }

      if (mdKey && zipData.texts[mdKey]) {
        const mdParsed = parseMarkdownSkillContent(zipData.texts[mdKey]!, parsedName);
        if (mdParsed.name) parsedName = mdParsed.name;
        if (mdParsed.code) parsedCode = mdParsed.code;
        if (mdParsed.version) parsedVersion = mdParsed.version;
        if (mdParsed.description) parsedDesc = mdParsed.description;
        if (mdParsed.tools && mdParsed.tools.length > 0) parsedTags = mdParsed.tools;
        parsedPrompt = mdParsed.body;
      } else if (!parsedPrompt) {
        const firstTxtKey = Object.keys(zipData.texts)[0];
        if (firstTxtKey) {
          parsedPrompt = zipData.texts[firstTxtKey] || '';
        } else {
          return errResp(400, 'NO_SKILL_CONTENT', `压缩包「${fileName}」内未找到 SKILL.md、.md 或 prompt.txt 等可读文本定义`);
        }
      }

      return okResp({
        importedFileName: fileName,
        name: parsedName,
        code: parsedCode,
        version: parsedVersion,
        description: parsedDesc,
        promptTemplate: parsedPrompt,
        requiredToolTags: parsedTags,
        extractedFiles: zipData.names
      });
    }
    if (subPath === '/skills' && method === 'GET') {
      return okResp(clone(ws.skills));
    }
    if (subPath === '/skills' && method === 'POST') {
      const name = String(body['name'] || '').trim();
      const code = String(body['code'] || '').trim() || `custom-skill-${Date.now().toString(36)}`;
      if (!name) return errResp(400, 'VALIDATION_ERROR', '技能名称不能为空');
      const source = (body['source'] as WorkspaceSkill['source']) || 'local_import';
      const version = String(body['version'] || 'v1.0.0');
      const promptTemplate = String(body['promptTemplate'] || '# Skill Instructions');
      const sourceType = source === 'zip_archive' ? 'zip' : source === 'markdown_file' ? 'markdown' : source === 'git_synced' ? 'git' : 'manual';
      const newSkill: WorkspaceSkill = {
        id: `sk-${Date.now()}`,
        name,
        code,
        version,
        source,
        gitRepoUrl: body['gitRepoUrl'] ? String(body['gitRepoUrl']) : undefined,
        gitBranch: body['gitBranch'] ? String(body['gitBranch']) : undefined,
        gitCommit: source === 'git_synced' ? String(body['gitCommit'] || Math.random().toString(16).slice(2, 9)) : undefined,
        importedFileName: body['importedFileName'] ? String(body['importedFileName']) : undefined,
        description: String(body['description'] || ''),
        promptTemplate,
        requiredToolTags: Array.isArray(body['requiredToolTags']) ? body['requiredToolTags'].map(String) : ['git', 'docs'],
        boundEmployeesCount: 0,
        enabled: true,
        createdAt: nowFullTimestamp().slice(0, 10),
        historyVersions: [
          {
            version,
            commitHash: source === 'git_synced' ? String(body['gitCommit'] || '7a9e10c') : undefined,
            sourceType,
            changeLog: String(body['changeLog'] || `初始化导入 (${source})`),
            promptTemplate,
            updatedAt: nowFullTimestamp().slice(0, 16)
          }
        ]
      };
      ws.skills.unshift(newSkill);
      return okResp(clone(newSkill), 201);
    }
    const skillMatch = /^\/skills\/([^/]+)$/.exec(subPath);
    if (skillMatch && skillMatch[1]) {
      const skId = decodeURIComponent(skillMatch[1]);
      const skill = ws.skills.find(s => s.id === skId);
      if (!skill) return errResp(404, 'SKILL_NOT_FOUND', '未找到指定 Skill');
      if (method === 'PATCH') {
        if (typeof body['enabled'] === 'boolean') skill.enabled = body['enabled'];
        if (typeof body['name'] === 'string' && body['name'].trim()) skill.name = body['name'].trim();
        if (typeof body['code'] === 'string' && body['code'].trim()) skill.code = body['code'].trim();
        if (typeof body['description'] === 'string') skill.description = body['description'];
        if (typeof body['promptTemplate'] === 'string') skill.promptTemplate = body['promptTemplate'];
        if (Array.isArray(body['requiredToolTags'])) {
          skill.requiredToolTags = body['requiredToolTags'].map(t => String(t).trim()).filter(Boolean);
        }
        if (typeof body['gitRepoUrl'] === 'string') skill.gitRepoUrl = body['gitRepoUrl'];
        if (typeof body['gitBranch'] === 'string') skill.gitBranch = body['gitBranch'];
        if (typeof body['version'] === 'string') {
          skill.version = body['version'];
          const foundVer = skill.historyVersions.find(v => v.version === body['version']);
          if (foundVer && typeof body['promptTemplate'] !== 'string') {
            skill.promptTemplate = foundVer.promptTemplate;
            if (foundVer.commitHash) skill.gitCommit = foundVer.commitHash;
          }
        }
        if (typeof body['gitCommit'] === 'string') skill.gitCommit = body['gitCommit'];
        return okResp(clone(skill));
      }
    }
    const skillSyncMatch = /^\/skills\/([^/]+)\/sync-git$/.exec(subPath);
    if (skillSyncMatch && skillSyncMatch[1] && method === 'POST') {
      const skId = decodeURIComponent(skillSyncMatch[1]);
      const skill = ws.skills.find(s => s.id === skId);
      if (!skill) return errResp(404, 'SKILL_NOT_FOUND', '未找到指定 Skill');
      const nextPatch = skill.historyVersions.length + 1;
      const newVer = `v1.${nextPatch}.0`;
      const newCommit = Math.random().toString(16).slice(2, 9);
      skill.version = newVer;
      skill.gitCommit = newCommit;
      skill.historyVersions.unshift({
        version: newVer,
        commitHash: newCommit,
        sourceType: 'git',
        changeLog: `从 Git 分支 (${skill.gitBranch || 'main'}) 拉取最新提交 ${newCommit}`,
        promptTemplate: skill.promptTemplate,
        updatedAt: nowFullTimestamp().slice(0, 16)
      });
      return okResp(clone(skill));
    }

    // MCP Endpoints (aligned with control-plane/src/mcp-resources.ts: discover issues receipt -> save consumes { receipt, allowedTools })
    if (subPath === '/mcp-servers' && method === 'GET') {
      return okResp(clone(ws.mcpServers));
    }
    if (subPath === '/mcp-servers/discover' && method === 'POST') {
      const mcpId = typeof body['id'] === 'string' && body['id'].trim() ? body['id'].trim() : null;
      const name = String(body['name'] || '').trim() || 'MCP Server';
      const endpoint = String(body['endpoint'] || '').trim();
      const protocol = ((body['protocol'] as McpServerEndpoint['protocol']) || 'Streamable HTTP');
      const authType = ((body['authType'] as McpServerEndpoint['authType']) || 'no_auth');
      const tokenInput = typeof body['token'] === 'string' ? body['token'].trim() : '';
      const apiKeyName = typeof body['apiKeyName'] === 'string' ? body['apiKeyName'].trim() : undefined;
      const apiKeyValue = typeof body['apiKeyValue'] === 'string' ? body['apiKeyValue'].trim() : '';
      const customHeaders = Array.isArray(body['customHeaders']) ? (body['customHeaders'] as McpServerEndpoint['customHeaders']) : undefined;

      if (isMaskedDisplayValue(tokenInput) || isMaskedDisplayValue(apiKeyValue)) {
        return errResp(400, 'MASKED_SECRET_REJECTED', '请输入真实的新凭证或留空复用已保存凭证');
      }

      try {
        const parsed = new URL(endpoint);
        if (parsed.protocol !== 'http:' && parsed.protocol !== 'https:') {
          return errResp(400, 'INVALID_ENDPOINT', 'MCP 端点必须使用 http 或 https 协议');
        }
        const hostLower = parsed.hostname.toLowerCase();
        if (!hostLower || hostLower.endsWith('.invalid') || hostLower.includes('unreachable') || hostLower.includes('badhost')) {
          return errResp(400, 'MCP_DISCOVERY_FAILED', `MCP 连接或工具发现失败：目标主机 ${parsed.host} 不可达`);
        }
      } catch {
        return errResp(400, 'INVALID_ENDPOINT', `MCP 端点格式无效：${endpoint || '空'}`);
      }

      const existingSecret = mcpId ? (ws.mcpSecrets[mcpId] || '') : '';
      const rawSecret = authType === 'no_auth' ? '' : (tokenInput || apiKeyValue || existingSecret);
      if (authType === 'bearer_token' && !rawSecret) {
        return errResp(400, 'MCP_AUTH_REQUIRED', 'Bearer Token 认证模式下请填写有效的 Token');
      }

      const lower = endpoint.toLowerCase();
      let tools: McpToolSchema[];
      if (lower.includes('github') || lower.includes('git')) {
        tools = [
          { name: 'list_pull_requests', description: '查询仓库 Pull Request 列表与变更统计', riskLevel: 'safe' },
          { name: 'get_pr_diff', description: '拉取指定 PR 的文件差异 Diff', riskLevel: 'safe' },
          { name: 'post_review_comment', description: '向 PR 发表代码审查行级评论', riskLevel: 'medium' }
        ];
      } else if (lower.includes('postgres') || lower.includes('mysql') || lower.includes('sql')) {
        tools = [
          { name: 'list_tables', description: '列出数据库只读副本表结构与索引', riskLevel: 'safe' },
          { name: 'run_readonly_sql', description: '执行受限 SELECT/EXPLAIN 只读查询', riskLevel: 'medium' }
        ];
      } else {
        tools = [
          { name: 'ping_health', description: '探测 MCP 工具服务运行健康度', riskLevel: 'safe' },
          { name: 'query_context', description: '检索远程业务上下文对象', riskLevel: 'safe' },
          { name: 'trigger_action', description: '发起受控写操作执行请求', riskLevel: 'high' }
        ];
      }

      const receipt = `rcpt_mcp_${Date.now().toString(36)}_${Math.random().toString(36).slice(2, 8)}`;
      const expiresAt = Date.now() + 300_000;
      this.mcpReceipts[receipt] = {
        wsId,
        id: mcpId,
        record: {
          name,
          endpoint,
          protocol,
          authType,
          apiKeyName,
          customHeaders,
          tools
        },
        secret: rawSecret,
        expiresAt
      };
      return okResp({ ok: true, receipt, tools, expiresAt, discoveredAt: nowFullTimestamp() });
    }
    if (subPath === '/mcp-servers' && method === 'POST') {
      const receiptId = typeof body['receipt'] === 'string' ? body['receipt'].trim() : '';
      const allowedTools = Array.isArray(body['allowedTools']) ? body['allowedTools'].map(String) : [];
      const enabled = body['enabled'] !== undefined ? Boolean(body['enabled']) : true;
      const r = this.mcpReceipts[receiptId];
      if (!r || r.wsId !== wsId || r.id !== null || r.expiresAt <= Date.now()) {
        return errResp(409, 'MCP_RECEIPT_INVALID', '连接测试已过期或尚未测试，请重新点击测试连接并探测工具');
      }
      if (allowedTools.length === 0 || allowedTools.some(t => !r.record.tools.some(rt => rt.name === t))) {
        return errResp(400, 'INVALID_MCP_TOOLS', '至少需勾选 1 个本次探测发现的有效工具');
      }
      delete this.mcpReceipts[receiptId];

      const newId = `mcp-${Date.now()}`;
      ws.mcpSecrets[newId] = r.secret;
      const created: McpServerEndpoint = {
        id: newId,
        name: r.record.name,
        endpoint: r.record.endpoint,
        protocol: r.record.protocol,
        authType: r.record.authType,
        tokenMasked: r.secret ? `mcp-••••${r.secret.slice(-4)}` : '无认证 (No Auth)',
        apiKeyName: r.record.apiKeyName,
        customHeaders: r.record.customHeaders,
        status: 'connected',
        tools: clone(r.record.tools),
        allowedTools: [...new Set(allowedTools)],
        whitelistMatched: true,
        enabled
      };
      ws.mcpServers.unshift(created);
      return okResp(clone(created), 201);
    }
    const mcpSubActionMatch = /^\/mcp-servers\/([^/]+)\/(tools|status)$/.exec(subPath);
    if (mcpSubActionMatch && mcpSubActionMatch[1] && mcpSubActionMatch[2] && method === 'PATCH') {
      const mcpId = decodeURIComponent(mcpSubActionMatch[1]);
      const action = mcpSubActionMatch[2];
      const target = ws.mcpServers.find(m => m.id === mcpId);
      if (!target) return errResp(404, 'MCP_NOT_FOUND', '未找到指定 MCP 服务');
      if (action === 'tools') {
        const allowedTools = Array.isArray(body['allowedTools']) ? body['allowedTools'].map(String) : [];
        if (allowedTools.some(t => !target.tools.some(rt => rt.name === t))) {
          return errResp(400, 'INVALID_MCP_TOOLS', '提交了未在该 MCP 服务端点中发现的未知工具');
        }
        target.allowedTools = [...new Set(allowedTools)];
      } else if (action === 'status') {
        target.enabled = Boolean(body['enabled']);
      }
      return okResp(clone(target));
    }
    const mcpMatch = /^\/mcp-servers\/([^/]+)$/.exec(subPath);
    if (mcpMatch && mcpMatch[1]) {
      const mcpId = decodeURIComponent(mcpMatch[1]);
      const target = ws.mcpServers.find(m => m.id === mcpId);
      if (!target) return errResp(404, 'MCP_NOT_FOUND', '未找到指定 MCP 服务');
      if (method === 'PUT') {
        const receiptId = typeof body['receipt'] === 'string' ? body['receipt'].trim() : '';
        const allowedTools = Array.isArray(body['allowedTools']) ? body['allowedTools'].map(String) : [];
        const r = this.mcpReceipts[receiptId];
        if (!r || r.wsId !== wsId || r.id !== mcpId || r.expiresAt <= Date.now()) {
          return errResp(409, 'MCP_RECEIPT_INVALID', '连接测试结果已过期或尚未测试，请先点击测试连接并探测工具');
        }
        if (allowedTools.length === 0 || allowedTools.some(t => !r.record.tools.some(rt => rt.name === t))) {
          return errResp(400, 'INVALID_MCP_TOOLS', '授权工具列表不合法或未选择任何工具');
        }
        delete this.mcpReceipts[receiptId];
        ws.mcpSecrets[mcpId] = r.secret;
        target.name = r.record.name;
        target.endpoint = r.record.endpoint;
        target.protocol = r.record.protocol;
        target.authType = r.record.authType;
        target.apiKeyName = r.record.apiKeyName;
        target.customHeaders = r.record.customHeaders;
        target.tools = clone(r.record.tools);
        target.allowedTools = [...new Set(allowedTools)];
        target.tokenMasked = r.secret ? `mcp-••••${r.secret.slice(-4)}` : '无认证 (No Auth)';
        target.status = 'connected';
        if (typeof body['enabled'] === 'boolean') target.enabled = body['enabled'];
        return okResp(clone(target));
      }
    }

    // Directory Sync & Users & WeCom OTP & Email Invitations
    if (subPath === '/directory/sync-sources' && method === 'GET') {
      return okResp({
        syncStatus: clone(ws.directorySyncStatus),
        sources: clone(ws.channelSyncSources),
        departments: clone(ws.directoryDepartments)
      });
    }
    const syncJobMatch = /^\/directory\/sync-jobs\/([^/]+)$/.exec(subPath);
    if (syncJobMatch && syncJobMatch[1] && method === 'GET') {
      const jobId = decodeURIComponent(syncJobMatch[1]);
      const job = ws.directorySyncJobs[jobId];
      if (!job) return errResp(404, 'SYNC_JOB_NOT_FOUND', `未找到同步任务 ${jobId}`);
      return okResp(clone(job));
    }
    if (subPath === '/directory/sync' && method === 'POST') {
      const sourceId = body['sourceId'] ? String(body['sourceId']) : '';
      const connectionId = body['connectionId'] ? String(body['connectionId']) : '';
      const simulateFailure = body['simulateFailure'] === true;
      const targetSources = sourceId
        ? ws.channelSyncSources.filter(s => s.id === sourceId)
        : connectionId
          ? ws.channelSyncSources.filter(s => s.connectionId === connectionId)
          : ws.channelSyncSources;
      if (targetSources.length === 0) return errResp(404, 'SOURCE_NOT_FOUND', '未找到指定同步源');

      const startedAt = nowFullTimestamp();
      const jobs: DirectorySyncJob[] = [];

      if (simulateFailure) {
        for (const src of targetSources) {
          const syncJobId = `syncjob_fail_${src.channel}_${Date.now().toString(36)}_${Math.random().toString(36).slice(2, 5)}`;
          src.status = 'error';
          src.progressPercent = 0;
          src.lastError = '同步失败：平台接口限流或超时，已保留原有通讯录数据';
          src.history.unshift({
            syncId: syncJobId,
            trigger: 'manual',
            time: startedAt.slice(5, 16),
            duration: '0.9s',
            nodesSynced: 0,
            status: 'failed',
            detail: src.lastError
          });
          const failJob: DirectorySyncJob = {
            syncJobId,
            sourceId: src.id,
            connectionId: src.connectionId,
            channel: src.channel,
            status: 'failed',
            progressPercent: 0,
            processedNodes: 0,
            totalNodes: src.snapshotNodeCount || src.totalMembers,
            snapshotVersion: src.lastSuccessfulSnapshot?.snapshotVersion || src.snapshotVersion,
            preservedPreviousSnapshot: true,
            error: src.lastError,
            startedAt,
            finishedAt: startedAt
          };
          ws.directorySyncJobs[syncJobId] = failJob;
          jobs.push(clone(failJob));
        }
        ws.directorySyncStatus.status = 'error';
        ws.directorySyncStatus.errorCount += 1;
        ws.directorySyncStatus.errorLogs.unshift({
          time: nowTimeLabel(),
          message: `${targetSources[0]!.channelName} 同步失败，已保留上次同步的数据`,
          channel: targetSources[0]!.channel
        });
        return okResp({
          syncJobId: jobs[0]!.syncJobId,
          jobs,
          syncStatus: clone(ws.directorySyncStatus),
          sources: clone(ws.channelSyncSources),
          preservedPreviousSnapshot: true
        });
      }

      for (const src of targetSources) {
        const syncJobId = `syncjob_${src.channel}_${Date.now().toString(36)}_${Math.random().toString(36).slice(2, 5)}`;
        src.status = 'syncing';
        src.progressPercent = 25;
        src.lastError = undefined;
        const job: DirectorySyncJob = {
          syncJobId,
          sourceId: src.id,
          connectionId: src.connectionId,
          channel: src.channel,
          status: 'accepted',
          progressPercent: 25,
          processedNodes: Math.floor((src.snapshotNodeCount || src.totalMembers) * 0.25),
          totalNodes: src.snapshotNodeCount || src.totalMembers,
          startedAt
        };
        ws.directorySyncJobs[syncJobId] = job;
        jobs.push(clone(job));
        this.timers.push({ kind: 'directory_sync', wsId, targetId: src.id, createdAtMs: Date.now(), opId: syncJobId });
      }
      ws.directorySyncStatus.status = 'syncing';
      ws.directorySyncStatus.lastSyncedAt = '刚刚 (同步中...)';
      return okResp({
        syncJobId: jobs[0]!.syncJobId,
        jobs,
        syncStatus: clone(ws.directorySyncStatus),
        sources: clone(ws.channelSyncSources)
      }, 202);
    }
    if (subPath === '/directory/users' && method === 'GET') {
      return okResp(clone(ws.directoryUsers));
    }

    // Connection-scoped snapshot queries (Feishu & DingTalk) & live search (WeCom) & Web directory
    const snapshotQueryMatch = /^\/channels\/(feishu|dingtalk)\/([^/]+)\/directory\/snapshot$/.exec(subPath);
    if (snapshotQueryMatch && snapshotQueryMatch[1] && snapshotQueryMatch[2] && method === 'GET') {
      const plat = snapshotQueryMatch[1] as 'feishu' | 'dingtalk';
      const connectionId = decodeURIComponent(snapshotQueryMatch[2]);
      const q = (parsedUrl.searchParams.get('q') || '').trim().toLowerCase();
      const rawCursor = parsedUrl.searchParams.get('cursor') || '';
      let requestedSnapVer = (parsedUrl.searchParams.get('snapshotVersion') || '').trim();
      let page = Math.max(1, Number(parsedUrl.searchParams.get('page') || 1));
      const pageSize = Math.min(50, Math.max(1, Number(parsedUrl.searchParams.get('pageSize') || 10)));

      if (rawCursor) {
        const parts = rawCursor.split('::');
        if (parts.length === 2 && parts[0] && !Number.isNaN(Number(parts[1]))) {
          requestedSnapVer = requestedSnapVer || parts[0];
          page = Math.max(1, Math.floor(Number(parts[1]) / pageSize) + 1);
        }
      }

      let latestSnap = ws.directorySnapshots[connectionId];
      const hasBoundChannel = ws.employees.some(e => e.channels.some(c => c.platform === plat && c.connectionId === connectionId));
      const syncSrc = ws.channelSyncSources.find(s => s.channel === plat && (s.connectionId === connectionId || hasBoundChannel));
      if (!latestSnap && (hasBoundChannel || syncSrc)) {
        const snapVer = syncSrc?.lastSuccessfulSnapshot?.snapshotVersion || syncSrc?.snapshotVersion || `snap-${plat}-init`;
        latestSnap = {
          snapshotVersion: snapVer,
          syncedAt: syncSrc?.lastSuccessfulSnapshot?.syncedAt || nowFullTimestamp(),
          users: clone(ws.directoryUsers.filter(u => u.platform === plat)).map(u => {
            const targetId = u.refId || u.id;
            const principal: PrincipalRef = { targetType: 'user', channel: plat, connectionId, targetId, displayName: u.name };
            principal.canonicalKey = buildCanonicalPrincipalKey(principal);
            return { ...u, connectionId, sourceType: plat, principal };
          }),
          departments: clone(ws.directoryDepartments.filter(d => d.platform === plat)).map(d => {
            const targetId = d.refId || d.id;
            const principal: PrincipalRef = { targetType: 'department', channel: plat, connectionId, targetId, displayName: d.name };
            principal.canonicalKey = buildCanonicalPrincipalKey(principal);
            return { ...d, connectionId, sourceType: plat, principal };
          })
        };
        ws.directorySnapshots[connectionId] = latestSnap;
        ws.directorySnapshotVersions[connectionId] = ws.directorySnapshotVersions[connectionId] || {};
        ws.directorySnapshotVersions[connectionId]![snapVer] = clone(latestSnap);
      }
      if (!latestSnap) {
        return errResp(404, 'CONNECTION_SNAPSHOT_NOT_FOUND', `未找到该渠道连接的通讯录数据`);
      }

      // Pin query to requested immutable snapshotVersion if available so concurrent sync never duplicates/skips pages
      const versionMap = ws.directorySnapshotVersions[connectionId] || {};
      const snap = (requestedSnapVer && versionMap[requestedSnapVer]) ? versionMap[requestedSnapVer]! : latestSnap;

      const filteredUsers = q
        ? snap.users.filter(u =>
            u.name.toLowerCase().includes(q) ||
            u.department.toLowerCase().includes(q) ||
            u.email.toLowerCase().includes(q) ||
            (u.refId || '').toLowerCase().includes(q)
          )
        : snap.users;
      const filteredDepts = q
        ? snap.departments.filter(d => d.name.toLowerCase().includes(q) || (d.refId || '').toLowerCase().includes(q))
        : snap.departments;

      const offset = (page - 1) * pageSize;
      const pagedUsers = filteredUsers.slice(offset, offset + pageSize).map(u => {
        const targetId = u.refId || u.id;
        const principal: PrincipalRef = u.principal || { targetType: 'user', channel: plat, connectionId, targetId, displayName: u.name };
        principal.canonicalKey = principal.canonicalKey || buildCanonicalPrincipalKey(principal);
        return { ...u, connectionId, sourceType: plat, principal };
      });
      const pinnedDepts = filteredDepts.map(d => {
        const targetId = d.refId || d.id;
        const principal: PrincipalRef = d.principal || { targetType: 'department', channel: plat, connectionId, targetId, displayName: d.name };
        principal.canonicalKey = principal.canonicalKey || buildCanonicalPrincipalKey(principal);
        return { ...d, connectionId, sourceType: plat, principal };
      });
      const nextOffset = offset + pageSize;
      const nextCursor = nextOffset < filteredUsers.length ? `${snap.snapshotVersion}::${nextOffset}` : undefined;

      return okResp({
        sourceType: plat,
        connectionId,
        channelName: plat === 'feishu' ? '飞书通讯录' : '钉钉通讯录',
        queryMode: 'snapshot',
        snapshotVersion: snap.snapshotVersion,
        snapshotSyncedAt: snap.syncedAt,
        snapshotFallbackUsed: Boolean(syncSrc?.lastError),
        lastSyncError: syncSrc?.lastError,
        page,
        pageSize,
        cursor: rawCursor || `${snap.snapshotVersion}::${offset}`,
        nextCursor,
        totalUsers: filteredUsers.length,
        totalDepartments: pinnedDepts.length,
        users: clone(pagedUsers),
        departments: clone(pinnedDepts)
      });
    }

    const wecomSearchMatch = /^\/channels\/wecom\/([^/]+)\/directory\/search$/.exec(subPath);
    if (wecomSearchMatch && wecomSearchMatch[1] && method === 'GET') {
      const connectionId = decodeURIComponent(wecomSearchMatch[1]);
      const q = (parsedUrl.searchParams.get('q') || '').trim().toLowerCase();
      const page = Math.max(1, Number(parsedUrl.searchParams.get('page') || 1));
      const pageSize = Math.min(50, Math.max(1, Number(parsedUrl.searchParams.get('pageSize') || 10)));

      const hasWecomConn = ws.channelSyncSources.some(s => s.channel === 'wecom' && s.connectionId === connectionId)
        || ws.employees.some(e => e.channels.some(c => c.platform === 'wecom' && c.connectionId === connectionId));
      if (!hasWecomConn) {
        return errResp(404, 'WECOM_CONNECTION_NOT_FOUND', `未找到企微机器人连接 (${connectionId})`);
      }

      const rawUsers = ws.directoryUsers.filter(u => u.platform === 'wecom').map(u => {
        const verified = Boolean(ws.wecomScopedBindings[`${connectionId}::${u.id}`]);
        const targetId = u.refId || u.id;
        const principal: PrincipalRef = { targetType: 'user', channel: 'wecom', connectionId, targetId, displayName: u.name };
        principal.canonicalKey = buildCanonicalPrincipalKey(principal);
        return {
          ...u,
          sourceType: 'wecom' as const,
          connectionId,
          isWecomVerified: verified,
          bindingStatus: (verified ? 'verified' : 'pending_binding') as 'verified' | 'pending_binding',
          principal
        };
      });
      const filteredUsers = q
        ? rawUsers.filter(u =>
            u.name.toLowerCase().includes(q) ||
            u.department.toLowerCase().includes(q) ||
            (u.corpUserId || '').toLowerCase().includes(q) ||
            u.email.toLowerCase().includes(q) ||
            (u.refId || '').toLowerCase().includes(q)
          )
        : rawUsers;
      const offset = (page - 1) * pageSize;
      const nextCursor = offset + pageSize < filteredUsers.length ? `wecom::${connectionId}::${offset + pageSize}` : undefined;

      return okResp({
        sourceType: 'wecom',
        connectionId,
        channelName: '企业微信智能机器人实时搜索',
        queryMode: 'live_search',
        page,
        pageSize,
        cursor: `wecom::${connectionId}::${offset}`,
        nextCursor,
        totalUsers: filteredUsers.length,
        totalDepartments: 0,
        users: clone(filteredUsers.slice(offset, offset + pageSize)),
        departments: []
      });
    }

    if (subPath === '/directory/web/members' && method === 'GET') {
      const q = (parsedUrl.searchParams.get('q') || '').trim().toLowerCase();
      const page = Math.max(1, Number(parsedUrl.searchParams.get('page') || 1));
      const pageSize = Math.min(50, Math.max(1, Number(parsedUrl.searchParams.get('pageSize') || 10)));
      const allWebUsers = ws.directoryUsers.filter(u => u.platform === 'web').map(u => {
        const principal: PrincipalRef = u.principal || { targetType: 'user', channel: 'web', connectionId: 'ws-web-directory', targetId: u.refId || u.id, displayName: u.name };
        principal.canonicalKey = principal.canonicalKey || buildCanonicalPrincipalKey(principal);
        return { ...u, connectionId: 'ws-web-directory', sourceType: 'web' as const, principal };
      });
      const allWebDepts = ws.directoryDepartments.filter(d => d.platform === 'web').map(d => {
        const principal: PrincipalRef = d.principal || { targetType: 'department', channel: 'web', connectionId: 'ws-web-directory', targetId: d.refId || d.id, displayName: d.name };
        principal.canonicalKey = principal.canonicalKey || buildCanonicalPrincipalKey(principal);
        return { ...d, connectionId: 'ws-web-directory', sourceType: 'web' as const, principal };
      });
      const filteredUsers = q
        ? allWebUsers.filter(u => u.name.toLowerCase().includes(q) || u.department.toLowerCase().includes(q) || u.email.toLowerCase().includes(q))
        : allWebUsers;
      const filteredDepts = q
        ? allWebDepts.filter(d => d.name.toLowerCase().includes(q))
        : allWebDepts;
      const offset = (page - 1) * pageSize;
      const nextCursor = offset + pageSize < filteredUsers.length ? `web::ws-web-directory::${offset + pageSize}` : undefined;
      return okResp({
        sourceType: 'web',
        connectionId: 'ws-web-directory',
        channelName: '工作区网页组织名录',
        queryMode: 'workspace_directory',
        page,
        pageSize,
        cursor: `web::ws-web-directory::${offset}`,
        nextCursor,
        totalUsers: filteredUsers.length,
        totalDepartments: filteredDepts.length,
        users: clone(filteredUsers.slice(offset, offset + pageSize)),
        departments: clone(filteredDepts)
      });
    }
    const userPermMatch = /^\/directory\/users\/([^/]+)\/permissions$/.exec(subPath);
    if (userPermMatch && userPermMatch[1] && method === 'GET') {
      const uId = decodeURIComponent(userPermMatch[1]);
      return okResp(this.computeEmployeePermissionsForUser(ws, uId));
    }
    // Scoped WeCom verification (strictly bound to workspace + connectionId + botId + userId; requires explicit connection scope or verifySessionId)
    if (subPath === '/wecom/verify-code' && method === 'POST') {
      const userId = String(body['userId'] || '').trim();
      const employeeId = typeof body['employeeId'] === 'string' && body['employeeId'].trim() ? body['employeeId'].trim() : undefined;
      let connectionId = typeof body['connectionId'] === 'string' && body['connectionId'].trim() ? body['connectionId'].trim() : undefined;
      let botId = typeof body['botId'] === 'string' && body['botId'].trim() ? body['botId'].trim() : undefined;

      const u = ws.directoryUsers.find(item => (item.id === userId || item.refId === userId) && item.platform === 'wecom');
      if (!u) return errResp(404, 'USER_NOT_FOUND', '未找到企微目录成员');

      if (!connectionId && employeeId) {
        const emp = ws.employees.find(e => e.id === employeeId);
        const wecomCh = emp?.channels.find(c => c.platform === 'wecom');
        if (!wecomCh || !wecomCh.connectionId) {
          return errResp(400, 'MISSING_WECOM_CONNECTION', '当前 AI 员工尚未绑定企业微信智能机器人连接，无法发起人机身份核验');
        }
        connectionId = wecomCh.connectionId;
        botId = botId || wecomCh.botId;
      }
      if (!connectionId) {
        return errResp(400, 'MISSING_CONNECTION_SCOPE', '发起企微绑定验证码必须明确指定 connectionId 或所属已绑定的 employeeId，禁止按 userId 隐式推断');
      }
      const matchedSyncSrc = ws.channelSyncSources.find(s => s.channel === 'wecom' && (s.connectionId === connectionId || s.id === connectionId));
      const resolvedConnId = matchedSyncSrc?.connectionId || connectionId;
      botId = botId || matchedSyncSrc?.appIdOrBotId || 'bot_wecom_sec_002';

      const scopeKey = `${resolvedConnId}::${u.id}`;
      const verifySessionId = `wc_vs_${resolvedConnId}_${u.id}_${Date.now().toString(36)}`;
      const code = String(Math.floor(100000 + Math.random() * 900000));
      const sess = {
        verifySessionId,
        workspaceId: wsId,
        employeeId,
        connectionId: resolvedConnId,
        botId,
        userId: u.id,
        userName: u.name,
        corpUserId: u.corpUserId || `wecom_${u.id}`,
        code,
        expiresInSeconds: 300,
        expiresAt: nowTimeLabel(),
        status: 'pending' as const
      };
      ws.wecomVerifySessions[verifySessionId] = sess;
      ws.wecomVerifySessions[scopeKey] = sess;
      this.timers.push({ kind: 'wecom_verify', wsId, targetId: verifySessionId, createdAtMs: Date.now() });
      return okResp(clone(sess), 201);
    }
    const wecomSessionPollMatch = /^\/wecom\/verify-code\/sessions\/([^/]+)$/.exec(subPath);
    if (wecomSessionPollMatch && wecomSessionPollMatch[1] && method === 'GET') {
      const sId = decodeURIComponent(wecomSessionPollMatch[1]);
      const sess = ws.wecomVerifySessions[sId];
      if (!sess) return errResp(404, 'SESSION_NOT_FOUND', `未找到企微验证会话 (${sId})`);
      return okResp(clone(sess));
    }
    const wecomSessionSimMatch = /^\/wecom\/verify-code\/sessions\/([^/]+)\/simulate$/.exec(subPath);
    if (wecomSessionSimMatch && wecomSessionSimMatch[1] && method === 'POST') {
      const sId = decodeURIComponent(wecomSessionSimMatch[1]);
      const sess = ws.wecomVerifySessions[sId];
      if (!sess) return errResp(404, 'SESSION_NOT_FOUND', `未找到企微验证会话 (${sId})`);
      sess.status = 'verified';
      sess.verifiedAt = nowFullTimestamp();
      const scopeKey = `${sess.connectionId}::${sess.userId}`;
      this.markScopedWecomVerified(ws, scopeKey, sess.userId, sess.connectionId);
      const u = ws.directoryUsers.find(item => item.id === sess.userId);
      return okResp({ ok: true, session: clone(sess), user: u ? clone(u) : null });
    }
    const wecomPollMatch = /^\/wecom\/verify-code\/([^/]+)$/.exec(subPath);
    if (wecomPollMatch && wecomPollMatch[1] && method === 'GET') {
      const uId = decodeURIComponent(wecomPollMatch[1]);
      const connectionId = (parsedUrl.searchParams.get('connectionId') || '').trim();
      if (!connectionId) {
        return errResp(400, 'MISSING_CONNECTION_SCOPE', '查询企微验证码状态必须提供 connectionId 或通过 /sessions/:verifySessionId 查询，禁止跨连接推断');
      }
      const sess = ws.wecomVerifySessions[`${connectionId}::${uId}`];
      if (!sess) return errResp(404, 'SESSION_NOT_FOUND', `未找到连接 ${connectionId} 下成员 ${uId} 的验证码会话`);
      return okResp(clone(sess));
    }
    // Dev-only simulated inbound WeCom DM webhook callback (strictly requires verifySessionId OR connectionId scope)
    const wecomSimMatch = /^\/wecom\/verify-code\/([^/]+)\/simulate$|^\/_mock\/wecom-dm-callback$/.exec(subPath);
    if (wecomSimMatch && method === 'POST') {
      const verifySessionId = typeof body['verifySessionId'] === 'string' ? body['verifySessionId'].trim() : '';
      if (verifySessionId && ws.wecomVerifySessions[verifySessionId]) {
        const sess = ws.wecomVerifySessions[verifySessionId]!;
        sess.status = 'verified';
        sess.verifiedAt = nowFullTimestamp();
        this.markScopedWecomVerified(ws, `${sess.connectionId}::${sess.userId}`, sess.userId, sess.connectionId);
        const u = ws.directoryUsers.find(item => item.id === sess.userId);
        return okResp({ ok: true, session: clone(sess), user: u ? clone(u) : null });
      }
      const uId = wecomSimMatch[1] ? decodeURIComponent(wecomSimMatch[1]) : String(body['userId'] || '');
      const connectionId = typeof body['connectionId'] === 'string' && body['connectionId'].trim() ? body['connectionId'].trim() : '';
      if (!connectionId) {
        return errResp(400, 'MISSING_CONNECTION_SCOPE', '企微入站回调验证必须携带 connectionId 或 verifySessionId');
      }
      const scopeKey = `${connectionId}::${uId}`;
      const sess = ws.wecomVerifySessions[scopeKey];
      if (!sess) return errResp(404, 'SESSION_NOT_FOUND', `连接 ${connectionId} 作用域下不存在待核验会话`);
      sess.status = 'verified';
      sess.verifiedAt = nowFullTimestamp();
      this.markScopedWecomVerified(ws, scopeKey, uId, sess.connectionId);
      const u = ws.directoryUsers.find(item => item.id === uId);
      return okResp({ ok: true, session: clone(sess), user: u ? clone(u) : null });
    }
    if (subPath === '/invitations/email' && method === 'GET') {
      return okResp(clone(ws.emailInvitations));
    }
    if (subPath === '/invitations/email' && method === 'POST') {
      const email = String(body['email'] || '').trim();
      const name = String(body['name'] || '').trim();
      const targetEmployeeIds = Array.isArray(body['targetEmployeeIds']) ? body['targetEmployeeIds'].map(String) : [];
      if (!email || !email.includes('@')) return errResp(400, 'INVALID_EMAIL', '请输入合法的企业邮箱地址');
      const created = {
        id: `inv-${Date.now()}`,
        email,
        name: name || email.split('@')[0] || '受邀成员',
        targetEmployeeIds,
        invitedAt: nowFullTimestamp().slice(0, 16),
        expiresAt: '7 天后到期',
        status: 'pending' as const
      };
      ws.emailInvitations.unshift(created);
      return okResp(clone(created), 201);
    }
    const invRevokeMatch = /^\/invitations\/email\/([^/]+)\/revoke$/.exec(subPath);
    if (invRevokeMatch && invRevokeMatch[1] && method === 'POST') {
      const invId = decodeURIComponent(invRevokeMatch[1]);
      const inv = ws.emailInvitations.find(i => i.id === invId);
      if (!inv) return errResp(404, 'INVITE_NOT_FOUND', '未找到邮箱邀请记录');
      inv.status = 'revoked';
      return okResp(clone(inv));
    }

    return null;
  }

  private computeEmployeePermissionsForUser(ws: WorkspaceState, userId: string): UserEmployeePermissionSummary[] {
    const targetUser = ws.directoryUsers.find(u => u.id === userId || u.refId === userId);
    const matchesTarget = (ids: string[], targets?: PrincipalRef[]) => {
      if (ids.includes(userId)) return true;
      if (targetUser) {
        const canonical = buildCanonicalPrincipalKey({
          targetType: 'user',
          channel: targetUser.platform,
          connectionId: targetUser.connectionId || (targetUser.platform === 'web' ? 'ws-web-directory' : `conn-${targetUser.platform}`),
          targetId: targetUser.refId || targetUser.id
        });
        if (ids.includes(canonical)) return true;
        if (targets?.some(t =>
          t.canonicalKey === canonical ||
          (t.targetType === 'user' && t.channel === targetUser.platform && (!t.connectionId || !targetUser.connectionId || t.connectionId === targetUser.connectionId) && (t.targetId === targetUser.refId || t.targetId === targetUser.id))
        )) {
          return true;
        }
      }
      return false;
    };
    const result: UserEmployeePermissionSummary[] = [];
    for (const emp of ws.employees) {
      let hasAccess = false;
      let accessType = '未授权';
      if (emp.unifiedAccess.mode === 'all_with_blacklist') {
        if (!matchesTarget(emp.unifiedAccess.blacklistMemberIds, emp.unifiedAccess.blacklistTargets)) {
          hasAccess = true;
          accessType = '工作区全员 (默认可用)';
        }
      } else if (
        matchesTarget(emp.unifiedAccess.whitelistMemberIds, emp.unifiedAccess.whitelistTargets) &&
        !matchesTarget(emp.unifiedAccess.blacklistMemberIds, emp.unifiedAccess.blacklistTargets)
      ) {
        hasAccess = true;
        accessType = '白名单指定成员';
      }
      const isApprover = emp.humanConfirmationRules?.some(r => r.target?.userId === userId) || false;
      if (hasAccess || isApprover) {
        result.push({ employeeId: emp.id, employeeName: emp.name, permissionType: accessType, isApprover });
      }
    }
    return result;
  }

  private validateAndApplyChannelConfigs(
    ws: WorkspaceState,
    emp: DigitalEmployee,
    rawChannels: unknown[]
  ): { ok: true; data: EmployeeChannelBinding[] } | { ok: false; error: MockHttpResponse } {
    const updatedChannels: EmployeeChannelBinding[] = clone(emp.channels);
    for (const rawItem of rawChannels) {
      if (!rawItem || typeof rawItem !== 'object') {
        return { ok: false, error: errResp(400, 'VALIDATION_ERROR', '渠道配置项格式无效') };
      }
      const item = rawItem as Record<string, unknown>;
      for (const k of FORBIDDEN_CHANNEL_WRITE_KEYS) {
        if (k in item && item[k] !== undefined) {
          return { ok: false, error: errResp(400, 'UNEDITABLE_SERVER_FIELDS', `保存接口不接受服务端管理字段 "${k}"，请仅提交用户可编辑配置`) };
        }
      }
      const platform = item['platform'] as PlatformType;
      const existingIdx = updatedChannels.findIndex(c => c.platform === platform);
      if (existingIdx === -1) {
        return { ok: false, error: errResp(400, 'CHANNEL_NOT_BOUND', `渠道 ${platform} 尚未完成绑定，无法通过配置保存接口写入连接`) };
      }
      const existing = updatedChannels[existingIdx]!;
      const connId = typeof item['connectionId'] === 'string' && item['connectionId'] ? item['connectionId'] : existing.connectionId;

      if (item['usageScope'] && typeof item['usageScope'] === 'object') {
        existing.usageScope = clone(item['usageScope'] as EmployeeChannelBinding['usageScope']);
      }
      if (item['capabilities'] && typeof item['capabilities'] === 'object') {
        const capIn = item['capabilities'] as Record<string, unknown>;
        for (const k of FORBIDDEN_CAP_WRITE_KEYS) {
          if (k in capIn && capIn[k] !== undefined) {
            return { ok: false, error: errResp(400, 'UNEDITABLE_SERVER_FIELDS', `保存接口不接受渠道能力只读字段 "${k}"`) };
          }
        }
        if (typeof capIn['ordinaryGroupEnabled'] === 'boolean') existing.capabilities.ordinaryGroupEnabled = capIn['ordinaryGroupEnabled'];
        if (typeof capIn['keywordTriggersEnabled'] === 'boolean') existing.capabilities.keywordTriggersEnabled = capIn['keywordTriggersEnabled'];
        if (Array.isArray(capIn['keywords'])) existing.capabilities.keywords = clone(capIn['keywords'] as string[]);
        if (typeof capIn['regexMatchEnabled'] === 'boolean') existing.capabilities.regexMatchEnabled = capIn['regexMatchEnabled'];
        if (typeof capIn['regexPattern'] === 'string') existing.capabilities.regexPattern = capIn['regexPattern'];
        if (Array.isArray(capIn['feishuRules'])) existing.capabilities.feishuRules = clone(capIn['feishuRules'] as EmployeeChannelBinding['capabilities']['feishuRules']);
        if (typeof capIn['recentContextEnabled'] === 'boolean') existing.capabilities.recentContextEnabled = capIn['recentContextEnabled'];
        if (typeof capIn['recentContextWindow'] === 'number') existing.capabilities.recentContextWindow = capIn['recentContextWindow'];
      }
      if (Array.isArray(item['sharingRules'])) {
        const nextSharing: CrossGroupSharingRule[] = [];
        for (const rawSr of item['sharingRules']) {
          if (!rawSr || typeof rawSr !== 'object') continue;
          const sr = rawSr as Record<string, unknown>;
          for (const k of FORBIDDEN_SHARING_WRITE_KEYS) {
            if (k in sr && sr[k] !== undefined) {
              return { ok: false, error: errResp(400, 'UNEDITABLE_SERVER_FIELDS', `保存接口不接受共享规则运行态字段 "${k}"`) };
            }
          }
          const ruleId = String(sr['id'] || `cgr-${Date.now()}`);
          const prevRule = existing.sharingRules.find(r => r.id === ruleId);
          const rawTargetId = typeof sr['targetUserId'] === 'string' && sr['targetUserId'] ? sr['targetUserId'] : undefined;
          const targetUserRef = normalizePrincipalRef(sr['targetUser'], {
            targetType: 'user',
            channel: platform,
            connectionId: connId || '',
            displayName: typeof sr['targetUserLabel'] === 'string' ? sr['targetUserLabel'] : undefined
          }) || (rawTargetId && connId
            ? {
                targetType: 'user' as const,
                channel: platform,
                connectionId: connId,
                targetId: rawTargetId,
                displayName: typeof sr['targetUserLabel'] === 'string' ? sr['targetUserLabel'] : undefined,
                canonicalKey: buildCanonicalPrincipalKey({ targetType: 'user', channel: platform, connectionId: connId, targetId: rawTargetId })
              }
            : undefined);
          const targetUserId = targetUserRef?.targetId || rawTargetId;
          const targetConnId = targetUserRef?.connectionId || connId;
          let syncStatus: CrossGroupSharingRule['syncStatus'] = 'applied';
          let authErrorDetail: string | undefined = prevRule?.syncStatus === 'auth_error' ? prevRule.authErrorDetail : undefined;
          if (platform === 'wecom' && targetUserId) {
            const matchedUser = resolveUserByPrincipalOrId(ws, { channel: 'wecom', connectionId: targetConnId, targetId: targetUserId });
            const lookupId = matchedUser?.id || targetUserId;
            const isVerified = Boolean(targetConnId && (ws.wecomScopedBindings[`${targetConnId}::${lookupId}`] || ws.wecomScopedBindings[`${targetConnId}::${targetUserId}`]));
            if (!isVerified) {
              syncStatus = 'auth_error';
              authErrorDetail = '目标人员尚未完成当前企微连接的私聊验证码绑定，授权暂无法生效';
            } else {
              syncStatus = 'applied';
              authErrorDetail = undefined;
            }
          } else if (prevRule?.syncStatus === 'auth_error') {
            syncStatus = 'auth_error';
          }
          nextSharing.push({
            id: ruleId,
            name: String(sr['name'] || '').trim() || '未命名共享规则',
            mode: (sr['mode'] === 'interconnect' ? 'interconnect' : 'directed'),
            sourceGroups: Array.isArray(sr['sourceGroups']) ? clone(sr['sourceGroups'] as string[]) : [],
            targetGroups: Array.isArray(sr['targetGroups']) ? clone(sr['targetGroups'] as string[]) : [],
            targetUserId,
            targetUserLabel: typeof sr['targetUserLabel'] === 'string' ? sr['targetUserLabel'] : targetUserRef?.displayName,
            targetUser: targetUserRef,
            status: sr['status'] === 'disabled' ? 'disabled' : 'active',
            syncStatus,
            authErrorDetail,
            savedAt: nowFullTimestamp()
          });
        }
        existing.sharingRules = nextSharing;
      }
    }
    return { ok: true, data: updatedChannels };
  }

  private validateAndNormalizeTriggers(
    wsId: string,
    emp: DigitalEmployee,
    triggers: unknown[]
  ): { ok: true; data: EmployeeTrigger[] } | { ok: false; error: MockHttpResponse } {
    const normalized: EmployeeTrigger[] = [];
    const consumedDraftKeys: string[] = [];
    const validWebhookActions = new Set(['create_task', 'supplement_task', 'cancel_task']);
    for (const rawItem of triggers) {
      if (!rawItem || typeof rawItem !== 'object') {
        return { ok: false, error: errResp(400, 'VALIDATION_ERROR', '触发器配置项格式无效') };
      }
      const rawObj = rawItem as Record<string, unknown>;
      for (const k of FORBIDDEN_TRIGGER_WRITE_KEYS) {
        if (k in rawObj && rawObj[k] !== undefined) {
          return { ok: false, error: errResp(400, 'UNEDITABLE_SERVER_FIELDS', `保存接口不接受触发器运行态/脱敏字段 "${k}"，请仅提交可编辑配置`) };
        }
      }
      const trg = clone(rawItem as EmployeeTrigger);
      if (!trg.name || !trg.name.trim()) {
        return { ok: false, error: errResp(400, 'VALIDATION_ERROR', '触发器名称不能为空') };
      }
      if (trg.type !== 'schedule' && trg.type !== 'webhook' && trg.type !== 'github_issue') {
        return { ok: false, error: errResp(400, 'VALIDATION_ERROR', `触发器「${trg.name}」类型无效`) };
      }

      let normalizedAllowedActions: EmployeeTrigger['allowedActions'];
      if (trg.type === 'schedule' || trg.type === 'github_issue') {
        if (Array.isArray(trg.allowedActions)) {
          const invalidActions = trg.allowedActions.filter(a => a !== 'create_task');
          if (invalidActions.length > 0 || trg.allowedActions.length === 0) {
            return {
              ok: false,
              error: errResp(
                400,
                'INVALID_TRIGGER_ACTIONS',
                `${trg.type === 'schedule' ? '定时' : 'GitHub Issue'}触发器「${trg.name}」仅支持「创建任务 (create_task)」动作`
              )
            };
          }
        }
        normalizedAllowedActions = ['create_task'];
      } else {
        if (Array.isArray(trg.allowedActions)) {
          const deduped = Array.from(new Set(trg.allowedActions));
          if (deduped.length === 0 || deduped.some(a => !validWebhookActions.has(a))) {
            return {
              ok: false,
              error: errResp(400, 'INVALID_TRIGGER_ACTIONS', `Webhook 触发器「${trg.name}」允许的任务动作配置无效`)
            };
          }
          normalizedAllowedActions = deduped;
        } else {
          normalizedAllowedActions = ['create_task'];
        }
      }

      let nextCronPreviews: string[] | undefined;
      if (trg.type === 'schedule') {
        const cronExpr = typeof trg.cron === 'string' ? trg.cron.trim() : '';
        if (!cronExpr) {
          return { ok: false, error: errResp(400, 'INVALID_CRON', `定时触发器「${trg.name}」必须填写 Cron 表达式`) };
        }
        const tz = (typeof trg.timezone === 'string' && trg.timezone.trim()) ? trg.timezone.trim() : 'Asia/Shanghai';
        const cronRes = computeNextCronRuns(cronExpr, tz, 5);
        if (!cronRes.ok) {
          return { ok: false, error: errResp(400, 'INVALID_CRON', `定时触发器「${trg.name}」的 Cron 表达式或时区无效：${cronRes.message}`) };
        }
        trg.cron = cronExpr;
        trg.timezone = tz;
        nextCronPreviews = cronRes.previews;
      }
      const nextTargets: EmployeeTrigger['notificationTargets'] = [];
      for (const nt of trg.notificationTargets || []) {
        const ch = (nt.connectionId
          ? emp.channels.find(c => c.platform === nt.platform && c.connectionId === nt.connectionId)
          : undefined) || emp.channels.find(c => c.platform === nt.platform);
        const allItems = platformCandidateGroupItems[nt.platform] || [];
        const matchedOpt = nt.groupId
          ? allItems.find(g => g.groupId === nt.groupId && (!nt.connectionId || !g.connectionId || g.connectionId === nt.connectionId)) ||
            allItems.find(g => g.groupId === nt.groupId)
          : allItems.find(g => g.name === nt.groupName);
        const boundGrp = nt.groupId
          ? ch?.boundGroups.find(g => g.id === nt.groupId)
          : ch?.boundGroups.find(g => g.name === nt.groupName);
        if (!nt.groupId && !matchedOpt && !boundGrp) {
          return { ok: false, error: errResp(400, 'INVALID_TARGET_GROUP', `触发器「${trg.name}」的回告目标群无效或缺少群 ID 引用`) };
        }
        nextTargets.push({
          platform: nt.platform,
          connectionId: nt.connectionId || ch?.connectionId || matchedOpt?.connectionId || `conn-${nt.platform}`,
          groupId: nt.groupId || boundGrp?.id || matchedOpt?.groupId || `grp-${nt.platform}-unknown`,
          groupName: boundGrp?.name || matchedOpt?.name || nt.groupName
        });
      }
      const existing = emp.triggers.find(t => t.id === trg.id);
      const isWebhook = trg.type === 'webhook';
      const draftKey = `${wsId}::${emp.id}::${trg.id}`;
      const rawDraftToken = typeof rawObj['draftSecretToken'] === 'string' ? rawObj['draftSecretToken'].trim() : '';
      let webhookEndpoint = existing?.webhookEndpoint;
      let keyId = existing?.keyId;
      let secretMasked = existing?.secretMasked;
      if (isWebhook && !existing) {
        if (rawDraftToken) {
          const draftRec = this.draftTriggerSecrets[draftKey];
          if (!draftRec || draftRec.draftSecretToken !== rawDraftToken) {
            return {
              ok: false,
              error: errResp(400, 'INVALID_DRAFT_SECRET_TOKEN', `触发器「${trg.name}」的草稿 Webhook 密钥令牌无效或已过期，请重新生成密钥`)
            };
          }
          webhookEndpoint = draftRec.webhookEndpoint;
          keyId = draftRec.keyId;
          secretMasked = draftRec.secretMasked;
          consumedDraftKeys.push(draftKey);
        } else {
          if (this.draftTriggerSecrets[draftKey]) {
            consumedDraftKeys.push(draftKey);
          }
          const genHex = Math.random().toString(16).slice(2, 8);
          const genTail = Math.random().toString(16).slice(2, 6);
          webhookEndpoint = `https://hooks.kiko.corp/v1/wh/${emp.id}/${trg.id}`;
          keyId = `whk_${genHex}`;
          secretMasked = `whsec_••••••••${genTail}`;
        }
      } else if (isWebhook && existing) {
        webhookEndpoint = existing.webhookEndpoint || `https://hooks.kiko.corp/v1/wh/${emp.id}/${trg.id}`;
        keyId = existing.keyId || `whk_${trg.id.slice(-6)}`;
        secretMasked = existing.secretMasked || 'whsec_••••••••94f2';
      } else {
        webhookEndpoint = undefined;
        keyId = undefined;
        secretMasked = undefined;
      }

      normalized.push({
        id: trg.id,
        name: trg.name.trim(),
        type: trg.type,
        enabled: Boolean(trg.enabled),
        allowedActions: normalizedAllowedActions,
        cron: trg.cron,
        timezone: trg.timezone,
        description: trg.description || '',
        businessKeyMode: trg.businessKeyMode,
        businessKeyField: trg.businessKeyField,
        businessKeyPattern: trg.businessKeyPattern,
        githubEvents: trg.githubEvents ? clone(trg.githubEvents) : undefined,
        githubRepo: trg.githubRepo,
        githubIssueSyncMode: trg.githubIssueSyncMode,
        historyPolicy: trg.historyPolicy || 'isolated',
        notificationTargets: nextTargets,
        // Server-owned state preserved or bound from verified draft secret token
        webhookEndpoint,
        keyId,
        secretMasked,
        nextExecutionTimes: trg.type === 'schedule' ? (nextCronPreviews || existing?.nextExecutionTimes) : existing?.nextExecutionTimes,
        executionLogs: existing ? clone(existing.executionLogs) : []
      });
    }
    for (const dk of consumedDraftKeys) {
      delete this.draftTriggerSecrets[dk];
    }
    return { ok: true, data: normalized };
  }

  private normalizePrincipalList(
    ws: WorkspaceState,
    rawTargets: unknown,
    fallbackIds: string[]
  ): { targets: PrincipalRef[]; ids: string[] } {
    // 只要显式传入新字段（包括空数组 []），即以新字段为唯一真值，绝不回退到旧字段防止清空失效
    if (rawTargets !== undefined) {
      const targetList = Array.isArray(rawTargets) ? rawTargets : [];
      const normalized: PrincipalRef[] = [];
      const ids: string[] = [];
      for (const item of targetList) {
        const ref = normalizePrincipalRef(item);
        if (!ref) continue;
        const userPool = collectConnectionDirectoryUsers(ws, ref.connectionId);
        const deptPool = collectConnectionDirectoryDepts(ws, ref.connectionId);
        const matchedUser =
          ref.targetType === 'user'
            ? userPool.find(
                u =>
                  (ref.canonicalKey && u.principal?.canonicalKey && u.principal.canonicalKey === ref.canonicalKey) ||
                  (u.platform === ref.channel &&
                    (!ref.connectionId || (u.connectionId || (u.platform === 'web' ? 'ws-web-directory' : '')) === ref.connectionId) &&
                    (u.id === ref.targetId || u.refId === ref.targetId))
              )
            : undefined;
        const matchedDept =
          ref.targetType === 'department'
            ? deptPool.find(
                d =>
                  (ref.canonicalKey && d.principal?.canonicalKey && d.principal.canonicalKey === ref.canonicalKey) ||
                  (d.platform === ref.channel &&
                    (!ref.connectionId || (d.connectionId || (d.platform === 'web' ? 'ws-web-directory' : '')) === ref.connectionId) &&
                    (d.id === ref.targetId || d.refId === ref.targetId))
              )
            : undefined;
        if (matchedUser && !ref.displayName) ref.displayName = matchedUser.name;
        if (matchedDept && !ref.displayName) ref.displayName = matchedDept.name;
        const canonicalKey = ref.canonicalKey || buildCanonicalPrincipalKey(ref);
        ref.canonicalKey = canonicalKey;
        normalized.push(ref);
        ids.push(canonicalKey);
      }
      return {
        targets: normalized,
        ids
      };
    }
    const derived: PrincipalRef[] = [];
    for (const id of fallbackIds) {
      const u = ws.directoryUsers.find(user => user.id === id || user.refId === id);
      if (u) {
        const channel = u.platform;
        const connectionId = u.connectionId || (channel === 'web' ? 'ws-web-directory' : `conn-${channel}`);
        const targetId = u.refId || u.id;
        derived.push({
          targetType: 'user',
          channel,
          connectionId,
          targetId,
          displayName: u.name,
          canonicalKey: buildCanonicalPrincipalKey({ targetType: 'user', channel, connectionId, targetId })
        });
        continue;
      }
      const d = ws.directoryDepartments.find(dept => dept.id === id || dept.refId === id);
      if (d) {
        const channel = d.platform;
        const connectionId = d.connectionId || (channel === 'web' ? 'ws-web-directory' : `conn-${channel}`);
        const targetId = d.refId || d.id;
        derived.push({
          targetType: 'department',
          channel,
          connectionId,
          targetId,
          displayName: d.name,
          canonicalKey: buildCanonicalPrincipalKey({ targetType: 'department', channel, connectionId, targetId })
        });
      }
    }
    return { targets: derived, ids: clone(fallbackIds) };
  }

  private normalizeAccessPolicy(ws: WorkspaceState, policy: DigitalEmployee['unifiedAccess']): DigitalEmployee['unifiedAccess'] {
    const wl = this.normalizePrincipalList(ws, policy.whitelistTargets, policy.whitelistMemberIds || []);
    const bl = this.normalizePrincipalList(ws, policy.blacklistTargets, policy.blacklistMemberIds || []);
    return {
      mode: policy.mode,
      whitelistMemberIds: wl.ids,
      blacklistMemberIds: bl.ids,
      whitelistTargets: wl.targets,
      blacklistTargets: bl.targets,
      allowGroupMembers: policy.allowGroupMembers ?? true
    };
  }

  private normalizeConfirmationRules(
    ws: WorkspaceState,
    emp: DigitalEmployee,
    rules: HumanConfirmationRule[]
  ): HumanConfirmationRule[] {
    return clone(rules).map(r => {
      const pRef = normalizePrincipalRef(r.target.principal, {
        targetType: r.target.targetType || 'user',
        channel: (r.target.channel || (r.target.provider === 'platform' ? 'web' : r.target.provider)) as MemberProviderType,
        connectionId: r.target.connectionId,
        displayName: r.target.label
      });
      const provider = (pRef?.channel || r.target.provider) as MemberProviderType;
      const ch = emp.channels.find(c => c.platform === provider);
      const connId = pRef?.connectionId || r.target.connectionId || ch?.connectionId
        || (provider === 'wecom' ? ws.channelSyncSources.find(s => s.channel === 'wecom')?.connectionId : provider === 'web' ? 'ws-web-directory' : undefined);
      const botId = r.target.botId || ch?.botId;
      const targetId = pRef?.targetId || r.target.targetId || r.target.userId;
      const dirUser = resolveUserByPrincipalOrId(ws, { channel: provider, connectionId: connId, targetId, id: r.target.userId });
      const userId = dirUser?.id || r.target.userId || targetId;

      let identityVerified = provider !== 'wecom';
      if (provider === 'wecom' && connId) {
        identityVerified = Boolean(ws.wecomScopedBindings[`${connId}::${userId}`] || ws.wecomScopedBindings[`${connId}::${targetId}`]);
      }
      const displayName = dirUser
        ? `${dirUser.name} (${provider === 'feishu' ? '飞书' : provider === 'wecom' ? '企业微信' : provider === 'dingtalk' ? '钉钉' : '网页'})`
        : r.target.label;
      const canonicalPrincipal: PrincipalRef | undefined = connId
        ? {
            targetType: 'user',
            channel: provider,
            connectionId: connId,
            targetId: dirUser?.refId || targetId,
            displayName: dirUser?.name || r.target.label,
            canonicalKey: buildCanonicalPrincipalKey({
              targetType: 'user',
              channel: provider,
              connectionId: connId,
              targetId: dirUser?.refId || targetId
            })
          }
        : undefined;
      return {
        ...r,
        questionScope: r.questionScope.trim() || '*',
        target: {
          ...r.target,
          targetType: 'user',
          channel: provider,
          targetId: dirUser?.refId || targetId,
          userId,
          label: displayName,
          connectionId: connId,
          botId,
          principal: canonicalPrincipal,
          identityVerified,
          bindingStatus: identityVerified ? 'verified' : 'pending_binding'
        }
      };
    });
  }

  private extractChannelCredentials(
    platform: PlatformType,
    body: Record<string, unknown>
  ): { ok: true; identifier: string; secret: string } | { ok: false; error: MockHttpResponse } {
    const identifier = platform === 'feishu'
      ? String(body['appId'] || '').trim()
      : platform === 'wecom'
        ? String(body['botId'] || '').trim()
        : String(body['appKey'] || '').trim();
    const secret = platform === 'wecom'
      ? String(body['botSecret'] || '').trim()
      : String(body['appSecret'] || '').trim();
    if (!identifier) {
      const label = platform === 'feishu' ? 'App ID' : platform === 'wecom' ? 'Bot ID' : 'App Key';
      return { ok: false, error: errResp(400, 'MISSING_IDENTIFIER', `请填写完整的 ${label}`) };
    }
    if (!secret) {
      return { ok: false, error: errResp(400, 'MISSING_SECRET', '请填写渠道密钥 Secret（不使用示例默认值）') };
    }
    if (isMaskedDisplayValue(secret)) {
      return { ok: false, error: errResp(400, 'MASKED_SECRET_REJECTED', '禁止提交脱敏占位密钥（••••），请输入真实 Secret') };
    }
    if (secret.length < 4) {
      return { ok: false, error: errResp(400, 'INVALID_SECRET', '密钥长度过短，无法完成平台鉴权校验') };
    }
    return { ok: true, identifier, secret };
  }

  private routeWorkforce(
    wsId: string,
    ws: WorkspaceState,
    method: string,
    subPath: string,
    body: Record<string, unknown>,
    headers?: Record<string, string | string[] | undefined>,
    parsedUrl: URL = new URL('http://localhost/')
  ): MockHttpResponse | null {
    if (subPath === '/capability-packages' && method === 'GET') {
      return okResp(clone(ws.capabilityPackagesCatalog));
    }
    if (subPath === '/entities' && method === 'GET') {
      const hasPagination = parsedUrl.searchParams.has('page') || parsedUrl.searchParams.has('pageSize') || parsedUrl.searchParams.get('summary') === 'true';
      if (!hasPagination) {
        return okResp(clone(ws.employees));
      }
      const page = Math.max(1, Number(parsedUrl.searchParams.get('page') || 1));
      const pageSize = Math.min(50, Math.max(1, Number(parsedUrl.searchParams.get('pageSize') || 20)));
      const offset = (page - 1) * pageSize;
      const paged = ws.employees.slice(offset, offset + pageSize).map(e => parsedUrl.searchParams.get('summary') === 'true'
        ? {
            id: e.id,
            code: e.code,
            version: e.version,
            name: e.name,
            role: e.role,
            avatar: e.avatar,
            status: e.status,
            executionEngine: e.executionEngine || 'pi',
            modelName: e.modelName,
            channelsCount: e.channels.length,
            packagesCount: e.capabilityPackages.filter(p => p.isBound).length,
            triggersCount: e.triggers.length,
            stats: clone(e.stats)
          }
        : clone(e));
      return okResp({
        items: paged,
        page,
        pageSize,
        total: ws.employees.length,
        hasMore: offset + pageSize < ws.employees.length
      });
    }
    if (subPath === '/entities' && method === 'POST') {
      const name = String(body['name'] || '').trim();
      const role = String(body['role'] || '').trim();
      if (!name) return errResp(400, 'VALIDATION_ERROR', 'AI 员工名称不能为空');
      const executionEngine = parseExecutionEngineParam(body['executionEngine'], 'pi');
      const enabledModels = ws.models.filter(m => m.enabled);
      if (!enabledModels.length) {
        return errResp(400, 'NO_AVAILABLE_MODEL', '当前工作区尚未配置可用模型，请先前往「资源中心 -> 模型管理」添加并启用模型');
      }
      const compatibleModels = enabledModels.filter(m => isModelCompatibleWithEngine(m.protocol, executionEngine));
      const requestedModelId = typeof body['modelId'] === 'string' ? body['modelId'].trim() : '';
      const foundModel = requestedModelId
        ? enabledModels.find(m => m.id === requestedModelId)
        : (compatibleModels.find(m => m.isDefault) || compatibleModels[0]);
      if (!foundModel) {
        if (!requestedModelId && !compatibleModels.length) {
          const engineMeta = getExecutionEngineMeta(executionEngine);
          return errResp(
            400,
            'INCOMPATIBLE_ENGINE_MODEL',
            `执行引擎「${engineMeta.shortLabel}」仅支持 ${engineMeta.requiredProtocolLabel} 协议模型，当前工作区暂无兼容的已启用模型`
          );
        }
        return errResp(400, 'INVALID_MODEL', `模型 "${requestedModelId}" 不存在或未启用，请选择有效的已启用模型`);
      }
      const compatErr = validateModelEngineCompatibility(foundModel, executionEngine);
      if (compatErr) return compatErr;
      const codeNum = ws.employees.length + 1;
      const newId = `emp-0${codeNum}-${Date.now().toString(36).slice(-4)}`;
      const newEmp: DigitalEmployee = {
        id: newId,
        code: `#EMP-${String(codeNum).padStart(3, '0')}`,
        version: 1,
        etag: `W/"emp-${newId}-v1"`,
        name,
        role: role || '通用协同助手',
        avatar: String(body['avatar'] || 'cn-v2-07'),
        status: 'pending_setup',
        executionEngine,
        modelId: foundModel.id,
        modelName: foundModel.name,
        thinkingLevel: (body['thinkingLevel'] as DigitalEmployee['thinkingLevel']) || 'medium',
        prompt: String(body['prompt'] || '你是一个专业的 AI 员工助手。'),
        unifiedAccess: {
          mode: 'all_with_blacklist',
          whitelistMemberIds: [],
          blacklistMemberIds: [],
          allowGroupMembers: true,
          ...(body['unifiedAccess'] as Partial<DigitalEmployee['unifiedAccess']> | undefined)
        },
        channels: [],
        capabilityPackages: [],
        triggers: [],
        humanConfirmationRules: [
          {
            id: `hcr-${Date.now()}`,
            questionScope: '*',
            target: { kind: 'platform_member', label: 'youzai (工作区所有者)', userId: 'dir-web-1', provider: 'web', identityVerified: true },
            fallbackChannel: 'Web 控制台通知'
          }
        ],
        webChatAccess: { mode: 'all', allowMemberIds: [], denyMemberIds: [] },
        handoffPolicy: { mode: 'all_with_blacklist', allowEmployeeIds: [], denyEmployeeIds: [] },
        stats: { todayTasks: 0, todayConversations: 0, successRate: '100%' }
      };
      ws.employees.unshift(newEmp);
      this.pushActivity(ws, 'policy_changes', `新建 AI 员工「${newEmp.name}」(${newEmp.code})`, 'info', '员工创建');
      return okResp(clone(newEmp), 201);
    }

    // Channel QR registration latest query / start / poll / confirm (scoped to wsId + empId + platform)
    const qrLatestMatch = /^\/entities\/([^/]+)\/channels\/(feishu|dingtalk|wecom)\/qr-registration\/latest$/.exec(subPath);
    if (qrLatestMatch && qrLatestMatch[1] && qrLatestMatch[2] && method === 'GET') {
      const empId = decodeURIComponent(qrLatestMatch[1]);
      const platform = qrLatestMatch[2] as PlatformType;
      const key = `${wsId}::${empId}::${platform}`;
      const sessionId = this.latestQrSessionByKey[key];
      const sess = sessionId ? ws.qrRegistrationSessions[sessionId] : undefined;
      const meta = sessionId ? this.qrSessionMeta[sessionId] : undefined;
      if (!sess || !meta) {
        return okResp({ session: null });
      }
      const elapsedSec = Math.floor((Date.now() - meta.createdAtMs) / 1000);
      if (sess.status !== 'authorized') {
        sess.expiresInSeconds = Math.max(0, 600 - elapsedSec);
        if (sess.expiresInSeconds === 0) {
          sess.status = 'expired';
          sess.qrStatus = 'expired';
        }
      }
      return okResp({ session: clone(sess) });
    }
    const qrStartMatch = /^\/entities\/([^/]+)\/channels\/(feishu|dingtalk|wecom)\/qr-registration$/.exec(subPath);
    if (qrStartMatch && qrStartMatch[1] && qrStartMatch[2] && method === 'POST') {
      const empId = decodeURIComponent(qrStartMatch[1]);
      const platform = qrStartMatch[2] as PlatformType;
      const emp = ws.employees.find(e => e.id === empId);
      if (!emp) return errResp(404, 'ENTITY_NOT_FOUND', '未找到 AI 员工');
      const sessionId = `qr_${platform}_${Date.now().toString(36)}_${Math.random().toString(36).slice(2, 6)}`;
      const qrPayload = `kiko://bind?ws=${encodeURIComponent(wsId)}&platform=${platform}&emp=${encodeURIComponent(empId)}&session=${sessionId}`;
      const sess = {
        sessionId,
        workspaceId: wsId,
        employeeId: empId,
        platform,
        qrPayload,
        qrSvgDataUrl: generateQrSvgDataUrl(qrPayload),
        expiresInSeconds: 600,
        status: 'waiting_scan' as const,
        qrStatus: 'waiting_scan' as const,
        credentialSaved: false,
        permissionReady: false,
        permissionStatus: (platform === 'dingtalk' ? 'pending_admin_approval' : 'unknown') as 'pending_admin_approval' | 'unknown',
        connectionState: 'pending' as const,
        suggestedAppId: platform === 'feishu' ? `cli_${Math.random().toString(16).slice(2, 10)}` : platform === 'dingtalk' ? `ding_${Math.random().toString(16).slice(2, 10)}` : undefined,
        suggestedBotId: platform === 'wecom' ? `bot_${Math.random().toString(16).slice(2, 10)}` : undefined
      };
      ws.qrRegistrationSessions[sessionId] = sess;
      this.qrSessionMeta[sessionId] = { wsId, empId, createdAtMs: Date.now() };
      this.latestQrSessionByKey[`${wsId}::${empId}::${platform}`] = sessionId;
      return okResp(clone(sess), 201);
    }
    const qrPollMatch = /^\/channels\/qr-registration\/([^/]+)$/.exec(subPath);
    if (qrPollMatch && qrPollMatch[1] && method === 'GET') {
      const sId = decodeURIComponent(qrPollMatch[1]);
      const sess = ws.qrRegistrationSessions[sId];
      if (!sess || sess.workspaceId !== wsId) return errResp(404, 'QR_SESSION_NOT_FOUND', '扫码会话已失效，请点击刷新二维码');
      const meta = this.qrSessionMeta[sId];
      if (meta && sess.status !== 'authorized') {
        const elapsedSec = Math.floor((Date.now() - meta.createdAtMs) / 1000);
        sess.expiresInSeconds = Math.max(0, 600 - elapsedSec);
        if (sess.expiresInSeconds === 0) {
          sess.status = 'expired';
          sess.qrStatus = 'expired';
        }
      }
      return okResp(clone(sess));
    }
    const qrConfirmMatch = /^\/channels\/qr-registration\/([^/]+)\/confirm$/.exec(subPath);
    if (qrConfirmMatch && qrConfirmMatch[1] && method === 'POST') {
      const sId = decodeURIComponent(qrConfirmMatch[1]);
      const sess = ws.qrRegistrationSessions[sId];
      const meta = this.qrSessionMeta[sId];
      if (!sess || !meta || meta.wsId !== wsId) return errResp(404, 'QR_SESSION_NOT_FOUND', '扫码会话不存在或已过期');
      const elapsedSec = Math.floor((Date.now() - meta.createdAtMs) / 1000);
      if (elapsedSec >= 600 || sess.status === 'expired') {
        sess.expiresInSeconds = 0;
        sess.status = 'expired';
        sess.qrStatus = 'expired';
        return errResp(400, 'QR_SESSION_EXPIRED', '授权二维码已超过 10 分钟有效期，请刷新二维码');
      }
      const emp = ws.employees.find(e => e.id === meta.empId);
      if (!emp) return errResp(404, 'ENTITY_NOT_FOUND', '未找到绑定目标 AI 员工');
      const suggestedId = sess.platform === 'wecom' ? sess.suggestedBotId : sess.suggestedAppId;
      const connId = `conn-${emp.id}-${sess.platform}-${Date.now().toString(36)}`;
      let binding = emp.channels.find(c => c.platform === sess.platform);
      if (!binding) {
        binding = buildServerChannelBinding(sess.platform, 'qrcode', suggestedId, connId);
        emp.channels.push(binding);
      } else {
        binding.connectionId = binding.connectionId || connId;
        binding.status = 'connected';
        binding.credentialStatus = 'saved';
        binding.connectionState = 'connected';
        binding.permissionReady = sess.platform !== 'dingtalk';
        binding.statusText = '已连接';
        binding.lastVerifiedAt = nowTimeLabel();
        binding.errorDetail = undefined;
      }
      ws.channelSecrets[binding.connectionId!] = `qr_issued_${sess.platform}_${Math.random().toString(36).slice(2, 10)}`;
      if (emp.status === 'pending_setup') emp.status = 'active';
      bumpEmployeeVersion(emp);
      sess.status = 'authorized';
      sess.qrStatus = 'authorized';
      sess.credentialSaved = true;
      sess.permissionReady = Boolean(binding.permissionReady);
      sess.permissionStatus = binding.permissionReady ? 'authorized' : 'pending_admin_approval';
      sess.connectionState = 'connected';
      sess.binding = clone(binding);
      this.pushActivity(ws, 'policy_changes', `「${emp.name}」通过扫码成功接入 ${binding.platformName}`, 'info', '渠道接入');
      return okResp(clone(sess));
    }

    // Channel Credential Test, Save Bind, & Rebind
    const chTestMatch = /^\/entities\/([^/]+)\/channels\/(feishu|dingtalk|wecom)\/test-connection$/.exec(subPath);
    if (chTestMatch && chTestMatch[1] && chTestMatch[2] && method === 'POST') {
      const empId = decodeURIComponent(chTestMatch[1]);
      const platform = chTestMatch[2] as PlatformType;
      const emp = ws.employees.find(e => e.id === empId);
      if (!emp) return errResp(404, 'ENTITY_NOT_FOUND', '未找到 AI 员工');

      const existingCh = emp.channels.find(c => c.platform === platform);
      let identifier = '';
      let secret = '';
      if (body['useStored'] === true && existingCh && existingCh.connectionId && ws.channelSecrets[existingCh.connectionId]) {
        identifier = platform === 'wecom'
          ? (existingCh.botId || '')
          : platform === 'dingtalk'
            ? (existingCh.appKey || '')
            : (existingCh.appId || '');
        secret = ws.channelSecrets[existingCh.connectionId] || '';
        existingCh.lastVerifiedAt = nowTimeLabel();
        existingCh.connectionState = 'connected';
        existingCh.status = 'connected';
        existingCh.errorDetail = undefined;
      } else {
        const creds = this.extractChannelCredentials(platform, body);
        if (!creds.ok) return creds.error;
        identifier = creds.identifier;
        secret = creds.secret;
      }
      if (identifier.toLowerCase().includes('invalid') || secret.toLowerCase().includes('invalid')) {
        return errResp(400, 'CHANNEL_AUTH_FAILED', `渠道凭证校验失败：${platform} 开放平台拒绝认证，请检查应用凭证`);
      }

      const digest = computeChannelCredDigest(wsId, empId, platform, identifier, secret);
      const channelTestReceipt = `rcpt_ch_${platform}_${Date.now().toString(36)}_${Math.random().toString(36).slice(2, 6)}`;
      this.channelTestReceipts[channelTestReceipt] = { wsId, empId, platform, digest, expiresAt: Date.now() + 300_000 };
      return okResp({
        ok: true,
        platform,
        latency: platform === 'wecom' ? '52ms' : platform === 'dingtalk' ? '39ms' : '44ms',
        channelTestReceipt,
        testedAt: nowTimeLabel()
      });
    }
    const chBindMatch = /^\/entities\/([^/]+)\/channels\/(feishu|dingtalk|wecom)\/(bind|rebind)$/.exec(subPath);
    if (chBindMatch && chBindMatch[1] && chBindMatch[2] && chBindMatch[3] && method === 'POST') {
      const empId = decodeURIComponent(chBindMatch[1]);
      const platform = chBindMatch[2] as PlatformType;
      const action = chBindMatch[3];
      const emp = ws.employees.find(e => e.id === empId);
      if (!emp) return errResp(404, 'ENTITY_NOT_FOUND', '未找到 AI 员工');
      const conflict = checkEmployeeConcurrency(emp, body, headers);
      if (conflict) return conflict;

      const creds = this.extractChannelCredentials(platform, body);
      if (!creds.ok) return creds.error;

      const receiptId = typeof body['channelTestReceipt'] === 'string' ? body['channelTestReceipt'].trim() : '';
      const receipt = this.channelTestReceipts[receiptId];
      const expectedDigest = computeChannelCredDigest(wsId, empId, platform, creds.identifier, creds.secret);
      if (!receipt || receipt.wsId !== wsId || receipt.empId !== empId || receipt.platform !== platform || receipt.expiresAt <= Date.now() || receipt.digest !== expectedDigest) {
        return errResp(409, 'CHANNEL_TEST_REQUIRED', '当前填入的渠道凭证尚未通过连接测试或已修改，请先验证连接');
      }

      let ch = emp.channels.find(c => c.platform === platform);
      const existingId = ch
        ? (platform === 'wecom' ? (ch.botId || '') : platform === 'dingtalk' ? (ch.appKey || '') : (ch.appId || ''))
        : '';
      const switchingRobot = action === 'rebind' && existingId !== '' && creds.identifier !== existingId;
      if (switchingRobot && body['confirmedImpact'] !== true) {
        return errResp(400, 'CONFIRM_IMPACT_REQUIRED', '换绑新机器人会影响既有工作群、群共享及确认人映射，请先勾选确认影响');
      }

      delete this.channelTestReceipts[receiptId];
      const connId = switchingRobot || !ch?.connectionId
        ? `conn-${empId}-${platform}-${Date.now().toString(36)}`
        : ch.connectionId;
      ws.channelSecrets[connId] = creds.secret;
      const secretMasked = `${platform === 'wecom' ? 'bsec' : 'sec'}_••••••••${creds.secret.slice(-4)}`;

      if (!ch) {
        ch = buildServerChannelBinding(platform, 'existing_app', creds.identifier, connId);
        emp.channels.push(ch);
      }
      ch.connectionId = connId;
      ch.connectionMethod = 'existing_app';
      ch.status = 'connected';
      ch.credentialStatus = 'saved';
      ch.connectionState = 'connected';
      ch.permissionReady = platform !== 'dingtalk';
      ch.statusText = '已连接';
      ch.lastVerifiedAt = nowTimeLabel();
      ch.errorDetail = undefined;

      if (platform === 'wecom') {
        ch.botId = creds.identifier;
        ch.botSecretMasked = secretMasked;
      } else if (platform === 'dingtalk') {
        ch.appKey = creds.identifier;
        ch.appSecretMasked = secretMasked;
        ch.adminConsoleUrl = 'https://open-dev.dingtalk.com/fe/app#/corp/app';
      } else {
        ch.appId = creds.identifier;
        ch.appSecretMasked = secretMasked;
      }

      if (switchingRobot) {
        ch.boundGroups = [];
        ch.sharingRules.forEach(r => {
          r.syncStatus = 'auth_error';
          r.authErrorDetail = '已换绑新机器人，需重新将新机器人拉入群聊并完成群授权验证';
        });
        for (const rule of emp.humanConfirmationRules) {
          if (rule.target.provider === platform) {
            rule.target.connectionId = connId;
            if (platform === 'wecom') {
              rule.target.botId = creds.identifier;
              rule.target.identityVerified = false;
              rule.target.bindingStatus = 'pending_binding';
            }
          }
        }
      }

      if (emp.status === 'pending_setup') emp.status = 'active';
      bumpEmployeeVersion(emp);
      const etag = emp.etag || `W/"emp-${emp.id}-v${emp.version}"`;
      this.pushActivity(ws, 'policy_changes', `「${emp.name}」${switchingRobot ? '换绑新机器人' : '保存渠道凭证'} (${ch.platformName})`, 'info', action === 'rebind' ? '渠道换绑' : '渠道绑定');
      return okResp({ employee: clone(emp), channel: clone(ch), switchingRobot }, 200, { ETag: etag });
    }

    // Feishu rule live regex/keyword test
    if (subPath === '/channels/feishu/test-rules' && method === 'POST') {
      const sampleText = String(body['sampleText'] || '');
      const rules = (Array.isArray(body['rules']) ? body['rules'] : []) as { id: string; name: string; type: 'keyword' | 'regex'; pattern: string; enabled: boolean }[];
      const matched: { ruleId: string; ruleName: string; type: string; matchedText: string }[] = [];
      for (const r of rules) {
        if (!r.enabled || !r.pattern.trim()) continue;
        if (r.type === 'keyword') {
          const tokens = r.pattern.split(/[,，]/).map(s => s.trim()).filter(Boolean);
          const hit = tokens.find(t => sampleText.toLowerCase().includes(t.toLowerCase()));
          if (hit) matched.push({ ruleId: r.id, ruleName: r.name, type: 'keyword', matchedText: hit });
        } else {
          try {
            let patternBody = r.pattern.trim();
            let flags = 'i';
            if (patternBody.startsWith('/') && patternBody.lastIndexOf('/') > 0) {
              const lastSlash = patternBody.lastIndexOf('/');
              flags = patternBody.slice(lastSlash + 1) || 'i';
              patternBody = patternBody.slice(1, lastSlash);
            }
            const re = new RegExp(patternBody, flags);
            const m = re.exec(sampleText);
            if (m) matched.push({ ruleId: r.id, ruleName: r.name, type: 'regex', matchedText: m[0] });
          } catch {
            // ignore bad regex in single rule
          }
        }
      }
      return okResp({ matchedCount: matched.length, matched });
    }

    // Entity detail & module-specific endpoints
    const empBaseMatch = /^\/entities\/([^/]+)(?:\/(.*))?$/.exec(subPath);
    if (!empBaseMatch || !empBaseMatch[1]) return null;
    const empId = decodeURIComponent(empBaseMatch[1]);
    const empSub = empBaseMatch[2] || '';
    const emp = ws.employees.find(e => e.id === empId);
    if (!emp) return errResp(404, 'ENTITY_NOT_FOUND', `AI 员工 ${empId} 不存在`);

    if (empSub === '' && method === 'GET') {
      return okEmpResp(emp);
    }
    if (empSub === '' && method === 'PUT') {
      for (const k of FORBIDDEN_EMP_WRITE_KEYS) {
        if (k in body && body[k] !== undefined) {
          return errResp(400, 'UNEDITABLE_SERVER_FIELDS', `保存接口不接受服务端管理字段 "${k}"，请仅提交用户可编辑配置`);
        }
      }
      const conflict = checkEmployeeConcurrency(emp, body, headers);
      if (conflict) return conflict;

      const nextName = typeof body['name'] === 'string' ? body['name'].trim() : emp.name;
      if (!nextName) return errResp(400, 'VALIDATION_ERROR', 'AI 员工名称不能为空');
      const resolved = resolveEmployeeEngineAndModel(ws, emp, body);
      if (!resolved.ok) return resolved.error;
      const { engine: nextEngine, model: foundModel } = resolved;
      const nextModelId = foundModel.id;

      let nextChannels = emp.channels;
      if (Array.isArray(body['channels'])) {
        const chRes = this.validateAndApplyChannelConfigs(ws, emp, body['channels']);
        if (!chRes.ok) return chRes.error;
        nextChannels = chRes.data;
      }

      const nextPackages = Array.isArray(body['capabilityPackages']) ? clone(body['capabilityPackages'] as CapabilityPackageBinding[]) : emp.capabilityPackages;
      const draftEmpForCheck: DigitalEmployee = { ...emp, channels: nextChannels };

      let nextTriggers = emp.triggers;
      if (Array.isArray(body['triggers'])) {
        const trgResult = this.validateAndNormalizeTriggers(wsId, draftEmpForCheck, body['triggers']);
        if (!trgResult.ok) return trgResult.error;
        nextTriggers = trgResult.data;
      }

      const rawRules = Array.isArray(body['humanConfirmationRules']) ? (body['humanConfirmationRules'] as HumanConfirmationRule[]) : emp.humanConfirmationRules;
      const nextRules = this.normalizeConfirmationRules(ws, draftEmpForCheck, rawRules);

      emp.name = nextName;
      if (typeof body['role'] === 'string') emp.role = body['role'].trim();
      if (typeof body['avatar'] === 'string') emp.avatar = body['avatar'];
      emp.executionEngine = nextEngine;
      emp.modelId = nextModelId;
      emp.modelName = foundModel.name;
      if (typeof body['thinkingLevel'] === 'string') emp.thinkingLevel = body['thinkingLevel'] as DigitalEmployee['thinkingLevel'];
      if (typeof body['prompt'] === 'string') emp.prompt = body['prompt'];
      emp.channels = nextChannels;
      emp.capabilityPackages = nextPackages.map(pkg => {
        if (pkg.scope?.userScope) {
          const wl = this.normalizePrincipalList(ws, pkg.scope.userScope.whitelistTargets, pkg.scope.userScope.whitelistMemberIds || []);
          const bl = this.normalizePrincipalList(ws, pkg.scope.userScope.blacklistTargets, pkg.scope.userScope.blacklistMemberIds || []);
          pkg.scope.userScope.whitelistTargets = wl.targets;
          pkg.scope.userScope.whitelistMemberIds = wl.ids;
          pkg.scope.userScope.blacklistTargets = bl.targets;
          pkg.scope.userScope.blacklistMemberIds = bl.ids;
        }
        return pkg;
      });
      emp.triggers = nextTriggers;
      if (body['unifiedAccess']) emp.unifiedAccess = this.normalizeAccessPolicy(ws, body['unifiedAccess'] as DigitalEmployee['unifiedAccess']);
      if (body['webChatAccess']) emp.webChatAccess = clone(body['webChatAccess'] as DigitalEmployee['webChatAccess']);
      if (body['handoffPolicy']) emp.handoffPolicy = clone(body['handoffPolicy'] as DigitalEmployee['handoffPolicy']);
      emp.humanConfirmationRules = nextRules;
      bumpEmployeeVersion(emp);
      this.pushActivity(ws, 'policy_changes', `「${emp.name}」配置已保存`, 'info', '全量保存');
      return okEmpResp(emp);
    }
    if (empSub === 'basics' && method === 'PATCH') {
      const conflict = checkEmployeeConcurrency(emp, body, headers);
      if (conflict) return conflict;
      const nextName = typeof body['name'] === 'string' ? body['name'].trim() : emp.name;
      if (!nextName) return errResp(400, 'VALIDATION_ERROR', 'AI 员工名称不能为空');
      const resolved = resolveEmployeeEngineAndModel(ws, emp, body);
      if (!resolved.ok) return resolved.error;
      const { engine: nextEngine, model: foundModel } = resolved;
      const nextModelId = foundModel.id;
      emp.name = nextName;
      if (typeof body['role'] === 'string') emp.role = body['role'].trim();
      if (typeof body['avatar'] === 'string') emp.avatar = body['avatar'];
      emp.executionEngine = nextEngine;
      emp.modelId = nextModelId;
      emp.modelName = foundModel.name;
      if (typeof body['thinkingLevel'] === 'string') emp.thinkingLevel = body['thinkingLevel'] as DigitalEmployee['thinkingLevel'];
      if (typeof body['prompt'] === 'string') emp.prompt = body['prompt'];
      bumpEmployeeVersion(emp);
      return okEmpResp(emp);
    }
    if (empSub === 'status' && method === 'PATCH') {
      const conflict = checkEmployeeConcurrency(emp, body, headers);
      if (conflict) return conflict;
      if (typeof body['status'] === 'string') {
        emp.status = body['status'] as DigitalEmployee['status'];
      }
      bumpEmployeeVersion(emp);
      return okEmpResp(emp);
    }
    if (empSub === 'capability-packages' && method === 'PUT') {
      const conflict = checkEmployeeConcurrency(emp, body, headers);
      if (conflict) return conflict;
      if (Array.isArray(body['capabilityPackages'])) {
        emp.capabilityPackages = clone(body['capabilityPackages'] as CapabilityPackageBinding[]).map(pkg => {
          if (pkg.scope?.userScope) {
            const wl = this.normalizePrincipalList(ws, pkg.scope.userScope.whitelistTargets, pkg.scope.userScope.whitelistMemberIds || []);
            const bl = this.normalizePrincipalList(ws, pkg.scope.userScope.blacklistTargets, pkg.scope.userScope.blacklistMemberIds || []);
            pkg.scope.userScope.whitelistTargets = wl.targets;
            pkg.scope.userScope.whitelistMemberIds = wl.ids;
            pkg.scope.userScope.blacklistTargets = bl.targets;
            pkg.scope.userScope.blacklistMemberIds = bl.ids;
          }
          return pkg;
        });
        bumpEmployeeVersion(emp);
        this.pushActivity(ws, 'policy_changes', `「${emp.name}」的能力包授权配置已更新`, 'info', '能力包更新');
      }
      return okEmpResp(emp);
    }
    if (empSub === 'channels' && method === 'PUT') {
      const conflict = checkEmployeeConcurrency(emp, body, headers);
      if (conflict) return conflict;
      if (Array.isArray(body['channels'])) {
        const chRes = this.validateAndApplyChannelConfigs(ws, emp, body['channels']);
        if (!chRes.ok) return chRes.error;
        emp.channels = chRes.data;
        bumpEmployeeVersion(emp);
        this.pushActivity(ws, 'policy_changes', `「${emp.name}」渠道群策略配置已更新`, 'info', '渠道配置');
      }
      return okEmpResp(emp);
    }
    const chStatusMatch = /^channels\/(feishu|dingtalk|wecom)\/status$/.exec(empSub);
    if (chStatusMatch && chStatusMatch[1] && method === 'PATCH') {
      const conflict = checkEmployeeConcurrency(emp, body, headers);
      if (conflict) return conflict;
      const platform = chStatusMatch[1] as PlatformType;
      const ch = emp.channels.find(c => c.platform === platform);
      if (!ch) return errResp(404, 'CHANNEL_NOT_FOUND', `渠道 ${platform} 尚未绑定`);
      const enabled = body['enabled'] !== false;
      if (enabled) {
        ch.status = 'connected';
        ch.connectionState = 'connected';
        ch.statusText = '已连接';
      } else {
        ch.status = 'disabled';
        ch.connectionState = 'disconnected';
        ch.statusText = '已停用';
      }
      bumpEmployeeVersion(emp);
      return okEmpResp(emp);
    }
    const chDeleteMatch = /^channels\/(feishu|dingtalk|wecom)$/.exec(empSub);
    if (chDeleteMatch && chDeleteMatch[1] && method === 'DELETE') {
      const conflict = checkEmployeeConcurrency(emp, body, headers);
      if (conflict) return conflict;
      const platform = chDeleteMatch[1] as PlatformType;
      const ch = emp.channels.find(c => c.platform === platform);
      if (!ch) return errResp(404, 'CHANNEL_NOT_FOUND', `渠道 ${platform} 尚未绑定`);
      if (ch.connectionId) {
        delete ws.channelSecrets[ch.connectionId];
      }
      emp.channels = emp.channels.filter(c => c.platform !== platform);
      bumpEmployeeVersion(emp);
      this.pushActivity(ws, 'policy_changes', `「${emp.name}」已解绑 ${platform} 渠道`, 'warning', '渠道解绑');
      return okEmpResp(emp);
    }
    if (empSub === 'channels/group-sharing/refresh-status' && method === 'POST') {
      const targetPlatform = String(body['platform'] || '');
      for (const ch of emp.channels) {
        if (targetPlatform && ch.platform !== targetPlatform) continue;
        for (const sr of ch.sharingRules) {
          if (sr.syncStatus === 'pending_apply' || sr.syncStatus === 'saved') {
            sr.syncStatus = 'applied';
            sr.authErrorDetail = undefined;
          }
        }
      }
      bumpEmployeeVersion(emp);
      return okEmpResp(emp);
    }
    if (empSub === 'channels/group-sharing' && method === 'PUT') {
      const conflict = checkEmployeeConcurrency(emp, body, headers);
      if (conflict) return conflict;
      const platform = body['platform'] as PlatformType;
      const rule = body['rule'] as CrossGroupSharingRule;
      const ch = emp.channels.find(c => c.platform === platform);
      if (!ch) return errResp(404, 'CHANNEL_NOT_FOUND', '未找到对应渠道绑定');
      const targetRef = normalizePrincipalRef(rule.targetUser, {
        targetType: 'user',
        channel: platform,
        connectionId: ch.connectionId || '',
        displayName: rule.targetUserLabel
      });
      const targetUserId = targetRef?.targetId || rule.targetUserId;
      const targetConnId = targetRef?.connectionId || ch.connectionId;
      let syncStatus: CrossGroupSharingRule['syncStatus'] = rule.syncStatus === 'auth_error' ? 'auth_error' : 'pending_apply';
      let authErrorDetail = rule.authErrorDetail;
      if (platform === 'wecom' && targetUserId) {
        const matched = resolveUserByPrincipalOrId(ws, { channel: 'wecom', connectionId: targetConnId, targetId: targetUserId });
        const lookupId = matched?.id || targetUserId;
        const verified = Boolean(targetConnId && (ws.wecomScopedBindings[`${targetConnId}::${lookupId}`] || ws.wecomScopedBindings[`${targetConnId}::${targetUserId}`]));
        if (!verified) {
          syncStatus = 'auth_error';
          authErrorDetail = '目标人员尚未完成当前企微连接的私聊验证码绑定，授权暂无法生效';
        }
      }
      const existingIdx = ch.sharingRules.findIndex(r => r.id === rule.id);
      const savedRule: CrossGroupSharingRule = {
        ...rule,
        targetUserId,
        targetUser: targetRef || rule.targetUser,
        syncStatus,
        authErrorDetail,
        savedAt: nowFullTimestamp()
      };
      if (existingIdx >= 0) {
        ch.sharingRules[existingIdx] = savedRule;
      } else {
        ch.sharingRules.unshift(savedRule);
      }
      bumpEmployeeVersion(emp);
      return okEmpResp(emp);
    }
    if (empSub === 'triggers' && method === 'PUT') {
      const conflict = checkEmployeeConcurrency(emp, body, headers);
      if (conflict) return conflict;
      if (Array.isArray(body['triggers'])) {
        const trgRes = this.validateAndNormalizeTriggers(wsId, emp, body['triggers']);
        if (!trgRes.ok) return trgRes.error;
        emp.triggers = trgRes.data;
        bumpEmployeeVersion(emp);
      }
      return okEmpResp(emp);
    }
    const regenSecretMatch = /^triggers\/([^/]+)\/regenerate-secret$/.exec(empSub);
    if (regenSecretMatch && regenSecretMatch[1] && method === 'POST') {
      const tId = decodeURIComponent(regenSecretMatch[1]);
      const trg = emp.triggers.find(t => t.id === tId);
      const isDraft = body['allowDraft'] === true;
      if (!trg && !isDraft) return errResp(404, 'TRIGGER_NOT_FOUND', '未找到指定触发器');
      if (trg) {
        const conflict = checkEmployeeConcurrency(emp, body, headers);
        if (conflict) return conflict;
      }
      const keyHex = Math.random().toString(16).slice(2, 8);
      const fullSecret = `whsec_${Math.random().toString(36).slice(2, 10)}${Math.random().toString(36).slice(2, 10)}${Math.random().toString(16).slice(2, 6)}`;
      const newKeyId = `whk_${keyHex}`;
      const newSecretMasked = `${fullSecret.slice(0, 12)}••••••••${fullSecret.slice(-4)}`;
      if (trg) {
        trg.keyId = newKeyId;
        trg.secretMasked = newSecretMasked;
        trg.webhookEndpoint = trg.webhookEndpoint || `https://hooks.kiko.corp/v1/wh/${emp.id}/${trg.id}`;
        bumpEmployeeVersion(emp);
        return okResp({
          trigger: clone(trg),
          secret: fullSecret,
          version: emp.version,
          etag: emp.etag,
          isDraft: false
        });
      }
      const webhookEndpoint = `https://hooks.kiko.corp/v1/wh/${emp.id}/${tId}`;
      const draftSecretToken = `dst_${Date.now().toString(36)}_${Math.random().toString(36).slice(2, 10)}`;
      this.draftTriggerSecrets[`${wsId}::${emp.id}::${tId}`] = {
        empId: emp.id,
        triggerId: tId,
        keyId: newKeyId,
        secretMasked: newSecretMasked,
        secret: fullSecret,
        webhookEndpoint,
        draftSecretToken
      };
      return okResp({
        trigger: {
          id: tId,
          name: '',
          type: 'webhook',
          enabled: true,
          allowedActions: ['create_task'],
          description: '',
          historyPolicy: 'isolated',
          notificationTargets: [],
          executionLogs: [],
          webhookEndpoint,
          keyId: newKeyId,
          secretMasked: newSecretMasked
        },
        secret: fullSecret,
        draftSecretToken,
        version: emp.version,
        etag: emp.etag,
        isDraft: true
      });
    }
    const runTriggerMatch = /^triggers\/([^/]+)\/run$/.exec(empSub);
    if (runTriggerMatch && runTriggerMatch[1] && method === 'POST') {
      const idemKey = getIdempotencyKey(body, headers);
      const idemHit = this.resolveIdempotencyHitOrConflict(wsId, method, subPath, idemKey, body);
      if (idemHit) return idemHit;

      const tId = decodeURIComponent(runTriggerMatch[1]);
      const trg = emp.triggers.find(t => t.id === tId);
      if (!trg) return errResp(404, 'TRIGGER_NOT_FOUND', '未找到已保存的自动触发器');
      const tLabel = nowFullTimestamp().slice(5, 16);
      const targetGroup = trg.notificationTargets[0]?.groupName;
      const executionId = `exec_trg_${Date.now().toString(36)}_${Math.random().toString(36).slice(2, 6)}`;
      const opId = `op_trg_${Date.now().toString(36)}_${Math.random().toString(36).slice(2, 6)}`;
      const newLog: EmployeeTrigger['executionLogs'][number] = {
        id: `log-${Date.now()}-${Math.random().toString(36).slice(2, 5)}`,
        executionId,
        triggeredAt: `${tLabel} (手动测试执行)`,
        status: 'running',
        duration: '执行中...',
        deliveryStatus: targetGroup ? `待投递至 ${targetGroup}` : '未指定回告群',
        summary: `已受理触发器「${trg.name}」执行请求（执行 ID: ${executionId}），正在异步执行`
      };
      trg.executionLogs.unshift(newLog);
      const newTask: TaskRecord = {
        id: `task-trg-${Date.now()}-${Math.random().toString(36).slice(2, 5)}`,
        taskId: `task_trg_${Date.now().toString().slice(-6)}`,
        employeeName: emp.name,
        employeeId: emp.id,
        channelGroup: targetGroup || `触发器 · ${trg.name}`,
        platform: trg.notificationTargets[0]?.platform || 'web',
        toolUsed: trg.type === 'schedule' ? 'cron_scheduler' : trg.type === 'webhook' ? 'webhook_ingress' : 'github_issue_sync',
        status: 'running',
        delivery: 'pending',
        duration: '进行中',
        version: 1,
        activeOperationId: opId,
        createdAt: nowTimeLabel(),
        retryHistory: [{ runIndex: 1, time: nowTimeLabel(), duration: '进行中', result: `已受理执行 (${executionId})` }],
        logs: [
          `[${nowTimeLabel()}] 触发器「${trg.name}」运行请求已受理（执行 ID: ${executionId}）`,
          `[${nowTimeLabel()}] 关联指令：${trg.description || '默认自动化巡检任务'}`
        ]
      };
      ws.tasks.unshift(newTask);
      this.timers.push({
        kind: 'trigger_run',
        wsId,
        targetId: executionId,
        createdAtMs: Date.now(),
        opId,
        extra: JSON.stringify({
          empId: emp.id,
          triggerId: trg.id,
          logId: newLog.id,
          taskId: newTask.id,
          targetGroup
        })
      });
      this.pushActivity(ws, 'tasks', `触发器「${trg.name}」已受理异步运行（执行 ID ${executionId}）`, 'info', '触发器受理');
      return this.recordOperationReceipt(
        wsId,
        method,
        subPath,
        idemKey,
        body,
        {
          operationId: opId,
          action: 'trigger_run',
          targetId: executionId,
          status: 'accepted',
          outcome: 'accepted',
          message: `触发器「${trg.name}」运行请求已受理，请通过操作单号追踪结果`,
          data: {
            executionId,
            executionLog: clone(newLog),
            trigger: clone(trg),
            task: clone(newTask)
          }
        },
        202
      );
    }
    if (empSub === 'channels/status' && method === 'GET') {
      const statuses = emp.channels.map(ch => ({
        platform: ch.platform,
        connectionId: ch.connectionId,
        status: ch.status,
        statusText: ch.statusText,
        credentialStatus: ch.credentialStatus,
        connectionState: ch.connectionState,
        permissionReady: ch.permissionReady,
        lastVerifiedAt: ch.lastVerifiedAt,
        errorDetail: ch.errorDetail,
        boundGroups: clone(ch.boundGroups),
        businessCapabilities: ch.businessCapabilities ? clone(ch.businessCapabilities) : undefined
      }));
      return okResp({
        employeeId: emp.id,
        status: emp.status,
        checkedAt: nowFullTimestamp(),
        channels: statuses
      });
    }
    if (empSub === 'triggers/preview-cron' && method === 'POST') {
      const cronExpr = String(body['cron'] || '').trim();
      const tz = String(body['timezone'] || 'Asia/Shanghai').trim() || 'Asia/Shanghai';
      if (!cronExpr) {
        return errResp(400, 'INVALID_CRON', '请输入有效的 5 段 Cron 表达式');
      }
      const computed = computeNextCronRuns(cronExpr, tz, 5);
      if (!computed.ok) {
        return errResp(400, 'INVALID_CRON', computed.message);
      }
      return okResp({ cron: cronExpr, timezone: tz, previews: computed.previews });
    }
    if (empSub === 'rules' && method === 'PUT') {
      const conflict = checkEmployeeConcurrency(emp, body, headers);
      if (conflict) return conflict;
      if (body['unifiedAccess']) emp.unifiedAccess = this.normalizeAccessPolicy(ws, body['unifiedAccess'] as DigitalEmployee['unifiedAccess']);
      if (body['webChatAccess']) emp.webChatAccess = clone(body['webChatAccess'] as DigitalEmployee['webChatAccess']);
      if (body['handoffPolicy']) emp.handoffPolicy = clone(body['handoffPolicy'] as DigitalEmployee['handoffPolicy']);
      if (Array.isArray(body['humanConfirmationRules'])) {
        emp.humanConfirmationRules = this.normalizeConfirmationRules(ws, emp, body['humanConfirmationRules'] as HumanConfirmationRule[]);
      }
      bumpEmployeeVersion(emp);
      this.pushActivity(ws, 'policy_changes', `「${emp.name}」权限范围与人工确认规则已保存生效`, 'info', '权限变更');
      return okEmpResp(emp);
    }

    return null;
  }

  private routeChat(
    wsId: string,
    ws: WorkspaceState,
    method: string,
    subPath: string,
    body: Record<string, unknown>,
    parsedUrl: URL = new URL('http://localhost/'),
    headers?: Record<string, string | string[] | undefined>
  ): MockHttpResponse | null {
    if (subPath === '/chat-employees' && method === 'GET') {
      const summaryOnly = parsedUrl.searchParams.get('summary') === 'true';
      if (summaryOnly) {
        const items = ws.employees.map(emp => {
          const sess = this.ensureSession(ws, emp);
          const lastMsg = sess.messages[sess.messages.length - 1];
          return {
            employee: clone(emp),
            sessionSummary: {
              id: sess.id,
              employeeId: emp.id,
              title: sess.title,
              updatedAt: sess.updatedAt,
              lastMessagePreview: lastMsg?.content || '',
              lastMessageTime: lastMsg?.time || sess.updatedAt,
              totalMessages: sess.messages.length,
              sessionTasksCount: sess.sessionTasks.length,
              activeExecutionId: sess.activeExecutionId
            }
          };
        });
        return okResp(items);
      }
      const items = ws.employees.map(emp => ({
        employee: clone(emp),
        session: clone(this.ensureSession(ws, emp))
      }));
      return okResp(items);
    }

    const convMatch = /^\/entities\/([^/]+)\/conversation$/.exec(subPath);
    if (convMatch && convMatch[1] && method === 'GET') {
      const empId = decodeURIComponent(convMatch[1]);
      const emp = ws.employees.find(e => e.id === empId);
      if (!emp) return errResp(404, 'ENTITY_NOT_FOUND', 'AI 员工不存在');
      const fullSess = this.ensureSession(ws, emp);
      const limitParam = parsedUrl.searchParams.get('limit');
      const beforeId = parsedUrl.searchParams.get('before');
      if (!limitParam && !beforeId) {
        return okResp({
          ...clone(fullSess),
          totalMessages: fullSess.messages.length,
          hasMoreMessages: false
        });
      }
      const limit = Math.min(50, Math.max(1, Number(limitParam || 20)));
      const endIdx = beforeId
        ? fullSess.messages.findIndex(m => m.id === beforeId)
        : fullSess.messages.length;
      const sliceEnd = endIdx < 0 ? fullSess.messages.length : endIdx;
      const sliceStart = Math.max(0, sliceEnd - limit);
      const pagedMsgs = fullSess.messages.slice(sliceStart, sliceEnd);
      return okResp({
        ...clone(fullSess),
        messages: clone(pagedMsgs),
        totalMessages: fullSess.messages.length,
        hasMoreMessages: sliceStart > 0,
        nextBeforeCursor: sliceStart > 0 ? pagedMsgs[0]?.id : undefined
      });
    }

    const turnsMatch = /^\/entities\/([^/]+)\/conversation\/turns$/.exec(subPath);
    if (turnsMatch && turnsMatch[1] && method === 'POST') {
      const empId = decodeURIComponent(turnsMatch[1]);
      const emp = ws.employees.find(e => e.id === empId);
      if (!emp) return errResp(404, 'ENTITY_NOT_FOUND', 'AI 员工不存在');
      const idemKey = getIdempotencyKey(body, headers);
      const idemHit = this.resolveIdempotencyHitOrConflict(wsId, 'POST', subPath, idemKey, body);
      if (idemHit) return idemHit;
      const content = String(body['content'] || '').trim();
      if (!content) return errResp(400, 'EMPTY_MESSAGE', '发送消息内容不能为空');

      const sess = this.ensureSession(ws, emp);
      const tLabel = nowTimeLabel();
      const messageId = typeof body['clientMessageId'] === 'string' && body['clientMessageId'].trim()
        ? body['clientMessageId'].trim()
        : `msg-u-${Date.now().toString(36)}_${Math.random().toString(36).slice(2, 5)}`;
      const executionId = `exec_${emp.id}_${Date.now().toString(36)}_${Math.random().toString(36).slice(2, 5)}`;
      const userMsg = {
        id: messageId,
        sender: 'user' as const,
        content,
        time: tLabel,
        executionId,
        status: 'accepted' as const
      };
      sess.messages.push(userMsg);
      sess.activeExecutionId = executionId;
      sess.updatedAt = tLabel;

      const execRec: ChatExecutionStatus = {
        executionId,
        employeeId: emp.id,
        sessionId: sess.id,
        messageId,
        status: 'accepted',
        thinking: '正在理解您的需求并检查可用能力...',
        updatedAt: nowFullTimestamp()
      };
      ws.chatExecutions[executionId] = execRec;
      this.timers.push({ kind: 'chat_execution', wsId, targetId: executionId, createdAtMs: Date.now(), opId: executionId, extra: content });

      const respBody = {
        sessionId: sess.id,
        messageId,
        executionId,
        status: 'accepted' as const,
        userMessage: clone(userMsg),
        session: clone(sess)
      };
      const httpResp = okResp(respBody, 202);
      this.storeIdempotencyHit(wsId, 'POST', subPath, idemKey, body, httpResp);
      return httpResp;
    }

    const execStatusMatch = /^\/entities\/([^/]+)\/conversation\/executions\/([^/]+)$/.exec(subPath);
    if (execStatusMatch && execStatusMatch[1] && execStatusMatch[2] && method === 'GET') {
      const empId = decodeURIComponent(execStatusMatch[1]);
      const execId = decodeURIComponent(execStatusMatch[2]);
      const exec = ws.chatExecutions[execId];
      if (!exec || exec.employeeId !== empId) {
        return errResp(404, 'EXECUTION_NOT_FOUND', `未找到执行记录 ${execId}`);
      }
      return okResp(clone(exec));
    }

    const execIntMatch = /^\/entities\/([^/]+)\/conversation\/executions\/([^/]+)\/interrupt$/.exec(subPath);
    if (execIntMatch && execIntMatch[1] && execIntMatch[2] && method === 'POST') {
      const empId = decodeURIComponent(execIntMatch[1]);
      const execId = decodeURIComponent(execIntMatch[2]);
      const emp = ws.employees.find(e => e.id === empId);
      if (!emp) return errResp(404, 'ENTITY_NOT_FOUND', 'AI 员工不存在');
      const exec = ws.chatExecutions[execId];
      if (!exec || exec.employeeId !== empId) {
        return errResp(404, 'EXECUTION_NOT_FOUND', `未找到要中断的执行 ${execId}`);
      }
      const sess = this.ensureSession(ws, emp);
      if (exec.status === 'accepted' || exec.status === 'running') {
        exec.status = 'interrupted';
        exec.updatedAt = nowFullTimestamp();
        const intMsg = {
          id: `msg-int-${execId}`,
          sender: 'assistant' as const,
          content: '已停止当前处理，对话记录已保留。',
          time: nowTimeLabel(),
          executionId: execId,
          status: 'interrupted' as const
        };
        exec.assistantMessage = intMsg;
        sess.messages.push(intMsg);
      }
      if (sess.activeExecutionId === execId) {
        sess.activeExecutionId = undefined;
      }
      return okResp({ execution: clone(exec), session: clone(sess) });
    }

    const intMatch = /^\/entities\/([^/]+)\/conversation\/interrupt$/.exec(subPath);
    if (intMatch && intMatch[1] && method === 'POST') {
      const empId = decodeURIComponent(intMatch[1]);
      const emp = ws.employees.find(e => e.id === empId);
      if (!emp) return errResp(404, 'ENTITY_NOT_FOUND', 'AI 员工不存在');
      const sess = this.ensureSession(ws, emp);
      const execId = typeof body['executionId'] === 'string' && body['executionId'] ? body['executionId'] : sess.activeExecutionId;
      if (execId && ws.chatExecutions[execId]) {
        const exec = ws.chatExecutions[execId]!;
        if (exec.status === 'accepted' || exec.status === 'running') {
          exec.status = 'interrupted';
          exec.updatedAt = nowFullTimestamp();
        }
      }
      sess.activeExecutionId = undefined;
      sess.messages.push({
        id: `msg-int-${Date.now()}`,
        sender: 'assistant',
        content: '已停止当前处理，对话记录已保留。',
        time: nowTimeLabel(),
        executionId: execId,
        status: 'interrupted'
      });
      return okResp(clone(sess));
    }

    return null;
  }

  private ensureSession(ws: WorkspaceState, emp: DigitalEmployee): EmployeeConversationSession {
    const existing = ws.employeeSessions[emp.id];
    if (existing) return existing;
    const created: EmployeeConversationSession = {
      id: `sess-${emp.id}`,
      employeeId: emp.id,
      title: `与「${emp.name}」的对话`,
      createdAt: '今天',
      updatedAt: '刚刚',
      messages: [
        {
          id: `msg-welcome-${emp.id}`,
          sender: 'assistant',
          content: `你好！我是 ${emp.name}（${emp.role}）。直接下达指令或提问即可。`,
          time: '刚刚',
          thinking: '正在初始化对话...'
        }
      ],
      sessionTasks: []
    };
    ws.employeeSessions[emp.id] = created;
    return created;
  }

  private recordOperationReceipt<T>(
    wsId: string,
    method: string,
    subPath: string,
    idemKey: string | null,
    body: Record<string, unknown>,
    receipt: AsyncOperationReceipt<T>,
    httpStatus = (receipt.status === 'accepted' || receipt.status === 'processing') ? 202 : 200
  ): MockHttpResponse {
    const finalReceipt: AsyncOperationReceipt<T> = idemKey ? { ...receipt, idempotencyKey: idemKey } : receipt;
    this.operations[finalReceipt.operationId] = clone(finalReceipt) as AsyncOperationReceipt;
    const resp = okResp(clone(finalReceipt), httpStatus);
    if (idemKey) {
      this.storeIdempotencyHit(wsId, method, subPath, idemKey, body, resp);
      this.operationsByIdempotencyKey[`${wsId}::${idemKey}`] = finalReceipt.operationId;
    }
    return resp;
  }

  private routeTasksAndWorkItems(
    wsId: string,
    ws: WorkspaceState,
    method: string,
    subPath: string,
    body: Record<string, unknown>,
    parsedUrl: URL = new URL('http://localhost/'),
    headers?: Record<string, string | string[] | undefined>
  ): MockHttpResponse | null {
    // Query operation receipt by idempotencyKey or ID
    if (subPath === '/operations' && method === 'GET') {
      const qKey = (parsedUrl.searchParams.get('idempotencyKey') || '').trim();
      if (!qKey) return errResp(400, 'MISSING_IDEMPOTENCY_KEY', '请提供操作查询标识');
      const opId = this.operationsByIdempotencyKey[`${wsId}::${qKey}`];
      const op = opId ? this.operations[opId] : undefined;
      if (!op) return errResp(404, 'OPERATION_NOT_FOUND', '未找到上次操作记录');
      return okResp({ ...clone(op), replayed: true });
    }
    const opMatch = /^\/operations\/([^/]+)$/.exec(subPath);
    if (opMatch && opMatch[1] && method === 'GET') {
      const opId = decodeURIComponent(opMatch[1]);
      const op = this.operations[opId];
      if (!op) return errResp(404, 'OPERATION_NOT_FOUND', '操作记录不存在');
      return okResp(clone(op));
    }

    const idemKey = method === 'POST' ? getIdempotencyKey(body, headers) : null;
    const idemHit = this.resolveIdempotencyHitOrConflict(wsId, method, subPath, idemKey, body);
    if (idemHit) return idemHit;

    // Work Items
    if (subPath === '/work-items' && method === 'GET') {
      const hasPagination = parsedUrl.searchParams.has('page') || parsedUrl.searchParams.has('pageSize') || parsedUrl.searchParams.get('summary') === 'true';
      if (!hasPagination) {
        return okResp(clone(ws.workItems));
      }
      const page = Math.max(1, Number(parsedUrl.searchParams.get('page') || 1));
      const pageSize = Math.min(50, Math.max(1, Number(parsedUrl.searchParams.get('pageSize') || 20)));
      const offset = (page - 1) * pageSize;
      const sliced = ws.workItems.slice(offset, offset + pageSize).map(w => parsedUrl.searchParams.get('summary') === 'true'
        ? { id: w.id, code: w.code, version: w.version, title: w.title, status: w.status, deliveryStatus: w.deliveryStatus, updatedAt: w.updatedAt }
        : clone(w));
      return okResp({ items: sliced, page, pageSize, total: ws.workItems.length, hasMore: offset + pageSize < ws.workItems.length });
    }
    const wiMatch = /^\/work-items\/([^/]+)\/(notes|reassign|takeover|verify-unknown|accept|cancel|resend-delivery)$/.exec(subPath);
    if (wiMatch && wiMatch[1] && wiMatch[2] && method === 'POST') {
      const wiId = decodeURIComponent(wiMatch[1]);
      const action = wiMatch[2];
      const wi = ws.workItems.find(w => w.id === wiId || w.code === wiId);
      if (!wi) return errResp(404, 'WORK_ITEM_NOT_FOUND', '未找到指定协作事项');
      const conflict = checkVersionConflict(wi.version || 1, `wi-${wi.id}`, body, headers);
      if (conflict) return conflict;
      const t = nowTimeLabel();

      if (action === 'notes') {
        const text = String(body['note'] || '').trim();
        const intent = body['intent'] === 'record' ? 'record' : 'execute';
        if (!text) return errResp(400, 'EMPTY_NOTE', '补充说明内容不能为空');
        if (wi.status === 'canceled' || wi.status === 'completed') {
          wi.events.unshift({
            id: `evt-${Date.now()}`,
            type: 'note',
            title: '补充说明（未采纳：事项已结束）',
            time: t,
            detail: text,
            adoptionStatus: 'unadoptable',
            adoptionStage: '事项已结束，仅做归档备注'
          });
          wi.supplementAdopted = false;
          wi.supplementAdoptionDetail = { status: 'unadoptable', stage: '事项已闭环/终止', intent, note: text };
        } else if (intent === 'record') {
          wi.events.unshift({
            id: `evt-${Date.now()}`,
            type: 'note',
            title: '补充记录（仅存档不重跑）',
            time: t,
            detail: text,
            adoptionStatus: 'record_only',
            adoptionStage: '仅记录在时间线，不打断当前执行步骤'
          });
        } else {
          wi.events.unshift({
            id: `evt-${Date.now()}`,
            type: 'note',
            title: '补充指引已提交',
            time: t,
            detail: text,
            adoptionStatus: 'pending',
            adoptionStage: '将在下一步执行中参考'
          });
          wi.supplementAdopted = false;
          wi.supplementAdoptionDetail = {
            status: 'pending',
            stage: '等待下一阶段执行采纳',
            intent: 'execute',
            note: text
          };
          this.timers.push({ kind: 'work_item_note', wsId, targetId: wi.id, createdAtMs: Date.now() });
        }
        wi.updatedAt = '刚刚';
        wi.version = (wi.version || 1) + 1;
        return okResp(clone(wi));
      }

      if (action === 'reassign' || action === 'takeover') {
        const targetEmpId = String(body['targetEmployeeId'] || wi.ownerEmployeeId);
        const targetEmp = ws.employees.find(e => e.id === targetEmpId);
        const targetName = targetEmp?.name || String(body['targetEmployeeName'] || '人工值守接管');
        const oldOwner = ws.employees.find(e => e.id === wi.ownerEmployeeId)?.name || '原责任员工';
        wi.ownerEmployeeId = targetEmpId;
        wi.assignments.push({
          id: `asg-${Date.now()}`,
          employeeName: action === 'takeover' ? '管理员人工接管' : targetName,
          sourceEmployeeName: oldOwner,
          action: action === 'takeover' ? '已由管理员接管处理' : `平台流转改派给「${targetName}」`,
          timestamp: t
        });
        wi.events.unshift({
          id: `evt-${Date.now()}`,
          type: 'handoff',
          title: action === 'takeover' ? '管理员人工接管事项' : `改派给「${targetName}」`,
          time: t,
          detail: String(body['reason'] || '通过控制台显式介入流转')
        });
        wi.updatedAt = '刚刚';
        wi.version = (wi.version || 1) + 1;
        return okResp(clone(wi));
      }

      if (action === 'verify-unknown') {
        if (wi.status !== 'unknown') {
          return errResp(409, 'INVALID_STATUS', '仅结果待核对的事项可发起核对');
        }
        if (wi.activeOperationId && this.operations[wi.activeOperationId]?.status === 'accepted') {
          const existingOp = this.operations[wi.activeOperationId]!;
          return okResp({ ...clone(existingOp), replayed: true }, 202);
        }
        const opId = `op_wi_verify_${Date.now().toString(36)}_${Math.random().toString(36).slice(2, 5)}`;
        wi.activeOperationId = opId;
        wi.deliveryStatus = '正在核对最新结果...';
        wi.version = (wi.version || 1) + 1;
        wi.events.unshift({
          id: `evt-${Date.now()}`,
          type: 'verify',
          title: '已发起结果核对',
          time: t,
          detail: '正在确认最新执行状态与产物'
        });
        wi.updatedAt = '刚刚';
        const simFail = body['simulateFailure'] === true ? 'simulate_fail' : undefined;
        this.timers.push({ kind: 'wi_verify', wsId, targetId: wi.id, createdAtMs: Date.now(), opId, extra: simFail });
        return this.recordOperationReceipt(wsId, method, subPath, idemKey, body, {
          operationId: opId,
          action: 'work_item_verify_unknown',
          targetId: wi.id,
          status: 'accepted',
          outcome: 'verification_pending',
          message: '已发起结果核对，请稍候查看最新状态',
          resourceVersion: wi.version,
          data: clone(wi)
        });
      }

      if (action === 'accept') {
        if (wi.status !== 'awaiting_acceptance') {
          return errResp(409, 'INVALID_STATUS', '仅「待验收」状态的事项允许执行验收通过');
        }
        wi.status = 'completed';
        wi.deliveryStatus = '验收通过 · 结果卡片已发送';
        wi.events.unshift({
          id: `evt-${Date.now()}`,
          type: 'accept',
          title: '管理员确认验收通过，事项已闭环',
          time: t,
          detail: '最终成果卡片已归档并同步至来源群聊'
        });
        wi.updatedAt = '刚刚';
        wi.version = (wi.version || 1) + 1;
        this.pushActivity(ws, 'tasks', `事项 ${wi.code}「${wi.title}」已由管理员验收闭环`, 'success', '事项闭环');
        return okResp(clone(wi));
      }

      if (action === 'cancel') {
        if (wi.status === 'canceled' || wi.status === 'completed') {
          return errResp(409, 'INVALID_STATUS', '该事项已结束，无法重复取消');
        }
        if (wi.status === 'canceling' && wi.activeOperationId && this.operations[wi.activeOperationId]) {
          return okResp({ ...clone(this.operations[wi.activeOperationId]!), replayed: true }, 202);
        }
        const opId = `op_wi_cancel_${Date.now().toString(36)}_${Math.random().toString(36).slice(2, 5)}`;
        const reason = String(body['reason'] || '操作员发起终止请求');
        wi.status = 'canceling';
        wi.activeOperationId = opId;
        wi.deliveryStatus = '正在停止执行...';
        wi.version = (wi.version || 1) + 1;
        wi.events.unshift({
          id: `evt-${Date.now()}`,
          type: 'cancel',
          title: '已提交终止请求',
          time: t,
          detail: reason
        });
        wi.updatedAt = '刚刚';
        this.timers.push({ kind: 'wi_cancel', wsId, targetId: wi.id, createdAtMs: Date.now(), opId, extra: reason });
        return this.recordOperationReceipt(wsId, method, subPath, idemKey, body, {
          operationId: opId,
          action: 'work_item_cancel',
          targetId: wi.id,
          status: 'accepted',
          outcome: 'canceling',
          message: '正在终止执行，请稍候',
          resourceVersion: wi.version,
          data: clone(wi)
        });
      }

      if (action === 'resend-delivery') {
        const opId = `op_wi_resend_${Date.now().toString(36)}_${Math.random().toString(36).slice(2, 5)}`;
        wi.deliveryStatus = '结果通知已重新发送';
        wi.version = (wi.version || 1) + 1;
        wi.events.unshift({
          id: `evt-${Date.now()}`,
          type: 'delivery',
          title: '已重发结果通知',
          time: t,
          detail: `目标群聊：${wi.originGroup}`
        });
        return this.recordOperationReceipt(wsId, method, subPath, idemKey, body, {
          operationId: opId,
          action: 'work_item_resend_delivery',
          targetId: wi.id,
          status: 'succeeded',
          outcome: 'delivery_resent_without_rerun',
          deliveryStatus: 'delivered',
          taskResumeStatus: 'not_applicable',
          message: '结果通知已重新发送',
          resourceVersion: wi.version,
          data: clone(wi)
        });
      }
    }

    // Lightweight badge summary for Pending Human Approvals (avoid full payload on badge refresh)
    if (subPath === '/work-items/pending-human/summary' && method === 'GET') {
      const pendingOnly = ws.humanApprovals.filter(a => a.status === 'pending');
      const versionSum = ws.humanApprovals.reduce((acc, a) => acc + (a.version || 1), 0);
      return okResp({
        count: pendingOnly.length,
        version: versionSum,
        latestUpdatedAt: nowFullTimestamp()
      });
    }
    // Pending Human Approvals (Unified HITL)
    if (subPath === '/work-items/pending-human' && method === 'GET') {
      const pendingOnly = ws.humanApprovals.filter(a => a.status === 'pending');
      return okResp(clone(pendingOnly));
    }
    if (subPath === '/approvals' && method === 'GET') {
      const hasPagination = parsedUrl.searchParams.has('page') || parsedUrl.searchParams.has('pageSize');
      if (!hasPagination) {
        return okResp(clone(ws.humanApprovals));
      }
      const page = Math.max(1, Number(parsedUrl.searchParams.get('page') || 1));
      const pageSize = Math.min(50, Math.max(1, Number(parsedUrl.searchParams.get('pageSize') || 20)));
      const offset = (page - 1) * pageSize;
      return okResp({
        items: clone(ws.humanApprovals.slice(offset, offset + pageSize)),
        page,
        pageSize,
        total: ws.humanApprovals.length,
        hasMore: offset + pageSize < ws.humanApprovals.length
      });
    }
    const appActionMatch = /^\/work-items\/pending-human\/([^/]+)\/(approve|reject|reassign)$/.exec(subPath);
    if (appActionMatch && appActionMatch[1] && appActionMatch[2] && method === 'POST') {
      const appId = decodeURIComponent(appActionMatch[1]);
      const action = appActionMatch[2];
      const item = ws.humanApprovals.find(a => a.id === appId);
      if (!item) return errResp(404, 'APPROVAL_NOT_FOUND', '未找到对应人工确认单');
      const conflict = checkVersionConflict(item.version || 1, `appr-${item.id}`, body, headers);
      if (conflict) return conflict;

      if (item.status !== 'pending') {
        return errResp(
          409,
          'APPROVAL_ALREADY_DECIDED',
          `该确认单已处理完成（当前状态：${item.status === 'approved' ? '已批准' : '已拒绝'}），禁止重复提交或覆盖`
        );
      }

      if (action === 'approve') {
        const selectedOption = body['selectedOption'] ? String(body['selectedOption']) : undefined;
        if (item.options && item.options.length > 0 && !selectedOption) {
          return errResp(400, 'EXPLICIT_OPTION_REQUIRED', '必须显式选择一项裁决方案后才能提交确认');
        }
        const simFail = body['simulateResumeFailure'] === true ? 'simulate_fail' : undefined;
        const opId = `op_appr_approve_${Date.now().toString(36)}_${Math.random().toString(36).slice(2, 5)}`;
        item.status = 'approved';
        item.selectedOption = selectedOption;
        item.decisionOperationId = opId;
        item.version = (item.version || 1) + 1;

        const linkedTask = ws.tasks.find(t => t.taskId === item.taskId || t.id === item.taskId);
        if (linkedTask) {
          // 确认通过仅代表已放行恢复执行，绝不直接标为任务已完成
          linkedTask.status = 'running';
          linkedTask.activeOperationId = opId;
          linkedTask.version = (linkedTask.version || 1) + 1;
          linkedTask.logs.push(`[人工确认] 已批准放行${selectedOption ? ` (${selectedOption})` : ''}，任务继续执行`);
          this.timers.push({ kind: 'approval_task_resume', wsId, targetId: linkedTask.taskId, createdAtMs: Date.now(), opId, extra: simFail });
        }
        if (item.workItemId) {
          const wi = ws.workItems.find(w => w.id === item.workItemId);
          if (wi) {
            wi.status = 'in_progress';
            wi.version = (wi.version || 1) + 1;
            wi.events.unshift({
              id: `evt-app-${Date.now()}`,
              type: 'approval',
              title: '人工确认已通过，任务继续推进中',
              time: nowTimeLabel(),
              detail: selectedOption ? `选定方案：${selectedOption}` : '已批准继续执行'
            });
          }
        }
        this.pushActivity(ws, 'alerts', `人工裁决已通过：${item.title}（后续任务继续推进中）`, 'info', '已放行');
        return this.recordOperationReceipt(wsId, method, subPath, idemKey, body, {
          operationId: opId,
          action: 'approval_approve',
          targetId: item.id,
          status: 'accepted',
          outcome: 'decision_recorded_task_resumed',
          approvalStatus: 'approved',
          taskResumeStatus: linkedTask ? 'running' : 'not_applicable',
          deliveryStatus: 'delivered',
          message: '已批准，关联任务继续执行中',
          resourceVersion: item.version,
          data: clone(item)
        });
      }

      if (action === 'reject') {
        const rejectReason = String(body['rejectReason'] || '').trim() || '管理员认为存在安全或合规风险，已人工拒绝';
        const opId = `op_appr_reject_${Date.now().toString(36)}_${Math.random().toString(36).slice(2, 5)}`;
        item.status = 'rejected';
        item.rejectReason = rejectReason;
        item.decisionOperationId = opId;
        item.version = (item.version || 1) + 1;

        const linkedTask = ws.tasks.find(t => t.taskId === item.taskId || t.id === item.taskId);
        if (linkedTask) {
          linkedTask.status = 'rejected';
          linkedTask.rejectReason = rejectReason;
          linkedTask.version = (linkedTask.version || 1) + 1;
          linkedTask.logs.push(`[人工拒绝] ${rejectReason}`);
        }
        if (item.workItemId) {
          const wi = ws.workItems.find(w => w.id === item.workItemId);
          if (wi) {
            wi.status = 'canceled';
            wi.waitReason = rejectReason;
            wi.version = (wi.version || 1) + 1;
            wi.events.unshift({
              id: `evt-rej-${Date.now()}`,
              type: 'reject',
              title: '已拒绝并终止执行',
              time: nowTimeLabel(),
              detail: rejectReason
            });
          }
        }
        this.pushActivity(ws, 'alerts', `人工拒绝请求：${item.title}`, 'error', '已拒绝');
        return this.recordOperationReceipt(wsId, method, subPath, idemKey, body, {
          operationId: opId,
          action: 'approval_reject',
          targetId: item.id,
          status: 'succeeded',
          outcome: 'decision_rejected',
          approvalStatus: 'rejected',
          taskResumeStatus: 'rejected',
          deliveryStatus: 'delivered',
          message: '已拒绝并终止关联任务',
          resourceVersion: item.version,
          data: clone(item)
        });
      }

      if (action === 'reassign') {
        const assignee = normalizePrincipalRef(body['assignee']);
        if (!assignee || assignee.targetType !== 'user') {
          return errResp(400, 'INVALID_ASSIGNEE', '改派确认人必须提交结构化人员身份 { targetType: "user", channel, connectionId, targetId }，禁止仅传展示名');
        }
        const matchedUser = resolveUserByPrincipalOrId(ws, assignee);
        if (!matchedUser) {
          return errResp(404, 'ASSIGNEE_NOT_FOUND', `未在渠道 ${assignee.channel} (${assignee.connectionId}) 找到指定成员 ${assignee.targetId}`);
        }
        const opId = `op_appr_reassign_${Date.now().toString(36)}_${Math.random().toString(36).slice(2, 5)}`;
        const chTag = assignee.channel === 'feishu' ? '飞书' : assignee.channel === 'wecom' ? '企业微信' : assignee.channel === 'dingtalk' ? '钉钉' : '网页';
        const resolvedLabel = `${matchedUser.name} (${chTag} · ${assignee.connectionId})`;

        let isWecomBound = true;
        if (assignee.channel === 'wecom') {
          isWecomBound = Boolean(
            ws.wecomScopedBindings[`${assignee.connectionId}::${matchedUser.id}`] ||
            ws.wecomScopedBindings[`${assignee.connectionId}::${assignee.targetId}`]
          );
        }

        item.assignee = {
          ...assignee,
          displayName: matchedUser.name,
          canonicalKey: buildCanonicalPrincipalKey(assignee)
        };
        item.recipientLabel = resolvedLabel;
        item.reassignSaved = true;
        item.decisionOperationId = opId;
        item.version = (item.version || 1) + 1;

        if (!isWecomBound) {
          item.notificationStatus = 'blocked';
          item.deliveryStatus = 'awaiting_binding';
          item.blockedReason = `新确认人「${matchedUser.name}」尚未在当前企业微信连接 (${assignee.connectionId}) 完成私聊核验绑定，审批卡片等待绑定完成后送达`;
        } else {
          item.notificationStatus = 'sent';
          item.deliveryStatus = 'delivered';
          item.blockedReason = undefined;
        }

        return this.recordOperationReceipt(wsId, method, subPath, idemKey, body, {
          operationId: opId,
          action: 'approval_reassign',
          targetId: item.id,
          status: 'succeeded',
          outcome: isWecomBound ? 'reassigned_and_delivered' : 'reassigned_awaiting_binding',
          reassignSaved: true,
          approvalStatus: 'pending',
          taskResumeStatus: 'not_applicable',
          deliveryStatus: item.deliveryStatus,
          message: isWecomBound
            ? `改派保存成功，审批卡片已送达「${matchedUser.name}」`
            : `改派保存成功，但「${matchedUser.name}」企微尚未绑定，通知处于等待绑定状态`,
          resourceVersion: item.version,
          data: clone(item)
        });
      }
    }

    // Tasks
    if (subPath === '/tasks' && method === 'GET') {
      const hasPagination = parsedUrl.searchParams.has('page') || parsedUrl.searchParams.has('pageSize') || parsedUrl.searchParams.get('summary') === 'true';
      if (!hasPagination) {
        return okResp(clone(ws.tasks));
      }
      const page = Math.max(1, Number(parsedUrl.searchParams.get('page') || 1));
      const pageSize = Math.min(50, Math.max(1, Number(parsedUrl.searchParams.get('pageSize') || 20)));
      const offset = (page - 1) * pageSize;
      const sliced = ws.tasks.slice(offset, offset + pageSize).map(t => parsedUrl.searchParams.get('summary') === 'true'
        ? { id: t.id, taskId: t.taskId, version: t.version, employeeName: t.employeeName, status: t.status, delivery: t.delivery, duration: t.duration, createdAt: t.createdAt }
        : clone(t));
      return okResp({ items: sliced, page, pageSize, total: ws.tasks.length, hasMore: offset + pageSize < ws.tasks.length });
    }
    const taskActionMatch = /^\/tasks\/([^/]+)\/(retry|cancel|verify-result|resend-delivery)$/.exec(subPath);
    if (taskActionMatch && taskActionMatch[1] && taskActionMatch[2] && method === 'POST') {
      const tId = decodeURIComponent(taskActionMatch[1]);
      const action = taskActionMatch[2];
      const task = ws.tasks.find(t => t.id === tId || t.taskId === tId);
      if (!task) return errResp(404, 'TASK_NOT_FOUND', '未找到指定执行任务');
      const conflict = checkVersionConflict(task.version || 1, `task-${task.id}`, body, headers);
      if (conflict) return conflict;
      const t = nowTimeLabel();

      if (action === 'retry') {
        if (task.status === 'rejected') {
          return errResp(409, 'REJECTED_CANNOT_RETRY', '该任务已被人工拒绝终止，如需处理请重新发起指令');
        }
        if (task.status === 'running' || task.status === 'canceling') {
          return errResp(409, 'TASK_IN_PROGRESS', '任务正在处理中，请等待本次运行结束后再试');
        }
        const opId = `op_task_retry_${Date.now().toString(36)}_${Math.random().toString(36).slice(2, 5)}`;
        task.runs = task.runs || [];
        const nextIdx = task.runs.length + 1;
        const runId = `run_${task.taskId}_${nextIdx}`;
        // 创建一条独立的新执行记录，并显式关联 parentTaskId，绝不覆盖历史 runs
        task.runs.push({
          runIndex: nextIdx,
          runId,
          parentTaskId: task.taskId,
          operationId: opId,
          startedAt: t,
          duration: '执行中...',
          status: 'running',
          triggerBy: '控制台人工重试',
          toolExecutions: [],
          logs: [`[${t}] 已发起重新运行，正在执行中...`]
        });
        task.status = 'running';
        task.activeOperationId = opId;
        task.version = (task.version || 1) + 1;
        task.logs.push(`[${t}] 已发起第 ${nextIdx} 次运行`);
        this.timers.push({ kind: 'task_retry', wsId, targetId: task.id, createdAtMs: Date.now(), opId, extra: runId });
        return this.recordOperationReceipt(wsId, method, subPath, idemKey, body, {
          operationId: opId,
          action: 'task_retry',
          targetId: task.id,
          status: 'accepted',
          outcome: 'rerun_run_created',
          message: '已发起重新运行，任务正在执行中',
          resourceVersion: task.version,
          data: clone(task)
        });
      }

      if (action === 'cancel') {
        if (task.status === 'canceled' || task.status === 'succeeded' || task.status === 'rejected') {
          return errResp(409, 'INVALID_STATUS', '任务已结束，不可中止');
        }
        if (task.status === 'canceling' && task.activeOperationId && this.operations[task.activeOperationId]) {
          return okResp({ ...clone(this.operations[task.activeOperationId]!), replayed: true }, 202);
        }
        const opId = `op_task_cancel_${Date.now().toString(36)}_${Math.random().toString(36).slice(2, 5)}`;
        task.status = 'canceling';
        task.activeOperationId = opId;
        task.version = (task.version || 1) + 1;
        task.logs.push(`[${t}] 正在停止任务执行...`);
        this.timers.push({ kind: 'task_cancel', wsId, targetId: task.id, createdAtMs: Date.now(), opId });
        return this.recordOperationReceipt(wsId, method, subPath, idemKey, body, {
          operationId: opId,
          action: 'task_cancel',
          targetId: task.id,
          status: 'accepted',
          outcome: 'canceling',
          message: '正在中止任务，请稍候',
          resourceVersion: task.version,
          data: clone(task)
        });
      }

      if (action === 'verify-result') {
        if (task.status !== 'unknown') {
          return errResp(409, 'INVALID_STATUS', '仅结果待核对的任务支持核对状态');
        }
        if (task.activeOperationId && this.operations[task.activeOperationId]?.status === 'accepted') {
          return okResp({ ...clone(this.operations[task.activeOperationId]!), replayed: true }, 202);
        }
        const opId = `op_task_verify_${Date.now().toString(36)}_${Math.random().toString(36).slice(2, 5)}`;
        task.activeOperationId = opId;
        task.version = (task.version || 1) + 1;
        // 核对已受理时保持 unknown，绝不立即改成 succeeded
        task.logs.push(`[${t}] 正在核对任务最新执行状态...`);
        const simFail = body['simulateFailure'] === true ? 'simulate_fail' : undefined;
        this.timers.push({ kind: 'task_verify', wsId, targetId: task.id, createdAtMs: Date.now(), opId, extra: simFail });
        return this.recordOperationReceipt(wsId, method, subPath, idemKey, body, {
          operationId: opId,
          action: 'task_verify_result',
          targetId: task.id,
          status: 'accepted',
          outcome: 'verification_pending',
          message: '正在核对最新执行结果，请稍候查看',
          resourceVersion: task.version,
          data: clone(task)
        });
      }

      if (action === 'resend-delivery') {
        const dId = body['deliveryId'] ? String(body['deliveryId']) : '';
        const opId = `op_task_resend_${Date.now().toString(36)}_${Math.random().toString(36).slice(2, 5)}`;
        task.delivery = 'delivered';
        task.version = (task.version || 1) + 1;
        const deliveries: DeliveryRecord[] = task.deliveries || [];
        const targetRec = dId ? deliveries.find(d => d.id === dId) : deliveries[0];
        if (targetRec) {
          targetRec.status = 'delivered';
          targetRec.sentAt = t;
          targetRec.statusCode = '已送达';
          targetRec.statusDetail = '结果通知已重新发送成功';
          targetRec.resendCount += 1;
        }
        // 严格只写回告记录，不修改 runs 或重跑任务
        task.logs.push(`[${t}] 已重新发送结果通知`);
        return this.recordOperationReceipt(wsId, method, subPath, idemKey, body, {
          operationId: opId,
          action: 'task_resend_delivery',
          targetId: task.id,
          status: 'succeeded',
          outcome: 'delivery_resent_without_rerun',
          deliveryStatus: 'delivered',
          taskResumeStatus: 'not_applicable',
          message: '结果通知已重新发送',
          resourceVersion: task.version,
          data: clone(task)
        });
      }
    }

    return null;
  }

  private routeSettings(
    ws: WorkspaceState,
    method: string,
    subPath: string,
    body: Record<string, unknown>
  ): MockHttpResponse | null {
    if (subPath === '/settings' && method === 'GET') {
      return okResp(clone(ws.settings));
    }
    if (subPath === '/settings' && (method === 'PUT' || method === 'PATCH')) {
      if (typeof body['name'] === 'string') {
        ws.settings.name = body['name'];
        ws.info.name = body['name'];
      }
      if (typeof body['desc'] === 'string') {
        ws.settings.desc = body['desc'];
        ws.info.desc = body['desc'];
      }
      if (typeof body['autoTrackingWorkItems'] === 'boolean') ws.settings.autoTrackingWorkItems = body['autoTrackingWorkItems'];
      if (typeof body['defaultApprovalTimeoutHours'] === 'string') ws.settings.defaultApprovalTimeoutHours = body['defaultApprovalTimeoutHours'];
      if (typeof body['notificationGroup'] === 'string') ws.settings.notificationGroup = body['notificationGroup'];
      this.pushActivity(ws, 'policy_changes', `工作区全局协同与安全策略已保存`, 'info', '策略保存');
      return okResp(clone(ws.settings));
    }
    if (subPath === '/members' && method === 'GET') {
      return okResp(clone(ws.members));
    }
    if (subPath === '/invitations' && method === 'POST') {
      const email = String(body['email'] || '').trim();
      const role = (body['role'] as 'admin' | 'member') || 'member';
      if (!email || !email.includes('@')) return errResp(400, 'INVALID_EMAIL', '请输入合法的团队成员企业邮箱');
      const token = `inv_${Date.now()}_${Math.random().toString(36).slice(2, 8)}`;
      const inviteLink = `https://kiko.corp/join?ws=${ws.info.id}&token=${token}&role=${role}&email=${encodeURIComponent(email)}`;
      const newMember = {
        id: `m-${Date.now()}`,
        name: email.split('@')[0] || '新成员',
        email,
        role,
        status: 'active' as const,
        joinedAt: '刚刚'
      };
      ws.members.push(newMember);
      return okResp({ inviteLink, member: clone(newMember) }, 201);
    }
    const memberMatch = /^\/members\/([^/]+)$/.exec(subPath);
    if (memberMatch && memberMatch[1] && method === 'PATCH') {
      const mId = decodeURIComponent(memberMatch[1]);
      const target = ws.members.find(m => m.id === mId);
      if (!target) return errResp(404, 'MEMBER_NOT_FOUND', '未找到工作区成员');
      if (body['status'] === 'active' || body['status'] === 'disabled') target.status = body['status'];
      if (body['role'] === 'owner' || body['role'] === 'admin' || body['role'] === 'member') target.role = body['role'];
      return okResp(clone(target));
    }
    if (subPath === '/runtime-monitor' && method === 'GET') {
      const monitor = clone(ws.runtimeMonitor);
      monitor.totalEmployees = ws.employees.length;
      monitor.runningEmployees = ws.employees.filter(e => e.status === 'active').length;
      monitor.warningEmployees = ws.employees.filter(e => e.status === 'warning').length;
      monitor.errorEmployees = ws.employees.filter(e => e.channels.some(ch => ch.status === 'error')).length;
      return okResp(monitor);
    }
    if (subPath === '/runtime-monitor/inspect-channels' && method === 'POST') {
      for (const ch of ws.runtimeMonitor.channels) {
        ch.lastHeartbeat = '刚刚';
        ch.status = 'online';
      }
      return okResp({
        ok: true,
        avgLatency: '45ms',
        monitor: clone(ws.runtimeMonitor)
      });
    }
    const cronMatch = /^\/runtime-monitor\/cron-jobs\/([^/]+)\/run$/.exec(subPath);
    if (cronMatch && cronMatch[1] && method === 'POST') {
      const cId = decodeURIComponent(cronMatch[1]);
      const job = ws.runtimeMonitor.cronJobs.find(j => j.id === cId || j.name === cId);
      if (!job) return errResp(404, 'CRON_NOT_FOUND', '未找到指定定时巡检作业');
      job.lastStatus = '成功 (手动触发)';
      return okResp(clone(job));
    }

    return null;
  }
}

export const sharedMockBackend = new D2MockBackend();

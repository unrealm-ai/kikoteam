import type { CallToolResult } from '@modelcontextprotocol/server';

const DETAIL_KEYS = new Set([
  'matchLimitReached',
  'linesTruncated',
  'resultLimitReached',
  'entryLimitReached',
  'diff',
  'patch',
  'firstChangedLine',
]);
const TRUNCATION_KEYS = new Set([
  'truncated',
  'truncatedBy',
  'totalLines',
  'totalBytes',
  'outputLines',
  'outputBytes',
  'lastLinePartial',
  'firstLineExceedsLimit',
  'maxLines',
  'maxBytes',
]);

function truncationDetails(value: unknown): Record<string, unknown> | undefined {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return undefined;
  const metadata: Record<string, unknown> = {};
  for (const [key, field] of Object.entries(value)) {
    if (
      TRUNCATION_KEYS.has(key) &&
      (field === null || ['boolean', 'number', 'string'].includes(typeof field))
    )
      metadata[key] = field;
  }
  return metadata;
}

export interface NativeResult {
  content: ({ type: 'text'; text: string } | { type: 'image'; data: string; mimeType: string })[];
  details?: unknown;
}

export function toMcpResult(result: NativeResult): CallToolResult {
  const content: CallToolResult['content'] = result.content.map((part) =>
    part.type === 'text'
      ? { type: 'text', text: part.text }
      : { type: 'image', data: part.data, mimeType: part.mimeType },
  );
  if (!result.details || typeof result.details !== 'object' || Array.isArray(result.details))
    return { content };
  const details: Record<string, unknown> = {};
  for (const [key, value] of Object.entries(result.details)) {
    if (key === 'truncation') {
      const metadata = truncationDetails(value);
      if (metadata) details.truncation = metadata;
    }
    if (DETAIL_KEYS.has(key) && value !== undefined) details[key] = value;
  }
  // Native read outputSchema is a string/image union. Do not advertise it as an
  // MCP object schema or duplicate the entire text body in structuredContent.
  return Object.keys(details).length ? { content, structuredContent: { details } } : { content };
}

export function toolError(error: unknown, cancelled: boolean): CallToolResult {
  const text = cancelled
    ? 'Operation cancelled; filesystem effects may already have occurred.'
    : error instanceof Error
      ? error.message.slice(0, 4096)
      : 'File tool execution failed.';
  return { isError: true, content: [{ type: 'text', text }] };
}

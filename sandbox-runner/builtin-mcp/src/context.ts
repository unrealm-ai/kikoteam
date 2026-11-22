import { ProtocolError, ProtocolErrorCode } from '@modelcontextprotocol/server';
import type { LogFields } from './logging.js';
import type { ProcessContext } from './process/model.js';

export const CALL_CONTEXT_KEY = 'builtin-mcp/call';
const RESOURCE_ID = /^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$/;
const TRACE_ID = /^(?!0{32}$)[0-9a-f]{32}$/;

// Executor owns the private pipe and authorizes operations. File calls use
// these for correlation; Bash also requires the immutable operation identity
// for effect deduplication and verifies the startup Workspace/Session scope.
export function callContext(meta: Record<string, unknown> | undefined): LogFields {
  const value = meta?.[CALL_CONTEXT_KEY];
  if (value === undefined) return {};
  if (!value || typeof value !== 'object' || Array.isArray(value)) throw invalid();
  const input: Record<string, unknown> = value as Record<string, unknown>;
  if (typeof input.trace_id !== 'string' || !TRACE_ID.test(input.trace_id)) throw invalid();
  if (typeof input.operation_id !== 'string' || !RESOURCE_ID.test(input.operation_id))
    throw invalid();
  const output: LogFields = {
    trace_id: input.trace_id,
    operation_id: input.operation_id,
  };
  for (const key of ['workspace_id', 'session_id', 'runtime_id', 'origin_turn_id'] as const) {
    const field = input[key];
    if (field === undefined) continue;
    if (typeof field !== 'string' || !RESOURCE_ID.test(field)) throw invalid();
    output[key] = field;
  }
  return output;
}

export function processContext(meta: Record<string, unknown> | undefined): ProcessContext {
  const context = callContext(meta);
  for (const key of [
    'workspace_id',
    'session_id',
    'runtime_id',
    'origin_turn_id',
    'operation_id',
    'trace_id',
  ] as const) {
    if (typeof context[key] !== 'string') throw invalid();
  }
  return context as ProcessContext;
}

function invalid(): ProtocolError {
  return new ProtocolError(ProtocolErrorCode.InvalidParams, 'Invalid builtin call context.');
}

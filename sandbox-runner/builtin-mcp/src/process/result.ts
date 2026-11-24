import type { CallToolResult } from '@modelcontextprotocol/server';

export function resultObject(result: CallToolResult): Record<string, unknown> {
  const value = result.structuredContent;
  if (!value || typeof value !== 'object' || Array.isArray(value))
    throw new Error('Invalid process result object.');
  return value as Record<string, unknown>;
}

export function processResult(value: Record<string, unknown>, failed = false): CallToolResult {
  return {
    // A placeholder text masks structured output in Pi's native MCP conversion.
    // Let the client render the structured result, including handles and cursors,
    // without duplicating potentially large process output on the wire.
    content: [],
    structuredContent: value,
    ...(failed ? { isError: true } : {}),
  };
}

export function processError(code: string, extra: Record<string, unknown> = {}): CallToolResult {
  return processResult(
    {
      ...extra,
      error: {
        code,
        message: 'Process operation could not complete. Inspect the error code and process state.',
      },
    },
    true,
  );
}

export class ProcessFailure extends Error {
  constructor(
    readonly code: string,
    readonly detail: Record<string, unknown> = {},
  ) {
    super(code);
  }
}

// Only for failures known to precede command execution.
export class SpawnFailure extends Error {
  constructor(readonly code = 'spawn_failed') {
    super(code);
  }
}

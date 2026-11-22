import { randomUUID } from 'node:crypto';
import { writeSync } from 'node:fs';

export type Level = 'DEBUG' | 'INFO' | 'WARN' | 'ERROR';
const LEVELS: Record<Level, number> = { DEBUG: 0, INFO: 1, WARN: 2, ERROR: 3 };

export type LogFields = {
  port?: number;
  tool_name?: string;
  outcome?: string;
  duration_ms?: number;
  trace_id?: string;
  operation_id?: string;
  process_id?: string;
  origin_turn_id?: string;
  workspace_id?: string;
  session_id?: string;
  runtime_id?: string;
  error_code?: string;
  reason?: string;
  dropped_records?: number;
};

function writeStderr(line: string): void {
  const bytes = Buffer.from(line);
  let offset = 0;
  while (offset < bytes.length) {
    const written = writeSync(2, bytes, offset, bytes.length - offset);
    if (written === 0) throw new Error('Log sink made no progress.');
    offset += written;
  }
}

export function createLogger(level: Level = 'INFO', emit: (line: string) => void = writeStderr) {
  const instance = randomUUID();
  let dropped = 0;
  const output = (event: string, msg: string, fields: LogFields, severity: Level, prefix = '') => {
    emit(
      prefix +
        JSON.stringify({
          time: new Date().toISOString(),
          level: severity,
          service: 'builtin-mcp',
          instance_id: instance,
          module:
            event.startsWith('process.') || fields.tool_name?.startsWith('bash_')
              ? 'builtin.processes'
              : 'builtin.files',
          event,
          msg,
          ...fields,
        }) +
        '\n',
    );
  };
  return (event: string, msg: string, fields: LogFields = {}, severity: Level = 'INFO'): void => {
    if (LEVELS[severity] < LEVELS[level]) return;
    try {
      if (dropped) {
        // A preceding failed write may have left a partial line in the pipe.
        output(
          'log.output_recovered',
          'Log output recovered',
          { dropped_records: dropped },
          'WARN',
          '\n',
        );
        dropped = 0;
      }
      output(event, msg, fields, severity);
    } catch {
      // Never replace a tool result, recurse into the broken sink, or use MCP
      // stdout as a log fallback. Retry on the next record with bounded state.
      dropped = Math.min(Number.MAX_SAFE_INTEGER, dropped + 1);
    }
  };
}

export type Logger = ReturnType<typeof createLogger>;

export function logLevel(value: string | undefined): Level {
  if (!value) return 'INFO';
  const upper = value.toUpperCase();
  if (upper === 'DEBUG' || upper === 'INFO' || upper === 'WARN' || upper === 'ERROR') return upper;
  throw new Error('Invalid log level.');
}

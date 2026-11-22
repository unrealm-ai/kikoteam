import type { CallToolResult } from '@modelcontextprotocol/server';
import type { ActivitySource } from './activity.js';

export type ProcessState = 'starting' | 'running' | 'terminating' | 'exited' | 'lost';
export type Stream = 'stdout' | 'stderr' | 'pty';
export type StopReason = 'user_requested' | 'deadline_reached' | 'session_closed';
export type BashTool = 'bash_start' | 'bash_poll' | 'bash_write' | 'bash_terminate' | 'bash_list';

export interface ProcessContext {
  workspace_id: string;
  session_id: string;
  runtime_id: string;
  origin_turn_id: string;
  operation_id: string;
  trace_id: string;
}

// Supplied by the trusted launcher, never by a tool argument. Runtime is not a
// process ownership key: a later authorized Runtime can manage the same Session.
export interface ProcessConfig {
  runtime_directory?: string;
  activity_source?: ActivitySource;
  workspace_id: string;
  session_id: string;
  environment_instance_id: string;
  executor_boot_id: string;
  state_directory: string;
  cwd: string;
  max_runtime_ms: number;
  allow_force: boolean;
  environment: Readonly<Record<string, string>>;
}

export interface ProcessSnapshot {
  process_id: string;
  state: ProcessState;
  exit_code: number | null;
  termination_signal: string | null;
  deadline_at: string | null;
  group_quiescent: boolean | null;
}

export interface ProcessSummary extends ProcessSnapshot {
  created_at: string;
  started_at: string | null;
  exited_at: string | null;
  cwd: string;
  origin_turn_id: string;
  stop_reason: StopReason | null;
}

export interface ProcessRecord extends ProcessSummary {
  owner_boot: string;
  executor_boot_id: string;
  origin: ProcessContext;
  pid: number | null;
  process_identity: string | null;
  tty: boolean;
  stdin_closed: boolean;
  output_closed: boolean;
  output_expired: boolean;
  output_generation: string;
  first_seq: number;
  next_seq: number;
  retained_bytes: number;
  termination_mode: 'graceful' | 'force' | null;
  kill_at: number | null;
}

export interface OutputChunk {
  stream: Stream;
  text: string;
  encoding_replaced?: true;
}

export interface OutputPage {
  chunks: OutputChunk[];
  next_cursor: string;
  first_available_cursor: string;
  has_more: boolean;
  output_complete: boolean;
  output_truncated: boolean;
}

export interface OperationRecord {
  operation_id: string;
  tool: BashTool;
  digest: string;
  origin: ProcessContext;
  process_id: string | null;
  phase: 'prepared' | 'effect_pending' | 'complete';
  result: CallToolResult | null;
  output_reference?: { from: string; to: string };
}

export interface StartInput {
  command: string;
  cwd: string;
  tty: boolean;
  stdin_open: boolean;
  yield_time_ms: number;
  max_output_bytes: number;
  max_runtime_ms: number;
}

export interface PollInput {
  process_id: string;
  cursor?: string;
  yield_time_ms: number;
  max_output_bytes: number;
}

export interface WriteInput {
  process_id: string;
  text: string;
  close_stdin: boolean;
}

export const PROCESS_LIMIT = 4;
export const OUTPUT_BYTES = 64 * 1024 * 1024;
export const OUTPUT_RETENTION_MS = 24 * 60 * 60 * 1000;
export const TERMINATION_GRACE_MS = 5000;

export function snapshot(record: ProcessRecord): ProcessSnapshot {
  return {
    process_id: record.process_id,
    state: record.state,
    exit_code: record.exit_code,
    termination_signal: record.termination_signal,
    deadline_at: record.deadline_at,
    group_quiescent: record.group_quiescent,
  };
}

export function summary(record: ProcessRecord): ProcessSummary {
  return {
    ...snapshot(record),
    created_at: record.created_at,
    started_at: record.started_at,
    exited_at: record.exited_at,
    cwd: record.cwd,
    origin_turn_id: record.origin_turn_id,
    stop_reason: record.stop_reason,
  };
}

export function retainsEnvironment(record: ProcessRecord): boolean {
  return record.group_quiescent !== true || !record.output_closed;
}

import { randomUUID } from 'node:crypto';
import { stat } from 'node:fs/promises';
import type { CallToolResult } from '@modelcontextprotocol/server';
import type { Logger } from '../logging.js';
import { CursorCodec, processId, requestDigest } from './identity.js';
import { OutputDecoder, ProcessOutput } from './output.js';
import { ProcessFailure, processError, processResult, resultObject } from './result.js';
import { ProcessStore, StoreFailure } from './store.js';
import {
  ActivityControl,
  ActivityConflict,
  type ActivityRequest,
  type ActivityResult,
} from './activity.js';
import {
  spawnPipe,
  spawnPty,
  SpawnFailure,
  type ProcessEvents,
  type ProcessHandle,
} from './driver.js';
import {
  OUTPUT_BYTES,
  OUTPUT_RETENTION_MS,
  PROCESS_LIMIT,
  TERMINATION_GRACE_MS,
  retainsEnvironment,
  snapshot,
  summary,
  type BashTool,
  type OperationRecord,
  type PollInput,
  type ProcessConfig,
  type ProcessContext,
  type ProcessRecord,
  type StartInput,
  type StopReason,
  type Stream,
  type WriteInput,
} from './model.js';

interface LiveProcess {
  record: ProcessRecord;
  handle?: ProcessHandle;
  decoders: Map<Stream, OutputDecoder>;
  ended: Set<Stream>;
  listeners: Set<() => void>;
  checking?: Promise<void>;
}

function abortable<T>(promise: Promise<T>, signal: AbortSignal): Promise<T> {
  if (signal.aborted) return Promise.reject(new ProcessFailure('cancelled'));
  return new Promise((resolve, reject) => {
    const abort = () => reject(new ProcessFailure('cancelled'));
    signal.addEventListener('abort', abort, { once: true });
    promise
      .then(resolve, reject)
      .finally(() => signal.removeEventListener('abort', abort))
      .catch(reject);
  });
}

export class ProcessManager {
  private readonly store: ProcessStore;
  private readonly codec: CursorCodec;
  private readonly output: ProcessOutput;
  private readonly boot = randomUUID();
  private readonly live = new Map<string, LiveProcess>();
  private readonly inflight = new Map<string, Promise<CallToolResult>>();
  private readonly writes = new Map<string, Promise<unknown>>();
  private readonly timer: NodeJS.Timeout;
  private closing = false;
  private failed = false;
  private lastSweep = 0;
  private readonly activityControl?: ActivityControl;

  constructor(
    readonly config: ProcessConfig,
    private readonly log: Logger,
    private readonly fatal: () => void = () => {},
  ) {
    this.store = new ProcessStore(config);
    try {
      if (config.activity_source) {
        const source = Object.freeze({ ...config.activity_source });
        this.store.claimActivitySource(
          JSON.stringify(Object.entries(source).sort(([a], [b]) => a.localeCompare(b))),
        );
        this.activityControl = new ActivityControl(source);
      }
      this.codec = new CursorCodec(
        this.store.key,
        JSON.stringify([config.workspace_id, config.session_id, config.environment_instance_id]),
      );
      this.output = new ProcessOutput(this.store, this.codec);
      this.recover();
    } catch (error) {
      this.store.close();
      throw error;
    }
    this.timer = setInterval(() => this.tick(), 250);
    this.timer.unref();
  }

  get managedActivity(): boolean {
    return this.activityControl !== undefined;
  }

  get hasPendingCalls(): boolean {
    return this.inflight.size > 0 || this.writes.size > 0;
  }

  get effectsBlocked(): boolean {
    return this.closing || this.failed || (this.activityControl?.blocked ?? false);
  }

  beginFileCall(): void {
    if (this.effectsBlocked) throw new ActivityConflict();
    try {
      this.store.transaction(() => this.store.beginFileCall());
    } catch (error) {
      this.storageFailed();
      throw error;
    }
  }

  finishFileCall(uncertain: boolean): void {
    this.background(() => this.store.transaction(() => this.store.finishFileCall(uncertain)));
  }

  async activity(request: ActivityRequest): Promise<ActivityResult> {
    const control = this.activityControl;
    if (!control) throw new ActivityConflict();
    control.synchronize(request);
    const identity = {
      source: { ...control.source },
      stop_gate: request.stop_gate ? { ...request.stop_gate } : null,
      local_revision: request.local_revision,
    };
    try {
      if (this.closing || this.failed) throw new StoreFailure();
      await Promise.all([...this.live.values()].map((live) => this.check(live)));
      if (!control.matches(request)) throw new ActivityConflict();
      if (this.closing || this.failed) throw new StoreFailure();
      const counts = this.store.activity();
      return {
        ...identity,
        ...counts,
        pending_writes: counts.pending_writes + this.writes.size,
        verified: true,
      };
    } catch (error) {
      if (error instanceof ActivityConflict) throw error;
      this.storageFailed();
      return {
        ...identity,
        revision: 0,
        verified: false,
        active: 0,
        unknown: 0,
        pending_writes: 0,
      };
    }
  }

  private recover(): void {
    this.store.transaction(() => {
      this.store.recoverFileCalls();
      for (const record of this.store.unresolved()) {
        if (retainsEnvironment(record)) {
          record.state = 'lost';
          record.group_quiescent = null;
          record.output_closed = false;
          this.store.putProcess(record);
        }
      }
      for (const operation of this.store.operations()) {
        if (operation.phase !== 'complete')
          this.store.putOperation({
            ...operation,
            phase: 'complete',
            result: this.failure(operation, 'execution_indeterminate'),
          });
      }
    });
  }

  async call(
    tool: BashTool,
    input: Record<string, unknown>,
    context: ProcessContext,
    signal: AbortSignal,
  ): Promise<CallToolResult> {
    try {
      return await this.accept(tool, input, context, signal);
    } catch (error) {
      if (!(error instanceof StoreFailure)) throw error;
      this.storageFailed();
      return processError('execution_indeterminate');
    }
  }

  private async accept(
    tool: BashTool,
    input: Record<string, unknown>,
    context: ProcessContext,
    signal: AbortSignal,
  ): Promise<CallToolResult> {
    if (
      context.workspace_id !== this.config.workspace_id ||
      context.session_id !== this.config.session_id
    )
      return processError('process_not_found');
    if (this.closing || this.failed) return processError('execution_indeterminate');
    const digest = requestDigest(tool, input);
    const previous = this.store.operation(context.operation_id);
    if (previous) {
      if (
        previous.tool !== tool ||
        previous.digest !== digest ||
        previous.origin.origin_turn_id !== context.origin_turn_id
      )
        return processError('operation_conflict');
      if (previous.result) return this.replay(previous);
      return (
        this.inflight.get(context.operation_id) ?? this.failure(previous, 'execution_indeterminate')
      );
    }
    if (this.effectsBlocked) return processError('capacity_unavailable');
    const operation: OperationRecord = {
      operation_id: context.operation_id,
      tool,
      digest,
      origin: context,
      process_id: null,
      phase: 'prepared',
      result: null,
    };
    this.store.transaction(() => this.store.putOperation(operation));
    const work = this.execute(operation, input, signal);
    this.inflight.set(context.operation_id, work);
    try {
      return await work;
    } finally {
      this.inflight.delete(context.operation_id);
    }
  }

  private replay(operation: OperationRecord): CallToolResult {
    const result = operation.result!;
    if (!operation.output_reference || !operation.process_id) return result;
    try {
      const record = this.store.process(operation.process_id);
      if (!record) throw new ProcessFailure('output_expired');
      const { from, to } = operation.output_reference;
      const page = this.output.page(record, from, 65536, to);
      if (page.next_cursor !== to) throw new ProcessFailure('output_cursor_expired');
      return { ...result, structuredContent: { ...resultObject(result), chunks: page.chunks } };
    } catch (error) {
      if (!(error instanceof ProcessFailure)) throw error;
      return this.failure(operation, error.code, error.detail);
    }
  }

  private async execute(
    operation: OperationRecord,
    input: Record<string, unknown>,
    signal: AbortSignal,
  ): Promise<CallToolResult> {
    let result: CallToolResult;
    try {
      if (signal.aborted) throw new ProcessFailure('cancelled');
      result = await this.route(operation, input, signal);
    } catch (error) {
      if (error instanceof StoreFailure) {
        this.storageFailed();
        return this.storageFailureResult(operation);
      }
      result =
        error instanceof ProcessFailure
          ? this.failure(operation, error.code, error.detail)
          : this.failure(operation, 'execution_indeterminate');
    }
    try {
      this.store.finish(operation, result);
    } catch {
      this.storageFailed();
      return this.storageFailureResult(operation);
    }
    return result;
  }

  private storageFailureResult(operation: OperationRecord): CallToolResult {
    // Do not read a poisoned store. Preserve a known handle from memory so
    // the caller can reconcile it, without claiming that a receipt committed.
    const record = operation.process_id ? this.live.get(operation.process_id)?.record : undefined;
    if (operation.tool === 'bash_write' && record)
      return processError('execution_indeterminate', {
        process_id: record.process_id,
        accepted_bytes: null,
        stdin_closed: record.stdin_closed,
        input_state: 'indeterminate',
      });
    return processError('execution_indeterminate', record ? { ...snapshot(record) } : {});
  }

  private failure(
    operation: OperationRecord,
    code: string,
    detail: Record<string, unknown> = {},
  ): CallToolResult {
    const record = operation.process_id ? this.store.process(operation.process_id) : undefined;
    if (operation.tool === 'bash_write') {
      if (code === 'execution_indeterminate' && record)
        return processError(code, {
          process_id: record.process_id,
          accepted_bytes: null,
          stdin_closed: record.stdin_closed,
          input_state: 'indeterminate',
        });
      return processError(code);
    }
    return processError(code, record ? { ...snapshot(record), ...detail } : {});
  }

  private route(
    operation: OperationRecord,
    input: Record<string, unknown>,
    signal: AbortSignal,
  ): Promise<CallToolResult> {
    switch (operation.tool) {
      case 'bash_start':
        return this.start(operation, input as unknown as StartInput, signal);
      case 'bash_poll':
        return this.poll(operation, input as unknown as PollInput, signal);
      case 'bash_write':
        return this.write(operation, input as unknown as WriteInput, signal);
      case 'bash_terminate':
        return this.terminate(
          operation,
          input as { process_id: string; mode: 'graceful' | 'force' },
        );
      case 'bash_list':
        return Promise.resolve(this.list(input as { cursor?: string; limit: number }));
    }
  }

  private effect(operation: OperationRecord, id: string): void {
    operation.process_id = id;
    operation.phase = 'effect_pending';
    this.store.transaction(() => this.store.putOperation(operation));
  }

  private newRecord(operation: OperationRecord, input: StartInput): ProcessRecord {
    return {
      process_id: processId(),
      state: 'starting',
      exit_code: null,
      termination_signal: null,
      deadline_at: null,
      group_quiescent: null,
      created_at: new Date().toISOString(),
      started_at: null,
      exited_at: null,
      cwd: input.cwd,
      origin_turn_id: operation.origin.origin_turn_id,
      stop_reason: null,
      owner_boot: this.boot,
      executor_boot_id: this.config.executor_boot_id,
      origin: operation.origin,
      pid: null,
      process_identity: null,
      tty: input.tty,
      stdin_closed: !input.stdin_open,
      output_closed: false,
      output_expired: false,
      output_generation: randomUUID(),
      first_seq: 0,
      next_seq: 0,
      retained_bytes: 0,
      termination_mode: null,
      kill_at: null,
    };
  }

  private async start(
    operation: OperationRecord,
    input: StartInput,
    signal: AbortSignal,
  ): Promise<CallToolResult> {
    if (input.max_runtime_ms > this.config.max_runtime_ms)
      throw new ProcessFailure('runtime_limit_exceeded');
    try {
      if (!(await stat(input.cwd)).isDirectory()) throw new Error();
    } catch {
      throw new ProcessFailure('invalid_cwd');
    }
    if (signal.aborted) throw new ProcessFailure('cancelled');
    if (this.effectsBlocked) throw new ProcessFailure('capacity_unavailable');
    if (this.store.unresolved().length >= PROCESS_LIMIT)
      throw new ProcessFailure('process_limit_exceeded');
    const record = this.newRecord(operation, input);
    operation.process_id = record.process_id;
    operation.phase = 'effect_pending';
    this.store.transaction(() => {
      this.store.putProcess(record);
      this.store.putOperation(operation);
    });
    const live: LiveProcess = {
      record,
      decoders: new Map(),
      ended: new Set(),
      listeners: new Set(),
    };
    this.live.set(record.process_id, live);
    try {
      const spawn = input.tty ? spawnPty : spawnPipe;
      live.handle = await spawn(
        input,
        this.config,
        this.events(live, input.max_runtime_ms),
        signal,
        () => !this.effectsBlocked,
      );
    } catch (error) {
      if (error instanceof SpawnFailure) {
        this.store.transaction(() => {
          this.store.removeUnstarted(record.process_id);
          operation.process_id = null;
          this.store.putOperation(operation);
        });
        this.live.delete(record.process_id);
        throw new ProcessFailure(error.code);
      }
      record.state = 'lost';
      this.save(live);
      throw new ProcessFailure('execution_indeterminate');
    }
    record.pid = live.handle.pid;
    if (record.state === 'starting') record.state = 'running';
    record.group_quiescent = await live.handle.group.inspect();
    this.save(live);
    this.log('process.started', 'Managed process started', {
      ...operation.origin,
      process_id: record.process_id,
    });
    await this.wait(
      live,
      input.yield_time_ms,
      signal,
      () => record.state === 'exited' || record.state === 'lost',
    );
    return this.observation(operation, record, undefined, input.max_output_bytes);
  }

  private events(live: LiveProcess, runtimeMs: number): ProcessEvents {
    const background = (action: () => void) => {
      if (this.live.get(live.record.process_id) === live) this.background(action);
    };
    return {
      attached: (pid, identity) => {
        live.record.pid = pid;
        live.record.process_identity = identity;
        this.save(live);
      },
      lost: () =>
        background(() => {
          live.record.state = 'lost';
          live.record.group_quiescent = null;
          this.save(live);
        }),
      spawned: (pid, identity) => {
        live.record.pid = pid;
        live.record.process_identity = identity;
        live.record.started_at = new Date().toISOString();
        live.record.deadline_at = new Date(Date.now() + runtimeMs).toISOString();
        if (live.record.state === 'starting') live.record.state = 'running';
        this.save(live);
      },
      data: (stream, bytes) =>
        background(() => {
          const decoder = live.decoders.get(stream) ?? new OutputDecoder();
          live.decoders.set(stream, decoder);
          for (let offset = 0; offset < bytes.length; offset += 32768) {
            const raw = bytes.subarray(offset, offset + 32768);
            const decoded = decoder.write(raw);
            this.store.append(
              live.record,
              stream,
              raw,
              decoded.text,
              decoded.replaced,
              OUTPUT_BYTES,
            );
          }
          this.notify(live);
        }),
      streamClosed: (stream) =>
        background(() => {
          const decoded = live.decoders.get(stream)?.write(Buffer.alloc(0), true);
          if (decoded?.text)
            this.store.append(
              live.record,
              stream,
              Buffer.alloc(0),
              decoded.text,
              decoded.replaced,
              OUTPUT_BYTES,
            );
          live.ended.add(stream);
        }),
      exit: (code, signal) =>
        background(() => {
          live.record.state = 'exited';
          live.record.exit_code = code;
          live.record.termination_signal = signal;
          live.record.exited_at = new Date().toISOString();
          live.record.stdin_closed = true;
          this.save(live);
          this.log('process.exited', 'Managed process exited', {
            ...live.record.origin,
            process_id: live.record.process_id,
          });
        }),
      deadline: () =>
        background(() => {
          const record = live.record;
          record.stop_reason ??= 'deadline_reached';
          record.termination_mode ??= 'graceful';
          record.kill_at ??= Date.now() + TERMINATION_GRACE_MS;
          if (record.state !== 'exited' && record.state !== 'lost') record.state = 'terminating';
          this.save(live);
        }),
      closed: () =>
        background(() => {
          live.record.output_closed = live.record.tty
            ? live.ended.has('pty')
            : live.ended.has('stdout') && live.ended.has('stderr');
          this.save(live);
        }),
    };
  }

  private record(operation: OperationRecord, id: string): ProcessRecord {
    const record = this.live.get(id)?.record ?? this.store.process(id);
    if (!record) throw new ProcessFailure('process_not_found');
    operation.process_id = id;
    return record;
  }

  private observation(
    operation: OperationRecord,
    record: ProcessRecord,
    cursor: string | undefined,
    budget: number,
  ): CallToolResult {
    const value = { ...snapshot(record), ...this.output.page(record, cursor, budget) };
    operation.output_reference = {
      from: cursor ?? value.first_available_cursor,
      to: value.next_cursor,
    };
    if (record.state === 'lost') return processError('execution_indeterminate', value);
    if (record.state === 'exited' && (record.exit_code !== 0 || record.termination_signal !== null))
      return processError('command_failed', value);
    return processResult(value);
  }

  private async poll(
    operation: OperationRecord,
    input: PollInput,
    signal: AbortSignal,
  ): Promise<CallToolResult> {
    const record = this.record(operation, input.process_id);
    const initial = this.output.page(record, input.cursor, input.max_output_bytes);
    const live = this.live.get(record.process_id);
    if (!initial.chunks.length && record.state !== 'exited' && record.state !== 'lost' && live) {
      const seq = record.next_seq;
      const state = record.state;
      await this.wait(
        live,
        input.yield_time_ms,
        signal,
        () => record.next_seq !== seq || record.state !== state,
      );
    }
    return this.observation(operation, record, input.cursor, input.max_output_bytes);
  }

  private async write(
    operation: OperationRecord,
    input: WriteInput,
    signal: AbortSignal,
  ): Promise<CallToolResult> {
    const record = this.record(operation, input.process_id);
    if (record.tty && input.close_stdin) throw new ProcessFailure('unsupported_operation');
    const previous = this.writes.get(record.process_id) ?? Promise.resolve();
    this.store.transaction(() => this.store.touchActivity());
    const work = previous.then(async () => {
      if (signal.aborted) throw new ProcessFailure('cancelled');
      if (this.effectsBlocked) throw new ProcessFailure('capacity_unavailable');
      const live = this.live.get(record.process_id);
      if (record.state === 'lost' || !live?.handle)
        throw new ProcessFailure('execution_indeterminate');
      if (record.stdin_closed && !input.text && input.close_stdin && !record.tty)
        return { accepted_bytes: 0, stdin_closed: true, input_state: 'accepted' as const };
      if (record.state === 'exited') throw new ProcessFailure('process_not_running');
      if (record.stdin_closed) throw new ProcessFailure('stdin_closed');
      this.effect(operation, record.process_id);
      const receipt = await live.handle.write(input.text, input.close_stdin);
      if (!this.closing && !this.failed) {
        record.stdin_closed ||= receipt.stdin_closed;
        this.save(live);
      }
      return receipt;
    });
    // Cancellation stops the waiting request, not this serialization barrier.
    // An in-flight OS write may still settle after a tool result became unknown.
    const barrier = work.then(
      () => {},
      () => {},
    );
    this.writes.set(record.process_id, barrier);
    barrier
      .finally(() => {
        if (this.writes.get(record.process_id) === barrier) {
          this.background(() => this.store.transaction(() => this.store.touchActivity()));
          this.writes.delete(record.process_id);
        }
      })
      .catch(() => {});
    try {
      const receipt = await abortable(work, signal);
      return receipt.input_state === 'accepted'
        ? processResult({ process_id: record.process_id, ...receipt })
        : processError('input_write_failed', { process_id: record.process_id, ...receipt });
    } catch (error) {
      if (signal.aborted && operation.phase === 'effect_pending')
        return processError('execution_indeterminate', {
          process_id: record.process_id,
          accepted_bytes: null,
          stdin_closed: record.stdin_closed,
          input_state: 'indeterminate',
        });
      throw error;
    }
  }

  private async terminate(
    operation: OperationRecord,
    input: { process_id: string; mode: 'graceful' | 'force' },
  ): Promise<CallToolResult> {
    const record = this.record(operation, input.process_id);
    if (input.mode === 'force' && !this.config.allow_force)
      throw new ProcessFailure('permission_denied');
    if (record.group_quiescent === true)
      return processResult({
        ...snapshot(record),
        termination_requested: false,
        termination_mode: record.termination_mode ?? input.mode,
      });
    this.effect(operation, record.process_id);
    await this.stop(record, input.mode, 'user_requested');
    return processResult({
      ...snapshot(record),
      termination_requested: true,
      termination_mode: record.termination_mode,
    });
  }

  private async stop(
    record: ProcessRecord,
    mode: 'graceful' | 'force',
    reason: StopReason,
  ): Promise<void> {
    const live = this.live.get(record.process_id);
    if (!live?.handle || record.state === 'lost')
      throw new ProcessFailure('execution_indeterminate');
    if (!record.stop_reason) record.stop_reason = reason;
    if (!record.termination_mode || mode === 'force') record.termination_mode = mode;
    if (record.kill_at === null) record.kill_at = Date.now() + TERMINATION_GRACE_MS;
    if (record.state !== 'exited') record.state = 'terminating';
    this.save(live);
    const sent = await live.handle.group.signal(
      record.termination_mode === 'force' ? 'SIGKILL' : 'SIGTERM',
    );
    if (!sent) throw new ProcessFailure('execution_indeterminate');
  }

  private list(input: { cursor?: string; limit: number }): CallToolResult {
    let boundary: { created_at: string; process_id: string } | undefined;
    if (input.cursor !== undefined) {
      let decoded: unknown;
      try {
        decoded = this.codec.decode(input.cursor);
      } catch {
        throw new ProcessFailure('invalid_cursor');
      }
      if (
        !decoded ||
        typeof decoded !== 'object' ||
        !('kind' in decoded) ||
        decoded.kind !== 'list' ||
        !('created_at' in decoded) ||
        typeof decoded.created_at !== 'string' ||
        !('process_id' in decoded) ||
        typeof decoded.process_id !== 'string'
      )
        throw new ProcessFailure('invalid_cursor');
      boundary = { created_at: decoded.created_at, process_id: decoded.process_id };
    }
    const records = this.store.list(input.limit + 1, boundary);
    const items = records.slice(0, input.limit).map(summary);
    const last = items.at(-1);
    const cursor =
      records.length > input.limit && last
        ? this.codec.encode({
            kind: 'list',
            created_at: last.created_at,
            process_id: last.process_id,
          })
        : null;
    return processResult({ items, next_cursor: cursor, has_more: cursor !== null });
  }

  private wait(
    live: LiveProcess,
    ms: number,
    signal: AbortSignal,
    ready: () => boolean,
  ): Promise<void> {
    if (signal.aborted) return Promise.reject(new ProcessFailure('cancelled'));
    if (ms === 0 || ready()) return Promise.resolve();
    return new Promise((resolve, reject) => {
      const cleanup = () => {
        clearTimeout(timer);
        live.listeners.delete(changed);
        signal.removeEventListener('abort', abort);
      };
      const finish = () => {
        cleanup();
        resolve();
      };
      const changed = () => {
        if (ready()) finish();
      };
      const abort = () => {
        cleanup();
        reject(new ProcessFailure('cancelled'));
      };
      const timer = setTimeout(finish, ms);
      live.listeners.add(changed);
      signal.addEventListener('abort', abort, { once: true });
    });
  }

  private notify(live: LiveProcess): void {
    for (const listener of live.listeners) listener();
  }
  private save(live: LiveProcess): void {
    this.store.transaction(() => this.store.putProcess(live.record));
    this.notify(live);
  }

  private background(action: () => void): void {
    if (this.closing || this.failed) return;
    try {
      action();
    } catch {
      this.storageFailed();
    }
  }

  private storageFailed(): void {
    if (this.failed) return;
    this.failed = true;
    this.log(
      'process.storage_failed',
      'Process persistence failed',
      { error_code: 'execution_indeterminate' },
      'ERROR',
    );
    this.fatal();
  }

  private tick(): void {
    if (this.closing || this.failed) return;
    if (Date.now() - this.lastSweep >= 60_000) {
      this.lastSweep = Date.now();
      this.background(() => {
        for (const record of this.store.expirable(
          new Date(Date.now() - OUTPUT_RETENTION_MS).toISOString(),
        ))
          this.store.expire(record);
      });
    }
    for (const live of this.live.values()) {
      if (live.checking || !live.handle) continue;
      this.check(live).catch(() => this.storageFailed());
    }
  }

  private check(live: LiveProcess): Promise<void> {
    if (!live.handle) return Promise.resolve();
    live.checking ??= this.inspect(live).finally(() => {
      live.checking = undefined;
    });
    return live.checking;
  }

  private async inspect(live: LiveProcess): Promise<void> {
    const record = live.record;
    const quiet = await live.handle!.group.inspect();
    if (this.closing || this.failed) return;
    if (record.group_quiescent !== quiet) {
      record.group_quiescent = quiet;
      this.save(live);
    }
    if (
      quiet !== true &&
      record.deadline_at &&
      Date.now() >= Date.parse(record.deadline_at) &&
      !record.termination_mode
    ) {
      try {
        await this.stop(record, 'graceful', 'deadline_reached');
      } catch (error) {
        if (!(error instanceof ProcessFailure)) throw error;
      }
    }
    if (
      quiet !== true &&
      record.kill_at !== null &&
      Date.now() >= record.kill_at &&
      record.termination_mode !== 'force'
    ) {
      try {
        await this.stop(record, 'force', record.stop_reason ?? 'deadline_reached');
      } catch (error) {
        if (!(error instanceof ProcessFailure)) throw error;
      }
    }
    if (!retainsEnvironment(record)) {
      live.handle!.detach();
      this.live.delete(record.process_id);
    }
  }

  // The trusted launcher closes admission before invoking this method.
  async stopAll(): Promise<void> {
    for (const record of this.store.unresolved()) {
      if (record.group_quiescent !== true) {
        await this.stop(record, 'graceful', 'session_closed');
      }
    }
  }

  close(): void {
    if (this.closing) return;
    this.closing = true;
    clearInterval(this.timer);
    try {
      if (!this.failed)
        this.store.transaction(() => {
          for (const live of this.live.values())
            if (retainsEnvironment(live.record)) {
              live.record.state = 'lost';
              live.record.group_quiescent = null;
              live.record.output_closed = false;
              this.store.putProcess(live.record);
            }
        });
    } finally {
      for (const live of this.live.values()) live.handle?.detach();
      this.store.close();
    }
  }
}

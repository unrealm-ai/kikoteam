import { spawn, type ChildProcessByStdio } from 'node:child_process';
import { closeSync, writeSync } from 'node:fs';
import { ReadStream } from 'node:tty';
import { fileURLToPath } from 'node:url';
import type { Readable, Writable } from 'node:stream';
import type { ProcessConfig, StartInput, Stream } from './model.js';
import { ProcessGroup } from './group.js';
import { ptyFork, ptyIdentity } from './pty-native.js';
import { ProcessFailure } from './result.js';
import { control } from './control.js';

export interface InputReceipt {
  accepted_bytes: number | null;
  stdin_closed: boolean;
  input_state: 'accepted' | 'partial' | 'indeterminate';
}

export interface ProcessEvents {
  attached(pid: number, identity: string): void;
  spawned(pid: number, identity: string): void;
  data(stream: Stream, bytes: Buffer): void;
  streamClosed(stream: Stream): void;
  exit(code: number | null, signal: string | null): void;
  deadline(): void;
  closed(): void;
  lost(): void;
}

export interface ProcessHandle {
  pid: number;
  group: ProcessGroup;
  write(text: string, close: boolean): Promise<InputReceipt>;
  detach(): void;
}

export class SpawnFailure extends Error {
  constructor(readonly code = 'spawn_failed') {
    super(code);
  }
}

function commandArgs(input: StartInput, path: string): string[] {
  return [
    '--disable-warning=ExperimentalWarning',
    fileURLToPath(new URL('./supervisor-entry.js', import.meta.url)),
    path,
    input.command,
    String(input.max_runtime_ms),
  ];
}

async function activate(
  group: ProcessGroup,
  events: ProcessEvents,
  exited: () => boolean,
  command: number,
  started: Promise<void>,
  start: () => Promise<void>,
  signal: AbortSignal,
  canStart: () => boolean,
): Promise<void> {
  try {
    await group.establish(command, exited);
    events.attached(group.pid, group.identity);
    if (signal.aborted || !canStart()) throw new SpawnFailure('cancelled');
    await start();
    await started;
    events.spawned(group.pid, group.identity);
  } catch (error) {
    // Use only the owned supervisor connection; never a historical PID.
    await group.signal('SIGKILL');
    throw error instanceof SpawnFailure
      ? error
      : error instanceof ProcessFailure && error.code === 'spawn_failed'
        ? new SpawnFailure()
        : error;
  }
}

function pipeWrite(stream: Writable, text: string, close: boolean): Promise<InputReceipt> {
  return new Promise((resolve) => {
    let accepted = 0;
    const finish = (error?: Error | null) =>
      resolve({
        accepted_bytes: error ? accepted || null : accepted,
        stdin_closed: !error && close,
        input_state: error ? (accepted ? 'partial' : 'indeterminate') : 'accepted',
      });
    const end = () => {
      if (close) stream.end((error?: Error | null) => finish(error));
      else finish();
    };
    if (!text) {
      end();
      return;
    }
    stream.write(Buffer.from(text), (error) => {
      if (error) {
        finish(error);
        return;
      }
      accepted = Buffer.byteLength(text);
      end();
    });
  });
}

export async function spawnPipe(
  input: StartInput,
  config: ProcessConfig,
  events: ProcessEvents,
  signal: AbortSignal,
  canStart: () => boolean = () => true,
): Promise<ProcessHandle> {
  if (signal.aborted) throw new SpawnFailure('cancelled');
  const bridge = await control(config.runtime_directory ?? config.state_directory, events);
  if (signal.aborted) {
    bridge.close();
    throw new SpawnFailure('cancelled');
  }
  const child: ChildProcessByStdio<Writable, Readable, Readable> = spawn(
    process.execPath,
    commandArgs(input, bridge.path),
    {
      cwd: input.cwd,
      env: { ...config.environment },
      detached: true,
      stdio: ['pipe', 'pipe', 'pipe'],
    },
  );
  let spawned = false;
  let failed = false;
  let exited = false;
  child.stdin.on('error', () => {
    /* write callbacks carry the input receipt. */
  });
  for (const [name, stream] of [
    ['stdout', child.stdout],
    ['stderr', child.stderr],
  ] as const) {
    stream.on('data', (bytes: Buffer) => events.data(name, bytes));
    stream.once('end', () => events.streamClosed(name));
    stream.on('error', () => {
      /* close without a complete stream remains unknown. */
    });
  }
  child.once('exit', () => {
    exited = true;
    // Let the control socket drain its ordered exit message even when the
    // supervisor was killed with its own group. EOF without one is unknown.
  });
  child.once('close', () => {
    if (!failed) events.closed();
  });
  await new Promise<void>((resolve, reject) => {
    child.once('spawn', () => {
      spawned = true;
      resolve();
    });
    child.once('error', () => {
      failed = !spawned;
      bridge.close();
      reject(new SpawnFailure());
    });
  });
  if (!input.stdin_open) child.stdin.end();
  const pid = child.pid!;
  const group = new ProcessGroup(pid, bridge.signal);
  try {
    await activate(
      group,
      events,
      () => exited,
      await bridge.ready,
      bridge.started,
      bridge.start,
      signal,
      canStart,
    );
  } catch (error) {
    await bridge.signal('SIGKILL').catch(() => {});
    child.stdin.destroy();
    child.stdout.destroy();
    child.stderr.destroy();
    child.unref();
    bridge.close();
    throw error;
  }
  return {
    pid,
    group,
    write: (text, close) => pipeWrite(child.stdin, text, close),
    detach() {
      child.stdout.destroy();
      child.stderr.destroy();
      child.stdin.destroy();
      child.unref();
      bridge.close();
    },
  };
}

export async function spawnPty(
  input: StartInput,
  config: ProcessConfig,
  events: ProcessEvents,
  signal: AbortSignal,
  canStart: () => boolean = () => true,
): Promise<ProcessHandle> {
  const native = ptyFork();
  if (signal.aborted) throw new SpawnFailure('cancelled');
  const bridge = await control(config.runtime_directory ?? config.state_directory, events);
  if (signal.aborted) {
    bridge.close();
    throw new SpawnFailure('cancelled');
  }
  let exited = false;
  const exit = () => {
    exited = true;
  };
  const environment = { ...config.environment, TERM: 'xterm-256color' };
  let terminal: ReturnType<typeof ptyIdentity>;
  try {
    terminal = ptyIdentity(
      native.fork(
        process.execPath,
        commandArgs(input, bridge.path),
        Object.entries(environment).map(([key, value]) => `${key}=${value}`),
        input.cwd,
        120,
        30,
        -1,
        -1,
        false,
        native.helper,
        exit,
      ),
    );
  } catch (error) {
    bridge.close();
    throw error;
  }
  let stream: ReadStream;
  try {
    stream = new ReadStream(terminal.fd);
  } catch (error) {
    closeSync(terminal.fd);
    // Readiness owns the child identity; never signal a bare PID on this path.
    await bridge.ready.then(() => bridge.signal('SIGKILL')).catch(() => {});
    bridge.close();
    throw error;
  }
  stream.on('data', (bytes: Buffer) => events.data('pty', bytes));
  stream.once('end', () => events.streamClosed('pty'));
  stream.on('error', (error: NodeJS.ErrnoException) => {
    if (error.code === 'EIO') events.streamClosed('pty');
  });
  stream.once('close', () => events.closed());
  const group = new ProcessGroup(terminal.pid, bridge.signal);
  try {
    await activate(
      group,
      events,
      () => exited,
      await bridge.ready,
      bridge.started,
      bridge.start,
      signal,
      canStart,
    );
  } catch (error) {
    await bridge.signal('SIGKILL').catch(() => {});
    stream.destroy();
    bridge.close();
    throw error;
  }
  return {
    pid: terminal.pid,
    group,
    write(text, close) {
      if (close || exited || stream.destroyed)
        return Promise.resolve({ accepted_bytes: 0, stdin_closed: false, input_state: 'partial' });
      try {
        const bytes = Buffer.from(text);
        const written = writeSync(terminal.fd, bytes);
        return Promise.resolve({
          accepted_bytes: written,
          stdin_closed: false,
          input_state: written === bytes.length ? 'accepted' : 'partial',
        });
      } catch {
        return Promise.resolve({ accepted_bytes: 0, stdin_closed: false, input_state: 'partial' });
      }
    },
    detach() {
      stream.destroy();
      bridge.close();
    },
  };
}

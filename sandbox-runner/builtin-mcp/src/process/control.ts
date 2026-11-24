import { createServer, type Socket } from 'node:net';
import { join } from 'node:path';
import { randomBytes } from 'node:crypto';
import { unlink } from 'node:fs/promises';
import type { ProcessEvents } from './driver.js';
import { ProcessFailure, SpawnFailure } from './result.js';

function exitMessage(
  value: object,
): value is { type: 'exit'; code: number | null; signal: string | null } {
  return (
    'type' in value &&
    value.type === 'exit' &&
    'code' in value &&
    'signal' in value &&
    (value.code === null || (typeof value.code === 'number' && Number.isInteger(value.code))) &&
    (value.signal === null || typeof value.signal === 'string')
  );
}

// This is private local supervision, not a second MCP or Go protocol. The
// socket is separate from the MCP endpoint. Same-UID Bash is not isolated
// from management files; the permissions here only exclude other accounts.
export async function control(directory: string, events: ProcessEvents) {
  const path = join(directory, `c-${randomBytes(4).toString('hex')}`);
  let socket: Socket | undefined;
  let exited = false;
  let closing = false;
  let readyResolve: (pid: number) => void = () => {};
  let readyReject: (error: Error) => void = () => {};
  let startedResolve: () => void = () => {};
  let startedReject: (error: Error) => void = () => {};
  const started = new Promise<void>((resolve, reject) => {
    startedResolve = resolve;
    startedReject = reject;
  });
  started.catch(() => {});
  const ready = new Promise<number>((resolve, reject) => {
    readyResolve = resolve;
    readyReject = reject;
  });
  ready.catch(() => {});
  const timer = setTimeout(() => {
    readyReject(new Error('Supervisor readiness deadline exceeded.'));
    startedReject(new Error('Command launch deadline exceeded.'));
  }, 5000);
  const server = createServer((connection) => {
    if (socket) {
      connection.destroy();
      return;
    }
    socket = connection;
    let input = '';
    connection.on('data', (bytes: Buffer) => {
      input += bytes.toString();
      if (input.length > 4096) {
        connection.destroy();
        return;
      }
      while (input.includes('\n')) {
        const end = input.indexOf('\n');
        const line = input.slice(0, end);
        input = input.slice(end + 1);
        try {
          const value: unknown = JSON.parse(line);
          if (!value || typeof value !== 'object' || !('type' in value)) throw new Error();
          if (
            value.type === 'ready' &&
            'pid' in value &&
            typeof value.pid === 'number' &&
            Number.isSafeInteger(value.pid) &&
            value.pid > 0
          ) {
            readyResolve(value.pid);
          } else if (value.type === 'started') {
            clearTimeout(timer);
            startedResolve();
          } else if (value.type === 'launch_failed') {
            clearTimeout(timer);
            startedReject(new ProcessFailure('spawn_failed'));
          } else if (value.type === 'deadline') {
            events.deadline();
          } else if (exitMessage(value)) {
            exited = true;
            events.exit(value.code, value.signal);
          } else throw new Error();
        } catch {
          connection.destroy();
        }
      }
    });
    connection.on('error', () => {});
    connection.once('close', () => {
      clearTimeout(timer);
      readyReject(new Error('Supervisor disconnected.'));
      startedReject(new Error('Supervisor disconnected.'));
      if (!closing && !exited) events.lost();
    });
  });
  try {
    await new Promise<void>((resolve, reject) => {
      server.once('error', reject);
      server.listen(path, resolve);
    });
  } catch {
    clearTimeout(timer);
    server.close();
    throw new SpawnFailure('control_socket_unavailable');
  }
  server.on('error', () => {});
  const send = (command: string) =>
    new Promise<void>((resolve, reject) => {
      if (!socket || socket.destroyed) {
        reject(new Error('Supervisor unavailable.'));
        return;
      }
      socket.write(command + '\n', (error) => (error ? reject(error) : resolve()));
    });
  return {
    path,
    ready,
    started,
    start: () => send('start'),
    signal: (signal: NodeJS.Signals) =>
      new Promise<void>((resolve, reject) => {
        if (!socket || socket.destroyed) {
          reject(new Error('Supervisor unavailable.'));
          return;
        }
        const command =
          signal === 'SIGKILL' ? 'force' : signal === 'SIGTERM' ? 'terminate' : undefined;
        if (!command) {
          reject(new Error('Unsupported managed signal.'));
          return;
        }
        socket.write(command + '\n', (error) => (error ? reject(error) : resolve()));
      }),
    close() {
      if (closing) return;
      closing = true;
      clearTimeout(timer);
      readyReject(new Error('Supervisor closed.'));
      startedReject(new Error('Supervisor closed.'));
      if (!exited) events.lost();
      socket?.destroy();
      server.close();
      unlink(path).catch(() => {});
    },
  };
}

import { createServer, type IncomingMessage, type ServerResponse } from 'node:http';
import { chmod } from 'node:fs/promises';
import { join } from 'node:path';
import { randomUUID, randomBytes, timingSafeEqual } from 'node:crypto';
import { Readable } from 'node:stream';
import { pipeline } from 'node:stream/promises';
import { createMcpHandler } from '@modelcontextprotocol/server';
import { createBuiltinServer, callScope } from './server.js';
import type { ProcessManager } from './process/manager.js';
import type { ProcessConfig, ProcessContext } from './process/model.js';
import type { FileTool } from './files.js';
import type { FileWorker } from './file-worker.js';
import type { StateGuard } from './state-guard.js';
import type { Logger } from './logging.js';

interface Window {
  control_revision: number;
  assignment_id: string;
  generation: number;
  harness_instance_id: string;
  turn_id: string;
  open: boolean;
  access_token: string;
}

const UUID = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;
const WINDOW_KEYS = [
  'control_revision',
  'assignment_id',
  'generation',
  'harness_instance_id',
  'turn_id',
  'open',
  'access_token',
];

function parseWindow(value: unknown): Window {
  if (!value || typeof value !== 'object' || Array.isArray(value))
    throw new Error('invalid_window');
  const input = value as Record<string, unknown>;
  if (
    Object.keys(input).length !== WINDOW_KEYS.length ||
    WINDOW_KEYS.some((key) => !(key in input))
  )
    throw new Error('invalid_window');
  for (const key of ['assignment_id', 'harness_instance_id', 'turn_id']) {
    if (typeof input[key] !== 'string' || !UUID.test(input[key])) throw new Error('invalid_window');
  }
  for (const key of ['control_revision', 'generation']) {
    if (!Number.isSafeInteger(input[key]) || Number(input[key]) < 1)
      throw new Error('invalid_window');
  }
  if (
    typeof input.open !== 'boolean' ||
    typeof input.access_token !== 'string' ||
    !/^[A-Za-z0-9_-]{32,256}$/.test(input.access_token)
  )
    throw new Error('invalid_window');
  // All properties have been validated at this boundary.
  return input as unknown as Window;
}

async function body(request: IncomingMessage, limit: number): Promise<Buffer> {
  const chunks: Buffer[] = [];
  let size = 0;
  for await (const chunk of request) {
    const bytes = Buffer.isBuffer(chunk) ? chunk : Buffer.from(chunk as string);
    size += bytes.length;
    if (size > limit) throw new Error('body_too_large');
    chunks.push(bytes);
  }
  return Buffer.concat(chunks);
}

function json(response: ServerResponse, status: number, value: object) {
  response.writeHead(status, { 'content-type': 'application/json', 'cache-control': 'no-store' });
  response.end(JSON.stringify(value));
}

function authorized(header: string | undefined, token: string): boolean {
  const actual = Buffer.from(header ?? '');
  const expected = Buffer.from('Bearer ' + token);
  return actual.length === expected.length && timingSafeEqual(actual, expected);
}

async function* responseBytes(stream: ReadableStream<Uint8Array>) {
  const reader = stream.getReader();
  try {
    while (true) {
      const next = await reader.read();
      if (next.done) return;
      yield next.value;
    }
  } finally {
    await reader.cancel();
    reader.releaseLock();
  }
}

export async function startHttp(options: {
  config: ProcessConfig;
  processes: ProcessManager;
  tools: FileTool[];
  guard: StateGuard;
  worker: FileWorker;
  log: Logger;
  host: string;
  port: number;
  allowedHosts: string[];
  discoveryToken: string;
}) {
  const { config, processes, tools, guard, worker, log } = options;
  const source = config.activity_source;
  if (!source) throw new Error('Managed activity source is required.');
  const scope = callScope();
  let window: Readonly<Window> | undefined;
  let closing = false;
  let managing = false;
  let admissions = 0;
  const socket = join(config.runtime_directory, 'm.sock');
  const activity = () =>
    processes.activity({
      source,
      local_revision: window?.control_revision ?? 1,
      stop_gate: window?.open
        ? null
        : { action_id: config.executor_boot_id, revision: window?.control_revision ?? 1 },
    });

  // Start closed. Discovery does not grant tool execution.
  await activity();

  function context(captured: Readonly<Window> | undefined): ProcessContext {
    if (closing || !captured?.open || window !== captured)
      throw new Error('execution_window_closed');
    return {
      workspace_id: config.workspace_id,
      session_id: config.session_id,
      runtime_id: captured.harness_instance_id,
      origin_turn_id: captured.turn_id,
      operation_id: randomUUID(),
      trace_id: randomBytes(16).toString('hex'),
    };
  }

  async function mcp(request: IncomingMessage, response: ServerResponse) {
    if (request.url !== '/mcp') return json(response, 404, { error: 'not_found' });
    const host = new URL('http://' + (request.headers.host ?? '')).hostname;
    if (request.headers.origin || !options.allowedHosts.includes(host))
      return json(response, 403, { error: 'origin_or_host_denied' });
    const captured = window;
    const token = captured?.access_token ?? options.discoveryToken;
    if (!authorized(request.headers.authorization, token))
      return json(response, 401, { error: 'unauthorized' });
    if (closing || admissions >= 12) return json(response, 503, { error: 'capacity_unavailable' });
    admissions++;
    const abort = new AbortController();
    response.once('close', () => {
      if (!response.writableEnded) abort.abort();
    });
    const handler = createMcpHandler(
      () =>
        createBuiltinServer(config.cwd, log, tools, processes, guard, worker, {
          context: () => context(captured),
          exposeActivity: false,
          scope,
          legacyHttp: true,
        }).server,
      // The SDK provides the 2025 HTTP handshake used by unmodified Pi 1.5.2.
      // Both dialects execute the same handlers and trusted Session window.
      { legacy: 'stateless', maxSubscriptions: 1, maxRequestBodySize: 20 * 1024 * 1024 },
    );
    try {
      const headers = new Headers();
      for (const [key, value] of Object.entries(request.headers)) {
        if (typeof value === 'string') headers.set(key, value);
      }
      const payload = request.method === 'POST' ? await body(request, 20 * 1024 * 1024) : undefined;
      const web = new Request('http://builtin/mcp', {
        method: request.method ?? 'GET',
        headers,
        signal: abort.signal,
        ...(payload ? { body: new Uint8Array(payload) } : {}),
      });
      const result = await handler.fetch(web);
      response.writeHead(result.status, Object.fromEntries(result.headers));
      if (result.body) await pipeline(Readable.from(responseBytes(result.body)), response);
      else response.end();
    } finally {
      await handler.close();
      admissions--;
    }
  }

  async function management(request: IncomingMessage, response: ServerResponse) {
    if (request.method === 'GET' && request.url === '/state') {
      const state = await activity();
      return json(response, 200, { ...state, control_revision: window?.control_revision ?? 0 });
    }
    if (request.method !== 'POST' || !['/execution-window', '/stop'].includes(request.url ?? ''))
      return json(response, 404, { error: 'not_found' });
    if (managing) return json(response, 409, { error: 'management_in_progress' });
    managing = true;
    try {
      const next = parseWindow(JSON.parse((await body(request, 8192)).toString('utf8')));
      if (request.url === '/stop' && next.open)
        return json(response, 400, { error: 'invalid_window' });
      const failure = await applyWindow(next);
      if (failure) return json(response, 409, { error: failure });
      let state = await activity();
      if (request.url === '/stop') {
        await processes.stopAll();
        state = await activity();
      }
      return json(response, quiescent(state) ? 200 : 202, state);
    } finally {
      managing = false;
    }
  }

  async function applyWindow(next: Window): Promise<string | undefined> {
    if (closing) return 'shutdown_in_progress';
    if ((!window && next.control_revision < 2) || conflicts(next)) return 'stale_control_revision';
    if (window && next.control_revision === window.control_revision) return undefined;
    if (window && next.access_token !== window.access_token) return 'fixed_access_token_required';
    if (next.open) {
      const current = await activity();
      if (
        !current.verified ||
        current.unknown ||
        admissions ||
        processes.hasPendingCalls ||
        scope.active.size ||
        scope.controls.size ||
        scope.terminations.size
      )
        return 'previous_execution_unresolved';
      // pending_writes includes open background output streams. They remain
      // owned by the Session across Turns; only a different runtime or a
      // stopped window requires all those streams and groups to be quiescent.
      if (!sameOwner(next) && !quiescent(current)) return 'previous_execution_unresolved';
    }
    // Preserve object identity on repeated synchronization, including during
    // admitted calls. Revisions replace execution windows; credentials stay fixed.
    window = Object.freeze(next);
    return undefined;
  }

  function sameOwner(next: Window): boolean {
    return (
      window?.open === true &&
      next.assignment_id === window.assignment_id &&
      next.generation === window.generation &&
      next.harness_instance_id === window.harness_instance_id
    );
  }

  function conflicts(next: Window): boolean {
    if (!window) return false;
    if (next.control_revision < window.control_revision) return true;
    return (
      next.control_revision === window.control_revision &&
      WINDOW_KEYS.some((key) => next[key as keyof Window] !== window?.[key as keyof Window])
    );
  }

  function quiescent(state: {
    active: number;
    unknown: number;
    pending_writes: number;
    verified: boolean;
  }) {
    return (
      state.verified && state.active === 0 && state.unknown === 0 && state.pending_writes === 0
    );
  }

  function listener(fn: typeof mcp) {
    return createServer((request, response) => {
      fn(request, response).catch((error: unknown) => {
        const status =
          error instanceof Error && error.message === 'body_too_large'
            ? 413
            : error instanceof SyntaxError ||
                error instanceof TypeError ||
                (error instanceof Error && error.message === 'invalid_window')
              ? 400
              : 503;
        log(
          'request.failed',
          'Builtin request failed',
          {
            error_code:
              error instanceof Error && error.message === 'body_too_large'
                ? 'body_too_large'
                : 'request_failed',
          },
          status === 503 ? 'ERROR' : 'WARN',
        );
        if (!response.headersSent)
          json(response, status, {
            error: 'request_failed',
          });
        else response.destroy();
      });
    });
  }
  const http = listener(mcp);
  const control = listener(management);
  http.requestTimeout = 30_000;
  control.requestTimeout = 10_000;
  try {
    await new Promise<void>((resolve, reject) => {
      control.once('error', reject);
      control.listen(socket, resolve);
    });
    await chmod(socket, 0o600);
    await new Promise<void>((resolve, reject) => {
      http.once('error', reject);
      http.listen(options.port, options.host, resolve);
    });
  } catch (error) {
    control.close();
    http.close();
    throw error;
  }
  const address = http.address();
  return {
    port: typeof address === 'object' && address ? address.port : options.port,
    socket,
    async close() {
      closing = true;
      http.close();
      // Close admission before any asynchronous cleanup. Preserve unknown
      // facts if shutdown cannot establish a quiescent process group.
      if (window)
        window = Object.freeze({
          ...window,
          open: false,
          control_revision: window.control_revision + 1,
        });
      await activity();
      await processes.stopAll();
      scope.shutdown.abort();
      await Promise.allSettled([...scope.active, ...scope.controls, ...scope.terminations]);
      const deadline = Date.now() + 8000;
      let state = await activity();
      while ((state.active || state.pending_writes) && !state.unknown && Date.now() < deadline) {
        await new Promise<void>((resolve) => setTimeout(resolve, 50));
        state = await activity();
      }
      control.close();
      http.closeAllConnections();
      control.closeAllConnections();
      await worker.close();
      processes.close();
      if (!state.verified || state.active || state.unknown || state.pending_writes)
        throw new Error('shutdown_unresolved');
    },
  };
}

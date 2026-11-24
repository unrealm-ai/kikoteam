import { execFile } from 'node:child_process';
import { mkdtemp, rm, stat } from 'node:fs/promises';
import { isAbsolute, join } from 'node:path';
import { tmpdir } from 'node:os';
import { promisify } from 'node:util';
import { serveStdio, StdioServerTransport } from '@modelcontextprotocol/server/stdio';
import { createLogger, logLevel } from './logging.js';
import { configuration, RuntimeDirectoryError } from './config.js';
import type { ProcessManager } from './process/manager.js';
import { StateGuard } from './state-guard.js';
import type { FileWorker } from './file-worker.js';

async function main(setPhase: (phase: string) => void): Promise<void> {
  const config = configuration(process.argv.slice(2), process.env);
  const { cwd } = config;
  if (!cwd || !isAbsolute(cwd) || !(await stat(cwd)).isDirectory())
    throw new Error('Invalid work directory.');
  if (process.platform !== 'linux' && process.platform !== 'darwin')
    throw new Error('Unsupported platform.');
  const log = createLogger(logLevel(process.env.BUILTIN_MCP_LOG_LEVEL));
  setPhase('private_directory');
  // Set before importing Pi: no user config/bin directory or on-demand download.
  const privateDir = await mkdtemp(join(tmpdir(), 'builtin-builtin-'));
  process.env.PI_CODING_AGENT_DIR = privateDir;
  process.env.PI_OFFLINE = '1';
  let processes: ProcessManager | undefined;
  let worker: FileWorker | undefined;
  try {
    for (const binary of ['rg', 'fd']) {
      setPhase(`dependency_${binary}`);
      await promisify(execFile)(binary, ['--version'], {
        timeout: 5000,
        maxBuffer: 4096,
      });
    }
    setPhase('server_initialization');
    let stop = () => {};
    const stopped = new Promise<void>((resolve) => {
      stop = resolve;
    });
    let guard: StateGuard | undefined;
    if (config.process) {
      setPhase('process_initialization');
      const { ProcessManager } = await import('./process/manager.js');
      processes = new ProcessManager(config.process, log, () => {
        process.exitCode = 1;
        stop();
      });
      guard = new StateGuard(config.process.state_directory, cwd);
      await guard.initialize();
    }
    const { createBuiltinServer } = await import('./server.js');
    const { createFileTools } = await import('./files.js');
    const tools = createFileTools(cwd, guard);
    if (config.process) {
      const { FileWorker } = await import('./file-worker.js');
      worker = new FileWorker(config.process, log);
      await worker.connect(tools);
    }
    const builtin = createBuiltinServer(cwd, log, tools, processes, guard, worker);
    const transport = new StdioServerTransport(process.stdin, process.stdout, {
      maxBufferSize: 20 * 1024 * 1024,
    });
    const handle = serveStdio(() => builtin.server, {
      legacy: 'reject',
      transport,
      maxSubscriptions: 1,
      onerror: () => {
        log('protocol.failed', 'MCP protocol failure', {}, 'WARN');
      },
    });
    const onclose = transport.onclose;
    transport.onclose = () => {
      onclose?.();
      stop();
    };
    process.once('SIGINT', stop);
    process.once('SIGTERM', stop);
    log('server.started', 'Builtin MCP server started');
    await stopped;
    setPhase('shutdown');
    // Covers transport close, underlying I/O, stdout backpressure and cleanup.
    // Keep it armed until process exit; unref permits immediate normal exit.
    setTimeout(() => {
      log('server.shutdown_timeout', 'Builtin shutdown deadline exceeded', {}, 'ERROR');
      process.exit(1);
    }, 5000).unref();
    process.off('SIGINT', stop);
    process.off('SIGTERM', stop);
    await handle.close();
    // The SDK pauses its readable on close; buffered unread bytes can still
    // keep fd 0 alive after an over-budget message. This process owns stdio.
    process.stdin.destroy();
    await builtin.drain();
    log('server.stopped', 'Builtin MCP server stopped');
  } finally {
    processes?.close();
    await worker?.close();
    await rm(privateDir, { recursive: true, force: true });
  }
}

let phase = 'configuration';
try {
  await main((current) => {
    phase = current;
  });
} catch (error) {
  createLogger()(
    'server.failed',
    'Builtin MCP startup or shutdown failed',
    {
      error_code: 'builtin_failure',
      reason: error instanceof RuntimeDirectoryError ? error.message : phase,
    },
    'ERROR',
  );
  process.exitCode = 1;
}

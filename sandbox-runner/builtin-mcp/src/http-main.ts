import { readFile, stat, mkdtemp, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { execFile } from 'node:child_process';
import { promisify } from 'node:util';
import { configuration, RuntimeDirectoryError } from './config.js';
import { createLogger } from './logging.js';

const log = createLogger();
let phase = 'configuration';
let privateDir: string | undefined;
try {
  if (process.argv.length !== 4 || process.argv[2] !== '--config')
    throw new Error('Expected --config file.');
  const path = process.argv[3]!;
  const info = await stat(path);
  if (
    !info.isFile() ||
    info.size > 65536 ||
    (info.mode & 0o077) !== 0 ||
    info.uid !== process.getuid?.()
  )
    throw new Error('Runtime configuration must be private and owned by the launcher.');
  const input: unknown = JSON.parse(await readFile(path, 'utf8'));
  if (!input || typeof input !== 'object' || Array.isArray(input))
    throw new Error('Invalid runtime configuration.');
  const fields = input as Record<string, unknown>;
  if (
    !Array.isArray(fields.launch_args) ||
    !fields.launch_args.every((arg: unknown) => typeof arg === 'string') ||
    typeof fields.host !== 'string' ||
    !Number.isSafeInteger(fields.port) ||
    Number(fields.port) < 0 ||
    Number(fields.port) > 65535 ||
    !Array.isArray(fields.allowed_hosts) ||
    !fields.allowed_hosts.length ||
    !fields.allowed_hosts.every((host: unknown) => typeof host === 'string') ||
    typeof fields.discovery_token !== 'string' ||
    !/^[A-Za-z0-9_-]{32,256}$/.test(fields.discovery_token)
  )
    throw new Error('Invalid runtime configuration.');
  const parsed = configuration(fields.launch_args, process.env);
  const config = parsed.process;
  if (!config?.activity_source)
    throw new Error('Full process and management configuration is required.');
  if (!(await stat(config.cwd)).isDirectory()) throw new Error('Work directory is unavailable.');
  privateDir = await mkdtemp(join(tmpdir(), 'builtin-pi-'));
  process.env.PI_CODING_AGENT_DIR = privateDir;
  process.env.PI_OFFLINE = '1';
  phase = 'dependencies';
  for (const binary of ['rg', 'fd'])
    await promisify(execFile)(binary, ['--version'], { timeout: 5000, maxBuffer: 4096 });
  const { ProcessManager } = await import('./process/manager.js');
  const { StateGuard } = await import('./state-guard.js');
  const { FileWorker } = await import('./file-worker.js');
  const { createFileTools } = await import('./files.js');
  const { startHttp } = await import('./http.js');
  phase = 'initialization';
  let stop = () => {};
  const stopped = new Promise<void>((resolve) => {
    stop = resolve;
  });
  const processes = new ProcessManager(config, log, () => {
    process.exitCode = 1;
    stop();
  });
  const guard = new StateGuard(config.state_directory, config.cwd);
  let worker: InstanceType<typeof FileWorker> | undefined;
  try {
    await guard.initialize();
    const tools = createFileTools(config.cwd, guard);
    worker = new FileWorker(config, log);
    await worker.connect(tools);
    const host = await startHttp({
      config,
      processes,
      guard,
      tools,
      worker,
      log,
      host: fields.host,
      port: Number(fields.port),
      allowedHosts: fields.allowed_hosts,
      discoveryToken: fields.discovery_token,
    });
    process.once('SIGTERM', stop);
    process.once('SIGINT', stop);
    log('server.started', 'Builtin HTTP MCP ready', { port: host.port });
    await stopped;
    phase = 'shutdown';
    const timer = setTimeout(() => process.exit(1), 12_000);
    timer.unref();
    process.off('SIGTERM', stop);
    process.off('SIGINT', stop);
    await host.close();
    log('server.stopped', 'Builtin HTTP MCP stopped');
  } finally {
    processes.close();
    await worker?.close();
  }
} catch (error) {
  log(
    'server.failed',
    'Builtin HTTP MCP failed',
    {
      error_code: 'builtin_failure',
      reason: error instanceof RuntimeDirectoryError ? error.message : phase,
    },
    'ERROR',
  );
  process.exitCode = 1;
} finally {
  if (privateDir) await rm(privateDir, { recursive: true, force: true });
}

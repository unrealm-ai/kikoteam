import { lstatSync } from 'node:fs';
import { isAbsolute, join } from 'node:path';
import type { ProcessConfig } from './process/model.js';
import type { ActivitySource } from './process/activity.js';

const FLAGS = new Set([
  '--cwd',
  '--workspace-id',
  '--session-id',
  '--sandbox-instance-id',
  '--runner-boot-id',
  '--state-dir',
  '--runtime-dir',
  '--connection-id',
  '--binding-id',
  '--config-revision',
]);
const RESOURCE_ID = /^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$/;

export class RuntimeDirectoryError extends Error {}

function runtimeDirectory(path: string | undefined): string {
  if (!path || !isAbsolute(path))
    throw new RuntimeDirectoryError('--runtime-dir requires an absolute path.');
  // The Bash socket has the longest name. Check bytes before starting either listener.
  if (Buffer.byteLength(join(path, 'c-00000000')) > 100)
    throw new RuntimeDirectoryError('--runtime-dir is too long for Unix sockets.');
  const dir = lstatSync(path, { throwIfNoEntry: false });
  if (!dir?.isDirectory() || dir.uid !== process.getuid?.() || (dir.mode & 0o077) !== 0)
    throw new RuntimeDirectoryError('--runtime-dir must be an existing private directory.');
  return path;
}

// Only launch configuration may choose the child environment. In particular,
// platform tokens, NODE_OPTIONS, BASH_ENV and injected loaders are not inherited.
function environment(source: NodeJS.ProcessEnv): Record<string, string> {
  const result: Record<string, string> = { PATH: source.PATH ?? '/usr/local/bin:/usr/bin:/bin' };
  for (const name of ['HOME', 'LANG', 'LC_ALL', 'TZ', 'TMPDIR']) {
    const value = source[name];
    if (value !== undefined) result[name] = value;
  }
  return result;
}

function parseFlags(args: string[]): Map<string, string> {
  const flags = new Map<string, string>();
  for (let index = 0; index < args.length; index += 2) {
    const name = args[index]!;
    const value = args[index + 1];
    if (!FLAGS.has(name) || flags.has(name) || !value || value.includes('\0'))
      throw new Error('Invalid launch configuration.');
    flags.set(name, value);
  }
  return flags;
}

export function configuration(args: string[], env: NodeJS.ProcessEnv) {
  const flags = parseFlags(args);
  const cwd = flags.get('--cwd') ?? '/workspace';
  if (!isAbsolute(cwd)) throw new Error('Invalid work directory.');
  if ([...flags.keys()].every((name) => name === '--cwd')) return { cwd };
  const required = (name: string): string => {
    const value = flags.get(name);
    if (!value) throw new Error('Incomplete process launch configuration.');
    return value;
  };
  const process: ProcessConfig = {
    workspace_id: required('--workspace-id'),
    session_id: required('--session-id'),
    environment_instance_id: required('--sandbox-instance-id'),
    executor_boot_id: required('--runner-boot-id'),
    state_directory: required('--state-dir'),
    runtime_directory: runtimeDirectory(flags.get('--runtime-dir')),
    cwd,
    max_runtime_ms: 86_400_000,
    allow_force: true,
    environment: environment(env),
  };
  if (!['linux', 'darwin'].includes(globalThis.process.platform))
    throw new Error('Production process mode requires macOS or Linux.');
  for (const id of [process.workspace_id, process.session_id, process.environment_instance_id]) {
    if (!RESOURCE_ID.test(id)) throw new Error('Invalid process launch identity.');
  }
  process.activity_source = activitySource(flags, process);
  if (
    !isAbsolute(process.state_directory) ||
    !/^[a-zA-Z0-9_-]{1,128}$/.test(process.executor_boot_id)
  )
    throw new Error('Invalid process launch configuration.');
  return { cwd, process };
}

function activitySource(
  flags: Map<string, string>,
  config: ProcessConfig,
): ActivitySource | undefined {
  if (!['--connection-id', '--binding-id', '--config-revision'].some((name) => flags.has(name)))
    return undefined;
  const connection = flags.get('--connection-id');
  const revision = flags.get('--config-revision');
  for (const id of [
    config.workspace_id,
    config.session_id,
    config.environment_instance_id,
    config.executor_boot_id,
  ]) {
    if (!RESOURCE_ID.test(id)) throw new Error('Invalid activity source.');
  }
  if (
    !connection ||
    !revision ||
    !/^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$/.test(
      connection,
    ) ||
    flags.get('--binding-id') !== 'builtin' ||
    !/^[1-9][0-9]*$/.test(revision) ||
    !Number.isSafeInteger(Number(revision))
  )
    throw new Error('Invalid activity source.');
  return {
    workspace_id: config.workspace_id,
    session_id: config.session_id,
    environment_instance_id: config.environment_instance_id,
    executor_boot_id: config.executor_boot_id,
    connection_id: connection,
    binding_id: 'builtin',
    config_revision: Number(revision),
  };
}

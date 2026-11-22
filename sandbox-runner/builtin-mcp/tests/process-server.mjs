// Real stdio component fixture. It intentionally runs under the test user's
// UID; production uses main.js and refuses this identity configuration.
import { serveStdio, StdioServerTransport } from '@modelcontextprotocol/server/stdio';
import { ProcessManager } from '../dist/process/manager.js';
import { createBuiltinServer } from '../dist/server.js';
import { createFileTools } from '../dist/files.js';
import { StateGuard } from '../dist/state-guard.js';
import { createLogger } from '../dist/logging.js';
import { join } from 'node:path';

const root = process.argv[2];
const config = {
  workspace_id: '3f716000-07b4-5252-9e2a-7da0bbdc5c8a',
  session_id: '921130c5-ba8b-5733-8fa6-66a9bbc776b3',
  environment_instance_id: 'ceb40783-4b1b-5ed9-9b10-7b3711def584',
  executor_boot_id: 'fixture',
  cwd: join(root, 'work'),
  state_directory: join(root, 'state'),
  max_runtime_ms: 86400000,
  allow_force: true,
  environment: { PATH: process.env.PATH, HOME: join(root, 'work') },
};
if (process.argv[3]) {
  config.environment_instance_id = '0b58d002-1b74-5ec6-b0ff-6ca530e98f03';
  config.executor_boot_id = '19707ddd-177e-506c-893b-9f45c2f975a1';
  config.activity_source = {
    workspace_id: config.workspace_id,
    session_id: config.session_id,
    environment_instance_id: config.environment_instance_id,
    executor_boot_id: config.executor_boot_id,
    connection_id: process.argv[3],
    binding_id: 'builtin',
    config_revision: 1,
  };
}
const log = createLogger('DEBUG');
const manager = new ProcessManager(config, log);
const guard = new StateGuard(config.state_directory, config.cwd);
await guard.initialize();
const builtin = createBuiltinServer(
  config.cwd,
  log,
  createFileTools(config.cwd, guard),
  manager,
  guard,
);
const transport = new StdioServerTransport();
const handle = serveStdio(() => builtin.server, { transport, legacy: 'reject' });
let stop;
const stopped = new Promise((resolve) => {
  stop = resolve;
});
const old = transport.onclose;
transport.onclose = () => {
  old?.();
  stop();
};
process.once('SIGTERM', stop);
await stopped;
setTimeout(() => process.exit(1), 3000).unref();
await handle.close();
process.stdin.destroy();
await builtin.drain();

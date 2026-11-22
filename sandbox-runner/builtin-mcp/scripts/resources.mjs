import { mkdir, copyFile, chmod } from 'node:fs/promises';
import { createRequire } from 'node:module';
import { dirname, join } from 'node:path';

// Runtime and validation read the same archived JSON Schema authority.
// The runtime artifact is self-contained and never reads the source checkout.
const output = new URL('../dist/assets/', import.meta.url);
await mkdir(output, { recursive: true });
await copyFile(
  new URL('../contracts/json-schema/builtin/tools.json', import.meta.url),
  new URL('bash-tools.json', output),
);
await copyFile(
  new URL('../contracts/json-schema/builtin/activity.json', import.meta.url),
  new URL('activity.json', output),
);

// The pinned macOS prebuild publishes spawn-helper without its executable
// bit. Package an executable copy, leaving shared installed dependencies alone.
if (process.platform === 'darwin') {
  const require = createRequire(import.meta.url);
  const { native } = require('node-pty');
  const root = dirname(require.resolve('node-pty/package.json'));
  const loaded = Object.keys(require.cache).find(
    (path) =>
      path.startsWith(root + '/') &&
      path.endsWith('/pty.node') &&
      require.cache[path].exports === native,
  );
  if (!loaded) throw new Error('PTY native module identity unavailable.');
  const helper = new URL('pty-spawn-helper', output);
  await copyFile(join(dirname(loaded), 'spawn-helper'), helper);
  await chmod(helper, 0o755);
}

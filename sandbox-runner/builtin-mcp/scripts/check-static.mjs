import { cp, mkdtemp, rm, symlink } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { spawnSync } from 'node:child_process';

// Build vendor declarations in isolation so lint also works on a clean checkout.
// Never reuse possibly stale dist/lib outputs or modify the source checkout.
const root = fileURLToPath(new URL('../', import.meta.url));
const temporary = await mkdtemp(join(tmpdir(), 'builtin-mcp-check-'));
try {
  await cp(root, temporary, {
    recursive: true,
    filter: (source) =>
      !['node_modules', 'dist', 'vendor/pi-tools/lib', '.git'].some(
        (excluded) =>
          source === join(root, excluded) || source.startsWith(join(root, excluded) + '/'),
      ),
  });
  await symlink(join(root, 'node_modules'), join(temporary, 'node_modules'), 'dir');
  const run = (command, args) => {
    const result = spawnSync(command, args, { cwd: temporary, stdio: 'inherit' });
    if (result.error) throw result.error;
    if (result.status !== 0)
      throw new Error(`${command} failed (${result.status ?? result.signal})`);
  };
  const tsc = join(temporary, 'node_modules/typescript/bin/tsc');
  run(process.execPath, [tsc, '-p', 'vendor/pi-tools/tsconfig.json']);
  run(process.execPath, [tsc, '-p', 'tsconfig.json', '--noEmit']);
  if (!process.argv.includes('--types-only')) {
    for (const script of ['lint', 'format:check', 'check:generated']) {
      run('npm', ['run', script]);
    }
  }
} finally {
  await rm(temporary, { recursive: true, force: true });
}

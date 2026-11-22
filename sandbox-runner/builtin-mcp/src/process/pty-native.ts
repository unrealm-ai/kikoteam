import { createRequire } from 'node:module';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

const require = createRequire(import.meta.url);
type Fork = (...args: unknown[]) => unknown;

// node-pty 1.1.0 exports this deliberately unstable API. Keep every ABI and
// loader assumption here; upgrades require the real PTY integration tests.
// Its high-level wrapper destroys the master 200ms after parent exit, which
// cannot represent a still-running descendant that owns the slave.
export function ptyFork(): { fork: Fork; helper: string } {
  const module: unknown = require('node-pty');
  if (!module || typeof module !== 'object' || !('native' in module))
    throw new Error('PTY native API unavailable.');
  const native = module.native;
  if (
    !native ||
    typeof native !== 'object' ||
    !('fork' in native) ||
    typeof native.fork !== 'function'
  )
    throw new Error('PTY native fork unavailable.');
  const root = dirname(require.resolve('node-pty/package.json'));
  // Match the module that was actually loaded, not the first existing helper.
  const loaded = Object.keys(require.cache).find(
    (path) =>
      path.startsWith(root + '/') &&
      path.endsWith('/pty.node') &&
      require.cache[path]?.exports === native,
  );
  if (!loaded) throw new Error('PTY native module identity unavailable.');
  return {
    fork: (...args) => Reflect.apply(native.fork as (...args: unknown[]) => unknown, native, args),
    helper:
      process.platform === 'darwin'
        ? fileURLToPath(new URL('../assets/pty-spawn-helper', import.meta.url))
        : join(dirname(loaded), 'spawn-helper'),
  };
}

export function ptyIdentity(value: unknown): { fd: number; pid: number } {
  if (
    !value ||
    typeof value !== 'object' ||
    !('fd' in value) ||
    !('pid' in value) ||
    typeof value.fd !== 'number' ||
    typeof value.pid !== 'number' ||
    !Number.isSafeInteger(value.fd) ||
    !Number.isSafeInteger(value.pid) ||
    value.fd < 0 ||
    value.pid <= 0
  )
    throw new Error('Invalid PTY native handle.');
  return { fd: value.fd, pid: value.pid };
}

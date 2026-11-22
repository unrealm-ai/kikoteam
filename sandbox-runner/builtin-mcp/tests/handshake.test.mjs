import assert from 'node:assert/strict';
import { test } from 'node:test';
import { mkdtemp, rm, readFile, stat } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { spawn } from 'node:child_process';
import { setTimeout as delay } from 'node:timers/promises';
import { control } from '../dist/process/control.js';
import { groupMembers } from '../dist/process/group.js';
import { spawnPipe, SpawnFailure } from '../dist/process/driver.js';

const events = () => ({
  attached() {},
  spawned() {},
  data() {},
  streamClosed() {},
  exit() {},
  deadline() {},
  closed() {},
  lost() {},
});
async function fixture(t, command, preload) {
  const root = await mkdtemp(join(tmpdir(), 'builtin-handshake-'));
  const seen = [];
  const callbacks = events();
  callbacks.exit = (code, signal) => seen.push({ code, signal });
  const bridge = await control(root, callbacks);
  const child = spawn(
    process.execPath,
    [
      ...(preload ? ['--import', fileURLToPath(new URL(preload, import.meta.url))] : []),
      fileURLToPath(new URL('../dist/process/supervisor-entry.js', import.meta.url)),
      bridge.path,
      command,
      '60000',
    ],
    { detached: true, cwd: root, stdio: 'ignore' },
  );
  const exited = new Promise((resolve) =>
    child.once('exit', (code, signal) => resolve({ code, signal })),
  );
  t.after(async () => {
    await bridge.signal('SIGKILL').catch(() => {});
    bridge.close();
    const timer = setTimeout(() => child.kill('SIGKILL'), 3000);
    await exited;
    clearTimeout(timer);
    await rm(root, { recursive: true, force: true });
  });
  assert.equal(await bridge.ready, child.pid);
  return { root, bridge, child, seen, exited };
}

test('ready waits for explicit start; Bash exit 70 is an actual command result', async (t) => {
  const f = await fixture(t, 'touch executed; exit 70');
  await assert.rejects(stat(join(f.root, 'executed')), { code: 'ENOENT' });
  await f.bridge.start();
  await f.bridge.started;
  await f.exited;
  await stat(join(f.root, 'executed'));
  assert.deepEqual(f.seen, [{ code: 70, signal: null }]);
});

test('stop before release never starts Bash', async (t) => {
  const f = await fixture(t, 'touch executed');
  await f.bridge.signal('SIGTERM');
  await f.exited;
  await assert.rejects(stat(join(f.root, 'executed')), { code: 'ENOENT' });
});

test('exec error is launch_failed and cannot be mistaken for Bash exit 70', async (t) => {
  const f = await fixture(t, 'touch executed', './spawn-failure-loader.mjs');
  await f.bridge.start();
  await assert.rejects(f.bridge.started, (error) => error.code === 'spawn_failed');
  await f.exited;
  assert.deepEqual(f.seen, []);
  await assert.rejects(stat(join(f.root, 'executed')), { code: 'ENOENT' });
});

test('failure to persist attachment prevents command effects', async (t) => {
  const root = await mkdtemp(join(tmpdir(), 'builtin-attach-'));
  t.after(() => rm(root, { recursive: true, force: true }));
  await assert.rejects(
    spawnPipe(
      { command: 'touch executed', cwd: root, stdin_open: false, max_runtime_ms: 60000 },
      { cwd: root, state_directory: root, environment: { PATH: process.env.PATH } },
      {
        ...events(),
        attached() {
          throw new Error('injected persistence failure');
        },
      },
      new AbortController().signal,
    ),
    /injected persistence failure/,
  );
  await assert.rejects(stat(join(root, 'executed')), { code: 'ENOENT' });
});

test('cancellation after attachment but before release has no command effects', async (t) => {
  const root = await mkdtemp(join(tmpdir(), 'builtin-cancel-release-'));
  t.after(() => rm(root, { recursive: true, force: true }));
  const abort = new AbortController();
  await assert.rejects(
    spawnPipe(
      { command: 'touch executed', cwd: root, stdin_open: false, max_runtime_ms: 60000 },
      { cwd: root, state_directory: root, environment: { PATH: process.env.PATH } },
      {
        ...events(),
        attached() {
          abort.abort();
        },
      },
      abort.signal,
    ),
    (error) => error instanceof SpawnFailure && error.code === 'cancelled',
  );
  await assert.rejects(stat(join(root, 'executed')), { code: 'ENOENT' });
});

test('closing the execution gate during attachment prevents release', async (t) => {
  const root = await mkdtemp(join(tmpdir(), 'builtin-gate-release-'));
  t.after(() => rm(root, { recursive: true, force: true }));
  let open = true;
  await assert.rejects(
    spawnPipe(
      { command: 'touch executed', cwd: root, stdin_open: false, max_runtime_ms: 60000 },
      { cwd: root, state_directory: root, environment: { PATH: process.env.PATH } },
      {
        ...events(),
        attached() {
          open = false;
        },
      },
      new AbortController().signal,
      () => open,
    ),
    (error) => error instanceof SpawnFailure && error.code === 'cancelled',
  );
  await assert.rejects(stat(join(root, 'executed')), { code: 'ENOENT' });
});

test(
  'Linux proc exit/read race does not interrupt group TERM escalation',
  { skip: process.platform !== 'linux' },
  async (t) => {
    const f = await fixture(
      t,
      'trap "" TERM; touch executed; while :; do sleep .1; done',
      './proc-race-loader.mjs',
    );
    await f.bridge.start();
    await f.bridge.started;
    await delay(100);
    await f.bridge.signal('SIGTERM');
    let observed;
    for (let i = 0; i < 100; i++) {
      try {
        observed = JSON.parse(await readFile(`${f.bridge.path}.race`, 'utf8'));
        break;
      } catch (error) {
        if (error.code !== 'ENOENT') throw error;
      }
      await delay(20);
    }
    assert.deepEqual(observed, { code: 'ESRCH' });
    f.bridge.close();
    assert.deepEqual(await f.exited, { code: null, signal: 'SIGKILL' });
    assert.deepEqual(await groupMembers(f.child.pid), []);
  },
);

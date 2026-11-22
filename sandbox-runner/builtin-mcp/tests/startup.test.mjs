import assert from 'node:assert/strict';
import { test } from 'node:test';
import { spawn } from 'node:child_process';
import { mkdtemp, rm, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { once } from 'node:events';

const main = fileURLToPath(new URL('../dist/main.js', import.meta.url));

async function child(t, env = {}) {
  const cwd = await mkdtemp(join(tmpdir(), 'builtin-startup-'));
  const process = spawn(globalThis.process.execPath, [main, '--cwd', cwd], {
    env: { PATH: globalThis.process.env.PATH, HOME: cwd, ...env },
    stdio: ['pipe', 'pipe', 'pipe'],
  });
  let stdout = '';
  let stderr = '';
  const started = new Promise((resolve) =>
    process.stderr.on('data', (chunk) => {
      stderr += chunk.toString();
      if (stderr.includes('server.started')) resolve();
    }),
  );
  process.stdout.on('data', (chunk) => {
    stdout += chunk.toString();
  });
  const exited = once(process, 'exit');
  const ready = Promise.race([
    started,
    exited.then(() => {
      throw new Error('Child exited before readiness: ' + stderr);
    }),
  ]);
  ready.catch(() => {});
  t.after(async () => {
    if (process.exitCode === null && process.signalCode === null) {
      process.kill('SIGKILL');
      await exited;
    }
    await rm(cwd, { recursive: true, force: true });
  });
  return {
    cwd,
    process,
    started: ready,
    exited,
    stdout: () => stdout,
    logs: () =>
      stderr
        .split('\n')
        .filter(Boolean)
        .map((line) => JSON.parse(line)),
  };
}

test('missing search binaries fail startup without downloading or writing protocol output', async (t) => {
  const f = await child(t, { PATH: '/nonexistent', PI_OFFLINE: '0' });
  const [code, signal] = await f.exited;
  assert.equal(code, 1);
  assert.equal(signal, null);
  assert.equal(f.stdout(), '');
  assert.ok(f.logs().some((line) => line.event === 'server.failed'));
  assert.equal(f.logs().find((line) => line.event === 'server.failed').reason, 'dependency_rg');
});

test('invalid deployment log level fails with safe JSON and no raw value', async (t) => {
  const f = await child(t, {
    BUILTIN_MCP_LOG_LEVEL: 'synthetic-private-invalid-level',
  });
  assert.equal((await f.exited)[0], 1);
  assert.equal(f.stdout(), '');
  assert.ok(!JSON.stringify(f.logs()).includes('synthetic-private-invalid-level'));
  assert.equal(f.logs().find((line) => line.event === 'server.failed').reason, 'configuration');
});

test('SIGTERM drains and exits even before first client request', async (t) => {
  const f = await child(t);
  await f.started;
  f.process.kill('SIGTERM');
  assert.deepEqual(await f.exited, [0, null]);
  assert.equal(f.stdout(), '');
  assert.ok(f.logs().some((line) => line.event === 'server.stopped'));
});

test('over-budget stdio input closes the connection and exits', async (t) => {
  const f = await child(t);
  await f.started;
  f.process.stdin.on('error', () => {}); // peer closes before the oversized write finishes
  f.process.stdin.write(Buffer.alloc(20 * 1024 * 1024 + 1, 120));
  assert.deepEqual(await f.exited, [0, null]);
  assert.ok(f.logs().some((line) => line.event === 'protocol.failed'));
});

test('shutdown deadline covers stdout backpressure after an actual edit completed', async (t) => {
  const f = await child(t);
  await f.started;
  await writeFile(join(f.cwd, 'large-edit'), 'hello' + 'x'.repeat(1_000_000) + '\n');
  f.process.stdout.pause();
  const completed = new Promise((resolve) =>
    f.process.stderr.on('data', () => {
      if (f.logs().some((line) => line.event === 'tool.completed' && line.outcome === 'succeeded'))
        resolve();
    }),
  );
  f.process.stdin.write(
    JSON.stringify({
      jsonrpc: '2.0',
      id: 1,
      method: 'tools/call',
      params: {
        name: 'edit',
        arguments: { path: 'large-edit', edits: [{ oldText: 'hello', newText: 'HELLO' }] },
        _meta: {
          'io.modelcontextprotocol/protocolVersion': '2026-07-28',
          'io.modelcontextprotocol/clientCapabilities': {},
        },
      },
    }) + '\n',
  );
  await completed;
  const started = performance.now();
  f.process.kill('SIGTERM');
  const [code, signal] = await f.exited;
  f.process.stdout.resume();
  assert.equal(code, 1);
  assert.equal(signal, null);
  assert.ok(performance.now() - started < 8000);
  assert.ok(f.logs().some((line) => line.event === 'server.shutdown_timeout'));
});

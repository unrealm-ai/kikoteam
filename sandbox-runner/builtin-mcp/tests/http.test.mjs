import assert from 'node:assert/strict';
import { test } from 'node:test';
import { randomUUID, randomBytes } from 'node:crypto';
import { mkdtemp, mkdir, writeFile, readFile, rm } from 'node:fs/promises';
import { join } from 'node:path';
import { spawn, spawnSync } from 'node:child_process';
import { request } from 'node:http';
import { once } from 'node:events';
import { setTimeout as delay } from 'node:timers/promises';
import {
  Client,
  StreamableHTTPClientTransport,
  fromJsonSchema,
} from '@modelcontextprotocol/client';

function manage(socketPath, method, path, value) {
  return new Promise((resolve, reject) => {
    const req = request(
      { socketPath, method, path, headers: { 'content-type': 'application/json' } },
      (res) => {
        let text = '';
        res.on('data', (data) => {
          text += data;
        });
        res.on('end', () => resolve({ status: res.statusCode, body: JSON.parse(text) }));
      },
    );
    req.on('error', reject);
    req.end(value ? JSON.stringify(value) : undefined);
  });
}

async function fixture(t) {
  const root = await mkdtemp('/tmp/builtin-http-');
  const cwd = join(root, 'work');
  await mkdir(cwd);
  const state = join(root, 'state-'.repeat(20));
  const args = [
    '--cwd',
    cwd,
    '--workspace-id',
    randomUUID(),
    '--session-id',
    randomUUID(),
    '--sandbox-instance-id',
    randomUUID(),
    '--runner-boot-id',
    randomUUID(),
    '--state-dir',
    state,
    '--runtime-dir',
    root,
    '--connection-id',
    randomUUID(),
    '--binding-id',
    'builtin',
    '--config-revision',
    '1',
  ];
  const discovery = randomBytes(32).toString('hex');
  const config = join(root, 'launch.json');
  await writeFile(
    config,
    JSON.stringify({
      launch_args: args,
      host: '127.0.0.1',
      port: 0,
      allowed_hosts: ['127.0.0.1'],
      discovery_token: discovery,
    }),
    { mode: 0o600 },
  );
  const child = spawn(
    process.execPath,
    ['--disable-warning=ExperimentalWarning', 'dist/http-main.js', '--config', config],
    { cwd: new URL('..', import.meta.url), stdio: ['ignore', 'ignore', 'pipe'] },
  );
  let logs = '';
  child.stderr.on('data', (data) => {
    logs += data;
  });
  const clients = [];
  t.after(async () => {
    await Promise.allSettled(clients.map((client) => client.close()));
    if (child.exitCode === null && child.signalCode === null) {
      child.kill('SIGTERM');
      const killed = setTimeout(() => child.kill('SIGKILL'), 14000);
      await once(child, 'exit');
      clearTimeout(killed);
    }
    await rm(root, { recursive: true, force: true });
  });
  let port;
  for (let n = 0; n < 200; n++) {
    const line = logs.split('\n').find((line) => line.includes('Builtin HTTP MCP ready'));
    if (line) {
      port = JSON.parse(line).port;
      break;
    }
    if (child.exitCode !== null) throw new Error('HTTP startup failed: ' + logs);
    await delay(50);
  }
  assert.ok(port, 'HTTP did not become ready: ' + logs);
  const url = new URL(`http://127.0.0.1:${port}/mcp`);
  const socket = join(root, 'm.sock');
  async function client(token) {
    const result = new Client(
      { name: 'builtin-http-test', version: '1' },
      { capabilities: {}, versionNegotiation: { mode: { pin: '2026-07-28' } } },
    );
    clients.push(result);
    await result.connect(
      new StreamableHTTPClientTransport(url, {
        requestInit: { headers: { Authorization: 'Bearer ' + token } },
      }),
      { timeout: 10000 },
    );
    return result;
  }
  const window = {
    control_revision: 2,
    assignment_id: randomUUID(),
    generation: 1,
    harness_instance_id: randomUUID(),
    turn_id: randomUUID(),
    open: true,
    access_token: randomBytes(32).toString('hex'),
  };
  return {
    child,
    root,
    cwd,
    logs: () => logs,
    url,
    client,
    discovery,
    window,
    manage: (method, path, value) => manage(socket, method, path, value),
  };
}

const supported = { skip: !['linux', 'darwin'].includes(process.platform) };
test(
  'launch rejects a missing, overlong or occupied runtime directory and preserves the live socket',
  supported,
  async (t) => {
    const f = await fixture(t);
    const config = JSON.parse(await readFile(join(f.root, 'launch.json'), 'utf8'));
    const args = config.launch_args;
    args[args.indexOf('--state-dir') + 1] = join(f.root, 'other-state');
    args.splice(args.indexOf('--runtime-dir'), 2);
    const second = join(f.root, 'second.json');
    for (const [flags, reason] of [
      [[], /--runtime-dir requires an absolute path/],
      [['--runtime-dir', join(f.root, '路'.repeat(32))], /too long for Unix sockets/],
      [['--runtime-dir', f.root], /initialization/],
    ]) {
      await writeFile(second, JSON.stringify({ ...config, launch_args: [...args, ...flags] }), {
        mode: 0o600,
      });
      const child = spawnSync(process.execPath, ['dist/http-main.js', '--config', second], {
        cwd: new URL('..', import.meta.url),
        encoding: 'utf8',
        timeout: 10000,
      });
      assert.equal(child.status, 1);
      assert.match(child.stderr, reason);
      assert.equal((await f.manage('GET', '/state')).status, 200);
    }
  },
);

test(
  'a later Turn of the same runtime can manage background work after HTTP disconnect',
  supported,
  async (t) => {
    const f = await fixture(t);
    assert.equal((await f.manage('POST', '/execution-window', f.window)).status, 200);
    const first = await f.client(f.window.access_token);
    const started = await first.callTool({
      name: 'bash_start',
      arguments: { command: 'sleep 60', yield_time_ms: 0 },
    });
    assert.ok(!started.isError);
    await first.close();
    const next = {
      ...f.window,
      control_revision: 3,
      turn_id: randomUUID(),
      access_token: f.window.access_token,
    };
    const switched = await f.manage('POST', '/execution-window', next);
    assert.ok(
      [200, 202].includes(switched.status),
      JSON.stringify({ switched, state: await f.manage('GET', '/state') }),
    );
    const second = await f.client(next.access_token);
    const listed = await second.callTool({ name: 'bash_list', arguments: {} });
    const process = listed.structuredContent.items.find(
      (item) => item.process_id === started.structuredContent.process_id,
    );
    assert.equal(process.origin_turn_id, f.window.turn_id);
    assert.equal(process.state, 'running');
    const stopped = await second.callTool({
      name: 'bash_terminate',
      arguments: { process_id: process.process_id },
    });
    assert.ok(!stopped.isError);
    await second.callTool({
      name: 'bash_poll',
      arguments: { process_id: process.process_id, yield_time_ms: 1000 },
    });
  },
);

test(
  'production HTTP exposes all eleven tools, binds identity, and stops managed commands',
  supported,
  async (t) => {
    const f = await fixture(t);
    assert.equal((await fetch(f.url, { method: 'POST' })).status, 401);
    const discovery = await f.client(f.discovery);
    assert.equal((await discovery.listTools()).tools.length, 11);
    await assert.rejects(
      discovery.callTool({
        name: 'bash_start',
        arguments: { command: 'touch forbidden' },
      }),
    );
    assert.equal((await f.manage('POST', '/execution-window', f.window)).status, 200);
    assert.equal((await f.manage('POST', '/execution-window', f.window)).status, 200);
    const client = await f.client(f.window.access_token);
    const call = (name, args) => client.callTool({ name, arguments: args }, { timeout: 15000 });
    await assert.rejects(
      client.request(
        { method: 'builtin/processes/activity', params: {} },
        fromJsonSchema({ type: 'object' }),
      ),
      /Method not found/,
    );
    const created = await call('write', { path: 'nested/demo.txt', content: 'alpha\nbeta\n' });
    assert.ok(!created.isError, JSON.stringify(created));
    const edited = await call('edit', {
      path: 'nested/demo.txt',
      edits: [{ oldText: 'beta', newText: 'gamma' }],
    });
    assert.ok(!edited.isError, JSON.stringify(edited));
    for (const [name, args] of [
      ['read', { path: 'nested/demo.txt' }],
      ['grep', { pattern: 'gamma', path: 'nested' }],
      ['find', { pattern: '**/*.txt' }],
      ['ls', { path: 'nested' }],
    ]) {
      const result = await call(name, args);
      assert.ok(!result.isError, name + JSON.stringify(result));
      assert.match(
        JSON.stringify(result),
        name === 'read' || name === 'grep' ? /gamma/ : /demo.txt/,
      );
    }
    assert.equal(await readFile(join(f.cwd, 'nested/demo.txt'), 'utf8'), 'alpha\ngamma\n');
    const started = await call('bash_start', {
      command: 'read line; printf "received:%s" "$line"',
      stdin_open: true,
      yield_time_ms: 0,
    });
    const pid = started.structuredContent.process_id;
    assert.match(pid, /^[0-9a-f-]{36}$/);
    const written = await call('bash_write', {
      process_id: pid,
      text: 'hello\n',
      close_stdin: true,
    });
    assert.equal(written.structuredContent.input_state, 'accepted');
    const polled = await call('bash_poll', { process_id: pid, yield_time_ms: 1000 });
    assert.match(JSON.stringify(polled), /received:hello/);
    const long = await call('bash_start', { command: 'sleep 60', yield_time_ms: 0 });
    assert.ok(
      (await call('bash_list', {})).structuredContent.items.some(
        (item) => item.process_id === long.structuredContent.process_id,
      ),
    );
    await call('bash_terminate', { process_id: long.structuredContent.process_id });
    await call('bash_poll', { process_id: long.structuredContent.process_id, yield_time_ms: 1000 });
    const stopping = await call('bash_start', { command: 'sleep 60', yield_time_ms: 0 });
    assert.ok(!stopping.isError);
    assert.equal(
      (
        await f.manage('POST', '/execution-window', {
          ...f.window,
          control_revision: 3,
          harness_instance_id: randomUUID(),
          access_token: randomBytes(32).toString('hex'),
        })
      ).status,
      409,
    );
    const stop = { ...f.window, control_revision: 3, open: false };
    assert.ok([200, 202].includes((await f.manage('POST', '/stop', stop)).status));
    let state;
    for (let i = 0; i < 160; i++) {
      state = (await f.manage('GET', '/state')).body;
      if (state.verified && !state.active && !state.unknown && !state.pending_writes) break;
      await delay(50);
    }
    assert.equal(state.verified, true);
    assert.equal(state.active + state.unknown + state.pending_writes, 0);
    assert.equal((await f.manage('POST', '/execution-window', f.window)).status, 409);
    await assert.rejects(call('bash_start', { command: 'touch forbidden' }));
    const next = {
      ...f.window,
      control_revision: 4,
      turn_id: randomUUID(),
      access_token: f.window.access_token,
    };
    assert.equal(
      (
        await f.manage('POST', '/execution-window', {
          ...next,
          access_token: randomBytes(32).toString('hex'),
        })
      ).status,
      409,
    );
    assert.equal((await f.manage('POST', '/execution-window', next)).status, 200);
    assert.notEqual(
      (
        await fetch(f.url, {
          method: 'POST',
          headers: { Authorization: 'Bearer ' + f.window.access_token },
        })
      ).status,
      401,
    );
    const current = await f.client(next.access_token);
    const result = await current.callTool({
      name: 'bash_start',
      arguments: { command: 'printf after-stop' },
      _meta: { 'builtin-mcp/call': { session_id: randomUUID() } },
    });
    assert.ok(!result.isError, JSON.stringify(result));
    assert.match(JSON.stringify(result), /after-stop/);
    const listed = await current.callTool({ name: 'bash_list', arguments: {} });
    assert.equal(
      listed.structuredContent.items.find(
        (item) => item.process_id === result.structuredContent.process_id,
      ).origin_turn_id,
      next.turn_id,
    );
    const shutdownWork = await current.callTool({
      name: 'bash_start',
      arguments: { command: 'sleep 60', yield_time_ms: 0 },
    });
    assert.ok(!shutdownWork.isError);
    assert.ok(!f.logs().includes(next.access_token));
    f.child.kill('SIGTERM');
    const [exit] = await once(f.child, 'exit');
    assert.equal(exit, 0, f.logs());
  },
);

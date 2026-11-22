import assert from 'node:assert/strict';
import { test } from 'node:test';
import { mkdtemp, mkdir, rm, readFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { setTimeout as delay } from 'node:timers/promises';
import { Client } from '@modelcontextprotocol/client';
import { StdioClientTransport } from '@modelcontextprotocol/client/stdio';
import { DatabaseSync } from 'node:sqlite';
import { execFileSync } from 'node:child_process';
import { processId } from '../dist/process/identity.js';
import { groupMembers } from '../dist/process/group.js';
import { fromJsonSchema } from '@modelcontextprotocol/server';

// Managed launch credentials identify exactly one process. The SDK probes a
// subclass in place instead of spawning a disposable sibling with those same
// credentials; this matches Executor's single owned stdio connection.
class ManagedStdioTransport extends StdioClientTransport {}
const meta = () => ({
  workspace_id: '3f716000-07b4-5252-9e2a-7da0bbdc5c8a',
  session_id: '921130c5-ba8b-5733-8fa6-66a9bbc776b3',
  runtime_id: '53b43a70-4eb0-5ece-b3a2-70d9cb273366',
  origin_turn_id: '6d36e372-640e-5885-8827-b32b3575585f',
  operation_id: processId(),
  trace_id: '1234567890abcdef1234567890abcdef',
});

async function fixture(t, managed = false) {
  const root = await mkdtemp(join(tmpdir(), 'builtin-wire-'));
  const cwd = join(root, 'work');
  await mkdir(cwd);
  const clients = [];
  let client;
  let transport;
  let logs = '';
  let source;
  const connect = async () => {
    const connection = processId();
    source = {
      workspace_id: '3f716000-07b4-5252-9e2a-7da0bbdc5c8a',
      session_id: '921130c5-ba8b-5733-8fa6-66a9bbc776b3',
      environment_instance_id: managed
        ? '0b58d002-1b74-5ec6-b0ff-6ca530e98f03'
        : 'ceb40783-4b1b-5ed9-9b10-7b3711def584',
      executor_boot_id: managed ? '19707ddd-177e-506c-893b-9f45c2f975a1' : 'fixture',
      connection_id: connection,
      binding_id: 'builtin',
      config_revision: 1,
    };
    const Transport = managed ? ManagedStdioTransport : StdioClientTransport;
    transport = new Transport({
      command: process.execPath,
      args: [
        '--disable-warning=ExperimentalWarning',
        fileURLToPath(new URL('./process-server.mjs', import.meta.url)),
        root,
        ...(managed ? [connection] : []),
      ],
      env: { PATH: process.env.PATH, HOME: cwd, PI_OFFLINE: '1' },
      stderr: 'pipe',
    });
    transport.stderr.on('data', (chunk) => {
      logs += chunk.toString();
    });
    client = new Client(
      { name: 'process-test', version: '1' },
      {
        capabilities: {},
        versionNegotiation: { mode: { pin: '2026-07-28' } },
        inputRequired: { autoFulfill: false },
      },
    );
    clients.push(client);
    try {
      await client.connect(transport, { timeout: 10000 });
    } catch (error) {
      throw new Error(`Synthetic stdio fixture failed: ${logs}`, { cause: error });
    }
  };
  await connect();
  const call = (name, args, origin = meta(), options = {}) =>
    client.callTool(
      {
        name,
        arguments: args,
        _meta: origin === null ? undefined : { 'builtin-mcp/call': origin },
      },
      { timeout: 20000, ...options },
    );
  t.after(async () => {
    for (const c of clients) await c.close();
    const db = new DatabaseSync(join(root, 'state', 'processes.sqlite'));
    for (const { body } of db.prepare('SELECT body FROM processes').all()) {
      const { pid } = JSON.parse(body);
      try {
        const command = execFileSync('/bin/ps', ['-p', String(pid), '-o', 'command='], {
          stdio: ['ignore', 'pipe', 'ignore'],
        }).toString();
        if (command.includes('supervisor-entry.js') && command.includes(root))
          process.kill(-pid, 'SIGKILL');
      } catch {
        /* Owned test group already exited. */
      }
    }
    db.close();
    await rm(root, { recursive: true, force: true });
  });
  return {
    cwd,
    root,
    call,
    get client() {
      return client;
    },
    get pid() {
      return transport.pid;
    },
    logs: () => logs,
    activity: (overrides = {}) =>
      client
        .request(
          {
            method: 'builtin/processes/activity',
            params: {
              source,
              stop_gate: null,
              local_revision: 1,
              ...overrides,
            },
          },
          fromJsonSchema({ type: 'object' }),
          { timeout: 10000 },
        )
        .catch((error) => {
          if (error.code === 'CONNECTION_CLOSED') t.diagnostic(logs);
          throw error;
        }),
    get source() {
      return source;
    },
    async restart() {
      process.kill(transport.pid, 'SIGKILL');
      await delay(150);
      await connect();
    },
  };
}

test('real standard MCP exposes eleven tools and enforces trusted process metadata', async (t) => {
  const f = await fixture(t);
  const tools = (await f.client.listTools()).tools;
  assert.equal(tools.length, 11);
  assert.equal(tools.filter((tool) => tool.name.startsWith('bash_')).length, 5);
  await assert.rejects(f.call('bash_list', {}, null), (error) => error.code === -32602);
  await assert.rejects(
    f.call('bash_start', { command: 'x', env: {} }),
    (error) => error.code === -32602,
  );
  const started = await f.call('bash_start', {
    command: 'printf via-wire',
    cwd: f.cwd,
    yield_time_ms: 1000,
  });
  assert.equal(started.isError, undefined);
  // Pi's native MCP conversion uses structuredContent only when content is
  // empty; a placeholder here hides the process handle and output from models.
  assert.deepEqual(started.content, []);
  assert.match(started.structuredContent.chunks.map((c) => c.text).join(''), /via-wire/);
  const processID = started.structuredContent.process_id;
  const missingID = processID.slice(0, -1) + (processID.endsWith('0') ? '1' : '0');
  const missing = await f.call('bash_poll', { process_id: missingID });
  assert.equal(missing.isError, true);
  assert.deepEqual(missing.content, []);
  assert.equal(missing.structuredContent.error.code, 'process_not_found');
  const denied = await f.call('read', { path: join(f.root, 'state', 'processes.sqlite') });
  assert.equal(denied.isError, true);
  const records = f.logs().trim().split('\n').map(JSON.parse);
  assert.ok(records.some((entry) => entry.module === 'builtin.processes' && entry.operation_id));
  assert.ok(!f.logs().includes('printf via-wire'));
});

test('management RPC preserves eleven tools and refuses unconfigured sources and invalid messages', async (t) => {
  const portable = await fixture(t);
  await assert.rejects(portable.activity(), (error) => error.code === -32601);
  const f = await fixture(t, true);
  assert.equal((await f.client.listTools()).tools.length, 11);
  assert.equal((await f.call('write', { path: 'blocked', content: 'never' })).isError, true);
  await assert.rejects(readFile(join(f.cwd, 'blocked')));
  const initial = await f.activity();
  assert.equal(initial.verified, true);
  assert.deepEqual(initial.source, f.source);
  assert.equal(initial.active + initial.unknown + initial.pending_writes, 0);
  for (const fields of [
    { local_revision: 0 },
    { local_revision: '1' },
    { local_revision: Number.MAX_SAFE_INTEGER + 1 },
    { extra: true },
    { source: { ...f.source, config_revision: 0 } },
    { source: { ...f.source, binding_id: 'third-party' } },
    { stop_gate: { action_id: processId(), revision: 0 } },
    { _meta: { secret: 'synthetic-secret'.repeat(1000) } },
  ])
    await assert.rejects(
      f.activity(fields),
      (error) => error.code === -32602 && !error.message.includes('synthetic-secret'),
    );
  const gate = { action_id: processId(), revision: 1 };
  const closed = await f.activity({ local_revision: 2, stop_gate: gate });
  assert.deepEqual(closed.stop_gate, gate);
  assert.equal(closed.revision, initial.revision);
  assert.equal(
    (await f.call('bash_start', { command: 'touch blocked', cwd: f.cwd })).isError,
    true,
  );
  await assert.rejects(f.activity(), (error) => error.code === -32602);
  await f.activity({ local_revision: 3 });
  assert.equal((await f.call('write', { path: 'allowed', content: 'yes' })).isError, undefined);
  const complete = await f.activity({ local_revision: 3 });
  assert.equal(complete.active + complete.unknown + complete.pending_writes, 0);
  assert.ok(complete.revision > initial.revision);
  const origin = meta();
  const args = { command: 'printf once', cwd: f.cwd, yield_time_ms: 1000 };
  const started = await f.call('bash_start', args, origin);
  assert.equal(started.isError, undefined);
  await f.activity({ local_revision: 4, stop_gate: gate });
  const replayed = await f.call('bash_start', args, origin);
  assert.equal(replayed.isError, undefined);
  assert.deepEqual(replayed.structuredContent, started.structuredContent);
});

test('two blocked stdin writes cannot consume terminate capacity', async (t) => {
  const f = await fixture(t);
  const started = await f.call('bash_start', {
    command: 'sleep 20',
    stdin_open: true,
    cwd: f.cwd,
    yield_time_ms: 0,
  });
  const id = started.structuredContent.process_id;
  let blocked;
  for (let i = 0; i < 100; i++) {
    const call = f.call('bash_write', { process_id: id, text: 'x'.repeat(16384) });
    const result = await Promise.race([call, delay(50).then(() => null)]);
    if (result === null) {
      blocked = call;
      break;
    }
    assert.equal(result.isError, undefined);
  }
  assert.ok(blocked, 'real pipe must become full');
  const queued = f.call('bash_write', { process_id: id, text: 'end' });
  await delay(50);
  const begin = Date.now();
  const terminated = await f.call('bash_terminate', { process_id: id, mode: 'force' });
  assert.equal(terminated.isError, undefined);
  assert.ok(Date.now() - begin < 2000);
  await Promise.all([blocked, queued]);
});

test('SIGKILL of the Server recovers the same start result and marks live state lost', async (t) => {
  const f = await fixture(t);
  const origin = meta();
  const args = { command: 'sleep 20', cwd: f.cwd, yield_time_ms: 0 };
  const started = await f.call('bash_start', args, origin);
  const id = started.structuredContent.process_id;
  await f.restart();
  const replay = await f.call('bash_start', args, origin);
  assert.deepEqual(replay.structuredContent, started.structuredContent);
  const poll = await f.call('bash_poll', { process_id: id });
  assert.equal(poll.structuredContent.state, 'lost');
  assert.equal(poll.structuredContent.group_quiescent, null);
  assert.equal((await f.call('bash_list', {})).structuredContent.items.length, 1);
});

test('delivered TERM still escalates after the managing Server crashes', async (t) => {
  const f = await fixture(t);
  const started = await f.call('bash_start', {
    command: 'trap "" TERM; printf "%s" "$$" > command-pid; while :; do sleep .1; done',
    cwd: f.cwd,
    yield_time_ms: 300,
  });
  const pid = Number(await readFile(join(f.cwd, 'command-pid'), 'utf8'));
  const group = Number(
    execFileSync('/bin/ps', ['-p', String(pid), '-o', 'pgid='])
      .toString()
      .trim(),
  );
  assert.ok(group > 0);
  const stopAt = Date.now();
  const stopped = await f.call('bash_terminate', {
    process_id: started.structuredContent.process_id,
  });
  assert.equal(stopped.isError, undefined, JSON.stringify(stopped));
  assert.equal(stopped.structuredContent.termination_requested, true);
  await f.restart();
  let members;
  do {
    members = await groupMembers(group);
    if (!members.length) break;
    await delay(50);
  } while (Date.now() - stopAt < 8000);
  if (members.length)
    t.diagnostic(
      JSON.stringify({ group, command_pid: pid, stop: stopped.structuredContent, logs: f.logs() }),
    );
  assert.deepEqual(members, [], 'supervisor must retain the original five-second grace');
  assert.equal(
    (await f.call('bash_poll', { process_id: started.structuredContent.process_id }))
      .structuredContent.state,
    'lost',
  );
});

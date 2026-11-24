import assert from 'node:assert/strict';
import { test } from 'node:test';
import { mkdtemp, mkdir, rm, readFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { setTimeout as delay } from 'node:timers/promises';
import { ProcessManager } from '../dist/process/manager.js';
import { processTools } from '../dist/process/tools.js';
import { processId } from '../dist/process/identity.js';
import { DatabaseSync } from 'node:sqlite';
import { execFileSync } from 'node:child_process';

const context = () => ({
  workspace_id: '3f716000-07b4-5252-9e2a-7da0bbdc5c8a',
  session_id: '921130c5-ba8b-5733-8fa6-66a9bbc776b3',
  runtime_id: '53b43a70-4eb0-5ece-b3a2-70d9cb273366',
  origin_turn_id: '6d36e372-640e-5885-8827-b32b3575585f',
  operation_id: processId(),
  trace_id: '1234567890abcdef1234567890abcdef',
});
const definitions = processTools();

async function fixture(t, managed = false) {
  const root = await mkdtemp(join(tmpdir(), 'builtin-process-'));
  const cwd = join(root, 'work');
  await mkdir(cwd);
  const config = {
    ...context(),
    cwd,
    environment_instance_id: 'ceb40783-4b1b-5ed9-9b10-7b3711def584',
    executor_boot_id: 'test-boot',
    state_directory: join(root, 'private'),
    runtime_directory: root,
    max_runtime_ms: 86400000,
    allow_force: true,
    environment: { PATH: process.env.PATH, HOME: cwd },
  };
  if (managed)
    config.activity_source = {
      workspace_id: config.workspace_id,
      session_id: config.session_id,
      environment_instance_id: config.environment_instance_id,
      executor_boot_id: config.executor_boot_id,
      connection_id: processId(),
      binding_id: 'builtin',
      config_revision: 1,
    };
  // Like the production CLI, execute under the inherited Runner account.
  let manager = new ProcessManager(config, () => {});
  const call = async (tool, input, origin = context(), signal = new AbortController().signal) => {
    const entry = definitions.get(tool);
    const result = await manager.call(tool, await entry.validate(input), origin, signal);
    await entry.validateResult(result.structuredContent);
    return result;
  };
  const start = (command, args = {}, origin, signal) =>
    call('bash_start', { command, cwd, yield_time_ms: 0, ...args }, origin, signal);
  const until = async (id, condition = (r) => r.output_complete, timeout = 10000) => {
    const end = Date.now() + timeout;
    let last;
    while (Date.now() < end) {
      last = (await call('bash_poll', { process_id: id, yield_time_ms: 20 })).structuredContent;
      if (condition(last)) return last;
      await delay(30);
    }
    assert.fail(`Process did not reach expected state: ${JSON.stringify(last)}`);
  };
  t.after(async () => {
    if (managed)
      await manager.activity({
        source: config.activity_source,
        local_revision: 10000,
        stop_gate: null,
      });
    for (const item of (await call('bash_list', {})).structuredContent.items ?? []) {
      if (item.group_quiescent === false) {
        await call('bash_terminate', { process_id: item.process_id, mode: 'force' });
        await until(item.process_id, (r) => r.group_quiescent === true).catch(() => {});
      }
    }
    manager.close();
    const db = new DatabaseSync(join(config.state_directory, 'processes.sqlite'));
    for (const { body } of db.prepare('SELECT body FROM processes').all()) {
      const { pid } = JSON.parse(body);
      try {
        const command = execFileSync('/bin/ps', ['-p', String(pid), '-o', 'command='], {
          stdio: ['ignore', 'pipe', 'ignore'],
        }).toString();
        if (command.includes('supervisor-entry.js') && command.includes(root))
          process.kill(-pid, 'SIGKILL');
      } catch {
        /* Owned test process already exited. */
      }
    }
    db.close();
    await rm(root, { recursive: true, force: true });
  });
  return {
    root,
    cwd,
    config,
    call,
    start,
    until,
    activity: (local_revision = 1, stop_gate = null, source = config.activity_source) =>
      manager.activity({ source, local_revision, stop_gate }),
    restart() {
      manager.close();
      if (managed) config.activity_source.connection_id = processId();
      manager = new ProcessManager(config, () => {});
    },
    editStore(edit) {
      manager.close();
      const db = new DatabaseSync(join(config.state_directory, 'processes.sqlite'));
      try {
        edit(db);
      } finally {
        db.close();
      }
      manager = new ProcessManager(config, () => {});
    },
  };
}

test('four socket bind failures leave the next Bash slot available across restart', async (t) => {
  const f = await fixture(t, true);
  await f.activity();
  f.config.runtime_directory = join(f.root, 'missing');
  for (const tty of [false, true, false, true]) {
    const result = await f.start('touch unexpected', { tty });
    assert.equal(result.structuredContent.error.code, 'control_socket_unavailable');
    assert.equal(result.structuredContent.process_id, undefined);
  }
  await assert.rejects(readFile(join(f.cwd, 'unexpected')), { code: 'ENOENT' });
  const state = await f.activity();
  assert.equal(state.verified, true);
  assert.equal(state.active + state.unknown + state.pending_writes, 0);
  assert.deepEqual((await f.call('bash_list', {})).structuredContent.items, []);
  f.config.runtime_directory = f.root;
  f.restart();
  await f.activity();
  const started = await f.start('printf recovered');
  const done = await f.until(started.structuredContent.process_id);
  assert.equal(done.chunks.map((chunk) => chunk.text).join(''), 'recovered');
});

test('management gate is initially closed and ordered independently of process activity', async (t) => {
  const f = await fixture(t, true);
  assert.equal(
    (await f.start('printf forbidden')).structuredContent.error.code,
    'capacity_unavailable',
  );
  const open = await f.activity();
  assert.equal(open.verified, true);
  assert.equal(open.active + open.unknown + open.pending_writes, 0);
  const gate = { action_id: processId(), revision: 1 };
  const closed = await f.activity(2, gate);
  assert.equal(closed.revision, open.revision, 'gate changes are not process activity');
  assert.deepEqual(closed.stop_gate, gate);
  await assert.rejects(f.activity(1));
  await assert.rejects(f.activity(2));
  await assert.rejects(f.activity(3, null, { ...f.config.activity_source, config_revision: 2 }));
  assert.equal((await f.start('printf still-forbidden')).isError, true);
  const reopened = await f.activity(3);
  assert.equal(reopened.revision, open.revision);
  const started = await f.start('printf allowed');
  assert.equal(started.isError, undefined);
  await f.until(started.structuredContent.process_id);
  let final;
  for (let i = 0; i < 100; i++) {
    final = await f.activity(3);
    if (final.active + final.unknown + final.pending_writes === 0) break;
    await delay(20);
  }
  assert.equal(final.active + final.unknown + final.pending_writes, 0);
  assert.ok(final.revision > open.revision);
  assert.equal((await f.activity(3)).revision, final.revision);
  const racing = await Promise.allSettled([f.activity(4, gate), f.activity(5)]);
  assert.equal(
    racing[0].status,
    'rejected',
    'superseded sampling must not return an old gate proof',
  );
  assert.equal(racing[1].status, 'fulfilled');
});

test('management observes descendants, pending stdin after cancellation and old-boot unknowns', async (t) => {
  const f = await fixture(t, true);
  await f.activity();
  const started = await f.start('sleep 15', { stdin_open: true });
  const id = started.structuredContent.process_id;
  const abort = new AbortController();
  let pending;
  for (let i = 0; i < 100; i++) {
    const call = f.call(
      'bash_write',
      { process_id: id, text: 'x'.repeat(16384) },
      context(),
      abort.signal,
    );
    if ((await Promise.race([call, delay(40).then(() => null)])) === null) {
      pending = call;
      break;
    }
  }
  assert.ok(pending, 'real pipe must block');
  abort.abort();
  await pending;
  const busy = await f.activity();
  assert.equal(busy.verified, true);
  assert.ok(busy.active > 0);
  assert.ok(busy.pending_writes > 0, 'cancel does not release the OS write barrier');
  f.restart();
  const lost = await f.activity();
  assert.equal(lost.verified, true);
  assert.ok(lost.unknown > 0);
  assert.ok(lost.revision > busy.revision);
});

test('start yields a durable handle; exact retry is not another spawn; nonzero is structured', async (t) => {
  const f = await fixture(t);
  const origin = context();
  const started = await f.start('printf hello; sleep .3; exit 7', {}, origin);
  assert.equal(started.isError, undefined);
  assert.equal(started.structuredContent.state, 'running');
  const replay = await f.start(
    'printf hello; sleep .3; exit 7',
    {},
    { ...origin, runtime_id: '0b78e87e-b59b-5e2c-8a04-4cc14e770224' },
  );
  assert.deepEqual(replay, started);
  const done = await f.until(started.structuredContent.process_id);
  assert.equal(done.exit_code, 7);
  assert.equal(done.error.code, 'command_failed');
  assert.equal(done.chunks.map((c) => c.text).join(''), 'hello');
  assert.equal((await f.call('bash_list', {})).structuredContent.items.length, 1);
  f.restart();
  assert.deepEqual(await f.start('printf hello; sleep .3; exit 7', {}, origin), started);
});

test('pipe input is ordered, deduplicated and half-closes without adding newlines', async (t) => {
  const f = await fixture(t);
  const started = await f.start('cat', { stdin_open: true });
  const id = started.structuredContent.process_id;
  const origin = context();
  const input = { process_id: id, text: '你好' };
  const written = await f.call('bash_write', input, origin);
  assert.equal(written.structuredContent.accepted_bytes, 6);
  assert.deepEqual(await f.call('bash_write', input, origin), written);
  await f.call('bash_write', { process_id: id, text: '!', close_stdin: true });
  const done = await f.until(id);
  assert.equal(done.chunks.map((c) => c.text).join(''), '你好!');
});

test('PTY is a terminal, combines streams, and rejects half-close before any input', async (t) => {
  const f = await fixture(t);
  const started = await f.start(
    'test -t 0 && printf tty; read -r line; printf "VALUE:%s" "$line"',
    { tty: true },
  );
  assert.equal(started.isError, undefined, JSON.stringify(started));
  const id = started.structuredContent.process_id;
  const rejected = await f.call('bash_write', {
    process_id: id,
    text: 'wrong\n',
    close_stdin: true,
  });
  assert.equal(rejected.structuredContent.error.code, 'unsupported_operation');
  await f.call('bash_write', { process_id: id, text: 'ok\n' });
  const done = await f.until(id);
  assert.ok(done.chunks.every((c) => c.stream === 'pty'));
  assert.match(done.chunks.map((c) => c.text).join(''), /VALUE:ok/);
  assert.ok(!JSON.stringify(done.chunks).includes('wrong'));
});

test('main exit leaves descendants observable and terminable', async (t) => {
  const f = await fixture(t);
  const started = await f.start('(sleep 2; printf tail) & exit 0');
  const id = started.structuredContent.process_id;
  const parent = await f.until(id, (r) => r.state === 'exited');
  assert.equal(parent.group_quiescent, false);
  assert.equal(parent.output_complete, false);
  const done = await f.until(id);
  assert.equal(done.output_complete, true);
  assert.equal(done.chunks.map((c) => c.text).join(''), 'tail');
  const next = await f.start('(sleep 10) & exit 0');
  await f.until(next.structuredContent.process_id, (r) => r.state === 'exited');
  const stopped = await f.call('bash_terminate', { process_id: next.structuredContent.process_id });
  assert.equal(stopped.isError, undefined, JSON.stringify(stopped));
  await f.until(next.structuredContent.process_id);
});

test('cancelled poll leaves a live process and a later Turn can manage it', async (t) => {
  const f = await fixture(t);
  const started = await f.start('sleep 10');
  const id = started.structuredContent.process_id;
  const abort = new AbortController();
  const waiting = f.call(
    'bash_poll',
    { process_id: id, yield_time_ms: 10000 },
    context(),
    abort.signal,
  );
  setTimeout(() => abort.abort(), 100);
  assert.equal((await waiting).structuredContent.error.code, 'cancelled');
  const origin = { ...context(), origin_turn_id: processId() };
  const stopped = await f.call('bash_terminate', { process_id: id, mode: 'force' }, origin);
  assert.equal(stopped.structuredContent.termination_requested, true);
  const done = await f.until(id);
  assert.equal(done.termination_signal, 'SIGKILL');
});

test('output paging respects UTF8 boundaries; receipt body does not duplicate output', async (t) => {
  const f = await fixture(t);
  const started = await f.start('printf "你好%.0s" {1..5000}', { max_output_bytes: 1024 });
  const id = started.structuredContent.process_id;
  await f.until(id, (r) => r.state === 'exited' && r.group_quiescent === true);
  let cursor;
  let text = '';
  let page;
  do {
    page = (await f.call('bash_poll', { process_id: id, cursor, max_output_bytes: 1024 }))
      .structuredContent;
    const chunk = page.chunks.map((c) => c.text).join('');
    assert.ok(Buffer.byteLength(chunk) <= 1024);
    assert.ok(!chunk.includes('�'));
    text += chunk;
    cursor = page.next_cursor;
  } while (page.has_more);
  assert.equal(text, '你好'.repeat(5000));
  // A corrupted cursor is rejected instead of resetting to the beginning.
  assert.equal(
    (await f.call('bash_poll', { process_id: id, cursor: cursor + 'x' })).structuredContent.error
      .code,
    'invalid_cursor',
  );
  assert.ok(
    (await readFile(join(f.config.state_directory, 'processes.sqlite'))).length < 2 * 1024 * 1024,
  );
});

test('TERM grace is fixed across repeats; deadline and force terminate whole groups', async (t) => {
  const f = await fixture(t);
  const started = await f.start('trap "" TERM; printf ready; while :; do sleep .1; done');
  const id = started.structuredContent.process_id;
  await f.until(id, (r) => r.chunks.some((c) => c.text.includes('ready')));
  const begin = Date.now();
  await f.call('bash_terminate', { process_id: id });
  await delay(3000);
  await f.call('bash_terminate', { process_id: id });
  const done = await f.until(id);
  assert.equal(done.termination_signal, 'SIGKILL');
  assert.ok(Date.now() - begin < 7500, 'repeat TERM must not restart the grace period');
  const limited = await f.start('sleep 10', { max_runtime_ms: 1000 });
  await f.until(limited.structuredContent.process_id);
  const list = (await f.call('bash_list', {})).structuredContent.items;
  assert.equal(
    list.find((p) => p.process_id === limited.structuredContent.process_id).stop_reason,
    'deadline_reached',
  );
});

test('PTY parent exit does not discard a nohup descendant output tail', async (t) => {
  const f = await fixture(t);
  const started = await f.start('trap "" HUP; (sleep 1; printf late-pty) & exit 0', { tty: true });
  const id = started.structuredContent.process_id;
  const parent = await f.until(id, (r) => r.state === 'exited');
  assert.equal(parent.output_complete, false);
  const done = await f.until(id);
  assert.match(done.chunks.map((c) => c.text).join(''), /late-pty/);
});

test('recovery never adopts or respawns an old live process; lost records consume quota', async (t) => {
  const f = await fixture(t);
  const origin = context();
  const started = await f.start('echo $$ > original-pid; sleep 20', {}, origin);
  const id = started.structuredContent.process_id;
  await delay(100);
  assert.ok(Number(await readFile(join(f.cwd, 'original-pid'), 'utf8')) > 0);
  f.restart();
  const lost = await f.call('bash_poll', { process_id: id });
  assert.equal(lost.structuredContent.state, 'lost');
  assert.equal(lost.structuredContent.group_quiescent, null);
  assert.equal(lost.structuredContent.error.code, 'execution_indeterminate');
  assert.deepEqual(await f.start('echo $$ > original-pid; sleep 20', {}, origin), started);
  for (let i = 0; i < 3; i++) assert.equal((await f.start('sleep 20')).isError, undefined);
  assert.equal(
    (await f.start('printf must-not-run')).structuredContent.error.code,
    'process_limit_exceeded',
  );
  assert.equal(
    (await f.call('bash_terminate', { process_id: id })).structuredContent.error.code,
    'execution_indeterminate',
  );
});

test('recovery expires final output and old receipts, preserving identity without replay', async (t) => {
  const f = await fixture(t);
  const origin = context();
  const started = await f.start('printf retained-once', { yield_time_ms: 1000 }, origin);
  const id = started.structuredContent.process_id;
  await f.until(id);
  f.editStore((db) => {
    const row = JSON.parse(db.prepare('SELECT body FROM processes WHERE id=?').get(id).body);
    row.exited_at = new Date(Date.now() - 25 * 60 * 60 * 1000).toISOString();
    db.prepare('UPDATE processes SET body=? WHERE id=?').run(JSON.stringify(row), id);
    const saved = JSON.parse(
      db.prepare('SELECT body FROM operations WHERE id=?').get(origin.operation_id).body,
    );
    assert.deepEqual(saved.result.structuredContent.chunks, []);
  });
  await delay(350);
  assert.equal(
    (await f.call('bash_poll', { process_id: id })).structuredContent.error.code,
    'output_expired',
  );
  assert.equal(
    (await f.start('printf retained-once', { yield_time_ms: 1000 }, origin)).structuredContent.error
      .code,
    'output_expired',
  );
  assert.equal((await f.call('bash_list', {})).structuredContent.items.length, 1);
});

test('start effect gap recovers as unknown without another spawn', async (t) => {
  const f = await fixture(t);
  const origin = context();
  const started = await f.start('printf once', {}, origin);
  const id = started.structuredContent.process_id;
  await f.until(id);
  f.editStore((db) => {
    const record = JSON.parse(
      db.prepare('SELECT body FROM operations WHERE id=?').get(origin.operation_id).body,
    );
    record.phase = 'effect_pending';
    record.result = null;
    delete record.output_reference;
    db.prepare('UPDATE operations SET body=? WHERE id=?').run(
      JSON.stringify(record),
      origin.operation_id,
    );
  });
  const uncertain = await f.start('printf once', {}, origin);
  assert.equal(uncertain.structuredContent.error.code, 'execution_indeterminate');
  assert.equal((await f.call('bash_list', {})).structuredContent.items.length, 1);
});

test('write effect gap retains an indeterminate receipt without resending input', async (t) => {
  const f = await fixture(t);
  const started = await f.start('read line; printf "%s" "$line" >> received', { stdin_open: true });
  const id = started.structuredContent.process_id;
  const origin = context();
  const input = { process_id: id, text: 'only-once\n', close_stdin: true };
  assert.equal(
    (await f.call('bash_write', input, origin)).structuredContent.input_state,
    'accepted',
  );
  await f.until(id);
  f.editStore((db) => {
    const record = JSON.parse(
      db.prepare('SELECT body FROM operations WHERE id=?').get(origin.operation_id).body,
    );
    record.phase = 'effect_pending';
    record.result = null;
    db.prepare('UPDATE operations SET body=? WHERE id=?').run(
      JSON.stringify(record),
      origin.operation_id,
    );
  });
  const replay = (await f.call('bash_write', input, origin)).structuredContent;
  assert.equal(replay.error.code, 'execution_indeterminate');
  assert.equal(replay.process_id, id);
  assert.equal(replay.accepted_bytes, null);
  assert.equal(replay.input_state, 'indeterminate');
  assert.equal(await readFile(join(f.cwd, 'received'), 'utf8'), 'only-once');
});

test('cancellation before start has no process or command effect', async (t) => {
  const f = await fixture(t);
  const signal = AbortSignal.abort();
  const result = await f.start('touch should-not-exist', {}, context(), signal);
  assert.equal(result.structuredContent.error.code, 'cancelled');
  assert.equal((await f.call('bash_list', {})).structuredContent.items.length, 0);
  await assert.rejects(readFile(join(f.cwd, 'should-not-exist')), { code: 'ENOENT' });
});

test('schemas reject bytes, nulls, invalid combinations and forged scope before effects', async (t) => {
  const f = await fixture(t);
  for (const args of [
    { command: ' ' },
    { command: 'x', tty: true, stdin_open: false },
    { command: '你'.repeat(30000) },
    { command: 'x', cwd: null },
    { command: 'x', yield_time_ms: 0.5 },
    { command: 'x', env: {} },
  ])
    await assert.rejects(f.call('bash_start', args));
  for (const args of [
    { process_id: '011d56a4-e0f9-5d8b-8774-804db242f1cf' },
    { process_id: '011d56a4-e0f9-5d8b-8774-804db242f1cf', text: '你'.repeat(6000) },
  ])
    await assert.rejects(f.call('bash_write', args));
  const wrong = { ...context(), session_id: processId() };
  assert.equal(
    (await f.start('printf forbidden', {}, wrong)).structuredContent.error.code,
    'process_not_found',
  );
  const origin = context();
  await f.start('true', {}, origin);
  assert.equal(
    (await f.start('false', {}, origin)).structuredContent.error.code,
    'operation_conflict',
  );
});

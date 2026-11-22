import assert from 'node:assert/strict';
import { test } from 'node:test';
import { mkdtemp, mkdir, rm, symlink, link, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { ProcessStore } from '../dist/process/store.js';
import { OutputDecoder, ProcessOutput } from '../dist/process/output.js';
import { CursorCodec } from '../dist/process/identity.js';
import { StateGuard } from '../dist/state-guard.js';
import { createFileTools } from '../dist/files.js';

async function fixture(t) {
  const root = await mkdtemp(join(tmpdir(), 'builtin-store-'));
  const cwd = join(root, 'work');
  await mkdir(cwd);
  const config = {
    workspace_id: 'ws-test',
    session_id: 'sess-test',
    environment_instance_id: 'env-test',
    executor_boot_id: 'boot',
    state_directory: join(root, 'private'),
    cwd,
  };
  t.after(() => rm(root, { recursive: true, force: true }));
  return { root, cwd, config };
}

test('exclusive process store rejects a second owner, scope drift and missing initialized database', async (t) => {
  const f = await fixture(t);
  const store = new ProcessStore(f.config);
  assert.throws(() => new ProcessStore(f.config));
  store.close();
  assert.throws(() => new ProcessStore({ ...f.config, session_id: 'another' }));
  const reopened = new ProcessStore(f.config);
  reopened.close();
  await rm(join(f.config.state_directory, 'processes.sqlite'));
  assert.throws(() => new ProcessStore(f.config), /missing/);
});

test('failed transaction rolls back and poisons the open connection', async (t) => {
  const f = await fixture(t);
  const store = new ProcessStore(f.config);
  assert.throws(() =>
    store.transaction(() => {
      store.putOperation({ operation_id: 'original', phase: 'prepared' });
      throw new Error('fault before commit');
    }),
  );
  assert.throws(() => store.operation('original'));
  store.close();
  const reopened = new ProcessStore(f.config);
  assert.equal(reopened.operation('original'), undefined);
  reopened.close();
});

test('bounded raw output rotates explicitly and UTF8 decoding retains split and invalid bytes', async (t) => {
  const f = await fixture(t);
  const store = new ProcessStore(f.config);
  t.after(() => store.close());
  const decoder = new OutputDecoder();
  assert.deepEqual(decoder.write(Buffer.from([0xe4])), { text: '', replaced: false });
  assert.deepEqual(decoder.write(Buffer.from([0xbd, 0xa0])), { text: '你', replaced: false });
  assert.deepEqual(decoder.write(Buffer.from([0xff])), { text: '�', replaced: true });
  const record = {
    process_id: 'proc-test',
    created_at: new Date().toISOString(),
    state: 'exited',
    output_generation: 'generation',
    first_seq: 0,
    next_seq: 0,
    retained_bytes: 0,
    output_expired: false,
    output_closed: true,
    group_quiescent: true,
  };
  store.transaction(() => store.putProcess(record));
  const output = new ProcessOutput(store, new CursorCodec(store.key, 'test-scope'));
  const first = output.cursor(record, { seq: 0, offset: 0 });
  for (let i = 0; i < 5; i++)
    store.append(
      record,
      'stdout',
      Buffer.alloc(40, 65 + i),
      String.fromCharCode(65 + i).repeat(40),
      false,
      512,
    );
  assert.ok(record.retained_bytes <= 512);
  assert.ok(record.first_seq > 0);
  assert.throws(
    () => output.page(record, first, 1024),
    (error) =>
      error.code === 'output_cursor_expired' && Boolean(error.detail.first_available_cursor),
  );
  const page = output.page(record, undefined, 1024);
  assert.equal(page.output_truncated, true);
  assert.equal(page.output_complete, true);
  assert.equal(page.chunks.map((chunk) => chunk.text).join(''), 'D'.repeat(40) + 'E'.repeat(40));
  store.expire(record);
  assert.throws(
    () => output.page(record, page.next_cursor, 1024),
    (error) => error.code === 'output_expired',
  );
});

test('state guard rejects direct, symlink, hardlink and ancestor traversal while ordinary files work', async (t) => {
  const f = await fixture(t);
  const store = new ProcessStore(f.config);
  t.after(() => store.close());
  const guard = new StateGuard(f.config.state_directory, f.cwd);
  await guard.initialize();
  const tools = new Map(createFileTools(f.cwd, guard).map((tool) => [tool.name, tool]));
  const database = join(f.config.state_directory, 'processes.sqlite');
  await writeFile(join(f.cwd, 'ordinary'), 'safe');
  assert.ok(await tools.get('read').execute('x', { path: 'ordinary' }));
  for (const target of [database, join(f.cwd, 'alias')]) {
    if (target.endsWith('alias')) await symlink(database, target);
    await assert.rejects(tools.get('read').execute('x', { path: target }));
    await assert.rejects(tools.get('write').execute('x', { path: target, content: 'corrupt' }));
  }
  await assert.rejects(guard.arguments('grep', { path: f.root }));
  await assert.rejects(guard.arguments('find', { path: '/' }));
  await link(database, join(f.cwd, 'hardlink'));
  await assert.rejects(guard.refresh());
});

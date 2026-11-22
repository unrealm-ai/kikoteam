import assert from 'node:assert/strict';
import { test } from 'node:test';
import { mkdtemp, mkdir, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { ProcessStore } from '../dist/process/store.js';
import { DatabaseSync } from 'node:sqlite';

async function fixture(t) {
  const root = await mkdtemp(join(tmpdir(), 'builtin-activity-store-'));
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
  let store = new ProcessStore(config);
  t.after(() => {
    store.close();
    return rm(root, { recursive: true, force: true });
  });
  return {
    get store() {
      return store;
    },
    config,
    reopen(edit) {
      store.close();
      if (edit) {
        const db = new DatabaseSync(join(config.state_directory, 'processes.sqlite'));
        try {
          edit(db);
        } finally {
          db.close();
        }
      }
      store = new ProcessStore(config);
    },
  };
}

function record(id, changes = {}) {
  return {
    process_id: id,
    created_at: '2026-10-29T00:00:00.000Z',
    state: 'exited',
    group_quiescent: true,
    output_closed: true,
    ...changes,
  };
}

test('management activity summarizes every record, including lost groups and tail output', async (t) => {
  const { store } = await fixture(t);
  store.transaction(() => {
    for (let i = 0; i < 150; i++) {
      store.putProcess(record(`lost-${i}`, { state: 'lost', group_quiescent: null }));
    }
    store.putProcess(record('descendants', { group_quiescent: false }));
    store.putProcess(record('tail', { output_closed: false }));
    store.putProcess(record('quiet'));
  });
  const observed = store.activity();
  assert.equal(observed.active, 1);
  assert.equal(observed.unknown, 150);
  assert.equal(observed.pending_writes, 1);
  assert.ok(Number.isSafeInteger(observed.revision));
  assert.deepEqual(store.activity(), observed, 'management reads must not count as activity');
});

test('activity revision records a complete idle-busy-idle interval between observations', async (t) => {
  const { store } = await fixture(t);
  const initial = store.activity();
  store.transaction(() => {
    store.putProcess(record('process', { state: 'running', group_quiescent: false }));
  });
  store.transaction(() => store.putProcess(record('process')));
  const final = store.activity();
  assert.equal(final.active, 0);
  assert.equal(final.unknown, 0);
  assert.equal(final.pending_writes, 0);
  assert.ok(final.revision >= initial.revision + 2);
  assert.deepEqual(store.activity(), final);
});

test('failed activity mutation rolls back and the poisoned store cannot report idle', async (t) => {
  const f = await fixture(t);
  const { store } = f;
  const initial = store.activity();
  assert.throws(() =>
    store.transaction(() => {
      store.putProcess(record('uncommitted', { state: 'running', group_quiescent: false }));
      throw new Error('synthetic commit boundary failure');
    }),
  );
  assert.throws(() => store.activity());
  f.reopen();
  assert.deepEqual(
    f.store.activity(),
    initial,
    'revision and process write must roll back together',
  );
});

test('file-worker pending effects and uncertainty survive the management process restarting', async (t) => {
  const f = await fixture(t);
  const initial = f.store.activity();
  f.store.transaction(() => f.store.beginFileCall());
  assert.equal(f.store.activity().pending_writes, 1);
  f.reopen();
  assert.equal(f.store.activity().pending_writes, 1);
  f.store.transaction(() => f.store.recoverFileCalls());
  assert.equal(f.store.activity().pending_writes, 0);
  assert.equal(f.store.activity().unknown, 1);
  f.store.transaction(() => f.store.beginFileCall());
  f.store.transaction(() => f.store.finishFileCall(false));
  assert.equal(
    f.store.activity().unknown,
    1,
    'a later successful call cannot clear old uncertainty',
  );
  f.reopen();
  assert.equal(f.store.activity().unknown, 1);
  assert.ok(f.store.activity().revision > initial.revision);
});

test('version one migration preserves old process facts and revision exhaustion cannot wrap', async (t) => {
  const f = await fixture(t);
  f.store.transaction(() =>
    f.store.putProcess(record('old', { state: 'lost', group_quiescent: null })),
  );
  f.reopen((db) => {
    db.exec(
      "UPDATE meta SET value='1' WHERE key='version'; DELETE FROM meta WHERE key='activity_revision'",
    );
  });
  assert.equal(f.store.activity().unknown, 1);
  f.reopen((db) => {
    db.prepare('UPDATE meta SET value=? WHERE key=?').run(
      String(Number.MAX_SAFE_INTEGER),
      'activity_revision',
    );
  });
  assert.throws(() => f.store.transaction(() => f.store.putProcess(record('too-late'))));
  f.reopen();
  assert.equal(f.store.activity().revision, Number.MAX_SAFE_INTEGER);
  assert.equal(f.store.process('too-late'), undefined);
});

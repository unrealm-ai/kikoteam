import assert from 'node:assert/strict';
import { test } from 'node:test';
import { mkdtemp, readFile, writeFile, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { InMemoryTransport } from '@modelcontextprotocol/server';
import { serveStdio } from '@modelcontextprotocol/server/stdio';
import { createEditTool, createWriteTool } from '@earendil-works/pi-coding-agent';
import { createBuiltinServer } from '../dist/server.js';
import { createLogger } from '../dist/logging.js';
import { CALL_CONTEXT_KEY } from '../dist/context.js';

const envelope = {
  'io.modelcontextprotocol/protocolVersion': '2026-07-28',
  'io.modelcontextprotocol/clientCapabilities': {},
};
const context = {
  trace_id: '1234567890abcdef1234567890abcdef',
  operation_id: '0f40c4f6-cb30-563b-a607-5d25b232f7eb',
};

function deferred() {
  let resolve;
  const promise = new Promise((done) => {
    resolve = done;
  });
  return { promise, resolve };
}

async function wire(t, tools, logs, emit = (line) => logs.push(JSON.parse(line))) {
  const [client, transport] = InMemoryTransport.createLinkedPair();
  const builtin = createBuiltinServer('/workspace', createLogger('DEBUG', emit), tools);
  const handle = serveStdio(() => builtin.server, {
    transport,
    legacy: 'reject',
  });
  const pending = new Map();
  client.onmessage = (message) => {
    const waiter = pending.get(message.id);
    if (waiter) {
      pending.delete(message.id);
      waiter.resolve(message);
    }
  };
  await client.start();
  t.after(async () => {
    await handle.close();
    await builtin.drain();
  });
  async function send(id, method, params = {}) {
    const waiter = deferred();
    pending.set(id, waiter);
    await client.send({
      jsonrpc: '2.0',
      id,
      method,
      params: { ...params, _meta: { ...envelope, ...params._meta } },
    });
    return waiter.promise;
  }
  await send(0, 'server/discover');
  return { client, send };
}

test('standard cancellation does not release Pi mutation queue before an active write settles', async (t) => {
  const cwd = await mkdtemp(join(tmpdir(), 'builtin-cancel-'));
  t.after(() => rm(cwd, { recursive: true, force: true }));
  const entered = deferred();
  const release = deferred();
  const cancelled = deferred();
  let writes = 0;
  const logs = [];
  const tool = createWriteTool(cwd, {
    operations: {
      mkdir: async () => {},
      writeFile: async (path, content) => {
        writes++;
        if (writes === 1) {
          entered.resolve();
          await release.promise;
        }
        await writeFile(path, content);
      },
    },
  });
  const [client, transport] = InMemoryTransport.createLinkedPair();
  const builtin = createBuiltinServer(
    cwd,
    createLogger('DEBUG', (line) => {
      const record = JSON.parse(line);
      logs.push(record);
      if (record.outcome === 'cancelled') cancelled.resolve();
    }),
    [tool],
  );
  const handle = serveStdio(() => builtin.server, {
    transport,
    legacy: 'reject',
  });
  t.after(async () => {
    release.resolve();
    await handle.close();
    await builtin.drain();
  });
  const second = deferred();
  client.onmessage = (message) => {
    if (message.id === 2) second.resolve(message);
  };
  await client.start();
  const call = (id, content) =>
    client.send({
      jsonrpc: '2.0',
      id,
      method: 'tools/call',
      params: {
        name: 'write',
        arguments: { path: 'same', content },
        _meta: { ...envelope, [CALL_CONTEXT_KEY]: context },
      },
    });
  await call(1, 'first');
  await entered.promise;
  await call(2, 'second');
  await client.send({
    jsonrpc: '2.0',
    method: 'notifications/cancelled',
    params: { requestId: 1, reason: 'test', _meta: envelope },
  });
  await new Promise((resolve) => setImmediate(resolve));
  assert.equal(writes, 1);
  release.resolve();
  const response = await second.promise;
  await cancelled.promise;
  assert.equal(response.result.isError, undefined);
  assert.equal(writes, 2);
  assert.equal(await readFile(join(cwd, 'same'), 'utf8'), 'second');
  assert.ok(
    logs.every(
      (record) =>
        record.trace_id === context.trace_id && record.operation_id === context.operation_id,
    ),
  );
});

test('invalid schema/context never reaches native effects; business errors retain isError', async (t) => {
  const logs = [];
  let effects = 0;
  const tool = createWriteTool('/workspace', {
    operations: {
      mkdir: async () => {},
      writeFile: async () => {
        effects++;
        throw new Error('Synthetic expected failure');
      },
    },
  });
  const { send } = await wire(t, [tool], logs);
  const args = { name: 'write', arguments: { path: 'a', content: 'b' } };
  const malformed = await send(1, 'tools/call', {
    ...args,
    arguments: { path: 'a', content: 9 },
  });
  assert.equal(malformed.error.code, -32602);
  const badContext = await send(2, 'tools/call', {
    ...args,
    _meta: {
      [CALL_CONTEXT_KEY]: { ...context, trace_id: 'secret-invalid-trace' },
    },
  });
  assert.equal(badContext.error.code, -32602);
  assert.equal(effects, 0);
  const failed = await send(3, 'tools/call', {
    ...args,
    _meta: { [CALL_CONTEXT_KEY]: context },
  });
  assert.equal(failed.result.isError, true);
  assert.equal(effects, 1);
  assert.ok(!JSON.stringify(logs).includes('Synthetic expected failure'));
  assert.ok(!JSON.stringify(logs).includes('secret-invalid-trace'));
});

test('capacity rejection does not execute or replay a ninth call', async (t) => {
  const release = deferred();
  const entered = deferred();
  let count = 0;
  const tool = createWriteTool('/workspace', {
    operations: {
      mkdir: async () => {},
      writeFile: async () => {
        count++;
        if (count === 8) entered.resolve();
        await release.promise;
      },
    },
  });
  t.after(() => release.resolve());
  const { send } = await wire(t, [tool], []);
  const calls = Array.from({ length: 8 }, (_, i) =>
    send(i + 1, 'tools/call', {
      name: 'write',
      arguments: { path: `file-${i}`, content: 'x' },
    }),
  );
  await entered.promise;
  const rejected = await send(9, 'tools/call', {
    name: 'write',
    arguments: { path: 'rejected', content: 'x' },
  });
  assert.equal(rejected.result.isError, true);
  assert.equal(count, 8);
  release.resolve();
  assert.ok((await Promise.all(calls)).every((message) => !message.result.isError));
  assert.equal(count, 8);
});

test('broken stderr preserves a successful native file result and reports recovery once', async (t) => {
  const cwd = await mkdtemp(join(tmpdir(), 'builtin-log-failure-'));
  t.after(() => rm(cwd, { recursive: true, force: true }));
  const logs = [];
  let broken = true;
  const { send } = await wire(t, [createWriteTool(cwd)], logs, (line) => {
    const record = JSON.parse(line);
    if (broken && record.event === 'tool.completed')
      throw Object.assign(new Error('synthetic-secret-EPIPE'), { code: 'EPIPE' });
    logs.push(record);
  });
  const request = { name: 'write', arguments: { path: 'real-file', content: 'persisted' } };
  assert.equal((await send(1, 'tools/call', request)).result.isError, undefined);
  assert.equal(await readFile(join(cwd, 'real-file'), 'utf8'), 'persisted');
  broken = false;
  assert.equal((await send(2, 'tools/call', request)).result.isError, undefined);
  const recovered = logs.filter((line) => line.event === 'log.output_recovered');
  assert.equal(recovered.length, 1);
  assert.equal(recovered[0].dropped_records, 1);
  assert.ok(!JSON.stringify(logs).includes('synthetic-secret-EPIPE'));
});

test('disk failure has ERROR classification without raw filesystem messages', async (t) => {
  const logs = [];
  const tool = createWriteTool('/workspace', {
    operations: {
      mkdir: async () => {},
      writeFile: async () => {
        throw Object.assign(new Error('synthetic-private-path-disk-full'), { code: 'ENOSPC' });
      },
    },
  });
  const { send } = await wire(t, [tool], logs);
  const result = await send(1, 'tools/call', {
    name: 'write',
    arguments: { path: 'any', content: 'x' },
  });
  assert.equal(result.result.isError, true);
  const completed = logs.find((entry) => entry.event === 'tool.completed');
  assert.equal(completed.level, 'ERROR');
  assert.equal(completed.error_code, 'filesystem_failure');
  assert.ok(!JSON.stringify(logs).includes('synthetic-private-path-disk-full'));
});

test('native edit mismatch and wrapped errno retain expected severity without leaking paths', async (t) => {
  for (const [code, level, errorCode] of [
    [undefined, 'INFO', 'invalid_edit'],
    ['ENOENT', 'INFO', 'file_unavailable'],
    ['ENOSPC', 'ERROR', 'filesystem_failure'],
    ['UNKNOWN', 'ERROR', 'tool_execution_failed'],
  ]) {
    const logs = [];
    let writes = 0;
    const tool = createEditTool('/workspace', {
      operations: {
        access: async () => {
          if (code) throw Object.assign(new Error('private-access-error'), { code });
        },
        readFile: async () => Buffer.from('original text'),
        writeFile: async () => {
          writes++;
        },
      },
    });
    const { send } = await wire(t, [tool], logs);
    const response = await send(1, 'tools/call', {
      name: 'edit',
      arguments: {
        path: 'private-file-path',
        edits: [{ oldText: 'absent text', newText: 'replacement' }],
      },
    });
    assert.equal(response.result.isError, true);
    assert.equal(writes, 0);
    const completed = logs.find((entry) => entry.event === 'tool.completed');
    assert.equal(completed.level, level);
    assert.equal(completed.error_code, errorCode);
    assert.ok(!JSON.stringify(logs).includes('private-file-path'));
    assert.ok(!JSON.stringify(logs).includes('private-access-error'));
  }
});

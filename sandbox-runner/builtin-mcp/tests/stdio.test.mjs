import assert from 'node:assert/strict';
import { test } from 'node:test';
import { execFile, spawn } from 'node:child_process';
import { mkdtemp, mkdir, readFile, writeFile, rm, symlink } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { promisify } from 'node:util';
import { Client } from '@modelcontextprotocol/client';
import { StdioClientTransport } from '@modelcontextprotocol/client/stdio';
import {
  createReadTool,
  createWriteTool,
  createEditTool,
  createGrepTool,
  createFindTool,
  createLsTool,
} from '@earendil-works/pi-coding-agent';
import { FILE_BYTES, IMAGE_BYTES } from '../dist/files.js';
import { toMcpResult } from '../dist/results.js';

const main = fileURLToPath(new URL('../dist/main.js', import.meta.url));
const factories = [
  createReadTool,
  createWriteTool,
  createEditTool,
  createGrepTool,
  createFindTool,
  createLsTool,
];
const version = '2026-07-28';
process.env.PI_OFFLINE = '1';

async function fixture(t) {
  const cwd = await mkdtemp(join(tmpdir(), 'builtin-builtin-test-'));
  const transport = new StdioClientTransport({
    command: process.execPath,
    args: ['--max-old-space-size=512', main, '--cwd', cwd],
    env: { PATH: process.env.PATH, HOME: cwd, BUILTIN_MCP_LOG_LEVEL: 'DEBUG' },
    stderr: 'pipe',
    maxBufferSize: 20 * 1024 * 1024,
  });
  const messages = [];
  const send = transport.send.bind(transport);
  transport.send = async (message) => {
    messages.push(message);
    await send(message);
  };
  let stderr = '';
  transport.stderr.on('data', (chunk) => {
    stderr += chunk.toString();
  });
  const client = new Client(
    { name: 'builtin-builtin-test', version: '1.0.0' },
    {
      capabilities: {},
      versionNegotiation: {
        mode: { pin: version },
        probe: { timeoutMs: 10000, maxRetries: 0 },
      },
      inputRequired: { autoFulfill: false },
    },
  );
  t.after(async () => {
    await client.close();
    await rm(cwd, { recursive: true, force: true });
  });
  await client.connect(transport, { timeout: 10000 });
  return {
    cwd,
    client,
    messages,
    logs: () =>
      stderr
        .split('\n')
        .filter(Boolean)
        .map((line) => JSON.parse(line)),
    call: async (name, args, options) => {
      const { _meta, ...result } = await client.callTool(
        { name, arguments: args },
        {
          timeout: 15000,
          ...options,
        },
      );
      assert.deepEqual(_meta, {
        'io.modelcontextprotocol/serverInfo': {
          name: 'builtin-builtin',
          version: '0.1.0',
        },
      });
      return result;
    },
  };
}

test('modern stdio discovers exactly six native schemas; no host/bash/private tools', async (t) => {
  const f = await fixture(t);
  const { tools } = await f.client.listTools();
  const originals = factories.map((factory) => factory(f.cwd));
  assert.deepEqual(tools.map((x) => x.name).sort(), originals.map((x) => x.name).sort());
  for (const original of originals) {
    const tool = tools.find((x) => x.name === original.name);
    assert.deepEqual(tool.inputSchema, JSON.parse(JSON.stringify(original.parameters)));
    assert.equal(tool.description, original.description);
    assert.equal(tool.outputSchema, undefined);
  }
  assert.ok(
    f.messages.some(
      (x) =>
        x.method === 'tools/list' &&
        x.params._meta['io.modelcontextprotocol/protocolVersion'] === version,
    ),
  );
  assert.ok(!f.messages.some((x) => x.method === 'initialize'));
});

test('write, read and multi-edit execute through a real child and preserve Pi results', async (t) => {
  const f = await fixture(t);
  const path = 'nested/sample.txt';
  const contents = '\ufeffone\r\ntwo\r\nthree\r\n';
  const written = await f.call('write', { path, content: contents });
  assert.deepEqual(
    written,
    toMcpResult(
      await createWriteTool(f.cwd).execute('native', {
        path,
        content: contents,
      }),
    ),
  );
  assert.deepEqual(
    await f.call('read', { path, offset: 2, limit: 1 }),
    toMcpResult(
      await createReadTool(f.cwd).execute('native', {
        path,
        offset: 2,
        limit: 1,
      }),
    ),
  );
  const edits = [
    { oldText: 'one', newText: 'ONE' },
    { oldText: 'three', newText: 'THREE' },
  ];
  const actual = await f.call('edit', { path, edits });
  const bytes = await readFile(join(f.cwd, path));
  await writeFile(join(f.cwd, path), contents);
  const expected = await createEditTool(f.cwd).execute('native', {
    path,
    edits,
  });
  assert.deepEqual(actual, toMcpResult(expected));
  assert.deepEqual(await readFile(join(f.cwd, path)), bytes);
  assert.equal(bytes.toString(), '\ufeffONE\r\ntwo\r\nTHREE\r\n');
});

test('grep, find and ls retain native ordering, ignore and hidden-file behavior', async (t) => {
  const f = await fixture(t);
  await mkdir(join(f.cwd, '.git'));
  await writeFile(join(f.cwd, '.gitignore'), 'ignored.txt\n');
  for (const name of ['a.txt', 'b.txt', 'ignored.txt', '.hidden.txt'])
    await writeFile(join(f.cwd, name), 'needle\nother\n');
  for (const [name, factory, args] of [
    ['grep', createGrepTool, { pattern: 'NEEDLE', ignoreCase: true, context: 1 }],
    ['find', createFindTool, { pattern: '*.txt' }],
    ['ls', createLsTool, { path: '.', limit: 3 }],
  ]) {
    const actual = await f.call(name, args);
    const expected = toMcpResult(await factory(f.cwd).execute('native', args));
    // ripgrep traverses in parallel; its native cross-file order is unspecified.
    if (name === 'grep') {
      actual.content[0].text = actual.content[0].text.split('\n').sort().join('\n');
      expected.content[0].text = expected.content[0].text.split('\n').sort().join('\n');
    }
    assert.deepEqual(actual, expected);
  }
});

test('text truncation and native image content survive MCP wrapping', async (t) => {
  const f = await fixture(t);
  await writeFile(
    join(f.cwd, 'large.txt'),
    Array.from({ length: 2100 }, (_, i) => `line ${i}`).join('\n'),
  );
  const png = Buffer.from(
    'iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAIAAACQd1PeAAAADElEQVR4nGP4z8AAAAMBAQDJ/pLvAAAAAElFTkSuQmCC',
    'base64',
  );
  await writeFile(join(f.cwd, 'pixel.png'), png);
  for (const path of ['large.txt', 'pixel.png']) {
    const actual = await f.call('read', { path });
    const native = toMcpResult(await createReadTool(f.cwd).execute('native', { path }));
    assert.deepEqual(actual, native);
    if (path.endsWith('.png')) assert.ok(actual.content.some((x) => x.type === 'image'));
    else {
      assert.equal(actual.structuredContent.details.truncation.truncated, true);
      assert.equal('content' in actual.structuredContent.details.truncation, false);
      assert.ok(!JSON.stringify(actual.structuredContent).includes('line 0'));
    }
  }
});

test('read uses the literal attachment name even when an interactive alias exists', async (t) => {
  const f = await fixture(t);
  await mkdir(join(f.cwd, 'uploads'));
  for (const [path, alias] of [
    ['@预算.csv', '预算.csv'],
    ['uploads/地区　汇总.csv', 'uploads/地区 汇总.csv'],
  ]) {
    await writeFile(join(f.cwd, alias), 'other,184');
    await writeFile(join(f.cwd, path), 'selected,731');
    const result = await f.call('read', { path });
    assert.equal(result.content[0].text, 'selected,731');
    await rm(join(f.cwd, path));
    assert.equal((await f.call('read', { path })).isError, true);
  }
});

test('invalid edit leaves bytes unchanged; schema errors do not execute writes', async (t) => {
  const f = await fixture(t);
  const path = 'repeat.txt';
  await writeFile(join(f.cwd, path), 'same same\n');
  const result = await f.call('edit', {
    path,
    edits: [{ oldText: 'same', newText: 'changed' }],
  });
  assert.equal(result.isError, true);
  assert.equal(await readFile(join(f.cwd, path), 'utf8'), 'same same\n');
  await assert.rejects(f.call('write', { path, content: 7 }));
  assert.equal(await readFile(join(f.cwd, path), 'utf8'), 'same same\n');
  await assert.rejects(f.call('bash', { command: 'echo unregistered' }));
});

test('special and oversized files fail promptly; normal symlinks retain native semantics', async (t) => {
  const f = await fixture(t);
  await promisify(execFile)('mkfifo', [join(f.cwd, 'pipe')]);
  for (const [name, args] of [
    ['read', { path: 'pipe' }],
    ['write', { path: 'pipe', content: 'x' }],
    ['grep', { pattern: 'x', path: 'pipe' }],
  ]) {
    assert.equal((await f.call(name, args)).isError, true);
  }
  await writeFile(join(f.cwd, 'oversized'), Buffer.alloc(FILE_BYTES + 1));
  assert.equal((await f.call('read', { path: 'oversized' })).isError, true);
  const hugePng = Buffer.alloc(IMAGE_BYTES + 1);
  Buffer.from('89504e470d0a1a0a0000000d49484452', 'hex').copy(hugePng);
  await writeFile(join(f.cwd, 'oversized.png'), hugePng);
  assert.equal((await f.call('read', { path: 'oversized.png' })).isError, true);
  const pixelBomb = Buffer.from(
    'iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAIAAACQd1PeAAAADElEQVR4nGP4z8AAAAMBAQDJ/pLvAAAAAElFTkSuQmCC',
    'base64',
  );
  pixelBomb.writeUInt32BE(100000, 16);
  pixelBomb.writeUInt32BE(100000, 20);
  await writeFile(join(f.cwd, 'pixels.png'), pixelBomb);
  const imageError = await f.call('read', { path: 'pixels.png' });
  assert.equal(imageError.isError, true);
  assert.match(imageError.content[0].text, /pixel limit/);
  // Native directory traversal must skip special contents without hanging.
  const directorySearch = await f.call('grep', { path: '.', pattern: 'not-present' });
  assert.equal(directorySearch.isError, undefined, JSON.stringify(directorySearch));
  const directoryFind = await f.call('find', { path: '.', pattern: '*' });
  assert.equal(directoryFind.isError, undefined, JSON.stringify(directoryFind));
  await writeFile(join(f.cwd, 'target'), 'before');
  await symlink('target', join(f.cwd, 'link'));
  assert.equal((await f.call('write', { path: 'link', content: 'after' })).isError, undefined);
  assert.equal(await readFile(join(f.cwd, 'target'), 'utf8'), 'after');
});

test('parallel writes and edits use Pi mutation queue; logs never contain arguments or contents', async (t) => {
  const f = await fixture(t);
  const secret = 'synthetic-private-file-content-DO-NOT-LOG';
  await f.call('write', { path: 'shared.txt', content: secret });
  const results = await Promise.all([
    f.call('edit', {
      path: 'shared.txt',
      edits: [{ oldText: secret, newText: 'first' }],
    }),
    f.call('edit', {
      path: 'shared.txt',
      edits: [{ oldText: 'first', newText: 'second' }],
    }),
  ]);
  assert.ok(results.every((x) => !x.isError));
  assert.equal(await readFile(join(f.cwd, 'shared.txt'), 'utf8'), 'second');
  await f.call('read', { path: 'nonexistent-secret-filename' });
  const logs = f.logs();
  assert.ok(logs.some((x) => x.event === 'tool.completed' && x.outcome === 'succeeded'));
  assert.ok(logs.every((x) => x.service === 'builtin-mcp' && x.instance_id && x.time && x.module));
  assert.ok(!JSON.stringify(logs).includes(secret));
  assert.ok(!JSON.stringify(logs).includes('nonexistent-secret-filename'));
});

test('legacy handshake is rejected; EOF shuts down without protocol stdout noise', async () => {
  const cwd = await mkdtemp(join(tmpdir(), 'builtin-builtin-legacy-'));
  try {
    const child = spawn(process.execPath, [main, '--cwd', cwd], {
      env: { PATH: process.env.PATH, HOME: cwd },
      stdio: ['pipe', 'pipe', 'pipe'],
    });
    let stdout = '';
    let stderr = '';
    child.stdout.on('data', (chunk) => {
      stdout += chunk.toString();
    });
    child.stderr.on('data', (chunk) => {
      stderr += chunk.toString();
    });
    const exit = new Promise((resolve, reject) => {
      child.once('error', reject);
      child.once('exit', (code, signal) => resolve({ code, signal }));
    });
    child.stdin.write(
      JSON.stringify({
        jsonrpc: '2.0',
        id: 1,
        method: 'initialize',
        params: {
          protocolVersion: '2025-11-25',
          capabilities: {},
          clientInfo: { name: 'old', version: '1' },
        },
      }) + '\n',
    );
    await new Promise((resolve) => child.stdout.once('data', resolve));
    child.stdin.write(
      JSON.stringify({
        jsonrpc: '2.0',
        id: 2,
        method: 'server/discover',
        params: {
          _meta: {
            'io.modelcontextprotocol/protocolVersion': version,
            'io.modelcontextprotocol/clientCapabilities': {},
          },
        },
      }) + '\n',
    );
    await new Promise((resolve) => child.stdout.once('data', resolve));
    child.stdin.end();
    assert.deepEqual(await exit, { code: 0, signal: null });
    const responses = stdout
      .trim()
      .split('\n')
      .map((line) => JSON.parse(line));
    assert.ok(responses[0].error);
    assert.deepEqual(responses[1].result.supportedVersions, [version]);
    assert.ok(
      stderr
        .split('\n')
        .filter(Boolean)
        .every((line) => JSON.parse(line).service === 'builtin-mcp'),
    );
  } finally {
    await rm(cwd, { recursive: true, force: true });
  }
});

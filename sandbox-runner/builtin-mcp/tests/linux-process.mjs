import assert from 'node:assert/strict';
import { test } from 'node:test';
import { chmod, mkdir, mkdtemp, readFile, rm, stat, writeFile } from 'node:fs/promises';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { setTimeout as delay } from 'node:timers/promises';
import { Client } from '@modelcontextprotocol/client';
import { StdioClientTransport } from '@modelcontextprotocol/client/stdio';
import { processId } from '../dist/process/identity.js';

assert.ok(
  ['linux', 'darwin'].includes(process.platform),
  'Process acceptance requires macOS or Linux',
);
const quote = (value) => `'${value.replaceAll("'", "'\\''")}'`;
const main = fileURLToPath(new URL('../dist/main.js', import.meta.url));

async function fixture(t) {
  const root = await mkdtemp('/tmp/builtin-linux-');
  await chmod(root, 0o755);
  const cwd = join(root, 'work');
  await mkdir(cwd);
  const state = join(root, 'private');
  const marker = join(cwd, 'must-not-source');
  const profile = join(cwd, 'bash-env');
  await writeFile(profile, `touch ${quote(marker)}`);
  const transport = new StdioClientTransport({
    command: process.execPath,
    args: [
      '--disable-warning=ExperimentalWarning',
      main,
      '--cwd',
      cwd,
      '--workspace-id',
      '3f716000-07b4-5252-9e2a-7da0bbdc5c8a',
      '--session-id',
      '921130c5-ba8b-5733-8fa6-66a9bbc776b3',
      '--sandbox-instance-id',
      'ceb40783-4b1b-5ed9-9b10-7b3711def584',
      '--runner-boot-id',
      'linux-acceptance',
      '--state-dir',
      state,
    ],
    env: {
      PATH: process.env.PATH,
      HOME: cwd,
      BUILTIN_MCP_LOG_LEVEL: 'DEBUG',
      BUILTIN_MCP_INTERNAL_SECRET: 'synthetic-management-only',
      BASH_ENV: profile,
    },
    stderr: 'pipe',
  });
  let logs = '';
  transport.stderr.on('data', (bytes) => {
    logs += bytes.toString();
  });
  const client = new Client(
    { name: 'linux-process', version: '1' },
    {
      capabilities: {},
      versionNegotiation: { mode: { pin: '2026-07-28' } },
      inputRequired: { autoFulfill: false },
    },
  );
  t.after(async () => {
    await client.close();
    await rm(root, { recursive: true, force: true });
  });
  try {
    await client.connect(transport, { timeout: 15000 });
  } catch (error) {
    assert.fail(`${error.message}\n${logs}`);
  }
  const call = (name, args) =>
    client.callTool(
      {
        name,
        arguments: args,
        _meta: {
          'builtin-mcp/call': {
            workspace_id: '3f716000-07b4-5252-9e2a-7da0bbdc5c8a',
            session_id: '921130c5-ba8b-5733-8fa6-66a9bbc776b3',
            runtime_id: '53b43a70-4eb0-5ece-b3a2-70d9cb273366',
            origin_turn_id: '6d36e372-640e-5885-8827-b32b3575585f',
            operation_id: processId(),
            trace_id: '1234567890abcdef1234567890abcdef',
          },
        },
      },
      { timeout: 20000 },
    );
  const until = async (id) => {
    let value;
    for (let i = 0; i < 200; i++) {
      value = (await call('bash_poll', { process_id: id, yield_time_ms: 50 })).structuredContent;
      if (value.output_complete) return value;
      await delay(50);
    }
    assert.fail(JSON.stringify(value));
  };
  return { cwd, root, state, marker, call, until, logs: () => logs, client };
}

test('production entry exposes eleven tools and inherits the Runner UID/GID', async (t) => {
  const f = await fixture(t);
  assert.equal((await f.client.listTools()).tools.length, 11);
  await f.call('write', { path: 'worker-owned', content: 'owned by business' });
  const file = await stat(join(f.cwd, 'worker-owned'));
  assert.equal(file.uid, process.getuid());
  // macOS creates files with the containing directory's group, which may
  // differ from the process's primary group. Process identity is checked below.
  assert.equal(file.gid, (await stat(f.cwd)).gid);
  // Compare the same API in the parent and actual Bash child. On macOS,
  // `id -G` can report extra groups absent from Node process.getgroups().
  // Keep exact supplementary-group equality rather than ignoring extra groups.
  const identitySnapshot = () => ({
    uid: process.getuid(),
    gid: process.getgid(),
    euid: process.geteuid(),
    egid: process.getegid(),
    groups: process.getgroups().sort((a, b) => a - b),
  });
  const identityScript = `console.log(JSON.stringify((${identitySnapshot.toString()})()))`;
  const started = await f.call('bash_start', {
    command: `${quote(process.execPath)} -e ${quote(identityScript)} && env && touch bash-owned`,
    cwd: f.cwd,
    yield_time_ms: 1000,
  });
  assert.equal(started.isError, undefined, JSON.stringify(started));
  const done = await f.until(started.structuredContent.process_id);
  assert.equal(done.exit_code, 0);
  const text = done.chunks.map((chunk) => chunk.text).join('');
  assert.deepEqual(JSON.parse(text.split('\n')[0]), identitySnapshot());
  assert.ok(!text.includes('synthetic-management-only'));
  assert.ok(!text.includes('BASH_ENV='));
  assert.equal((await stat(join(f.cwd, 'bash-owned'))).uid, process.getuid());
  await assert.rejects(stat(f.marker));
  assert.ok(!f.logs().includes('synthetic-management-only'));
});

test('file-tool guards remain while same-UID Bash can access account-owned state', async (t) => {
  const f = await fixture(t);
  const database = join(f.state, 'processes.sqlite');
  const result = await f.call('bash_start', {
    command: `test -r ${quote(database)} && test -w ${quote(database)}`,
    cwd: f.cwd,
    yield_time_ms: 1000,
  });
  const done = await f.until(result.structuredContent.process_id);
  assert.equal(done.exit_code, 0);
  assert.equal((await f.call('read', { path: database })).isError, true);
  assert.equal((await f.call('write', { path: database, content: 'corrupt' })).isError, true);
  assert.equal((await stat(database)).uid, process.getuid());
  assert.equal((await stat(database)).mode & 0o777, 0o600);
  assert.ok((await readFile(database)).subarray(0, 16).equals(Buffer.from('SQLite format 3\0')));
  assert.equal((await f.call('bash_list', {})).isError, undefined);
});

test('Linux PTY preserves descendant tail and force has real wait evidence', async (t) => {
  const f = await fixture(t);
  const tail = await f.call('bash_start', {
    command: 'test -t 0; id -u; trap "" HUP; (sleep 1; printf late-linux-pty) & exit 0',
    tty: true,
    cwd: f.cwd,
    yield_time_ms: 0,
  });
  assert.equal(tail.isError, undefined, JSON.stringify(tail));
  const done = await f.until(tail.structuredContent.process_id);
  assert.match(
    done.chunks.map((c) => c.text).join(''),
    new RegExp(`${process.getuid()}[\r\n]+late-linux-pty`),
  );
  const live = await f.call('bash_start', {
    command: 'trap "" TERM; while :; do sleep .1; done',
    tty: true,
    cwd: f.cwd,
    yield_time_ms: 0,
  });
  await f.call('bash_terminate', { process_id: live.structuredContent.process_id, mode: 'force' });
  assert.equal((await f.until(live.structuredContent.process_id)).termination_signal, 'SIGKILL');
});

test('force still kills a TERM-ignoring member with newline and parentheses in comm', async (t) => {
  const f = await fixture(t);
  const rename =
    process.platform === 'darwin'
      ? 'ctypes.CDLL(None).pthread_setname_np(b"odd\\n)name")'
      : 'ctypes.CDLL(None).prctl(15,b"odd\\n)name",0,0,0)';
  const script = `import ctypes,signal,time; ${rename}; signal.signal(signal.SIGTERM,signal.SIG_IGN); print("renamed",flush=True); time.sleep(60)`;
  const started = await f.call('bash_start', {
    command: `exec python3 -c ${quote(script)}`,
    cwd: f.cwd,
    yield_time_ms: 1000,
  });
  assert.equal(started.isError, undefined, JSON.stringify(started));
  assert.match(started.structuredContent.chunks.map((c) => c.text).join(''), /renamed/);
  const stopped = await f.call('bash_terminate', {
    process_id: started.structuredContent.process_id,
    mode: 'force',
  });
  assert.equal(stopped.isError, undefined, JSON.stringify(stopped));
  const done = await f.until(started.structuredContent.process_id);
  assert.equal(done.termination_signal, 'SIGKILL');
  assert.equal(done.group_quiescent, true);
});

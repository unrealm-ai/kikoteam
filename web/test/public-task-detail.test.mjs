import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
import { Window } from 'happy-dom';
import { parse, compileScript } from '@vue/compiler-sfc';
import esbuild from 'esbuild';

const window = new Window({ url: 'https://example.com/tasks/synthetic-token' });
for (const key of ['window', 'document', 'location', 'navigator', 'HTMLElement', 'Element', 'SVGElement', 'Node', 'Text', 'Comment', 'Image', 'InputEvent', 'MouseEvent', 'KeyboardEvent', 'HTMLInputElement', 'Document', 'MutationObserver', 'ResizeObserver', 'getComputedStyle', 'requestAnimationFrame', 'cancelAnimationFrame']) {
  const value = key === 'window' ? window : window[key];
  if (value !== undefined) Object.defineProperty(globalThis, key, { configurable: true, writable: true, value: typeof value === 'function' && ['getComputedStyle', 'requestAnimationFrame', 'cancelAnimationFrame'].includes(key) ? value.bind(window) : value });
}
const built = await esbuild.build({
  stdin: { contents: `
    export {default as App} from './src/App.vue';
    export {default as Settings} from './src/direction2/views/PlatformTaskLinkSettings.vue';
    export {publicTaskToken} from './src/direction2/api/public-task';
    export {createApp,h,nextTick} from 'vue';
  `, resolveDir: process.cwd(), loader: 'ts' },
  bundle: true, write: false, format: 'esm', platform: 'node', loader: {'.css':'empty'},
  plugins: [{ name: 'vue', setup(build) {
    build.onLoad({ filter: /\.vue$/ }, async ({path}) => ({
      contents: compileScript(parse(await fs.readFile(path, 'utf8')).descriptor, {id:path, inlineTemplate:true}).content, loader:'ts',
    }));
  } }],
});
const { App, Settings, publicTaskToken, createApp, h, nextTick } = await import(`data:text/javascript;base64,${Buffer.from(built.outputFiles[0].text).toString('base64')}`);
const pause = async (ms = 40) => { await new Promise(r => setTimeout(r, ms)); await nextTick(); };
const button = (host, name) => [...host.querySelectorAll('button')].find(b => b.textContent.trim() === name);
const token = 'synthetic-token';
const path = '/api/v1/public/task-detail';
function detail(overrides = {}) {
  return {
    title: '同一任务', status: 'running', permission: 'read', can_append: false, can_stop: false, can_continue: false,
    cursor: 'current', activities: [{kind:'output', text:'## 最新可见结果', occurred_at:'2026-11-24T00:00:00Z'}],
    artifacts: [], activity_truncated: false, ...overrides,
  };
}

async function fixture({initial, write, streamResponse, snapshotResponse} = {}) {
  let state = initial || detail(), stream;
  const calls = [];
  globalThis.fetch = async (url, init = {}) => {
    calls.push({url, init, body: init.body ? JSON.parse(init.body) : null});
    if (url === path + '/events') {
      const override = streamResponse?.(init);
      if (override) return override;
      const body = new ReadableStream({ start(controller) {
        stream = controller;
        init.signal.addEventListener('abort', () => {
          try { controller.close(); } catch { /* closed independently */ }
        }, {once:true});
      } });
      return new Response(body, {headers:{'Content-Type':'text/event-stream'}});
    }
    if (init.method === 'POST') return write ? write(url, init) : Response.json(state);
    if (url === path) return Response.json(snapshotResponse?.() || state);
    throw new Error('Unexpected request: ' + url);
  };
  const host = document.createElement('div');
  document.body.append(host);
  const app = createApp({ render: () => h(App) }); app.mount(host);
  await pause();
  return {
    host, calls,
    setState(next) { state = next; },
    emit(event, data, id = '') {
      const lines = `${id ? 'id: '+id+'\r\n' : ''}event: ${event}\r\ndata: ${JSON.stringify(data)}\r\n\r\n`;
      const bytes = new TextEncoder().encode(lines);
      stream.enqueue(bytes.slice(0, 13)); stream.enqueue(bytes.slice(13));
    },
    breakStream() { stream.close(); },
    cleanup() { app.unmount(); host.remove(); },
  };
}

function enterText(host, value) {
  const input = host.querySelector('textarea');
  assert.ok(input);
  input.value = value;
  input.dispatchEvent(new window.Event('input', {bubbles:true}));
  return input;
}

test('public route extracts only the bearer path and shares the actual read-only detail shell', async () => {
  assert.equal(publicTaskToken('/tasks/synthetic-token'), token);
  for (const p of ['/tasks/', '/task/synthetic-token', '/tasks/token/extra', '/tasks/token%2Fother']) assert.equal(publicTaskToken(p), null);
  window.localStorage.setItem('kiko:d2:auth-token', 'unrelated-login');
  const m = await fixture({initial:detail({activity_truncated:true})});
  try {
    assert.match(m.host.textContent, /仅显示近期执行记录/);
    assert.ok(m.host.querySelector('.task-detail-panel .session-panel'));
    assert.ok(m.host.querySelector('h2, h3'));
    assert.match(m.host.textContent, /最新可见结果/);
    assert.match(m.host.textContent, /只读/);
    assert.equal(m.host.querySelector('textarea'), null);
    for (const name of ['发送', '停止此任务', '提交并重新开始', '归档任务', '撤回此输入', '修改标题']) assert.equal(button(m.host, name), undefined);
    for (const c of m.calls) {
      assert.equal(c.init.headers.get('Authorization'), 'Bearer ' + token);
      assert.equal(c.init.credentials, 'omit');
      assert.equal(c.init.referrerPolicy, 'no-referrer');
      assert.doesNotMatch(c.url, /synthetic-token|workspaces/);
    }
    assert.equal(m.calls[1].init.headers.get('Last-Event-ID'), 'current');
  } finally { m.cleanup(); }
});

test('a confirmed public SSE cursor is used on resume, and revoke clears the shared view', async () => {
  const m = await fixture();
  try {
    const next = detail({cursor:'next', activities:[{kind:'output', text:'订阅中的结果', occurred_at:'2026-11-24T00:00:00Z'}]});
    m.emit('task.updated', next, 'next'); await pause();
    assert.match(m.host.textContent, /订阅中的结果/);
    m.breakStream(); await pause(1130);
    const streams = m.calls.filter(c => c.url.endsWith('/events'));
    assert.ok(streams.length >= 2);
    assert.equal(streams.at(-1).init.headers.get('Last-Event-ID'), 'next');
    m.emit('stream.error', {error:{code:'task_detail_link_unavailable', message:'revoked'}});
    await pause();
    assert.match(m.host.textContent, /无效、已过期或已撤销/);
    assert.doesNotMatch(m.host.textContent, /订阅中的结果|同一任务/);
  } finally { m.cleanup(); }
});

test('public write submits steer normally, queues when held, and requires a click for explicit continue', async () => {
  const initial = detail({permission:'write', can_append:true, can_stop:true});
  const m = await fixture({initial, write:() => Response.json({...initial, input_receipt:{state:'queued'}})});
  try {
    assert.ok(button(m.host, '停止此任务'));
    enterText(m.host, '继续调查'); await nextTick();
    button(m.host, '发送').click(); await pause();
    assert.deepEqual(m.calls.find(c => c.url === path + '/append').body, {text:'继续调查', delivery_mode:'steer'});

    const held = detail({cursor:'held', status:'waiting_for_action', permission:'write', can_append:true,
      activities:[{kind:'input_queued', text:'旧补充', occurred_at:'2026-11-24T00:00:00Z'}]});
    m.setState(held); m.emit('task.updated', held, 'held'); await pause();
    assert.match(m.host.querySelector('.event.user').textContent, /任务输入.*旧补充.*已排队/);
    assert.equal(button(m.host, '停止此任务'), undefined);
    enterText(m.host, '挂起时补充'); await nextTick();
    button(m.host, '发送').click(); await pause();
    assert.deepEqual(m.calls.filter(c => c.url === path + '/append').at(-1).body, {text:'挂起时补充', delivery_mode:'queue'});

    const stopped = detail({cursor:'stopped', status:'stopped', permission:'write', can_continue:true, stop_version:'77'});
    m.setState(stopped); m.emit('task.updated', stopped, 'stopped'); await pause();
    const textarea = enterText(m.host, '明确开始新一轮'); await nextTick();
    textarea.dispatchEvent(new window.KeyboardEvent('keydown', { key:'Enter', bubbles:true })); await pause();
    assert.equal(m.calls.filter(c => c.url === path + '/continue').length, 0);
    button(m.host, '提交并重新开始').click(); await pause();
    const call = m.calls.find(c => c.url === path + '/continue');
    assert.deepEqual(call.body, {text:'明确开始新一轮', stop_version:'77'});
    assert.ok(call.init.headers.get('Idempotency-Key'));
  } finally { m.cleanup(); }
});

test('platform setting clearing the domain keeps the saved checkbox preference', async () => {
  const calls = [];
  globalThis.fetch = async (url, init = {}) => {
    assert.equal(url, '/api/v1/platform/task-link-settings');
    if (init.method === 'PATCH') {
      const body = JSON.parse(init.body); calls.push(body);
      return Response.json({version: 8, origin:body.origin, enabled:body.enabled});
    }
    return Response.json({version:7, origin:'https://example.com', enabled:false});
  };
  const host = document.createElement('div'); document.body.append(host);
  const app = createApp({render:()=>h(Settings)}); app.mount(host);
  try {
    await pause();
    const domain = host.querySelector('#platform-task-origin');
    assert.ok(domain); domain.value = '';
    domain.dispatchEvent(new window.Event('input', {bubbles:true})); await nextTick();
    const checkbox = host.querySelector('[role=checkbox]');
    assert.equal(checkbox.getAttribute('aria-checked'), 'false');
    assert.equal(checkbox.getAttribute('aria-disabled'), 'true');
    host.querySelector('form').dispatchEvent(new window.Event('submit', {bubbles:true, cancelable:true})); await pause();
    assert.deepEqual(calls, [{expected_version:7, origin:'', enabled:false}]);
    domain.value = 'http://192.0.2.15:8080/';
    domain.dispatchEvent(new window.Event('input', {bubbles:true})); await nextTick();
    assert.equal(checkbox.getAttribute('aria-disabled'), 'false');
    assert.equal(checkbox.getAttribute('aria-checked'), 'false');
    host.querySelector('form').dispatchEvent(new window.Event('submit', {bubbles:true, cancelable:true})); await pause();
    assert.deepEqual(calls[1], {expected_version:8, origin:'http://192.0.2.15:8080/', enabled:false});
  } finally { app.unmount(); host.remove(); }
});


test('an expired resume cursor reloads one fresh snapshot instead of retrying stale state', async () => {
  const original = detail({cursor:'expired'}), repaired = detail({cursor:'fresh'});
  let expired = true;
  const m = await fixture({
    snapshotResponse: () => expired ? original : repaired,
    streamResponse: () => {
      if (!expired) return null;
      expired = false;
      return Response.json({error:{code:'invalid_cursor'}}, {status:400});
    },
  });
  try {
    const streams = m.calls.filter(c=>c.url.endsWith('/events'));
    assert.deepEqual(streams.map(c=>c.init.headers.get('Last-Event-ID')), ['expired','fresh']);
    assert.equal(m.calls.filter(c=>c.url === path).length, 2);
  } finally { m.cleanup(); }
});

test('public timeout retry keeps the draft and reuses its request key', async () => {
  let sends = 0;
  const initial = detail({permission:'write', can_append:true, can_stop:true});
  const m = await fixture({ initial, write: () => ++sends === 1
    ? Response.json({error:{code:'unavailable',message:'稍后再试'}},{status:503}) : Response.json(initial) });
  try {
    const textarea = enterText(m.host, '网络错误后重试同一补充'); await nextTick();
    button(m.host, '发送').click(); await pause();
    assert.equal(textarea.value, '网络错误后重试同一补充');
    assert.match(m.host.textContent, /稍后再试/);
    button(m.host, '发送').click(); await pause();
    const writes = m.calls.filter(c=>c.url === path+'/append');
    assert.equal(writes.length,2);
    assert.equal(writes[0].init.headers.get('Idempotency-Key'), writes[1].init.headers.get('Idempotency-Key'));
  } finally { m.cleanup(); }
});

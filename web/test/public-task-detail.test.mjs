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
  let state = initial || detail(), stream, execution;
  const calls = [];
  globalThis.fetch = async (url, init = {}) => {
    calls.push({url, init, body: init.body ? JSON.parse(init.body) : null});
    if (url === path + '/execution') return Response.json({
      items: [eventRow('agent.message','1',{message_id:'initial',content:[{type:'text',text:'## 最新可见结果'}]})],
      next_cursor: null, resume_cursor: 'execution-start',
    });
    if (url === path + '/events' || url === path + '/execution/stream') {
      const override = url.endsWith('/events') ? streamResponse?.(init) : undefined;
      if (override) return override;
      const body = new ReadableStream({ start(controller) {
        if (url.endsWith('/events')) stream = controller; else execution = controller;
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
    emit(event, data, id = '', live = false) {
      const lines = `${id ? 'id: '+id+'\r\n' : ''}event: ${event}\r\ndata: ${JSON.stringify(data)}\r\n\r\n`;
      const bytes = new TextEncoder().encode(lines);
      const channel = live ? execution : stream;
      channel.enqueue(bytes.slice(0, 13)); channel.enqueue(bytes.slice(13));
    },
    breakStream(live = false) { (live ? execution : stream).close(); },
    cleanup() { app.unmount(); host.remove(); },
  };
}

function eventRow(type, seq, data) {
  return {type, seq, session_id:'bound-session', occurred_at:'2026-11-24T00:00:00Z', ...data};
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
    assert.ok(m.host.querySelector('.task-detail-panel .session-panel'));
    assert.ok(m.host.querySelector('h2, h3'));
    assert.match(m.host.textContent, /最新可见结果/);
    assert.match(m.host.textContent, /只读/);
    assert.equal(m.host.querySelector('textarea'), null);
    const signIn = m.host.querySelector('[aria-label="KIKO 登录引导"] a');
    assert.ok(signIn);
    assert.equal(signIn.textContent.trim(), '登录 KIKO');
    assert.equal(signIn.getAttribute('href'), '/');
    assert.equal(signIn.target, '_blank');
    assert.equal(signIn.getAttribute('referrerpolicy'), 'no-referrer');
    assert.ok(signIn.relList.contains('noopener'));
    assert.ok(signIn.relList.contains('noreferrer'));
    assert.match(signIn.closest('aside').textContent, /查看你有权限的任务/);
    assert.equal(m.host.querySelector('.task-detail-panel a'), null);
    for (const name of ['发送', '停止此任务', '提交并重新开始', '归档任务', '撤回此输入', '修改标题']) assert.equal(button(m.host, name), undefined);
    for (const c of m.calls) {
      assert.equal(c.init.headers.get('Authorization'), 'Bearer ' + token);
      assert.equal(c.init.credentials, 'omit');
      assert.equal(c.init.referrerPolicy, 'no-referrer');
      assert.doesNotMatch(c.url, /synthetic-token|workspaces/);
    }
    assert.equal(m.calls.find(c => c.url === path + '/events').init.headers.get('Last-Event-ID'), 'current');
    assert.equal(m.calls.find(c => c.url === path + '/execution/stream').init.headers.get('Last-Event-ID'), 'execution-start');
  } finally { m.cleanup(); }
});

test('a confirmed public SSE cursor is used on resume, and revoke clears the shared view', async () => {
  const m = await fixture();
  try {
    const next = detail({cursor:'next', activities:[{kind:'output', text:'订阅中的结果', occurred_at:'2026-11-24T00:00:00Z'}]});
    m.emit('task.updated', next, 'next'); await pause();
    assert.match(m.host.textContent, /最新可见结果/);
    m.breakStream(); await pause(1130);
    const streams = m.calls.filter(c => c.url.endsWith('/events'));
    assert.ok(streams.length >= 2);
    assert.equal(streams.at(-1).init.headers.get('Last-Event-ID'), 'next');
    m.emit('stream.error', {error:{code:'task_detail_link_unavailable', message:'revoked'}});
    await pause();
    assert.match(m.host.textContent, /无效、已过期或已撤销/);
    assert.doesNotMatch(m.host.textContent, /最新可见结果|同一任务/);
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
    m.emit('user.message', eventRow('user.message','2',{message_id:'supplement',status:'queued',content:[{type:'text',text:'旧补充'}]}),'execution-2',true);
    await pause();
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

test('public tools update in place before completion and resume without duplicating a step', async () => {
  const m = await fixture();
  try {
    const tool = {tool_use_id:'tool-1', name:'run_report', status:'running', input_state:'available', input:{report:'sample'}};
    m.emit('agent.tool_use', eventRow('agent.tool_use','2',tool), 'execution-2', true);
    await pause();
    assert.match(m.host.querySelector('.event.tool').textContent, /run_report/);
    assert.equal(m.host.querySelectorAll('.event.tool').length, 1);
    m.breakStream(true); await pause(1130);
    const streams = m.calls.filter(c => c.url.endsWith('/execution/stream'));
    assert.equal(streams.at(-1).init.headers.get('Last-Event-ID'), 'execution-2');
    m.emit('agent.tool_result', eventRow('agent.tool_result','3',{...tool,status:'completed',output_state:'available',output:'report-ready'}), 'execution-3', true);
    m.emit('agent.tool_use', eventRow('agent.tool_use','4',{...tool,tool_use_id:'tool-2',name:'save_pdf'}),'execution-4',true);
    await pause();
    assert.equal(m.host.querySelectorAll('.event.tool').length, 2);
    assert.match(m.host.textContent, /save_pdf/);
    m.emit('stream.error', {error:{code:'task_detail_link_unavailable'}}, '', true);
    await pause();
    assert.match(m.host.textContent, /无效、已过期或已撤销/);
    assert.doesNotMatch(m.host.textContent, /run_report|save_pdf/);
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

test('public task detail renders sequential stream and inline markdown/csv artifact surface without navigation leaks', async () => {
  const initial = detail({
    artifacts: [
      { id: 'pub-md', name: '公开报告.md', size: 42 },
      { id: 'pub-csv', name: '数据表.csv', size: 36 },
    ],
  });
  const m = await fixture({
    initial,
    snapshotResponse: () => initial,
  });
  const baseFetch = globalThis.fetch;
  globalThis.fetch = async (url, init = {}) => {
    if (url === path + '/artifacts/pub-md') return new Response('# 免登产物报告\n验证通过', { headers: { 'Content-Type': 'application/octet-stream' } });
    if (url === path + '/artifacts/pub-csv') return new Response('项目,数值\n营收,9800', { headers: { 'Content-Type': 'application/octet-stream' } });
    return baseFetch(url, init);
  };
  try {
    const mdCard = [...m.host.querySelectorAll('.artifact-card-main')].find(el => el.textContent.includes('公开报告.md'));
    assert.ok(mdCard);
    mdCard.click();
    await pause(80);
    assert.equal(m.host.querySelector('[aria-label="选择执行轮次"]'), null);
    assert.equal(button(m.host, '← 返回任务列表'), undefined);
    const preview = m.host.querySelector('[aria-label="产物预览画布"]');
    assert.ok(preview);
    assert.equal(preview.querySelector('h1')?.textContent, '免登产物报告');
    const csvCard = [...m.host.querySelectorAll('.artifact-card-main')].find(el => el.textContent.includes('数据表.csv'));
    assert.ok(csvCard);
    csvCard.click();
    await pause(80);
    assert.match(preview.querySelector('.surface-csv-table').textContent, /9800/);
  } finally { m.cleanup(); }
});

test('public task detail unifies progress.phase status, keeps svg download-only, and preserves waterfall scroll state', async () => {
  let scrollHeight = 800;
  let initialExecutionRelease;
  const initial = detail({
    status: 'idle',
    progress: { phase: 'waiting_confirmation' },
    artifacts: [{ id: 'pub-svg', name: 'topology.svg', size: 88 }],
  });
  let state = initial;
  const calls = [];
  let executionStreamController;
  let taskStreamController;
  globalThis.fetch = async (url, init = {}) => {
    calls.push({ url, init });
    if (url === path) return Response.json(state);
    if (url === path + '/execution') {
      return new Promise(resolve => {
        initialExecutionRelease = () => resolve(Response.json({
          items: [eventRow('agent.message', '10', { message_id: 'm10', content: [{ type: 'text', text: '最新一条输出' }] })],
          next_cursor: 'pub-older-1',
          resume_cursor: 'pub-exec-10',
        }));
      });
    }
    if (url === path + '/execution?cursor=pub-older-1') {
      scrollHeight += 420;
      return Response.json({
        items: [eventRow('agent.message', '2', { message_id: 'm2', content: [{ type: 'text', text: '较早一条输出' }] })],
        next_cursor: 'pub-older-2',
        resume_cursor: 'pub-exec-10',
      });
    }
    if (url === path + '/execution?cursor=pub-older-2') {
      scrollHeight += 240;
      return Response.json({
        items: [eventRow('agent.message', '1', { message_id: 'm1', content: [{ type: 'text', text: '最早一条输出' }] })],
        next_cursor: null,
        resume_cursor: 'pub-exec-10',
      });
    }
    if (url === path + '/events' || url === path + '/execution/stream') {
      const body = new ReadableStream({ start(controller) {
        if (url.endsWith('/events')) taskStreamController = controller;
        else executionStreamController = controller;
        init.signal.addEventListener('abort', () => { try { controller.close(); } catch {} }, { once: true });
      } });
      return new Response(body, { headers: { 'Content-Type': 'text/event-stream' } });
    }
    throw new Error('Unexpected request: ' + url);
  };

  const host = document.createElement('div');
  document.body.append(host);
  const app = createApp({ render: () => h(App) });
  app.mount(host);
  await pause(60);

  try {
    // 1. Unified status badge prioritizes progress.phase
    const badge = host.querySelector('.status-badge');
    assert.equal(badge.textContent.trim(), '等待人工确认');
    for (const [phase, expected] of [
      ['completed', '已完成'],
      ['waiting_handoff', '已交接，等待处理结果'],
      ['waiting_resume', '已收到答复，等待继续执行'],
    ]) {
      const next = detail({ cursor: 'c-' + phase, status: 'idle', progress: { phase }, artifacts: initial.artifacts });
      taskStreamController.enqueue(new TextEncoder().encode(`id: ${next.cursor}\r\nevent: task.updated\r\ndata: ${JSON.stringify(next)}\r\n\r\n`));
      await pause(40);
      assert.equal(badge.textContent.trim(), expected);
    }

    // 2. SVG artifact is download-only (never fetched for inline preview, never renders <img>)
    const svgInlineCard = [...host.querySelectorAll('.artifact-surface-card')].find(el => el.textContent.includes('topology.svg'));
    assert.ok(svgInlineCard);
    assert.match(svgInlineCard.textContent, /仅下载/);
    assert.doesNotMatch(svgInlineCard.textContent, /预览 →/);
    const preview = host.querySelector('[aria-label="产物预览画布"]');
    assert.ok(preview);
    assert.equal(preview.querySelector('.image-preview-wrap img'), null);
    assert.ok(preview.querySelector('.binary-preview-card'));
    assert.ok(!calls.some(c => c.url.includes('/artifacts/pub-svg')));

    // 3. Waterfall scroll mechanics on public view
    const scroller = host.querySelector('.messages');
    assert.ok(scroller);
    Object.defineProperty(scroller, 'scrollHeight', { configurable: true, get: () => scrollHeight });
    Object.defineProperty(scroller, 'clientHeight', { configurable: true, value: 300 });
    assert.equal(typeof initialExecutionRelease, 'function');
    initialExecutionRelease();
    await pause(80);
    assert.equal(scroller.scrollTop, 800);

    // Scroll up into middle history -> SSE arrival does not yank back to bottom
    scroller.scrollTop = 200;
    scroller.dispatchEvent(new window.Event('scroll'));
    await nextTick();
    scrollHeight = 900;
    executionStreamController.enqueue(new TextEncoder().encode(
      `id: pub-exec-11\r\nevent: agent.message\r\ndata: ${JSON.stringify(eventRow('agent.message', '11', { message_id: 'm11', content: [{ type: 'text', text: '免登实时新增消息' }] }))}\r\n\r\n`,
    ));
    await pause(60);
    assert.equal(scroller.scrollTop, 200);
    assert.match(host.textContent, /免登实时新增消息/);

    const rowSizes = {'message:m2':420,'message:m1':240};
    scroller.getBoundingClientRect=()=>({top:0,bottom:300});
    function installRowGeometry() {
      const rows=()=>[...scroller.querySelectorAll('[data-history-key]')];
      for (const node of rows()) node.getBoundingClientRect=()=>{
        const all=rows(), height=rowSizes[node.dataset.historyKey]||300;
        const top=all.slice(0,all.indexOf(node)).reduce((sum,row)=>sum+(rowSizes[row.dataset.historyKey]||300),0)-scroller.scrollTop;
        return {top,bottom:top+height};
      };
    }
    installRowGeometry();

    // Scroll near top -> auto loads pub-older-1 and retains offset (+420)
    scroller.scrollTop = 25;
    scroller.dispatchEvent(new window.Event('scroll'));
    await pause(80);
    assert.ok(calls.some(c => c.url.endsWith('/execution?cursor=pub-older-1')));
    assert.match(host.textContent, /较早一条输出/);
    assert.equal(scroller.scrollTop, 445);

    installRowGeometry();
    // Manual button fallback still loads pub-older-2
    const olderBtn = button(host, '加载更早历史消息');
    assert.ok(olderBtn);
    olderBtn.click();
    await pause(80);
    assert.ok(calls.some(c => c.url.endsWith('/execution?cursor=pub-older-2')));
    assert.match(host.textContent, /最早一条输出/);
    assert.equal(scroller.scrollTop, 685);
  } finally {
    app.unmount();
    host.remove();
  }
});

test('mobile task detail switches to an artifact and returns without remounting the writable timeline', async () => {
  const nativeMatch = window.matchMedia;
  window.matchMedia = () => ({matches:true,addEventListener(){},removeEventListener(){}});
  const initial=detail({permission:'write',can_append:true,artifacts:[{id:'mobile-md',name:'手机报告.md',size:42}]});
  const m=await fixture({initial});
  const baseFetch=globalThis.fetch;
  globalThis.fetch=(url,init={})=>url===path+'/artifacts/mobile-md'
    ? Promise.resolve(new Response('# 手机报告内容'))
    : baseFetch(url,init);
  try {
    const process=m.host.querySelector('.process');
    const surface=m.host.querySelector('.surface-pane');
    assert.notEqual(process.style.display,'none');
    assert.equal(surface.style.display,'none');
    const draft=enterText(m.host,'返回后还没发送');
    const timeline=m.host.querySelector('.messages');
    timeline.scrollTop=32;
    m.host.querySelector('.artifact-card-main').click();
    await pause();
    assert.equal(process.style.display,'none');
    assert.notEqual(surface.style.display,'none');
    assert.match(surface.querySelector('.artifact-preview-surface').textContent,/手机报告内容/);
    button(m.host,'返回执行过程').click();
    await pause();
    assert.notEqual(process.style.display,'none');
    assert.equal(surface.style.display,'none');
    assert.equal(m.host.querySelector('.messages'),timeline);
    assert.equal(timeline.scrollTop,32);
    assert.equal(m.host.querySelector('textarea'),draft);
    assert.equal(draft.value,'返回后还没发送');
    assert.equal(button(m.host,'返回来源对话'),undefined);
    assert.equal(m.calls.filter(c=>c.url===path+'/execution/stream').length,1);
  } finally {m.cleanup();window.matchMedia=nativeMatch;}
});

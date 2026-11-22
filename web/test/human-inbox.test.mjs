import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
import { Window } from 'happy-dom';
import { parse, compileScript } from '@vue/compiler-sfc';
import esbuild from 'esbuild';

Error.stackTraceLimit = 0;

const window = new Window({ url: 'http://127.0.0.1:5173/' });
for (const name of ['window', 'document', 'navigator', 'HTMLElement', 'Element', 'SVGElement', 'Node', 'Text', 'Comment',
  'KeyboardEvent', 'Image', 'Document', 'MutationObserver', 'ResizeObserver', 'getComputedStyle', 'requestAnimationFrame', 'cancelAnimationFrame']) {
  const value = name === 'window' ? window : window[name];
  if (value === undefined) continue;
  const bound = ['getComputedStyle', 'requestAnimationFrame', 'cancelAnimationFrame'].includes(name) ? value.bind(window) : value;
  Object.defineProperty(globalThis, name, { configurable: true, writable: true, value: bound });
}

const built = await esbuild.build({
  stdin: {
    contents: "export {default as Inbox} from './src/direction2/views/PlatformHumanInbox.vue'; export {createApp, h, ref} from 'vue';",
    resolveDir: process.cwd(), loader: 'ts',
  },
  bundle: true, write: false, format: 'esm', platform: 'node',
  plugins: [{
    name: 'vue',
    setup(build) {
      build.onLoad({ filter: /\.vue$/ }, async ({ path }) => {
        const { descriptor } = parse(await fs.readFile(path, 'utf8'));
        return { contents: compileScript(descriptor, { id: path, inlineTemplate: true }).content, loader: 'ts' };
      });
    },
  }],
});
const { Inbox, createApp, h, ref } = await import(`data:text/javascript;base64,${Buffer.from(built.outputFiles[0].text).toString('base64')}`);
const settle = () => new Promise(resolve => setTimeout(resolve, 55));

test('assigned question uses the structured answer endpoint and disappears after a saved answer', async () => {
  const calls = [];
  const item = { id: 'request-a', version: '4', state: 'pending', question: { question: '包含退款吗？', answer_type: 'decision' } };
  globalThis.fetch = async (path, init) => {
    calls.push([path, init]);
    return init.method === 'POST' ? Response.json({ id: item.id, state: 'approved' }) : Response.json({ items: [item] });
  };
  const host = document.createElement('div');
  document.body.append(host);
  const app = createApp(Inbox, { workspace: 'workspace-a' });
  app.mount(host);
  try {
    await settle();
    assert.match(host.textContent, /包含退款吗/);
    const approve = [...host.querySelectorAll('button')].find(button => button.textContent.trim() === '同意');
    assert.ok(approve);
    approve.click();
    await settle();
    assert.equal(calls[1][0], '/api/v1/workspaces/workspace-a/human-requests/request-a/answer');
    assert.deepEqual(JSON.parse(calls[1][1].body), { version: '4', decision: 'approve', text: '' });
    assert.doesNotMatch(host.textContent, /包含退款吗/);
    assert.match(host.textContent, /原任务已排队继续/);
  } finally {
    app.unmount();
    host.remove();
  }
});

test('read-only inbox keeps the full count, direct links and pagination without answer controls', async () => {
  const calls = [], counts = [];
  const question = id => ({ id, version: '1', state: 'pending', detail_visible: true, question: { question: id, answer_type: 'decision', background: '确认背景' } });
  const first = Array.from({length: 100}, (_, index) => question('question-' + index));
  const target = {...question('linked-request'),recipient_name:'王敏',agent_name:'客服员工'};
  globalThis.fetch = async (path, init = {}) => {
    calls.push({ path, method: init.method || 'GET' });
    if (path.includes('request_id=')) return Response.json({ items: [target], total: 1, pending:1, resolved:0 });
    return Response.json({ items: path.endsWith('offset=100') ? [target] : first, total: 101, pending:101,resolved:0 });
  };
  const host = document.createElement('div');
  document.body.append(host);
  const app = createApp(Inbox, { workspace: 'w', readOnly: true, selectedId: target.id, onCount: n => counts.push(n) });
  app.mount(host);
  try {
    await settle();
    assert.equal(host.querySelector('.highlighted .appr-title').textContent.trim(), target.id);
    assert.ok(calls.some(call => call.path.endsWith('&request_id=linked-request')));
    assert.equal(counts.at(-1), 101);
    assert.match(host.textContent,/待处理 \(101\)/);
    assert.match(host.textContent,/王敏/);
    assert.equal(host.querySelectorAll('textarea,.answers').length, 0);
    [...host.querySelectorAll('button')].find(b => b.textContent.trim() === '下一页').click();
    await settle();
    assert.equal(host.querySelectorAll('.approval-card').length, 1);
    assert.equal(host.querySelector('.highlighted .appr-title').textContent.trim(), target.id);
    assert.equal(counts.at(-1), 101);
    assert.match(host.textContent,/待处理 \(101\)/);
    assert.match(host.textContent,/王敏/);
    assert.ok(calls.every(call => call.method === 'GET'));
  } finally {
    app.unmount();
    host.remove();
  }
});

test('resolved history displays the recorded decision and only fields supplied by the server', async () => {
  const calls = [], counts = [];
  globalThis.fetch=async (path, init={}) => {
    calls.push({path,method:init.method||'GET'});
    const resolved = path.includes('filter=resolved');
    return Response.json({pending:3,resolved:1,total:resolved?1:3,items:resolved?[{
      id:'historic',version:'1',state:'revised',detail_visible:true,question:{question:'请核对费用',background:'对比草案',answer_type:'decision'},
      recipient_name:'王敏',recipient_provider:'web',agent_name:'财务员工',created_at:'2026-11-18T10:00:00Z',
      answer:{decision:'revise',text:'请附原始明细'},answered_at:'2026-11-19T10:00:00Z',
    }]:[]});
  };
  const host=document.createElement('div');document.body.append(host);
  const app=createApp(Inbox,{workspace:'w',readOnly:true,onCount: value => counts.push(value)});app.mount(host);
  try{
    await settle();[...host.querySelectorAll('button')].find(b=>b.textContent.includes('已处理')).click();await settle();
    for(const t of ['王敏','请核对费用','财务员工','已退回修改','请附原始明细'])assert.ok(host.textContent.includes(t));
    assert.deepEqual(counts, [3, 3]);
    assert.equal(host.querySelector('.appr-diff-box,.appr-decision-box,textarea,.answers'),null);
    assert.ok(calls.every(c=>c.path.includes('/human-requests/history?') && c.method==='GET'));
  }finally{app.unmount();host.remove();}
});

test('workspace badge polling refreshes the open confirmation list when another handler finishes', async () => {
  const pending = ref(1);
  const request = { id: 'peer-request', state: 'pending', detail_visible: true, recipient_name: '王敏',
    question: { question: '核对合同', answer_type: 'decision' } };
  globalThis.fetch = async () => Response.json({
    items: pending.value ? [request] : [], pending: pending.value, resolved: 1 - pending.value, total: pending.value,
  });
  const host = document.createElement('div');
  document.body.append(host);
  const app = createApp(() => h(Inbox, { workspace: 'w', readOnly: true, workspacePending: pending.value }));
  app.mount(host);
  try {
    await settle();
    assert.match(host.textContent, /核对合同/);
    pending.value = 0;
    await settle();
    assert.doesNotMatch(host.textContent, /核对合同/);
    assert.match(host.textContent, /待处理 \(0\)/);
    assert.match(host.textContent, /已处理 \(1\)/);
  } finally {
    app.unmount();
    host.remove();
  }
});


const visibleRequest = (id, body) => ({
  id, version: '1', state: 'pending', detail_visible: true, created_at: '2026-11-18T10:00:00Z',
  question: { question: body, answer_type: 'decision' },
});
const historyResponse = (...items) => Response.json({ items, total: items.length, pending: items.length, resolved: 0 });

test('summary cards fail closed even if payload includes private fields without detail_visible', async () => {
  globalThis.fetch = async () => historyResponse(
    {
      ...visibleRequest('redacted', '不可显示的问题'),
      detail_visible: false, state: 'revised', recipient_name: '私密处理人', agent_name: '私密员工',
      recipient_provider: 'feishu', answered_at: '2026-11-19T10:00:00Z',
      answer: { decision: 'revise', text: '私密答复' }, clarification: '私密补问',
      command_preview: '演示命令', options: [{ label: '演示选项' }], selected_option: '演示选中',
    },
    { ...visibleRequest('missing-flag', '缺少权限标记的问题'), detail_visible: undefined },
    { id: 'summary-only', detail_visible: false, version: '1', state: 'pending', created_at: '2026-11-18T10:00:00Z' },
    visibleRequest('authorized', '有权查看的问题'),
  );
  const host = document.createElement('div'); document.body.append(host);
  const app = createApp(Inbox, { workspace: 'w', readOnly: true }); app.mount(host);
  try {
    await settle();
    assert.equal(host.querySelectorAll('.approval-card').length, 4);
    assert.match(host.textContent, /已退回修改/);
    assert.match(host.textContent, /有权查看的问题/);
    assert.match(host.textContent, /仅可查看处理状态/);
    assert.doesNotMatch(host.textContent, /不可显示|缺少权限标记|私密|演示/);
    assert.equal(host.querySelectorAll('textarea,.answers').length, 0);
  } finally { app.unmount(); host.remove(); }
});

test('invalid, unavailable and stale focused records do not destroy successful history', async () => {
  for (const status of [400, 404, 500, 200]) {
    globalThis.fetch = async path => {
      if (!path.includes('request_id=')) return historyResponse(visibleRequest('list-item', '正常列表内容'));
      return status === 200 ? historyResponse() : Response.json({ error: { message: '专属查询失败' } }, { status });
    };
    const host = document.createElement('div'); document.body.append(host);
    const app = createApp(Inbox, { workspace: 'w', readOnly: true, selectedId: 'missing' }); app.mount(host);
    try {
      await settle();
      assert.match(host.textContent, /正常列表内容/);
      assert.match(host.textContent, /此确认请求当前不可查看|无法加载链接指向的确认请求/);
      assert.ok(host.querySelector('.approval-filter-bar'));
      assert.equal(host.querySelector('[role="alert"]'), null);
    } finally { app.unmount(); host.remove(); }
  }
});

test('authorization failure for a focused lookup clears previously visible data', async () => {
  for (const status of [401, 403]) {
    let allowed = true;
    const counts = [];
    globalThis.fetch = async path => {
      if (path.includes('request_id=')) {
        return allowed
          ? historyResponse(visibleRequest('focused', '之前可见的焦点'))
          : Response.json({ error: { message: '访问已撤销' } }, { status });
      }
      return historyResponse(visibleRequest('list-item', '之前可见的列表内容'));
    };
    const host = document.createElement('div'); document.body.append(host);
    const app = createApp(Inbox, { workspace: 'w', readOnly: true, selectedId: 'focused', onCount: n => counts.push(n) });
    const panel = app.mount(host);
    try {
      await settle();
      assert.match(host.textContent, /之前可见的焦点/);
      assert.match(host.textContent, /之前可见的列表内容/);
      allowed = false;
      await panel.refresh();
      await settle();
      assert.doesNotMatch(host.textContent, /之前可见的/);
      assert.match(host.textContent, /访问已撤销/);
      assert.equal(host.querySelector('.approval-filter-bar'), null);
      assert.deepEqual(counts, [1, 0]);
    } finally { app.unmount(); host.remove(); }
  }
});

test('open history refreshes at equal counts, preserves a transient failure, and clears on revocation', async () => {
  const oldSetInterval = globalThis.setInterval, oldClearInterval = globalThis.clearInterval;
  const pollID = Symbol('approval poll');
  const oldHidden = Object.getOwnPropertyDescriptor(document, 'hidden');
  let poll, stopped = false, response = () => historyResponse(visibleRequest('prior-a', '被替换的申请 A'));
  const paths = [];
  globalThis.setInterval = (callback, interval, ...args) => {
    if (interval !== 10_000) return oldSetInterval(callback, interval, ...args);
    poll = callback;
    return pollID;
  };
  globalThis.clearInterval = id => {
    if (id === pollID) stopped = true;
    else oldClearInterval(id);
  };
  globalThis.fetch = async path => { paths.push(path); return response(); };
  const host = document.createElement('div'); document.body.append(host);
  const app = createApp(Inbox, { workspace: 'w', readOnly: true, workspacePending: 1 });
  app.mount(host);
  try {
    await settle();
    assert.match(host.textContent, /被替换的申请 A/);
    assert.equal(paths.length, 1);
    assert.equal(typeof poll, 'function');
    response = () => historyResponse(visibleRequest('new-b', '新申请 B'));
    Object.defineProperty(document, 'hidden', { configurable: true, value: true });
    poll();
    await settle();
    assert.equal(paths.length, 1);
    Object.defineProperty(document, 'hidden', { configurable: true, value: false });
    poll();
    await settle();
    assert.equal(paths.length, 2);
    assert.match(host.textContent, /新申请 B/);
    assert.doesNotMatch(host.textContent, /被替换的申请 A/);
    assert.ok(paths.every(path => path.endsWith('filter=pending&offset=0')));
    response = () => Response.json({ error: { message: '暂时离线' } }, { status: 503 });
    poll();
    await settle();
    assert.match(host.textContent, /新申请 B/);
    assert.match(host.textContent, /暂时离线/);
    assert.ok(host.querySelector('.approval-filter-bar'));
    response = () => Response.json({ error: { message: '工作区访问权限已撤销' } }, { status: 403 });
    poll();
    await settle();
    assert.doesNotMatch(host.textContent, /新申请 B/);
    assert.match(host.textContent, /工作区访问权限已撤销/);
  } finally {
    app.unmount(); host.remove();
    globalThis.setInterval = oldSetInterval;
    globalThis.clearInterval = oldClearInterval;
    if (oldHidden) Object.defineProperty(document, 'hidden', oldHidden);
    else delete document.hidden;
  }
  assert.equal(stopped, true);
});

test('replacing workspace ignores a late focused response even if transport ignores abortion', async () => {
  const workspace = ref('old');
  let finishOld, lookupSignal;
  globalThis.fetch = async (path, init) => {
    if (path.includes('/old/') && path.includes('request_id=')) {
      lookupSignal = init.signal;
      return new Promise(resolve => { finishOld = resolve; });
    }
    return path.includes('/old/')
      ? historyResponse(visibleRequest('old-list', '旧工作区内容'))
      : historyResponse(visibleRequest('target', '新工作区内容'));
  };
  const host = document.createElement('div'); document.body.append(host);
  const app = createApp(() => h(Inbox, { workspace: workspace.value, readOnly: true, selectedId: 'target' }));
  app.mount(host);
  try {
    await settle();
    assert.equal(typeof finishOld, 'function');
    workspace.value = 'new';
    await settle();
    assert.equal(lookupSignal.aborted, true);
    finishOld(historyResponse(visibleRequest('target', '旧工作区焦点')));
    await settle();
    assert.match(host.textContent, /新工作区内容/);
    assert.doesNotMatch(host.textContent, /旧工作区/);
  } finally { app.unmount(); host.remove(); }
});

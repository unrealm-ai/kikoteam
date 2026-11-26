import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
import { Window } from 'happy-dom';
import { parse, compileScript } from '@vue/compiler-sfc';
import esbuild from 'esbuild';
const window = new Window({ url: 'http://127.0.0.1:5173/' });
for (const key of ['window','document','navigator','HTMLElement','Element','SVGElement','Node','Text','Comment','Image','InputEvent','MouseEvent','KeyboardEvent','HTMLInputElement','Document','MutationObserver','ResizeObserver','getComputedStyle','requestAnimationFrame','cancelAnimationFrame']) {
  const value = key === 'window' ? window : window[key]; if (value !== undefined) Object.defineProperty(globalThis, key, { configurable: true, writable: true, value: typeof value === 'function' && ['getComputedStyle','requestAnimationFrame','cancelAnimationFrame'].includes(key) ? value.bind(window) : value });
}
const built = await esbuild.build({ stdin: { contents: "export {default as Detail} from './src/direction2/components/TaskDetailPanel.vue'; export {default as Board} from './src/direction2/views/PlatformWorkItems.vue'; export {default as List} from './src/direction2/views/PlatformTasksView.vue'; export {activeWorkspaceId} from './src/direction2/api/client'; export {createApp,h,nextTick,ref} from 'vue';", resolveDir: process.cwd(), loader: 'ts' }, bundle: true, write: false, format: 'esm', platform: 'node', define: { 'import.meta.env.VITE_PLATFORM_REAL':'true', 'import.meta.env.DEV':'false' }, plugins: [{name:'vue',setup(b){b.onLoad({filter:/\.vue$/},async({path})=>({contents:compileScript(parse(await fs.readFile(path,'utf8')).descriptor,{id:path,inlineTemplate:true}).content,loader:'ts'}));}}] });
const { Detail, Board, List, activeWorkspaceId, createApp, h, nextTick, ref } = await import(`data:text/javascript;base64,${Buffer.from(built.outputFiles[0].text).toString('base64')}`);
const wait = async (ms=60) => { await new Promise(r=>setTimeout(r,ms)); await nextTick(); };
class Source { static all=[]; constructor(url){this.url=url;this.closed=false;this.listeners=new Map();Source.all.push(this);} addEventListener(t,f){this.listeners.set(t,f);} close(){this.closed=true;} }
globalThis.EventSource=Source;
const task = (id='one') => ({ task_id:id,session_id:`s-${id}`,conversation_id:'c',title:`任务 ${id}`,agent:{id:'a',name:'小析',avatar:''},source:{kind:'chat_delegation',chat_session_id:'chat',chat_input_id:'origin'},lifecycle:'active',execution:{state:'idle',active_turn_id:null,queued_input_count:0,background_process_count:0,block_reason:null},latest_input:null,latest_turn:{id:'turn-1',turn_no:'1',status:'completed',started_at:'2026-11-17T01:00:00Z',finished_at:'2026-11-17T01:00:42Z',has_unknown_tool_results:false},created_at:'2026-11-17T01:00:00Z',revision:'1',accepts_input:true,input_block_reason:null,actions:{rename:{allowed:true},archive:{allowed:true},stop:{allowed:true}},initial_input:{id:'i',text:'分析销售数据'},latest_result:null });
const empty = () => Response.json({items:[],next_cursor:null});
const button = text => [...document.querySelectorAll('button')].find(b=>b.textContent.trim()===text);
async function mount(component=Detail, override=()=>null, initial={}) {
  const calls=[], navigation=[], props=ref(component===Detail?{workspaceId:'w',taskId:'one',...initial}:initial); Source.all=[]; activeWorkspaceId.value='w';
  globalThis.fetch=async(url,init={})=>{calls.push({url,init});const custom=await override(url,init);if(custom)return custom;
    if(url.includes('/agents?'))return Response.json({items:[{id:'a',name:'小析'}],page:1,total:1});
    if(/\/tasks\?/.test(url))return Response.json({items:[task()],next_cursor:null});
    if(/\/tasks\/[^/]+\/reports/.test(url)||/\/(inputs|turns)\?/.test(url))return empty();
    if(/\/tasks\/[^/?]+$/.test(url))return Response.json(task(url.split('/').pop()));
    if(url.includes('/events?'))return Response.json({items:[{type:'agent.message',seq:'1',session_id:url.match(/sessions\/([^/]+)/)[1],message_id:'m',content:[{type:'text',text:'## 实际执行结果'}]}],next_cursor:null,resume_cursor:'H'});
    if(/\/sessions\/[^/?]+$/.test(url))return Response.json({id:url.split('/').pop(),lifecycle:'active',accepts_input:true,execution:task().execution});
    return Response.json({id:'input'});
  };
  const host=document.createElement('div');document.body.appendChild(host);const app=createApp({render:()=>h(component,{...props.value,onNavigate:(...args)=>navigation.push(args)})});app.mount(host);await wait();await wait();
  return {calls,props,host,navigation,async cleanup(){app.unmount();host.remove();await wait();}};
}
test('task list reads Manager data with server search, paging and no legacy approval dependencies',async()=>{
  const m=await mount(List,url=>url.includes('/tasks?')?Response.json({items:[task(url.includes('cursor=next')?'two':'one')],next_cursor:url.includes('cursor=next')?null:'next'}):null);
  try {assert.match(m.host.textContent,/任务 one/);button('下一页').click();await wait();assert.match(m.host.textContent,/任务 two/);const input=m.host.querySelector('input');input.value='销售';input.dispatchEvent(new window.Event('input',{bubbles:true}));await wait(380);assert.ok(m.calls.some(c=>c.url.includes('q=%E9%94%80%E5%94%AE')&&!c.url.includes('cursor=next')));assert.ok(!m.calls.some(c=>/approvals|work-items|\/api\/workspaces/.test(c.url)));}finally{await m.cleanup();}
});
test('direct-linked task opens outside the loaded list and uses the common detail',async()=>{
  const m=await mount(List,()=>null,{selectedTaskId:'off-page'});
  try {assert.ok(document.querySelector('.task-detail-panel'));assert.match(document.body.textContent,/任务 off-page/);assert.ok(m.calls.some(c=>c.url.endsWith('/tasks/off-page')));assert.equal(Source.all.filter(s=>!s.closed).length,1);button('关闭任务详情').click();await wait();assert.ok(!m.calls.some(c=>c.url.endsWith('/stop')));}finally{await m.cleanup();}
});
test('task navigation reuses the session input, stop and archive commands', async () => {
  const id = 'off-page', sessionPath = '/api/v1/workspaces/w/sessions/s-off-page';
  let saved = '', archived = false;
  const m = await mount(List, (url, init) => {
    if (url === sessionPath + '/inputs' && init.method === 'POST') saved = JSON.parse(init.body).text;
    if (url === sessionPath + '/archive' && init.method === 'POST') archived = true;
    if (url.includes('/inputs?')) return Response.json({items: saved ? [{id:'i',seq:'2',status:'queued',text:saved,delivery_mode:'steer'}] : [], next_cursor:null});
    const value = task(id);
    if (archived) {
      value.lifecycle = 'archived';
      value.accepts_input = false;
      value.actions.archive.allowed = value.actions.stop.allowed = false;
    }
    if (url.endsWith('/tasks/' + id)) return Response.json(value);
    if (url === sessionPath) return Response.json({...value,id:'s-off-page',execution:{
      ...value.execution, required_actions:[{type:'confirmation',request_id:'other',title:'待确认',message:'待人工核对'}],
    }});
    return null;
  }, {selectedTaskId:id});
  try {
    assert.ok(button('修改标题') && !button('是') && !button('否'));
    assert.match(document.body.textContent, /待人工核对/);
    assert.ok(m.calls.every(c => !c.init.method || c.init.method === 'GET'));
    const input = document.querySelector('textarea');
    input.value = '核对退款明细';
    input.dispatchEvent(new window.Event('input',{bubbles:true}));
    await nextTick();
    button('发送').click();
    await wait();
    button('输入记录').click();
    await wait();
    assert.match(document.querySelector('.records').textContent,/核对退款明细/);
    assert.ok(button('撤回此输入'));
    button('执行过程').click();
    await nextTick();
    button('停止此任务').click();
    await wait();
    assert.match(document.body.textContent,/停止请求已受理/);
    button('归档任务').click();
    await wait();
    assert.match(document.body.textContent,/任务已归档/);
    assert.ok(button('停止此任务').disabled && button('归档任务').disabled && input.disabled);
    const writes = m.calls.filter(c => c.init.method === 'POST');
    assert.deepEqual(writes.map(c => c.url), ['inputs','stop','archive'].map(t => sessionPath + '/' + t));
    assert.deepEqual(JSON.parse(writes[0].init.body), {text:saved, delivery_mode:'steer'});
    assert.ok(writes[0].init.headers.get('Idempotency-Key'));
  } finally { await m.cleanup(); }
});
test('task center renames with the server revision and withdraws only eligible queued records', async () => {
  const root = '/api/v1/workspaces/w/sessions/s-one';
  let title = '任务 one', revision = '7', canRename = true, withdrawn = false;
  const m = await mount(List, (url, init) => {
    if (url === root && init.method === 'PATCH') {
      const body = JSON.parse(init.body);
      if (body.expected_revision !== revision) return Response.json({error:{message:'标题版本过期'}}, {status:409});
      title = body.title;
      revision = '8';
      canRename = false;
      return Response.json({id:'s-one', title, revision});
    }
    if (url === root + '/inputs/queued/withdraw' && init.method === 'POST') {
      withdrawn = true;
      return Response.json({id:'queued',status:'canceled'});
    }
    if (url.endsWith('/tasks/one')) {
      const response = {...task(),title,revision};
      response.actions.rename.allowed = canRename;
      return Response.json(response);
    }
    if (url.includes('/inputs?')) {
      const input = {delivery_mode:'queue',turn_id:null};
      return Response.json({items:[
        {...input,id:'queued',seq:'1',status:withdrawn?'canceled':'queued',text:'普通输入'},
        {...input,id:'confirm',seq:'2',status:'queued',text:'确认答复',confirmation_request_id:'required'},
        {...input,id:'processing',seq:'3',status:'started',text:'执行已开始'},
        {...input,id:'done',seq:'4',status:'processed',text:'已经处理的输入'},
      ],next_cursor:null});
    }
    return null;
  }, {selectedTaskId:'one'});
  try {
    button('修改标题').click();
    await nextTick();
    const input = document.querySelector('.rename input');
    input.value = '  对账任务  ';
    input.dispatchEvent(new window.Event('input',{bubbles:true}));
    await nextTick();
    button('保存标题').click();
    await wait();
    assert.match(document.querySelector('.task-summary h2').textContent,/对账任务/);
    assert.ok(button('修改标题').disabled);
    const patch = m.calls.find(c=>c.url===root && c.init.method==='PATCH');
    assert.deepEqual(JSON.parse(patch.init.body),{title:'对账任务',expected_revision:'7'});
    assert.equal(patch.init.headers.get('X-CSRF-Protection'),'1');

    button('输入记录').click();
    await nextTick();
    const withdrawals = [...document.querySelectorAll('.records button')].filter(b=>b.textContent.trim()==='撤回此输入');
    assert.equal(withdrawals.length,1);
    assert.match(withdrawals[0].closest('article').textContent,/普通输入/);
    withdrawals[0].click();
    await wait();
    assert.match(document.body.textContent,/输入已撤回/);
    assert.equal(button('撤回此输入'),undefined);
    const post = m.calls.find(c=>c.url===root+'/inputs/queued/withdraw');
    assert.equal(post.init.method,'POST');
    assert.equal(post.init.headers.get('X-CSRF-Protection'),'1');
  } finally { await m.cleanup(); }
});

test('a late task refresh cannot give a stale title draft a newer revision', async () => {
  let revision = '1';
  const m = await mount(Detail, (url, init) => {
    if (url.endsWith('/sessions/s-one') && init.method === 'PATCH') {
      const body = JSON.parse(init.body);
      assert.equal(body.expected_revision, '1');
      return Response.json({error:{code:'session_revision_conflict',message:'修订号已变化，请重新查看'}}, {status:409});
    }
    if (url.endsWith('/tasks/one')) return Response.json({...task(), revision, title: revision === '1' ? '任务 one' : '其他人已更新'});
    return null;
  },{taskCenter:true,taskRecordActions:true});
  try {
    button('修改标题').click();
    await nextTick();
    const input = document.querySelector('.rename input');
    input.value = '本地草稿';
    input.dispatchEvent(new window.Event('input',{bubbles:true}));
    revision = '2';
    document.dispatchEvent(new window.Event('visibilitychange'));
    await wait();
    assert.match(document.querySelector('.task-summary h2').textContent,/其他人已更新/);
    button('保存标题').click();
    await wait();
    assert.match(m.host.textContent,/修订号已变化/);
    assert.doesNotMatch(m.host.textContent,/标题已更新/);
    assert.equal(document.querySelector('.rename input').value,'本地草稿');
  } finally { await m.cleanup(); }
});

test('task center honors independent input, stop and archive permissions and keeps full session stream when initialTurnId is given', async () => {
  const m = await mount(Detail, url => url.endsWith('/tasks/one') ? Response.json({...task(), accepts_input:true,
    actions:{supplement:{allowed:false,reason:'system_managed_input'},rename:{allowed:true}, stop:{allowed:true}, archive:{allowed:false}}}) : null, {taskCenter:true});
  try {
    assert.ok(document.querySelector('textarea').disabled);
    assert.match(m.host.textContent,/此任务的输入由系统派发/);
    assert.equal(button('停止此任务').disabled,false);
    assert.ok(button('归档任务').disabled);
    m.props.value = {...m.props.value,initialTurnId:'old-turn'};
    await wait();
    assert.ok(document.querySelector('textarea').disabled);
    assert.match(m.host.textContent,/此任务的输入由系统派发/);
    assert.equal(button('停止此任务').disabled,false);
    assert.ok(button('归档任务').disabled);
    assert.ok(!m.calls.some(c => c.url.includes('/events?') && c.url.includes('turn_id=')));
    assert.ok(m.calls.every(c => !c.init.method || c.init.method === 'GET'));
  } finally { await m.cleanup(); }
});
test('archive refreshes task permissions even when a pre-archive read finishes late', async () => {
  let reads = 0, finishEarlierRead, archived = false;
  const m = await mount(Detail, (url, init) => {
    if (url.endsWith('/sessions/s-one/archive') && init.method === 'POST') archived = true;
    if (!url.endsWith('/tasks/one')) return null;
    const response = task();
    response.actions.archive.allowed = !archived;
    if (++reads === 2) return new Promise(resolve => { finishEarlierRead = () => resolve(Response.json(response)); });
    return Response.json(response);
  }, {taskCenter:true});
  try {
    document.dispatchEvent(new window.Event('visibilitychange'));
    await nextTick();
    assert.equal(typeof finishEarlierRead, 'function');
    button('归档任务').click();
    await wait();
    assert.ok(archived);
    finishEarlierRead();
    await wait();
    assert.ok(button('归档任务').disabled);
    assert.equal(reads, 3);
  } finally { await m.cleanup(); }
});
test('historical report selection opens full Session waterfall without turn filtering and keeps composer available',async()=>{
  const m=await mount(Detail,()=>null,{initialTurnId:'old-turn'});
  try {
    assert.ok(m.calls.some(c=>c.url.includes('/events?')&&!c.url.includes('turn_id=')));
    assert.ok(Source.all[0] && !/turn_id=/.test(Source.all[0].url));
    assert.equal(m.host.querySelector('.turn-focus-bar'),null);
    assert.equal(m.host.querySelector('[aria-label="选择执行轮次"]'),null);
    assert.ok(m.host.querySelector('textarea'));
    assert.match(m.host.textContent,/最近：本轮完成/);
    assert.doesNotMatch(m.host.textContent,/任务已完成/);
  } finally { await m.cleanup(); }
});
test('input records withdraw only queued inputs and report states preserve pending, historical and suppressed facts',async()=>{
  const m=await mount(Detail,url=>url.includes('/inputs?')?Response.json({items:[{id:'q',seq:'1',status:'queued',text:'补充要求',delivery_mode:'queue',turn_id:null},{id:'p',seq:'2',status:'processed',text:'已处理输入',delivery_mode:'steer',turn_id:'t'}],next_cursor:null}):url.includes('/reports?')?Response.json({items:[{source_input_id:'i1',source_input_seq:'1',status:'not_enabled',outcome:'completed'},{source_input_id:'i2',source_input_seq:'2',status:'suppressed',outcome:'failed',reason:'target_archived'},{source_input_id:'i3',source_input_seq:'3',status:'pending',outcome:'completed'}],next_cursor:null}):null);
  try {button('输入记录').click();await wait();assert.equal([...m.host.querySelectorAll('button')].filter(b=>b.textContent==='撤回此输入').length,1);button('撤回此输入').click();await wait();assert.ok(m.calls.some(c=>c.url.endsWith('/inputs/q/withdraw')&&c.init.headers.get('X-CSRF-Protection')==='1'));button('主会话回报').click();await wait();assert.match(m.host.textContent,/历史输入未启用回报/);assert.match(m.host.textContent,/未回报到主会话/);assert.match(m.host.textContent,/等待回报补偿/);}finally{await m.cleanup();}
});
test('failed task controls preserve errors and refresh facts without inventing success',async()=>{
  const m=await mount(Detail,(url,init)=>url.endsWith('/archive')&&init.method?Response.json({error:{code:'session_busy',message:'仍有后台进程',request_id:'trace-1'}},{status:409}):null);
  try {button('归档任务').click();await wait();assert.match(m.host.textContent,/仍有后台进程/);assert.match(m.host.textContent,/trace-1/);assert.doesNotMatch(m.host.textContent,/任务已归档/);}finally{await m.cleanup();}
});
test('task switch closes prior stream and discards late detail response',async()=>{
  let release;const m=await mount(Detail,url=>url.endsWith('/tasks/delayed')?new Promise(r=>{release=r;}):null);
  try {m.props.value={workspaceId:'w',taskId:'delayed'};await wait();assert.equal(Source.all[0].closed,true);m.props.value={workspaceId:'w2',taskId:'two'};await wait();release(Response.json(task('SECRET_OLD_TASK')));await wait();assert.doesNotMatch(m.host.textContent,/SECRET_OLD_TASK/);assert.match(m.host.textContent,/任务 two/);assert.ok(Source.all.filter(s=>!s.closed).every(s=>s.url.includes('/w2/')));}finally{await m.cleanup();}
});

test('planned and canceled todos are not presented as idle execution and expose no input composer',async()=>{
  for(const planning_state of ['planned','canceled']) {
    const m=await mount(Detail,url=>/\/tasks\/[^/?]+$/.test(url)?Response.json({...task(),planning_state,planning_version:'1',initial_input:null,latest_turn:null,accepts_input:false,input_block_reason:`task_${planning_state}`,actions:{rename:{allowed:true},archive:{allowed:true},stop:{allowed:false}}}):null);
    try {assert.match(m.host.textContent,planning_state==='planned'?/待办：尚未启动/:/待办已取消（未执行）/);assert.equal(m.host.querySelector('textarea'),null);assert.equal(Source.all.length,0);assert.ok(!m.calls.some(c=>c.init.method==='POST'));}finally{await m.cleanup();}
  }
});

test('historical result fetches same-Turn supplement receipts, without claiming adoption',async()=>{
  const m=await mount(Detail,url=>{
    if(url.includes('/turns?'))return Response.json({items:[{id:'old-turn',started_at:'2026-11-17T00:00:00Z',finished_at:'2026-11-17T00:00:07Z'}],next_cursor:null});
    if(url.includes('/reports?')&&url.includes('turn_id=old-turn'))return Response.json({items:[{
      source_turn_id:'old-turn',source_input_id:'starter',outcome:'completed',status:'not_applicable',
      supplement_receipts:[{input_id:'steer-b',input_seq:'2',context_state:'committed'},{input_id:'steer-u',input_seq:'3',context_state:'unknown'}],
      supplement_receipts_truncated:false
    }],next_cursor:null});
    if(url.includes('/reports?'))return Response.json({items:[{source_turn_id:'turn-1',source_input_id:'other',outcome:'completed',status:'admitted',supplement_receipts:[{input_id:'other-steer',input_seq:'5',context_state:'committed'}]}],next_cursor:null});
    if(url.includes('/inputs?'))return Response.json({items:[{id:'later-c',seq:'4',status:'queued',text:'稍后处理',delivery_mode:'queue',turn_id:null}],next_cursor:null});
    return null;
  },{initialTurnId:'old-turn'});
  try {
    const receipts=m.host.querySelector('.process .task-supplements');
    assert.ok(receipts);
    assert.match(receipts.textContent,/输入 5：已进入本轮上下文/);
    assert.doesNotMatch(receipts.textContent,/输入 [234]/);
    assert.match(m.host.querySelector('.task-summary').textContent,/用时 42\.0 秒/);
    button('主会话回报').click();await wait();
    const reports=[...m.host.querySelectorAll('.records .task-supplements')];
    assert.equal(reports.length,2);
    assert.match(reports[0].textContent,/输入 5：已进入本轮上下文/);
    assert.match(reports[1].textContent,/输入 2：已进入本轮上下文/);
    assert.match(reports[1].textContent,/输入 3：接收状态待核对/);
    assert.match(reports[1].textContent,/不代表模型采纳/);
    assert.doesNotMatch(reports[1].textContent,/输入 [45]/);
  }finally{await m.cleanup();}
});

test('work board and task detail open the same paginated, read-only timeline', async () => {
  const item = { linked_task_ids:['one'],id:'matter',title:'对账差异',status:'awaiting_acceptance',current_employee:'原发起人',goal:'核对退款',criteria:'补齐明细',reason:'等待原模型恢复',updated_at:'2026-11-20T09:00:00Z' };
  const requests = (url) => {
    if (url.includes('/work-items/matter')) {
      const older=url.includes('before=12');
      return Response.json({...item,has_more:!older,events:[{id:older?'11':'12',kind:older?'returned':'submitted',employee:'经办员工',target_employee:'',summary:older?'缺少凭证':'已附修订件',created_at:item.updated_at,deliveries:older?[{conversation_id:'g',destination:'对账群',status:'sent'}]:[]}]});
    }
    if (url.includes('/work-items?')) return Response.json({items:[item],next_cursor:null});
    if (url.endsWith('/tasks/one')) return Response.json({...task(),work_item_id:item.id});
    return null;
  };
  const opened = [];
  const board=await mount(Board,requests,{workspace:"w",selected:item.id,onOpenTask:id=>opened.push(id)});
  try {
    assert.deepEqual([...board.host.querySelectorAll('.lane-title')].map(x=>x.textContent),['推进中','待验收 / 等待中','已闭环与终止']);
    assert.match(board.host.textContent,/对账差异/);
    board.host.querySelector('.task-quick-tag').click();
    assert.deepEqual(opened,['one']);
    assert.match(document.body.textContent,/等待原模型恢复/);
    button('查看更早记录').click(); await wait();
    assert.match(document.body.textContent,/缺少凭证/); assert.match(document.body.textContent,/对账群 · 已送达/);
    assert.ok(board.calls.filter(c=>c.url.includes('/work-items')).every(c=>!c.init.method || c.init.method==='GET'));
  } finally { await board.cleanup(); }
  const detail=await mount(Detail,requests);
  try {button('事项时间线').click(); await wait();assert.match(detail.host.textContent,/已附修订件/);}
  finally {await detail.cleanup();}
});


test('authenticated task detail queues supplements but cannot stop during human confirmation or handoff', async () => {
  for (const type of ['human_confirmation', 'work_item']) {
    const m = await mount(Detail, (url) => {
      if (url.endsWith('/sessions/s-one')) {
        const d = task();
        return Response.json({ ...d, id: 's-one', execution: {
          ...d.execution, required_actions: [{ type, request_id: 'holding', title:'挂起中', message:'等待人工确认或交接结果' }],
        }});
      }
      return null;
    });
    try {
      assert.ok(button('停止此任务').disabled);
      assert.match(m.host.textContent, /补充会先排队，暂不能停止/);
      const box = m.host.querySelector('textarea');
      assert.ok(box && !box.disabled);
      box.value = '挂起时的补充要求';
      box.dispatchEvent(new window.Event('input', {bubbles:true})); await nextTick();
      button('发送').click(); await wait();
      const writes = m.calls.filter(c=>c.init.method==='POST').map(c=>c.url);
      assert.deepEqual(writes, ['/api/v1/workspaces/w/sessions/s-one/inputs']);
    } finally { await m.cleanup(); }
  }
});

test('task detail separates root, handoff and delivery state without opening another task', async () => {
  const m = await mount(Detail, url => url.endsWith('/tasks/one') ? Response.json({...task(), progress:{
    phase:'waiting_handoff', assignee:'原员工', handoffs:[{assignee:'财务 AI 员工', phase:'failed'}],
    delivery:[{channel:'dingtalk', phase:'unconfirmed'}],
  }}) : null);
  try {
    const summary = m.host.querySelector('[aria-label="任务状态摘要"]');
    assert.match(summary.textContent, /已交接，等待处理结果/);
    assert.match(summary.textContent, /财务 AI 员工 · 失败/);
    assert.match(summary.textContent, /钉钉 · 发送结果暂未确认/);
    assert.equal(summary.querySelector('a,button'), null);
  } finally { await m.cleanup(); }
});

test('task detail removes turn selector, opens drawer fullscreen, and previews markdown & csv artifacts on right surface', async () => {
  const artifacts = [
    { id: 'art-md', name: '归因报告.md', size: 64 },
    { id: 'art-csv', name: '退货明细.csv', size: 48 },
  ];
  const m = await mount(List, (url) => {
    if (url.endsWith('/tasks/one')) return Response.json({ ...task(), artifacts });
    if (url.endsWith('/tasks/one/artifacts/art-md')) return new Response('# Q3 归因结论\n总额 **184200**', { headers: { 'Content-Type': 'text/markdown' } });
    if (url.endsWith('/tasks/one/artifacts/art-csv')) return new Response('仓别,退货率\n苏州仓,7.4%\n杭州仓,2.0%', { headers: { 'Content-Type': 'text/csv' } });
    return null;
  }, { selectedTaskId: 'one' });
  try {
    assert.equal(document.querySelector('[aria-label="选择执行轮次"]'), null);
    assert.match(document.body.textContent, /归因报告\.md/);
    const expandBtn = [...document.querySelectorAll('button')].find(b => b.textContent.includes('放大全屏'));
    assert.ok(expandBtn);
    expandBtn.click();
    await wait();
    assert.ok(button('← 返回任务列表'));
    assert.ok(button('⤡ 退出全屏'));
    const preview = document.querySelector('[aria-label="产物预览画布"]');
    assert.ok(preview);
    assert.equal(preview.querySelector('h1').textContent, 'Q3 归因结论');
    assert.match(preview.textContent, /184200/);

    const csvSelect = [...document.querySelectorAll('.artifact-row-select')].find(el => el.textContent.includes('退货明细.csv'));
    assert.ok(csvSelect);
    csvSelect.click();
    await wait();
    const table = preview.querySelector('.surface-csv-table');
    assert.ok(table);
    assert.equal(table.querySelectorAll('tbody tr').length, 2);
    assert.match(table.textContent, /苏州仓/);

    button('⤡ 退出全屏').click();
    await wait();
    assert.equal(button('← 返回任务列表'), undefined);
  } finally { await m.cleanup(); }
});

test('header badge and status summary consistently prioritize progress.phase over idle execution state', async () => {
  const cases = [
    ['completed', '已完成'],
    ['waiting_confirmation', '等待人工确认'],
    ['waiting_handoff', '已交接，等待处理结果'],
    ['waiting_resume', '已收到答复，等待继续执行'],
  ];
  for (const [phase, expected] of cases) {
    const m = await mount(Detail, url => url.endsWith('/tasks/one')
      ? Response.json({ ...task(), execution: { ...task().execution, state: 'idle' }, progress: { phase, assignee: '小析' } })
      : null);
    try {
      const badge = m.host.querySelector('.status-badge');
      assert.ok(badge);
      assert.equal(badge.textContent.trim(), expected);
      assert.doesNotMatch(badge.textContent, /当前无执行轮次/);
      const summary = m.host.querySelector('[aria-label="任务状态摘要"]');
      assert.ok(summary);
      assert.match(summary.textContent, new RegExp(expected));
    } finally { await m.cleanup(); }
  }
});

test('svg artifacts are download-only and never rendered as inline image previews', async () => {
  const artifacts = [
    { id: 'art-svg', name: 'architecture.svg', size: 128 },
    { id: 'art-md', name: '总结.md', size: 48 },
  ];
  const m = await mount(List, (url) => {
    if (url.endsWith('/tasks/one')) return Response.json({ ...task(), artifacts });
    if (url.endsWith('/tasks/one/artifacts/art-md')) return new Response('# 默认选中可预览文件', { headers: { 'Content-Type': 'text/markdown' } });
    return null;
  }, { selectedTaskId: 'one' });
  try {
    const svgInlineCard = [...document.querySelectorAll('.artifact-surface-card')].find(el => el.textContent.includes('architecture.svg'));
    assert.ok(svgInlineCard);
    assert.match(svgInlineCard.textContent, /仅下载/);
    assert.doesNotMatch(svgInlineCard.textContent, /预览 →/);

    const expandBtn = [...document.querySelectorAll('button')].find(b => b.textContent.includes('放大全屏'));
    expandBtn.click();
    await wait();
    const preview = document.querySelector('[aria-label="产物预览画布"]');
    assert.equal(preview.querySelector('h1')?.textContent, '默认选中可预览文件');
    assert.ok(!m.calls.some(c => c.url.includes('/artifacts/art-svg')));

    const svgRow = [...document.querySelectorAll('.artifact-row-select')].find(el => el.textContent.includes('architecture.svg'));
    assert.ok(svgRow);
    svgRow.click();
    await wait();
    assert.equal(preview.querySelector('.image-preview-wrap img'), null);
    assert.ok(preview.querySelector('.binary-preview-card'));
    assert.match(preview.textContent, /暂不支持浏览器内联渲染，请直接下载查看/);
    assert.ok(!m.calls.some(c => c.url.includes('/artifacts/art-svg')));
  } finally { await m.cleanup(); }
});

test('session waterfall lands at bottom, auto-loads older on upward scroll while keeping offset, and does not yank on SSE while reading history', async () => {
  let releaseInitial;
  let scrollHeight = 900;
  let olderPrependDelta = 0;
  const msg = (seq, text) => ({ type: 'agent.message', seq: String(seq), session_id: 's-one', message_id: `m-${seq}`, content: [{ type: 'text', text }], occurred_at: '2026-11-26T01:00:00Z' });
  const m = await mount(Detail, (url) => {
    if (url.includes('/events?') && !url.includes('cursor=')) {
      return new Promise(resolve => {
        releaseInitial = () => resolve(Response.json({
          items: [msg(10, '第十条'), msg(11, '第十一条')],
          next_cursor: 'page-older-1',
          resume_cursor: 'H11',
        }));
      });
    }
    if (url.includes('/events?') && url.includes('cursor=page-older-1')) {
      scrollHeight += olderPrependDelta;
      return Response.json({
        items: [msg(5, '第五条'), msg(6, '第六条')],
        next_cursor: 'page-older-2',
        resume_cursor: 'H11',
      });
    }
    if (url.includes('/events?') && url.includes('cursor=page-older-2')) {
      scrollHeight += 300;
      return Response.json({
        items: [msg(1, '第一条')],
        next_cursor: null,
        resume_cursor: 'H11',
      });
    }
    return null;
  });
  try {
    const scroller = m.host.querySelector('.messages');
    assert.ok(scroller);
    Object.defineProperty(scroller, 'scrollHeight', { configurable: true, get: () => scrollHeight });
    Object.defineProperty(scroller, 'clientHeight', { configurable: true, value: 300 });
    assert.equal(typeof releaseInitial, 'function');
    releaseInitial();
    await wait();

    assert.equal(scroller.scrollTop, 900);

    // Scroll into middle history (not near top yet): SSE new message must NOT pull back to bottom
    scroller.scrollTop = 250;
    scroller.dispatchEvent(new window.Event('scroll'));
    await nextTick();

    scrollHeight = 1020;
    const stream = Source.all.find(s => s.url.includes('/s-one/'));
    assert.ok(stream);
    stream.listeners.get('agent.message')?.({
      data: JSON.stringify(msg(12, '第十二条新消息')),
      lastEventId: 'H12',
    });
    await wait();
    assert.equal(scroller.scrollTop, 250);
    assert.match(m.host.textContent, /第十二条新消息/);

    const rowSizes = {'message:m-5':250,'message:m-6':250,'message:m-1':300};
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

    // Scroll near top: should automatically load older page and preserve scroll offset (+500)
    olderPrependDelta = 500;
    scroller.scrollTop = 30;
    scroller.dispatchEvent(new window.Event('scroll'));
    await wait();

    assert.ok(m.calls.some(c => c.url.includes('cursor=page-older-1')));
    assert.match(m.host.textContent, /第五条/);
    assert.equal(scroller.scrollTop, 530);

    // SSE event arriving while still looking at prepended history must NOT pull back to bottom
    scrollHeight += 120;
    stream.listeners.get('agent.message')?.({
      data: JSON.stringify(msg(13, '第十三条新消息')),
      lastEventId: 'H13',
    });
    await wait();
    assert.equal(scroller.scrollTop, 530);

    installRowGeometry();
    // Manual fallback button remains available for earlier page
    const manualBtn = [...m.host.querySelectorAll('button')].find(b => b.textContent.trim() === '加载更早历史消息');
    assert.ok(manualBtn);
    manualBtn.click();
    await wait();
    assert.ok(m.calls.some(c => c.url.includes('cursor=page-older-2')));
    assert.match(m.host.textContent, /第一条/);
    assert.equal(scroller.scrollTop, 830);
  } finally { await m.cleanup(); }
});

test('history prepend preserves the visible event even when live SSE appends during the request', async () => {
  let release;
  const item = (seq, text) => ({type:'agent.message',seq:String(seq),session_id:'s-one',message_id:'m'+seq,content:[{type:'text',text}]});
  const m = await mount(Detail, url => {
    if (!url.includes('/events?')) return null;
    if (url.includes('cursor=older')) return new Promise(resolve => { release = resolve; });
    return Response.json({items:[item(3,'起始可见消息'),item(4,'近期消息'),item(5,'末尾消息')],next_cursor:'older',resume_cursor:'live'});
  });
  try {
    const timeline = m.host.querySelector('.messages');
    const geometry = (top, height) => ({top,bottom:top+height,height});
    const rows = () => [...timeline.querySelectorAll('[data-history-key]')];
    Object.defineProperty(timeline,'clientHeight',{get:()=>80});
    Object.defineProperty(timeline,'scrollHeight',{get:()=>rows().length*40});
    timeline.getBoundingClientRect=()=>geometry(0,80);
    function installLayout() {
      for (const node of rows()) node.getBoundingClientRect=()=>geometry(rows().indexOf(node)*40-timeline.scrollTop,40);
    }
    installLayout();
    timeline.scrollTop=40;
    timeline.dispatchEvent(new window.Event('scroll'));
    timeline.scrollTop=20;
    const anchor = rows()[0];
    assert.equal(anchor.getBoundingClientRect().top,-20);
    // Trigger the actual upward-scroll load, then append while it is pending.
    timeline.dispatchEvent(new window.Event('scroll'));
    await nextTick();
    assert.equal(typeof release,'function');
    Source.all[0].listeners.get('agent.message')({data:JSON.stringify(item(6,'并发来的实时消息')),lastEventId:'live-6'});
    await nextTick();
    installLayout();
    // Newly inserted rows need no mocked geometry for the existing anchor's displacement.
    release(Response.json({items:[item(1,'历史第一页'),item(2,'历史第二条')],next_cursor:null,resume_cursor:'live'}));
    await wait();
    assert.equal(timeline.scrollTop,100);
    assert.equal(anchor.getBoundingClientRect().top,-20);
    assert.deepEqual(rows().map(row=>row.dataset.historyKey),[1,2,3,4,5,6].map(i=>'message:m'+i));
  } finally {await m.cleanup();}
});

test('artifacts surface supports preview/source toggle, copy action, and drawer artifact tab without fullscreen', async () => {
  const prevClipboard = navigator.clipboard;
  const copied = [];
  Object.defineProperty(navigator, 'clipboard', {
    configurable: true,
    value: { writeText: async (text) => { copied.push(text); } },
  });
  const artifacts = [
    { id: 'art-md', name: '归因报告.md', size: 64 },
    { id: 'art-csv', name: '退货明细.csv', size: 48 },
  ];
  const m = await mount(List, (url) => {
    if (url.endsWith('/tasks/one')) return Response.json({ ...task(), artifacts });
    if (url.endsWith('/tasks/one/artifacts/art-md')) return new Response('# Q4 结论\n**正常**', { headers: { 'Content-Type': 'text/markdown' } });
    if (url.endsWith('/tasks/one/artifacts/art-csv')) return new Response('仓别,退货率\n苏州仓,7.4%', { headers: { 'Content-Type': 'text/csv' } });
    return null;
  }, { selectedTaskId: 'one' });
  try {
    assert.ok(m.host.querySelector('.task-id-code'));
    assert.match(m.host.querySelector('.task-id-code').textContent, /^#one$/);
    const artifactTab = [...document.querySelectorAll('.drawer-nav button')].find(b => b.textContent.includes('产物详情'));
    assert.ok(artifactTab);
    artifactTab.click();
    await wait();

    const preview = document.querySelector('[aria-label="产物预览画布"]');
    assert.ok(preview);
    assert.equal(preview.querySelector('h1').textContent, 'Q4 结论');
    button('源码').click();
    await nextTick();
    assert.match(preview.querySelector('.surface-code-block').textContent, /# Q4 结论/);
    button('复制内容').click();
    await wait();
    assert.deepEqual(copied, ['# Q4 结论\n**正常**']);

    const csvBtn = [...document.querySelectorAll('.artifact-row-select')].find(el => el.textContent.includes('退货明细.csv'));
    csvBtn.click();
    await wait();
    assert.match(preview.querySelector('.surface-csv-table').textContent, /苏州仓/);
    button('源码').click();
    await nextTick();
    assert.match(preview.querySelector('.surface-code-block').textContent, /仓别,退货率/);
  } finally {
    Object.defineProperty(navigator, 'clipboard', { configurable: true, value: prevClipboard });
    await m.cleanup();
  }
});

test('task list colors current work before the latest finished turn and keeps both indicators in sync', async () => {
  const cases = [
    ['queued', 'failed', {}, 'info', 'running'],
    ['running', 'failed', {}, 'info', 'running'],
    ['stopping', 'completed', {}, 'info', 'running'],
    ['blocked', 'failed', {}, 'warning', 'blocked'],
    ['idle', 'failed', {required_actions:[{type:'human_confirmation',message:'请确认'}]}, 'warning', 'blocked'],
    ['idle', 'completed', {required_actions:[{type:'work_item',message:'等待交接'}]}, 'warning', 'blocked'],
    ['idle', 'failed', {background_process_count:1}, 'info', 'running'],
    ['idle', 'completed', {}, 'success', 'completed'],
    ['idle', 'failed', {}, 'danger', 'failed'],
  ];
  const items = cases.map(([state, status, pending], i) => {
    const t = task('tone-' + i);
    return {...t, execution:{...t.execution, state, ...pending}, latest_turn:{...t.latest_turn,status}};
  });
  const m = await mount(List, url => url.includes('/tasks?') ? Response.json({items,next_cursor:null}) : null);
  try {
    const rows = [...m.host.querySelectorAll('tbody tr')];
    assert.equal(rows.length,cases.length);
    cases.forEach(([, status, , tone, dot], i) => {
      assert.equal(rows[i].querySelector('.status-pill').className,'status-pill ' + tone);
      assert.equal(rows[i].querySelector('.row-dot').className,'row-dot ' + dot);
      assert.match(rows[i].querySelector('.stage-subline').textContent,status === 'failed' ? /最近：.*失败/s : /最近：.*本轮完成/s);
    });
  } finally {await m.cleanup();}
});

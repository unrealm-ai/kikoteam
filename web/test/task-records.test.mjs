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

test('task center honors independent input, stop and archive permissions, including historical turns', async () => {
  const m = await mount(Detail, url => url.endsWith('/tasks/one') ? Response.json({...task(), accepts_input:true,
    actions:{supplement:{allowed:false,reason:'system_managed_input'},rename:{allowed:true}, stop:{allowed:true}, archive:{allowed:false}}}) : null, {taskCenter:true});
  try {
    assert.ok(document.querySelector('textarea').disabled);
    assert.match(m.host.textContent,/此任务的输入由系统派发/);
    assert.equal(button('停止此任务').disabled,false);
    assert.ok(button('归档任务').disabled);
    m.props.value = {...m.props.value,initialTurnId:'old-turn'};
    await wait();
    assert.equal(document.querySelector('textarea'),null);
    assert.equal(button('停止此任务'),undefined);
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
test('historical report selection binds history and stream to its Turn and hides current composer',async()=>{
  const m=await mount(Detail,()=>null,{initialTurnId:'old-turn'});
  try {assert.ok(m.calls.some(c=>c.url.includes('/events?')&&c.url.includes('turn_id=old-turn')));assert.match(Source.all[0].url,/turn_id=old-turn/);assert.equal(m.host.querySelector('textarea'),null);assert.match(m.host.textContent,/最近：本轮完成/);assert.doesNotMatch(m.host.textContent,/任务已完成/);}finally{await m.cleanup();}
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
    assert.match(receipts.textContent,/输入 2：已进入本轮上下文/);
    assert.match(receipts.textContent,/输入 3：接收状态待核对/);
    assert.match(receipts.textContent,/不代表模型采纳/);
    assert.doesNotMatch(receipts.textContent,/输入 [45]/);
    button('主会话回报').click();await wait();
    assert.match(m.host.querySelector('.records .task-supplements').textContent,/输入 5：已进入本轮上下文/);
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

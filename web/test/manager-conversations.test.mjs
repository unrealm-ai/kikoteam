import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
import {Window} from 'happy-dom';
import {parse,compileScript} from '@vue/compiler-sfc';
import esbuild from 'esbuild';
const window=new Window({url:'http://127.0.0.1:5173/'});
for(const name of ['window','document','navigator','HTMLElement','Element','SVGElement','Node','Text','Comment','Image','InputEvent','MouseEvent','KeyboardEvent','HTMLInputElement','Document','MutationObserver','ResizeObserver','getComputedStyle','requestAnimationFrame','cancelAnimationFrame']){
 if(name==='window')globalThis.window=window;else if(window[name]!==undefined)Object.defineProperty(globalThis,name,{configurable:true,writable:true,value:typeof window[name]==='function'&&['getComputedStyle','requestAnimationFrame','cancelAnimationFrame'].includes(name)?window[name].bind(window):window[name]});
}
const built=await esbuild.build({stdin:{contents:"export {default as PlatformConversationsView} from './src/direction2/views/PlatformConversationsView.vue'; export {mergeEvents, projectEvents, managerApi} from './src/direction2/api/manager'; export {activeWorkspaceId} from './src/direction2/api/client'; export {createApp,h,nextTick} from 'vue'; export {NMessageProvider,NDialogProvider} from 'naive-ui';",resolveDir:process.cwd(),loader:'ts'},bundle:true,write:false,format:'esm',platform:'node',define:{'import.meta.env.VITE_PLATFORM_REAL':'true','import.meta.env.DEV':'false'},plugins:[{name:'vue-test',setup(build){build.onLoad({filter:/\.vue$/},async({path})=>{const source=await fs.readFile(path,'utf8');const {descriptor}=parse(source);return {contents:compileScript(descriptor,{id:path,inlineTemplate:true}).content,loader:'ts'};});}}]});
const {PlatformConversationsView,mergeEvents,projectEvents,managerApi,activeWorkspaceId,createApp,h,NMessageProvider,NDialogProvider,nextTick}=await import(`data:text/javascript;base64,${Buffer.from(built.outputFiles[0].text).toString('base64')}`);
const pause=async()=>{await new Promise(r=>setTimeout(r,50));await nextTick()};
class FakeSource {
 static all=[];
 constructor(url){this.url=url;this.closed=false;this.listeners=new Map();FakeSource.all.push(this)}
 addEventListener(type,fn){this.listeners.set(type,fn)}
 close(){this.closed=true}
 emit(value,id='cursor-next'){this.listeners.get(value.type)?.({data:JSON.stringify(value),lastEventId:id})}
}
globalThis.EventSource=FakeSource;
const agent={id:'a',name:'测试员工',role:'工程',avatar:'cn-v2-01',enabled:true};
const resource=id=>({id,task_id:id==='chat'?null:`business-${id}`,conversation_id:'c',title:id,mode:id==='chat'?'chat':'task',lifecycle:'active',accepts_input:true,execution:{state:'idle',active_turn_id:null,queued_input_count:0,background_process_count:0,block_reason:null}});
const taskDetail=id=>({task_id:id,session_id:id.replace('business-',''),conversation_id:'c',title:id,agent:{id:'a',name:'测试员工',avatar:'cn-v2-01'},source:{kind:'chat_delegation',chat_session_id:'chat',chat_input_id:null},lifecycle:'active',execution:resource('chat').execution,latest_turn:null,latest_input:null,created_at:'2026-11-17T01:00:00Z',revision:'1',accepts_input:true,input_block_reason:null,initial_input:null,latest_result:null,actions:{rename:{allowed:true},archive:{allowed:true},stop:{allowed:true}}});
const event=(seq,session='chat',text='内容')=>({type:'agent.message',seq:String(seq),session_id:session,message_id:`m${seq}`,content:[{type:'text',text}],status:'completed'});
const button=text=>[...document.querySelectorAll('button')].find(b=>(b.getAttribute('aria-label') || b.textContent.trim())===text);
async function mount({chat='chat',override,props={}}={}) {
 const calls=[];FakeSource.all=[];
 globalThis.fetch=async(url,init={})=>{
  calls.push({url,init});const response=await override?.(url,init);if(response)return response;
  if(url.endsWith('/assistant'))return Response.json(props.assistantEntry ?? {cards:[]});
  if(url.includes('/agents?'))return Response.json({items:[agent],total:1,page:1,page_size:20});
  if(url.endsWith('/web-direct')||url.endsWith('/conversations/c'))return Response.json({id:'c',agent_id:'a',chat_session_id:chat,lifecycle:'active'});
  if(url.includes('/tasks?'))return Response.json({items:[taskDetail('business-task1'),taskDetail('business-task2')],next_cursor:null});
  if(/\/tasks\/[^/]+\/reports/.test(url)||/\/(inputs|turns)\?/.test(url))return Response.json({items:[],next_cursor:null});
  if(/\/tasks\/[^/?]+$/.test(url))return Response.json(taskDetail(url.split('/').pop()));
  if(url.includes('/events?')){const id=url.match(/sessions\/([^/]+)/)[1];return Response.json({items:[event(10,id,'最近消息')],next_cursor:null,resume_cursor:`resume-${id}`})}
  if(url.endsWith('/sessions')&&init.method)return Response.json(resource('chat'));
  if(url.endsWith('/inputs'))return Response.json({id:'input'});
  if(url.endsWith('/stop'))return Response.json({...resource('chat'),execution:{...resource('chat').execution,state:'stopping'}});
  return Response.json(resource(url.split('/').pop()));
 };
 activeWorkspaceId.value='w';const host=document.createElement('div');document.body.appendChild(host);
 const app=createApp({render:()=>h(NMessageProvider,null,{default:()=>h(NDialogProvider,null,{default:()=>h(PlatformConversationsView,props)})})});app.mount(host);await pause();await pause();
 return {calls,host,async cleanup(){app.unmount();host.remove();await pause()}};
}
test('fixed direct conversation uses cookie CSRF and latest history; no new/archive entry',async()=>{
 const m=await mount();try{assert.match(m.host.textContent,/最近消息/);assert.equal(button('新建会话'),undefined);assert.equal(button('归档'),undefined);assert.ok(m.calls.some(c=>c.url.includes('direction=backward')));const c=m.calls.find(c=>c.url.endsWith('/web-direct'));assert.equal(c.init.headers.get('X-CSRF-Protection'),'1');assert.equal(c.init.credentials,'include');assert.equal(c.init.headers.has('Authorization'),false);assert.equal(FakeSource.all.length,1);assert.match(FakeSource.all[0].url,/after=resume-chat/);}finally{await m.cleanup()}assert.ok(FakeSource.all.every(s=>s.closed));
});
test('returning to a source Conversation reads its exact identity without creating a fixed replacement',async()=>{
 const m=await mount({props:{selectedEmployeeId:'a',selectedConversationId:'source-conversation'},override:url=>url.endsWith('/conversations/source-conversation')?Response.json({id:'source-conversation',agent_id:'a',chat_session_id:'original-chat',lifecycle:'active'}):null});
 try{assert.ok(m.calls.some(c=>c.url.endsWith('/conversations/source-conversation')));assert.ok(m.calls.some(c=>c.url.includes('/original-chat/events?')));assert.ok(!m.calls.some(c=>c.url.endsWith('/web-direct')));}finally{await m.cleanup()}
});
test('one task detail stream at a time, main stays connected; closing never stops task',async()=>{
 const m=await mount();try{button('任务与文件').click();await pause();button('会话任务 (2)').click();await pause();[...document.querySelectorAll('button')].find(b=>b.textContent.includes('task1')).click();await pause();assert.equal(FakeSource.all.filter(s=>!s.closed).length,2);[...document.querySelectorAll('button')].find(b=>b.textContent.includes('task2')).click();await pause();assert.equal(FakeSource.all.filter(s=>!s.closed).length,2);assert.equal(FakeSource.all.find(s=>s.url.includes('/task1/')).closed,true);button('关闭任务详情').click();await pause();assert.equal(FakeSource.all.filter(s=>!s.closed).length,1);assert.ok(!m.calls.some(c=>c.url.endsWith('/stop')));}finally{await m.cleanup()}
});
test('old pages never rewind SSE cursor; duplicate events and message snapshots merge',async()=>{
 const m=await mount({override:(url)=>url.includes('/events?')?Response.json(url.includes('cursor=older')?{items:[event(1)],next_cursor:null,resume_cursor:'H'}:{items:[event(10)],next_cursor:'older',resume_cursor:'H'}):null});try{const stream=FakeSource.all[0];stream.emit(event(11),'H11');stream.emit(event(11),'H11');await pause();button('加载更早历史消息').click();await pause();assert.equal(FakeSource.all.length,1);assert.equal(m.host.querySelectorAll('article').length,3);assert.match(stream.url,/after=H/);}finally{await m.cleanup()}
 const e=event(1);assert.equal(mergeEvents([e],[e],'chat').length,1);assert.throws(()=>mergeEvents([],[e],'other'));assert.equal(projectEvents([e,{...e,seq:'2',type:'input.updated',content:[{type:'text',text:'更新'}]}]).length,1);
});
test('first send retry reuses idempotency key and body after unknown network result',async()=>{
 let tries=0;const m=await mount({chat:null,override:(url,init)=>{if(url.endsWith('/sessions')&&init.method&&++tries===1)throw new Error('network lost');return null}});try{const input=document.querySelector('textarea');input.value='do work';input.dispatchEvent(new window.Event('input',{bubbles:true}));await pause();button('发送').click();await pause();assert.match(m.host.textContent,/network lost/);button('重试发送').click();await pause();const calls=m.calls.filter(c=>c.url.endsWith('/sessions')&&c.init.method);assert.equal(calls.length,2);assert.equal(calls[0].init.headers.get('Idempotency-Key'),calls[1].init.headers.get('Idempotency-Key'));assert.equal(calls[0].init.body,calls[1].init.body);}finally{await m.cleanup()}
});
test('clear old streams and stale histories on workspace change',async()=>{
 let finish;const m=await mount();try{const old=FakeSource.all[0];const original=fetch;globalThis.fetch=async(url,init)=>url.includes('/w2/')&&url.includes('/events?')?new Promise(r=>{finish=r}):original(url,init);activeWorkspaceId.value='w2';await pause();assert.equal(old.closed,true);activeWorkspaceId.value='w3';await pause();finish(Response.json({items:[event(20,'chat','STALE')],next_cursor:null,resume_cursor:'OLD'}));await pause();assert.doesNotMatch(m.host.textContent,/STALE/);assert.ok(FakeSource.all.filter(s=>!s.closed).every(s=>s.url.includes('/w3/')));}finally{await m.cleanup()}
});
test('concurrent chat creation conflict submits once to winner without inventing another conversation',async()=>{
 let creates=0;const m=await mount({chat:null,override:(url,init)=>{
  if(url.endsWith('/sessions')&&init.method){creates++;return Response.json({error:{code:'chat_session_exists',message:'exists'}},{status:409})}
  if(url.endsWith('/conversations/c'))return Response.json({id:'c',chat_session_id:'winner'});
  return null;
 }});try{const input=document.querySelector('textarea');input.value='hello';input.dispatchEvent(new window.Event('input',{bubbles:true}));await pause();button('发送').click();button('发送')?.click();await pause();assert.equal(creates,1);assert.equal(m.calls.filter(c=>c.url.endsWith('/winner/inputs')).length,1);assert.equal(m.calls.filter(c=>c.url.endsWith('/web-direct')).length,1);}finally{await m.cleanup()}
});
test('revoked subscription closes and clears history, does not blindly reconnect',async()=>{
 const m=await mount();try{assert.match(m.host.textContent,/最近消息/);const source=FakeSource.all[0];globalThis.fetch=async()=>Response.json({error:{code:'forbidden',message:'已撤权'}},{status:403});source.emit({type:'stream.error',message:'已撤权'},'');await pause();assert.equal(source.closed,true);assert.doesNotMatch(m.host.textContent,/最近消息/);assert.match(m.host.textContent,/已撤权/);assert.equal(FakeSource.all.length,1);}finally{await m.cleanup()}
});
test('native confirmation is restored from the session and submits an explicit negative answer', async () => {
 const request = {type:'confirmation',request_id:'question',turn_id:'turn',title:'报告范围',message:'包含海外订单吗？'};
 let actions=[request];
 const m=await mount({override:(url, init)=>{
   if(url.endsWith('/sessions/chat')) return Response.json({...resource('chat'),execution:{...resource('chat').execution,required_actions:actions}});
   if(url.endsWith('/chat/inputs')) { assert.deepEqual(JSON.parse(init.body),{confirmation:{request_id:'question',confirmed:false}}); actions=[];return Response.json({id:'answer'}); }
 }});
 try {
   assert.match(m.host.textContent,/包含海外订单吗/);
   button('否').click();await pause();
   assert.equal(button('是'),undefined);
   FakeSource.all[0].emit({type:'session.requires_action',session_id:'chat',seq:'11',required_actions:[request]});await pause();
   assert.equal(button('是'),undefined);
 } finally { await m.cleanup(); }
});
test('main input steers while running and stop is a request, not task completion',async()=>{
 const m=await mount({override:(url,init)=>!init.method&&url.endsWith('/sessions/chat')?Response.json({...resource('chat'),execution:{...resource('chat').execution,state:'running',active_turn_id:'turn'}}):null});try{const input=document.querySelector('textarea');assert.equal(input.disabled,false);assert.equal(m.host.querySelector('[aria-label="输入处理方式"]'),null);assert.ok(button('发送').querySelector('svg'));assert.ok(button('停止主对话').querySelector('svg'));input.value='next request';input.dispatchEvent(new window.Event('input',{bubbles:true}));await pause();button('发送').click();await pause();const [inputRequest]=m.calls.filter(c=>c.url.endsWith('/chat/inputs'));assert.equal(JSON.parse(inputRequest.init.body).delivery_mode,'steer');button('停止主对话').click();await pause();assert.match(m.host.textContent,/停止请求已受理/);assert.ok(!m.calls.some(c=>c.url.includes('/task1/stop')));}finally{await m.cleanup()}
});
test('transport reconnect resumes from last reliable ID rather than history cursor',async()=>{
 const m=await mount();try{const source=FakeSource.all[0];source.emit(event(11),'last-reliable');source.onerror();await new Promise(r=>setTimeout(r,2150));await pause();assert.equal(source.closed,true);assert.equal(FakeSource.all.length,2);assert.match(FakeSource.all[1].url,/after=last-reliable/);}finally{await m.cleanup()}
});
test('invalid stream cursor stops with reload action rather than reconnect loop',async()=>{
 const m=await mount();try{const source=FakeSource.all[0];source.emit({type:'stream.error',code:'invalid_cursor',message:'游标失效'},'');await pause();assert.equal(source.closed,true);assert.match(m.host.textContent,/游标失效/);assert.equal(FakeSource.all.length,1);}finally{await m.cleanup()}
});
test('task pagination becomes available after initial empty list grows beyond one page',async()=>{
 let many=false;const m=await mount({override:url=>url.includes('/tasks?')?Response.json(many?{items:Array.from({length:50},(_,i)=>taskDetail(`business-task${i}`)),next_cursor:'older-tasks'}:{items:[],next_cursor:null}):null});try{button('任务与文件').click();await pause();button('会话任务 (0)').click();await pause();assert.equal(button('加载更多任务'),undefined);many=true;await new Promise(r=>setTimeout(r,1050));document.dispatchEvent(new window.Event('visibilitychange'));await pause();assert.ok(button('加载更多任务'));}finally{await m.cleanup()}
});
test('late conflict lookup never submits input after switching workspace',async()=>{
 let finish;const m=await mount({chat:null,override:(url,init)=>{
  if(url.endsWith('/sessions')&&init.method)return Response.json({error:{code:'chat_session_exists',message:'exists'}},{status:409});
  if(url.endsWith('/conversations/c'))return new Promise(r=>{finish=r});return null;
 }});try{const input=document.querySelector('textarea');input.value='old draft';input.dispatchEvent(new window.Event('input',{bubbles:true}));await pause();button('发送').click();await pause();activeWorkspaceId.value='new';await pause();finish(Response.json({id:'c',chat_session_id:'winner'}));await pause();assert.equal(m.calls.filter(c=>c.url.endsWith('/winner/inputs')).length,0);}finally{await m.cleanup()}
});
test('repeated SSE HTTP handshake failures stop even when resource GET still succeeds',async()=>{
 const m=await mount();try{for(let i=0;i<3;i++){FakeSource.all.at(-1).onerror();await new Promise(r=>setTimeout(r,2100))}FakeSource.all.at(-1).onerror();await pause();assert.equal(FakeSource.all.filter(s=>!s.closed).length,0);assert.match(m.host.textContent,/连续重连失败/);assert.equal(FakeSource.all.length,4);}finally{await m.cleanup()}
});
test('Chinese IME confirmation does not send a message',async()=>{
 const m=await mount();try{const input=document.querySelector('textarea');input.value='中文';input.dispatchEvent(new window.Event('input',{bubbles:true}));input.dispatchEvent(new window.KeyboardEvent('keydown',{key:'Enter',isComposing:true,bubbles:true}));await pause();assert.equal(m.calls.filter(c=>c.url.endsWith('/inputs')).length,0);}finally{await m.cleanup()}
});

test('main conversation client exposes no generic archive operation',()=>{
 assert.equal('archive' in managerApi,false);
});

test('manually archived fixed conversation retains history and never creates replacement',async()=>{
 const m=await mount({override:url=>url.endsWith('/web-direct')?Response.json({id:'c',agent_id:'a',chat_session_id:'chat',lifecycle:'archived'}):null});try{assert.match(m.host.textContent,/已归档/);assert.match(m.host.textContent,/最近消息/);assert.equal(document.querySelector('textarea').disabled,true);assert.equal(m.calls.filter(c=>c.url.endsWith('/sessions')&&c.init.method).length,0);}finally{await m.cleanup()}
});
test('archived empty fixed conversation does not offer first-send creation',async()=>{
 const m=await mount({chat:null,override:url=>url.endsWith('/web-direct')?Response.json({id:'c',agent_id:'a',chat_session_id:null,lifecycle:'archived'}):null});try{assert.match(m.host.textContent,/已归档/);assert.equal(document.querySelector('textarea'),null);assert.equal(button('发送'),undefined);assert.equal(m.calls.filter(c=>c.url.endsWith('/sessions')&&c.init.method).length,0);}finally{await m.cleanup()}
});
test('archived main chat remains readable and disabled inside active conversation',async()=>{
 const m=await mount({override:(url,init)=>!init.method&&url.endsWith('/sessions/chat')?Response.json({...resource('chat'),lifecycle:'archived',accepts_input:false,input_block_reason:'session_archived'}):null});try{assert.match(m.host.textContent,/此会话已归档/);assert.match(m.host.textContent,/最近消息/);assert.equal(document.querySelector('textarea').disabled,true);assert.equal(button('新建会话'),undefined);}finally{await m.cleanup()}
});

const toolEvent = (changes = {}) => ({type:'agent.tool_result',session_id:'chat',seq:'11',tool_use_id:'tool-1',name:'task_delegation_create_session',status:'completed',input_state:'available',input:{title:'销售分析',instruction:'分析销售明细'},output_state:'available',output:{structuredContent:{structuredContent:{session_id:'task1',execution:{state:'queued'}}}},...changes});
const reportEvent = (changes = {}) => ({type:'input.updated',session_id:'chat',seq:'12',message_id:'report',input_id:'report-input',turn_id:'report-turn',source_kind:'task_report',status:'processed',content:[{type:'text',text:'平台任务回报\n'+JSON.stringify({session_id:'task1',outcome:'completed',result:'# 原始分析\n总额 **3375**',background_process_count:0,queued_input_count:0,has_unknown_tool_results:false})}],...changes});
test('closed task drawer shows only the linked active turn progress and measured counts', async()=>{
 let state='running';
 const progress={turn_id:'turn-a',seq:'32',occurred_at:'2026-11-20T08:00:00Z',plan_version:'1',summary:'核对第四段素材',steps:[{id:'read',label:'核对素材',status:'in_progress'}],metric:{step_id:'read',done:3,total:6,unit:'个'}};
 const m=await mount({override:url=>{
  if(url.includes('/tasks?'))return Response.json({items:[{...taskDetail('business-task1'),execution:{...resource('task1').execution,state,active_turn_id:'turn-a',required_actions:state==='blocked'?[{type:'confirmation',message:'请选择配音'}]:[]},reported_progress:progress}],next_cursor:null});
  if(url.includes('/sessions/chat/events?'))return Response.json({items:[toolEvent()],resume_cursor:'H',next_cursor:null});
  return null;
 }});
 try{
  const card=m.host.querySelector('.task-action');
  assert.equal(card.querySelector('header .tool-status').textContent,'执行中');
  assert.match(card.textContent,/核对第四段素材/);
  assert.equal(card.querySelector('progress').value,3);
  assert.equal(card.querySelector('.step-progress details').open,false);
  assert.equal(FakeSource.all.length,1);
  state='blocked';
  await new Promise(r=>setTimeout(r,1050));document.dispatchEvent(new window.Event('visibilitychange'));await pause();
  assert.match(card.textContent,/请选择配音/);
  assert.equal(card.querySelector('progress'),null);
 }finally{await m.cleanup()}
});

test('task report previews only its authorized published PNG versions', async()=>{
 const a='00000000-0000-4000-8000-000000000001',task='00000000-0000-4000-8000-000000000002';
 const body={session_id:'task1',task_id:task,outcome:'completed',result:'已发布图片',artifact_candidates:[
   {artifact_id:a,name:'poster.png',media_type:'image/png',url:'https://example.com/foreign'},
   {artifact_id:'../other',name:'invalid.png',media_type:'image/png'}]};
 const row=reportEvent({content:[{type:'text',text:JSON.stringify(body)}]});
 const m=await mount({override:url=>url.includes('/events?')?Response.json({items:[row],next_cursor:null,resume_cursor:'H'}):null});
 try{const imgs=m.host.querySelectorAll('.report-card img');assert.equal(imgs.length,1);
 const src=imgs[0].getAttribute('src');assert.ok(src.startsWith('/api/v1/workspaces/'));assert.ok(src.endsWith('/tasks/'+task+'/artifacts/'+a+'/content'));
 assert.equal(m.host.querySelector('.report-card a[download]').getAttribute('href'),src+'?download=1');
 }finally{await m.cleanup();}
});
test('Markdown renders headings, tables and code without executing HTML or loading embedded media',async()=>{
 const markdown = '# 销售报告\n\n**合计**：3375\n\n| 商品 | 金额 |\n| --- | ---: |\n| A | 100 |\n\n```json\n{"total":3375}\n```\n\n<script>alert(1)</script>\n<img src=x onerror=alert(1)>\n\n[危险](javascript:alert%281%29) [本地](file:///etc/passwd) [远程](https://example.com/report) ![跟踪图片](https://example.com/track.png)';
 const m=await mount({override:url=>url.includes('/events?')?Response.json({items:[event(10,'chat',markdown)],next_cursor:null,resume_cursor:'H'}):null});
 try{const content=m.host.querySelector('.session-markdown');assert.equal(content.querySelector('h1').textContent,'销售报告');assert.equal(content.querySelectorAll('table tbody tr').length,1);assert.equal(content.querySelector('pre code').textContent.trim(),'{"total":3375}');assert.equal(content.querySelectorAll('script,img,iframe').length,0);assert.equal(content.querySelectorAll('a').length,1);const link=content.querySelector('a');assert.equal(link.getAttribute('rel'),'noopener noreferrer');assert.equal(link.getAttribute('target'),'_blank');assert.match(content.textContent,/跟踪图片/);}finally{await m.cleanup()}
});
test('task detail consumes SSE progress without fetching the task again or rolling back to an old plan',async()=>{
 const execution={...resource('task1').execution,state:'running',active_turn_id:'turn-a'};
 const snapshot={turn_id:'turn-a',seq:'22',occurred_at:'2026-11-28T01:00:00Z',plan_version:'1',summary:'正在整理脚本',steps:[],metric:null};
 const record={...taskDetail('business-task1'),execution,reported_progress:snapshot};
 const m=await mount({override:(url,init)=>{
  if(url.includes('/tasks?'))return Response.json({items:[record],next_cursor:null});
  if(url.endsWith('/tasks/business-task1'))return Response.json(record);
  if(url.endsWith('/sessions/task1')&&!init.method)return Response.json({...resource('task1'),execution});
  if(url.includes('/chat/events?'))return Response.json({items:[toolEvent()],resume_cursor:'H',next_cursor:null});
  return null;
 }});
 try{
  button('查看任务 ↗').click();await pause();await pause();
  const panel=m.host.querySelector('.task-detail');
  assert.match(panel.textContent,/正在整理脚本/);
  const stream=FakeSource.all.find(s=>!s.closed&&s.url.includes('/sessions/task1/'));
  const before=m.calls.filter(c=>c.url.endsWith('/tasks/business-task1')).length;
  const event={...snapshot,type:'task.progress_updated',session_id:'task1',source:'agent'};
  stream.emit({...event,seq:'25',summary:'正在校对字幕'},'cursor25');await pause();
  stream.emit({...event,seq:'24',summary:'旧步骤'},'cursor24');await pause();
  assert.match(panel.textContent,/正在校对字幕/);assert.doesNotMatch(panel.textContent,/旧步骤/);
  assert.equal(m.calls.filter(c=>c.url.endsWith('/tasks/business-task1')).length,before);
 }finally{await m.cleanup()}
});
test('trigger card opens only a known task; input and output details use separate tabs',async()=>{
 const m=await mount({override:url=>url.includes('/chat/events?')?Response.json({items:[toolEvent()],next_cursor:null,resume_cursor:'H'}):null});
 try{const card=m.host.querySelector('.task-action');assert.match(card.textContent,/触发任务/);assert.match(card.textContent,/已触发/);assert.equal(card.querySelector('.tool-details').open,false);card.querySelector('.tool-details').open=true;assert.equal(card.querySelector('[role=tabpanel]').textContent.includes('task1'),true);card.querySelector('[role=tab]').click();await pause();assert.match(card.querySelector('[role=tabpanel]').textContent,/销售分析/);button('查看任务 ↗').click();await pause();assert.equal(FakeSource.all.filter(s=>!s.closed).length,2);assert.ok(FakeSource.all.find(s=>s.url.includes('/task1/')));assert.equal(m.calls.filter(c=>c.init.method&&c.url.endsWith('/inputs')).length,0);}finally{await m.cleanup()}
});
test('hidden tool data never renders or creates task navigation, and nested MCP errors stay failures',async()=>{
 const m=await mount({override:url=>url.includes('/events?')?Response.json({items:[toolEvent({input_state:'redacted',input:{title:'SECRET_INPUT'},output_state:'forbidden',output:{session_id:'task1',secret:'SECRET_OUTPUT'}}),toolEvent({seq:'12',tool_use_id:'failed-tool',output:{isError:true,structuredContent:{content:[{type:'text',text:'backend refused'}]}}})],next_cursor:null,resume_cursor:'H'}):null});
 try{const cards=m.host.querySelectorAll('.tool-card');assert.doesNotMatch(m.host.textContent,/SECRET_INPUT|SECRET_OUTPUT/);assert.equal(m.host.querySelector('.task-link'),null);assert.match(cards[0].textContent,/调用结束/);assert.match(cards[0].textContent,/无权查看/);cards[0].querySelector('[role=tab]').click();await pause();assert.match(cards[0].textContent,/内容已隐藏/);assert.match(cards[1].textContent,/执行失败/);assert.doesNotMatch(cards[1].textContent,/正在向员工发起任务/);}finally{await m.cleanup()}
});
test('command results expose stderr, exit code and incomplete output without claiming success',async()=>{
 const m=await mount({override:url=>url.includes('/events?')?Response.json({items:[toolEvent({name:'bash_start',input:{command:'python analyze.py',cwd:'/project'},output:{structuredContent:{state:'exited',exit_code:2,chunks:[{stream:'stderr',text:'missing input'}],has_more:true}}})],next_cursor:null,resume_cursor:'H'}):null});
 try{assert.match(m.host.textContent,/执行命令/);assert.match(m.host.textContent,/执行失败/);assert.match(m.host.textContent,/退出码 2/);assert.match(m.host.textContent,/\[stderr\] missing input/);assert.match(m.host.textContent,/当前输出不完整/);m.host.querySelector('[role=tab]').click();await pause();assert.match(m.host.querySelector('[role=tabpanel]').textContent,/python analyze.py/);assert.match(m.host.querySelector('[role=tabpanel]').textContent,/工作目录/);}finally{await m.cleanup()}
});
test('report folds only its own reply and keeps original result accessible after SSE snapshots',async()=>{
 const m=await mount({override:url=>url.includes('/chat/events?')?Response.json({items:[reportEvent()],next_cursor:null,resume_cursor:'H'}):null});
 try{assert.equal(m.host.querySelector('.report-original').open,true);const reply={...event(13,'chat','## 分析摘要\n总额 3375'),input_id:'report-input',turn_id:'report-turn'};FakeSource.all[0].emit(reply);await pause();const report=m.host.querySelector('.report-card');assert.equal(report.querySelector('.report-original').open,false);assert.equal(report.querySelector('.report-answer h2').textContent,'分析摘要');assert.equal(m.host.querySelectorAll('article').length,1);FakeSource.all[0].emit({...reply,seq:'14',content:[{type:'text',text:'## 更新摘要'}]});await pause();assert.equal(report.querySelector('.report-answer h2').textContent,'更新摘要');assert.equal(report.querySelector('.report-original h1').textContent,'原始分析');button('查看任务与执行过程 ↗').click();await pause();assert.equal(FakeSource.all.filter(s=>!s.closed).length,2);}finally{await m.cleanup()}
});
test('unrelated replies and interleaved user inputs never fold into a task report',async()=>{
 const messages=[reportEvent(),{...event(13,'chat','无关回答'),input_id:'other-input',turn_id:'report-turn'},{...event(14,'chat','补充要求'),type:'input.updated',source_kind:'user',input_id:'new-input',turn_id:'report-turn'},{...event(15,'chat','后续回复'),input_id:'report-input',turn_id:'report-turn'}];
 const m=await mount({override:url=>url.includes('/events?')?Response.json({items:messages,next_cursor:null,resume_cursor:'H'}):null});
 try{assert.equal(m.host.querySelectorAll('article').length,4);assert.equal(m.host.querySelector('.report-answer'),null);assert.equal(m.host.querySelector('.report-original').open,true);assert.match(m.host.textContent,/无关回答/);assert.match(m.host.textContent,/后续回复/);}finally{await m.cleanup()}
});
test('failed, unknown and malformed task reports stay visible without a success claim',async()=>{
 const reports=[reportEvent({content:[{type:'text',text:JSON.stringify({session_id:'task1',outcome:'failed',result:'工具失败',has_unknown_tool_results:true,background_process_count:null,queued_input_count:2})}]}),reportEvent({seq:'13',message_id:'bad-report',content:[{type:'text',text:'bad json'}]})];
 const m=await mount({override:url=>url.includes('/events?')?Response.json({items:reports,next_cursor:null,resume_cursor:'H'}):null});
 try{assert.equal(m.host.querySelector('.report-card.successful'),null);assert.match(m.host.textContent,/结果未知的工具调用/);assert.match(m.host.textContent,/后台进程状态待确认/);assert.match(m.host.textContent,/2 条排队输入/);assert.match(m.host.textContent,/回报格式暂不可识别/);assert.match(m.host.textContent,/工具失败/);}finally{await m.cleanup()}
});
test('background process remains running and tool tabs support keyboard navigation',async()=>{
 const m=await mount({override:url=>url.includes('/events?')?Response.json({items:[toolEvent({name:'bash_start',input:{command:'long-running-job'},output:{structuredContent:{state:'running',chunks:[],exit_code:null}}})],next_cursor:null,resume_cursor:'H'}):null});
 try{const card=m.host.querySelector('.tool-card');assert.match(card.textContent,/后台运行中/);assert.doesNotMatch(card.textContent,/执行成功/);card.querySelector('.tool-details').open=true;const output=card.querySelectorAll('[role=tab]')[1];output.focus();output.dispatchEvent(new window.KeyboardEvent('keydown',{key:'ArrowLeft',bubbles:true}));await pause();assert.equal(document.activeElement.textContent,'输入');assert.equal(card.querySelector('[role=tab][aria-selected=true]').textContent,'输入');assert.match(card.querySelector('[role=tabpanel]').textContent,/long-running-job/);}finally{await m.cleanup()}
});
test('sidebar retry keeps the key and destination after a lost upload response', async () => {
  const previous = globalThis.XMLHttpRequest;
  const attempts = [];
  const stored = new Map();
  class Upload {
    upload = {};
    open() {}
    setRequestHeader(name, value) { if (name === 'Idempotency-Key') this.key = value; }
    send(form) {
      const directory = form.get('directory');
      const file = form.get('file');
      attempts.push([this.key, directory]);
      queueMicrotask(() => {
        if (!stored.has(this.key)) {
          stored.set(this.key, {kind: 'file', name: file.name, path: directory + '/' + file.name, size_bytes: file.size});
          this.onerror();
        } else {
          this.status = 200;
          this.response = stored.get(this.key);
          this.onload();
        }
        this.onloadend();
      });
    }
  }
  globalThis.XMLHttpRequest = Upload;
  const m = await mount({override: url => {
    if (!url.includes('/files?')) return null;
    const folder = new URL(url, window.location.href).searchParams.get('path');
    return Response.json({path: folder, entries: folder ? [...stored.values()].filter(f => f.path.startsWith(folder + '/')) :
      ['outputs','other'].map(name => ({kind:'directory',path:name,name}))});
  }});
  try {
    button('任务与文件').click();
    await pause();
    const tree = m.host.querySelector('.file-tree');
    const select = label => [...tree.querySelectorAll('nav button')].find(b => b.textContent.includes(label)).click();
    select('outputs');
    await pause();
    const picker = tree.querySelector('input[type=file]');
    Object.defineProperty(picker,'files',{value:[new File(['data'],'report.csv')]});
    picker.dispatchEvent(new window.Event('change',{bubbles:true}));
    await pause();
    assert.ok(tree.querySelector('[role=alert]'));
    select('other');
    await pause();
    tree.querySelector('.chip button').click();
    await pause();
    assert.equal(attempts.length, 2);
    assert.ok(attempts[0][0]);
    assert.deepEqual(attempts, [[attempts[0][0], 'outputs'], [attempts[0][0], 'outputs']]);
    assert.equal(stored.size, 1);
    assert.equal(tree.querySelectorAll('.chip').length, 0);
    assert.match(tree.querySelector('nav').textContent, /report\.csv/);
    assert.equal(m.host.querySelectorAll('.chat-column .chip').length, 0);
  } finally {
    await m.cleanup();
    globalThis.XMLHttpRequest = previous;
  }
});

for(const existing of [false,true]){
 test(`project tree path and dropped image ID keep their references when the sidebar closes (existing=${existing})`,async()=>{
  const prev=globalThis.XMLHttpRequest;
  const uploads=[];
  class Upload {
    upload={};open(){}setRequestHeader(k,v){if(k==='Idempotency-Key')this.key=v;}
    send(){uploads.push(this.key);queueMicrotask(()=>{this.upload.onprogress({lengthComputable:true,loaded:10,total:10});if(existing&&uploads.length===1){this.onerror();this.onloadend();return;}this.status=201;this.response={path:'uploads/趋势图 (2).png',name:'趋势图 (2).png',size_bytes:10};this.onload();this.onloadend();});}
  }
  globalThis.XMLHttpRequest=Upload;
  const m=await mount({chat:existing?'chat':null,override:(url,init)=>{
    if (url.endsWith('/attachments')) {
      assert.deepEqual(JSON.parse(init.body), {name:'趋势图.png', size:3});
      assert.ok(init.headers.get('Idempotency-Key'));
      return Response.json({id:'reserved-image'});
    }
    if(!url.includes('/files?'))return null;
    const p=new URL(url,window.location.href).searchParams.get('path');
    return Response.json({path:p,entries:p?[{kind:'file',path:'outputs/地区汇总.csv',name:'地区汇总.csv',size_bytes:42}]:[{kind:'directory',name:'outputs',path:'outputs'}]});
  }});
  try{
    const tree=()=>m.host.querySelector('.file-tree');
    const drawer=()=>m.host.querySelector('.collab-drawer');
    const choose=label=>[...tree().querySelectorAll('button')].find(b=>b.textContent.includes(label)).click();
    assert.equal(tree(),null);button('任务与文件').click();await pause();
    assert.equal(drawer().style.display,'');
    choose('outputs');await pause();choose('地区汇总.csv');await pause();choose('引用到对话');await pause();
    const textarea=m.host.querySelector('.chat-column textarea');
    textarea.value='核对汇总表与趋势图';textarea.dispatchEvent(new window.Event('input',{bubbles:true}));
    const transfer={types:['Files'],files:[new File(['png'],'趋势图.png',{type:'image/png'})]};
    function drag(type){const e=new window.Event(type,{bubbles:true,cancelable:true});Object.defineProperty(e,'dataTransfer',{value:transfer});m.host.querySelector('.chat-column').dispatchEvent(e);}
    drag('dragenter');await pause();assert.ok(m.host.querySelector('.drop-hint'));drag('drop');await pause();
    assert.equal(m.host.querySelector('.drop-hint'),null);assert.equal(uploads.length,1);
    if(existing){assert.equal(button('发送').disabled,false);button('重试').click();await pause();assert.deepEqual(uploads,[uploads[0],uploads[0]]);}
    drawer().querySelector('[aria-label="收起项目文件"]').click();await pause();
    assert.equal(tree().style.display,'none');assert.equal(drawer().style.display,'none');assert.equal(textarea.value,'核对汇总表与趋势图');
    assert.deepEqual([...m.host.querySelectorAll('.chip')].map(e=>e.title),['outputs/地区汇总.csv','uploads/趋势图 (2).png']);
    button('任务与文件').click();await pause();assert.ok(tree().textContent.includes('地区汇总.csv'));button('任务与文件').click();await pause();assert.equal(drawer().style.display,'none');
    const endpoint=existing?'/sessions/chat/inputs':'/sessions';
    const sends=()=>m.calls.filter(c=>c.url.endsWith(endpoint)&&c.init.method==='POST');
    assert.equal(sends().length,0);button('发送').click();await pause();
    assert.equal(sends().length,1);const body=JSON.parse(sends()[0].init.body);const input=existing?body:body.input;
    assert.deepEqual(input.attachment_paths,['outputs/地区汇总.csv']);
    assert.deepEqual(input.attachment_ids,['reserved-image']);
    assert.equal(input.text,'核对汇总表与趋势图');
  }finally{await m.cleanup();globalThis.XMLHttpRequest=prev;}
 });
}

for (const existing of [false, true]) {
  test(`file-only send does not wait for upload and retains progress after acceptance (existing=${existing})`, async () => {
    const previous = globalThis.XMLHttpRequest;
    let active;
    class Upload {
      upload = {};
      open(method, url) { this.url = url; }
      setRequestHeader() {}
      send() { active = this; }
    }
    globalThis.XMLHttpRequest = Upload;
    const m = await mount({chat: existing ? 'chat' : null, override: url =>
      url.endsWith('/attachments') ? Response.json({id:'slow-file'}) : null
    });
    try {
      const picker = m.host.querySelector('.chat-column input[type=file]');
      Object.defineProperty(picker, 'files', {value:[new File(['content'],'slow.txt')]});
      picker.dispatchEvent(new window.Event('change', {bubbles:true}));
      await pause();
      assert.match(active.url, /\/attachments\/slow-file\/upload$/);
      active.upload.onprogress({lengthComputable:true, loaded:1, total:4});
      await pause();
      assert.match(m.host.querySelector('.chat-column .chip').textContent, /25%/);
      assert.equal(button('发送').disabled, false);
      button('发送').click();
      await pause();
      const endpoint = existing ? '/chat/inputs' : '/sessions';
      const sends = m.calls.filter(c => c.url.endsWith(endpoint) && c.init.method === 'POST');
      assert.equal(sends.length, 1);
      const body = JSON.parse(sends[0].init.body);
      const input = existing ? body : body.input;
      assert.equal(input.text, '');
      assert.equal(input.attachment_paths, undefined);
      assert.deepEqual(input.attachment_ids, ['slow-file']);
      assert.match(m.host.querySelector('.chat-column .chip').textContent, /25%/);
      assert.equal(m.host.querySelector('.chat-column [aria-label="移除 slow.txt"]'), null);
      FakeSource.all[0].emit({
        ...event(11),type:'user.message',content:[],
        attachments:[{id:'slow-file',name:'slow.txt',size_bytes:7,status:'downloading'}]
      });
      await pause();
      assert.match(m.host.querySelector('.attachment-link').textContent, /准备中/);
      active.status = 200;
      active.response = {path:'uploads/slow.txt', name:'slow.txt', size_bytes:7};
      active.onload();
      active.onloadend();
      await pause();
      assert.equal(m.host.querySelector('.chat-column .chip'), null);
      assert.match(m.host.querySelector('.attachment-link').textContent, /KB/);
      assert.equal(m.host.querySelector('.attachment-link').title, 'uploads/slow.txt');
    } finally {
      await m.cleanup();
      globalThis.XMLHttpRequest = previous;
    }
  });
}

test('historical attachment without a path can check current status using its ID', async () => {
  const snapshot = {...event(11),type:'user.message',content:[],
    attachments:[{id:'saved-file',name:'report.txt',size_bytes:20,status:'downloading'}]};
  const m=await mount({override:url=>{
    if (url.includes('/events?')) return Response.json({items:[snapshot],next_cursor:null,resume_cursor:'H'});
    if (url.endsWith('/attachments/saved-file')) return Response.json({id:'saved-file',name:'report.txt',size_bytes:20,status:'failed'});
    return null;
  }});
  try {
    assert.match(m.host.querySelector('.attachment-link').textContent, /准备中/);
    m.host.querySelector('button.attachment-link').click();
    await pause();
    assert.equal(m.calls.filter(c=>c.url.endsWith('/attachments/saved-file')).length,1);
    assert.equal(m.host.querySelector('button.attachment-link'),null);
    assert.match(m.host.querySelector('.attachment-link').textContent, /附件不可用/);
    assert.ok(!m.calls.some(c=>c.url.includes('/files/content')));
  } finally { await m.cleanup(); }
});

test('authenticated conversation task detail keeps its own write path and disables stop while held', async () => {
 const taskPath = '/api/v1/workspaces/w/sessions/task1';
 let holding = true;
 const m = await mount({override:(url, init) => {
   if (url !== taskPath || init.method) return null;
   const current = resource('task1');
   if (holding) current.execution.required_actions = [{type:'human_confirmation', request_id:'confirm-1', title:'待人工确认', message:'等待批准'}];
   return Response.json(current);
 }});
 try {
   button('任务与文件').click(); await pause();
   button('会话任务 (2)').click(); await pause();
   [...document.querySelectorAll('button')].find(b=>b.textContent.includes('task1')).click(); await pause();
   const panel = m.host.querySelector('.task-detail-panel');
   const action = label => [...panel.querySelectorAll('button')].find(b=>(b.getAttribute('aria-label')||b.textContent.trim())===label);
   const stop = action('停止此任务');
   assert.ok(stop.disabled);
   const input = panel.querySelector('textarea');
   assert.ok(!input.disabled);
   input.value = '来自对话内任务详情的补充'; input.dispatchEvent(new window.Event('input',{bubbles:true})); await pause();
   action('发送').click(); await pause();
   holding = false;
   FakeSource.all.find(s=>!s.closed && s.url.includes('/task1/')).emit({type:'session.updated', session_id:'task1', seq:'11'});
   await pause();
   assert.ok(!stop.disabled);
   stop.click(); await pause();
   const taskWrites = m.calls.filter(c=>c.init.method==='POST' && c.url.startsWith(taskPath));
   assert.deepEqual(taskWrites.map(c=>c.url), [taskPath+'/inputs',taskPath+'/stop']);
   assert.ok(taskWrites.every(c=>c.init.credentials==='include' && !c.init.headers.has('Authorization')));
   assert.ok(!m.calls.some(c=>c.url.startsWith('/api/v1/public/')));
 } finally { await m.cleanup(); }
});

test('creation with only task_id opens off-list task using business lookup, not a same-valued Session ID', async () => {
 const created = toolEvent({output:{structuredContent:{accepted:true,task_id:'task1',input_id:'new-input'}}});
 const actual = {...taskDetail('task1'),session_id:'actual-session',title:'正确业务任务'};
 const m = await mount({override:url => {
   if (url.includes('/chat/events?')) return Response.json({items:[created],next_cursor:null,resume_cursor:'H'});
   if (url.endsWith('/tasks/task1')) return Response.json(actual);
   return null;
 }});
 try {
   assert.match(m.host.querySelector('.task-action').textContent,/已触发|任务已受理/);
   button('查看任务 ↗').click(); await pause();
   assert.match(m.host.querySelector('.task-detail-panel').textContent,/正确业务任务/);
   assert.ok(m.calls.some(c=>c.url.endsWith('/tasks/task1')));
   assert.ok(!m.calls.some(c=>c.url.endsWith('/sessions/task1')));
   assert.ok(FakeSource.all.find(s=>s.url.includes('/actual-session/')));
 } finally { await m.cleanup(); }
});

test('task_id-only report opens full session waterfall without turn_id filtering and channel card receipt is not falsely delivered', async () => {
 const task_id='business-task2';
 const report = reportEvent({content:[{type:'text',text:'平台任务回报\n'+JSON.stringify({
   task_id,turn_id:'source-turn',outcome:'completed',result:'# 新回报正文',background_process_count:0,queued_input_count:0,
 })}]});
 const card = toolEvent({seq:'11',name:'send_task_detail_link',input:{task_id},output:{structuredContent:{status:'queued'}}});
 const m = await mount({override:url=>url.includes('/chat/events?')?Response.json({items:[card,report],next_cursor:null,resume_cursor:'H'}):null});
 try {
   const receipt=m.host.querySelector('.task-action');
   assert.match(receipt.textContent,/已提交投递/);
   assert.match(receipt.textContent,/尚未确认送达/);
   assert.doesNotMatch(receipt.textContent,/已发送/);
   assert.equal(m.host.querySelector('.report-original h1').textContent,'新回报正文');
   assert.match(m.host.querySelector('.report-card h3').textContent,/task2/);
   button('查看任务与执行过程 ↗').click(); await pause();
   const taskStream = FakeSource.all.find(s=>s.url.includes('/task2/'));
   assert.ok(taskStream);
   assert.ok(!taskStream.url.includes('turn_id='));
   assert.ok(!m.calls.some(c=>c.url.includes('/task2/events?')&&c.url.includes('turn_id=')));
   assert.equal(m.host.querySelector('.turn-focus-bar'), null);
   assert.ok(m.host.querySelector('.task-detail-panel textarea'));
   assert.ok(!m.calls.some(c=>c.url.endsWith('/sessions/'+task_id)));
 } finally { await m.cleanup(); }
});

test('task locator rejects a valid task from another conversation without opening its execution history', async () => {
 const created=toolEvent({output:{structuredContent:{accepted:true,task_id:'different-task'}}});
 const m=await mount({override:url=>{
   if (url.includes('/chat/events?')) return Response.json({items:[created],next_cursor:null,resume_cursor:'H'});
   if (url.endsWith('/tasks/different-task')) return Response.json({...taskDetail('different-task'),session_id:'different-session',conversation_id:'other-chat'});
   return null;
 }});
 try {
   button('查看任务 ↗').click(); await pause();
   assert.ok(m.calls.some(c=>c.url.endsWith('/tasks/different-task')));
   assert.equal(m.host.querySelector('.task-detail-panel'),null);
   assert.equal(FakeSource.all.filter(s=>!s.closed).length,1);
   assert.ok(!m.calls.some(c=>c.url.includes('/different-session/')));
 } finally { await m.cleanup(); }
});

test('query, supplement and stop tool outputs use task_id without depending on hidden Session IDs', async () => {
 const names = ['task_delegation_get_session','task_delegation_send_message','task_delegation_stop_session'];
 const items = names.map((name,index)=>toolEvent({name,seq:String(20+index),tool_use_id:'only-task-id-'+index,
   input:{task_id:'business-task2'},output:{structuredContent:{task_id:'business-task2'}}}));
 const m=await mount({override:url=>url.includes('/chat/events?')?Response.json({items,next_cursor:null,resume_cursor:'H'}):null});
 try {
   const cards=[...m.host.querySelectorAll('.task-action')];
   assert.equal(cards.length,3);
   assert.match(cards[0].textContent,/查看任务进展/);
   assert.match(cards[1].textContent,/补充任务要求.*已受理/);
   assert.match(cards[2].textContent,/停止任务.*已受理/);
   assert.ok(cards.every(card=>card.querySelector('.task-link')));
   cards[2].querySelector('.task-link').click(); await pause();
   assert.ok(FakeSource.all.find(s=>s.url.includes('/task2/')));
   assert.ok(!m.calls.some(c=>c.url.endsWith('/sessions/business-task2')));
   assert.ok(!m.calls.some(c=>c.url.endsWith('/stop')), 'viewing a stop receipt does not issue a second stop');
 } finally { await m.cleanup(); }
});

test('conversation sidebar fullscreen expands into dual-pane workspace and preserves task, scroll position, and draft on exit', async () => {
 const m = await mount({
   override: url => url.endsWith('/tasks/business-task1')
     ? Response.json({
         ...taskDetail('business-task1'),
         progress: { phase: 'completed' },
         artifacts: [{ id: 'art-1', name: '分析说明.md', size: 32 }],
       })
     : url.endsWith('/tasks/business-task1/artifacts/art-1')
       ? new Response('# 全屏分析说明', { headers: { 'Content-Type': 'text/markdown' } })
       : null,
 });
 try {
   button('任务与文件').click(); await pause();
   button('会话任务 (2)').click(); await pause();
   [...document.querySelectorAll('button')].find(b=>b.textContent.includes('task1')).click(); await pause();

   const panel = m.host.querySelector('.task-detail-panel');
   assert.ok(panel);
   assert.equal(panel.querySelector('.status-badge').textContent.trim(), '已完成');
   assert.equal(button('返回来源对话'), undefined);

   const scroller = panel.querySelector('.messages');
   Object.defineProperty(scroller, 'scrollHeight', { configurable: true, writable: true, value: 1200 });
   Object.defineProperty(scroller, 'clientHeight', { configurable: true, writable: true, value: 320 });
   scroller.scrollTop = 240;
   scroller.dispatchEvent(new window.Event('scroll'));

   const box = panel.querySelector('textarea');
   box.value = '全屏前保留的草稿';
   box.dispatchEvent(new window.Event('input', { bubbles: true }));
   await nextTick();

   const initialTaskStreams = FakeSource.all.filter(s => s.url.includes('/task1/')).length;
   const expandBtn = [...panel.querySelectorAll('button')].find(b => b.textContent.includes('放大全屏'));
   assert.ok(expandBtn);
   expandBtn.click();
   await pause();

   assert.ok(m.host.querySelector('.tasks.is-fullscreen'));
   assert.ok(panel.classList.contains('is-fullscreen'));
   assert.ok(panel.querySelector('.workspace-body.dual-pane'));
   assert.ok(panel.querySelector('[aria-label="产物预览画布"]'));
   assert.equal(panel.querySelector('.status-badge').textContent.trim(), '已完成');
   assert.equal(scroller.scrollTop, 240);
   assert.equal(box.value, '全屏前保留的草稿');
   assert.equal(FakeSource.all.filter(s => s.url.includes('/task1/')).length, initialTaskStreams);

   box.value = '全屏修改后的草稿';
   box.dispatchEvent(new window.Event('input', { bubbles: true }));
   scroller.scrollTop = 180;
   scroller.dispatchEvent(new window.Event('scroll'));
   await nextTick();

   button('⤡ 退出全屏').click();
   await pause();
   assert.equal(m.host.querySelector('.tasks.is-fullscreen'), null);
   assert.equal(panel.classList.contains('is-fullscreen'), false);
   assert.ok(m.host.querySelector('.tasks.has-detail'));
   assert.equal(scroller.scrollTop, 180);
   assert.equal(box.value, '全屏修改后的草稿');

   expandBtn.click();
   await pause();
   assert.ok(panel.classList.contains('is-fullscreen'));
   const backBtn = [...panel.querySelectorAll('button')].find(b => b.textContent.trim() === '← 返回对话');
   assert.ok(backBtn);
   backBtn.click();
   await pause();

   assert.equal(m.host.querySelector('.tasks.is-fullscreen'), null);
   assert.ok(m.host.querySelector('.tasks.has-detail'));
   assert.equal(scroller.scrollTop, 180);
   assert.equal(box.value, '全屏修改后的草稿');
   assert.equal(FakeSource.all.filter(s => s.url.includes('/task1/')).length, initialTaskStreams);
 } finally { await m.cleanup(); }
});

test('unstarted employees stay out of the active sidebar until launched via start-chat modal', async () => {
 window.localStorage?.clear?.();
 const agents = [
   { id: 'a1', name: '已聊员工', role: '分析', avatar: 'cn-v2-01', enabled: true },
   { id: 'a2', name: '未聊助手', role: '交付', avatar: 'cn-v2-02', enabled: true },
 ];
 const m = await mount({
   override: (url, init) => {
     if (url.includes('/agents?')) return Response.json({ items: agents, total: 2, page: 1, page_size: 20 });
     if (url.includes('/conversations?')) return Response.json({ items: [{ id: 'c1', agent_id: 'a1', chat_session_id: 'chat1', lifecycle: 'active' }], next_cursor: null });
     if (url.endsWith('/web-direct')) {
       const body = JSON.parse(init.body);
       return Response.json({ id: 'c-' + body.agent_id, agent_id: body.agent_id, chat_session_id: body.agent_id === 'a1' ? 'chat1' : null, lifecycle: 'active' });
     }
     return null;
   },
 });
 try {
   const sidebarButtons = () => [...m.host.querySelectorAll('.employee-list .employee')].map(b => b.textContent);
   assert.equal(sidebarButtons().length, 1);
   assert.match(sidebarButtons()[0], /已聊员工/);
   assert.doesNotMatch(m.host.querySelector('.employee-list').textContent, /未聊助手/);

   button('+ 发起对话').click();
   await pause();
   const pick = [...document.querySelectorAll('.modal-agent-item')].find(el => el.textContent.includes('未聊助手'));
   assert.ok(pick);
   pick.click();
   await pause();

   assert.equal(sidebarButtons().length, 2);
   assert.match(m.host.querySelector('.employee-list').textContent, /未聊助手/);
   assert.match(m.host.querySelector('.chat-area').textContent, /与「未聊助手」开始协作/);
 } finally {
   window.localStorage?.clear?.();
   await m.cleanup();
 }
});

test('sidebar preserves server recency and never reads another account or legacy cache',async()=>{
 const second={...agent,id:'b',name:'最近回复员工'};
 const third={...agent,id:'c-local',name:'另一账号已打开员工'};
 Object.defineProperty(globalThis,'localStorage',{configurable:true,value:window.localStorage});
 try{
  window.localStorage.setItem('kiko:started-chats:w',JSON.stringify([third.id]));
  window.localStorage.setItem('kiko:started-chats:v2:someone-else:w',JSON.stringify([third.id]));
  const override=url=>{
   if(url.includes('/agents?'))return Response.json({items:[agent,second,third],total:3,page:1,page_size:20});
   if(url.includes('/conversations?'))return Response.json({items:[{agent_id:'b',chat_session_id:'chat-b'},{agent_id:'a',chat_session_id:'chat'}],next_cursor:'ignored'});
   return null;
  };
  const m=await mount({props:{accountId:'owner'},override});
  try{
   const entries=[...m.host.querySelectorAll('.sidebar-items .employee strong')].map(e=>e.textContent);
   assert.deepEqual(entries,[second.name,agent.name]);
   assert.equal(m.calls.filter(c=>c.url.includes('/conversations?')).length,1);
  }finally{await m.cleanup()}
 }finally{window.localStorage.clear();delete globalThis.localStorage;}
});

test('default employee is not counted as history before a conversation is opened',async()=>{
 const m=await mount({chat:null,override:url=>url.includes('/conversations?')?Response.json({items:[],next_cursor:null}):null});
 try{
  assert.equal(m.host.querySelectorAll('.sidebar-items .employee').length,0);
  assert.match(m.host.querySelector('.chat-area').textContent,/开始协作/);
 }finally{await m.cleanup()}
});

test('assistant navigation opens ordinary chat without sending, card click sends a single sample message',async()=>{
 const card={key:'sample',title:'体验并行客诉',description:'核对两个订单',input:'请并行核对两笔订单'};
 const m=await mount({chat:null,props:{selectedEmployeeId:'a',assistantEntry:{agent_id:'a',cards:[card]}}});
 try {
  const writes=()=>m.calls.filter(c=>c.url.endsWith('/sessions')&&c.init.method==='POST');
  assert.equal(writes().length,0);
  const action=[...m.host.querySelectorAll('button')].find(b=>b.textContent.includes(card.title));
  assert.ok(action);action.click();action.click();await pause();
  assert.equal(writes().length,1);
  assert.deepEqual(JSON.parse(writes()[0].init.body).input,{text:card.input});
  assert.equal(m.calls.filter(c=>c.url.endsWith('/web-direct')).length,1);
 }finally{await m.cleanup()}
});

test('assistant denied Web demo access or already experienced does not auto-show sample cards',async()=>{
 const card={key:'sample',title:'体验并行客诉',description:'核对两个订单',input:'请并行核对两笔订单'};
 const unauthorized=await mount({chat:null,props:{selectedEmployeeId:'a',assistantEntry:{agent_id:'a',cards:[]}}});
 try {assert.doesNotMatch(unauthorized.host.textContent,/体验场景|体验并行客诉/)}finally{await unauthorized.cleanup()}
 const experienced=await mount({chat:null,props:{selectedEmployeeId:'a',assistantEntry:{agent_id:'a',cards:[card],experienced:true}}});
 try {assert.doesNotMatch(experienced.host.textContent,/体验并行客诉/);button('体验场景').click();await pause();assert.match(experienced.host.textContent,/体验并行客诉/)}finally{await experienced.cleanup()}
});

test('unified drawer switches between project files with Markdown preview and session tasks', async () => {
 let largeRequested = false;
 let streamCancelled = false;
 const m = await mount({
  override: url => {
   if (url.includes('/files?')) {
    return Response.json({
     path: '',
     entries: [
      { kind: 'file', path: 'ORD-1087_核查报告.md', name: 'ORD-1087_核查报告.md', size_bytes: 64 },
      { kind: 'file', path: 'stale-large.md', name: 'stale-large.md', size_bytes: 64 }
     ]
    });
   }
   if (url.includes('/files/content?') && url.includes('stale-large.md')) {
    largeRequested = true;
    const chunk = new Uint8Array(65536).fill(65);
    let sentChunks = 0;
    const stream = new ReadableStream({
     pull(controller) {
      if (sentChunks >= 6) {
       controller.close();
       return;
      }
      sentChunks += 1;
      controller.enqueue(chunk);
     },
     cancel() {
      streamCancelled = true;
     }
    });
    return new Response(stream, { headers: { 'Content-Type': 'text/markdown' } });
   }
   if (url.includes('/files/content?')) {
    return new Response('# 核查报告\n\n- 下单：**黑色 X2**', { headers: { 'Content-Type': 'text/markdown' } });
   }
   return null;
  }
 });
 try {
  const drawer = () => m.host.querySelector('.collab-drawer');
  assert.equal(drawer().style.display, 'none');
  button('任务与文件').click();
  await pause();
  assert.equal(drawer().style.display, '');
  const fileBtn = [...drawer().querySelectorAll('.file-tree nav button')].find(b => b.textContent.includes('ORD-1087_核查报告.md'));
  assert.ok(fileBtn);
  fileBtn.click();
  await pause();
  const preview = drawer().querySelector('.live-preview');
  assert.ok(preview);
  assert.equal(preview.getAttribute('aria-label'), '文件预览');
  assert.doesNotMatch(preview.textContent, /实时预览/);
  assert.equal(preview.querySelector('.session-markdown h1')?.textContent, '核查报告');

  const largeBtn = [...drawer().querySelectorAll('.file-tree nav button')].find(b => b.textContent.includes('stale-large.md'));
  assert.ok(largeBtn);
  largeBtn.click();
  await pause();
  assert.equal(largeRequested, true);
  assert.equal(streamCancelled, true);
  assert.match(drawer().querySelector('.live-preview').textContent, /超过 256 KB/);
  assert.equal(drawer().querySelector('.live-preview .session-markdown'), null);

  button('会话任务 (2)').click();
  await pause();
  assert.equal(drawer().querySelector('.file-tree').style.display, 'none');
  assert.ok(drawer().querySelector('.tasks'));
 } finally {
  await m.cleanup();
 }
});

test('assistant with no model keeps demos visible until clicked, then offers model setup without inserting a chat',async()=>{
 const card={key:'sample',title:'体验并行客诉',description:'核对两个订单',input:'请并行核对两笔订单'};
 const missing=()=>Response.json({error:{code:'assistant_model_required',message:'后端提示不应直接显示'}},{status:422});
 let configured=false, opened=0;
 const props={selectedEmployeeId:'a',assistantEntry:{agent_id:'a',cards:[card]},canConfigureModels:true,onConfigureModels:()=>opened++};
 const matchWrite=(url,init)=>url.endsWith('/sessions')&&init.method==='POST';
 const override=(url,init)=>matchWrite(url,init)&&!configured?missing():null;
 const demo=()=>[...document.querySelectorAll('button')].find(b=>b.textContent.includes(card.title));
 let m=await mount({chat:null,props,override});
 try {
  assert.ok(demo());assert.equal(m.host.querySelector('.assistant-model-required'),null);
  demo().click();await pause();
  assert.equal(m.calls.filter(c=>matchWrite(c.url,c.init)).length,1);
  assert.equal(demo(),undefined);
  assert.match(m.host.querySelector('.assistant-model-required').textContent,/当前没有可用模型，消息未发送/);
  assert.equal(FakeSource.all.length,0);
  button('前往模型配置').click();assert.equal(opened,1);
 }finally{await m.cleanup()}
 configured=true;
 m=await mount({chat:null,props,override});
 try {
  assert.ok(demo());assert.equal(m.calls.filter(c=>matchWrite(c.url,c.init)).length,0);
  demo().click();await pause();
  assert.equal(m.calls.filter(c=>matchWrite(c.url,c.init)).length,1);
  assert.equal(FakeSource.all.length,1);
 }finally{await m.cleanup()}
});

test('assistant missing a model in an existing conversation preserves text and gives members a contact prompt',async()=>{
 let configured=false;
 const m=await mount({props:{selectedEmployeeId:'a',assistantEntry:{agent_id:'a',cards:[]}},override:(url,init)=>
  url.endsWith('/inputs')&&init.method==='POST'&&!configured?Response.json({error:{code:'assistant_model_required',message:'未配置'}},{status:422}):null});
 try {
  const input=m.host.querySelector('textarea');input.value='保留这段文字';input.dispatchEvent(new window.Event('input',{bubbles:true}));await pause();
  button('发送').click();await pause();
  assert.match(m.host.querySelector('.assistant-model-required').textContent,/请联系工作区管理员/);
  assert.equal(button('前往模型配置'),undefined);assert.equal(input.value,'保留这段文字');
  configured=true;button('发送').click();await pause();
  assert.equal(m.host.querySelector('.assistant-model-required'),null);
  assert.equal(input.value,'');
 }finally{await m.cleanup()}
});

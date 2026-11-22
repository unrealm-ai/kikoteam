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
const built=await esbuild.build({stdin:{contents:"export {default as PlatformEmployeesView} from './src/direction2/views/PlatformEmployeesView.vue'; export {activeWorkspaceId} from './src/direction2/api/client'; export {createApp,h,nextTick} from 'vue'; export {NMessageProvider,NDialogProvider} from 'naive-ui';",resolveDir:process.cwd(),loader:'ts'},bundle:true,write:false,format:'esm',platform:'node',define:{'import.meta.env.VITE_PLATFORM_REAL':'true','import.meta.env.DEV':'false'},plugins:[{name:'vue-test',setup(build){build.onLoad({filter:/\.vue$/},async({path})=>{const source=await fs.readFile(path,'utf8');const {descriptor}=parse(source);return {contents:compileScript(descriptor,{id:path,inlineTemplate:true}).content,loader:'ts'};});}}]});
const {PlatformEmployeesView,activeWorkspaceId,createApp,h,NMessageProvider,NDialogProvider,nextTick}=await import(`data:text/javascript;base64,${Buffer.from(built.outputFiles[0].text).toString('base64')}`);
const pause=async()=>{await new Promise(r=>setTimeout(r,40));await nextTick()};
const model={id:'model-1',name:'Test Model',enabled:true,validated:true,is_default:true};
const agent={id:'agent-1',name:'Assistant',role:'Engineering',avatar:'研',instruction:'',model_profile_id:'model-1',model_name:'Test Model',enabled:true,version:3,config_version:2,admission_epoch:1,harness:'pi'};
const button=text=>[...document.querySelectorAll('button')].find(b=>b.textContent.trim()===text);
async function mount({role='owner',items=[agent],getHandler,writeHandler,modelHandler,requestHandler}={}){
 const calls=[];const guards=[];
 globalThis.fetch=async(url,init={})=>{
  calls.push({url,init,body:init.body?JSON.parse(init.body):null});
  const custom=await requestHandler?.(url,init);if(custom)return custom;
  if(init.method)return writeHandler?writeHandler(url,init):Response.json({...agent,...JSON.parse(init.body),version:4});
  if(url.includes('/model-profiles'))return modelHandler?modelHandler(url):Response.json({items:[model],total:1,page:1,page_size:20});
  if(url.endsWith('/agents/agent-1'))return getHandler?getHandler():Response.json(agent);
  if(url.includes('/agents?'))return Response.json({items,total:items.length,page:1,page_size:20});
  return Response.json({role});
 };
 activeWorkspaceId.value='w';const host=document.createElement('div');document.body.appendChild(host);
 const app=createApp({render:()=>h(NMessageProvider,null,{default:()=>h(NDialogProvider,null,{default:()=>h(PlatformEmployeesView)})})});
 app.provide('d2DraftGuard',{register:g=>guards.push(g),unregister:()=>{}});app.mount(host);await pause();
 return {calls,guards,async cleanup(){app.unmount();host.remove();await pause()}};
}
function input(value,next){const node=[...document.querySelectorAll('input')].find(i=>i.value===value);assert.ok(node);node.value=next;node.dispatchEvent(new window.Event('input',{bubbles:true}));}
test('Employee foundation loads only real platform resources; does not depend on unimplemented mock APIs',async()=>{
 const m=await mount();try{assert.match(document.body.textContent,/Assistant/);assert.match(document.body.textContent,/尚未接入/);assert.ok(m.calls.every(c=>c.url.startsWith('/api/v1/')));assert.ok(m.calls.every(c=>!/(skills|mcp|invitations|directory)/.test(c.url)));assert.equal(document.body.textContent.includes('Claude Code'),false);}finally{await m.cleanup()}
});
test('Create allows empty instructions, prefills explicit model and sends no harness or thinking configuration',async()=>{
 const m=await mount({items:[]});try{button('新建 AI 员工').click();await pause();assert.ok(button('创建员工').disabled);assert.doesNotMatch(document.body.textContent,/岗位行为规范/);input('','New employee');await pause();assert.equal(button('创建员工').disabled,false);button('创建员工').click();await pause();const call=m.calls.find(c=>c.init.method==='POST');assert.ok(call);assert.equal(call.body.instruction,'');assert.equal(call.body.model_profile_id,'model-1');assert.equal('harness' in call.body,false);assert.equal('thinkingLevel' in call.body,false);assert.equal(call.init.credentials,'include');assert.equal(call.init.headers.get('X-CSRF-Protection'),'1');assert.equal(call.init.headers.has('Authorization'),false);assert.match(document.body.textContent,/员工配置已保存/);}finally{await m.cleanup()}
});
test('Update preserves explicit model, sends optimistic version and registers dirty navigation guard',async()=>{
 const m=await mount();try{button('配置员工').click();await pause();input('Assistant','Renamed');await pause();assert.equal(m.guards[0].hasUnsaved(),true);button('保存配置').click();await pause();const call=m.calls.find(c=>c.init.method==='PATCH');assert.equal(call.body.version,3);assert.equal(call.body.model_profile_id,'model-1');assert.equal(call.body.name,'Renamed');assert.equal('enabled' in call.body,false);assert.equal(m.guards[0].hasUnsaved(),false);}finally{await m.cleanup()}
});
test('Member view is read-only; failed detail cannot accidentally create an employee',async()=>{
 let m=await mount({role:'member'});try{assert.equal(button('新建 AI 员工'),undefined);assert.equal(button('停用'),undefined);button('查看详情').click();await pause();assert.equal(button('保存配置'),undefined);}finally{await m.cleanup()}
 m=await mount({getHandler:()=>Response.json({error:{message:'detail failed'}},{status:503})});try{button('配置员工').click();await pause();assert.match(document.body.textContent,/detail failed/);assert.ok(button('创建员工').disabled);assert.equal(m.calls.filter(c=>c.init.method).length,0);}finally{await m.cleanup()}
});
test('Workspace change discards late detail results and closes the previous editor',async()=>{
 let finish;const m=await mount({getHandler:()=>new Promise(r=>{finish=r})});try{button('配置员工').click();await pause();activeWorkspaceId.value='new';await pause();finish(Response.json({...agent,name:'STALE DETAIL'}));await pause();assert.doesNotMatch(document.body.textContent,/STALE DETAIL/);assert.ok(m.calls.some(c=>c.url.includes('/workspaces/new/agents')));}finally{await m.cleanup()}
});
test('Write success is not converted to failure when subsequent list refresh fails',async()=>{
 const m=await mount();try{button('配置员工').click();await pause();input('Assistant','Saved');const original=fetch;globalThis.fetch=async(url,init={})=>init.method?original(url,init):Response.json({error:{message:'refresh failed'}},{status:503});button('保存配置').click();await pause();assert.match(document.body.textContent,/员工配置已保存/);assert.match(document.body.textContent,/refresh failed/);assert.doesNotMatch(document.body.textContent,/保存失败/);}finally{await m.cleanup()}
});
test('Model pagination protocol errors stop iteration instead of looping',async()=>{
 const m=await mount({modelHandler:()=>Response.json({items:[model],total:100,page:1,page_size:20})});try{assert.match(document.body.textContent,/模型分页响应重复或页码错误/);assert.equal(m.calls.filter(c=>c.url.includes('/model-profiles')).length,2);assert.ok(button('新建 AI 员工').disabled);}finally{await m.cleanup()}
});
test('Avatar picker preserves original presets, and detail instructions are optional',async()=>{
 const m=await mount();try{button('配置员工').click();await pause();assert.match(document.body.textContent,/岗位行为规范/);button('选择头像').click();await pause();document.querySelector('[aria-label="选择头像 cn-v2-02"]').click();await pause();button('保存配置').click();await pause();assert.equal(m.calls.find(c=>c.init.method==='PATCH').body.avatar,'cn-v2-02');}finally{await m.cleanup()}
});
test('Conflict retains draft and never silently reloads or overwrites newer employee configuration',async()=>{
 const m=await mount({writeHandler:()=>Response.json({error:{code:'version_conflict',message:'配置已更新'}},{status:409})});try{button('配置员工').click();await pause();input('Assistant','My draft');button('保存配置').click();await pause();assert.match(document.body.textContent,/配置已更新/);assert.ok([...document.querySelectorAll('input')].some(i=>i.value==='My draft'));assert.equal(m.guards[0].hasUnsaved(),true);}finally{await m.cleanup()}
});

const onboardingResponse=(url)=>{
 if(/\/channels\/(feishu|dingtalk|wecom)$/.test(url))return Response.json({error:{code:'channel_not_configured',message:'not configured'}},{status:404});
 if(url.endsWith('/registration'))return Response.json({registration:{id:'old',state:'failed',expires_at:new Date(Date.now()+600000).toISOString(),next_poll_at:new Date(Date.now()+100000).toISOString(),error_code:'previous_failure'}});
};
test('Original full-page employee modules retain persona drafts when switching tabs',async()=>{
 const m=await mount({requestHandler:onboardingResponse});try{
  button('配置员工').click();await pause();
  assert.equal(document.querySelectorAll('.detail-tabs button').length,6);assert.ok(document.querySelector('.employee-detail-container'));assert.equal(document.querySelector('.n-drawer'),null);
  input('Assistant','Unsaved persona');button('渠道接入与群策略').click();await pause();
  assert.match(document.body.textContent,/存在未保存的配置草稿/);button('继续编辑').click();await pause();
  assert.ok([...document.querySelectorAll('input')].some(i=>i.value==='Unsaved persona'));
  button('渠道接入与群策略').click();await pause();button('放弃修改').click();await pause();await pause();
  assert.match(document.body.textContent,/扫码接入与权限检查/);assert.ok(button('已有机器人应用'));assert.equal(m.guards[0].hasUnsaved(),false);
  assert.equal(m.calls.filter(c=>c.init.method==='POST').length,0);
 }finally{await m.cleanup()}
});
test('Channel child drafts participate in employee navigation guard without recursive close dialogs',async()=>{
 const m=await mount({requestHandler:onboardingResponse});try{
  button('飞书渠道').click();await pause();await pause();button('已有机器人应用').click();await pause();
  const id=document.querySelector('input[placeholder="飞书自建应用 App ID"]');assert.ok(id);id.value='cli_draft';id.dispatchEvent(new window.Event('input',{bubbles:true}));await pause();assert.equal(m.guards[0].hasUnsaved(),true);
  button('钉钉').click();await pause();assert.match(document.body.textContent,/存在未保存的配置草稿/);button('继续编辑').click();await pause();assert.equal(id.value,'cli_draft');
  button('钉钉').click();await pause();button('放弃修改').click();await pause();await pause();
  assert.match(document.body.textContent,/钉钉扫码接入/);assert.equal(m.guards[0].hasUnsaved(),false);
  assert.equal(button('继续编辑'),undefined);assert.equal(m.calls.filter(c=>c.init.method==='PUT').length,0);
 }finally{await m.cleanup()}
});

test('Confirmed return from a child panel leaves the employee detail once',async()=>{
 const m=await mount({requestHandler:onboardingResponse});try{
  button('飞书渠道').click();await pause();await pause();button('已有机器人应用').click();await pause();
  const id=document.querySelector('input[placeholder="飞书自建应用 App ID"]');id.value='cli_draft';id.dispatchEvent(new window.Event('input',{bubbles:true}));await pause();
  button('返回员工列表').click();await pause();button('放弃修改').click();await pause();await pause();
  assert.ok(button('配置员工'));assert.equal(button('继续编辑'),undefined);assert.equal(m.guards[0].hasUnsaved(),false);
 }finally{await m.cleanup()}
});

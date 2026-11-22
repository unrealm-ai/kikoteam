import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
import {Window} from 'happy-dom';
import {parse,compileScript} from '@vue/compiler-sfc';
import esbuild from 'esbuild';
const window=new Window({url:'http://127.0.0.1:5173/#/?ws=w&view=employees'});
for(const name of ['Event','EventTarget','PointerEvent','location','history','window','document','navigator','HTMLElement','Element','SVGElement','Node','Text','Comment','Image','InputEvent','MouseEvent','KeyboardEvent','HTMLInputElement','Document','MutationObserver','ResizeObserver','getComputedStyle','requestAnimationFrame','cancelAnimationFrame']){
 if(name==='window')globalThis.window=window;else if(window[name]!==undefined)Object.defineProperty(globalThis,name,{configurable:true,writable:true,value:typeof window[name]==='function'&&['getComputedStyle','requestAnimationFrame','cancelAnimationFrame'].includes(name)?window[name].bind(window):window[name]});
}
const built=await esbuild.build({stdin:{contents:"export {default as RootApp} from './src/App.vue'; export {activeWorkspaceId} from './src/direction2/api/client'; export {createApp,h,nextTick} from 'vue'; export {NMessageProvider,NDialogProvider} from 'naive-ui';",resolveDir:process.cwd(),loader:'ts'},bundle:true,write:false,format:'esm',platform:'node',loader:{'.css':'empty'},define:{'import.meta.env.VITE_PLATFORM_REAL':'true','import.meta.env.DEV':'false'},plugins:[{name:'vue-test',setup(build){build.onLoad({filter:/\.vue$/},async({path})=>{const source=await fs.readFile(path,'utf8');const {descriptor}=parse(source);return {contents:compileScript(descriptor,{id:path,inlineTemplate:true}).content,loader:'ts'};});}}]});
const {RootApp,activeWorkspaceId,createApp,h,NMessageProvider,NDialogProvider,nextTick}=await import(`data:text/javascript;base64,${Buffer.from(built.outputFiles[0].text).toString('base64')}`);
const pause=async()=>{await new Promise(r=>setTimeout(r,40));await nextTick()};
test('Actual root app provides dialog context and employee page remains mounted through dirty-close confirmation',async()=>{
 const errors=[],requests=[];
 const agent={id:'a',name:'Original Employee',role:'Engineering',avatar:'cn-v2-02',instruction:'',model_profile_id:'m',model_name:'Model',model_available:true,enabled:true,version:3,config_version:2,admission_epoch:1,harness:'pi'};
 globalThis.fetch=async(url,init={})=>{
 requests.push({url,init});
  if(url==='/api/v1/me')return Response.json({user:{id:'u',name:'Owner',email:'owner@example.test'}});
  if(url.includes('/api/v1/workspaces?'))return Response.json({items:[{id:'w',name:'Workspace',role:'owner',version:1}],total:1});
  if(url.includes('/model-profiles'))return Response.json({items:[{id:'m',name:'Model',enabled:true,validated:true,is_default:true}],total:1,page:1,page_size:20});
  if(url.includes('/agents?'))return Response.json({items:[agent],total:1,page:1,page_size:20});
  if(url.endsWith('/agents/a'))return Response.json(agent);
  if(url.endsWith('/usage-config'))return Response.json({version:2,usage_config:{access:{mode:'all',allow:[],deny:[],group_members_enabled:true},history:{}}});
  if(url.endsWith('/trigger-rules'))return Response.json({version:1,rules:[]});
  if(url.includes('/capability-packages'))return Response.json({items:[],total:0,page:1,page_size:20});
  if(url.endsWith('/triggers'))return Response.json({items:[]});
  if(url.includes('/trigger-notification-targets'))return Response.json({items:[],truncated:false});
  if(url.endsWith('/channels/feishu/registration'))return Response.json({registration:null,permissions:[]});
  if(/\/channels\/(feishu|dingtalk|wecom)$/.test(url))return Response.json({error:{code:'channel_not_configured',message:'not configured'}},{status:404});
  if(url==='/api/v1/workspaces/w')return Response.json({id:'w',name:'Workspace',role:'owner',version:1});
  return Response.json({error:{message:'not implemented'}},{status:404});
 };
 const host=document.createElement('div');document.body.appendChild(host);
 // No test-created providers: use exactly App -> Direction2App as production does.
 const app=createApp(RootApp);app.config.errorHandler=e=>errors.push(String(e));app.mount(host);
 try{
  await pause();await pause();
  const button=text=>[...document.querySelectorAll('button')].find(b=>b.textContent.trim()===text);
  assert.deepEqual(errors,[]);assert.ok(button('管理配置'));
  assert.ok(document.querySelector('article')); // Original directory, not the replacement page.
  assert.equal(document.title,'AI 员工 · Workspace · KIKO 企业级AI员工云平台');
  assert.equal(document.querySelector('.brand-logo').title,'KIKO 企业级AI员工云平台');
  assert.equal(document.querySelector('.brand-logo .platform-tag').textContent,'企业级AI员工云平台');
  assert.equal(document.querySelector('.brand-logo img').getAttribute('src'),'/brand/mark.svg');
  assert.equal(document.querySelector('.view-title').textContent,'AI 员工名录');
  button('管理配置').click();await pause();
  const persona=[...document.querySelectorAll('.n-tabs-tab')].find(t=>t.textContent.trim()==='设定与模型');assert.ok(persona);persona.click();await pause();
  const input=document.querySelector('.emp-name-input input');assert.ok(input);input.value='Draft';input.dispatchEvent(new window.Event('input',{bubbles:true}));await pause();
  button('← 返回员工名录').click();await pause();assert.match(document.body.textContent,/存在未保存的配置草稿/);assert.ok(button('继续编辑'));assert.ok(document.querySelector('.employee-detail-container'));assert.deepEqual(errors,[]);
  button('继续编辑').click();await pause();assert.equal(document.querySelector('.emp-name-input input').value,'Draft');assert.ok(document.querySelector('.employee-detail-container'));
  assert.ok(requests.every(r=>r.url.startsWith('/api/v1/')));assert.equal(requests.some(r=>r.init.method && r.init.method!=='GET'),false);assert.deepEqual(errors,[]);
 }finally{app.unmount();host.remove();await pause()}
});

test('Market is the first and default destination, but explicit links still open their view',async()=>{
 const ws={id:'w',name:'Workspace',role:'owner',version:1};
 globalThis.fetch=async url=>{
  if(url==='/api/v1/me')return Response.json({user:{id:'u',name:'Owner',email:'owner@example.test'}});
  if(url.includes('/api/v1/workspaces?'))return Response.json({items:[ws],total:1});
  if(url==='/api/v1/workspaces/w')return Response.json(ws);
  if(url.includes('agent-templates'))return Response.json({items:[],total:0,page:1,page_size:20});
  return Response.json({error:{code:'not_found',message:'接口不存在'}},{status:404});
 };
 for(const [initial,expected,view] of [
  ['/','AI 员工市场','market'],
  ['/#/?ws=w','AI 员工市场','market'],
  ['/#/?ws=w&view=settings','设置','settings'],
  ['/#/?ws=w&view=home','首页','home']
 ]) {
  window.history.replaceState(null,'',initial);
  const host=document.createElement('div');document.body.appendChild(host);
  const errors=[];
  const app=createApp(RootApp);app.config.errorHandler=e=>errors.push(String(e));app.mount(host);
  try{
   await pause();await pause();
   const rail=[...host.querySelectorAll('nav[aria-label="主导航"] button')];
   assert.equal(rail[0]?.getAttribute('aria-label'),'AI 员工市场');
   assert.equal(rail[0]?.getAttribute('aria-current'),view==='market'?'page':null);
   assert.ok(!rail.some(button=>button.getAttribute('aria-label')==='首页'));
   assert.equal(document.title,`${expected} · Workspace · KIKO 企业级AI员工云平台`);
   assert.equal(window.location.hash,`#/?ws=w&view=${view}`);
   if(view==='market')assert.equal(host.querySelector('h1')?.textContent,'AI 员工市场');
   if(view==='home'){
    host.querySelector('.brand-logo').click();await pause();
    assert.equal(window.location.hash,'#/?ws=w&view=market');
    assert.equal(host.querySelector('h1')?.textContent,'AI 员工市场');
   }
   assert.deepEqual(errors,[]);
  }finally{app.unmount();host.remove();await pause()}
 }
});

test('Platform administration is the third settings tab for real platform administrators only',async()=>{
 const ws={id:'w',name:'Workspace',role:'owner',version:1};
 for(const scenario of [
  {admin:true,link:'view=settings',failWorkspace:false},
  {admin:true,link:'view=settings',failWorkspace:true},
  {admin:true,link:'view=platform-admin',failWorkspace:true},
  {admin:true,link:'view=settings&sub=platform-admin',failWorkspace:false},
  {admin:false,link:'view=platform-admin',failWorkspace:false},
  {admin:false,link:'view=settings&sub=platform-admin',failWorkspace:false}
 ]) {
  const canOpen=scenario.admin;
  const opensAdminDirectly=canOpen&&scenario.link!=='view=settings';
  const requests=[];
  const adminRequests=()=>requests.filter(url=>url.includes('/accounts?')||url.endsWith('/credit-settings'));
  globalThis.fetch=async url=>{
   requests.push(url);
   if(url==='/api/v1/me')return Response.json({user:{id:'u',name:'Owner',email:'owner@example.test',is_platform_admin:scenario.admin}});
   if(url.includes('/api/v1/workspaces?'))return Response.json({items:[ws],total:1});
   if(url==='/api/v1/workspaces/w'&&!scenario.failWorkspace)return Response.json(ws);
   if(url.includes('/accounts?'))return Response.json({items:[],total:0});
   if(url.endsWith('/credit-settings'))return Response.json({signup_bonus:'0',version:1});
   if(url.endsWith('/platform/mail-settings'))return Response.json({
    version:0,source:'environment',enabled:false,registration_verification_required:false,
    address:'',host:'',username:'',from:'',implicit_tls:false,password_configured:false
   });
   return Response.json({error:{code:'not_found',message:'接口不存在'}},{status:404});
  };
  window.history.replaceState(null,'',`/#/?ws=w&${scenario.link}`);
  const host=document.createElement('div');document.body.appendChild(host);
  const errors=[];
  const app=createApp(RootApp);app.config.errorHandler=e=>errors.push(String(e));app.mount(host);
  try{
   await pause();await pause();
   assert.equal(host.querySelector('nav[aria-label="主导航"] [aria-label="平台管理"]'),null);
   const tabs=[...host.querySelectorAll('.settings-main-tabs .n-tabs-tab')];
   const expected=['工作区设置','成员与权限'];
   if(canOpen)expected.push('平台管理');
   assert.deepEqual(tabs.map(tab=>tab.textContent.trim()),expected);
   assert.equal(window.location.hash,opensAdminDirectly?'#/?ws=w&view=settings&sub=platform-admin':'#/?ws=w&view=settings');
   if(!canOpen){
    assert.equal(host.querySelector('.accounts'),null);
    assert.deepEqual(adminRequests(),[]);
   }else{
    if(!opensAdminDirectly){
     if(scenario.failWorkspace)assert.match(host.textContent,/无法加载工作区设置/);
     assert.deepEqual(adminRequests(),[]);
     tabs[2].click();await pause();
     assert.equal(window.location.hash,'#/?ws=w&view=settings&sub=platform-admin');
    }
    assert.equal(host.querySelector('.accounts h1')?.textContent,'平台管理');
    assert.ok(host.querySelector('.accounts nav')?.textContent.includes('官方模型'));
    assert.equal(adminRequests().length,2);
    if(scenario.link==='view=settings'&&!scenario.failWorkspace){
     const button=(root,label)=>[...root.querySelectorAll('button')].find(node=>node.textContent.trim()===label);
     button(host,'邮件与注册').click();await pause();
     const field=host.querySelector('#platform-smtp-address');
     field.value='smtp.example.test:587';field.dispatchEvent(new window.Event('input',{bubbles:true}));await pause();
     tabs[0].click();await pause();
     assert.match(document.body.textContent,/平台配置尚未保存/);
     assert.ok(host.querySelector('.accounts'));
     assert.equal(window.location.hash,'#/?ws=w&view=settings&sub=platform-admin');
     button(document,'继续编辑').click();await pause();
     assert.equal(field.value,'smtp.example.test:587');
     tabs[0].click();await pause();
     button(document,'放弃并切换').click();await pause();
    }else{
     tabs[0].click();await pause();
    }
    assert.equal(host.querySelector('.accounts'),null);
    assert.equal(window.location.hash,'#/?ws=w&view=settings');
   }
   assert.ok(!requests.includes('/api/v1/auth/options'));
   assert.deepEqual(errors,[]);
  }finally{app.unmount();host.remove();await pause()}
 }
});

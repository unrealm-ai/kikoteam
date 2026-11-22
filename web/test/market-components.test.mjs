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
const built=await esbuild.build({stdin:{contents:"export {default as AgentMarketView} from './src/direction2/views/AgentMarketView.vue'; export {activeWorkspaceId} from './src/direction2/api/client'; export {createApp,h,nextTick} from 'vue'; export {NMessageProvider,NDialogProvider} from 'naive-ui';",resolveDir:process.cwd(),loader:'ts'},bundle:true,write:false,format:'esm',platform:'node',define:{'import.meta.env.VITE_PLATFORM_REAL':'true','import.meta.env.DEV':'false'},plugins:[{name:'vue-test',setup(build){build.onLoad({filter:/\.vue$/},async({path})=>{const source=await fs.readFile(path,'utf8');const {descriptor}=parse(source);return {contents:compileScript(descriptor,{id:path,inlineTemplate:true}).content,loader:'ts'};});}}]});
const {AgentMarketView,activeWorkspaceId,createApp,h,NMessageProvider,NDialogProvider,nextTick}=await import(`data:text/javascript;base64,${Buffer.from(built.outputFiles[0].text).toString('base64')}`);
const pause=async()=>{await new Promise(r=>setTimeout(r,40));await nextTick()};

const fixture={id:'template-one',scope:'official',version:2,sequence:1,template_version_id:'fixed-version',latest_version_id:'fixed-version',published_version_id:'fixed-version',is_listed:true,status:'published',has_unpublished_changes:false,content:{schema_version:1,name:'周报员',role:'整理工单与代码',avatar:'',category:'engineering',instruction_template:'整理周报',parameters:[],resource_refs:[],examples:[],packages:[{package_key:'weekly',name:'整理工单',description:'业务进展',onboarding_requirement:'required',skills:[],plugins:[]},{package_key:'code',name:'补充代码变更',description:'代码提交摘要',onboarding_requirement:'deferred',skills:[],plugins:[]}]}};
const button=text=>[...document.querySelectorAll('button')].find(b=>b.textContent.trim()===text);
async function mount(role='owner',create,template=fixture){
 const calls=[],guards=[],navigations=[];globalThis.fetch=async(url,init={})=>{
 calls.push({url,init,body:init.body?JSON.parse(init.body):null});
 if(url.endsWith('/agents/from-template'))return create?create():Response.json({id:'created-agent',name:'周报员'});
 if(url.includes('/capability-package-templates?'))return Response.json({items:[],total:0,page:1,page_size:20});
 if(url.includes('/skills?'))return Response.json({items:[{id:'skill',name:'分析数据',scope:'official',enabled:true,current_version_id:'sv1'}],total:1,page:1,page_size:20});
 if(url.includes('/plugins?'))return Response.json({items:[{id:'plugin',name:'内部系统',scope:'workspace',enabled:true,version_id:'pv2'}],total:1,page:1,page_size:20});
 if(url.includes('/model-profiles'))return Response.json({items:[{id:'model-one',name:'Test',enabled:true,validated:true,is_default:true}],total:1,page:1,page_size:20});
 if(url.includes('/official-agent-templates'))return Response.json({items:[template],total:1,page:1,page_size:20});
 if(url.endsWith('/agent-templates') && init.method==='POST')return Response.json({id:'workspace-copy',content:JSON.parse(init.body).content},{status:201});
 if(url.includes('/agent-templates?'))return Response.json({items:[],total:0,page:1,page_size:20});
 return Response.json({role});
 };
 activeWorkspaceId.value='workspace-one';const host=document.createElement('div');document.body.append(host);
 const app=createApp({render:()=>h(NMessageProvider,null,{default:()=>h(AgentMarketView,{platformAdmin:false,onNavigate:(...a)=>navigations.push(a)})})});app.provide('d2DraftGuard',{register:g=>guards.push(g),unregister:()=>{}});app.mount(host);await pause();
 return {calls,guards,navigations,async cleanup(){app.unmount();host.remove();await pause()}};
}
test('market card and detail display the portrait saved in the template', async () => {
 const template = {...fixture, content: {...fixture.content, avatar: 'cn-v2-06'}};
 const m = await mount('member', undefined, template);
 try {
  const portrait = '/employee-avatars/cn-v2-06.webp';
  assert.equal(document.querySelector('.template-card img')?.getAttribute('src'), portrait);
  button('查看详情').click();
  await pause();
  assert.equal(document.querySelector('.n-drawer img')?.getAttribute('src'), portrait);
 } finally {
  await m.cleanup();
 }
});
test('market member can browse fixed template but cannot create or manage',async()=>{const m=await mount('member');try{assert.equal(button('管理模板'),undefined);button('查看详情').click();await pause();assert.equal(button('创建员工需要工作区管理权限').disabled,true);assert.equal(m.calls.some(c=>c.init.method),false)}finally{await m.cleanup()}});
test('workspace owner copies a built-in role as an independent workspace draft',async()=>{
 const m=await mount('owner',undefined,{...fixture,builtin_key:'business-analyst'});
 try{
  button('查看详情').click();await pause();
  button('复制为工作区模板').click();await pause();
  const writes=m.calls.filter(c=>c.init.method==='POST');
  assert.equal(writes.length,1);
  assert.match(writes[0].url,/workspaces\/workspace-one\/agent-templates$/);
  assert.deepEqual(writes[0].body,{content:fixture.content});
  assert.ok(button('新建模板'));
 }finally{await m.cleanup()}
});
test('template creation preserves every preset ability and submits once without a request key',async()=>{
 let resolve;const m=await mount('owner',()=>new Promise(r=>{resolve=r}));try{
 button('查看详情').click();await pause();button('用此模板创建').click();await pause();button('下一步').click();await pause();
 assert.match(document.body.textContent,/模板已预设的能力/);assert.match(document.body.textContent,/整理工单/);assert.match(document.body.textContent,/补充代码变更/);assert.equal(document.querySelectorAll('.n-drawer input[type=checkbox]').length,0);
 button('下一步').click();await pause();button('创建员工').click();await pause();assert.equal(button('创建员工').disabled,true);
 const writes=m.calls.filter(c=>c.init.method==='POST');assert.equal(writes.length,1);assert.equal(writes[0].body.template_version_id,'fixed-version');assert.equal(writes[0].init.headers.has('Idempotency-Key'),false);assert.equal('selected_capabilities' in writes[0].body,false);
 resolve(Response.json({id:'created-agent',name:'周报员'}));await pause();assert.equal(m.guards[0].hasUnsaved(),false);assert.deepEqual(m.navigations[0],['employees',undefined,'created-agent']);
 }finally{await m.cleanup()}
});
test('network-unknown creation preserves form and does not automatically retry',async()=>{const m=await mount('owner',()=>Promise.reject(new TypeError('network failure')));try{button('查看详情').click();await pause();button('用此模板创建').click();await pause();button('下一步').click();await pause();button('下一步').click();await pause();button('创建员工').click();await pause();assert.match(document.body.textContent,/先到 AI 员工列表核对/);assert.equal(m.calls.filter(c=>c.init.method==='POST').length,1);assert.equal(m.navigations.length,0)}finally{await m.cleanup()}});

test('create a market preset directly without a workspace package template',async()=>{
 const m=await mount();
 try {
  button('工作区模板').click();await pause();
  button('管理模板').click();await pause();
  button('新建模板').click();await pause();
  button('新建能力包').click();await pause();
  const field=document.querySelector('input[placeholder="能力名称"]');
  field.value='业务分析';field.dispatchEvent(new window.Event('input',{bubbles:true}));
  const pack=field.closest('article');
  for(const [label,choice] of [['技能','分析数据'],['MCP 插件','内部系统']]){
   const item=[...pack.querySelectorAll('.n-form-item')].find(el=>el.textContent.includes(label));
   item.querySelector('.n-base-selection').click();await pause();
   const option=[...document.querySelectorAll('.n-base-select-option')].find(el=>el.textContent.trim()===choice);
   assert.ok(option,choice);option.click();document.body.click();await pause();
  }
  button('保存草稿').click();await pause();
  const c=m.calls.find(call=>call.init.method==='POST'&&call.url.endsWith('/agent-templates')).body.content;
  assert.equal(c.packages[0].name,'业务分析');
  const [skill,plugin]=c.resource_refs;
  assert.equal(skill.workspace_id,undefined);
  assert.equal(plugin.workspace_id,'workspace-one');
  assert.deepEqual(c.packages[0].skills,[{resource_key:skill.resource_key}]);
  assert.deepEqual(c.packages[0].plugins,[{resource_key:plugin.resource_key}]);
 }finally{await m.cleanup()}
});

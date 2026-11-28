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
const built=await esbuild.build({stdin:{contents:"export {default as Setup} from './src/direction2/views/AgentSetupPanel.vue'; export {default as AgentMarketView} from './src/direction2/views/AgentMarketView.vue'; export {activeWorkspaceId} from './src/direction2/api/client'; export {createApp,h,nextTick,ref} from 'vue'; export {NMessageProvider,NDialogProvider} from 'naive-ui';",resolveDir:process.cwd(),loader:'ts'},bundle:true,write:false,format:'esm',platform:'node',define:{'import.meta.env.VITE_PLATFORM_REAL':'true','import.meta.env.DEV':'false'},plugins:[{name:'vue-test',setup(build){build.onLoad({filter:/\.vue$/},async({path})=>{const source=await fs.readFile(path,'utf8');const {descriptor}=parse(source);return {contents:compileScript(descriptor,{id:path,inlineTemplate:true}).content,loader:'ts'};});}}]});
const {Setup,AgentMarketView,activeWorkspaceId,createApp,h,NMessageProvider,NDialogProvider,nextTick,ref}=await import(`data:text/javascript;base64,${Buffer.from(built.outputFiles[0].text).toString('base64')}`);
const pause=async()=>{await new Promise(r=>setTimeout(r,40));await nextTick()};

const fixture={id:'template-one',scope:'official',version:2,sequence:1,template_version_id:'fixed-version',latest_version_id:'fixed-version',published_version_id:'fixed-version',is_listed:true,status:'published',has_unpublished_changes:false,content:{schema_version:1,name:'周报员',role:'整理工单与代码',avatar:'',category:'engineering',instruction_template:'整理周报',parameters:[],resource_refs:[],examples:[],packages:[{package_key:'weekly',name:'整理工单',description:'业务进展',onboarding_requirement:'required',skills:[],plugins:[]},{package_key:'code',name:'补充代码变更',description:'代码提交摘要',onboarding_requirement:'deferred',skills:[],plugins:[]}]}};
const button=(text,root=document)=>[...root.querySelectorAll('button')].find(b=>b.textContent.trim()===text);
const drawerButton=text=>button(text,document.querySelector('.n-drawer')??document.createElement('div'));
async function mount(role='owner',create,template=fixture,extraProps={},scenario={}){
 const calls=[],guards=[],navigations=[],demos=[];let assistantOpens=0,workspaceTemplate;
 const list=items=>Response.json({items,total:items.length,page:1,page_size:20});globalThis.fetch=async(url,init={})=>{
 calls.push({url,init,body:init.body?JSON.parse(init.body):null});
 if(url.endsWith('/agents') && init.method==='POST')return Response.json({id:'quick-created',name:JSON.parse(init.body).name,role:JSON.parse(init.body).role,avatar:JSON.parse(init.body).avatar,enabled:true,model_profile_id:'model-one',harness:'pi'});
 if(url.endsWith('/agents/from-template'))return create?create():Response.json({id:'created-agent',name:'周报员'});
 if(url.includes('/capability-package-templates?'))return Response.json({items:[],total:0,page:1,page_size:20});
 if(url.includes('/skills?'))return Response.json({items:[{id:'skill',name:'分析数据',scope:'official',enabled:true,current_version_id:'sv1'}],total:1,page:1,page_size:20});
 if(url.includes('/plugins?'))return Response.json({items:[{id:'plugin',name:'内部系统',scope:'workspace',enabled:true,version_id:'pv2'}],total:1,page:1,page_size:20});
 if(url.includes('/model-profiles'))return Response.json({items:[{id:'model-one',name:'Test',protocol:'chat_completions',enabled:true,validated:true,is_default:true}],total:1,page:1,page_size:20});
 if(url.endsWith('/agents/employee-one/setup-status'))return Response.json({conversation_model:{available:true},task_model:{available:true},access_allowed:true,packages:[],issues:[],examples:[]});
 if(url.endsWith('/agents/employee-one/agent-templates')&&init.method==='POST'){
  workspaceTemplate={...fixture,id:'workspace-copy',scope:'workspace',content:{...fixture.content,name:'另存的员工'},status:'draft',is_listed:false,published_version_id:''};
  return Response.json(workspaceTemplate,{status:201});
 }
 if(url.includes('/official-agent-templates?'))return list([template]);
 if(url.includes('/official-agent-templates/template-one/versions?'))return list([]);
 if(url.endsWith('/official-agent-templates/template-one?view=manage'))return Response.json(scenario.draft??template);
 if(url.endsWith('/agent-templates') && init.method==='POST'){
  workspaceTemplate={...fixture,id:'workspace-copy',scope:'workspace',content:JSON.parse(init.body).content,status:'draft',is_listed:false,template_version_id:'draft-version',latest_version_id:'draft-version',published_version_id:''};
  return Response.json(workspaceTemplate,{status:201});
 }
 if(url.includes('/agent-templates?')){
  const requested=new URL(url,'https://test.invalid').searchParams.get('status')||'published';
  return list(workspaceTemplate?.status===requested?[workspaceTemplate]:[]);
 }
 if(url.includes('/agent-templates/workspace-copy/versions?'))return list([]);
 if(url.endsWith('/agent-templates/workspace-copy?view=manage'))return Response.json(workspaceTemplate);
 if(url.endsWith('/agent-templates/workspace-copy/publish') && init.method==='POST'){
  workspaceTemplate={...workspaceTemplate,status:'published',is_listed:true,published_version_id:'draft-version'};
  return Response.json(workspaceTemplate);
 }
 if(url.endsWith('/agent-templates/workspace-copy') && init.method==='PATCH'){
  const {is_listed,content}=JSON.parse(init.body);
  workspaceTemplate={...workspaceTemplate,is_listed,status:is_listed?'published':'unlisted',content:content??workspaceTemplate.content};
  return Response.json(workspaceTemplate);
 }
 return Response.json({role});
 };
 activeWorkspaceId.value='workspace-one';const host=document.createElement('div');document.body.append(host);
 const setup=ref(scenario.fromEmployee===true),initialTab=ref(extraProps.initialTab);
 const app=createApp({render:()=>h(NMessageProvider,null,{default:()=>setup.value
  ? h(Setup,{agent:{id:'employee-one',name:'另存的员工',version:3,config_version:7},canManage:true,canManageTemplates:true,onNavigate:(...a)=>{navigations.push(a);initialTab.value=a[1];setup.value=false;}})
  : h(AgentMarketView,{platformAdmin:false,...extraProps,initialTab:initialTab.value,onNavigate:(...a)=>navigations.push(a),onAssistantDemo:c=>demos.push(c),onOpenAssistant:()=>{assistantOpens+=1}})})});app.provide('d2DraftGuard',{register:g=>guards.push(g),unregister:()=>{}});app.mount(host);await pause();
 return {calls,guards,navigations,demos,get assistantOpens(){return assistantOpens},async cleanup(){app.unmount();host.remove();await pause()}};
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
test('market keeps decision-making copy while hiding version metadata in browse mode', async () => {
 const template={...fixture,builtin_key:'market-reporter',content:{...fixture.content,deliverables:'每日上午一份行情简报'}};
 const m=await mount('member',undefined,template);
 try {
  const card=document.querySelector('.template-card');
  assert.match(card.textContent,/整理工单与代码/);
  assert.match(card.textContent,/每日上午一份行情简报/);
  assert.equal(card.querySelector('.chips'),null);
  assert.doesNotMatch(card.querySelector('.card-actions').textContent,/官方模板|v1/);
  button('查看详情',card).click();await pause();
  assert.ok(drawerButton('一键创建行情简报员'));
  assert.match(document.querySelector('.n-drawer').textContent,/模板更新不会影响已创建的员工/);
 } finally {await m.cleanup()}
});

test('market member can create from a published template without gaining template management',async()=>{const m=await mount('member');try{assert.equal(button('管理模板'),undefined);assert.equal(button('新建模板'),undefined);assert.equal(button('编辑模板'),undefined);button('工作区模板').click();await pause();assert.equal(button('新建模板'),undefined);assert.ok(!m.calls.some(c=>c.url.includes('view=manage')));button('官方精选').click();await pause();button('查看详情').click();await pause();assert.equal(drawerButton('用此模板创建').disabled,false);assert.equal(button('复制为工作区模板'),undefined);assert.equal(m.calls.some(c=>c.init.method),false)}finally{await m.cleanup()}});
test('template creation follows the selected scope and existing roles', async()=>{
 const m=await mount('member',undefined,fixture,{platformAdmin:true});
 try {
  const toolbar=document.querySelector('.template-toolbar');
  assert.ok(button('新建模板',toolbar));
  assert.ok(button('编辑模板'));
  button('工作区模板',toolbar).click();await pause();
  assert.equal(button('新建模板',toolbar),undefined);
  assert.equal(m.calls.some(c=>c.url.includes('view=manage')),false);
  button('官方精选',toolbar).click();await pause();
  button('新建模板',toolbar).click();await pause();
  assert.ok(drawerButton('保存草稿'));
  assert.equal(m.calls.some(c=>c.init.method),false);
 }finally{await m.cleanup()}
});
test('workspace owner copies a built-in role as an independent workspace draft',async()=>{
 const m=await mount('owner',undefined,{...fixture,builtin_key:'business-analyst'});
 try{
  button('查看详情').click();await pause();
  button('复制为工作区模板').click();await pause();
  const writes=m.calls.filter(c=>c.init.method==='POST');
  assert.equal(writes.length,1);
  assert.match(writes[0].url,/workspaces\/workspace-one\/agent-templates$/);
  assert.deepEqual(writes[0].body,{content:fixture.content});
  assert.ok(button('新建模板',document.querySelector('.template-toolbar')));
  assert.ok(button('编辑模板'));
  assert.ok(m.calls.some(c=>c.url.includes('view=manage&status=draft')));
 }finally{await m.cleanup()}
});
test('saving an existing employee lands directly in the workspace draft list', async()=>{
 const m=await mount('owner',undefined,fixture,{}, {fromEmployee:true});
 try {
  button('另存为工作区模板').click();await pause();await pause();
  assert.deepEqual(m.navigations[0],['market','workspace-drafts']);
  assert.equal(button('工作区模板').getAttribute('aria-pressed'),'true');
  assert.match(document.querySelector('.template-card').textContent,/另存的员工.*未发布/s);
  assert.ok(button('编辑模板'));
  assert.equal(button('用此模板创建'),undefined);
  assert.equal(m.calls.filter(c=>c.init.method==='POST').length,1);
 }finally{await m.cleanup()}
});

test('a member opening a draft link only reads published workspace templates', async()=>{
 const m=await mount('member',undefined,fixture,{initialTab:'workspace-drafts'});
 try {
  assert.equal(button('工作区模板').getAttribute('aria-pressed'),'true');
  assert.ok(m.calls.some(c=>c.url.includes('/agent-templates?')));
  assert.equal(m.calls.some(c=>c.url.includes('view=manage')),false);
  assert.equal(button('新建模板'),undefined);
 }finally{await m.cleanup()}
});

test('one published card edits the saved draft but creates from the published version', async()=>{
 const draft={...fixture,template_version_id:'unpublished-version',latest_version_id:'unpublished-version',has_unpublished_changes:true,content:{...fixture.content,name:'未发布改名',instruction_template:'草稿指令'}};
 const m=await mount('owner',undefined,fixture,{platformAdmin:true},{draft});
 try {
  assert.match(document.querySelector('.template-card').textContent,/周报员/);
  button('编辑模板').click();await pause();
  assert.ok([...document.querySelectorAll('.n-drawer input')].some(i=>i.value==='未发布改名'));
  assert.ok([...document.querySelectorAll('.n-drawer textarea')].some(i=>i.value==='草稿指令'));
  drawerButton('关闭').click();await pause();
  button('用此模板创建',document.querySelector('.template-card')).click();await pause();
  assert.ok([...document.querySelectorAll('.n-drawer input')].some(i=>i.value==='周报员'));
  assert.equal(document.querySelector('.n-drawer').textContent.includes('草稿指令'),false);
  drawerButton('下一步').click();await pause();drawerButton('下一步').click();await pause();drawerButton('创建员工').click();await pause();
  assert.equal(m.calls.find(c=>c.url.endsWith('/agents/from-template')).body.template_version_id,'fixed-version');
 }finally{await m.cleanup()}
});

test('template creation preserves every preset ability and submits once without a request key',async()=>{
 let resolve;const m=await mount('owner',()=>new Promise(r=>{resolve=r}));try{
 button('用此模板创建',document.querySelector('.template-card')).click();await pause();assert.equal(drawerButton('查看详情'),undefined);assert.match(document.querySelector('.n-drawer').textContent,/岗位设定/);button('下一步').click();await pause();
 assert.match(document.body.textContent,/模板已预设的能力/);assert.match(document.body.textContent,/整理工单/);assert.match(document.body.textContent,/补充代码变更/);assert.equal(document.querySelectorAll('.n-drawer input[type=checkbox]').length,0);
 button('下一步').click();await pause();button('创建员工').click();await pause();assert.equal(button('创建员工').disabled,true);
 const writes=m.calls.filter(c=>c.init.method==='POST');assert.equal(writes.length,1);assert.equal(writes[0].body.template_version_id,'fixed-version');assert.equal(writes[0].init.headers.has('Idempotency-Key'),false);assert.equal('selected_capabilities' in writes[0].body,false);
 resolve(Response.json({id:'created-agent',name:'周报员'}));await pause();assert.equal(m.guards[0].hasUnsaved(),false);assert.deepEqual(m.navigations[0],['employees',undefined,'created-agent']);
 }finally{await m.cleanup()}
});
test('network-unknown creation preserves form and does not automatically retry',async()=>{const m=await mount('owner',()=>Promise.reject(new TypeError('network failure')));try{button('查看详情').click();await pause();drawerButton('用此模板创建').click();await pause();button('下一步').click();await pause();button('下一步').click();await pause();button('创建员工').click();await pause();assert.match(document.body.textContent,/先到 AI 员工列表核对/);assert.equal(m.calls.filter(c=>c.init.method==='POST').length,1);assert.equal(m.navigations.length,0)}finally{await m.cleanup()}});

test('create a market preset directly without a workspace package template',async()=>{
 const m=await mount();
 try {
  button('工作区模板').click();await pause();
  assert.equal(button('管理模板'),undefined);
  button('新建模板',document.querySelector('.template-toolbar')).click();await pause();
  assert.equal(button('返回市场'),undefined);
  assert.ok(drawerButton('保存草稿'));
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
  drawerButton('关闭').click();await pause();
  assert.ok(button('编辑模板'));
  assert.equal(button('用此模板创建'),undefined);
  button('编辑模板').click();await pause();
  drawerButton('发布并上架').click();await pause();
  drawerButton('关闭').click();await pause();
  assert.ok(button('查看详情'));assert.ok(button('用此模板创建'));
  assert.doesNotMatch(m.calls.filter(c=>c.url.includes('/agent-templates?')).at(-1).url,/view=manage/);
  button('编辑模板').click();await pause();
  drawerButton('下架').click();await pause();
  drawerButton('关闭').click();await pause();
  assert.ok(button('编辑模板'));assert.equal(button('用此模板创建'),undefined);
  assert.match(m.calls.filter(c=>c.url.includes('/agent-templates?')).at(-1).url,/view=manage&status=unlisted/);
 }finally{await m.cleanup()}
});

test('market puts assistant creation in the demo header and keeps demo clicks unchanged', async () => {
 const card = {key:'complaints',title:'客诉太多，处理不过来？',description:'同时查两单，分别给出处理方案。',input:'demo-input'};
 const m = await mount('member', undefined, fixture, {assistantAgentId:'assistant-1',assistantCards:[card]});
 try {
  const section = document.querySelector('.market-onboarding');
  const action = button('让 KIKO 帮我创建员工', section.querySelector('.onboarding-header'));
  const cardBtn = section.querySelector('.demo-card');
  assert.ok(action && cardBtn);
  assert.equal(section.querySelector('.onboarding-pill').textContent,'体验场景使用样例数据');
  assert.doesNotMatch(cardBtn.textContent,/并行推进两单|同时查两单，分别给出处理方案/);
  assert.equal(cardBtn.querySelector('.demo-card-art').getAttribute('src'),'/brand/assistant-experiences/complaints.webp');
  assert.equal(cardBtn.querySelector('.demo-card-capability').textContent,'并行推荐任务');
  assert.equal(action.compareDocumentPosition(cardBtn) & window.Node.DOCUMENT_POSITION_FOLLOWING, window.Node.DOCUMENT_POSITION_FOLLOWING);
  assert.notEqual(cardBtn.textContent.indexOf(card.title),-1);
  cardBtn.click();
  action.click();
  await pause();
  assert.deepEqual(m.demos,[card]);
  assert.equal(m.assistantOpens,1);
  assert.doesNotMatch(document.querySelector('.market').textContent,/快速创建员工|空白快速创建/);
  assert.deepEqual(m.navigations,[]);
  assert.equal(m.calls.some(c=>c.init.method==='POST'),false);
 } finally { await m.cleanup(); }
});

test('without demo access the assistant remains optional and blank creation stays off the market', async () => {
 for (const [role,assistantAgentId,expectAction] of [['member','assistant-1',true],['member',undefined,false],['viewer','assistant-1',false]]) {
  const m = await mount(role,undefined,fixture,{assistantAgentId,assistantCards:[]});
  try {
   assert.equal(Boolean(document.querySelector('.market-onboarding.is-compact')),expectAction);
   assert.equal(Boolean(button('让 KIKO 帮我创建员工')),expectAction);
   assert.equal(Boolean(button('用此模板创建')),role==='member');
   assert.doesNotMatch(document.querySelector('.market').textContent,/快速创建员工|空白快速创建/);
  } finally {await m.cleanup();}
 }
});

test('market template details close when the backdrop is clicked', async () => {
 const m = await mount('member');
 try {
  button('查看详情').click();
  await pause();
  const mask = document.querySelector('.n-drawer-mask');
  assert.ok(mask);
  assert.ok(drawerButton('用此模板创建'));
  mask.click();
  await new Promise(r => setTimeout(r, 350));
  assert.equal(drawerButton('用此模板创建'), undefined);
 } finally { await m.cleanup(); }
});

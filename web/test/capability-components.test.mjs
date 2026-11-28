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
const built=await esbuild.build({stdin:{contents:"export {default as ResourceDirectory} from './src/direction2/views/ResourceDirectory.vue'; export {default as PlatformPluginAccount} from './src/direction2/views/PlatformPluginAccount.vue'; export {default as PlatformSkillsPanel} from './src/direction2/views/PlatformSkillsPanel.vue'; export {default as PlatformPluginsPanel} from './src/direction2/views/PlatformPluginsPanel.vue'; export {default as PlatformPackagesPanel} from './src/direction2/views/PlatformPackagesPanel.vue'; export {skillsApi,pluginsApi,packagesApi} from './src/direction2/api/capabilities'; export {activeWorkspaceId} from './src/direction2/api/client'; export {createApp,h,nextTick} from 'vue'; export {NMessageProvider,NDialogProvider} from 'naive-ui';",resolveDir:process.cwd(),loader:'ts'},bundle:true,write:false,format:'esm',platform:'node',define:{'import.meta.env.VITE_PLATFORM_REAL':'true','import.meta.env.DEV':'false'},plugins:[{name:'vue-test',setup(build){build.onLoad({filter:/\.css$/},()=>({contents:'',loader:'text'}));build.onLoad({filter:/\.vue$/},async({path})=>{const source=await fs.readFile(path,'utf8');const {descriptor}=parse(source);return {contents:compileScript(descriptor,{id:path,inlineTemplate:true}).content,loader:'ts'};});}}]});
const {ResourceDirectory,skillsApi,pluginsApi,packagesApi,PlatformPluginAccount,PlatformSkillsPanel,PlatformPluginsPanel,PlatformPackagesPanel,activeWorkspaceId,createApp,h,NMessageProvider,NDialogProvider,nextTick}=await import(`data:text/javascript;base64,${Buffer.from(built.outputFiles[0].text).toString('base64')}`);
const pause=async(ms=40)=>{await new Promise(r=>setTimeout(r,ms));await nextTick()};
const button=text=>[...document.querySelectorAll('button')].find(b=>b.textContent.trim()===text);
const manifest={format_version:1,files:[{path:'SKILL.md',size:40,sha256:'digest'},{path:'scripts/run.py',size:12,sha256:'script'}],digest:'bundle'};
const version={id:'sv1',sequence:1,version_label:'',technical_name:'test-skill',description:'Test',manifest,source:{type:'zip'},created_at:'2026-11-16'};
const skill={id:'skill',name:'Test skill',description:'Test',enabled:true,version:1,current_version_id:'sv1',current_version:version};
const plugin={id:'plugin',name:'MCP fixture',definition_version:1,description:'',endpoint:'https://example.test/mcp',auth_type:'bearer',header_name:'',enabled:true,version:1,version_id:'pv1'};
const packageItem={id:'package',name:'Work package',description:'',enabled:true,version:1,scope:{mode:'inherit_with_blacklist',allow_member_ids:[],deny_member_ids:[]},skills:[{skill_id:'skill',version_id:'sv1'}],plugins:[]};
function page(items,p=1){return {items,total:items.length,page:p,page_size:20};}
async function mount(component,{role='owner',props={},handle}={}){const calls=[],guards=[];globalThis.fetch=async(url,init={})=>{const body=init.body?JSON.parse(init.body):undefined;calls.push({url,init,body});const custom=await handle?.(url,init,body);if(custom)return custom;if(url.includes('/selectors/people/sources?'))return Response.json({items:[{provider:'web',available:true,supports_department_tree:false}]});if(url.includes('/selectors/people?'))return Response.json({items:[],next_cursor:null});if(url.endsWith('/selectors/people/resolve'))return Response.json({items:[],next_cursor:null});if(url.endsWith('/triggers'))return Response.json({items:[]});if(url.includes('/skills/')&&url.includes('/versions'))return Response.json(page([version]));if(url.includes('/skills?'))return Response.json(page([skill]));if(url.includes('/plugins?'))return Response.json(page([plugin]));if(url.includes('/members?'))return Response.json(page([{id:'member',name:'You',email:'you@example.test',status:'active'}]));if(url.includes('/capability-package-templates'))return Response.json(page([]));if(url.includes('/capability-packages?'))return Response.json(page([packageItem]));return Response.json({role});};activeWorkspaceId.value='w';const host=document.createElement('div');document.body.appendChild(host);const app=createApp({render:()=>h(NMessageProvider,null,{default:()=>h(NDialogProvider,null,{default:()=>h(component,props)})})});app.provide('d2DraftGuard',{register:g=>guards.push(g),unregister:()=>{}});app.mount(host);await pause();return {calls,guards,async cleanup(){app.unmount();host.remove();await pause()}};}
function input(placeholder,value){const el=[...document.querySelectorAll('input,textarea')].find(e=>e.placeholder===placeholder);assert.ok(el,placeholder);el.value=value;el.dispatchEvent(new window.Event('input',{bubbles:true}));}
function radio(text){[...document.querySelectorAll('.n-radio')].find(e=>e.textContent.trim()===text)?.click();}
test('Skill offers ZIP, GitHub and Markdown and publishes import reference, not lossy prompt fields',async()=>{const m=await mount(PlatformSkillsPanel,{handle:(url,init)=>{if(init.method==='POST'&&url.endsWith('/skill-imports'))return Response.json({id:'import',technical_name:'test-skill',description:'Test',manifest,source:{type:'markdown'},expires_at:new Date(Date.now()+60000).toISOString()});if(init.method==='POST'&&url.endsWith('/skills'))return Response.json(skill);}});try{button('+ 导入技能').click();await pause();assert.deepEqual([...document.querySelectorAll('.n-radio')].map(e=>e.textContent.trim()),['ZIP','GitHub','Markdown']);radio('Markdown');await pause();input('粘贴包含 name / description 的 SKILL.md','---\nname: test-skill\ndescription: Test\n---\nBody');button('校验并预览').click();await pause();assert.match(document.body.textContent,/scripts\/run.py/);button('保存 Skill').click();await pause();const save=m.calls.find(c=>c.init.method==='POST'&&c.url.endsWith('/skills'));assert.equal(save.body.import_id,'import');assert.equal('promptTemplate' in save.body,false);assert.equal(save.init.credentials,'include');assert.equal(save.init.headers.get('X-CSRF-Protection'),'1');}finally{await m.cleanup();}});
test('Editing import invalidates receipt; late import cannot resurrect editor after workspace change',async()=>{let finish;const m=await mount(PlatformSkillsPanel,{handle:(url,init)=>init.method==='POST'?new Promise(r=>finish=r):undefined});try{button('+ 导入技能').click();await pause();radio('Markdown');await pause();input('粘贴包含 name / description 的 SKILL.md','first');button('校验并预览').click();await pause();activeWorkspaceId.value='other';await pause();finish(Response.json({id:'late',technical_name:'STALE IMPORT',manifest,source:{},expires_at:new Date(Date.now()+60000).toISOString()}));await pause();assert.doesNotMatch(document.body.textContent,/STALE IMPORT/);assert.equal(button('保存 Skill'),undefined);}finally{await m.cleanup();}});
test('Plugin resource editor has no secret field or fake connection validation',async()=>{const m=await mount(PlatformPluginsPanel);try{button('+ 新增插件').click();await pause();assert.equal(document.querySelector('input[type=password]'),null);assert.match(document.body.textContent,/账号密钥/);assert.match(document.body.textContent,/定义保存不表示服务已连接/);assert.equal(button('测试并保存账号'),undefined);}finally{await m.cleanup();}});
test('Member cannot create resource or package; package retains pinned skill version',async()=>{let m=await mount(PlatformSkillsPanel,{role:'member'});try{assert.equal(button('+ 导入技能'),undefined);assert.equal(button('上传新版本'),undefined);}finally{await m.cleanup();}m=await mount(PlatformPackagesPanel,{props:{employeeId:'employee'},handle:(url,init,body)=>init.method==='PATCH'?Response.json({...packageItem,...body}):undefined});try{button('配置能力包').click();await pause();await pause();assert.match(document.body.textContent,/统一使用范围/);assert.match(document.body.textContent,/四渠道身份独立/);const name=document.querySelector('.n-drawer input');name.value='Renamed';name.dispatchEvent(new window.Event('input',{bubbles:true}));await pause();button('保存能力包').click();await pause();const save=m.calls.find(c=>c.init.method==='PATCH');assert.equal(save.body.skills,undefined);assert.equal(save.body.version,1);assert.ok(save.url.includes('/agents/employee/capability-packages/package'));}finally{await m.cleanup();}});

test('Employee account preserves omitted credentials, requires explicit replacement, never echoes secrets',async()=>{
const account={id:'account',plugin_id:'plugin',plugin_version_id:'pv1',name:'Existing account',version:3,enabled:true,validated:true,tools:[]};
const m=await mount(PlatformPluginAccount,{props:{employeeId:'employee',plugin,versionId:'pv1',canManage:true},handle:(url,init)=>url.includes('/connection-source')?Response.json({source:'agent',revision:1,account}):url.includes('/plugin-accounts/')?Response.json(account):undefined});
try{assert.equal(document.querySelector('input[type=password]'),null);button('测试并保存账号').click();await pause();const put=m.calls.find(c=>c.init.method==='PUT');assert.equal(put.body.version,3);assert.equal('secret' in put.body,false);assert.equal(put.body.plugin_version_id,'pv1');const checkbox=document.querySelector('.n-checkbox');checkbox.click();await pause();assert.ok(document.querySelector('input[type=password]'));assert.equal(button('测试并保存账号').disabled,true);}finally{await m.cleanup();}
});

test('Official Tavily saves an API key to the selected connection', async () => {
  const tavily = {...plugin, scope:'official', endpoint:'https://mcp.tavily.com/mcp', name:'Tavily 联网搜索'};
  const m = await mount(PlatformPluginAccount, {
    props:{employeeId:'employee',plugin:tavily,versionId:'pv1',canManage:true},
    handle:(url, init) => {
      if (url.includes('/connection-source')) return Response.json({source:'workspace', revision:0});
      if (init.method === 'PUT') return Response.json({id:'account',version:1,plugin_version_id:'pv1',enabled:true,validated:true,tools:[]});
    },
  });
  try {
    const field = document.querySelector('input[type=password]');
    assert.match(field.closest('label').textContent, /Tavily API Key/);
    field.value = 'tvly-test-key';
    field.dispatchEvent(new window.Event('input', {bubbles:true}));
    await pause();
    button('测试并保存为空间共享账号').click();
    await pause();
    const saved = m.calls.find(c => c.init.method === 'PUT');
    assert.equal(saved.body.secret, 'tvly-test-key');
    assert.ok(saved.url.endsWith('/workspaces/w/plugin-accounts/plugin'));
    assert.equal(document.querySelector('input[type=password]'), null);
  } finally { await m.cleanup(); }
});

test('Official GitHub defaults to PAT and opens OAuth for the selected connection owner', async () => {
  const github = {...plugin, scope:'official', name:'GitHub', endpoint:'https://api.githubcopilot.com/mcp/readonly',
    auth_type:'oauth', supported_auth_methods:['bearer','oauth'], default_auth_method:'bearer'};
  const authURL = 'https://github.com/login/oauth/authorize?state=test';
  const open = window.open;
  let redirected;
  window.open = () => ({location:{replace:url => {redirected=url}},close(){}});
  try {
    for (const source of ['workspace','agent']) {
      redirected = undefined;
      const m = await mount(PlatformPluginAccount,{props:{employeeId:'employee',plugin:github,versionId:'pv1',canManage:true},handle:(url,init,body) => {
        if (url.includes('/connection-source')) return Response.json({source,revision:0});
        if (url.endsWith('/oauth/authorize')) return Response.json({authorization_url:authURL});
        if (init.method==='PUT') return Response.json({...body,id:'conn',version:1,enabled:true,validated:true,tools:[],agent_count:2});
      }});
      try {
        const picker=document.querySelector('select[aria-label="认证方式"]');
        assert.equal(picker.value,'bearer');
        const password=document.querySelector('input[type=password]');
        password.value='fixture-pat'; password.dispatchEvent(new window.Event('input',{bubbles:true})); await pause();
        button(source==='workspace'?'测试并保存为空间共享账号':'测试并保存账号').click(); await pause();
        const saved=m.calls.find(c=>c.init.method==='PUT');
        assert.equal(saved.body.auth_method,'bearer'); assert.equal(saved.body.secret,'fixture-pat');
        assert.equal(saved.url.includes('/agents/employee/'),source==='agent');
        picker.value='oauth'; picker.dispatchEvent(new window.Event('change',{bubbles:true})); await pause();
        assert.equal(document.querySelector('input[type=password]'),null);
        if (source==='workspace') assert.match(document.body.textContent,/2 位 AI 员工/);
        button('前往 GitHub 授权').click(); await pause();
        const grant=m.calls.find(c=>c.url.endsWith('/oauth/authorize'));
        assert.equal(grant.url.includes('/agents/employee/'),source==='agent');
        assert.equal(grant.body.version,1); assert.equal(grant.body.secret,undefined);
        assert.equal(redirected,authURL);
      } finally { await m.cleanup(); }
    }
    const account={id:'conn',version:4,name:'Existing OAuth',plugin_version_id:'pv1',auth_method:'oauth',enabled:true,validated:true,tools:[]};
    const m=await mount(PlatformPluginAccount,{props:{employeeId:'employee',plugin:{...github,auth_type:'bearer'},versionId:'pv1',canManage:true},handle:url=>url.includes('/connection-source')?Response.json({source:'agent',revision:1,account}):undefined});
    try {
      const picker=document.querySelector('select[aria-label="认证方式"]');
      assert.equal(picker.value,'oauth');
      picker.value='bearer'; picker.dispatchEvent(new window.Event('change',{bubbles:true})); await pause();
      assert.ok(document.querySelector('input[type=password]')); assert.equal(button('测试并保存账号').disabled,true);
    } finally {await m.cleanup();}
  } finally {window.open=open;}
});

test('A new private GitHub grant keeps the shared account active while consent is pending or rejected', async () => {
  const github={...plugin,scope:'official',name:'GitHub',endpoint:'https://api.githubcopilot.com/mcp/readonly',
    supported_auth_methods:['bearer','oauth'],default_auth_method:'bearer'};
  const account={id:'shared',version:5,name:'Team account',plugin_version_id:'pv1',auth_method:'bearer',enabled:true,validated:true,tools:[]};
  const originalOpen=window.open;
  window.open=()=>({close(){}});
  const m=await mount(PlatformPluginAccount,{props:{employeeId:'employee',plugin:github,versionId:'pv1',canManage:true,allowSourceChange:true},handle:(url,init)=>{
    if(url.includes('/connection-source')) return Response.json({source:'workspace',revision:0,account});
    if(url.endsWith('/oauth/authorize')) return Response.json({error:{message:'网页授权失败'}},{status:502});
    if(!init.method || init.method==='GET') return new Response(null,{status:404});
  }});
  try {
    document.querySelector('.n-base-selection').click(); await pause();
    const option=[...document.querySelectorAll('.n-base-select-option')].find(e=>e.textContent.trim()==='此数字人独享账号');
    assert.ok(option); option.click(); await pause();
    assert.match(document.body.textContent,/验证期间，此 AI 员工继续使用空间共享账号/);
    const picker=document.querySelector('select[aria-label="认证方式"]');
    picker.value='oauth'; picker.dispatchEvent(new window.Event('change',{bubbles:true})); await pause();
    button('前往 GitHub 授权').click(); await pause();
    const attempt=m.calls.find(c=>c.url.endsWith('/oauth/authorize'));
    assert.ok(attempt.url.includes('/agents/employee/')); assert.equal(attempt.body.version,0);
    assert.match(document.body.textContent,/网页授权失败/);
    assert.equal(m.calls.some(c=>c.init.method==='PUT'),false);
  } finally {window.open=originalOpen; await m.cleanup();}
});

test('Catalog cards share the selected summary with details and keep authored workspace copy', async () => {
  const official = {...plugin, id:'git', name:'GitHub', scope:'official', assigned_agent_count:2,
    shared_connection_state:'revoked', card:{category:'dev',summary_zh:'读取仓库改动',summary_en:'Read repository changes',highlights:'PR 和提交记录'}};
  const custom = {...plugin, id:'custom', name:'GitHub', scope:'workspace', description:'团队自己的说明'};
  const m = await mount(PlatformPluginsPanel,{ role:'member', handle:url => url.includes('/plugins?') ? Response.json(page([official,custom])) : undefined });
  try {
    const cards = document.querySelectorAll('.catalog-card');
    assert.equal(cards.length,2);
    assert.match(cards[0].textContent,/读取仓库改动/);
    assert.match(cards[0].textContent,/共享连接已撤销/);
    assert.doesNotMatch(cards[0].textContent,/https:|Bearer/);
    const language = document.querySelector('select[aria-label="简介语言"]');
    assert.equal(language.value,'zh');
    language.value='en'; language.dispatchEvent(new window.Event('change',{bubbles:true})); await pause();
    assert.match(cards[0].textContent,/Read repository changes/);
    assert.match(cards[1].textContent,/团队自己的说明/);
    cards[0].querySelector('button').click(); await pause();
    assert.match(document.querySelector('.n-drawer').textContent,/Read repository changes/);
    assert.equal(button('分配与管理'),undefined);
    assert.equal(button('配置共享连接'),undefined);
    assert.equal(m.calls.filter(c=>c.init.method && c.init.method!=='GET').length,0);
  } finally {await m.cleanup();}
});
test('Revoked account does not prevent existing package metadata update',async()=>{
const bound={...packageItem,plugins:[{plugin_id:'plugin',version_id:'pv1',tools:[]}]};
const m=await mount(PlatformPackagesPanel,{props:{employeeId:'employee'},handle:(url,init,body)=>{
if(url.includes('/capability-packages?'))return Response.json(page([bound]));
if(url.includes('/plugins/plugin/versions'))return Response.json(page([plugin]));
if(url.includes('/connection-source'))return Response.json({source:'agent',revision:1,account:{id:'account',plugin_id:'plugin',plugin_version_id:'pv1',name:'Revoked',version:2,enabled:false,validated:false,tools:[]}});
if(init.method==='PATCH')return Response.json({...bound,...body});
}});
try{button('配置能力包').click();await pause();await pause();const name=document.querySelector('.n-drawer input');name.value='Renamed package';name.dispatchEvent(new window.Event('input',{bubbles:true}));await pause();assert.equal(button('保存能力包').disabled,false);button('保存能力包').click();await pause();const patch=m.calls.find(c=>c.init.method==='PATCH');assert.equal(patch.body.name,'Renamed package');assert.equal('plugins' in patch.body,false);}finally{await m.cleanup();}
});

test('Version publish excludes rename and account revoke carries optimistic revision',async()=>{
const calls=[];globalThis.fetch=async(url,init)=>{calls.push({url,body:JSON.parse(init.body),method:init.method});return Response.json({});};
await skillsApi.publish('w','import','Display name','release',skill);await pluginsApi.revokeAccount('w','employee','plugin',4);
assert.equal('name' in calls[0].body,false);assert.equal(calls[0].body.version,1);assert.equal(calls[1].method,'DELETE');assert.deepEqual(calls[1].body,{version:4});
});
async function triggerChoice(index,label){const field=[...document.querySelectorAll('fieldset')].find(e=>e.querySelector('legend')?.textContent==='自动化任务使用范围');const e=field.querySelectorAll('.n-base-selection')[index];assert.ok(e);e.dispatchEvent(new window.MouseEvent('click',{bubbles:true}));await pause();const opt=[...document.querySelectorAll('.n-base-select-option')].reverse().find(e=>e.textContent.trim()===label);assert.ok(opt,label);opt.click();await pause();}
test('Automation allowlist and denylist are exclusive and save only the selected side',async()=>{
 const tr={id:'trigger-1',kind:'schedule',config:{description:'日报'},enabled:true};
 for(const mode of ['all','allowlist']){
  const m=await mount(PlatformPackagesPanel,{props:{employeeId:'employee'},handle:(url,init,body)=>url.endsWith('/triggers')?Response.json({items:[tr]}):init.method==='PATCH'?Response.json({...packageItem,...body}):null});
  try {
   button('配置能力包').click();await pause();await pause();
   if(mode==='allowlist')await triggerChoice(0,'白名单');
   assert.equal(document.querySelectorAll('[data-scene="trigger"] .n-base-selection').length,2);
   await triggerChoice(1,'定时 · 日报');document.body.click();await pause();
   button('保存能力包').click();await pause();
   const c=m.calls.find(c=>c.init.method==='PATCH');
   assert.deepEqual(c.body.trigger_scope,{mode,allow_trigger_ids:mode==='allowlist'?['trigger-1']:[],deny_trigger_ids:mode==='all'?['trigger-1']:[]});
   assert.equal(c.body.scope,undefined);
  }finally{await m.cleanup()}
 }
});

test('Unknown trigger IDs remain visible and template allowlist stays empty',async()=>{const bounded={...packageItem,trigger_scope:{mode:'allowlist',allow_trigger_ids:['unknown-trigger'],deny_trigger_ids:[]}};let m=await mount(PlatformPackagesPanel,{props:{employeeId:'employee'},handle:url=>url.includes('/capability-packages?')?Response.json(page([bounded])):null});try{button('配置能力包').click();await pause();await pause();assert.match(document.body.textContent,/不可见或已失效：unknown-trigger/);}finally{await m.cleanup()}m=await mount(PlatformPackagesPanel,{handle:(url,init,body)=>url.includes('/capability-package-templates')&&!init.method?Response.json(page([{...packageItem,trigger_scope:{mode:'allowlist',allow_trigger_ids:[],deny_trigger_ids:[]}}])):init.method==='PATCH'?Response.json({...packageItem,...body}):null});try{button('配置能力包').click();await pause();await pause();assert.match(document.body.textContent,/复制后仍禁止全部自动化任务/);const el=document.querySelector('.n-drawer input');el.value='Rename';el.dispatchEvent(new window.Event('input',{bubbles:true}));await pause();button('保存能力包').click();await pause();const c=m.calls.find(c=>c.init.method==='PATCH');assert.equal(c.body.trigger_scope,undefined);assert.equal(m.calls.some(c=>c.url.endsWith('/triggers')),false)}finally{await m.cleanup()}});

test('Unified package people persist scoped channel identity and do not change trigger scope',async()=>{
 const ref={kind:'user',provider:'feishu',workspace_id:'w',agent_id:'employee',binding_id:'binding',tenant_id:'tenant',identity_epoch:7,connection_generation:3,identity_namespace:'open_id',external_id:'ou_person'};
 const row={ref,display_name:'同名真人',status:'active',selectable:true};
 const conversation_scope={private:{enabled:true,mode:'allowlist',allow:[],deny:[]},group:{enabled:false,mode:'inherit_with_blacklist',allow:[],deny:[]}};
 const m=await mount(PlatformPackagesPanel,{props:{employeeId:'employee'},handle:(url,init,body)=>url.includes('/capability-packages?')?Response.json(page([{...packageItem,conversation_scope,trigger_scope:{mode:'all',allow_trigger_ids:[],deny_trigger_ids:[]}}])):url.includes('/selectors/people/sources?')?Response.json({items:[{provider:'web',available:true,supports_department_tree:false}]}):url.includes('/selectors/people?')?Response.json({items:[row],next_cursor:null}):url.endsWith('/selectors/people/resolve')?Response.json({items:[row],next_cursor:null}):init.method==='PATCH'?Response.json({...packageItem,...body}):null});
 try{button('配置能力包').click();await pause();await pause();button('选择').click();await pause();assert.match(document.body.textContent,/同名真人/);button('保存能力包').click();await pause();const c=m.calls.find(c=>c.init.method==='PATCH');assert.deepEqual(c.body.conversation_scope.private.allow,[{channel:'feishu',kind:'user',id:'ou_person',binding_id:'binding',identity_epoch:7}]);assert.equal(c.body.scope,undefined);assert.equal(c.body.trigger_scope,undefined);assert.equal(c.body.conversation_scope.private.allow.some(v=>v.kind==='group'),false);}finally{await m.cleanup()}
});
test('Unknown selected people survive lookup failure and can be explicitly removed',async()=>{
 const saved={...packageItem,conversation_scope:{private:{enabled:true,mode:'allowlist',allow:[{channel:'wecom',kind:'user',id:'callback-old',binding_id:'old-binding',identity_epoch:2}],deny:[]},group:{enabled:false,mode:'inherit_with_blacklist',allow:[],deny:[]}}};
 const m=await mount(PlatformPackagesPanel,{props:{employeeId:'employee'},handle:(url,init,body)=>url.includes('/capability-packages?')?Response.json(page([saved])):url.endsWith('/selectors/people/resolve')?Response.json({error:{code:'unavailable',message:'来源已变更'}},{status:409}):init.method==='PATCH'?Response.json({...saved,...body}):null});
 try{button('配置能力包').click();await pause();await pause();assert.match(document.body.textContent,/callback-old/);assert.match(document.body.textContent,/原有选择仍保留/);assert.equal(m.calls.some(c=>c.init.method==='PATCH'),false);button('移除主体').click();await pause();button('保存能力包').click();await pause();assert.deepEqual(m.calls.find(c=>c.init.method==='PATCH').body.conversation_scope.private.allow,[]);}finally{await m.cleanup()}
});

const conversationScopes=()=>({private:{enabled:false,mode:'inherit_with_blacklist',allow:[],deny:[]},group:{enabled:false,mode:'inherit_with_blacklist',allow:[],deny:[]}});
function sceneSwitch(label){const el=[...document.querySelectorAll('.scene-switches .n-checkbox')].find(e=>e.textContent.trim()===label);assert.ok(el,label);el.click();}
function renamePackage(name){const el=document.querySelector('.n-drawer input');el.value=name;el.dispatchEvent(new window.Event('input',{bubbles:true}));}
test('A new package inherits the employee scope and enables all triggers by default', async () => {
  const m = await mount(PlatformPackagesPanel, {props: {employeeId: 'employee'}, handle: (_url, init, body) => init.method === 'POST' ? Response.json({...packageItem, ...body}) : undefined});
  try {
    button('新建能力包').click();
    await pause();
    renamePackage('First package');
    await pause();
    button('保存能力包').click();
    await pause();
    const request = m.calls.find(c => c.init.method === 'POST' && c.url.endsWith('/capability-packages'));
    const expected = {enabled: true, mode: 'inherit_with_blacklist', allow: [], deny: []};
    assert.deepEqual(request.body.conversation_scope, {private: expected, group: expected});
    assert.deepEqual(request.body.trigger_scope, {enabled: true, mode: 'all', allow_trigger_ids: [], deny_trigger_ids: []});
  } finally {
    await m.cleanup();
  }
});
test('Employee package editor shows scene rules above its own handoff scope', async () => {
  const m = await mount(PlatformPackagesPanel, {props: {employeeId: 'employee'}});
  try {
    assert.doesNotMatch(document.body.textContent, /允许向此员工交接的来源/);
    button('新建能力包').click();
    await pause();
    const section = [...document.querySelectorAll('.package-sections > section')].find(
      el => el.querySelector('h4')?.textContent === '3. 使用范围授权',
    );
    assert.ok(section);
    const text = section.textContent;
    const scenes = text.indexOf('使用场景');
    const handoff = text.indexOf('事项交接时允许使用此能力包的来源');
    assert.ok(scenes >= 0 && handoff > scenes);
    assert.equal(m.calls.some(c => c.url.endsWith('/handoff-scope')), false);
  } finally {
    await m.cleanup();
  }
});
test('Package scenes switch independently and disabled scene rules remain intact',async()=>{
 const conversation_scope=conversationScopes();conversation_scope.private.deny=[{channel:'web',kind:'user',id:'hidden-user'}];
 const saved={...packageItem,conversation_scope,trigger_scope:{enabled:false,mode:'all',allow_trigger_ids:[],deny_trigger_ids:['old-trigger']}};
 const m=await mount(PlatformPackagesPanel,{props:{employeeId:'employee'},handle:(url,init,body)=>url.includes('/capability-packages?')?Response.json(page([saved])):init.method==='PATCH'?Response.json({...saved,...body}):undefined});
 try{button('配置能力包').click();await pause();await pause();assert.equal(document.querySelectorAll('[data-scene]').length,0);sceneSwitch('私聊');await pause();assert.ok(document.querySelector('[data-scene="private"]'));assert.equal(document.querySelector('[data-scene="group"]'),null);sceneSwitch('私聊');sceneSwitch('触发器');await pause();assert.ok(document.querySelector('[data-scene="trigger"]'));assert.equal(document.querySelector('[data-scene="private"]'),null);button('保存能力包').click();await pause();const savedRequest=m.calls.find(c=>c.init.method==='PATCH');assert.equal(savedRequest.body.scope,undefined);assert.equal(savedRequest.body.conversation_scope,undefined);assert.equal(savedRequest.body.trigger_scope.enabled,true);assert.deepEqual(savedRequest.body.trigger_scope.deny_trigger_ids,['old-trigger']);assert.equal(m.calls.some(c=>c.url.includes('/members?')),false);}finally{await m.cleanup()}
});
test('Legacy metadata edits preserve raw member fields and conversion requires an explicit review action',async()=>{
 const saved={...packageItem,scope:{mode:'whitelist_with_blacklist',allow_member_ids:['web-legacy'],deny_member_ids:['deny-legacy'],allow:[],deny:[]}};
 const m=await mount(PlatformPackagesPanel,{props:{employeeId:'employee'},handle:(url,init,body)=>url.includes('/capability-packages?')?Response.json(page([saved])):init.method==='PATCH'?Response.json({...saved,...body}):undefined});
 try{button('配置能力包').click();await pause();await pause();assert.match(document.body.textContent,/不能自动转换/);renamePackage('metadata only');await pause();button('保存能力包').click();await pause();const first=m.calls.find(c=>c.init.method==='PATCH');assert.equal(first.body.scope,undefined);assert.equal(first.body.conversation_scope,undefined);button('配置能力包').click();await pause();await pause();button('确认重新配置私聊和群聊（初始关闭）').click();await pause();sceneSwitch('群聊');await pause();button('保存能力包').click();await pause();const second=m.calls.filter(c=>c.init.method==='PATCH').at(-1);assert.equal(second.body.scope,undefined);assert.equal(second.body.conversation_scope.private.enabled,false);assert.equal(second.body.conversation_scope.group.enabled,true);assert.deepEqual(second.body.conversation_scope.group.allow,[]);}finally{await m.cleanup()}
});
test('Workspace template scene retains Web member selection without channel selectors',async()=>{
 const conversation_scope=conversationScopes();conversation_scope.private={enabled:true,mode:'allowlist',allow:[],deny:[]};
 const saved={...packageItem,conversation_scope,trigger_scope:{enabled:false,mode:'all',allow_trigger_ids:[],deny_trigger_ids:[]}};
 const m=await mount(PlatformPackagesPanel,{handle:(url,init,body)=>url.includes('/capability-package-templates')&&!init.method?Response.json(page([saved])):init.method==='PATCH'?Response.json({...saved,...body}):undefined});
 try{button('配置能力包').click();await pause();await pause();const field=document.querySelector('[data-scene="private"]');assert.ok(field);const picker=field.querySelectorAll('.n-base-selection')[1];picker.click();await pause();const option=[...document.querySelectorAll('.n-base-select-option')].find(e=>e.textContent.includes('you@example.test'));assert.ok(option);option.click();document.body.click();await pause();button('保存能力包').click();await pause();const request=m.calls.find(c=>c.init.method==='PATCH');assert.deepEqual(request.body.conversation_scope.private.allow,[{channel:'web',kind:'user',id:'member'}]);assert.equal(request.body.scope,undefined);assert.equal(m.calls.some(c=>c.url.includes('/selectors/')),false);}finally{await m.cleanup()}
});

test('Group scene can select a scoped group while private scene never queries groups',async()=>{
 const ref={kind:'group',provider:'web',workspace_id:'w',agent_id:'employee',external_id:'group-1',candidate_id:'candidate-1'};
 const row={ref,display_name:'项目讨论群',status:'active',selectable:true};
 const conversation_scope=conversationScopes();conversation_scope.group={enabled:true,mode:'allowlist',allow:[],deny:[]};
 const saved={...packageItem,conversation_scope,trigger_scope:{enabled:false,mode:'all',allow_trigger_ids:[],deny_trigger_ids:[]}};
 const m=await mount(PlatformPackagesPanel,{props:{employeeId:'employee'},handle:(url,init,body)=>url.includes('/capability-packages?')?Response.json(page([saved])):url.includes('/selectors/groups?')?Response.json({items:[row],next_cursor:null}):url.endsWith('/selectors/groups/resolve')?Response.json({items:[row]}):init.method==='PATCH'?Response.json({...saved,...body}):undefined});
 try{button('配置能力包').click();await pause();await pause();assert.match(document.body.textContent,/必须同时满足人员范围与群范围/);button('选择').click();await pause();button('保存能力包').click();await pause();const request=m.calls.find(c=>c.init.method==='PATCH');assert.deepEqual(request.body.conversation_scope.group.allow,[{channel:'web',kind:'group',id:'group-1'}]);assert.equal(request.body.conversation_scope.private.enabled,false);assert.equal(request.body.scope,undefined);}finally{await m.cleanup()}
});
test('A populated scene list cannot be silently discarded by switching its mode',async()=>{
 const conversation_scope=conversationScopes();conversation_scope.private={enabled:true,mode:'inherit_with_blacklist',allow:[],deny:[{channel:'web',kind:'user',id:'restricted'}]};
 const saved={...packageItem,conversation_scope};
 const m=await mount(PlatformPackagesPanel,{props:{employeeId:'employee'},handle:url=>url.includes('/capability-packages?')?Response.json(page([saved])):undefined});
 try{button('配置能力包').click();await pause();await pause();document.querySelector('[data-scene="private"] .n-base-selection').click();await pause();const option=[...document.querySelectorAll('.n-base-select-option')].find(e=>e.textContent.trim()==='白名单');assert.ok(option);assert.ok(option.className.includes('disabled'));option.click();await pause();assert.match(document.querySelector('[data-scene="private"]').textContent,/拒绝名单/);assert.equal(m.calls.some(c=>c.init.method==='PATCH'),false);}finally{await m.cleanup()}
});


test('New package transport submits only scene scope, never the legacy authorization model',async()=>{
 const calls=[];globalThis.fetch=async(url,init)=>{calls.push({url,body:JSON.parse(init.body)});return Response.json({id:'created'})};
 await packagesApi.save('w','employee',{...packageItem,conversation_scope:conversationScopes(),trigger_scope:{enabled:false,mode:'all',allow_trigger_ids:[],deny_trigger_ids:[]}});
 assert.equal(calls.length,1);assert.equal(calls[0].body.scope,undefined);assert.equal(calls[0].body.conversation_scope.private.enabled,false);assert.equal(calls[0].body.trigger_scope.enabled,false);
});

test('Renaming a Skill patches only its metadata; reupload is locked to the first published source', async () => {
  const latest = {...version, id:'sv2', sequence:2, source:{type:'markdown'}};
  const saved = {...skill, current_version_id:'sv2', current_version:latest, version:3};
  const m = await mount(PlatformSkillsPanel, {handle(url, init) {
    if (url.includes('/skills?')) return Response.json(page([saved]));
    if (url.includes('/skills/skill/versions?')) return Response.json(page([latest, version]));
    if (init.method === 'PATCH') return Response.json({...saved, name:'新展示名'});
  }});
  try {
    button('查看详情').click(); await pause();
    button('改名').click(); await pause();
    input('Skill 名称','新展示名'); await pause();
    button('保存名称').click(); await pause();
    const writes = m.calls.filter(c => c.init.method && c.init.method !== 'GET');
    assert.equal(writes.length, 1);
    assert.ok(writes[0].url.endsWith('/skills/skill'));
    assert.deepEqual(writes[0].body, {version:3, name:'新展示名'});
    button('查看详情').click(); await pause();
    button('上传新版本').click(); await pause();
    assert.match(document.body.textContent, /原始导入方式 · ZIP/);
    assert.equal(document.querySelector('.n-radio'), null);
    assert.equal(document.querySelector('input[type=file]').accept, '.zip');
    assert.equal(button('发布新版本').disabled, true);
  } finally { await m.cleanup(); }
});

test('Directory keeps two Feishu bindings separate, pins pagination and scopes sync to the chosen app', async () => {
  const snapshot = {status:'ready', snapshot_version:8, complete:true, user_count:54, department_count:1};
  const m = await mount(ResourceDirectory, {handle(url) {
    if (url.includes('/agents?')) return Response.json(page(['A','B'].map(id => ({
      id, name:'员工 '+id, card_resources:{channels:['feishu']}
    }))));
    const id = url.includes('/agents/A/') ? 'A' : 'B';
    if (url.endsWith('/channels/feishu')) return Response.json({id, app_id:'app-'+id, enabled:true, directory_status:snapshot});
    if (url.endsWith('/directory/sync')) return Response.json({...snapshot,status:'syncing'});
    if (url.includes('/directory/users?')) {
      const q = new URL(url,'http://test').searchParams;
      return Response.json({items:[{id,display_name:'成员 '+id,department_ids:[]}],snapshot_version:8,next_cursor:q.has('cursor') ? null : 'second'});
    }
    if (url.includes('/directory/departments?')) return Response.json({items:[{id:'dept',display_name:'工程部',parent_id:''}],snapshot_version:8});
  }});
  try {
    const cards = [...document.querySelectorAll('.sync-source-card')];
    assert.equal(cards.length, 3);
    assert.equal(m.calls.some(c=>c.url.includes('/directory/users?')), false);
    assert.doesNotMatch(document.body.textContent, /邮箱邀请/);
    const first = cards.find(c=>c.textContent.includes('app-A'));
    const second = cards.find(c=>c.textContent.includes('app-B'));
    first.querySelector('.source-choice').click(); await pause();
    assert.match(document.body.textContent, /成员 A/);
    button('下一页').click(); await pause();
    assert.ok(m.calls.some(c=>c.url.includes('/A/channels/feishu/directory/users?') && c.url.includes('snapshot_version=8') && c.url.includes('cursor=second')));
    button('部门').click(); await pause();
    button('部门成员').click(); await pause();
    assert.ok(m.calls.some(c=>c.url.includes('/A/channels/feishu/directory/users?') && c.url.includes('department_id=dept')));
    second.querySelector('.source-choice').click(); await pause();
    assert.match(document.body.textContent, /成员 B/);
    assert.doesNotMatch(document.body.textContent, /成员 A/);
    assert.equal(m.calls.some(c=>c.init.method==='POST'),false);
    second.querySelector('.ssc-footer button').click(); await pause();
    assert.deepEqual(m.calls.filter(c=>c.init.method==='POST').map(c=>c.url.split('/agents/')[1]), ['B/channels/feishu/directory/sync']);
  } finally { await m.cleanup(); }
});

test('A workspace member can browse Web members without accessing employee binding management', async () => {
  const m = await mount(ResourceDirectory, {role:'member'});
  try {
    assert.equal(document.querySelectorAll('.sync-source-card').length, 1);
    assert.match(document.body.textContent, /you@example.test/);
    assert.equal(m.calls.some(c=>c.url.includes('/agents')), false);
    assert.equal(button('立即同步该来源'), undefined);
  } finally { await m.cleanup(); }
});

test('Off-page resource links fetch the requested Skill instructions and MCP definition history by ID', async () => {
  const linked = {...skill, id:'off-page', name:'Linked Skill'};
  let m = await mount(PlatformSkillsPanel, {props:{targetId:'off-page'}, handle(url) {
    if (url.endsWith('/skills/off-page')) return Response.json(linked);
    if (url.includes('/off-page/versions/sv1/files?path=SKILL.md')) {
      return Response.json({size:5,content_base64:Buffer.from('Hello').toString('base64')});
    }
  }});
  try {
    assert.match(document.querySelector('.n-drawer').textContent, /Linked Skill/);
    assert.equal(document.querySelector('.sk-prompt-body').textContent,'Hello');
  } finally { await m.cleanup(); }
  m = await mount(PlatformPluginsPanel, {props:{targetId:'off-page'}, handle(url) {
    if (url.includes('/plugins/off-page/versions?')) return Response.json(page([{...plugin, name:'Linked MCP'}]));
  }});
  try {
    assert.match(document.querySelector('.n-drawer').textContent, /Linked MCP/);
  } finally { await m.cleanup(); }
});

test('WeCom directory verifies a searched contact, reopens its pending code and can cancel and reissue', async () => {
  const pending = {id:'verification',search_user_id:'person',status:'pending',code:'TEST-123',expires_at:new Date(Date.now()+60_000).toISOString()};
  const m = await mount(ResourceDirectory, {handle(url) {
    if (url.includes('/agents?')) return Response.json(page([{id:'a', name:'企微员工',card_resources:{channels:['wecom']}}]));
    if (url.endsWith('/channels/wecom')) return Response.json({id:'bot',app_id:'app-id',enabled:true,identity_confirmed:true,connection_generation:2,identity_verified_generation:2});
    if (url.includes('/contacts/search?')) return Response.json({items:[{search_user_id:'person',display_name:'赵同学',verification_status:'unknown'}],limited:false});
    if (url.endsWith('/cancel')) return Response.json({...pending,status:'cancelled',code:undefined});
    if (url.includes('/contacts/verifications')) return Response.json(pending);
  }});
  try {
    [...document.querySelectorAll('.source-choice')].find(c=>c.textContent.includes('企微员工')).click();
    await pause();
    input('搜索企业微信联系人姓名…','赵'); await pause();
    button('搜索联系人').click(); await pause();
    button('核验绑定身份').click(); await pause();
    assert.equal(document.querySelector('.v-code').textContent,'TEST-123');
    button('关闭').click(); await pause();
    button('核验绑定身份').click(); await pause();
    assert.ok(m.calls.some(c=>c.url.endsWith('/contacts/verifications/verification') && !c.init.method));
    button('取消验证码').click(); await pause();
    assert.match(document.querySelector('.wecom-verify-flow').textContent,/已取消/);
    assert.equal(document.querySelector('.v-code'),null);
    button('关闭').click(); await pause();
    button('核验绑定身份').click(); await pause();
    const issued=m.calls.filter(c=>c.url.endsWith('/contacts/verifications'));
    assert.equal(issued.length,2);
    assert.notEqual(issued[0].body.request_id,issued[1].body.request_id);
  } finally { await m.cleanup(); }
});

async function waitDirectoryCondition(check) {
  for (let i=0;i<125;i++) {
    if(check())return;
    await new Promise(resolve=>setTimeout(resolve,50));
  }
  assert.ok(check(),'directory state did not arrive before timeout');
}

test('Directory polling retains a usable snapshot on transient failure, then hides invalidated data', async () => {
  const snapshot={status:'syncing',snapshot_version:8,complete:true,user_count:1,department_count:1,expires_at:new Date(Date.now()+60_000).toISOString()};
  let polls=0;
  const m=await mount(ResourceDirectory,{handle(url){
    if(url.includes('/agents?'))return Response.json(page([{id:'a',name:'飞书员工',card_resources:{channels:['feishu']}}]));
    if(url.endsWith('/channels/feishu'))return Response.json({id:'bot',app_id:'app',enabled:true,directory_status:snapshot});
    if(url.endsWith('/directory')){
      if(++polls===1)return new Response('',{status:503});
      return Response.json({...snapshot,status:'invalidated',complete:false});
    }
    if(url.includes('/directory/users?'))return Response.json({items:[{id:'p',display_name:'快照成员',department_ids:[]}],snapshot_version:8});
  }});
  try{
    [...document.querySelectorAll('.source-choice')].find(c=>c.textContent.includes('飞书员工')).click();await pause();
    assert.match(document.body.textContent,/快照成员/);
    await waitDirectoryCondition(()=>document.body.textContent.includes('暂时无法刷新同步状态'));
    assert.match(document.body.textContent,/快照成员/);
    await waitDirectoryCondition(()=>document.body.textContent.includes('快照已失效'));
    assert.doesNotMatch(document.body.textContent,/快照成员|暂时无法刷新同步状态/);
    assert.equal(polls,2);
  }finally{await m.cleanup();}
});

test('WeCom transient verification poll keeps code and contact, can cancel the same ID', async () => {
  const pending={id:'same-verification',search_user_id:'person',status:'pending',code:'LOCAL-TEST',expires_at:new Date(Date.now()+60_000).toISOString()};
  const m=await mount(ResourceDirectory,{handle(url,init){
    if(url.includes('/agents?'))return Response.json(page([{id:'a',name:'企微员工',card_resources:{channels:['wecom']}}]));
    if(url.endsWith('/channels/wecom'))return Response.json({id:'bot',app_id:'app',enabled:true,identity_confirmed:true,connection_generation:2,identity_verified_generation:2});
    if(url.includes('/contacts/search?'))return Response.json({items:[{search_user_id:'person',display_name:'核验联系人',verification_status:'unknown'}],limited:false});
    if(url.endsWith('/same-verification/cancel'))return Response.json({...pending,status:'cancelled',code:undefined});
    if(url.endsWith('/same-verification'))return new Response('',{status:503});
    if(url.endsWith('/contacts/verifications')&&init.method==='POST')return Response.json(pending);
  }});
  try{
    [...document.querySelectorAll('.source-choice')].find(c=>c.textContent.includes('企微员工')).click();await pause();
    input('搜索企业微信联系人姓名…','核验');await pause();button('搜索联系人').click();await pause();
    button('核验绑定身份').click();await pause();
    await waitDirectoryCondition(()=>document.body.textContent.includes('暂时无法查询验证状态'));
    assert.match(document.body.textContent,/核验联系人/);
    assert.equal(document.querySelector('.v-code').textContent,'LOCAL-TEST');
    button('取消验证码').click();await pause();
    assert.ok(m.calls.some(c=>c.url.endsWith('/same-verification/cancel')));
    assert.equal(document.querySelector('.v-code'),null);
    assert.match(document.body.textContent,/已取消/);
  }finally{await m.cleanup();}
});

test('WeCom verification denial removes prior rows and displayed code', async () => {
  const pending={id:'denied-verification',search_user_id:'person',status:'pending',code:'LOCAL-DENIED',expires_at:new Date(Date.now()+60_000).toISOString()};
  const m=await mount(ResourceDirectory,{handle(url,init){
    if(url.includes('/agents?'))return Response.json(page([{id:'a',name:'企微员工',card_resources:{channels:['wecom']}}]));
    if(url.endsWith('/channels/wecom'))return Response.json({id:'bot',app_id:'app',enabled:true,identity_confirmed:true,connection_generation:2,identity_verified_generation:2});
    if(url.includes('/contacts/search?'))return Response.json({items:[{search_user_id:'person',display_name:'私有联系人',verification_status:'unknown'}],limited:false});
    if(url.endsWith('/denied-verification'))return new Response('',{status:403});
    if(url.endsWith('/contacts/verifications')&&init.method==='POST')return Response.json(pending);
  }});
  try{
    [...document.querySelectorAll('.source-choice')].find(c=>c.textContent.includes('企微员工')).click();await pause();
    input('搜索企业微信联系人姓名…','私有');await pause();button('搜索联系人').click();await pause();
    button('核验绑定身份').click();await pause();
    assert.equal(document.querySelector('.v-code').textContent,'LOCAL-DENIED');
    button('刷新核验状态').click();await pause();
    assert.doesNotMatch(document.body.textContent,/私有联系人|LOCAL-DENIED/);
    assert.ok(m.calls.some(c=>c.url.endsWith('/denied-verification')));
  }finally{await m.cleanup();}
});

async function selectAssignment(label, value) {
  const field = [...document.querySelectorAll('.n-drawer label')].find(el=>el.textContent.trim().startsWith(label));
  field.querySelector('.n-base-selection').click();
  await pause();
  const option=[...document.querySelectorAll('.n-base-select-option')].find(el=>el.textContent.trim()===value);
  assert.ok(option, value); option.click(); await pause();
}

test('Assigning an official Skill pins the selected employee package without copying the Skill', async () => {
  const saved = {...skill, scope:'official', listed:true, assigned_agent_count:0};
  const m = await mount(PlatformSkillsPanel, {handle(url) {
    if (url.includes('/skills?')) return Response.json(page([saved]));
    if (url.includes('/skills/skill/agents?')) return Response.json(page([]));
    if (url.includes('/workspaces/w/agents?')) return Response.json(page([{id:'employee',name:'Test employee'}]));
    if (url.endsWith('/skills/skill/assign')) return Response.json({items:[{agent_id:'employee',package_id:'package',status:'assigned'}]});
  }});
  try {
    assert.ok(m.calls.some(c=>c.url.includes('scope=all')));
    assert.equal(button('改名'), undefined);
    button('查看详情').click(); await pause();
    button('分配与管理').click();
    await pause();
    await selectAssignment('员工','Test employee');
    await selectAssignment('能力包','Work package');
    button('确认分配').click();
    await pause();
    const writes=m.calls.filter(c=>c.init.method==='POST');
    assert.equal(writes.length,1);
    assert.ok(writes[0].url.endsWith('/workspaces/w/skills/skill/assign'));
    assert.deepEqual(writes[0].body,{version_id:'sv1',targets:[{agent_id:'employee',package_id:'package',package_version:1}]});
    assert.equal(document.querySelector('fieldset [role=status]').textContent,'已分配');
  } finally {await m.cleanup();}
});

test('Official plugin assignment can be saved before discovery and never selects individual tools', async () => {
  const definition={...plugin,scope:'official',auth_type:'none',listed:true};
  const tools=['read','write'].map(name=>({name,description:name,input_schema:{},schema_digest:name+'-digest'}));
  const m=await mount(PlatformPluginsPanel,{handle(url,init) {
    if(url.includes('/plugins?')) return Response.json(page([definition]));
    if(url.includes('/plugins/plugin/versions?')) return Response.json(page([definition]));
    if(url.includes('/plugins/plugin/agents?')) return Response.json(page([]));
    if(url.includes('/workspaces/w/agents?')) return Response.json(page([{id:'agent',name:'Reviewer'}]));
    if(url.includes('/connection-source?')) return Response.json({source:'workspace',revision:0});
    if(url.endsWith('/plugin-accounts/plugin')) {
      if(init.method==='PUT') return Response.json({id:'account',plugin_id:'plugin',plugin_version_id:'pv1',name:'Reviewer account',version:1,enabled:true,validated:true,tools});
      return Response.json({error:{code:'not_found',message:'Missing account'}},{status:404});
    }
    if(url.endsWith('/plugins/plugin/assign')) return Response.json({items:[{agent_id:'agent',package_id:'package',status:'assigned'}]});
  }});
  try {
    button('查看详情').click(); await pause();
    button('分配与管理').click(); await pause();
    await selectAssignment('员工','Reviewer');
    await selectAssignment('能力包','Work package');
    await pause();
    assert.ok(document.body.textContent.includes('使用时获取工具列表'));
    assert.equal(document.querySelector('.tool .n-checkbox'), null);
    button('确认分配').click(); await pause();
    assert.equal(m.calls.some(c=>c.init.method==='PUT'),false);
    const assignment=m.calls.find(c=>c.url.endsWith('/plugins/plugin/assign'));
    assert.deepEqual(assignment.body.targets,[{agent_id:'agent',package_id:'package',package_version:1}]);
    assert.equal(document.querySelector('fieldset [role=status]').textContent,'已分配');
  } finally {await m.cleanup();}
});

test('employee package shows the selected definition number and account tools as read-only', async () => {
 const saved={...packageItem,plugins:[{plugin_id:'plugin',version_id:'pv1',tools:[]}]};
 const m=await mount(PlatformPackagesPanel,{props:{employeeId:'employee'},handle(url){
  if(url.includes('/capability-packages?')) return Response.json(page([saved]));
  if(url.includes('/plugins?')) return Response.json(page([{...plugin,version:9,definition_version:3}]));
  if(url.includes('/connection-source?')) return Response.json({source:'workspace',revision:1,account:{id:'conn',plugin_version_id:'pv1',enabled:true,validated:true,tools:[{name:'lookup',description:'读取条目'}]}});
 }});
 try{
  button('配置能力包').click(); await pause(); await pause();
  const list=document.querySelector('[aria-label="可用工具"]');
  assert.ok(list?.textContent.includes('lookup'));
  assert.equal(list.querySelectorAll('[role="checkbox"],input[type="checkbox"]').length,0);
  assert.match(document.body.textContent,/当前使用版本 3/);
  assert.ok(!m.calls.some(c=>c.url.includes('/versions?')));
 }finally{await m.cleanup();}
});

test('unlisted employee plugin retains its name and connection and upgrades explicitly',async()=>{
 let saved={...packageItem,plugins:[{plugin_id:'plugin',version_id:'pv1',tools:[]}]};
 const latest={...plugin,listed:false,version_id:'pv2',definition_version:2,auth_type:'none'};
 const m=await mount(PlatformPackagesPanel,{props:{employeeId:'employee'},handle(url,init,body){
  if(url.includes('/capability-packages?'))return Response.json(page([saved]));
  if(url.endsWith('/capability-packages/package'))return Response.json(saved);
  if(url.includes('/plugins?'))return Response.json(page([]));
  if(url.endsWith('/plugins/plugin'))return Response.json(latest);
  if(url.includes('/plugins/plugin/versions?'))return Response.json(page([latest,plugin]));
  if(url.includes('/connection-source?'))return Response.json({source:'workspace',revision:0});
  if(url.endsWith('/capability-upgrades') && init.method==='POST'){
    assert.deepEqual(body,{kind:'plugin',resource_id:'plugin',from_version_id:'pv1',to_version_id:'pv2'});
    saved={...saved,version:2,plugins:[{plugin_id:'plugin',version_id:'pv2',tools:[]}]};
    return Response.json({upgraded:true});
  }
 }});
 try{
  button('配置能力包').click();await pause();await pause();
  assert.match(document.body.textContent,/MCP fixture/);assert.match(document.body.textContent,/已下架/);
  assert.ok(m.calls.some(c=>c.url.includes('connection-source?plugin_version_id=pv1')));
  button('升级至版本 2').click();await pause();
  assert.ok(m.calls.some(c=>c.url.includes('connection-source?plugin_version_id=pv2')));
  button('确认升级').click();await pause();await pause();
  assert.equal(m.calls.filter(c=>c.url.endsWith('/capability-upgrades')).length,1);
  assert.match(document.body.textContent,/当前使用版本 2/);
 }finally{await m.cleanup()}
});

test('Drawer backdrop closes a clean editor, but preserves dirty data until discard or save', async () => {
 const m = await mount(PlatformPluginsPanel, {
  handle: (url, init, body) => init.method === 'POST' && url.endsWith('/plugins') ? Response.json({...plugin, ...body}) : undefined,
 });
 const waitFor = async (check, message) => {
  const deadline = Date.now() + 3000;
  while (!check() && Date.now() < deadline) await pause(20);
  assert.ok(check(), message);
 };
 const open = async () => {
  button('+ 新增插件').click();
  await waitFor(() => document.querySelector('.n-drawer-mask') && button('关闭'), 'drawer should be open');
 };
 const closed = () => waitFor(() => !button('关闭'), 'drawer should be closed');
 try {
  const backdrop = () => {
   const mask = document.querySelector('.n-drawer-mask');
   assert.ok(mask);
   mask.click();
  };
  await open();
  backdrop();
  await closed();
  await open();
  const fields = [...document.querySelectorAll('.n-drawer input')];
  const name = fields[0];
  const endpoint = fields.find(field => field.placeholder.startsWith('https://'));
  assert.ok(name && endpoint);
  for (const [field, value] of [[name, 'Backdrop draft'], [endpoint, 'https://example.test/mcp']]) {
   field.value = value;
   field.dispatchEvent(new window.Event('input', {bubbles: true}));
  }
  await pause();
  backdrop();
  await pause();
  assert.ok(button('继续编辑'));
  assert.ok(button('放弃修改'));
  assert.ok(button('保存并关闭'));
  button('继续编辑').click();
  await waitFor(() => !button('继续编辑'), 'cancel dialog should close');
  assert.equal(name.value, 'Backdrop draft');
  assert.equal(m.calls.some(c => c.init.method === 'POST'), false);
  backdrop();
  await pause();
  button('保存并关闭').click();
  await closed();
  assert.equal(m.calls.find(c => c.init.method === 'POST')?.body.name, 'Backdrop draft');
  await open();
  const next = document.querySelector('.n-drawer input');
  next.value = 'Discarded draft';
  next.dispatchEvent(new window.Event('input', {bubbles: true}));
  backdrop();
  await pause();
  button('放弃修改').click();
  await closed();
  assert.equal(m.calls.filter(c => c.init.method === 'POST').length, 1);
 } finally {
  await m.cleanup();
 }
});

test('Only the demo package shows the bold simulated-data notice', async () => {
 for (const kind of ['demo','platform']) {
  const item={...packageItem,assistant_tool_set:kind,assistant_tools:[{name:'fixture_tool',description:'Fixture'}]};
  const m=await mount(PlatformPackagesPanel,{props:{employeeId:'employee'},handle:url=>
   url.includes('/capability-packages?') ? Response.json(page([item])) : null});
  try {
   button('配置能力包').click();await pause();await pause();
   const alert=document.querySelector('.package-sections section:nth-child(2) .n-alert');
   assert.ok(alert);
   assert.match(alert.textContent,/fixture_tool/);
   if(kind==='demo') {
    assert.equal(alert.querySelector('strong')?.textContent,'仅供体验，使用模拟数据，不会读写真实业务数据。');
    assert.doesNotMatch(alert.textContent,/只能修改启停/);
   } else {
    assert.match(alert.textContent,/只能修改启停和使用范围/);
    assert.doesNotMatch(alert.textContent,/模拟数据/);
   }
  } finally {await m.cleanup()}
 }
});

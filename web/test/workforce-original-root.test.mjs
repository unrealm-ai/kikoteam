// Root integration regression: original directory, detail and channels render normally.
// Legacy package and trigger modules are stubbed. Approved real package/trigger panels render normally.
// No claim of browser or server end-to-end coverage.
import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
import {Window} from 'happy-dom';
import {parse,compileScript} from '@vue/compiler-sfc';
import esbuild from 'esbuild';
const window=new Window({url:'http://127.0.0.1:5173/'});
for(const name of ['Event','EventTarget','PointerEvent','window','document','navigator','HTMLElement','Element','SVGElement','Node','Text','Comment','Image','InputEvent','MouseEvent','KeyboardEvent','HTMLInputElement','Document','MutationObserver','ResizeObserver','getComputedStyle','requestAnimationFrame','cancelAnimationFrame']){
 if(name==='window')globalThis.window=window;else if(window[name]!==undefined)Object.defineProperty(globalThis,name,{configurable:true,writable:true,value:typeof window[name]==='function'&&['getComputedStyle','requestAnimationFrame','cancelAnimationFrame'].includes(name)?window[name].bind(window):window[name]});
}
// Keep the real QR component; HappyDOM has no raster canvas implementation.
window.HTMLCanvasElement.prototype.getContext=()=>new Proxy({},{get:()=>()=>{}});
globalThis.fetch=async()=>{throw new Error('Unexpected HTTP: use injected Workforce ports')};
const built=await esbuild.build({stdin:{contents:"export {default as WorkforceView} from './src/direction2/views/WorkforceView.vue'; export {workforcePortsKey} from './src/direction2/api/workforce-ports'; export {createWorkforcePlatformPorts} from './src/direction2/api/workforce-platform-ports'; export {activeWorkspaceId} from './src/direction2/api/client'; export {createApp,h,nextTick,reactive} from 'vue'; export {NMessageProvider,NDialogProvider} from 'naive-ui';",resolveDir:process.cwd(),loader:'ts'},bundle:true,write:false,format:'esm',platform:'node',define:{'import.meta.env.VITE_PLATFORM_REAL':'true','import.meta.env.DEV':'false'},plugins:[{name:'vue-test',setup(build){build.onLoad({filter:/\.vue$/},async({path})=>{if(/Workforce(Packages|Triggers)Tab\.vue$/.test(path))return {contents:`import {defineComponent,h} from 'vue';export default defineComponent({setup(_p,{expose}){expose({stopWecomVerifyPoll(){}});return ()=>h('div',{'data-test':'unrelated-module-stub'})}})`,loader:'ts'};const {descriptor}=parse(await fs.readFile(path,'utf8'));return{contents:compileScript(descriptor,{id:path,inlineTemplate:true}).content,loader:'ts'};});}}]});
const {WorkforceView,workforcePortsKey,createWorkforcePlatformPorts,activeWorkspaceId,createApp,h,nextTick,reactive,NMessageProvider,NDialogProvider}=await import(`data:text/javascript;base64,${Buffer.from(built.outputFiles[0].text).toString('base64')}`);
const pause=async()=>{await new Promise(r=>setTimeout(r,40));await nextTick()};
const button=text=>[...document.querySelectorAll('button')].find(b=>b.textContent.trim()===text);
const tab=text=>[...document.querySelectorAll('.n-tabs-tab')].find(n=>n.textContent.trim()===text);
const employee=(extra={})=>({id:'employee-1',code:'employee-1',version:7,name:'测试 AI 员工',role:'研发助手',avatar:'cn-v2-02',status:'active',executionEngine:'pi',modelId:'workspace:model-1',modelName:'测试模型',thinkingLevel:null,prompt:'已有岗位规范',resourceVersions:{usageAccess:2,usageHistory:2,triggerRules:3,channels:{feishu:4}},unifiedAccess:{mode:'all_with_blacklist',whitelistMemberIds:[],blacklistMemberIds:[],whitelistTargets:[],blacklistTargets:[],allowGroupMembers:true},webChatAccess:{mode:'all',allowMemberIds:[],denyMemberIds:[]},handoffPolicy:{mode:'whitelist',allowEmployeeIds:[],denyEmployeeIds:[]},channels:[],capabilityPackages:[],triggers:[],humanConfirmationRules:[],stats:{todayTasks:null,todayConversations:null,successRate:null},...extra});
const qr={sessionId:'feishu:r',workspaceId:'workspace-1',employeeId:'employee-1',platform:'feishu',verificationUrl:'https://accounts.feishu.cn/confirm?fixture=root',qrPayload:'',qrSvgDataUrl:'',expiresInSeconds:600,status:'waiting_scan',qrStatus:'waiting_scan',credentialSaved:false,permissionReady:false,permissionStatus:'unknown',connectionState:'pending'};
function components(v,out=[]){if(!v)return out;if(v.component){out.push(v.component);components(v.component.subTree,out)}if(Array.isArray(v.children))for(const c of v.children)if(c&&typeof c==='object')components(c,out);return out}
const child=(m,name)=>components(m.app._instance.subTree).find(c=>(c.type.__name??c.type.name)===name || (name==='WorkforceChannelsTab' && typeof c.props.syncServerFieldIntoSnapshot==='function' && 'activeChannelPlatform' in c.props));
const inputName=value=>{const n=document.querySelector('.emp-name-input input');assert.ok(n);n.value=value;n.dispatchEvent(new window.Event('input',{bubbles:true}))};
async function mount({props={},overrides={},total,fetchExtra,realChannelApi=false}={}){
 const calls=[],errors=[],requests=[],guards=[];let saved=employee();activeWorkspaceId.value='workspace-1';
 globalThis.fetch=async(url,init={})=>{requests.push({url,init});if(fetchExtra){const result=await fetchExtra(url,init);if(result)return result;}if(/\/agents\?/.test(String(url))){const page=Number(new URL(String(url),'http://localhost').searchParams.get('page'));const rows=await tracked.listEmployees(page);return Response.json({items:rows.map(e=>({id:e.id,name:e.name,role:e.role,avatar:e.avatar,enabled:e.status==='active',harness:'pi',model_profile_id:'model-1',model_name:'测试模型',version:e.version,instruction:e.prompt})),total:total??rows.length,status_counts:{enabled:rows.filter(e=>e.status==='active').length,disabled:rows.filter(e=>e.status==='disabled').length},page,page_size:20});}if(/^\/api\/v1\/workspaces\?/.test(String(url)))return Response.json({items:[{id:'workspace-1',name:'工作区1',role:'owner'},{id:'workspace-2',name:'工作区2',role:'owner'}],total:2});if(/^\/api\/v1\/workspaces\/[^/?]+$/.test(String(url)))return Response.json({role:'owner'});if(String(url).includes('/triggers')||String(url).includes('/trigger-notification-targets')||String(url).includes('/selectors/people/sources'))return Response.json({items:[],truncated:false});if(String(url).endsWith('/handoff-scope'))return Response.json({version:'0',scope:{mode:'all',employee_ids:[]}});if(String(url).endsWith('/human-recipients'))return Response.json({version:'0',recipients:[]});if(String(url).endsWith('/maintainers'))return Response.json({version:1,people:[]});if(String(url).includes('/sharing-policies?'))return Response.json({items:[],total:0});if(String(url).includes('/capability-packages'))return Response.json({items:[],total:0,page:1,page_size:20});if(String(url).includes('/model-profiles?include_platform=true'))return Response.json({items:[{id:'model-1',source:'workspace',name:'测试模型',protocol:'openai',model_id:'fixture-model',enabled:true,validated:true,is_default:true}],total:1,page:1,page_size:20});throw new Error(`Unexpected HTTP ${url}`)};
 const platformPorts=realChannelApi?createWorkforcePlatformPorts():undefined;
 const platformReads=platformPorts?.workforceApi;
 const api={listEmployees:async()=>[saved],getEmployee:async()=>saved,getEmployeeChannelStatuses:async()=>({status:'active',channels:[]}),updateEmployeeBasics:async(_id,payload,version)=>{saved={...saved,...payload,version:version+1};return structuredClone(saved)},getLatestChannelQrRegistration:async()=>({session:structuredClone(qr)}),pollChannelQrRegistration:async()=>structuredClone(qr),cancelChannelQrRegistration:async()=>({}),
  ...(platformReads?{getEmployee:platformReads.getEmployee,getEmployeeChannelStatuses:platformReads.getEmployeeChannelStatuses,getEmployeeUsage:platformReads.getEmployeeUsage}:{}),...overrides};
 const tracked=Object.fromEntries(Object.entries(api).map(([name,fn])=>[name,async(...args)=>{calls.push({name,ws:activeWorkspaceId.value,args:JSON.parse(JSON.stringify(args))});return fn(...args)}]));
 const host=document.createElement('div');document.body.appendChild(host);const app=createApp({render:()=>h(NMessageProvider,null,{default:()=>h(NDialogProvider,null,{default:()=>h(WorkforceView,props)})})});app.config.errorHandler=e=>errors.push(e.message);app.provide(workforcePortsKey,{workforceApi:tracked,resourcesApi:{},tasksApi:{},groupPoliciesApi:platformPorts?.groupPoliciesApi});app.provide('d2DraftGuard',{register:g=>guards.push(g),unregister:()=>{}});app.mount(host);await pause();return{app,calls,errors,requests,guards,async cleanup(){app.unmount();host.remove();await pause()}};
}

test('Original employee cards open the original six-tab detail and display unavailable statistics',async()=>{const m=await mount();try{assert.ok(document.querySelector('article'));assert.ok(button('管理配置'));button('管理配置').click();await pause();assert.ok(document.querySelector('.employee-detail-container'));assert.equal(document.querySelectorAll('.employee-detail-container > .n-tabs .n-tabs-tab').length,6);assert.equal(document.querySelectorAll('.s-val').length,3);assert.ok([...document.querySelectorAll('.s-val')].every(n=>n.textContent==='暂无数据'));tab('设定与模型').click();await pause();const level=[...document.querySelectorAll('.fb-item')].find(n=>n.textContent.includes('深度思考等级'));assert.match(level.textContent,/暂无数据/);assert.ok(level.querySelector('.n-base-selection--disabled'));assert.deepEqual(m.errors,[])}finally{await m.cleanup()}});

test('Real settings and permissions number their visible sections in order', async () => {
 const m = await mount({props: {selectedEmployeeId: 'employee-1'}});
 try {
  button('设置人工确认规则').click();
  await pause();
  assert.deepEqual([...document.querySelectorAll('h4.fb-title')].map(heading => heading.textContent.trim()), [
   '1. 员工档案基本信息', '2. 人工确认规则', '3. 底层执行引擎与基座模型'
  ]);
  const profileHeading = [...document.querySelectorAll('h4.fb-title')].find(heading => heading.textContent.includes('员工档案基本信息'));
  assert.ok(profileHeading.closest('.form-block').querySelector('textarea[aria-label="岗位行为规范与人设约束"]'));
  const rules = document.querySelector('[aria-labelledby="workforce-human-recipients-title"]');
  assert.match(rules.textContent, /尚未配置确认人/);
  assert.match(rules.textContent, /投递状态与补救/);
  assert.equal(m.requests.some(({url}) => url.endsWith('/human-recovery')), false);
  assert.ok(m.requests.some(({url}) => url === '/api/v1/workspaces/workspace-1/agents/employee-1/human-recipients'));
  tab('权限和共享').click();
  await pause();
  assert.deepEqual([...document.querySelectorAll('.cb-title')].map(heading => heading.textContent.trim()), [
   '1. AI 员工使用范围', '2. AI 员工接手范围', '3. 群消息共享'
  ]);
  assert.deepEqual(m.errors, []);
 } finally {
  await m.cleanup();
 }
});

test('Employee handoff scope uses its own versioned API in permissions and preserves the draft on conflict', async () => {
 let writes = 0;
 const m = await mount({props: {selectedEmployeeId: 'employee-1', initialTab: 'rules'}, fetchExtra: async (url, init) => {
   if (!url.endsWith('/handoff-scope')) return;
   if (init.method === 'PUT') {
     if (++writes === 1) return Response.json({error: {code: 'work_scope_changed', message: '员工接手规则已变更，请重新读取后保存'}}, {status: 409});
     return Response.json({version: '5', scope: JSON.parse(init.body).scope});
   }
   return Response.json({version: writes ? '4' : '3', scope: {mode: 'all', employee_ids: []}});
 }});
 try {
   const card = document.querySelector('[aria-labelledby="employee-receiver-heading"]');
   const chooseAllowlist = async () => {
     const input = [...card.querySelectorAll('.n-radio')].find(item => item.textContent.includes('仅允许白名单')).querySelector('input');
     input.checked = true;
     input.dispatchEvent(new window.Event('change', {bubbles: true}));
     await pause();
   };
   assert.match(card.textContent, /黑名单留空时允许全部员工/);
   assert.equal(button('保存'), undefined);
   await chooseAllowlist();
   assert.match(card.textContent, /白名单留空时不接受任何员工的交接/);
   assert.equal(m.guards[0].hasUnsaved(), true);
   const save = async () => {
     [...card.querySelectorAll('button')].find(item => item.textContent.trim() === '保存').click();
     await pause();
   };
   await save();
   assert.ok(button('重新读取'));
   assert.match(card.textContent, /白名单留空时不接受任何员工的交接/);
   button('重新读取').click();
   await pause();
   await chooseAllowlist();
   await save();
   assert.match(card.textContent, /接手规则已保存/);
   assert.equal(m.guards[0].hasUnsaved(), false);
   assert.deepEqual(m.requests.filter(({init,url}) => url.endsWith('/handoff-scope') && init.method === 'PUT').map(({init}) => JSON.parse(init.body)), [
     {version: '3', scope: {mode: 'allowlist', employee_ids: []}},
     {version: '4', scope: {mode: 'allowlist', employee_ids: []}},
   ]);
   assert.equal(m.calls.some(({name}) => name === 'saveEmployeeRules'), false);
   assert.deepEqual(m.errors, []);
 } finally {
   await m.cleanup();
 }
});

test('The confirmation editor exposes four sources and saves a channel default without a specialist scope', async () => {
  const webRef = {kind: 'user', provider: 'web', workspace_id: 'workspace-1', agent_id: 'employee-1', external_id: 'approver'};
  const ref = {...webRef, provider: 'dingtalk', binding_id: 'ding-binding', tenant_id: 'ding-tenant', identity_namespace: 'user_id', identity_epoch: 1, connection_generation: 2};
  const row = {ref, display_name: '兜底同事', status: 'active', selectable: true};
  const m = await mount({props: {selectedEmployeeId: 'employee-1', initialTab: 'persona'}, fetchExtra: async (url, init) => {
    if (url.endsWith('/human-recipients') && !init.method) return Response.json({version: '1', recipients: [{ref: webRef, name: 'Web 同事', scope: '财务'}]});
    if (url.endsWith('/human-recipients') && init.method === 'PATCH') {
      const body = JSON.parse(init.body);
      return Response.json({version: '2', recipients: body.recipients});
    }
    if (url.includes('/selectors/people/sources?')) return Response.json({items: ['web', 'feishu', 'dingtalk', 'wecom'].map(provider => ({provider, available: true, supports_department_tree: ['feishu', 'dingtalk'].includes(provider)}))});
    if (url.includes('/selectors/people?')) return Response.json({items: url.includes('provider=dingtalk') ? [row] : [], next_cursor: null});
    if (url.endsWith('/selectors/people/resolve')) return Response.json({items: [row]});
  }});
  try {
    button('选择默认确认人').click();
    await pause();
    const modal = document.querySelector('.n-modal');
    modal.querySelector('.n-base-selection').dispatchEvent(new window.MouseEvent('click', {bubbles: true}));
    await pause();
    const options = [...document.querySelectorAll('.n-base-select-option')];
    assert.deepEqual(options.map(option => option.textContent.trim()), ['Web', '飞书', '钉钉', '企微']);
    options[2].click();
    await pause();
    assert.match(modal.textContent, /兜底同事/);
    assert.doesNotMatch(modal.textContent, /选择部门/);
    button('选择').click();
    await pause();
    const editor = document.querySelector('[aria-labelledby="workforce-human-recipients-title"]');
    assert.match(editor.textContent, /默认确认人.*兜底同事/s);
    [...editor.querySelectorAll('button')].find(node => node.textContent.trim() === '保存').click();
    await pause();
    const request = m.requests.find(({url, init}) => url.endsWith('/human-recipients') && init.method === 'PATCH');
    assert.deepEqual(JSON.parse(request.init.body), {version: '1', recipients: [
      {ref: webRef, name: 'Web 同事', scope: '财务'},
      {ref, name: '兜底同事', scope: '', is_default: true}
    ]});
    assert.deepEqual(m.errors, []);
  } finally {
    await m.cleanup();
  }
});

test('An employee maintainer can configure a multi-person whitelist without department or inherited access', async () => {
  const selected = ['first', 'second'].map(external_id => ({
    ref: {kind: 'user', provider: 'web', workspace_id: 'workspace-1', agent_id: 'employee-1', external_id},
    display_name: external_id, status: 'active', selectable: true
  }));
  const m = await mount({props: {selectedEmployeeId: 'employee-1', initialTab: 'rules'},
    overrides: {getEmployee: async () => employee({canManage: true})},
    fetchExtra: (url, init) => {
      if (/^\/api\/v1\/workspaces\?/.test(url)) return Response.json({items: [{id: 'workspace-1', role: 'member'}], total: 1});
      if (url.includes('/selectors/people/sources?purpose=maintenance')) return Response.json({items: ['web','feishu','dingtalk','wecom'].map(provider => ({provider, available: true, supports_department_tree: true}))});
      if (url.includes('/selectors/people?') && url.includes('purpose=maintenance')) return Response.json({items: selected, next_cursor: null});
      if (url.endsWith('/selectors/people/resolve') && JSON.parse(init.body).purpose === 'maintenance') return Response.json({items: selected.filter(c => c.ref.external_id === JSON.parse(init.body).refs[0].external_id)});
      if (url.endsWith('/maintainers') && init.method === 'PUT') return Response.json({version: 2, people: selected});
    }
  });
  try {
    for (let n = 0; n < 2; n++) {
      button('添加维护人').click();
      await pause();
      assert.doesNotMatch(document.querySelector('.n-modal').textContent, /选择部门|继承使用范围|黑名单/);
      const picks = [...document.querySelectorAll('.n-modal button')].filter(b => b.textContent.trim() === '选择');
      picks[n].click();
      await pause();
    }
    const editor = document.querySelector('[aria-labelledby="workforce-maintainers-title"]');
    assert.match(editor.textContent, /first.*second/s);
    [...editor.querySelectorAll('button')].find(b => b.textContent.trim() === '保存').click();
    await pause();
    const request = m.requests.find(({url, init}) => url.endsWith('/maintainers') && init.method === 'PUT');
    assert.deepEqual(JSON.parse(request.init.body), {version: 1, people: selected.map(p => p.ref)});
    assert.deepEqual(m.errors, []);
  } finally {
    await m.cleanup();
  }
});

test('Create shows a multiline responsibility example and submits the entered lines', async () => {
 const m = await mount({
  overrides: {createEmployee: async payload => employee(payload)},
  fetchExtra: url => url.includes('/model-profiles?include_platform=true') ? Response.json({
   items: [{id: 'model-1', source: 'workspace', name: '测试模型', protocol: 'chat_completions', enabled: true, validated: true, is_default: true}], total: 1, page: 1, page_size: 20
  }) : undefined
 });
 try {
  button('+ 新建 AI 员工').click();
  await pause();
  const modal = document.querySelector('.n-modal');
  const name = modal.querySelector('input[placeholder="例如：SRE 故障值班助手"]');
  const role = modal.querySelector('textarea[aria-label="岗位职责"]');
  assert.ok(role);
  assert.equal(role.value, '');
  const roleHint = role.closest('.n-input').querySelector('.n-input__placeholder').textContent;
  assert.deepEqual(roleHint.match(/【[^】]+】/g), ['【服务对象】', '【负责事项】', '【交付成果】']);
  const responsibilities = '支持研发团队\n归纳故障和跟进事项';
  for (const [field, value] of [[name, '研发值班助手'], [role, responsibilities]]) {
   field.value = value;
   field.dispatchEvent(new window.Event('input', {bubbles: true}));
  }
  await pause();
  const create = button('创建并进入渠道配置');
  assert.equal(create.disabled, false);
  create.click();
  await pause();
  const call = m.calls.find(c => c.name === 'createEmployee');
  assert.ok(call);
  assert.equal(call.args[0].role, responsibilities);
  assert.deepEqual(m.errors, []);
 } finally {
  await m.cleanup();
 }
});

test('Basic profile saves multiline responsibilities and retains the separate prompt draft', async () => {
 const m = await mount({props: {selectedEmployeeId: 'employee-1', initialTab: 'persona'}});
 try {
  inputName('保存后的名字');
  const basics = document.querySelector('.form-block');
  const role = basics.querySelector('textarea[aria-label="岗位职责"]');
  const promptBlock = basics.querySelector('.persona-profile-guidance');
  const prompt = promptBlock.querySelector('textarea');
  assert.equal(role.value, '研发助手');
  role.value = '';
  role.dispatchEvent(new window.Event('input', {bubbles: true}));
  prompt.value = '';
  prompt.dispatchEvent(new window.Event('input', {bubbles: true}));
  await pause();
  const roleHint = role.closest('.n-input').querySelector('.n-input__placeholder').textContent;
  const constraintHint = promptBlock.querySelector('.n-input__placeholder').textContent;
  assert.deepEqual(roleHint.match(/【[^】]+】/g), ['【服务对象】', '【负责事项】', '【交付成果】']);
  assert.deepEqual(constraintHint.match(/【[^】]+】/g), ['【回复方式】', '【信息不足】', '【确认前提】']);
  assert.doesNotMatch(roleHint + constraintHint, /转交|交接/);
  const rolePresetBar = basics.querySelector('.persona-basic-stack .persona-preset-bar');
  assert.ok(rolePresetBar);
  const targetChip = [...rolePresetBar.querySelectorAll('button.persona-preset-chip')].find(b => b.textContent.trim() === '+ 服务对象');
  const dutyChip = [...rolePresetBar.querySelectorAll('button.persona-preset-chip')].find(b => b.textContent.trim() === '+ 负责事项');
  assert.ok(targetChip && dutyChip);
  targetChip.click();
  await pause();
  assert.equal(basics.querySelector('textarea[aria-label="岗位职责"]').value, '【服务对象】');
  assert.equal(targetChip.textContent.trim(), '✓ 服务对象');
  assert.equal(targetChip.disabled, true);
  assert.ok(targetChip.classList.contains('applied'));
  dutyChip.click();
  await pause();
  assert.equal(basics.querySelector('textarea[aria-label="岗位职责"]').value, '【服务对象】\n【负责事项】');
  assert.equal(dutyChip.textContent.trim(), '✓ 负责事项');
  assert.equal(dutyChip.disabled, true);
  const responsibilities = '支持研发团队\n整理每周问题清单';
  role.value = responsibilities;
  role.dispatchEvent(new window.Event('input', {bubbles: true}));
  prompt.value = '尚未保存的岗位草稿';
  prompt.dispatchEvent(new window.Event('input', {bubbles: true}));
  await pause();
  [...basics.querySelectorAll('button')].find(n => n.textContent.trim() === '保存').click();
  await pause();
  assert.match(document.body.textContent, /员工基本信息已保存/);
  assert.equal(document.querySelector('.emp-name-input input').value, '保存后的名字');
  assert.equal(basics.querySelector('textarea[aria-label="岗位职责"]').value, responsibilities);
  assert.equal(promptBlock.querySelector('textarea').value, '尚未保存的岗位草稿');
  const call = m.calls.find(c => c.name === 'updateEmployeeBasics');
  assert.equal(call.args[2], 7);
  assert.deepEqual(call.args[1], {name: '保存后的名字', role: responsibilities});
  assert.deepEqual(m.errors, []);
  assert.equal(m.guards[0].hasUnsaved(), true);
 } finally {
  await m.cleanup();
 }
});

test('Channel resource versions update only their card and cannot advance another basic draft',async()=>{const m=await mount({props:{selectedEmployeeId:'employee-1',initialTab:'channels'}});try{const channels=child(m,'WorkforceChannelsTab');assert.ok(channels);const sync=channels.props.syncServerFieldIntoSnapshot;tab('设定与模型').click();await pause();inputName('仍然是本地草稿');await pause();sync('employee-1',employee({version:99,name:'不可覆盖的远端名字',resourceVersions:{usageAccess:90,usageHistory:8,triggerRules:9,channels:{feishu:10}}}),['channels']);await pause();assert.equal(document.querySelector('.emp-name-input input').value,'仍然是本地草稿');tab('渠道接入与群策略').click();await pause();const after=child(m,'WorkforceChannelsTab');assert.equal(after.props.employee.version,7);assert.deepEqual(JSON.parse(JSON.stringify(after.props.employee.resourceVersions)),{usageAccess:2,usageHistory:8,triggerRules:9,channels:{feishu:10}});assert.equal(after.props.savedEmployee.name,'测试 AI 员工');assert.equal(after.props.savedEmployee.version,7);sync('employee-1',employee({version:100,resourceVersions:{usageAccess:11,usageHistory:90,triggerRules:91,channels:{feishu:92}}}),['unifiedAccess']);await pause();assert.equal(after.props.employee.version,7);assert.deepEqual(JSON.parse(JSON.stringify(after.props.employee.resourceVersions)),{usageAccess:11,usageHistory:8,triggerRules:9,channels:{feishu:10}});assert.equal(after.props.employee.name,'仍然是本地草稿');assert.deepEqual(m.errors,[])}finally{await m.cleanup()}});

test('Employee detail loads policies on demand and a failed channel can be retried independently', async () => {
 const endpoint = '/api/v1/workspaces/workspace-1/agents/employee-1';
 const access = {mode:'allowlist',group_members_enabled:true,allow:[],deny:[]};
 const usage = {version:12,usage_config:{access,trigger:{mention:true,keywords:[],regexes:[]},history:{enabled:true,limit:20,channels:['wecom']}}};
 let feishuUnavailable = true;
 const m = await mount({
  props:{selectedEmployeeId:'employee-1',initialTab:'overview'},realChannelApi:true,
  fetchExtra:async url=>{
   if(url===endpoint)return Response.json({id:'employee-1',name:'测试 AI 员工',role:'研发助手',avatar:'',enabled:true,harness:'pi',version:7,instruction:'测试说明',model_profile_id:'model-1',model_name:'模型',model_available:true});
   if(url===endpoint+'/usage-config')return Response.json(usage);
   if(url===endpoint+'/trigger-rules')return Response.json({version:3,rules:[]});
   if(url.startsWith(endpoint+'/channels/')){
    const provider=url.split('/').at(-1);
    if(provider==='feishu'&&feishuUnavailable)return Response.json({error:{code:'unavailable',message:'飞书暂不可用',request_id:'channel-test'}},{status:503});
    return Response.json({id:provider,provider,app_id:provider+'_app',enabled:true,has_secret:true,connection_generation:1,config_revision:4,
     access:{mode:'allowlist',group_members_enabled:true,allow_ids:[],deny_ids:[]}});
   }
  },
 });
 const policyReads = () => m.requests.map(call=>call.url).filter(url=>url===endpoint+'/usage-config'||url===endpoint+'/trigger-rules').map(url=>url.split('/').at(-1));
 const channelReads = () => m.requests.map(call=>call.url).filter(url=>url.startsWith(endpoint+'/channels/')).map(url=>url.split('/').at(-1));
 try {
  await pause();
  assert.ok(document.querySelector('.employee-detail-container'));
  assert.match(document.body.textContent,/部分渠道配置读取失败/);
  assert.doesNotMatch(document.body.textContent,/员工配置读取失败/);
  assert.deepEqual(policyReads(),[]);

  tab('渠道接入与群策略').click();await pause();
  assert.match(document.querySelector('[aria-label="渠道配置读取失败"]').textContent,/飞书暂不可用.*channel-test/);
  assert.equal(button('绑定已有机器人'),undefined);
  tab('钉钉机器人').click();await pause();
  assert.match(document.body.textContent,/钉钉群配置/);
  assert.equal(button('重试读取渠道'),undefined);
  assert.deepEqual(policyReads(),[]);

  tab('企业微信智能机器人').click();await pause();
  assert.ok(document.querySelector('[aria-label="启用近期群历史"]'));
  assert.match(document.body.textContent,/员工使用白名单模式/);
  assert.deepEqual(policyReads(),['usage-config']);

  tab('飞书机器人').click();await pause();
  const previousReads = channelReads().length;
  feishuUnavailable = false;
  button('重试读取渠道').click();await pause();
  assert.deepEqual(channelReads().slice(previousReads),['feishu']);
  assert.equal(button('重试读取渠道'),undefined);
  assert.match(document.body.textContent,/飞书群配置/);
  assert.equal(document.querySelector('.channel-config-box details').open,false);
  assert.deepEqual(policyReads(),['usage-config','usage-config','trigger-rules']);

  tab('权限和共享').click();await pause();
  assert.ok(document.querySelector('.cb-form'));
  assert.match(document.body.textContent,/允许使用的人员或部门白名单/);
  assert.deepEqual(policyReads(),['usage-config','usage-config','trigger-rules','usage-config']);
  assert.deepEqual(m.errors,[]);
 } finally {
  await m.cleanup();
 }
});

test('Remote channel changes clear the old online state and retain drafts until a successful confirmed reload', async () => {
 const original = {
  platform: 'feishu', platformName: '飞书', connectionId: 'binding', appId: 'cli_old',
  status: 'connected', statusText: '已连接', connectionState: 'connected', permissionReady: true,
  usageScope: {mode: 'all', whitelistMembers: [], whitelistDepartments: [], blacklistMembers: [], allowedGroupIds: []},
  capabilities: {}, sharingRules: [], boundGroups: []
 };
 const disabled = {...original, appId: 'cli_new', status: 'disabled', statusText: '已停用', connectionState: 'disconnected'};
 const initial = employee({channels: [original]});
 const fresh = employee({channels: [disabled], resourceVersions: {...initial.resourceVersions, channels: {feishu: 5}}});
 let remoteChanged = false, failReload = false, observationUnavailable = false;
 const m = await mount({
  props: {selectedEmployeeId: 'employee-1', initialTab: 'channels'},
  overrides: {
   getEmployee: async () => {
    if (failReload) throw new Error('配置读取暂不可用');
    return remoteChanged ? fresh : initial;
   },
   getEmployeeChannelStatuses: async () => ({
    status: 'active',
    channels: observationUnavailable ? [{...original, status: 'pending_config', statusText: '状态未知', connectionState: 'disconnected'}]
     : remoteChanged ? [{...disabled, observedConfigRevision: 5}] : [{...original, observedConfigRevision: 4}]
   })
  }
 });
 try {
  assert.equal(document.querySelector('.connection-status').textContent.trim(), '已连接');
  tab('设定与模型').click();
  await pause();
  inputName('仍待保存的员工草稿');
  remoteChanged = true;
  window.dispatchEvent(new window.Event('focus'));
  await pause();
  tab('渠道接入与群策略').click();
  await pause();
  assert.equal(document.querySelector('.connection-status').textContent.trim(), '配置已变更');
  assert.equal(child(m, 'WorkforceChannelsTab').props.employee.resourceVersions.channels.feishu, 4);
  assert.equal(child(m, 'WorkforceChannelsTab').props.employee.channels[0].appId, 'cli_old');
  assert.equal(child(m, 'WorkforceChannelsTab').props.employee.channels[0].permissionReady, undefined);
  assert.equal(button('更新凭据 / 换绑').disabled, true);
  assert.equal(m.guards[0].hasUnsaved(), true);

  observationUnavailable = true;
  window.dispatchEvent(new window.Event('focus'));
  await pause();
  assert.equal(document.querySelector('.connection-status').textContent.trim(), '配置已变更');
  button('重新加载配置').click();
  await pause();
  button('保留草稿').click();
  await pause();
  assert.equal(m.calls.filter(call => call.name === 'getEmployee').length, 1);

  failReload = true;
  button('重新加载配置').click();
  await pause();
  button('重新加载').click();
  await pause();
  assert.match(document.body.textContent, /配置读取暂不可用/);
  assert.equal(child(m, 'WorkforceChannelsTab').props.employee.name, '仍待保存的员工草稿');
  assert.equal(child(m, 'WorkforceChannelsTab').props.employee.resourceVersions.channels.feishu, 4);

  failReload = false;
  observationUnavailable = false;
  button('重新加载配置').click();
  await pause();
  button('重新加载').click();
  await pause();
  assert.equal(button('重新加载配置'), undefined);
  assert.equal(document.querySelector('.connection-status').textContent.trim(), '已停用');
  assert.equal(child(m, 'WorkforceChannelsTab').props.employee.resourceVersions.channels.feishu, 5);
  assert.equal(child(m, 'WorkforceChannelsTab').props.employee.channels[0].appId, 'cli_new');
  assert.equal(m.guards[0].hasUnsaved(), false);
  assert.deepEqual(m.errors, []);
 } finally {
  await m.cleanup();
 }
});

test('Initial channels stay idle until explicit creation loads the official QR',async()=>{const m=await mount({props:{selectedEmployeeId:'employee-1',initialTab:'channels'}});try{assert.ok(button('创建智能机器人'));assert.ok(button('绑定已有机器人'));assert.equal(m.calls.some(c=>c.name==='getLatestChannelQrRegistration'),false);button('创建智能机器人').click();await pause();assert.ok(document.querySelector('.onboarding-qr-pane'));const qrComponent=components(m.app._instance.subTree).find(c=>/QrCode/i.test(c.type.name??''));assert.equal(qrComponent.props.value,qr.verificationUrl);assert.ok(m.calls.some(c=>c.name==='getLatestChannelQrRegistration'));assert.ok(m.requests.every(r=>String(r.url).includes('/agents?page=') || String(r.url).includes('/model-profiles?include_platform=true') || /^\/api\/v1\/workspaces\?/.test(String(r.url))));assert.deepEqual(m.errors,[])}finally{await m.cleanup()}});

test('Workspace changes discard late employee list responses from the previous workspace',async()=>{let finish;const m=await mount({overrides:{listEmployees:()=>activeWorkspaceId.value==='workspace-1'?new Promise(resolve=>{finish=resolve}):Promise.resolve([employee({id:'new-employee',name:'新工作区员工'})])}});try{activeWorkspaceId.value='workspace-2';await pause();finish([employee({name:'旧工作区迟到员工'})]);await pause();assert.match(document.body.textContent,/新工作区员工/);assert.doesNotMatch(document.body.textContent,/旧工作区迟到员工/);assert.ok(m.calls.some(c=>c.name==='listEmployees'&&c.ws==='workspace-2'));assert.deepEqual(m.errors,[])}finally{await m.cleanup()}});

test('Approved real trigger/package modules do not fall back to Mock and child drafts guard tab navigation',async()=>{
 const m=await mount({props:{selectedEmployeeId:'employee-1',initialTab:'triggers'}});
 try{
  assert.ok(button('新增自动化任务'));assert.ok(m.requests.some(r=>String(r.url).endsWith('/agents/employee-1/triggers')));
  button('新增自动化任务').click();await pause();const input=document.querySelector('input[placeholder="例如：客户日报"]');assert.ok(input);input.value='未保存触发器草稿';input.dispatchEvent(new window.Event('input',{bubbles:true}));await pause();assert.equal(m.guards[0].hasUnsaved(),true);
  assert.equal(await m.guards[0].save(),false);await pause();assert.match(document.body.textContent,/分别保存/);assert.equal(m.calls.some(c=>c.name==='saveFullEmployee'),false);
  tab('设定与模型').click();await pause();assert.match(document.body.textContent,/存在未保存的配置草稿/);button('继续编辑').click();await pause();assert.equal(document.querySelector('input[placeholder="例如：客户日报"]').value,'未保存触发器草稿');
  tab('能力包与授权').click();await pause();button('放弃修改并切换').click();await pause();await pause();assert.ok(m.requests.some(r=>String(r.url).includes('/agents/employee-1/capability-packages')));assert.equal(document.querySelector('input[placeholder="例如：客户日报"]'),null);assert.equal(m.guards[0].hasUnsaved(),false);assert.ok(m.requests.every(r=>String(r.url).startsWith('/api/v1/')));assert.deepEqual(m.errors,[]);
 }finally{await m.cleanup()}
});

test('Directory pages twenty records without model or employee detail fanout; detail outage stays separate',async()=>{
 const m=await mount({total:41,overrides:{listEmployees:async page=>Array.from({length:page===3?1:20},(_,i)=>employee({id:`p${page}-${i}`,name:`第${page}页员工${i}`})),getEmployee:async()=>{throw new Error('该员工配置暂不可用')}}});
 try{assert.equal(document.querySelectorAll('article.emp-card').length,20);assert.equal(m.calls.some(c=>c.name==='getEmployee'),false);assert.equal(m.requests.some(r=>r.url.includes('model-profiles')),false);button('下一页').click();await pause();assert.match(document.body.textContent,/第 2 页/);assert.match(document.body.textContent,/第2页员工0/);button('管理配置').click();await pause();assert.match(document.body.textContent,/员工配置读取失败/);assert.equal(document.querySelectorAll('article.emp-card').length,20);assert.equal(m.calls.filter(c=>c.name==='getEmployee').length,1);button('下一页').click();await pause();assert.equal(document.querySelectorAll('article.emp-card').length,1);assert.ok(button('下一页').disabled);assert.ok(m.requests.filter(r=>r.url.includes('/agents?')).every(r=>r.url.includes('page_size=20')));assert.deepEqual(m.errors,[]);}finally{await m.cleanup()}
});
test('Deep link loads selected employee outside current directory page and late detail cannot cross workspace',async()=>{
 let finish;const m=await mount({props:{selectedEmployeeId:'outside',initialTab:'persona'},overrides:{getEmployee:async id=>activeWorkspaceId.value==='workspace-1'?new Promise(r=>finish=()=>r(employee({id,name:'旧工作区详情'}))):employee({id,name:'当前工作区详情'})}});
 try{assert.ok(m.calls.some(c=>c.name==='getEmployee'&&c.args[0]==='outside'));activeWorkspaceId.value='workspace-2';await pause();finish();await pause();assert.equal(document.querySelector('.emp-name-input input').value,'当前工作区详情');assert.doesNotMatch(document.body.textContent,/旧工作区详情/);assert.deepEqual(m.errors,[]);}finally{await m.cleanup()}
});
test('Directory renders when management lookup is unavailable and never enables writes',async()=>{
 const m=await mount({fetchExtra:async url=>/^\/api\/v1\/workspaces\?/.test(url)?Response.json({error:{code:'unavailable',message:'管理权限暂不可用'}},{status:503}):null});
 try{assert.ok(document.querySelector('article.emp-card'));button('+ 新建 AI 员工').click();await pause();assert.match(document.body.textContent,/创建员工需要有效工作区成员身份/);assert.equal(document.querySelector('.n-modal'),null);assert.equal(m.requests.some(r=>r.url.includes('model-profiles')),false);assert.deepEqual(m.errors,[]);}finally{await m.cleanup()}
});

test('Confirmed creation resets directory to page one; optional read failure cannot reclassify write',async()=>{
 let fail=false;const m=await mount({total:41,overrides:{listEmployees:async page=>{if(fail)throw new Error('名录刷新暂不可用');return [employee({name:`page${page}`})]},getEmployee:async()=>{throw new Error('配置刷新暂不可用')}}});
 try{button('下一页').click();await pause();assert.match(document.body.textContent,/第 2 页/);fail=true;const directory=components(m.app._instance.subTree).find(c=>'filteredEmployees' in c.props);assert.ok(directory);directory.emit('created',employee({id:'confirmed-new',name:'已确认新员工'}));await pause();assert.match(document.body.textContent,/第 1 页/);assert.match(document.body.textContent,/刷新数据失败/);assert.match(document.body.textContent,/员工配置读取失败/);assert.doesNotMatch(document.body.textContent,/创建 AI 员工失败/);assert.equal(m.calls.filter(c=>c.name==='listEmployees').at(-1).args[0],1);assert.equal(m.calls.some(c=>c.name==='createEmployee'),false);assert.deepEqual(m.errors,[]);}finally{await m.cleanup()}
});
test('Accepted basic save arriving after workspace change does not overwrite new employee or show failure',async()=>{
 let finish;const m=await mount({props:{selectedEmployeeId:'employee-1',initialTab:'persona'},overrides:{updateEmployeeBasics:async()=>new Promise(r=>finish=()=>r(employee({name:'旧空间已保存',version:8}))),getEmployee:async()=>employee({name:activeWorkspaceId.value==='workspace-1'?'原名字':'新空间名字'})}});
 try{inputName('保存请求');await pause();const block=document.querySelector('.form-block');[...block.querySelectorAll('button')].find(b=>b.textContent.trim()==='保存').click();await pause();activeWorkspaceId.value='workspace-2';await pause();finish();await pause();assert.equal(document.querySelector('.emp-name-input input').value,'新空间名字');assert.doesNotMatch(document.body.textContent,/旧空间已保存|保存失败/);assert.equal(m.calls.filter(c=>c.name==='updateEmployeeBasics').length,1);assert.deepEqual(m.errors,[]);}finally{await m.cleanup()}
});

test('Member directory renders the full card summary before and after filtering and rejects stale results', async () => {
  let finishSlowSearch;
  const seen = [];
  const target = {
    id: '12345678-1111-2222-3333-444444445678', name: '跨页目标', role: '财务', enabled: false,
    model_name: '模型', model_profile_id: 'm', config_version: 8, harness: 'pi',
    card_resources: {channels: ['feishu'], package_count: 3, trigger_count: 2}
  };
  const m = await mount({fetchExtra: async url => {
    if (/^\/api\/v1\/workspaces\?/.test(url)) {
      return Response.json({items: [{id: 'workspace-1', name: '工作区1', role: 'member'}], total: 1});
    }
    if (!url.includes('/agents?')) return;
    const query = new URL(url, 'http://localhost').searchParams;
    seen.push(query);
    if (query.get('q') === '慢查询') await new Promise(resolve => { finishSlowSearch = resolve; });
    const matched = query.has('q');
    const items = [{...target, name: query.get('q') === '慢查询' ? '旧查询员工' : target.name}];
    return Response.json({items, total: matched ? 1 : 21, page: 1, page_size: 20,
      status_counts: {enabled: matched ? 0 : 20, disabled: 1}});
  }});
  try {
    assert.ok(button('全部员工 (21)'));
    assert.ok(button('已启用 (20)'));
    assert.ok(button('下一页'));
    const card = document.querySelector('.emp-card');
    assert.equal(card.querySelector('.ec-code').textContent, '12345678…5678');
    assert.equal(card.querySelector('.ec-code').title, target.id);
    assert.ok(card.querySelector('.ec-channel-logos img[alt="飞书"]'));
    assert.match(card.textContent, /3 个能力包 · 2 触发器/);
    const search = document.querySelector('.list-search-input input');
    function type(value) {
      search.value = value;
      search.dispatchEvent(new window.Event('input', {bubbles: true}));
    }
    type('慢查询'); await pause();
    type('财务'); await pause();
    finishSlowSearch(); await pause();
    assert.doesNotMatch(document.body.textContent, /旧查询员工/);
    assert.ok(button('全部员工 (1)'));
    assert.equal(button('下一页'), undefined);
    button('已停用 (1)').click(); await pause();
    assert.deepEqual(seen.map(q => [q.get('q'), q.get('enabled'), q.get('page')]), [
      [null,null,'1'], ['慢查询',null,'1'], ['财务',null,'1'], ['财务','false','1']
    ]);
    const row = document.querySelector('.ec-resources-row');
    assert.ok(row.querySelector('.ec-channel-logos img[alt="飞书"]'));
    assert.match(row.textContent, /3 个能力包 · 2 触发器/);
    assert.equal(m.calls.some(c => c.name === 'getEmployee'), false);
    assert.deepEqual(m.errors, []);
  } finally { await m.cleanup(); }
});
test('Unavailable model cannot be enabled from employee detail',async()=>{
 const m=await mount({props:{selectedEmployeeId:'employee-1',initialTab:'overview'},overrides:{getEmployee:async()=>employee({status:'disabled',modelAvailable:false}),getEmployeeChannelStatuses:async()=>({status:'disabled',channels:[]})}});
 try{assert.ok(button('恢复在岗').disabled);button('恢复在岗').click();await pause();assert.equal(m.calls.some(c=>c.name==='updateEmployeeStatus'),false);assert.deepEqual(m.errors,[]);}finally{await m.cleanup()}
});

test('ordinary employee users open overview only; direct settings links do not expose other tabs', async () => {
  const m = await mount({
    props: {selectedEmployeeId:'employee-1', initialTab:'channels'},
    overrides: {getEmployee: async () => employee({canManage:false, canChat:true, prompt:'hidden-in-test'})},
    fetchExtra: url => /^\/api\/v1\/workspaces\?/.test(url)
      ? Response.json({items:[{id:'workspace-1',role:'member'}],total:1})
      : undefined,
  });
  try {
    assert.deepEqual([...m.app._container.querySelectorAll('.detail-tabs .n-tabs-tab')].map(el=>el.textContent.trim()),['概览']);
    assert.ok(button('开始对话'));
    assert.doesNotMatch(document.body.textContent,/hidden-in-test|立即接入渠道|停用员工/);
    assert.equal(m.requests.some(r=>r.url.includes('/channels/')||r.url.endsWith('/maintainers')), false);
    assert.deepEqual(m.errors,[]);
  }finally{await m.cleanup()}
});

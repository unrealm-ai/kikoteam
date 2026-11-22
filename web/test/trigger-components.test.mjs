import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
import {Window} from 'happy-dom';
import {parse,compileScript} from '@vue/compiler-sfc';
import esbuild from 'esbuild';
const window=new Window({url:'http://127.0.0.1:5173/'});
for(const name of ['window','document','navigator','HTMLElement','Element','SVGElement','Node','Text','Comment','Image','InputEvent','MouseEvent','KeyboardEvent','HTMLInputElement','Document','MutationObserver','ResizeObserver','getComputedStyle','requestAnimationFrame','cancelAnimationFrame'])if(name==='window')globalThis.window=window;else if(window[name]!==undefined)Object.defineProperty(globalThis,name,{configurable:true,writable:true,value:typeof window[name]==='function'&&['getComputedStyle','requestAnimationFrame','cancelAnimationFrame'].includes(name)?window[name].bind(window):window[name]});
const built=await esbuild.build({stdin:{contents:"export {default as View} from './src/direction2/views/PlatformTriggersPanel.vue';export {createApp,h,ref,nextTick} from 'vue';export {NDialogProvider} from 'naive-ui';",resolveDir:process.cwd(),loader:'ts'},bundle:true,write:false,format:'esm',platform:'node',define:{'import.meta.env.VITE_PLATFORM_REAL':'true','import.meta.env.DEV':'false'},plugins:[{name:'vue',setup(build){build.onLoad({filter:/\.vue$/},async({path})=>{if(path.endsWith('/TaskDetailPanel.vue'))return {contents:"import {defineComponent,h} from 'vue';export default defineComponent({props:['taskId','workspaceId'],setup:p=>()=>h('div',{'data-testid':'task-detail'},p.workspaceId+':'+p.taskId)})",loader:'ts'};const {descriptor}=parse(await fs.readFile(path,'utf8'));return {contents:compileScript(descriptor,{id:path,inlineTemplate:true}).content,loader:'ts'}})}}]});
const {View,createApp,h,ref,nextTick,NDialogProvider}=await import(`data:text/javascript;base64,${Buffer.from(built.outputFiles[0].text).toString('base64')}`);
const pause=async()=>{await new Promise(r=>setTimeout(r,40));await nextTick()};
test('Native event editor saves a prompt, previews an unknown field and displays the public address',async()=>{
  let saved;
  const url='https://hooks.example.test/api/triggers/events/tr-1';
  const m=await mount({handle:(path,init,body)=>{
    if(init.method==='POST'&&path.endsWith('/triggers')){saved={...trigger,...body,endpoint:url};return Response.json(saved)}
    if(saved&&!init.method&&path.endsWith('/tr-1'))return Response.json(saved);
    return null;
  }});
  try{
    button('新增自动化任务').click();await pause();
    await choose(0,'接收外部事件');await choose(1,'其他系统 · Bearer token');
    fill('例如：main 有推送时概括代码变化；新建 Issue 时给出标题和链接。','报告自定义事件');
    fill('粘贴一段通知正文','{"reading":21}');
    await pause();assert.match(document.querySelector('.event-preview').textContent,/reading/);
    const enabled = [...document.querySelectorAll('.n-checkbox')].find(box => box.textContent.includes('启用此触发器'));
    enabled.click(); await pause();
    button('仅创建').click();await pause();
    assert.equal(saved.enabled, false);
    assert.deepEqual(saved.config,{adapter:'event',source:'custom',prompt:'报告自定义事件'});
    assert.equal(document.querySelector('.trigger-detail textarea[readonly]').value,url);
    assert.ok(button('复制地址'));assert.match(document.body.textContent,/Bearer/);
  }finally{await m.cleanup()}
});
test('GitHub creation immediately exposes the enabled hook and copyable one-time secret', async t => {
  let saved;
  const endpoint = 'http://hooks.example.test/api/triggers/events/tr-1';
  const first = {key_id:'initial', secret:'test-first-key', status:'active', created_at:trigger.created_at};
  const copied = [];
  const original = Object.getOwnPropertyDescriptor(navigator, 'clipboard');
  Object.defineProperty(navigator, 'clipboard', {configurable:true, value:{writeText: async value => copied.push(value)}});
  t.after(() => original ? Object.defineProperty(navigator, 'clipboard', original) : delete navigator.clipboard);
  let finishRead;
  const m = await mount({ handle: (path, init, body) => {
    if (init.method === 'POST' && path.endsWith('/triggers')) {
      saved = {...trigger, ...body, endpoint};
      return Response.json({...saved, initial_credential:first});
    }
    if (saved && path.endsWith('/tr-1')) {
      if (!finishRead) return new Promise(resolve => { finishRead = resolve; });
      return Response.json(saved);
    }
  }});
  try {
    button('新增自动化任务').click();
    await pause();
    await choose(0, '接收外部事件');
    assert.doesNotMatch(document.body.textContent, /GitHub 仓库 ID/);
    fill('例如：main 有推送时概括代码变化；新建 Issue 时给出标题和链接。', '按事件类型回告群');
    button('创建并启用').click();
    await pause();
    assert.deepEqual(saved.config, {adapter:'event', source:'github', prompt:'按事件类型回告群'});
    const detail = document.querySelector('.trigger-detail');
    assert.equal(detail.querySelector('textarea[readonly]').value, endpoint);
    assert.equal(saved.enabled, true);
    assert.equal(detail.querySelectorAll('textarea[readonly]')[1].value, first.secret);
    button('复制地址').click();
    button('复制密钥').click();
    await pause();
    assert.deepEqual(copied, [endpoint, first.secret]);
    finishRead(Response.json(saved));
    await pause();
    button('刷新接入与投递').click();
    await pause();
    assert.ok(button('复制密钥'));
    detail.closest('[role="dialog"]').querySelector('button[aria-label="close"]').click();
    await pause();
    button('查看触发记录与接入信息').click();
    await pause();
    assert.equal(button('复制密钥'), undefined);
    assert.equal(m.calls.filter(c => c.init.method).length, 1);
  } finally { await m.cleanup(); }
});

test('GitLab signing setup creates a paused hook and saves its own token and activation together', async () => {
  let saved;
  const m = await mount({handle:(path, init, body) => {
    if (init.method === 'POST' && path.endsWith('/triggers')) saved = {...trigger, ...body, endpoint:'https://hooks.example.test/api/triggers/events/tr-1'};
    if (init.method === 'PUT') saved = {...saved, enabled:body.enabled, version:'4'};
    if (saved && (init.method || path.endsWith('/tr-1'))) return Response.json(saved);
  }});
  try {
    button('新增自动化任务').click(); await pause();
    await choose(0, '接收外部事件'); await choose(1, 'GitLab · Signing token');
    fill('例如：main 有推送时概括代码变化；新建 Issue 时给出标题和链接。', '汇总');
    button('仅创建').click(); await pause();
    assert.equal(saved.enabled, false);
    assert.equal(button('复制密钥'), undefined);
    fill('GitLab Signing token', 'provider-token'); await pause();
    button('保存并启用').click(); await pause();
    const writes = m.calls.filter(c => c.init.method);
    assert.deepEqual(writes.map(c => c.init.method), ['POST','PUT']);
    assert.equal(writes[1].body.signing_token, 'provider-token');
    assert.equal(writes[1].body.version, '3');
    assert.equal(saved.enabled, true);
    assert.equal(button('保存并启用'), undefined);
    assert.equal(document.querySelector('[placeholder="GitLab Signing token"]').value, '');
    assert.equal(button('刷新接入与投递').disabled, false);
  } finally { await m.cleanup(); }
});

test('Copying uses the address inside the dialog when clipboard permission is denied', async t => {
  const endpoint = 'http://hooks.example.test/api/triggers/events/tr-1';
  const hook = {...trigger, endpoint, config:{adapter:'event',source:'github',prompt:'汇总变更'}};
  const original = Object.getOwnPropertyDescriptor(navigator, 'clipboard');
  Object.defineProperty(navigator, 'clipboard', {configurable:true, value:{writeText: async () => { throw new Error('denied'); }}});
  t.after(() => original ? Object.defineProperty(navigator, 'clipboard', original) : delete navigator.clipboard);
  let allowed = true, selected;
  const originalCommand = Object.getOwnPropertyDescriptor(document, 'execCommand');
  Object.defineProperty(document, 'execCommand', {configurable:true, value: command => {
    assert.equal(command, 'copy');
    const field = document.activeElement;
    selected = field.value.slice(field.selectionStart, field.selectionEnd);
    return allowed;
  }});
  t.after(() => originalCommand ? Object.defineProperty(document, 'execCommand', originalCommand) : delete document.execCommand);
  const m = await mount({handle:path => path.endsWith('/tr-1') ? Response.json(hook) : null});
  try {
    button('查看触发记录与接入信息').click();
    await pause();
    button('复制地址').click();
    await pause();
    assert.equal(selected, endpoint);
    const status = document.querySelector('.trigger-detail .address [role="status"]');
    assert.match(status.textContent, /已复制/);
    allowed = false;
    button('复制地址').click();
    await pause();
    assert.equal(selected, endpoint);
    assert.match(status.textContent, /手动复制/);
    assert.doesNotMatch(status.textContent, /已复制/);
  } finally { await m.cleanup(); }
});
const trigger={name:'测试触发器',notification_targets:[],id:'tr-1',workspace_id:'w',agent_id:'a',kind:'webhook',enabled:false,version:'3',config:{adapter:'generic'},allowed_actions:['create_task'],created_at:'2026-11-18T00:00:00Z',updated_at:'2026-11-18T00:00:00Z',next_due:null};
const candidate=(id,provider='web',name='员工群')=>({ref:{id,kind:'group',provider,workspace_id:'w',agent_id:'a',identity_namespace:'chat_id',external_id:'external-'+id},name,provider,evidence:provider==='web'?'member':'observed',coverage:provider==='web'?'all_joined':'observed_only',selectable:true,status:'active'});
const button=t=>[...document.querySelectorAll('button')].find(b=>b.textContent.trim()===t);
async function mount({handle,canManage=true}={}){const calls=[],guards=[],workspace=ref('w');globalThis.fetch=async(url,init={})=>{const body=init.body?JSON.parse(init.body):null;calls.push({url,init,body});const v=handle?.(url,init,body);if(v)return v;if(init.method&&url.endsWith('/selectors/groups/resolve'))return Response.json({items:body.refs.map(ref=>({ref,name:'已核验群',selectable:true,status:'active'}))});if(url.includes('/selectors/groups?'))return Response.json({items:[],next_cursor:null});if(init.method&&url.endsWith('/trigger-group-candidates/resolve'))return Response.json({conversation_id:'resolved-conversation'});if(init.method)return Response.json(trigger);if(url.includes('/trigger-group-candidates?'))return Response.json({items:[],truncated:false});if(url.includes('/trigger-notification-targets?'))return Response.json({items:[],truncated:false});if(url.includes('/notifications?'))return Response.json({items:[],truncated:false});if(url.endsWith('/triggers'))return Response.json({items:[trigger]});if(url.endsWith('/credentials'))return Response.json({items:[{key_id:'k',status:'active',created_at:trigger.created_at}]});if(url.includes('/events?'))return Response.json({items:[],next_cursor:null});return Response.json(trigger)};const host=document.createElement('div');document.body.append(host);const app=createApp({render:()=>h(NDialogProvider,null,{default:()=>h(View,{workspace:workspace.value,employeeId:'a',canManage})})});app.provide('d2DraftGuard',{register:v=>guards.push(v),unregister:()=>{}});app.mount(host);await pause();return{calls,guards,workspace,async cleanup(){app.unmount();host.remove();await pause()}}}
function fill(placeholder,value){const input=document.querySelector(`[placeholder="${placeholder}"]`);assert.ok(input);input.value=value;input.dispatchEvent(new window.Event('input',{bubbles:true}))}
test('Trigger manager uses real list and cursor events, no webhook execution',async()=>{const m=await mount();try{button('查看触发记录与接入信息').click();await pause();assert.ok(m.calls.some(c=>c.url.endsWith('/triggers/tr-1/credentials')));assert.ok(m.calls.some(c=>c.url.endsWith('/events?limit=20')));assert.equal(m.calls.filter(c=>c.init.method).length,0);assert.match(document.body.textContent,/已受理不等于执行完成/)}finally{await m.cleanup()}});
test('Schedule creation defaults enabled and sends real complete body',async()=>{const m=await mount();try{button('新增自动化任务').click();await pause();fill('让员工完成什么工作','汇总客户日报');button('创建并启用').click();await pause();const c=m.calls.find(c=>c.init.method==='POST');assert.ok(c.url.endsWith('/triggers'));assert.deepEqual(c.body,{name:'自动化任务',notification_targets:[],kind:'schedule',enabled:true,config:{cron:'0 9 * * *',timezone:'Asia/Shanghai',description:'汇总客户日报',businessKey:{mode:'empty'}},allowed_actions:['create_task']});assert.ok(c.init.headers.get('X-CSRF-Protection'));}finally{await m.cleanup()}});
test('Trigger edit sends version CAS and conflict preserves draft without auto retry',async()=>{const m=await mount({handle:(url,init)=>init.method==='PUT'?Response.json({error:{code:'version_conflict',message:'配置冲突'}},{status:409}):null});try{button('编辑配置').click();await pause();button('保存触发器').click();await pause();assert.equal(m.calls.filter(c=>c.init.method==='PUT').length,1);assert.equal(m.calls.find(c=>c.init.method==='PUT').body.version,'3');assert.match(document.body.textContent,/配置冲突/);assert.ok(button('保存触发器'));assert.doesNotMatch(document.body.textContent,/配置已保存/)}finally{await m.cleanup()}});
test('Member has no trigger requests or write controls',async()=>{const m=await mount({canManage:false});try{assert.equal(m.calls.length,0);assert.equal(button('新增自动化任务'),undefined);assert.match(document.body.textContent,/管理员权限/)}finally{await m.cleanup()}});
test('Credential confirmation is bound to workspace and cannot write after navigation',async()=>{const m=await mount();try{button('查看触发记录与接入信息').click();await pause();button('生成新签名密钥').click();await pause();m.workspace.value='w2';await pause();button('确认生成').click();await pause();assert.equal(m.calls.filter(c=>c.init.method).length,0)}finally{await m.cleanup()}});
test('Rotated secret only appears after confirmation and clears on workspace change',async()=>{const m=await mount({handle:(url,init)=>init.method==='POST'?Response.json({key_id:'new-key',secret:'test-only-one-time-secret',status:'active',created_at:trigger.created_at}):null});try{button('查看触发记录与接入信息').click();await pause();assert.equal(document.querySelector('.trigger-detail .n-alert textarea[readonly]'),null);button('生成新签名密钥').click();await pause();assert.equal(m.calls.filter(c=>c.init.method).length,0);button('确认生成').click();await pause();assert.equal(document.querySelector('.trigger-detail .n-alert textarea[readonly]').value,'test-only-one-time-secret');m.workspace.value='w2';await pause();assert.equal(document.querySelector('.trigger-detail .n-alert textarea[readonly]'),null)}finally{await m.cleanup()}});
test('403 invalidates older detail response and prevents credential requests',async()=>{let finish,lists=0;const m=await mount({handle:(url,init)=>!init.method&&url.endsWith('/tr-1')?new Promise(r=>finish=r):url.endsWith('/triggers')&&++lists>1?Response.json({error:{code:'forbidden',message:'权限撤销'}},{status:403}):null});try{button('查看触发记录与接入信息').click();await pause();button('刷新触发器').click();await pause();finish(Response.json(trigger));await pause();assert.equal(button('生成新签名密钥'),undefined);assert.equal(m.calls.some(c=>c.url.endsWith('/credentials')),false)}finally{await m.cleanup()}});
test('Notification selector writes only resolved conversation IDs and preserves paging',async()=>{const m=await mount({handle:url=>url.includes('/selectors/groups?')?Response.json({items:[candidate('web-candidate')],next_cursor:'more'}):null});try{button('新增自动化任务').click();await pause();fill('让员工完成什么工作','日报');button('选择').click();await pause();assert.ok(button('移除此群'));button('创建并启用').click();await pause();const c=m.calls.find(c=>c.init.method==='POST'&&c.url.endsWith('/triggers'));assert.deepEqual(c.body.notification_targets,[{conversation_id:'resolved-conversation'}]);assert.equal(c.body.config.notificationTargets,undefined);assert.equal(c.body.config.notification_targets,undefined)}finally{await m.cleanup()}});
test('Group delivery shows accepted, sent, failed and unknown independently of task completion',async()=>{const m=await mount({handle:url=>url.includes('/notifications?')?Response.json({items:['accepted','sent','failed','unknown'].map((status,i)=>({source_input_id:'i'+i,source_turn_id:'turn',task_id:'task',conversation_id:'group',status,context_status:'pending',created_at:trigger.created_at})),truncated:false}):null});try{button('查看触发记录与接入信息').click();await pause();for(const label of ['渠道已受理，送达待核对','发送已确认','发送失败','结果未知，不自动重发'])assert.match(document.body.textContent,new RegExp(label));assert.doesNotMatch(document.body.textContent,/任务已完成/)}finally{await m.cleanup()}});
test('Group candidate error never becomes empty success or permits saving',async()=>{const m=await mount({handle:url=>url.includes('/trigger-notification-targets?')?Response.json({error:{code:'dependency_unavailable',message:'群目标读取失败'}},{status:503}):null});try{button('新增自动化任务').click();await pause();assert.match(document.body.textContent,/群目标读取失败/);assert.equal(button('创建并启用').disabled,true);assert.equal(m.calls.filter(c=>c.init.method).length,0)}finally{await m.cleanup()}});
async function choose(index,label){document.querySelectorAll('.n-base-selection')[index].dispatchEvent(new window.MouseEvent('click',{bubbles:true}));await pause();const opt=[...document.querySelectorAll('.n-base-select-option')].reverse().find(o=>o.textContent.trim()===label);assert.ok(opt,label);opt.click();document.body.click();await pause();}
test('Fixed reminder requires target and never submits a model task or business key',async()=>{const m=await mount({handle:url=>url.includes('/selectors/groups?')?Response.json({items:[candidate('group')],next_cursor:null}):null});try{button('新增自动化任务').click();await pause();await choose(1,'发送固定文案提醒（不调用模型）');fill('让员工完成什么工作','请更新日报');button('创建并启用').click();await pause();assert.match(document.body.textContent,/固定提醒至少需要一个群目标/);assert.equal(m.calls.some(c=>c.init.method==='POST'&&c.url.endsWith('/triggers')),false);button('选择').click();await pause();button('创建并启用').click();await pause();const c=m.calls.find(c=>c.init.method==='POST'&&c.url.endsWith('/triggers'));assert.equal(c.body.config.action,'send_message');assert.equal(c.body.config.businessKey,undefined);assert.deepEqual(c.body.allowed_actions,['send_message']);assert.deepEqual(c.body.notification_targets,[{conversation_id:'resolved-conversation'}]);}finally{await m.cleanup()}});
test('GitHub Issue adapter uses real dedicated config and signature instructions',async()=>{const gh={...trigger,config:{adapter:'github_issue',repositoryId:123,installationId:456}};const m=await mount({handle:(url,init)=>!init.method&&url.endsWith('/tr-1')?Response.json(gh):null});try{button('新增自动化任务').click();await pause();await choose(0,'GitHub 新建 Issue');const nums=document.querySelectorAll('.n-input-number input');for(const [i,n] of [123,456].entries()){nums[i].value=String(n);nums[i].dispatchEvent(new window.Event('input',{bubbles:true}));nums[i].dispatchEvent(new window.Event('blur',{bubbles:true}));}await pause();button('创建并启用').click();await pause();const c=m.calls.find(c=>c.init.method==='POST');assert.equal(c.body.kind,'webhook');assert.equal(c.body.config.adapter,'github_issue');assert.deepEqual(c.body.allowed_actions,['create_task']);assert.match(document.body.textContent,/\/api\/triggers\/github\/tr-1/);assert.match(document.body.textContent,/X-Hub-Signature-256/);assert.doesNotMatch(document.body.textContent,/X-Timestamp 的原值/)}finally{await m.cleanup()}});
test('All six calendar key periods are selectable and submitted with IANA timezone',async()=>{for(const [period,label] of [['minute','分钟'],['hour','小时'],['day','日'],['week','周（周一开始）'],['month','月'],['year','年']]){const m=await mount();try{button('新增自动化任务').click();await pause();fill('让员工完成什么工作','周期汇总');fill('Asia/Shanghai','America/New_York');await choose(2,'按日历周期');await choose(3,label);assert.match(document.body.textContent,/周从周一开始/);assert.match(document.body.textContent,/UTC 起止时刻/);button('创建并启用').click();await pause();const c=m.calls.find(c=>c.init.method==='POST');assert.deepEqual(c.body.config.businessKey,{mode:'period',period});assert.equal(c.body.config.timezone,'America/New_York');assert.equal(c.body.config.business_key,undefined)}finally{await m.cleanup()}}});
test('Fixed calendar key uses unchanged user value',async()=>{const m=await mount();try{button('新增自动化任务').click();await pause();fill('让员工完成什么工作','业务汇总');await choose(2,'固定业务键');fill('固定业务键','customer-42');button('创建并启用').click();await pause();assert.deepEqual(m.calls.find(c=>c.init.method==='POST').body.config.businessKey,{mode:'fixed',value:'customer-42'})}finally{await m.cleanup()}});
test('Candidate resolution uses provider evidence and does not select pending or late results',async()=>{let finish;const m=await mount({handle:(url,init)=>url.includes('/selectors/groups?')?Response.json({items:[candidate('observed','wecom')],next_cursor:null}):url.endsWith('/trigger-group-candidates/resolve')?new Promise(r=>finish=r):null});try{button('新增自动化任务').click();await pause();button('选择').click();await pause();const c=m.calls.find(c=>c.url.endsWith('/trigger-group-candidates/resolve'));assert.deepEqual(c.body,{provider:'wecom',candidate_id:'observed'});assert.equal(button('移除此群'),undefined);assert.equal(button('创建并启用').disabled,true);m.workspace.value='w2';await pause();finish(Response.json({conversation_id:'late-conversation'}));await pause();assert.equal(button('移除此群'),undefined);assert.doesNotMatch(document.body.textContent,/late-conversation/)}finally{await m.cleanup()}});
test('Failed candidate resolution never forges a group ID into notification targets',async()=>{const m=await mount({handle:url=>url.includes('/selectors/groups?')?Response.json({items:[candidate('external-group-id','dingtalk')],next_cursor:null}):url.endsWith('/trigger-group-candidates/resolve')?Response.json({error:{code:'candidate_stale',message:'候选已失效'}},{status:409}):null});try{button('新增自动化任务').click();await pause();button('选择').click();await pause();assert.match(document.body.textContent,/候选已失效/);assert.equal(button('移除此群'),undefined);fill('让员工完成什么工作','汇总');button('创建并启用').click();await pause();assert.deepEqual(m.calls.find(c=>c.init.method==='POST'&&c.url.endsWith('/triggers')).body.notification_targets,[])}finally{await m.cleanup()}});
test('Double-click credential creation opens one dialog and submits only one request',async()=>{let finish;const m=await mount({handle:(url,init)=>init.method==='POST'&&url.endsWith('/credentials')?new Promise(r=>finish=r):null});try{button('查看触发记录与接入信息').click();await pause();const create=button('生成新签名密钥');create.click();create.click();await pause();const confirms=[...document.querySelectorAll('button')].filter(b=>b.textContent.trim()==='确认生成');assert.equal(confirms.length,1);confirms[0].click();confirms[0].click();await pause();assert.equal(m.calls.filter(c=>c.init.method==='POST'&&c.url.endsWith('/credentials')).length,1);finish(Response.json({key_id:'single',secret:'one-secret',status:'active',created_at:trigger.created_at}));await pause();assert.equal(document.querySelector('.trigger-detail .n-alert textarea[readonly]').value,'one-secret')}finally{await m.cleanup()}});
test('Feishu real group discovery paginates then re-queries unified candidates',async()=>{let page=0;const m=await mount({handle:(url,init,body)=>url.endsWith('/channels/feishu/groups/discover')?(page++,Response.json({items:[],next_cursor:body.cursor?'':'next'})):url.includes('/selectors/groups?')&&page?Response.json({items:[candidate('candidate-page'+page,'feishu')],next_cursor:null}):null});try{button('新增自动化任务').click();await pause();await choose(document.querySelectorAll('.n-base-selection').length-1,'飞书');button('刷新飞书群').click();await pause();button('继续刷新飞书群').click();await pause();assert.deepEqual(m.calls.filter(c=>c.url.endsWith('/groups/discover')).map(c=>c.body),[{cursor:''},{cursor:'next'}]);assert.equal(button('继续刷新飞书群'),undefined);button('选择').click();await pause();assert.deepEqual(m.calls.find(c=>c.url.endsWith('/trigger-group-candidates/resolve')).body,{provider:'feishu',candidate_id:'candidate-page2'});assert.ok(button('移除此群'))}finally{await m.cleanup()}});
test('Repeated Feishu discovery cursor is rejected without claiming complete groups',async()=>{const m=await mount({handle:url=>url.endsWith('/groups/discover')?Response.json({items:[],next_cursor:'same'}):null});try{button('新增自动化任务').click();await pause();await choose(document.querySelectorAll('.n-base-selection').length-1,'飞书');button('刷新飞书群').click();await pause();button('继续刷新飞书群').click();await pause();assert.match(document.body.textContent,/群刷新游标重复/);assert.equal(button('移除此群'),undefined)}finally{await m.cleanup()}});
test('Unified group candidates use cursor pagination beyond initial page',async()=>{const m=await mount({handle:url=>url.includes('/selectors/groups?')?Response.json(url.includes('cursor=page-2')?{items:[candidate('dt-last','dingtalk')],next_cursor:null}:{items:[candidate('web-1')],next_cursor:'page-2'}):null});try{button('新增自动化任务').click();await pause();button('继续读取候选').click();await pause();assert.equal(button('继续读取候选'),undefined);assert.ok(m.calls.some(c=>c.url.includes('cursor=page-2')));document.querySelectorAll('.identity-picker li button')[1].click();await pause();assert.deepEqual(m.calls.find(c=>c.url.endsWith('/trigger-group-candidates/resolve')).body,{provider:'dingtalk',candidate_id:'dt-last'})}finally{await m.cleanup()}});
test('Candidate pagination loop discards candidates without deleting existing targets',async()=>{const m=await mount({handle:url=>url.includes('/selectors/groups?')?Response.json({items:[candidate('g')],next_cursor:'same'}):null});try{button('新增自动化任务').click();await pause();button('继续读取候选').click();await pause();assert.match(document.body.textContent,/候选分页游标重复/);assert.equal(button('继续读取候选'),undefined);assert.equal(button('选择'),undefined);assert.equal(button('移除此群'),undefined)}finally{await m.cleanup()}});
test('Web employee-group metadata can be configured without reading operator membership or messages',async()=>{const m=await mount({handle:url=>url.includes('/selectors/groups?')?Response.json({items:[candidate('employee-group','web','员工加入但管理员未加入')],next_cursor:null}):null});try{button('新增自动化任务').click();await pause();assert.match(document.body.textContent,/管理员无需本人入群/);assert.match(document.body.textContent,/不授予读取群消息的权限/);button('选择').click();await pause();assert.ok(button('移除此群'));assert.equal(m.calls.some(c=>/\/messages|\/members/.test(c.url)),false);assert.deepEqual(m.calls.find(c=>c.url.endsWith('/trigger-group-candidates/resolve')).body,{provider:'web',candidate_id:'employee-group'})}finally{await m.cleanup()}});

test('Closing trigger records discards late detail data and permits reopening', async () => {
  let finishFirstRead;
  const m = await mount({
    handle: url => {
      if (url.endsWith('/triggers/tr-1') && !finishFirstRead) {
        return new Promise(resolve => { finishFirstRead = resolve; });
      }
    }
  });
  try {
    button('查看触发记录与接入信息').click();
    await pause();
    const modal = document.querySelector('[role="dialog"][aria-label="触发记录与接入信息"]');
    assert.ok(modal);
    modal.querySelector('button[aria-label="close"]').click();
    finishFirstRead(Response.json(trigger));
    await pause();
    assert.equal(button('生成新签名密钥'), undefined);
    assert.equal(m.calls.some(c => c.url.includes('/events?')), false);

    button('查看触发记录与接入信息').click();
    await pause();
    assert.ok(button('生成新签名密钥'));
    assert.equal(m.calls.filter(c => c.url.endsWith('/credentials')).length, 1);
  } finally {
    await m.cleanup();
  }
});

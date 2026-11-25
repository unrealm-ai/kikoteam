import test from 'node:test';
import assert from 'node:assert/strict';
import {build} from 'esbuild';
const built=await build({entryPoints:['src/direction2/api/workforce-platform-data.ts'],bundle:true,write:false,format:'esm',platform:'node',define:{'import.meta.env':'{}'}});
const api=await import(`data:text/javascript;base64,${Buffer.from(built.outputFiles[0].text).toString('base64')}`);
const raw=()=>({agent:{id:'a',name:'员工',role:'研发',avatar:'',instruction:'真实提示词',harness:'pi',model_profile_id:'m',model_name:'M',model_available:false,enabled:true,version:7,config_version:8,admission_epoch:2,created_at:'',updated_at:''},usage:{version:9,usage_config:{access:{mode:'allowlist',group_members_enabled:true,allow:[{channel:'web',kind:'user',id:'u'},{channel:'feishu',kind:'user',id:'u',binding_id:'b',identity_epoch:3}],deny:[]},trigger:{mention:true,keywords:[],regexes:[]},history:{enabled:true,limit:20,channels:['feishu']}}},bindings:{},rules:{version:11,rules:[]},packages:[],triggers:[],targets:[]});
test('projection preserves separate revisions and does not invent statistics, thinking, model availability or aggregate health',()=>{
 const input=raw(),s=api.projectWorkforceSnapshot(input);
 assert.equal(s.employee.configVersion,8);assert.equal(s.employee.modelAvailable,false);assert.equal(s.employee.version,7);assert.equal(s.usage.version,9);assert.equal(s.rules.version,11);
 assert.deepEqual(s.employee.stats,{todayTasks:null,todayConversations:null,successRate:null});assert.equal(s.employee.thinkingLevel,null);
 assert.equal(s.employee.status,'active');assert.equal(s.availability.model,false);assert.equal(s.employee.channels.length,0);
 assert.equal(s.employee.unifiedAccess.whitelistTargets.length,2);assert.equal(s.employee.unifiedAccess.whitelistTargets[1].connectionId,'b');
 assert.deepEqual(s.employee.unifiedAccess.whitelistMemberIds,['web::::user::u','feishu::b::user::u']);assert.equal(s.employee.unifiedAccess.whitelistTargets[1].usageSelector.identity_epoch,3);assert.equal(s.employee.modelId,'workspace:m');
 s.agent.name='changed';assert.equal(input.agent.name,'员工');
});
test('named rule IDs and disabled rows survive projection, Web-only rules stay in raw snapshot',()=>{
 const x=raw();x.bindings.feishu={id:'b',provider:'feishu',app_id:'cli',enabled:true,has_secret:true,connection_generation:2,config_revision:3,rules_version:4,applied:{status:'connected',connection_generation:1,config_revision:3,rules_version:4}};
 x.rules.rules=[{id:'r',name:'规则',type:'keyword',pattern:'测试',enabled:false,channels:['feishu','web']},{id:'w',name:'Web',type:'regex',pattern:'x',enabled:true,channels:['web']}];
 const s=api.projectWorkforceSnapshot(x);assert.equal(s.employee.channels[0].status,'pending_config');assert.equal(s.employee.channels[0].capabilities.feishuRules[0].id,'r');assert.equal(s.employee.channels[0].capabilities.feishuRules[0].enabled,false);assert.equal(s.rules.rules.length,2);
});
function response(url,x){if(url.endsWith('/registration'))return Response.json({registration:null,permissions:[]});if(url.endsWith('/agents/a'))return Response.json(x.agent);if(url.endsWith('/usage-config'))return Response.json(x.usage);if(url.endsWith('/trigger-rules'))return Response.json(x.rules);if(url.includes('/capability-packages?'))return Response.json({items:[],total:0,page:1,page_size:20});if(url.endsWith('/triggers'))return Response.json({items:[]});if(url.includes('/trigger-notification-targets?'))return Response.json({items:[],truncated:false});return Response.json({error:{code:'channel_not_configured',message:'none'}},{status:404});}
test('employee detail skips policy reads and isolates failed channels from missing and bound channels',async()=>{
 const urls=[];globalThis.fetch=async url=>{urls.push(url);return response(url,raw());};
 const emp=await api.getWorkforcePlatformEmployee('w','a');assert.equal(emp.id,'a');assert.ok(urls.every(u=>u.startsWith('/api/v1/workspaces/w/agents/a')));
 assert.ok(urls.every(url=>!url.endsWith('/usage-config')&&!url.endsWith('/trigger-rules')));
 assert.equal(emp.channelReadErrors,undefined);
 assert.equal(api.getWorkforcePlatformSnapshot('w','a').agent.version,7);
 const copy=api.getWorkforcePlatformSnapshot('w','a');copy.agent.version=999;assert.equal(api.getWorkforcePlatformSnapshot('w','a').agent.version,7);
 const wecom={id:'bot',provider:'wecom',app_id:'bot_ID',enabled:true,has_secret:true,connection_generation:1,config_revision:2,
  access:{mode:'allowlist',group_members_enabled:false,allow_ids:[],allow_department_ids:[]}};
 globalThis.fetch=async url=>{
  if(url.endsWith('/channels/feishu'))return Response.json({error:{code:'forbidden',message:'拒绝'}},{status:403});
  if(url.endsWith('/channels/wecom'))return Response.json(wecom);
  return response(url,raw());
 };
 const partial=await api.getWorkforcePlatformEmployee('w','a');
 assert.equal(partial.name,'员工');
 assert.deepEqual(partial.channelReadErrors,{feishu:'拒绝'});
 assert.deepEqual(partial.channels.map(channel=>channel.platform),['wecom']);
 assert.equal(partial.channels[0].usageScope.mode,'whitelist');
 assert.equal(partial.channels[0].usageScope.groupMembersEnabled,false);
 assert.deepEqual(partial.resourceVersions,{channels:{wecom:2}});
});
test('invalidated in-flight read cannot repopulate stale revision cache',async()=>{
 let release;globalThis.fetch=async url=>{if(url.endsWith('/agents/a'))await new Promise(r=>release=r);return response(url,raw());};
 const pending=api.getWorkforcePlatformEmployee('w2','a');api.invalidateWorkforcePlatformSnapshot('w2');release();
 await assert.rejects(pending,{name:'AbortError'});assert.equal(api.getWorkforcePlatformSnapshot('w2','a'),undefined);
});

test('department descendant and identity metadata are lossless in canonical selection',()=>{const x=raw();x.usage.usage_config.access.allow=[{channel:'feishu',kind:'department',id:'d',binding_id:'b',identity_epoch:4,include_descendants:false}];const s=api.projectWorkforceSnapshot(x);assert.deepEqual(s.employee.unifiedAccess.whitelistTargets[0].usageSelector,x.usage.usage_config.access.allow[0]);assert.deepEqual(s.employee.unifiedAccess.whitelistMemberIds,['feishu::b::department::d']);});

test('the current binding supplies permission evidence for the business table',async()=>{
 const x=raw();const binding={id:'b',provider:'feishu',app_id:'cli',enabled:true,has_secret:true,connection_generation:1,config_revision:1};
 let checked=true;const urls=[];
 globalThis.fetch=async url=>{urls.push(url);return url.endsWith('/channels/feishu')?Response.json(checked?{...binding,permission_diagnostics:{ready:true,checked_at:'checked'},permission_groups:[{id:'docs',label:'文档',status:'granted',scopeStates:[{scope:'docs:read',status:'granted'}]}]}:binding):response(url,x);};
 let emp=await api.getWorkforcePlatformEmployee('groups','a');assert.equal(emp.channels[0].businessCapabilities[0].name,'文档');assert.equal(emp.channels[0].businessCapabilities[0].resourceAccessStatus,'unknown');assert.equal(emp.channels[0].permissionReady,true);
 assert.deepEqual(emp.channels[0].businessCapabilities.map(row=>row.id),['docs','feishu-card-callback']);
 checked=false;emp=await api.getWorkforcePlatformEmployee('groups','a');
 assert.deepEqual(emp.channels[0].businessCapabilities.map(({id,description,platformAuthStatus})=>({id,description,platformAuthStatus})),[
  {id:'feishu-card-callback',description:'可选',platformAuthStatus:'unknown'},
 ]);
 assert.equal(emp.channels[0].permissionReady,undefined);
 assert.ok(urls.every(url=>!url.endsWith('/registration')));
});

test('directory requests exactly one real 20-item page and never reads employee configuration',async()=>{
 const urls=[];globalThis.fetch=async url=>{urls.push(url);if(!url.endsWith('/agents?page=2&page_size=20'))throw new Error('Unrelated detail unavailable');return Response.json({items:[raw().agent],total:41,page:2,page_size:20});};
 const page=await api.getWorkforceEmployeePage('directory',2);assert.equal(page.page,2);assert.equal(page.total,41);assert.equal(page.items[0].name,'员工');assert.equal(urls.length,1);assert.equal(api.getWorkforcePlatformSnapshot('directory','a'),undefined);
});

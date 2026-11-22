import test from 'node:test';import assert from 'node:assert/strict';import {build} from 'esbuild';
const built=await build({stdin:{contents:"export * from './src/direction2/api/workforce-platform-actions';export * from './src/direction2/api/workforce-platform-data';",resolveDir:process.cwd(),loader:'ts'},bundle:true,write:false,format:'esm',platform:'node',define:{'import.meta.env':'{}'}});
const api=await import(`data:text/javascript;base64,${Buffer.from(built.outputFiles[0].text).toString('base64')}`);
const agent={id:'a',name:'Employee',role:'Role',avatar:'avatar',instruction:'Prompt',harness:'pi',model_profile_id:'m',model_name:'M',model_available:true,enabled:true,version:7};
const usage={version:9,usage_config:{access:{mode:'all',group_members_enabled:true,allow:[],deny:[]},trigger:{mention:true,keywords:[],regexes:[]},history:{enabled:true,limit:20,channels:['feishu']}}};
function response(url){if(url.endsWith('/registration'))return Response.json({registration:null,permissions:[]});if(url.endsWith('/agents/a'))return Response.json(agent);if(url.endsWith('/usage-config'))return Response.json(usage);if(url.endsWith('/trigger-rules'))return Response.json({version:9,rules:[]});if(url.includes('/capability-packages?'))return Response.json({items:[],total:0,page:1});if(url.endsWith('/triggers'))return Response.json({items:[]});if(url.includes('/trigger-notification-targets?'))return Response.json({items:[],truncated:false});return Response.json({error:{code:'channel_not_configured',message:'none'}},{status:404});}
test('basic save preserves employee CAS, omits unchanged model and never submits unrelated draft fields',async()=>{
 const writes=[];globalThis.fetch=async(url,init={})=>{if(init.method==='PATCH')writes.push(JSON.parse(init.body));return response(url);};
 await api.getWorkforcePlatformEmployee('w','a');const port=api.createWorkforcePlatformActions(()=> 'w');
 await port.updateEmployeeBasics('a',{name:'New',modelId:'workspace:m',thinkingLevel:null,channels:['not submitted']},7);
 assert.deepEqual(writes,[{name:'New',version:7}]);
});
test('stale form is rejected before write and full save refuses cross-resource draft overwrite',async()=>{
 let writes=0;globalThis.fetch=async(url,init={})=>{if(init.method==='PATCH')writes++;return response(url);};
 const employee=await api.getWorkforcePlatformEmployee('w','a');const port=api.createWorkforcePlatformActions(()=> 'w');
 await assert.rejects(port.updateEmployeeBasics('a',{name:'New'},6),/重新加载/);
 await assert.rejects(port.saveFullEmployee('a',{...employee,unifiedAccess:{...employee.unifiedAccess,allowGroupMembers:!employee.unifiedAccess.allowGroupMembers}},7),/分别保存/);
 assert.equal(writes,0);
});
test('thinking and unsupported engine never silently become successful settings',async()=>{
 globalThis.fetch=async url=>response(url);await api.getWorkforcePlatformEmployee('w','a');const port=api.createWorkforcePlatformActions(()=> 'w');
 await assert.rejects(port.updateEmployeeBasics('a',{thinkingLevel:'high'},7),/暂无/);
 await assert.rejects(port.updateEmployeeBasics('a',{executionEngine:'codex'},7),/仅支持/);
});

test('read-only status polling discovers a completed background bind and exposes unavailable observations',async()=>{
 const calls=[];
 let binding,unavailable=false;
 globalThis.fetch=async(url,init={})=>{
  calls.push(init.method||'GET');
  if(url.endsWith('/channels/feishu')&&binding)return unavailable?Response.json({error:{code:'down',message:'异常'}},{status:503}):Response.json(binding);
  return response(url);
 };
 await api.getWorkforcePlatformEmployee('background','a');
 const port=api.createWorkforcePlatformActions(()=> 'background');
 assert.equal(api.getWorkforcePlatformSnapshot('background','a').employee.resourceVersions.usageAccess,undefined);
 await port.getEmployeeUsage('a','background',new AbortController().signal);
 binding={id:'new',provider:'feishu',app_id:'cli_new',enabled:true,has_secret:true,connection_generation:1,config_revision:1};
 const added=await port.getEmployeeChannelStatuses('a');
 assert.equal(added.addedBindings[0].channel.connectionId,'new');
 assert.equal(added.addedBindings[0].revision,1);
 assert.equal(api.getWorkforcePlatformSnapshot('background','a').employee.resourceVersions.usageAccess,9);
 unavailable=true;
 const status=await port.getEmployeeChannelStatuses('a');
 assert.equal(status.addedBindings.length,0);
 assert.equal(status.channels[0].statusText,'状态未知');
 assert.ok(calls.every(method=>method==='GET'));
});

test('status polling reports remote binding changes without moving saved versions', async () => {
 const original = {
  id: 'old', provider: 'feishu', app_id: 'cli_old', enabled: true, has_secret: true,
  connection_generation: 1, config_revision: 1,
  applied: {status: 'connected', connection_generation: 1, config_revision: 1}
 };
 let remote = Response.json(original);
 const calls = [];
 globalThis.fetch = async (url, init = {}) => {
  calls.push(init.method || 'GET');
  return url.endsWith('/channels/feishu') ? remote.clone() : response(url);
 };
 await api.getWorkforcePlatformEmployee('observed', 'a');
 const port = api.createWorkforcePlatformActions(() => 'observed');
 await port.getEmployeeUsage('a','observed',new AbortController().signal);
 remote = Response.json({...original, enabled: false, config_revision: 2});
 let status = await port.getEmployeeChannelStatuses('a');
 assert.equal(status.channels[0].observedConfigRevision, 2);
 assert.equal(status.channels[0].statusText, '已停用');
 remote = Response.json({...original, id: 'replacement', app_id: 'cli_new'});
 status = await port.getEmployeeChannelStatuses('a');
 assert.equal(status.channels[0].connectionId, 'replacement');
 assert.deepEqual(status.addedBindings, []);
 remote = Response.json({error: {code: 'channel_not_configured', message: 'none'}}, {status: 404});
 status = await port.getEmployeeChannelStatuses('a');
 assert.equal(status.channels[0].bindingMissing, true);
 const snapshot = api.getWorkforcePlatformSnapshot('observed', 'a');
 assert.equal(snapshot.bindings.feishu.id, 'old');
 assert.equal(snapshot.employee.resourceVersions.channels.feishu, 1);
 assert.equal(snapshot.employee.resourceVersions.usageAccess, 9);
 const edited = structuredClone(snapshot.employee.channels);
 edited[0].configurationChanged = true;
 edited[0].capabilities.recentContextWindow = 50;
 await assert.rejects(port.saveEmployeeChannels('a', edited), /渠道配置已变更/);
 assert.ok(calls.every(method => method === 'GET'));
});

test('confirmed basic write returns server truth and advances CAS without optional detail refresh',async()=>{
 globalThis.fetch=async url=>response(url);await api.getWorkforcePlatformEmployee('confirmed','a');let writes=0,reads=0;
 globalThis.fetch=async(url,init={})=>{if(init.method==='PATCH'){writes++;return Response.json({...agent,name:'服务端保存',version:8});}reads++;throw new Error('后续配置不可用');};
 const saved=await api.createWorkforcePlatformActions(()=> 'confirmed').updateEmployeeBasics('a',{name:'请求名字'},7);
 assert.equal(saved.name,'服务端保存');assert.equal(saved.version,8);assert.equal(api.getWorkforcePlatformSnapshot('confirmed','a').agent.version,8);assert.equal(writes,1);assert.equal(reads,0);
});
test('create accepted across workspace switch keeps returned identity with no retry or refresh in new scope',async()=>{
 let w='before',release;const urls=[];globalThis.fetch=async(url,init={})=>{urls.push([url,init.method]);if(init.method!=='POST')throw new Error('详情故障');return new Promise(r=>release=()=>r(Response.json({...agent,id:'created',version:1})));};
 const pending=api.createWorkforcePlatformActions(()=>w).createEmployee({name:'new',modelId:'workspace:m'});w='after';release();const saved=await pending;
 assert.equal(saved.id,'created');assert.equal(saved.unifiedAccess.mode,'all_with_blacklist');assert.deepEqual(saved.unifiedAccess.blacklistMemberIds,[]);assert.deepEqual(urls,[['/api/v1/workspaces/before/agents','POST']]);assert.equal(api.getWorkforcePlatformSnapshot('after','created'),undefined);
});

test('real unavailable model blocks enable before HTTP but does not block disable',async()=>{
 let enabled=false,writes=0;globalThis.fetch=async(url,init={})=>{if(init.method==='PATCH'){writes++;enabled=!enabled;}if(url.endsWith('/agents/a'))return Response.json({...agent,enabled,model_available:false});return response(url);};
 await api.getWorkforcePlatformEmployee('model-block','a');const port=api.createWorkforcePlatformActions(()=> 'model-block');await assert.rejects(port.updateEmployeeStatus('a','active',7),/模型当前不可用/);assert.equal(writes,0);
 enabled=true;await api.getWorkforcePlatformEmployee('model-block','a');await port.updateEmployeeStatus('a','disabled',7);assert.equal(writes,1);
});

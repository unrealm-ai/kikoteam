import test from 'node:test';import assert from 'node:assert/strict';import {build} from 'esbuild';
const built=await build({entryPoints:['src/direction2/api/workforce-channel-binding.ts'],bundle:true,write:false,format:'esm',platform:'node',define:{'import.meta.env':'{}'}});
const {bindingForWorkforce}=await import(`data:text/javascript;base64,${Buffer.from(built.outputFiles[0].text).toString('base64')}`);
const usage={usage_config:{access:{mode:'all',allow:[],deny:[]},trigger:{keywords:[],regexes:[]},history:{enabled:true,limit:20,channels:['feishu','wecom']}}};
const binding={provider:'feishu',id:'binding',app_id:'app',enabled:true,has_secret:true,connection_generation:2,applied:{status:'connected',connection_generation:1}};
test('obsolete hub generation does not count as connected',()=>{const r=bindingForWorkforce(binding,usage);assert.equal(r.connectionState,'disconnected');assert.equal(r.permissionReady,undefined);});
test('connection and permission readiness are independent observations',()=>{const r=bindingForWorkforce({...binding,applied:{status:'connected',connection_generation:2}},usage,{ready:false,scope_status:'failed'});assert.equal(r.connectionState,'connected');assert.equal(r.permissionReady,false);});
test('disabled channel stays disconnected even if hub retains previous connected state',()=>{const r=bindingForWorkforce({...binding,enabled:false,applied:{status:'connected',connection_generation:2}},usage);assert.equal(r.status,'disabled');assert.equal(r.connectionState,'disconnected');});

test('WeCom distinguishes a current authentication failure from a previous configuration', () => {
  const applied = {status: 'failed', connection_generation: 2, config_revision: 3, error_code: 'wecom_authentication_failed'};
  const wecom = {...binding, provider: 'wecom', config_revision: 3, applied};
  const failed = bindingForWorkforce(wecom, usage);
  assert.equal(failed.connectionState, 'error');
  assert.match(failed.errorDetail, /BotID 和 Secret/);
  assert.equal(bindingForWorkforce({...wecom, config_revision: 4}, usage).statusText, '正在连接');
  const connected = bindingForWorkforce({...wecom, applied: {...applied, status: 'connected'}}, usage);
  assert.equal(connected.statusText, '已连接');
  assert.equal(connected.errorDetail, undefined);
});

test('unknown permission evidence stays unknown for all providers and failed Feishu is explicit',()=>{for(const provider of ['feishu','dingtalk','wecom'])assert.equal(bindingForWorkforce({...binding,provider},usage).permissionReady,undefined);assert.equal(bindingForWorkforce(binding,usage,{ready:false,permissions:[{required:true,status:'unknown'}]}).permissionReady,undefined);assert.equal(bindingForWorkforce(binding,usage,{ready:false,permissions:[{required:true,status:'missing'}]}).permissionReady,false);});
test('Feishu actual permission groups never imply specific resource access',()=>{const r=bindingForWorkforce(binding,usage,{ready:true},[{id:'docs',label:'文档',status:'granted',scopeStates:[{scope:'doc:read',status:'granted'}]},{id:'base',label:'表格',status:'missing',scopeStates:[{scope:'a',status:'granted'},{scope:'b',status:'missing'}]}]);assert.equal(r.permissionReady,true);assert.equal(r.businessCapabilities[0].platformAuthStatus,'authorized');assert.equal(r.businessCapabilities[0].resourceAccessStatus,'unknown');assert.equal(r.businessCapabilities[1].platformAuthStatus,'partial');assert.match(r.businessCapabilities[1].statusDetail,/b：缺失/);});
test('current binding evidence replaces cached readiness and points to the missing Feishu scope',()=>{
  const group={id:'docs',label:'文档',status:'missing',scopeStates:[{scope:'doc:read',required:true,status:'missing'}]};
  const projected=bindingForWorkforce({...binding,app_id:'cli_current',permission_groups:[group],
    permission_diagnostics:{ready:true,scope_status:'pass',permissions:group.scopeStates}},usage);
  assert.equal(projected.permissionReady,false);
  const url=new URL(projected.businessCapabilities[0].authorizationUrl);
  assert.equal(url.pathname,'/app/cli_current/auth');
  assert.equal(url.searchParams.get('q'),'doc:read');
  assert.equal(bindingForWorkforce(binding,usage).permissionReady,undefined);
});
test('connection tooltip only receives safe code from the current backend observation',()=>{const applied={status:'failed',connection_generation:2,config_revision:3,error_code:'connection_failed'};const b={...binding,config_revision:3,applied};assert.equal(bindingForWorkforce(b,usage).connectionErrorCode,'connection_failed');assert.equal(bindingForWorkforce({...b,config_revision:4},usage).connectionErrorCode,undefined);assert.equal(bindingForWorkforce({...b,applied:{...applied,status:'connected'}},usage).connectionErrorCode,undefined);for(const code of ['', 'Secret: abc', '<script>', 'https://token.test/', 'x'.repeat(97)])assert.equal(bindingForWorkforce({...b,applied:{...applied,error_code:code}},usage).connectionErrorCode,undefined)});

test('DingTalk proactive-send permission warning comes only from enabled DingTalk binding', () => {
  const ding = {...binding, provider: 'dingtalk', dingtalk_proactive_send_permission_missing: true};
  assert.equal(bindingForWorkforce(ding, usage).dingtalkProactiveSendPermissionMissing, true);
  assert.equal(bindingForWorkforce({...ding, enabled: false}, usage).dingtalkProactiveSendPermissionMissing, undefined);
  assert.equal(bindingForWorkforce({...ding, provider: 'wecom'}, usage).dingtalkProactiveSendPermissionMissing, undefined);
});

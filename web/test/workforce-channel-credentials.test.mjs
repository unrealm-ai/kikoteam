import test from 'node:test';
import assert from 'node:assert/strict';
import { build } from 'esbuild';
const bundled = await build({ entryPoints:['src/direction2/api/workforce-channel-credentials.ts'],bundle:true,write:false,format:'esm',platform:'node',define:{'import.meta.env':'{}'} });
const {channelCredentialPayload,createWorkforceChannelCredentialTransport} = await import(`data:text/javascript;base64,${Buffer.from(bundled.outputFiles[0].text).toString('base64')}`);
for (const [provider, credentials] of Object.entries({feishu:{appId:' app ',appSecret:' secret '},dingtalk:{appKey:' app ',appSecret:' secret '},wecom:{botId:' app ',botSecret:' secret '}})) {
 test(`${provider}: original form keys map to credential-only save with binding revision and disabled state retained`,async()=>{
  assert.deepEqual(channelCredentialPayload(provider,credentials),{app_id:'app',app_secret:'secret'});
  const calls=[];const binding={agent_id:'employee',provider,config_revision:19,enabled:false};const signal=new AbortController().signal;
  const port=createWorkforceChannelCredentialTransport({save:async(...args)=>{calls.push(args);return {...binding,config_revision:20};}});
  const result=await port.replace('workspace','employee',provider,binding,credentials,signal);
  assert.deepEqual(calls,[['workspace','employee',provider,{app_id:'app',app_secret:'secret',version:19,enabled:false},signal]]);
  assert.equal(result.config_revision,20);
 });
}
test('invalid credentials and mismatched binding never save',async()=>{
 let called=false;const port=createWorkforceChannelCredentialTransport({save:async()=>{called=true;}});
 for(const value of ['', '****','••••']) assert.throws(()=>channelCredentialPayload('feishu',{appId:'id',appSecret:value}));
 await assert.rejects(port.replace('w','a','wecom',{agent_id:'b',provider:'wecom'},{botId:'id',botSecret:'secret'},new AbortController().signal));assert.equal(called,false);
});
test('conflict or unknown save is propagated once, not retried or replaced with Mock success',async()=>{
 let attempts=0;const failure=new Error('version_conflict');const port=createWorkforceChannelCredentialTransport({save:async()=>{attempts++;throw failure;}});
 await assert.rejects(port.replace('w','a','feishu',{agent_id:'a',provider:'feishu',config_revision:1,enabled:true},{appId:'id',appSecret:'secret'},new AbortController().signal),e=>e===failure);assert.equal(attempts,1);
});

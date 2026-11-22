import test from 'node:test';
import assert from 'node:assert/strict';
import esbuild from 'esbuild';
const code = esbuild.buildSync({entryPoints:['src/direction2/api/models.ts'],bundle:true,write:false,platform:'node',format:'esm',define:{'import.meta.env.VITE_PLATFORM_REAL':'true'}}).outputFiles[0].text;
const {modelProfilesApi:api} = await import(`data:text/javascript;base64,${Buffer.from(code).toString('base64')}`);
const config={name:'Model',description:'',protocol:'chat_completions',base_url:'https://model.example/v1',model_id:'m'};
test('Model test uses cookie/CSRF, three protocol values and keeps omitted vs empty credentials distinct',async()=>{
 const calls=[];globalThis.fetch=async(url,init)=>{calls.push({url,init,body:JSON.parse(init.body)});return Response.json({receipt:'r',expires_at:'2030-01-01',text:{status:'passed'},image:{status:'unknown'}})};
 for(const protocol of ['chat_completions','openai_responses','anthropic_messages'])await api.test('w',{...config,protocol},{id:'p',version:7});
 await api.test('w',{...config,api_key:''},{id:'p',version:7});
 for(const c of calls){assert.equal(c.url,'/api/v1/workspaces/w/model-profiles/test');assert.equal(c.init.credentials,'include');assert.equal(c.init.headers.get('X-CSRF-Protection'),'1');assert.equal(c.init.headers.get('Authorization'),null);assert.equal(c.body.model_profile_id,'p');assert.equal(c.body.version,7);assert.ok(c.init.signal);}
 assert.equal('api_key' in calls[0].body,false);assert.equal(calls[3].body.api_key,'');assert.equal(calls[2].body.protocol,'anthropic_messages');
});
test('Model save is bound to receipt and resource version; actions send optimistic version',async()=>{
 const calls=[];globalThis.fetch=async(url,init)=>{calls.push({url,method:init.method,body:JSON.parse(init.body)});return Response.json({id:'p'})};
 await api.save('w',config,'receipt-1');await api.save('w',config,'receipt-2',{id:'p',version:4});
 await api.status('w',{id:'p',version:4,enabled:true});await api.setDefault('w',{id:'p',version:4});await api.revokeKey('w',{id:'p',version:4});
 assert.equal(calls[0].method,'POST');assert.equal(calls[1].method,'PATCH');assert.equal(calls[1].body.test_receipt,'receipt-2');assert.equal(calls[1].body.version,4);assert.equal('api_key' in calls[1].body,false);
 assert.deepEqual(calls[2].body,{version:4,enabled:false});assert.equal(calls[3].url.endsWith('/set-default'),true);assert.deepEqual(calls[4].body,{version:4});
});
test('Model errors do not become successful receipt; request ID survives',async()=>{
 globalThis.fetch=async()=>Response.json({error:{code:'test_receipt_expired',message:'Test expired',request_id:'r42'}},{status:409});
 await assert.rejects(api.save('w',config,'stale'),e=>e.code==='test_receipt_expired'&&e.message.includes('r42'));
});
test('Probe transport keeps 50-second budget rather than generic 20-second request timeout',async()=>{
 const original=AbortSignal.timeout;const budgets=[];
 AbortSignal.timeout=milliseconds=>{budgets.push(milliseconds);return original(milliseconds)};
 globalThis.fetch=async()=>Response.json({});
 try{await api.test('w',config);assert.deepEqual(budgets,[50000]);}finally{AbortSignal.timeout=original;}
});

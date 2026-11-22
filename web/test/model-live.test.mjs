// Opt-in only: an isolated local backend plus loopback mock model, never production.
import test from 'node:test';
import assert from 'node:assert/strict';
import esbuild from 'esbuild';
const origin=process.env.KIKO_MODEL_TEST_ORIGIN, modelURL=process.env.KIKO_MODEL_TEST_ENDPOINT;
test('real frontend model client: three protocols, receipts, editing and actions', {skip:!origin||!modelURL}, async()=>{
 assert.equal(new URL(origin).hostname,'127.0.0.1');assert.equal(new URL(modelURL).hostname,'127.0.0.1');assert.notEqual(new URL(origin).port,'18080');
 const code=esbuild.buildSync({stdin:{contents:"export {modelProfilesApi} from './src/direction2/api/models'; export {platformAuth,platformRequest} from './src/direction2/api/platform';",resolveDir:process.cwd(),loader:'ts'},bundle:true,write:false,platform:'node',format:'esm',define:{'import.meta.env.VITE_PLATFORM_REAL':'true'}}).outputFiles[0].text;
 const {modelProfilesApi:m,platformAuth:a}=await import(`data:text/javascript;base64,${Buffer.from(code).toString('base64')}`);
 const original=globalThis.fetch;let cookie='';
 globalThis.fetch=async(url,init={})=>{const headers=new Headers(init.headers);headers.set('Origin',origin);if(cookie)headers.set('Cookie',cookie);const response=await original(new URL(url,origin),{...init,headers});const value=response.headers.get('set-cookie');if(value)cookie=value.split(';')[0];return response;};
 try{
  await a.registerWithEmail({email:`model-ui-${Date.now()}@example.test`,name:'Model UI Test',password:'model-fixture-password',code:'',method:'password'});
  const workspace=(await a.getWorkspaces())[0].id;
  for(const protocol of ['chat_completions','openai_responses','anthropic_messages']){
   const config={name:`UI ${protocol}`,description:'isolated mock',protocol,base_url:modelURL,model_id:'mock',api_key:'fixture-only-not-a-secret'};
   const probe=await m.test(workspace,config);assert.equal(probe.text.status,'passed');assert.ok(probe.text.output);assert.ok(['passed','unknown','unsupported'].includes(probe.image.status));assert.ok(probe.receipt);
   let profile=await m.save(workspace,config,probe.receipt);assert.equal(profile.protocol,protocol);assert.equal(profile.has_key,true);
   await assert.rejects(m.save(workspace,{...config,model_id:'changed'},probe.receipt),e=>e.status===409||e.status===400);
   const edit={...config,name:`Edited ${protocol}`};delete edit.api_key;
   const retest=await m.test(workspace,edit,profile);profile=await m.save(workspace,edit,retest.receipt,profile);assert.equal(profile.name,edit.name);assert.equal(profile.has_key,true);
   profile=await m.status(workspace,profile);assert.equal(profile.enabled,false);profile=await m.status(workspace,profile);assert.equal(profile.enabled,true);
   profile=await m.setDefault(workspace,profile);assert.equal(profile.is_default,true);
   profile=await m.revokeKey(workspace,profile);assert.equal(profile.has_key,false);assert.equal(profile.validated,false);
  }
  assert.equal((await m.list(workspace)).items.length,3);await a.logout();
 }finally{globalThis.fetch=original;}
});

import test from 'node:test';import assert from 'node:assert/strict';import {build} from 'esbuild';
const built=await build({entryPoints:['src/direction2/api/workforce-channel-registration.ts'],bundle:true,write:false,format:'esm',platform:'node',define:{'import.meta.env':'{}'}});
const {registrationForWorkforce,createWorkforceRegistrationTransport}=await import(`data:text/javascript;base64,${Buffer.from(built.outputFiles[0].text).toString('base64')}`);
const value={id:'registration',state:'credentials_saved',credential_saved:true,expires_at:'2026-11-18T10:10:00Z',verification_url:'https://open.feishu.cn/test',error_code:''};
test('Feishu failures preserve the provider detail and the actual retry policy', () => {
  for (const [code, detail, eligible] of [
    ['feishu_authentication_failed', '21001 Access denied due to platform policy.', false],
    ['feishu_directory_check_unavailable', 'directory_scope_undisclosed', true],
    ['feishu_authentication_failed', '', false]
  ]) {
    const session = registrationForWorkforce('w', 'a', 'feishu', {...value, state:'needs_attention', error_code:code,
      auto_retry_blocked:!eligible, auto_check:{eligible}, diagnostics:{error_code:code, error_detail:detail}});
    assert.equal(session.feishuFailure.detail, detail || code);
    assert.equal(session.feishuFailure.automaticRetry, eligible);
    assert.equal(session.feishuFailure.canReauthorize, true);
  }
});
test('stored credentials do not imply connected or permissions ready',()=>{const r=registrationForWorkforce('w','a','feishu',value,undefined,Date.parse('2026-11-18T10:00:00Z'));assert.equal(r.connectionState,'pending');assert.equal(r.permissionReady,undefined);assert.equal(r.status,'scanned_confirming');assert.equal(r.expiresInSeconds,600);assert.equal(r.verificationUrl,'');});
test('only observed connection and diagnostics are ready',()=>{const r=registrationForWorkforce('w','a','feishu',{...value,state:'connected',diagnostics:{ready:true}},{status:'connected',connectionState:'connected'});assert.equal(r.connectionState,'connected');assert.equal(r.permissionReady,true);assert.equal(r.status,'authorized');});
test('DingTalk consumed binding is not fabricated as a verified Stream connection',()=>{const r=registrationForWorkforce('w','a','dingtalk',{...value,state:'consumed',verification_url:'https://open-dev.dingtalk.com/test'},{status:'pending_config',connectionState:'disconnected'});assert.equal(r.status,'authorized');assert.equal(r.connectionState,'pending');assert.equal(r.permissionReady,undefined);});
test('untrusted URL is never rendered as authorization QR',()=>{for(const url of ['https://evil.example/','javascript:alert(1)','https://open.feishu.cn.evil.example/']) assert.equal(registrationForWorkforce('w','a','feishu',{...value,state:'awaiting_confirmation',credential_saved:false,verification_url:url}).verificationUrl,'');});

test('Feishu denial ends the previous registration while an in-flight poll keeps its QR', () => {
  const registration = {...value, credential_saved: false};
  const polling = registrationForWorkforce('w', 'a', 'feishu', {...registration, state: 'polling'});
  assert.equal(polling.status, 'waiting_scan');
  assert.equal(polling.verificationUrl, value.verification_url);
  const denied = registrationForWorkforce('w', 'a', 'feishu', {...registration, state: 'denied'});
  assert.equal(denied.status, 'expired');
  assert.equal(denied.verificationUrl, '');
});

test('An interrupted registration is replaceable at its deadline, while saved credentials can still be checked', () => {
  const expiresAt = Date.parse(value.expires_at);
  for (const provider of ['feishu', 'dingtalk']) {
    const interrupted = {...value, state: 'interrupted', credential_saved: false};
    const before = registrationForWorkforce('w', 'a', provider, interrupted, undefined, expiresAt - 1000);
    assert.equal(before.registrationState, 'interrupted');
    assert.equal(before.restartNeedsReview, true);
    const expired = registrationForWorkforce('w', 'a', provider, interrupted, undefined, expiresAt);
    assert.equal(expired.registrationState, 'expired');
    assert.equal(expired.restartNeedsReview, false);
  }
  const saved = registrationForWorkforce('w', 'a', 'feishu', value, undefined, expiresAt);
  assert.equal(saved.status, 'scanned_confirming');
  assert.equal(saved.credentialSaved, true);
  const dingExpired = registrationForWorkforce('w', 'a', 'dingtalk', {
    ...value, state: 'expired', app_id: 'ding_old', credential_saved: false,
  }, undefined, expiresAt);
  assert.equal(dingExpired.restartNeedsReview, false);
});

const memory=new Map();globalThis.sessionStorage={getItem:k=>memory.get(k)||null,setItem:(k,v)=>memory.set(k,v)};
test('Feishu creation uses the server default for the contact range', async () => {
  const previous = globalThis.fetch;
  const requests = [];
  globalThis.fetch = async (url, init) => {
    requests.push({url, method: init.method, body: JSON.parse(init.body)});
    return Response.json({registration: {...value, state: 'awaiting_confirmation', credential_saved: false}});
  };
  try {
    const port = createWorkforceRegistrationTransport({workspace: () => 'w', binding: async () => undefined});
    const session = await port.startChannelQrRegistration('a', 'feishu');
    assert.equal(session.status, 'waiting_scan');
    assert.deepEqual(requests, [{url: '/api/v1/workspaces/w/agents/a/channels/feishu/registration', method: 'POST', body: {}}]);
  } finally {
    globalThis.fetch = previous;
  }
});

test('Feishu reauthorization targets the displayed failure and replays the same key on response loss', async () => {
  const previous = globalThis.fetch;
  const requests = [];
  globalThis.fetch = async (_url, init) => {
    if (init.method !== 'POST') return Response.json({registration:{...value, state:'needs_attention'}});
    requests.push({body:JSON.parse(init.body), key:init.headers.get('Idempotency-Key')});
    if (requests.length === 1) throw new Error('response lost');
    return Response.json({registration:{...value, id:'new', state:'awaiting_confirmation', credential_saved:false}});
  };
  try {
    const port = createWorkforceRegistrationTransport({workspace:()=> 'w', binding:async()=>undefined});
    await port.getLatestChannelQrRegistration('a', 'feishu');
    await assert.rejects(port.startChannelQrRegistration('other', 'feishu', 'w', value.id));
    await assert.rejects(port.startChannelQrRegistration('a', 'feishu', 'w', value.id));
    const replacement = await port.startChannelQrRegistration('a', 'feishu', 'w', value.id);
    assert.equal(replacement.sessionId, 'new');
    assert.deepEqual(requests[0].body, {replace_registration_id:value.id});
    assert.ok(requests[0].key);
    assert.deepEqual(requests, [requests[0], requests[0]]);
  } finally { globalThis.fetch = previous; }
});

test('lost create result reuses idempotency key, not a second application request identity',async()=>{
 const requests=[];const previous=globalThis.fetch;
 globalThis.fetch=async(url,init)=>{requests.push(init);throw new Error('response lost');};
 try{const port=createWorkforceRegistrationTransport({workspace:()=> 'w',binding:async()=>undefined});
  await assert.rejects(port.startChannelQrRegistration('a','dingtalk'));
  await assert.rejects(port.startChannelQrRegistration('a','dingtalk'));
  assert.equal(requests[0].headers.get('Idempotency-Key'),requests[1].headers.get('Idempotency-Key'));
 }finally{globalThis.fetch=previous;}
});
test('DingTalk confirmation loss never automatically confirms twice and explicit retry checks durable state first',async()=>{
 const previous=globalThis.fetch;const calls=[];let saved=false;
 const registration={...value,id:'ding-loss',state:'ready',verification_url:'https://open-dev.dingtalk.com/test'};
 globalThis.fetch=async(url,init)=>{calls.push([url,init.method||'GET']);
  if(url.endsWith('/confirm')){saved=true;throw new Error('response lost');}
  return new Response(JSON.stringify({registration:{...registration,state:saved?'consumed':'ready'}}),{status:200,headers:{'Content-Type':'application/json'}});
 };
 try{const port=createWorkforceRegistrationTransport({workspace:()=> 'w',binding:async()=>({status:'pending_config',connectionState:'disconnected'})});
  await port.getLatestChannelQrRegistration('a','dingtalk');
  await assert.rejects(port.pollChannelQrRegistration('ding-loss'));
  const resolved=await port.confirmChannelQrRegistration('ding-loss');
  assert.equal(resolved.status,'authorized');assert.equal(resolved.connectionState,'pending');
  assert.equal(calls.filter(([url])=>url.endsWith('/confirm')).length,1);
  assert.equal(calls.at(-1)[1],'GET');
 }finally{globalThis.fetch=previous;}
});

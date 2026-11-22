import test from 'node:test';
import assert from 'node:assert/strict';
import esbuild from 'esbuild';
const output = esbuild.buildSync({entryPoints:['src/direction2/api/platform.ts'],bundle:true,write:false,platform:'node',format:'esm',define:{'import.meta.env.VITE_PLATFORM_REAL':'true'}}).outputFiles[0].text;
const {platformAuth,platformRequest,validPlatformPassword,readInvitationToken} = await import(`data:text/javascript;base64,${Buffer.from(output).toString('base64')}`);
test('Platform email challenge, session and cookie transport follow real contract without debug secrets', async () => {
  const requests=[];
  globalThis.fetch=async (url,init) => {
    requests.push({url,init,body:init.body ? JSON.parse(init.body) : null});
    if(url.endsWith('/email-codes')) return Response.json({challenge_id:'challenge-A',expires_at:new Date(Date.now()+300000).toISOString(),resend_after:60,debugCode:'MUST_NOT_EXPOSE'});
    return Response.json({user:{id:'u',name:'User',email:'user@example.test'}});
  };
  const receipt = await platformAuth.sendEmailCode({email:'user@example.test',purpose:'login'});
  assert.equal(receipt.debugCode,undefined);
  const session = await platformAuth.loginWithEmailCode({email:'user@example.test',code:'123456'});
  assert.deepEqual(session.workspaces,[]);
  assert.equal(requests[1].body.challenge_id,'challenge-A');
  assert.equal(requests[1].body.method,'code');
  assert.equal(requests[1].init.credentials,'include');
  assert.equal(requests[1].init.headers.get('X-CSRF-Protection'),'1');
  assert.equal(requests[1].init.headers.get('Authorization'),null);
});
test('Platform registration does not invent workspace or leak invitation into account creation',async()=>{
  let body;
  globalThis.fetch=async (_url,init)=>{body=JSON.parse(init.body);return Response.json({user:{id:'u',name:'User',email:'user@example.test'}})};
  await platformAuth.registerWithEmail({email:'user@example.test',name:'User',code:'123456',password:'safe-test-password'});
  assert.equal(body.invitation_token,undefined);
  assert.equal(body.workspace_name,undefined);
});
test('Platform errors preserve request identifiers and logout handles 204', async()=>{
  globalThis.fetch=async()=>Response.json({error:{code:'version_conflict',message:'版本已更新',request_id:'request-7'}},{status:409});
  await assert.rejects(platformRequest('/workspaces/a'),error=>error.status===409&&error.code==='version_conflict'&&error.message.includes('request-7'));
  globalThis.fetch=async()=>new Response(null,{status:204});
  assert.deepEqual(await platformAuth.logout(),{ok:true});
});

test('Platform password uses backend UTF-8 byte bounds',()=>{
 assert.equal(validPlatformPassword('123456789'),false);
 assert.equal(validPlatformPassword('1234567890'),true);
 assert.equal(validPlatformPassword('密'.repeat(24)),true);
 assert.equal(validPlatformPassword('密'.repeat(25)),false);
});

test('Password registration requires no email challenge and preserves unverified status',async()=>{
 const requests=[];
 globalThis.fetch=async(url,init)=>{requests.push({url,body:init.body?JSON.parse(init.body):null});
   if(url.endsWith('/auth/options')) return Response.json({password_registration_enabled:true,email_code_enabled:false,registration_verification_required:false});
   return Response.json({user:{id:'u',name:'User',email:'user@example.test',email_verified:false}});
 };
 assert.deepEqual(await platformAuth.getConfig(),{password_registration_enabled:true,email_code_enabled:false,registration_verification_required:false});
 const session=await platformAuth.registerWithEmail({email:'user@example.test',name:'User',password:'test-password-only',code:'stale-code',method:'password'});
 assert.deepEqual(requests[1].body,{email:'user@example.test',name:'User',password:'test-password-only',method:'password'});
 assert.equal(session.user.emailVerified,false);
 assert.deepEqual(session.workspaces,[]);
});
test('Verified status comes from server and verification-required error is actionable',async()=>{
 globalThis.fetch=async()=>Response.json({user:{id:'u',name:'User',email:'user@example.test',email_verified:true}});
 assert.equal((await platformAuth.getMe()).emailVerified,true);
 globalThis.fetch=async()=>Response.json({error:{code:'email_verification_required',message:'verification required'}},{status:403});
 await assert.rejects(platformRequest('/invitations/token/accept'),e=>e.code==='email_verification_required'&&e.message.includes('重置密码'));
});

test('Invitation token is consumed from URL without persistent storage',()=>{
 const oldLocation=globalThis.location, oldHistory=globalThis.history;
 let replaced;
 globalThis.location={href:'https://local.example/?invitation_token=test-secret&keep=1#settings'};
 globalThis.history={replaceState:(_s,_t,url)=>{replaced=String(url)}};
 try { assert.equal(readInvitationToken(),'test-secret');assert.equal(replaced,'https://local.example/?keep=1#settings'); }
 finally {globalThis.location=oldLocation;globalThis.history=oldHistory;}
});

// Opt-in: isolated local backend fixture + fake mail sink, never production SMTP.
import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
import path from 'node:path';
import { createHash } from 'node:crypto';
import esbuild from 'esbuild';
const origin=process.env.KIKO_UI_TEST_ORIGIN, mailDir=process.env.KIKO_UI_TEST_MAIL_DIR;
test('real frontend client: OTP, workspace and invitation lifecycle', {skip:!origin||!mailDir}, async()=>{
 assert.equal(new URL(origin).hostname,'127.0.0.1');
 const code=esbuild.buildSync({entryPoints:['src/direction2/api/platform.ts'],bundle:true,write:false,platform:'node',format:'esm',define:{'import.meta.env.VITE_PLATFORM_REAL':'true'}}).outputFiles[0].text;
 const {platformAuth:a,platformRequest:r,post}=await import(`data:text/javascript;base64,${Buffer.from(code).toString('base64')}`);
 const original=globalThis.fetch;let cookie='';
 globalThis.fetch=async(url,init={})=>{const headers=new Headers(init.headers);headers.set('Origin',origin);if(cookie)headers.set('Cookie',cookie);const response=await original(new URL(url,origin),{...init,headers});const value=response.headers.get('set-cookie');if(value)cookie=value.split(';')[0];return response;};
 const read=async email=>(await fs.readFile(path.join(mailDir,createHash('sha256').update(email).digest('hex')+'.code'),'utf8')).trim();
 async function register(email,name){const receipt=await a.sendEmailCode({email,purpose:'register'});assert.equal(receipt.debugCode,undefined);return a.registerWithEmail({email,name,code:await read(email),password:''});}
 try{
  const id=Date.now(), owner=`web-owner-${id}@example.test`, member=`web-member-${id}@example.test`;
  assert.equal((await register(owner,'Web Owner')).user.email,owner);assert.equal((await a.getWorkspaces()).length,1);
  const ws=await r('/workspaces',post({name:'Frontend Integration',description:'isolated fixture'}));
  assert.ok((await a.getWorkspaces()).some(w=>w.id===ws.id));
  const updated=await r(`/workspaces/${ws.id}`,{method:'PATCH',body:JSON.stringify({name:'Frontend Updated',version:ws.version})});assert.equal(updated.name,'Frontend Updated');
  const invite=await r(`/workspaces/${ws.id}/invitations`,post({email:member,role:'member'}));const token=new URL(invite.link).searchParams.get('invitation_token');assert.ok(token);
  assert.equal((await r(`/workspaces/${ws.id}/members`)).items.length,1);assert.equal((await r(`/invitations/${token}`)).status,'valid');
  const revoked=await r(`/workspaces/${ws.id}/invitations`,post({email:`revoked-${id}@example.test`,role:'member'}));await r(`/workspaces/${ws.id}/invitations/${revoked.id}/revoke`,post({}));
  const rev=await r(`/invitations/${new URL(revoked.link).searchParams.get('invitation_token')}`);assert.equal(rev.status,'revoked');assert.equal(rev.contact_email,owner);
  const unknown=await r('/invitations/not-a-valid-token');assert.equal(unknown.status,'unknown');assert.ok(!unknown.contact_email);
  await a.logout();await register(member,'Web Member');assert.equal((await a.getWorkspaces()).length,1);await r(`/invitations/${token}/accept`,post({}));assert.ok((await a.getWorkspaces()).some(w=>w.id===ws.id));
  await a.logout();await a.sendEmailCode({email:member,purpose:'login'});assert.equal((await a.loginWithEmailCode({email:member,code:await read(member)})).user.email,member);assert.equal((await a.getMe()).email,member);
  await a.logout();await assert.rejects(a.getMe(),error=>error.status===401);
 }finally{globalThis.fetch=original;}
});

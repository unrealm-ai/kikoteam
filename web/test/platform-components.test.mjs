import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
import {Window} from 'happy-dom';
import {parse,compileScript} from '@vue/compiler-sfc';
import esbuild from 'esbuild';
const window=new Window({url:'http://127.0.0.1:5173/'});
for(const name of ['window','document','navigator','HTMLElement','Element','SVGElement','Node','Text','Comment','Image','InputEvent','MouseEvent','KeyboardEvent','HTMLInputElement','Document','MutationObserver','ResizeObserver','getComputedStyle','requestAnimationFrame','cancelAnimationFrame']){
 if(name==='window')globalThis.window=window;else if(window[name]!==undefined)Object.defineProperty(globalThis,name,{configurable:true,writable:true,value:typeof window[name]==='function'&&['getComputedStyle','requestAnimationFrame','cancelAnimationFrame'].includes(name)?window[name].bind(window):window[name]});
}
const built=await esbuild.build({stdin:{contents:"export {default as SettingsView} from './src/direction2/views/SettingsView.vue'; export {default as AuthView} from './src/direction2/views/AuthView.vue'; export {default as InvitationView} from './src/direction2/views/InvitationView.vue'; export {createApp,h,nextTick} from 'vue'; export {NMessageProvider} from 'naive-ui';",resolveDir:process.cwd(),loader:'ts'},bundle:true,write:false,format:'esm',platform:'node',define:{'import.meta.env.VITE_PLATFORM_REAL':'true','import.meta.env.DEV':'false'},plugins:[{name:'vue-test',setup(build){build.onLoad({filter:/\.vue$/},async({path})=>{const source=await fs.readFile(path,'utf8');const {descriptor}=parse(source);return {contents:compileScript(descriptor,{id:path,inlineTemplate:true}).content,loader:'ts'};});}}]});
const {SettingsView,AuthView,InvitationView,createApp,h,NMessageProvider,nextTick}=await import(`data:text/javascript;base64,${Buffer.from(built.outputFiles[0].text).toString('base64')}`);
async function render(inspection){
 globalThis.fetch=async()=>Response.json(inspection);
 const host=document.createElement('div');document.body.appendChild(host);
 const app=createApp({render:()=>h(NMessageProvider,null,{default:()=>h(InvitationView,{token:'test-only',user:null})})});app.provide('d2DraftGuard',{register(){},unregister(){}});app.mount(host);
 await new Promise(resolve=>setTimeout(resolve,30));await nextTick();
 const text=host.textContent;app.unmount();host.remove();return text;
}
test('Invitation component: revoked link renders inviter contact, not a generic 404',async()=>{
 const text=await render({status:'revoked',contact_email:'inviter@example.test'});assert.match(text,/邀请链接已失效/);assert.match(text,/inviter@example.test/);assert.doesNotMatch(text,/404/);
});
test('Invitation component: unknown link does not invent contact email',async()=>{
 const text=await render({status:'unknown'});assert.match(text,/请联系邀请人获取新的邀请链接/);assert.doesNotMatch(text,/@/);
});
test('Invitation component: valid link requires login before acceptance',async()=>{
 const text=await render({status:'valid',workspace_name:'Test Team',email_masked:'m***@example.test'});assert.match(text,/Test Team/);assert.match(text,/登录或注册后加入/);
});
async function renderAuth(config,tab='register',extra={}) {
 globalThis.fetch=async()=>Response.json(config);
 const host=document.createElement('div');document.body.appendChild(host);
 const app=createApp({render:()=>h(NMessageProvider,null,{default:()=>h(AuthView,{initialTab:tab,...extra})})});app.provide('d2DraftGuard',{register(){},unregister(){}});app.mount(host);
 await new Promise(resolve=>setTimeout(resolve,40));await nextTick();
 const text=host.textContent;app.unmount();host.remove();return text;
}
test('Password-only registration shows required password and no code request',async()=>{
 const text=await renderAuth({password_registration_enabled:true,email_code_enabled:false,registration_verification_required:false});
 assert.match(text,/设置密码/);assert.doesNotMatch(text,/设置密码（可选）|发送验证码|邮箱验证码/);
});
test('SMTP mode retains code registration, password login is default',async()=>{
 const text=await renderAuth({password_registration_enabled:false,email_code_enabled:true,registration_verification_required:true});
 assert.match(text,/邮箱验证码/);assert.match(text,/设置密码（可选）/);
 const login=await renderAuth({password_registration_enabled:true,email_code_enabled:false,registration_verification_required:false},'login');
 assert.match(login,/登录密码/);assert.doesNotMatch(login,/使用邮箱验证码登录|忘记密码？/);
 assert.match(login,/KIKO\s*企业级AI员工云平台/);
});
test('Invalid and overlong email blocks registration, password login and code delivery requests',async()=>{
 for (const tab of ['login','register']) {
  for (const email of ['not-an-email', `${'x'.repeat(250)}@example.test`]) {
   const requests=[];
   globalThis.fetch=async(url)=>{requests.push(url);return Response.json({password_registration_enabled:true,email_code_enabled:true,registration_verification_required:false});};
   const host=document.createElement('div');document.body.appendChild(host);
   const app=createApp({render:()=>h(NMessageProvider,null,{default:()=>h(AuthView,{initialTab:tab})})});app.provide('d2DraftGuard',{register(){},unregister(){}});app.mount(host);
   await new Promise(resolve=>setTimeout(resolve,40));await nextTick();
   const form=host.querySelector('form');assert.ok(form);
   for(const input of form.querySelectorAll('input')) {
    input.value=input.type==='email'?email:input.type==='password'?'Password12345':'Test User';
    input.dispatchEvent(new window.Event('input',{bubbles:true}));
   }
   await nextTick();form.dispatchEvent(new window.Event('submit',{bubbles:true,cancelable:true}));
   await new Promise(resolve=>setTimeout(resolve,40));await nextTick();
   assert.deepEqual(requests,['/api/v1/auth/options']);
   assert.match(host.textContent,/格式正确的邮箱/);
   if(tab==='login') {
    [...host.querySelectorAll('button')].find(b=>b.textContent.includes('使用邮箱验证码登录')).click();
    await nextTick();
    const input=host.querySelector('input[type=email]');input.value=email;input.dispatchEvent(new window.Event('input',{bubbles:true}));await nextTick();
    const send=[...host.querySelectorAll('button')].find(b=>b.textContent.includes('发送验证码'));assert.ok(send);send.click();
    await new Promise(resolve=>setTimeout(resolve,30));
    assert.deepEqual(requests,['/api/v1/auth/options']);
    assert.match(host.textContent,/请先输入格式正确的邮箱地址/);
   }
   app.unmount();host.remove();
  }
 }
});
test('Reusable link auto-accepts logged-in unverified user and selects accepted workspace',async()=>{
 const calls=[],completed=[];
 globalThis.fetch=async(url)=>{calls.push(url);return Response.json(url.endsWith('/accept')?{workspace_id:'joined-space',role:'member'}:{status:'valid',kind:'link',workspace_name:'Team'});};
 const host=document.createElement('div');document.body.appendChild(host);
 const app=createApp({render:()=>h(NMessageProvider,null,{default:()=>h(InvitationView,{token:'test-token',user:{id:'u',email:'u@example.test',emailVerified:false},onCompleted:id=>completed.push(id)})})});app.provide('d2DraftGuard',{register(){},unregister(){}});app.mount(host);
 await new Promise(r=>setTimeout(r,60));await nextTick();
 assert.equal(calls.filter(url=>url.endsWith('/accept')).length,1);assert.deepEqual(completed,['joined-space']);
 app.unmount();host.remove();
});
test('Invitation acceptance failure retains signed-in state and personal workspace exit',async()=>{
 const completed=[];
 globalThis.fetch=async(url)=>url.endsWith('/accept')?Response.json({error:{code:'invitation_expired',message:'邀请已过期'}},{status:410}):Response.json({status:'valid',kind:'link',workspace_name:'Team'});
 const host=document.createElement('div');document.body.appendChild(host);
 const app=createApp({render:()=>h(NMessageProvider,null,{default:()=>h(InvitationView,{token:'test-token',user:{id:'u',email:'u@example.test'},onCompleted:id=>completed.push(id)})})});app.provide('d2DraftGuard',{register(){},unregister(){}});app.mount(host);
 await new Promise(r=>setTimeout(r,60));await nextTick();
 assert.match(host.textContent,/账号已登录/);assert.match(host.textContent,/邀请已过期/);assert.match(host.textContent,/进入自己的工作区/);assert.deepEqual(completed,[]);
 assert.doesNotMatch(host.textContent,/注册失败/);app.unmount();host.remove();
});
test('Workspace invite defaults to reusable member link for seven days without email',async()=>{
 const writes=[];
 globalThis.fetch=async(url,init)=>{
  if(init.method==='POST'){writes.push(JSON.parse(init.body));return Response.json({id:'i',kind:'link',link:'https://example.test/?invitation_token=test-only'});}
  if(url.includes('/members?')||url.includes('/invitations?'))return Response.json({items:[],total:0});
  return Response.json({id:'w',name:'Workspace',role:'owner',version:1});
 };
 const host=document.createElement('div');document.body.appendChild(host);
 const app=createApp({render:()=>h(NMessageProvider,null,{default:()=>h(SettingsView,{workspaceId:'w',user:{id:'u'},initialTab:'members'})})});app.provide('d2DraftGuard',{register(){},unregister(){}});app.mount(host);
 await new Promise(r=>setTimeout(r,50));await nextTick();
 [...host.querySelectorAll('button')].find(b=>b.textContent.includes('邀请成员')).click();await new Promise(r=>setTimeout(r,40));await nextTick();
 const generate=[...document.querySelectorAll('button')].find(b=>b.textContent.includes('生成邀请链接'));assert.ok(generate);generate.click();
 await new Promise(r=>setTimeout(r,40));
 assert.deepEqual(writes,[{kind:'link',role:'member',expires_in_days:7}]);
 app.unmount();host.remove();
});
test('Email invite option follows mail capability and fails closed; historical email stays visible',async()=>{
 for(const capability of [false,true,'error']) {
  globalThis.fetch=async(url)=>{
   if(url.endsWith('/auth/options')) return capability==='error'?Response.json({error:{code:'unavailable',message:'unavailable'}},{status:503}):Response.json({email_code_enabled:capability});
   if(url.includes('/invitations?'))return Response.json({items:[{id:'old',kind:'email',email:'history@example.test',delivery_status:'failed',status:'pending',role:'member',expires_at:'2030-01-01'}],total:1});
   if(url.includes('/members?'))return Response.json({items:[],total:0});
   return Response.json({id:'w',name:'Workspace',role:'owner',version:1});
  };
  const host=document.createElement('div');document.body.appendChild(host);
  const app=createApp({render:()=>h(NMessageProvider,null,{default:()=>h(SettingsView,{workspaceId:'w',user:{id:'u'},initialTab:'members'})})});app.provide('d2DraftGuard',{register(){},unregister(){}});app.mount(host);
  await new Promise(r=>setTimeout(r,50));await nextTick();
  assert.match(host.textContent,/history@example.test/);assert.match(host.textContent,/撤销/);assert.match(host.textContent,/发送失败/);
  if(capability===true)assert.match(host.textContent,/重试发送/);else {assert.doesNotMatch(host.textContent,/重试发送/);assert.match(host.textContent,/邮件未启用/);}
  [...host.querySelectorAll('button')].find(b=>b.textContent.includes('邀请成员')).click();await new Promise(r=>setTimeout(r,40));await nextTick();
  const modal=document.querySelector('.n-modal');
  assert.match(modal.textContent,/通用链接/);
  assert.equal(!!modal.querySelector('textarea'),capability===true);
  if (capability===true) assert.match(modal.textContent,/受邀邮箱/);
  app.unmount();host.remove();await new Promise(r=>setTimeout(r,30));
 }
});
test('Email invitation safely resets password then logs in before retrying same token',async()=>{
 const calls=[],completed=[];let verified=false,invalidated=0;
 globalThis.fetch=async(url,init)=>{calls.push({url,body:init.body?JSON.parse(init.body):null});
  if(url.endsWith('/auth/options'))return Response.json({password_registration_enabled:true,email_code_enabled:true,registration_verification_required:false});
  if(url.endsWith('/email-codes'))return Response.json({challenge_id:'reset-challenge',expires_at:new Date(Date.now()+300000).toISOString(),resend_after:60});
  if(url.endsWith('/password-reset')){verified=true;return new Response(null,{status:204});}
  if(url.endsWith('/auth/sessions'))return Response.json({user:{id:'u',email:'u@example.test',name:'User',email_verified:true}});
  if(url.endsWith('/accept'))return verified?Response.json({workspace_id:'joined'}):Response.json({error:{code:'email_verification_required',message:'verify'}},{status:403});
  return Response.json({status:'valid',kind:'email',email_masked:'u***@example.test'});
 };
 const host=document.createElement('div');document.body.appendChild(host);
 const app=createApp({render:()=>h(NMessageProvider,null,{default:()=>h(InvitationView,{token:'kept-token',user:{id:'u',email:'u@example.test',emailVerified:false},onSessionInvalidated:()=>invalidated++,onCompleted:id=>completed.push(id)})})});app.mount(host);
 const tick=async()=>{await new Promise(r=>setTimeout(r,50));await nextTick();};await tick();
 [...host.querySelectorAll('button')].find(b=>b.textContent==='验证邮箱').click();await tick();
 assert.equal(host.querySelector('input[type=email]').value,'u@example.test');
 [...host.querySelectorAll('button')].find(b=>b.textContent.includes('发送验证码')).click();await tick();
 for(const input of host.querySelectorAll('input')) {if(input.type==='email')continue; input.value=input.type==='password'?'NewPassword12345':'123456';input.dispatchEvent(new window.Event('input',{bubbles:true}));}
 await nextTick();host.querySelector('form').dispatchEvent(new window.Event('submit',{bubbles:true,cancelable:true}));await tick();
 assert.equal(invalidated,1);assert.match(host.textContent,/密码重置成功/);assert.equal(calls.filter(c=>c.url.endsWith('/accept')).length,1);
 const password=host.querySelector('input[type=password]');password.value='NewPassword12345';password.dispatchEvent(new window.Event('input',{bubbles:true}));await nextTick();
 host.querySelector('form').dispatchEvent(new window.Event('submit',{bubbles:true,cancelable:true}));await tick();
 assert.deepEqual(completed,['joined']);assert.equal(calls.filter(c=>c.url==='/api/v1/invitations/kept-token/accept').length,2);
 app.unmount();host.remove();
});
test('Email invitation registration prefers OTP when mail is available without changing global policy',async()=>{
 const text=await renderAuth({password_registration_enabled:true,email_code_enabled:true,registration_verification_required:false},'register',{preferCodeRegistration:true});
 assert.match(text,/邮箱验证码/);assert.match(text,/设置密码（可选）/);
});
test('Unknown delivery warns about duplicate email and retries same invitation explicitly',async()=>{
 const writes=[];
 globalThis.fetch=async(url,init)=>{
  if(init.method==='POST'){writes.push({url,body:JSON.parse(init.body)});return Response.json({id:'i',kind:'email',delivery_status:'pending'});}
  if(url.endsWith('/auth/options'))return Response.json({email_code_enabled:true});
  if(url.includes('/members?'))return Response.json({items:[],total:0});
  if(url.includes('/invitations?'))return Response.json({items:[{id:'i',kind:'email',email:'u@example.test',role:'member',status:'pending',expires_at:'2030-01-01',delivery_status:'unknown'}],total:1});
  return Response.json({id:'w',name:'Workspace',role:'owner',version:1});
 };
 const host=document.createElement('div');document.body.appendChild(host);
 const app=createApp({render:()=>h(NMessageProvider,null,{default:()=>h(SettingsView,{workspaceId:'w',user:{id:'u'},initialTab:'members'})})});app.provide('d2DraftGuard',{register(){},unregister(){}});app.mount(host);
 await new Promise(r=>setTimeout(r,50));await nextTick();
 [...host.querySelectorAll('button')].find(b=>b.textContent==='重试发送').click();await new Promise(r=>setTimeout(r,40));await nextTick();
 assert.match(document.body.textContent,/可能产生重复邮件/);assert.equal(writes.length,0);
 [...document.querySelectorAll('button')].find(b=>b.textContent==='确认重试').click();await new Promise(r=>setTimeout(r,40));
 assert.deepEqual(writes,[{url:'/api/v1/workspaces/w/invitations/i/retry-delivery',body:{acknowledge_possible_duplicate:true}}]);
 app.unmount();host.remove();
});
test('Original settings has two tabs and preserves the draft on a version conflict', async()=>{
 const writes=[],names=[]; let cachedVersion=1;
 globalThis.fetch=async(url,init)=>{
   if(init.method==='PATCH'){
     writes.push(JSON.parse(init.body));
     if(writes.length===1){cachedVersion=2;return Response.json({error:{code:'version_conflict',message:'配置已更新'}},{status:409});}
     return Response.json({id:'w',name:'我编辑的名称',description:'',role:'owner',version:3});
   }
   assert.equal(url,'/api/v1/workspaces/w');
   return Response.json({id:'w',name:cachedVersion===1?'原名称':'并发编辑',description:'',role:'owner',version:cachedVersion});
 };
 const host=document.createElement('div');document.body.appendChild(host);
 const app=createApp({render:()=>h(NMessageProvider,null,{default:()=>h(SettingsView,{workspaceId:'w',user:{id:'owner'},onRenamed:v=>names.push(v.name)})})});
 let guard;app.provide('d2DraftGuard',{register(v){guard=v},unregister(){}});app.mount(host);
 await new Promise(r=>setTimeout(r,60));
 assert.doesNotMatch(host.textContent,/系统状态监控|全局抄送通知群/);
 const name=host.querySelector('input[placeholder="输入工作区名称"]');
 name.value='我编辑的名称';name.dispatchEvent(new window.Event('input',{bubbles:true}));await nextTick();
 assert.ok(guard.hasUnsaved());assert.equal(await guard.save(),false);await nextTick();
 assert.equal(name.value,'我编辑的名称');assert.match(host.textContent,/并发编辑/);
 assert.equal(await guard.save(),true);assert.deepEqual(writes.map(w=>w.version),[1,2]);assert.deepEqual(names,['我编辑的名称']);
 app.unmount();host.remove();
});

test('Optional recipient field sends one shared invitation and retains its link when the list refresh fails',async()=>{
 const writes=[];
 globalThis.fetch=async(url,init)=>{
   if(init.method==='POST'){writes.push(JSON.parse(init.body));return Response.json({id:'i',kind:'email',delivery_status:'pending',link:'https://example.test/shared'});}
   if(url.endsWith('/auth/options'))return Response.json({email_code_enabled:true});
   if(url.includes('/invitations?') && writes.length)return Response.json({error:{message:'刷新失败'}},{status:503});
   if(url.includes('/members?') || url.includes('/invitations?'))return Response.json({items:[],total:0});
   return Response.json({id:'w',name:'Team',role:'owner',version:1});
 };
 const host=document.createElement('div');document.body.appendChild(host);
 const app=createApp({render:()=>h(NMessageProvider,null,{default:()=>h(SettingsView,{workspaceId:'w',user:{id:'o'},initialTab:'members'})})});
 app.provide('d2DraftGuard',{register(){},unregister(){}});app.mount(host);
 const tick=()=>new Promise(r=>setTimeout(r,60));await tick();
 [...host.querySelectorAll('button')].find(b=>b.textContent.includes('邀请成员')).click();await tick();
 const input=document.querySelector('.n-modal textarea');input.value='Alice@example.test，bob@example.test\n alice@example.test';
 input.dispatchEvent(new window.Event('input',{bubbles:true}));await tick();
 [...document.querySelectorAll('button')].find(b=>b.textContent.includes('生成邀请链接')).click();await tick();
 assert.deepEqual(writes,[{kind:'email',role:'member',emails:['alice@example.test','bob@example.test'],expires_in_days:7}]);
 assert.equal(document.querySelector('.n-modal input[readonly]').value,'https://example.test/shared');
 assert.match(host.textContent,/刷新失败/);assert.doesNotMatch(host.textContent,/alice@example.test/);
 app.unmount();host.remove();
});


test('Member controls follow actor role and combined changes use one versioned PATCH',async()=>{
 const records=[
   {id:'owner',role:'owner',name:'Owner'}, {id:'admin',role:'admin',name:'Admin'}, {id:'member',role:'member',name:'Member'}
 ].map(row=>({...row,email:row.id+'@example.test',status:'active',version:4,joined_at:'2026-11-19T12:00:00Z'}));
 const tick=()=>new Promise(r=>setTimeout(r,50));
 for(const role of ['member','admin','owner']){
   const writes=[];
   globalThis.fetch=async(url,init)=>{
     if(init.method==='PATCH'){
       const changes=JSON.parse(init.body);writes.push(changes);
       return Response.json({...records[2],...changes,version:5});
     }
     if(url.includes('/members?'))return Response.json({items:records,total:records.length});
     if(url.includes('/invitations?')){assert.notEqual(role,'member');return Response.json({items:[],total:0});}
     if(url.endsWith('/auth/options'))return Response.json({email_code_enabled:false});
     return Response.json({id:'w',name:'Team',role,version:1});
   };
   const host=document.createElement('div');document.body.appendChild(host);
   const app=createApp({render:()=>h(NMessageProvider,null,{default:()=>h(SettingsView,{workspaceId:'w',user:{id:role},initialTab:'members'})})});
   app.provide('d2DraftGuard',{register(){},unregister(){}});app.mount(host);await tick();
   const rows=[...host.querySelector('table').querySelectorAll('tbody tr')];
   assert.equal(rows[0].querySelector('button'),null);
   assert.equal(!!rows[1].querySelector('button'),role==='owner');
   assert.equal(!!rows[2].querySelector('button'),role!=='member');
   if(role==='owner'){
     rows[2].querySelector('button').click();await tick();
     document.querySelector('.n-modal .n-base-selection').dispatchEvent(new window.MouseEvent('click',{bubbles:true}));await tick();
     [...document.querySelectorAll('.n-base-select-option')].find(el=>el.textContent.includes('管理员')).click();await tick();
     [...document.querySelectorAll('.n-modal button')].find(el=>el.textContent.includes('确认执行')).click();await tick();
     assert.deepEqual(writes,[{version:4,role:'admin',status:'disabled'}]);
   }
   app.unmount();host.remove();await tick();
 }
});

test('Invitation link copy button selects modal input and falls back to execCommand when clipboard API rejects', async t => {
 const inviteUrl='https://example.test/?invitation_token=fallback-copy-test';
 const originalClipboard=Object.getOwnPropertyDescriptor(navigator,'clipboard');
 Object.defineProperty(navigator,'clipboard',{configurable:true,value:{writeText:async()=>{throw new Error('denied');}}});
 t.after(()=>{if(originalClipboard)Object.defineProperty(navigator,'clipboard',originalClipboard);else delete navigator.clipboard;});
 let copiedText='';
 const originalCommand=Object.getOwnPropertyDescriptor(document,'execCommand');
 Object.defineProperty(document,'execCommand',{configurable:true,value:command=>{
  assert.equal(command,'copy');
  const active=document.activeElement;
  assert.ok(active instanceof HTMLInputElement);
  copiedText=active.value.slice(active.selectionStart,active.selectionEnd);
  return true;
 }});
 t.after(()=>{if(originalCommand)Object.defineProperty(document,'execCommand',originalCommand);else delete document.execCommand;});
 globalThis.fetch=async(url,init)=>{
  if(init.method==='POST')return Response.json({id:'i',kind:'link',link:inviteUrl});
  if(url.includes('/members?')||url.includes('/invitations?'))return Response.json({items:[],total:0});
  return Response.json({id:'w',name:'Workspace',role:'owner',version:1});
 };
 const host=document.createElement('div');document.body.appendChild(host);
 const app=createApp({render:()=>h(NMessageProvider,null,{default:()=>h(SettingsView,{workspaceId:'w',user:{id:'u'},initialTab:'members'})})});
 app.provide('d2DraftGuard',{register(){},unregister(){}});app.mount(host);
 try{
  await new Promise(r=>setTimeout(r,50));await nextTick();
  [...host.querySelectorAll('button')].find(b=>b.textContent.includes('邀请成员')).click();
  await new Promise(r=>setTimeout(r,40));await nextTick();
  [...document.querySelectorAll('button')].find(b=>b.textContent.includes('生成邀请链接')).click();
  await new Promise(r=>setTimeout(r,40));await nextTick();
  const readonlyInput=document.querySelector('.n-modal input[readonly]');
  assert.ok(readonlyInput);
  assert.equal(readonlyInput.value,inviteUrl);
  const copyBtn=[...document.querySelectorAll('.n-modal button')].find(b=>b.textContent.trim()==='复制链接');
  assert.ok(copyBtn);
  copyBtn.click();
  await new Promise(r=>setTimeout(r,40));await nextTick();
  assert.equal(copiedText,inviteUrl);
  assert.equal(document.activeElement,readonlyInput);
  assert.equal(copyBtn.textContent.trim(),'已复制');
 }finally{app.unmount();host.remove();await new Promise(r=>setTimeout(r,30));}
});

test.after(()=>window.happyDOM.close());

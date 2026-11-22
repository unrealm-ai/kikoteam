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
const built=await esbuild.build({stdin:{contents:"export {default as PlatformModelsPanel} from './src/direction2/views/PlatformModelsPanel.vue'; export {activeWorkspaceId} from './src/direction2/api/client'; export {createApp,h,nextTick} from 'vue'; export {NMessageProvider} from 'naive-ui';",resolveDir:process.cwd(),loader:'ts'},bundle:true,write:false,format:'esm',platform:'node',define:{'import.meta.env.VITE_PLATFORM_REAL':'true','import.meta.env.DEV':'false'},plugins:[{name:'vue-test',setup(build){build.onLoad({filter:/\.vue$/},async({path})=>{const source=await fs.readFile(path,'utf8');const {descriptor}=parse(source);return {contents:compileScript(descriptor,{id:path,inlineTemplate:true}).content,loader:'ts'};});}}]});
const {PlatformModelsPanel,activeWorkspaceId,createApp,h,NMessageProvider,nextTick}=await import(`data:text/javascript;base64,${Buffer.from(built.outputFiles[0].text).toString('base64')}`);
const pause=async()=>{await new Promise(r=>setTimeout(r,40));await nextTick()};
const profile={id:'p',name:'Existing',description:'',protocol:'chat_completions',base_url:'https://model.example/v1',model_id:'m',version:3,has_key:true,vision_status:'unknown',enabled:true,is_default:false};
const result=(text='passed',image='unknown')=>({receipt:text==='passed'?'receipt':'',expires_at:new Date(Date.now()+60000).toISOString(),text:{status:text,output:'ACTUAL TEXT OUTPUT',duration_ms:4},image:{status:image,output:'ACTUAL IMAGE OUTPUT',issue:image==='unknown'?'timeout':'',duration_ms:8}});
const button=(text)=>[...document.querySelectorAll('button')].find(b=>b.textContent.trim()===text);
async function mount({role='owner',items=[profile],testResponse=result(),testHandler,saveHandler}={}){
 const calls=[];
 globalThis.fetch=async(url,init={})=>{
  calls.push({url,init,body:init.body?JSON.parse(init.body):null});
  if(url.endsWith('/test'))return testHandler?testHandler():Response.json(testResponse);
  if(init.method==='PATCH'||init.method==='POST')return saveHandler?saveHandler():Response.json(profile);
  if(url.includes('/model-profiles'))return Response.json({items,total:items.length,page:1,page_size:20});
  return Response.json({role});
 };
 activeWorkspaceId.value='w';
 const host=document.createElement('div');document.body.appendChild(host);
 const app=createApp({render:()=>h(NMessageProvider,null,{default:()=>h(PlatformModelsPanel)})});app.mount(host);await pause();
 return {calls,async cleanup(){app.unmount();host.remove();await pause();}};
}
async function edit(){button('编辑配置').click();await pause()}
test('Actual case outputs visible; unknown image permits saving after passed text',async()=>{
 const m=await mount();try{await edit();assert.ok(button('保存配置').disabled);button('测试连接').click();await pause();assert.match(document.body.textContent,/ACTUAL TEXT OUTPUT/);assert.match(document.body.textContent,/ACTUAL IMAGE OUTPUT/);assert.match(document.body.textContent,/图片测试 · 不确定/);assert.equal(button('保存配置').disabled,false);button('保存配置').click();await pause();const save=m.calls.find(c=>c.init.method==='PATCH');assert.ok(save);assert.equal(save.body.test_receipt,'receipt');assert.equal(save.body.version,3);assert.equal('api_key' in save.body,false);}finally{await m.cleanup()}
});
test('Failed text cannot save even if image passed; unsupported image does not block passed text',async()=>{
 for(const [text,image,disabled]of[['failed','passed',true],['passed','unsupported',false]]){const m=await mount({testResponse:result(text,image)});try{await edit();button('测试连接').click();await pause();assert.equal(button('保存配置').disabled,disabled);}finally{await m.cleanup()}}
});
test('Editing during in-flight probe ignores late success and cannot save stale config',async()=>{
 let finish;const pending=new Promise(r=>{finish=r});const m=await mount({testHandler:()=>pending});try{await edit();button('测试连接').click();await pause();const input=[...document.querySelectorAll('input')].find(i=>i.value==='m');input.value='changed';input.dispatchEvent(new window.Event('input',{bubbles:true}));await pause();finish(Response.json(result()));await pause();assert.ok(button('保存配置').disabled);assert.doesNotMatch(document.body.textContent,/ACTUAL TEXT OUTPUT/);}finally{await m.cleanup()}
});
test('Expired receipt cannot save; members cannot mutate model configurations',async()=>{
 let m=await mount({testResponse:{...result(),expires_at:'2000-01-01T00:00:00Z'}});try{await edit();button('测试连接').click();await pause();assert.ok(button('保存配置').disabled);assert.match(document.body.textContent,/测试结果已过期/);}finally{await m.cleanup()}
 m=await mount({role:'member'});try{assert.equal(button('+ 添加模型配置'),undefined);assert.equal(button('编辑配置'),undefined);assert.match(document.body.textContent,/当前仅可查看/);}finally{await m.cleanup()}
});
test('Workspace switch closes editor and discards old test result',async()=>{
 let finish;const pending=new Promise(r=>{finish=r});const m=await mount({testHandler:()=>pending});try{await edit();button('测试连接').click();await pause();activeWorkspaceId.value='new-workspace';await pause();finish(Response.json(result()));await pause();assert.doesNotMatch(document.body.textContent,/ACTUAL TEXT OUTPUT/);assert.ok(m.calls.some(c=>c.url.includes('/workspaces/new-workspace/model-profiles')));}finally{await m.cleanup()}
});
test('Changing name invalidates receipt and entering a replacement key needs no checkbox',async()=>{
 const m=await mount();try{await edit();button('测试连接').click();await pause();assert.equal(button('保存配置').disabled,false);const input=[...document.querySelectorAll('input')].find(i=>i.value==='Existing');input.value='Renamed';input.dispatchEvent(new window.Event('input',{bubbles:true}));await pause();assert.ok(button('保存配置').disabled);
 assert.equal(document.querySelector('[role=checkbox]'), null);
 fillKey('replacement-key');
 await pause();
 button('测试连接').click();
 await pause();
 const probe=m.calls.filter(c=>c.url.endsWith('/test')).at(-1);
 assert.equal(probe.body.api_key,'replacement-key');

 }finally{await m.cleanup()}
});
test('Successful save remains successful when the following list refresh fails',async()=>{
 const m=await mount();try{await edit();button('测试连接').click();await pause();const original=globalThis.fetch;globalThis.fetch=async(url,init={})=>init.method==='PATCH'?original(url,init):Response.json({error:{message:'refresh-only-failed'}},{status:503});button('保存配置').click();await pause();assert.match(document.body.textContent,/模型配置已保存/);assert.match(document.body.textContent,/refresh-only-failed/);assert.doesNotMatch(document.body.textContent,/保存失败/);}finally{await m.cleanup()}
});
test('Changed Base URL must explicitly replace credentials before any probe is sent',async()=>{
 const m=await mount();try{await edit();const input=[...document.querySelectorAll('input')].find(i=>i.value==='https://model.example/v1');input.value='https://other.example/v1';input.dispatchEvent(new window.Event('input',{bubbles:true}));await pause();button('测试连接').click();await pause();assert.equal(m.calls.some(c=>c.url.endsWith('/test')),false);assert.match(document.body.textContent,/避免将旧密钥发往其他服务/);fillKey('new-key');await pause();button('测试连接').click();await pause();const probe=m.calls.find(c=>c.url.endsWith('/test'));assert.ok(probe);assert.equal(probe.body.api_key,'new-key');}finally{await m.cleanup()}
});
test('Revoked/unvalidated profile cannot simply be enabled without retesting',async()=>{
 const m=await mount({items:[{...profile,validated:false,has_key:false,enabled:false}]});try{assert.ok(button('启用').disabled);assert.match(document.body.textContent,/待重新测试/);assert.ok(button('编辑配置'));}finally{await m.cleanup()}
});

function fillKey(value) {
 const input = document.querySelector('input[type=password]');
 assert.ok(input);
 input.value = value;
 input.dispatchEvent(new window.Event('input', {bubbles:true}));
}
test('New and previously keyless models require a nonblank credential before probing', async () => {
 for (const existing of [false, true]) {
  const m = await mount({items:existing ? [{...profile, has_key:false}] : []});
  try {
   button(existing ? '编辑配置' : '+ 添加模型配置').click();
   await pause();
   fillKey('  ');
   button('测试连接').click();
   await pause();
   assert.match(document.body.textContent, /请填写 API Key/);
   assert.equal(m.calls.some(c => c.url.endsWith('/test')), false);
   assert.equal(button('保存配置').disabled, true);
  } finally { await m.cleanup(); }
 }
});

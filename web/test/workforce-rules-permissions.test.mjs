import test from 'node:test';
Error.stackTraceLimit = 0;
import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
import {Window} from 'happy-dom';
import {parse,compileScript} from '@vue/compiler-sfc';
import esbuild from 'esbuild';
const window=new Window({url:'http://127.0.0.1:5173/'});
for(const name of ['Event','EventTarget','PointerEvent','window','document','navigator','HTMLElement','Element','SVGElement','Node','Text','Comment','Image','InputEvent','MouseEvent','KeyboardEvent','HTMLInputElement','Document','MutationObserver','ResizeObserver','getComputedStyle','requestAnimationFrame','cancelAnimationFrame'])if(name==='window')globalThis.window=window;else if(window[name]!==undefined)Object.defineProperty(globalThis,name,{configurable:true,writable:true,value:typeof window[name]==='function'&&['getComputedStyle','requestAnimationFrame','cancelAnimationFrame'].includes(name)?window[name].bind(window):window[name]});
const built=await esbuild.build({stdin:{contents:"export {default as View} from './src/direction2/components/workforce/WorkforceRulesTab.vue';export {createApp,h,ref,nextTick} from 'vue';export {NMessageProvider} from 'naive-ui';export {workforcePortsKey} from './src/direction2/api/workforce-ports';export {activeWorkspaceId} from './src/direction2/api/client';export {buildCanonicalPrincipalKey} from './src/direction2/types';",resolveDir:process.cwd(),loader:'ts'},bundle:true,write:false,format:'esm',platform:'node',define:{'import.meta.env.VITE_PLATFORM_REAL':'true','import.meta.env.DEV':'false'},plugins:[{name:'vue',setup(build){build.onLoad({filter:/workforce-platform-data\.ts$/},async({path})=>({contents:(await fs.readFile(path,'utf8')).replace('const value=cache.get(key(w,a));','const value=globalThis.__usageSnapshot;'),loader:'ts'}));build.onLoad({filter:/\.vue$/},async({path})=>{if(path.endsWith('/WorkforceSharingRules.vue'))return {contents:"import {defineComponent,h} from 'vue';export default defineComponent({props:['canManage'],setup:p=>()=>h('div',{'data-testid':'sharing-access'},String(p.canManage))})",loader:'ts'};const {descriptor}=parse(await fs.readFile(path,'utf8'));return {contents:compileScript(descriptor,{id:path,inlineTemplate:true}).content,loader:'ts'}})}}]});
const {View,createApp,h,ref,nextTick,NMessageProvider,workforcePortsKey,activeWorkspaceId,buildCanonicalPrincipalKey}=await import(`data:text/javascript;base64,${Buffer.from(built.outputFiles[0].text).toString('base64')}`);
const pause=async()=>{await new Promise(r=>setTimeout(r,40));await nextTick()};
const button=label=>[...document.querySelectorAll('button')].find(el=>el.textContent.trim()===label);
const employee=()=>({id:'a',version:3,name:'员工',resourceVersions:{usageAccess:7},unifiedAccess:{mode:'all_with_blacklist',allowGroupMembers:true,whitelistMemberIds:[],blacklistMemberIds:[],whitelistTargets:[],blacklistTargets:[]},handoffPolicy:{mode:'whitelist',allowEmployeeIds:[],denyEmployeeIds:[]},humanConfirmationRules:[],channels:[]});
async function mount(allowed, seed, directoryDepartments=[]){const calls=[],canManage=ref(allowed),draft=ref(seed ? structuredClone(seed) : employee()),saved=ref(seed ? structuredClone(seed) : employee());if(!seed) draft.value.unifiedAccess.allowGroupMembers=false;const host=document.createElement('div');document.body.append(host);const app=createApp({render:()=>h(NMessageProvider,null,{default:()=>h(View,{employee:draft.value,savedEmployee:saved.value,employees:[],directoryUsers:[],directoryDepartments,isDevMode:false,isActiveTab:!!seed,canManage:canManage.value,syncServerFieldIntoSnapshot:()=>{},handleOptimisticConflictError:()=>false})})});app.provide(workforcePortsKey,{workforceApi:{saveEmployeeRules:async(...args)=>{calls.push(args);return employee()}},resourcesApi:{},tasksApi:{}});app.mount(host);await pause();return{calls,canManage,draft,async cleanup(){app.unmount();host.remove();await pause()}}}
test('Read-only rules preserve layout but disable writes and pass permission into sharing',async()=>{const m=await mount(false);try{assert.ok(document.querySelector('.settings-card-block'));assert.equal(button('保存').disabled,true);assert.equal(button('从通讯录选择').disabled,true);assert.equal(document.querySelector('[data-testid="sharing-access"]').textContent,'false');button('保存').click();await pause();assert.equal(m.calls.length,0)}finally{await m.cleanup()}});
test('Management permission permits access save with captured resource version, not employee CAS',async()=>{const m=await mount(true);try{button('保存').click();await pause();assert.equal(m.calls.length,1);assert.equal(m.calls[0][1]._usageVersion,7);assert.equal(m.calls[0][2],3)}finally{await m.cleanup()}});
test('Find-person configuration and the obsolete handoff placeholder are absent from permissions',async()=>{const m=await mount(true);try{assert.doesNotMatch(document.body.textContent,/尚未接入/);assert.equal(button('+ 添加确认规则'),undefined);assert.equal(button('+ 发起企业邮箱定向邀请'),undefined);assert.doesNotMatch(document.body.textContent,/企业邮箱定向邀请|外协成员授权/)}finally{await m.cleanup()}});
test('Permission revocation disables a previously editable draft without submitting it',async()=>{const m=await mount(true);try{m.canManage.value=false;await pause();assert.equal(button('保存').disabled,true);assert.equal(document.querySelector('[data-testid="sharing-access"]').textContent,'false');button('保存').click();await pause();assert.equal(m.calls.length,0)}finally{await m.cleanup()}});


test('Pure whitelist hides deny selector and group availability remains independent',async()=>{
 const m=await mount(true);try{m.draft.value.unifiedAccess.mode='whitelist_with_blacklist';m.draft.value.unifiedAccess.allowGroupMembers=undefined;await pause();const card=document.querySelector('.settings-card-block');assert.match(card.textContent,/白名单/);assert.match(card.textContent,/群内可用/);assert.doesNotMatch(card.textContent,/额外排除|选择群/);assert.equal(card.querySelector('[role="switch"]').getAttribute('aria-checked'),'true')}finally{await m.cleanup()}
});
test('Legacy mixed rules require explicit review and renewed confirmation after draft edits',async()=>{
 globalThis.__usageSnapshot={usage:{usage_config:{access:{mode:'allowlist',group_members_enabled:false,allow:[{channel:'wecom',kind:'group',id:'old-group'}],deny:[{channel:'web',kind:'user',id:'excluded-user'}]}}}};
 const m=await mount(true);try{
  assert.match(document.body.textContent,/old-group|excluded-user/);assert.equal(button('保存'),undefined);assert.equal(!!document.querySelector('.settings-card-block').querySelector('.cb-form'),false);
  button('审阅并转换使用范围').click();await pause();assert.equal(button('保存').disabled,true);
  const checkbox=document.querySelector('.legacy-access-review [role="checkbox"]');checkbox.click();await pause();assert.equal(button('保存').disabled,false);
  m.draft.value.unifiedAccess.allowGroupMembers=true;await pause();assert.equal(button('保存').disabled,true);
  checkbox.click();await pause();button('保存').click();await pause();assert.equal(m.calls.length,1);assert.equal(m.calls[0][1]._replaceLegacyAccess,true);
 }finally{await m.cleanup();delete globalThis.__usageSnapshot}
});

test('Real ACL label ignores stale directory epoch and does not create a draft; configuration change clears it', async () => {
  const seed = employee();
  const selector = {channel:'feishu', kind:'department', id:'dep-id', binding_id:'binding', identity_epoch:4, include_descendants:false};
  const principal = {channel:'feishu', targetType:'department', targetId:'dep-id', connectionId:'binding', usageSelector:selector};
  principal.canonicalKey = buildCanonicalPrincipalKey(principal);
  seed.unifiedAccess.blacklistMemberIds = [principal.canonicalKey];
  seed.unifiedAccess.blacklistTargets = [principal];
  seed.channels = [{platform:'feishu', platformName:'飞书', connectionId:'binding', connectionGeneration:2, identityEpoch:4, configRevision:3, status:'connected'}];
  const oldDirectory = [{id:'dep-id', refId:'dep-id', platform:'feishu', connectionId:'binding', name:'Previous tenant name',
    principal:{...principal, usageSelector:{...selector, identity_epoch:3}}}];
  const previousFetch = globalThis.fetch;
  let lookups = 0;
  activeWorkspaceId.value = 'w';
  globalThis.fetch = async url => {
    if (!url.endsWith('/selectors/people/resolve')) return Response.json({version:'0', scope:{mode:'all', employee_ids:[]}});
    lookups++;
    return Response.json({items:[{ref:{workspace_id:'w', agent_id:'a', provider:'feishu', kind:'department',
      external_id:'dep-id', binding_id:'binding', identity_epoch:4}, status:'active', display_name:'Current department'}]});
  };
  let m;
  try {
    m = await mount(true, seed, oldDirectory);
    const card = () => document.querySelector('.settings-card-block');
    for (let i = 0; i < 10 && !card().textContent.includes('Current department'); i++) await pause();
    assert.equal(lookups, 1);
    assert.match(card().textContent, /Current department/);
    assert.doesNotMatch(card().textContent, /Previous tenant name|未保存/);
    assert.equal(lookups, 1);
    m.draft.value.channels[0].configurationChanged = true;
    await pause();
    assert.match(card().textContent, /dep-id/);
    assert.doesNotMatch(card().textContent, /Current department|Previous tenant name/);
    assert.equal(lookups, 1);
    m.draft.value.unifiedAccess.blacklistTargets[0].usageSelector.include_descendants = true;
    await pause();
    assert.match(card().textContent, /未保存/);
  } finally {
    await m?.cleanup();
    activeWorkspaceId.value = '';
    globalThis.fetch = previousFetch;
  }
});

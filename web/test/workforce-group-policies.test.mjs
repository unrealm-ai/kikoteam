import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
import {Window} from 'happy-dom';
import {parse,compileScript} from '@vue/compiler-sfc';
import esbuild from 'esbuild';
const window=new Window({url:'http://localhost/'});
for(const name of ['window','document','navigator','HTMLElement','Element','SVGElement','Node','Text','Comment','Image','InputEvent','MouseEvent','KeyboardEvent','HTMLInputElement','Document','MutationObserver','ResizeObserver','getComputedStyle','requestAnimationFrame','cancelAnimationFrame']){
 if(name==='window')globalThis.window=window;else if(window[name]!==undefined)Object.defineProperty(globalThis,name,{configurable:true,writable:true,value:typeof window[name]==='function'&&['getComputedStyle','requestAnimationFrame','cancelAnimationFrame'].includes(name)?window[name].bind(window):window[name]});
}
globalThis.fetch=async()=>{throw new Error('Use injected real-contract ports')};
const built=await esbuild.build({stdin:{contents:"export {default as Policies} from './src/direction2/components/workforce/WorkforceGroupPolicies.vue'; export {workforcePortsKey} from './src/direction2/api/workforce-ports'; export {replaceGroupPolicy,replaceChannelHistory,groupPolicyDraft} from './src/direction2/api/workforce-group-policies'; export {createApp,h,nextTick,reactive} from 'vue';",resolveDir:process.cwd(),loader:'ts'},bundle:true,write:false,format:'esm',platform:'node',define:{'import.meta.env.VITE_PLATFORM_REAL':'true'},plugins:[{name:'vue',setup(build){build.onLoad({filter:/\.vue$/},async({path})=>{const {descriptor}=parse(await fs.readFile(path,'utf8'));return{contents:compileScript(descriptor,{id:path,inlineTemplate:true}).content,loader:'ts'};});}}]});
const {Policies,workforcePortsKey,replaceGroupPolicy,replaceChannelHistory,groupPolicyDraft,createApp,h,nextTick,reactive}=await import(`data:text/javascript;base64,${Buffer.from(built.outputFiles[0].text).toString('base64')}`);
const pause=async()=>{await new Promise(r=>setTimeout(r,30));await nextTick()};
const button=t=>[...document.querySelectorAll('button')].find(v=>v.textContent.trim()===t);
const field=(placeholder,value)=>{const el=[...document.querySelectorAll('input')].find(v=>v.placeholder===placeholder);assert.ok(el,placeholder);el.value=value;el.dispatchEvent(new window.Event('input',{bubbles:true}));};
const usage=()=>({version:7,usage_config:{access:{mode:'all',group_members_enabled:true,allow:[],deny:[{kind:'user',channel:'web',id:'blocked'}]},trigger:{mention:true,keywords:[{text:'do',channels:['web']}],regexes:[]},history:{enabled:true,limit:37,channels:['feishu','wecom']}}});
const rules=()=>({version:9,rules:[{id:'web-rule',name:'Web专属',type:'keyword',pattern:'web',enabled:true,channels:['web']},{id:'fs-rule',name:'飞书专属',type:'regex',pattern:'飞书.*',enabled:true,channels:['feishu']}]});
async function mount(overrides={},channel){
 const calls=[];let u=usage(),r=rules();const props=reactive({workspace:'w',employeeId:'a',canManage:true,...(channel?{channel}:{})});
 const api={getUsage:async()=>u,getRules:async()=>r,saveUsage:async(w,a,v)=>{calls.push(['usage',w,a,structuredClone(v)]);u={...v,version:v.version+1};return u;},saveRules:async(w,a,v)=>{calls.push(['rules',w,a,structuredClone(v)]);r={...v,version:v.version+1};return r;},testRules:async(...args)=>{calls.push(['test',...args]);return{matched_count:1,matched:[{rule_id:'web-rule',rule_name:'Web专属',type:'keyword',matched_text:'web'}]};},...overrides};
 const root=document.createElement('div');document.body.append(root);const app=createApp({render:()=>h(Policies,props)});app.provide(workforcePortsKey,{groupPoliciesApi:api,workforceApi:{},resourcesApi:{},tasksApi:{}});app.mount(root);await pause();return{calls,props,close:async()=>{app.unmount();root.remove();await pause();}};
}
test('policy projection preserves access, matcher rows, CAS and history channels while toggling global enable',()=>{
 const old=usage(),draft=groupPolicyDraft(old);draft.historyEnabled=false;draft.historyChannels.push('web');const next=replaceGroupPolicy(old,draft);
 assert.equal(next.version,7);assert.deepEqual(next.usage_config.access,old.usage_config.access);assert.deepEqual(next.usage_config.trigger.keywords,old.usage_config.trigger.keywords);assert.deepEqual(next.usage_config.history,{enabled:false,limit:37,channels:['feishu','wecom','web']});assert.equal(old.usage_config.trigger.mention,true);
});
test('original group settings expose shared mention, Web history and preserve existing channels on save',async()=>{
 const f=await mount();try{
 assert.match(document.body.textContent,/支持群 @ 触发/);
 assert.equal(document.querySelector('[aria-label="Web／飞书群 @ 触发"]'),null);
 document.querySelector('[aria-label="启用近期群历史"]').click();await pause();
 const web=[...document.querySelectorAll('.n-checkbox')].find(v=>v.textContent.trim()==='Web 群');web.click();await pause();button('保存历史设置').click();await pause();
 const saved=f.calls.find(c=>c[0]==='usage')[3];assert.equal(saved.usage_config.trigger.mention,true);assert.deepEqual(saved.usage_config.history.channels,['feishu','wecom','web']);assert.equal(saved.usage_config.history.limit,37);assert.equal(saved.usage_config.access.deny[0].id,'blocked');
 }finally{await f.close();}
});
test('editing a Feishu named rule retains Web-only row and tests Web draft through same contract',async()=>{
 const f=await mount();try{
 const editButtons=[...document.querySelectorAll('button')].filter(v=>v.textContent.trim()==='编辑');editButtons[1].click();await pause();field('规则名称','飞书更新');
 button('保存群规则').click();await pause();const saved=f.calls.find(c=>c[0]==='rules')[3];assert.equal(saved.version,9);assert.deepEqual(saved.rules[0],rules().rules[0]);assert.deepEqual(saved.rules[1].channels,['feishu']);assert.equal(saved.rules[1].name,'飞书更新');
 field('输入一条群消息，测试是否命中规则','web');await pause();button('测试命中').click();await pause();const call=f.calls.find(c=>c[0]==='test');assert.equal(call[5],'web');assert.deepEqual(call[4][0].channels,['web']);assert.match(document.body.textContent,/Web专属：web/);
 }finally{await f.close();}
});
test('revocation clears loaded rules and prevents writes',async()=>{
 const f=await mount({saveUsage:async()=>{throw Object.assign(new Error('denied'),{status:403});}});try{document.querySelector('[aria-label="启用近期群历史"]').click();await pause();button('保存历史设置').click();await pause();assert.equal(document.body.textContent.includes('Web专属'),false);assert.match(document.body.textContent,/管理权限/);}finally{await f.close();}
});
test('rule editor selects Web and Feishu without dropping other rows and tests unsaved channels',async()=>{
 const f=await mount();try{
 [...document.querySelectorAll('button')].filter(v=>v.textContent.trim()==='编辑')[1].click();await pause();
 const modal=document.querySelector('.n-modal');assert.ok(modal);
 [...modal.querySelectorAll('.n-checkbox')].find(v=>v.textContent.trim()==='Web 群').click();await pause();
 const input=[...modal.querySelectorAll('input')].find(v=>v.placeholder==='输入一条群消息，测试是否命中规则');input.value='web';input.dispatchEvent(new window.Event('input',{bubbles:true}));await pause();button('测试草稿命中').click();await pause();
 const attempted=f.calls.find(c=>c[0]==='test');assert.equal(attempted[5],'web');assert.deepEqual(attempted[4][1].channels,['feishu','web']);assert.equal(f.calls.some(c=>c[0]==='rules'),false);
 button('保存群规则').click();await pause();const saved=f.calls.find(c=>c[0]==='rules')[3];assert.deepEqual(saved.rules[1].channels,['feishu','web']);assert.deepEqual(saved.rules[0],rules().rules[0]);
 }finally{await f.close();}
});
test('CAS conflict keeps draft and does not retry or rewrite unrelated rules',async()=>{
 let attempts=0;const f=await mount({saveUsage:async()=>{attempts++;throw Object.assign(new Error('版本冲突，请刷新'),{status:409});}});try{
 document.querySelector('[aria-label="启用近期群历史"]').click();await pause();button('保存历史设置').click();await pause();assert.equal(attempts,1);assert.match(document.body.textContent,/版本冲突/);assert.match(document.body.textContent,/Web专属/);assert.equal(document.querySelector('[aria-label="启用近期群历史"]').getAttribute('aria-checked'),'false');
 }finally{await f.close();}
});
test('confirmed rule save survives usage refresh failure and stale policy cannot be rewritten',async()=>{
 let reads=0;const f=await mount({getUsage:async()=>{if(++reads>1)throw new Error('read unavailable');return usage();}});try{
 [...document.querySelectorAll('button')].filter(v=>v.textContent.trim()==='编辑')[1].click();await pause();field('规则名称','已保存名字');button('保存群规则').click();await pause();assert.equal(f.calls.filter(c=>c[0]==='rules').length,1);assert.match(document.body.textContent,/群触发规则已保存，但历史设置刷新失败/);assert.match(document.body.textContent,/已保存名字/);assert.equal(document.querySelector('[aria-label="启用近期群历史"]').classList.contains('n-switch--disabled'),true);assert.equal(button('保存历史设置').disabled,true);
 }finally{await f.close();}
});

test('disabled mention remains disabled when editing history, without an @ switch',async()=>{
 const initial=usage();initial.usage_config.trigger.mention=false;
 const f=await mount({getUsage:async()=>initial});try{
 assert.match(document.body.textContent,/已有 @ 配置为关闭/);
 assert.equal(document.querySelector('[aria-label="Web／飞书群 @ 触发"]'),null);
 document.querySelector('[aria-label="启用近期群历史"]').click();await pause();button('保存历史设置').click();await pause();
 const saved=f.calls.find(c=>c[0]==='usage')[3];assert.equal(saved.usage_config.trigger.mention,false);assert.deepEqual(saved.usage_config.trigger,initial.usage_config.trigger);assert.equal(saved.usage_config.history.enabled,false);
 }finally{await f.close();}
});

test('channel history projection never enables other disabled channels or changes mention',()=>{
 for(const global of [true,false])for(const enabled of [true,false]){
 const old=usage();old.usage_config.history.enabled=global;old.usage_config.trigger.mention=false;
 const next=replaceChannelHistory(old,'web',enabled,43);
 assert.equal(next.usage_config.trigger.mention,false);
 assert.equal(next.usage_config.history.enabled&&next.usage_config.history.channels.includes('web'),enabled);
 for(const channel of ['feishu','wecom'])assert.equal(next.usage_config.history.enabled&&next.usage_config.history.channels.includes(channel),global);
 assert.equal(next.usage_config.history.limit,43);assert.deepEqual(next.usage_config.access,old.usage_config.access);
 }
});
test('provider panels display only supported capabilities and channel-scoped history',async()=>{
 for(const channel of ['web','feishu','wecom','dingtalk']){
 const f=await mount({},channel);try{
 assert.match(document.body.textContent,/群 @ 触发支持/);
 assert.equal(!!document.querySelector('[aria-label="启用近期群历史"]'),channel!=='dingtalk');
 assert.equal(!!button('新增群匹配规则'),['web','feishu'].includes(channel));
 if(channel==='feishu'){
 document.querySelector('[aria-label="启用近期群历史"]').click();await pause();button('保存历史设置').click();await pause();
 const saved=f.calls.find(c=>c[0]==='usage')[3];assert.deepEqual(saved.usage_config.history.channels,['wecom']);assert.equal(saved.usage_config.history.enabled,true);assert.equal(saved.usage_config.trigger.mention,true);
 }
 }finally{await f.close();}
 }
});
test('WeCom history loads, refreshes and saves through usage only', async () => {
 let ruleReads = 0;
 const f = await mount({getRules:async()=>{ruleReads++;throw new Error('群规则服务不可用');}}, 'wecom');
 try {
  assert.equal(document.querySelector('[aria-label="启用近期群历史"]').getAttribute('aria-checked'), 'true');
  button('刷新群配置').click();await pause();
  document.querySelector('[aria-label="启用近期群历史"]').click();await pause();
  button('保存历史设置').click();await pause();
  assert.equal(ruleReads, 0);
  assert.deepEqual(f.calls.map(call=>call[0]), ['usage']);
  assert.deepEqual(f.calls[0][3].usage_config.history, {enabled:true,limit:37,channels:['feishu']});
 } finally {
  await f.close();
 }
});
test('Feishu-only UI splits shared edits and deletes without changing hidden Web rules',async()=>{
 const original=rules();original.rules[1].channels=['web','feishu'];
 for(const action of ['edit','delete']){
 const f=await mount({getRules:async()=>structuredClone(original)},'feishu');try{
 const row=[...document.querySelectorAll('tbody tr')].find(v=>v.textContent.includes('飞书专属'));
 if(action==='edit'){
 [...row.querySelectorAll('button')].find(v=>v.textContent==='编辑').click();await pause();
 assert.equal(document.querySelector('.n-modal .n-checkbox-group'),null);field('规则名称','仅飞书更改');button('保存群规则').click();
 }else [...row.querySelectorAll('button')].find(v=>v.textContent==='删除').click();
 await pause();const saved=f.calls.find(c=>c[0]==='rules')[3];
 assert.deepEqual(saved.rules[0],original.rules[0]);assert.deepEqual(saved.rules.find(v=>v.id==='fs-rule'),{...original.rules[1],channels:['web']});
 if(action==='edit'){assert.deepEqual(saved.rules[2].channels,['feishu']);assert.equal(saved.rules[2].id,'');assert.equal(saved.rules[2].name,'仅飞书更改');}else assert.equal(saved.rules.length,2);
 }finally{await f.close();}
 }
});

test('Feishu test fixes the simulated channel and never saves draft rules',async()=>{
 const f=await mount({},'feishu');try{
  assert.equal(document.querySelector('.group-policies .n-select'),null);
  assert.match(document.body.textContent,/只模拟匹配，不发送群消息，不触发员工回复/);
  field('输入一条群消息，测试是否命中规则','飞书测试');await pause();button('测试命中').click();await pause();
  const row=[...document.querySelectorAll('tbody tr')].find(v=>v.textContent.includes('飞书专属'));
  [...row.querySelectorAll('button')].find(v=>v.textContent.trim()==='编辑').click();await pause();
  assert.equal(document.querySelector('.n-modal .n-select'),null);
  field('匹配内容','新规则');await pause();button('测试草稿命中').click();await pause();
  assert.deepEqual(f.calls.map(c=>[c[0],c[3],c[5]]),[['test','飞书测试','feishu'],['test','飞书测试','feishu']]);
  assert.equal(f.calls[1][4][1].pattern,'新规则');
 }finally{await f.close();}
});

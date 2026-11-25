import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
import {Window} from 'happy-dom';
import {parse,compileScript} from '@vue/compiler-sfc';
import esbuild from 'esbuild';
const window=new Window({url:'http://127.0.0.1:5173/'});
for(const name of ['window','document','navigator','HTMLElement','Element','SVGElement','Node','Text','Comment','Image','InputEvent','MouseEvent','KeyboardEvent','HTMLInputElement','Document','MutationObserver','ResizeObserver','getComputedStyle','requestAnimationFrame','cancelAnimationFrame'])if(name==='window')globalThis.window=window;else if(window[name]!==undefined)Object.defineProperty(globalThis,name,{configurable:true,writable:true,value:typeof window[name]==='function'&&['getComputedStyle','requestAnimationFrame','cancelAnimationFrame'].includes(name)?window[name].bind(window):window[name]});
const built=await esbuild.build({stdin:{contents:"export {default as View} from './src/direction2/components/workforce/WorkforceSharingPicker.vue';export {createApp,h,ref,nextTick} from 'vue';",resolveDir:process.cwd(),loader:'ts'},bundle:true,write:false,format:'esm',platform:'node',define:{'import.meta.env.VITE_PLATFORM_REAL':'true','import.meta.env.DEV':'false'},plugins:[{name:'vue',setup(build){build.onLoad({filter:/\.vue$/},async({path})=>{if(path.endsWith('/TaskDetailPanel.vue'))return {contents:"import {defineComponent,h} from 'vue';export default defineComponent({props:['taskId','workspaceId'],setup:p=>()=>h('div',{'data-testid':'task-detail'},p.workspaceId+':'+p.taskId)})",loader:'ts'};const {descriptor}=parse(await fs.readFile(path,'utf8'));return {contents:compileScript(descriptor,{id:path,inlineTemplate:true}).content,loader:'ts'}})}}]});
const {View,createApp,h,ref,nextTick}=await import(`data:text/javascript;base64,${Buffer.from(built.outputFiles[0].text).toString('base64')}`);
const pause=async()=>{await new Promise(r=>setTimeout(r,40));await nextTick()};
const discovery = '/channels/feishu/groups/discover';
const listGroups = '/selectors/groups?';
const resolveGroup = '/selectors/groups/resolve';
const group = {id:'saved-group', kind:'group', provider:'feishu', workspace_id:'w', agent_id:'a', identity_namespace:'chat_id', external_id:'group'};
const candidate = {ref:group, name:'真实员工群', selectable:true, status:'active', evidence:'observed'};
const observedPage = () => Response.json({source:'observed', items:[], next_cursor:''});
const livePage = (items, next_cursor='') => Response.json({source:'provider', items, next_cursor});
const button = label => [...document.querySelectorAll('button')].find(el=>el.textContent.trim()===label);
const optionLabels = () => [...document.querySelectorAll('.n-base-select-option')].map(el=>el.textContent.trim());

async function mount(handle=()=>undefined) {
  const calls=[], picked=[], workspace=ref('w');
  globalThis.fetch=async(url,init={})=>{
    const body=init.body?JSON.parse(init.body):undefined;
    const method=init.method ?? 'GET';
    calls.push({url,body,method});
    const result=await handle(url,body);
    if(result) return result;
    if(url.endsWith(discovery)) return observedPage();
    if(url.endsWith(resolveGroup)) return Response.json({items:[candidate]});
    if(url.includes(listGroups)) return Response.json({items:[candidate],next_cursor:null});
    throw new Error('Unexpected test request: '+url);
  };
  const host=document.createElement('div');
  document.body.append(host);
  const app=createApp({render:()=>h(View,{workspace:workspace.value,employeeId:'a',kind:'groups',onPick:row=>picked.push(row)})});
  app.mount(host);
  await pause();
  return {calls,picked,workspace,async cleanup(){app.unmount();host.remove();await pause()}};
}

async function openPicker(index) {
  document.querySelectorAll('.n-base-selection')[index].dispatchEvent(new window.MouseEvent('click',{bubbles:true}));
  await pause();
}

async function chooseGroup(label='真实员工群') {
  await openPicker(1);
  const option=[...document.querySelectorAll('.n-base-select-option')].find(el=>el.textContent.trim()===label);
  assert.ok(option,label);
  option.click();
  document.body.click();
  await pause();
}

test('Feishu provider failure labels and resolves the observed group before selecting it',async()=>{
  const m=await mount();
  try {
    assert.match(document.body.textContent,/显示历史观察到的群，可能不全/);
    assert.ok(m.calls.some(call=>call.url.includes(listGroups)&&call.url.includes('provider=feishu')));
    await chooseGroup();
    assert.equal(m.picked.length,1);
    assert.deepEqual(m.picked[0].ref,group);
    assert.deepEqual(m.calls.find(call=>call.url.endsWith(resolveGroup)).body,{refs:[group]});
  } finally {await m.cleanup()}
});

test('Observed sharing candidate becoming unavailable is never selected',async()=>{
  const m=await mount(url=>url.endsWith(resolveGroup)?Response.json({items:[{...candidate,selectable:false,disabled_reason:'已退出群'}]}):undefined);
  try {
    await chooseGroup();
    assert.equal(m.picked.length,0);
    assert.match(document.body.textContent,/已退出群/);
  } finally {await m.cleanup()}
});

test('Observed sharing select detects repeated pagination cursor',async()=>{
  const m=await mount(url=>url.includes(listGroups)?Response.json({items:[candidate],next_cursor:'same'}):undefined);
  try {
    button('加载更多候选').click();
    await pause();
    assert.match(document.body.textContent,/候选分页异常/);
    assert.equal(button('加载更多候选'),undefined);
  } finally {await m.cleanup()}
});

test('Sharing select shows new workspace candidates and drops a late old workspace page',async()=>{
  let finish;
  const fresh={...candidate, name:'新工作区员工群',ref:{...group,workspace_id:'new',id:'new-group',external_id:'new-group'}};
  const m=await mount(url=>url.includes(listGroups)?
    (url.includes('/workspaces/w/')?new Promise(r=>finish=r):Response.json({items:[fresh],next_cursor:null})):undefined);
  try {
    assert.equal(typeof finish,'function');
    m.workspace.value='new';
    await pause();
    finish(Response.json({items:[candidate],next_cursor:null}));
    await pause();
    await openPicker(1);
    assert.ok(optionLabels().includes('新工作区员工群'));
    assert.ok(!optionLabels().includes('真实员工群'));
  } finally {await m.cleanup()}
});

test('Sharing channel selector exposes only Feishu and WeCom and discovers Feishu by default',async()=>{
  const m=await mount();
  try {
    assert.match(m.calls[0].url,/\/channels\/feishu\/groups\/discover$/);
    assert.equal(m.calls[0].method,'POST');
    assert.deepEqual(m.calls[0].body,{cursor:''});
    await openPicker(0);
    assert.deepEqual(optionLabels().sort(),['企微','飞书'].sort());
  } finally {await m.cleanup()}
});

test('Sharing picker never offers hidden-channel groups returned by search',async()=>{
  const m=await mount(url=>url.includes(listGroups)?Response.json({items:[
    candidate,
    {...candidate,ref:{...group,provider:'web'},name:'隐藏 Web 群'},
    {...candidate,ref:{...group,provider:'dingtalk'},name:'隐藏钉钉群'},
  ],next_cursor:null}):undefined);
  try {
    await openPicker(1);
    assert.deepEqual(optionLabels(),['真实员工群']);
  } finally {await m.cleanup()}
});

test('Live Feishu sharing pages without falling back and emits only the verified selection',async()=>{
  const preview={...candidate,evidence:'provider',ref:{...group,id:undefined}};
  const m=await mount((url,body)=>{
    if(!url.endsWith(discovery)) return undefined;
    if(body.selection_token) return livePage([{selection:candidate}]);
    if(body.cursor==='next') return livePage([{selection:preview,selection_token:'test-signed-proof'}]);
    return livePage([], 'next');
  });
  try {
    assert.doesNotMatch(document.body.textContent,/显示历史观察到的群/);
    button('继续刷新飞书群').click();
    await pause();
    assert.equal(button('继续刷新飞书群'),undefined);
    await chooseGroup();
    assert.deepEqual(m.calls.filter(call=>call.url.endsWith(discovery)).map(call=>call.body),[
      {cursor:''},{cursor:'next'},{selection_token:'test-signed-proof'},
    ]);
    assert.ok(!m.calls.some(call=>call.url.includes(listGroups)||call.url.endsWith(resolveGroup)));
    assert.deepEqual(m.picked,[candidate]);
  } finally {await m.cleanup()}
});

test('Live Feishu sharing rejects a repeated discovery cursor',async()=>{
  const m=await mount(url=>url.endsWith(discovery)?livePage([], 'same'):undefined);
  try {
    button('继续刷新飞书群').click();
    await pause();
    assert.match(document.body.textContent,/群刷新游标重复/);
    assert.ok(!m.calls.some(call=>call.url.includes(listGroups)));
    assert.deepEqual(m.picked,[]);
  } finally {await m.cleanup()}
});

import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
import { Window } from 'happy-dom';
import { parse, compileScript } from '@vue/compiler-sfc';
import esbuild from 'esbuild';

const window = new Window();
globalThis.window = window;
for (const name of ['document', 'HTMLElement', 'Element', 'SVGElement', 'Node', 'Text', 'Comment', 'MutationObserver', 'ResizeObserver']) {
  if (window[name]) Object.defineProperty(globalThis, name, { configurable: true, value: window[name] });
}
const input = "export {default as View} from './src/direction2/components/workforce/WorkforceWecomContactPermission.vue'; export {createApp,h,ref,nextTick} from 'vue';";
const built = await esbuild.build({stdin:{contents:input,resolveDir:process.cwd(),loader:'ts'},bundle:true,write:false,format:'esm',platform:'node',plugins:[{name:'view',setup(build){
  build.onResolve({filter:/^naive-ui$/},()=>({namespace:'fake',path:'tag'}));
  build.onLoad({filter:/^tag$/,namespace:'fake'},()=>({contents:"import {h} from 'vue'; export const NTag = (_, {slots, attrs}) => h('span', attrs, slots.default())",resolveDir:process.cwd()}));
  build.onResolve({filter:/api\/wecom-contacts$/},()=>({namespace:'fake',path:'wecom'}));
  build.onLoad({filter:/wecom/,namespace:'fake'},()=>({contents:'export const wecomContactsApi = {permission: (...args) => globalThis.wecomProbe(...args)};'}));
  build.onLoad({filter:/\.vue$/},async ({path})=>{const {descriptor}=parse(await fs.readFile(path,'utf8'));return {contents:compileScript(descriptor,{id:path,inlineTemplate:true}).content,loader:'ts'}});
}}]});
const {View,createApp,h,ref,nextTick}=await import(`data:text/javascript;base64,${Buffer.from(built.outputFiles[0].text).toString('base64')}`);
const flush = async () => { await Promise.resolve(); await nextTick(); await nextTick(); };
test('WeCom permission badge is lazy and scoped, with revision/abort fencing', async () => {
  const pending=[];
  globalThis.wecomProbe = (...args) => new Promise((resolve,reject)=>pending.push({args,resolve,reject}));
  const scope = ref({workspace:'workspace',employee:'employee',binding:'binding',generation:2,revision:5,eligible:false});
  const host=document.createElement('div');document.body.append(host);
  const app=createApp({render:()=>h(View,scope.value)});app.mount(host);
  const update = async (change) => { scope.value={...scope.value,...change}; await flush(); };
  try {
    await flush();
    assert.match(host.textContent,/联系人搜索未检测/);
    assert.equal(pending.length,0);
    await update({eligible:true});
    assert.match(host.textContent,/正在检测联系人搜索/);
    assert.deepEqual(pending[0].args.slice(0,2),['workspace','employee']);
    await update({generation:3,revision:6});
    assert.equal(pending[0].args[2].aborted,true);
    pending[0].resolve({status:'available',binding_id:'binding',connection_generation:2,config_revision:5});
    await flush(); assert.match(host.textContent,/正在检测联系人搜索/);
    pending[1].resolve({status:'available',binding_id:'binding',connection_generation:2,config_revision:5});
    await flush(); assert.match(host.textContent,/联系人搜索未检测/);
    await update({revision:7});
    pending[2].resolve({status:'denied',binding_id:'binding',connection_generation:3,config_revision:7});
    await flush(); assert.match(host.textContent,/联系人搜索未获授权/);
    await update({revision:8});
    pending[3].reject(new Error('secret provider error'));
    await flush(); assert.match(host.textContent,/联系人搜索暂无法检测/);assert.doesNotMatch(host.textContent,/secret/);
    await update({revision:9});
    pending[4].resolve({status:'available',binding_id:'binding',connection_generation:3,config_revision:9});
    await flush(); assert.match(host.textContent,/联系人搜索可用/);assert.doesNotMatch(host.textContent,/权限就绪/);
    await update({binding:'new'});
    pending[5].resolve({status:'available',binding_id:'binding',connection_generation:3,config_revision:9});
    await flush(); assert.match(host.textContent,/联系人搜索未检测/);
    await update({revision:10});
    await update({eligible:false});
    assert.equal(pending[6].args[2].aborted,true);
    await flush(); assert.match(host.textContent,/联系人搜索未检测/);
  } finally {app.unmount();host.remove();delete globalThis.wecomProbe}
});

import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
import {Window} from 'happy-dom';
import {parse,compileScript} from '@vue/compiler-sfc';
import esbuild from 'esbuild';
const window=new Window({url:'http://127.0.0.1:5173/'});
for(const name of ['window','document','navigator','HTMLElement','Element','SVGElement','Node','Text','Comment','Image','InputEvent','MouseEvent','KeyboardEvent','HTMLInputElement','Document','MutationObserver','ResizeObserver','getComputedStyle','requestAnimationFrame','cancelAnimationFrame'])if(name==='window')globalThis.window=window;else if(window[name]!==undefined)Object.defineProperty(globalThis,name,{configurable:true,writable:true,value:typeof window[name]==='function'&&['getComputedStyle','requestAnimationFrame','cancelAnimationFrame'].includes(name)?window[name].bind(window):window[name]});
const built=await esbuild.build({stdin:{contents:"export {default as View} from './src/direction2/components/workforce/WorkforceConnectionStatus.vue';export {createApp,h,ref,nextTick} from 'vue';export {NDialogProvider} from 'naive-ui';",resolveDir:process.cwd(),loader:'ts'},bundle:true,write:false,format:'esm',platform:'node',define:{'import.meta.env.VITE_PLATFORM_REAL':'true','import.meta.env.DEV':'false'},plugins:[{name:'vue',setup(build){build.onLoad({filter:/\.vue$/},async({path})=>{if(path.endsWith('/WorkforceSharingPicker.vue'))return {contents:"import {defineComponent,h} from 'vue';export default defineComponent({props:['kind','disabled'],emits:['pick'],setup:(p,{emit})=>()=>h('button',{disabled:p.disabled,onClick:()=>emit('pick',{selectable:true,ref:{kind:p.kind==='groups'?'group':'user',provider:p.kind==='groups'?'feishu':'web',workspace_id:'w',agent_id:'a',external_id:p.kind==='groups'?'g':'u',binding_id:'b',identity_epoch:2,identity_namespace:'chat_id'}})},'选入'+p.kind)})",loader:'ts'};const {descriptor}=parse(await fs.readFile(path,'utf8'));return {contents:compileScript(descriptor,{id:path,inlineTemplate:true}).content,loader:'ts'}})}}]});
const {View,createApp,h,ref,nextTick,NDialogProvider}=await import(`data:text/javascript;base64,${Buffer.from(built.outputFiles[0].text).toString('base64')}`);
const pause=async()=>{await new Promise(r=>setTimeout(r,40));await nextTick()};

test('Connection code is tooltip only for real disconnected status, supports hover and keyboard focus',async()=>{
 const connected=ref(false),code=ref('connection_failed'),real=ref(true),host=document.createElement('div');document.body.append(host);
 const app=createApp({render:()=>h(View,{connected:connected.value,errorCode:code.value,realBackend:real.value})});app.mount(host);await pause();
 try {
  const badge=()=>host.querySelector('.connection-status');
  assert.match(host.textContent,/未连接/);assert.equal(host.querySelector('[role="tooltip"]'),null);assert.equal(badge().tabIndex,0);
  badge().dispatchEvent(new window.Event('mouseenter'));await pause();assert.match(host.querySelector('[role="tooltip"]').textContent,/connection_failed/);
  badge().dispatchEvent(new window.Event('mouseleave'));await pause();assert.equal(host.querySelector('[role="tooltip"]'),null);
  badge().focus();await pause();assert.ok(host.querySelector('[role="tooltip"]'));badge().blur();await pause();assert.equal(host.querySelector('[role="tooltip"]'),null);
  connected.value=true;await pause();badge().dispatchEvent(new window.Event('mouseenter'));await pause();assert.match(host.textContent,/已连接/);assert.equal(host.querySelector('[role="tooltip"]'),null);
  connected.value=false;code.value='https://secret.example/token';await pause();badge().dispatchEvent(new window.Event('mouseenter'));await pause();assert.equal(host.querySelector('[role="tooltip"]'),null);assert.doesNotMatch(host.textContent,/secret/);
  code.value='connection_failed';real.value=false;await pause();badge().dispatchEvent(new window.Event('mouseenter'));await pause();assert.equal(host.querySelector('[role="tooltip"]'),null);
  real.value=true;code.value=undefined;await pause();assert.equal(badge().getAttribute('tabindex'),null);
 }finally{app.unmount();host.remove();await pause()}
});

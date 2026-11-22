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
const built=await esbuild.build({stdin:{contents:"export {default as HomeDashboard} from './src/direction2/views/HomeDashboard.vue'; export {activeWorkspaceId} from './src/direction2/api/client'; export {createApp,h,nextTick} from 'vue'; export {NMessageProvider,NDialogProvider} from 'naive-ui';",resolveDir:process.cwd(),loader:'ts'},bundle:true,write:false,format:'esm',platform:'node',define:{'import.meta.env.VITE_PLATFORM_REAL':'true','import.meta.env.DEV':'false'},plugins:[{name:'vue-test',setup(build){build.onLoad({filter:/\.vue$/},async({path})=>{const source=await fs.readFile(path,'utf8');const {descriptor}=parse(source);return {contents:compileScript(descriptor,{id:path,inlineTemplate:true}).content,loader:'ts'};});}}]});
const {HomeDashboard,activeWorkspaceId,createApp,h,NMessageProvider,NDialogProvider,nextTick}=await import(`data:text/javascript;base64,${Buffer.from(built.outputFiles[0].text).toString('base64')}`);

async function renderError(status, code, message) {
 globalThis.fetch=async()=>Response.json({error:{code,message}},{status});
 activeWorkspaceId.value='docker-qa';
 const host=document.createElement('div');document.body.appendChild(host);
 const app=createApp(HomeDashboard);app.mount(host);
 await new Promise(r=>setTimeout(r,40));await nextTick();
 const text=host.textContent;app.unmount();host.remove();return text;
}
test('Missing overview route clearly states migration boundary without fake metrics',async()=>{
 const text=await renderError(404,'not_found','接口不存在');
 assert.match(text,/工作台概览尚未接入/);assert.match(text,/不展示模拟数据/);
 assert.doesNotMatch(text,/工作台概览数据加载失败/);
});
test('Other API failures remain actionable errors, not migration placeholders',async()=>{
 for(const [status,code,message] of [[503,'unavailable','数据库暂不可用'],[404,'workspace_not_found','工作区不存在'],[401,'unauthorized','需要登录']]){
  const text=await renderError(status,code,message);
  assert.match(text,/工作台概览数据加载失败/);assert.ok(text.includes(message));assert.doesNotMatch(text,/工作台概览尚未接入/);
 }
});

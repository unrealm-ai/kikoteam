import test from 'node:test';
import assert from 'node:assert/strict';
import { build } from 'esbuild';

const bundle=await build({stdin:{
  contents:"export { useWorkforceChannelStatuses } from './src/direction2/composables/useWorkforceChannelStatuses'; export { createRenderer, ref, defineComponent } from 'vue';",
  resolveDir:process.cwd(),loader:'ts',
},bundle:true,write:false,format:'esm',platform:'node',define:{
  'import.meta.env.VITE_PLATFORM_REAL':'true','import.meta.env.DEV':'false',
}});
const {useWorkforceChannelStatuses,createRenderer,ref,defineComponent}=await import(
  `data:text/javascript;base64,${Buffer.from(bundle.outputFiles[0].text).toString('base64')}`,
);

test('stable DingTalk permission observation reads only on the visible channel tab without parallel polls',async()=>{
  const oldWindow=globalThis.window,oldDocument=globalThis.document;
  const page=Object.assign(new EventTarget(),{hidden:false});
  globalThis.window=new EventTarget();
  globalThis.document=page;
  const timers=new Map();
  const oldSetInterval=globalThis.setInterval,oldClearInterval=globalThis.clearInterval;
  globalThis.setInterval=(fn,ms)=>{timers.set(ms,fn);return ms};
  globalThis.clearInterval=ms=>timers.delete(ms);

  const channel={platform:'dingtalk',status:'connected',connectionState:'connected',connectionId:'d',connectionGeneration:3,dingtalkProactiveSendPermissionMissing:true};
  const employee=ref({id:'a',status:'active',channels:[channel],resourceVersions:{channels:{dingtalk:5}}});
  const calls=[];
  let finish;
  let currentTab=false;
  const app=createRenderer({createComment:()=>({}),insert:()=>{},parentNode:()=>null,remove:()=>{}}).createApp(defineComponent({
    setup(){
      useWorkforceChannelStatuses({
        employee,employees:ref([]),snapshot:ref(''),onSnapshotPatched(){},
        isDingTalkTabActive:()=>currentTab,
        getStatuses:async(_id,_workspace,options)=>{
          calls.push(options.channels);
          return new Promise(resolve=>{finish=resolve});
        },
      });
      return ()=>null;
    },
  }));
  const getObservation=()=>({employeeId:'a',status:'active',channels:[{
    platform:'dingtalk',status:'connected',statusText:'已连接',connectionState:'connected',
    connectionId:'d',connectionGeneration:3,observedConfigRevision:5,
  }]});
  try {
    app.mount({});
    const tick=timers.get(30_000);
    assert.ok(tick);
    tick();
    currentTab=true;
    page.hidden=true;
    tick();
    page.hidden=false;
    employee.value.channels[0].status='disabled';
    tick();
    employee.value.channels[0].status='connected';
    assert.deepEqual(calls,[]);
    tick();
    tick();
    assert.deepEqual(calls,[['dingtalk']]);

    // A temporary response has no permission field; no new evidence to overwrite the last successful value.
    finish(getObservation());
    await new Promise(setImmediate);
    assert.equal(employee.value.channels[0].dingtalkProactiveSendPermissionMissing,true);
    tick();
    const successful=getObservation();
    successful.channels[0].dingtalkProactiveSendPermissionMissing=undefined;
    finish(successful);
    await new Promise(setImmediate);
    assert.equal(employee.value.channels[0].dingtalkProactiveSendPermissionMissing,undefined);

    employee.value.channels[0].dingtalkProactiveSendPermissionMissing=true;
    tick();
    const rotated=getObservation();
    rotated.channels[0].connectionGeneration=4;
    finish(rotated);
    await new Promise(setImmediate);
    assert.equal(employee.value.channels[0].configurationChanged,true);
    assert.equal(employee.value.channels[0].dingtalkProactiveSendPermissionMissing,undefined);
  } finally {
    app.unmount();
    assert.equal(timers.has(30_000),false);
    globalThis.setInterval=oldSetInterval;
    globalThis.clearInterval=oldClearInterval;
    globalThis.window=oldWindow;
    globalThis.document=oldDocument;
  }
});

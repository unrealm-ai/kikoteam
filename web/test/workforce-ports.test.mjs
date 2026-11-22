import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
import {createHash} from 'node:crypto';
import esbuild from 'esbuild';
const built=await esbuild.build({stdin:{contents:"export {useWorkforcePorts,workforcePortsKey} from './src/direction2/api/workforce-ports';export {createApp} from 'vue';",resolveDir:process.cwd(),loader:'ts'},bundle:true,write:false,format:'esm',platform:'node',define:{'import.meta.env.VITE_PLATFORM_REAL':'false'}});
const {createApp,useWorkforcePorts,workforcePortsKey}=await import(`data:text/javascript;base64,${Buffer.from(built.outputFiles[0].text).toString('base64')}`);
test('Original Workforce transport can be replaced as a complete group without a Mock fallback merge',()=>{
 const app=createApp({});const prototype=app.runWithContext(useWorkforcePorts);
 assert.equal(typeof prototype.workforceApi.getEmployee,'function');
 const supplied={workforceApi:{getEmployee:()=> 'real'},resourcesApi:{},tasksApi:{}};
 app.provide(workforcePortsKey,supplied);
 const actual=app.runWithContext(useWorkforcePorts);
 assert.equal(actual,supplied);assert.equal(actual.workforceApi.getEmployee(),'real');
 assert.equal(actual.workforceApi.testChannelConnection,undefined);
});
// Baseline is the Workforce design source, not Platform*.
// The employee settings layout uses both scoped and shared rules; pin both. Channel onboarding keeps the approved narrow QR layout.
// Updated 2026-11-22: unified dirty-card border/pill styling, multiline role truncation, channel tab status dots, and legacy access list polish.
const baseline={"components/workforce/workforce-shared.css": "a7fbf34a00790bfbc61efef1c4e501b80cce980617e677e6c7a4ea1d6ab78799", "views/WorkforceView.vue": "b90556f7bfe27bcd3e8e096736826c8dcc3dfef69424e1e702b2f49ba9965b19", "components/workforce/WorkforceDirectoryList.vue": "2dfa7cf5a656a43ed8913a35fcb3ab4bf5e57bf1cc86d434d4f1647dbef4c548", "components/workforce/WorkforceChannelsTab.vue": "180fb209c1a608f3e26258cf2e29497161a0d5e7a87276d1b026d81433432a80", "components/workforce/WorkforceRulesTab.vue": "074c321dad425e07f1e6b580e4f035a904b294c7e8709714b377f846ca6b768b", "components/workforce/WorkforceTriggersTab.vue": "8c5ceba0eb6982fc0b8c9082bd352a6cb24eea572decb710c57b9446927bdfb2"};
test('Original Workforce styles retain baseline including approved notice color unification',async()=>{
 for(const [file,hash]of Object.entries(baseline)){
  const source=await fs.readFile(`src/direction2/${file}`,'utf8');
  assert.equal(createHash('sha256').update(file.endsWith('.css') ? source : source.split('<style')[1]).digest('hex'),hash,file);
 }
});

test('Approved original channel changes preserve layout and show actual status',async()=>{
 const source=await fs.readFile('src/direction2/components/workforce/WorkforceChannelsTab.vue','utf8');
 const template=source.slice(source.indexOf('</script>'));
 for(const text of ['class="qr-pane-body"','class="qr-details"','class="onboarding-form-pane"','智能机器人状态',"connectionState === 'connected'","permissionReady === true",'WorkforceConnectionStatus','规则命中测试器']) assert.ok(source.includes(text),text);
 for(const text of ['解绑','Encrypt Key','Verification Token','可按需读取研发中心与基础架构部通讯录架构']) assert.ok(!template.includes(text),text);
});

import test from 'node:test';import assert from 'node:assert/strict';import {build} from 'esbuild';
const built=await build({entryPoints:['src/direction2/api/workforce-trigger-rules.ts'],bundle:true,write:false,format:'esm',platform:'node',define:{'import.meta.env':'{}'}});
const {replaceFeishuTriggerRules}=await import(`data:text/javascript;base64,${Buffer.from(built.outputFiles[0].text).toString('base64')}`);
const rule=(id,channels)=>({id,channels,name:id,type:'keyword',pattern:'测试',enabled:true});
test('Feishu edit preserves Web-only rows, shared channels, and loaded CAS revision',()=>{const a=rule('a',['feishu','web']),b=rule('b',['web']);const r=replaceFeishuTriggerRules({version:4,rules:[a,b]},[{...a,name:'新名'}]);assert.equal(r.version,4);assert.deepEqual(r.rules[0],b);assert.deepEqual(r.rules[1].channels,['feishu','web']);});
test('removing Feishu shared rule retains its Web side and new IDs are backend allocated',()=>{const r=replaceFeishuTriggerRules({version:5,rules:[rule('a',['feishu','web'])]},[rule('draft-local',['feishu'])]);assert.deepEqual(r.rules[0].channels,['web']);assert.equal(r.rules[1].id,'');assert.equal(r.rules[1].name,'draft-local');});

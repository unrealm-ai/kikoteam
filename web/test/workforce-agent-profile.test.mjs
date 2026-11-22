import test from 'node:test';
import assert from 'node:assert/strict';
import {build} from 'esbuild';
const built=await build({entryPoints:['src/direction2/api/workforce-agent-profile.ts'],bundle:true,write:false,format:'esm',platform:'node',define:{'import.meta.env':'{}'}});
const {workforceAgentProfile,createWorkforceAgentProfileTransport}=await import(`data:text/javascript;base64,${Buffer.from(built.outputFiles[0].text).toString('base64')}`);
const agent={id:'a',version:7,name:'员工',role:'助手',avatar:'preset',instruction:'原始人设',enabled:true};
test('profile projection does not invent prototype statistics, status or channel data',()=>{
 assert.deepEqual(workforceAgentProfile(agent),{id:'a',version:7,name:'员工',role:'助手',avatar:'preset',prompt:'原始人设'});
});
test('profile save keeps original version and only sends explicitly edited supported fields',async()=>{
 let calls=[];const transport=createWorkforceAgentProfileTransport({update:async(...args)=>{calls.push(args);return agent;}});
 await transport.save('w',agent,{prompt:'',name:'新名',channels:['unrelated'],version:99});
 assert.deepEqual(calls,[['w',agent,{name:'新名',instruction:''}]]);
});
test('profile conflict is returned without retries or prototype success',async()=>{
 let count=0;const error=new Error('version_conflict');
 const transport=createWorkforceAgentProfileTransport({update:async()=>{count++;throw error;}});
 await assert.rejects(transport.save('w',agent,{name:'新名'}),e=>e===error);assert.equal(count,1);
});

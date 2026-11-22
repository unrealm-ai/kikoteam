import test from 'node:test';
import assert from 'node:assert/strict';
import {build} from 'esbuild';
const built=await build({entryPoints:['src/direction2/utils/workforce.ts'],bundle:true,write:false,format:'esm',platform:'node',define:{'import.meta.env':'{}'}});
const {patchChannelRuntimeStatusFields}=await import(`data:text/javascript;base64,${Buffer.from(built.outputFiles[0].text).toString('base64')}`);
test('explicit unknown runtime evidence clears old ready status without touching editable policies',()=>{
 const target={permissionReady:true,businessCapabilities:[{id:'old'}],capabilities:{recentContextWindow:40}};
 patchChannelRuntimeStatusFields(target,{status:'connected',statusText:'已连接',permissionReady:undefined,businessCapabilities:undefined});
 assert.equal(target.permissionReady,undefined);assert.equal(target.businessCapabilities,undefined);assert.equal(target.capabilities.recentContextWindow,40);
});
test('omitted prototype evidence is not a request to clear existing diagnostics',()=>{
 const target={permissionReady:true,businessCapabilities:[{id:'old'}]};patchChannelRuntimeStatusFields(target,{status:'connected',statusText:'已连接'});assert.equal(target.permissionReady,true);assert.equal(target.businessCapabilities.length,1);
});

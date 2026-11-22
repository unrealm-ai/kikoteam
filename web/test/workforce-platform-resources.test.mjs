import test from 'node:test';
import assert from 'node:assert/strict';
import {build} from 'esbuild';
const built=await build({entryPoints:['src/direction2/api/workforce-platform-resources.ts'],bundle:true,write:false,format:'esm',platform:'node',define:{'import.meta.env':'{}'}});
const {workforcePlatformResources:api,modelResourceForWorkforce,skillResourceForWorkforce,pluginResourceForWorkforce,invitationResourceForWorkforce}=await import(`data:text/javascript;base64,${Buffer.from(built.outputFiles[0].text).toString('base64')}`);
const model={id:'same',name:'Model',source:'workspace',protocol:'openai_responses',enabled:true,validated:true,is_default:false,model_id:'gpt-test',base_url:'https://example.invalid',has_key:true};
const official={id:'same',name:'Official',source:'platform',protocol:'openai_responses',enabled:true,validated:true,is_default:false,prices:{input:'1',output:'2',cache_read:null,cache_write:null}};
const skill={id:'s',name:'Skill',description:'Desc',enabled:true,current_version_id:'v',created_at:'date',current_version:{technical_name:'tech',version_label:'v1',source:{type:'github',repository_url:'https://github.com/example/repo',commit:'abc'}}};
const plugin={id:'p',name:'Plugin',endpoint:'https://example.invalid/mcp',auth_type:'bearer',enabled:true};
test('model projection preserves namespaces, prices and unknown facts',()=>{
 const a=modelResourceForWorkforce(model),b=modelResourceForWorkforce(official);
 assert.equal(a.id,'workspace:same');assert.equal(b.id,'platform:same');assert.equal(b.endpoint,null);assert.equal(b.modelCode,null);assert.equal(b.supportsThinking,null);assert.equal(b.boundEmployeesCount,null);assert.equal(b.platformResource.prices.input,'1');
 assert.equal(modelResourceForWorkforce({...model,validated:false}).selectable,false);
 assert.equal(modelResourceForWorkforce({...model,validated:undefined}).selectable,false);
});
test('skill source and actual version are retained without inventing body or usage counts',()=>{
 const s=skillResourceForWorkforce(skill);assert.equal(s.versionId,'v');assert.equal(s.code,'tech');assert.equal(s.source,'git_synced');assert.equal(s.gitCommit,'abc');assert.equal(s.promptTemplate,null);assert.equal(s.boundEmployeesCount,null);assert.equal(s.historyVersions,null);
});
test('workspace plugin never claims employee authentication or discovery',()=>{
 const p=pluginResourceForWorkforce(plugin);assert.equal(p.accountScope,'employee');assert.equal(p.status,null);assert.equal(p.tools,null);assert.equal(p.allowedTools,null);assert.equal(p.whitelistMatched,null);
});
test('workspace invitation never invents employee-specific grants or date/name',()=>{
 const x=invitationResourceForWorkforce({id:'i',kind:'email',email:'x@example.invalid',role:'member',status:'accepted',expires_at:'date',delivery_status:'sent',contact_email:''});assert.equal(x.status,'activated');assert.equal(x.scope,'workspace');assert.equal(x.name,null);assert.equal(x.targetEmployeeIds,null);assert.equal(x.invitedAt,null);
});
test('models paginate using composite namespaces; provider errors propagate rather than falling back',async()=>{
 const original=globalThis.fetch;const calls=[];
 try{globalThis.fetch=async(path)=>{calls.push(path);return new Response(JSON.stringify(calls.length===1?{items:[model],page:1,total:2,page_size:1}:{items:[official],page:2,total:2,page_size:1}),{status:200});};
 assert.deepEqual((await api.listModels('w')).map(x=>x.id),['workspace:same','platform:same']);assert.equal(calls.length,2);assert.match(calls[0],/^\/api\/v1\/workspaces\/w\/model-profiles\?include_platform=true/);
 globalThis.fetch=async()=>new Response(JSON.stringify({error:{code:'workspace_forbidden',message:'Denied'}}),{status:403});await assert.rejects(api.listSkills('w'),/Denied/);
 }finally{globalThis.fetch=original;}
});
test('skill/plugin/template/invitation lists use real endpoint pagination; only email invitations selected',async()=>{
 const original=globalThis.fetch;const calls=[];
 try{globalThis.fetch=async(path)=>{calls.push(path);const items=path.includes('/skills?')?[skill]:path.includes('/plugins?')?[plugin]:path.includes('capability-package-templates')?[{id:'template',name:'Template'}]:[{id:'i',kind:'email',email:'x@example.invalid',status:'valid',expires_at:'date'},{id:'l',kind:'link'}];return new Response(JSON.stringify({items,total:items.length,page:1,page_size:100}));};
 assert.equal((await api.listSkills('w')).length,1);assert.equal((await api.listMcpServers('w'))[0].status,null);assert.equal((await api.getCapabilityPackagesCatalog('w'))[0].id,'template');assert.equal((await api.listEmailInvitations('w')).length,1);assert.equal(calls.length,4);
 }finally{globalThis.fetch=original;}
});
test('directory and group options require employee scope, never manufacture a global synced empty directory',async()=>{
 assert.throws(()=>api.getDirectorySyncSources('w'),/AI 员工/);assert.throws(()=>api.getOptions('w'),/AI 员工/);
 const original=globalThis.fetch;try{globalThis.fetch=async(path)=>{assert.match(path,/agents\/a\/selectors\/people\/sources\?purpose=sharing/);return new Response(JSON.stringify({items:[{provider:'wecom',available:true,supports_department_tree:false,supports_browse:false,supports_pagination:true}]}));};const result=await api.getOptions('w','a','sharing');assert.equal(result.items[0].supports_department_tree,false);}finally{globalThis.fetch=original;}
});

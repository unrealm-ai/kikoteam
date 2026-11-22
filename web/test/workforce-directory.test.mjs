import test from 'node:test';
import assert from 'node:assert/strict';
import esbuild from 'esbuild';
const built=await esbuild.build({stdin:{contents:"export {createWorkforceDirectoryReader,directoryPrincipal} from './src/direction2/api/workforce-directory';",resolveDir:process.cwd(),loader:'ts'},bundle:true,write:false,format:'esm',platform:'node',define:{'import.meta.env.VITE_PLATFORM_REAL':'true'}});
const {createWorkforceDirectoryReader,directoryPrincipal}=await import(`data:text/javascript;base64,${Buffer.from(built.outputFiles[0].text).toString('base64')}`);
const context={workspace:'w',employeeId:'a',purpose:'usage'},signal=new AbortController().signal;
const candidate=(kind,id)=>({ref:{kind,provider:'feishu',workspace_id:'w',agent_id:'a',external_id:id,binding_id:'b',identity_epoch:3},name:id,status:'active',selectable:true});
test('Directory transport maps original DTO while retaining typed selector and cursor boundary',async()=>{const calls=[];const reader=createWorkforceDirectoryReader({search:async(...args)=>{calls.push(args);return{items:[candidate(args[2]==='departments'?'department':'user',args[5]||'first')],next_cursor:args[5]?null:'next',snapshot_version:10}}});const first=await reader.page(context,'feishu','b','',1,true,signal);assert.equal(first.nextCursor,'2');assert.equal(first.users[0].principal.usageSelector.identity_epoch,3);assert.equal(first.departments[0].principal.usageSelector.include_descendants,false);await reader.page(context,'feishu','b','',2,true,signal);assert.equal(calls[2][5],'next');assert.equal(calls[2][8],10)});
test('Directory transport never restarts an exhausted user cursor during department paging',async()=>{const calls=[];const reader=createWorkforceDirectoryReader({search:async(...args)=>{calls.push(args);return{items:[],next_cursor:args[2]==='departments'&&!args[5]?'next':null,snapshot_version:10}}});await reader.page(context,'feishu','b','',1,true,signal);await reader.page(context,'feishu','b','',2,true,signal);assert.equal(calls.filter(args=>args[2]==='people').length,1);assert.equal(calls.length,3)});
test('WeCom empty query does not fake browsing or department support',async()=>{const reader=createWorkforceDirectoryReader({search:async()=>{throw Error('must not call')}});const page=await reader.page(context,'wecom','b','',1,true,signal);assert.deepEqual(page.users,[]);assert.deepEqual(page.departments,[])});
test('Directory transport refuses changed snapshots and repeated cursors',async()=>{let n=0;const reader=createWorkforceDirectoryReader({search:async()=>({items:[],next_cursor:'same',snapshot_version:++n})});await reader.page(context,'feishu','b','',1,false,signal);await assert.rejects(reader.page(context,'feishu','b','',2,false,signal),/游标重复|快照已变化/)});
test('Directory choice is resolved against authoritative identity before selection',async()=>{const row=candidate('department','d');const reader=createWorkforceDirectoryReader({resolve:async()=>({items:[{...row,selectable:false,disabled_reason:'部门失效'}]})});await assert.rejects(reader.verify(context,directoryPrincipal(row),signal),/部门失效/)});

test('Verified WeCom contact name refresh preserves callback identity and saved rule selector', async () => {
  const row = {ref:{kind:'user',provider:'wecom',workspace_id:'w',agent_id:'a',binding_id:'b',identity_epoch:3,connection_generation:5,identity_namespace:'callback_user_id',external_id:'callback-A'},display_name:'原姓名',search_user_id:'search-A',verification_status:'verified',status:'unknown',selectable:true};
  const before = directoryPrincipal(row);
  const reader = createWorkforceDirectoryReader({resolve:async(_w,_a,_kind,refs)=>{
    assert.deepEqual(refs,[row.ref]);
    return {items:[{...row,display_name:'更新姓名'}]};
  }});
  const after = await reader.verify(context,before,signal);
  assert.equal(after.displayName,'更新姓名');
  assert.equal(after.targetId,'callback-A');
  assert.equal(after.canonicalKey,before.canonicalKey);
  assert.deepEqual(after.usageSelector,before.usageSelector);
  assert.equal(after.usageSelector.id,'callback-A');
  assert.equal(after.selectionCandidate.search_user_id,'search-A');
  assert.deepEqual(after.selectionCandidate.ref,row.ref);
});

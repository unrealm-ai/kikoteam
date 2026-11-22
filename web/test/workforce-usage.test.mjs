import test from 'node:test';
import assert from 'node:assert/strict';
import esbuild from 'esbuild';
const built=await esbuild.build({stdin:{contents:"export {replaceWorkforceAccess,accessSelectors,createWorkforceUsageActions,workforceAccessRequiresReview,workforceAccessIsDirty} from './src/direction2/api/workforce-usage';export {buildCanonicalPrincipalKey} from './src/direction2/types';export {reactive,effectScope,shallowRef,nextTick} from 'vue';export {useWorkforceAccessLabels,workforceAccessIdentityKey} from './src/direction2/composables/useWorkforceAccessLabels';export {buildPrincipalRefsFromIds} from './src/direction2/utils/workforce';",resolveDir:process.cwd(),loader:'ts'},bundle:true,write:false,format:'esm',platform:'node',define:{'import.meta.env.VITE_PLATFORM_REAL':'true'}});
const {replaceWorkforceAccess,accessSelectors,createWorkforceUsageActions,workforceAccessRequiresReview,workforceAccessIsDirty,buildCanonicalPrincipalKey,reactive,effectScope,shallowRef,nextTick,useWorkforceAccessLabels,workforceAccessIdentityKey,buildPrincipalRefsFromIds}=await import(`data:text/javascript;base64,${Buffer.from(built.outputFiles[0].text).toString('base64')}`);
const dept={channel:'feishu',kind:'department',id:'dep',binding_id:'b',identity_epoch:3,include_descendants:false};
const group={channel:'wecom',kind:'group',id:'g',binding_id:'b2',identity_epoch:9};
const original={version:8,usage_config:{access:{mode:'allowlist',group_members_enabled:false,allow:[dept],deny:[]},trigger:{mention:true,keywords:[{text:'x',channels:['web']}],regexes:[]},history:{enabled:true,limit:30,channels:['feishu']}}};
const principal={channel:'feishu',targetType:'department',targetId:'dep',connectionId:'b',usageSelector:dept};principal.canonicalKey=buildCanonicalPrincipalKey(principal);
const access={mode:'whitelist_with_blacklist',allowGroupMembers:true,whitelistMemberIds:[principal.canonicalKey],blacklistMemberIds:[],whitelistTargets:[principal],blacklistTargets:[]};
test('Original access UI preserves usage CAS, channel identity, child-department flag without introducing group rules',()=>{const result=replaceWorkforceAccess(original,reactive(access));assert.equal(result.version,8);assert.deepEqual(result.usage_config.trigger,original.usage_config.trigger);assert.deepEqual(result.usage_config.history,original.usage_config.history);assert.deepEqual(result.usage_config.access.allow,[dept]);assert.equal(result.usage_config.access.group_members_enabled,true);assert.equal(original.usage_config.access.group_members_enabled,false)});
test('New ambiguous raw IDs cannot silently receive employee current channel authority',()=>{assert.throws(()=>accessSelectors(['someone'],[],[]),/真实授权信息/);assert.throws(()=>accessSelectors([principal.canonicalKey],[{...principal,usageSelector:{...dept,identity_epoch:undefined}}],[]),/身份版本缺失/)});
test('Same names and native IDs in separate channels stay separate',()=>{const other={...principal,channel:'dingtalk',connectionId:'b3',usageSelector:{...dept,channel:'dingtalk',binding_id:'b3',identity_epoch:12}};other.canonicalKey=buildCanonicalPrincipalKey(other);assert.deepEqual(accessSelectors([principal.canonicalKey,other.canonicalKey],[principal,other],[]),[dept,other.usageSelector])});
test('Existing immutable identity survives without a current directory snapshot',()=>{assert.deepEqual(accessSelectors([principal.canonicalKey],[],[dept]),[dept])});
test('Forged or mismatched identity is rejected rather than broadened',()=>{assert.throws(()=>accessSelectors([principal.canonicalKey],[{...principal,usageSelector:{...dept,id:'other'}}],[]),/不一致/)});

test('Original dropdown retains selector metadata including empty Web binding',()=>{const web={channel:'web',targetType:'user',connectionId:'',targetId:'account',usageSelector:{channel:'web',kind:'user',id:'account'}};web.canonicalKey=buildCanonicalPrincipalKey(web);const refs=buildPrincipalRefsFromIds([web.canonicalKey],[],[{id:'account',refId:'account',platform:'web',connectionId:'',name:'人',principal:web}],[]);assert.deepEqual(accessSelectors([web.canonicalKey],refs,[]),[web.usageSelector]);});

test('Newer cache cannot advance old access draft CAS; conflict is not retried',async()=>{const calls=[];const conflict=Object.assign(new Error('版本冲突'),{status:409});const actions=createWorkforceUsageActions(()=> 'w',{snapshot:()=>({employee:{version:4},usage:{...original,version:19}}),save:async(w,id,body)=>{calls.push({w,id,body});throw conflict},invalidate:()=>{throw Error('must not invalidate failed write')},reload:()=>{throw Error('must not rebase or retry')}});await assert.rejects(actions.saveEmployeeRules('a',{unifiedAccess:access,_usageVersion:8},4),error=>error===conflict);assert.equal(calls.length,1);assert.equal(calls[0].body.version,8);assert.equal(calls[0].body.usage_config.access.group_members_enabled,true);});
test('Real access save refuses missing or invalid draft CAS before any write',async()=>{let calls=0;const actions=createWorkforceUsageActions(()=> 'w',{snapshot:()=>{calls++;return {employee:{version:4},usage:original}},save:async()=>{calls++},invalidate:()=>{},reload:async()=>{}});for(const version of [undefined,0,-1,1.5,'8'])await assert.rejects(actions.saveEmployeeRules('a',{unifiedAccess:access,...(version===undefined?{}:{_usageVersion:version})},4),/草稿版本缺失/);assert.equal(calls,0);});


test('Legacy policies require explicit review and preserve original data',()=>{
 for(const old of [ {...original.usage_config.access,allow:[dept,group]}, {...original.usage_config.access,deny:[dept]}, {...original.usage_config.access,mode:'all'} ]){
  assert.equal(workforceAccessRequiresReview(old),true);const current={...original,usage_config:{...original.usage_config,access:old}};const before=JSON.stringify(current);
  assert.throws(()=>replaceWorkforceAccess(current,access),/需复核/);assert.equal(JSON.stringify(current),before);
  const converted=replaceWorkforceAccess(current,access,true);assert.equal(converted.replace_legacy_access,true);assert.deepEqual(converted.usage_config.access.allow,[dept]);assert.deepEqual(converted.usage_config.access.deny,[]);assert.equal(JSON.stringify(current),before);
 }
});
test('Two modes emit only selected list and preserve independent group toggle',()=>{
 const white=replaceWorkforceAccess(original,{...access,blacklistMemberIds:['inactive'],allowGroupMembers:false});assert.deepEqual(white.usage_config.access.deny,[]);assert.equal(white.usage_config.access.group_members_enabled,false);assert.equal(white.replace_legacy_access,undefined);
 const all=replaceWorkforceAccess(original,{...access,mode:'all_with_blacklist',whitelistMemberIds:['inactive'],blacklistMemberIds:[principal.canonicalKey],blacklistTargets:[principal],allowGroupMembers:undefined},true);assert.deepEqual(all.usage_config.access.allow,[]);assert.deepEqual(all.usage_config.access.deny,[dept]);assert.equal(all.usage_config.access.group_members_enabled,true);assert.equal(all.replace_legacy_access,undefined);
});

test('Shared usage draft comparison includes only persisted selector changes beyond mock semantics', () => {
  const edited = overrides => ({...access, whitelistTargets: [{...principal, usageSelector: {...dept, ...overrides}}]});
  assert.equal(workforceAccessIsDirty(edited({include_descendants: true}), access, true), true);
  assert.equal(workforceAccessIsDirty(edited({identity_epoch: 4}), access, true), true);
  assert.equal(workforceAccessIsDirty(edited({include_descendants: true}), access, false), false);
  const relabeled = {...access, whitelistTargets: [{...principal, displayName: 'New label'}]};
  assert.equal(workforceAccessIsDirty(relabeled, access, true), false);
  assert.equal(workforceAccessIsDirty({...access, whitelistMemberIds: []}, access, false), true);
});

const labelScope = (selectors, bindingRevision = '0', usageRead = 0) => ({
  workspace: 'w', employee: 'a', selectors, bindingRevision, usageRevision: 8, usageRead,
});
const label = (v, name, status = 'active', selectable = true, agent_id = 'a') => ({
  ref: {workspace_id: 'w', agent_id, provider: v.channel, kind: v.kind, external_id: v.id, binding_id: v.binding_id, identity_epoch: v.identity_epoch},
  display_name: name, status, selectable,
});
const flushLabels = async () => { await nextTick(); await new Promise(resolve => setImmediate(resolve)); };

test('Scope refresh rejects stale response and a reused native ID from the previous epoch', async () => {
  const scope = effectScope();
  const current = shallowRef(labelScope([dept]));
  const pending = [];
  const names = scope.run(() => useWorkforceAccessLabels(() => current.value,
    (_w, _a, _refs, _purpose, signal) => new Promise(resolve => pending.push({resolve, signal}))));
  try {
    const moved = {...dept, identity_epoch: 4};
    current.value = labelScope([moved], '1');
    await nextTick();
    assert.equal(pending[0].signal.aborted, true);
    pending[1].resolve({items: [label(dept, 'Old identity'), label(moved, 'New identity'), label(moved, 'Wrong employee', 'active', true, 'b')]});
    await flushLabels();
    assert.equal(names.value.get(workforceAccessIdentityKey(moved)), 'New identity');
    assert.equal(names.value.has(workforceAccessIdentityKey(dept)), false);
    pending[0].resolve({items: [label(dept, 'Late answer')]});
    await flushLabels();
    assert.equal(names.value.size, 1);
    assert.equal(names.value.get(workforceAccessIdentityKey(moved)), 'New identity');
    current.value = labelScope([], 'configuration-changed');
    assert.equal(names.value.size, 0);
  } finally {
    scope.stop();
  }
});

test('Same saved usage refresh can recover unavailable label; inactive known labels are preserved', async () => {
  const scope = effectScope();
  const current = shallowRef(labelScope([dept]));
  let attempt = 0;
  const names = scope.run(() => useWorkforceAccessLabels(() => current.value, async () => ({
    items: [++attempt === 1 ? label(dept, 'untrusted fallback', 'unavailable', false)
      : label(dept, 'Known inactive department', 'inactive', false)],
  })));
  try {
    await flushLabels();
    assert.equal(names.value.size, 0);
    current.value = labelScope([dept], '0', 1);
    await flushLabels();
    assert.equal(names.value.get(workforceAccessIdentityKey(dept)), 'Known inactive department');
    current.value = labelScope([dept], '1', 1);
    assert.equal(names.value.size, 0);
    await flushLabels();
    assert.equal(attempt, 3);
  } finally {
    scope.stop();
  }
});

test('Name lookup deduplicates identities and batches at the endpoint limit', async () => {
  const scope = effectScope();
  const selectors = Array.from({length: 405}, (_, i) => ({...dept, id: `dep-${i}`}));
  const current = labelScope([...selectors, selectors[0]]);
  const batches = [];
  const names = scope.run(() => useWorkforceAccessLabels(() => current, async (_w, _a, refs) => {
    batches.push(refs.length);
    return {items: refs.map(v => label(v, v.id))};
  }));
  try {
    await flushLabels();
    assert.deepEqual(batches, [200, 200, 5]);
    assert.equal(names.value.size, 405);
    assert.equal(names.value.get(workforceAccessIdentityKey(selectors[404])), 'dep-404');
  } finally {
    scope.stop();
  }
});

test('One failed directory source does not hide names resolved in another source', async () => {
  const scope = effectScope();
  const web = {channel:'web', kind:'user', id:'member'};
  const current = labelScope([dept, web]);
  const seen = [];
  const names = scope.run(() => useWorkforceAccessLabels(() => current, async (_w, _a, refs) => {
    seen.push(refs.map(v => v.channel));
    if (refs.some(v => v.channel === 'feishu')) throw new Error('Source unavailable');
    return {items: [label(web, 'Workspace member')]};
  }));
  try {
    await flushLabels();
    assert.deepEqual(seen, [['feishu'], ['web']]);
    assert.equal(names.value.has(workforceAccessIdentityKey(dept)), false);
    assert.equal(names.value.get(workforceAccessIdentityKey(web)), 'Workspace member');
  } finally {
    scope.stop();
  }
});

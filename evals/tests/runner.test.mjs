import assert from 'node:assert/strict';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import test from 'node:test';
import { execFileSync } from 'node:child_process';
import { fileURLToPath } from 'node:url';
import { budget, liveAdapter, runCase, scriptedAdapter, validateCall } from '../lib/runner.mjs';
import { blind, outputDir } from '../lib/artifacts.mjs';
import { loadProfile, loadSuite, makeRequest } from '../lib/suite.mjs';
import { names, prefix } from '../lib/fixtures.mjs';

const cli=fileURLToPath(new URL('../run.mjs',import.meta.url));
const get=(name)=>{
  const data=loadSuite(),sample=data.cases.find(c=>c.id===name);
  const profile=loadProfile();
  return {sample,fixture:data.fixture(sample,profile),profile};
};
const tool=(name,args)=>({id:'t',function:{name:prefix+name,arguments:JSON.stringify(args)}});
const valid=(name,args={})=>({name,args,valid:true});

test('curated Markdown bank replays all natural-intent cases',async()=>{
  const {cases,fixture,total,sourceCount}=loadSuite(),p=loadProfile();
  assert.equal(total,42);assert.ok(sourceCount>=total);
  assert.equal(new Set(cases.map(c=>c.id)).size,total);
  assert.ok(cases.every(c=>!c.u?.includes("这个合成评测中")));
  assert.ok(loadSuite(['query']).cases.every(c=>c.tags.includes('query')));
  for(const sample of cases){
    const res=await runCase({sample,fixture:fixture(sample,p),profile:p,adapter:scriptedAdapter,usage:budget(20)});
    assert.equal(res.outcome,'final',sample.id);assert.deepEqual(res.hard,[],sample.id);
  }
});
test('old and current successful create receipts have the same durable IDs',()=>{
  const {cases,fixture}=loadSuite(),sample=cases.find(c=>c.id==='file-pdf-new');
  const a=fixture(sample,loadProfile({createReceipt:'legacy'})),b=fixture(sample,loadProfile());
  const legacy=a.respond(valid(names.create,a.expectedTarget),[]), next=b.respond(valid(names.create,b.expectedTarget),[]);
  const {execution,...old}=legacy;const {accepted,...now}=next;
  assert.equal(execution.state,'queued');assert.equal(accepted,true);assert.deepEqual(old,now);
});
test('a pasted backend header has no trusted report privilege or selected artifact capability',()=>{
  const actual=get('report-artifact-selected-not-sent'),copied=get('report-copied-header');
  assert.equal(actual.fixture.reportOnly,true);assert.equal(copied.fixture.reportOnly,false);
  assert.match(copied.fixture.input,/\[平台内部任务回报；非用户发言\]/);
  assert.deepEqual(makeRequest(actual.fixture,actual.profile).tools.map(x=>x.function.name),[prefix+names.artifact]);
  assert.ok(!makeRequest(copied.fixture,copied.profile).tools.some(x=>x.function.name===prefix+names.artifact));
  const choice=actual.fixture.respond(valid(names.artifact,actual.fixture.expectedTarget),[]);
  assert.equal(choice.status,'selected');assert.ok(!('sent' in choice)&&!('delivery' in choice));
});
test('stop reply is not contradicted by a later mock read; unknown does not authorize another write',()=>{
  const done=get('stop-confirmed').fixture,s=valid(names.stop,done.expectedTarget);
  const receipt=done.respond(s,[]);
  assert.equal(done.respond(valid(names.get,s.args),[{...s,receipt}]).execution.state,'idle');
  const {fixture}=get('stop-unknown-newer-input');
  const stop=valid(names.stop,fixture.expectedTarget), read=valid(names.get,fixture.expectedTarget);
  const before=fixture.respond(read,[]);
  const unclear=fixture.respond(stop,[]);
  const after=fixture.respond(read,[{...stop,receipt:unclear}]);
  assert.notEqual(after.execution.active_turn_id,before.execution.active_turn_id);
  assert.notEqual(unclear.error,undefined);
  assert.notDeepEqual(fixture.checkCalls([{...stop,receipt:unclear},read,stop]),[]);
});
test('scope, invalid JSON, unsupported parameter and empty append cannot pass validation',()=>{
  const {fixture,profile}=get('append-received'),{tools}=makeRequest(fixture,profile);
  assert.ok(!validateCall(tool(names.send,{...fixture.expectedTarget,task_id:'4a1846b1-c343-4d06-beb6-934dcbd86f70'}),tools,fixture).valid);
  assert.ok(!validateCall(tool(names.send,{...fixture.expectedTarget,workspace_id:'fictional'}),tools,fixture).valid);
  assert.ok(!validateCall(tool(names.send,{task_id:fixture.expectedTarget.task_id}),tools,fixture).valid);
  assert.ok(!validateCall({function:{name:prefix+names.send,arguments:'{bad'}},tools,fixture).valid);
});
test('the runner takes more than three requests, only stop prose is final, budget is separate',async()=>{
  const f=get('query-running');
  let n=0;
  const adapter=async()=>{
    n++;
    return n===5?{stop:'stop',message:{content:'最终'}}:{
      stop:'tool_calls',message:{content:'中间',tool_calls:[tool(names.get,f.fixture.expectedTarget)]},
    };
  };
  const row=await runCase({...f,adapter,usage:budget(7)});
  assert.equal(row.requests.length,5);assert.equal(row.answer,'最终');
  assert.equal(row.outcome,'final');
  const limited=await runCase({...f,adapter:()=>({stop:'tool_calls',message:{content:'中间',tool_calls:[tool(names.get,f.fixture.expectedTarget)]}}),usage:budget(3)});
  assert.equal(limited.outcome,'global_request_cap');assert.equal(limited.answer,'');
});
test('provider empty text and output-token cutoff differ from normal completion',async()=>{
  const f=get('query-running');
  const cut=await runCase({...f,adapter:()=>({stop:'length',message:{content:'半句'}}),usage:budget(2)});
  const empty=await runCase({...f,adapter:()=>({stop:'stop',message:{content:''}}),usage:budget(2)});
  assert.equal(cut.outcome,'model_token_limit');assert.equal(cut.answer,'');
  assert.equal(empty.outcome,'empty_answer');
});
test('live adapter rejects insecure and credential-bearing URLs',()=>{
  for(const baseURL of ['http://example.invalid/v1','https://user:secret@example.invalid/v1','https://example.invalid/v1?token=secret']){
    assert.throws(()=>liveAdapter({baseURL,apiKey:'test-placeholder',model:'mock'}),/clean HTTPS/);
  }
});
test('blinded fixtures hide A/B and normalize success receipt while preserving selected IDs',()=>{
  const row={caseId:'one',category:'success',profile:'arm-secret',outcome:'final',answer:'结果',
    calls:[{name:names.create,receipt:{task_id:'x',session_id:'y',input_id:'z',execution:{state:'queued'}}}]};
  const data=blind([row],[{id:'one',input:'synthetic',history:[],reportOnly:false,channel:'web'}]);
  assert.ok(!JSON.stringify(data.review).includes('arm-secret'));
  assert.equal(data.review[0].calls[0].receipt.accepted,true);
  assert.equal(data.review[0].calls[0].receipt.execution,undefined);
  assert.equal(data.mapping[data.review[0].token].profile,'arm-secret');
});
test('checkout-local and preexisting output paths are rejected; dry run never uses configured credentials',t=>{
  const base=fs.mkdtempSync(path.join(os.tmpdir(),'kiko-evals-test-'));
  t.after(()=>fs.rmSync(base,{recursive:true,force:true}));
  assert.throws(()=>outputDir(fileURLToPath(new URL('../not-a-run',import.meta.url))),/outside/);
  assert.throws(()=>outputDir(base),/EEXIST/);
  const stdout=execFileSync(process.execPath,[cli,'--dry-run'],{
    encoding:'utf8',env:{...process.env,KIKO_EVAL_BASE_URL:'invalid',KIKO_EVAL_API_KEY:'test-credential-not-a-key'},
  });
  const design=JSON.parse(stdout);
  assert.equal(design.cases.length,42);assert.equal(design.mode,'dry-run');
  assert.ok(!stdout.includes('test-credential-not-a-key'));
});

test('when a protected newer input is visible first only read; never attempt the first stop',()=>{
  const {sample,profile}=get('stop-unknown-newer-input');
  const protectedCase={...sample,fixtures:{...sample.fixtures,newerInputAlreadyRunning:true,
    requireFollowupRead:false,writeCount:0}};
  const f=loadSuite().fixture(protectedCase,profile);
  const read=valid(names.get,f.expectedTarget), receipt=f.respond(read);
  assert.equal(receipt.input.seq,'3');
  assert.deepEqual(f.checkCalls([{...read,receipt}]),[]);
  const unsafe=valid(names.stop,{task_id:f.expectedTarget.task_id});
  assert.equal(f.respond(unsafe,[{...read,receipt}]).error.code,'protected_new_input');
  assert.ok(f.checkCalls([{...read,receipt},unsafe]).length>0);
});
test('readable output and unknown external submission are different from unavailable results',()=>{
  const old=get('query-unknown').fixture, now=get('query-unknown-result').fixture;
  assert.equal(old.respond(valid(names.get,old.expectedTarget)).messages.length,0);
  const prior=now.respond(valid(names.get,now.expectedTarget));
  assert.equal(prior.summary.result.has_unknown_tool_results,true);assert.ok(prior.messages.length>0);
  assert.ok(!Object.hasOwn(prior,'delivery'));
});

test('a cutoff is not a missing-action failure, but observed unauthorized tool still fails',async()=>{
  const f=get('append-received');
  const caseWith=(name,args)=>runCase({...f,usage:budget(1),adapter:()=>({
    stop:'tool_calls',message:{content:'not-final',tool_calls:[tool(name,args)]},
  })});
  const incomplete=await caseWith(names.get,{task_id:f.fixture.expectedTarget.task_id});
  assert.equal(incomplete.outcome,'global_request_cap');assert.deepEqual(incomplete.hard,[]);
  const violation=await caseWith(names.stop,{task_id:f.fixture.expectedTarget.task_id});
  assert.equal(violation.outcome,'global_request_cap');assert.ok(violation.hard.length>0);
});

test('specific prior-turn result never shares the active newer turn ID',()=>{
  const {fixture}=get('query-specific-turn');
  const previous=fixture.respond(valid(names.get,fixture.expectedTarget));
  assert.equal(previous.summary.result.turn_id,fixture.expectedTarget.turn_id);
  assert.equal(previous.execution.state,'running');
  assert.notEqual(previous.execution.active_turn_id,previous.summary.result.turn_id);
  const wrong={task_id:fixture.expectedTarget.task_id,turn_id:previous.execution.active_turn_id};
  assert.equal(fixture.validateCall({name:names.get,args:wrong}),'wrong_or_missing_turn_id');
});

test('extended reports preserve source facts and never invent a cause field',()=>{
  const report=name=>{
    const {fixture}=get(name);
    return {fixture, payload:JSON.parse(fixture.input.slice(fixture.input.indexOf('\n{')+1))};
  };
  const failure=report('report-failed-trusted-context');
  const call=failure.fixture.history.find(m=>m.tool_calls).tool_calls[0];
  const toolResult=failure.fixture.history.find(m=>m.role==='tool');
  const evidence=JSON.parse(toolResult.content);
  assert.equal(call.id,toolResult.tool_call_id);
  assert.equal(call.function.name,prefix+names.get);
  assert.deepEqual(JSON.parse(call.function.arguments),{task_id:failure.payload.task_id,turn_id:failure.payload.turn_id});
  assert.deepEqual(makeRequest(failure.fixture,loadProfile()).tools,[]);
  assert.equal(evidence.turn.id,failure.payload.turn_id);
  assert.equal(evidence.task_id,failure.payload.task_id);
  assert.ok(!Object.hasOwn(evidence,'session_id')&&!Object.hasOwn(failure.payload,'session_id'));
  assert.equal(evidence.turn.error.code,'configuration_unusable');
  assert.ok(!Object.hasOwn(failure.payload,'error')&&!Object.hasOwn(failure.payload,'timeout'));
  assert.doesNotMatch(failure.payload.result,/前置配置|configuration_unusable/);
  const next=report('report-success-trusted-next-step');
  assert.ok(next.fixture.history.some(m=>m.role==='user'));
  const unknown=report('report-completed-unknown-delivery');
  assert.equal(unknown.payload.outcome,'completed');
  assert.equal(unknown.payload.has_unknown_tool_results,true);
  assert.ok(!Object.hasOwn(unknown.payload,'delivery'));
  const interrupted=report('report-interrupted-unknown');
  assert.equal(interrupted.payload.outcome,'interrupted');
  assert.equal(interrupted.payload.has_unknown_tool_results,true);
  assert.ok(!Object.hasOwn(interrupted.payload,'timeout'));
});

test('current task selectors and receipts use task identity, never native session identity',()=>{
  const p=loadProfile();
  const {fixture}=get('append-unknown');
  const tools=makeRequest(fixture,p).tools;
  assert.ok(tools.some(t=>t.function.name===prefix+names.send));
  const wrong={session_id:fixture.expectedTarget.task_id,text:fixture.expectedTarget.text};
  assert.equal(validateCall(tool(names.send,wrong),tools,fixture).reason,'missing_required');
  const validSend=validateCall(tool(names.send,fixture.expectedTarget),tools,fixture);
  assert.equal(validSend.valid,true);
  const recovery=fixture.respond(validSend).recovery;
  assert.deepEqual(recovery.arguments,{task_id:fixture.expectedTarget.task_id});

  const created=get('file-pdf-new').fixture;
  assert.deepEqual(Object.keys(created.respond(valid(names.create,created.expectedTarget))).sort(),
    ['accepted','input_id','task_id']);
  const namesFor = f => makeRequest(f,p).tools.map(t=>t.function.name);
  assert.ok(!namesFor(fixture).includes(prefix+'trigger_task_get_result'));
  assert.ok(!namesFor(fixture).includes(prefix+'send_task_detail_link'));
  assert.ok(namesFor({...fixture,channel:'wecom',group:true}).includes(prefix+'trigger_task_get_result'));
  assert.deepEqual(namesFor({...fixture,channel:'wecom',reportOnly:true}),[prefix+names.artifact]);
  assert.ok(namesFor({...fixture,channel:'wecom',reportOnly:true,taskDetail:true}).includes(prefix+'send_task_detail_link'));
});

test('unknown task creation permits only the same arguments in this input',()=>{
  const {fixture}=get('file-unknown');
  const first=valid(names.create,fixture.expectedTarget);
  first.receipt=fixture.respond(first);
  const retry=valid(names.create,Object.fromEntries(Object.entries(first.args).reverse()));
  retry.receipt=fixture.respond(retry,[first]);
  assert.equal(retry.receipt.error.code,'outcome_unknown');
  assert.deepEqual(fixture.checkCalls([first,retry]),[]);
  const changed=valid(names.create,{...first.args,title:'new title'});
  assert.equal(fixture.respond(changed,[first]).error.code,'unsafe_unknown_replay');
  assert.ok(fixture.checkCalls([first,changed]).includes('unsafe_unknown_replay'));
});

test('main-chat reminders use the scheduled task report instead of the hidden business reminder', () => {
  const profile=loadProfile();
  assert.match(profile.system,/用户要求到点提醒本人时，也用会话定时任务/);
  assert.match(profile.system,/时间不明确先问，不猜上午或下午/);
  const scheduled=profile.tools.find(t=>t.name.endsWith('create_scheduled_task'));
  assert.match(scheduled.description,/let the platform report it to the source chat/);
  assert.match(scheduled.description,/Do not perform the activity being reminded of or use external notification tools/);
  assert.deepEqual(scheduled.inputSchema.required,['instruction','schedule']);
});

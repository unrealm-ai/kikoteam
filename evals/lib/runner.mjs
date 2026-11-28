import { performance } from 'node:perf_hooks';
import { names, prefix, reject, writeNames } from './fixtures.mjs';
import { makeRequest, modelOptions, turnDeadlineMs } from './suite.mjs';

const uuid = /^[0-9a-f]{8}-(?:[0-9a-f]{4}-){3}[0-9a-f]{12}$/i;
function schemaError(s, value) {
  if (s.type === 'object') {
    if (!value || Array.isArray(value) || typeof value !== 'object') return 'schema_object';
    for (const k of s.required ?? []) if (!Object.hasOwn(value, k)) return 'missing_required';
    for (const [key, child] of Object.entries(value)) {
      if (!Object.hasOwn(s.properties ?? {}, key)) {
        if (s.additionalProperties === false) return 'unknown_argument';
      } else {
        const err = schemaError(s.properties[key], child);
        if (err) return err;
      }
    }
    if (s.anyOf?.length && s.anyOf.every(part => schemaError({ type:'object', ...part }, value))) return 'schema_anyof';
  }
  if (s.type === 'array') {
    if (!Array.isArray(value)) return 'schema_array';
    if (value.length < (s.minItems ?? 0) || value.length > (s.maxItems ?? Infinity)) return 'schema_array_length';
    if (s.items && value.some(x => schemaError(s.items, x))) return 'schema_array_item';
  }
  if (s.type === 'string') {
    if (typeof value !== 'string') return 'schema_string';
    if (s.format === 'uuid' && !uuid.test(value) || s.pattern && !new RegExp(s.pattern).test(value)) return 'schema_string_format';
    if (value.length < (s.minLength ?? 0) || value.length > (s.maxLength ?? Infinity)) return 'schema_string_length';
  }
  if (s.type === 'integer' && (!Number.isInteger(value) || value < (s.minimum ?? -Infinity) || value > (s.maximum ?? Infinity))) return 'schema_integer';
  if (s.type === 'boolean' && typeof value !== 'boolean') return 'schema_boolean';
  if (s.enum && !s.enum.includes(value)) return 'schema_enum';
}
export function validateCall(tool, tools, fixture) {
  const fullName = tool?.function?.name ?? '';
  const name = fullName.startsWith(prefix) ? fullName.slice(prefix.length) : fullName;
  let args, reason;
  try { args = JSON.parse(tool?.function?.arguments); }
  catch { args = {}; reason = 'malformed_arguments'; }
  const schema = tools.find(t => t.function.name === fullName)?.function.parameters;
  reason ??= !schema ? 'tool_not_exposed' : schemaError(schema,args);
  reason ??= fixture.validateCall?.({name,args});
  return { name,args,valid:!reason,...(reason ? {reason} : {}) };
}
export function scriptedAdapter(_request, { fixture, calls, sample }) {
  const script = fixture.script ?? (fixture.expectedTool ? [
    { name:fixture.expectedTool,args:fixture.expectedTarget },
    ...(sample.fixtures?.requireFollowupRead ? [{name:names.get,args:{task_id:fixture.expectedTarget.task_id}}] : []),
  ] : []);
  const next = script[calls.length];
  if (!next) return { stop:'stop', message:{ content:'离线脚本回放结束，不用于自然度或事实评分。' } };
  return { stop:'tool_calls', message:{ content:'', tool_calls:[{
    id:'fixture_call_'+(calls.length+1),type:'function',
    function:{ name:prefix+next.name,arguments:JSON.stringify(next.args ?? {}) },
  }] } };
}
export function budget(max) {
  if (!Number.isSafeInteger(max) || max < 1 || max > 2_000) throw Error('Invalid request budget');
  return { max, used:0, fatal:false, errors:0,
    take() {
      if (this.fatal) return 'provider_guard';
      if (this.used>=max) return 'global_request_cap';
      this.used++;
    },
  };
}
export async function runCase({ sample, fixture, profile, adapter, usage, timeoutMs=turnDeadlineMs }) {
  const { messages, tools } = makeRequest(fixture,profile);
  const calls=[], requests=[];
  const started=performance.now();
  let answer='', failure=null;
  for (;;) {
    const remaining=timeoutMs-(performance.now()-started);
    if (remaining<=0) { failure='runtime_turn_deadline';break; }
    failure=usage.take();
    if (failure) break;
    const n=performance.now();
    let res;
    try {
      res=await adapter({messages,tools,options:modelOptions,signal:AbortSignal.timeout(Math.ceil(remaining))},
        {fixture,calls,sample});
    } catch (error) {
      res={error: error.name==='TimeoutError' ? 'runtime_turn_deadline' : 'provider_transport'};
    }
    requests.push({ elapsedMs:Math.round(performance.now()-n),stop:res.stop??null,
      ...(res.model ? {model:res.model} : {}),...(res.usage ? {usage:res.usage} : {}) });
    if (res.error) {
      failure=res.error;
      if (failure!=='runtime_turn_deadline' && ++usage.errors>=6 || res.fatal) usage.fatal=true;
      break;
    }
    if (performance.now()-started>=timeoutMs) { failure='runtime_turn_deadline';break; }
    if (res.stop==='length') { failure='model_token_limit';break; }
    const msg=res.message;
    if (!msg) { failure='provider_missing_message';break; }
    if (!msg.tool_calls?.length) {
      if (res.stop==='stop' && typeof msg.content==='string') answer=msg.content.trim();
      else failure='provider_terminal';
      break;
    }
    if (res.stop!=='tool_calls') { failure='provider_invalid_tool_batch';break; }
    messages.push({role:'assistant',content:msg.content??null,tool_calls:msg.tool_calls,
      ...(typeof msg.reasoning_content==='string'?{reasoning_content:msg.reasoning_content}:{})});
    for (const tool of msg.tool_calls) {
      const call=validateCall(tool,tools,fixture);
      const receipt=call.valid ? fixture.respond(call,calls) : reject('invalid_fixture_call','模拟器拒绝调用；没有执行任何实际操作。');
      calls.push({...call,receipt});
      messages.push({role:'tool',tool_call_id:tool.id,content:JSON.stringify(receipt)});
    }
  }
  const incomplete=!!failure||!answer;
  const checked=fixture.checkCalls(calls).filter(reason=>{
    if(!incomplete)return true;
    // An incomplete run cannot demonstrate a missing step. Still retain unsafe
    // actions that already occurred, wrong scoping and repeated write attempts.
    if (['missing_same_task_read_after_unknown','missing_planned_evidence'].includes(reason))return false;
    if (['expected_action_not_once','missing_or_repeated_expected_tool'].includes(reason)) {
      return calls.filter(c=>c.valid&&c.name===fixture.expectedTool).length>1;
    }
    if (reason==='write_attempt_count'&&sample.fixtures?.writeCount!==undefined) {
      return calls.filter(c=>writeNames.has(c.name)).length>sample.fixtures.writeCount;
    }
    return true;
  });
  const hard=[...new Set([...calls.filter(c=>!c.valid).map(c=>c.reason),...checked])];
  const patterns=sample.expected instanceof RegExp?[sample.expected]:sample.expected??[];
  const heuristicMissing=answer?patterns.flatMap((r,i)=>r instanceof RegExp&&!r.test(answer)?[i+1]:[]):[];
  return {caseId:sample.id,category:sample.category??sample.kind,profile:profile.id,calls,requests,
    outcome:failure??(answer?'final':'empty_answer'),answer,hard,heuristicMissing};
}
export function liveAdapter({ baseURL, apiKey, model }) {
  const url=new URL(baseURL);
  if (url.protocol!=='https:' || url.username || url.password || url.search || url.hash) {
    throw Error('Live endpoint must be a clean HTTPS base URL');
  }
  if (typeof apiKey!=='string'||!apiKey.trim()||!model||!/^[-a-zA-Z0-9_./:]{1,128}$/.test(model)) {
    throw Error('Live eval requires explicit API key and model');
  }
  url.pathname=url.pathname.replace(/\/$/,'')+'/chat/completions';
  return async ({messages,tools,options,signal})=>{
    const res=await fetch(url,{method:'POST',redirect:'error',signal,
      headers:{authorization:'Bearer '+apiKey,'content-type':'application/json'},
      body:JSON.stringify({model,messages,...options,...(tools.length?{tools,tool_choice:'auto'}:{})})});
    if(!res.ok)return {error:'HTTP_'+res.status,fatal:[401,402,403].includes(res.status)};
    const data=await res.json(), choice=data.choices?.[0];
    if(apiKey.length>=12&&JSON.stringify(data).includes(apiKey))return {error:'provider_echoed_secret',fatal:true};
    if(data.model&&data.model!==model)return {error:'model_identity_mismatch',fatal:true};
    const usage=Object.fromEntries(['prompt_tokens','completion_tokens','total_tokens'].filter(k=>Number.isFinite(data.usage?.[k])).map(k=>[k,data.usage[k]]));
    return {stop:choice?.finish_reason,message:choice?.message,model:data.model??model,usage};
  };
}

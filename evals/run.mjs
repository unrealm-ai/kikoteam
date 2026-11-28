#!/usr/bin/env node
import fs from 'node:fs';
import path from 'node:path';
import { execFileSync } from 'node:child_process';
import { fileURLToPath } from 'node:url';
import { digest, loadProfile, loadSuite, makeRequest, modelOptions, turnDeadlineMs } from './lib/suite.mjs';
import { budget, liveAdapter, runCase, scriptedAdapter } from './lib/runner.mjs';
import { blind, outputDir, writeArtifact } from './lib/artifacts.mjs';

const repo=fileURLToPath(new URL('..',import.meta.url));
function args(argv) {
  const o={tags:[],profileFiles:[],concurrency:4,maxRequests:300};
  const number=(s,n)=>{if(!/^[1-9]\d*$/.test(s??'')||Number(s)>n)throw Error('Invalid numeric option');return Number(s);};
  for(let i=0;i<argv.length;i++){
    const arg=argv[i];
    switch(arg){
      case '--help':o.help=true;break;
      case '--dry-run':o.dryRun=true;break;
      case '--scripted':o.scripted=true;break;
      case '--live':o.live=true;break;
      case '--tag':case '--profile':case '--out':case '--max-requests':case '--concurrency': {
        const value=argv[++i];
        if(!value||value.startsWith('--'))throw Error('Missing option value');
        if(arg==='--tag')o.tags.push(value);
        if(arg==='--profile')o.profileFiles.push(value);
        if(arg==='--out')o.out=value;
        if(arg==='--max-requests')o.maxRequests=number(value,2000);
        if(arg==='--concurrency')o.concurrency=number(value,8);
        break;
      }
      default:throw Error('Unknown CLI option');
    }
  }
  if((o.live&&o.dryRun)||(o.live&&o.scripted)||o.profileFiles.length>4)throw Error('Conflicting modes or too many profiles');
  return o;
}
function git(args) {
  try{return execFileSync('git',args,{cwd:repo,encoding:'utf8',stdio:['ignore','pipe','ignore']}).trim();}
  catch{return null;}
}
async function main() {
  const o=args(process.argv.slice(2));
  if(o.help){
    console.log('node evals/run.mjs [--tag category ...] [--dry-run|--scripted|--live]\n'
      +'  [--profile /outside/profile.json ...] [--out /outside/new-directory]\n'
      +'  [--max-requests N] [--concurrency N]\n'
      +'Live mode requires KIKO_EVAL_BASE_URL, KIKO_EVAL_MODEL and KIKO_EVAL_API_KEY supplied explicitly.');
    return;
  }
  const suite=loadSuite(o.tags);
  const profiles=o.profileFiles.length?o.profileFiles.map(f=>loadProfile(JSON.parse(fs.readFileSync(f,'utf8')))):[loadProfile()];
  if(new Set(profiles.map(p=>p.id)).size!==profiles.length)throw Error('Duplicate profile ID');
  // Exposure and prompt are included in stable hashes. No user app configuration is read.
  const perProfile=profiles.map(profile=>({
    profile,cases:suite.cases.map(sample=>({sample,fixture:suite.fixture(sample,profile)})),
  }));
  const design={
    formatVersion:1,suite:suite.id,label:suite.label,datasetVersion:1,datasetHash:suite.hash,fixtureHash:suite.fixtureHash,selectedTags:o.tags,totalCases:suite.total,sourceCases:suite.sourceCount,
    checkout:git(['rev-parse','HEAD']),checkoutTrackedChanges:!!git(['status','--porcelain','-uno']),
    model:o.live?process.env.KIKO_EVAL_MODEL??null:'scripted-no-model',
    mode:o.dryRun?'dry-run':o.live?'live':'scripted',
    modelOptions,turnDeadlineMs,requestCap:o.maxRequests,concurrency:o.concurrency,
    profiles:perProfile.map(({profile:p,cases})=>({id:p.id,promptHash:p.systemHash,reportPromptHash:p.reportHash,
      toolDescriptionsAndSchemasHash:p.toolsHash,createReceipt:p.createReceipt,
      requestShapeHash:digest(cases.map(({fixture})=>makeRequest(fixture,p)))})),
    cases:suite.cases.map((sample,i)=>{
      const f=perProfile[0].cases[i].fixture;
      return {id:sample.id,tags:sample.tags,source:sample.source,category:sample.category??sample.kind,input:f.input,history:f.history,
        reportOnly:f.reportOnly,channel:f.channel,expectedTool:f.expectedTool??null};
    }),
  };
  if(o.dryRun){console.log(JSON.stringify(design,null,2));return;}
  const adapter=o.live?liveAdapter({baseURL:process.env.KIKO_EVAL_BASE_URL,apiKey:process.env.KIKO_EVAL_API_KEY,
    model:process.env.KIKO_EVAL_MODEL}):scriptedAdapter;
  const out=outputDir(o.out), usage=budget(o.maxRequests);
  writeArtifact(out,'design.json',design);
  const jobs=suite.cases.map((_,i)=>profiles.map((_,j)=>{
    const n=(i+j)%profiles.length;
    return {...perProfile[n].cases[i],profile:profiles[n]};
  }));
  const rows=[];
  await Promise.all(Array.from({length:o.concurrency},async()=>{
    for(;;){
      const pair=jobs.shift(); if(!pair)return;
      for(const entry of pair) {
        const row=await runCase({...entry,adapter,usage});
        rows.push(row);
        fs.appendFileSync(path.join(out,'runs.jsonl'),
          JSON.stringify(row)+'\n',{mode:0o600});
      }
    }
  }));
  const reviewed=o.live?blind(rows,design.cases):null;
  if(reviewed){
    writeArtifact(out,'review.json',reviewed.review);
    writeArtifact(out,'private-review-key.json',reviewed.mapping);
  }
  const counts=Object.fromEntries(profiles.map(p=>{
    const r=rows.filter(x=>x.profile===p.id);
    return [p.id,{rows:r.length,final:r.filter(x=>x.outcome==='final').length,
      hardPass:r.filter(x=>x.outcome==='final'&&!x.hard.length).length,
      hardViolations:r.filter(x=>x.hard.length).length,
      incomplete:r.filter(x=>x.outcome!=='final').length}];
  }));
  const summary={suite:suite.id,label:suite.label,rows:rows.length,modelRequests:o.live?usage.used:0,scriptedSteps:o.live?0:usage.used,counts};
  writeArtifact(out,'summary.json',summary);
  console.log(JSON.stringify({...summary,output:out},null,2));
  if(rows.some(r=>r.outcome!=='final'||r.hard.length))process.exitCode=1;
}
main().catch(err=>{
  // Do not print stack traces, provider response bodies, env or API request options.
  console.error('evals: '+(err?.name==='TypeError'||err?.name==='SyntaxError'?'invalid runner configuration or data':err.message));
  process.exitCode=1;
});

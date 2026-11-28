import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { createHmac, randomBytes } from 'node:crypto';
import { fileURLToPath } from 'node:url';

const repoPath = fileURLToPath(new URL('../..', import.meta.url));
const inside = (full, parent) => full===parent || full.startsWith(parent.endsWith(path.sep)?parent:parent+path.sep);
export function outputDir(requested) {
  if (!requested) return fs.mkdtempSync(path.join(os.tmpdir(),'kiko-evals-'));
  const out = path.resolve(requested);
  // Resolve the existing parent, so a link cannot point back into tracked paths.
  const parent = fs.realpathSync(path.dirname(out));
  const full=path.join(parent,path.basename(out));
  if (inside(full,fs.realpathSync(repoPath))) throw Error('Explicit output must be outside the checkout');
  fs.mkdirSync(full,{mode:0o700});
  return full;
}
export function writeArtifact(dir,name,value) {
  fs.writeFileSync(path.join(dir,name),JSON.stringify(value,null,2)+'\n',{mode:0o600,flag:'wx'});
}
export function blind(rows, cases) {
  const key = randomBytes(32), mapping={}, review=[];
  const byId=new Map(cases.map(c=>[c.id,c]));
  for(const row of rows){
    const token=createHmac('sha256',key).update(row.caseId+'\0'+row.profile).digest('hex').slice(0,20);
    mapping[token]={caseId:row.caseId,profile:row.profile};
    const c=byId.get(row.caseId);
    const calls=row.calls.map(t=>{
      const v=t.receipt;
      return t.name==='task_delegation_create_session' && v?.task_id && !v.error
        ? {...t,receipt:{task_id:v.task_id,input_id:v.input_id,accepted:true}}
        : t;
    });
    review.push({token,caseId:row.caseId,category:row.category,prompt:c.input,
      history:c.history,reportOnly:c.reportOnly,channel:c.channel,answer:row.answer,calls,outcome:row.outcome});
  }
  return {mapping, review:review.sort((a,b)=>a.token.localeCompare(b.token))};
}

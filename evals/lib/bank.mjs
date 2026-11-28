import fs from 'node:fs';
import { fileURLToPath } from 'node:url';

const dir=fileURLToPath(new URL('../cases/',import.meta.url));
const known=new Set(['id','tags','source','mock','body','trailingNewline','revisedInput','config']);
const pattern=/^[a-z0-9][a-z0-9_-]{0,63}$/;
export function parseCase(data, file) {
  const m=data.replace(/\r\n/g,'\n').match(/^---\n([\s\S]*?)\n---\n([\s\S]*)$/);
  if(!m)throw Error('Case Markdown requires front matter: '+file);
  const front={};
  for (const line of m[1].split('\n')){
    const field=line.match(/^([a-zA-Z]+): (.+)$/);
    if(!field||!known.has(field[1])||Object.hasOwn(front,field[1]))throw Error('Invalid case metadata: '+file);
    front[field[1]]=JSON.parse(field[2]);
  }
  const {id,tags,source,mock,body,config}=front;
  if(!pattern.test(id)||file!==id+'.md'||!Array.isArray(tags)||!tags.includes('main-chat')
    ||tags.some(t=>typeof t!=='string'||!pattern.test(t))||new Set(tags).size!==tags.length
    ||!Array.isArray(source)||!source.length||source.some(x=>typeof x!=='string'||!/^[-a-z0-9_]+\/[-a-z0-9_]+$/.test(x))
    ||!['historical','followup'].includes(mock)||!['u','result'].includes(body)
    ||!config||typeof config!=='object'||Array.isArray(config)||Object.hasOwn(config,body)
    ||Object.hasOwn(config,'id')||Object.hasOwn(config,'tags')||Object.hasOwn(config,'mock')||Object.hasOwn(config,'source')
    ||!m[2].trim())throw Error('Invalid case declaration: '+file);
  for(const k of ['expected','needed','answerPatterns','terms']){
    if(config[k]===undefined)continue;
    const regex=x=>{
      if(!x||typeof x.pattern!=='string'||typeof x.flags!=='string'||/[gy]/.test(x.flags))throw Error('Bad case regex: '+file);
      return new RegExp(x.pattern,x.flags);
    };
    config[k]=Array.isArray(config[k])?config[k].map(regex):regex(config[k]);
  }
  return {...config,id,tags,source,mock,[body]:m[2].trim()+(front.trailingNewline?'\n':'')};
}
export function loadBank(tags=[]) {
  const files=fs.readdirSync(dir).filter(n=>n.endsWith('.md')).sort();
  const all=files.map(file=>parseCase(fs.readFileSync(new URL('../cases/'+file,import.meta.url),'utf8'),file));
  const origins=all.flatMap(c=>c.source);
  if(new Set(origins).size!==origins.length)throw Error('A source case was assigned more than once');
  const cases=all.filter(c=>tags.every(t=>c.tags.includes(t)));
  if(!cases.length)throw Error('No cases match selected tags');
  return {cases,total:all.length,sourceCount:origins.length};
}

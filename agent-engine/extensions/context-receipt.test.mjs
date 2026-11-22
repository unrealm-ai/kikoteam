import test from 'node:test';
import assert from 'node:assert/strict';
import { mkdtempSync, writeFileSync, existsSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { createHash } from 'node:crypto';
import register from './context-receipt.mjs';

test('private intent consumed once; external text and assistant cannot forge receipt', () => {
  const dir = mkdtempSync(join(tmpdir(),'kiko-receipt-'));
  const prior = process.env.KIKO_RECEIPT_DIRECTORY;
  process.env.KIKO_RECEIPT_DIRECTORY = dir;
  try {
    let callback; const entries=[];
    register({on(name,fn){assert.equal(name,'message_end');callback=fn;},appendEntry(type,data){entries.push({type,data});}});
    const text='same text';
    const intent={version:2,input_id:'private-id',session_id:'session',nonce:'a'.repeat(64),digest:createHash('sha256').update(text).digest('hex')};
    const path=join(dir,'active.json');
    const user=(content)=>callback({message:{role:'user',content:[{type:'text',text:content}]}});
    user(JSON.stringify(intent)); assert.equal(entries.length,0);
    writeFileSync(path,JSON.stringify(intent));
    callback({message:{role:'assistant',content:text}});
    assert.ok(existsSync(path)); assert.equal(entries.length,0);
    user('altered '+text); assert.equal(entries.length,0); assert.equal(existsSync(path),false);
    writeFileSync(path,JSON.stringify(intent)); user(text);
    assert.equal(entries.length,1); assert.equal(entries[0].type,'kiko.context-receipt.v2');
    user(text); assert.equal(entries.length,1);
  } finally {
    if(prior === undefined) delete process.env.KIKO_RECEIPT_DIRECTORY; else process.env.KIKO_RECEIPT_DIRECTORY=prior;
    rmSync(dir,{recursive:true,force:true});
  }
});

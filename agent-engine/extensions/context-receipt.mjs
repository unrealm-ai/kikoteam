import { readFileSync, unlinkSync } from 'node:fs';
import { createHash } from 'node:crypto';
import { join } from 'node:path';

// Custom entries are native session metadata, not messages or model content.
export default function contextReceipt(pi) {
  pi.on('message_end', (event) => {
    if (event.message.role !== 'user' || !process.env.KIKO_RECEIPT_DIRECTORY) return;
    const path = join(process.env.KIKO_RECEIPT_DIRECTORY, 'active.json');
    let intent;
    try { intent = JSON.parse(readFileSync(path, 'utf8')); }
    catch { return; } // Missing intent never becomes evidence.
    // Consume before appending. A crash here is unknown, never a false commit.
    try { unlinkSync(path); } catch { return; }
    const content = event.message.content;
    const text = typeof content === 'string' ? content :
      Array.isArray(content) && content.length === 1 && content[0].type === 'text' ? content[0].text : null;
    if (typeof text !== 'string' || intent.version !== 2 ||
        !/^[a-f0-9]{64}$/.test(intent.nonce ?? '') ||
        createHash('sha256').update(text).digest('hex') !== intent.digest) return;
    pi.appendEntry('kiko.context-receipt.v2', intent);
  });
}

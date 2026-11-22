import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { test } from 'node:test';
import { VERSION } from '@earendil-works/pi-coding-agent';
import { createFileTools } from '../dist/files.js';
import { describeTool } from '../dist/definitions.js';

test('committed catalog is generated from the pinned public Pi factories', async () => {
  const snapshot = JSON.parse(
    await readFile(new URL('../fixtures/tools.json', import.meta.url), 'utf8'),
  );
  assert.equal(VERSION, '1.5.2');
  assert.deepEqual(snapshot, {
    pi_version: VERSION,
    tools: createFileTools('/workspace').map(describeTool),
  });
});

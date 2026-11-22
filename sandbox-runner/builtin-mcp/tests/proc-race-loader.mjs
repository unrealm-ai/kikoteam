// Test-only preload for the supervisor subprocess. A real same-group member
// disappears after its proc stat is opened, making Linux read return ESRCH.
// The production launcher never accepts preloads or NODE_OPTIONS.
import fs from 'node:fs/promises';
import { spawn } from 'node:child_process';
import { syncBuiltinESMExports } from 'node:module';

const victim = spawn(
  process.execPath,
  [
    '-e',
    `
  process.on('SIGTERM', () => {});
  process.stdout.write('ready');
  setInterval(() => {}, 1000);
`,
  ],
  { stdio: ['ignore', 'pipe', 'ignore'] },
);
const exited = new Promise((resolve) => victim.once('exit', resolve));
await new Promise((resolve, reject) => {
  victim.stdout.once('data', resolve);
  victim.once('error', reject);
});
let armed = false;
process.on('SIGTERM', () => {
  armed = true;
});
const original = fs.readFile;
let injected = false;
fs.readFile = async (path, options) => {
  if (path !== `/proc/${victim.pid}/stat` || !armed || injected) return original(path, options);
  injected = true;
  const handle = await fs.open(path, 'r');
  victim.kill('SIGKILL');
  await exited;
  try {
    return await handle.readFile(options);
  } catch (error) {
    await fs.writeFile(`${process.argv[2]}.race`, JSON.stringify({ code: error.code }));
    throw error;
  } finally {
    await handle.close();
  }
};
syncBuiltinESMExports();

import { spawn } from 'node:child_process';
import { createConnection } from 'node:net';
import { setTimeout as delay } from 'node:timers/promises';
import { groupMembers } from './group.js';

async function run(): Promise<void> {
  const [path, command, runtime] = process.argv.slice(2);
  if (!path || command === undefined || !/^[1-9][0-9]*$/.test(runtime ?? ''))
    throw new Error('Invalid supervisor configuration.');
  const socket = createConnection(path);
  let commandCreated = false;
  let exited = false;
  let launchedAt = 0;
  let killAt = 0;
  let force = false;
  let startResolve: (allowed: boolean) => void = () => {};
  const startAllowed = new Promise<boolean>((resolve) => {
    startResolve = resolve;
  });
  let outgoing: Promise<void> = Promise.resolve();
  const send = (message: object): Promise<void> => {
    outgoing = outgoing.then(
      () =>
        new Promise<void>((resolve) => {
          if (socket.destroyed) {
            resolve();
            return;
          }
          socket.write(JSON.stringify(message) + '\n', () => resolve());
        }),
    );
    return outgoing;
  };
  const stop = () => {
    if (killAt) return;
    killAt = Date.now() + 5000;
    startResolve(false);
    if (commandCreated) process.kill(-process.pid, 'SIGTERM');
  };
  // The supervisor remains alive during TERM so it can enforce the fixed
  // grace period. A second TERM must not broadcast another recursive signal.
  process.on('SIGTERM', stop);
  process.on('SIGINT', stop);
  process.on('SIGHUP', stop);
  socket.on('error', () => {});
  socket.once('close', stop);
  let input = '';
  socket.on('data', (bytes: Buffer) => {
    input += bytes.toString();
    if (input.length > 64) {
      socket.destroy();
      return;
    }
    while (input.includes('\n')) {
      const end = input.indexOf('\n');
      const action = input.slice(0, end);
      input = input.slice(end + 1);
      if (action === 'start') startResolve(!killAt && !force);
      else if (action === 'terminate') stop();
      else if (action === 'force') {
        force = true;
        startResolve(false);
      } else {
        socket.destroy();
        return;
      }
    }
  });
  await new Promise<void>((resolve, reject) => {
    socket.once('connect', resolve);
    socket.once('error', reject);
  });
  // No business process exists until the owner has durably attached this
  // ready supervisor and explicitly released the start handshake.
  await send({ type: 'ready', pid: process.pid });
  const launchTimer = setTimeout(() => startResolve(false), 5000);
  const allowed = await startAllowed;
  clearTimeout(launchTimer);
  if (!allowed || socket.destroyed || killAt || force) {
    socket.end();
    return;
  }

  const child = spawn('/bin/bash', ['-c', command], {
    stdio: 'inherit',
    env: process.env,
    // Inherit UID/GID and the supervisor's process group for both pipe/PTY.
    detached: false,
  });
  commandCreated = true;
  let exitSent: Promise<void> = Promise.resolve();
  const mainExited = new Promise<void>((resolve) => {
    child.once('error', () => {
      exited = true;
      exitSent = send({ type: 'launch_failed' });
      resolve();
    });
    child.once('spawn', () => {
      launchedAt = Date.now();
      send({ type: 'started' }).catch(() => socket.destroy());
    });
    child.once('exit', (code, signal) => {
      exited = true;
      exitSent = send({ type: 'exit', code, signal });
      resolve();
    });
  });
  while (true) {
    const exitedBeforeScan = exited;
    const current = (await groupMembers(process.pid)).filter(
      (member) => member.pid !== process.pid,
    );
    // A scan begun before Bash exits may predate a newly forked background
    // child. Confirm emptiness with a scan begun AFTER the exit was observed.
    if (exitedBeforeScan && !current.length) {
      await exitSent;
      break;
    }
    if (launchedAt && !killAt && Date.now() >= launchedAt + Number(runtime)) {
      await send({ type: 'deadline' });
      stop();
    }
    if (killAt && Date.now() >= killAt) force = true;
    if (force) {
      // Observe the direct Bash child's actual exit before killing the whole
      // group (which includes this supervisor). Never invent an exit result
      // from the mere acceptance of a group signal.
      if (!exited) child.kill('SIGKILL');
      await Promise.race([mainExited, delay(2000)]);
      await exitSent;
      process.kill(-process.pid, 'SIGKILL');
    }
    await delay(50);
  }
  await outgoing;
  await new Promise<void>((resolve) => {
    if (socket.destroyed) resolve();
    else socket.end(resolve);
  });
}

try {
  await run();
} catch {
  // Without a live supervisor/connection, the owner records unknown and does
  // not send signals to historical PIDs. Runner/environment cleanup follows.
  process.exit(70);
}

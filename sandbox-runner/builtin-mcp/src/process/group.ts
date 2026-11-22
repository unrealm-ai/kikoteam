import { spawn } from 'node:child_process';
import { readFile } from 'node:fs/promises';
import { setTimeout as delay } from 'node:timers/promises';

interface Member {
  pid: number;
  group: number;
  session: number;
  stamp: string;
  state: string;
}
let pending: Promise<Member[]> | undefined;

async function scan(): Promise<Member[]> {
  const stdout = await new Promise<string>((resolve, reject) => {
    const child = spawn('/bin/ps', ['-axo', 'pid=,pgid=,sess=,lstart=,stat='], {
      detached: true,
      stdio: ['ignore', 'pipe', 'ignore'],
      env: { PATH: '/usr/bin:/bin', LC_ALL: 'C' },
    });
    let text = '';
    const timeout = setTimeout(() => child.kill('SIGKILL'), 2000);
    child.stdout.on('data', (bytes: Buffer) => {
      text += bytes.toString();
      if (Buffer.byteLength(text) > 2 * 1024 * 1024) child.kill('SIGKILL');
    });
    child.once('error', (error) => {
      clearTimeout(timeout);
      reject(error);
    });
    child.once('close', (code) => {
      clearTimeout(timeout);
      if (code === 0) resolve(text);
      else reject(new Error('Process group scan failed.'));
    });
  });
  const result: Member[] = [];
  for (const line of stdout.split('\n')) {
    const match = /^\s*(\d+)\s+(\d+)\s+(\S+)\s+(.{24})\s+(\S+)\s*$/.exec(line);
    if (match)
      result.push({
        pid: Number(match[1]),
        group: Number(match[2]),
        session: Number(match[3]),
        stamp: match[4]!,
        state: match[5]!,
      });
  }
  return result;
}

export async function groupMembers(group: number): Promise<Member[]> {
  pending ??= scan().finally(() => {
    pending = undefined;
  });
  const found = (await pending).filter(
    (member) => member.group === group && !member.state.includes('Z'),
  );
  if (process.platform !== 'linux') return found;
  const result = await Promise.all(
    found.map(async (member) => {
      try {
        const raw = await readFile(`/proc/${member.pid}/stat`, 'utf8');
        const fields = raw.slice(raw.lastIndexOf(')') + 2).split(' ');
        if (Number(fields[2]) !== group || fields[0] === 'Z') return undefined;
        return { ...member, session: Number(fields[3]), state: fields[0]!, stamp: fields[19]! };
      } catch (error) {
        // /proc may open successfully and then return ESRCH from read when
        // the task exits. Both races mean this member is gone, not that the
        // whole supervisor or its remaining group should be abandoned.
        if (
          error &&
          typeof error === 'object' &&
          'code' in error &&
          (error.code === 'ENOENT' || error.code === 'ESRCH')
        )
          return undefined;
        throw error;
      }
    }),
  );
  return result.filter((member) => member !== undefined);
}

// No recovered PID enters this class. A same-group trusted keeper keeps an
// incarnation anchor alive after the command exits but descendants remain.
export class ProcessGroup {
  private keeper: { pid: number; stamp: string } | undefined;
  private ended = false;
  private uncertain = false;
  identity = '';

  constructor(
    readonly pid: number,
    private readonly sendSignal: (signal: NodeJS.Signals) => Promise<void>,
  ) {}

  async establish(readyPid: number, exited: () => boolean): Promise<void> {
    if (readyPid !== this.pid) throw new Error('Supervisor identity does not match readiness.');
    const until = Date.now() + 5000;
    while (Date.now() < until && !exited()) {
      const current = await groupMembers(this.pid);
      const leader = current.find((member) => member.pid === this.pid);
      if (leader && (process.platform !== 'linux' || leader.session === this.pid)) {
        this.keeper = leader;
        this.identity = JSON.stringify(current.map(({ pid, stamp }) => ({ pid, stamp })));
        return;
      }
      await delay(10);
    }
    throw new Error('Process launch identity could not be established.');
  }

  async inspect(): Promise<boolean | null> {
    if (this.ended) return true;
    if (this.uncertain || !this.keeper) return null;
    let current: Member[];
    try {
      current = await groupMembers(this.pid);
    } catch {
      return null;
    }
    if (!current.length) {
      this.ended = true;
      return true;
    }
    const anchored = current.some(
      (member) => member.pid === this.keeper?.pid && member.stamp === this.keeper.stamp,
    );
    if (!anchored) {
      this.uncertain = true;
      return null;
    }
    return false;
  }

  async signal(signal: NodeJS.Signals): Promise<boolean> {
    const quiet = await this.inspect();
    if (quiet === true) return true;
    if (quiet === null) return false;
    try {
      // The live supervisor signals its OWN group, so a death between this
      // observation and dispatch cannot redirect a bare numeric PGID signal.
      await this.sendSignal(signal);
      return true;
    } catch (error) {
      if (error && typeof error === 'object' && 'code' in error && error.code === 'ESRCH')
        return (await this.inspect()) === true;
      return false;
    }
  }
}

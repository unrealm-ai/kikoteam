import { lstat, readdir, realpath, stat } from 'node:fs/promises';
import { dirname, isAbsolute, join, relative, resolve, sep } from 'node:path';
import { homedir } from 'node:os';
import { fileURLToPath } from 'node:url';
import type { FileHandle } from 'node:fs/promises';

function contains(root: string, path: string): boolean {
  const part = relative(root, path);
  return part === '' || (!part.startsWith(`..${sep}`) && part !== '..' && !isAbsolute(part));
}

async function canonical(path: string): Promise<string> {
  try {
    return await realpath(path);
  } catch (error) {
    if (!error || typeof error !== 'object' || !('code' in error) || error.code !== 'ENOENT')
      throw error;
    const parent = dirname(path);
    if (parent === path) throw error;
    return resolve(await canonical(parent), relative(parent, path));
  }
}

export class StateGuard {
  private root = '';
  private inodes = new Set<string>();

  constructor(
    private readonly stateDirectory: string,
    private readonly cwd: string,
  ) {}

  async initialize(): Promise<void> {
    this.root = await realpath(this.stateDirectory);
    const work = await realpath(this.cwd);
    const workspace = await canonical('/workspace');
    if (
      contains(work, this.root) ||
      contains(this.root, work) ||
      contains(workspace, this.root) ||
      contains(this.root, workspace)
    )
      throw new Error('Process state and work directories must be separate.');
    await this.refresh();
  }

  async refresh(): Promise<void> {
    const paths = [this.root, ...(await readdir(this.root)).map((name) => join(this.root, name))];
    const inodes = new Set<string>();
    for (const path of paths) {
      let info;
      try {
        info = await lstat(path);
      } catch (error) {
        if (error && typeof error === 'object' && 'code' in error && error.code === 'ENOENT')
          continue;
        throw error;
      }
      if (info.isSymbolicLink() || (info.isFile() && info.nlink !== 1))
        throw new Error('Private process state has an unsafe link.');
      inodes.add(`${info.dev}:${info.ino}`);
    }
    this.inodes = inodes;
  }

  async path(path: string, recursive = false): Promise<void> {
    const target = await canonical(path);
    if (contains(this.root, target) || (recursive && contains(target, this.root)))
      throw new Error('Private process state is not accessible to file tools.');
    try {
      const info = await stat(target);
      if (this.inodes.has(`${info.dev}:${info.ino}`))
        throw new Error('Private process state is not accessible to file tools.');
    } catch (error) {
      if (!error || typeof error !== 'object' || !('code' in error) || error.code !== 'ENOENT')
        throw error;
    }
  }

  async handle(handle: FileHandle): Promise<void> {
    const info = await handle.stat();
    if (this.inodes.has(`${info.dev}:${info.ino}`))
      throw new Error('Private process state is not accessible to file tools.');
  }

  async arguments(tool: string, args: Record<string, unknown>): Promise<void> {
    await this.refresh();
    let path = typeof args.path === 'string' ? args.path : this.cwd;
    if (tool === 'read') {
      await this.path(path.startsWith('file://') ? fileURLToPath(path) : resolve(this.cwd, path));
      return;
    }
    if (path.startsWith('@')) path = path.slice(1);
    path = path.replace(/[\u00a0\u2000-\u200a\u202f\u205f\u3000]/g, ' ');
    if (path === '~') path = homedir();
    else if (path.startsWith('~/')) path = join(homedir(), path.slice(2));
    if (path.startsWith('file://')) path = fileURLToPath(path);
    await this.path(resolve(this.cwd, path), tool === 'grep' || tool === 'find' || tool === 'ls');
  }
}

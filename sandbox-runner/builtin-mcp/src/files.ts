import { constants } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { resolve } from 'node:path';
import { access, mkdir, open, stat, type FileHandle } from 'node:fs/promises';
import { createReadTool } from '../vendor/pi-tools/lib/core/tools/read.js';
import { createWriteTool } from '../vendor/pi-tools/lib/core/tools/write.js';
import { createEditTool } from '../vendor/pi-tools/lib/core/tools/edit.js';
import { createGrepTool } from '../vendor/pi-tools/lib/core/tools/grep.js';
import { createFindTool } from '../vendor/pi-tools/lib/core/tools/find.js';
import { createLsTool } from '../vendor/pi-tools/lib/core/tools/ls.js';
import { detectSupportedImageMimeTypeFromFile } from '../vendor/pi-tools/lib/utils/mime.js';
import { imageSize } from 'image-size';
import type { StateGuard } from './state-guard.js';

export const FILE_BYTES = 16 * 1024 * 1024;
export const IMAGE_BYTES = 32 * 1024 * 1024;
export const IMAGE_PIXELS = 40_000_000;

export type FileTool =
  | ReturnType<typeof createReadTool>
  | ReturnType<typeof createWriteTool>
  | ReturnType<typeof createEditTool>
  | ReturnType<typeof createGrepTool>
  | ReturnType<typeof createFindTool>
  | ReturnType<typeof createLsTool>;

async function regular(handle: FileHandle): Promise<void> {
  if (!(await handle.stat()).isFile()) {
    throw new Error(
      'Only regular files are supported; devices, sockets and pipes are not allowed.',
    );
  }
}

async function withReadable<T>(
  path: string,
  action: (handle: FileHandle) => Promise<T>,
  guard?: StateGuard,
): Promise<T> {
  await guard?.path(path);
  // NONBLOCK makes the fstat guard safe even when a path is replaced with a FIFO.
  const handle = await open(path, constants.O_RDONLY | constants.O_NONBLOCK);
  try {
    await regular(handle);
    await guard?.handle(handle);
    return await action(handle);
  } finally {
    await handle.close();
  }
}

async function imageMime(handle: FileHandle): Promise<string | null> {
  // Keep Pi's own sniffing semantics, including its animated PNG/JPEG exclusions.
  // The public helper opens the pinned regular inode, not the mutable user path.
  const descriptor =
    process.platform === 'linux' ? `/proc/self/fd/${handle.fd}` : `/dev/fd/${handle.fd}`;
  return detectSupportedImageMimeTypeFromFile(descriptor);
}

async function boundedRead(handle: FileHandle, limit: number): Promise<Buffer> {
  if ((await handle.stat()).size > limit)
    throw new Error(
      'File exceeds the builtin input size limit; use a managed command to process it in chunks.',
    );
  const buffer = Buffer.alloc(limit + 1);
  let offset = 0;
  while (offset < buffer.length) {
    const { bytesRead } = await handle.read(buffer, offset, buffer.length - offset, offset);
    if (!bytesRead) break;
    offset += bytesRead;
  }
  if (offset > limit)
    throw new Error(
      'File exceeds the builtin input size limit; use a managed command to process it in chunks.',
    );
  return buffer.subarray(0, offset);
}

async function readContents(handle: FileHandle): Promise<Buffer> {
  const mime = await imageMime(handle);
  const bytes = await boundedRead(handle, mime ? IMAGE_BYTES : FILE_BYTES);
  if (mime) {
    const { width, height } = imageSize(bytes);
    if (!width || !height || width * height > IMAGE_PIXELS) {
      throw new Error('Image exceeds the builtin pixel limit.');
    }
  }
  return bytes;
}

export async function readFile(path: string, guard?: StateGuard): Promise<Buffer> {
  return withReadable(path, readContents, guard);
}

// PDF is binary even when a parser can extract text from it. Identify the
// header on the pinned handle, not by a user-controlled filename or a new open.
async function rejectPDFRead(handle: FileHandle): Promise<void> {
  const header = Buffer.alloc(8);
  const { bytesRead } = await handle.read(header, 0, header.length, 0);
  if (bytesRead === header.length && /^%PDF-[0-9]\.[0-9]$/.test(header.toString('latin1'))) {
    throw new Error(
      'The read tool cannot extract text from a PDF. Use the managed Bash tool with pdftotext on the authorized project file.',
    );
  }
}

// Pi accepts interactive filename aliases. Bind its read operations to the inode
// opened from the literal argument so an attachment can never select an alias.
function projectReadTool(cwd: string, guard?: StateGuard): ReturnType<typeof createReadTool> {
  return {
    ...createReadTool(cwd),
    execute(id, args, signal, onUpdate) {
      const path = args.path.startsWith('file://')
        ? fileURLToPath(args.path)
        : resolve(cwd, args.path);
      return withReadable(
        path,
        async (handle) => {
          await rejectPDFRead(handle);
          return createReadTool(cwd, {
            operations: {
              access: () => regular(handle),
              readFile: () => readContents(handle),
              detectImageMimeType: () => imageMime(handle),
            },
          }).execute(id, args, signal, onUpdate);
        },
        guard,
      );
    },
  };
}

export async function writeFile(path: string, content: string, guard?: StateGuard): Promise<void> {
  await guard?.path(path);
  if (Buffer.byteLength(content) > FILE_BYTES)
    throw new Error('File exceeds the builtin output size limit.');
  // Opening without TRUNC lets us reject special files before changing content.
  const handle = await open(
    path,
    constants.O_WRONLY | constants.O_CREAT | constants.O_NONBLOCK,
    0o666,
  );
  try {
    await regular(handle);
    await guard?.handle(handle);
    await handle.truncate(0);
    await handle.writeFile(content, 'utf8');
  } finally {
    await handle.close();
  }
}

async function checkReadable(path: string, guard?: StateGuard): Promise<void> {
  await access(path, constants.R_OK);
  await withReadable(path, async () => {}, guard);
}

async function isDirectory(path: string, guard?: StateGuard): Promise<boolean> {
  await guard?.path(path, true);
  const info = await stat(path);
  if (!info.isDirectory() && !info.isFile())
    throw new Error('Search paths must be regular files or directories.');
  return info.isDirectory();
}

export function createFileTools(cwd: string, guard?: StateGuard): FileTool[] {
  return [
    projectReadTool(cwd, guard),
    createWriteTool(cwd, {
      operations: {
        writeFile: (path, content) => writeFile(path, content, guard),
        mkdir: async (path) => {
          await guard?.path(path);
          await mkdir(path, { recursive: true });
        },
      },
    }),
    createEditTool(cwd, {
      operations: {
        readFile: (path) => readFile(path, guard),
        writeFile: (path, content) => writeFile(path, content, guard),
        access: async (path) => {
          await access(path, constants.R_OK | constants.W_OK);
          await checkReadable(path, guard);
        },
      },
    }),
    createGrepTool(cwd, {
      operations: {
        isDirectory: (path) => isDirectory(path, guard),
        readFile: async (path) => (await readFile(path, guard)).toString('utf8'),
      },
    }),
    createFindTool(cwd),
    createLsTool(cwd),
  ];
}

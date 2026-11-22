import { createHash, createHmac, randomUUID, timingSafeEqual } from 'node:crypto';

// UUID handles are independent from OS PIDs and have no product prefix.
export function processId(): string {
  return randomUUID();
}

function stable(value: unknown): unknown {
  if (Array.isArray(value)) return value.map(stable);
  if (value && typeof value === 'object')
    return Object.fromEntries(
      Object.entries(value)
        .sort(([a], [b]) => a.localeCompare(b))
        .map(([k, v]) => [k, stable(v)]),
    );
  return value;
}

export function requestDigest(tool: string, input: unknown): string {
  return createHash('sha256')
    .update(JSON.stringify([tool, stable(input)]))
    .digest('hex');
}

export class CursorCodec {
  constructor(
    private readonly key: Buffer,
    private readonly scope: string,
  ) {}

  encode(value: unknown): string {
    const body = Buffer.from(JSON.stringify({ version: 1, scope: this.scope, value })).toString(
      'base64url',
    );
    return `${body}.${createHmac('sha256', this.key).update(body).digest('base64url')}`;
  }

  decode(cursor: string): unknown {
    try {
      if (!cursor || cursor.length > 4096) throw new Error();
      const parts = cursor.split('.');
      if (parts.length !== 2 || !parts[0] || !parts[1]) throw new Error();
      const signature = Buffer.from(parts[1], 'base64url');
      const expected = createHmac('sha256', this.key).update(parts[0]).digest();
      if (signature.length !== expected.length || !timingSafeEqual(signature, expected))
        throw new Error();
      const decoded: unknown = JSON.parse(Buffer.from(parts[0], 'base64url').toString('utf8'));
      if (
        !decoded ||
        typeof decoded !== 'object' ||
        !('version' in decoded) ||
        !('scope' in decoded) ||
        !('value' in decoded)
      )
        throw new Error();
      if (decoded.version !== 1 || decoded.scope !== this.scope) throw new Error();
      return decoded.value;
    } catch {
      throw new Error('Invalid process cursor.');
    }
  }
}

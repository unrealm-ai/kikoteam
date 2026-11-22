import { isUtf8 } from 'node:buffer';
import type { OutputChunk, OutputPage, ProcessRecord, Stream } from './model.js';
import { CursorCodec } from './identity.js';
import { ProcessFailure } from './result.js';
import { ProcessStore } from './store.js';

// Preserve a possibly split UTF-8 code point. Invalid complete sequences are
// decoded explicitly as replacements; the original bytes remain in SQLite.
export class OutputDecoder {
  private pending = Buffer.alloc(0);

  write(raw: Buffer, final = false): { text: string; replaced: boolean } {
    const bytes = Buffer.concat([this.pending, raw]);
    let split = bytes.length;
    if (!final && bytes.length) {
      let lead = bytes.length - 1;
      while (lead > 0 && (bytes[lead]! & 0xc0) === 0x80 && bytes.length - lead < 4) lead--;
      const first = bytes[lead]!;
      const size =
        first >= 0xc2 && first <= 0xdf
          ? 2
          : first >= 0xe0 && first <= 0xef
            ? 3
            : first >= 0xf0 && first <= 0xf4
              ? 4
              : 1;
      if (size > bytes.length - lead) split = lead;
    }
    const complete = bytes.subarray(0, split);
    this.pending = Buffer.from(bytes.subarray(split));
    return { text: complete.toString('utf8'), replaced: !isUtf8(complete) };
  }
}

interface Position {
  seq: number;
  offset: number;
}

function sliceEnd(bytes: Buffer, start: number, budget: number, limit = bytes.length): number {
  if (start > bytes.length || (start < bytes.length && (bytes[start]! & 0xc0) === 0x80))
    throw new ProcessFailure('invalid_cursor');
  let end = Math.min(bytes.length, start + budget, limit);
  while (end < bytes.length && end > start && (bytes[end]! & 0xc0) === 0x80) end--;
  return end;
}

export class ProcessOutput {
  constructor(
    private readonly store: ProcessStore,
    private readonly codec: CursorCodec,
  ) {}

  cursor(record: ProcessRecord, position: Position): string {
    return this.codec.encode({
      kind: 'output',
      process_id: record.process_id,
      generation: record.output_generation,
      ...position,
    });
  }

  position(record: ProcessRecord, cursor?: string): Position {
    if (cursor === undefined) return { seq: record.first_seq, offset: 0 };
    let value: unknown;
    try {
      value = this.codec.decode(cursor);
    } catch {
      throw new ProcessFailure('invalid_cursor');
    }
    if (!value || typeof value !== 'object') throw new ProcessFailure('invalid_cursor');
    const position = value as Record<string, unknown>;
    if (
      position.kind !== 'output' ||
      position.process_id !== record.process_id ||
      position.generation !== record.output_generation ||
      !Number.isSafeInteger(position.seq) ||
      !Number.isSafeInteger(position.offset)
    )
      throw new ProcessFailure('invalid_cursor');
    const seq = position.seq as number;
    const offset = position.offset as number;
    if (seq < 0 || seq > record.next_seq || offset < 0) throw new ProcessFailure('invalid_cursor');
    if (seq < record.first_seq)
      throw new ProcessFailure('output_cursor_expired', {
        first_available_cursor: this.cursor(record, { seq: record.first_seq, offset: 0 }),
      });
    if (seq === record.next_seq && offset !== 0) throw new ProcessFailure('invalid_cursor');
    return { seq, offset };
  }

  page(
    record: ProcessRecord,
    cursor: string | undefined,
    budget: number,
    until?: string,
  ): OutputPage {
    if (record.output_expired) throw new ProcessFailure('output_expired');
    const position = this.position(record, cursor);
    const boundary = until === undefined ? undefined : this.position(record, until);
    const chunks: OutputChunk[] = [];
    let remaining = budget;
    for (const row of this.store.output(record.process_id, position.seq)) {
      if (
        boundary &&
        (row.seq > boundary.seq || (row.seq === boundary.seq && boundary.offset === 0))
      )
        break;
      const bytes = Buffer.from(row.text);
      const start = row.seq === position.seq ? position.offset : 0;
      const end = sliceEnd(
        bytes,
        start,
        remaining,
        boundary && row.seq === boundary.seq ? boundary.offset : undefined,
      );
      if (end > start) {
        const text = bytes.subarray(start, end).toString('utf8');
        this.add(chunks, row.stream, text, row.replaced === 1);
        remaining -= end - start;
      }
      position.seq = end === bytes.length ? row.seq + 1 : row.seq;
      position.offset = end === bytes.length ? 0 : end;
      if (end < bytes.length || remaining === 0) break;
    }
    const hasMore = position.seq < record.next_seq;
    return {
      chunks,
      next_cursor: this.cursor(record, position),
      first_available_cursor: this.cursor(record, { seq: record.first_seq, offset: 0 }),
      has_more: hasMore,
      output_complete:
        record.output_closed &&
        record.state === 'exited' &&
        record.group_quiescent === true &&
        !hasMore,
      output_truncated: record.first_seq > 0,
    };
  }

  private add(chunks: OutputChunk[], stream: Stream, text: string, replaced: boolean): void {
    const last = chunks.at(-1);
    if (last?.stream === stream && Boolean(last.encoding_replaced) === replaced) last.text += text;
    else chunks.push({ stream, text, ...(replaced ? { encoding_replaced: true as const } : {}) });
  }
}

import { DatabaseSync, type SQLInputValue, type SQLOutputValue } from 'node:sqlite';
import {
  constants,
  closeSync,
  existsSync,
  fstatSync,
  fsyncSync,
  lstatSync,
  mkdirSync,
  openSync,
  readFileSync,
  writeFileSync,
} from 'node:fs';
import { isAbsolute, join, resolve } from 'node:path';
import { randomBytes } from 'node:crypto';
import type { CallToolResult } from '@modelcontextprotocol/server';
import type { OperationRecord, ProcessConfig, ProcessRecord, Stream } from './model.js';
import { resultObject } from './result.js';

const SCHEMA = `
CREATE TABLE meta (key TEXT PRIMARY KEY, value TEXT NOT NULL);
CREATE TABLE processes (id TEXT PRIMARY KEY, created_at TEXT NOT NULL, body TEXT NOT NULL);
CREATE INDEX processes_order ON processes(created_at DESC, id DESC);
CREATE TABLE operations (id TEXT PRIMARY KEY, body TEXT NOT NULL);
CREATE TABLE output (
  process_id TEXT NOT NULL REFERENCES processes(id), seq INTEGER NOT NULL,
  stream TEXT NOT NULL, text TEXT NOT NULL, raw BLOB NOT NULL,
  replaced INTEGER NOT NULL, bytes INTEGER NOT NULL,
  PRIMARY KEY(process_id, seq)
);
`;

export class StoreFailure extends Error {
  constructor() {
    super('Process state is unavailable; effects may be indeterminate.');
  }
}

export interface StoredOutput {
  seq: number;
  stream: Stream;
  text: string;
  replaced: number;
  bytes: number;
}

function privateFile(path: string): void {
  const fd = openSync(path, constants.O_RDONLY | constants.O_NOFOLLOW | constants.O_NONBLOCK);
  try {
    const stat = fstatSync(fd);
    if (
      !stat.isFile() ||
      stat.nlink !== 1 ||
      stat.uid !== process.getuid?.() ||
      (stat.mode & 0o077) !== 0
    )
      throw new Error('Invalid private process state file.');
  } finally {
    closeSync(fd);
  }
}

function syncDirectory(path: string): void {
  const fd = openSync(path, constants.O_RDONLY);
  try {
    fsyncSync(fd);
  } finally {
    closeSync(fd);
  }
}

function prepareDirectory(config: ProcessConfig): {
  database: string;
  fresh: boolean;
  witness: string;
} {
  if (!isAbsolute(config.state_directory))
    throw new Error('Process state directory must be absolute.');
  mkdirSync(config.state_directory, { mode: 0o700, recursive: true });
  const stat = lstatSync(config.state_directory);
  if (
    !stat.isDirectory() ||
    stat.isSymbolicLink() ||
    stat.uid !== process.getuid?.() ||
    (stat.mode & 0o077) !== 0
  )
    throw new Error('Process state directory must be private.');
  const database = join(config.state_directory, 'processes.sqlite');
  const witness = join(config.state_directory, 'initialized');
  const fresh = !existsSync(database);
  if (fresh && existsSync(witness)) throw new Error('Initialized process store is missing.');
  if (fresh) {
    const fd = openSync(database, constants.O_CREAT | constants.O_EXCL | constants.O_WRONLY, 0o600);
    try {
      fsyncSync(fd);
    } finally {
      closeSync(fd);
    }
    syncDirectory(config.state_directory);
  }
  for (const path of [
    database,
    `${database}-journal`,
    `${database}-wal`,
    `${database}-shm`,
    witness,
  ]) {
    if (existsSync(path)) privateFile(path);
  }
  return { database, fresh, witness };
}

function decode<T>(row: Record<string, SQLOutputValue> | undefined): T | undefined {
  if (!row) return undefined;
  if (typeof row.body !== 'string') throw new StoreFailure();
  return JSON.parse(row.body) as T;
}

export class ProcessStore {
  private readonly db: DatabaseSync;
  private broken = false;
  private activityDirty = false;
  readonly key: Buffer;

  constructor(readonly config: ProcessConfig) {
    const { database, fresh, witness } = prepareDirectory(config);
    this.db = new DatabaseSync(database, { timeout: 0, allowExtension: false });
    try {
      this.db.exec(
        'PRAGMA locking_mode=EXCLUSIVE; PRAGMA journal_mode=DELETE; PRAGMA synchronous=EXTRA; PRAGMA foreign_keys=ON;',
      );
      // The EXCLUSIVE connection retains its lock between transactions. A
      // second Server cannot observe or modify this store until we close it.
      const scope = JSON.stringify([
        config.workspace_id,
        config.session_id,
        config.environment_instance_id,
        resolve(config.state_directory),
      ]);
      this.transaction(() => {
        if (fresh) {
          this.db.exec(SCHEMA);
          this.run('INSERT INTO meta VALUES (?, ?)', 'version', '1');
          this.run('INSERT INTO meta VALUES (?, ?)', 'scope', scope);
          this.run('INSERT INTO meta VALUES (?, ?)', 'cursor_key', randomBytes(32).toString('hex'));
        }
        const version = this.get('SELECT value FROM meta WHERE key=?', 'version')?.value;
        if (
          (version !== '1' && version !== '2') ||
          this.get('SELECT value FROM meta WHERE key=?', 'scope')?.value !== scope
        )
          throw new StoreFailure();
        if (version === '1') {
          this.run('INSERT INTO meta VALUES (?, ?)', 'activity_revision', '0');
          this.run('UPDATE meta SET value=? WHERE key=?', '2', 'version');
        }
        this.run('UPDATE meta SET value=value WHERE key=?', 'version');
      });
      if (this.get('PRAGMA integrity_check')?.integrity_check !== 'ok') throw new StoreFailure();
      const key = this.get('SELECT value FROM meta WHERE key=?', 'cursor_key')?.value;
      if (typeof key !== 'string' || !/^[0-9a-f]{64}$/.test(key)) throw new StoreFailure();
      this.key = Buffer.from(key, 'hex');
      if (!existsSync(witness)) {
        const fd = openSync(
          witness,
          constants.O_CREAT | constants.O_EXCL | constants.O_WRONLY,
          0o600,
        );
        try {
          writeFileSync(fd, scope);
          fsyncSync(fd);
        } finally {
          closeSync(fd);
        }
        syncDirectory(config.state_directory);
      } else if (readFileSync(witness, 'utf8') !== scope) throw new StoreFailure();
    } catch (error) {
      this.db.close();
      throw error;
    }
  }

  transaction<T>(action: () => T): T {
    if (this.broken) throw new StoreFailure();
    try {
      this.db.exec('BEGIN IMMEDIATE');
      this.activityDirty = false;
      const result = action();
      if (this.activityDirty) {
        const revision = this.activityRevision();
        if (revision === Number.MAX_SAFE_INTEGER) throw new StoreFailure();
        this.run('UPDATE meta SET value=? WHERE key=?', String(revision + 1), 'activity_revision');
      }
      this.db.exec('COMMIT');
      return result;
    } catch {
      this.broken = true;
      if (this.db.isTransaction) {
        try {
          this.db.exec('ROLLBACK');
        } catch {
          /* Store remains poisoned. */
        }
      }
      throw new StoreFailure();
    }
  }

  private activityRevision(): number {
    return this.counter('activity_revision');
  }

  private counter(key: string): number {
    const raw =
      this.get('SELECT value FROM meta WHERE key=?', key)?.value ??
      (key === 'pending_files' ? '0' : undefined);
    if (typeof raw !== 'string' || !/^(0|[1-9][0-9]*)$/.test(raw)) throw new StoreFailure();
    const value = Number(raw);
    if (!Number.isSafeInteger(value)) throw new StoreFailure();
    return value;
  }

  // Must commit before an in-memory side effect is admitted, and again when
  // its last asynchronous write barrier settles. Queries never call this.
  touchActivity(): void {
    if (!this.db.isTransaction) throw new StoreFailure();
    this.activityDirty = true;
  }

  claimActivitySource(source: string): void {
    this.transaction(() => {
      if (this.get('SELECT value FROM meta WHERE key=?', 'activity_source')?.value === source)
        throw new StoreFailure();
      this.run(
        'INSERT INTO meta VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value=excluded.value',
        'activity_source',
        source,
      );
      this.touchActivity();
    });
  }

  markActivityUnknown(): void {
    if (this.get('SELECT value FROM meta WHERE key=?', 'activity_uncertain')?.value === '1') return;
    this.run(
      'INSERT INTO meta VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value=excluded.value',
      'activity_uncertain',
      '1',
    );
    this.touchActivity();
  }

  beginFileCall(): void {
    const pending = this.counter('pending_files');
    if (pending === Number.MAX_SAFE_INTEGER) throw new StoreFailure();
    this.run(
      'INSERT INTO meta VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value=excluded.value',
      'pending_files',
      String(pending + 1),
    );
    this.touchActivity();
  }

  finishFileCall(uncertain: boolean): void {
    const pending = this.counter('pending_files');
    if (pending < 1) throw new StoreFailure();
    if (uncertain) this.markActivityUnknown();
    this.run('UPDATE meta SET value=? WHERE key=?', String(pending - 1), 'pending_files');
    this.touchActivity();
  }

  recoverFileCalls(): void {
    if (this.counter('pending_files') === 0) return;
    this.markActivityUnknown();
    this.run('UPDATE meta SET value=? WHERE key=?', '0', 'pending_files');
  }

  activity(): { revision: number; active: number; unknown: number; pending_writes: number } {
    return this.checked(() => {
      // Full ledger aggregates: never use the tool's paginated discovery or
      // its capacity limit as evidence that all old groups have been checked.
      const row = this.get(`WITH facts AS (
        SELECT json_extract(body, '$.state') AS state,
          json_extract(body, '$.group_quiescent') AS quiet,
          json_extract(body, '$.output_closed') AS closed FROM processes
      ) SELECT
        COALESCE(SUM(state IN ('starting','running','terminating') OR (quiet = 0 AND state = 'exited')), 0) AS active,
        COALESCE(SUM(state IS NULL OR state NOT IN ('starting','running','terminating','exited')
          OR quiet IS NULL OR quiet NOT IN (0,1) OR closed IS NULL OR closed NOT IN (0,1)), 0) AS unknown,
        COALESCE(SUM(closed IS NOT 1), 0) AS pending FROM facts`);
      const operations = this.get(`SELECT COUNT(*) AS pending FROM operations
        WHERE json_extract(body, '$.phase') != 'complete'
        AND json_extract(body, '$.tool') IN ('bash_start','bash_write','bash_terminate')`);
      const count = (value: SQLOutputValue | undefined): number => {
        if (typeof value !== 'number' || !Number.isSafeInteger(value) || value < 0)
          throw new StoreFailure();
        return value;
      };
      const uncertain = this.get('SELECT value FROM meta WHERE key=?', 'activity_uncertain')?.value;
      if (uncertain !== undefined && uncertain !== '1') throw new StoreFailure();
      return {
        revision: this.activityRevision(),
        active: count(row?.active),
        unknown: count(row?.unknown) + (uncertain === '1' ? 1 : 0),
        pending_writes:
          count(row?.pending) + count(operations?.pending) + this.counter('pending_files'),
      };
    });
  }

  private get(sql: string, ...values: SQLInputValue[]) {
    return this.checked(() => this.db.prepare(sql).get(...values));
  }

  private run(sql: string, ...values: SQLInputValue[]) {
    return this.checked(() => this.db.prepare(sql).run(...values));
  }

  private all(sql: string, ...values: SQLInputValue[]) {
    return this.checked(() => this.db.prepare(sql).all(...values));
  }

  private checked<T>(action: () => T): T {
    if (this.broken) throw new StoreFailure();
    try {
      return action();
    } catch {
      this.broken = true;
      throw new StoreFailure();
    }
  }

  process(id: string): ProcessRecord | undefined {
    return decode<ProcessRecord>(this.get('SELECT body FROM processes WHERE id=?', id));
  }

  unresolved(): ProcessRecord[] {
    return this.all(
      "SELECT body FROM processes WHERE json_extract(body, '$.group_quiescent') IS NOT 1 OR json_extract(body, '$.output_closed') IS NOT 1",
    ).map((row) => decode<ProcessRecord>(row)!);
  }

  list(limit: number, boundary?: { created_at: string; process_id: string }): ProcessRecord[] {
    const rows = boundary
      ? this.all(
          'SELECT body FROM processes WHERE created_at < ? OR (created_at = ? AND id < ?) ORDER BY created_at DESC, id DESC LIMIT ?',
          boundary.created_at,
          boundary.created_at,
          boundary.process_id,
          limit,
        )
      : this.all('SELECT body FROM processes ORDER BY created_at DESC, id DESC LIMIT ?', limit);
    return rows.map((row) => decode<ProcessRecord>(row)!);
  }

  // Called only inside a transaction so process and operation facts can commit
  // together at the exact before-effect/after-effect boundaries.
  putProcess(record: ProcessRecord): void {
    this.touchActivity();
    this.run(
      'INSERT INTO processes VALUES (?, ?, ?) ON CONFLICT(id) DO UPDATE SET body=excluded.body',
      record.process_id,
      record.created_at,
      JSON.stringify(record),
    );
  }

  removeUnstarted(id: string): void {
    this.touchActivity();
    this.run('DELETE FROM output WHERE process_id=?', id);
    this.run('DELETE FROM processes WHERE id=?', id);
  }

  operation(id: string): OperationRecord | undefined {
    return decode<OperationRecord>(this.get('SELECT body FROM operations WHERE id=?', id));
  }

  operations(): OperationRecord[] {
    return this.all(
      "SELECT body FROM operations WHERE json_extract(body, '$.phase') != 'complete'",
    ).map((row) => decode<OperationRecord>(row)!);
  }

  putOperation(record: OperationRecord): void {
    if (record.tool !== 'bash_poll' && record.tool !== 'bash_list') this.touchActivity();
    this.run(
      'INSERT INTO operations VALUES (?, ?) ON CONFLICT(id) DO UPDATE SET body=excluded.body',
      record.operation_id,
      JSON.stringify(record),
    );
  }

  finish(operation: OperationRecord, result: CallToolResult): void {
    // Retain operation identity/state, not another copy of the output log.
    const saved = operation.output_reference
      ? { ...result, structuredContent: { ...resultObject(result), chunks: [] } }
      : result;
    this.transaction(() => this.putOperation({ ...operation, phase: 'complete', result: saved }));
  }

  expirable(before: string): ProcessRecord[] {
    return this.all(
      `SELECT body FROM processes WHERE json_extract(body, '$.output_expired') = 0
      AND json_extract(body, '$.group_quiescent') = 1 AND json_extract(body, '$.output_closed') = 1
      AND json_extract(body, '$.exited_at') <= ? LIMIT 100`,
      before,
    ).map((row) => decode<ProcessRecord>(row)!);
  }

  output(id: string, from: number, limit = 1024): StoredOutput[] {
    return this.all(
      'SELECT seq, stream, text, replaced, bytes FROM output WHERE process_id=? AND seq>=? ORDER BY seq LIMIT ?',
      id,
      from,
      limit,
    ) as unknown as StoredOutput[];
  }

  append(
    record: ProcessRecord,
    stream: Stream,
    raw: Buffer,
    text: string,
    replaced: boolean,
    budget: number,
  ): void {
    this.transaction(() => {
      const bytes = raw.length + Buffer.byteLength(text) + 128;
      this.run(
        'INSERT INTO output VALUES (?, ?, ?, ?, ?, ?, ?)',
        record.process_id,
        record.next_seq,
        stream,
        text,
        raw,
        Number(replaced),
        bytes,
      );
      record.next_seq++;
      record.retained_bytes += bytes;
      while (record.retained_bytes > budget && record.first_seq < record.next_seq) {
        const old = this.get(
          'SELECT bytes FROM output WHERE process_id=? AND seq=?',
          record.process_id,
          record.first_seq,
        );
        if (typeof old?.bytes !== 'number') throw new StoreFailure();
        this.run(
          'DELETE FROM output WHERE process_id=? AND seq=?',
          record.process_id,
          record.first_seq,
        );
        record.retained_bytes -= old.bytes;
        record.first_seq++;
      }
      this.putProcess(record);
    });
  }

  expire(record: ProcessRecord): void {
    this.transaction(() => {
      this.run('DELETE FROM output WHERE process_id=?', record.process_id);
      record.output_expired = true;
      record.first_seq = record.next_seq;
      record.retained_bytes = 0;
      this.putProcess(record);
    });
  }

  close(): void {
    this.db.close();
  }
}

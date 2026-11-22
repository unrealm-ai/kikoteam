import { Client } from '@modelcontextprotocol/client';
import { StdioClientTransport } from '@modelcontextprotocol/client/stdio';
import { fileURLToPath } from 'node:url';
import type { ProcessConfig } from './process/model.js';
import type { Logger, LogFields } from './logging.js';
import type { FileTool } from './files.js';
import { describeTool } from './definitions.js';
import { CALL_CONTEXT_KEY } from './context.js';
import { isDeepStrictEqual } from 'node:util';

export class FileWorker {
  private readonly client: Client;
  private readonly transport: StdioClientTransport;
  private failed = false;
  private closing = false;

  constructor(config: ProcessConfig, log: Logger) {
    this.transport = new StdioClientTransport({
      command: process.execPath,
      args: [
        '--disable-warning=ExperimentalWarning',
        '--max-old-space-size=512',
        fileURLToPath(new URL('./file-worker-entry.js', import.meta.url)),
        config.cwd,
      ],
      cwd: config.cwd,
      env: { ...config.environment },
      stderr: 'inherit',
      maxBufferSize: 20 * 1024 * 1024,
    });
    this.client = new Client(
      { name: 'builtin-file-worker-client', version: '0.1.0' },
      {
        capabilities: {},
        inputRequired: { autoFulfill: false },
        versionNegotiation: {
          mode: { pin: '2026-07-28' },
          probe: { timeoutMs: 10000, maxRetries: 0 },
        },
      },
    );
    this.client.onclose = () => {
      this.failed = true;
      if (!this.closing)
        log(
          'worker.closed',
          'File worker connection closed',
          { error_code: 'execution_indeterminate' },
          'ERROR',
        );
    };
  }

  async connect(tools: FileTool[]): Promise<void> {
    await this.client.connect(this.transport, { timeout: 10000 });
    const actual = (await this.client.listTools()).tools;
    if (!isDeepStrictEqual(actual, tools.map(describeTool)))
      throw new Error('File worker catalog differs from pinned factories.');
  }

  async call(name: string, args: Record<string, unknown>, context: LogFields, signal: AbortSignal) {
    if (this.failed) throw new Error('File worker is unavailable; no automatic replay.');
    const meta = context.operation_id ? { [CALL_CONTEXT_KEY]: context } : undefined;
    return this.client.callTool(
      { name, arguments: args, _meta: meta },
      { signal, timeout: 2_147_483_647 },
    );
  }

  async close(): Promise<void> {
    this.closing = true;
    await this.client.close();
  }
}

import {
  Server,
  ProtocolError,
  ProtocolErrorCode,
  type ServerContext,
} from '@modelcontextprotocol/server';
import { createFileTools, type FileTool } from './files.js';
import { toMcpResult, toolError } from './results.js';
import type { Logger } from './logging.js';
import { argumentValidator, describeTool } from './definitions.js';
import { callContext, processContext } from './context.js';
import { failureInfo } from './errors.js';
import type { ProcessManager } from './process/manager.js';
import { processTools } from './process/tools.js';
import type { BashTool } from './process/model.js';
import { processError } from './process/result.js';
import type { StateGuard } from './state-guard.js';
import type { FileWorker } from './file-worker.js';
import { registerActivity } from './activity-protocol.js';

export const PROTOCOL_VERSION = '2026-07-28';
export const MAX_CALLS = 8;

export function callScope() {
  return {
    shutdown: new AbortController(),
    active: new Set<Promise<unknown>>(),
    controls: new Set<Promise<unknown>>(),
    terminations: new Set<Promise<unknown>>(),
  };
}

export function createBuiltinServer(
  cwd: string,
  log: Logger,
  tools: FileTool[] = createFileTools(cwd),
  processes?: ProcessManager,
  guard?: StateGuard,
  worker?: FileWorker,
  options: {
    context?: () => ReturnType<typeof processContext>;
    exposeActivity?: boolean;
    scope?: ReturnType<typeof callScope>;
    legacyHttp?: boolean;
  } = {},
) {
  const { shutdown, active, controls, terminations } = options.scope ?? callScope();
  // The SDK's high-level tool handler converts schema errors into isError.
  // Use its public protocol handler so invalid params stay protocol errors.
  const server = new Server(
    { name: 'builtin-builtin', version: '0.1.0' },
    {
      capabilities: { tools: {} },
      supportedProtocolVersions: options.legacyHttp
        ? [PROTOCOL_VERSION, '2025-11-25']
        : [PROTOCOL_VERSION],
    },
  );
  const registry = new Map(
    tools.map((tool) => [tool.name, { tool, validate: argumentValidator(tool) }]),
  );
  if (processes && options.exposeActivity !== false) registerActivity(server, processes);
  const catalog = tools.map(describeTool);
  const bash = processes ? processTools(cwd) : undefined;
  if (bash) catalog.push(...[...bash.values()].map((entry) => entry.definition));
  server.setRequestHandler('tools/list', (request) => {
    if (request.params?.cursor !== undefined)
      throw new ProtocolError(ProtocolErrorCode.InvalidParams, 'Unknown catalog cursor.');
    return { tools: catalog };
  });
  server.setRequestHandler('tools/call', async (request, ctx) => {
    const processEntry = bash?.get(request.params.name as BashTool);
    if (processEntry && processes) {
      const context = options.context?.() ?? processContext(ctx.mcpReq._meta);
      const args = await processEntry.validate(request.params.arguments ?? {});
      const control =
        request.params.name === 'bash_write' || request.params.name === 'bash_terminate';
      const calls =
        request.params.name === 'bash_terminate' ? terminations : control ? controls : active;
      if (shutdown.signal.aborted || calls.size >= (control ? 2 : MAX_CALLS))
        return processError('capacity_unavailable');
      const work = executeProcess(
        request.params.name as BashTool,
        args,
        context,
        ctx,
        processEntry,
      );
      calls.add(work);
      try {
        return await work;
      } finally {
        calls.delete(work);
      }
    }
    const entry = registry.get(request.params.name);
    if (!entry) throw new ProtocolError(ProtocolErrorCode.InvalidParams, 'Unknown builtin tool.');
    const args = await entry.validate(request.params.arguments ?? {});
    if (processes?.effectsBlocked)
      return toolError(new Error('Builtin tool capacity is unavailable.'), false);
    if (shutdown.signal.aborted || active.size >= MAX_CALLS)
      return toolError(new Error('Builtin tool capacity is unavailable.'), false);
    const call = execute(entry.tool, args, ctx);
    active.add(call);
    try {
      return server.projectCallToolResult(await call, undefined);
    } finally {
      active.delete(call);
    }
  });

  async function execute(tool: FileTool, args: Record<string, unknown>, ctx: ServerContext) {
    const context = options.context?.() ?? callContext(ctx.mcpReq._meta);
    const signal = AbortSignal.any([shutdown.signal, ctx.mcpReq.signal]);
    const started = performance.now();
    let observing = false;
    let uncertain = false;
    log('tool.started', 'File tool started', {
      ...context,
      tool_name: tool.name,
    });
    try {
      await guard?.arguments(tool.name, args);
      if (processes) {
        processes.beginFileCall();
        observing = true;
      }
      // Every factory has a different input type. Its unmodified schema was
      // validated above; this single boundary is the registry's type erasure.
      const result = worker
        ? await worker.call(tool.name, args, context, signal)
        : toMcpResult(
            await tool.execute(
              context.operation_id ?? String(ctx.mcpReq.id),
              args as never,
              signal,
            ),
          );
      await guard?.refresh();
      const mapped = result;
      log('tool.completed', 'File tool completed', {
        ...context,
        tool_name: tool.name,
        outcome: mapped.isError ? 'failed' : 'succeeded',
        duration_ms: performance.now() - started,
      });
      return mapped;
    } catch (error) {
      // A cancelled/failed RPC wait does not prove the business worker stopped
      // its filesystem operation. Retain this uncertainty across Server boots.
      uncertain = observing && worker !== undefined;
      const failure = failureInfo(error, tool.name, signal.aborted);
      log(
        'tool.completed',
        'File tool completed',
        {
          ...context,
          tool_name: tool.name,
          outcome: signal.aborted ? 'cancelled' : 'failed',
          duration_ms: performance.now() - started,
          error_code: failure.error_code,
        },
        failure.level,
      );
      return toolError(error, signal.aborted);
    } finally {
      if (observing) processes!.finishFileCall(uncertain);
    }
  }

  async function executeProcess(
    name: BashTool,
    args: Record<string, unknown>,
    context: ReturnType<typeof processContext>,
    ctx: ServerContext,
    entry: NonNullable<typeof bash> extends Map<BashTool, infer T> ? T : never,
  ) {
    const started = performance.now();
    const signal = AbortSignal.any([shutdown.signal, ctx.mcpReq.signal]);
    const quiet = name === 'bash_poll' || name === 'bash_list';
    log(
      'tool.started',
      'Process tool started',
      { ...context, tool_name: name },
      quiet ? 'DEBUG' : 'INFO',
    );
    try {
      const result = await processes!.call(name, args, context, signal);
      await entry.validateResult(result.structuredContent);
      log(
        'tool.completed',
        'Process tool completed',
        {
          ...context,
          tool_name: name,
          outcome: result.isError ? 'failed' : 'succeeded',
          duration_ms: performance.now() - started,
        },
        quiet ? 'DEBUG' : 'INFO',
      );
      return server.projectCallToolResult(result, entry.definition.outputSchema);
    } catch {
      log(
        'tool.completed',
        'Process tool failed',
        {
          ...context,
          tool_name: name,
          error_code: 'execution_indeterminate',
          duration_ms: performance.now() - started,
        },
        'ERROR',
      );
      return processError('execution_indeterminate');
    }
  }

  return {
    server,
    async drain() {
      shutdown.abort();
      await Promise.allSettled([...active, ...controls, ...terminations]);
      processes?.close();
    },
  };
}

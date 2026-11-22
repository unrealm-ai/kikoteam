import { readFileSync } from 'node:fs';
import {
  fromJsonSchema,
  ProtocolError,
  ProtocolErrorCode,
  type Server,
  type StandardSchemaV1,
} from '@modelcontextprotocol/server';
import { ActivityConflict, type ActivityRequest, type ActivityResult } from './process/activity.js';
import type { ProcessManager } from './process/manager.js';

export const ACTIVITY_METHOD = 'builtin/processes/activity';
const MAX_BYTES = 8192;

function withinBudget(value: unknown): boolean {
  const encoded = JSON.stringify(value);
  return encoded !== undefined && Buffer.byteLength(encoded) <= MAX_BYTES;
}

function object(value: unknown): Record<string, unknown> {
  if (!value || typeof value !== 'object' || Array.isArray(value))
    throw new Error('Invalid process activity contract.');
  return value as Record<string, unknown>;
}

function bounded<T>(definition: Record<string, unknown>): StandardSchemaV1<unknown, T> {
  const schema = fromJsonSchema<T>(definition);
  return {
    '~standard': {
      version: 1 as const,
      vendor: 'builtin-activity',
      async validate(input: unknown) {
        try {
          if (withinBudget(input)) {
            const result = await schema['~standard'].validate(input);
            if (!result.issues) return { value: result.value };
          }
        } catch {
          /* Contract and payload details never reach protocol errors. */
        }
        return { issues: [{ message: 'Invalid process activity message.' }] };
      },
    },
  };
}

export function registerActivity(server: Server, processes: ProcessManager): void {
  if (!processes.managedActivity) return;
  const contract = object(
    JSON.parse(readFileSync(new URL('./assets/activity.json', import.meta.url), 'utf8')),
  );
  if (contract.method !== ACTIVITY_METHOD) throw new Error('Invalid process activity method.');
  server.setRequestHandler(
    ACTIVITY_METHOD,
    {
      params: bounded<ActivityRequest>(object(contract.params)),
      result: bounded<ActivityResult>(object(contract.result)),
    },
    async (request, ctx) => {
      try {
        // The SDK lifts reserved metadata before schema dispatch. Include it
        // when checking the original params budget, before changing the gate.
        if (!withinBudget({ ...request, _meta: ctx.mcpReq._meta })) throw new ActivityConflict();
        return await processes.activity(request);
      } catch (error) {
        throw new ProtocolError(
          error instanceof ActivityConflict
            ? ProtocolErrorCode.InvalidParams
            : ProtocolErrorCode.InternalError,
          'Process activity is unavailable.',
        );
      }
    },
  );
}

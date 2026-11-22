import { readFileSync } from 'node:fs';
import {
  fromJsonSchema,
  ProtocolError,
  ProtocolErrorCode,
  specTypeSchemas,
} from '@modelcontextprotocol/server';
import type { BashTool } from './model.js';

const NAMES = new Set(['bash_start', 'bash_poll', 'bash_write', 'bash_terminate', 'bash_list']);

function object(value: unknown): Record<string, unknown> {
  if (!value || typeof value !== 'object' || Array.isArray(value))
    throw new Error('Invalid generated process schema.');
  return value as Record<string, unknown>;
}

function invalid(): ProtocolError {
  return new ProtocolError(ProtocolErrorCode.InvalidParams, 'Invalid process tool arguments.');
}

export function processTools(cwd = '/workspace') {
  const source = object(
    JSON.parse(readFileSync(new URL('../assets/bash-tools.json', import.meta.url), 'utf8')),
  );
  if (!Array.isArray(source.tools)) throw new Error('Missing process tool definitions.');
  for (const raw of source.tools) {
    const tool = object(raw);
    if (tool.name === 'bash_start') {
      object(object(object(tool.inputSchema).properties).cwd).default = cwd;
    }
  }
  const entries = source.tools.map((raw: unknown) => {
    const parsed = specTypeSchemas.Tool['~standard'].validate(raw);
    if (parsed.issues || !NAMES.has(parsed.value.name) || !parsed.value.outputSchema)
      throw new Error('Invalid generated process tool.');
    const definition = parsed.value;
    const properties = object(definition.inputSchema.properties);
    const defs = object(definition.inputSchema.$defs ?? {});
    // Tool's wire type allows arbitrary JSON in schema properties. Hand the
    // checked object to the SDK's JSON Schema compiler, which validates the
    // recursive schema itself rather than asserting a different wire type.
    const input = fromJsonSchema<Record<string, unknown>>(object(definition.inputSchema));
    const output = fromJsonSchema<Record<string, unknown>>(object(definition.outputSchema));
    return [
      definition.name as BashTool,
      {
        definition,
        async validate(value: unknown): Promise<Record<string, unknown>> {
          const result = await input['~standard'].validate(value);
          if (result.issues) throw invalid();
          const args = { ...result.value };
          for (const [key, rawProperty] of Object.entries(properties)) {
            const property = object(rawProperty);
            if (args[key] === undefined && property.default !== undefined)
              args[key] = property.default;
            const ref =
              typeof property.$ref === 'string' ? property.$ref.replace('#/$defs/', '') : undefined;
            const resolved = ref ? object(defs[ref]) : property;
            const bytes = resolved['x-max-utf8-bytes'];
            if (
              typeof args[key] === 'string' &&
              typeof bytes === 'number' &&
              Buffer.byteLength(args[key]) > bytes
            )
              throw invalid();
          }
          if (definition.name === 'bash_start' && args.stdin_open === undefined)
            args.stdin_open = args.tty === true;
          if (definition.name === 'bash_write' && args.text === undefined) args.text = '';
          return args;
        },
        async validateResult(value: unknown): Promise<void> {
          if ((await output['~standard'].validate(value)).issues)
            throw new Error('Invalid process tool result.');
        },
      },
    ] as const;
  });
  const registry = new Map(entries);
  if (registry.size !== NAMES.size) throw new Error('Incomplete process tool definitions.');
  return registry;
}

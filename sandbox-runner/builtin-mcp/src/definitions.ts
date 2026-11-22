import {
  specTypeSchemas,
  fromJsonSchema,
  ProtocolError,
  ProtocolErrorCode,
} from '@modelcontextprotocol/server';
import type { FileTool } from './files.js';

export function describeTool(tool: FileTool) {
  const inputSchema: unknown = JSON.parse(JSON.stringify(tool.parameters));
  const parsed = specTypeSchemas.Tool['~standard'].validate({
    name: tool.name,
    description: tool.description,
    inputSchema,
  });
  if (parsed.issues) throw new Error('Invalid builtin tool definition.');
  return parsed.value;
}

export function argumentValidator(tool: FileTool) {
  const schema = fromJsonSchema<Record<string, unknown>>(tool.parameters);
  return async (input: unknown): Promise<Record<string, unknown>> => {
    try {
      const prepared: unknown = tool.prepareArguments ? tool.prepareArguments(input) : input;
      const parsed = await schema['~standard'].validate(prepared);
      if (!parsed.issues) return parsed.value;
    } catch {
      /* Tool schemas and preprocessors do not expose raw errors. */
    }
    throw new ProtocolError(ProtocolErrorCode.InvalidParams, 'Invalid tool arguments.');
  };
}

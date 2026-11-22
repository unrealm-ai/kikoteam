import type { Level } from './logging.js';

export function failureInfo(
  error: unknown,
  tool: string,
  cancelled: boolean,
): { level: Level; error_code: string } {
  if (cancelled) return { level: 'INFO', error_code: 'cancelled' };
  const message = error instanceof Error ? error.message : '';
  const nativeCode = error && typeof error === 'object' && 'code' in error ? error.code : undefined;
  // Pi wraps edit access failures and drops errno. Only recognize its pinned
  // wrapper; the allowlists below still decide whether the failure is expected.
  const code =
    nativeCode ??
    (tool === 'edit'
      ? /^Could not edit file: [\s\S]*\. Error code: ([A-Z][A-Z0-9]+)\.$/.exec(message)?.[1]
      : undefined);
  if (
    typeof code === 'string' &&
    ['ENOENT', 'ENOTDIR', 'EISDIR', 'EACCES', 'EPERM', 'ELOOP'].includes(code)
  ) {
    return { level: 'INFO', error_code: 'file_unavailable' };
  }
  if (
    typeof code === 'string' &&
    ['ENOSPC', 'EDQUOT', 'EIO', 'ENOMEM', 'EMFILE', 'ENFILE'].includes(code)
  ) {
    return { level: 'ERROR', error_code: 'filesystem_failure' };
  }
  if (
    /^(File exceeds the builtin |Image exceeds the builtin |Only regular files are supported|Search paths must be regular)/.test(
      message,
    )
  ) {
    return { level: 'INFO', error_code: 'file_limit' };
  }
  // Pi's pinned plain Error messages do not expose typed business codes.
  // Match only known categories for severity; never log the matched message.
  if (
    tool === 'edit' &&
    /^(Edit tool input is invalid|Could not find (the (exact )?text|edits\[)|Found \d+ occurrences|oldText must not be empty|edits\[\d+\]|No changes made to )/.test(
      message,
    )
  ) {
    return { level: 'INFO', error_code: 'invalid_edit' };
  }
  if (
    /^(Offset \d+ is beyond end of file|Path not found:|Not a directory:|regex parse error:)/.test(
      message,
    )
  ) {
    return { level: 'INFO', error_code: 'invalid_tool_input' };
  }
  return { level: 'ERROR', error_code: 'tool_execution_failed' };
}

import "@typespec/json-schema";
using TypeSpec.JsonSchema;

@jsonSchema("https://builtin-mcp.invalid/schemas/")
namespace BuiltinBuiltin;

@minLength(1)
@maxLength(4096)
scalar Cursor extends string;
@pattern("^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$")
scalar ProcessID extends string;
@pattern("^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$")
scalar TurnID extends string;
@minValue(0)
@maxValue(20000)
scalar YieldTime extends int32;
@minValue(1024)
@maxValue(65536)
scalar OutputBudget extends int32;
@minValue(1000)
@maxValue(86400000)
scalar RuntimeBudget extends int32;
@minValue(0)
@maxValue(9007199254740991)
scalar ByteCount extends int64;
@minLength(1)
@maxLength(32)
scalar TerminationSignal extends string;
@minLength(1)
@maxLength(4096)
@pattern("^/[^\\u0000]*$")
@extension("x-max-utf8-bytes", 4096)
scalar WorkingDirectory extends string;

@extension("additionalProperties", false)
@extension("x-tool-name", "bash_start")
@extension("x-output-schema", "BashStartOutput")
@doc("Start a managed Bash process and return its handle, state and initial output after a bounded yield. A running process is not a command failure; use bash_poll for later output.")
@example(#{ command: "printf ready", yield_time_ms: 5000 })
@example(#{ command: "cat", tty: true })
@extension(
  "allOf",
  #[
    #{
      `if`: #{ properties: #{ tty: #{ `const`: true } }, required: #["tty"] },
      then: #{ properties: #{ stdin_open: #{ `const`: true, default: true } } },
      `else`: #{ properties: #{ stdin_open: #{ default: false } } },
    }
  ]
)
model BashStartInput {
  @minLength(1)
  @maxLength(65536)
  @pattern("^[^\\u0000]*[^\\u0000\\s][^\\u0000]*$")
  @extension("x-max-utf8-bytes", 65536)
  command: string;

  cwd?: WorkingDirectory = "/workspace";
  tty?: boolean = false;

  @doc("Defaults to true with tty=true, otherwise false. Explicit false with tty=true is invalid.")
  stdin_open?: boolean;

  yield_time_ms?: YieldTime = 5000;
  max_output_bytes?: OutputBudget = 16384;

  @doc("Also bounded by the trusted launch policy; poll/write do not extend it.")
  max_runtime_ms?: RuntimeBudget = 600000;
}

@extension("additionalProperties", false)
@extension("x-tool-name", "bash_poll")
@extension("x-output-schema", "BashPollOutput")
@doc("Read a managed process's current state and cursor-based output. Canceling this observation does not terminate the process or extend its runtime deadline.")
@example(#{
  process_id: "proc-00000000000000000000000001",
  cursor: "opaque-position",
  yield_time_ms: 5000,
})
model BashPollInput {
  process_id: ProcessID;
  cursor?: Cursor;
  yield_time_ms?: YieldTime = 5000;
  max_output_bytes?: OutputBudget = 16384;
}

@extension("additionalProperties", false)
@extension("x-tool-name", "bash_write")
@extension("x-output-schema", "BashWriteOutput")
@doc("Write UTF-8 text to a managed process or close ordinary stdin. Input receipts distinguish accepted, partial and indeterminate writes; never resend uncertain text automatically.")
@example(#{ process_id: "proc-00000000000000000000000001", text: "continue\n" })
@example(#{ process_id: "proc-00000000000000000000000001", close_stdin: true })
@extension(
  "anyOf",
  #[
    #{ required: #["text"], properties: #{ text: #{ type: "string", minLength: 1 } } },
    #{ required: #["close_stdin"], properties: #{ close_stdin: #{ `const`: true } } }
  ]
)
model BashWriteInput {
  process_id: ProcessID;

  @maxLength(16384)
  @pattern("^[^\\u0000]*$")
  @extension("x-max-utf8-bytes", 16384)
  text?: string;

  close_stdin?: boolean = false;
}

@extension("additionalProperties", false)
@extension("x-tool-name", "bash_terminate")
@extension("x-output-schema", "BashTerminateOutput")
@doc("Request termination of the managed process group. Graceful mode uses TERM then a fixed KILL grace period; the response is current evidence, not proof of exit.")
@example(#{ process_id: "proc-00000000000000000000000001", mode: "graceful" })
model BashTerminateInput {
  process_id: ProcessID;
  mode?: "graceful" | "force" = "graceful";
}

@extension("additionalProperties", false)
@extension("x-tool-name", "bash_list")
@extension("x-output-schema", "BashListOutput")
@doc("List this Session's managed processes and retained exit records. Results contain metadata only; use bash_poll to retrieve output.")
@example(#{ limit: 50 })
model BashListInput {
  cursor?: Cursor;

  @minValue(1)
  @maxValue(200)
  limit?: int32 = 50;
}

enum ProcessState {
  starting,
  running,
  terminating,
  exited,
  lost,
}
enum StopReason {
  user_requested,
  deadline_reached,
  session_closed,
}

const processDeadlineConstraints = #[
  #{
    `if`: #{
      properties: #{ state: #{ `enum`: #["running", "terminating", "exited"] } },
      required: #["state"],
    },
    then: #{ properties: #{ deadline_at: #{ type: "string", format: "date-time" } } },
  }
];

model ProcessSnapshot {
  process_id: ProcessID;
  state: ProcessState;
  exit_code: int32 | null;
  termination_signal: TerminationSignal | null;

  @doc("Null only when starting/lost and no confirmed spawn deadline is known.")
  deadline_at: utcDateTime | null;

  group_quiescent: boolean | null;
}

@extension("additionalProperties", false)
@extension("allOf", processDeadlineConstraints)
model ProcessSummary {
  ...ProcessSnapshot;
  created_at: utcDateTime;
  started_at: utcDateTime | null;
  exited_at: utcDateTime | null;
  cwd: WorkingDirectory;
  origin_turn_id: TurnID;
  stop_reason: StopReason | null;
}

@extension("additionalProperties", false)
model OutputChunk {
  stream: "stdout" | "stderr" | "pty";
  @maxLength(65536) @extension("x-max-utf8-bytes", 65536) text: string;
  encoding_replaced?: true;
}
model OutputPage {
  @doc("Returned text is additionally bounded by this call's max_output_bytes in UTF-8; JSON framing is charged separately. The collection cap also bounds empty chunks.")
  @maxItems(65536)
  chunks: OutputChunk[];

  next_cursor: Cursor;
  first_available_cursor: Cursor;
  has_more: boolean;
  output_complete: boolean;
  output_truncated: boolean;
}

@extension("additionalProperties", false)
model ToolError {
  @pattern("^[a-z][a-z0-9_]{0,63}$") code: string;
  @minLength(1) @maxLength(256) message: string;
}
@extension("additionalProperties", false)
model ToolFailure {
  error: ToolError;
}
@extension("additionalProperties", false)
@extension("allOf", processDeadlineConstraints)
model ProcessFailure {
  ...ProcessSnapshot;
  error: ToolError;
  first_available_cursor?: Cursor;
}
@extension("additionalProperties", false)
@extension("allOf", processDeadlineConstraints)
model ProcessOutput {
  ...ProcessSnapshot;
  ...OutputPage;

  @doc("May accompany a complete snapshot for a nonzero/signal exit; isError remains a standard MCP result flag.")
  error?: ToolError;
}

@extension("additionalProperties", false)
model BashWriteAccepted {
  process_id: ProcessID;
  accepted_bytes: ByteCount;
  stdin_closed: boolean;
  input_state: "accepted";
}
@extension("additionalProperties", false)
model BashWritePartial {
  process_id: ProcessID;
  accepted_bytes: ByteCount;
  stdin_closed?: boolean;
  input_state: "partial";
  error: ToolError;
}
@extension("additionalProperties", false)
model BashWriteIndeterminate {
  process_id: ProcessID;
  accepted_bytes: null;
  stdin_closed?: boolean;
  input_state: "indeterminate";
  error: ToolError;
}
@extension("additionalProperties", false)
@extension("allOf", processDeadlineConstraints)
model BashTermination {
  ...ProcessSnapshot;
  termination_requested: boolean;
  termination_mode: "graceful" | "force";
}
@extension("additionalProperties", false)
model BashProcessPage {
  @maxItems(200) items: ProcessSummary[];
  next_cursor: Cursor | null;
  has_more: boolean;
}

const outputExample = #{
  process_id: "proc-00000000000000000000000001",
  state: ProcessState.running,
  exit_code: null,
  termination_signal: null,
  group_quiescent: false,
  deadline_at: utcDateTime.fromISO("2026-10-28T00:10:00Z"),
  chunks: #[#{ stream: "stdout", text: "ready\n" }],
  next_cursor: "opaque-next",
  first_available_cursor: "opaque-first",
  has_more: false,
  output_complete: false,
  output_truncated: false,
};
const missingProcessExample = #{
  error: #{ code: "process_not_found", message: "Process is unavailable" },
};

@extension("type", "object")
@example(outputExample)
@example(#{ error: #{ code: "spawn_failed", message: "Process could not be started" } })
union BashStartOutput {
  ProcessOutput,
  ProcessFailure,
  ToolFailure,
}
@extension("type", "object")
@example(outputExample)
@example(missingProcessExample)
union BashPollOutput {
  ProcessOutput,
  ProcessFailure,
  ToolFailure,
}
@extension("type", "object")
@example(#{
  process_id: "proc-00000000000000000000000001",
  accepted_bytes: 9,
  stdin_closed: false,
  input_state: "accepted",
})
@example(#{
  process_id: "proc-00000000000000000000000001",
  accepted_bytes: 3,
  input_state: "partial",
  error: #{ code: "input_write_failed", message: "Input was only partially written" },
})
@example(#{
  process_id: "proc-00000000000000000000000001",
  accepted_bytes: null,
  input_state: "indeterminate",
  error: #{ code: "execution_indeterminate", message: "Input needs reconciliation" },
})
union BashWriteOutput {
  BashWriteAccepted,
  BashWritePartial,
  BashWriteIndeterminate,
  ToolFailure,
}
@extension("type", "object")
@example(#{
  process_id: "proc-00000000000000000000000001",
  state: ProcessState.terminating,
  exit_code: null,
  termination_signal: null,
  group_quiescent: false,
  deadline_at: utcDateTime.fromISO("2026-10-28T00:10:00Z"),
  termination_requested: true,
  termination_mode: "graceful",
})
@example(missingProcessExample)
union BashTerminateOutput {
  BashTermination,
  ProcessFailure,
  ToolFailure,
}
@extension("type", "object")
@example(#{ items: #[], next_cursor: null, has_more: false })
@example(missingProcessExample)
union BashListOutput {
  BashProcessPage,
  ToolFailure,
}

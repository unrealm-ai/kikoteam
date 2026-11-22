import "@typespec/json-schema";
using TypeSpec.JsonSchema;

@jsonSchema("https://builtin-mcp.invalid/schemas/activity/")
namespace BuiltinInternal;

@minValue(0) @maxValue(9007199254740991)
scalar BuiltinActivityCounter extends int64;
@minValue(1) @maxValue(9007199254740991)
scalar BuiltinActivityRevision extends int64;

model BuiltinActivityMetadata is Record<unknown>;

model BuiltinActivitySource {
  ...Record<never>;
  @pattern("^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$") workspace_id: string;
  @pattern("^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$") session_id: string;
  @pattern("^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$") environment_instance_id: string;
  @pattern("^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$") executor_boot_id: string;
  @pattern("^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$") connection_id: string;
  binding_id: "builtin";
  config_revision: BuiltinActivityRevision;
}

model BuiltinActivityGate {
  ...Record<never>;
  @pattern("^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$") action_id: string;
  revision: BuiltinActivityRevision;
}

@doc("Parameters of the fixed private MCP JSON-RPC method builtin/processes/activity, never a tool or Harness method. Only the launch-owned stdio peer may invoke it. Serialized params <=8192 UTF-8 bytes, strict business fields; _meta is the standard MCP extension object. Before ready publication, Go synchronizes the actual source/gate directly through the initialized peer; no manager lock, SQLite transaction or execution fence spans the network wait. Missing management launch identity/method, source mismatch, protocol failure or peer loss is unknown, not idle.")
@extension("x-mcp-method", "builtin/processes/activity")
@extension("x-max-message-bytes", 8192)
model BuiltinActivityRequest {
  ...Record<never>;
  source: BuiltinActivitySource;
  @doc("Current active Stop identity, or null after a proven release/no gate. Not a request to invent or clear a Go gate; Go has already persisted and fenced this state.")
  stop_gate: BuiltinActivityGate | null;
  @doc("Go executor state_revision, NOT activity_revision. Monotonic within this exact boot/source, including gate-only changes. TS rejects older revisions or a different gate at an equal revision. The first valid sync opens the initially closed tool boundary only if gate=null; active gate rejects new side effects while already admitted work drains. Pure synchronization does not increment the TS activity revision.")
  local_revision: BuiltinActivityRevision;
  _meta?: BuiltinActivityMetadata;
}

// This model is the handler business result before MCP SDK serialization.
// On the 2026 wire the SDK adds resultType="complete" and standard _meta.
// The receiving protocol adapter first validates the full <=8192-byte wire
// result (including duplicate keys), requires complete, then removes only
// resultType before validating this model. input_required is not supported.
@doc("Result of builtin/processes/activity, <=8192 UTF-8 bytes. Echo the exact source/gate/local_revision only after synchronizing the gate and checking all persisted and in-memory work. Counts describe distinct retention facts, not operation outcomes or an additive process total; a running group may also have pending output. verified=false never proves idle even if numeric counters are zero. No URLs, credentials, process command/output, PIDs, paths or arbitrary diagnostic messages. Go rechecks its own source, peer liveness, boot/state revision and gate after the call before publishing evidence. Busy/unknown does not prevent directory readiness if gate synchronization succeeded.")
model BuiltinActivityResult {
  ...Record<never>;
  source: BuiltinActivitySource;
  stop_gate: BuiltinActivityGate | null;
  local_revision: BuiltinActivityRevision;
  @doc("Persistent TS process activity revision. Every retention-relevant mutation advances it with its durable record; zero->busy->zero remains visible between samples. Ordinary reads, unchanged samples and gate synchronization do not advance it. A changed connection is a new source, never proof old groups are gone.")
  revision: BuiltinActivityCounter;
  verified: boolean;
  @doc("Known active process groups, including admitted starts and termination in progress. Complete ledger, not a bounded bash_list page.")
  active: BuiltinActivityCounter;
  @doc("Process groups or accepted work whose survival/cleanup cannot be established, including sticky uncertainty after file-worker cancellation, loss or restart. Such a file call leaves pending_writes only when its uncertain tail is durably retained here; inspection failure cannot become an empty set.")
  `unknown`: BuiltinActivityCounter;
  @doc("Pending stdin writes, output drain, unconfirmed output/metadata persistence and all admitted file-worker calls (reads as well as writes) awaiting confirmed completion. A file call is counted here while in flight, then removed only on confirmed completion or durable transfer to unknown. Cancellation of the awaiting RPC does not imply the underlying write completed.")
  pending_writes: BuiltinActivityCounter;
  _meta?: BuiltinActivityMetadata;
}

// Management-only facts carried on the launcher-owned MCP connection. These
// types are not model tool inputs; the wire validator uses the root contract.
export interface ActivitySource {
  workspace_id: string;
  session_id: string;
  environment_instance_id: string;
  executor_boot_id: string;
  connection_id: string;
  binding_id: 'builtin';
  config_revision: number;
}

export interface ActivityGate {
  action_id: string;
  revision: number;
}

export interface ActivityRequest {
  source: ActivitySource;
  stop_gate: ActivityGate | null;
  local_revision: number;
  _meta?: Record<string, unknown>;
}

export interface ActivityResult extends ActivityRequest {
  revision: number;
  verified: boolean;
  active: number;
  unknown: number;
  pending_writes: number;
}

export class ActivityConflict extends Error {
  constructor() {
    super('Process activity source or revision is unavailable.');
  }
}

function sameGate(a: ActivityGate | null, b: ActivityGate | null): boolean {
  return a === null || b === null
    ? a === b
    : a.action_id === b.action_id && a.revision === b.revision;
}

export class ActivityControl {
  private localRevision = 0;
  private gate: ActivityGate | null = null;

  constructor(readonly source: Readonly<ActivitySource>) {}

  get blocked(): boolean {
    return this.localRevision === 0 || this.gate !== null;
  }

  synchronize(request: ActivityRequest): void {
    if (
      Object.keys(this.source).some(
        (key) =>
          this.source[key as keyof ActivitySource] !== request.source[key as keyof ActivitySource],
      ) ||
      request.local_revision < this.localRevision ||
      (request.local_revision === this.localRevision && !sameGate(request.stop_gate, this.gate))
    )
      throw new ActivityConflict();
    this.localRevision = request.local_revision;
    this.gate = request.stop_gate ? { ...request.stop_gate } : null;
  }

  matches(request: ActivityRequest): boolean {
    return request.local_revision === this.localRevision && sameGate(request.stop_gate, this.gate);
  }
}

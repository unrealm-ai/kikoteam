// A platform-owned extension loaded explicitly through Pi's public API.
// Pi/ACP and their built-in MCP implementation are used without patches.
import { readFileSync } from "node:fs";
import { createMcpToolName } from "@earendil-works/pi-mcp";
import { formatSkillsForPrompt } from "@earendil-works/pi-coding-agent";

export function installPolicy(pi, policy, request = fetch) {
  const permitted = new Map();
  const receipts = new Map();
  for (const plugin of policy.plugins ?? []) {
    const server = plugin.server ?? `plugin_${plugin.plugin_id}`;
    for (const tool of plugin.tools) {
      const entry = { plugin, tool };
      permitted.set(createMcpToolName(server, tool.name), entry);
      permitted.set(createMcpToolName(server, tool.name, () => true), entry);
    }
  }

  pi.on("before_agent_start", (event) => {
    pi.setActiveTools(pi.getActiveTools().filter((name) => !name.startsWith("mcp__plugin_") || permitted.has(name)));
    const options = event?.systemPromptOptions;
    if (options) {
      // Reuse Pi's own parsed metadata and formatter, including its native
      // disable-model-invocation behavior. No Skill body is injected here.
      const catalog = formatSkillsForPrompt(options.skills ?? [], "indirect");
      if (catalog) options.sections.platform_skills = `${catalog}\nRead Skill documents with builtin read; resolve references/scripts against the Skill directory. Execute scripts through builtin bash_start with the appropriate interpreter. Default command cwd remains the shared project.`;
      else delete options.sections.platform_skills;
    }
  });

  async function call(body, signal, timeout = 22000) {
    const signals = [AbortSignal.timeout(timeout)];
    if (signal) signals.push(signal);
    const response = await request(policy.url, {
      method: "POST",
      headers: { Authorization: `Bearer ${policy.token}`, "Content-Type": "application/json" },
      body: JSON.stringify(body),
      signal: AbortSignal.any(signals),
      redirect: "error",
    });
    if (!response.ok) throw new Error("Capability authorization unavailable");
    return response.json();
  }

  pi.on("tool_call", async (event, ctx) => {
    if (["list_mcp_resources", "list_mcp_resource_templates", "read_mcp_resource"].includes(event.toolName)) {
      return { block: true, reason: "Plugin resources are not granted by tool permissions." };
    }
    if (!event.toolName.startsWith("mcp__plugin_")) return;
    const entry = permitted.get(event.toolName);
    const actual = pi.getAllTools().find((tool) => tool.name === event.toolName);
    if (!entry || !actual) return { block: true, reason: "Tool is outside the authorized capability package." };
    try {
      const result = await call({
        phase: entry.plugin.relay ? "check" : "authorize",
        plugin_id: entry.plugin.plugin_id,
        account_id: entry.plugin.account_id,
        tool_name: entry.tool.name,
        native_tool_call_id: event.toolCallId,
        input_schema: actual.parameters,
      }, ctx.signal);
      if (!result.allowed) return { block: true, reason: "Tool permission, account or schema is no longer valid." };
      if (result.call_id) receipts.set(event.toolCallId, result.call_id);
    } catch {
      return { block: true, reason: "Current tool authorization could not be verified." };
    }
  });

  pi.on("tool_result", async (event) => {
    const id = receipts.get(event.toolCallId);
    if (!id) return;
    receipts.delete(event.toolCallId);
    const entry = permitted.get(event.toolName);
    // A native MCP response (including a server-reported business error) has
    // structured server/tool metadata. A transport exception does not prove
    // the remote operation stopped, so leave its outcome unknown.
    if (event.isError && (!entry || event.details?.server !== (entry.plugin.server ?? `plugin_${entry.plugin.plugin_id}`) || event.details?.tool !== entry.tool.name)) return;
    try {
      await call({ phase: "finish", call_id: id, status: event.isError ? "failed" : "succeeded" }, undefined, 3000);
    } catch {
      // Leave the authorized record without a final outcome. Never rerun a
      // remote tool or replace its real result to repair an audit write.
    }
  });
}

export default function capabilityPolicy(pi) {
  const policy = JSON.parse(readFileSync(process.env.KIKO_CAPABILITY_POLICY, "utf8"));
  installPolicy(pi, policy);
}

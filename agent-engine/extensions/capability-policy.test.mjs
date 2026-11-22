import assert from "node:assert/strict";
import test from "node:test";
import { createMcpToolName } from "@earendil-works/pi-mcp";
import { installPolicy } from "./capability-policy.mjs";

test("permission failures block calls and only actual results finish receipts", async () => {
  const handlers = new Map();
  const plugin = { plugin_id: "00000000-0000-4000-8000-000000000001", tools: [{ name: "lookup" }] };
  const name = createMcpToolName(`plugin_${plugin.plugin_id}`, "lookup");
  const forbidden = createMcpToolName(`plugin_${plugin.plugin_id}`, "delete");
  let active = [name, forbidden, "mcp__builtin__read"];
  const pi = {
    on: (type, handler) => handlers.set(type, handler),
    getAllTools: () => [{ name, parameters: { type: "object", properties: {} } }],
    getActiveTools: () => active,
    setActiveTools: (names) => { active = names; },
  };
  const requests = [];
  let available = true;
  let allowed = true;
  const request = async (_url, options) => {
    const body = JSON.parse(options.body);
    requests.push(body);
    assert.equal(options.headers.Authorization, "Bearer scoped-fixture");
    assert.equal(options.redirect, "error");
    if (!available) throw new Error("network unavailable");
    return { ok: true, json: async () => ({ allowed, call_id: "receipt" }) };
  };
  installPolicy(pi, { plugins: [plugin], url: "https://backend.test/guard", token: "scoped-fixture" }, request);
  await handlers.get("before_agent_start")();
  assert.deepEqual(active, [name, "mcp__builtin__read"]);
  const eventPrompt = { systemPromptOptions: { sections: {}, skills: [
    { name: "automatic", description: "Available automatically", filePath: "/skills/automatic/SKILL.md", disableModelInvocation: false },
    { name: "explicit-only", description: "Explicit command only", filePath: "/skills/explicit/SKILL.md", disableModelInvocation: true },
  ] } };
  await handlers.get("before_agent_start")(eventPrompt);
  assert.match(eventPrompt.systemPromptOptions.sections.platform_skills, /automatic/);
  assert.doesNotMatch(eventPrompt.systemPromptOptions.sections.platform_skills, /explicit-only/);
  assert.equal((await handlers.get("tool_call")({ toolName: forbidden }, {})).block, true);
  assert.equal(requests.length, 0);
  available = false;
  const event = { toolName: name, toolCallId: "native-1", input: { private: "not sent to guard" } };
  assert.equal((await handlers.get("tool_call")(event, {})).block, true);
  available = true;
  allowed = false;
  assert.equal((await handlers.get("tool_call")(event, {})).block, true);
  await handlers.get("tool_result")(event);
  assert.equal(requests.length, 2);
  allowed = true;
  assert.equal(await handlers.get("tool_call")(event, {}), undefined);
  assert.equal(JSON.stringify(requests).includes("not sent to guard"), false);
  assert.equal(requests.at(-1).tool_name, "lookup");
  available = false;
  await handlers.get("tool_result")({ ...event, isError: false });
  assert.equal(requests.at(-1).phase, "finish");
  const count = requests.length;
  await handlers.get("tool_result")({ ...event, isError: false });
  assert.equal(requests.length, count); // No replay to repair a failed audit write.
  available = true;
  await handlers.get("tool_call")(event, {});
  const pending = requests.length;
  await handlers.get("tool_result")({ ...event, isError: true });
  assert.equal(requests.length, pending); // Transport error is an unknown remote outcome.
  await handlers.get("tool_call")(event, {});
  await handlers.get("tool_result")({ ...event, isError: true, details: { server: `plugin_${plugin.plugin_id}`, tool: "lookup" } });
  assert.equal(requests.at(-1).status, "failed"); // A real MCP business error is observable.
  assert.equal((await handlers.get("tool_call")({ toolName: "read_mcp_resource" }, {})).block, true);
});

import test from "node:test";
import assert from "node:assert/strict";
import { mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import registerHumanTools, { refreshWorkMCP } from "./human-tools.mjs";

const listTool = "mcp__work_items__work_item_list_handoff_employees";
const currentTool = "mcp__work_items__work_item_get_current";
const config = (input = "first", role = "ordinary") => ({
  input, work_role: role, work_mcp_url: "https://example.test/mcp/work-items/" + role, work_mcp_token: "fixture-" + input,
});

function fixture(names = [listTool, "mcp__work_items__artifact_publish"]) {
  let active = [...names];
  const calls = [];
  const pi = {
    getActiveTools: () => active,
    getAllTools: () => names.map((name) => ({ name, exposure: active.includes(name) ? "direct" : "hidden" })),
    unregisterMcpServer: (name) => {
      calls.push({ operation: "unregister", name });
      setImmediate(() => { active = []; });
    },
    registerMcpServer: (name, next) => {
      assert.deepEqual(active, [], "old tools must be hidden before re-registering the same name");
      calls.push({ operation: "register", name, next });
      setImmediate(() => { active = [...names]; });
    },
  };
  return { pi, calls };
}

test("work MCP refresh waits out old definitions and registers only the next request headers", async () => {
  const { pi, calls } = fixture();
  await refreshWorkMCP(pi, config("first"), "ordinary");
  await refreshWorkMCP(pi, config("second"), "ordinary");
  assert.deepEqual(calls.map((c) => c.operation), ["unregister", "register", "unregister", "register"]);
  assert.ok(calls.every((c) => c.name === "work_items"));
  assert.deepEqual(calls[1].next.headers, { Authorization: "Bearer fixture-first", "X-Kiko-Work-Input": "first" });
  assert.deepEqual(calls[3].next.headers, { Authorization: "Bearer fixture-second", "X-Kiko-Work-Input": "second" });
  assert.equal(calls[3].next.exposure, "direct");
});

test("work MCP refresh does not admit a role mismatch or falsely accept unexpected exposed tools", async () => {
  const mismatched = fixture();
  await assert.rejects(refreshWorkMCP(mismatched.pi, config("next", "review"), "ordinary"), /kiko_work_mcp_unavailable/);
  assert.deepEqual(mismatched.calls, []);
  const unexpected = fixture([listTool, currentTool]);
  await assert.rejects(refreshWorkMCP(unexpected.pi, config(), "ordinary", AbortSignal.timeout(120)));
  assert.deepEqual(unexpected.calls.map((c) => c.operation), ["unregister", "register"]);
});

test("one task Pi rotates recipient, TaskSpace and plugin relay credentials", async (t) => {
  const dir = mkdtempSync(join(tmpdir(), "kiko-task-credentials-"));
  t.after(() => rmSync(dir, { recursive: true, force: true }));
  const path = join(dir, "config.json");
  const prior = process.env.KIKO_HUMAN_TOOLS_CONFIG;
  process.env.KIKO_HUMAN_TOOLS_CONFIG = path;
  t.after(() => {
    if (prior === undefined) delete process.env.KIKO_HUMAN_TOOLS_CONFIG;
    else process.env.KIKO_HUMAN_TOOLS_CONFIG = prior;
  });
  const names = {
    human_confirmation: ["list_human_confirmation_scopes"],
    task_space_history: ["task_space_history_list_tasks", "task_space_history_get_task", "task_space_history_save_summary"],
    plugin_test: ["lookup"],
  };
  const hooks = new Map(), registrations = new Map();
  const active = () => [...registrations.keys()].flatMap((server) => names[server].map((tool) => "mcp__" + server + "__" + tool));
  const pi = {
    on: (name, handler) => hooks.set(name, handler), registerTool() {},
    unregisterMcpServer: (name) => registrations.delete(name),
    registerMcpServer: (name, config) => registrations.set(name, config),
    getActiveTools: active, getAllTools: () => active().map((name) => ({ name, exposure: "direct" })),
  };
  const write = (input, token) => writeFileSync(path, JSON.stringify({
    mode: "task", input, recipients_mcp: true, recipients_mcp_url: "https://manager.test/human",
    recipients_mcp_token: token, task_space_history_url: "https://manager.test/history", task_space_history_token: "history-" + input,
    relay_servers: { plugin_test: { url: "https://manager.test/relay", toolExposure: { lookup: "direct" } } },
    relay_headers: { plugin_test: { Authorization: "Bearer relay-" + input } },
  }), { mode: 0o600 });
  write("one", "human-one");
  registerHumanTools(pi);
  const start = hooks.get("before_agent_start");
  for (const input of ["one", "two"]) {
    write(input, "human-" + input);
    await start({}, { abort() { assert.fail("fresh credentials should be accepted"); } });
    assert.deepEqual(registrations.get("human_confirmation").headers, { Authorization: "Bearer human-" + input, "X-Kiko-Human-Input": input });
    assert.deepEqual(registrations.get("task_space_history").headers, { Authorization: "Bearer history-" + input, "X-Kiko-Task-Space-Input": input });
    assert.deepEqual(registrations.get("plugin_test"), {
      url: "https://manager.test/relay", headers: { Authorization: "Bearer relay-" + input }, exposure: "hidden", toolExposure: { lookup: "direct" },
    });
  }
  write("three", "");
  let aborted = false;
  await assert.rejects(start({}, { abort() { aborted = true; } }), /kiko_human_mcp_unavailable/);
  assert.ok(aborted);
  assert.equal(registrations.size, 0);
});

test("current assignment and submission IDs are not model inputs; genuine choices and version remain", () => {
  const dir = mkdtempSync(join(tmpdir(), "kiko-human-tools-"));
  const path = join(dir, "config.json");
  const prior = process.env.KIKO_HUMAN_TOOLS_CONFIG;
  process.env.KIKO_HUMAN_TOOLS_CONFIG = path;
  function toolsFor(role) {
    writeFileSync(path, JSON.stringify({ mode: "task", work_role: role }), { mode: 0o600 });
    const tools = new Map();
    registerHumanTools({ on() {}, registerTool(tool) { tools.set(tool.name, tool); } });
    return tools;
  }
  function check(tool, required, optional = ["file_paths", "file_ids"]) {
    assert.deepEqual(tool.parameters.required, required);
    assert.deepEqual(Object.keys(tool.parameters.properties).sort(), [...required, ...optional].sort());
    assert.equal(tool.parameters.additionalProperties, false);
    if (required.includes("expected_version")) {
      assert.deepEqual(tool.parameters.properties.expected_version, { type: "string", pattern: "^[1-9][0-9]*$" });
    }
    assert.equal(tool.executionMode, "sequential");
  }
  try {
    const ordinary = toolsFor("ordinary");
    check(ordinary.get("work_item_handoff"), ["target_employee_id", "summary", "title", "goal", "criteria"]);
    assert.equal(ordinary.has("work_item_submit_result"), false);
    const assignee = toolsFor("assignee");
    check(assignee.get("work_item_handoff"), ["target_employee_id", "summary", "expected_version"]);
    check(assignee.get("work_item_submit_result"), ["expected_version", "result", "completion_evidence"]);
    const review = toolsFor("review");
    check(review.get("work_item_handoff"), ["target_employee_id", "summary", "expected_version"]);
    assert.equal(review.has("work_item_submit_result"), false);
    const reviewHandoff = review.get("work_item_handoff").description;
    assert.match(reviewHandoff, /优先选择本次成果的提交员工.*仍在名单中/);
    assert.match(reviewHandoff, /哪条验收标准没过、具体哪里不符及需要补什么/);
    for (const role of [ordinary, assignee]) {
      assert.doesNotMatch(role.get("work_item_handoff").description, /优先选择本次成果的提交员工/);
    }
  } finally {
    if (prior === undefined) delete process.env.KIKO_HUMAN_TOOLS_CONFIG;
    else process.env.KIKO_HUMAN_TOOLS_CONFIG = prior;
    rmSync(dir, { recursive: true, force: true });
  }
});

test("legacy execution tool uses the same fallback guidance as Manager MCP", () => {
  const dir = mkdtempSync(join(tmpdir(), "kiko-human-routing-"));
  const path = join(dir, "config.json");
  const prior = process.env.KIKO_HUMAN_TOOLS_CONFIG;
  process.env.KIKO_HUMAN_TOOLS_CONFIG = path;
  try {
    writeFileSync(path, JSON.stringify({ mode: "task", token: "test-only" }), { mode: 0o600 });
    const tools = new Map();
    registerHumanTools({ on() {}, registerTool(tool) { tools.set(tool.name, tool); } });
    const contract = JSON.parse(readFileSync(new URL("../../backend/contracts/humanconfirmation/tools.json", import.meta.url), "utf8"));
    const name = "list_human_confirmation_scopes";
    const description = tools.get(name).description;
    assert.equal(description, contract.task.find((tool) => tool.name === name).description);
    assert.match(description, /人选不明确时使用 default_recipient/);
    assert.match(description, /没有默认人就报告阻塞/);
  } finally {
    if (prior === undefined) delete process.env.KIKO_HUMAN_TOOLS_CONFIG;
    else process.env.KIKO_HUMAN_TOOLS_CONFIG = prior;
    rmSync(dir, { recursive: true, force: true });
  }
});

test("ask_human rejects a duplicate approval choice without fencing and accepts corrected zero-field approval", async () => {
  const dir = mkdtempSync(join(tmpdir(), "kiko-human-mode-"));
  const path = join(dir, "config.json");
  const prior = process.env.KIKO_HUMAN_TOOLS_CONFIG;
  const priorFetch = globalThis.fetch;
  process.env.KIKO_HUMAN_TOOLS_CONFIG = path;
  try {
    writeFileSync(path, JSON.stringify({ mode: "task", token: "test-only", input: "fixture", url: "https://example.test/human" }), { mode: 0o600 });
    const tools = new Map();
    const hooks = new Map();
    registerHumanTools({ on(name, handler) { hooks.set(name, handler); }, registerTool(tool) { tools.set(tool.name, tool); } });
    const tool = tools.get("ask_human");
    assert.ok(tool.parameters.required.includes("type"));
    assert.ok(!tool.parameters.required.includes("questions"));
    assert.deepEqual(tool.parameters.properties.type.enum, ["approval", "form"]);
    assert.equal(tool.parameters.properties.decisionRequired, undefined);
    assert.equal(tool.parameters.properties.questionScope, undefined);
    assert.match(tool.description, /同意／拒绝是确认决定，不得作为一道题/);
    assert.match(tool.description, /type=form，questions 至少一道/);
    assert.match(tool.parameters.properties.goal.description, /简短问题（标题）/);
    assert.match(tool.parameters.properties.background.description, /独立的背景说明/);
    const calls = [];
    globalThis.fetch = async (_url, request) => {
      const body = JSON.parse(request.body);
      calls.push(body);
      return Response.json(body.operation === "request" ? {id: "demo"} : {fenced: true});
    };
    const questions = [{id: "approve", text: "是否批准", required: true, answerType: "choice", options: [
      {id: "approve", label: "同意"}, {id: "reject", label: "拒绝"},
    ]}];
    const mixed = await tool.execute("", { type: "approval", questions });
    assert.equal(mixed.isError, true);
    assert.equal(mixed.terminate, undefined);
    assert.match(JSON.parse(mixed.content[0].text).message, /不传 questions/);
    const emptyInformation = await tool.execute("", { type: "form", questions: [] });
    assert.equal(emptyInformation.isError, true);
    assert.match(JSON.parse(emptyInformation.content[0].text).message, /至少一道/);
    assert.deepEqual(calls, []);
    assert.equal(await hooks.get("before_provider_request")({}, { abort() { assert.fail("invalid answer closed the gate"); } }), undefined);
    const corrected = await tool.execute("", { type: "approval" });
    assert.equal(corrected.terminate, true);
    assert.deepEqual(calls, [
      { operation: "request", question: { decisionRequired: true } },
      { operation: "fenced", request_id: "demo" },
    ]);
  } finally {
    globalThis.fetch = priorFetch;
    if (prior === undefined) delete process.env.KIKO_HUMAN_TOOLS_CONFIG;
    else process.env.KIKO_HUMAN_TOOLS_CONFIG = prior;
    rmSync(dir, { recursive: true, force: true });
  }
});

test("ask_human translates a form and disallows forms during reapproval", async () => {
  const dir = mkdtempSync(join(tmpdir(), "kiko-human-form-"));
  const path = join(dir, "config.json");
  const prior = process.env.KIKO_HUMAN_TOOLS_CONFIG;
  const priorFetch = globalThis.fetch;
  process.env.KIKO_HUMAN_TOOLS_CONFIG = path;
  try {
    const config = { mode: "task", token: "test-only", input: "fixture", url: "https://example.test/human" };
    const ask = (flags) => {
      writeFileSync(path, JSON.stringify({ ...config, ...flags }), { mode: 0o600 });
      const tools = new Map();
      registerHumanTools({ on() {}, registerTool(tool) { tools.set(tool.name, tool); } });
      return tools.get("ask_human");
    };
    const calls = [];
    globalThis.fetch = async (_url, request) => {
      const body = JSON.parse(request.body);
      calls.push(body);
      return Response.json(body.operation === "request" ? { id: "demo" } : { fenced: true });
    };
    const form = { type: "form", candidateId: "recipient", rules_version: "3",
      questions: [{ id: "slot", text: "几点？", required: true, answerType: "number" }] };
    const reapproval = ask({ reapproval_only: true });
    assert.deepEqual(reapproval.parameters.properties.type.enum, ["approval"]);
    assert.equal(reapproval.parameters.properties.questions, undefined);
    assert.equal((await reapproval.execute("", form)).isError, true);
    assert.deepEqual(calls, []);
    const approval = ask({});
    assert.equal((await approval.execute("", form)).terminate, true);
    const { type: _type, ...expected } = form;
    assert.deepEqual(calls, [
      { operation: "request", question: { ...expected, decisionRequired: false } },
      { operation: "fenced", request_id: "demo" },
    ]);
  } finally {
    globalThis.fetch = priorFetch;
    if (prior === undefined) delete process.env.KIKO_HUMAN_TOOLS_CONFIG;
    else process.env.KIKO_HUMAN_TOOLS_CONFIG = prior;
    rmSync(dir, { recursive: true, force: true });
  }
});

test("legacy chat exposes the same model values and forwards them for Manager validation", async () => {
  const dir = mkdtempSync(join(tmpdir(), "kiko-human-values-"));
  const path = join(dir, "config.json");
  const prior = process.env.KIKO_HUMAN_TOOLS_CONFIG;
  const priorFetch = globalThis.fetch;
  process.env.KIKO_HUMAN_TOOLS_CONFIG = path;
  try {
    writeFileSync(path, JSON.stringify({ mode: "chat", token: "test-only", input: "fixture", url: "https://example.test/human" }), { mode: 0o600 });
    const tools = new Map();
    registerHumanTools({ registerTool(tool) { tools.set(tool.name, tool); } });
    const tool = tools.get("submit_human_confirmation");
    const contract = JSON.parse(readFileSync(new URL("../../backend/contracts/humanconfirmation/tools.json", import.meta.url), "utf8"));
    const published = contract.chat.find(({ name }) => name === tool.name).inputSchema.properties.answers;
    assert.deepEqual(tool.parameters.properties.answers, published);
    const modelAnswers = [{ questionId: "time_slot", values: ["1"] }, { questionId: "locations", values: ["east", "west"] }];
    const answer = { decision: "information", version: "1", association: "current_message", questionDigest: "a".repeat(64) };
    globalThis.fetch = async (_url, request) => {
      assert.deepEqual(JSON.parse(request.body), { operation: "propose", request_id: "fixture", operationId: "submit-1", answer, modelAnswers });
      return Response.json({ state: "approved" });
    };
    const result = await tool.execute("", { request_id: "fixture", operationId: "submit-1", ...answer, answers: modelAnswers });
    assert.equal(JSON.parse(result.content[0].text).state, "approved");
  } finally {
    globalThis.fetch = priorFetch;
    if (prior === undefined) delete process.env.KIKO_HUMAN_TOOLS_CONFIG;
    else process.env.KIKO_HUMAN_TOOLS_CONFIG = prior;
    rmSync(dir, { recursive: true, force: true });
  }
});

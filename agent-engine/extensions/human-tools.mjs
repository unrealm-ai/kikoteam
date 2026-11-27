// Platform tools registered through the public Pi extension API.
import { readFileSync } from "node:fs";
import { setTimeout } from "node:timers/promises";
import { createMcpToolName, mcpNamespace } from "@earendil-works/pi-mcp";

const schema = (properties = {}, required = []) => ({ type: "object", properties, required, additionalProperties: false });
const string = { type: "string" };
const askName = "ask_human";
const approvalMode = "审批用 type=approval，不传 questions。同意／拒绝是确认决定，不得作为一道题或一组选项。goal 是给确认人看的简短审批问题（标题），background 单独写背景。";
const informationMode = "提交表单（收集信息）用 type=form，questions 至少一道实际问题。选择题用 choice 或 multi_choice 并提供 options，文字或数字填空用 text 或 number。提交表单不能替代审批。";
function askQuestionModeError({ type, questions }, reapprovalOnly) {
  if (type === "approval") return questions === undefined ? "" : approvalMode;
  if (reapprovalOnly) return approvalMode;
  if (type !== "form") return approvalMode + informationMode;
  return Array.isArray(questions) && questions.length > 0 ? "" : informationMode;
}
const workServer = "work_items";
const workUnavailable = "[kiko_work_mcp_unavailable]";
const recipientsServer = "human_confirmation";
const recipientsUnavailable = "[kiko_human_mcp_unavailable]";
const historyUnavailable = "[kiko_task_space_history_mcp_unavailable]";
const taskServers = [
  { name: recipientsServer, url: "recipients_mcp_url", token: "recipients_mcp_token", header: "X-Kiko-Human-Input",
    tools: ["list_human_confirmation_scopes"], error: recipientsUnavailable },
  { name: "task_space_history", url: "task_space_history_url", token: "task_space_history_token", header: "X-Kiko-Task-Space-Input",
    tools: ["task_space_history_list_tasks", "task_space_history_get_task", "task_space_history_save_summary"], error: historyUnavailable },
];
const assistantUnavailable = "[kiko_assistant_mcp_unavailable]";
const assistantFamilies = [
  {
    name: "employee_management", endpoint: "platform", token: "employee_management_token",
    tools: ["get_employee", "list_employee_resources", "update_employee"],
    assistantTools: ["create_employee", "list_employees"],
  },
  {
    name: "assistant_demo", endpoint: "demo", token: "assistant_demo_token",
    tools: [
      "get_order", "get_logistics", "get_support_history", "get_meeting_notes", "get_release_plan",
      "list_project_chat", "get_incident_logs", "get_incident_metrics", "get_release_changes",
      "get_oncall_runbook", "rollback_release",
    ],
  },
];
const roleTools = {
  ordinary: ["work_item_list_handoff_employees"],
  assignee: ["work_item_list_handoff_employees", "work_item_get_current","work_item_read_file"],
  review: ["work_item_list_handoff_employees", "work_item_get_current","work_item_read_file", "work_item_accept"],
};

// Pi does not remove definitions from getAllTools: hiding makes them
// unreachable and removes them from getActiveTools. Check both surfaces.
const toolPrefix = (server) => mcpNamespace(server) + "__";
const visibleMCP = (pi, server) => new Set([
  ...pi.getActiveTools().filter((name) => name.startsWith(toolPrefix(server))),
  ...pi.getAllTools().filter((tool) => tool.name.startsWith(toolPrefix(server)) && tool.exposure !== "hidden").map((tool) => tool.name),
]);
async function waitForMCP(predicate, signal, unavailable) {
  const deadline = Date.now() + 3000;
  for (;;) {
    if (signal?.aborted) throw new Error(unavailable);
    if (predicate()) return;
    if (Date.now() >= deadline) throw new Error(unavailable);
    await setTimeout(20, undefined, { signal });
  }
}

function catalogueReady(pi, name, expected) {
  const active = new Set(pi.getActiveTools());
  const visible = visibleMCP(pi, name);
  return visible.size === expected.length && expected.every((tool) => {
    return [createMcpToolName(name, tool), createMcpToolName(name, tool, () => true)]
      .some((nativeName) => active.has(nativeName) && visible.has(nativeName));
  });
}

export async function refreshWorkMCP(pi, credentials, role, signal) {
  const { work_mcp_url: url, work_mcp_token: token, input } = credentials;
  if (!url || !token || !input || credentials.work_role !== role || !Object.hasOwn(roleTools, role)) {
    throw new Error(workUnavailable);
  }
  await refreshTurnMCP(pi, workServer, url, { Authorization: "Bearer " + token, "X-Kiko-Work-Input": input },
    ["artifact_publish", ...roleTools[role]], signal, workUnavailable);
}

async function refreshTurnMCP(pi, server, url, headers, expected, signal, unavailable, toolExposure) {
  // The built-in MCP extension handles this asynchronously. Remove the
  // old registration and observe tools hidden before testing freshness of
  // the new same-name tools. Old in-flight HTTP requests keep old headers.
  pi.unregisterMcpServer(server);
  await waitForMCP(() => visibleMCP(pi, server).size === 0, signal, unavailable);
  pi.registerMcpServer(server, toolExposure ? { url, headers, exposure: "hidden", toolExposure } : { url, headers, exposure: "direct" });
  await waitForMCP(() => catalogueReady(pi, server, expected), signal, unavailable);
}

function removeAssistantMCP(pi) {
  for (const family of assistantFamilies) pi.unregisterMcpServer(family.name);
}

async function refreshAssistantMCP(pi, credentials, signal) {
  // Engine sends the next prompt only after Pi agent_settled. Native Pi awaits
  // its tool batches before settling: no prior prepared call crosses this
  // registration boundary. A dispatched HTTP call captured its old transport.
  removeAssistantMCP(pi);
  await waitForMCP(() => assistantFamilies.every(({ name }) => visibleMCP(pi, name).size === 0), signal, assistantUnavailable);
  const active = assistantFamilies.filter((family) => credentials[family.token]);
  if (active.length === 0) return;
  const { assistant_mcp_base_url: base, input } = credentials;
  if (!base || !input) throw new Error(assistantUnavailable);
  for (const family of active) {
    pi.registerMcpServer(family.name, {
      url: base + "/" + family.endpoint,
      headers: { Authorization: "Bearer " + credentials[family.token], "X-Kiko-Assistant-Input": input },
      exposure: "direct",
    });
  }
  // Manager decides whether the management catalogue is the 3-tool local one
  // or the full 5-tool KIKO Web catalogue, using the signed current Input.
  await waitForMCP(() => active.every(({ name, tools, assistantTools }) =>
    catalogueReady(pi, name, tools) ||
      (assistantTools && catalogueReady(pi, name, [...tools, ...assistantTools])),
  ), signal, assistantUnavailable);
}

export default function humanTools(pi) {
  const config = () => JSON.parse(readFileSync(process.env.KIKO_HUMAN_TOOLS_CONFIG, "utf8"));
  const initial = config();
  const { mode, recipients_mcp, reapproval_only, token, work_role } = initial;
  const scopedServers = taskServers.filter((server) => initial[server.url]).map((server) => ({ ...server, endpoint: initial[server.url] }));
  const relays = Object.entries(initial.relay_servers ?? {});
  async function call(credentials, operation, params, signal, work = false) {
    const timeout = AbortSignal.timeout(5000);
    const response = await fetch(work ? credentials.work_url : credentials.url, {
      method: "POST",
      headers: {
        Authorization: "Bearer " + (work ? credentials.work_token : credentials.token),
        "Content-Type": "application/json",
        [work ? "X-Kiko-Work-Input" : "X-Kiko-Human-Input"]: credentials.input,
      },
      body: JSON.stringify(work ? { operation, arguments: params } : { operation, ...params }),
      signal: signal ? AbortSignal.any([signal, timeout]) : timeout,
      redirect: "error",
    });
    const data = await response.json();
    if (!response.ok) {
      const error = new Error(data.error?.message ?? "人工确认服务暂不可用");
      error.rejected = [400, 401, 403, 404, 409].includes(response.status);
      throw error;
    }
    return data;
  }

  const result = (data, extra) => ({ content: [{ type: "text", text: JSON.stringify(data) }], details: {}, ...extra });
  const register = (name, description, parameters, execute, extra) => {
    pi.registerTool({ name, label: name, description, parameters, execute, ...extra });
  };

  if (mode === "chat") {
    const operationId = { type: "string", pattern: "^[A-Za-z0-9_-]{1,64}$" };
    register("list_pending_human_confirmations", "按创建时间倒序分页查询当前用户的确认单及当前消息证据。不带编号默认查 pending；提供编号但不传 status 时，精确查该单的待确认或已处理状态。明确传 status 时只查指定状态。已处理结果仅供查询。可传完整 ID 或短码精确定位。", schema({
        requestId: string, status: { enum: ["pending", "resolved"] }, page: { type: "integer", minimum: 1 }, pageSize: { type: "integer", minimum: 1, maximum: 50 },
      }), async (_id, { requestId, status, page, pageSize }, signal) =>
        result(await call(config(), "pending", { request_id: requestId, status, page, pageSize }, signal)));
    register("submit_human_confirmation", "提交当前消息中明确对应一张确认单的答复。先查询问题。每题统一传 values 字符串数组：选择题填选项 ID，单选及填空恰好一项，多选按实际选择传一项或多项。没有小题答案时传 answers: []。题目不明确就直接追问；若答复不完整或回执返回 explicit_decision_required，提交 needs_more 和 clarificationText，最终回复原样发出追问。approve 或 reject 的 text 只填用户明确提供的说明，没有就省略，不替用户写理由。revise 的 text 填入用户修改意见。每次提交指定当前消息内唯一的 operationId，例如 submit-1；超时用原标识查回执，原样重试沿用，变更内容用新标识。",
      schema({ request_id: string, operationId, version: string, questionDigest: string,
        association: { enum: ["current_message", "clarification", "unclear"] },
        decision: { enum: ["approve", "reject", "information", "needs_more", "revise"] },
        text: string, clarificationText: string, evidenceSegmentIds: { type: "array", items: string },
        answers: { type: "array", maxItems: 12, items: schema({ questionId: string, values: { type: "array", minItems: 1, maxItems: 20, uniqueItems: true, description: "单选和填空恰好一项；选择题传选项 ID，数字填空传数字字符串。", items: { type: "string", minLength: 1, maxLength: 2000 } } }, ["questionId", "values"]) },
      }, ["request_id", "operationId", "version", "questionDigest", "association", "decision", "answers"]),
      async (_id, { request_id, operationId, answers, ...answer }, signal) => result(await call(config(), "propose", { request_id, operationId, answer, modelAnswers: answers }, signal)));
    register("get_human_confirmation_receipt", "提交超时或结果未知时，按当前用户消息内的原提交标识和确认单 ID 查回执。unknown 表示暂未查到，已排队不代表任务完成。",
      schema({ request_id: string, operationId }, ["request_id", "operationId"]),
      async (_id, { request_id, operationId }, signal) => result(await call(config(), "receipt", { request_id, operationId }, signal)));
    return;
  }

  const terminal = new Set([askName, "work_item_handoff", "work_item_submit_result"]);
  let gate = false;
  let roundConfig;
  pi.on("before_agent_start", async (_event, ctx) => {
    gate = false;
    roundConfig = undefined;
    const assistant = mode === "task" && !reapproval_only;
    if (!work_role && !assistant) return;
    // Snapshot credentials for the entire native run. Do not reload mutable
    // credentials from a delayed tool or retry after a new Turn has opened.
    let failure = work_role ? workUnavailable : assistantUnavailable;
    try {
      const current = config();
      if (work_role) await refreshWorkMCP(pi, current, work_role, ctx.signal);
      for (const server of scopedServers) {
        failure = server.error;
        if (!current.input || !current[server.token] || current[server.url] !== server.endpoint) throw new Error(failure);
        await refreshTurnMCP(pi, server.name, server.endpoint, {
          Authorization: "Bearer " + current[server.token], [server.header]: current.input,
        }, server.tools, ctx.signal, failure);
      }
      if (assistant) {
        failure = assistantUnavailable;
        await refreshAssistantMCP(pi, current, ctx.signal);
      }
      await Promise.all(relays.map(async ([name, { url, toolExposure }]) => {
        // A disconnected optional plugin should not block an unrelated task.
        try {
          const headers = current.relay_headers?.[name];
          if (!headers?.Authorization) throw new Error("Missing relay credentials");
          await refreshTurnMCP(pi, name, url, headers, Object.keys(toolExposure), ctx.signal, "Relay unavailable", toolExposure);
        } catch {
          pi.unregisterMcpServer(name);
        }
      }));
      roundConfig = Object.freeze(current);
    } catch {
      gate = true;
      try {
        if (work_role) pi.unregisterMcpServer(workServer);
        for (const server of scopedServers) pi.unregisterMcpServer(server.name);
        for (const [name] of relays) pi.unregisterMcpServer(name);
        if (assistant) removeAssistantMCP(pi);
      } finally {
        await ctx.abort();
        throw new Error(failure);
      }
    }
  });
  pi.on("tool_call", (event, ctx) => {
    const assistant = [...ctx.sessionManager.getBranch()].reverse().find((entry) => entry.type === "message" && entry.message.role === "assistant");
    const boundaries = assistant?.message?.content.filter((part) => part.type === "toolCall" && terminal.has(part.name)) ?? [];
    if (gate || boundaries.length > 1 || (boundaries.length === 1 && event.toolName !== boundaries[0].name)) {
      return { block: true, terminate: true, reason: "每批仅允许一个结束当前执行的动作。同批其他操作未执行。" };
    }
  });
  pi.on("before_provider_request", (_event, ctx) => {
    // A malformed or unknown sibling may bypass tool_call and prevent batch
    // termination. Keep the provider and all subsequent tools closed.
    if (gate) ctx.abort();
  });

  if (work_role) {
    const text = (maxLength) => ({ type: "string", minLength: 1, maxLength });
    const identifier = { type: "string", format: "uuid" };
    const version = { type: "string", pattern: "^[1-9][0-9]*$" };
    const fields = work_role === "ordinary" ? { title: text(200), goal: text(4000), criteria: text(4000) }
      : { expected_version: version };
    const files = {
      file_paths: {type:"array",maxItems:32,items:text(4096),description:"明确选给下一环节的本任务项目相对路径，固定为本次版本。"},
      file_ids: {type:"array",maxItems:32,items:identifier,description:"从本环节获准的 files 中选取要继续移交的版本。"}
    };
    const handoff = { target_employee_id: identifier, summary: text(4000), ...fields };
    register("work_item_handoff", work_role === "ordinary"
      ? "单独调用。当前任务确需另一位员工继续处理时使用。先用 work_item_list_handoff_employees 查询获准接手的员工，按职责和能力选择。给出目标、验收标准和必要摘要。受理后挂起原任务，成果会回到原任务验收。"
      : work_role === "review"
        ? "单独调用。按原标准验收失败时退回整改。先用 work_item_get_current 核对本次提交和版本（无需传提交 ID），用 work_item_list_handoff_employees 查询可接手名单，优先选择本次成果的提交员工（验收输入中的员工），前提是仍在名单中。若缺陷明确归属其他有资格的员工，可选择对应员工。写清哪条验收标准没过、具体哪里不符及需要补什么。原任务等待修订成果。"
        : "单独调用。当前事项确需下一位员工接手时使用。先用 work_item_get_current 核对本环节和版本（无需传环节 ID），再用 work_item_list_handoff_employees 按职责和能力选择接手员工。摘要提供下一环节所需事实。受理后结束当前环节。",
    schema({...handoff,...files}, Object.keys(handoff)), (id, args, signal) => workBoundary("handoff", id, args, signal), { executionMode: "sequential" });
    if (work_role === "assignee") {
      const submit = { ...fields, result: text(10000), completion_evidence: text(4000) };
      register("work_item_submit_result", "单独调用。成果准备好后提交给原发起任务验收，使用 work_item_get_current 返回的版本，无需传环节 ID。提交后结束当前环节。", schema({...submit,...files}, Object.keys(submit)),
        (id, args, signal) => workBoundary("submit", id, args, signal), { executionMode: "sequential" });
    }
  }

  async function workBoundary(operation, id, args, signal) {
    const credentials = roundConfig;
    if (!credentials || gate) throw new Error(workUnavailable);
    try {
      const payload = { ...args, operation_id: id };
      let intent;
      try {
        intent = await call(credentials, operation, payload, signal, true);
      } catch (error) {
        if (error.rejected || signal?.aborted) throw error;
        intent = await call(credentials, operation, payload, signal, true);
      }
      gate = true;
      const deadline = Date.now() + 8000;
      do {
        const snapshot = await call(credentials, "fenced", { boundary_id: intent.id }, signal, true);
        if (snapshot.fenced) {
          return result({ status: "accepted", boundary_id: intent.id }, {
            details: { work_boundary_id: intent.id }, terminate: true,
          });
        }
        await setTimeout(120, undefined, { signal });
      } while (Date.now() < deadline);
    } catch (error) {
      if (error.rejected && !gate) throw error;
    }
    gate = true;
    return result({ status: "work_boundary_unresolved" }, {
      details: { work_boundary_unresolved: true }, terminate: true, isError: true,
    });
  }

  if (!token) return;

  if (!recipients_mcp) {
    register("list_human_confirmation_scopes", "查看可用的确认职责范围。按问题语义匹配 items 的 questionScope，能够确定时使用对应 candidateId；没有匹配或人选不明确时使用 default_recipient 的 candidateId，默认范围无需匹配。没有默认人就报告阻塞。把选中的 candidateId 与 version 一同交给 ask_human。",
      schema(), async (_id, _args, signal) => result(await call(config(), "recipients", {}, signal)));
  }
  const questionFields = {
    candidateId: { type: "string", description: "确认职责列表中选定的候选 ID；没有明确匹配时使用有效默认确认人。" },
    rules_version: { type: "string", description: "与选定候选来自同一次查询的 version。" },
    type: { type: "string", enum: reapproval_only ? ["approval"] : ["approval", "form"], description: reapproval_only ? approvalMode : approvalMode + informationMode },
    background: { type: "string", description: "独立的背景说明，不用重复审批标题。" },
    goal: { type: "string", description: "审批时填写确认人直接看到的简短问题（标题）；提交表单时填写收集信息的目标。" },
    knownFacts: { type: "array", items: schema({ text: string, evidenceRefs: { type: "array", items: string } }, ["text"]) },
    ...(!reapproval_only ? { questions: { type: "array", description: "仅提交表单时必填，至少一道实际问题。审批不传。", minItems: 1, maxItems: 12, items: schema({
      id: string, text: string, required: { type: "boolean" }, answerType: { enum: ["text", "choice", "multi_choice", "boolean", "number"] },
      constraints: { type: "array", items: string },
      options: { type: "array", minItems: 1, maxItems: 20, items: schema({ id: string, label: string }, ["id", "label"]) },
    }, ["id", "text", "required", "answerType"]) } } : {}),
    completionCriteria: { type: "array", items: string },
    proposedAction: string, impactScope: string, ifNotConfirmed: string,
  };
  register(askName, reapproval_only
    ? "提交修改后的方案，向已选职责对应的确认人请求明确批准。交代目标、完成标准、拟执行动作和影响范围。" + approvalMode + "单独调用，成功后等待新批准。"
    : "向已选职责对应的确认人提问，每次只选一种模式。" + approvalMode + informationMode + "明确写出拟执行动作和影响范围。单独调用，成功后暂停当前执行段。",
    schema(questionFields, Object.keys(questionFields).filter((name) => name !== "questions")),
    async (_id, question, signal) => {
      const message = askQuestionModeError(question, reapproval_only);
      if (message) {
        return result({ status: "invalid_question_mode", code: "human_question_mode_conflict", message }, { isError: true });
      }
      const credentials = config();
      try {
        const { type, ...internalQuestion } = question;
        const request = await call(credentials, "request", { question: { ...internalQuestion, decisionRequired: type === "approval" } }, signal);
        gate = true;
        const deadline = Date.now() + 8000;
        do {
          const snapshot = await call(credentials, "fenced", { request_id: request.id }, signal);
          if (snapshot.fenced) {
            return result({ status: "waiting_human", request_id: request.id }, {
              details: { human_request_id: request.id }, terminate: true,
            });
          }
          await setTimeout(120, undefined, { signal });
        } while (Date.now() < deadline);
      } catch (error) {
        if (error.rejected && !gate) throw error;
      }
      gate = true;
      return result({ status: "human_request_unresolved" }, {
        details: { human_request_unresolved: true }, terminate: true, isError: true,
      });
    }, { executionMode: "sequential" });
}

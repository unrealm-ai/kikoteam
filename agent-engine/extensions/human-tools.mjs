// Platform tools registered through the public Pi extension API.
import { readFileSync } from "node:fs";
import { setTimeout } from "node:timers/promises";

const schema = (properties = {}, required = []) => ({ type: "object", properties, required, additionalProperties: false });
const string = { type: "string" };
const askName = "ask_human";
const workServer = "work_items";
const workPrefix = "mcp__work_items__";
const workUnavailable = "[kiko_work_mcp_unavailable]";
const roleTools = {
  ordinary: ["work_item_list_handoff_employees"],
  assignee: ["work_item_list_handoff_employees", "work_item_get_current","work_item_read_file"],
  review: ["work_item_list_handoff_employees", "work_item_get_current","work_item_read_file", "work_item_accept"],
};

// Pi does not remove definitions from getAllTools: hiding makes them
// unreachable and removes them from getActiveTools. Check both surfaces.
const visibleWork = (pi) => new Set([
  ...pi.getActiveTools().filter((name) => name.startsWith(workPrefix)),
  ...pi.getAllTools().filter((tool) => tool.name.startsWith(workPrefix) && tool.exposure !== "hidden").map((tool) => tool.name),
]);
async function waitForWork(predicate, signal, timeout = 3000) {
  const deadline = Date.now() + timeout;
  for (;;) {
    if (signal?.aborted) throw new Error(workUnavailable);
    if (predicate()) return;
    if (Date.now() >= deadline) throw new Error(workUnavailable);
    await setTimeout(20, undefined, { signal });
  }
}
export async function refreshWorkMCP(pi, credentials, role, signal) {
  const { work_mcp_url: url, work_mcp_token: token, input } = credentials;
  if (!url || !token || !input || credentials.work_role !== role || !Object.hasOwn(roleTools, role)) {
    throw new Error(workUnavailable);
  }
  const expected = ["artifact_publish", ...roleTools[role]].map((name) => workPrefix + name);
  // The built-in MCP extension handles this asynchronously. Remove the
  // old registration and observe tools hidden before testing freshness of
  // the new same-name tools. Old in-flight HTTP requests keep old headers.
  pi.unregisterMcpServer(workServer);
  await waitForWork(() => visibleWork(pi).size === 0, signal);
  pi.registerMcpServer(workServer, {
    url, headers: { Authorization: "Bearer " + token, "X-Kiko-Work-Input": input }, exposure: "direct",
  });
  await waitForWork(() => {
    const active = new Set(pi.getActiveTools());
    const visible = visibleWork(pi);
    return visible.size === expected.length && expected.every((name) => active.has(name) && visible.has(name));
  }, signal);
}

export default function humanTools(pi) {
  const config = () => JSON.parse(readFileSync(process.env.KIKO_HUMAN_TOOLS_CONFIG, "utf8"));
  const { mode, recipients_mcp, reapproval_only, token, work_role } = config();
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
    register("list_pending_human_confirmations", "分页核对当前用户的待确认单、当前消息的真实片段、可信引用及已送达的澄清焦点。可传完整 ID 或短码精确定位。", schema({
        requestId: string, page: { type: "integer", minimum: 1 }, pageSize: { type: "integer", minimum: 1, maximum: 50 },
      }), async (_id, { requestId, page, pageSize }, signal) =>
        result(await call(config(), "pending", { request_id: requestId, page, pageSize }, signal)));
    register("submit_human_confirmation", "提交当前消息中明确对应一张确认单的答复。先查询问题。题目不明确就直接追问；若答复不完整或回执返回 explicit_decision_required，提交 needs_more 和 clarificationText，最终回复原样发出追问。revise 的 text 填入用户修改意见。每次提交指定当前消息内唯一的 operationId，例如 submit-1；超时用原标识查回执，原样重试沿用，变更内容用新标识。",
      schema({ request_id: string, operationId, version: string, questionDigest: string,
        association: { enum: ["current_message", "clarification", "unclear"] },
        decision: { enum: ["approve", "reject", "information", "needs_more", "revise"] },
        text: string, clarificationText: string, evidenceSegmentIds: { type: "array", items: string },
        answers: { type: "array", items: schema({ questionId: string, value: string }, ["questionId", "value"]) },
      }, ["request_id", "operationId", "version", "questionDigest", "association", "decision", "answers"]),
      async (_id, { request_id, operationId, ...answer }, signal) => result(await call(config(), "propose", { request_id, operationId, answer }, signal)));
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
    if (!work_role) return;
    // Snapshot work credentials for the entire native run. Do not reload mutable
    // credentials from a delayed tool or retry after a new Turn has opened.
    try {
      const current = config();
      await refreshWorkMCP(pi, current, work_role, ctx.signal);
      roundConfig = Object.freeze(current);
    } catch {
      gate = true;
      pi.unregisterMcpServer(workServer);
      await ctx.abort();
      throw new Error(workUnavailable);
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
        ? "单独调用。按原标准验收失败时退回整改。先用 work_item_get_current 核对本次提交和版本（无需传提交 ID），用 work_item_list_handoff_employees 确定接手员工，并说明具体缺陷。原任务等待修订成果。"
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
    candidateId: string, rules_version: string, questionScope: string,
    decisionRequired: reapproval_only ? { type: "boolean", enum: [true] } : { type: "boolean" },
    background: string, goal: string,
    knownFacts: { type: "array", items: schema({ text: string, evidenceRefs: { type: "array", items: string } }, ["text"]) },
    questions: { type: "array", items: schema({
      id: string, text: string, required: { type: "boolean" }, answerType: { enum: ["text", "choice", "boolean", "number"] },
      constraints: { type: "array", items: string },
    }, ["id", "text", "required", "answerType"]) },
    completionCriteria: { type: "array", items: string },
    proposedAction: string, impactScope: string, ifNotConfirmed: string,
  };
  register(askName, reapproval_only
    ? "提交修改后的方案，向已选职责对应的确认人请求明确批准。交代修改后的问题、必答项、完成标准、拟执行动作和影响范围。单独调用，成功后等待新批准。"
    : "向已选职责对应的确认人提问。提交可直接阅读的问题、必答项、完成标准、拟执行动作和影响范围。只有纯信息请求才设置 decisionRequired=false，默认要求明确决定。单独调用，成功后暂停当前执行段。",
    schema(questionFields, Object.keys(questionFields).filter(field => field !== "decisionRequired")),
    async (_id, question, signal) => {
      const credentials = config();
      try {
        const request = await call(credentials, "request", { question }, signal);
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

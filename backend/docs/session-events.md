# 会话事件协议 v1

**输入来源增量（2026-11-17）**：Input 投影增加 delivery_mode、source_kind 和 report_id；内部 task_report 只发 input.updated，不冒充 user.message。一个 Turn 可关联多个已消费输入，公共 input_id 保留起始输入含义，全部输入由 input.updated 的 turn_id 关联。正文与兼容以[输入及回报设计](session-input-and-task-report.md)为准。

状态：2026-11-16 完整协议设计及最小实现基线。当前已实现 user.message、input.updated、turn.updated、session.updated、session.requires_action、work_item.notice、agent.message、agent.tool_use、agent.tool_result 与连接级 stream.error；其余目录项尚未启用。可靠历史与 SSE 共用事件表，首版不发送临时文本 delta，不提供 Session.event_cursor；使用历史末页 resume_cursor 衔接实时流。14 个操作见[会话 API](session-api.md)，数据约束见[数据模型](../../docs/architecture/session-data-model.md)，实际运行见[说明](../../docs/operations/minimal-agent-runtime.md)。

**本文的 type 是按完整能力预先设计的类型目录，不是本期必须全部实现的清单。后续开发按实际功能需要，从这里查找并采用对应定义；只有启用的类型才需要实现、发布契约和验收。文档中列出类型，不表示服务已支持，也不要求先实现所有类型再交付。**

## 1. 范围与类型目录

本协议描述独立执行 Session 中的用户输入、Agent 输出、工具事实及执行状态。Conversation、Task、Session 与共享 Sandbox 的归属遵循[关系与生命周期](../../docs/architecture/conversation-lifecycle.md)：Task 与 task Session 首版一对一，各 Session 的模型上下文、事件序号和流独立。Session 内按 Input 顺序执行，一个 Input 最多对应一个逻辑 Turn，一轮可以包含多个模型请求、消息和工具调用；Turn 完成不等于 Task 业务完成或共享环境可回收。

五个核心 type 借鉴 [Claude Managed Agents](https://platform.claude.com/docs/en/managed-agents/reference) 的命名；字段、工具归一化、可公开思考内容和续读机制属于本项目定义，不声明第三方 schema 兼容。

| 分类 | type | 交付 |
| --- | --- | --- |
| 用户消息 | `user.message` | 可靠 |
| Agent 消息 | `agent.message` | 可靠 |
| 思考状态及公开内容 | `agent.thinking` | 可靠 |
| 工具调用及进度 | `agent.tool_use` | 可靠 |
| 工具结果 | `agent.tool_result` | 可靠 |
| 子任务业务进展 | `task.progress_updated` | 可靠 |
| 输入状态 | `input.updated` | 可靠 |
| 执行轮次状态 | `turn.updated` | 可靠 |
| 等待用户确认 | `session.requires_action` | 可靠 |
| 会话概况 | `session.updated` | 可靠 |
| 执行错误 | `session.error` | 可靠 |
| 实时文本增量 | `message.delta` | 仅实时 |
| 当前流连接错误 | `stream.error` | 仅实时 |

可靠事件可按游标续读，临时事件只在当前连接交付。普通工具、builtin MCP 和插件 MCP 共用 `agent.tool_use` / `agent.tool_result`，由 source 区分，不另设 MCP 专属 type。输入仍走 POST inputs，停止走 POST stop，默认直接执行获准工具。

每次实现应在该功能的正式契约和交付说明中声明已启用的类型子集，按实际能力验收，不新增类型发现接口作为前置条件。未实现的类型不发占位事件、不制造缺失的上游事实；实现某项功能后，应按本文语义表达它，不能省略必要的受理、正式输出或终态而仍声称历史完整。新增类型或不兼容字段调整先更新本文并核对消费者，再更新契约和实现。

## 2. 公共字段、身份与顺序

公共字段和事件专属字段位于同一层，**不使用事件级 data 或 payload 包装**。content 数组、error、source、execution、usage 及工具参数和结果保留其正常业务结构。历史 items 和 SSE 的 data 行都承载同一个平铺 Event 对象；SSE 的 `data:` 是传输格式，不是 JSON 属性。

| 公共字段 | 类型 / 可空 | 约定 |
| --- | --- | --- |
| type | string ! | 本文定义且当前已启用的事件类型；与 SSE event 名称相同 |
| schema_version | integer ! | 当前为 1，表示该类型的数据结构版本 |
| seq | 十进制字符串 / null | 可靠事件从 "1" 起严格递增；临时事件固定 null |
| session_id | UUID ! | 所属执行 Session，写入者从可信归属解析 |
| input_id | UUID? | 输入、消息、思考、工具和轮次事件关联原 Input；会话级事件为 null |
| turn_id | UUID? | 初始 user.message 和尚未执行的输入更新为 null；执行输出事件必填；会话级事件为 null |
| occurred_at | 时间 ! | 平台记录该事实的时间，UTC RFC 3339；不冒充外部操作的精确发生时间 |

公共字段始终出现，可空字段用 null。专属字段以各节为准，不另设通用 id。资源 ID 为 UUID；消费者将其视为不透明引用。seq、input_seq、turn_no、call_no、revision 和 Token 用量使用无前导零的十进制字符串，避免大整数精度丢失；schema_version、分页 limit、增量索引和后台进程计数为 JSON 整数。

| 身份或顺序字段 | 职责 |
| --- | --- |
| `(session_id, seq)` | 可靠事件唯一身份及顺序；同一事件重放不变，不增加 event_id |
| input_id / input_seq | 一次输入及受理顺序；事件中的 input_seq 映射 HTTP Input 与数据库 inputs.seq |
| turn_id / turn_no | 一轮实际执行及开始顺序，不等同于 Input 顺序 |
| message_id | 同一消息或思考块的预览与正式快照；用户消息直接取 input_id |
| tool_use_id / call_no | 同一次工具调用及轮内首次观察顺序，不代表完成顺序 |
| revision | Session 用户元数据修订号，不随执行进度递增 |

可靠事件与相关状态同事务提交后才推送，已发布事件不能原地修改；更新追加新 seq。服务端在一次事务内先记录输入、轮次、消息或工具事实，再记录本次会话概况。消费者按 seq 排序，不按时间；筛选后存在序号缺口是正常情况，不能据此判定丢失。

可靠消息示例：

```json
{
  "type": "agent.message",
  "schema_version": 1,
  "seq": "12",
  "session_id": "11111111-1111-4111-8111-111111111111",
  "input_id": "22222222-2222-4222-8222-222222222222",
  "turn_id": "33333333-3333-4333-8333-333333333333",
  "occurred_at": "2026-11-16T08:00:00Z",
  "message_id": "44444444-4444-4444-8444-444444444444",
  "content": [{"type": "text", "text": "已完成检查，下面是结果。"}],
  "status": "completed"
}
```

## 3. 用户消息与输入状态

`user.message` 与 `input.updated` 共用下列完整输入投影。以下字段直接与公共字段同级，不包装 Input 对象；HTTP Input 仍使用 text，事件转换成 content，不同时发送两份正文。

| 专属字段 | 类型 / 可空 | 约定 |
| --- | --- | --- |
| message_id | UUID ! | 等于 input_id，不另建用户消息资源 |
| input_seq | 十进制字符串 ! | 输入受理顺序，避免与事件 seq 冲突 |
| submitted_by | string ! | 平台核验的提交主体，不由事件提交者自报 |
| content | TextBlock[] ! | 原样受理的正文，非空文本块数组，保持顺序 |
| status | string ! | queued / started / processed / canceled / rejected |
| cancel_reason | string? | canceled 时为 withdrawn / session_stopped / session_archived，其他状态 null |
| rejection | object? | rejected 时为 {code, message}，其他状态 null |
| received_at | 时间 ! | 输入受理时间 |
| started_at、finished_at | 时间? | 开始及终结时间，尚未发生为 null |

TextBlock 为 `{type: "text", text: string}`。首版用户文本沿用 [Input 校验](session-api.md#1-路径身份与共同约定)：不可全空白，原样保存，不由平台解析 Skill 斜杠命令。Web 私聊允许附件单独发送。Input 查询和用户输入事件可携带有序的 attachments 快照，历史事件缺失时按空数组处理。字段及路径语义见[私聊项目文件](conversation-files.md)。

user.message 在受理事务中只产生一次，初始 status=queued、turn_id=null；它表示输入已接收，不表示模型已开始处理。Input 提交去重命中不再产生事件。开始、终结、撤回和拒绝以 input.updated 提供完整新快照，共用 message_id，客户端更新同一条用户消息。

状态转换为 queued → started → processed，或 queued → canceled / rejected。没有开始的输入取消或拒绝时不建立 Turn；processed 仅表示对应 Turn 已终结，成功与否仍读 Turn。具体停用、撤回和停止竞争按 [API](session-api.md#3-幂等与并发) 与 [事务设计](../../docs/architecture/session-data-model.md#7-事务与并发裁决)裁决。

按 turn_id 筛选时，最初 user.message 的 turn_id=null 不匹配；开始后的 input.updated 包含完整正文和 Turn 归属，仍可还原该轮输入。禁止回填修改已发布受理事件的 turn_id。

## 4. Agent 正式消息

`agent.message` 除公共字段外包含：

| 专属字段 | 类型 / 可空 | 约定 |
| --- | --- | --- |
| message_id | UUID ! | 当前正式消息及其预览的稳定身份 |
| content | TextBlock[] ! | 完整已提交正文，非空数组，保持块顺序 |
| status | string ! | completed / interrupted |

completed 表示该条消息正常结束；interrupted 表示因停止或错误中断，保留已确认产生的部分正文。没有可确认正文时不制造空消息。每个 message_id 最多有一条正式 agent.message，正式内容替换其预览而不是再次拼接。

一轮可以有多条消息，例如工具调用前的说明与调用后的结论。角色由 type 表达，不重复增加 role；不依靠 is_final 或某条消息的 completed 判断整轮成功，轮次终态由 turn.updated 表达。

## 5. 思考状态与公开内容

`agent.thinking` 除公共字段外包含：

| 专属字段 | 类型 / 可空 | 约定 |
| --- | --- | --- |
| message_id | UUID ! | 思考块身份，与正文消息分别编号 |
| status | string ! | running / completed / interrupted |
| content | TextBlock[] / null | Harness 明确公开的思考摘要或文本；没有内容为 null |

同一思考块可以产生开始和结束等完整快照，客户端按 message_id 和更大的 seq 更新，不反复新增。content 非 null 时为非空数组，包含该快照的完整公开内容，不是增量；没有上游依据时不生成推测性思考正文或状态。

官方 Claude Managed Agents 的 agent.thinking 只是进展信号、不携带正文；本项目可选公开 content 是承接 Harness 能力的扩展，不声明与官方字段相同。chat 关闭可配置思考的策略仍见 [Engine 模式](../../agent-engine/docs/chat-and-task.md)，不因目录有此 type 就要求 chat 产生它。

## 6. 工具调用、进度与结果

`agent.tool_use` 与 `agent.tool_result` 共用完整工具快照。source、input、output 等都是专属业务字段，直接位于顶层。

| 专属字段 | 类型 / 可空 | 约定 |
| --- | --- | --- |
| tool_use_id | UUID ! | 平台内稳定调用身份，同一调用的进度和结果不换 ID |
| call_no | 十进制字符串 ! | 轮内首次观察顺序，从 "1" 起 |
| name | string? | 实际工具名；上游未提供则 null，不从 title 猜测 |
| title | string? | 脱敏展示说明 |
| source | object ! | {kind, name}；kind 为 mcp / harness / unknown，name 为已知服务或来源名，否则 null |
| status | string ! | pending / running / completed / failed / canceled / unknown |
| input、output | JSON 值 / null | 已知且允许交付的完整参数、结果或当前进度内容，受下列状态约束 |
| input_state、output_state | string ! | available / unavailable / redacted / too_large |
| summary | string? | 脱敏展示摘要，不冒充完整结果 |
| error | object? | {code, message}；failed / unknown 提供原因，其他状态按实际情况为 null |
| started_at、finished_at | 时间? | 平台首次观察到开始、确认确定结果的时间，不承诺工具内部精确计时；未知结果 finished_at=null |

| type | 允许 status | 含义 |
| --- | --- | --- |
| agent.tool_use | pending / running | 已出现调用或正在执行；实质进度更新追加完整快照 |
| agent.tool_result | completed / failed / canceled / unknown | 已确认结果、取消，或明确的结果未知事实 |

pending 不等于等待人工确认，也可能表示上游仍在准备调用。多个工具可以交错执行，call_no 不保证结果顺序。每次进度都发布当前完整快照，不把上游局部更新直接当成完整记录。

| input_state / output_state | 值的含义 |
| --- | --- |
| available | 对应字段有经过常规秘密清理且允许交付的值；真实 JSON null 也可用此状态表达 |
| unavailable | 尚未产生或上游未提供；对应值为 null |
| redacted | 当前读取主体无详情权限；对应值为 null，只交付摘要 |
| too_large | 完整值超出事件大小预算，未通过该事件保存；对应值为 null |

input / output 保留结构化 JSON，MCP 的 content、structuredContent 等可作为真实结果保留；协议不把所有输出强制拼成字符串。权限投影不改变 seq；当前主体的详情权限以本次查询或订阅核验为准，不把读取者专属状态固化进可靠事实。

unknown 后由当前获准写入者核对出结果，可对同一 tool_use_id 追加 completed / failed / canceled，不能回到 running、创建新调用或复活已结束 Turn；旧代次迟到提交仍按既有归属约束拒绝。必要时追加 turn.updated 更新 has_unknown_tool_results。单个工具失败交给 Harness 决定是否继续，不直接判整轮失败。明确终结的调用不因迟到进度而回退。

**后台进程与工具 RPC 分开。** bash_start 正常返回只说明这次工具调用完成，结果仍可能为进程 running；后续 Turn 可通过 bash_poll / bash_write / bash_terminate 操作所属执行 Session 的进程，使用各自新的 tool_use_id。进程退出不自动改写原 bash_start 的调用结果。进程归属、停止范围和计数见 [Session 后台进程](../../agent-engine/docs/session-runtime.md#后台进程属于执行-session)。

## 7. Turn 与 Session 状态快照

### task.progress_updated

任务 Session 的可靠事件。沿用公共 session_id、input_id、turn_id、seq、occurred_at。source 为 agent 或 executor；plan_version 为十进制字符串；summary、steps 和可空 metric 构成该轮完整快照。metric 含 step_id、done、total、unit，数值只描述对应步骤。计划版本只随 Agent 写入递增，合并所有来源以 Session 的 seq 为准。历史与 SSE 共用相同结构和游标，详情仅在当前任务权限范围内显示。卡片按任务返回的执行状态决定是否仍显示为当前进展。详见[任务记录](task-records.md#对话卡片与执行计划)。

### turn.updated

下列字段与公共字段同级，身份使用公共 turn_id / input_id，不另加 id：

| 专属字段 | 类型 / 可空 | 约定 |
| --- | --- | --- |
| turn_no | 十进制字符串 ! | 实际开始顺序 |
| status | string ! | running / stopping / completed / failed / canceled / interrupted |
| result_summary | string? | 已确认结果摘要，正式答案读取 agent.message |
| error | object? | {code, message}；failed / interrupted 必填，其他状态 null |
| has_unknown_tool_results | boolean ! | 是否有结果未知的调用，终态不意味着没有外部副作用 |
| usage | object? | 已知的 input_tokens / output_tokens / total_tokens 十进制字符串；对象或其未知项为 null，不补零 |
| started_at、finished_at | 时间 !、时间? | 活动轮次 finished_at=null，终态必填 |

首次 running 以及状态或可查询结果实质变化时产生完整快照。终态不可退回活动状态；Harness 自身的有界模型重试仍属于当前 Turn，不增加平台恢复尝试状态机。已确认进程故障按[故障后继续](../../agent-engine/docs/session-runtime.md#6-事件原生历史与故障后继续)中断旧 Turn；用户后续文本作为新 Input，加载同一 Session 的原生历史后开始新 Turn，不重发旧 prompt。

### work_item.notice

事项的首次交接、退回整改、最终验收或明确失败可在原聊天中生成一条可靠通知。字段包含 `work_item_id`、`message_id`、`source_kind=work_item_notice` 和文本 `content`。页面按消息 ID 展示。投递本身不触发新的执行轮次。

### session.updated

公共 input_id / turn_id 为 null，专属字段为：

| 专属字段 | 类型 / 可空 | 约定 |
| --- | --- | --- |
| title | string? | 当前标题 |
| lifecycle | string ! | active / archived |
| revision | 十进制字符串 ! | 用户元数据修订号，仅标题或归档等变化时递增 |
| archived_at | 时间? | 归档时间 |
| execution | object ! | {state, active_turn_id, queued_input_count, background_process_count, block_reason} |

execution.state 为 blocked / stopping / running / queued / idle，判定优先级按该顺序。active_turn_id 是可空 UUID，queued_input_count 为非负整数；background_process_count 为本执行 Session 已确认的后台存活数（非负整数），未知为 null 并 blocked；block_reason 为脱敏机器码或 null。

创建、标题、生命周期、队列或执行概况有持久变化时发布。idle 表示没有活动 Turn 或有效队列且没有未决执行，不等于后台进程全退出或环境可销毁；已确认后台进程可以跨正常 Turn 保留。原执行未知、旧实例未确认隔离时保持 blocked，不能仅因活动指针清空就宣布 idle。背景进程存在时的驻留和回收按当前 Engine 设计，不新增“进程完成即重新执行 Turn”的语义。

session.updated 是概况投影，不重复发送固定配置、凭据、Engine 地址、原生目录或当前调用方专属的 accepts_input / 权限。完整资源和当前准入查询 Session；Agent 停用的派生拒绝投影及后台核对仍按 API 设计执行。客户端按事件 seq 更新概况，不能只依赖 revision 判断执行状态的新旧。

Conversation 归档先关闭父级准入，再逐个归档 Session；子级事件尚未到达、lifecycle 仍为 active 时，也不能推断可以继续提交或领取 Input，应以 Session 查询的当前 accepts_input 和提交结果为准。子级归档及实际收尾变化在各自事件流内发布，不新增父级事件类型，不承诺多个 Session 之间的全局事件顺序。主 chat 或单个 task 的归档事件不表示其他任务或共享环境已停止。

### session.requires_action

Chat 和 Task 使用同一事件。Engine 收到活动 Turn 的 Pi RPC `extension_ui_request`（`method=confirm`）时，在记录问题的事务内追加事件。Session 级公共 `input_id`、`turn_id` 均为 null。`required_actions` 是完整待答列表，每项包含 `type=confirmation`、平台 UUID `request_id`、来源 `turn_id`、`title`、`message`，可选 UTC `expires_at`。普通助手文本沿用 `agent.message`。

Web 首次打开和重连读取 `GET Session` 的 `execution.required_actions`。按 `request_id` 展示确认项，历史事件用于保留提问记录，当前快照决定能否作答。答复、停止和过期通过 `session.updated` 移除对应项。发布时先部署迁移 51 并协调更新消费端和 Engine，再启用新事件。

## 8. 执行错误与流错误

`session.error` 是可靠执行诊断，专属字段直接平铺为 code、message、retry_status。code 是稳定机器码，message 为安全摘要；retry_status 为 retrying / exhausted / not_retrying。错误属于某轮时填写公共 input_id / turn_id，会话级错误则为 null。

session.error 不独立决定终态，资源状态仍以对应 updated 事件和 GET 为准。局部工具失败优先用 agent.tool_result 表达，不要求每次再重复产生会话错误；是否实现独立 session.error 按功能需要确定。

`stream.error` 为临时事件，seq=null，专属字段直接平铺为 code、message、request_id，公共 input_id / turn_id 为 null。发送后关闭当前 SSE，不能借此停止 Turn。它不受业务 type / turn_id 筛选隐藏。

开流前失败使用[统一 HTTP 错误体](session-api.md#9-http-错误与执行错误)。已启用 stream.error 时，开流后的错误按其格式发送再关闭；未启用时可关闭连接，由客户端重连时取得 HTTP 错误，不发送其他形状的占位事件。没有正常 HTTP 响应不能一概推断提交未成功，Input 沿原 key 和内容重试；stop 每次重新覆盖当前已有工作，不使用 key，也不创建控制回执。

## 9. 临时文本增量

`message.delta` 使用公共字段，seq=null，input_id / turn_id 必填，专属字段为：

| 字段 | 类型 | 约定 |
| --- | --- | --- |
| target_type | string | agent.message / agent.thinking |
| message_id | UUID | 对应消息或思考块 |
| segment_id | UUID | 本次生成段身份；新的生成段不能与旧段混接 |
| delta_index | integer | 同一 message_id + segment_id 内从 0 起递增的帧序号 |
| content_index | integer | 对应 content 数组中的非负文本块下标 |
| text | string | 本帧新增文本，不是当前全文 |

```json
{
  "type": "message.delta",
  "schema_version": 1,
  "seq": null,
  "session_id": "11111111-1111-4111-8111-111111111111",
  "input_id": "22222222-2222-4222-8222-222222222222",
  "turn_id": "33333333-3333-4333-8333-333333333333",
  "occurred_at": "2026-11-16T08:00:00Z",
  "target_type": "agent.message",
  "message_id": "44444444-4444-4444-8444-444444444444",
  "segment_id": "55555555-5555-4555-8555-555555555555",
  "delta_index": 0,
  "content_index": 0,
  "text": "我先检查"
}
```

增量只用于临时展示，不持久化、不消耗可靠序号、不推进续读游标。同一消息、段和 delta_index 重复到达忽略；缺口或未取得从 0 开始的连续段时停止拼接，等待正式快照。正式消息到达后丢弃相应预览并用完整正文替换，后续迟到增量不能覆盖它。思考状态 running 的空 content 不表示新增正文；思考最终快照关闭预览。

断线、切换段或 Turn 终结后丢弃未确认预览；缺失的增量不补发、不自动拼成正式消息。跨故障继续使用新 Input / Turn 和新消息身份，不借 segment_id 续跑旧 Turn。没有公开思考文本时不发送思考文本增量。生产者若因负载丢弃了某段增量，应停止该段的后续增量或让消费者通过序号缺口停用预览，不能把中间缺字的拼接视为完整前缀。

这是本项目的 message.delta 定义。官方 Claude Managed Agents 的 [event_start / event_delta](https://platform.claude.com/docs/en/managed-agents/event-deltas) 同样区分预览与正式记录，但字段不直接套用到本项目。

## 10. HTTP 入口与历史分页

路径及认证前缀沿用 [Session API](session-api.md#1-路径身份与共同约定)，以下省略工作区前缀。会话操作仍共 14 个，相关入口为：

| 操作 | 用途 |
| --- | --- |
| POST /sessions/{session_id}/inputs | 提交 text，返回 Input，受理后产生 user.message；仅此类输入提交使用必要去重 |
| GET /sessions/{session_id}/events | 分页可靠历史，不返回临时事件 |
| GET /sessions/{session_id}/events/stream | 追赶可靠历史并实时订阅 |
| POST /sessions/{session_id}/stop | 停止该次受理时已有工作，直接返回 Session；不要求 key |

客户端不能提交完整 Event、选择 seq / Turn 归属或冒充服务端输出。创建 Session 时携带首条 Input 仍按其专门去重约定处理。没有新建 messages、tool-calls、control-requests 或任意 POST events 入口。

GET events 固定按 seq ASC，仅返回可靠事件。参数为：

| 参数 | 规则 |
| --- | --- |
| turn_id | 可选 UUID，核验属于当前 Session，只匹配事件发布时记录的 turn_id |
| type | 可选单值精确匹配，限当前已启用的可靠类型；临时、未知或尚未启用的类型返回 400 invalid_argument |
| cursor | 可选历史分页游标；省略时开始一次新遍历 |
| limit | 默认 50，1–200；调整 limit 不改变此次遍历边界 |

筛选条件按 AND 组合，拒绝未知 sort_by / sort_order 和重复标量查询参数。第一页在同一读取点取得已提交最大 seq=H，仅遍历符合条件的 seq≤H；后续 next_cursor 固定 H、已读边界和筛选条件，不让新事件无限推长历史。

| 响应字段 | 含义 |
| --- | --- |
| items | 本页平铺 Event 数组，空页为 [] |
| next_cursor | 还有匹配历史时为历史分页游标，否则 null |
| resume_cursor | 非末页为 null；末页为此次 H 对应的实时续读游标，空历史也有合法起点 |

```json
{"items": [], "next_cursor": null, "resume_cursor": "opaque_resume_cursor_20_filtered"}
```

历史分页游标与实时游标用途不同，不能互换；均绑定工作区、读取主体、Session、turn_id / type 筛选及格式版本并校验完整性，改变作用域或筛选复用返回 400 invalid_cursor。末页 resume_cursor 表示已经遍历到 H，不是最后一条匹配事件的 seq，即使零匹配也能接续相同筛选。

Session.event_cursor 是无筛选实时游标，用于读取当前快照后的订阅，不能假装已经加载全部消息历史。历史游标不能用于 inputs / turns 等列表；非事件列表仍返回 {items, next_cursor}，不带 resume_cursor，其状态筛选为实时视图。

## 11. SSE、断线续读与消费

GET events/stream 请求 Accept: text/event-stream，支持 turn_id / type，以及 after 或 Last-Event-ID。筛选与历史相同；设置 type 时只交付该可靠类型，不夹带临时正文；未设置 type 时可接收匹配 turn_id 的可用临时增量，stream.error 始终可达。

非空 Last-Event-ID 优先于 URL after。两者都没有时从会话起点追赶可靠历史，再实时推送。可靠事件的 SSE id 为该位置的实时游标；增量、心跳和流错误不设置 id，也不发送空 id 重置位置，符合 [WHATWG SSE 续连规则](https://html.spec.whatwg.org/multipage/server-sent-events.html#the-last-event-id-header)。

```text
id: opaque_resume_cursor_12
event: agent.message
data: {"type":"agent.message","schema_version":1,"seq":"12","session_id":"11111111-1111-4111-8111-111111111111","input_id":"22222222-2222-4222-8222-222222222222","turn_id":"33333333-3333-4333-8333-333333333333","occurred_at":"2026-11-16T08:00:00Z","message_id":"44444444-4444-4444-8444-444444444444","content":[{"type":"text","text":"已完成检查。"}],"status":"completed"}

```

客户端首次进入会话：

1. GET Session 获得当前状态，GET events 开始历史遍历并保持筛选。
2. 按 next_cursor 读完固定 H 内的历史，按 (session_id, seq) 去重。
3. 使用末页 resume_cursor 建立 events/stream，保持相同筛选。
4. 服务端补发 H 之后、建连前已经提交的事件，再实时推送。

例如历史固定 H=20，分页期间新产生 21–23，建连时又产生 24；历史读到 20，流从 21 追赶。禁止用建连时的最新 Session.event_cursor=24 替换末页游标，否则跳过 21–24。

可靠来源是数据库已提交事件，实时通知仅用于唤醒。服务端先建立唤醒订阅再按 seq 追赶并持续检查提交，通知丢失和副本切换不跳过可靠事件。可靠部分递增发送；临时增量仅在追赶完成后按可用情况交付，不承诺补齐错过的段。

重连从最后成功应用的可靠游标续读，可能重复交付。原生 EventSource 的 Last-Event-ID 只说明浏览器收到了事件，不能代替应用处理确认；需跨应用崩溃恢复时，持久化处理游标并据此重建连接。客户端分别按 input_id、turn_id、message_id、tool_use_id 合并对象并记录最后应用的 seq；历史补发不覆盖更大 seq 的状态。

GET Session 快照与事件同读点的 event_cursor 用于识别旧 session.updated；也可在重建历史后再次 GET 校准。可靠更新使用完整投影；不同消息不能因同一 Turn 被合并。遇到未知 type 或不支持的 schema_version，报告契约不兼容并停止推进处理游标，不能无声跳过正式事实；预设计目录不等于允许服务端任意启用消费者不支持的类型。

首版可靠事件随 Session 历史保留，不按短期传输日志清理。游标因格式或签名版本淘汰等原因失效时，开流前返回 410 event_cursor_expired；历史仍在，客户端重新遍历后取得新末页游标。伪造、未来位置或跨范围游标返回 400 invalid_cursor。将来调整保留策略必须同步设计完整历史恢复入口。

查询、续读和发送业务内容前核验当前读取及详情权限，心跳时复核身份；候选心跳间隔 15 秒，使用 SSE 注释。撤权最多发送不含业务内容的 unauthenticated / permission_denied 流错误后关闭，重连重新鉴权。权限投影不改原 seq，同一权限下历史与实时得到相同视图。SSE 断开或慢客户端不取消 Turn；不能只因没有增量判断执行失败，归档仍可读历史和收尾事实。

## 12. Engine 映射与示例流程

Engine 直接消费未修改 Pi 的原生 RPC 事件，不要求补丁、私有事件注入或 MCP Gateway 中转所有工具。工具更新按 [Pi 原生事件协议](https://github.com/earendil-works/pi/blob/v1.5.2/packages/coding-agent/docs/json.md)合并；上游省略字段不意味着清空原值。

| 可观察来源 | 平台投影 |
| --- | --- |
| Agent 文本 chunk | message.delta；按可核验的块边界关闭后发布 agent.message，不把每个 chunk 当正式消息 |
| 公开思考通知 | agent.thinking；只有真实公开文本才填 content 或发布思考增量 |
| Pi tool_execution_start | 建立稳定 tool_use_id，发布 agent.tool_use |
| Pi tool_execution_update / tool_execution_end | 先合并原快照，再按状态发布 agent.tool_use / agent.tool_result |
| Prompt 返回及执行收尾证据 | turn.updated、input.updated 和必要 session.updated |

消息块边界、原生 ID 映射及缺失信息按固定上游版本联调，不承诺适配器没有暴露的消息、工具名、参数、结果或用量。Pi 原生 toolCallId、HTTP/stdio 请求编号、本地 operation_id 与公开 tool_use_id 分别关联，不直接互换，不解析展示文本猜错误或调用关系。

事件历史与 Pi 持久原生历史分开。原生会话加载和历史查询只用于核验，不重复发布 agent.message；公开事件不用于拼接近似原生上下文，也不新增逐轮检查点包或旧 Turn 自动续跑。原生目录、加载失败和故障后新 Input 继续的行为，以 [Engine 原生历史设计](../../agent-engine/docs/session-runtime.md#6-事件原生历史与故障后继续)为准。

以下是相关能力均启用时的示意，不是每个版本必须产生全部事件或固定次数的要求：

```text
user.message                 queued，turn_id=null
input.updated                started，关联 Turn
turn.updated                 running
agent.thinking               running
agent.thinking               completed
message.delta                临时说明
agent.message                完整说明，替换预览
agent.tool_use               read，pending
agent.tool_use               read，running
agent.tool_result            read，completed
message.delta                临时答案
agent.message                完整答案，替换预览
turn.updated                 completed
input.updated                processed
session.updated              执行概况，可仍有已确认后台进程
```

执行中新增 Input 正常排队。stop 以受理边界收尾已有输入、当前工作及范围内本 Session 后台进程，后来的 Input 不自动纳入旧边界；即使没有 active_turn_id 也不能忽略后台进程。其他 Session 共享的 Sandbox 不扩大停止范围，未知外部结果如实保留。停止与归档直接返回 Session，具体 202 / 200 和冲突规则仍见 [API](session-api.md#7-停止与归档直接观察-session)。

## 13. 大小、版本与按需验收

- 单条可靠事件候选上限 8 MiB，按 UTF-8 JSON 编码后的字节数计算，含公共字段和转义开销，具体数值在实现验收前固定。工具详情超限用 too_large 并保留安全摘要，不能返回截断正文后标 available；尚未定义文件下载入口来补齐它。
- 正式消息若需分段，在生成过程中提前控制每个 message_id 的大小，关闭当前块并为后续块分配新身份和预览；最终完整正文以顺序排列的消息保留，不能到提交时丢弃尾部。content 和临时帧不能切断 Unicode 字符。
- 凭据在持久化前清理，当前详情权限在交付时核验；事件正文不是无限原始日志，也不把工具输出中的文本当作平台指令或 HTML。
- schema_version=1 为当前设计版本；新增类型不是天然兼容，启用前同步正式契约、生产者和消费者。目录中的未实现类型不纳入当前服务的可用承诺。
- 后续实现按所启用类型验收：输入去重及单条用户消息更新、完整正文替换预览、工具状态合并和未知结果核对、Turn / 后台进程区分、分页到流的无遗漏衔接、应用游标重连去重、权限变化及超限行为。某类型尚未启用时不要求先实现它来通过其他功能的验收；已启用功能所需的可靠语义必须完整。

最小实现的 API、表和归属按[生命周期设计](../../docs/architecture/conversation-lifecycle.md)接入；未启用类型和高级读取投影不构成当前承诺。当前 MCP 真实名称/来源从 Pi 结构化结果 details 取得，Pi toolName 保留原生名称，MCP 来源缺失时保持未知；不以展示文本猜测。完整输入投影使 turn_id 筛选也能还原用户正文；结果只用 agent.tool_result。实际运行与限制见[运行说明](../../docs/operations/minimal-agent-runtime.md)。

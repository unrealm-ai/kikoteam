# Session、Input 与 Turn API 详细设计

**Web 群增量（2026-11-18，实施中）**：群消息每群一份，各员工独立触发/授权后补入各自 Input；新增人员和员工可读加入前历史，加入不要求重置 Pi；退出后禁止读取后续并复验迟到任务与投递。详见[Web 群专项](../../docs/flows/web-group-conversations.md)。本文旧私聊约束不作为 Web 群历史的加入时间限制。

**输入方式增量设计（2026-11-17，已接入，验收范围见专项设计）**：新增 `delivery_mode: steer | queue`，新提交的用户输入默认 steer，引导可加入活动 Turn；任务回报通过有可信来源的内部输入在主 chat 排队单独执行。合并、输入关联、去重和旧数据兼容见[引导输入与任务轮次回报](session-input-and-task-report.md)。以下逐条排队旧说明以专项设计为准；公开契约已增加输入方式，内部契约已增加回调和补偿入口。

**归档准入（2026-11-17）**：首次归档要求目标 Session（Conversation 则包含主 chat 和全部关联任务）无活动轮次、排队输入、后台进程、停止未确认或未知本地活动，否则返回 409 `session_busy` / `conversation_busy` 且不修改状态。先停止并等收尾再归档。锁序、旧归档兼容与死锁缓解边界见[生命周期](../../docs/architecture/conversation-lifecycle.md#停止与归档)。

> **2026-11-16 关系与生命周期已确认**：一 Task 一 task Session，共享环境归 Conversation；创建、停止、归档、首版放置及三层回收按[业务会话与共享环境生命周期](../../docs/architecture/conversation-lifecycle.md)。本页已同步目标设计，执行编排及相关机器契约仍待实现。

状态：会话接口及可靠历史/SSE 已实现；2026-11-17 新增 Web 固定私聊解析和倒序历史。Session 保持 14 个操作：消息和工具记录统一进入事件历史与实时流，停止及归档直接返回 Session，暂不提供控制资源或控制标识。Agent 停用、依赖局部失败和 PluginAuth 换账号的产品取舍保持不变；具体字段、错误码和数值限制仍属技术候选。产品依据见[会话底座需求](../../docs/product/agent-sessions.md)，事务依据见[数据模型与状态机](../../docs/architecture/session-data-model.md)。

本稿用于先评审操作、身份、请求、响应和失败行为；评审后按[契约优先](../../docs/rules/backend.md#契约优先)手写 OpenAPI 3.1 YAML，作为唯一机器可读契约源，再生成类型和实现。本文不替代 OpenAPI，不预建空契约或业务代码。入口分区、配置资源和内部协作仍由 [HTTP 总览](http-api.md)维护。

## 1. 路径、身份与共同约定

所有路径使用前缀 `/api/v1/workspaces/{workspace_id}`，下文省略此前缀。所有操作均要求用户登录态或具有相应能力的服务账号 / API Key；内部服务凭据不能直接冒充用户。workspace_id 从路径读取，实际主体从认证上下文解析，请求体不得设置工作区、创建者、提交者或执行者。

| 授权能力（语义名，具体身份实现另定） | 操作 |
| --- | --- |
| 创建会话及使用 Agent | POST /sessions；核验 Agent、实际配置引用及可用范围 |
| 读取会话 | 列表、详情、Input、Turn、事件历史与实时流 |
| 提交输入 | POST inputs；可读取不代表可提交 |
| 编辑会话 | PATCH 标题 |
| 控制会话 | 撤回、停止、归档；核验目标会话，不只比较 created_by |
| 读取工具详情 | 同一事件接口中的工具参数和结果；无此能力时只返回摘要与明确的隐藏标记 |

无有效身份返回 401。资源不存在、归属不匹配或无读取权限统一返回 404 resource_not_found，避免泄漏其他工作区或会话。资源可见但动作无权限返回 403 permission_denied。列表先按权限过滤再分页，不用计数泄漏不可见记录。上述约定覆盖每个方法及幂等重放；Cookie 写操作还须通过 CSRF 校验，冲突的多凭据组合拒绝。

| 约定 | 本稿候选 |
| --- | --- |
| ID | 资源 ID 使用 UUID 字符串；调用方将其视为不透明引用 |
| 序号与版本 | seq、事件 input_seq、turn_no、call_no、revision 及配置版本对外用无前导零的十进制字符串，避免大整数精度丢失；计数、事件 schema_version 及分页 limit 为 JSON 整数 |
| 时间 | RFC 3339，响应 UTC；未发生的时间显式 null |
| JSON | UTF-8；未知写入字段、重复字段、重复标量查询参数、非法枚举均拒绝 |
| 文本 | 正文限 65,536 个 Unicode 标量值，有正文时至少含一个非空白字符。Web 私聊有附件时可省略正文或传空串。受理时原样保存正文及有序附件引用，见[私聊项目文件](conversation-files.md) |
| 标题 | title 可空；非空时 1–200 个 Unicode 标量值，不能全空白；null 表示无标题 |
| 请求体上限 | JSON 请求体候选上限 1 MiB，超出返回 413；不能依赖服务端截断文本 |
| 分页 | cursor + limit，默认 50，范围 1–200；所有下列列表固定使用游标，无 total |
| 响应 | 单项直接返回对象；列表为 {items, next_cursor}，末页 next_cursor=null；事件历史额外返回用于衔接实时流的 resume_cursor |
| 响应头 | X-Request-ID 标识本次 HTTP 调用；JSON 为 application/json，私有响应 Cache-Control: no-store |

创建资源的首次成功返回 201；每次停止或首次归档仍需执行收尾时返回 202，直接以 Session 快照表达已受理及当前状态，Location 指向 Session 详情；已经完成的同步操作返回 200。命中 Input 提交去重记录或重复归档时返回 200 及当前资源，是否仍有执行需看资源状态。不另建控制回执资源。资源创建与后台执行结束是不同事实，Input 排队也返回 201。这些状态码含义依据 [RFC 9110](https://www.rfc-editor.org/rfc/rfc9110.html#name-status-codes)。

## 2. 操作目录

表内成功码是首次请求的结果，重试行为逐操作定义，不统一要求写接口携带 key。只有提交 Input（含创建 Session 时携带首条输入）使用专门去重；stop 每次停止本次受理时已有的工作，详见下一节。operationId 是 OpenAPI 中的固定操作名称，用于文档与代码生成，不是请求参数，也不承担业务去重身份。

| 方法与路径 | operationId | 请求 / 查询 | 成功结果 |
| --- | --- | --- | --- |
| POST /sessions | createAgentSession | conversation_id、mode、title?、input? | 201 Session；task 同事务创建 Task |
| GET /sessions | listSessions | conversation_id?、agent_id?、mode?、lifecycle?、cursor?、limit? | 200 Session 列表 |
| GET /sessions/{session_id} | getSession | 无请求体 | 200 Session |
| PATCH /sessions/{session_id} | updateSession | title、expected_revision | 200 Session |
| POST /sessions/{session_id}/inputs | createSessionInput | 正文、可选 `attachment_paths` 和 `delivery_mode`，或 `confirmation` | 201 Input |
| GET /sessions/{session_id}/inputs | listSessionInputs | status?、cursor?、limit? | 200 Input 列表 |
| GET /sessions/{session_id}/inputs/{input_id} | getSessionInput | 无请求体 | 200 Input |
| POST /sessions/{session_id}/inputs/{input_id}/withdraw | withdrawSessionInput | 空对象 | 200 Input |
| GET /sessions/{session_id}/turns | listSessionTurns | status?、cursor?、limit? | 200 Turn 列表 |
| GET /sessions/{session_id}/turns/{turn_id} | getSessionTurn | 无请求体 | 200 Turn |
| GET /sessions/{session_id}/events | listSessionEvents | turn_id?、type?、cursor?、limit? | 200 可靠事件历史 |
| GET /sessions/{session_id}/events/stream | streamSessionEvents | turn_id?、type?、after?，或 Last-Event-ID 请求头 | 200 text/event-stream |
| POST /sessions/{session_id}/stop | stopSession | 空对象 | 202 / 200 Session |
| POST /sessions/{session_id}/archive | archiveSession | 空对象 | 202 / 200 Session |

正式消息、工具调用及执行状态通过同一套事件读取。没有独立 messages、tool-calls、control-requests 接口或 control_id，也没有创建 / 重试 / PATCH Turn、修改 Input 内容、取消归档或删除 Session 的接口。

结构化答复沿用 POST inputs，提交 `{"confirmation":{"request_id":"<UUID>","confirmed":false}}` 并携带 Idempotency-Key。当前 Web 用户须具有 Session 输入权限，且与提问轮次的起始输入主体一致。首次受理 201，同一人重复提交相同答案返回 200 和原 Input；冲突或已失效的问题返回 409。答复的 `confirmation_request_id`、`confirmed` 和提交人随 Input 保存。排队上限不会阻塞答复。停止会话可以取消已受理、尚未执行的答复。

## 3. 幂等与并发

### 逐操作的重试语义

普通 CRUD 优先通过资源唯一条件、目标状态和版本检查处理，不建设所有写接口通用的响应回放机制。创建动作不天然保证重试返回同一资源，需按具体业务决定；展示名相同不等于同一创建请求。

| 操作 | Idempotency-Key | 重试行为 |
| --- | --- | --- |
| GET 查询 | 不使用 | 重新读取当前可见数据 |
| POST /sessions，不带 input | 不使用 | task 每次成功创建新 Task/Session；chat 按 Conversation 唯一主对话约束拒绝重复创建，不承诺请求级回放 |
| POST /sessions，带 input | 必填 | 按首条 Input 提交去重，复用同一个 Session 和 initial_input_id，防止在不同会话重复执行首条输入 |
| POST inputs | 必填 | 同一次提交只创建一个 Input；重试返回它的当前状态 |
| PATCH 标题 | 不使用 | expected_revision 条件更新；旧版本重试可返回冲突，重新读取判断，不回放旧成功响应 |
| POST withdraw | 不使用 | queued 转 canceled；已 canceled 返回当前 Input，不更改原原因或重复发事件；已开始或 rejected 返回冲突 |
| POST stop | 不使用 | 每次按当前已受理工作确定范围；重试会覆盖两次调用之间新增的工作，无状态变化时不重复发事件 |
| POST archive | 不使用 | 首次归档要求停稳，否则 409；已 archived 返回当前 Session，不重复递增 revision 或扩大边界 |

不使用 key 的端点不读取该请求头，不返回 Idempotency-Replayed，也不因收到这个头承诺去重。配置资源 CRUD 同样逐操作定义唯一条件及版本冲突，不自动套用会话关键操作的去重机制。

标题修改先核验生命周期和 expected_revision，再判断内容是否变化。revision 只随标题或归档变化递增，Input / Turn 进度不改变它；缺少版本返回 400 invalid_argument，版本过期返回 409 session_revision_conflict。有效版本下相同标题不递增；第一次修改成功后的旧版本重试仍可能冲突，服务端不替调用方自动换成最新版本重做。

### Input 提交的必要去重

只在 Input 提交入口（含创建 Session 时携带首条输入）使用 Idempotency-Key。候选为客户端生成的 1–128 字符不透明键，字符集为字母、数字、连字符和下划线；缺失返回 400 idempotency_key_required，格式错误返回 400 invalid_argument。SDK 可生成并在同一逻辑操作的重试中复用，不能每次网络重试换键；X-Request-ID 仅作追踪。

内部唯一范围为 `(workspace_id, actor_id, operation_kind, target, key)`，operation_kind 是稳定业务类别，与 OpenAPI operationId 独立；后者保持契约名称稳定，但不作为数据库去重键的一部分：

- initial_input：创建 Session 并提交首条输入，target 为固定 sessions 集合；请求指纹覆盖 conversation_id、mode、归一化后的 title 和 input。task 的 Task 引用随原 Session 保存，重放不重建任务。
- session_input：向已有会话提交输入，target 为该 Session；指纹覆盖不可变文本。

不同端点或业务类别之间不自动合并同名 key，重试须沿用原端点、作用域和内容。字符串内容不做空白归一化；同一作用域中同键不同内容返回 409 idempotency_conflict。两次文本相同但 key 不同，表示两条合法的新输入，不按正文合并。

| 情况 | 返回与副作用 |
| --- | --- |
| 首次成功受理 | 原子保存必要的请求指纹、资源引用及输入写入，返回 201 |
| 命中已成功的 Input 提交 | 200，返回原 Input 的当前状态；创建携带输入则返回原 Session，保留原 initial_input_id |
| 命中成功去重记录的响应头 | Idempotency-Replayed: true；Location 指向原 Input 或 Session，无独立查询资源 |
| 同键首次事务尚未决且等待超时 | 409 idempotency_in_progress，Retry-After: 1；沿原键和内容重试 |
| 校验失败或事务确认回滚 | 不提交去重记录及业务创建；错误不是成功受理结果 |
| Input 提交网络断开、5xx 或提交结果未知 | 沿原端点、原键和原内容重试，不能换键假定上次未受理 |

去重命中先核验当前身份、原操作权限和资源读取权限；未获准不得返回资源。成功记录的查找先于新的启停、归档或队列容量判断：这些变化可以阻止新提交，但不能把已受理 Input 重建成另一条。失败 / 取消后明确继续工作使用新 key、新 Input，原 key 始终指向原输入。

仅保留两个 Input 提交入口所需的轻量去重记录，不缓存完整 HTTP 响应，不记录停止或普通 CRUD 请求。首版随相关会话历史保留映射，防止旧 key 失效后被当作新执行；后续清理须先定义旧键的拒绝规则。

### 并发控制的职责划分

去重识别同一次请求；不同请求之间的竞争仍靠业务版本、条件更新及会话事务解决。并发裁决的依据是数据库中的受理 / 领取提交顺序，不是客户端时间戳或网络到达先后。

| 竞争 | 对外行为与控制依据 |
| --- | --- |
| 同 key 提交 Input | 唯一登记只受理一次；其他请求读取原 Input |
| 不同 key 同时提交 | 两条 Input 都可受理，在会话事务中分配不同 seq，按 seq 执行 |
| 两个请求改同一标题 / 配置 | 预期版本只能允许一个不同修改成功，另一方冲突；不自动覆盖 |
| 撤回与领取 | 撤回先提交则不创建 Turn；领取先提交则撤回 409，不暗中停止整轮 |
| 停止与新增输入 | 输入先受理则纳入本次停止范围；停止先受理则新输入在本次边界之外，再次 stop 时重新确定范围 |
| 归档与新增输入 | 输入先受理且仍排队/运行则归档 409；归档先提交则输入 409，不能产生孤立 Input |
| Agent 停用与领取 | 领取先提交则当前轮可收尾；停用先提交则未开始输入拒绝，快速启用也不复活旧队列 |
| 多个 Engine 领取或接管 | 只有当前执行归属和代次可以提交，最多一个活跃 Turn；旧进程未隔离不能启动新的并行执行 |

不同 Session 的模型与工具执行可并行，数据库事务只短暂保护状态，不覆盖整个模型调用。具体锁模式、顺序、失败重试及不变量见[并发裁决设计](../../docs/architecture/session-data-model.md#7-事务与并发裁决)。

## 4. Session：创建、读取与修改

### 创建请求

| 字段 | 类型 | 必填 / 默认 | 约束 |
| --- | --- | --- | --- |
| conversation_id | UUID 字符串 | 必填 | 已存在、active 且当前主体可使用的业务会话；员工从该归属解析并核验启用状态 |
| mode | string | 必填，无隐式默认 | chat / task |
| title | string / null | 可省略，默认 null | 按共同长度约束 |
| input | object | 可省略；提供时不能为 null | 仅含必填 text，与创建会话原子受理 |

```json
{
  "conversation_id": "77777777-7777-4777-8777-777777777777",
  "mode": "task",
  "title": "分析项目",
  "input": {"text": "帮我分析这个项目的数据库设计。"}
}
```

这是按已确认关系更新的候选创建契约，尚未发布：Backend 从 Conversation 解析员工，不接受独立 agent_id、task_id 或 Sandbox 选择。chat 在该 Conversation 首次创建唯一主 Session；已存在时返回 409 chat_session_exists，后续输入使用原 Session。task 由同一用例生成 Task 和 Session，调用方不必预先创建 Task；title 和首条文本提供最小目标材料，完整任务业务参数另行细化。Conversation 的管理入口不计入本表 14 个执行操作，也不在本稿预建新端点。

主 chat 的[task_delegation MCP](task-delegation-mcp.md)复用同一 Manager 用例，从专用运行身份解析来源 Conversation / chat，固定 mode=task 并携带首条 Input，原子保存委派来源与 Task / Session；不向本 REST 请求新增 parent_session_id、回报地址或来源主体字段。其追加、查询和停止同样复用既有用例，request_key 映射到输入提交的必要去重，不使 stop 要求 key。当前八个主对话工具已实现，含待办记录/启动/取消；运行追加需输入版本CAS，详见[待办与回执](chat-planned-tasks.md)，不增加REST写入口。

chat 采用平台固定运行策略与 AI 员工所选模型（自备或官方），不回退工作区或官方默认；task 继续解析员工任务配置，Skill / Plugin 采用能力包固定版本。请求不能覆盖模型、指令、技能、插件、授权或环境，也不接受 parent_session_id、群/用户交流范围或文件字段。配置无效时返回 422 configuration_unusable，整个创建事务回滚。已创建的 chat 和 task 均保留创建时的模型快照，不因员工换模型而自动切换，也不改写历史。

创建不探测所有第三方网络服务。合法插件引用尚待登录或暂不可达按局部失败处理，跨工作区或无引用权限仍拒绝。不带 input 不要求 key：空 task 可重复创建不同任务，chat 受唯一约束。带 input 则将 Task（task 模式）、Session、快照、Input、事件和必要去重映射原子提交。成功去重命中先于当前父归档及主对话唯一性判断，按当前读取/操作权限返回原资源，不重建任务、不重新解析配置。

### Session 响应

| 字段 | 类型 / 可空 | 含义 |
| --- | --- | --- |
| id、workspace_id、agent_id | UUID ! | 稳定归属 |
| conversation_id、task_id | UUID !、UUID? | 所属业务会话；task 必须有关联任务，chat 为 null，调用方不可修改 |
| created_by | string ! | 经核验主体的稳定引用，不由请求提供 |
| mode、title、lifecycle | string !、string?、string ! | chat / task；标题；active / archived |
| revision | 十进制字符串 ! | 用户元数据修订号，初始 "1" |
| initial_input_id | UUID? | 仅创建时携带首条 input 才设置；空会话后来追加输入不回填 |
| configuration | object ! | 实际版本摘要，见下文 |
| agent_enabled | boolean ! | 当前 Agent 状态，独立于历史快照 |
| accepts_input、input_block_reason | boolean !、string? | 当前主体的准入；原因 conversation_archived / session_archived / agent_disabled / permission_denied，允许时 null；父归档即拒绝，不等待子归档完成 |
| execution | object ! | 执行概况，见下文 |
| event_cursor | string ! | 此次读取点的可靠事件高水位，供后续订阅；无事件也返回合法起点游标 |
| archived_at | 时间? | 归档时间；不提供归档请求标识 |
| created_at、updated_at、last_activity_at | 时间 ! | 创建、元数据更新及最近业务活动时间 |

configuration 含 agent_version、chat_preset_version（task 为 null）、model_profile_id、model_version 和能力包来源摘要；包内 skills 元素固定 skill_id + version，包内 plugins 固定 plugin_id + version + allowed_operations，员工插件账号映射保存 plugin_id + plugin_auth_id（无需认证可空）。具体字段待资源契约评审，不再提供 Agent 直接绑定 skills/plugins 的配置模型。运行时展开清单只能来自当前获准包，同员工多包同插件共用该员工账号，各包工具范围可不同；摘要须保留包来源。chat 不加载 Agent 任务技能或插件，按实际预设输出摘要。普通会话读取不返回完整指令、凭据引用、接入密钥、Engine 地址或原生历史路径。

execution 含 state、active_turn_id?、queued_input_count、background_process_count 和 block_reason?。background_process_count 是当前执行 Session 已确认存活的业务后台进程数（非负整数）；无 Sandbox 或从未启动命令时为 0，证据失联或未知时为 null，不能记作 0。不计 Pi、Runner、常驻 MCP 服务或同 Sandbox 内其他 Session 的进程；采样须来自当前绑定的完整账本，历史计数不能在失联后冒充已核验状态。

state 的判定优先级为 blocked → stopping → running → queued → idle：运行门禁因旧执行未隔离、后台活动不可核验或原生历史不可用而 blocked，或 assignment 为 unresolved 时，概况为 blocked；已受理停止且边界内 Turn、后台进程或在途调用尚未收尾则 stopping，即使 active_turn_id=null；否则按活动 Turn 和有效排队输入计算。block_reason 为脱敏机器码。idle 只表示没有活动轮次、有效排队输入或未决调用，可以有 background_process_count>0；不能仅凭 idle 回收运行实例或 Sandbox。尚未分配 Engine 的排队会话仍为 queued。

accepts_input 不承诺容量或立即执行，容量超限仍可拒绝请求。运行 blocked 时只要准入条件满足仍可提交 Input，执行需等待环境可继续；Agent 停用与归档则拒绝新输入。Agent 停用不妨碍有权主体读取、停止、归档已有会话。

首次创建后 Location 指向 Session 详情。示例为刚受理、尚未领取的快照：

```json
{
  "id": "11111111-1111-4111-8111-111111111111",
  "workspace_id": "33333333-3333-4333-8333-333333333333",
  "agent_id": "22222222-2222-4222-8222-222222222222",
  "conversation_id": "77777777-7777-4777-8777-777777777777",
  "task_id": "88888888-8888-4888-8888-888888888888",
  "created_by": "actor_demo",
  "mode": "task",
  "title": "分析项目",
  "lifecycle": "active",
  "revision": "1",
  "initial_input_id": "44444444-4444-4444-8444-444444444444",
  "configuration": {
    "agent_version": "3",
    "chat_preset_version": null,
    "model_profile_id": "66666666-6666-4666-8666-666666666666",
    "model_version": "2",
    "skills": [],
    "plugins": []
  },
  "agent_enabled": true,
  "accepts_input": true,
  "input_block_reason": null,
  "execution": {"state": "queued", "active_turn_id": null, "queued_input_count": 1, "background_process_count": 0, "block_reason": null},
  "event_cursor": "opaque_event_cursor_2",
  "archived_at": null,
  "created_at": "2026-11-15T08:00:00Z",
  "updated_at": "2026-11-15T08:00:00Z",
  "last_activity_at": "2026-11-15T08:00:00Z"
}
```

### 标题修改与会话列表

PATCH 只接受如下结构，title 必须出现，null 可清空；空对象、其他字段和归档后修改均拒绝。

```json
{"title": "数据库设计讨论", "expected_revision": "1"}
```

成功返回完整 Session，revision 递增；冲突响应携带当前修订号，调用方先重新读取再决定如何修改。无权操作、停用、归档、并发冲突的处理不能通过客户端传入 lifecycle 或 revision 强制覆盖。

会话列表允许 conversation_id、agent_id、mode、lifecycle 精确筛选，不提供任意 q 或动态排序；默认包含 active 与 archived，固定 `(created_at DESC, id DESC)`。详情和列表采用同样的权限及父级准入投影。游标绑定筛选；分页为实时视图，不保证跨页冻结状态，原页后的变化通过详情或事件读取。

## 5. Input：提交、查询与撤回

POST inputs 只接收 `{"text":"继续分析索引设计。"}`。成功返回 201 Input，Location 指向该 Input；即便 Engine 已可执行，也不等待答案。流式输出通过独立 GET events 获取。新输入固定排队顺序，不合并到当前 Turn，也不能请求中途插话或指定 Skill 字段。

### Input 响应

| 字段 | 类型 / 可空 | 含义 |
| --- | --- | --- |
| id、workspace_id、session_id | UUID ! | 标识与归属 |
| submitted_by | string ! | 本次输入真实提交主体 |
| seq、text | 十进制字符串 !、string ! | 会话内受理顺序、不可变正文 |
| status | string ! | queued / started / processed / canceled / rejected |
| turn_id | UUID? | 开始时固定；未开始取消或拒绝时为 null |
| cancel_reason | string? | 仅 canceled：withdrawn / session_stopped / session_archived |
| rejection | object? | 仅 rejected：{code, message}，包括 agent_disabled / access_revoked |
| received_at、started_at、finished_at | 时间 !、时间?、时间? | 受理、开始、终结；queued 时后两项 null |

```json
{
  "id": "44444444-4444-4444-8444-444444444444",
  "workspace_id": "33333333-3333-4333-8333-333333333333",
  "session_id": "11111111-1111-4111-8111-111111111111",
  "submitted_by": "actor_demo",
  "seq": "1",
  "text": "帮我分析这个项目的数据库设计。",
  "status": "queued",
  "turn_id": null,
  "cancel_reason": null,
  "rejection": null,
  "received_at": "2026-11-15T08:00:00Z",
  "started_at": null,
  "finished_at": null
}
```

Input 为 processed 时 turn_id 必填，但仍需读取 Turn 才能知道成功、失败或取消。受理后 Agent 停用导致拒绝，查询仍返回 HTTP 200 + Input rejected；没有 Input 被创建的提交拒绝才使用 HTTP 错误。Agent 再启用不会重新入队旧代次输入。

撤回请求体为 `{}`。queued 时同步变成 canceled，返回 200；已经 canceled 时可返回原取消事实，不改写原 cancel_reason。started / processed / rejected 返回 409 input_not_withdrawable，并带当前 status 和已存在的 turn_id；它不会隐式停止整个会话。归档会话只允许读取或重放既有取消事实，不能借撤回修改已经终结的输入。

Input 列表固定 seq ASC，status 可选单值精确筛选，不支持改正文或批量合并。列表和单条详情必须使用一致的停用代次投影，拒绝输入不计入 queued_input_count；finished_at 取拒绝事实生效时间，后台落库不能改变此前已展示的结果。

## 6. Turn：执行结果与工具详情

Turn 只能由 Engine 在领取 Input 时创建。列表和详情均返回下列对象，不内嵌无限增长的消息或工具历史；这些记录通过 GET events 按 turn_id 分页查看。

| 字段 | 类型 / 可空 | 含义 |
| --- | --- | --- |
| id、workspace_id、session_id、input_id | UUID ! | 固定来源与归属 |
| turn_no | 十进制字符串 ! | 实际开始顺序，不等同于 Input seq |
| status | string ! | running / stopping / completed / failed / canceled / interrupted |
| result_summary | string? | 已提交的结果摘要，可在终态后为 null，正式答案读取 agent.message 事件 |
| error | object? | {code, message}；failed / interrupted 必填，其他状态为 null |
| has_unknown_tool_results | boolean ! | 存在结果未知的工具调用，不能因整轮已结束视为无副作用 |
| usage | object? | 可提供时为 input_tokens?、output_tokens?、total_tokens? 的十进制字符串；未知值 null，不补零 |
| started_at、finished_at | 时间 !、时间? | 活动轮次 finished_at=null，终态必填 |

实际配置通过 session_id 读取会话的固定 configuration，不在每轮允许覆盖。内部执行代次、Pi 原生 Session ID 和原生目录位置不属于普通返回字段。Turn 可能输出多条助手消息，不能用某条消息结束来推断 Turn 完成。

例如模型暂时故障重试耗尽后的查询结果仍是 HTTP 200：

```json
{
  "id": "55555555-5555-4555-8555-555555555555",
  "workspace_id": "33333333-3333-4333-8333-333333333333",
  "session_id": "11111111-1111-4111-8111-111111111111",
  "input_id": "44444444-4444-4444-8444-444444444444",
  "turn_no": "1",
  "status": "failed",
  "result_summary": null,
  "error": {"code": "model_unavailable", "message": "模型服务暂不可用，有限重试已结束。"},
  "has_unknown_tool_results": false,
  "usage": null,
  "started_at": "2026-11-15T08:00:01Z",
  "finished_at": "2026-11-15T08:00:20Z"
}
```

Turn 列表固定 turn_no ASC，可按单值 status 筛选。失败、取消或进程故障后继续使用 POST inputs，不提供重试旧 Turn。进程故障令旧 Turn interrupted、对应 Input processed；后续新 Input 可触发 Engine 启动 Pi 并 `--session` 加载原生历史，再执行新 Turn，平台不自动重发旧 prompt。尚未开始的 queued Input 保留普通串行语义。终态不退回 running，加载失败不创建空白原生会话。

bash_start 等工具请求完成与后台命令退出是不同事实。正常 Turn 可在后台命令仍存活时 completed，后续 Turn 可以管理同一执行 Session 的原进程；后台命令后续退出不将已完成 Turn 改成新的终态，其活动变化反映在 Session 执行概况。

工具执行通过 agent.tool_use / agent.tool_result 事件提供稳定 tool_use_id、状态、摘要和受控详情，平铺字段见[会话事件协议](session-events.md#6-工具调用进度与结果)。Engine 根据 Pi 原生事件建立投影，不假定所有调用经过 Runner；上游未提供的参数或结果使用 unavailable + null，当前无详情权限使用 redacted + null，不伪造内容。builtin 的 TS 工具与本地执行记录见[专项设计](../../sandbox-runner/docs/builtin-mcp.md)，只在有可靠关联时补充事件，不新增工具查询或控制接口。单个插件授权过期、撤销、缺依赖或不可达，对应能力记录明确错误并交给 Harness；Turn 只有在 Harness 最终无法继续或遇到必需执行故障时才失败。

## 7. 停止与归档：直接观察 Session

两个 POST 操作都要求 `{}`，均不要求 Idempotency-Key。返回的 Session 即表示请求已被受理，不需要调用方保存另一种控制标识。每次 stop 仍需收尾时返回 202，已经无目标工作时返回 200；首次 archive 同样区分是否需收尾，重复归档返回 200，不代表执行已收尾。Location 均指向 Session 详情。客户端查询 GET Session 或消费 session.updated，查看当前 execution.state。

| execution.state | 当前含义 |
| --- | --- |
| stopping | 已收到停止意图，旧执行仍在收尾；不能显示为已停止 |
| idle | 无活动 Turn、有效排队输入或未决调用；已确认后台进程单列计数，不代表可以回收环境 |
| blocked | 尚不能确认旧执行已结束或环境可继续，block_reason 解释原因 |
| queued | 存在等待执行的新输入；它不属于之前已经受理的停止范围 |
| running | 当前轮正在执行；若是停止后提交的新输入，这是后续工作，不代表旧停止失效 |

例如第一轮运行、第二和第三条输入排队时停止，backend 取消当时仍未开始的输入，并要求第一轮收尾。此时返回 Session 中的 execution 可为：

```json
{
  "state": "stopping",
  "active_turn_id": "55555555-5555-4555-8555-555555555555",
  "queued_input_count": 0,
  "background_process_count": 0,
  "block_reason": null
}
```

该 JSON 仅示意 execution 字段，完整响应沿用第 4 节 Session。没有新输入时，安全收尾后变为 idle。停止后新提交的第四条输入可受理，等待旧轮次安全收尾后执行；其后 Session 可能直接从 stopping 变为 queued 或 running。需要区分具体轮次时使用现有 active_turn_id 和 Turn 详情，不逐次追踪停止请求的独立状态。

停止意图仍只保存 Session 的 stop_through_input_seq：每次受理 stop 都在会话事务内取当前 last_input_seq，取消边界内的有效 queued 输入，并将停止边界单调推进。当前 Turn 的来源 Input 在边界内时响应停止；本次边界之后的输入不受这一次停止影响，再次 stop 则按新的 last_input_seq 重新确定范围。停止不保存请求去重记录；即使客户端携带相同 key 也不读取该请求头，不返回 Idempotency-Replayed。

停止同样覆盖本 Session 已完成 Turn 留下的后台进程，按 origin_turn_id 对应的 Input.seq 纳入边界。没有活动 Turn 时仍需唤醒 Engine，关闭目标 Session 工具入口、终止其受管进程并排空调用；共享 Sandbox 中其他 Session 不受影响。Engine 在 runtime 保存已应用停止边界和活动证据，不新增控制资源。收尾中返回 202；在没有后续新输入的场景，确认目标后台进程为 0、无未决调用后才可返回 200/idle。未知时 blocked，不能把 idle 加非零后台计数作为 stop 已完成的回执。

例如先 stop，再提交第四条 Input，再次 stop 会取消尚未开始的第四条输入，或要求该输入已开始的 Turn 收尾。网络重试和显式再次调用使用相同语义。没有新增输入且状态没有变化时，返回当前 Session，不重复更新元数据、发状态事件或重新建立停止流程；已经在收尾则仍返回 202，当前无工作且旧执行已结束或隔离时返回 200。原执行未知时保持运行门禁 blocked，不能仅因为活动指针清空就宣告 idle。

archive 不要求 key，首次调用在同一事务先核验停稳（否则 409），再将 Session 标记 archived、递增 revision、设置 archived_at 并推进停止边界。收到 202 时新输入已禁止；归档是否完成执行收尾仍看 execution.state。已归档会话再次 archive 返回 200 及当前 Session，不再更新边界、时间或修订号，也不重复追加归档事件。

本接口只归档目标 Session；task 的任务归档事实同步保持一致，chat 不级联任务。单个 Session 的 stop/archive 均不回收共享 Sandbox。父 Conversation 归档由业务用例先核验全部子 Session 停稳，再关闭全会话准入，再逐项复用本归档逻辑；所有参与者停稳后才释放环境运行资源，历史和工作文件保留，见[生命周期](../../docs/architecture/conversation-lifecycle.md#停止与归档)。本表不增加 Conversation stop 或控制资源。

stop 遇到已归档会话一律返回 409 session_archived，包括先前停止成功后在归档期间到达的重试，调用方直接查看 Session 状态。Agent 停用不阻止停止与归档。backend 只记录停止意图，Engine 提交实际 Turn 结果和运行状态；本期不创建控制表、控制状态机或控制专属事件。

## 8. 统一事件、历史查询与实时流

事件字段、类型、完整快照、增量、历史分页及 SSE 消费的统一定义见[《会话事件协议 v1》](session-events.md)。历史 items 与 SSE 的 data 行承载相同平铺 Event；JSON 不使用事件级 data / payload 包装。SSE 的 data: 是传输格式，不是 JSON 属性。

**事件 type 是按完整能力预先设计的目录，不是全部必须实现的交付清单。开发按实际功能采用对应定义，在正式契约中声明并验收已启用的子集；列入文档不表示当前已支持。** 本文保留 14 个操作及资源受理语义，不重复维护另一份事件 schema。

| 操作 | 参数与响应 | 定义位置 |
| --- | --- | --- |
| GET /sessions/{session_id}/events | turn_id?、type?、cursor?、limit?；200 {items, next_cursor, resume_cursor} | [历史分页与两类游标](session-events.md#10-http-入口与历史分页) |
| GET /sessions/{session_id}/events/stream | turn_id?、type?、after? 或非空 Last-Event-ID；200 text/event-stream | [实时交付与续读](session-events.md#11-sse断线续读与消费) |

共同约定：

- 五类核心类型为 user.message、agent.message、agent.thinking、agent.tool_use、agent.tool_result。用户受理与 Input 状态更新共用 message_id=input_id，不重复显示用户消息；工具更新共用 tool_use_id，工具失败不直接等于 Turn 失败。
- 事件 seq 表示可靠顺序；Input 的排队序号在事件里使用 input_seq，HTTP Input.seq 不变。工具字段采用 name、input / input_state、output / output_state、source 和完整状态快照，按当前权限投影，不再使用详情可见性布尔值代替明确的 redacted 状态。
- GET events 按 seq ASC 固定高水位 H 分页，末页 resume_cursor 供同筛选 SSE 从 H 后追赶；不拿最新 Session.event_cursor 替换它。历史分页和实时游标不能互换，筛选零匹配也返回合法末页续读位置。
- SSE 的可靠事件 id 为不透明实时游标，非空 Last-Event-ID 优先于 after；两者皆无则从起点追赶。临时增量、心跳和流错误不推进游标。重复交付按 (session_id, seq) 去重，成功应用后才推进处理位置。
- 原生历史加载不重复生成公开输出；故障后通过新 Input / Turn 继续。已确认后台进程可以跨正常 Turn 保留，其数量由 session.updated.execution.background_process_count 表达，不能把工具调用完成等同于后台命令退出。
- 可靠历史随会话保留；游标过期返回 410 event_cursor_expired，重新遍历后接流。开流前使用第 9 节 HTTP 错误体，开流后使用平铺 stream.error 并关闭当前连接。查询、交付和重连重新鉴权，断流不取消 Turn。

正式字段、11 类预设计类型的专属字段、大小候选及按需验收均以会话事件协议为准；未启用类型不能作为可用能力接受筛选或发送占位事实。

## 9. HTTP 错误与执行错误

HTTP 错误说明本次请求未取得正常成功响应，不能一概推断业务尚未提交；尤其提交结果未知时，应按对应操作的重试语义处理。已经受理的 Input、失败的 Turn、不可用工具、未决停止均通过对应资源表达，正常查询仍是 200。统一错误体：

```json
{
  "error": {
    "code": "input_not_withdrawable",
    "message": "输入已经开始执行，无法撤回。",
    "request_id": "req_demo",
    "details": {"status": "started", "turn_id": "55555555-5555-4555-8555-555555555555"}
  }
}
```

request_id 与当前响应 X-Request-ID 相同；details 仅返回有权查看的字段，可省略。字段校验使用 details.fields 数组，每项为 field、reason；不回显提交的秘密或整段正文。调用方按 code 分支，不解析中文 message。

### HTTP 错误码

| HTTP | code | 触发及处理 |
| --- | --- | --- |
| 400 | invalid_argument | 非法 JSON / ID、重复或未知字段、非法枚举 / 长度 / 查询条件、缺 expected_revision；修正后作为新请求提交 |
| 400 | idempotency_key_required | Input 提交（含建会话携带首条输入）缺 Idempotency-Key；stop 等其他写操作不使用此错误 |
| 400 | invalid_cursor | 游标格式、作用域、筛选或未来位置无效；重新读取当前作用域 |
| 400 | conflicting_credentials | 提交了互相冲突的认证方式；先选择一种合法身份 |
| 401 | unauthenticated | 没有有效凭据或凭据失效 |
| 403 | permission_denied | 可见资源上的动作无权限；不得自动更换主体重试 |
| 403 | csrf_failed | Cookie 写操作未通过 CSRF 校验 |
| 404 | resource_not_found | 不存在、归属不匹配或无读取权限，响应不区分原因 |
| 409 | agent_disabled | 创建会话 / 新输入时 Agent 停用；重新启用后用新逻辑请求提交 |
| 409 | conversation_busy / session_busy | 首次归档目标或关联任务仍有活动、排队、停止未确认或未知状态，先停稳再重试 |
| 409 | session_archived | 新输入、标题修改或任一次 stop 遇到已归档会话 |
| 409 | conversation_archived | 父业务会话已归档，拒绝创建 Session 或新 Input；已有收尾及历史读取继续允许 |
| 409 | chat_session_exists | 同一业务会话已有主 chat Session；读取原 Session，不能自动替换或将本次首条输入暗中追加 |
| 409 | session_revision_conflict | PATCH 修订号过期；details.current_revision 指出当前版本 |
| 409 | input_not_withdrawable | 已 started / processed / rejected，不能撤回 |
| 409 | idempotency_conflict | Input 提交同范围键绑定不同请求内容；核对原逻辑操作 |
| 409 | idempotency_in_progress | Input 提交同键首次事务尚未决；沿原键和内容按 Retry-After 重试 |
| 409 | resource_busy | 关键状态事务等待竞争资源超时，已确认回滚；按操作重试规则处理，Retry-After 提供建议 |
| 410 | event_cursor_expired | 分页或实时游标已失效，可靠历史仍保留；重新遍历后接流 |
| 413 | request_too_large | 请求体超出字节上限 |
| 415 | unsupported_media_type | 非 application/json 的写入 |
| 422 | configuration_unusable | 必需配置引用不完整或不可解析；不用于单个插件暂时故障 |
| 429 | rate_limited | 主体或工作区请求限流；Retry-After 给出等待秒数 |
| 429 | session_queue_full | 会话待执行输入达到部署容量；Retry-After 提供建议，不新建 Input |
| 500 | internal_error | 未分类内部错误，脱敏；不假定未提交，按对应操作的去重或状态 / 版本规则处理 |
| 503 | service_unavailable | 数据库或 backend 必需依赖暂不可用；Input 提交沿原键核对，stop 重发按当前工作重新停止，普通操作按资源状态处理，空会话创建重试不保证同一 ID |

每个操作都可能出现适用的共同校验、身份、资源、限流或服务错误；状态错误的适用范围在表中限定。读取 Turn failed 不返回 500，读取 Session.execution.state=blocked 不返回 503，Engine 尚未分配也不让已受理 Input 的创建返回 503。

### 持久执行原因码

这些 code 出现在 Input.rejection、Turn.error、工具事件的 error 或 Session.execution.block_reason 中，不直接作为读取请求的 HTTP 状态。

| 所在对象 | code | 含义及下一步 |
| --- | --- | --- |
| Input | agent_disabled | 停用时未开始，或受理代次已过期；重新启用后只能提交新 Input |
| Input / Turn | access_revoked | 核心使用权限失效；未开始拒绝，执行中按实际收尾记录，不能绕过撤权 |
| Turn | model_unavailable | 模型暂时故障，安全范围内有限重试已耗尽 |
| Turn | model_auth_required | 模型凭据无效，需要修复授权；不无限重试 |
| Turn | model_request_failed | 不可重试的模型调用错误，保留脱敏原因 |
| Turn | runtime_unavailable | 必需执行环境准备失败，安全重试耗尽 |
| Turn | execution_failed | 其他已确认执行失败 |
| Turn | execution_interrupted | 进程故障等异常中断；旧 Turn 不自动续跑，未知工具结果另行标注 |
| Turn / Session.execution.block_reason | native_session_unavailable | 原生历史、Engine 身份映射或目录不可用；修复后用后续 Input 继续，不能空白替代 |
| 工具 | plugin_auth_required | 需首次认证或重新授权；只禁止对应工具 |
| 工具 | plugin_auth_revoked | 原授权已撤销，不能使用快照或其他账号替代 |
| 工具 | tool_permission_denied | 当前主体、Agent 操作范围或外部账号不允许该工具 |
| 工具 | plugin_unavailable / plugin_dependency_missing | 服务不可达或缺运行依赖，交由 Harness 决定是否继续 |
| 工具 | tool_failed / tool_result_unknown | 已知失败 / 无法确认原操作结果；未知先核对原调用，不能盲目重试 |
| Session.execution.block_reason | previous_execution_unresolved | 尚无法确认旧执行已结束；继续核对原执行记录，不能清空门禁冒充 idle |
| Session.execution.block_reason | background_activity_unknown | 无法核验本 Session 后台活动，计数为 null，不能按零活动推进回收或停止完成 |

单个工具失败不自动成为 Turn.error；若 Harness 使用其他能力完成，Turn 仍可 completed，工具错误保留。模型重试在原 Turn 内进行，外部有副作用的未知工具调用不适用普通网络自动重试。

## 10. 并发场景与待实现验收

| 场景 | backend 应体现的结果 |
| --- | --- |
| 创建携带首条输入，响应丢失 | 同键重放返回原 Session 和 initial_input_id；不存在孤立 Input 或第二个会话 |
| 空会话创建响应丢失后再次创建 | 允许得到另一个独立空会话，不宣称所有 POST 都自动去重 |
| Agent 停用与输入领取竞争 | 停用边界前已开始可收尾；之后不开始，快速重新启用不恢复旧队列 |
| 正在执行时追加两条文本 | 分别受理成两个 Input，最多一个活跃 Turn，工具调用次数不改变轮次计数 |
| 撤回与开始竞争 | 200 canceled 且无 Turn，或 409 input_not_withdrawable；不能同时成功 |
| 两个调用方从同一旧标题提交不同新标题 | 相同 expected_revision 仅一个修改成功，另一个 409；不受模型输出进度干扰 |
| 停止与追加竞争 | 本次 cutoff 之前已接纳工作收尾，之后的新输入不受这一次停止影响；再次 stop 重新取边界 |
| 再次 stop 时已有新输入，含携带相同 key 的重试 | 不读取 key，按新的边界取消排队输入或停止当前轮，返回当前 Session |
| 无新增工作时重复 stop | 收尾中返回 202，当前无工作且安全收尾后返回 200；状态未变不重复发事件或改元数据 |
| 停止成功后会话归档，再重试 stop | 返回 409 session_archived，不查询或回放旧停止结果 |
| 不带 key 重复归档或撤回 | 按目标资源状态返回，取消原因、归档时间及 revision 不重复改写，不重复发状态事件 |
| 标题修改响应丢失后用旧版本重试 | 返回版本冲突并重新读取，不绕过版本校验、不回放旧响应 |
| 当前轮仍在退出时首次归档 | 返回 409 session_busy / conversation_busy，不改生命周期；先停稳再归档 |
| 父 Conversation 已归档，子 Session 尚未完成级联 | 新建和新 Input 返回 conversation_archived，queued 不再领取；已开始执行可提交收尾，逐项归档不重复发事件 |
| 两请求首次创建同一 Conversation 的主对话 | 同 key 按首条 Input 去重；不同请求至多创建一个主 Session，另一请求返回 chat_session_exists |
| 归档 task Session 或停止主 chat | 只影响目标 Session；其他 Task 和共享环境保留 |
| 插件失效，但模型和其他能力可用 | 提交仍可 201，工具错误单独可查，Harness 决定是否继续 |
| 新 PluginAuth 更换账号 | Agent 新版本供新会话使用；旧 Session.configuration 保持旧引用 |
| 相同幂等键重放时已撤权 | 返回身份 / 权限错误，不泄漏原成功结果 |
| SSE 重连时 URL 和 Last-Event-ID 不同 | 使用最新有效请求头位置，按 seq 去重；不因旧 URL 回到起点 |
| 查询某轮 failed 或 Session blocked | HTTP 200，资源准确表达失败或未决；客户端不自动重复输入 |
| 历史分多页时不断产生新事件 | 固定 H 内翻页，末页 resume_cursor 接流，H 之后的事件不丢失 |
| 筛选后没有任何匹配历史 | 返回空 items 和 H 的同筛选 resume_cursor；不能拿它跳过未读的其他类型 |
| 工具原结果未知，后来查清 | 同一 tool_use_id 新增 agent.tool_result，更新对应记录；不创建新调用、不复活 Turn |
| 无工具详情权限的历史与 SSE | 两种通道都返回 input_state / output_state=redacted、input / output=null，不靠客户端隐藏 |

本轮仅完成文档审查、链接与示例静态检查，以上行为尚未运行验证。后续将本稿及[会话事件协议](session-events.md)中实际启用的类型转为 OpenAPI 并验证引用、示例、各操作授权和错误响应，再以真实 HTTP / PostgreSQL 并发测试验收；事件目录不要求一次全部实现。

尚需技术细化：实际身份与 CSRF 方案、平台预设管理、Harness 取消及原生 `--session` 加载 接入、事件大小候选的实现验收、模型重试次数和超时、队列容量与限流数值、历史存储容量及游标签名轮换。首版保留可靠历史；本文给出的标题 / 文本上限、分页和心跳为可评审默认值，不是 Agent 可调生成参数。task_delegation 的具体 MCP 契约与回报接入按[专项设计](task-delegation-mcp.md)推进，不另建委派 / Run 资源或引入已暂缓的文件 API。

## Web 固定私聊与最近历史（2026-11-17）

`POST /conversations/web-direct {agent_id}` 原子查找或创建工作区、已认证用户、AI 员工三元组唯一的私聊，成功统一 200，安全重试无需请求 key。用户不能替其他人传入 owner。首次不创建 chat，第一次发送沿现有 Session/input 幂等语义创建唯一 chat。返回 `web_user_id`；私聊的全部子任务、事件、输入、控制和 SSE 仅该活跃成员可访问。其他成员即使是工作区管理员也不能仅凭成员身份读取私聊；不存在和不可见统一 404。非私聊旧会话保持原有工作区可见规则。

后端 Conversation/chat/task 手动归档均保留原有停稳规则，Web 默认不提供归档入口。固定私聊或主 chat 经 API 手动归档后，Resolve 始终返回原 Conversation（含原 chat_session_id 和 lifecycle），保持只读历史，不另建、替换或自动恢复；前端按 lifecycle/accepts_input 禁止发送，无 chat 的已归档 Conversation 也不能创建首条会话。迁移不会把已有 generic/已归档会话自动认作私聊，不丢弃或更换历史；新增 nullable owner 字段和部分唯一索引由 Event Hub 版本化迁移执行。未来渠道/群聊使用独立映射，不能复用此私聊唯一键。

协作会话侧栏使用 Conversation 列表的第一页（至多 50 条服务端记录），按 `(COALESCE(last_message_at, created_at) DESC, id DESC)` 读取。主 chat 正常结束的助手消息持久化时，同事务推进 `last_message_at`；用户输入、工具事件、执行任务正文与未完成的助手消息不改变它。迁移从已有主 chat 消息回填。没有已完成回复时回退创建时间；变更前签发的列表游标需要从第一页重新开始。打开服务端列表之外的员工仍按固定私聊查找，显式打开记录在当前登录账号与工作区的本地历史，不使用跨账号的旧缓存。


历史 `GET /sessions/{id}/events?direction=backward` 首次冻结高水位 H，取最近 limit 个可靠事件，但页内仍按 seq 升序。`next_cursor` 向更旧事件分页；`resume_cursor` 每页均指向同一个 H，可在第一页返回后立即订阅 SSE。后续旧页必须继续带 direction=backward；历史 cursor 与实时 cursor 分离，旧页不能覆盖实时进度。翻页期间新增事件由 SSE 从 H 之后补齐，前端按 session_id/seq 去重。默认 forward 及末页续流语义兼容。

### 隐私变更的部署与回退边界

本次 Web 私聊权限是应用层执行，旧 Backend 不认识 `web_user_id`，**不允许新旧 Backend 混跑或直接回退到旧 Backend**，否则旧读接口可能把新私聊按原工作区共享规则返回。上线需先停止/摘除全部旧 Backend，再由 Event Hub 升级迁移，然后启动新 Backend；本地 Compose 更新也遵守该顺序。不得在新私聊已经写入后直接执行 down 迁移或删除 owner 字段。回退只允许切换到同样执行私聊隔离的兼容修复版本，或在停服维护期间由经过审查的数据/权限迁移完成，不能以丢弃 owner 元数据恢复可用性。

内部 Dispatch 只分配执行并校验 Input 的当前授权，不是工作区 owner 的私聊读取权限。它使用仓储内部独立入口获取调度锁；HTTP/MCP 调用方无法通过请求字段启用该内部路径，普通成员私聊也可分配 Engine，撤权后排队输入仍能被拒绝并收尾。

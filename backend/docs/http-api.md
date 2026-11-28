# API 接口设计

状态：入口分区与命名约定沿用已确认方案；本页已按 2026-11-15 的[会话底座需求](../../docs/product/agent-sessions.md)收敛资源与操作范围。2026-11-16 已实现 Platform 邮箱认证、工作区、成员和邀请，契约见 [OpenAPI](../contracts/openapi.yaml)，行为和运行配置见 [Platform 身份设计](platform-auth.md)。Manager Conversation/Session、Engine 内部接口及 task_delegation 已接通；其余后续范围须按各专项文档区分，不表示全部上线。

backend 职责见[组件说明](../README.md)，通用 HTTP、认证、幂等及分页规则见[后端开发规则](../../docs/rules/backend.md)。产品语义由需求维护，本页仅维护到 backend 的映射。

## 入口分区与路由

| 入口 | 用途 | 当前范围与访问要求 |
| --- | --- | --- |
| `/api/v1/...` | Web、SDK 和外部集成的资源与操作 | 当前后端 API 默认认证，匿名例外逐操作声明 |
| `/api/internal/v1/...` | 授权组件之间的协作 | 受控内部入口及限定能力的服务身份 |
| `/model-proxy/v1/...` | 官方模型原生协议代理 | 使用员工代理 Key 及独立流式处理 |
| `/webhooks/{provider}/{connection_id}` | 第三方业务事件 | 后续渠道阶段采用，按提供方协议验签 |
| `/mcp/task-delegation` | Manager 内部主 chat 委派 | 已实现，核验专用运行身份，拒绝 Cookie/Origin，不允许外部个人 Agent 复用 |
| `/mcp/task-progress` | Manager 内部任务进展 | 执行任务按当前 Input 身份上报步骤摘要。详见[任务记录](task-records.md#对话卡片与执行计划)。 |
| `/mcp/platform-plugins/image-generation` | 平台预置文生图 | 执行任务凭当前 Input 的短期授权调用 `generate_image`，成功生成的图片按受理时单价结算。详见[平台预置插件与文生图](../../docs/proposal/platform-default-plugins-and-image-generation.md)。 |
| `/mcp/task-space-history` | Manager 内部 TaskSpace 任务查询 | 仅 TaskSpace 执行任务可挂载，按活动 Input 和来源权限读任务。见 [TaskSpace 执行历史](task-spaces.md#执行-agent-读取同空间任务) |
| `/mcp/work-items/{ordinary,assignee,review}` | Manager 内部事项工具 | 按当前任务角色提供候选查询、事项读取或验收。见[事项交接](work-items.md)。 |
| `/mcp/human-confirmations/{chat,task}` | Manager 内部人工确认工具 | 按角色暴露工具，使用当前 Input 的专用凭据，拒绝 Cookie/Origin。见[人工确认](human-requests.md#内部-mcp-接口) |
| 对外 MCP（路径待定） | webgw/mcp 的外部业务工具入口 | 仅预留包边界，无端点；后续另定外部登录、授权与契约 |
| `/healthz`、`/readyz` | backend 探针 | 按部署策略控制访问，不随 API 版本变化 |
| `/assets/...`、页面路径 | backend 同容器的 Web 静态资源 | 前端阶段交付，保留路由分区 |

不增加 public 或 protected 业务分组。internal 表示内部专用接口面，v1 表示其契约主版本。所有外部入站请求仍由唯一公网域名下的 backend 接收，公网代理应排除 `/api/internal/v1/` 和内部 MCP 路径（含 `/mcp/task-delegation`、`/mcp/task-space-history`、`/mcp/task-progress`、`/mcp/human-confirmations/`、`/mcp/work-items/` 和 `/mcp/platform-plugins/`），应用层继续核验服务身份和能力。当前实现共用单 listener，入口的网络可达性由部署控制。其他组件的实际 HTTP 入口及探针由该组件维护。

先匹配 API、协议入口、探针和静态资源，最后才处理页面回退。未知 API 或协议端点不能返回前端 HTML。入口前缀不决定访问权限，启用 MCP 时单独声明访问范围。backend 探针及启动遵循[部署与退出](../README.md#部署与退出)。

## 认证与授权

| 接口类别 | 身份与授权 |
| --- | --- |
| 对外资源接口 | 用户登录态或该操作允许的服务账号 / API Key，核验工作区和资源范围 |
| 显式匿名操作 | 仅身份流程或明确允许公开的数据，逐方法声明 |
| 内部协作接口 | 指定组件身份及能力，代表用户时保留经核验的真实主体 |
| 身份及插件授权回调 | 校验授权流程关联和凭证，不能因无需已有登录态而跳过验证 |
| 后续 Webhook / MCP | 按协议及来源验证，运行时仍受会话、轮次和当前主体权限约束 |

浏览器登录态优先同域安全 Cookie，SDK 和外部集成使用请求头凭据；细节由身份契约确定。Cookie 防护、多凭据冲突及匿名声明遵循[身份与业务写入](../../docs/rules/backend.md#身份与业务写入)。API Key 需解析到明确主体，不是新的业务身份。

工作区管理员仍走相同资源路径，管理权限不等于内部组件身份。标识只能定位待核验资源，不能授权读取；列表、详情、工具记录、SSE 和关键请求去重命中的资源同样核验当前权限。具体行为见[身份、授权与可见性](../../docs/product/agent-sessions.md#7-身份授权与可见性)。

**平台管理员**：账号增加 is_platform_admin；首次成功登录初始化一名管理员，后续由管理员在平台用户管理中授权。拟新增 `GET /api/v1/accounts` 和 `PATCH /api/v1/accounts/{account_id}/platform-admin`，`/me`、登录及注册成功响应返回当前标记。官方模板管理只接受平台管理员，工作区模板仍按工作区角色判断；完整契约见[身份设计](platform-auth.md#平台管理员)。

## 工作区与资源层级

账号和工作区入口沿用：

```text
GET /api/v1/me
GET /api/v1/workspaces
GET /api/v1/workspaces/{workspace_id}
```

工作区资源使用共同前缀 `/api/v1/workspaces/{workspace_id}`：

```text
/agents
/model-profiles
/skills
/plugins
/plugin-auths
/sessions
```

集合和详情保持相同归属层级；请求体不再提供独立可写 workspace_id。backend 校验资源及其配置引用的实际归属和可见性。平台预置模型或资源按真实平台归属另行确定入口，不能伪装成某个租户资源。

Skill 目录导入及插件包导入属于对应资源操作，不据此引入通用 files、artifacts 或共享目录接口。资源 ID、路径、JSON 字段及操作命名遵循[路径与操作命名](../../docs/rules/backend.md#路径与操作命名)。

## 配置资源的操作范围

官方模型与积分契约见[模型代理技术设计](model-proxy.md#4-api)：原 `model-profiles` 可选合并官方候选，员工创建/保存内部处理 Key；官方管理使用 `/platform-models`，余额与流水归账户，原生代理使用上表独立入口。没有员工 Key 管理 API。原生协议由[模型代理 OpenAPI](../contracts/model-proxy/openapi.yaml)维护。

| 资源 | 后续契约需覆盖的操作 |
| --- | --- |
| Agent | 创建、查询、保存配置、查看版本、启停；单套配置和模型选择，通过能力包获取技能与插件，不直接绑定 |
| CapabilityPackage | 员工绑定、查询、编辑资源与使用范围、显式升级；工作区模板复制后独立 |
| AgentTemplate | 保存自动累计版本，显式发布后市场可见；PATCH 承接内容和上下架属性；整体复制创建复用员工与资源用例，来源/使用范围扩展 Agent GET/PATCH，接入概览为只读聚合；详见[专项技术设计](agent-template-market.md) |
| ModelProfile | 创建、查询、更新版本、验证接入；新会话解析最新模型版本 |
| Skill | 导入普通目录、查询内容与版本、更新版本；不另建技能参数协议 |
| Plugin | 登记或导入 CLI / MCP 定义、查询与更新版本、检查接入条件 |
| PluginAuth | 创建、查询、修改、发起认证、查询状态、重新授权和撤销 |

Agent 和模型保存即生成版本，无草稿发布操作。包内 Skill / Plugin 新建或新绑定默认最新并固定，已有绑定提示并显式升级。同员工多个包同插件共用该员工的一份账号授权，各包工具范围可不同；账号映射不授予能力。Agent 停用拒绝新会话、新输入及未开始的旧输入，当前轮正常收尾；重新启用不恢复旧队列。PluginAuth 的同账号凭据刷新独立管理，换账号、租户或服务目标须新建授权并更新 Agent 绑定；旧会话保留原引用。普通接入设置的具体结构、验证动作和资源版本查询路径在配置契约中细化。

## 统一会话接口

下面省略工作区前缀，当前共 14 个会话操作。请求参数、返回模型、operationId 和错误码见 [Session、Input 与 Turn API 评审稿](session-api.md)，事件字段及 SSE 规则见[会话事件协议 v1](session-events.md)，实现时按已启用的类型转写 OpenAPI；Agent 发现等配置操作归上一节，本表只保留会话操作导航。

| 方法与路径 | 产品行为 |
| --- | --- |
| `POST /sessions` | 在 Conversation 内创建唯一主 chat 或新 Task/task Session，可携带首条文本输入 |
| `GET /sessions` | 查询有权访问的会话，支持按 Agent、模式和生命周期筛选 |
| `GET /sessions/{session_id}` | 查询会话及执行概况 |
| `PATCH /sessions/{session_id}` | 修改可编辑的标题，不修改 Agent、模式或快照 |
| `POST /sessions/{session_id}/inputs` | 提交文本输入或失败后的新输入 |
| `GET /sessions/{session_id}/inputs` | 查询受理及处理情况 |
| `GET /sessions/{session_id}/inputs/{input_id}` | 查询单条输入及关联轮次 |
| `POST /sessions/{session_id}/inputs/{input_id}/withdraw` | 仅撤回尚未开始执行的输入，竞争失败时明确返回状态冲突 |
| `GET /sessions/{session_id}/turns` | 查询实际轮次 |
| `GET /sessions/{session_id}/turns/{turn_id}` | 查询轮次状态、结果摘要、错误及用量；正式消息和工具记录读取事件 |
| `GET /sessions/{session_id}/events` | 分页查询统一事件历史，包含正式消息、工具记录和状态变化 |
| `GET /sessions/{session_id}/events/stream` | 使用同一事件结构实时推送，支持可靠续读 |
| `POST /sessions/{session_id}/stop` | 固定停止受理时已有工作，返回 Session，后续查看其执行状态 |
| `POST /sessions/{session_id}/archive` | 关闭新输入并收尾，返回 Session，历史只读 |

最小创建参数示意：

```json
{
  "conversation_id": "77777777-7777-4777-8777-777777777777",
  "mode": "task"
}
```

候选契约采用 UUID 字符串。Backend 核验 conversation_id 并解析员工；chat 在业务会话中唯一创建，后续聊天向原 Session 提交输入；task 同事务创建 Task/Session，不接受独立 task_id。没有 parent_session_id、群/用户交流范围、文件、指定 Skill 或 Sandbox 覆盖参数。空 task 创建可产生另一个任务；chat 受唯一约束。含首条 Input 则沿原 key 复用全部关联对象。业务会话管理入口另行细化，不改变 14 个执行操作的数量。

chat 使用平台固定的运行策略，并在创建时解析 AI 员工所选模型，不使用工作区或官方默认作为运行兜底；task 保持员工任务配置。调用方不能修改会话归属、模式或配置快照。Backend 业务用例一次建立 Task/Session，调用方不需分步创建，不引入 Run。Session stop/archive 只作用于自身；父 Conversation 归档先关闭全会话准入，再级联子级收尾并回收运行资源，文件保留。当前不提供 stop-tasks、Conversation stop、取消归档、删除 Session 或重试 Turn。

Input 与 Turn 保留独立身份，一条 Input 最多一个逻辑 Turn。同一执行 Session 串行，同一 Conversation 内不同任务可并行；未执行输入可撤回，内容不可原地修改，失败后继续提交新 Input。每次 stop 按本次受理时已有工作确定范围，之后输入不受这一次停止影响，再次调用覆盖其间新增工作；不提供独立控制标识或回执接口，直接查询 Session.execution。具体语义见[停止、继续与归档](../../docs/product/agent-sessions.md#停止继续与归档)。

## 事件与历史

GET events 返回 JSON 历史，GET events/stream 返回 SSE，二者共享[会话事件协议 v1](session-events.md)的平铺 Event 结构和会话内 seq，JSON 不使用事件级 data / payload 包装。五类核心 type 为 user.message、agent.message、agent.thinking、agent.tool_use、agent.tool_result；目录是预设计，实现按功能需要采用定义并启用对应子集，不要求全部先实现。正式消息、工具参数 / 结果及状态变化使用可靠事件；临时文本增量只实时发送，不承诺逐片重放。历史分页固定读取高水位，末页 resume_cursor 衔接实时流，断流不取消执行。

查询、订阅及重连核验当前权限，工具详情使用 input_state / output_state 和 input / output 投影，未授权为 redacted + null。字段、长连接撤权、游标过期、去重和衔接步骤见[事件协议](session-events.md#11-sse断线续读与消费)，接口操作见[会话 API](session-api.md#8-统一事件历史查询与实时流)。首版可靠事件随 Session 历史保留，不能因其通过 SSE 传输就按短期日志删除。归档仍可读取历史和收尾事实，当前事件不包含文件或产物资源引用。

## 内部接口与写入归属

Engine 注册、心跳、对账、释放和配置获取采用内部 HTTP，见[注册分配详细设计](engine-dispatch.md)。Engine 的准备/唤醒接口与 Runner 的管理及 MCP 入口分别由[Engine 设计](../../agent-engine/docs/session-runtime.md)和[Runner 设计](../../sandbox-runner/docs/runtime-and-mcp.md)维护；已有数据库交互不一律改为 HTTP，渠道和产物接口仍暂缓。

backend 拥有配置、输入受理、生命周期、控制意图和执行归属；当前 Engine 确认 Turn、消息、可靠事件及运行概况；原生上下文由 Pi 持久化并加载。撤回与领取通过条件转换协调，backend 不能替运行时宣告活跃轮次已停止。服务凭据按能力限定，普通主体或其他组件不能冒充当前执行者。完整边界见[数据归属](../../docs/architecture/data-ownership.md)。

## Webhook、身份回调与 MCP

员工的「接收外部事件」入口为 `POST /api/triggers/events/{trigger_id}`，支持 HTTP 和 HTTPS。每个触发器独立校验凭据，员工按保存的 Prompt 处理验签通过的业务事件。GitHub 短表单事件会将解码后的 `payload` 放入员工输入的 `body`，并在 `raw_form` 保留原始表单。业务事件无需携带仓库或项目 ID。旧事件配置中的 `repositoryId` 可继续读写，接入时忽略。HTTP 明文传输正文和请求头。签名、重投和握手响应见[回调契约](../contracts/modules/manager/triggers.yaml)。

后续第三方业务事件使用 `/webhooks/{provider}/{connection_id}`，connection_id 仅定位渠道绑定，不能当成凭据，也不等于 PluginAuth。提供方特殊握手由适配器另行声明。

登录及外部账号授权回调沿用 `/api/v1/auth/callbacks/{provider}` 的入口约定，与业务 Webhook 区分。PluginAuth 的 OAuth / CLI 授权需要绑定真实主体、工作区和目标授权记录；具体授权请求及回调契约由认证适配设计完成。

Pi 的原生配置分别登记 Sandbox builtin MCP、Runner 为 stdio 插件暴露的 HTTP MCP 端点以及远程 HTTP MCP，Engine 不提供 Gateway。stdio 子进程留在 Sandbox，由 Runner 负责传输适配；这些内部端点不挂到 backend 公网路由。Backend 的 `internal/manager/mcp/taskdelegation` 提供内部主 chat 的 Streamable HTTP 适配，复用 Manager 用例。入口 `/mcp/task-delegation` 已实现八个 task_delegation_ 工具，以专用委派 Bearer 确定来源 Conversation/chat 及当前执行归属，原子创建 Task/task Session/首条 Input，并查询、追加或停止任务；拒绝浏览器 Cookie 和 Origin。对外业务 MCP 与渠道 Webhook 分别归 `internal/webgw/mcp` 和 `internal/webgw/webhook`，目前仅预留，没有新增外部端点或通用登录授权。公开 Session 仍保留 14 个操作，不因此允许客户端提交 parent_session_id 或自行选择来源主体。跨员工委派及自动回报唤醒的接入条件见专项文档；个人 Agent 的登录与授权仍另行细化。通用边界见[远程 MCP 接入](../../docs/architecture/system-modules.md#远程-mcp-接入边界)，Sandbox builtin MCP 的 11 个工具见[专项设计](../../sandbox-runner/docs/builtin-mcp.md)；两者职责不同，MCP 契约不转成平台资源 API。

## 版本与契约组织

外部业务使用 /api/v1，内部使用 /api/internal/v1，同一接口面各模块共用主版本；两套接口面可独立演进。兼容性、滚动发布及第三方协议版本分别遵循[版本与兼容](../../docs/rules/backend.md#版本与兼容)。

后续契约布局示意，未实现模块不预建空文件：

```text
backend/contracts/
  external/
    openapi.yaml
    modules/
      identity/
      workspaces/
      agents/
      model-profiles/
      skills/
      plugins/
      plugin-auths/
      sessions/
    components/
  internal/
    openapi.yaml
    modules/          # 按实际内部协作模块建立
    components/
```

external 表示对外契约，包含逐操作声明的匿名例外。契约源和生成方式沿用[契约优先](../../docs/rules/backend.md#契约优先)。本轮不建立文件、产物或委派的空契约模块；task_delegation 的工具 JSON Schema 在实际接入时发布，复用 Session 业务用例，不为了 MCP 再复制一套 REST 操作。

取消所有写接口统一携带 Idempotency-Key 的要求。普通 CRUD 按资源唯一条件、目标状态和版本检查处理：标题使用 expected_revision，撤回和归档按当前状态重复调用。仅 Input 提交（含创建 Session 时携带首条输入）要求 key，保存轻量去重引用，不缓存完整响应，重试返回原资源的当前状态。stop 不要求也不读取 key，每次按当前工作重新停止，不保存请求去重记录；无状态变化时不重复写入或发事件。具体见[逐操作重试与并发约定](session-api.md#3-幂等与并发)。X-Request-ID 只作追踪，operationId 只命名文档和生成代码中的操作。

[AI 员工市场技术设计](agent-template-market.md)中的 `POST /workspaces/{workspace_id}/agents/from-template` 同样遵循普通创建约定，不要求请求去重键。客户端不自动重试；结果未知时先核对员工列表，显式再次提交可能创建另一位员工。

员工列表和详情返回该调用人的 `can_manage` 与 Web 私聊使用权 `can_chat`。没有目标员工管理权限时仅公开概览所需信息，不返回岗位行为规范、模型配置标识、模板参数和付款人；员工能力包列表和详情按目标员工管理权限校验，工作区模板目录不受影响。普通有效工作区成员可创建员工，员工资源写入按目标员工校验，不沿用工作区资源发布权限。维护名单使用 `GET /workspaces/{workspace_id}/agents/{agent_id}/maintainers` 和 `PUT` 同路径；读取返回 `{version, people}`，其中 `people` 为统一选人的已保存候选项。更新提交当前 `version` 和 `people`（统一选人的个人引用，最多 100 项，清空用 `[]`），不接受模式、继承、黑名单或部门。名单版本冲突返回 `409 maintainers_version_conflict`。读取和写入均需要目标员工管理权限。升级时先暂停旧 backend 的员工创建，再应用 Event Hub 迁移 105，旧员工创建人一次性回填为工作区 owner；有员工但没有 owner 的工作区须先修复，避免迁移猜测归属。

多 Backend 实例的输入排序、撤回 / 领取、停止 / 新输入和归档 / 新输入竞争，由数据库中的短事务与条件更新裁决；配置编辑另用预期版本。不同 Session 并行，同一 Session 最多一个活动 Turn，Engine 提交还需校验当前执行代次；具体锁顺序与恢复边界见[并发裁决设计](../../docs/architecture/session-data-model.md#7-事务与并发裁决)。

会话、输入、轮次和统一事件历史采用固定排序的游标分页，参数及候选上限见[会话接口详细设计](session-api.md)。配置资源列表按[分页规则](../../docs/rules/backend.md#排序筛选与分页)另行确定模式。

## 交付范围与验收

按[会话底座阶段](../../docs/product/agent-sessions.md#8-分阶段交付与验收)规划核心资源、能力包、文本会话、工具、授权和运行控制，按实际阶段验收。前端已按身份、模型、员工及本批资源管理推进；渠道、业务编排、文件与产物均不纳入本期契约。

除[通用接口验收](../../docs/rules/backend.md#接口验收)外，覆盖：

- 未知 API 不返回 HTML；匿名例外逐操作生效；用户和普通 API Key 无法调用内部入口。
- 工作区与资源引用一致，伪造主体不能扩大权限；旧快照、关键请求去重和长连接均受当前授权约束。
- chat / task 共用接口且核验 Conversation 归属；同一 Conversation 至多一个主 chat，每个 Task 唯一 task Session；临时配置覆盖被拒绝。
- 首条及追加 Input 沿原 key 重试只受理一次且不重复创建 Task；stop 重发按当前工作重新停止。空 task 可产生另一任务，chat 受唯一约束，普通修改与撤回/归档按版本或状态处理。
- 撤回与开始竞争结果唯一，已开始不能撤回；同一 Input 不生成第二个逻辑 Turn。
- 停止每次覆盖本次受理时已有工作，重试也会覆盖其间新增的输入；没有状态变化时不重复发事件。归档关闭新输入、收尾且只读；不提供恢复、删除或重试轮次。
- SSE 重连恢复可靠状态，跨范围游标被拒绝，断流后执行继续。
- Agent、模型、Skill / Plugin 的更新传播分别遵循需求，PluginAuth 撤销不能被快照绕过。
- 文件、产物及委派执行链路仍未实现；task_delegation 仅补齐工具设计，不因文档落盘就视为功能已交付。

## 后续契约设计

数据模型和并发状态机的第一稿见[技术草案](../../docs/architecture/session-data-model.md)。草案评审后，契约仍需补齐：

- 身份登录、Cookie 与请求头认证、CSRF、匿名操作、主体冲突及资源存在性策略。
- 评审 [Session、Input 与 Turn 详细设计](session-api.md)，将 14 个操作及统一事件的请求、响应、错误和权限转写 OpenAPI；其他五类配置资源继续细化独立契约。
- 实现并验证配置快照、Input / Turn 状态联动、Agent 停用代次、工具详情权限、撤回与控制的并发规则。
- 落实历史分页与 SSE 衔接、权限投影及游标失效后的完整重建，验证正式历史不会被当作短期传输日志清理。
- PluginAuth 授权发起、回调、续期、撤销及非敏感接入设置与凭据引用的装配关系。
- 将[内部注册与分配设计](engine-dispatch.md)转写契约，并联调 Engine / Runner 的默认环境生命周期与失败恢复；builtin MCP 按[工具专项设计](../../sandbox-runner/docs/builtin-mcp.md)移植并验证 HTTP 接入。
- 配额、并发限制、超时及分页上限；本期不提前设计文件大小或文件保留策略。

### 托管助手发放与入口

- 先应用 Event Hub 迁移 106。默认关闭的数据库发放开关由管理员通过 GET/PATCH /api/v1/platform/assistant-settings 查看和保存。更新携带 expected_version 与 enabled。
- 开关启用后，注册自动建区和手动建区都通过同一个数据库事务留下发放记录。Backend 按工作区异步领取并重验开关与已验证的平台默认模型；不补发存量工作区。创建员工、默认确认人、模型扣费绑定、内置能力包、助手关联和发放成功一起提交。
- GET /api/v1/workspaces/{workspace_id}/assistant 仅为有权使用该 Web 私聊的成员返回 agent_id，满足体验包 Web 私聊权限才返回体验卡。卡片只发送一条普通聊天输入。工作区发放中返回 pending，不暴露后台错误详情。
- 管理包默认工作区创建人的 Web 私聊可用；体验包继承员工私聊范围；群聊与触发器默认关闭。运行时凭证绑定本轮输入和执行分配，每次工具调用重验场景及权限。管理工具目前只返回接入状态，不读写业务配置，体验工具只用样例。

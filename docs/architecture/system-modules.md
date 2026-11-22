# 系统模块与职责边界

文件持久化跨模块遵循[统一存储配置与资产约定](storage-configuration.md)，不新增公共存储 lib 或独立服务。Platform、Manager 和 Engine 各自实现所需适配，启动层注入同名配置；Event Hub 不因此成为配置、密钥或文件中心。

状态：2026-11-15 确认系统级模块划分，2026-11-16 补充远程 MCP 接入、task_delegation 工具集及 manager 命名；2026-11-17 将内部委派归 Manager、对外入口归 webgw。本文同时标注已实现与后续边界，不表示所有目标功能均已实现，也不把后续 AI 员工业务提前变成当前 Session 底座的必填依赖。

## 已确认的划分

Backend 为多实例业务服务，进程内先划分三个系统模块。模块名使用 `platform`、`manager`、`webgw`；不再用 `runtime` 命名 AI 员工业务运行层，以免与 Agent Runtime 混淆。Engine 客户端属于 `manager` 的内部适配，不再单列 `engine-api` 系统模块。

```text
web                         前端页面，迁入 web 组件
  │
backend（多实例）
  ├── platform              平台管理与配置
  ├── webgw                 对外入口（webhook / mcp，仅预留）
  └── manager               AI 员工业务运行及 Engine 对接
       └── mcp/taskdelegation 内部主 chat 的任务委派
         ▲          │
         │          ▼
event-hub              agent-engine（多实例）
长连接、定时触发         Agent Loop、运行时 Session、交互/执行 Agent
                             │ MCP
                             ▼
                       sandbox-runner
                       工具执行与工作环境
```

这三个名称表达系统边界，不是按页面拆出的账号、员工、任务等细粒度业务包，也不意味着立刻拆成三个进程。实际 Go 包目录在实现时按组件规则确定。

**2026-11-18 已实现**　官方模型与积分集中到 `backend/internal/platform/modelproxy/`，作为 Platform 的独立功能子模块，仍共用 Backend 进程、Go module 和数据库，不增加第四个系统模块。内部维护官方模型、员工 Key、模型 HTTP 转发、用量及积分结算；原注册、员工创建、模型选择与运行配置通过窄接口接入，事务参与和完整目录见[模型代理设计](../../backend/docs/model-proxy.md)。流式模型请求属于该子模块，不改变机器人渠道长连接归 Event Hub、Agent Loop 归 Engine/Pi 的边界。

## 命名过渡

本文的顶层目标模块为 `platform`、`manager`、`webgw`。`manager` 承接此前顶层 `service` 的 AI 员工业务运行及 Engine 对接职责；`service` 留作模块内部的业务用例层名称，避免同名混淆。

会话底座实现已采用 `backend/internal/manager` 并移除原顶层 service 占位。Manager 当前包含 controller/service/repository 和内部 mcp/taskdelegation；其他层随实际调用按需建立。Backend 的具体包布局与分层约定见[组件说明](../../backend/README.md#模块命名与内部分层)。

## 各模块负责什么

| 模块 | 职责 | 不负责 |
| --- | --- | --- |
| `platform` | 管理账号、工作区、AI 员工、渠道、模型、资源、权限及规则配置；提供管理端查询与操作入口 | 不运行 Agent Loop，不持有机器人长连接 |
| `webgw/webhook` | 接收渠道 HTTP 回调和通用 Webhook，按协议验签、解密、校验、转换及返回回执；将事件交给 `manager` 统一受理 | 不另建一套消息权限、业务任务或交接规则 |
| `webgw/mcp` | 接收后续个人 Agent / 外部集成的业务 MCP 请求，按各自身份处理协议并交 `manager` 受理 | 不运行 Agent Loop，不另建授权或任务状态机，不作为 Sandbox builtin MCP；工具设计不代表已实现 |
| `manager` | AI 员工消息受理与鉴权、业务会话映射、任务、人工确认、事项交接、消息投递与业务状态；对接 Engine，提交输入和控制意图、处理执行事件，并提供受控业务工具入口 | 不运行模型循环，不维护 Harness 内部上下文，不替代 Engine/Runner 执行模型和工具 |

`manager` 是 AI 员工业务服务，不是任意公共代码的集合。日志、数据库连接、配置装配等基础设施不因通用性而放入 `manager`。它内部可以按职责继续拆分业务包和 Engine 客户端适配包。

Web 管理操作与消息触发应复用同一业务用例；例如管理页面提交人工确认答案，仍由 `manager` 校验问题状态、身份和幂等，不允许 `platform` 绕过业务规则直接改运行终态。

## 其他组件边界

- **Event Hub**：业务职责收窄为长连接及定时触发，避免在当前全局单活组件中集中复杂 AI 员工逻辑。协议收发、连接状态和必要回执仍属于连接适配；需要沿原连接回复时，由持有连接的 Hub 完成发送，`manager` 管理投递身份、状态与业务重试。普通 Webhook 在 Backend，不经过 Hub。已有选举与数据库迁移基础设施不因本次命名调整而删除或迁移。
- **Agent Engine**：托管交互 Agent 与执行 Agent，管理 Agent Loop、运行时 Session、上下文、执行事件及恢复。不内嵌 AI 员工的确认人、交接策略等业务规则；Agent 可以通过受控业务工具调用 `manager`。交互和执行统一托管，不代表共享同一上下文或必须采用同一种 Harness。
- **Sandbox Runner**：提供获准的工具执行和工作环境。保留现有首版裸目录与后续容器方案的区分，不把裸目录描述为容器隔离。
- **Web**：工作台与管理端页面位于 `web` 组件；接口及工程脚本按 Backend 契约适配。

Event Hub 内部先按 `channel`、`scheduler` 和生命周期职责组织；生命周期由现有 `app`、`election`、`database/postgres` 协作承担，不新建独立 Go module 或强制搬入 `lifecycle`。当前已实现与后续包布局、未来拆进程约束统一维护在 [Event Hub 内部模块与 Go 工程组织](../../event-hub/README.md#内部模块与-go-工程组织)，不在此重复定义。

`manager` 创建 Conversation、Task 和 Session 并维护归属/生命周期；Engine 只承担 Session 的运行及实际结果。首版一 Task 一 task Session，一 Session 可有多个 Input/Turn；人工确认协议另行设计。Backend 登记唯一共享环境及管理 Engine，首版同会话 task 固定到此 Engine/宿主；归档回收裁决归 Backend，Engine/Runner 执行收尾，详见[生命周期](conversation-lifecycle.md)。

## 各部分的调用与限制

| 部分 | 负责的逻辑范围 | 调用与状态边界 | 不负责 |
| --- | --- | --- | --- |
| Web | 配置编辑、任务与会话展示、人工操作、连接及运行状态展示 | 调 Backend 的公开业务接口，真实回执决定保存和执行状态；不直接访问数据库、Engine 或 Runner | 不做最终权限裁决，不把 mock 成功作为真实成功，不执行后台业务推进 |
| Backend / platform | 管理配置、身份、资源、权限策略及管理端查询；通过子模块提供官方模型代理和积分 | 为 manager 提供所需配置能力；涉及业务状态变更的管理动作走 manager 用例 | 不直接越过 manager 改任务/确认终态，不运行 Agent Loop 或持有机器人渠道长连接 |
| Backend / webgw/webhook | 外部 HTTP 渠道回调、通用 Webhook 入口及协议响应 | 转换统一入站事件并交 manager；来源认证与业务授权分别处理 | 不运行 LLM，不另起一套任务状态或业务去重逻辑 |
| Backend / webgw/mcp | 后续个人 Agent / 外部集成的业务 MCP 协议适配 | 核验对应主体、工作区及会话范围，复用 manager 的业务用例和授权；传输连接与业务任务分离 | 不直接调用 Engine 绕过业务规则，不持有渠道长连接，不承担 Sandbox 工具执行 |
| Backend / manager | 消息鉴权、业务会话、任务、确认、交接、出站记录、业务重试；Engine 客户端适配与执行事件处理 | 对接 Engine 的输入和控制接口；向 Hub 提交需要连接发送的请求并收回回执；Agent 业务工具调用在此校验并持久化 | 不维护 Harness 内部上下文，不把模型推测或一次 HTTP 受理等同于执行/送达成功 |
| Event Hub | 长连接的建连、重连、协议收发和状态；定时点触发；维持已实现的选举与迁移基础设施 | 长连接消息及定时事件交 Backend/manager；沿所持连接发送并回报可证实回执 | 不判断 AI 员工业务意图，不选择确认人，不承载人工确认或事项状态机；Engine 注册与 Session 分配由 Backend 承接 |
| Agent Engine | 托管交互/执行 Agent；Agent Loop、运行时 Session、上下文、轮次执行、停止、事件及 Pi 原生会话加载；按现有设计准备运行环境 | 接收 Backend 提交的工作及控制意图；输出运行事实；Agent 经 MCP 调 Runner 或获准的业务工具 | 不持有渠道业务规则，不自行决定平台授权、确认人或事项流转，不替 Backend 判定投递成功 |
| Sandbox Runner | 会话工作目录中的文件、命令及获准工具；权限校验、工具回执、环境内后台进程管理 | 接受获准 MCP 调用；业务工具需经受控接口进入 Backend/manager，不能靠沙箱身份绕过用户授权 | 不运行交互/执行 Agent Loop，不管理员工配置和业务任务终态；首版不宣称容器/网络安全隔离 |

Backend 中的三个模块先使用进程内接口，不为解耦强制添加网络调用。除已有专项设计与下述 MCP 传输方向外，跨进程路径在此只定义谁对接谁，不预设 HTTP、RPC、消息队列、数据库通知或轮询机制。Engine 注册、归属与内部配置接口按 [Backend 详细设计](../../backend/docs/engine-dispatch.md)执行；其他涉及数据库、持久事件和投递的具体协议需在后续设计中明确。

## 远程 MCP 接入边界

按调用方向和信任边界划分，不按是否使用 MCP 协议混为一个模块：

| 入口 | 归属与状态 | 身份边界 |
| --- | --- | --- |
| 内部 task_delegation | `manager/mcp/taskdelegation` 已实现六个工具，保持 `/mcp/task-delegation` | 主 chat 的专用委派 Bearer，绑定 Session、Engine、assignment/generation；拒绝 Cookie/Origin，不允许匿名或外部个人 Agent 复用 |
| 对外业务 MCP | `webgw/mcp` 仅预留，无外部端点或登录流程 | 后续核验外部主体、工作区与能力范围，复用 Manager 最终业务授权，不共享内部委派身份 |
| 渠道 HTTP / Webhook | `webgw/webhook` 仅预留 | 后续按渠道验签、解密、转换和协议回执；持久受理、去重与业务授权归 Manager |

```text
内部：主 chat / Pi → manager/mcp/taskdelegation → manager 用例 → Engine
外部（待实现）：渠道回调 / 个人 Agent → webgw/webhook 或 webgw/mcp → manager 用例
```

内部委派采用官方 Go MCP SDK 的 stateless Streamable HTTP；契约、六个工具、专用凭据、跨员工扩展及结果回报边界见[专项设计](../../backend/docs/task-delegation-mcp.md)。传输会话不等于业务 Conversation / Session，协议成功不等于任务完成，自动回报/唤醒仍未实现。

**部署与信任边界**：当前仍由 `app` 在同一 Backend 进程、同一 HTTP listener 装配，保留现有路径及鉴权行为。移动 Go package 不构成网络隔离；直接暴露整个 Backend listener 时，内部入口仍可被网络访问，只受应用层凭据检查保护。公网代理应排除 `/api/internal/v1/` 和 `/mcp/task-delegation`，同时保证内部 Engine 可达。独立内部 listener 或独立 webgw 进程属于后续部署方案，本次没有实施，也不声明已经隔离。

外部 webgw 只负责协议认证、验签、转换和回执，业务持久化、授权、去重及任务状态机复用 Manager，不直接调用 Engine 绕过业务规则。后续实现需明确持久受理与 ACK 顺序、多副本恢复、重试、撤权及取消；断流不得丢失已受理任务，也不自动取消任务。不预设个人 Agent 都支持主动唤醒或后台推送。

以上均不同于 Sandbox builtin MCP、Runner 插件传输适配及资源库出站 MCP 插件。机器人长连接与定时触发继续属于 Event Hub，本次不移动或新增这些功能。

## 与已有设计的差异及待定事项

最小实现已由 Backend 承接 Engine 注册/心跳、数据库分配和 Runner 配置控制，实际范围见[运行说明](../operations/minimal-agent-runtime.md)。以下保留完整目标与后续协议边界：

1. **执行分配与 Session 单活**：Backend 维护注册、心跳、容量和 assignment/generation；Engine 只推进当前归属的执行事实，event-hub 不参与分配。内部 OpenAPI 与裸环境凭据已接入；容器环境、失联自动接管和其他高级供应适配不在本期交付范围。
2. **共享数据库与接口边界**：保留现有 PostgreSQL 架构和数据写入权，直到后续明确调整。模块化不等于独立库，也不要求同进程调用绕行 HTTP。未来拆进程需逐项明确契约、授权和事务边界。
3. **可靠事件受理和 ACK**：Webhook 与长连接统一进入业务受理，但不同渠道有不同响应时限。持久化受理、去重与 ACK 的具体顺序以及失败补偿需按渠道定义，不能先确认后仅用内存转交。
4. **触发与投递**：Hub 产生定时事件，`manager` 推进业务。计划版本、计划时刻去重、漏触发补偿，以及投递领取、回执丢失和未知结果核对协议，需细化。已有数据库迁移机制维持原状。
5. **交付范围**：当前[会话底座需求](../product/agent-sessions.md)仍是基础交付基线。

当前架构图、数据归属及组件文档以 Backend 承接 Engine 注册和 Session 分配为准；新开发应结合上述详细设计，不再沿用 Hub 负责执行分配的旧表述。后续补齐契约时继续同步时序图、组件文档和协议，避免形成两套相互矛盾的实现依据。

## 验收重点

后续实现需验证多个 Backend 实例受理同一消息或确认只产生一次有效业务动作；不同入口复用相同鉴权；任务完成而发送失败只恢复投递；业务会话和执行 Session 边界清晰；Hub 故障不会使已持久化的业务事件丢失。具体测试随功能落地，不以本次文档更新表示这些场景已通过。

### 2026-11-17 重构验收与已知限制

内部 task_delegation 只移动 package/import，HTTP 路径、工具契约与授权行为保持不变；外部 webgw 仍只预留边界。合并能力执行提交后，在 Mac 上使用隔离 PostgreSQL、真实 Engine/Pi/builtin 与测试 TLS MCP 执行验收，不调用收费模型或修改业务数据库。

验收期间曾观察到一次归档请求 `40P01` 死锁；原锁环尚未定位，后续重复通过不能视为根因修复。Manager 增加仅 `40P01` 的完整事务有限重试，规则见 Backend README。确定性测试分别覆盖真实锁等待、注入 PostgreSQL 死锁错误后的回滚/单次写入、重试上限及重新鉴权；注入测试不是重现原锁环。

Mac 的 builtin 主套件通过，但额外 Linux 进程测试中的辅助组列表断言有平台差异，不放宽其断言；Linux 验收以对应 CI 为准。

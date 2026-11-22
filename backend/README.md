**主对话待办改造（开发中）**：主对话工具增加待办记录、显式启动和未启动取消，移除目标员工列表；具体状态、事务与边界见[主对话待办](docs/chat-planned-tasks.md)。

# backend · 业务与调度后端

员工「接收外部事件」通过 `POST /api/triggers/events/{trigger_id}` 接收 GitHub、GitLab 和自建系统的通知。Manager 将配置的处理说明、事件原文及所选结果群绑定到本次任务。部署需要 Event Hub 迁移 87。见[触发规则设计](docs/trigger-rules.md)。

[后台任务找人确认](docs/human-requests.md)支持 Web 指定成员、Pi 停稳、工作台或固定主私聊答复，以及沿原 Task 和 Session 续跑。

**TaskSpace / 定时 / Hook（2026-11-18，联合验收中）**：新工作区已实现独立 TaskSpace、可信触发输入、generic/GitHub Hook、cron、固定定时提醒和群结果通知，未部署。历史实施记录中的“定时未交付”不代表本轮代码状态；Web 人工确认已接入，会话快照与历史见[会话事件协议](docs/session-events.md#sessionrequires_action)。纯文本事项交接已接入，成果返回原任务验收。见[事项交接](docs/work-items.md)。TaskSpace 不自动注入其他任务历史；cron L/#、预览和自定义密钥导入未包含。通知与验证边界见[通知设计](docs/trigger-notifications.md)。

**任务记录（2026-11-17）**：新增 Task 列表、详情和回报三个只读接口，复用现有 Task/Session/Input/Turn 事实与私聊可见性；任务页和对话共用详情。数据库仅增加迁移 16 的查询索引，操作继续进入 Session 用例。契约、兼容和验收入口见[任务记录](docs/task-records.md)。

**Input 合批与渠道话题（2026-11-18，实施中）**：迁移 19 增加默认 true 的 trigger_agent_loop 和 system / human_confirmation 来源，迁移 20 关联有序批次，迁移 21 隔离主会话/各话题。历史 false Input 等待首个 true 由 Engine 合成一条原生 Pi user prompt；群历史边界以本员工事件成功写入 Input 为准，不等待 native committed。公开请求不开放自选来源/触发字段。飞书群话题按 thread_id 分页补历史，私聊话题不抓历史；锚点/范围不可靠时只处理当前事件，不借主群历史替代。非 user sender 保留防循环过滤，渠道人工答复入口仍待接入。代码、局部验收及限制见[输入专项](docs/session-input-and-task-report.md#input-有序合批2026-11-18实施中迁移-20)和[渠道运行](docs/channel-runtime.md)。

**任务回报与输入方式（2026-11-17）**：输入默认 steer，可选 queue；Engine 提交轮次结果后调用内部回调，Manager 原子登记回报与主 chat 输入，event-hub 每 30s 兜底补偿。backend 不新增回报扫描 worker。实现、权限、兼容与发布见[专项设计](docs/session-input-and-task-report.md)。

**Web 接入支持（2026-11-17）**：新增固定私聊解析接口 `POST /conversations/web-direct`，同工作区、用户和 AI 员工唯一；私聊及子 Session 由 owner 隔离读写，后端归档保留原有停稳规则，Web 默认不显示归档入口；手动归档后固定映射仍指向原历史，不新建替代。可靠历史新增 `direction=backward`，最近页同时返回向旧页游标和固定高水位 SSE 游标，默认正向接口兼容。会话 API 已实现，Web 接入及自动回报的进度分别验收，不将接口可用当作前端已联调。详见[会话接口](docs/session-api.md#web-固定私聊与最近历史2026-11-17)。


**能力执行进度（2026-11-17）**：task Session 已保存获准能力包及资源/账号版本快照；运行时按当前 Input 主体重验，支持固定目录 Skill 与静态认证远程 MCP 的执行配置、逐调用授权和审计。公开 Session 操作仍为 14 个，新增接口均为内部受限入口。实现及边界见[资源与能力包设计](docs/platform-capabilities.md)。

**实施进度（2026-11-16）**：Manager 已实现 4 个 Conversation 与 14 个 Session 操作、输入去重/排队、停止/归档、可靠历史/SSE、Engine 注册心跳与分配，以及受限 Runner 配置。manager/mcp/taskdelegation 通过官方 Go MCP SDK 1.8.0 提供八个主对话任务工具，复用 Manager 并记录来源 chat。配置 KIKO_ENGINE_TOKEN（至少 32 字节）后开启执行分配；内部服务、Runner 配置、委派 MCP 和浏览器凭据分别授权。实际 Engine/Runner/Pi 链路使用确定性模型服务验证；范围和限制见[运行说明](../docs/operations/minimal-agent-runtime.md)。

> **2026-11-16 关系与生命周期已确认**：一 Task 一 task Session，共享环境归 Conversation；创建、停止、归档、首版放置及三层回收按[业务会话与共享环境生命周期](../docs/architecture/conversation-lifecycle.md)。下文保留完整目标范围；当前最小实现与限制见仓库运行说明和实施计划。

**存储配置**　Platform Skill 资产已有本地/S3 适配；本期会话使用 PG 与 Engine/Runner 的本机持久目录。其他 Manager 资产按[统一存储配置与资产约定](../docs/architecture/storage-configuration.md)后续接入，不新建统一存储 lib。部署层按消费者注入配置，本地根默认 `~/.kiko/`。

组件名、目录、Go module 和命令统一使用 `backend`。API 表示其提供的接口，不再作为组件名称；HTTP 路径 `/api/v1`、`/api/internal/v1` 保持不变。

**系统模块更新（2026-11-16）**：Backend 进程内按 `platform`、`manager`、`webgw` 划分；`manager` 承接 AI 员工业务运行与 Engine 对接，不是 Agent Runtime。职责和迁移映射见[Backend 系统模块设计](../docs/architecture/system-modules.md)，Engine 注册与 Session 分配按[详细设计](docs/engine-dispatch.md)由 Backend 承接。本文后续范围与既有数据约束在明确调整前仍保留。

Go 实现，可部署多个副本。Backend 承担平台业务逻辑、资源与状态流转、Engine 注册和 Session 调度，同时提供 HTTP API 与 Web 入口。平台唯一的公网域名指向 Backend，每个副本都从本容器提供 Web 静态文件，并路由资源接口、SSE 和外部回调；内部组件接口仅向授权的内部调用方开放。Web、SDK、HTTP 回调和渠道输入共用业务用例，输入回执和任务状态持久化。

## 职责与边界

- **身份与授权**　解析调用主体及工作区，校验资源、模型、Skill、插件操作和 PluginAuth 的当前使用范围。配置快照及内部服务身份不能绕过撤权。
- **配置资源**　管理 Agent、员工能力包、ModelProfile、Skill、Plugin、员工插件账号映射及 PluginAuth 版本。员工仅通过能力包绑定技能和插件，各包限定工具范围；同员工多包同插件共用该员工的一份授权。Agent 只有一套执行配置，保存即生效，生成参数跟随 Harness；模型更新影响引用它的新会话，包内 Skill / Plugin 初次绑定默认最新并固定，已绑定项提示并显式升级。
- **启停与账号变更**　Agent 停用关闭新会话与新输入，拒绝尚未开始的工作但允许当前轮收尾；重新启用不恢复旧队列。PluginAuth 换账号或服务目标时新建记录，旧会话不静默切换账号。
- **插件授权**　管理静态凭据、OAuth 和 CLI 登录的授权记录、状态、续期及撤销。保存凭据引用，向运行时提供限范围认证材料；不把 Token 刷新当成插件升级。
- **业务会话与执行输入**　创建 Conversation 元数据，首次聊天建立唯一 chat Session，任务用例一次建立 Task/唯一 task Session/首条 Input。管理归属、生命周期、快照和控制意图；仅 Input 提交去重。员工从 Conversation 解析，不接受 parent_session_id、群范围、指定 Skill、文件或 Sandbox 覆盖。
- **执行查询与控制**　提供 Session / Input / Turn 查询，以及统一事件历史和 SSE；消息、工具详情和状态变化共用事件结构。受理未开始输入撤回、停止及归档，直接返回 Session 供观察执行状态，不提供独立控制标识。Engine 确认实际结果，Backend 不直接将活跃轮次写成取消完成。Session 概况单列后台进程数；无活动 Turn 的 stop 仍处理本 Session 后台工作，不影响共享 Sandbox 中其他执行 Session。
- **Engine 注册与分配**　接收注册、心跳及对账，维护容量、Session 归属和 Conversation 唯一环境/管理者。首版同会话 task 固定到管理 Engine/宿主，chat 独立分配。多副本用数据库裁决并发，周期维护补偿通知和核对失联。
- **归档与环境回收**　Session 级控制只作用自身；Conversation 首次归档要求主 chat 与全部关联任务无运行、排队、后台或未知活动，否则 409；先停止并等收尾，再关闭父准入并推进子级归档。全部停稳后授权管理 Engine 回收运行资源，活跃会话不自动空闲回收，历史和文件保留。
- **运行配置**　分别向 Engine 和 Runner 提供固定资源清单与当前授权材料，核验调用主体、会话、实例及执行代次。Runner 主动领取 Sandbox 配置，不直接访问业务数据库。

**后续范围**　不引入 Run；业务管理页面、自动委派结果回报、模板、渠道、定时、人工协作和 Delivery 后续细化，文件体系另行设计。最小归属和生命周期已接入，详见[生命周期](../docs/architecture/conversation-lifecycle.md)及[会话底座需求](../docs/product/agent-sessions.md)。

**写入归属**　Backend 提交配置、受理、控制意图及会话执行归属，当前 Engine 提交 Turn、消息、可靠执行事件及运行概况，Pi 自己维护原生历史与映射。输入撤回与开始领取通过条件转换互斥，详见[数据归属](../docs/architecture/data-ownership.md)。

**会话事务死锁缓解**　Manager 的 `WithSession` 仅对 PostgreSQL 明确返回的 `40P01` 重试整个已回滚事务，最多 3 次，每次重建事务并重新鉴权、获取锁及读取状态；等待可由请求 context 取消。回调只能含事务内数据库操作与本地计算，必须重置尝试输出，不得包含外部网络、进程或文件副作用。连接错误、其他 SQLSTATE 和结果不明的提交不重试，也不整体重放已部分提交的 Conversation 归档。该机制是短暂死锁的有限缓解，不代表已消除锁环。

**数据库启动行为**　PostgreSQL 迁移由 [event-hub](../event-hub/README.md#启动与数据库迁移) 统一执行，Backend 维护自己的业务查询和数据访问代码。Backend 不执行迁移，不检查数据库结构版本，也不以迁移完成作为启动或整体接流量的前置条件。缺表或缺字段造成的错误按本次请求失败处理，不能导致进程退出或永久失效的初始化状态；结构补齐后，后续请求正常恢复。

**验收场景**　同一次 Input 提交沿同一去重键落到两个 Backend 副本时只受理一次；撤回与执行开始竞争时只有一个结果。stop 不要求 key，再次调用会停止其间新增的工作，无状态变化时不重复发事件；归档后拒绝新输入，旧快照不能绕过 PluginAuth 撤权。

Web 私聊的项目目录与[文件接口](docs/conversation-files.md)使用会话相对路径。

## 接口设计

**AI 员工市场（2026-11-18，已实现）**　[API、数据库与关键流程](docs/agent-template-market.md)统一使用 AgentTemplate：保存自动累计版本、发布切换市场版本；模板业务收敛为四张表，来源与使用范围复用员工 GET/PATCH。创建为无请求键的普通 POST，资源准备复用已有 Skill 暂存与存储，不建租约任务；执行沿现有资源授权过滤，评价及验证徽章后置。完整能力由模板预设。

**平台管理员（2026-11-18，已实现）**　官方模板仅平台管理员可管理，工作区模板仍由工作区 owner/admin 管理。第一个成功登录的用户自动成为平台管理员，之后可在平台用户管理中给其他账号授权；账号字段、一次性初始化与授权 API 见[身份设计](docs/platform-auth.md#平台管理员)，不再使用发布者账号配置白名单。

**官方模型与积分（2026-11-18，已实现）**　独立功能模块位于 `internal/platform/modelproxy`，沿用单进程、单 Go module。官方模型合并到现有模型选择入口，员工 Key 内部创建和下发；请求完成后按员工固定扣费账户结算，允许余额为负，不做任务冻结或独立 Key 管理。API、六张业务表、事务、目录与验收见[模型代理技术设计](docs/model-proxy.md)，UI 见[产品设计](../docs/product/official-models-and-credits.md)。官方代理按单次模型请求裁决停用，不保证当前整轮后续模型请求继续放行。集成验收及环境边界见[实施验证](docs/model-proxy.md#12-实施验证)。

`internal/manager/mcp/taskdelegation` 已实现主 chat 的八个主对话任务工具，入口 `/mcp/task-delegation`，机器契约在 contracts/taskdelegation/tools.json；业务授权、受理和持久化复用 Manager。个人 Agent 等通用 MCP 后续实现，边界见[系统模块](../docs/architecture/system-modules.md#远程-mcp-接入边界)。

`internal/manager/mcp/taskspacehistory` 为 TaskSpace 的执行任务提供同空间任务列表、状态、执行过程和结果查询。见 [TaskSpace 执行历史](docs/task-spaces.md#执行-agent-读取同空间任务)。

[Engine 注册、Session 分配与运行配置](docs/engine-dispatch.md)维护完整目标。首版采用数据库分配及轮询补偿，机器契约见 contracts/internal/openapi.yaml；高级失联接管与消息推送后续实现，不改变 event-hub 的迁移归属。

[API 接口设计](docs/http-api.md)维护入口分区、层级和边界，14 个会话操作已进入 OpenAPI 并实现。[会话事件协议 v1](docs/session-events.md)维护目标事件字典，首版实现完整消息、工具和 Input/Turn/Session 状态子集及历史/SSE，不发送临时 delta；其他类型不因此视为已交付。

新增或修改接口先读对应设计章节，再按[后端开发规则](../docs/rules/backend.md#http-接口与客户端)核对通用规范。实现阶段同时验收[项目接口场景](docs/http-api.md#交付范围与验收)和所属业务需求，不因新增内部 HTTP 入口而改变组件写入归属。

内部 `manager/mcp/taskdelegation` 与未来对外 `webgw/mcp` 使用不同信任边界：前者只接受绑定来源 chat/Engine/assignment/generation 的委派 Bearer，拒绝 Cookie 和 Origin；后者需另行定义外部主体登录与授权，不复用委派凭据。当前共用同一 Backend 进程和 HTTP listener。公网代理应排除 `/api/internal/v1/` 与[内部 MCP 路由](docs/http-api.md#入口分区与路由)，内部 Engine 保持可达。

## 部署与退出

GitHub Actions 随每次 `main` 更新发布含 Web 的 `ghcr.io/unrealm-ai/kikoteam/backend`。支持的平台和镜像标签见[镜像说明](../README.md#容器镜像)。

Web 静态构建产物随 Backend 制成同一容器镜像，平台唯一公网域名路由到 Backend 副本。验收同域页面、静态文件、业务接口、SSE 和外部回调，确认内部组件接口仅在授权的内部访问范围内可用。具体入口与访问范围见[入口分区与路由](docs/http-api.md#入口分区与路由)。静态前端的产品展示名可独立配置。

Backend 可滚动更新，退出先停止接入，再收尾在途请求；SSE 客户端可连到其他副本续读。启动及就绪检查不校验数据库结构版本或完整性，也不等待 event-hub 迁移。已经失败的请求按原回执及重试约定处理，后续请求在结构补齐后正常恢复。

## 工程骨架与进程内模块

当前使用一个 Backend Go module 和一个 `cmd/backend` 命令入口，三个系统模块作为进程内 package，不各建 `go.mod`，不各启动 HTTP 服务。原顶层 service 占位已移除，业务执行实现归 manager。

```text
backend/
├── go.mod                     # Backend 自己的依赖和导入路径
├── Makefile                   # 本地与 CI 共用检查入口
├── cmd/backend/main.go            # HTTP 进程入口
├── internal/
│   ├── app/                   # 配置、依赖装配和生命周期边界
│   ├── platform/              # 平台管理及配置
│   ├── webgw/                 # 对外入口边界，webhook/ 与 mcp/ 仅预留
│   └── manager/               # 会话受理、控制、事件查询与 Engine 分配
│       └── mcp/taskdelegation/ # 已实现的内部主 chat 委派适配
└── docs/                      # 本组件接口和执行分配设计
```

Platform 员工基础管理见[员工配置](docs/platform-agents.md)，只交付配置持久化与启停标志，不宣称运行层已接入。

Platform 模型配置与三协议并行诊断见[模型配置](docs/platform-models.md)；Engine 的 DeepSeek 真实会话和任务工具调用已通过专项验收，范围见实施计划。

Platform 已实现默认邮箱密码注册登录、可选邮箱验证码、会话、工作区成员邀请，见 [身份与邀请](docs/platform-auth.md) 与 [OpenAPI](contracts/openapi.yaml)。入口装配 PostgreSQL、SMTP、HTTP 和可选静态 Web；配置缺失时明确启动失败。Manager 及内部 task_delegation 已实现；webgw/webhook 和 webgw/mcp 仅预留外部协议边界，不代表渠道和对外 MCP 已接通。

以后由入口和 `app` 装配配置、仓储及客户端，模块按实际需求定义小接口；不为占位先造通用事件总线或业务模型。`webgw/webhook`、`webgw/mcp` 与管理端业务动作复用 `manager` 的业务用例，Engine 客户端归入 `manager` 的 integration 适配实现。跨包不直接修改其他模块拥有的业务状态；最终协议和数据写入约束继续以对应设计为准。Backend 不接管 Event Hub 的选举或全局 SQL 迁移。

## 模块命名与内部分层

业务表前缀按[数据库建模规则](../docs/rules/database.md#建模与查询)表达数据所有权：Platform 为 `platform_`，Manager 为 `manager_`；仍共用全局迁移集，不按前缀分库。

顶层模块为 `platform`、`manager`、`webgw`；职责见[系统模块设计](../docs/architecture/system-modules.md)。`manager` 承接此前顶层 `service` 的业务运行与 Engine 对接职责，源码已采用该名称；模块内部 service 表达业务用例。

各模块内部按需要使用以下分层，不强制每个模块都建立四个包，也不为占位创建空目录：

| 层 | 职责 | 边界 |
| --- | --- | --- |
| `controller` | HTTP、Webhook、MCP 等入口的解析、协议认证衔接、输入校验及响应映射 | 调用业务用例，不直接执行 SQL 或编排 Engine |
| `service` | 业务用例、业务授权、状态流转及事务协调 | 不依赖入口协议，不把 HTTP handler 或数据库驱动当业务接口 |
| `repository` | 数据查询、保存、条件更新和事务机制的持久化适配 | 不另定业务授权或状态机，不绕过所属模块写入边界 |
| `integration` | Engine、渠道及其他外部服务的客户端适配 | 不充当入站 controller，不重复实现业务规则 |

目前 Manager 已有 controller/service/repository 和内部 mcp/taskdelegation；integration 在需要主动调用 Engine 时再建立。首版通过持久分配和 Engine 轮询配合。`webgw/webhook`、`webgw/mcp` 作为外部协议适配复用 Manager 用例，没有独立存储需求就不重复建 repository。

依赖通过消费方所需的小接口表达，由 `app` 装配具体实现。controller 调用 service；service 定义或消费持久化及外部能力接口，repository/integration 提供实现，不反过来调用 controller。不同模块通过明确用例协作，不直接操作对方表或内部实现，也不互相导入形成循环。这里区分“调用方向”和“Go 导入方向”，不要求 service 导入具体 repository/integration 包。

平台管理动作需要推进运行状态时调用 manager 用例；manager 读取平台配置则依赖窄接口提供的配置能力，而非反向依赖平台 controller。必要时将共享契约放到明确归属的最小包，由 app 接线，不能以新建无边界 common 包掩盖循环依赖。app 负责启动和装配，不成为所有请求的数据中转层。

这些分层保持单进程、单 Go module，不新增 HTTP 跳转。未来拆进程时替换接口适配，并重新明确事务、幂等、身份传播和故障语义，不把本次命名约定视为已完成分布式拆分。

在本目录执行（最低 Go 1.26，与 Event Hub 一致；CI 固定 Go 1.27.2）：

```sh
make fmt                         # 显式格式化
make lint                        # 复用统一规则与契约验证
make check                       # lint + OpenAPI + 真实 PG race 测试，须 BACKEND_TEST_DATABASE_URL
make build VERSION=<版本或提交>   # 生成 bin/backend，不启动服务
```

当前使用 pgx PostgreSQL 驱动及 bcrypt，依赖由 go.mod/go.sum 锁定。CI 见 [Backend workflow](../.github/workflows/backend.yml)，与本地复用 `make check`。身份与工作区测试包含真实 PostgreSQL，需设置 BACKEND_TEST_DATABASE_URL；未设置时数据库测试明确 skip，不能称为完整业务通过。

契约验证默认复用 `../event-hub/.venv/bin/openapi-spec-validator`，可通过 `OPENAPI_VALIDATOR` 指定。根目录 `make lint COMPONENT=backend` 与 CI 使用同一入口；`make check` 在缺少隔离 PostgreSQL DSN 时明确失败，不以跳过数据库测试冒充通过。

开源本地默认允许无 SMTP 的密码注册，账号邮箱保持未验证，不能接受邮箱定向邀请。公开部署应先在受控网络完成首位管理员登录，再从「设置 → 平台管理 → 邮件与注册」配置 SMTP 并启用强制邮箱验证码注册。完整验证与抢注恢复边界见 [身份与邀请](docs/platform-auth.md#本地入门与邮箱验证策略)。

### 可扩展配置文件

新部署统一使用仓库根目录 `.env.example` 与本地 `.env`，运行 `python3 tools/local.py up` 由 Compose 启动 PostgreSQL、Event Hub 和包含 Web 的 Backend。旧 JSON 配置仅保留过渡读取，不再提供第二份示例。配置、迁移保护和公开部署边界见[启动配置](docs/configuration.md)。

### 功能迁移交付顺序

Backend 功能迁移（包括对应 Web 页面与接口联调）按以下顺序验收，不得以代码测试代替浏览器验收：

1. 完成实现后先运行受影响的单元、接口、真实隔离数据库、契约、静态检查及前端测试；失败先修复。
2. 自测通过后，按已授权的本地启动方式重启更新后的服务；涉及数据库升级先备份，仍由 Event Hub 执行版本化迁移并核对数据保留。未获本地重启授权时应说明等待项。
3. 使用真实浏览器验证用户动线、实际接口请求、保存回读、错误提示和权限边界。外部模型可用本地模拟提供方配合真实 Backend 与隔离数据库，不以 Mock 前端固定成功结果冒充联调；真实外部服务及凭证使用仍须在授权范围内。
4. 代码自测、本地重启与浏览器验收全部通过后，才进入已获授权的提交和推送；明确记录验收环境、结果及未覆盖项。受环境或权限阻塞时不得宣称通过。

本顺序补充本组件的迁移交付要求；通用测试规范继续以 [AI_RULES 测试与验收](../AI_RULES.md#测试与验收) 为准。

### 渠道群生命周期（2026-11-18）

Manager 在迁移 25 中持久化群/成员证据、核验租约和解散墓碑，统一入站/执行/历史及固定消息出站的群门禁。Platform 不写这些状态；Hub 仅提交可信事件和远程核验事实。接口、乱序和退群恢复边界见 [群生命周期](docs/channel-group-lifecycle.md)。

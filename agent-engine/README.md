# agent-engine · Agent 执行引擎

**Input 有序合批（2026-11-18，实施中，迁移 19/20）**：false 输入单独等待，不触发 Pi；Engine 到同 Session 首个 true 截批，合并前面 false 与该 true 为一条原生 user prompt。原始行保留，以 batch_input_id 关联触发根与上下文成员，receipt/状态按批次传播；触发根提供执行权限，不把历史作者权限混入当前 Turn。steer 不越过更早 true 或 queue；明确 absent 的 false 在非取消场景归还，unknown 不盲目重放。主会话/话题以独立 Session 隔离 Input、Pi 和批次，Backend 已同步独立话题映射，真实渠道端到端仍待验收。须停稳旧 Engine 后同代升级；验证范围及未完成项见[输入专项](../backend/docs/session-input-and-task-report.md#input-有序合批2026-11-18实施中迁移-20)。

**任务回报与输入方式（2026-11-17）**：默认 steer、可选 queue；Engine 在 task Turn 提交后主动回调 backend，Hub 仅定时补偿。运行中同主体输入通过原生 steering 加入当前 Turn，原生 prompt 承载带来源标记的内部回报，不新增或修改 Pi 扩展。详见[输入与回报](../backend/docs/session-input-and-task-report.md)。

**实施进度（2026-11-17）**：已接通注册/心跳、Backend 分配、chat/task 串行执行、可靠事件、停止和原生历史继续。使用未修改的 Pi 1.5.2，通过原生 `--mode rpc` 直连；runtime 管 Conversation Runner 和 Session Pi/builtin 窗口。chat 挂载 task_delegation，Web 固定私聊按输入挂载[人工确认 MCP](../backend/docs/human-requests.md#内部-mcp-接口)。task 支持自身 builtin、固定版本 Skill、免认证及通过 Backend 业务连接访问的远程 MCP，并通过 Pi 公开扩展钩子逐调用授权。实际范围和验收见[运行说明](../docs/operations/minimal-agent-runtime.md)。

开发启动：在仓库根目录设置 DATABASE_URL、KIKO_BACKEND_URL、KIKO_ENGINE_TOKEN（32 字节以上）、KIKO_REPO_ROOT，再运行 `go run ./cmd/agent-engine`（工作目录 agent-engine）。KIKO_STORAGE_LOCAL_ROOT 默认为 ~/.kiko，KIKO_ENGINE_HOST_ID 默认为宿主名，KIKO_ENGINE_CAPACITY 默认为 8。Backend 同时设置相同 KIKO_ENGINE_TOKEN 才开启分配。部署须将同一份支持跨宿主文件锁的 KIKO_STORAGE_LOCAL_ROOT 挂载给 Backend 与所有 Engine，宿主名用于诊断。

> **2026-11-16 关系与生命周期已确认**：一 Task 一 task Session，共享环境归 Conversation；创建、停止、归档、首版放置及三层回收按[业务会话与共享环境生命周期](../docs/architecture/conversation-lifecycle.md)。下文保留完整目标范围；当前最小实现与限制见仓库运行说明和实施计划。

**存储配置**　Pi 原生 state 使用持久 POSIX 目录，默认本地根 `~/.kiko/`，分配保持宿主亲和，不逐轮发布检查点。Skill 经授权 Backend 内部接口读取既有本地/S3 资产，在执行 Session 下物化并校验固定目录；Engine 不接收 S3 凭据，不引入统一存储 lib。配置归属见[统一存储配置](../docs/architecture/storage-configuration.md)。

Go 实现，可多实例部署，主动向 backend 注册、心跳和对账。backend 分配 Session；Engine 为每个归属自己的活跃 Session 启动一个专属 Pi RPC 进程，运行在 Engine 侧，与 Sandbox 分开。跨轮复用，无后台进程等活动时可释放；后续 Input 通过 Pi 原生 `--session` 加载并继续同一会话。

## 职责与边界

首版[裸 Sandbox](../sandbox-runner/docs/local-sandbox.md) 与 Engine 运行在同一台 Mac/Linux、同一账号环境中。Pi 与 Sandbox 分开指管理职责及进程边界。原生 RPC 已通过 Mac 与 Linux 验收。

- **会话与轮次**　创建 Session 必填 chat/task；chat 固定 Pi、创建时使用 AI 员工所选模型且关闭思考，不申请 Sandbox；task 使用 AI 员工任务配置及业务会话共享 Sandbox。同一 Session 串行，一条 Input 最多一个逻辑 Turn；只通过未修改 Pi 的原生 RPC 对接。
- **配置装配**　从 backend 获取当前获准能力包展开的固定资源与当前授权，保留包来源；插件可使用空间共享或员工专属连接，带凭据的调用通过 Backend 转发，但只开放当前获准包的工具，生成独立的 Pi 原生模型、角色指令、Skill 和 MCP 配置。task 生成参数跟随 Harness；chat 强制非思考策略及白名单扩展，不增加创建会话的覆盖参数。
- **后台进程与认证**　后台命令归执行 Session，可跨正常 Turn 存活；stop/归档收尾目标 Session，其他共享环境参与者不受影响。仍有后台进程时保留运行实例和环境引用。Sandbox MCP Token 生命周期固定，同 Session/同范围 builtin 与代理可共用，管理凭据独立。
- **环境生命周期**　Backend 登记唯一 Sandbox、宿主及管理 Engine，首版同业务会话 task 均分配到该管理 Engine，chat 独立。指定 Engine 执行创建及获准回收，Runner 管内部进程；环境管理职责不随单个 Session 释放解除。活跃 Conversation 不自动空闲回收，父归档全部停稳后释放运行资源，历史及工作文件保留。
- **MCP 接入**　把 builtin MCP、Runner 对每个 stdio 插件提供的 HTTP 端点，以及远程 HTTP MCP 分别写入 Harness 配置。Pi 直接调用目标 MCP，Engine 不实现 MCP Gateway。
- **人工确认整改**　原 Session 使用 Pi CLI 的受限配置继续原生历史。整改轮次只通过既有人工确认扩展开放职责查询和 `ask_human`，沿原停稳协议交给 Manager 等待新批准。
- **事项交接**　复用人工确认的 Pi 终止及停稳机制。Manager 按 Input 装配普通任务、接手任务及原任务验收工具。仅事项 MCP 的逐 Input 凭据变化不触发 Pi 重启；现有平台授权扩展通过 Pi 的公开 MCP 注册接口在本轮执行前替换相应客户端，Manager 仍核对请求原有的 Input 身份。模型、事项角色等稳定配置变化不绕过现有 Runner 停稳门禁。见[事项交接](../backend/docs/work-items.md)。
- **TaskSpace 执行历史**　仅 TaskSpace 的执行任务挂载 `task_space_history`，使用当前 Input 的专用凭据直连 Manager MCP。见 [TaskSpace 执行历史](../backend/docs/task-spaces.md#执行-agent-读取同空间任务)。
- **执行控制**　按 backend 登记的归属与代次领取 Input，响应持久停止边界；与 Runner 协调收尾，无法确认旧执行隔离时阻塞后续执行。backend 受理控制不等于执行已停止。
- **记录与恢复**　把 Pi 原生输出映射到[平铺会话事件](../backend/docs/session-events.md)，按实际功能实现预设计类型子集；可靠状态提交后推送，临时文本增量不持久化。Pi 持久化原生历史，Engine 保存身份映射，Engine 保留目录并提交 Turn、Input 和可靠事件；故障中断旧 Turn，后续新 Input 加载原会话，不做逐轮检查点或自动续跑。
- **依赖与授权**　领取时重验主体及配置权限，Agent 停用拒绝未开始输入。模型必需依赖有限安全重试，单插件失败由 Harness 决定是否继续，不自动重放未知副作用或换账号。

chat 的后台压缩与仅追加 assistant 而不触发回复属于待评估设计，新增扩展或特殊适配须先取得用户明确确认，详见[对话与任务及扩展](docs/chat-and-task.md)。

原生集成遵守 [Pi 原生能力优先规则](../AI_RULES.md#通用开发与静态检查)；既有授权和输入回执扩展保留原用途，不因迁移新增插件。方案与运行时接入见[Session 运行时与 Pi RPC 接入](docs/session-runtime.md)。

检查入口：`npm ci` 安装固定依赖；`make check build` 运行格式/契约/lint、扩展测试、实际 Pi/MCP race 回归和构建。需要先安装 Runner builtin 依赖并构建，契约校验器按 Event Hub 的开发依赖安装。

详细方案见[Session 运行时与 Pi RPC 接入](docs/session-runtime.md)，分配协议见[backend 注册与调度](../backend/docs/engine-dispatch.md)，环境控制见[Runner 设计](../sandbox-runner/docs/runtime-and-mcp.md)。builtin MCP 保留 TS，包含六个 Pi 文件工具和五个 Bash 工具，复用与 HTTP 接入方案见[内置工具设计](../sandbox-runner/docs/builtin-mcp.md)；它仍在 Sandbox 中运行。

**数据库启动行为**　PostgreSQL 迁移由 [event-hub](../event-hub/README.md#启动与数据库迁移) 统一执行。Engine 不执行迁移、不检查结构版本、不等待迁移才启动；受影响的注册、请求或任务暂时失败，结构补齐后后续访问可恢复，不形成永久失效的初始化状态。

**范围说明**　本期按[会话底座需求](../docs/product/agent-sessions.md)和[生命周期](../docs/architecture/conversation-lifecycle.md)设计可信 Conversation 归属及一 Task 一 task Session，首版不建 Run。主 chat 经 Backend [task_delegation MCP](../backend/docs/task-delegation-mcp.md)创建、查询、追加及停止 task 会话的模块设计已定，Engine 只负责原生配置挂载和执行，不接管委派业务。Chat 和 Task 的原生 Pi 确认及释放后续跑见[原生确认与进程释放](docs/session-runtime.md#原生确认与进程释放)。跨员工委派和 File/Artifact API 仍待实现。

## 部署与退出

本地 `./start.sh` 会用仓库中的 Dockerfile 构建 Engine，挂载与 Backend 相同的持久目录，并通过 Compose 内部网络注册。Engine 的 `/healthz` 反映进程存活，`/readyz` 在 Backend 确认注册并接收心跳后返回 204。启动器等待后者就绪，再报告完整服务已启动。

发布镜像 `ghcr.io/unrealm-ai/kikoteam/agent-engine` 同时打包 Pi、Go Runner、builtin MCP 及其原生依赖。容器以 UID 1000 运行，按会话在容器内启动子进程。提供 `DATABASE_URL`、容器可访问的 `KIKO_BACKEND_URL` 和与 Backend 相同的 `KIKO_ENGINE_TOKEN`。为每个实例设置稳定且唯一的 `KIKO_ENGINE_HOST_ID`，将该实例可写的持久卷挂到 `/var/lib/kiko`，以便重建容器后继续使用原生历史和工作目录。构建上下文为仓库根目录，本地使用 `docker build -f agent-engine/Dockerfile -t kiko-agent-engine .`。镜像标签和发布入口见[仓库镜像说明](../README.md#容器镜像)。

发布固定 Pi RPC 版本与工件摘要，核验原生会话创建/加载、prompt/abort 和 MCP 配置兼容性。每次 Engine 进程重启使用新 engine_id，完成对账后接单；注册的是单实例地址，不是负载均衡服务地址。

正常退出先向 backend 报告不接受新分配，再收尾已有执行、保留 Pi 原生目录及映射并确认释放。失联后不能凭新心跳复活旧归属，未知旧执行先隔离。进程释放、Sandbox 释放、Turn 终结和 Session 归档分别处理。

**验收场景**　重复分配只启动一个 Session worker；执行事实按 assignment/generation 写入，原生上下文不可用不创建空白替代；停止未确认前不领取下一条 Input。已执行范围和未验证项见实施计划。

### Input 消费链路日志

`input.claimed` 在平台 Claim 事务成功后输出 input/session/turn ID、序号、主体类型及 `consumption_mode=turn|steer`；`queue_wait_ms` 为该输入创建至此次领取的耗时（重新领取时包含此前等待/尝试，不是纯排队性能统计）。此事件只证明平台领取，不证明 Pi 已提交、模型已采用或外发成功；false 上下文合批沿既有 batch_input_id 与触发根关联，不分别制造 Turn。`turn.finished` 仅在终态提交成功后记录并携带根 input_id；steer 的 `input.steer_reconciled` 记录持久回执事实，明确归还队列后另记 `input.requeued`。日志不含消息正文、渠道用户身份或凭据。普通 queue 后续输入由下一次 Claim 记录，重放/未知保持既有执行语义。

裸 Sandbox 执行内置表格和演示文稿 Skill 时，发布镜像提供 `openpyxl`、pandas、LibreOffice、`markitdown[pptx]`、`pptxgenjs` 和中文字体。直接在宿主机启动 Engine 时，应在其 PATH 上安装相同的 Python 和 LibreOffice 依赖，并将 NODE_PATH 指向运行过 `npm ci` 的 agent-engine/node_modules 绝对路径。

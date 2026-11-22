# 运行时、沙箱与接管

> **2026-11-16 关系与生命周期已确认**：一 Task 一 task Session，共享环境归 Conversation；创建、停止、归档、首版放置及三层回收按[业务会话与共享环境生命周期](conversation-lifecycle.md)。下文保留完整目标范围；当前最小实现与限制见仓库运行说明和实施计划。

backend 负责 Engine 注册、心跳和 Session 分配。Engine 在自己这一侧为每个活跃 Session 启动专属 Pi RPC 进程，跨 Turn 复用；Sandbox 内运行常驻 sandbox-runner，负责配置获取、环境管理和 stdio MCP 代理。当前产品边界见[会话底座需求](../product/agent-sessions.md)。

对象关系和生命周期以[专项设计](conversation-lifecycle.md)为准：Backend 同事务创建 Task 与唯一 task Session，首次任务为 Conversation 登记唯一环境和管理 Engine，首版该会话所有 task 固定到此 Engine/宿主，chat 可独立分配。管理 Engine 执行创建/释放，Runner 负责环境内事实；释放某个 Session 分配不解除环境管理职责。

## 详细设计入口

| 主题 | 权威设计 |
| --- | --- |
| 注册、对账、容量、分配事务、周期维护和配置领取 | [backend：Engine 注册与 Session 分配](../../backend/docs/engine-dispatch.md) |
| 原生配置、RPC、进程复用、事件、原生历史与停止 | [Engine：Session 运行时](../../agent-engine/docs/session-runtime.md) |
| 常驻管理、配置准备、stdio 代理和管理接口 | [Runner：运行时与 MCP](../../sandbox-runner/docs/runtime-and-mcp.md) |
| Mac/Linux 本机项目目录、进程、依赖和容器扩展边界 | [裸 Sandbox](../../sandbox-runner/docs/local-sandbox.md) |
| 六个文件工具、五个 Bash 工具、TS 复用与 HTTP 接入 | [builtin MCP：工具与运行记录](../../sandbox-runner/docs/builtin-mcp.md) |

上述为详细技术设计，尚未实现或完成联调。仅使用未修改的上游 Pi RPC；builtin MCP 采用 TS 独立包及子进程，Runner / Gateway 仍用 Go；具体工具与本地执行证据由专项设计维护。

chat 不申请 Sandbox 或启动 Runner，固定 Pi 与非思考平台模型，并通过平台扩展实现后台压缩和静默 assistant 追加，见[对话与任务设计](../../agent-engine/docs/chat-and-task.md)。下述环境创建、Runner 准备和回收仅适用于 task；chat 仍需 Engine 侧持久原生历史及 Engine 身份映射，不因此开放本地执行工具。

## 业务会话共享沙箱的两种部署

| 后端 | 工作目录与 Runner | 找回环境时核对什么 |
| --- | --- | --- |
| 裸目录（首版目标） | Mac/Linux 的 Engine 在本机启动同账号 Runner 子进程；任务以固定 project 目录为 cwd，不需要独立 Sandbox 服务 | 共享存储上的稳定项目/state、实例和 Runner boot；失联或目录缺失时不以新空目录代替 |
| 容器级（仅扩展设计） | 以后由环境实现接入容器或外部 Sandbox 系统；本期不做供应、镜像和调度 | 后续单独定义容器、工作盘及隔离边界，不改变会话与 MCP 关系 |

Agent 和 Session 不接受模板、镜像、挂载等特殊配置。首版使用本机工具链、同账号普通权限；Pi、Runner 和命令在同一个操作系统环境，组件边界不等于安全隔离。builtin 已允许 Mac/Linux，Mac 的实际执行链含 pipe/PTY 已验证，Linux 本批结果待 CI 单独记录。Web 私聊通过 Backend 浏览、上传和下载当前项目的文件，task 以相对路径读取附件。见[私聊项目文件](../../backend/docs/conversation-files.md)。

## 一轮执行的边界

| 阶段 | 行为 |
| --- | --- |
| 分配 | backend 在短事务中登记唯一归属与执行代次、预留容量；提交后通知 Engine，扫描补偿丢失通知 |
| 准备 | Engine 与 task 的 Runner 分别从 backend 领取所需配置及当前授权；chat 使用平台预设，task 使用 AI 员工快照和所属业务会话的共享环境 |
| 建立运行时 | 仅 task 的 Runner 启动 builtin MCP / stdio 插件并报告端点；Engine 生成原生配置，启动或加载 Pi RPC 会话 |
| 领取 | Engine 校验 backend 登记的归属、代次和执行门禁，原子领取未撤回 Input；同一执行 Session 最多一个活跃 Turn，不把业务会话内多个任务强制串行 |
| 使用 MCP | Pi 直连 builtin MCP、Runner 的 stdio 代理端点及远程 HTTP MCP；Engine 不转发工具调用 |
| 收尾 | 排空本轮 RPC 并提交 Turn、Input 和可靠事件；Pi 自行持久化原生历史，已确认后台进程可跨正常 Turn 保留；本期不要求导出产物 |

backend 管理员工插件账号映射及 PluginAuth。员工只通过能力包获得技能和插件操作，同员工多包同插件共用该员工账号，各包工具范围可不同；认证材料只发给当前获准的资源消费者。按消费者提供认证材料：模型和远程 HTTP MCP 配置交给 Harness，CLI / stdio 插件配置交给 Runner。长期凭据不写入 Skill、提示词、快照或普通日志。账号刷新不改变插件版本，换账号或服务目标须新建授权。

平台在资源领取和轮次准入时核验当前主体；Runner 代理核验会话、实例、代次及获准绑定，远程直连由目标 MCP 验证认证与权限。撤权通过配置拒绝、运行时通知、关闭连接及凭据撤销传播，不声称所有远程调用都经过平台同步拦截。普通 Agent 停用拒绝尚未开始输入，不强制取消当前轮，重新启用也不恢复旧队列。

## 停止、撤回与归档

撤回只作用于未领取 Input，与开始处理竞争时只能一方成功。每次 stop 都覆盖本次受理时已有工作，不要求 key；后续新增输入不受这一次停止影响，但须等待当前轮安全收尾。再次 stop 含网络重试会覆盖其间新增工作，无状态变化时不重复建立停止流程。

Engine 读取停止边界，取消 Pi prompt 并通过 Runner 管理接口关闭目标执行 Session 的窗口、收尾其后台进程；无活动 Turn 也要处理，不影响共享 Sandbox 内其他 Session。远程 HTTP MCP 不受 Runner 管理，断开连接不证明远端副作用已停止。旧执行无法确认隔离时保持 blocked，不能用清空活动指针替代实际停稳。

停止和归档直接返回 Session，不建立控制资源。一次 Input 最多一个逻辑 Turn；进程故障中断旧 Turn，用户用新 Input 经 `--session` 加载并继续原会话，平台不自动续跑旧 Turn。执行 Session 归档立即关闭自己的新输入并异步收尾；不清理仍被其他任务使用的共享环境，历史保持可查，不提供取消归档或删除操作。

Session 的 idle 仅表示无活动 Turn、有效队列或未决调用，后台活动以 background_process_count 单独展示；未知时为 null 并 blocked。首版有后台进程时保留所属运行实例及环境引用。Sandbox MCP Token 按环境生命周期固定，同 Session/同范围入口可共用，不与管理或配置领取凭据共用；窗口、代次和当前权限检查独立生效。

## 故障落到哪里

停止/归档主 chat 不级联任务，Task 操作只收尾自己的 Session。Conversation 归档先关闭全会话准入，再归档所有关联 Session；Backend 核验全部停稳后授权管理 Engine 回收环境运行资源。活跃 Conversation 不自动空闲回收，工作文件和 Pi 原生历史保留，单个 Session 归档也不删除环境。

- **通知丢失。** backend 从持久待办补偿，重复准备沿用原分配身份，不重复启动进程。
- **创建环境响应丢失。** 按原环境和创建动作身份核对，不能换新身份直接创建第二个环境。
- **prompt 或 MCP 请求结果未知。** 保留原运行及可用协议证据，不把传输重连当作业务重试；builtin 使用本地操作记录及原进程句柄核对，第三方 MCP 依其自身能力；没有可靠关联时不猜测成功或自动重放。
- **Engine 失联。** backend 标记失联并逐会话核对，确认旧执行隔离后才重新分配并增加代次；新 Engine 不凭数据库写入隔离就开始并发执行。
- **原生上下文加载失败。** 明确报告恢复失败，不能新建空会话或用公开消息拼出近似历史继续运行。
- **工作目录丢失。** 如实报告环境不可恢复；加载 Pi 原生历史不重建业务文件。
- **授权撤销或依赖故障。** 关闭受影响能力，保留已完成记录；模型必需依赖有限安全重试，单插件失败由 Harness 决定是否继续。
- **控制与完成竞争。** 已提交终态不改写；停止边界、Turn 和 Session 保留实际事实。

**联调验收**：多 backend 并发分配、丢失唤醒、跨代次迟到写入、Pi 原生恢复、Runner 自启动配置领取及 stdio MCP 代理均需实现验证。builtin MCP 的 11 个工具、Mac/Linux 进程管理、HTTP 身份绑定和完整活动采样按[专项验收](../../sandbox-runner/docs/builtin-mcp.md#8-移植与验收顺序)执行，已有 Linux 结果只作为对应平台的基线。

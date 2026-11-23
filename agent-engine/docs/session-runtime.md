# Session 运行时与 Pi 原生 RPC 接入

**输入调度增量（2026-11-17，已接入，验收范围见专项设计）**：默认 steer 输入可在安全边界加入活动 Turn，queue 继续按后续轮次执行；任务结果回报也使用 steer，空闲主 chat 自动唤醒。单 Session 单活动 Turn 保持，一轮可消费多条输入，见[输入、回报与原生交付设计](../../backend/docs/session-input-and-task-report.md)。下文逐条领取的实现已切换，存量输入保留 queue，普通 prompt 不能直接视为引导能力。

> **2026-11-16 关系与生命周期已确认**：一 Task 一 task Session，共享环境归 Conversation；创建、停止、归档、首版放置及三层回收按[业务会话与共享环境生命周期](../../docs/architecture/conversation-lifecycle.md)。下文保留完整目标范围；当前最小实现与限制见仓库运行说明和实施计划。

状态：2026-11-17 最小 chat/task、固定 MCP Token、后台驻留、原生历史继续与查询式委派已实现并完成本地实际进程及 DeepSeek 验收。下文保留完整目标；实际机器契约、目录和未实现范围见[运行说明](../../docs/operations/minimal-agent-runtime.md)，公开接口见[会话接口](../../backend/docs/session-api.md)。

后续已实现能力包、Skill 与静态认证远程 MCP 执行：本机目录经 Backend 授权读取和摘要校验，Pi 使用原生 Skill/MCP 及公开策略扩展。以下 stdio、热更新等完整目标不因此全部视为已交付。

## 1. 组件与进程边界

首版执行环境按[裸 Sandbox](../../sandbox-runner/docs/local-sandbox.md)：Engine、Pi、Runner 和工具同在一台 Mac/Linux、同账号运行，无安全隔离。Engine 对环境的操作仅为准备固定项目目录、启动 Runner、读取状态和停止/释放；本期不接独立 Sandbox 系统。Pi 与工具保持不同职责，但不存在容器或命名空间边界。

backend 分配 Session 并登记执行代次；Engine 为每个活跃 Session 直接启动一个专属 `pi --mode rpc --session <file>` 进程，跨 Turn 复用，不能放进 Sandbox。Engine 的 Go 客户端使用公开 JSONL RPC，不再启动 pi-acp。

Sandbox 内的常驻进程是 sandbox-runner。它向 backend 获取配置，准备环境并启动 builtin MCP / 本地 stdio MCP。Pi 直接连接 builtin MCP、Runner 的 stdio MCP 代理端点以及获准的远程 HTTP MCP；Engine 不实现 MCP Gateway，也不成为每次工具调用的中转站。

创建空 Session 不启动进程。chat 固定 Pi、创建时使用 AI 员工所选模型并关闭思考，不创建 Sandbox；task 使用 AI 员工快照及业务会话共享 Sandbox，不开放特殊模板参数。进程驻留、Turn 结束、Sandbox 回收和 Session 归档分别管理。

Conversation、Task 和 Session 均由 Backend 创建，首版一 Task 一 task Session；追加或故障后继续不更换它们。Backend 登记 Conversation 的唯一环境及管理 Engine，首版该会话所有 task Session 固定到此 Engine/宿主，chat 独立分配。管理 Engine 只按授权创建/释放环境，其责任不随某个 Session 结束而解除，见[生命周期](../../docs/architecture/conversation-lifecycle.md)。

模式与扩展的新增要求见[对话与任务 Session](chat-and-task.md)：chat 背景压缩与静默 assistant 追加不能被当作普通 prompt；当前仍待能力通道与一致性验收。

## 2. 上游版本与使用约束

使用固定的 [Pi v1.5.2](https://github.com/earendil-works/pi/releases/tag/v1.5.2)，不在启动时拉取 latest。旧 Pi ACP 0.0.34 只用于识别历史映射，不再是运行依赖。

遵守 [Pi 原生能力优先规则](../../AI_RULES.md#通用开发与静态检查)：未经用户明确确认，不改 Pi 源码、补丁/fork，不新增 Pi 插件、扩展或特殊桥接。Engine 负责公开协议通信、平台事件映射及旧数据迁移，不重写内部循环。原有 capability-policy.mjs 用于逐调用授权及 Skill 提示，context-receipt.mjs 用于输入回执；两者保留既有用途，本次未修改。官方 builtin:mcp 继续显式加载。 找人确认经用户确认增加 `human-tools.mjs`，使用公开工具注册和调用钩子结束执行段。Engine 在收到终态前核对输入门禁及所属 Runner，详见[找人确认](../../backend/docs/human-requests.md)。

| 原生能力 | Engine 处理 |
| --- | --- |
| CLI/RPC 和 get_state | 直接启动，核验原生身份及历史路径，无 ACP initialize/new/load |
| prompt response | 只表示受理；started 等待执行，handled 无需等待，意外 queued 拒绝并回收进程 |
| agent_settled | 在按序处理前序事件后，核对最后 assistant stopReason；agent_end 不代表最终结束 |
| steer / follow_up / 两种队列模式 / clear_queue | 客户端支持；不以 Pi 内存队列替代平台持久 Input 队列 |
| MCP、Skill、原生配置 | 继续按固定版本公开能力装配 |
| 任意角色批量消息、静默 assistant、后台压缩 | 原生 RPC 未提供相应接口；既有设计不代表已获准新增扩展，需另行明确确认 |

依据：[RPC 协议](https://github.com/earendil-works/pi/blob/v1.5.2/packages/coding-agent/docs/rpc.md)、[RPC 命令](https://github.com/earendil-works/pi/blob/v1.5.2/packages/coding-agent/docs/rpc-commands.md)、[原生配置](https://github.com/earendil-works/pi/blob/v1.5.2/packages/coding-agent/docs/configuration.md)。升级时重验这些公开契约。

## 3. 运行实例与生命周期

内部 harness_instance_id 标识一组真实进程；assignment_id / generation 标识 backend 授予的执行归属。平台 Session、Pi 原生 Session 与历史位置保存稳定映射，进程重建不改变会话身份。首版不建立同一 Turn 的多次恢复尝试或 execution_attempt 状态机；故障中断旧 Turn，后续 Input 创建自己的新 Turn。

| 阶段 | 行为 |
| --- | --- |
| preparing | 核验分配、隔离旧执行、领取配置、仅 task 准备 Sandbox，chat 只准备 Engine 侧原生资源 |
| ready | get_state 已核验原生会话就绪，可按会话队列领取输入 |
| busy | 单个 prompt 执行，保留活动 Input / Turn 的绑定 |
| closing | 等待本轮 RPC 排空、取消或停止收尾及可靠事件提交，禁止下一条 prompt；正常换轮可保留已确认的 Session 后台进程 |
| released | 已释放进程；原生上下文和公开历史仍保留 |
| unresolved | 无法确认旧执行、上下文或副作用，保留阻塞事实等待核对 |

这些是内部运行阶段，不替换公开 Turn 的 running / stopping / completed / failed / canceled / interrupted。Engine 注册和容量按[backend 分配设计](../../backend/docs/engine-dispatch.md)处理；每次重启先对账。

同一 assignment 的重复准备请求返回原运行实例。harness_instance_id 在启动子进程前登记；启动结果未知时检查原实例，不生成新身份再次启动。释放只在实际收尾后报告 backend，不能因 HTTP 超时假定已经释放。

## 4. 配置装配和内部目录

每个 Session 使用独立 HOME、原生配置目录、工作目录及环境变量白名单，不继承开发者账号、祖先项目指令或任意扩展。仅允许固定工件、配置声明的原生能力及平台白名单扩展；task 模型参数跟随 Harness，chat 强制非思考策略；保持原生 MCP 内置支持，不因关闭任意扩展而一并禁用它。

当前 Engine 将历史和身份映射保存在每个执行 Session 的 `context/native/`，位置独立于 Engine、assignment 和进程启动代次。Pi 配置和下载的 Skill 也在这个目录下生成。重构将主对话、任务的 Pi 状态统一到 `workspaces/<W>/sessions/<S>/pi/`，工作项目按独立的项目标识定位。迁移需要保留原生历史中的身份及文件映射。[存储目录方案](../../docs/architecture/storage-paths.md)

通过原生 settings.json、models.json、mcp.json、`--system-prompt` 及必要环境变量装配。认证材料按消费者最小范围提供，与持久历史分开。新会话的 sessions/*.jsonl 由 Pi 初始化和写入，Engine 在 session.json 保存 id、nativeId 与 sessionFile。仅首次创建空文件供 Pi 初始化持久头部，Engine 不写原生条目；旧会话只读 home/.pi/pi-acp/session-map.json 找到同一文件，并保留旧数据库身份为别名。历史缺失、损坏、身份不符或越界时拒绝加载，不创建空白替代。进程重启使用同一持久目录和有效路径映射，不能手改历史来伪造成功加载。

Backend 在获准的 HarnessConfig 响应中提供本轮指令。主 chat 组合员工当前名称、职责和岗位规范与平台交互规则。task 组合已固定的员工版本、平台执行规则及有效能力包摘要。包名和说明取自任务快照，摘要仅包含当前仍授权且至少有一个可用 Skill 或工具的包。Engine 将装配后的指令与输入来源规则写入私有文件，通过 Pi 原生 `--system-prompt` 用作系统提示词开头。升级时清理旧版本生成的 `APPEND_SYSTEM.md`，保留原生会话历史。任务要求作为原生 user prompt 提交。

Pi 的配置中每个 MCP 单独登记名称、HTTP 地址和当前认证：builtin MCP 直连 Sandbox 服务；stdio MCP 指向 Runner 对应 binding 的 HTTP 代理入口；远程 HTTP MCP 直连远端。不把第三方 stdio 的 command 写入 Engine 配置，否则进程会运行到 Engine。

主 chat 的平台预设挂载[task_delegation](../../backend/docs/task-delegation-mcp.md)，指向 Backend `/mcp/task-delegation`，凭据绑定来源 Session/Engine/assignment/generation，不复用 Sandbox Token 或 PluginAuth。Backend 每次从当前 started Input 核验主体和权限，写用例在事务内再次核验。Engine 通过原生配置挂载，不创建 chat Sandbox；task 不挂此工具集。该路径已通过真实供应商验收，自动回报/唤醒另行实现。

Sandbox MCP Token 在环境首次授权绑定时生成，跟随 Sandbox 实例生命周期，换 Turn、Pi 或 Runner 重启均不轮换；同一执行 Session、同一授权范围的 builtin 与 Runner MCP 代理可共用。业务会话共享 Sandbox 时，不同 Session/授权范围各有独立固定 Token，入口从凭据解析可信调用范围，不能按 URL 或模型字段冒充其他 Session。管理及配置领取凭据独立，远程 MCP 的第三方认证仍按 PluginAuth 管理。具体约束见[Runner 认证与停止](../../sandbox-runner/docs/runtime-and-mcp.md#5-身份认证与停止)。

以原生工具配置关闭本地文件/命令执行工具，配置只连接获准的 MCP。builtin MCP 提供[六个 Pi 文件工具和五个 Bash 工具](../../sandbox-runner/docs/builtin-mcp.md#3-工具清单)，由 TS 服务在 Sandbox 执行；原生 MCP 的工具展示方式按固定版本能力选择，不通过隐藏展示替代服务端授权。[Pi 工具配置](https://github.com/earendil-works/pi/blob/v1.5.2/packages/coding-agent/docs/settings.md#tools)

### Skill 资源与加载

**当前实现**　Skill 保持标准目录及 SKILL.md。Engine 经受限 Backend 接口下载并校验固定版本目录，通过原生 `--skill` 供 Pi 加载和显式 `/skill:name` 使用；首版本机 Runner/builtin 复用同一绝对路径，不重复下载一份资产。脚本依赖使用本机已有程序，不自动执行安装脚本；chat 不加载员工任务 Skill/Plugin。自动选择仍由 Harness / 模型决定，不新增 Input 字段或平台斜杠解析器。

当前 Pi 在禁用 read / bash 后不自动附加 Skill 目录提示。白名单扩展通过公开 before_agent_start 接口，复用 Pi 的已解析 Skill 元数据及 formatSkillsForPrompt，补入名称、描述和位置，并遵守原生 disable-model-invocation。正文按需读取。[上游提示生成](https://github.com/earendil-works/pi/blob/v1.5.2/packages/coding-agent/src/core/system-prompt.ts#L173)

当前 Pi 与本机工具共用 Engine 准备的同一绝对路径。任务命令在所属项目执行，引用 Skill 脚本时应按该版本的目录定位。重构后两个沙箱模式直接引用正式版本，结束 Session 级下载与复制。筛选、版本固定时机、装配、升级和撤权行为留待专项设计。[Skill 版本目录与沙箱](../../docs/architecture/storage-paths.md#skill-版本目录与沙箱)

## 5. 一轮执行与 RPC

1. 收到 backend 通知后按持久分配重读待办。通知只是提示，重复和丢失不改变 Input 身份。
2. preparing 期间核验归属、当前授权和环境。初始准备不能进行时，未领取输入保持 queued 或按准入规则 rejected；已领取后发生的故障按既有 Turn 记录失败，不能抹掉已开始事实。
3. Engine 直接启动 Pi RPC，用 `--session` 加载原生文件，get_state 验证身份、路径；加载失败不得退回空白会话。新会话由 Pi 初始化，平台和原生身份分别保存。
4. 按员工 → Conversation → Session 顺序核验父级准入、分配代次、门禁及 Input，原子设置 started、建立唯一 Turn。父会话已归档即停止领取，不能等待子级归档传播；已开始 Turn 仍可提交收尾事实。
5. 发送前在 session_runtime 保存当前 Turn、目标实例及最小投递阶段。对该实例只维持一个活动 prompt；后续 Input 留在数据库队列，不交给 Pi 的另一层队列。这些记录用于核对是否已发送，不用于自动重发或恢复旧 Turn。
6. Pi 原生消息/工具事件映射为正式消息和工具状态；底层模型子步骤不另建平台 Turn。客户端支持 steer/follow_up，但当前 Worker 仍逐条领取，业务多输入关联及回报尚未上线。
7. 收到 agent_settled 后核对最后 assistant 结果；stop 成功、error 失败、length 为截断失败、aborted 为取消；无最终结果不能报成功。处理完前序通知并核对没有未决调用；在当前代次事务中提交 Turn 终态、Input processed、可靠事件和 Session 运行概况，再处理下一条 Input。原生历史由 Pi 持久化，不增加检查点上传或数据库引用提交屏障。已确认的后台进程不阻止正常 Turn 结束。

投递阶段区分未发送、发送结果待确认、已取得执行证据和已结束。Pi RPC command ID 只关联连接内响应，不是 prompt 幂等键。写入管道或收到传输确认不证明模型完成；连接断开后不能盲目再次发送原 prompt。

模型或环境必需依赖采用有界安全重试；单个插件不可用由 Harness 决定是否继续。重试预算不因新建运行实例无限重置，也不重放未知副作用。不能只因长时间没有文本增量判定 Turn 失败。

### 原生确认与进程释放

收到活动 Turn 的原生 confirm 后，Engine 持久化平台请求，仅在当前 Pi 进程内保存与 RPC 请求 ID 的对应关系。等待期间暂停发送普通引导输入。匹配的答复优先领取，先将答复绑定到活动 Turn，再向 Pi 写入 `extension_ui_response`。写入结果不明时保留执行事实，避免重发。Pi 的超时按问题的 `expires_at` 裁决。

达到 Engine 的空闲时长且本 Session 后台已停稳、没有未决远程调用时，Engine 核对并退回明确未消费的引导消息，关闭 Pi，把旧 Turn 记为 `interrupted`、原因为 `awaiting_confirmation`。平台待答项保留。用户随后回答时，Engine 加载同一原生历史，用原问题及答案单独开启新 Turn，普通排队输入留至后续执行。原生对话框只能由原进程继续。

## 6. 事件、原生历史与故障后继续

公开事件与原生上下文分别保存。正式消息、工具状态和执行状态按[会话事件协议 v1](../../backend/docs/session-events.md)提交后推送：公共字段与专属字段平铺，不套 data / payload；核心输出为 agent.message、agent.thinking、agent.tool_use、agent.tool_result，user.message 由输入受理产生。type 是预设计目录，按实际功能实现所需子集，不要求每轮或每个版本都产生所有类型；message.delta 可临时发送。SSE 断开或慢客户端不会取消执行。启动加载和 get_state 不产生本轮模型输出，不能把读取的历史写成新一轮输出。

工具事件来自 Pi 原生信息，并在有可靠关联时补充目标 MCP 的证据。不假定所有工具经过 Runner；Pi 原生 toolCallId、公开 tool_use_id、HTTP/stdio 请求编号和插件内部操作身份分别关联，不直接互换。tool_execution_update 先合并已有记录，再发布同一 tool_use_id 的完整调用或结果快照；缺失参数或结果为 unavailable + null，无详情权限为 redacted + null。消息内容统一为 content，用户输入的排队序号为 input_seq；缺失用量不伪造零值，也不解析展示文本猜错误结果。消息块边界和原生 ID 关联按固定版本联调，不能把每个文本 chunk 都当成完整消息。

Skill 等不可变资产遵守[统一存储配置](../../docs/architecture/storage-configuration.md)。Pi 的可写原生历史使用持久 POSIX 目录，不向 S3 append，也不建设逐轮快照发布机制；部署选择 S3 保存资产不改变这项运行数据要求。所有 Engine 由部署统一挂载原生目录。接管复用原 Session 身份及已落盘的历史。

故障处理只做必要的收尾和原生加载：

1. 已确认执行进程故障时，将未结束 Turn 记为 interrupted、对应 Input 记为 processed，保留未知工具结果；不重新排入原 Input，不重发原 prompt。仅心跳超时还不能证明进程退出，先保留 blocked；后续获准 Engine 核对旧实例后可补记中断事实，不执行旧 Turn。
2. 用户提交“继续”或其他文本，作为普通新 Input。没有驻留进程时，Engine 先确认旧 Pi 已退出、旧请求已排空，再启动 Pi RPC，使用保存的原生文件路径执行 --session，随后为该 Input 执行新 Turn；不是空白新会话，也不解析专用“继续”命令。
3. 尚未开始的 queued Input 保留正常的串行和撤回规则；它们的首次执行不是恢复旧 Turn。没有待处理 Input 时不因中断主动启动模型。已受理的停止和归档仍优先收尾。
4. 历史或映射缺失、目录不可达、加载失败时明确报告 native_session_unavailable，不能新建空白上下文或从公开事件拼接替代。原生加载只恢复 Pi 已持久化的上下文，不保证恢复故障瞬间尚未落盘的输出，也不证明旧外部操作已停止。

平台保留 Session/Pi 身份映射、原生目录位置与访问条件及旧实例收尾事实；删除 checkpoint_ref、检查点包和同一 Turn 的恢复尝试设计。加载时的历史回放仅用于核验，不重复写 agent.message；新 Input / Turn 使用新的消息身份，不借 message.delta 的 segment_id 续跑旧 Turn。Pi 原生历史与 PostgreSQL 事件没有跨存储原子提交承诺，故障后的差异如实保留，不靠重放旧 prompt 补齐。

### 后台进程属于执行 Session

bash_start 返回 running 表示工具 RPC 已完成、命令仍存活。进程归创建它的执行 Session，origin_turn_id 保留来源；后续 Turn 可以 poll/write/terminate。业务会话共享 Sandbox 不共享进程控制权，Runner 按可信 Session 过滤和控制其账本，不把其他任务的进程计入本 Session。

正常 Turn 结束只要求本轮请求已排空、结果可判定，不等待已确认后台进程退出；同一会话仍只有一个活动 Turn。Session.execution 用 background_process_count 单独表达后台存活数，未知时为 null 并 blocked；idle 只表示没有活动 Turn 或排队 Input，不等于可以销毁环境。首版有后台进程时保留所属 Pi 运行实例、Runner 绑定及 Sandbox 引用，不做自动闲置回收；异常换实例先收尾旧进程，不实现跨实例接管后台命令。

## 7. 停止、撤权与回收

公开 stop 每次覆盖受理时已有工作，不要求 key；Engine 读取持久停止边界，拒绝旧队列继续开始，发送 clear_queue 和 abort，并等待停稳；取消后回收 Pi 进程，防止与迟到 prompt 前置检查竞争，并仅对 task 通过 Runner 管理接口关闭本执行 Session 的新执行、收尾其后台进程，即使 active_turn_id 为空也执行收尾。共享 Sandbox 内其他 Session 的工作不受影响。进程保留 origin_turn_id 及对应 Input.seq，停止按该来源核对范围；收尾确认前不启动边界后的 Input。取消不自动等于外部副作用回滚。

Runner 的门禁按执行 Session 绑定实例、代次和内部控制修订，与固定 MCP Token 分开。标准 MCP 请求不会自动携带平台 Turn ID；轮次关联由 Engine 当前运行窗口和 Runner 管理状态建立，builtin HTTP 入口在准入时固定可信上下文，不要求给 Pi 打补丁或发送原归档的私有 _meta。前一轮未确认排空时不能切换窗口，必要时关闭旧连接；固定 Token 不能隔离旧 Pi，新实例启动仍须确认旧 Pi 已退出。正常切轮保留已确认后台进程；stop 关闭窗口并收尾本 Session，完成后记录已应用停止边界，下一轮仍使用原 Token。

远程 HTTP MCP 不经过 Runner，停止 Pi/连接不能证明远端操作已停止。原执行结果未知则保留事实；只在确认旧执行不能与新轮重叠、环境安全后开启后续工作。否则 Session.execution.state 保持 blocked，即使 Turn 已记 interrupted 也不清除阻塞。

当前通过逐轮配置领取和白名单扩展的逐 MCP 调用重验传播撤权；每次调用包含只读目录探测与实际 schema 核验。获准集合变化后，在安全轮次边界重建 Pi 并关闭旧连接，继续原生历史和原 Session 快照；不实现热重载或后台推送。静态账号版本改变时旧 Session 不换账号。已获准却缺少可确认结果的远程调用保留审计并令 Session blocked，Runner 的空闲采样不能清除此状态。远端凭据刷新/撤销遵循提供方能力，不承诺瞬间收回已发凭据。

Pi 空闲释放要求没有活动 Turn、有效队列、本 Session 后台进程或未决调用；关闭连接、结束进程并报告释放，保留原生目录和 Engine 身份映射，环境管理职责不随该分配解除。主 chat 的停止/归档不影响任务，task Session 只收尾自身。

活跃 Conversation 的 Sandbox 不自动空闲回收，即使最后一个任务结束或归档也保留。父 Conversation 归档后，Backend 先关闭准入、推进全部子级收尾，核验停稳后授予管理 Engine 环境回收权限。Engine 调用 Runner 环境级 shutdown，关闭 MCP/插件及进程，再释放运行资源；工作目录和核对证据保留，未知活动不能报回收完成。

正常退出先向 Backend 报告不接新分配，再收尾 Pi 并处理自己管理的环境。服务停机需有序停止 Runner，但不删除活跃 Conversation 的环境登记或文件；重启后按共享存储上的执行权核对结果接管，不将停机当作业务归档或自动续跑旧 Turn。

## 8. Engine 内部管理面

前缀 `/api/internal/v1`，仅授权 Backend 调用。Session 操作携带 assignment_id/generation；环境操作携带 sandbox_instance_id、Backend 登记的管理 Engine 身份和管理修订，不借用已释放的 Session assignment。Engine 验证自己的 engine_id 并重读持久事实。

| 方法与路径 | operationId | 行为 |
| --- | --- | --- |
| POST /sessions/{session_id}/prepare | prepareSessionRuntime | 202，建立或复用原分配的准备过程；重复请求不再启动一组进程 |
| POST /sessions/{session_id}/wake | wakeSessionRuntime | 202，仅提示重读 Input / 停止边界 / 归档；不接受调用方提供任意 prompt 或终态 |
| GET /sessions/{session_id}/runtime | getSessionRuntime | 200，返回当前实例、阶段、代次、已确认环境和脱敏失败原因 |
| POST /sandbox-instances/{sandbox_instance_id}/wake | wakeSandboxRuntime | 202，只提示核对已登记的准备/回收意图；Backend 未授权回收不得关闭活跃环境，重复提示沿原身份处理 |
| GET /sandbox-instances/{sandbox_instance_id}/runtime | getManagedSandboxRuntime | 200，返回该管理 Engine 可核验的环境、Runner 和收尾事实，不以 HTTP 成功替代停稳 |

共用错误体；错误代次或身份冲突 409、无调用权限 403、未找到且可见 404、暂时依赖不可用 503。返回受理不等于 prompt 已执行。网络失败后查询原 runtime，通知可按原归属再次发出。

环境级通知及持久待办核对独立于 Session 唤醒；最后一个 Pi 分配已释放后，Backend 仍可通知管理 Engine 完成父归档后的环境收尾，不为清理空环境重新启动 Pi。创建中的环境收到归档时不得开放执行，沿原实例核对并清理已创建的运行资源。

## 9. 验收与未决集成项

- Mac/Linux 原生进程启动链均可运行，不强制 Docker；Runner 按需启动，项目/state 路径跨重启稳定。当前 builtin 的 Linux 限制、PTY/平台工件和本地启动入口需实际适配验证。

- 固定版本 Pi 原生 RPC 的新建/加载、prompt、abort 可用；禁用本地工具后 MCP 在 Sandbox 执行。
- 同一 Session 只启动一组进程、一个在途 prompt；重复通知、输入撤回和停止竞争不会重复执行。
- Skill 自动选择与 `/skill:name` 均可用；版本一致，路径不会落到 Engine 执行脚本。
- 模型错误不能因命令 ACK 或 agent_end 而误记完成；未知用量和工具结果如实表达。
- 取消、进程崩溃、失联接管、旧结果迟到及原生加载失败分别验收；旧执行未隔离不能开启新执行。
- 原生历史回放不重复写事件，可靠事件提交后才推送，空闲恢复保留已完成上下文。
- 两轮连续调用使用同一 Sandbox MCP Token；停止后再次执行不因换轮要求刷新 Token，其他 Session/管理接口不能凭该工具凭据访问。
- 第一轮启动后台命令后可以完成，第二轮可管理原进程；无活动 Turn 时 stop 仍收尾，其他 Session 的后台进程不受影响。
- 故障后新 Input 经 --session 加载继续，旧 Input/Turn 不重跑；目录丢失不创建空会话，首版不自动跨机搬运或接管后台命令。

builtin MCP 的工具、进程状态、SQLite 记录、HTTP 准入及停止证据见[专项设计](../../sandbox-runner/docs/builtin-mcp.md)。工具的内部 operation_id 不等于 Pi 原生 toolCallId 或公开 tool_use_id，也不承诺跨网络自动去重；Engine 不靠解析输出文本猜测关联。上述验收是实现目标，本次 RPC 验收范围和证据记录在迁移方案中，未实现目标不据此宣称完成；事件相关验收按本次实际启用的类型执行，不以完整预设计目录作为一次性交付清单。

# Engine 注册、Session 分配与运行配置

> **2026-11-16 关系与生命周期已确认**：一 Task 一 task Session，共享环境归 Conversation；创建、停止、归档、首版放置及三层回收按[业务会话与共享环境生命周期](../../docs/architecture/conversation-lifecycle.md)。下文保留完整目标范围；当前最小实现与限制见仓库运行说明和实施计划。

状态：2026-11-15 确认的职责划分与详细技术设计；尚未实现业务接口、建表或调度代码。公开 Session / Input / Turn 的 14 个操作保持不变，见[会话接口](session-api.md)。本文只定义 backend 提供的内部协作面。

backend 负责 Engine 注册、心跳、容量预留、Session 分配及失联核对。event-hub 负责数据库迁移和后续渠道连接、业务定时触发、结果投递，不参与 Session 分配。Backend 多副本都可即时调度，数据库裁决并发归属；周期维护另选单一执行者，不把 backend 整体变成单活。

## 1. 内部记录与身份

以下是[会话数据模型](../../docs/architecture/session-data-model.md)的补充，不新增公开产品资源。ID 采用 UUID，代次和序号在 JSON 中使用十进制字符串。

| 记录 | 关键字段 | 约束与写入方 |
| --- | --- | --- |
| agent_engines | engine_id、execution_scope、endpoint、runtime_version、capabilities、capacity、allocated_count、reported_count、availability、accepting、heartbeat_seq、last_seen_at、availability_revision | backend 保存；engine_id 对应一次进程启动，重启换 ID；endpoint 是可达的单实例地址，不能是随机负载均衡地址 |
| session_assignments | assignment_id、workspace_id、session_id、engine_id、generation、state、reserved_at、updated_at、ended_at? | backend 写入；同一 Session 最多一条未结束分配，generation 单调递增，历史分配保留 |
| session_runtime | bound_generation、active_turn_id?、execution_gate、harness_instance_id?、sandbox_instance_id?、原生目录/Engine 身份映射、后台活动、已应用停止边界与未决来源 | backend 创建中性初值，当前 Engine 推进执行事实；分配不能替 Engine 宣告 Turn 完成 |
| conversation_sandboxes | conversation_id、sandbox_instance_id、host_id、manager_engine_id、管理身份修订、环境代次及准备/收尾状态 | Backend 唯一登记；管理 Engine 执行创建/回收，职责独立于 Session assignment |
| sandbox_session_bindings | sandbox_instance_id、conversation_id、task_id、session_id、分配身份及授权范围 | Backend 登记参与关系，Runner 按 Session 核验；禁止 chat 或跨业务会话混用 |

execution_scope 是部署侧的执行池边界，不是用户可填写的 Sandbox 参数。capabilities 表达兼容的运行时、环境后端和协议能力。capacity 计量可驻留的会话运行实例；预留、准备、活跃、空闲驻留和释放中的分配均占槽位。reported_count 只用于对账，不覆盖事务维护的 allocated_count。

部署负责将同一份支持跨宿主 POSIX 文件锁的持久存储挂载到 Backend 和所有 Engine。host_id 用于诊断。调度依据引擎健康状态、容量和沙箱管理权选择实例，直接复用原项目与 Session 目录。

Engine 注册状态为 reconciling → ready；ready 可进入 draining 或 lost。失联后恢复通信先回到 reconciling，不能凭一条心跳直接恢复旧归属。accepting=false 阻止新分配，不自动中止已有轮次。缩小 capacity 到当前占用以下时停止新增预留，不驱逐已有会话。

assignment 状态为 reserved → active → releasing → released；异常可进入 unresolved。reserved 已占容量；unresolved 在执行未查清前也不能释放容量。released 终态不能复活。该状态与 Turn 状态分别管理。

## 2. 注册、心跳与对账

1. Engine 校验本地配置及 Pi 工件，生成 engine_id，启动内部监听，向 backend 注册。数据库暂不可用时可退避重试注册，但不接新执行；不检查结构版本，也不把暂时失败变成永久初始化失败。
2. backend 保存固定身份、地址、能力及容量，状态为 reconciling、accepting=false。注册响应下发维护参数和服务端观察版本。
3. Engine 报告本地准备中、驻留中、释放中实例及原分配身份，backend 与持久分配逐项核对。返回保留、停止、查证或继续准备的决策；未知旧执行保留阻塞事实。
4. 对账完成后 backend 将其置为 ready，Engine 上报 accepting=true，才进入候选池。
5. 心跳携带严格递增 heartbeat_seq。相同序号及内容可重复确认但不刷新 last_seen_at；旧序号拒绝，防止延迟请求伪造新鲜存活。时间判断使用 backend/数据库时钟。

注册和心跳均核验部署服务身份；调用指定 Engine 时核对响应中的 engine_id，防止旧 IP 被新进程复用。状态修订用于阻止失联前的迟到请求覆盖新决策。对账按稳定身份分批处理，不依赖一次无限大的请求。

心跳间隔、失联阈值、扫描间隔、本地自停预算属于部署参数，由 backend 下发并校验合理关系；本稿不照搬参考项目的固定秒数。本地联系 backend 失败达到预算后，Engine 停止新准入并收尾已有执行；超时本身不证明旧进程已经停止。

## 3. 分配事务与容量

### 业务会话的环境放置

首版环境为[Mac/Linux 裸 Sandbox](../../sandbox-runner/docs/local-sandbox.md)，host_id 表示 Engine 所在本机。创建动作只是建立/核对稳定 project/state 目录并启动同账号 Runner 子进程，不接容器或独立环境服务，不提供安全隔离。Backend 保存的身份、授权及放置只裁决业务关系，不构成操作系统隔离。容器扩展留后续。

首个 task 待执行时，Backend 为 Conversation 登记唯一沙箱和管理 Engine。同会话的 task 共用当前管理者。健康管理者容量已满时继续排队，chat 独立分配。

周期调度检查待处理任务的沙箱管理者。管理者停接或失联时，Backend 在事务内锁定原沙箱记录，核实交接条件，选择有容量的新引擎，更新管理者并递增 Session 分配代次。旧实例的迟到写入仍受当前归属校验。

Engine 在注册前锁住 `<root>/temp/runtime/<engine_instance_id>.lock`。Pi、Runner 和 builtin 继承文件描述符，Backend 接管时尝试获取同一文件的锁。Runner 发现父 Engine 退出会收尾，Pi 收到 RPC 输入关闭后退出。旧实例没有锁文件时，仅接受已停接、沙箱为 `stopped` 或 `preparing`、全部任务分配及工具活动已收尾的交接。状态不明时任务详情显示恢复原因。数据库加停止确认的接管协议归入[后续 OSS 改造](../../docs/architecture/storage-paths.md#7-验收与旧设计协调)。

### Session 分配

空 Session 先只保存配置快照；有未开始的排队 Input 时才分配模型执行，另可为已有停止/归档安排收尾。backend 在受理事务提交后发即时唤醒，周期扫描补偿丢失通知；不会因旧 Turn interrupted 就创建恢复任务或再次发送 prompt。后续 Input 经 Pi 原生 `--session` 加载 开启新 Turn。重复唤醒仅重读状态，不创建第二份输入、分配或 Pi 进程。

分配采用短事务，顺序为：

```text
BEGIN
  按全局顺序核验员工，锁 Conversation，再锁 Session，重读父子生命周期、待办和旧执行
  task 锁环境登记；首次申请通过 Conversation 唯一约束确定环境及管理者
  有健康且可复用的归属：返回原分配
  旧执行尚未隔离：保持阻塞，不建立第二份执行
  chat / 首次 task 选择匹配能力、原生目录和容量的 Engine；已有环境的 task 只选其管理 Engine
  锁定 Engine 行（竞争时可 SKIP LOCKED），重验状态修订和剩余容量
  条件增加 allocated_count
  原子登记必要的环境放置、参与关系及 assignment_id、engine_id、新 generation
COMMIT
通知登记地址上的 Engine 准备或继续执行
```

可自由选择时，候选按 allocated_count / capacity 升序，同等随机；已有环境的 task 不绕过管理 Engine 约束。父级锁、环境唯一约束、未结束分配唯一约束和容量条件更新共同防止双创建/双分配及超卖。复用已有归属不增加代次或容量；通知失败不回滚持久登记，后续沿原身份重试通知。

全局锁顺序为必要的 Agent → Conversation → Task（需要时）→ Session → 环境登记 → assignment → Engine 注册行 → runtime → Input → Turn → event，按[数据模型](../../docs/architecture/session-data-model.md#统一串行点)取共享或排他锁。父级归档先单独提交准入关闭；网络动作在事务外。心跳只锁 Engine 行，失联标记提交后再逐个核对环境和 Session，禁止持有 Engine 行反向锁父记录。

Engine 报告释放后，backend 核对原 assignment_id、generation 和已停止/隔离证据；仅首次将分配置为 released 时扣容量，重复报告返回原结果。超时和容量上报差异不能直接扣减。实际重新分配才提升 generation；数据库代次不代替旧进程和旧工具访问的隔离。

Turn 完成不等于可以释放：首版有本 Session 后台进程时保留运行实例、容量和共享 Sandbox 引用。stop 即使没有活动 Turn 也要唤醒归属 Engine 收尾目标进程；其他参与 Session 的工作不受影响。后台事实未知则保持阻塞，不能按 idle 或旧计数扣容量；Pi 原生目录不随 assignment 释放删除。

Pi 分配可在本 Session 无活动 Turn、队列、后台命令、在途或未决活动时释放，环境管理登记继续保留。活跃 Conversation 的 Sandbox 不自动空闲回收；Conversation 归档后 Backend 关闭准入、推进全部子级归档，核验所有参与者收尾后授权管理 Engine 回收运行资源。Runner 先关闭 MCP/进程，Engine 报告结果，Backend 才确认环境释放；工作目录、原生历史和核对证据不删除。

## 4. 即时调度与周期维护

每个 Backend 副本都有有界、可合并的即时唤醒队列。提交 Input、停止或归档后唤醒当前执行者；新分配仍经过同一事务入口。队列溢出只丢唤醒提示，不丢数据库中的业务事实。

周期维护在 Backend 实例间使用独立 PostgreSQL 会话级 advisory lock 选出一个执行者；锁键与 event-hub 选举、迁移锁区分。该锁只仲裁失联扫描、待分配补偿和释放核对，不保护全部即时分配。扫描持锁连接负责发现候选，实际 Session 变更仍由上述可并发、可重复的事务裁决；失锁停止本轮扫描，已提交的提示可以被安全重复处理。

backend 维护角色切换不批量重分配 Session。健康 Engine 的轮次继续执行；不把 event-hub 换主作为执行故障。event-hub 的迁移机制保持原归属，见[启动与数据库迁移](../../event-hub/README.md#启动与数据库迁移)。

没有容量时保留排队输入并退避，不抢占其他健康会话、不超卖。确定不可执行、准备预算耗尽与暂时繁忙分别记录原因；开始前拒绝 Input 与开始后 Turn 失败遵循已有状态机。

## 5. backend 内部操作

统一前缀为 `/api/internal/v1`。下表是待转写 OpenAPI 的设计，不代表接口已交付；无通用 Idempotency-Key 要求，按已有资源身份、序号和条件更新处理重试。

| 方法与路径 | operationId | 请求要点 | 成功结果 |
| --- | --- | --- | --- |
| POST /engines | registerEngine | engine_id、execution_scope、endpoint、runtime_version、capacity、capabilities | 201；同身份同固定信息重试 200，返回状态修订和维护参数；不刷新存活 |
| POST /engines/{engine_id}/heartbeat | heartbeatEngine | heartbeat_seq、availability_revision、accepting、reported_count | 200，返回当前状态及是否需要对账 |
| POST /engines/{engine_id}/reconcile | reconcileEngine | reconcile_seq、状态修订、有界本地实例清单及分页身份 | 200，返回逐实例决策；未完成完整核对前不进入 ready |
| POST /engines/{engine_id}/drain | drainEngine | 当前状态修订 | 200，停止新分配；已有执行结果另行收尾 |
| POST /sessions/{session_id}/assignments/{assignment_id}/release | releaseSessionAssignment | engine_id、generation、harness_instance_id?、释放/隔离事实 | 200，原分配当前状态；无充分证据时 409，不提前减容量 |
| GET /sessions/{session_id}/harness-config | getSessionHarnessConfig | 当前 Engine 服务身份；assignment_id、generation；执行时关联 input_id | 200，固定配置清单、当前授权状态及限范围认证材料 |
| GET /sandbox-instances/{sandbox_instance_id}/config | getSandboxInstanceConfig | Runner 启动凭据及 config_revision? | 200，Conversation 归属、管理身份、逐 Session 固定资源与授权、准备要求和限制 |

内部操作沿用错误体与追踪规范，常用失败包括 400 invalid_request、401 authentication_required、403 permission_denied、404 resource_not_found、409 engine_identity_conflict / stale_revision / stale_generation / assignment_unresolved、422 configuration_unusable、503 dependency_unavailable。可见性规则先于资源详情返回；业务暂不可用与鉴权失败分开。

注册固定信息冲突返回 409，不覆盖同 ID 的地址；更新容量等可变信息随心跳及修订核验细化。release 不含新控制资源，也不改变公开 stop 无 key 的规则。配置响应使用 Cache-Control: no-store，Token 不进普通日志、快照或事件。

## 6. 配置、授权与 Runner 启动

Session 创建必须明确 mode。chat 的有效 Harness 固定为 Pi、模型来自该 AI 员工配置并强制非思考，不沿用员工任务引擎，不签发 Runner 启动凭据或领取 Sandbox 配置；分配须匹配该 chat 预设和扩展能力，不要求 Sandbox 后端可用。扩展控制及上下文写入见[Engine 对话与任务设计](../../agent-engine/docs/chat-and-task.md)。

Engine 向 backend 领取配置；backend 根据 Session 快照固定 Agent、ModelProfile、能力包来源及包内 Skill / Plugin 版本，结合实际 Input 提交主体重验当前使用权限。空闲恢复先核验 Session 访问身份，开始下一条 Input 时必须核验该输入主体，不能始终借创建者权限执行。

仅 Backend 登记的管理 Engine 可以按固定 sandbox_instance_id 创建/找到本机目录并启动 Runner 子进程。Backend 核验父级准入、管理身份后提供限范围配置领取凭据，通过本机启动参数或受控配置注入；不增加远程环境供应方。重复准备复用原实例，父归档交错时关闭准入并核对已创建资源，不让迟到 ready 重新开放执行。启动载荷和环境管理接口仍需转为机器契约，容器供应方留后续扩展。

Runner 启动即从环境级 config 路由领取材料，不等待 Engine 另发首次准备通知。配置包含 employee_id、conversation_id、sandbox_instance_id/环境代次、管理 Engine/身份修订及获准 task_id/session_id 清单；每个 Session 分别装配固定版本和授权，不合并权限，不含模型凭据。启动和新增参与者均核验父级准入；归档后的状态核对/收尾仍允许，不再提供新执行授权。Engine 管理、Runner 配置领取和 Harness MCP 凭据分别限定用途。

Sandbox MCP Token 随环境实例生命周期固定，并按执行 Session/授权范围保存绑定；重复配置领取及 Pi/Runner 重启返回同一绑定凭据，不因 Turn 改变重签。授权撤销关闭使用范围，Sandbox 销毁使绑定失效；新环境重新生成。builtin 与 Runner 代理在同范围可共用工具 Token，管理及领取配置的凭据不能交给 Pi。远程 PluginAuth 认证刷新规则保持独立。

| 消费者 | 可以取得的配置与认证 |
| --- | --- |
| Engine / Pi | 模型、角色指令、Skill 资源、各 MCP 端点；远程 HTTP MCP 所需的当前认证；访问 Sandbox MCP 的限范围凭据 |
| Runner / 本地插件 | Sandbox 固定资源、CLI 和 stdio MCP 启动定义、对应 PluginAuth 认证材料及准备限制 |
| event-hub | 后续渠道和计划业务所需配置；不能因内部身份取得任意模型或 Sandbox 凭据 |

没有 Engine MCP Gateway。直接访问远程 MCP 时，平台通过配置授权、凭据管理及运行时撤权通知控制接入；远端是否已终止在途操作需其实际证据，不能承诺平台逐调用同步拦截。Runner 内 stdio 代理的接入检查与远端直连的边界见[Runner 设计](../../sandbox-runner/docs/runtime-and-mcp.md)。

操作范围由当前获准能力包解析，必须保留包来源并落实各包范围；员工插件账号只供认证，不授予工具能力。同员工多个包同插件共用该员工账号，各包可允许不同工具。远程直连应使用提供方可执行的账号权限或受限凭据；若目标服务不能落实需要的范围，则该接入不可用，不能靠提示词或仅隐藏工具名称宣称授权已经收窄。具体授权适配在 PluginAuth 契约中验证。

## 7. 验收与后续契约

- 两个 backend 并发分配同一 Session，只出现一个未结束分配和一次容量预留。
- 多 Session 竞争最后一个槽位不超卖；重复释放不重复减容量；失联未查清不释放占用。
- Engine 重启换 ID，旧地址、旧序号和旧状态修订不能复活旧分配。
- backend 即时提示丢失、维护角色切换后，持久待办仍能被发现；正常 Engine 不被批量迁移。
- 失联标记、注册对账和分配竞争不死锁，网络调用不占用数据库事务。
- 当前主体、实例或执行代次不匹配时拒绝配置领取；缺表时操作失败，结构补齐后可恢复。
- Pi 原生目录只在可访问的 Engine 加载，故障旧 Turn 不自动重跑；无排队输入时不因中断启动模型。
- 有后台进程的 Session 不因对话 idle 释放容量；无活动 Turn 的 stop 仍可收尾，同共享环境其他 Session 不受影响。

下一步将内部操作转写 OpenAPI，并补齐环境供应方适配和凭据签发契约。builtin MCP 的 TS 复用、11 个工具、HTTP 直连、可信上下文及私有记录见[内置工具设计](../../sandbox-runner/docs/builtin-mcp.md)，这些由 Sandbox 内服务实现，不移入 backend 分配逻辑。

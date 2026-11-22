# Agent 会话底座：数据模型与状态机

**归档准入（2026-11-17）**：首次归档要求目标 Session（Conversation 则包含主 chat 和全部关联任务）无活动轮次、排队输入、后台进程、停止未确认或未知本地活动，否则返回 409 `session_busy` / `conversation_busy` 且不修改状态。先停止并等收尾再归档。锁序、旧归档兼容与死锁缓解边界见[生命周期](conversation-lifecycle.md#停止与归档)。

> **2026-11-16 关系与生命周期已确认**：一 Task 一 task Session，共享环境归 Conversation；创建、停止、归档、首版放置及三层回收按[业务会话与共享环境生命周期](conversation-lifecycle.md)。下文保留完整目标范围；当前最小实现与限制见仓库运行说明和实施计划。

状态：2026-11-16 完整目标设计。Manager 的最小 Conversation/Task/Session/Input/Turn/事件、分配与环境表已在全局迁移 000010 实现，对外/内部 OpenAPI 归 Backend；具体取舍、实际表字段和未实现范围以[运行说明](../operations/minimal-agent-runtime.md)及迁移为准。能力包、授权、自动回报等后续内容不能据本稿认定已交付。

PostgreSQL 16 全局迁移由 event-hub 管理，Platform 配置资源复用已有表，Manager 查询代码归 Backend/Engine。选举、启动与迁移机制保持既有约定。接口组织见 [API 设计](../../backend/docs/http-api.md)，写入职责见[数据归属](data-ownership.md)。

**输入关系增量（2026-11-17，已接入，验收范围见专项设计）**：新增默认 steer / 可选 queue 及有可信来源的任务回报输入，一 Turn 可消费多条 Input，每条 Input 最多归属一个 Turn。起始 input_id 保留，全部消费输入由 inputs.turn_id 关联。详见[引导输入与任务轮次回报](../../backend/docs/session-input-and-task-report.md)；下文为旧基线，本次新增迁移 15。

## 1. 设计前提与不变量

1. Session 必须归属 Conversation：每个 Conversation 最多一个 chat Session；每个 Task 与一个 task Session 同事务创建且一对一。归属不可变，任务继续不新建 Session，不由客户端指定 Sandbox。
2. Agent 保存即生成不可变配置版本。新会话解析模型当前版本，Skill / Plugin 采用员工能力包固定的版本；已有会话的快照不被更新覆盖。
3. chat 固定 Pi、采用非思考平台预设且不申请 Sandbox；task 使用员工配置，生成参数交给 Harness，取得业务会话共享的 Mac/Linux 本机 project 目录及同账号 Runner，不做安全隔离；容器仅保留扩展。
4. 同一 Session 同时最多一个活跃 Turn。一条 Input 最多一个逻辑 Turn；进程故障中断旧 Turn，不自动恢复或重发。后续 Input 加载原生历史并创建自己的新 Turn。
5. Input 受理内容及顺序固定。撤回只针对尚未开始的输入；每次停止都按本次受理时已有工作确定范围，重试同样重新确定范围。
6. Session 归档只收尾自身；Conversation 归档先关闭全会话准入，再级联归档全部 Session。归档无逆向操作，允许已开始执行提交收尾事实；全部停稳后才回收环境运行资源，数据保留。
7. 当前 Engine 才能提交执行事实。执行代次隔离数据库写入，运行环境另需阻止旧进程继续工作，不能把数据库隔离当作外部操作已停止。
8. 配置快照只保存凭据引用。授权撤销、刷新和当前访问权限独立生效，不从历史快照恢复旧授权。

## 2. 类型、归属与版本约定

以下字段名是本稿的技术候选。`!` 表示非空；`?` 表示允许为空。标识采用应用生成的 UUID，具体生成库随实现锁定；时间使用 timestamptz；序号和版本使用正 bigint；状态使用 text 加 CHECK，后续枚举扩展通过迁移完成。

业务记录均携带 workspace_id，并以组合外键验证运行记录的工作区及会话归属。Agent、PluginAuth、Session 及执行记录的 workspace_id 非空。ModelProfile、Skill、Plugin 的 workspace_id 可空，空值专指平台预置资源，普通工作区调用方不能创建或修改平台资源；资源归属创建后不变。

平台资源可被授权引用，因此配置引用除外键存在性外，还必须校验“同工作区或获准的平台资源”。工作区 A 的私有资源不能被工作区 B 引用。平台资源权限与成员体系由身份模块定义，本稿不新增群或业务会话映射。

配置保存使用资源行锁和预期版本校验：验证引用 → 新增不可变版本 → 更新 current_version，均在同一事务完成，不要求通用幂等键或保存 HTTP 响应。并发从同一旧版本修改时一方成功，另一方返回版本冲突，不静默覆盖。保存成功但响应丢失时，旧预期版本的重试返回冲突，调用方重新读取确认，不自动换用新版本再次保存。

当前版本指针必须引用同一资源下的版本。若实现采用资源表与版本表相互引用，可用延迟检查的组合外键在同一创建事务内满足引用；版本内容禁止原地改写。主键、唯一和外键承担结构约束，跨表状态转换不能仅靠单行 CHECK 表达。[PostgreSQL 16 约束说明](https://www.postgresql.org/docs/16/ddl-constraints.html)

## 3. 配置数据模型

| 表族 | 关键字段与关系 | 写入方 |
| --- | --- | --- |
| agents / agent_versions | 资源：id、workspace_id、name、enabled、admission_epoch、current_version；版本：agent_id + version、职责简介、岗位指令、model_profile_id | backend |
| 员工能力包及 Agent 配置内包快照 | agent_id、package_id、包配置修订、启停及使用范围；工作区模板复制后独立，随 Agent 配置固定内容；包独立历史表方案待定 | backend |
| 包内 Skill 引用 | package_id、配置快照标识、skill_id、skill_version；不可绕过包直接关联 Agent | backend，随配置提交 |
| 包内 Plugin 引用 | package_id、配置快照标识、plugin_id、plugin_version、allowed_operations；工具范围属于包 | backend，随配置提交 |
| 员工插件账号映射及配置快照 | agent_id、plugin_id、plugin_auth_id?、settings；同员工同插件统一授权，供多个包引用，不作为能力授予入口 | backend，随配置提交 |
| model_profiles / model_profile_versions | 当前指针及版本中的 protocol、base_url、model_id、credential_ref?；验证时间、状态及原因另存，不改版本内容 | backend |
| skills / skill_versions | 资源元信息；版本中的 SKILL.md 名称、描述、目录包内部引用和摘要 | backend |
| plugins / plugin_versions | 资源元信息；版本中的 cli / mcp 类型、运行定义、接入参数定义、认证声明、能力说明和附带 Skill 目录包引用 | backend |
| agent_admission_changes | agent_id、retired_epoch、disabled_at、changed_by；每个失效受理代次唯一，记录停用的持久边界 | backend，随停用同事务提交 |
| chat_presets / chat_preset_versions | 平台内部运行预设的设计位置；聊天指令和平台内置能力独立于员工模型 | backend 的平台管理用例；不新增 AgentRole 产品资源 |
| plugin_auths | 见下文；身份和授权动态状态与插件版本分开 | backend / 授权适配器 |

每个版本表以 `(resource_id, version)` 唯一，版本号从 1 起。Agent 资源首次创建需要有效的完整配置引用，但外部服务暂不可达或 PluginAuth 尚待授权的状态单独呈现；受影响工具不能被标为可用，也不据此阻止整个会话创建。具体配置保存校验在相应契约中逐项声明。

员工当前插件账号映射对 `(agent_id, plugin_id)` 唯一，配置快照另记录所选账号引用；同一员工多个能力包使用同一插件时共用此授权，各包保存自己的工具范围。此映射只提供账号，不提供插件能力，缺少获准能力包时不可执行；不同员工不会因引用同一工作区插件而自动共用账号。plugin_auth_id 可空仅表示此绑定无需认证；要求认证却缺少授权时不能开始对应工具调用。普通非敏感接入值放在 settings，敏感值使用凭据引用，禁止混入明文。

PluginAuth 必须属于同一工作区并指向同一 Plugin，不能只验证授权 ID 存在。插件附带 Skill 由 plugin_version 固定，不自动复制成可独立编辑的 Skill。目录包引用是内部运行内容存储，不引入 File / Artifact 资源。

### PluginAuth 与认证流程

| 字段 | 类型 / 空值 | 语义 |
| --- | --- | --- |
| id、workspace_id、plugin_id | uuid ! | 固定资源归属 |
| name | text ! | 可修改的展示名称 |
| auth_method、target | text !、jsonb ! | 认证方法及服务、租户等授权目标 |
| external_account | jsonb ? | 认证成功后可取得的外部账号身份，不包含凭据 |
| requested_scopes、granted_scopes | jsonb !，默认空集合 | 请求与实际授予范围分别记录 |
| credential_ref | text ? | 受控凭据存储引用；不保存明文 Token 到普通业务 JSON |
| status | text ! | pending、authorizing、ready、reauth_required、revoked |
| auth_epoch、credential_generation | bigint !，初始 1 / 0 | 授权流程隔离代次及凭据更新代次 |
| expires_at、last_checked_at、last_error | 时间 / 脱敏说明，可空 | 过期、检查和失败事实 |
| authorized_by、授权与更新时间 | 主体引用及时间，可空或非空随状态 | 记录授权操作者与审计事实 |

每次发起 OAuth / CLI 登录建立内部 auth_attempt，绑定 PluginAuth、真实主体、目标摘要、auth_epoch、过期时间和流程状态。回调或登录完成时按同一代次比较更新；旧流程不能覆盖较新的授权或撤销。它是认证流程记录，不是新的用户管理资源。

| 触发 | 状态处理 |
| --- | --- |
| 创建待认证记录 | pending |
| 显式开始首次认证或重新授权 | 提升 auth_epoch，记录新流程，进入 authorizing |
| 同代次认证成功 | 保存凭据引用、外部身份及授权范围，进入 ready |
| 静态凭据验证成功 | 完成同样的身份和代次校验后进入 ready |
| 认证失败、过期且无法续期 | 进入 reauth_required，保留脱敏原因 |
| ready 状态自动刷新成功 | 比较当前 auth_epoch 和 credential_generation 后原子更新凭据代次；不生成插件版本 |
| 撤销 | 提升 auth_epoch，立即禁止平台后续使用，进入 revoked；供应商撤销结果另行记录，失败不恢复平台使用权 |
| revoked 后重新连接 | 只能通过新的显式授权操作进入 authorizing，不能被旧回调或刷新结果恢复 |
| 重新授权返回不同账号或目标 | 本次 auth_attempt 失败，不替换旧身份与凭据；同代次条件下按旧授权有效性恢复 ready / reauth_required / revoked |

网络不可达独立记为可用性检查失败，不据此判定凭据已失效。运行中的访问凭据不能靠修改数据库瞬间撤回，下一次工具授权及凭据发放须检查最新状态；第三方在途调用按实际结果记录。

已确认：更换外部账号、租户或服务目标时新建 PluginAuth，再更新 Agent 绑定并产生新版本；旧会话继续引用原授权。同账号和目标的刷新、密钥轮换及重新授权沿用原记录。首次授权允许填入未知 external_account，此后认证完成必须比较身份与目标；不一致时不得覆盖旧 credential_ref，返回账号不匹配并引导建立新授权。旧的有效授权状态不被失败的换账号尝试冒充或覆盖，流程错误记录在 auth_attempt。

### Agent 停用与依赖失败的执行边界

Agent 普通停用禁止新会话和新输入，将停用时尚未开始的输入拒绝执行，当前轮仍可收尾；不会归档会话或撤销已授予的工具权限。重新启用后旧会话仍使用原配置快照，旧排队输入不自动恢复。当前权限撤销独立生效，不能因允许普通停用收尾而绕过撤权。

为防止“停用后立即启用，后台尚未处理旧队列”导致旧输入重新执行，候选实现使用 agents.admission_epoch（从 1 起）：每次 enabled 从 true 变为 false 时递增；每条 Input 固定受理时的 agent_admission_epoch。重新启用不回退代次。queued 输入在当前 Agent 停用或代次已过期时，只能进入 rejected，错误为 agent_disabled。

停用事务同时写入 agent_admission_changes，记录被废止的旧代次与 disabled_at；重复停用不新增代次。旧输入的拒绝生效时间取其受理代次对应的 disabled_at，不使用异步核对时间，保证查询投影与最终落库的 finished_at 一致。

创建会话、追加输入和领取轮次，先对 Agent 行取共享行锁，核验启停及代次，再锁 Session。停用操作仅对 Agent 行取排他行锁并提交新代次，不在该事务内遍历锁住全部会话。backend 的持久化核对流程按会话补齐旧 queued 输入的 rejected 状态，当前 Engine 领取时也核验同一条件；两者均通过会话锁互斥。即使没有 Engine，backend 仍能完成拒绝记录。核对暂未完成时，查询按失效代次投影 rejected，不能对外显示为仍可执行的队列；可靠事件在相应状态正式提交时写入。

普通停用与领取并发时，先取得 Agent 锁并提交的操作确定边界：领取先提交则该轮可收尾，停用先提交则不得开始。backend 只补齐尚未开始输入的拒绝事实，不改写 started 或 Turn 的实际结果。

撤回、停止和归档也按 Agent → Session 加锁，但不要求 Agent 仍启用；先将失效代次 queued 输入补齐为 rejected，再处理剩余可取消输入。这样已经对外呈现拒绝的旧输入不会因核对延迟变成 canceled。单独的后台拒绝核对可依据不可变停用记录直接取得 Session 锁，不再反向锁 Agent。

必需模型或基础环境故障发生在领取后的 Turn 中，按可安全重试的步骤有限重试，耗尽后 failed；不得把临时网络失败直接批量记成 queued 输入的永久拒绝。失去会话或 Agent 使用权限时，尚未开始的 Input 进入 rejected。单个插件认证或可用性异常只禁止对应能力，报告给 Harness 决定是否继续；引用权限与第三方认证状态分别校验，不能静默扩大授权或换用其他账号。

## 4. Session 与不可变快照

### 业务归属与环境记录

下列为待实现的最小关系，不包含渠道路由、文件或任务结果协议。表名为语义简称，实际迁移按数据所有权命名；Manager 记录使用 `manager_` 前缀，既有 Platform 表不改写。

| 记录 | 必需身份与关系 | 写入及约束 |
| --- | --- | --- |
| conversations | id、workspace_id、employee_id、lifecycle、archived_at、持久子级收尾进度 | Backend 创建/归档；创建仅元数据；父归档立即阻止新任务、Session、Input 和领取 |
| tasks | id、workspace_id、conversation_id、目标/来源、唯一 task Session 关联 | Backend 与 Session 同事务创建；业务结果独立，归档以关联 Session 为一致事实，不另建一套执行状态机 |
| conversation_sandboxes | 环境实例 id、workspace_id、conversation_id、宿主、manager_engine_id、管理身份修订、环境代次及状态 | Backend 唯一登记当前环境；Engine 报告创建/收尾事实后由 Backend 核对推进，不能仅按超时新建替代 |
| sandbox_session_bindings | 环境实例、task_id、session_id、当前分配及授权/使用关系 | Backend 登记参与者，Runner 按该身份管理窗口和进程；chat 不建立绑定 |

首版同一 Conversation 的 task Session 分配到同一管理 Engine/宿主，容量不足排队；chat 可独立分配。环境管理身份独立于单个 Session assignment，释放最后一个 Pi 分配也不删除活跃 Conversation 的环境登记。创建、停止和三层回收见[生命周期正文](conversation-lifecycle.md)。

### Session 字段

| sessions 字段 | 类型 / 默认 | 约定 |
| --- | --- | --- |
| id、workspace_id、agent_id、created_by | uuid / 主体引用 ! | Agent 必须属于当前工作区 |
| conversation_id | uuid ! | 同工作区、同员工的业务会话；创建时固定 |
| task_id | uuid ? | task 必填且唯一，chat 必须为空；Task 必须属于同一 Conversation |
| mode | text ! | chat 或 task，不可修改 |
| title | text ? | active 时可编辑；归档后只读 |
| lifecycle | text !，active | active 或 archived |
| revision | bigint !，1 | 仅标题及归档等用户可编辑元数据变化时递增，执行事件不增加此值 |
| initial_input_id | uuid ? | 仅创建会话携带首条输入时设置，引用同会话 Input，之后不变 |
| last_input_seq、last_turn_seq、last_event_seq | bigint !，0 | 在会话事务锁下分配，仅作内部顺序计数 |
| stop_through_input_seq | bigint !，0 | backend 受理停止 / 归档时单调推进的内部输入边界，不超过 last_input_seq；不对外暴露为控制资源 |
| archived_at | 时间 ? | 归档时设置，不保存控制请求外键 |
| created_at、updated_at、last_activity_at | 时间 ! | 执行与用户活动的统计依据 |

每个 Session 对应一份 session_snapshot，创建时与 Session 原子保存，之后不可更新：

| 快照内容 | 解析规则 |
| --- | --- |
| agent_id、agent_version | 固定身份信息及来源版本；chat 不因记录该版本而加载 Agent 的任务配置 |
| chat_preset_version? | chat 必填，task 为空；chat 固定 Pi、非思考策略及白名单扩展版本/摘要，详见 Engine 模式设计 |
| model_profile_id、model_version | 新建 chat 与 task 均从 Agent 的模型引用解析当前版本，不从工作区或官方默认项兜底；已有会话快照保留 |
| resolved_manifest、摘要 | 复制实际指令、能力包来源及范围、包内固定 Skill / Plugin 版本、允许操作、接入参数和员工插件账号引用；不包含长期凭据或用户覆盖参数 |

解析 Agent / 平台预设当前版本及其模型 current_version 时，以同一 SQL 读取点形成版本选择。其余内容按不可变版本读取，再原子登记 Session。并发保存的新配置只影响读取点之后的新会话，旧会话不跟随指针变化。

同插件的运行配置可合并当前获准包的工具范围，但保留包来源及当前授权判定，不把所有已配置包的工具全部开放。包内同资源不同版本的冲突策略仍需按资源迁移设计确认，不默选最新。

归属引用和版本外键必须一致，manifest 是已解析执行材料，不能成为绕过版本和权限校验的任意 JSON。保存快照不授予持续权限，领取 Input 和调用工具时仍重验。

chat 的有效运行策略属于不可变快照，不是调用方自由填写的生成参数；后台压缩与静默追加的上下文修订、持久去重和原生写入待实现时细化，不能套用 Input 来触发模型，也不建立平台检查点发布流程。见[Engine 模式设计](../../agent-engine/docs/chat-and-task.md)。

Backend 从已核验的 Conversation 解析员工，task 创建时同时建立 Task 及关联，不允许客户端挂接其他任务或指定 Sandbox。运行状态从执行表读取，不用 lifecycle 表达“正在思考”或“某轮失败”。Task 业务完成、Turn 结束和环境回收是不同事实。

## 5. Input、Turn 与辅助执行记录

| inputs 字段 | 类型 / 默认 | 约定 |
| --- | --- | --- |
| id、workspace_id、session_id、submitted_by | uuid / 主体引用 ! | 真实调用主体独立记录 |
| seq | bigint ! | 同一 Session 内从 1 递增，不复用已取消位置；事件投影命名为 input_seq，与事件自身 seq 区分 |
| agent_admission_epoch | bigint ! | 受理时的 Agent 代次，停用后未开始输入不得自动恢复 |
| accepted_event_seq | bigint ! | 对应 user.message 的可靠事件序号，作为用户消息稳定历史顺序；内部列名不代表公开 type |
| text | text ! | 受理后不可修改，不解析平台专属斜杠命令 |
| status | text !，queued | queued、started、processed、canceled、rejected |
| rejection_code、cancel_reason | text ? | 区分撤回、停止、归档或无法执行 |
| received_at、started_at、finished_at | 时间，后两者可空 | 以实际转换记录 |

| turns 字段 | 类型 / 默认 | 约定 |
| --- | --- | --- |
| id、workspace_id、session_id、input_id | uuid ! | Input 与 Session 必须一致，input_id 全表唯一 |
| turn_no | bigint ! | 同一会话按实际开始顺序递增 |
| status | text !，running | running、stopping、completed、failed、canceled、interrupted |
| execution_generation | bigint ! | 开始该轮的执行代次，作为来源保留；故障后不改代次继续执行旧 Turn |
| result_summary、error_code、error_detail | text / 脱敏内容，可空 | 不把未知外部结果包装成确定失败或成功 |
| usage | jsonb ? | Harness 可提供的用量，未知不记为零 |
| started_at、finished_at | 时间，结束时间可空 | 终态不得改回活动状态 |

Input 的 processed 只表示对应 Turn 已终结，不表示执行成功；成功与否查询 Turn。没有开始的输入被取消或拒绝时不创建 Turn。实际处理开始后，即使模型启动或环境准备失败，也保留已创建 Turn 并记录 failed。

辅助表属于执行基础，不扩展产品资源清单：

| 表 | 内容与写入方 |
| --- | --- |
| agent_engines | Engine 进程身份、可达地址、状态、心跳及容量，由 backend 维护；字段与状态见[注册分配设计](../../backend/docs/engine-dispatch.md#1-内部记录与身份) |
| session_assignments | 当前 Engine、generation 和分配状态；backend 通过 Session 锁及容量条件更新写入，同一会话最多一条未结束分配 |
| session_runtime | 当前 active_turn_id?、bound_generation、execution_gate、harness_instance_id?、最小 prompt_delivery、unresolved_turn_id? / 原因、Pi 原生 Session 与原生目录映射、后台进程概况及 applied_stop_through_input_seq；backend 创建中性初值，当前 Engine 在代次校验后推进 |
| session_events | 正式历史及可靠状态事件，含完整助手消息、Input 快照、受控工具详情；backend 写受理与会话状态，Engine 写执行事实，均与对应状态同事务提交 |
| tool_calls | 工具调用身份、进度、脱敏详情和结果是否已知的持久投影；Engine 根据 Pi 原生事件写入，可靠关联时补充目标 MCP 证据 |
| session_request_dedup | 仅用于 Input 提交（含创建时首条输入）；请求指纹及原资源引用与输入写入一同提交，不缓存完整 HTTP 响应，不登记停止请求 |

tool_calls 是平台可查询投影，不是所有工具执行前的统一拦截记录。Pi 直连 builtin MCP、Runner 的 stdio 代理和远程 HTTP MCP；Engine 根据 Pi 调用身份建立稳定投影，不能将连接内 RPC 编号当作跨连接业务身份。builtin 的 Bash 操作与进程记录位于[本地私有 SQLite](../../sandbox-runner/docs/builtin-mcp.md#4-进程状态结果与持久化)，不在此预建统一工具执行表；本地 operation_id 不等于 Pi 调用身份，也不保证跨网络重试 exactly-once。

本期不建立独立控制表或助手消息正文表。停止边界保存在 sessions，执行是否收尾由 Turn 和 runtime 事实决定；正式助手正文作为 agent.message 保存在 session_events。用户输入正文继续以 Input 为权威，user.message 和 input.updated 保存同一 message_id=input_id 的不可变输入投影；text 转成 content，受理顺序转成 input_seq。tool_calls 用于运行时核对，公开身份统一为 tool_use_id，agent.tool_use / agent.tool_result 的完整快照提供对外历史，两者更新同事务完成；不要求把所有业务表改造成事件溯源系统。

不建立 execution_attempts 或 checkpoint_ref。runtime 的 prompt_delivery 仅区分未发送、发送待确认、已观察到执行、已结束，用于防止重发；Pi 原生 Session ID、原生目录及其可访问部署范围独立于当前进程实例保存。后台进程只汇总本执行 Session 的业务命令，不计 Pi、Runner 或常驻 MCP 服务；保存来源实例、采样修订、verified 和 background_process_count，无法核验时计数为 null。完整进程账本仍归 Runner/builtin，origin_turn_id 及其 Input.seq 用于停止范围核对。计数与可见状态变化同事务追加 session.updated。

applied_stop_through_input_seq 默认 0，由 Engine 在边界内 Turn、后台进程及在途调用均已收尾后单调推进，不超过 sessions.stop_through_input_seq。它是 runtime 的收尾事实，不是公开控制标识或请求去重记录。没有活动 Turn 也须处理尚未应用的停止边界；后续 Input 不能越过未完成收尾。没有启动过运行实例或已有可信的完全释放记录时，不为仅取消队列的 stop 启动 Pi；下一次准备核实没有遗留活动后同步已应用边界，再领取新 Input。

session_events 的核心字段为 workspace_id、session_id、seq、source_key、type、schema_version、input_id?、turn_id?、occurred_at、payload。payload 仅为数据库内部存放事件专属字段的列；序列化时与公共字段合并成平铺对象，不作为公开 JSON 的 data / payload 包装。具体字段见[会话事件协议 v1](../../backend/docs/session-events.md)，message_id、tool_use_id 在各类型的顶层稳定引用，复合归属约束仍校验关联 Input / Turn。工具详情改用 input / input_state、output / output_state，读取无权限时为 redacted + null。敏感凭据在持久化前去除，临时增量不写表、不消耗可靠 seq。type 是预设计目录，表设计不意味着必须先实现所有事件；按实际功能采用对应定义。

session_request_dedup 只保存 workspace_id、actor_id、operation_kind、target、key、request_fingerprint、session_id、input_id、accepted_at；唯一范围为前五项。operation_kind 仅取 initial_input、session_input，不引用 OpenAPI operationId。创建携带首条输入的 target 为固定 sessions 集合，向已有会话提交则为目标 Session；成功登记必须引用原 Session 和 Input。停止边界仍以 sessions 为准，停止请求不写此表。资源引用校验工作区及会话归属，普通 CRUD 不写此表。

### 必须落实的数据库约束

- sessions 增加 `(workspace_id, id)` 唯一键，关联 Agent 使用工作区组合外键。
- Session 的 `(workspace_id, conversation_id, agent_id)` 验证 Conversation 归属；chat 按 Conversation 建唯一部分约束，包含已归档主 Session，不能暗中创建替代。
- task_id 仅允许在 task Session 非空且唯一；Task 与 Session 的组合关联必须验证同工作区、同 Conversation，可用延迟组合外键在创建事务结束时校验完整一对一关系。
- conversation_sandboxes 对 Conversation 的未释放环境建唯一约束，准备中和结果未知也占用当前身份；sandbox_session_bindings 拒绝跨业务会话及 chat 绑定。
- inputs 的 `(workspace_id, session_id, seq)` 唯一；增加 `(workspace_id, session_id, id)` 唯一键供组合外键引用。
- turns 的 input_id 唯一，且 `(workspace_id, session_id, input_id)` 引用 inputs 对应组合键；turn_no 在会话内唯一。
- turns 对活动状态建立唯一部分索引，作为会话串行的最后一道约束。
- session_runtime.active_turn_id 引用同一工作区、同一会话的 Turn；未开始或已收尾时可空。
- session_assignments 对未结束分配建立同会话唯一约束，记录 generation 单调提升；容量预留与分配写入同事务完成，释放仅扣减一次。
- sessions.initial_input_id 仅引用同一会话 Input；创建时可用延迟外键完成原子关联，默认空会话不设置。revision 的并发校验和递增与标题修改或归档同事务完成。
- 版本引用、PluginAuth 的工作区及插件归属使用外键；业务使用权限由用例核验。
- 终态不可逆、内容不可变及跨表 Input / Turn 联动通过限定转换的事务写入口实现；实现时评审数据库角色权限或约束触发器，避免普通任意 UPDATE 绕过。

索引示意，不是已交付迁移：

```sql
CREATE UNIQUE INDEX turns_one_active_per_session
ON turns (workspace_id, session_id)
WHERE status IN ('running', 'stopping');

CREATE INDEX inputs_next_queued
ON inputs (workspace_id, session_id, seq)
WHERE status = 'queued';

CREATE UNIQUE INDEX events_sequence_per_session
ON session_events (workspace_id, session_id, seq);

CREATE UNIQUE INDEX events_source_per_session
ON session_events (workspace_id, session_id, source_key);
```

部分唯一索引用于约束同一会话的活动轮次，不能替代领取、停止与归档的事务互斥。[PostgreSQL 16 部分索引](https://www.postgresql.org/docs/16/indexes-partial.html)

## 6. 状态机

### Session 生命周期

| 当前状态 | 操作 | 结果 |
| --- | --- | --- |
| active | 提交 Input / 修改标题 | 维持 active，按权限受理 |
| active | stop | 维持 active，每次按当前 last_input_seq 原子推进停止边界并取消边界内有效 queued 输入；没有变化时不重复写入 |
| active | archive | 原子变为 archived，拒绝后续输入，同时记录停止范围 |
| archived | 输入 / 修改标题 | 拒绝；不隐式创建另一会话 |
| archived | stop（含重试） | 按当前归档状态拒绝，不回放旧停止结果 |
| archived | 查询历史或收尾结果 | 允许获准读取，运行时仍能提交原工作的收尾事实 |
| archived | 再次 archive | 返回当前 Session，不再次推进停止边界 |

没有 archived → active 或删除转换。实际是否仍有进程收尾由 runtime 和 Turn 表达，对外查看 Session.execution，不增加控制资源或可继续输入的“归档中”状态。

### Input 处理状态

| 转换 | 执行方与条件 |
| --- | --- |
| 创建 → queued | backend 在会话 active 且权限有效时受理 |
| queued → canceled | backend 成功撤回，或输入处于本次 stop / archive 受理范围内 |
| queued → rejected | backend 核对或当前 Engine 确认 Agent 停用 / 受理代次过期；当前 Engine 确认会话或 Agent 使用权限失效；尚未创建 Turn，保存原因 |
| queued → started | 当前 Engine 原子领取，同事务创建唯一 Turn 和 active_turn 指针 |
| started → processed | Engine 提交对应 Turn 终态，同事务清理活动指针 |

processed、canceled、rejected 为输入终态，不再重新入队。started 时撤回失败，不降级为隐式停止。停止已开始输入的结果体现在 Turn，Input 最后为 processed，不能误写成“从未执行”的 canceled。

### Turn 执行状态

| 转换 | 条件 |
| --- | --- |
| 创建 → running | 当前 Engine 取得执行权并领取 Input；环境准备也属于这轮实际处理 |
| running → stopping | Engine 发现已受理的停止或归档控制，开始关闭工具窗口和收尾 |
| running / stopping → completed | 实际工作已完成；停止请求不能改写已确认完成事实 |
| running / stopping → failed | 确认执行失败，保留错误和已知副作用 |
| stopping → canceled | 能确认目标执行已停止；保留已完成的局部操作 |
| running / stopping → interrupted | 无法继续且不能确认正常完成，例如进程或环境丢失；外部结果未知单独标注 |

终态不可重新进入 running。失败、取消或进程中断后继续使用新 Input / Turn；没有自动续跑旧 Turn 或重试旧 Turn 接口。正常 Turn 的 completed 表示本轮对话和工具请求结束，不要求其启动的已确认后台进程退出。

### 运行门禁与会话执行概况

execution_gate 使用 closed、open、blocked：未分配或准备环境时 closed，验证代次、原生历史及环境后 open，不能证明旧执行者已隔离、后台活动未知或所需原生历史不可用时 blocked，并保存具体原因。领取新 Input 同时要求 gate=open、bound_generation 等于当前有效归属、没有活动 Turn、会话 active，且既有停止范围已收尾。backend 登记 assignment 为 unresolved 时，即使失联 Engine 未更新 gate，也必须派生为阻塞并拒绝领取；backend 不因此代写 Turn 终态。

结果未知不必永久阻塞整个会话：已隔离旧进程且环境安全时，可以接受用于核对结果的新轮次，但不能自动重放原副作用；未完成隔离时，即使把 Turn 记为 interrupted，也必须保持 blocked。

对外 execution.state 按 blocked → stopping → running → queued → idle 优先级计算。运行门禁因旧执行未隔离、后台活动无法核验或原生历史不可用而 blocked，或 assignment 为 unresolved 时，概况为 blocked；已受理停止且边界内 Turn、后台进程或在途调用尚未收尾时为 stopping，即使 active_turn_id 为空；其余按当前 Turn 和有效排队输入计算。idle 表示没有活动 Turn、有效队列或未决调用，可以存在已确认后台进程；同时返回 background_process_count，不能据 idle 推断 Sandbox 可以回收。无法核验的旧执行在 runtime 中保留来源与原因，确认收尾后才解除阻塞。

后台进程归创建它的执行 Session，origin_turn_id 不随后续 poll/write 改变。业务会话共享 Sandbox 内按 Session 核对活动与停止，不把其他 Session 的进程计入本会话。首版有后台进程时保留对应运行实例和 Sandbox 引用；正常换轮允许保留，stop/归档则收尾目标 Session 的进程。其他参与任务仍活动时不能释放共享 Sandbox。

停止返回 Session 表示已受理，后续查询同一个 Session 即可观察是否仍在收尾；没有独立控制结果状态机。停止后的新输入可能使 Session 直接进入 queued / running，此时通过 active_turn_id 或已有 Turn 查询区分当前轮次，不追踪每个停止请求的独立完成状态。

## 7. 事务与并发裁决

### 统一串行点

只有 Input 提交（含创建时首条输入）先完成去重键的唯一登记；空会话创建、标题修改、撤回、停止和归档不登记请求。需裁决 Agent 启停或 queued 输入的操作，按下列顺序取得必要的锁，已取得后不反向加锁：

1. Input 提交的去重唯一登记，仅两个输入提交入口需要。
2. Agent 行 FOR SHARE：用于创建会话、追加输入、领取、撤回、停止和归档；后面三项不要求 Agent 启用，但要先补齐失效输入的拒绝事实。
3. Conversation 行：普通输入/领取用 FOR SHARE 核验父级准入；创建唯一主对话、环境登记或父归档用 FOR UPDATE。需要任务关系更新时先 Task 后 Session。
4. Session 行 FOR UPDATE：串行裁决同一执行会话状态与序号；创建时为本事务新建行。
5. 环境登记（需要时）→ assignment → Engine 注册行（需要预留/释放容量时）→ runtime → Input（按 seq）→ Turn → event，按操作需要访问及更新。

Agent 停用事务对 Agent 行取 FOR UPDATE，只更新启停、代次和不可变停用记录，不在该事务内遍历所有 Session。共享行锁允许不同 Session 并行；这里使用 FOR SHARE，而不是不能阻止普通列更新的 FOR KEY SHARE。纯执行终态、已受理的收尾和可靠事件编号可从 Session 锁开始，不能随后反向锁父记录；需要父级准入或环境分配的操作必须从上游锁顺序进入。事务内不调用模型、网络、CLI 或对象存储。

会话计数器由对应受理或执行事务在同一会话锁下分配，不改变 backend 对生命周期及归属、Engine 对执行结果的确认权。心跳只锁 Engine 注册行；失联标记提交后再逐 Session 核对，禁止持有 Engine 行反向锁 Session。后续数据库角色应提供限定列或操作的入口，不能把“可以分配事件序号”扩展成任意修改 Session。

初版采用 READ COMMITTED 加显式行锁，不依赖进程锁跨 backend / Engine 实例协调。持锁后重新读取状态及关联记录，不能用锁外旧读结果执行更新。并发胜负以受锁保护且成功提交的业务转换为准，不按客户端时间或请求到达次序裁决。跨会话批处理按稳定 ID 顺序加锁，或拆成单会话短事务。行锁解决数据库竞争，不代替外部幂等。[行锁与死锁规则](https://www.postgresql.org/docs/16/explicit-locking.html#LOCKING-ROWS)、[事务隔离](https://www.postgresql.org/docs/16/transaction-iso.html#XACT-READ-COMMITTED)

查询 Session 概况、有效排队数、当前 Agent 状态及 event_cursor 时使用同一数据库读取快照，避免拼出互相矛盾的状态或把尚未反映的事件当作已读。可用一条查询或只读一致性事务完成；普通读不取得上述写锁，SSE 连接不长期持有数据库事务。

backend 各副本按上述锁顺序即时分配，以唯一约束和容量条件更新裁决；周期维护单独选主，仅负责补偿与失联扫描。backend 仅变更归属及代次，不替 Engine 写 Turn 结果；维护角色切换或 event-hub 换主不无条件重新分配所有会话。完整流程见[注册与分配设计](../../backend/docs/engine-dispatch.md)。

### 创建与提交

1. 验证身份、目标资源和参数；Input 提交先查找 / 登记去重身份，命中成功记录时按当前权限返回原资源，不重新解析配置或执行准入动作。
2. 新建 Session 核验员工及 active Conversation，按同一读取点解析配置快照。chat 只允许创建唯一主对话；task 同时创建 Task。携带首条输入时连同 seq=1 Input、事件和去重引用原子提交；空 task 创建每次可产生新 Task/Session，空 chat 仍受唯一约束。
3. 向已有 Session 追加输入时，按 Agent → Conversation → Session 核验准入，递增 last_input_seq，保存 queued Input、受理事件和去重引用。不同 key 的并发输入均可受理，以 Session 事务分配的 seq 排序，不按文本去重。
4. 提交后可发唤醒通知；通知丢失不丢工作，调度与执行者从持久化 queued 输入重新发现。

### 领取与撤回

Engine 按 Agent → Conversation → Session 核验父级准入、启停、输入受理代次、分配、门禁和当前授权，选择最早 queued 输入。父级已归档时不创建 Turn，由子级归档流程取消队列；停用或权限失效则 rejected。可以执行时原子创建 Turn、将 Input 改为 started、登记事件，提交后才启动外部处理。领取先提交且仍运行则归档返回 409。单插件临时不可用不阻止整轮领取；必需依赖的执行失败记入 Turn。

撤回不需要 key，按统一锁顺序先补齐已失效代次输入的拒绝事实。若仍 queued，改为 canceled 并提交对应事件；若已 started / processed / rejected，返回状态冲突；已 canceled 返回当前 Input，保留原取消原因和时间，不重复发事件。不隐式取消其他排队工作。因此“开始”与“撤回”只有一个胜者，数据库唯一约束防止重复 Turn。

### 停止与新增输入

每次 stop 均不使用 key，backend 按 Agent → Session 的锁顺序核验当前权限及生命周期、补齐失效输入的拒绝事实，再取 cutoff_seq=last_input_seq，将 stop_through_input_seq 更新为两者较大值，并取消 seq≤cutoff_seq 的有效 queued 输入。Engine 响应边界内的当前 Turn、来源 Turn 已完成的后台进程及在途调用；backend 不直接写 Turn 终态。即使 active_turn_id 为空也要唤醒执行者处理后台活动。停止边界、输入取消及实际状态变化的相关事件一起提交，不保存停止请求去重记录或原 HTTP 响应。不创建控制资源或 control_id。

本次 stop 之后的新输入得到更大的 seq，不受这一次停止影响。执行者先收尾旧轮次，再处理新输入；若旧环境尚未隔离则保持门禁阻塞。再次 stop（包括网络重试）会读取新的 last_input_seq，停止两次受理之间新增的工作；即使请求头携带相同 key 也不改变这一语义。已归档会话上的任一次 stop 均返回冲突，不查询旧请求结果。

若边界与业务状态没有变化，直接返回当前 Session，不重复更新元数据、发状态事件或重新建立停止流程；已有收尾继续进行。stop 仍需执行收尾时返回 202，当前无工作且旧执行已结束或隔离时返回 200。不能因 Turn 已记为 interrupted 就推断进程停止，仍未确认隔离时保持 blocked。

### 归档与输入竞争

Conversation 首次归档先取得父记录排他锁，锁定子 Session 并核验全部停稳，否则 409 且不修改状态；通过后提交 archived 及可持久发现的子级收尾意图；不持有父事务等待网络。此后所有新建和 Input 受理/领取都拒绝，即使某个 Session 仍显示 active。Backend 逐个按下述 Session 归档转换补齐，task 的 Task 归档事实保持一致；已有 Turn 仍可写收尾结果。重复父归档继续核对未完成项，不重复改写已归档子项。全部参与 Session 收尾后才授权环境回收，首版不自动删除工作文件。

Session 首次归档在同一会话锁下核验停稳，否则 409；通过后将 lifecycle 改为 archived，设置 archived_at、递增 revision、推进停止边界并取消有效 queued 工作。提交之后的新输入直接拒绝；仍排队或执行的输入阻止首次归档；历史已归档对象的收尾继续允许。原 Turn 的执行事件不因生命周期 archived 而被拒绝，否则会话永远无法确认收尾。

归档不需要 key。对已归档会话再次归档返回当前 Session，不改变已提交边界、revision 或 archived_at，不重复发归档事件。仅有历史读取权限不等于拥有归档权限，重复调用仍核验当前调用者权限。

单个 Session 归档只收尾自己；主 chat 不级联任务，task 不影响其他 Task。即使它是最后一个任务，父 Conversation 活跃时也不回收共享 Sandbox。Pi 进程释放条件和环境回收条件分别按[三层回收](conversation-lifecycle.md#三层回收)核验。

标题修改与归档也由同一 Session 锁裁决：标题先提交则归档保留新标题；归档先提交则拒绝后到的修改。标题修改必须匹配 expected_revision，即使请求内容恰好等于当前标题也不能跳过旧版本冲突检查。版本匹配且内容未变时不递增 revision，不发重复事件。执行进度不递增用户元数据版本，避免模型持续输出导致标题无法保存。

### 完成与控制竞争

Engine 准备好可靠执行事实后，在会话锁下重新核验执行归属、代次及原 Turn 状态，再提交终态、Input processed、可靠事件及活动指针清理；不等待平台检查点上传。完成先提交时，后到控制不改写终态，但仍收尾该 Turn 留下的后台进程；控制先受理时，Engine 按实际完成或停止结果报告，不能单靠“收到停止”推断 canceled。

Engine 提交运行事实及 session.updated，backend 从 Session、Turn 和 runtime 查询当前概况，无需另一个控制回执核对流程。重启后沿持久停止边界与原实例活动账本继续收尾，确认后更新 applied_stop_through_input_seq；停止每次按当前工作更新范围，无状态变化时不重复写入，归档用不可逆生命周期防止重复应用。

### Input 提交去重与可靠事件

仅在 session_request_dedup 中登记两个入口的 Input 提交，范围及字段见前文。唯一登记、输入写入和最终资源引用在同一事务完成；结果未补齐必须回滚，不提交长期 processing 行，也不另设请求处理状态机。不同内容复用同一键返回冲突；命中成功记录先校验当前权限，返回原 Input 或创建时原 Session 的当前状态，详细响应见 [API 去重规则](../../backend/docs/session-api.md#input-提交的必要去重)。

唯一登记先于 Agent / Session 锁，不能只执行无锁的“先查询再插入”。登记阶段只占去重键，资源引用在取得业务锁后补齐，避免外键校验提前锁住 Session 形成反向顺序；事务不得提交缺少必要引用的登记。同键并发由唯一约束等待首次事务；INSERT ON CONFLICT 未插入后，用后续语句读取已提交记录再核对指纹和权限，不能假定冲突行已在原语句快照中可见。创建携带首条输入的 Session / Input 引用是首次成功结果，重试不能重新生成。首次事务回滚则无成功登记，后续调用可正常竞争受理。首版随会话历史保留映射；清理前须定义旧键拒绝规则，不得过期后无提示地重新执行。

可靠事件的 seq 在会话事务锁下分配并与对应状态一同提交，避免较大序号先提交而让续读者漏掉较小序号。source_key 必须标识具体事实，例如输入 ID + 受理事件类型、消息 ID + 正式提交，或工具调用 ID + 事件类型 + 持久进展序号；同一次调用的开始、进展和完成不能共用一个去重键，重放同一事实也不能重新生成身份。临时文本增量不分配可续读的正式事件序号，正式消息提交另有可靠事件。

历史查询第一页固定已提交高水位 H，分页只读取 seq≤H，末页提供 H 的实时续读游标。实时端点从该位置之后补发数据库事件，通知只用于唤醒，不能作为唯一可靠来源。筛选及作用域绑定游标，事件历史与流共用平铺结构；具体约定见[事件历史与两类游标](../../backend/docs/session-events.md#10-http-入口与历史分页)。

### Engine 并发与事务重试

多个 Engine 领取或提交结果时，均在同一 Session 锁下校验 assignment、generation 和活动 Turn；归属变更也经过该锁。领取唯一性由 input_id 唯一约束及活动 Turn 唯一部分索引兜底，旧代次提交被拒绝。同一 Engine 内按 Session 串行驱动，运行实例和工具调用沿用稳定身份；领取或启动响应丢失后查询原记录，不凭“没有收到响应”再次启动模型进程或命令。代次不能隔离同一代次的两个进程，进程启动与接管协议仍须落实实际运行互斥。

数据库约束只保证持久状态。旧 Pi 或未决调用可能继续执行外部操作时，保持 execution_gate 阻塞，确认原执行已停止或隔离后才能启动后续工作；不以释放数据库锁代替进程隔离。不同 Session 可以并行，同一 Session 只允许一个活动 Turn / 在途 prompt；已确认后台进程可跨 Turn 共存，不属于第二个对话执行。锁不覆盖整个 LLM / 工具调用时长。

数据库死锁等已确认回滚的短事务可以有限重试，重试重新读取状态并保留原 key / expected_revision；版本冲突直接返回，不能自动替调用方换版本。锁等待超时并确认事务回滚时返回 409 resource_busy；同键首次事务等待超时返回 409 idempotency_in_progress，给出重试提示。等待上限和内部重试次数在实现契约中限定，不无限等待。

连接中断或提交结果未知不能套用“已回滚”重试逻辑：Input 提交沿原 key 核对，stop 可重新发起并按重新受理时的当前工作确定范围，普通 CRUD 按目标状态、唯一条件或预期版本重读处理；空会话重新创建可能产生另一个空会话。数据库事务重试不包括任何外部模型、CLI 或 MCP 调用，未知外部副作用按原执行身份核对。

## 8. 恢复、索引与数据保留

进程故障后旧 Turn 进入 interrupted，Input 进入 processed；不创建恢复尝试、不重新入队或发送旧 prompt。backend 核对旧执行隔离后才重新分配；新获准 Engine 可在 Session 锁及当前 assignment 校验下补记遗留非终态 Turn 的中断事实，不修改其原 execution_generation，不让旧 Turn 重返运行。无法证明旧执行已停止时保持 blocked，心跳超时本身不是终止证据。

后续新 Input（例如“继续”）由 Engine 启动或复用 Pi，经 `--session` 加载 加载同一原生会话并执行新 Turn。尚未开始的 queued Input 仍按原顺序首次执行，不视为旧 Turn 恢复。平台保存 Pi 身份与持久目录位置，Pi 自己维护原生历史和映射；不建平台检查点包及引用表。部署须保留目录并保证分配目标可访问，首版不做跨机自动迁移；原生数据不可用明确失败，不用公开消息拼接或空白新会话替代。Pi 历史与平台事件没有跨存储原子提交承诺。

查询索引优先覆盖授权后的会话列表 `(workspace_id, agent_id, lifecycle, created_at, id)`、会话内 Input / Turn 顺序、事件 seq 及实际需要的 turn_id / type 筛选、未决 runtime 和 PluginAuth 状态查询。平台或工作区资源的版本详情用 `(resource_id, version)`。索引最终根据真实查询和执行计划核验，不为所有可选筛选预建组合。

本期不提供会话删除，归档保留历史。session_events 是正式消息及工具历史的对外来源，首版随 Session 历史保留，不按短期 SSE 日志清理；临时增量不持久化。游标淘汰不等于删除历史，可重新遍历可靠事件再接流。幂等记录保留约定见 backend，认证流程及原生目录的备份和保留策略仍需细化。Skill 包、凭据存储与默认工作目录各自有内部生命周期，暂不抽象成通用文件资源。

## 9. 并发与故障验收清单

以下为实现后的验收要求，目前仅完成设计审查，未运行数据库或业务测试。

| 场景 | 应保持的结果 |
| --- | --- |
| 两个 backend 同键提交，首次响应丢失 | 只有一个 Input，重试得到原 Input 当前状态；不同内容冲突 |
| 创建携带首条输入与空会话创建分别重试 | 前者只创建一个 Session / Input，后者可创建多个独立空会话 |
| 两个不同 key 同时提交相同文本 | 两条 Input 都受理，seq 唯一且串行执行，不按正文误合并 |
| 同版本并发修改标题 / 配置 | 不同修改只有一个成功，另一方版本冲突；不回放或自动覆盖 |
| 两个 Engine 同时领取同一会话 | 只有当前归属能提交，同一会话只存在一个活动 Turn |
| 撤回与领取竞争 | 要么 Input canceled 且无 Turn，要么 started 且撤回失败 |
| 停止与新输入并发 | 本次边界内工作收尾，边界后的输入不受这一次停止影响且不并行越过旧轮次 |
| 再次 stop 时已有新输入，含携带相同 key 的重试 | 按新的 last_input_seq 推进边界，取消新排队输入或要求新一轮收尾，不创建停止去重记录 |
| 无新增输入且状态未变时重复 stop | 不重复更新元数据、发状态事件或重建停止流程；收尾中 202，当前无工作且安全收尾后 200 |
| stop 成功后归档，再重试 stop | 按当前 archived 状态返回冲突，不回放原结果 |
| 无 key 重复撤回 / 归档 | 返回当前资源，不重复改变原因、时间、revision 或发事件 |
| 归档与提交并发 | 先受理且未结束的输入使归档返回 409；归档先提交则新输入拒绝 |
| 归档与标题修改并发 | 修改先提交则保留新标题；归档先提交则拒绝修改 |
| 完成与停止并发 | 已确认完成不被改成取消；Session 执行概况和 Turn 反映实际结果 |
| 旧 Engine 在提升代次后回报 | 不能覆盖新记录；旧环境未隔离前不能并发启动新轮次 |
| Turn 已中断，但原命令结果不明 | 展示未知状态，不能换调用 ID 盲目重跑 |
| OAuth 撤销后旧回调或刷新到达 | auth_epoch 比较失败，不恢复已撤销授权 |
| 模型、Skill 或插件更新 | 新会话按各自传播规则解析，旧快照保持不变 |
| Agent 停用与领取竞争，随后立即启用 | 已开始轮次可收尾；旧代次 queued 输入拒绝，不因核对延迟复活 |
| 一个插件认证过期，其他能力仍可用 | 仅相应调用报错，Harness 决定继续；不自动取消整轮 |
| 重新授权返回不同账号 | 不覆盖旧授权身份或凭据；需新建 PluginAuth 并显式绑定 |
| 多个事务写正式事件 | 会话内可稳定续读，无因提交乱序造成的漏读 |
| 事务死锁、锁超时或提交响应丢失 | 确认回滚才有限重试；未知提交按逐操作语义核对，不盲目重复外部调用 |
| 历史分页期间产生新事件 | 固定 H 后分页结束，从 H 后接流，不跳到新的最新位置 |
| 工作目录丢失但 Pi 原生历史可读 | 如实报告环境问题，不把加载上下文当成恢复工作文件 |
| 故障后用户提交“继续” | 加载原 Pi 会话并创建新 Turn，旧 Turn 保持 interrupted，不重发原 prompt |
| 无活动 Turn，但存在后台命令 | idle 同时返回后台计数；stop 进入收尾，完成后计数为 0，历史 Turn 终态不变 |

## 10. 本稿待细化项

- 按 [Engine 运行时设计](../../agent-engine/docs/session-runtime.md)验证固定版本 Pi 的原生会话加载、错误与取消行为，落实原生目录、Engine 身份映射、后台活动及故障中断事实。
- 按 [backend 分配设计](../../backend/docs/engine-dispatch.md)落实注册、分配、容量及相应数据库约束；按 [Runner 设计](../../sandbox-runner/docs/runtime-and-mcp.md)落实常驻管理与 stdio 代理。builtin 的 TS 工具复用、HTTP 接入和本地执行记录按[专项设计](../../sandbox-runner/docs/builtin-mcp.md)移植与联调。
- 完成身份和平台资源授权结构，验证 Agent 停用代次的并发实现，以及插件操作权限如何在 CLI 执行边界落实。
- 细化 PluginAuth 身份比对、非敏感接入值的结构、凭据存储与 OAuth / CLI 适配；换账号新建授权的产品行为已确认，本稿不选定供应商或凭据后端。
- 把本稿事务约束落实为迁移、组合外键、受限写入口及对应真实 PostgreSQL 并发验收，再生成结构快照；当前不创建生产 SQL。
- Session、Input、Turn 的 14 个接口、直接观察 Session 的停止结果和错误码见 [API 详细设计](../../backend/docs/session-api.md)，事件字段和历史 / SSE 见[会话事件协议 v1](../../backend/docs/session-events.md)。后续按实际启用的类型转写 OpenAPI，细化 Harness 超时、容量及游标轮换，不要求一次实现全部预设计 type，也不引入已暂缓的业务或文件模型。

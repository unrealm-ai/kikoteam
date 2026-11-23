# 数据归属

> 普通业务会话及其任务共享项目，TaskSpace 中的独立任务各自拥有项目。每个执行 Session 保留自己的历史和工具账本。生命周期见[业务会话与共享环境](conversation-lifecycle.md)，现行目录与目标布局的对应见[存储路径设计](storage-paths.md#7-验收与旧设计协调)。

内部 Skill 等不可变资产采用[统一存储配置](storage-configuration.md)：各组件自行实现存储适配，不新增共享存储 lib；权限和内容发布仍由上表的数据所有者负责。数据库引用与资产必须共同备份，本地目录相同不表示跨机器共享。

共享 PostgreSQL 时，按记录或字段划分事实的确认权。当前范围见[会话底座需求](../product/agent-sessions.md)；下表区分本期执行事实和后续业务规划，不由旧 Task / Run 或文件方案倒推核心表结构。

| 事实 | 写入者与权威位置 | 谁依赖它 |
| --- | --- | --- |
| 数据库迁移 SQL、执行器、迁移历史与结构快照 | event-hub 统一维护；当选实例在持有会话级选举锁的同一连接执行迁移并记录版本 | 各组件维护自己的查询；backend 和 Engine 不检查数据库结构版本 |
| Agent、员工能力包、ModelProfile、Skill、Plugin、员工插件账号映射及 PluginAuth 版本/权限 | backend 管理持久化配置、内容引用和授权状态；Skill 目录包按不可变版本保存；能力包拥有资源与操作范围，同员工同插件统一账号授权 | Engine 取得当前轮获准的配置与认证材料 |
| Conversation、Task、Session 关联及父级归档边界 | Backend / Manager 创建；首版一 Task 一 task Session；Conversation 归档先持久关闭准入，再推进子级收尾 | 所有创建、输入受理和 Engine 领取均核验父级生命周期 |
| Conversation 当前 Sandbox、宿主与管理 Engine | Backend 唯一登记、授予创建/回收权限；管理职责不随首个 Session 释放而解除 | Engine 创建/释放物理环境，Runner 管环境内执行事实 |
| Session 生命周期、配置快照、Input 内容及受理顺序、停止边界 | backend 登记受理事实、Input 提交的轻量去重引用及每次 stop 的当前停止边界；普通 CRUD 按状态或版本更新 | Engine 按序处理及响应停止，backend 从持久待办发现分配需求 |
| Engine 注册、容量、当前归属与执行代次 | backend 写入；多个副本以会话锁、分配唯一约束及容量条件更新裁决 | Engine 领取和提交时校验，新分配才提升代次，周期维护不代替事务互斥 |
| Turn、正式文本与工具事件、运行状态 | 当前 Engine 确认执行事实，可靠事件与相关状态同事务提交，后台活动按执行 Session 汇总 | backend 查询统一事件历史和执行概况，订阅者续读 |
| 项目附件与上传回执 | Backend 在项目所在卷保存 `payload` 和文件回执，再排他重命名到项目内。目录重构将暂存与回执放在 `<root>/temp/uploads/` | 回执保存请求指纹与候选路径，Web 和渠道按原键恢复上传结果 |
| 已交付产物与事项文件 | Manager 固定项目文件的发布版本，资产存储按工作区与内容摘要保存字节，数据库保存其来源和交付引用 | 使用者按授权读取当时交付的版本 |
| Pi 原生历史及 Engine 身份映射 | Pi 写专属持久目录；Engine 保存 Session 映射及可访问位置，不建逐轮检查点包 | 后续 Input 经 `--session` 加载并继续，新 Engine 先核验旧进程已退出及目录可访问 |
| 未执行输入撤回、输入开始与执行结束的处理事实 | backend 取消未领取输入，并按持久停用代次核对拒绝旧排队输入；当前 Engine 核验启停及权限后领取并确认执行结果；通过事务互斥，不互相覆盖终态 | backend 返回单一可查询结果，Engine 不执行已撤回或停用边界内的输入 |
| Sandbox 配置应用、进程、端点及工作目录 | Runner 管理环境内事实；builtin MCP 独立提供 HTTP 接入，stdio MCP 经 Runner Gateway 代理 | Engine 读取管理状态及可核对证据，未知结果不能当作可安全重试 |
| builtin 命令、操作阶段、输出及活动修订 | TS builtin MCP 的私有 SQLite；Runner 经受限管理通道核对，按[内置工具设计](../../sandbox-runner/docs/builtin-mcp.md)收尾 | 仅用于 builtin 执行核对，不替代平台工具事件，不由 event-hub 维护 SQLite |
| 工具调用可观察事件 | Engine 根据 Pi 原生事件持久化，可靠关联时补充目标 MCP 证据；远程 MCP 不经过 Runner | backend 查询统一事件；连接请求编号不直接等于业务调用身份 |
| 运行时认证材料 | backend 管理 PluginAuth 及受控凭据引用，授权适配器按权限取得或更新认证状态 | CLI / MCP 在限定认证上下文使用；不随临时 Sandbox 回收删除授权 |

backend 拥有控制意图，Engine 拥有实际执行结果。控制请求到达不等于正在运行的 Turn 已取消，不能由 backend 将运行中的工作直接写成取消完成。跨组件写入同一处理记录时，仅允许职责范围内的条件转换，具体事务约束在数据模型与状态机中明确。

对应的候选表、状态及事务顺序见[数据模型与状态机草案](session-data-model.md)。停止与归档直接返回 Session，backend 根据持久停止边界、Turn 和 runtime 计算会话概况，不建立独立控制回执核对流程，也不代替执行者提交终态。

event-hub 的主身份由 PostgreSQL 会话级 advisory lock 仲裁，同一把锁覆盖迁移和渠道、业务定时及投递工作，不保护 Session 分配。backend 的周期维护使用另一把锁，各副本即时分配仍走普通短事务。两者失锁均停止所属后台工作，不改变对方职责；详见 [backend 分配设计](../../backend/docs/engine-dispatch.md)及 [event-hub README](../../event-hub/README.md#选举锁与连接)。

## 三个提交点

1. **受理输入。** Backend 核验 Conversation 准入后原子保存输入身份、Session 内顺序、必要去重引用及持久待办。首次聊天连同唯一 chat Session 提交；立即开始任务时连同 Task、task Session 及快照提交。调用方不必预先分步创建 Task，空创建不登记去重，不引入 Run。
2. **受理控制。** 撤回与执行领取互斥；每次停止在 Session 内按当前已受理输入确定边界，重试会覆盖其间新增的工作。停止不要求 key，也不登记去重，无状态变化时不重复写入。归档同时关闭新输入，重复归档依靠生命周期返回当前状态，不要求 key。两者都不缓存原 HTTP 响应，执行结果另由 Engine 确认。
3. **确认执行。** Engine 在核对当前归属代次后提交消息、Turn/Input 结果及可靠事件；Pi 原生历史独立持久化，不增加跨存储提交屏障。临时文本增量只用于即时展示，恢复和查询以持久事实为准。

## 归档与恢复

单个 Session 归档只关闭自身输入并收尾自身工作；task 模式的 Task 归档事实保持一致，不影响其他任务或回收共享 Sandbox。停止/归档主 chat 也不级联停止任务。Conversation 归档则先关闭父级新任务、新输入及领取准入，再逐项归档全部 Session；已开始工作的终态和收尾记录仍允许提交。

Backend 在全部参与者停稳、无在途调用或未知活动后授权管理 Engine 回收 Sandbox 运行资源，Runner 先关闭 MCP 和环境内进程。活跃 Conversation 不做环境空闲回收；单个 Pi 进程可在无队列、后台命令或未决活动时独立释放。历史、Pi 原生目录和工作文件保留，不自动删除或取消归档。完整顺序见[生命周期](conversation-lifecycle.md#三层回收)。

统一可靠事件是正式消息与工具历史的读取来源，首版随会话保留；临时文本增量不保存。[会话事件协议 v1](../../backend/docs/session-events.md)统一定义平铺字段、message_id / tool_use_id、详情权限状态及历史到流的衔接；type 是按需实现的预设计目录，不改变上述状态写入归属。历史查询和实时流按相同权限投影，分页固定高水位并用末页游标续流，不能依赖已经移除的消息接口补回被删除的事件。

Pi 原生历史目录与 Sandbox 工作目录分别管理；前者独立于进程实例保留，故障后新 Input 加载原会话，旧 Turn 不自动续跑。原工作目录或执行证据丢失时报告实际可恢复范围，不把恢复模型上下文当作恢复全部文件。清理运行目录不撤销或删除 PluginAuth；撤权通过配置准入、运行时连接关闭与提供方凭据能力落实，不能声称平台可以瞬间撤回远端已接受的操作。

迁移归属保持不变，见 [event-hub 启动与数据库迁移](../../event-hub/README.md#启动与数据库迁移)；通用要求见[数据库规则](../rules/database.md)。

## 后续范围

项目上传、入站附件和固定版本产物的现行业务见[会话文件](../../backend/docs/conversation-files.md)。未来共享资料与记忆的发布接口由各自业务设计明确。此次开发环境保留数据库业务身份，允许放弃旧文件，切换后新建的内容按正常恢复规则保留。

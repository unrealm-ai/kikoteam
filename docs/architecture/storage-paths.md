# 文件路径、业务会话与隔离约束

状态：2026-11-16 已同步确认的关系及生命周期；路径约束和执行编排尚未实现。**主体是 AI 员工，业务会话是共享沙箱及未来文件/记忆的业务边界；执行 Session 保存独立模型上下文。** 配置按[统一存储配置](storage-configuration.md)，不新增统一存储 lib。候选数据模型、API 和调度已同步本文关系，机器契约、SQL 及代码仍待实施；不把路径文档当作完成迁移。

## 1. 关系与术语

```text
Workspace
  ├─ 工作区共享资源（Skill、插件非秘密资产、显式共享资料）
  └─ AI 员工 Employee
       ├─ 显式共享给该员工的记忆/资料
       └─ 业务会话 Conversation
            ├─ 对话执行 Session（chat）：独立模型上下文
            ├─ 本会话触发的任务 Task
            │    └─ 唯一执行 Session（task）：独立 Pi 原生上下文及 Engine 身份映射
            ├─ 会话共享 Sandbox / 工作文件
            └─ 会话记忆：从获准对话及任务结果中抽取
```

- 不另建“Agent 主体”层。文档中的 `chat/task Agent` 仅指执行角色。现有代码/API 的 `agent_id` 是 AI 员工的历史技术字段，不能据此再造 agents 路径；后续接口/表字段重命名需单独迁移，不在本次改已有 SQL。
- `conversation_id` 标识业务上的持续交流/处理范围；`session_id` 标识一份执行上下文。多个执行 Session/任务关联同一业务会话，不能仅因为模型进程重建或执行重试就新建业务会话。
- Session 内仍按既有 Input/Turn 规则串行，多个任务可并发；业务会话共享工作盘不要求不同任务共用模型历史。chat 的后台压缩和静默追加仍只操作对应执行上下文。
- 同一业务会话及其触发的多个任务复用该业务会话的共享 Sandbox。chat 自身不申请/启动 Sandbox、不开放本地工具；任务首次需要执行环境时创建或取得业务会话 Sandbox，后续任务复用。共享不意味着把 Sandbox 工具暴露给 chat。
- 不同业务会话默认不共享 Sandbox。将记忆显式共享给其他会话或整个员工，不代表也共享工作盘、执行历史、账号或全部输入文件。

完整关系及创建、停止、归档和回收职责见[业务会话与共享环境生命周期](conversation-lifecycle.md)。Backend 创建业务对象、唯一环境登记及管理者分配；Engine 管物理环境，Runner 管内部进程。首版同一业务会话的 task Session 固定到同一管理 Engine/宿主；活跃业务会话不自动回收共享环境。

## 2. 统一逻辑 key 与物理根

不可变持久对象 key 直接使用 `workspaces/<workspace_id>/...`，不额外增加 `storage/` 或全局布局版本目录。Pi 的可写原生历史是第 5 节的持久运行状态，不属于这一套对象发布流程：

- local：`${KIKO_STORAGE_LOCAL_ROOT}/<logical_key>`，根默认 `~/.kiko/`。
- S3：`s3://<bucket>/<KIKO_STORAGE_S3_PREFIX><logical_key>`。所有组件统一规范化前缀及 `/`；不可用机器绝对路径当 key。
- 资产格式版本记录在 `manifest.json` 的 `format_version` 及数据库资产元信息中，不用顶层 `v1/` 目录表达。Skill 等资源的 `versions/<version_id>/` 仍必须保留，表示不可变内容版本，不是格式版本。未来需要改变不兼容布局时通过显式迁移处理，不凭当前配置重解释旧 key。
- 不把员工名、群名、原始业务 key、邮箱或 Token 放进路径，采用服务端稳定 ID。
- DB 保存存储身份、逻辑 key、格式、大小、SHA-256、归属、来源及授权引用。读取仍须验证权限，目录存在不是授权凭据。

```text
workspaces/<workspace_id>/
  shared/
    resources/
      skills/<skill_id>/versions/<version_id>/
        manifest.json
        files/<validated_relative_path>
      plugins/<plugin_id>/versions/<version_id>/
        manifest.json
        files/<validated_relative_path>
    files/<file_id>/versions/<version_id>/content
  employees/<employee_id>/
    shared/
      files/<file_id>/versions/<version_id>/content
    conversations/<conversation_id>/
      inputs/<input_id>/files/<file_id>/versions/<version_id>/content
      artifacts/<artifact_id>/versions/<version_id>/
        manifest.json
        files/<validated_relative_path>
      memory/<memory_store_id>/revisions/<revision_id>/...
      tasks/<task_id>/
        inputs/<task_input_id>/files/<file_id>/versions/<version_id>/content
        artifacts/<artifact_id>/versions/<version_id>/...
```

### 资产归属与引用

| 类别 | 权威写入者及规则 |
| --- | --- |
| 工作区 Skill/插件资产 | Platform 保存完整不可变版本；不重复复制到员工持久目录。员工必须通过获准能力包取得版本。插件定义和附带文件不能含业务账号秘密 |
| 工作区/员工共享资料 | 后续文件业务显式发布并授权；路径归属不表示全员可见，不自动提升私聊/群聊材料范围 |
| 业务会话输入 | 后续 Manager 文件受理管理来源与读者；输入引用现有文件时可以仅保存 DB 引用，不为凑路径复制内容 |
| 任务输入 | 使用该任务获准的会话材料引用；任务新增或具有独立保留语义的材料才存 task 输入目录，任务 ID 不产生额外隐式访问权 |
| 会话或任务产物 | 后续发布模块校验并提交稳定 artifact/version 引用；记清所属会话、任务及执行 Session/Turn/尝试。Runner 工作盘里的文件不是自动已发布产物 |
| 执行上下文 | Pi 写执行 Session 专属持久目录，Engine 管理身份映射与访问位置；不逐轮发布检查点，task 之间及 chat/task 之间不混用原生历史 |
| 会话记忆 | 最小抽取和来源归属为业务会话，不为每个执行 Session 或每个任务建立独立记忆仓；具体写入服务和抽取流程在记忆能力实现时确定 |

单文件上传使用 `content` 存字节，原文件名/MIME 存元信息。完整 Skill/目录产物使用校验过的相对路径和 manifest；先暂存、校验、发布，再提交可见引用。尚未建立 Task/Input/文件 API 时不预造空业务记录；未正式受理的上传在临时区，不因路径方案隐式扩张现有接口。

## 3. 会话记忆的共享方式

1. 记忆从业务会话内获准的对话、任务输入与结果抽取，保留来源文件/消息/任务和授权范围。原生执行上下文及其摘要不自动成为可共享记忆。
2. 一个会话的记忆可显式授权给多个指定业务会话，或共享给整个 AI 员工；共享以 DB 中的记忆范围/读者关联表达，**仍引用原会话的权威版本**，不靠复制到多个目录实现同步。
3. “整个 AI 员工”是共享目标范围，不等于该员工所有使用者/群都能读。检索必须同时满足共享范围、当前访问主体权限和来源限制；记忆不能绕过私聊、群聊或任务授权。
4. 不为执行 Session 新建 memory 目录；跨任务检索读同一业务会话记忆中自己有权访问的内容。多个抽取任务并发更新时按 revision/CAS 提交，不能相互覆盖；重复材料按来源身份去重。
5. 共享不授予写源记忆的权限，不自动扩大沙箱/文件/插件账号权限。明确需要独立副本时新建身份并记录来源，不默认为复制模式。
6. 撤权/删除来源时同步处理检索索引、派生摘要和缓存可见性；不能只隐藏原文仍从记忆泄露。向量或结构化索引可以在 DB/专用存储，目录只保存文件型正文/版本，不强迫全部记忆文件化。
7. 本次确定最小业务会话粒度和显式共享范围；抽取模型、触发时机、保留期限与冲突合并算法尚未决定。工作区范围的“全员记忆共享”也不因工作区共享资源目录而自动启用。

## 4. Hook、定时触发与业务 key

Hook/定时任务由业务配置定义 **business key 的提取或生成规则**，决定事件落在哪个业务会话；不能按每次 HTTP 请求或调度执行新建一个隔离会话，也不能把所有触发统一塞到员工的唯一会话。

- 在工作区、AI 员工、业务命名空间/触发规则范围内解析 key，并映射到服务端 conversation_id。不同规则是否共用命名空间要显式配置，不能因同字符串 key 自动合流。
- 原始 key 可以包含业务敏感标识，只保存于受保护的路由元信息，不直接写入对象路径、公开 URL 或普通日志；路径只用 conversation_id。哈希编码只解决命名，不代替访问控制。
- 同范围同 key 的并发到达须由事务/唯一约束裁决为同一业务会话；事件自身的幂等 ID 与 conversation key 分开，避免“同会话”误等于“重复事件”。
- 解析后仍验证员工、来源身份、目标会话、权限边界及生命周期；不能通过自填 key 访问别人的会话。
- **待业务规则明确**：key 缺失/空值/提取失败时拒绝、等待修正还是创建独立会话；冲突 key 与不同安全域如何处理；已归档会话遇新事件是拒绝、开新代还是其他策略；规则修改后的旧映射如何迁移。实现前必须有显式策略与测试，不能默认合并、复活归档会话或随机分流。策略未配置时不得静默路由到现有共享数据，应返回可诊断的路由未就绪结果。

## 5. 共享沙箱与本机目录

首版是[Mac/Linux 本地项目目录模式](../../sandbox-runner/docs/local-sandbox.md)，与 Engine 同宿主、同账号，不做安全隔离；项目及 Runner 账本放在稳定 state 下，不能随进程实例回收。容器只保留扩展设计。

本地根下的 `workspaces/` 是权威不可变资产，`state/` 是必须持久保留的运行状态；`runtime/`、`cache/`、`tmp/` 与它们并列。S3 只映射不可变资产的逻辑 key，不上传整棵本地根、Pi 状态或运行目录。即使资产配置为 S3，Pi 仍使用持久 POSIX 目录，不能把它当作 S3 故障后的资产回退。

S3 保存稳定持久对象，不提供可写 POSIX 工作盘。运行目录仍在本机，不能让多个进程直接向 S3 上的 JSONL/SQLite append：

```text
<KIKO_STORAGE_LOCAL_ROOT>/
  state/engines/workspaces/<workspace_id>/employees/<employee_id>/
    conversations/<conversation_id>/executions/<session_id>/context/native/
      ...                 # Pi 原生会话与 Engine 身份映射；不含逐轮 checkpoint_id
  runtime/engines/<engine_id>/workspaces/<workspace_id>/employees/<employee_id>/
    conversations/<conversation_id>/executions/<session_id>/
      assignments/<assignment_id>/generations/<generation>/instances/<harness_instance_id>/
        home/             # 装配配置；原生数据指向对应 Session 的持久 state
        resources/        # 固定获准版本的只读物化
        work/             # 受控 Engine cwd，不是共享 Sandbox
        runtime/          # 最小认证材料，禁止整体打包上传
  state/sandboxes/workspaces/<workspace_id>/employees/<employee_id>/
    conversations/<conversation_id>/
      project/            # 稳定项目目录，任务命令默认 cwd
        tasks/<task_id>/   # 可选产出组织方式，不是独立项目或隔离边界
      runner-state/       # 固定凭据、环境身份、SQLite 账本及命令输出
  runtime/runners/<runner_boot_id>/
    ...                   # 本次启动配置、端口及连接信息；socket 使用短路径
  cache/<component>/<instance_id>/workspaces/<workspace_id>/<digest>/...
  tmp/<component>/<instance_id>/workspaces/<workspace_id>/<operation_id>/...
```

Pi 原生目录、Engine 身份映射、项目目录及 Runner 持久状态的位置均独立于 Engine、Runner boot、assignment 和执行代次；环境身份保存在元数据中。Pi 通过固定版本支持的目录配置保持映射有效，不能引用已清理的实例目录。已有项目丢失时明确报告，只有首次创建才建空目录；不整体备份含秘密的 HOME。部署负责各引擎挂载同一份共享存储，Backend 按健康状态、容量与当前沙箱管理权分配。固定 Skill 物化可在 Engine/工具间共用同一绝对路径，任务升级不改写原版本。

- Sandbox 归属业务会话，不归属某一个执行 Session。其 sandbox_generation 与每个 Session 的 assignment generation 分开；任务重试/迁移不能把同会话其他任务正在使用的 Sandbox 删掉或重新初始化。
- Backend / Manager 在 Conversation 锁和唯一约束下登记当前环境、宿主、管理 Engine 及获准参与 Session；指定 Engine 创建或取得原环境，Runner 按 Session 跟踪进程、工具调用和文件访问。重复申请复用准备中或已确认实例，结果未知先核对；首版不建执行尝试或每 Session 独占环境。
- MCP 配置、资源接口和账号使用仍按调用者业务授权装配，不能将任务权限取并集。首版本机命令使用运行账号正常权限，这些检查不构成操作系统文件或进程隔离。
- 所有任务默认使用同一 project 根；可以按任务子目录组织输出，但不强制独立项目。共享文件并发写入需协调写入者或串行化；产物发布 API 另行设计，路径本身不提供锁或合并语义。
- 首版 Mac/Linux 裸目录不提供安全隔离，不创建容器、独立账号或命名空间；绝对路径、链接和命令可达范围遵循本机账号权限。不把路径组织或状态文件防误操作当作沙箱边界，也不以未实现隔离作为本地运行的前置阻塞条件。
- 停止/归档某个执行 Session 只停止它拥有的工作，主 chat 的操作不影响任务。Conversation 归档先关闭全会话准入，再归档并收尾所有关联 Session；Backend 核验全部工作停稳后授权 Engine 回收环境运行资源，Runner 先关闭内部进程。活跃 Conversation 不自动空闲回收，工作文件和历史保留，不递归删除环境目录。
- 后台命令归执行 Session，保留 origin_turn_id，允许跨正常 Turn 存活；对话 idle 与后台进程数分别展示。有后台进程时首版保留其运行实例和环境引用，不能仅凭 Turn 完成回收。stop/归档即使没有活动 Turn 也要按所属 Session 收尾。
- Sandbox 工具 Token 随环境生命周期固定，换 Turn/进程重启不轮换。同 Session/同授权范围的 builtin 和代理入口可共用，其他 Session/范围独立绑定；管理和配置领取凭据分开。固定凭据不替代当前授权、执行窗口和旧进程退出核对。
- chat 不创建或直接操作 Sandbox，但后续任务可以使用已经存在的同一会话环境；Engine 侧原生历史始终独立保存，不把 Sandbox 快照当模型上下文。

## 6. 安全、发布、删除与恢复

- 平台资产/元数据接口按已验证的 workspace/employee/conversation/task/session 生成归属路径，拒绝穿越和跨资源访问；这不限制裸 Sandbox 工具的系统权限，工具工作目录不是 chroot。客户端传 ID 不等于获得平台资源权限。
- 对象源默认私有；禁止永久公开 URL、任意目录列表和高权限存储凭据下发。账号/Token/密钥不进入 key、manifest、Skill 或记忆，持久凭据走受保护存储，运行配置不作为公开资产或历史事件。
- 不可变对象先校验发布，再原子提交 DB 引用；数据库和存储无共同事务，须处理中断、重试、旧代次写入与孤儿清理。多文件版本由 manifest/DB 发布状态界定，不假设 S3 支持目录 rename。
- Pi 原生历史由 Pi 持久化，身份映射由 Engine 保存；平台只保存位置和身份，不增加检查点包或上传提交屏障。公开消息和可靠事件仍以 DB 为准，原生历史供模型上下文加载，两者无跨存储原子提交承诺；压缩和静默追加遵守各自 Session 的单写者顺序。故障后旧 Turn interrupted，新 Input 经 `--session` 加载并继续，不自动重发旧 prompt。
- 运行/缓存/tmp 清理不得删除引用中的持久资产。共享 Sandbox 回收不删除会话记忆、历史产物或 PluginAuth；删除员工不级联清除仍有引用的工作区资源。
- 归档、停用、撤权与物理删除分开；未定保留期限前不默认启用破坏性 TTL。共享记忆以引用和授权计算保留，不能删除单个任务就清掉整个会话记忆。
- 相同本地路径不是跨机器共享：不可变资产使用共同 S3/共享盘或授权传输，原生 state 则需要可访问的持久 POSIX 目录；本地缓存不是权威源。备份/迁移须同时覆盖 DB、资产、原生 state 与必要秘密，原生上下文加载不代表共享工作盘已恢复。原生目录缺失时明确失败，不新建空白会话替代。

## 7. 验收与旧设计协调

本设计尚未实施。业务会话关联、共享 Sandbox 所有权/使用关系及生命周期已确认并同步到候选数据模型和调度文档；后续落实 SQL 和机器契约。business key 路由及记忆共享授权仍需独立业务设计：

- 两工作区、员工和业务会话的平台资源引用与模型上下文不串线；同业务会话任务复用一个项目目录。裸模式不验收操作系统安全隔离。
- task 重试、一个执行 Session 停止/归档、Engine 切换，不误停或删除其他活跃任务的环境。
- 会话最小粒度抽取，多个会话/整员工显式共享与撤销有效；共享目标身份和来源限制同时生效，不能经记忆越权。
- Hook/定时同 key 并发归一、事件幂等独立；缺失/冲突/归档/规则变更策略有明确结果，不能默默扩大共享。
- local/S3 互读同一逻辑内容；本机进程重启保留持久数据，容器重建留后续扩展验收；配置错误不回退；发布失败可恢复且孤儿清理不破坏引用。
- Mac/Linux 真实运行、共享项目文件冲突、按 Session 停止、进程退出及文件保留分别验收，不把目录设计当作安全隔离结果。

Task 与 task Session 首版一对一，具体语义按[生命周期设计](conversation-lifecycle.md)。现有 agent_id 仍表示员工，不直接修改已发布员工表/API；候选 Session 与环境契约采用明确业务归属，不沿用单 Session Sandbox 路由。key 缺失/冲突的产品策略、记忆和文件 API 仍不在本次确定范围。

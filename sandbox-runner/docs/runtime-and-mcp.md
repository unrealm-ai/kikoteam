# Sandbox 常驻管理与 MCP 传输代理

> **2026-11-16 关系与生命周期已确认**：一 Task 一 task Session，共享环境归 Conversation；创建、停止、归档、首版放置及三层回收按[业务会话与共享环境生命周期](../../docs/architecture/conversation-lifecycle.md)。下文保留完整目标范围；当前最小实现与限制见仓库运行说明和实施计划。

状态：2026-11-17 裸 Runner 配置领取、内置 MCP 编排、固定 Token、活动核对与 Session 定向停止/释放已实现，详见[运行说明](../../docs/operations/minimal-agent-runtime.md)。sandbox-runner 对应参考项目的 Executor；下文保留完整目标，stdio MCP → Streamable HTTP 的 Gateway 本期不实现。

builtin MCP 采用用户提供的 TS 实现，包含六个 Pi 文件工具和五个 Bash 工具，由 Runner 启动并直接提供 Streamable HTTP。工具清单、复用边界、私有 SQLite、HTTP 与管理适配集中维护在[builtin MCP 专项设计](builtin-mcp.md)，本稿维护 Go Runner 和 stdio Gateway 的职责。

## 1. 部署与生命周期

首版按[裸 Sandbox 设计](local-sandbox.md)实现：Engine 本机项目目录加受管工具进程，支持 Mac/Linux，不提供安全隔离；Runner 是 Engine 启动的同账号子进程，不需要独立环境系统。Go Runner 与 builtin 的 Mac/Linux 链路已接通；本批 Skill 复用本机固定版本目录，远程 MCP 不经过 Runner，stdio Gateway 留后续。

| 模式 | Runner 与环境 | 管理边界 |
| --- | --- | --- |
| 裸目录，首版 | Engine 在 Mac/Linux 本机固定 project 目录启动 Runner；复用本机程序、依赖和账号 | 无文件/网络/进程/资源隔离，目录和 Session 账本仅组织工作与控制范围 |
| 容器，仅后续扩展 | 将来替换 Engine 的小型环境实现并接入容器或外部系统 | 本期不做供应服务、镜像、容器调度或安全隔离实现 |

Agent / Session 不接受模板、镜像、挂载覆盖。Pi 始终由 Engine 启动并运行在 Engine 侧；Runner 不启动模型会话，不决定 Input / Turn 如何分配。

命令以所属项目为 cwd。项目按 Conversation 或独立任务归属，跨 Engine/Runner 重启保留，工具 Token 和账本按 Session 保存，`launch.json` 与 socket 放在本次启动的私有临时目录。

Sandbox 归 Conversation，Backend 唯一登记环境、宿主和管理 Engine，首版该业务会话全部 task Session 固定到此管理 Engine。Runner 只执行获准的准备和收尾，不因首个任务退出而自毁，不自行创建业务对象、改变归属或选择回收时机。活跃 Conversation 不做环境自动空闲回收；父会话归档且全部参与者停稳后，Backend 授权 Engine 回收运行资源，文件及核对证据保留。

Runner 启动阶段为 starting → configuring → ready；部分插件失败可报告 degraded，必需环境不可用则 failed。stopping 表示关闭新执行并收尾，stopped 表示管理范围内进程已确认结束。进程存活、资源准备完成和某个 MCP 可用是不同事实。

## 2. 配置领取与准备

1. Backend 预先登记唯一 sandbox_instance_id、Conversation 和管理 Engine；指定 Engine 通过环境启动通道注入 Backend 地址及受限 Runner 凭据，不自行创建另一环境身份。
2. Runner 生成本次进程的 runner_boot_id，核对已有环境身份；主动调用 backend 的 Sandbox 配置接口，不直接访问业务数据库。
3. Backend 的环境级 config 返回 Conversation、管理身份及逐 task_id/session_id 的固定版本、包来源、资源摘要和当前授权。Runner 分别核验参与关系和准备资源，不将权限取并集。新增 Session 必须属于同一活跃 Conversation；父归档后只允许核对和收尾。管理角色变化与各 Session 分配代次分别验证。
4. 启动 builtin MCP 和获准的 stdio 插件，完成实际连接与可用性检查后发布对应端点；不能以“配置已下载”冒充服务 ready。
5. Engine 从管理接口读取完整状态及端点，生成 Harness 原生配置；准备失败逐项报告，不将部分目录当作完整成功结果。

Runner 从 config 接口获取什么及凭据边界，见[backend 运行配置](../../backend/docs/engine-dispatch.md#6-配置授权与-runner-启动)。资源版本固定，认证材料按当前授权更新；刷新 Token 不升级插件版本，也不能静默切换 PluginAuth 账号。

重启后重新观察进程、监听地址及 MCP 连接，不沿用旧 PID 假定进程仍在。工作目录保留不等于 stdio 管道可恢复。已有执行事实不明时保留未决状态；builtin 的本地执行记录按[工具持久化设计](builtin-mcp.md#4-进程状态结果与持久化)核对，不能创建空记录伪装“从未执行”；第三方插件按其自身能力恢复。

## 3. 三条 MCP 路径

```text
Pi（Engine 内）
  ├─ Streamable HTTP → builtin MCP（Sandbox 内，Runner 启动）
  ├─ Streamable HTTP → Runner MCP Gateway → stdio 插件（Sandbox 内）
  └─ Streamable HTTP → 外部远程 MCP

Engine → 本机 Runner 管理接口：状态、准备、停止、回收
Runner → backend：配置及授权获取
```

builtin MCP 的工具请求直接到该 MCP；Runner 管理接口不转发普通工具调用。Gateway 只为 Sandbox 内 stdio 插件做传输适配，不位于 Engine，不聚合所有工具，不代理外部 HTTP MCP。

首版本机 builtin/Gateway 监听 127.0.0.1、绑定端口 0 后报告实际端点，不先探测端口再重绑。管理通道同样限本机，builtin 可沿用短路径 Unix socket；不增加反向代理。重启可更换端口，Engine 重读端点后在安全边界重连，Token 和环境身份保持稳定，不重放未知调用。身份/授权核验仍保留，但同账号工具具有宿主正常权限，不提供恶意代码隔离。

每个插件 binding 独立暴露 HTTP MCP 地址，分别写入 Harness 配置。binding_id 标识 Session 快照中由获准能力包解析出的运行插件绑定，不是员工直接绑定插件的配置入口；同插件账号来自员工统一映射，运行清单保留获准包来源。该标识与网络连接 ID、PluginAuth ID、工具调用 ID 区分。地址由受控配置和实际监听确定，不能由模型提供任意目标地址。

内部 MCP 端点示意；工具契约由对应服务维护：

```text
<builtin-mcp-base>/mcp
<runner-base>/mcp/plugins/<binding_id>
<remote-mcp-url>
```

这些都是内部可达地址，不新增公网域名；外部用户仍只访问 backend。Engine 与 Sandbox 之间需要实际可达的网络或部署通道；仅填写远程地址不能建立连通性。容器内 loopback 地址不能直接交给容器外 Pi。

## 4. Gateway 的协议边界

Gateway 作为 HTTP 侧 MCP 服务端和 stdio 侧 MCP 客户端，使用与 Harness、插件实际兼容的协议/SDK 版本。每个绑定及连接隔离协议状态；请求编号只在相应连接内关联，不直接视为业务去重键。

职责包括协议初始化/版本处理、请求响应及通知转发、取消传播、流关闭、连接错误、背压和有界缓冲。只向上游声明代理链路实际支持的能力；不支持的能力应明确拒绝，不能静默吞掉或宣告成功。版本相关的握手、会话标识和取消语义以固定版本规范及 SDK 行为为准，不自行发明协议。

stdio 子进程的 stdout 仅用于协议帧，stderr 单独收集诊断；禁止把日志混入协议。程序、参数、环境和 cwd 来自固定配置，使用参数数组启动，不把模型文本拼成插件启动 shell。

默认按当前会话运行身份隔离连接；断开或重启后的新连接不接收上一条连接的迟到响应。是否保留子进程、如何兼容不同协议版本的连接模型，在代理实现中按固定 SDK 测试确定，不能跨 Session 共享未隔离的插件状态。

连接可以重建，但已经发出的工具请求不能因断流而自动重放。客户端关闭流或发取消只表示取消意图，不能据此推断外部副作用尚未发生或已撤回；结果不明明确报告未知。第三方插件的工具级恢复取决于其自身契约，不能直接套用 builtin 的本地操作记录。

协议依据：[MCP Transport](https://modelcontextprotocol.io/specification/2026-07-28/basic/transports)。该规范修订与当前 Pi/插件 SDK 的实际支持范围需联调，不因为规范已发布就默认所有依赖支持。Gateway 不包裹平台 REST 错误外形来替代 MCP 响应。

## 5. 身份、认证与停止

| 凭据 | 消费者与范围 |
| --- | --- |
| Runner 配置领取凭据 | Runner → backend；限定工作区、Session、Sandbox 实例及所需配置 |
| 环境管理凭据 | Engine → Runner；限定当前分配、代次和实例，不能交给模型 |
| MCP 访问凭据 | Pi → Sandbox MCP / Runner Gateway；绑定 Sandbox 实例、执行 Session 及获准范围，生命周期随 Sandbox，换 Turn/进程不轮换 |
| 插件第三方凭据 | Runner 提供给对应 stdio / CLI 进程；隔离账号上下文，不默认转交 Pi |

Gateway 在连接及请求准入时核验自身可验证的授权、有效期和当前管理状态，旧代次被关闭后拒绝新请求。地址、MCP 连接 ID、binding_id 本身不是凭据。长期授权仍由 backend 的 PluginAuth 管理，Runner 回收不删除授权记录。

Sandbox 首次授权绑定时生成固定 MCP Token，持久保存在受保护的运行配置中；同实例 Runner、builtin 或 Pi 重启仍使用原值，Sandbox 销毁时失效。换轮、stop 后继续及普通进程重启不要求认证热更新；撤权可以关闭对应授权，固定生命周期不表示撤权后仍可用。远程 MCP 的第三方 Token 按提供方规则刷新，不适用此约定。

同一执行 Session、同一授权范围的 builtin 与 stdio Gateway 可共用一个工具 Token。共享 Sandbox 中不同 Session/授权范围分别使用独立固定 Token，由服务端绑定可访问的 Session、资源和 binding；不能拿同一个高权限 Token 再靠客户端选择 session_id 限权。Runner 配置领取、环境管理与 MCP 工具凭据不互认，Pi 不取得管理权限。凭据稳定不替代执行窗口及当前授权检查。

Runner 按执行 Session 为管理请求保存单调 control_revision，绑定当前 assignment / generation / harness_instance。停止旧窗口和允许新窗口必须按修订核对，迟到的旧开启请求不能重新打开入口；切换窗口不轮换 Token。该修订仅用于内部并发，不新增公开 control_id 或停止幂等 key。

标准 MCP 不要求携带平台 Turn ID。Engine 通过管理链路绑定当前执行窗口；Runner 不能按模型传入字段自行选择其他 Turn。前轮请求未排空时不得切到新轮；需要时关闭旧连接和插件进程，保持未决结果，直到可安全开启后续执行。

执行 Session 的停止范围覆盖它拥有的受管进程和 stdio 连接，包括已完成 Turn 留下的后台命令；共享 Sandbox 的其他参与 Session 不受影响。builtin MCP 通过独立私有管理通道同步该 Session 的窗口和控制修订、关闭准入，并核对其完整活动账本，详见[活动与停止](builtin-mcp.md#6-runner-管理活动核对与停止)。普通 MCP 端点不开放私有管理方法，取消请求本身不证明命令或文件操作已结束。远程 HTTP MCP 由 Pi 直连，Runner 不能报告它已停止。撤权禁止后续使用，已经送达远端的副作用依实际证据判断。

正常换轮仅排空本轮 RPC 和未知调用，可保留本 Session 已确认后台进程及其输出流；后台进程保留 origin_turn_id，后续 Turn 可管理。停止/归档必须终止目标 Session 的后台进程并排空调用；环境回收才要求全部参与 Session 收尾。首版只要有业务后台进程，就保留其运行实例和环境引用；进程重建后的后台接管不在本期范围。固定 Token 下，新实例前仍须确认旧 Pi 已退出，不能只改数据库 generation 后就允许两个 Pi 共用入口。

## 6. 管理接口与状态

管理接口前缀 `/api/internal/v1`，与 `/mcp/...` 协议入口分离。以下为设计稿；Builtin MCP 不使用本表定义工具契约。

下表区分环境级 state/shutdown 与 Session 级 prepare/execution-window/stop；Session 路由按目标汇总活动，管理者读取环境级完整参与者状态。管理身份及修订不代替每个 Session 的 assignment/generation。具体机器契约待转写，现有 builtin 单绑定实现尚待适配，不能将 Session stop 实现成环境 shutdown。

| 方法与路径 | operationId | 请求 / 返回 |
| --- | --- | --- |
| GET /sandbox-instances/{sandbox_instance_id}/state | getSandboxRuntimeState | 管理 Engine 身份；200 返回实例/boot、管理修订、全部参与 Session 的活动及环境准备状态 |
| GET /sandbox-instances/{sandbox_instance_id}/sessions/{session_id}/state | getSandboxSessionState | 该 Session 获准管理身份；200 返回所属活动和端点，不泄露其他任务 |
| POST /sandbox-instances/{sandbox_instance_id}/sessions/{session_id}/prepare | prepareSandboxSession | assignment_id、generation、期望配置修订；202 返回准备状态，同目标复用 |
| POST /sandbox-instances/{sandbox_instance_id}/sessions/{session_id}/execution-window | setSandboxSessionWindow | harness_instance_id、control_revision、open/closed；200 已应用，收尾中 202 |
| POST /sandbox-instances/{sandbox_instance_id}/sessions/{session_id}/stop | stopSandboxSession | 关闭并收尾该 Session；200/202 后查询该 Session state |
| POST /sandbox-instances/{sandbox_instance_id}/shutdown | shutdownSandboxRuntime | Backend 授予的环境回收权限、管理身份/修订；关闭全环境准入并核验全部参与者，停稳后退出 MCP/Runner；未知活动不报成功 |

state 汇总目标 Session 的 builtin 管理证据时保留来源、已应用 control_revision、activity revision、verified、active、unknown、pending_writes；另区分未结束的工具 RPC 与已返回但命令仍在运行的后台活动，供 Engine 派生 background_process_count。pending_writes 沿用归档字段，包含所有在途文件调用及后台输出流，不能把它一律当成禁止正常换轮的条件。来源不符、连接失联或未验证不能折算为空闲，不向普通 MCP 调用方暴露管理通道。

state 中端点记录含 binding_id、资源版本摘要、transport、url、availability 和 error_code?，不返回明文 Token。响应为有界完整快照；若后续引入分页须有一致快照标识，不能用半份响应替换完整端点清单。

旧代次、修订或环境身份冲突返回 409；无权限 403；合法调用方查询不存在实例 404；临时依赖故障 503。请求超时后查询原状态，不重新生成实例身份。管理接口的 HTTP 错误体沿用后端规则，MCP 入口遵循协议错误。

## 7. 验收与待补充设计

- Mac/Linux 分别通过本机启动、依赖检查、动态端口、文件和普通命令、后台进程及退出验收；PTY 单独核验。`/proc` 不作为基本运行前提；现有 Linux 测试不替代 Mac 验收，详见[本地方案](local-sandbox.md#本地启动与跨平台验收)。

- Runner 自启动获取配置，Engine 无需逐项推送初始化命令；错误实例身份不能领取配置。
- builtin MCP 与 stdio 插件实际在 Sandbox 内运行，Pi 留在 Engine。
- 两个 stdio 插件有独立 HTTP 端点；同名工具、相同请求编号和并发流不会串到另一绑定或会话。
- stderr 日志不污染协议；通知、取消、异常退出、缓冲超限和不兼容能力都明确处理。
- 插件重启不会自动重放结果未知的请求；旧连接响应不能交给新运行身份。
- 旧代次或迟到管理请求不能重新开放停止窗口；管理凭据无法由普通 MCP 调用取得。
- 换 Turn、stop 后继续、同 Sandbox 进程重启沿用固定工具 Token；其他 Session 及管理接口不能凭该 Token 访问。
- 无活动 Turn 时仍能停止本 Session 后台进程，停止一个参与者不关闭其他参与者的工具入口或销毁共享环境。

builtin MCP 的 11 个工具、TS 复用、HTTP 直连及私有管理通道已有实现，运行与验收入口见[模块 README](../builtin-mcp/README.md)。现有 HTTP 校验仍按旧方案要求切轮换 Token，且启动时绑定单个 Session；固定生命周期 Token、共享环境内按 Session 控制属于待适配项，本次文档不代表已实现。Go Runner 仍需实现配置领取、启动编排与 Gateway，并联调未修改的 Pi、实际 SDK 协商及完整活动采样。

## 文件路径与隔离

隔离沙箱的挂载约定为只读挂入官方和当前工作区两个 Skill 目录，Pi 和工具通过 Session 本地入口读取本次选择的版本。实际可见目录由挂载范围确定，Skill 筛选和授权变化另行设计。[挂载与链接路径](../../docs/architecture/storage-paths.md#skill-版本目录与沙箱)

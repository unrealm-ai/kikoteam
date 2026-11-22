# sandbox-runner · Sandbox 常驻管理进程

**当前实现（2026-11-17）**：Go cmd/sandbox-runner 已接通裸环境配置领取、每 task Session 独立 builtin、固定 Token、共享 project、窗口控制、完整活动采样、定向停止与退出。Skill 的固定目录由本机 Engine 下载并校验，builtin 复用同一绝对路径读取资料、执行脚本；远程 MCP 由 Pi 直连。stdio 插件与 Gateway 仍未实现。`make build` 构建，`make check` 检查 Go 与契约，实际联调见[运行说明](../docs/operations/minimal-agent-runtime.md)。

> **2026-11-16 关系与生命周期已确认**：一 Task 一 task Session，共享环境归 Conversation；创建、停止、归档、首版放置及三层回收按[业务会话与共享环境生命周期](../docs/architecture/conversation-lifecycle.md)。下文保留完整目标范围；当前最小实现与限制见仓库运行说明和实施计划。

Runner 对应参考项目的 Executor，管理进程及 Gateway 用 Go。builtin MCP 保留 TypeScript 独立包和子进程，与 Runner 一起运行在 Sandbox 内。Runner 负责 Sandbox 内部控制、向 backend 获取配置、准备资源、启动和管理 MCP / 插件进程，并提供 stdio MCP 到 Streamable HTTP 的 Gateway。该 Gateway 是 Runner 内部能力，不增加部署组件。

**TaskSpace 增量（2026-11-18，实施中）**：普通 Conversation 共享环境保持；TaskSpace 按 Task 独立 Sandbox/Runner/project，路径和 owner 校验见 [本地环境](docs/local-sandbox.md#taskspace-增量2026-11-18实施中)。这是软件归属和目录分离，不是 OS 安全隔离，也不提供共享文件。

## 部署方式

**首版运行**　[裸 Sandbox](docs/local-sandbox.md) 是 Engine 本机的项目目录和受管进程，Runner 由 Engine 作为同账号子进程启动。Mac 已通过 pipe/PTY 及整链路，Linux 入口已接 CI，结果分别记录；不要求 Docker 或独立 Sandbox 系统。

| 模式 | 部署 | 隔离边界 |
| --- | --- | --- |
| 首版 · 裸目录 | Mac/Linux 的 Engine 在本机固定 project 目录启动同账号 Runner 子进程，默认命令 cwd 为 project | 不做安全隔离，文件/网络/进程能力使用本机账号正常权限；任务子目录仅组织文件 |
| 后续 · 容器扩展 | 以后单独接容器或外部 Sandbox 系统 | 当前只保留替换环境实现的边界，不实现镜像、供应、网络或安全隔离 |

Backend 唯一登记 Conversation 的环境、宿主和管理 Engine；首版指定 Engine 建立稳定目录、启动 Runner，回收时停止进程和端点并保留文件。Pi 仍由 Engine 管理，和 Runner 在同一个本机环境。同会话 task 均分配到该 Engine，Agent/Session 不开放特殊 Sandbox 配置；容器供应方留后续。

## 职责与边界

- **身份与配置**　核验 Session、Sandbox 实例及启动身份，使用限定凭据主动向 backend 领取固定资源和当前授权，不直接读取业务数据库。
- **环境准备**　准备工作目录、Skill、CLI 和插件依赖，逐项报告实际准备结果、配置修订及失败原因。
- **本机兼容**　项目及 Runner 账本使用不含进程实例 ID 的持久路径；MCP 默认 loopback 动态端口，资源使用本机绝对路径。Mac/Linux 的进程组、信号、工具依赖及 PTY 分别验收，基本运行不依赖 Linux `/proc`。
- **进程管理**　启动并维护 builtin MCP 与本地 stdio MCP，按执行 Session 管理环境内进程的健康、停止和回收，已确认后台命令可跨正常 Turn 存活；重启后重新观察现场，不沿用旧 PID 推断状态。
- **MCP Gateway**　为每个 stdio 插件提供独立 HTTP MCP 端点，转发协议请求、响应、通知和取消；不聚合工具目录，不代理远程 HTTP MCP，不将传输重连当作业务重试。
- **管理与认证**　向 Engine 提供状态、准备和停止接口。管理凭据、配置领取凭据与 MCP 调用凭据分别授权；MCP Token 随 Sandbox 生命周期固定，不随 Turn 或进程重启轮换，共享环境按执行 Session/授权范围独立绑定；第三方 stdio / CLI 认证仅交给对应插件进程。
- **收尾与恢复**　核对当前管理身份和修订，关闭旧执行入口并收尾；结果未知时保留事实，不能把连接中断当作操作未执行。环境回收不删除 PluginAuth 的持久授权。

Session stop/archive 只关闭其执行窗口并收尾所属进程，不影响其他任务，也不退出整个 Runner。Conversation 归档先由 Backend 关闭全会话准入并收尾所有 Session，管理 Engine 获得回收授权后才关闭环境内 MCP/Runner。活跃会话不自动空闲回收，工作目录和核对证据先保留；详细顺序见[生命周期](../docs/architecture/conversation-lifecycle.md#三层回收)。

详细设计见[Sandbox 常驻管理与 MCP 传输代理](docs/runtime-and-mcp.md)及[builtin MCP 工具复用与接入](docs/builtin-mcp.md)。Pi 直连 Sandbox 内的 builtin MCP；对于 stdio 插件，Pi 连接 Runner 代理；远程 HTTP MCP 由 Pi 直接连接远端。

**内置工具**　六个文件工具 read、write、edit、grep、find、ls 从固定版本 Pi 源码移植；bash_start、bash_poll、bash_write、bash_terminate、bash_list 及进程管理复用用户提供的 TS 归档。新增薄 HTTP 入口，适配可信上下文、UUID 和实际工作路径；保留文件保护、私有 SQLite 及完整活动核对，不修改 Pi。

**实现入口**　Go 管理在 cmd/sandbox-runner 与 internal/runner；[builtin-mcp](builtin-mcp/README.md) 提供独立构建和检查。管理契约见 [contracts/openapi.yaml](contracts/openapi.yaml)。Token 保存在 runner-state/S/token，进程重建沿用；临时控制 socket 使用短路径，持久账本不变。第三方 stdio Gateway 后续实现。

**暂缓范围**　File、Artifact 和发布接口仍未实现；共享项目及每 Session 的工具身份、停止与回收已接入，Sandbox 文件工具不等于平台文件资源。空闲释放或归档 Session 时退出它的 builtin 进程，保留 Token/账本和共享 Runner；普通 stop 保留 Pi/builtin 以支持继续对话。

## 健康检查与退出

进程存活、配置准备和每个 MCP 的实际可用性分别报告。停止时关闭新执行入口并追踪管理范围内进程；远程 MCP 不受 Runner 管理，不能由 Runner 宣告其副作用已停止。清理失败保留可继续处理的状态。

**验收场景**　Runner 自启动获取配置，错误实例不能领取；两个 stdio 插件的端点和请求互不串线；日志不污染协议；旧代次不能重新开放停止窗口。11 个 builtin 工具、文件保护、后台命令、输出恢复、停止与身份适配的验收见[专项设计](docs/builtin-mcp.md#8-移植与验收顺序)。

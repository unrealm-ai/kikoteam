# 裸 Sandbox：Mac / Linux 本地运行

状态：2026-11-16 裸目录最小实现已接通，Mac 本地实际进程验收通过，Linux 入口接 CI 后单独记录结果。实际命令、路径、检查及限制见[运行说明](../../docs/operations/minimal-agent-runtime.md)；下文保留整体目标，Gateway 等超出首版范围的内容不表示已实现。

## 定义与部署边界

首版 Sandbox 就是 **Agent Engine 本机的一个项目目录，加上一组受管理的工具进程**。Engine、Pi、Runner、builtin MCP 和命令使用同一操作系统、同一运行账号；它们是职责不同的本地进程，没有容器、独立用户或文件系统/网络/PID 命名空间。目标为 macOS 和 Linux 本地开发及简单部署，不提供安全隔离能力。

```text
Mac / Linux 本机
  agent-engine
    ├─ 每个执行 Session 的 Pi --mode rpc
    └─ 每个业务会话按需启动的 sandbox-runner
         ├─ builtin MCP（TypeScript / Node）
         ├─ stdio MCP 插件（按 Session/授权绑定管理）
         └─ Bash / Python / Node / Git 等命令
                         ↓
               本地 project/ 目录
```

Runner 仍是独立 Go 组件，本地由 Engine 启动为子进程，不要求开发者另起一个 Sandbox 服务。Pi 由 Engine 启动，不由 Runner 启动；工具通过 MCP 调用，保留未来环境替换边界。Backend 仍创建业务对象、登记唯一环境并分配 Session，Runner 不做业务调度。

项目目录是默认工作位置，不是文件系统访问边界。命令使用运行账号本来的文件、网络和程序权限，不做 chroot、账号切换、cgroup 或资源隔离。平台的资源授权、MCP 调用身份、Session 归属和停止范围仍按业务规则核验；这些机制不承诺阻止同账号代码读取项目之外的文件或其他进程的材料，也不把未提供安全隔离当作本地模式无法启动的条件。

## TaskSpace 增量（2026-11-18，实施中）

普通 Conversation 仍复用会话环境；TaskSpace 内按 Task 独立登记 Sandbox 和 Runner，目录为 `state/sandboxes/workspaces/<workspace_id>/employees/<agent_id>/taskspaces/<space_id>/tasks/<task_id>/{project,runner-state}`。不使用共享的 space project，不复制其他任务文件。范围校验要求 Conversation owner 与 TaskSpace+Task owner 互斥，Runner 以真实 Session 归属核验成员，不能把缺失 Conversation 当通配。

路径和身份的软件隔离不等于 OS 安全隔离：进程仍使用同账号权限。重启保留同一任务目录和环境身份；停止/回收仅处理该任务的环境，不影响同空间其他任务。首版不提供空间文件共享 API。代码与验收状态见 [TaskSpace 设计](../../backend/docs/task-spaces.md)。

## 稳定目录与工作方式

复用部署配置 `KIKO_STORAGE_LOCAL_ROOT`，默认 `~/.kiko/`，启动层展开并规范为绝对路径。以下省略不相关的资产和缓存目录；完整布局见[路径约定](../../docs/architecture/storage-paths.md#5-共享沙箱与本机目录)。

```text
<KIKO_STORAGE_LOCAL_ROOT>/
  state/sandboxes/workspaces/<workspace_id>/employees/<employee_id>/
    conversations/<conversation_id>/
      project/                  # 稳定项目根，默认命令 cwd
        tasks/<task_id>/         # 可选：按任务组织产出，不是隔离边界
      runner-state/             # 稳定环境身份、固定凭据、命令账本和输出
  state/engines/workspaces/<workspace_id>/employees/<employee_id>/
    conversations/<conversation_id>/executions/<session_id>/context/native/
                                # Pi 原生历史和 Engine 身份映射
  runtime/runners/<runner_boot_id>/...
                                # 本次启动的临时配置、端口及连接信息
```

项目目录和持久状态路径不包含 Engine ID、Runner boot ID、assignment 或执行代次。环境身份变化写入元数据，不通过改路径丢弃原文件；重启使用原位置，已登记环境目录缺失时报告不可用，不悄悄创建空目录冒充恢复。首次创建才初始化空项目，不自动复制仓库或安装开发环境。

同一 Conversation 的 task Session 默认以同一个 `project/` 为命令工作目录，共用代码和本机安装的依赖。`tasks/<task_id>/` 仅是可选文件组织方式，不强制每个任务独立项目；共享文件的并发写入由任务协调写入者或串行化，不因路径不同而声称自动避免冲突。模型上下文仍各自保存。

项目目录和 Runner 状态属于必须保留的本地工作数据，不放入可随实例清除的 runtime/cache/tmp。即使配置 S3 保存 Skill 等不可变资产，项目工作盘、SQLite 和 Pi 原生历史仍使用本地文件系统，不上传整棵工作目录或逐轮快照。Web 私聊的目录树、上传、下载和聊天附件使用这个项目目录，接口及部署条件见[私聊项目文件](../../backend/docs/conversation-files.md)。

## 创建与配置

1. Backend 为 Conversation 唯一登记环境身份、宿主和管理 Engine；首版同一 Conversation 的 task Session 固定到这个 Engine。chat 可独立分配且不需要 Runner。
2. Engine 找到或首次建立项目及状态目录，在事务外启动 Runner，传入绝对目录、环境身份、Backend 地址及配置领取凭据。
3. Runner 领取获准配置，核对 Session 绑定，启动 builtin MCP 和需要的 stdio 插件，报告准备结果及实际端点。
4. Engine 装配 Pi 配置并执行输入。后续任务和轮次复用目录及 Runner，空 Conversation/Session 不启动运行进程。

重复准备沿用原身份及实例，结果未知先核对，不创建第二个 Runner。没有环境供应服务、镜像构建、容器调度或为本机组件新增文件传输链路。Engine 内仅收敛准备目录、启动 Runner、查询状态、停止/释放这几项小操作，不预建通用 Sandbox 平台。

本机 Git、Python、Node、Bash 等直接从经启动检查的 PATH 或显式程序路径使用；继承正常用户 HOME、语言、时区、临时目录及明确配置的代理/项目环境。Pi 使用独立的 Session 配置及历史位置，避免污染开发者个人 Pi 会话。平台管理凭据不主动注入普通业务命令；运行配置不是安全隔离机制。程序使用参数数组启动，含空格或中文的路径须正确传递，不拼接未转义 shell 命令。

Engine 与工具可使用同一份已物化的固定版本 Skill 绝对路径，避免重复复制和两套路径映射；S3 资产先下载校验到本机。固定资源不由任务原地升级；这是资源管理约定，不是对同账号命令的文件写入隔离承诺。

## 本机通信

| 项目 | 首版方式 |
| --- | --- |
| builtin MCP 与 stdio Gateway | 监听 `127.0.0.1`，提供 Streamable HTTP，不经过公网 Backend 反向代理 |
| 端口 | 直接绑定端口 0，由系统分配；监听成功后报告真实地址，不先找空闲端口再关闭重绑 |
| Runner 管理 | 使用既有内部管理语义和受限本机通道；builtin 可沿用私有 Unix socket，不增加外部管理服务 |
| 凭据 | Sandbox 生命周期内固定工具 Token，按 Session/范围绑定；管理和配置领取凭据独立 |
| 重启 | 环境身份与 Token 保留；端口可以变化，Engine 重新读取端点、在安全边界重建必要连接，不把旧端口当永久身份 |

Unix socket 使用短的本机 runtime 路径，避免完整业务目录超出平台路径长度；凭据和账本仍保存在稳定状态目录。Runner/builtin 重启不自动重放工具调用。进程身份和端点握手用于防止连错旧实例，不代表本机进程之间有安全隔离。

## 停止、退出和回收

普通命令使用受管子进程/进程组，先请求退出，宽限期后强制结束；保留 Session、origin_turn_id、输出和实际退出状态。macOS/Linux 差异放在薄平台适配中，基本运行不强制依赖 Linux `/proc`，也不使用历史 PID 猜测应杀哪个进程。结果无法核验时保留 unknown/lost，不清空账本报成功；首版不建设跨进程重启的后台接管或自动重放。

- 单个 Task/Session stop 只关闭自身窗口、调用及受管后台命令；主 chat stop 不影响任务。
- 后台命令可跨正常 Turn 存活；仍有后台活动时保留该 Session 实例及容量。
- 活跃 Conversation 的项目目录和共享环境保留，不自动空闲回收；无后台活动的 Pi 可单独释放。
- Conversation 归档先由 Backend 关闭全部准入、推进子级收尾；停稳后授权 Engine 关闭 Runner/MCP 和端点，保留项目文件、Pi 历史及核对证据。
- Engine 正常退出先停止接单并收尾 Pi、Runner 和工具进程；服务退出不等于业务归档，不删环境登记。重启后在原宿主核对现场，不自动恢复旧 Turn。

目录分组和进程组用于工作管理，不承诺捕获主动脱离进程组的所有程序。对残留或身份不明的工作保留诊断及人工核对入口，不以“裸目录环境销毁”宣称操作系统已经清除全部执行。

## 本地启动与跨平台验收

安装依赖、配置 PostgreSQL 和模型后，使用 `python3 tools/runtime.py prepare` / `run` 构建并启动原生 Backend、Event Hub、Engine，Runner 由 Engine 按需启动。数据库可以连接已有实例；根 start.sh 仍是另一套 Compose 入口，不包含执行链。参数和检查见运行说明。

启动检查明确报告 Go/Node、Bash、`rg`、`fd` 等构建/运行依赖及版本，不能只报告“环境失败”。普通命令默认 pipe；PTY 是单独验证的交互能力，运行时按需加载，不可用时该模式明确报错，不能伪装为 pipe。依赖安装方式和可选打包需要在实现时一起调整，不能仅改 README 声称原生依赖已可选。

**跨平台实现**：builtin 入口允许 darwin/linux，test:process 在本机执行通用与真实进程/PTY 验收；Linux 专有 /proc 竞争用例在 Mac 显式跳过。Runner 传入稳定 project 和持久账本的绝对路径，并为管理/命令监督 socket 使用私有短临时目录。Mac 的本批结果已记录，Linux 本批结果待 CI，不能互相替代。

两平台都需完成：11 工具的适用能力、HTTP 调用、普通命令输入输出、后台进程跨轮、Session 定向停止、TERM/KILL、中文/空格路径、依赖缺失、动态端口、正常退出、异常状态可诊断、原生历史继续及项目保留。PTY 在声明支持的平台单独验收；不以跳过用例宣称整个平台完成。不修改上游 Pi。

## 容器只保留扩展边界

后续可以为 Engine 的小型环境模块增加容器或外部 Sandbox 系统实现，仍返回环境身份、Runner 端点和工作盘信息。容器生命周期、镜像、挂载、网络和安全隔离到该阶段单独设计；当前不实现容器供应方、统一控制服务或 Agent/Session 的镜像参数。业务关系、Session/Input/Turn 和 MCP 调用方式继续复用。

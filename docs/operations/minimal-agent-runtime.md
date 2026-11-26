# 最小 Agent 会话底座运行说明

本期是后端 API 与本机执行链。Backend 管 Conversation、Session、Input、Turn、事件和分配；Engine 管未修改的 Pi 1.5.2 原生 RPC；一个 Conversation 的 task 共用一个裸 Runner/project，每个 task Session 有独立 Pi 历史、builtin 进程、账本和固定 Token。chat 仅挂 Backend task_delegation，不挂 Sandbox 工具。后续已接通能力包、Skill 与静态认证远程 MCP；stdio Gateway、前端会话页、自动回报/唤醒及自研上下文压缩仍暂缓。

## 本机启动

已验证环境为 macOS arm64、Go 1.27.2、Node 26.12.0、PostgreSQL 16.15。依赖还包括 npm、make、Bash、Python 3、rg、fd；Linux CI 使用固定 Node 24.18.1。Mac 可用 Homebrew 安装 PostgreSQL、ripgrep、fd；Linux 的 fd-find 需要提供名为 fd 的可执行入口。基本运行不要求 Docker、root 或额外 Sandbox 服务。

```sh
python3 tools/runtime.py prepare
# 在本机环境设置，以下只表示变量，不是可直接使用的凭据：
export DATABASE_URL='<本机 PostgreSQL DSN>'
export KIKO_AUTH_SECRET='<32 字节以上、与既有数据库一致的认证密钥>'
export KIKO_ENGINE_TOKEN='<32 字节以上的内部部署密钥>'
export KIKO_STORAGE_LOCAL_ROOT='/绝对路径/持久状态根'
python3 tools/runtime.py run
```

启动器不创建数据库、不替换既有密钥，不生成模型配置；Event Hub 持锁执行全局迁移。Backend 与 Engine 独立启动，初始依赖缺失时后续请求/轮询可恢复。默认 Backend 为 `127.0.0.1:8080`，Origin 为 `http://localhost:8080`，Engine 使用 `KIKO_BACKEND_URL`（默认 `http://127.0.0.1:8080`）。改端口时同时配置 `KIKO_HTTP_ADDRESS`、`KIKO_PUBLIC_ORIGIN`、`KIKO_BACKEND_URL`。Event Hub 探针端口默认 8081，可通过 EVENT_HUB_HEALTH_ADDR 调整。

Ctrl-C 依次停止 Engine、Backend、Event Hub；Engine 先关闭 Pi、停止 Session 工具并释放分配，再退出 Runner，数据保留。裸环境使用当前账号，命令可访问该账号本来能访问的文件和网络；目录和凭据分开组织不提供安全隔离。

先用现有 Platform API 注册/登录、创建工作区、测试并保存模型，再创建绑定模型的员工。工作区默认设置可保留作创建预选，并非聊天的启动依赖。端点和请求见 [Backend OpenAPI](../../backend/contracts/openapi.yaml)；不预置账号或模型 key。新建 chat 与 task 均使用员工所选模型并固定创建时目标；chat 不回退到工作区或官方默认。执行时只领取当前有效凭据。

## 会话最小操作

沿已登录的 Cookie/CSRF 调用工作区 API，`W`、`A`、`C`、`S` 分别是工作区、员工、业务会话、执行会话 ID：

```text
POST /api/v1/workspaces/W/conversations
  {"agent_id":"A"}
POST /api/v1/workspaces/W/sessions
  Idempotency-Key: first-input-001
  {"conversation_id":"C","mode":"chat","input":{"text":"你好"}}
POST /api/v1/workspaces/W/sessions/S/inputs
  Idempotency-Key: second-input-001
  {"text":"继续"}
GET  /api/v1/workspaces/W/sessions/S/events
GET  /api/v1/workspaces/W/sessions/S/events/stream
POST /api/v1/workspaces/W/sessions/S/stop
  {}
POST /api/v1/workspaces/W/conversations/C/archive
  {}
```

task 的创建请求将 mode 改为 task，保持同一个 Conversation。通常主 chat 使用 task_delegation 的 create_session 建立；工具响应仅代表已受理，调用 get_session 查询实际进度/结果。继续追问用 send_message，仍是原 Session 的新 Input；同一次重试沿用 request_key。首版只支持当前员工，跨员工请求明确拒绝，任务自身不能递归委派。主 chat 停止不会停止已委派任务，Conversation 归档才会收尾全部 Session。

可靠历史及 SSE 来自同一事件表；首版发送完整消息和工具/状态快照，不发送临时文本 delta。读取历史固定高水位，通过 resume_cursor / Last-Event-ID 接上实时流；断流不停止 Turn。只有 Input 提交做必要去重，stop 每次按当前已受理范围停止，不需要 key。

## 进程、目录与凭据

下列目录用于诊断现行二进制。共享挂载盘的 `temp/` 保存上传回执和实例锁，Runner 启动配置及 socket 位于根外的进程私有临时目录。各模式的完整路径和旧开发会话切换步骤见[存储目录设计](../architecture/storage-paths.md)。

```text
<root>/workspaces/W/
  projects/conv-C/                 # 会话任务共享项目
  projects/task-T/                 # TaskSpace 独立项目
  sessions/S/pi/                   # Pi 身份和原生 JSONL
  sessions/S/runner/token          # 固定 MCP Token
  sessions/S/runner/processes/     # SQLite 账本和初始化标记
<root>/temp/uploads/               # 项目上传回执和 Skill 导入暂存
<root>/temp/runtime/               # Engine 生命周期锁
```

Runner 的 Backend 配置领取 token 只允许读取指定环境/管理 Engine 的配置；Engine→Runner 管理随机 token 与 MCP token 各自独立。builtin 仅监听 loopback 动态端口；每 Session 使用独立进程和 Token，可信执行窗口带 assignment/generation/Harness/Turn。工具参数不能覆盖这些身份。管理路径见 [Runner 契约](../../sandbox-runner/contracts/openapi.yaml)，Backend 内部注册/分配/环境配置见[内部契约](../../backend/contracts/internal/openapi.yaml)。

Backend 每秒扫描待办/分配，并先拒绝停用/旧受理代次或已撤权主体的排队 Input，不为其启动执行资源。Engine 每秒取自己的分配，每 Session 串行领取。每个 Engine 默认最多同时绑定 100 个 Session（KIKO_ENGINE_CAPACITY）；无活动和后台工作后默认 1 分钟释放 Pi、所属 builtin 与分配（KIKO_ENGINE_IDLE_TIMEOUT，至少 1s）。Token 和账本保留，后续 Input 加载原生历史并重建端点。Runner 独立常驻；父归档、全部 Session 停稳并释放后回收 Runner，project 与历史继续保留。

正常退出后新 Engine 可在相同 host_id 取得已确认 stopped 的环境，加载原生历史；MCP Token 不轮换。原生数据不可用不重建空会话。意外失联且旧执行结果未知时不自动接管，不按历史 PID 杀进程；保留分配/账本并报告无法确认，需先人工核对。没有自动检查点、自动重放或跨机恢复。

## 验证入口

```sh
make lint
make -C agent-engine check
make -C sandbox-runner check
make -C sandbox-runner/builtin-mcp check
# 使用测试库；每项 PG 测试在自己的 schema 重放迁移并清理：
BACKEND_TEST_DATABASE_URL='<测试 DSN>' KIKO_RUNTIME_E2E=1 make -C backend check
```

普通进程回归使用真实 Backend HTTP/PG、独立 Engine/Runner、未修改 Pi 原生 RPC、builtin HTTP MCP 和真实文件/Bash/PTY；模型为本地确定性 OpenAI 协议服务。覆盖多轮/重启、排队和停止、历史/SSE、共享目录、独立上下文、后台保留、定向停止、固定 Token、归档及查询式委派。

以下真实供应商验收发生于移除 pi-acp 之前；现已切换为 Pi 原生 RPC。

2026-11-17 另以 DeepSeek 的 deepseek-flash 完成真实供应商验收：生产模型验证/保存、chat 多轮及 Engine 重启后的原生历史、task 实际 write/Bash、主 chat 委派及查询、共享项目与归档。该用例显式启用才发送计费请求，普通 make check 不调用供应商：

```sh
# 私密根 .env（0600）或进程环境设置 DEEPSEEK_API_KEY、DEEPSEEK_BASE_URL、DEEPSEEK_MODEL。
# 这些是显式启用的测试配置，不属于 .env.example，也不配置官方模型。
BACKEND_TEST_DATABASE_URL='<测试 DSN>' python3 tools/runtime.py verify-model
```

verify-model 读取本地 .env 中这三个字段，已注入的环境值优先，不执行 source 或把凭据放入命令参数；使用隔离 PG schema 与临时状态并在退出时清理。正常运行的模型资源仍通过 Platform 管理，不因 .env 自动建立业务工作区或员工。Mac 真实 DeepSeek 验收与 Linux 确定性模型执行链路 CI 已通过；这些结果不代表所有供应商和协议均已验收。

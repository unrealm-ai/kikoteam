# 参与贡献指南（Contributing Guide）

感谢你对 KIKO 企业级AI员工云平台的关注与贡献！参与本项目即表示你同意遵守我们的 **[贡献者行为准则（CODE_OF_CONDUCT.md）](CODE_OF_CONDUCT.md)**。本指南说明如何搭建本地原生开发与测试环境、运行静态检查与自动化测试，以及提交 Issue 和 Pull Request 的规范。

## 1. 仓库组件与文档导览

修改代码前，请先阅读对应组件的 README 了解其职责边界：

| 组件目录 | 语言与技术栈 | 说明入口 |
| --- | --- | --- |
| [`web/`](web/README.md) | Vue 3.5 + TypeScript + Vite + Naive UI | 工作台与管理控制台前端 |
| [`backend/`](backend/README.md) | Go 1.26+（CI 固定 Go 1.27.2） | 平台管理（`platform`）、会话与任务调度（`manager`）、对外入口（`webgw`） |
| [`event-hub/`](event-hub/README.md) | Go 1.26+ + Goose SQL 迁移 | PostgreSQL Advisory Lock 选主、全局数据库自动迁移、IM 长连接与定时触发 |
| [`agent-engine/`](agent-engine/README.md) | Go 1.26+ + `@earendil-works/pi-coding-agent` 1.5.2 | 会话执行引擎，通过 `pi --mode rpc` 托管模型与工具调用 |
| [`sandbox-runner/`](sandbox-runner/README.md) | Go 1.26+ + TypeScript [`builtin-mcp`](sandbox-runner/builtin-mcp/README.md) | 沙箱环境管理进程与 11 个内置文件/受管 Bash MCP 工具 |

> [!TIP]
> **使用 AI 编码代理开发**：本仓库在根目录维护了面向编码代理的 [AGENTS.md](AGENTS.md) 与 [AI_RULES.md](AI_RULES.md)（按任务索引前端、后端、数据库与运维规则）。如果你使用 AI 助手编写或评审代码，请让其从 `AGENTS.md` 开始按需读取规则。

## 2. 本地开发环境要求

如果仅体验或验证整体功能，直接运行 `./start.sh up` 通过 Docker Compose 构建即可。若需在宿主机直接编译、运行单测和静态检查，请准备：

- **Go**：`1.26+`（CI 使用 `1.27.2`）
- **Node.js & npm**：`Node 24+`（Linux CI 使用 `24.18.1`）
- **Python 3**：`3.10+`（用于启动脚本与 OpenAPI 契约校验虚拟环境）
- **PostgreSQL**：`16+`（用于运行带真实数据库的集成与并发 race 测试）
- **命令行工具**：`git`、`make`、`bash`、`rg`（ripgrep）、`fd`（Linux 上 `fd-find` 需提供 `fd` 命令入口），以及编译 `node-pty` 所需的 C++ 基础构建工具

### 初始化开发校验依赖

```sh
# 1. 初始化 Event Hub 的 Python 虚拟环境（提供 openapi-spec-validator）
python3 -m venv event-hub/.venv
event-hub/.venv/bin/pip install -r event-hub/requirements-dev.txt

# 2. 安装 Node 组件依赖
(cd sandbox-runner/builtin-mcp && npm ci && npm rebuild node-pty)
(cd agent-engine && npm ci)
(cd web && npm ci)
```

> Go 静态检查工具（`golangci-lint`、`actionlint`）在运行 `make lint` 时会按 `tools/versions.mk` 中锁定的版本自动下载并安装到仓库的 `.tools/` 目录。

## 3. 原生进程联调（不通过 Docker）

当你希望在宿主机直接启动原生二进制进行调试时，可使用 `tools/runtime.py`：

```sh
# 编译各组件
python3 tools/runtime.py prepare

# 配置本机 PostgreSQL 与大于 32 字节的密钥后启动
export DATABASE_URL='postgres://localhost:5432/kikoteam_dev?sslmode=disable'
export KIKO_AUTH_SECRET='replace-with-at-least-32-bytes-secret-key'
export KIKO_ENGINE_TOKEN='replace-with-at-least-32-bytes-engine-token'
export KIKO_STORAGE_LOCAL_ROOT="$HOME/.kiko-dev"
python3 tools/runtime.py run
```

详细参数与最小 API 调试示例见 [最小会话底座运行说明](docs/operations/minimal-agent-runtime.md)。

## 4. 格式化、静态检查与测试

仓库根目录的 `Makefile` 提供了与 GitHub Actions CI 完全一致的检查入口：

```sh
# 运行全仓静态检查（含 GitHub Actions 工作流、Go 代码、TS 代码与 OpenAPI 契约）
make lint

# 仅检查单个组件
make lint COMPONENT=backend
make lint COMPONENT=event-hub
make lint COMPONENT=agent-engine
make lint COMPONENT=sandbox-runner
make lint COMPONENT=sandbox-runner/builtin-mcp

# 显式格式化代码
make fmt
make fmt COMPONENT=backend
```

### 运行完整测试与数据库验收（`make check`）

`backend` 和 `event-hub` 的 `make check` 包含针对真实 PostgreSQL 的事务、约束与并发 `-race` 测试，**不会以跳过数据库测试冒充通过**。运行前需提供专用的一次性测试数据库 DSN：

```sh
# Backend 完整检查（lint + OpenAPI 契约 + 真实 PG race 测试）
BACKEND_TEST_DATABASE_URL='postgres://localhost:5432/kiko_backend_test?sslmode=disable' \
  make check COMPONENT=backend

# Event Hub 完整检查（lint + 迁移快照校验 + 真实 PG 选举与迁移测试）
EVENT_HUB_TEST_DSN='postgres://localhost:5432/kiko_hub_test?sslmode=disable' \
  make check COMPONENT=event-hub

# Agent Engine 与 Sandbox Runner 检查
make check COMPONENT=sandbox-runner/builtin-mcp
make check COMPONENT=sandbox-runner
make check COMPONENT=agent-engine

# Web 前端类型检查、单元测试与构建检查
(cd web && npm test && npm run build)

# 启动器与 Docker 辅助脚本单元测试
python3 -m unittest discover -s tools -p 'test_*.py' -v
```

具体复杂度阈值、lint 规则与测试豁免原则见 [静态检查规范](docs/rules/static-checks.md)。

## 5. 核心工程约定

1. **数据库迁移统一归 `event-hub`**：所有 PostgreSQL schema 变更必须以编号迁移文件加入 `event-hub/database/postgres/migrations/`，并按 [event-hub 迁移说明](event-hub/README.md#迁移文件与生成) 更新结构快照。`backend` 和 `agent-engine` 不执行 schema 迁移。
2. **契约先行**：修改 HTTP API 或内部契约时，同步更新对应的 OpenAPI 规范文件并确保契约校验通过。
3. **Pi 原生能力优先**：`agent-engine` 优先通过未修改的公开 `pi --mode rpc` CLI、配置和事件对接，不维护私有 Pi 源码 fork 或侵入式补丁。
4. **幂等与并发安全**：写接口、后台重放、定时触发和渠道事件投递须遵守 [后端开发规则](docs/rules/backend.md) 中的事务与幂等要求。

## 6. 提交信息与 Pull Request 流程

1. **分支与粒度**：每个 PR 聚焦一个清晰的功能或缺陷修复，以可观察的行为为单位。
2. **Commit Message 风格**：推荐使用 Conventional Commits 风格（中英文均可，保持清晰简明），例如：
   - `feat(catalog): add Tavily search plugin and skill with API key authentication`
   - `fix(runtime): recover queued tasks across engine restarts on shared storage`
   - `docs: rewrite open-source README and user guides`
3. **提交前自检清单**：
   - [ ] `make lint` 全部通过（如修改了 `web/`，前端 lint 与构建通过）
   - [ ] 受影响组件的 `make check`（含真实隔离 PostgreSQL 测试）通过
   - [ ] 若生成文件（如 schema 快照、派生类型）发生变化，已通过源定义重新生成并提交
   - [ ] 在 PR 描述中写明改动背景、验证方式（实际执行的检查命令）以及任何已知限制或兼容性影响

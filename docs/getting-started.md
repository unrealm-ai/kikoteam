# 5 分钟快速上手

本指南带你使用 Docker Compose 在本地一键启动 KIKO 企业级AI员工云平台，完成管理员注册、模型配置、AI 员工创建，并跑通第一条后台任务。

## 1. 环境要求

- **操作系统**：macOS（13.3+，支持 Apple Silicon 与 Intel）或 Linux（推荐 Ubuntu 22.04+ / Debian 11+）
- **必需工具**：
  - `python3`（系统自带即可）
  - **Docker Engine** 与 **Docker Compose v2**（支持 `docker compose up --wait`）
- **硬件建议**：至少 2 核 CPU、4 GB 可用内存、10 GB 磁盘空间
- **网络要求**：首次启动需拉取基础镜像（PostgreSQL 16、Go、Node.js）并访问你所配置的 LLM 模型 API

> [!TIP]
> 如果本机尚未安装 Docker，直接运行 `./start.sh` 会检测系统版本并提供从 Docker 官方源下载安装的交互式确认引导，不会静默提权或修改系统配置。

## 2. 启动本地服务

克隆仓库并运行启动脚本：

```sh
git clone https://github.com/unrealm-ai/kikoteam.git
cd kikoteam
./start.sh
```

首次运行 `./start.sh` 时，启动器会依次完成以下工作：

1. **生成本地配置**：在根目录生成权限为 `0600` 的 `.env`（包含随机生成的数据库密码与 `KIKO_AUTH_SECRET`）和 `.kiko-local/runtime.env`（内部 `KIKO_HUB_TOKEN` 与 `KIKO_ENGINE_TOKEN`），并在 `~/.kiko` 初始化持久资产与沙箱工作目录。
2. **构建应用镜像**：在容器内编译 `web`、`backend`、`event-hub`、`agent-engine` 与 `sandbox-runner`。
3. **启动与自动迁移**：
   - 启动 `postgres:16.6` 与 `backend`；
   - 启动 `event-hub`，通过 PostgreSQL Advisory Lock 自动执行数据库 schema 迁移；
   - 运行 `template-catalog` 同步内置的 7 个官方 AI 员工模板、11 个官方 Skill 与 11 个官方 MCP 插件；
   - 启动 `agent-engine` 并向 `backend` 完成注册与心跳确认。

当终端输出访问地址与 Git 版本号后，在浏览器打开：

```text
http://localhost:18080
```

<!-- TODO(开源素材): 在此处补充首次启动成功后的登录/工作台首页截图 docs/assets/quickstart-workbench.png -->

## 3. 注册首个管理员并初始化工作区

1. 打开 `http://localhost:18080`，点击**注册**。
2. 全新本地部署默认无需配置 SMTP 邮件服务器，直接输入邮箱与密码即可完成注册并创建你的第一个工作区。

> [!IMPORTANT]
> **平台管理员初始化规则**：整个实例中**第一个成功注册并登录的账号**会自动获得平台管理员（`is_platform_admin`）权限，可管理官方精选模板并为其他账号授予平台管理员权限。

## 4. 配置第一个大模型

AI 员工需要绑定模型配置才能对话和执行任务：

1. 在左侧导航栏进入「**资源库 → 模型**」，点击「**新建模型**」。
2. 填写模型信息：
   - **名称**：例如 `DeepSeek V3`、`GPT-4o` 或 `Claude Sonnet`
   - **协议类型**：按服务商选择 `Chat Completions`、`Responses` 或 `Anthropic Messages`
   - **服务地址（Base URL）**：例如 `https://api.deepseek.com` 或 `https://api.openai.com/v1`
   - **模型标识（Model ID）**：例如 `deepseek-chat`
   - **API Key**：填入你在对应平台申请的密钥（加密存储于数据库中，保存后不回显）
3. 点击「**测试连接**」，确认返回连通回执后保存。

<!-- TODO(开源素材): 在此处补充「资源库 → 模型」新建与测试连接截图 docs/assets/quickstart-model-setup.png -->

## 5. 从模板创建你的第一位 AI 员工

最快的方式是从内置的「AI 员工市场」一键创建：

1. 在左侧导航栏点击「**AI 员工市场**」。
2. 浏览官方精选模板（如「**经营数据分析师**」「**行情简报员**」「**研发交付助理**」「**会议跟进专员**」），点击任一模板卡片查看其岗位职责、预设的 Skill / MCP 插件以及交付样例。
3. 点击「**使用此模板创建**」：
   - **第一步（岗位设定）**：可修改员工名称（如「财务组数据分析师」）和具体项目/团队参数；
   - **第二步（模型与接入）**：选择刚刚配置好的模型；模板预设的能力包、Skill 和插件操作会自动整体带入，无需手动逐个勾选；
   - **第三步（确认创建）**：完成创建。

> 如果模板中包含需要 API Key 的远程 MCP 插件（例如「Tavily 联网搜索」或「GitHub」），可以在员工详情页的「**技能与插件**」页签下为其绑定账号凭据；免认证插件（如「A 股行情快查」「Exa 公开资料检索」）和内置沙箱工具无需额外配置即可直接使用。

<!-- TODO(开源素材): 在此处补充「从模板创建 AI 员工」三步向导截图 docs/assets/quickstart-create-employee.png -->

## 6. 发起对话与委派后台任务

1. 进入「**AI 员工**」名录，点击刚创建的员工进入**对话工作台**。
2. **即时对话 vs 后台任务**：
   - 发送简单问题时，主对话（`chat`）会直接回复；
   - 发送需要多步执行、读写文件、运行脚本或检索外部数据的工作（例如：「*帮我查询今日上证指数和贵州茅台行情，并在工作目录生成一份 Markdown 收盘简报*」），主对话会为你创建或委派一条**后台任务（Task）**。
3. **上传项目文件（可选）**：
   - 在 Web 私聊右侧可以打开**项目目录树**，将本地 Excel、CSV、代码或文档拖拽上传到当前会话的共享项目目录（`project/`）；
   - 在聊天框中引用已上传的文件，AI 员工的后台任务即可直接在沙箱内通过内置文件工具或 Python/Bash 脚本读取并处理这些文件。
4. **追踪进度与补充要求**：
   - 后台任务运行期间，你仍可以在主对话中继续和员工聊天；
   - 点击任务卡片或前往左侧「**任务**」中心，可以实时查看工具调用过程、在任务运行中**追加补充要求**（默认 `steer` 即时引导当前轮次，也可排队）、**停止**任务或下载生成的产物文件。

## 7. 日常启停与数据备份

```sh
# 查看服务健康状态
./start.sh status

# 查看实时日志
./start.sh logs

# 使用当前本地代码构建并启动，不拉取 Git 更新
./start.sh

# 快进拉取当前分支的上游，再构建并启动
./start.sh --update

# 停止容器（保留所有数据库数据与本地文件）
./start.sh down
```

> [!WARNING]
> **切勿轻易删除 `.env` 和 `.kiko-local/`**：`.env` 中的 `KIKO_AUTH_SECRET` 用于加密数据库中保存的所有模型 API Key 与插件凭据。一旦丢失且已有数据库卷存在，启动器会拒绝生成新密钥以防损坏既有加密数据。备份数据库（`kikoteam-local-postgres-data` 卷）时，请务必同时备份 `.env`、`.kiko-local/` 以及 `~/.kiko` 目录。

## 下一步

- 想自定义员工岗位指令、导入自有 Skill 或接入内部 MCP 服务？阅读 **[AI 员工、能力包、Skill 与 MCP 插件指南](guides/employees-and-capabilities.md)**。
- 想把 AI 员工拉进飞书、钉钉、企业微信群，或设置每天定时跑周报？阅读 **[渠道接入、自动化触发与人机协同指南](guides/channels-and-collaboration.md)**。
- 准备部署到团队服务器？阅读 **[部署与配置指南](deployment.md)** 与 **[安全说明](../SECURITY.md)**。

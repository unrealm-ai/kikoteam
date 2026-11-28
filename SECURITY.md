# 安全模型与安全部署指南（Security Policy）

KIKO 企业级AI员工云平台支持 AI 员工执行代码、读写文件和调用外部业务系统。在自托管或向团队开放使用前，请仔细阅读本安全模型说明。

## 1. 沙箱与代码执行隔离边界（重要）

> [!CAUTION]
> **当前版本的沙箱（`sandbox-runner` 与 `builtin-mcp`）不提供操作系统级或容器级安全隔离。**

- **运行机制**：每个业务会话（Conversation）或任务空间（TaskSpace）在 `KIKO_STORAGE_LOCAL_ROOT` 下拥有独立的项目目录（`project/`），但 `agent-engine`、`pi`、`sandbox-runner`、`builtin-mcp` 以及 AI 员工通过 `bash_start` 执行的 Shell / Python / Node / Git 命令，**全部运行在同一操作系统（或同一个 `agent-engine` 容器）的同一个运行账号（UID/GID）下**。
- **没有 OS 级强边界**：当前未对单次任务启用独立容器、chroot、独立 Linux Namespace、cgroup 或 seccomp 限制。因此，由 AI 员工执行的命令在操作系统层面拥有与 `agent-engine` 进程相同的文件系统与网络访问权限。
- **平台层面的软件防护**：
  - 主对话（`chat` Session）不挂载任何沙箱文件或 Bash 工具；
  - 平台管理密钥（如 `KIKO_AUTH_SECRET`、`DATABASE_URL`、S3 密钥）不会注入业务命令环境变量，S3 密钥仅注入 `backend` 而不注入 `agent-engine`；
  - 每个执行 Session 的工具调用均绑定固定生命周期的内部 Token 与会话范围校验，单个 Session 停止或终止时仅按进程组清理本 Session 启动的受管子进程。
- **安全部署建议**：
  1. 仅向受信任的内部团队成员授权具备 Bash / 脚本执行能力的 AI 员工；
  2. 切勿以 `root` 账号运行 `agent-engine`（官方镜像默认使用非特权普通 UID）；
  3. 生产环境应将 `agent-engine` 部署在专用的工作节点或隔离容器中，与核心基础设施隔离。

## 2. 凭据保护与密钥管理

- **静态加密存储**：用户在工作台配置的模型 API Key、MCP 插件静态凭据（API Key / Bearer Token）与 OAuth 令牌均使用 `KIKO_AUTH_SECRET`（至少 32 字节）加密后存入 PostgreSQL，前端界面与 API 响应永不回显明文凭据。
- **内部服务鉴权**：
  - `backend` 与 `event-hub` 之间通过 `KIKO_HUB_TOKEN`（32 字节以上）鉴权；
  - `backend` 与 `agent-engine` 之间通过 `KIKO_ENGINE_TOKEN`（32 字节以上）鉴权；
  - 内部 MCP 调用（如任务委派、人工确认、事项交接）使用绑定到具体 `Session` / `Input` / `Assignment` 执行代次的短期专用 Bearer 凭据，且严格拒绝浏览器 Cookie 与 Origin 请求。
- **日志脱敏**：模型提示词正文、工具参数、文件内容以及各类认证材料不会写入普通运行日志。

## 3. 身份、注册与两层权限模型

1. **平台管理员初始化**：实例启动后**第一个成功注册并登录的用户**自动成为平台管理员（`is_platform_admin`）。请在受控网络完成首个管理员登录，再开放公网入口，防止外部用户抢先取得权限。
2. **注册策略加固**：
   - 全新部署默认允许免邮箱验证的密码注册；
   - **面向公网或企业正式部署时**，请先在「设置 → 平台管理 → 邮件与注册」配置并测试邮件，再启用强制邮箱验证和定向邮箱邀请。
3. **两层业务授权**：
   - **员工级使用范围**：通过白名单/黑名单与「群内可用」开关控制哪些人员、飞书/钉钉部门或已核验企微联系人可以与 AI 员工交互；
   - **能力包分场景授权**：每个能力包独立配置私聊、群聊、自动化触发器三个场景的启停与适用范围；运行时在每个轮次（Turn）和每次工具调用前重验当前用户权限与插件账号状态，停用员工或撤销插件授权会立即阻断未开始的调用。

## 4. 网络安全与反向代理边界

1. **必须屏蔽内部 HTTP 路径**：`backend` 的 `/api/internal/v1/` 以及 `/mcp/task-delegation`、`/mcp/task-space-history`、`/mcp/task-progress`、`/mcp/human-confirmations/`、`/mcp/work-items/` 仅供集群内部调用。**公网反向代理必须显式拦截这些路径（返回 403）**，配置示例见 [部署指南](docs/deployment.md#2-反向代理与必须屏蔽的内部路径)。
2. **远程 MCP SSRF 防护**：当用户在资源库配置远程 HTTP/HTTPS MCP 地址时，Backend 默认拦截所有指向环回地址（`127.0.0.0/8`、`::1`）、链路本地地址（`169.254.0.0/16` 等云厂商元数据地址）以及私有内网网段的请求。仅当管理员在环境变量 `KIKO_MCP_PRIVATE_CIDRS` 中显式列入允许的内网 CIDR 时，才允许连接对应私网网段内的自建 MCP 服务（环回与链路本地地址始终禁止）。
3. **HTTPS 与 Origin 校验**：生产环境必须通过 HTTPS 暴露 `KIKO_PUBLIC_ORIGIN`。非 localhost 的纯 HTTP IP 访问默认被拒绝（除非内网测试显式开启 `KIKO_ALLOW_HTTP_IP_ORIGIN=true`）。

## 5. 生产环境安全检查清单

- [ ] 启动后已第一时间注册首个账号，确认取得平台管理员权限
- [ ] 已在平台管理开启邮件和强制邮箱验证（或限制网络仅内网可达）
- [ ] `KIKO_AUTH_SECRET`、`KIKO_HUB_TOKEN`、`KIKO_ENGINE_TOKEN` 均为独立生成的 32 字节以上高熵随机值
- [ ] 公网反向代理已配置 TLS，并已屏蔽 `/api/internal/v1/` 与 `/mcp/*` 内部路径
- [ ] PostgreSQL、`event-hub`（8081/8082）、`agent-engine`（8083）端口均未暴露到公网
- [ ] 已定期备份 PostgreSQL 数据库、`.env`、`.kiko-local/` 与持久存储目录

## 6. 报告安全漏洞

如果你在 KIKO 中发现了潜在的安全漏洞，**请勿在公开的 GitHub Issue 中直接披露利用细节**。请通过 GitHub 仓库的 **Security Advisories（Report a vulnerability）** 私密提交漏洞报告，附上复现步骤、影响范围与相关配置说明，我们会尽快确认并处理。

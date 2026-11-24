# KIKO 文档中心

欢迎查阅 KIKO 企业级AI员工云平台文档。你可以按自己的角色选择阅读路径：

## 1. 快速上手与使用指南（面向使用者与工作区管理员）

| 文档 | 内容概览 |
| --- | --- |
| [5 分钟快速上手](getting-started.md) | 使用 `./start.sh` 一键启动本地环境、注册首个管理员账号、配置模型、从模板创建第一位 AI 员工并委派后台任务 |
| [AI 员工、能力包、Skill 与 MCP 插件指南](guides/employees-and-capabilities.md) | 理解 AI 员工与能力包模型、导入版本化 Skill、配置远程 MCP 与 OAuth、设置私聊/群聊/触发器使用范围、发布 AI 员工模板 |
| [渠道接入、自动化触发与人机协同指南](guides/channels-and-collaboration.md) | 绑定飞书/钉钉/企业微信机器人、跨群历史共享、配置 Cron 定时与 Webhook 触发器、处理后台任务人工确认与跨员工事项交接 |

## 2. 部署、运维与安全（面向自托管运维人员）

| 文档 | 内容概览 |
| --- | --- |
| [部署与配置指南](deployment.md) | 本地 Compose 与生产多实例拓扑、反向代理内部路由屏蔽规则、全量环境变量参考表、Event Hub 数据库自动迁移、升级与备份恢复 |
| [安全模型与安全指南](../SECURITY.md) | 沙箱执行边界说明（同账号进程、无 OS 容器隔离）、凭据加密、权限控制层级、私网 MCP SSRF 防护与安全部署清单 |
| [Backend 启动配置详解](../backend/docs/configuration.md) | `./start.sh` 启动器保护机制、Docker 安装辅助、SMTP 邮件与注册验证策略、S3/本地资产存储细节 |
| [最小会话底座原生运行说明](operations/minimal-agent-runtime.md) | 不使用 Docker，直接在 macOS / Linux 宿主机编译并运行 Backend、Event Hub、Agent Engine 与 Sandbox Runner |

## 3. 架构设计与二次开发（面向贡献者与开发者）

| 文档 | 内容概览 |
| --- | --- |
| [参与贡献指南](../CONTRIBUTING.md) | 开发环境依赖、代码静态检查（`make lint` / `make check`）、隔离 PostgreSQL 测试、提交规范与 PR 流程 |
| [设计与开发文档总索引](design-index.md) | 仓库内所有产品设计、系统架构、API 契约、状态机与业务流程文档的完整分类索引 |
| [存储目录设计](architecture/storage-paths.md) · [当前存储配置](architecture/storage-configuration.md) | 确认的资产、项目与 Session 目录重构，及现行版本的存储配置和备份范围 |
| [整体架构速览](architecture/README.md) | 五大组件（`web`、`backend`、`event-hub`、`agent-engine`、`sandbox-runner`）交互关系与执行链路 |
| [领域模型](architecture/domain-model.md) · [会话生命周期](architecture/conversation-lifecycle.md) | `Agent`、`CapabilityPackage`、`Conversation`、`Task`、`Session`、`Input`、`Turn` 与 `Sandbox` 的边界与状态转换 |
| [编码代理入口（AGENTS.md）](../AGENTS.md) · [工程开发规则（AI_RULES.md）](../AI_RULES.md) | 跨组件工程规范、Go/Vue/数据库/运维约束与验收准则 |

## 功能状态

下表汇总当前主线版本各项能力的实现状态：

| 领域 | 功能 | 当前状态 | 备注 |
| --- | --- | --- | --- |
| **账号与工作区** | 邮箱密码注册/登录、可选邮箱 OTP 验证、工作区与成员邀请 | ✅ 已实现 | 首个登录用户自动成为平台管理员；支持定向邀请链接 |
| **模型管理** | OpenAI Chat Completions / Responses、Anthropic Messages 三协议接入与连通性诊断 | ✅ 已实现 | 支持官方模型代理与积分结算（`modelproxy`） |
| **AI 员工与市场** | 从模板/空白创建员工、7 个官方精选模板、工作区模板发布与版本管理 | ✅ 已实现 | 模板采用复制创建，后续修改互不干扰 |
| **能力包与 Skill** | 能力包按场景（私聊/群聊/触发器）授权、ZIP/GitHub/Markdown 导入不可变版本 Skill | ✅ 已实现 | 支持本地 `~/.kiko` 或 S3 兼容对象存储保存 Skill 资产 |
| **MCP 插件** | 远程 HTTP/HTTPS MCP（免认证、API Key、Bearer、OAuth）、11 个官方插件目录 | ✅ 已实现 | 本地 stdio MCP Gateway 与 CLI 插件执行处于规划中 |
| **Web 对话与任务** | 固定私聊、Web 群聊、项目文件上传与目录树、后台任务委派、待办计划、Steer/Queue 补充输入 | ✅ 已实现 | 主对话按[受理与结果规则](spec/task-package-prompt.md)反馈；任务结果自动回报，支持随时停止与归档 |
| **人机协同** | 后台任务向指定成员发起人工确认、跨员工事项交接、三列事项看板与任务空间（TaskSpace） | ✅ 已实现 | 支持在 Web 或私聊答复人工确认后沿原 Session 续跑 |
| **原消息接收反应** | 飞书、钉钉真人消息成功入队后，在原消息上反馈已收到 | [已实现](spec/channel-receipt-reactions.md) | 隔离 PostgreSQL 与供应商 HTTP 夹具；真实客户端待验 |
| **办公渠道** | 飞书（WebSocket）、钉钉（Stream）、企业微信智能机器人长连接、扫码/手动绑定、群历史共享 | ✅ 已实现 | 支持 Markdown/富文本收发与飞书话题回复；图片/语音附件暂仅占位 |
| **自动化触发** | Cron 定时任务、固定提醒、GitHub / 通用签名 Webhook 触发与群聊结果投递 | ✅ 已实现 | 同一事件或同一期计划严格幂等去重 |
| **沙箱执行** | Mac / Linux 持久项目目录沙箱、6 个内置文件工具与 5 个受管 Bash 工具（支持 PTY） | ✅ 已实现 | 当前为同账号本地进程，容器级安全隔离处于规划中 |


[已实现模块行为规范](spec/README.md)，包含相应实现与回归用例。

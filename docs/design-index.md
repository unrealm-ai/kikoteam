# 设计与开发文档索引

本页面向贡献者和编码代理，汇总各项设计文档的入口。使用者请从[文档首页](README.md)开始阅读。

> [!NOTE]
> 设计文档记录方案、取舍和验收证据，其中部分章节描述的是目标设计或后续范围。功能是否已经可用，以[文档首页的功能状态](README.md#功能状态)和各组件 README 为准。

## 产品设计

| 主题 | 文档 |
| --- | --- |
| AI 员工整体产品 | [AI 员工平台产品设计](product/digital-employees.md) |
| 员工模板与市场 | [AI 员工市场与员工模板设计](product/digital-employee-market.md)、[API、数据库与关键流程](../backend/docs/agent-template-market.md) |
| 官方模型与积分 | [产品设计](product/official-models-and-credits.md)、[Platform modelproxy 技术设计](../backend/docs/model-proxy.md) |
| 会话与执行底座 | [Agent 会话与执行底座产品需求](product/agent-sessions.md) |
| 能力包、Skill 与 MCP | [资源与能力包设计](../backend/docs/platform-capabilities.md)、[插件授权与执行](../backend/docs/platform-plugins.md) |
| 选人与群共享 | [统一选人与跨渠道群共享](product/identity-selection-and-group-sharing.md) |
| 私聊项目文件 | [会话文件契约](../backend/docs/conversation-files.md) |

## 架构

| 主题 | 文档 |
| --- | --- |
| 整体架构 | [整体架构速览](architecture/README.md)、[Backend 系统模块](architecture/system-modules.md) |
| 领域模型与数据 | [领域模型](architecture/domain-model.md)、[数据模型与状态机](architecture/session-data-model.md)、[数据归属](architecture/data-ownership.md) |
| 生命周期 | [业务会话与共享环境生命周期](architecture/conversation-lifecycle.md) |
| 运行时与隔离 | [运行时、沙箱与接管](architecture/runtime-and-isolation.md)、[裸 Sandbox](../sandbox-runner/docs/local-sandbox.md) |
| 存储 | [统一存储配置](architecture/storage-configuration.md)、[路径约定](architecture/storage-paths.md) |

当前执行链为 `Engine → pi --mode rpc → 模型/MCP`。架构手稿 [architecture-sketch-v4.png](architecture/assets/architecture-sketch-v4.png) 中仍保留早期手稿标注，现已统一使用 Pi 原生 RPC。

## 执行底座详细设计

- [Backend 注册与 Session 分配](../backend/docs/engine-dispatch.md)
- [Engine 与 Pi RPC 运行时](../agent-engine/docs/session-runtime.md)
- [Runner 常驻管理与 stdio MCP 代理](../sandbox-runner/docs/runtime-and-mcp.md)（stdio Gateway 后续实现）
- [builtin MCP 工具复用与接入](../sandbox-runner/docs/builtin-mcp.md)
- [最小会话底座运行说明](operations/minimal-agent-runtime.md)：机器契约及当前限制以此为准

## 接口与协议

- [API 接口设计](../backend/docs/http-api.md)，包括入口分区、认证和契约组织
- [Session、Input 与 Turn API](../backend/docs/session-api.md)
- [会话事件协议 v1](../backend/docs/session-events.md)：type 是预设计目录，按功能需要逐步实现，不代表全部已经支持
- [task_delegation MCP](../backend/docs/task-delegation-mcp.md)：主 chat 创建、查询、追加和停止任务

## 业务流程

- [对话到投递](flows/conversation-and-task.md)
- [触发、分配与接管](flows/triggers-and-dispatch.md)
- [人工确认与事项交接](flows/human-and-handoff.md)
- [Web 会话导航](flows/web-conversation-navigation.md)、[Web 群聊](flows/web-group-conversations.md)

## 数据库与发布

PostgreSQL 迁移由 Event Hub 统一执行。Event Hub 通过会话级 advisory lock 选举，当选实例在持锁连接上执行全项目迁移，然后启动业务工作。Backend 和 Engine 独立启动，不以迁移状态作为启动条件。详见 [event-hub 的启动与数据库迁移](../event-hub/README.md#启动与数据库迁移)和[退出与接管](../event-hub/README.md#退出与接管)。

## 开发规则

- [开发规则索引](../AI_RULES.md)、[静态检查](rules/static-checks.md)

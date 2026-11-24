# 已实现行为规范

全项目功能与状态见[文档中心的能力总览](../README.md)。这里是与代码及回归用例对应的模块行为基线；讨论中的变更见 [proposal](../proposal/)。

| 模块 | 行为基线 | 验证范围 |
| --- | --- | --- |
| 平台服务配置 | [邮件与注册、GitHub 插件 OAuth](platform-service-settings.md) | 自动化夹具和隔离 PostgreSQL；未验证真实 SMTP 送达或真实 GitHub 授权 |
| 本地启动器 | [无宿主机 Python 的 Compose 启停与安装引导](local-launcher.md) | macOS 真机 Compose；macOS/Linux CLI 黑盒（含模拟 Docker 安装） |
| 镜像发布 | [主分支与版本标签的 AMD64 镜像发布](tagged-multiarch-release.md) | 推送到主分支后构建三个服务镜像，通过 AMD64 验收后更新部署指针 |
| 渠道接收反应 | [飞书、钉钉原消息接收反应](channel-receipt-reactions.md) | 隔离 PostgreSQL、供应商 HTTP 夹具与钉钉实测通过；飞书真实贴表情待验 |
| 主对话与任务提示词 | [委派回话与受理回执、员工身份和能力包文案](task-package-prompt.md) | 主对话规则单测、受理回执的隔离 PostgreSQL 与原生 Pi 协议集成验证；身份与能力包文案经测试模型端到端验证 |
| 钉钉回复 | [原会话回复、主动投递与缺权限提示](dingtalk-session-reply.md) | 本地渠道协议夹具、隔离 PostgreSQL；真实账号待验 |
| 入站附件 | [四入口异步准备与任务按需读取](async-inbound-attachments.md) | 隔离 PostgreSQL、Engine/Pi 端到端及 100 MiB 边界测试通过；真实渠道尚未全部通过 |
| 主对话工具恢复 | [任务状态、写入回执和回复产物](chat-tool-recovery.md) | MCP 错误负载、任务规则和隔离 PostgreSQL 的候选授权回归；未用真实 IM 交付验收 |
| 任务详情链接 | [三入口复用、实时投影、普通任务详情链接、主动回告与 Hook／定时结果通知](public-task-detail-links.md) | 隔离 PostgreSQL、HTTP/SSE、组件测试和三渠道协议夹具通过；真实客户端卡片显示、打开链接及部署代理待验 |

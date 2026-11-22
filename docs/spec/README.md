# 已实现行为规范

全项目功能与状态见[文档中心的能力总览](../README.md)。这里是与代码及回归用例对应的模块行为基线；讨论中的变更见 [proposal](../proposal/)。

| 模块 | 行为基线 | 验证范围 |
| --- | --- | --- |
| 平台服务配置 | [邮件与注册、GitHub 插件 OAuth](platform-service-settings.md) | 自动化夹具和隔离 PostgreSQL；未验证真实 SMTP 送达或真实 GitHub 授权 |
| 本地启动器 | [无宿主机 Python 的 Compose 启停与安装引导](local-launcher.md) | macOS 真机 Compose；macOS/Linux CLI 黑盒（含模拟 Docker 安装） |

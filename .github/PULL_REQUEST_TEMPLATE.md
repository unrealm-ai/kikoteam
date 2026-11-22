## 改动概述 (Summary)

<!-- 请简明说明本次 PR 解决的问题或新增的可观察行为。如关联 Issue，请注明 Fixes #编号 -->

## 涉及组件 (Affected Components)

- [ ] `web`（AI 员工工作台与管理控制台）
- [ ] `backend`（平台管理、会话/任务调度、HTTP API 与内部 MCP）
- [ ] `event-hub`（PostgreSQL 选举与迁移、IM 渠道长连接、定时触发）
- [ ] `agent-engine`（Pi RPC 运行时、配置装配与会话执行）
- [ ] `sandbox-runner` / `builtin-mcp`（沙箱进程管理、内置文件与 Bash 工具）
- [ ] `tools` / `deploy` / `.github`（启动脚本、容器构建与 CI 工作流）
- [ ] `docs`（文档更新）

## 变更细节与兼容性 (Details & Compatibility)

- **核心行为变化**：
- **数据库迁移 / 契约变更**（如有）：
  - 是否新增 `event-hub/database/postgres/migrations/` 迁移文件并更新结构快照？
  - 是否修改了公开或内部 OpenAPI 契约（`backend/contracts/`）？
- **已有数据或升级影响**（如有）：

## 测试与验证记录 (Verification)

<!-- 请勾选并列出你在本地实际执行通过的检查命令，未覆盖或受环境限制的部分请如实注明 -->

- [ ] `make lint` 静态与契约检查通过
- [ ] 受影响组件的 `make check`（含真实隔离 PostgreSQL `-race` 测试）通过
- [ ] 前端测试与构建通过（`cd web && npm test && npm run build`，如涉及 `web`）
- [ ] 真实浏览器 / 本地服务重启端到端验证（如涉及 UI 动线或跨组件联调）

**实际执行的验证命令：**

```sh
# 在此粘贴实际运行通过的命令，例如：
# make lint
# BACKEND_TEST_DATABASE_URL=... make check COMPONENT=backend
```

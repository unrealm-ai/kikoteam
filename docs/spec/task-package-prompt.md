# 主对话与任务提示词

| 编号 | 可观察行为 |
| --- | --- |
| TASK-PROMPT-01 | 任务的员工身份按创建任务时的员工版本固定；之后改名不更改已有任务的身份，新任务使用修改后的身份。主对话使用当前员工身份，改名不清空对话历史。 |
| TASK-PROMPT-02 | 任务提示词中的能力包名称和说明来自当前执行轮次的能力包快照。能力包改名或改说明后，从尚未建立快照的下一轮生效，包括旧任务尚未开始的第一轮；已取得快照的轮次不重读文案。 |
| CHAT-PROMPT-01 | 当前对话与可靠信息足够时直接回答。后台任务明确受理后，用一句自然的话说明要替用户做什么，不默认复述任务 ID、排队或文件未生成；结束当前轮次，不轮询，不承诺未经确认的交付方式、额外通知或时间。三条例句只用于普通新任务已经明确受理且用户未查询进度时，按当前目标换词；需要回告时才说明会给用户什么结果，不暗示已经启动或完成。失败或不明时沿原规则说明事实与下一步。 |
| CHAT-PROMPT-02 | 仅立即创建任务的 MCP 成功回执（含获准去重重放）返回 `accepted: true` 和原 Task、Session、Input ID，无瞬时执行概况。待办、查询、补充、停止、错误和恢复提示保持原契约。 |

[主对话提示词](../../backend/internal/manager/service/prompts.go) 在给 Engine 装配提示词时使用当前平台文案，已有主对话也不需迁移。它不改变执行任务提示词、委派工具名称与入参、执行授权或结果回告路径。[提示词单测](../../backend/internal/manager/service/chat_policy_test.go) 覆盖受理、错误、主动查进度，并验证主对话和任务配置分离；不等同于真实模型措辞验收。

[Manager](../../backend/internal/manager/repository/engines.go) 从 Session 读取任务的员工版本，在一轮首次装配时将获准能力包写入 Turn 快照，后续装配复用该快照。提示词只展示经当前校验且仍有可用资源的包。文案是提示词说明而不是授权；能力包成员、资源版本与权限另见[能力包设计](../../backend/docs/platform-capabilities.md)。

[Engine/Pi 端到端用例](../../backend/internal/platform/repository/manager_runtime_provider_test.go) 使用隔离 PostgreSQL 和本地测试模型，验证旧任务修改前后两轮、修改前创建但修改后才执行首轮的任务、新任务及主对话；模型端检查实际收到的新/旧文案、员工名和 Skill。快照复用由代码路径支撑，不把资源升级用例等同于运行中改文案的端到端验证。

[委派 MCP 契约](../../backend/docs/task-delegation-mcp.md#创建与追加)定义创建回执；[MCP 去重与文本回归](../../backend/internal/platform/repository/manager_delegation_test.go)核对原 ID 与结构化、文本双回执，[实际 Pi 模型请求夹具](../../backend/internal/platform/repository/manager_runtime_provider_test.go)分别检查创建回执与完整进度查询。权限和错误恢复见[主对话工具的失败恢复](chat-tool-recovery.md)。

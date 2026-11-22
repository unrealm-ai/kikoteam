# 领域模型

> **2026-11-16 关系与生命周期已确认**：一 Task 一 task Session，共享环境归 Conversation；创建、停止、归档、首版放置及三层回收按[业务会话与共享环境生命周期](conversation-lifecycle.md)。本页已同步目标设计，执行编排及相关机器契约仍待实现。

2026-11-16 资源绑定改为员工能力包，替代此前直接绑定方案；本批范围及待确认项见[资源与能力包迁移设计](../../backend/docs/platform-capabilities.md)。

本页描述当前 Agent 会话底座的概念边界，产品参数和操作范围以[需求基线](../product/agent-sessions.md)为准。Conversation / Task / Session / Sandbox 的关系及生命周期已确认，完整正文见[业务会话与共享环境生命周期](conversation-lifecycle.md)；事项交接和文件资源仍另行设计，不引入 Run。

表结构、配置快照、并发约束和状态转换见[数据模型与状态机](session-data-model.md)，当前为技术第一稿，尚未实施。

## 身份、角色与配置

| 对象 | 含义与关系 |
| --- | --- |
| Workspace、主体 | 工作区界定资源归属。每次操作核验真实用户或服务账号、工作区及资源权限。传入标识不授予访问权。 |
| 平台管理员 | 账号级 is_platform_admin，管理官方模板及其他账号的平台管理员资格；首次成功登录初始化一名。独立于工作区 owner/admin/member，详见[身份设计](../../backend/docs/platform-auth.md#平台管理员)。 |
| AI 员工 Employee（现有技术字段 agent_id） | 持续身份和一套面向任务的配置，包含岗位行为规范、ModelProfile 引用及能力包绑定；资源和使用范围通过能力包配置。 |
| CapabilityPackage | 员工的技能与插件操作组合及使用范围；工作区模板复制给员工，不自动联动。包自身版本策略尚待确认。 |
| AgentConfigVersion | 保存 Agent 配置即产生的不可变版本，无草稿或发布操作。模型仅保存选择，思考强度及其他参数跟随 Harness。 |
| ModelProfile | 平台或工作区维护的一条完整模型调用配置。新 Session 解析其当前版本，已有 Session 固定已解析版本。 |
| Skill | 工作区共享的完整 SKILL.md 目录资源，每个不可变内容版本有独立目录与 manifest；员工能力包固定引用资源/版本，不复制权威资产到员工目录。插件附带技能随插件版本固定；具体导入方案与简化路径见[资源与能力包设计](../../backend/docs/platform-capabilities.md#skill-完整目录版本与导入方案待实施评审)。 |
| Plugin | CLI 或 MCP 能力定义，包含运行或连接定义、接入参数定义、认证声明及可选配套 Skill。员工通过能力包固定插件版本及操作范围，不直接绑定插件。 |
| PluginAuth | 插件授权，保存账号、授权目标、认证方式、凭据引用及动态授权状态；账号在员工绑定时授权；同员工多个包包含同插件时共用该员工的一份账号授权，各包工具范围可不同；账号授权关系不能绕过包授予能力。 |
| 平台聊天预设、Session 快照 | chat 采用平台固定运行策略与员工所选模型，task 继续采用既有员工任务配置。创建时固定实际配置及资源版本，保存凭据引用；后续执行重验当前权限和认证状态。 |

运行模式不是可独立配置的 AgentRole 资源。当前不提供 Assistant 预设选择、两套 Agent 角色配置或特殊 Sandbox 参数。非敏感实际值称接入设置，不另建 PluginConnection；认证记录统一命名 PluginAuth。

ModelProfile 的新版本自动用于引用它的新会话，包内 Skill / Plugin 新建或新绑定时默认取最新可用版本并固定，已绑定项提示并由用户显式升级。这两种更新传播规则不能混同。凭据刷新和撤销独立于不可变配置版本。

Agent 停用关闭新会话及新输入，拒绝未开始工作，当前轮正常收尾；重新启用不恢复旧排队输入，未归档会话可以接收新输入。每个执行轮次使用员工当时选定的 Skill、MCP 版本及账号。手动升级在下一轮生效，撤销账号会阻止后续调用。

## 会话、输入与轮次

| 对象 | 标识什么 | 创建与关系 |
| --- | --- | --- |
| 业务会话 Conversation | 同一员工下的持续业务范围，拥有共享 Sandbox；记忆另行实现 | Backend 创建元数据；首次聊天创建唯一主 chat Session，首次任务执行创建唯一当前 Sandbox |
| 任务 Task | 一件业务工作的目标、来源与结果关联 | Backend 同事务建立 Task 与唯一 task Session；补充、停止后继续和故障后继续都沿用此关系 |
| 执行 Session | 独立模型上下文、配置快照及执行生命周期 | 必须归属 Conversation；chat 没有 Task，task 必须关联唯一 Task，模式和归属不可修改 |
| Sandbox | 业务会话共享工作环境 | Backend 登记唯一环境及管理 Engine；Engine 创建/释放，Runner 管内部进程；Session 仅持有使用关系 |
| Input | 已受理的一条不可原地修改的文本输入 | backend 受理后赋予身份和会话内顺序；未开始可撤回。输入不包含指定 Skill、文件引用或业务任务反馈字段。 |
| Turn | 实际处理一条 Input 的逻辑轮次 | 运行时开始处理时创建；一条 Input 最多一个逻辑 Turn，一轮可以多次调用模型和工具。 |
| 统一事件 | 正式消息、输入 / 轮次状态及工具执行事实 | [事件协议](../../backend/docs/session-events.md)定义历史和实时流共用的平铺字段与序号；关联 Session、Input 或 Turn，工具公开身份为 tool_use_id。type 是按需实现的预设计目录，不另外提供消息或工具调用查询接口。 |
| Session 停止边界 | 已受理停止所覆盖的输入范围 | 每次 stop 都按当前已受理工作推进边界，无需 key；重试会覆盖其间新增的工作，返回当前 Session，不引入控制资源或标识。 |

同一执行 Session 串行，不同任务可并行，每个活跃 Session 对应一个位于 Engine 侧的专属 Pi 进程，由 Backend 分配执行归属。首版同一 Conversation 的 task Session 固定到环境的管理 Engine 和宿主，chat 独立分配。空闲释放 Pi、Turn 结束、Task 业务完成和 Conversation 归档分别处理。后台命令归执行 Session，可跨正常 Turn 存活；stop/归档只收尾本 Session，活跃 Conversation 的 Sandbox 不做自动空闲回收。

## 生命周期与继续

| 动作 | 沿用 | 新建或变化 |
| --- | --- | --- |
| 空 task Session 创建响应丢失后再次创建 | 不保证沿用原 ID | 每次成功创建新 Task/Session，不要求 key；chat 则受业务会话内唯一主对话约束 |
| 携带首条输入的创建响应丢失 | 原 Session 和 initial_input_id | 按 Input 提交去重，同 key 不再创建会话或输入 |
| 原输入的受理响应丢失，再次提交同一请求 | 原 Input，返回当前状态 | 不产生第二次受理；去重身份相同但内容不同报告冲突 |
| 当前轮执行中追加文本 | 原 Session | 新 Input 排队，轮到时创建自己的 Turn |
| 撤回未执行输入 | 原 Input | 标记取消，不创建 Turn；已开始时撤回失败 |
| 失败或取消后继续交流 | 原 Session 和已有历史 | 提交新 Input，产生新 Turn；不设重试 Turn 操作 |
| 进程故障后继续 | 原 Session、配置快照与 Pi 原生历史 | 旧 Turn interrupted；新 Input 经 `--session` 加载 执行新 Turn，不自动续跑旧轮次 |
| 停止 Session / Task | 原 Task、Session 和历史 | 只收尾目标 Session 的当前工作；停止 chat 不停止任务，后续输入仍等待串行收尾 |
| 归档 Task / Session | 原历史、其他任务和共享环境 | 关闭目标输入并收尾；task Session 与 Task 归档保持一致，不释放整个 Sandbox |
| 归档 Conversation | 原历史、Pi 原生上下文和工作文件 | 先关闭父级准入，再归档所有关联 Session；全部停稳后回收运行资源，不自动删数据 |
| 归档后重新交流 | 旧对象只读 | 新任务创建新 Task/Session；原主对话或 Conversation 已归档时新建 Conversation，不自动复制上下文 |

## 后续业务概念

Conversation 和 Task 的最小关系及生命周期已纳入当前设计；主 chat 的[task_delegation MCP](../../backend/docs/task-delegation-mcp.md)已明确发现目标、创建、列表、查询、追加和停止六个预设计工具，复用一 Task 一 task Session 的业务用例，尚未实现。具体业务管理入口、业务完成判定及自动回报唤醒仍待细化。Delivery、WorkItem 和 AgentTemplate 保留在[完整产品规划](../product/digital-employees.md)中。首版不增加 Run，不因运行重试增加 Task 或 Session。

默认向同员工、同 Conversation 委派的 MCP 入口已设计；其他员工仍需获准名单、原始主体核验及跨员工归属方案就绪后才启用，不能凭 employee_id 绕过当前边界。[人工继续与事项交接](../flows/human-and-handoff.md)、渠道流程和架构图中的产物投递均作为后续参考，不定义本期 API 必填字段。

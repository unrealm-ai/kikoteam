# AI 员工市场 API 数据库与关键流程设计

状态：2026-11-18，首版已实现。本文承接[产品设计](../../docs/product/digital-employee-market.md)，API 已进入 OpenAPI，数据库使用迁移 000017。

核心方案：模板预设全部工作能力；保存自动累计版本，发布切换市场可见版本；创建者填写岗位参数并选择模型，通过一次普通创建请求生成独立员工。账号接入和真实任务执行复用现有能力。

## 1. 本次复核与取舍

依据[后端规则](../../docs/rules/backend.md)、[数据库规则](../../docs/rules/database.md)及[前端规则](../../docs/rules/frontend.md)，分别核对资源职责、现有接口、事务边界与失败语义。下面的简化是结合首版范围做出的设计取舍，不是规则禁止所有独立表或状态机。

| 原设计 | 简化后 | 原因 |
| --- | --- | --- |
| 模板业务新增 7 张表 | 4 张：模板、保存版本、固定资源引用、工作区导入映射 | 来源是员工的创建属性；准备和满意度反馈不再单独建表 |
| 准备记录、租约、续期、接管和准备状态错误码 | 同步请求内准备文件，最终短事务核对导入映射 | 首版没有后台受理与断点续传需求，不建立任务协调机制 |
| 官方发布时复制平台资产、记录发布候选 | 发布固定已有不可变资源版本，使用 FK 保留资产；创建时才复制到目标工作区 | 现有 Skill 已有不可变版本及完整 manifest，避免再维护一份发布资产 |
| 独立内容保存、上架、下架接口 | 模板 PATCH 承接保存和上下架，POST publish 只表达发布动作 | 普通属性更新走现有资源方法；每类模板由 11 个操作收敛为 8 个 |
| 员工来源、Web 范围各有新接口 | 扩展现有员工 GET/PATCH | 不给已有资源的属性另建接口面 |
| 创建响应包裹 agent/origin/setup，并返回下一步枚举 | 直接返回 Agent，接入概览按需查询 | 创建成功与后续配置读取分别表达，不建立向导状态机 |
| 整包 all_resources 执行策略 | 保留当前按资源与操作过滤的执行规则 | 能力全部预设不等于必须新增一种运行策略；缺账号只限制未授权工具 |
| 满意度反馈表、验证任务关联、已验证徽章 | 首版保留真实试用、调整岗位和开放团队；评价与认证功能后置 | 完成模板创建闭环不依赖额外评价系统 |
| 所有官方管理读写都锁管理员单例行 | 普通读取检查当前角色；写入锁操作者账号，提交时重验 | 初始化记录只协调初始化与角色变更，不作为市场读写全局锁 |

保留的必要约束：固定版本、工作区归属、岗位与能力包原子创建、默认全员可用、当前授权重验、管理员初始化只执行一次。它们分别支撑可复现配置、租户隔离、完整员工配置和已确定的权限行为。

## 2. 当前实现与职责

| 当前事实 | 本次处理 |
| --- | --- |
| `AgentService.Create/Update` 和 `platform_agent_versions` 已有员工与配置版本 | 复用校验、GET/PATCH 和事务内写入；新增从模板创建用例 |
| `platform_capability_packages.agent_id IS NULL` 表示能力包模板，已有包版本快照 | 完整员工模板单独建模，不挪用旧 source_template_id |
| `packageTx.ValidatePlugin` 当前要求员工账号已验证 | 拆出固定定义校验；模板创建可完整保存尚未接入的预设 |
| `SnapshotCapabilities` 和 `RuntimeService.Effective/Resolve` 已按资源过滤、按快照与当前授权取交集 | 复用，不增加 readiness_policy 或模板专用运行器 |
| Skill 已有不可变版本、manifest、存储接口及 `platform_skill_imports` | 复用版本引用、暂存登记与文件校验；复制移到最终事务外 |
| Agent 当前无员工级 Web 范围；能力包范围只收窄使用权 | 补员工级 web_access，不能仅靠包范围实现“仅自己” |
| 新建 chat 与 task 都从员工所选模型解析，既有 Session 快照不变；插件账号接口已存在 | 接入概览按员工所选模型显示对话就绪状态，官方模型须已有扣费绑定；连接账号沿用原接口 |
| 账号无平台管理员标记 | 按[身份设计](platform-auth.md#平台管理员)增加账号标记与一次性初始化 |

业务归 Backend Platform；Manager 只接入员工 Web 准入，任务、会话与执行快照继续按现有规则。Engine、Runner、Pi 不增加模板概念。Event Hub 只执行全局迁移。

## 3. API 设计

### 3.1 归属与通用约定

记 `W=/api/v1/workspaces/{workspace_id}`，`T=W/agent-templates`，`O=/api/v1/official-agent-templates`；下文 B 表示 T 或 O。

- 官方模板仅平台管理员可管理，工作区模板由该工作区 owner/admin 管理；普通成员只看获准的已发布内容。首次管理员及后续授权按身份设计，不使用部署白名单。
- 官方作者使用工作区资源时仍需该来源工作区的资源管理权。一个版本只使用一个来源工作区；官方分发通过已发布模板授权，不把来源工作区权限给使用者。
- 复用 Cookie、写操作 CSRF、统一错误体；拒绝未知可写字段和重复标量查询参数。不可见资源返回 404，已知范围内管理权不足返回 403。
- UUID 标识资源；`version` 用于主记录并发校验，`sequence` 是自动累计的内容版本号。时间用 UTC RFC 3339。
- 页码分页默认 20，page_size 为 1–100，返回 `{items,total,page,page_size}`；授权和筛选先于计数、排序和分页。
- 创建不要求 Idempotency-Key，不存请求指纹或回放响应；相同正文再次提交也可创建另一位员工。编辑使用 version，不自动换用新版本覆盖冲突。

### 3.2 模板接口

两类模板共用模型与用例；operationId 的资源名分别为 WorkspaceAgentTemplate、OfficialAgentTemplate，例如 listWorkspaceAgentTemplates。

| 方法与路径 | operationId 基名 | 契约 |
| --- | --- | --- |
| `GET B` | list…s | 200；q、category、sort_by、sort_order、page、page_size、view；默认市场视图，view=manage 仅管理者 |
| `GET B/{template_id}` | get… | 200；默认取发布内容；view=manage 另返回最新保存内容、版本指针、version 和 has_unpublished_changes |
| `GET B/{template_id}/versions` | list…Versions | 200 分页；默认仅曾发布版本，管理视图包含所有保存版本；sequence DESC/id DESC |
| `GET B/{template_id}/versions/{template_version_id}` | get…Version | 200 固定版本；草稿仅管理者可读，必须核验父子归属 |
| `POST B` | create… | content，含名称；201 模板及未发布 v1；普通创建，不自动重试 |
| `PATCH B/{template_id}` | update… | version 必填；可提交完整 content 和可选 change_note，或 recommended_rank，或 is_listed；三种更新分开提交；200 当前模板 |
| `POST B/{template_id}/publish` | publish… | version、template_version_id；200 当前模板；目标必须是最新保存版本，校验后发布并上架，不新建内容版本 |
| `DELETE B/{template_id}` | delete… | body version；仅从未发布的模板，204；已有发布版本返回 409，使用下架 |

保存 content 有变化时生成下一 sequence，更新 latest_version_id，不改变 published_version_id 或 is_listed；相同内容不生成空版本。recommended_rank 只是排序属性，不增加内容版本。PATCH is_listed=false 下架；true 重新校验并展示原 published 版本，不能顺带发布草稿。尚无发布版本时不能直接上架。

所有修改先检查当前 version；过期返回 409，客户端重新读取核对，不特设旧发布请求的结果重放。版本前提匹配且目标已相同则返回当前模板，不重复增版。发布时如果有人保存新版本，拒绝旧请求；失败保持原线上版本和状态。变更说明可空，不引入审批、手填版本号或升级类型。

市场 q 按名称、职责短句、交付物作大小写不敏感子串匹配，最多 100 字并转义 LIKE 通配符。category 为 engineering/marketing/sales/analytics/office。sort_by 为 recommended/published_at，默认 recommended DESC、published_at DESC、id DESC；sort_order 控制主排序，唯一 ID 作为最终顺序。市场只查询发布内容，草稿修改不影响搜索结果或最近发布时间。管理视图支持 status 筛选，按 updated_at DESC/id DESC 排序。

`POST W/agents/{agent_id}/agent-templates`（createAgentTemplateFromAgent）保留“另存为模板”：提交 agent_version、config_version、name，201 返回工作区模板和 v1；锁定来源工作区后读取员工及包的一致快照，剔除账号、名单、渠道和历史。版本冲突返回 409，普通创建不自动重试。

### 3.3 创建、员工属性与接入

| 方法与路径 | 操作 | 契约 |
| --- | --- | --- |
| `POST W/agents/from-template` | createAgentFromTemplate，新接口 | 201 直接返回 Agent；内部完成固定资源准备和原子创建 |
| `GET W/agents/{agent_id}` | getAgent，扩展现有接口 | 管理者响应增加只读 template_origin，包含来源版本、初始参数和来源包标识；无来源时省略该字段；普通成员不返回该管理字段 |
| `PATCH W/agents/{agent_id}` | updateAgent，扩展现有接口 | 增加 web_access；与 version 单独提交，不和配置、enabled 混用；省略时保留原范围 |
| `GET W/agents/{agent_id}/setup-status` | getAgentSetupStatus，新接口 | 管理者可读；汇总当前模型、包资源、插件账号问题及示例所需条件，只读计算，不保存状态 |
| 既有 plugin-accounts、Conversation/Task/Session 接口 | 沿用 | 连接账号、发送真实任务、读取进度与结果 |

员工使用范围和来源没有独立路由；不设置模板准备、岗位预览、模板评价接口。setup-status 是管理页需要的聚合查询，不承担流程推进，不返回人工维护的 ready 状态或 next_action 枚举。前端根据问题显示配置入口，运行时仍由现有授权路径裁决。

setup-status 返回 conversation_model、task_model 的当前可用性，当前主体的 access_allowed，按 package_id 列出的 issues，以及按示例 key 计算的 can_try。问题码限于已有事实：needs_connection、missing_operation、schema_changed、resource_unavailable、disabled；同时提供受影响资源标识和可读说明。can_try 要求执行权、两个实际模型及示例声明的资源均满足条件，不把员工模型可用推断为 chat 模型可用，也不将预检作为执行授权。

创建请求示例：

```http
POST /api/v1/workspaces/11111111-1111-4111-8111-111111111111/agents/from-template
X-CSRF-Protection: 1
Content-Type: application/json
```

```json
{
  "template_id": "22222222-2222-4222-8222-222222222222",
  "template_version_id": "33333333-3333-4333-8333-333333333333",
  "name": "支付组周报助手",
  "model_profile_id": "55555555-5555-4555-8555-555555555555",
  "parameters": {
    "project_scope": "支付项目"
  }
}
```

name 必填 1–100 字，model_profile_id 必填且属于目标工作区、已启用并验证。role/avatar/instruction_override 可选，省略继承模板，显式 null 不接受；长度沿用 2000/100/8000。参数只接受模板声明字段，应用默认值、验证必填与类型。创建正文上限 128 KiB。

不接受 packages/skills/plugins/tools/selected_capabilities、准备 ID、来源工作区、账号凭据或创建时的开放名单。后台加载指定的已发布模板版本，完整复制能力；用户已选择的旧发布版在模板仍上架且可分发时可继续创建。

响应沿用现有 Agent 字段，增加管理者可见的只读 template_origin，不额外包一层 agent 或混入接入查询结果。员工创建成功后再读取 setup-status；查询失败只影响后续页面，不撤销或重建员工。

### 3.4 模板内容和参数

保存内容含 schema_version=1、展示信息、instruction_template、参数、全部预设能力包、固定资源引用、接入说明、示例与交付样例。保存允许必填展示项暂未填齐；已填写引用、结构和无凭据约束必须成立。发布才检查完整性及分发条件。

每个包有模板内 package_key，每个资源有 resource_key。技能和插件固定到明确版本；工具固定 name 与 schema_digest。这两个 key 是模板内引用，不是请求去重键或跨工作区授权。

```json
{
  "schema_version": 1,
  "instruction_template": "为 {{project_scope}} 整理研发周报。",
  "parameters": [
    {"key": "project_scope", "type": "short_text", "required": true, "max_length": 200}
  ],
  "packages": [
    {
      "package_key": "weekly_report",
      "name": "整理研发周报",
      "onboarding_requirement": "required",
      "skills": [{"resource_key": "weekly_report_skill"}],
      "plugins": [{
        "resource_key": "work_items",
        "tools": [{"name": "list_issues", "schema_digest": "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"}]
      }]
    }
  ]
}
```

上述为内容节选。onboarding_requirement=required/deferred 只影响接入说明；两个值的能力都完整复制，不改变执行权限。示例声明需要的 package_key，用于试用前提示依赖；它不修改任务状态或运行时授权。

参数首版只做声明占位符的文本替换，不执行表达式、命令或脚本。前端根据固定模板内容即时预览，提交时后端重新渲染；用户改写后的岗位指令通过 instruction_override 显式提交。参数不替换插件 endpoint 或认证头。

普通市场 DTO 只返回岗位、参数、能力说明、resource_key 与接入要求，剔除内部来源工作区和资源 ID、存储路径、凭据。插件分发只复制允许的远程定义字段，沿用当前已支持协议；不从账号探测结果扩大预设操作。

首版不保存 validation_task_id、验证证据或满意度评价，不展示“已验证”徽章。示例内容明确标注为样例；真正的试用结果沿现有任务记录展示。

边界上限保留为首版建议：20 个能力包、50 个不同资源、20 个岗位参数、10 个示例，内容 JSON 256 KiB；需复制的技能资产合计 20 MiB、500 文件，同时受现有 Skill 校验限制。参数 key 用 `[a-z][a-z0-9_]{0,63}`，替换后指令仍受现有 8000 字限制。只沿用必要的输入和资源限制，不建立可配置规则引擎。

### 3.5 错误与重试

| 场景 | 处理 |
| --- | --- |
| 输入、未知字段或参数非法 | 400，沿用统一错误体并定位字段 |
| 未登录、无管理权、资源不可见 | 401/403/404，按真实权限边界 |
| 模板/员工 version 冲突 | 409 template_revision_conflict / agent_version_conflict，重新读取核对 |
| 下架、固定依赖失效、模型不可用 | 409 template_unavailable / template_dependency_unavailable / model_unavailable |
| 尚不支持模板要求的运行方式 | 422 template_runtime_unsupported |
| 文件读取或复制失败 | 503 template_storage_unavailable；若发生在员工事务前，明确未创建员工 |

不设置准备中、租约失效、发布证据冲突或幂等键错误码。插件探测继续使用已有错误语义；账号保存成功但缺少模板操作时，接入概览列出缺失操作，不宣称能力完整。

创建 POST 不自动重试。提交中禁用重复按钮；超时、断线或提交结果未知时保留表单，提示核对现有员工列表和详情；暂未查到记录不证明失败，再次提交可能创建另一位员工。已获得 201 和员工 ID 后只刷新该员工或接入状态。

## 4. 数据库设计

### 4.1 模板业务新增四张表

沿用 PostgreSQL、Platform 表归属和全局迁移。UUID 主键由服务生成，所有资源/账号/工作区 ID 为 UUID；scope、kind、resource_key 为 text，时间为 timestamptz；除注明可空外均非空。以下是设计，不是已执行 DDL。

| 表 | 主要字段 | 约束与用途 |
| --- | --- | --- |
| platform_agent_templates | id；scope；owner_workspace_id 可空；version bigint=1；latest_version_id；published_version_id 可空；is_listed boolean=false；recommended_rank int=0；created_by；created_at/updated_at | scope=official/workspace；official 无 owner、workspace 有 owner；上架要求 published 非空 |
| platform_agent_template_versions | id；template_id；sequence bigint；content JSONB；change_note text 默认空；created_by；created_at；published_by/published_at 可空 | UNIQUE(template_id,sequence)、UNIQUE(template_id,id)；保存正文不可变；首次发布仅补发布人和时间，不复制另一份正文 |
| platform_agent_template_version_resources | template_version_id；resource_key；kind；source_workspace_id；skill_id/skill_version_id 或 plugin_id/plugin_version_id | PK(template_version_id,resource_key)；固定引用既有不可变版本，随保存版本一次写入、不更新 |
| platform_agent_template_resource_imports | workspace_id；template_version_id；resource_key；kind；skill_id/skill_version_id 或 plugin_id/plugin_version_id；created_at | PK(workspace_id,template_version_id,resource_key)；仅登记官方模板已成功导入的目标资源，不存准备进度、租约或候选状态 |

主表两个版本指针以 (id,version_id) 复合 FK 引用版本的 (template_id,id)，延迟检查支持一次事务创建模板和 v1。version、sequence 必须大于零，content 为 JSON object，published_by/published_at 同空或同非空。schema_version 只存在 content 中，不再维护重复列或整份内容指纹。

资源表 kind 为 skill/plugin，用 CHECK 保证只填对应一组，复合 FK 指向既有 (workspace_id,skill_id,id) 或 (workspace_id,plugin_id,id)。工作区模板的来源必须同工作区；官方模板的私有来源引用仅供服务端分发。所有保存版本的资源目录与 content.resource_key 一致性由保存事务核验。

导入表 (template_version_id,resource_key) FK 指向版本资源，并用目标工作区复合 FK 校验目标资源归属。资源类型必须与来源匹配。工作区模板直接复用固定引用，不登记重复导入行；官方模板不同员工复用相同目标映射，不按同名合并资源。

账号、工作区和模板的实际引用均设置 FK；上述可延迟的版本指针使用 NO ACTION，其余默认 RESTRICT。从未发布的模板允许在同一事务删除其主记录、保存历史和资源引用，提交时不得留下悬空指针。已发布模板只下架，已发布版本和正在使用的资产不硬删除。

状态由指针计算：无 published 为草稿；有 published 且 is_listed 为已发布；否则已下架。管理端 has_unpublished_changes 由 latest 与 published 是否不同计算。草稿不会改变市场的旧版本内容和状态。

### 4.2 扩展已有记录

| 表 | 新字段 | 语义 |
| --- | --- | --- |
| platform_agents | source_agent_template_version_id UUID 可空 FK → 模板版本；template_parameters JSONB 默认 {} | 创建来源和初始参数，只在从模板创建时填写；普通员工为空，来源与当前配置分开，后续编辑不改写创建事实 |
| platform_agents | web_access JSONB | 兼容字段，模板新员工沿用工作区成员范围 |
| platform_capability_packages | source_package_key text 可空 | 对应员工来源版本中的包标识；当前员工同 key 唯一，旧 source_template_id 仍只指能力包模板 |

参数为 JSON object，保存补齐默认值后的初始参数，不保存秘密；没有模板来源时参数必须为空。source_package_key 仅服务端填入，校验员工确有模板来源且该 key 存在；普通包为空。只使用包 key 标识初始来源，不在每个包重复存模板版本、模板 JSON 或独立来源记录。来源 key 可随现有包快照保存，旧快照缺该字段不影响执行。员工及全部包同事务生成；后续增加普通包或修改包不强行恢复模板。

web_access 沿用既定员工范围语义：mode=workspace/allowlist，allow_member_ids 和 deny_member_ids 各最多 100 个有效 Web user_id，deny 优先；不接受渠道 open_id。字段默认 workspace，当前准入依据统一 usage_config。GET 响应按调用权限过滤管理字段；PATCH web_access 与 version 单独提交，只推进员工 version，不改变模型配置版本或启停代次。

从模板和空白创建员工时，统一 usage_config 初始为全员可用且黑名单为空。模板能力包初始 scope 使用 inherit_with_blacklist 且名单为空，继承员工准入并保留预设工具限制。

平台管理员仍保留账号标记、角色并发版本和一个初始化单例表；它支撑首位用户的一次性授予，字段与事务归[身份设计](platform-auth.md#拟新增的数据库字段与初始化记录)。

### 4.3 资产保留、索引和清理

官方模板可以直接引用官方 Skill/Plugin 的不可变版本，创建员工时把同一组 ID 写入员工能力包。模板引用空间自建资源时，内部记录来源工作区和不可变版本。发布完成即形成对这份内容的分发授权；普通用户只能经模板创建用例获得目标工作区副本，不能访问来源工作区接口或任意源目录。公开 DTO 不暴露这些私有引用。

版本 FK 保留源资源版本及 Skill 资产，首版现有资源也不支持硬删除；后续资产 GC 必须计入模板版本引用。发布之后来源工作区资源的启停不改写已发布官方版本，官方停止分发通过下架管理；工作区模板和目标已导入资源仍遵守各自当前启停状态。账号始终不在分发范围中。文件损坏、不可读或存储身份变化时报错，不静默换版本或回退存储。

模板分发的空间自建 Skill 文件复制到目标工作区版本目录，发布阶段不额外复制平台资产。候选先登记、文件完整校验后再提交资源引用；复用既有 platform_skill_imports，见创建流程。不存在额外 publication_candidates JSON 台账。

索引只覆盖实际查询：模板的 scope/owner/is_listed 与排序字段、版本的 (template_id,sequence DESC,id DESC)、导入映射唯一键、员工来源版本索引、非空包 key 的 (agent_id,source_package_key) 唯一索引。首版不引入搜索服务，实施时以代表性数据核对列表执行计划。

不增加自动清理 worker。现有暂存清理只清其暂存目录；已引用版本目录必须保留。仅能在明确事务未提交且候选不被任何版本或导入映射引用时清理该候选；提交结果未知时保留并核对。存储残留不能通过删除已发布资产来“补偿”。

## 5. 关键流程

### 5.1 保存与发布

1. 保存取得对应管理权限：工作区使用现有工作区锁；官方写操作先锁操作者账号并核验平台管理员，再锁来源工作区和模板。校验 version、内容结构和固定引用。
2. content 有变化时，以模板锁内最大 sequence+1 插入不可变保存版本及资源引用，更新 latest 并推进 version。相同内容不增版；编辑不移动 published。
3. 发布同样按 version 核验管理权、目标等于 latest、岗位完整性和固定资源可分发性。依赖既有已完成发布的 Skill/Plugin 版本及 manifest，不复制文件，不执行工具或试用任务。
4. 一次短事务补首次发布时间/发布人，更新 published 指针和 is_listed，推进主记录 version。已经是相同目标且前提仍匹配则返回当前值；过期请求统一返回 409。
5. PATCH 上下架只改变 is_listed；重新上架沿用 published，不发布未保存或尚为草稿的修改。

保存 v1 → 发布 v1 → 保存 v2/v3 时市场仍显示 v1 → 发布 v3 后市场切换到 v3。旧版本创建向导固定原版本；下架阻止新员工创建，不删除已有员工。

### 5.2 从模板创建

```text
CreateFromTemplate(ctx, workspace, actor, request)
  validate request and current workspace management permission
  load exact published template version; validate model and parameters
  resolve fixed workspace references or existing official import mappings
  for missing official Skill assets:
    register candidate using existing Skill import staging
    copy and verify files outside the final transaction
  begin short transaction; lock target workspace
    recheck actor, model, template listing and fixed version
    re-read official import mappings
    use committed mappings, or insert complete target resources and mappings
    render final instruction
    insert Agent + initial config version + source fields + default unified usage
    insert every preset package + fixed tools + source_package_key + snapshot
  commit; return Agent
```

来源及所有包属于员工创建事务；不能串调多次各自提交的 AgentService.Create 和 PackageService.Create。抽取现有纯校验和事务内写入供一个创建用例使用，避免第二套员工规则。

文件准备复用现有 Skill 导入的暂存登记和 manifest/存储接口；需要给内部 SkillSource 增加 agent_template 来源及目标候选元数据，但不增加对外 import 步骤。登记 import 时保存模板版本、resource_key、拟使用的目标资源 ID 与最终目录；再写暂存、复制到唯一候选版本目录并校验。最终事务只消费本请求准备的、未过期、ready 且 storage_identity 匹配的候选，同时写目标 Skill/Version、导入映射、员工、能力包，并标记对应 import 的发布引用。暂存 key_prefix 保持暂存目录语义，不能改成正式目录后交给现有 Cleanup 删除。Plugin 只复制获准定义字段，不探测账号，不复制凭据。

不能直接调用目前把 Assets.Copy 放在 SQL 事务中的 SkillService.Publish；需要复用底层能力，把本用例的文件 I/O 提前。也不沿用其发布指纹作为员工请求去重标识。现有 Skill 导入 API 的契约保持不变。

两个并发创建可以各自准备候选文件；最终都经目标工作区锁重新读取导入映射，已有映射则复用，只有一份目标资源进入数据库。失去使用机会的候选按无引用文件处理；员工请求仍各自创建独立员工。首版接受少量重复文件准备的成本，以避免租约、心跳和接管机制。

准备失败不会创建员工；最终事务明确回滚时，新增资源行、映射、员工及包一起回滚。进程退出后没有待执行创建任务；用户重新提交是新的请求。文件候选不会因客户端断线而被冒险删除，已有映射也不因用户关闭向导而删除或重启用。

模板内容及引用校验在锁内完成；完整文件校验在事务外。请求沿用可取消 context 和现有 Skill 导入的有界超时，不另定续期、心跳或准备 TTL。部署 HTTP/代理时间预算需覆盖所采用的同步准备预算；首版不假返回 202。

### 5.3 账号接入和运行

1. 创建时完整保存预设的 Skill、Plugin 版本和工具清单；资源定义必须合法，但账号缺失不使配置保存失败。
2. 用户通过既有员工 plugin-accounts 接口连接账号。复用真实 initialize/tools/list、加密、两阶段鉴权与版本检查；请求不包含能力勾选。
3. setup-status 比较当前固定定义、预设工具摘要和账号状态，列出未连接、操作缺失、schema 变化、资源停用等事实。连接成功不等于全部工作都能完成。
4. 新任务继续使用现有资源过滤和快照逻辑；未授权工具不能进入可执行集合，其余已授权资源按原规则可用。创建时复制完整能力不等于接入前已具备执行权。
5. 执行时继续取旧快照与当前同一包、主体和账号授权的交集；后接入或新增的权限只用于新任务，不能扩大旧任务。无整包 all_resources 策略、独立 ready 开关或模板运行器。

试用按钮根据示例声明的所需包和资源提示未满足条件；真正执行仍沿用 Manager。Task 成功与岗位满足预期由用户通过真实交付判断，不写另一套任务评价状态。结果页提供继续使用、调整岗位与开放团队；不自动开放范围或创建任务。

### 5.4 员工 Web 使用范围

Platform 依据有效成员、员工启用状态和统一使用范围裁决网页准入。Manager 在普通私聊、固定私聊、Task、Session、Input、队列执行与内部委派复用该判断。模型、技能和工具在领取运行配置或调用时重验员工及原始主体。

管理权不等于执行权或他人私聊读取权；渠道继续使用原渠道身份和范围，不套用 Web 名单。修改范围复用 Agent PATCH 和 version；收窄只限制失权主体的后续调用，已发出的外部调用无法回滚，不对仍有权者推进启停代次。扩大范围不扩展旧任务快照。

## 6. 并发、兼容与实现边界

锁顺序：官方管理写入先锁操作者账号，随后工作区（多个按 ID 排序）→ 模板 → 员工 → 能力包。普通工作区业务沿原工作区锁顺序；Manager 保持工作区 → 员工 → Conversation → Session。不持模板锁调用 Manager，不在持业务锁时复制文件或探测外部账号。

| 竞争或失败 | 处理 |
| --- | --- |
| 两人保存，或发布期间有新保存 | version 与模板锁裁决，旧请求 409，不覆盖或发布未审阅版本 |
| 下架与创建并发 | 最终创建事务持模板锁；先提交的一方确定本次是否允许新建 |
| 两次相同创建正文 | 按两次普通创建处理，均成功可得到两位完整员工 |
| 多请求需要同版官方资源 | 最终工作区锁与导入唯一约束裁决映射，候选文件可重复准备 |
| 成员、模型、资源、平台管理员资格变化 | 写入事务重验对应当前权限和状态；官方写锁操作者账号，与该账号撤权按事务顺序裁决 |
| 创建成功后概览加载失败 | 保留员工成功身份，只重读详情或 setup-status |
| 数据库提交结果未知 | 不重放创建、不删除候选，先核对现有资源；X-Request-ID 仅用于日志追踪 |

实现只扩展实际职责：Platform 模板管理和创建用例、Repository 同事务写入、现有 Skill 资产适配、账号角色、员工访问判断；Controller 做解析和错误映射，app 只装配。文件按实际规模拆分，不预建准备服务、反馈服务或通用事件/任务框架。

确认后更新手写 OpenAPI 3.1 与客户端；Agent GET/PATCH 新字段按权限和省略语义兼容旧调用方。只有显式传 web_access 才走范围更新分支，旧配置/启停请求保留行为。模板创建返回资源对象，正常员工创建路径不变。SkillSource 新来源在正式资源 DTO 中若可见，需同步核对枚举及客户端显示分支；候选目录与计划 ID 只留服务端，不作为可写 API 字段。

新增全局迁移由 Event Hub 执行，保留旧 SQL 并生成结构快照；Backend 不增加数据库版本启动闸门。旧员工保持原 Web 范围，旧包运行策略不变。账号初始化升级规则归身份设计。

模板创建写入员工默认统一使用策略。显式收窄的范围继续由 Backend 与运行时当前权限检查执行。

## 7. 实现验收

以下为实现验收要求，已执行项目和证据见实施记录：

- API：官方/工作区权限分开，草稿不外露；普通创建无请求键及自动重试；既有 Agent GET/PATCH 兼容；不额外开放准备、来源、范围或反馈路由。
- 版本：保存自动累计，空保存不增版，发布不增版；草稿不改变旧市场内容；历史发布版本可固定创建，未发布版本不行。
- 数据库：四张模板表的空库/存量迁移、复合 FK、来源字段、导入唯一约束；并发保存、发布/下架与创建竞争；任一步明确回滚无半成员工。
- 资产：官方固定源版本被保留且不暴露私有引用；目标工作区资源可独立使用；并发只提交一份导入映射；丢弃候选与已引用目录严格区分，提交未知不误删。
- 接入：全量预设保存，无勾选；缺账号可创建但不能调用未授权工具；旧包过滤语义不变，接入后新任务生效、旧任务不扩权。
- 范围：默认全员可用。显式成员限制覆盖对话、任务、队列、委派、模型和工具路径。私聊历史仍按会话归属授权。
- 试用：真实用户发送后才执行；对话模型与任务模型分开判断；静态交付样例不作为执行证据，首版无满意度记录或已验证徽章。
- 失败：模型/资源失效保留表单，重复点击被阻止，网络未知不自动重发，201 后页面失败只刷新已有员工。
## 内置官方模板的发布与维护

Backend 编译时嵌入 `internal/platform/service/catalog/` 的三类资源。`skills/manifest.json` 记录 11 个 Skill 的来源、许可和 SHA-256，`plugins/manifest.json` 固定 11 个 MCP 的地址、认证和授权操作，`templates/` 保存 7 个岗位的发布内容。根清单维护模板顺序和整个目录的发布修订号。更新任一清单或资产时递增修订号，以 `listed: false` 下架模板并保留历史版本。

Event Hub 完成迁移 79 后，Compose 使用 `backend sync-catalog` 发布目录。`sync-templates` 继续兼容已有部署脚本。维护任务和 Backend 需使用同一个 DATABASE_URL、资产卷、UID/GID 或 S3 配置。同步先写入并校验固定版本的 Skill 文件，再在一个数据库事务中切换官方资源、模板和能力引用。并发部署使用同一把目录行锁。中途失败可重跑。

目录修订 10 将「内容运营编辑」模板的首次试用样例更新为 AI 员工用语。升级只为有变化的官方模板发布新版本，不重写既有版本或工作区副本；重跑同一修订不重复增版。同步命令拒绝低于数据库目录修订号的旧二进制。

新工作区不会自动创建行情简报员，配置模型也不会触发创建。需要时，用户可在 AI 员工市场打开「行情简报员」，选择「用此模板创建」或「一键体验免认证行情」。普通模板创建保留可延后接入的业务包，接口传 `required_packages_only: true` 可仅安装必需包。此变更不影响 Kiko 助手的启用设置和创建流程。

迁移 108 取消待创建标记，并让新工作区默认不再排队。滚动升级时先由 Event Hub 应用迁移，以停止旧 Backend 自动创建行情简报员；迁移前手动添加仍会清除待创建标记，避免旧实例随后重复创建。已创建的员工、能力包和连接不受影响。回退此迁移只恢复后续新工作区的旧默认值，不会把已有工作区重新加入队列。

真实主聊天验收入口在 Backend 的 `TestLiveBuiltinMarket`、`TestLiveBuiltinGitHub` 和 `TestLiveBuiltinOffice`。通过 `BACKEND_TEST_DATABASE_URL` 指向隔离的 PostgreSQL，使用 `KIKO_LIVE_MODEL_E2E=1` 和自有测试模型的 `DEEPSEEK_*` 配置运行。GitHub 用例通过 `KIKO_LIVE_GITHUB_COMMIT` 校验独立读取的 `octocat/Hello-World` 默认分支提交 SHA。设置 `KIKO_LIVE_GITHUB_TOKEN` 可验证 PAT。要验证网页授权，留空该变量，由测试将 GitHub App 凭据写入隔离数据库，传入 `KIKO_LIVE_TEST_GITHUB_CLIENT_ID`、`KIKO_LIVE_TEST_GITHUB_CLIENT_SECRET` 和回调地址 `http://127.0.0.1:18089/api/v1/auth/callbacks/plugin-oauth`。在 Backend 目录执行 `go test -v ./internal/platform/repository -run '^TestLiveBuiltinGitHub$' -count=1`，选择网页授权时打开测试输出的本地链接。

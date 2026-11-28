# 平台默认插件与官方文生图设计

**已通过火山方舟真实出图的本地端到端验收，待部署。** 本文记录产品行为、接口、数据结构和验收条件。首版接入一个供应商和一个生效的文生图模型，一次调用生成一张图片。文中的 **5 积分/张** 是定价示例，实际单价由平台管理员配置。

## 平台管理与员工体验

平台为所有数字员工维护同一份默认插件清单。管理员在「设置 → 平台管理 → 默认插件」配置文生图的供应商、模型、API Key、每张图的价格和限额，测试成功后启用。已有员工和新建员工都自动继承，包括没有岗位能力包、使用自备聊天模型的员工。日后增加的默认插件沿用同一套分发机制。

| 入口 | 用户看到的行为 |
| --- | --- |
| 平台管理 | 显示发行版本、待配置、已启用、已停用或具体异常。管理员可测试草稿、保存、调价、停用和查询用量。测试用一张小样验证实际出图能力，页面提示会产生供应商成本。 |
| 数字员工详情 | 「平台默认能力」只读展示，例如“文生图 · 官方预置 · 5 积分/张”。有员工管理权限的人可以查看付款绑定状态。工作区所有者可以为缺少绑定的员工确认“由我的积分支付”。 |
| Web 或办公渠道对话 | 用户直接说“生成一张横版招聘海报”。主对话委派后台任务，用户可以继续聊天。任务完成后，Web 回复提供图片预览和下载，办公渠道按原消息或话题交付。 |
| 我的积分 | 分别列出“对话模型”和“文生图”消耗。文生图流水展示员工、生成时间、成功张数、受理单价、实际扣分和生成记录 ID。 |

默认插件只有平台一级配置。工作区和员工无需安装、绑定插件业务账号或选择是否继承。工作区 ID 用于任务归属、访问隔离、限额计数和用量统计。既有官方 GitHub、飞书等插件继续按员工能力包和共享或独享连接授权。[现有插件授权与执行](../../backend/docs/platform-plugins.md)

## 平台升级与生效时点

| 时点 | 固定什么 | 结果 |
| --- | --- | --- |
| 激活发行目录 | 插件稳定标识、版本、执行入口、默认分发范围、场景、工具名及 schema 摘要 | 全部署使用同一份已激活的清单。定义与管理员保存的凭据、价格和启停选择分别维护。 |
| 开始 task 轮次 | 本轮有效的默认插件版本和工具集合 | 已有任务的下一轮、新任务的第一轮均按当前目录继承。已经开始的轮次继续使用自己的快照。 |
| 受理一次新生成 | 可信任务主体、付款账户、实际模型及配置版本、每张图的价格 | 同一轮里的两次新生成可以按各自受理时的价格结算。同一生成的重试沿用原记录。 |

1. **发布工具定义。** 官方发行清单显式声明 `distribution=all_agents`。文生图使用稳定标识 `image-generation`、执行类型 `platform_managed` 和受信任的 `image_generation` handler。只有官方清单可以声明全员默认。空间自建、手动复制的插件或供应商临时增加的工具均不能获得此授权。
2. **先具备执行能力，再激活清单。** Event Hub 执行增量迁移，部署支持新契约的 Backend、Engine 和 Web。启动同步和显式目录同步都要遵守同一激活顺序。滚动发布期间，应先退出仍可能接到任务的旧实例，或验证其兼容待激活版本。目录在一次事务中切换修订。同一修订可重复同步，失败时保留上一次完整目录。
3. **保留管理员选择。** 目录同步只在需要时初始化缺失的部署配置，保留既有停用、单价和凭据。后续新增的默认插件已满足运行条件时随版本生效。需要新凭据的插件显示「待配置」，配置一次后全员继承。
4. **按轮次装配，按调用撤权。** Manager 在 `manager_turns.capability_snapshot` 中记录 `default_plugins[]`，Engine 通过 Pi 原生 MCP 配置挂载获准工具。旧格式快照缺少该字段时按空集合读取。每次发起生成，Backend 重验当前开关、员工、付款成员、任务归属和实际来源。私聊校验发起人，群聊校验发起人和群，定时或 Hook 校验触发器身份。尚未开始的新调用在停用后立即被拒绝，已受理调用按原记录收尾。
5. **回退工具版本。** 优先通过平台开关停用有问题的插件。需要恢复旧契约时，以更高的目录修订重新发布兼容的工具版本。存量生成记录、价格快照和产物版本继续可查。升级全程无需批量改写员工、模板或岗位能力包。[官方目录同步与修订](../../backend/docs/agent-template-market.md#内置官方模板的发布与维护) · [任务轮次快照实现](../../backend/internal/manager/repository/engines.go) · [能力包使用场景](../../backend/docs/platform-capabilities.md#能力包按场景授权)

## 单张图片的费用

新请求由工作区的在任所有者付款。付款人取员工现有的 `platform_agent_model_keys.payer_account_id`。员工的聊天模型、消息发送人、渠道和触发方式均不改变付款人。新员工沿用创建时的绑定。老员工缺少绑定时显示「已继承，待绑定付款账户」，允许工作区所有者显式绑定自己的钱包。读取员工、系统升级及后台触发都不会代办首次绑定。付款成员退出工作区后拒绝新请求，已受理请求仍按原付款人结算。[员工付款绑定与金额精度](../../backend/docs/model-proxy.md)

| 情形 | 账务处理 |
| --- | --- |
| 受理与定价 | 首版方形、横版、竖版同价。应扣金额等于受理单价乘以成功保存张数，每次最终为 0 张或 1 张。1 积分等于 1,000,000 units，数据库使用 `bigint`，API 使用十进制字符串。 |
| 生成中调价 | 以 5 积分受理后改为 8 积分，原图成功扣 5，下一次新生成成功扣 8。轮次快照和目录同步均不锁定后续请求的单价。 |
| 余额准入 | 沿用钱包的正余额准入、事后结算。余额 2 积分可以受理单价 5 积分的一张图，成功后为 −3，之后的新生成被拒绝。并发时多笔已放行请求都可能完成。 |
| 一张可信结果 | Backend 取得通过格式、尺寸和大小校验的图片并保存为可恢复的固定文件版本后，确认 1 张用量。供应商仅返回“已受理”、临时 URL 或成功状态码时，尚无可结算结果。 |
| 重试或重发 | 同键同参取回原生成记录，余额降至零后仍可按权限取回原结果。预览、下载、重新发布或重新投递同一原图均不扣费。用户要求再画一版时使用新键，另计一次。 |
| 明确失败或结果未知 | 明确未出图记 0。供应商可能已受理但无法确认时暂不扣分，标为待核对。找回并保存原图后按原价结算，确认原图不可恢复后关闭为零收费。 |
| 管理员测试 | 候选配置的测试成本由平台承担，不写员工积分流水，不占工作区的员工出图额度。结果未知时不自动重发测试请求。 |

平台可以设置全平台最大并发和统一的每工作区每日张数上限。日额度按 UTC 受理日计算，配置页显示重置时间。各 Backend 副本须在同一数据库准入临界区中检查和占用名额，确保最后一个名额只放行一次。同键重试复用原占用，明确失败释放原受理日额度，未知结果保留当日占用直至核对。员工钱包的正余额检查与供应商用量上限独立。

## 执行、交付与恢复

员工间转派和整改走发行清单的 `work` 场景。合法接手任务在新轮次继承官方文生图。每次请求复验当前事项分配及接手权限，按接手员工的付款绑定计费。原图写入本环节的任务项目。接手员工通过 `artifact_publish` 发布原图，再用 `work_item_submit_result` 把原图随成果提交。登录工作台后，有该任务读取权限的用户可在任务详情预览、下载已成功提交的同一文件版本。

1. **委派和查重。** 主 chat 委派给 task Session。Engine 将 Backend 的内部 MCP 入口及绑定当前 Input 和执行分配的短期凭据交给该任务的 Pi。Backend 核实当前主体和任务访问权，按 `(workspace_id, task_id, request_key)` 查重，再比较规范化业务参数指纹。命中原请求时只返回获准查看的原记录。仅对新生成检查付款绑定、正余额、平台开关与额度。
2. **受理和发送。** 在短事务中写入 `admitted` 记录、配置及单价快照和截止时间，提交后才调用供应商。只有取得 `admitted → dispatched` 条件转换的执行者可以发送。上游支持幂等键时传入 `generation_id`，并尽早保存供应商请求 ID。进程在发送前后失联时，补偿按原记录核对。无法判断是否已送达时进入待核对，避免第二次付费请求。
3. **接收与保存。** Backend 按供应商协议解码图片字节，或从供应商受信任的下载目标取得图片。应用限制格式、实际字节数和解码尺寸，遵守既有产物大小上限。将固定原图版本与 `generation_id` 建立可恢复的存储关联，再将工作副本原子写入 `outputs/images/<generation_id>.png`。会话任务使用共享项目目录，TaskSpace 任务使用自己的独立目录。即使存储成功后、数据库确认前进程崩溃，也能按原生成 ID 核对同一原图。
4. **结算。** 先锁生成记录，再锁其固定的钱包。在一个事务内按快照单价扣减余额、递增钱包版本、插入唯一图片流水并更新记录为 `settled`。核对金额与余额的整数运算范围。提交结果未知时按生成记录和唯一流水查询。原图已保存但结算暂时失败，记录保持 `ready / pending`，补偿只重试账务。
5. **交付。** 任务调用 `artifact_publish(path)` 发布固定版本。Web 根据受信任的产物元数据加载图片卡片。独立 TaskSpace 的用户可从任务详情预览和下载最近发布的 20 张图片。办公渠道从回报的候选产物中通过 `artifact_select` 选择，沿原 Inbox 和话题投递。工作文件、固定原图和渠道回执各自可追踪。工作副本丢失可从原图恢复，投递失败重投同一产物。下载和重新交付时复核当前访问权。[项目文件与渠道投递](../../backend/docs/conversation-files.md)
6. **补偿和关闭。** Event Hub 周期触发 Backend 有界扫描超时和待结算记录，生成与账务状态由 Backend 写入。已受理任务即使停止或归档，仍按实际生成证据收尾。找回原图只补文件或结算。人工处理与迟到回执竞争时锁同一生成记录，已结算或已关闭为零收费的终态不被覆盖。运维日志记录生成 ID、耗时、模型配置版本和错误码，避免输出 Key、临时下载地址和完整提示词。

首版适配 OpenAI Images 兼容协议。Backend 发送 `POST /images/generations`，请求 PNG、张数 1，正方形为 1024 × 1024，横版和竖版为 1536 × 1024 与 1024 × 1536。支持 `b64_json` 和供应商同源 HTTPS 临时地址。未知结果可以核对共享持久卷中按生成 ID 保存的原图；该协议没有通用的上游任务找回接口。日额度以 UTC 零点重置。[供应商适配器](../../backend/internal/platform/imagegen/provider.go) · [原图与工作副本](../../backend/internal/manager/service/generated_image.go)

服务地址包含网关的 API 版本前缀（例如 `/v1`）。对默认返回 CDN 链接、支持内嵌图片的兼容网关，管理员可勾选「要求网关直接返回图片（Base64）」。测试和员工调用都会携带 `response_format=b64_json`。修改此选项须重新测试。

火山方舟北京节点的服务地址填写 `https://ark.cn-beijing.volces.com/api/v3`，Backend 会拼接生成接口的路径。向 Seedream 请求 `output_format=png` 和 `response_format=b64_json`，从生成响应中读取原图。

## 接口

### 管理与查询 HTTP API

表中路径统一省略 `/api/v1`。平台设置仅限平台管理员。管理写操作沿用登录态、Origin 和 CSRF 检查，配置携带当前 `version`，版本过期返回 `409`。已有账单和员工接口按兼容方式扩展。[平台资源契约](../../backend/contracts/openapi.yaml) · [现有流水契约](../../backend/contracts/modules/platform/model-proxy.yaml)

| 方法与路径 | 可操作主体 | 输入、输出及行为 |
| --- | --- | --- |
| `GET /platform/default-plugins` | 平台管理员 | 列出默认插件、发行版本、有效状态、配置缺项和单价。 |
| `GET /platform/default-plugins/image-generation` | 平台管理员 | 返回当前供应商、型号、单价、额度、并发上限、配置版本和 `credential_configured`。凭据只写。 |
| `POST /platform/default-plugins/image-generation/test` | 平台管理员 | 对候选配置实际出图，返回预览、绑定候选目标、型号及凭据的短期验证回执和过期时间。测试失败保留在线配置。 |
| `PATCH /platform/default-plugins/image-generation` | 平台管理员 | 首次启用或修改供应商、地址、模型、Key 时校验匹配的测试回执。单独调价、改额度和紧急停用可直接保存。同一目标省略 Key 表示保留，更换供应商或服务地址须提交新 Key。 |
| `GET /workspaces/{w}/agents/{a}/setup-status`（扩展） | 沿用员工查看权限 | 返回只读 `default_plugins[]`，展示能力来源、单价、可用状态和原因。员工管理视图按权限展示付款绑定。 |
| `PUT /workspaces/{w}/agents/{a}/payer-binding` | 在任工作区所有者 | 提交员工 `version` 和 `confirm_self=true`。首次绑定当前登录人，相同绑定可重放，已绑定其他账户时返回 `409`。与既有员工首次绑定使用同一事务约束。 |
| `GET /accounts/{id}/credit-ledger?kind=image_charge`（扩展） | 付款人本人或平台管理员 | 增加文生图过滤，返回员工、张数、受理单价、扣款、扣后余额及生成 ID。 |
| `GET /image-generations` | 本人付款记录，平台管理员可显式选择全局 | 按员工、时间、生成状态和计费状态过滤，游标分页。账务视图只含元数据，提示词、私聊和原图仍受任务权限保护。 |
| `POST /image-generations/{id}/resolve` | 平台管理员 | 提交 `action=reconcile` 或 `close_no_charge` 并说明原因。记录操作者及处理时间。重新结算须有可恢复原图，终态冲突返回 `409`。 |
| `GET /workspaces/{w}/tasks/{task_id}/artifacts/{artifact_id}/content` | 当前任务产物的合法读者 | 为 Web 提供指定产物版本的图片预览或下载，逐次校验工作区、任务、产物归属和读取权限。 |

例如，要将单价设为 5 积分，可向配置接口提交下列 PATCH 请求体。成功返回下一版本及完整生效配置。

```json
{
  "version": 7,
  "enabled": true,
  "credits_per_image": "5.000000",
  "max_concurrent_generations": 8,
  "workspace_daily_image_limit": 100
}
```

### 员工文生图 MCP

内部端点为 `POST /mcp/platform-plugins/image-generation`。task Session 通过原生 MCP 的 `tools/call` 调用 `generate_image`。工作区、付款人、供应商型号、输出目录和投递目标均从已验证的运行上下文或平台配置取得。供应商 API Key 仅在 Backend 出站时解密，不进入 Pi、Sandbox、工具结果或轮次快照。模型只传画面描述、可选画幅和本次出图意图的稳定键。以下为工具调用的 `params` 示例。

```json
{
  "name": "generate_image",
  "arguments": {
    "prompt": "春季招聘海报，绿色主色，右侧留出中文标题区域",
    "aspect_ratio": "landscape",
    "request_key": "spring-hiring-poster-v1"
  }
}
```

成功结算后，工具返回下列业务数据。员工取得项目相对路径即可发布图片，纯文本聊天模型也可交付原图。

```json
{
  "generation_id": "018f5000-0000-7000-8000-000000000001",
  "status": "ready",
  "path": "outputs/images/018f5000-0000-7000-8000-000000000001.png",
  "mime_type": "image/png",
  "width": 1536,
  "height": 1024,
  "image_count": 1,
  "credits_per_image": "5.000000",
  "billing_status": "settled",
  "charged_credits": "5.000000"
}
```

原图已保存、扣款待恢复时返回 `billing_status=pending` 和 `charged_credits=null`。同键改提示词或画幅返回 `idempotency_conflict`。其他可操作的业务原因包括 `payer_binding_required`、`insufficient_credits`、`plugin_disabled`、`daily_limit_reached`、`concurrency_limit_reached` 和 `generation_pending_review`。前端依原因展示补绑定、补积分、等待或核对原记录的入口。

## PostgreSQL 表与约束

首版新增三张表，共用现有官方插件、员工付款绑定、钱包、流水、轮次快照和任务产物。员工与工作区均无需默认插件绑定表。以下字段与约束用于实施评审。

### 官方目录及轮次快照的扩展

| 现有表 | 字段和兼容规则 |
| --- | --- |
| `platform_plugins` | 复用官方归属和稳定 `builtin_key`。文生图升级、改名沿用同一插件 ID。 |
| `platform_plugin_versions` | 增加 `execution_kind`、`distribution`、`handler_key`、`default_tools jsonb`（工具名、schema 摘要）、`default_scenes jsonb`。旧记录兼容值为 `remote / manual`。全员预置须校验官方归属、发行声明及已注册 handler，托管分支通过内部运行凭据调用，普通远程插件仍按既有连接鉴权。 |
| `manager_turns.capability_snapshot` | JSON 增加可选 `default_plugins[]`，保存插件 ID、版本 ID、工具摘要和来源。按本轮可信场景解析，旧快照兼容空列表。 |

### platform_default_plugin_settings

每项默认插件一条部署级生效记录。通用管理版本与插件专属配置版本分别维护，以便立即停用已有配置。

| 字段 | 类型与约束 | 含义 |
| --- | --- | --- |
| `plugin_id` | `uuid PRIMARY KEY`，引用 `platform_plugins.id` | 官方插件身份 |
| `enabled` | `boolean NOT NULL` | 平台总开关 |
| `version` | `bigint NOT NULL` | 管理配置的乐观锁版本 |
| `active_config_version` | `bigint NULL` | 当前专属配置版本，文生图启用时必须属于本插件且已验证 |
| `updated_by`, `updated_at` | `uuid`, `timestamptz` | 最近操作账号与生效时间 |

### platform_image_generation_config_versions

| 字段 | 类型与约束 | 含义 |
| --- | --- | --- |
| `plugin_id`, `version` | `uuid`, `bigint`，联合主键 | 可被历史生成记录引用的配置身份 |
| `provider`, `base_url`, `upstream_model_id` | `text` | 已验证的供应商、服务地址和型号 |
| `credential_ciphertext` | `bytea` | 按用途加密，供 Backend 调用供应商 |
| `unit_price_units` | `bigint CHECK (unit_price_units > 0)` | 单张价格，5 积分存为 `5000000` |
| `supported_sizes` | `jsonb` | 受支持画幅到实际尺寸的映射 |
| `max_concurrent_generations`, `workspace_daily_image_limit`, `timeout_seconds` | 正整数 | 平台并发上限、统一日额度、单次超时 |
| `validated_at`, `created_at`, `created_by` | `timestamptz`, `timestamptz`, `uuid` | 验证时间、创建时间及管理员 |

修改价格或供应商配置时，在同一事务插入新配置版本并推进生效指针。旧请求始终引用原版本。凭据撤销后，恢复流程须另行核验可用凭据与原供应商身份的对应关系。历史配置用于审计，不能借新目标的凭据查询旧供应商。

### platform_image_generations

每行对应一次员工出图意图。平台管理员测试独立于员工生成记录和钱包。

| 字段组 | 关键字段及类型 |
| --- | --- |
| 生成与可信归属 | `id uuid PK`、`workspace_id`、`agent_id`、`payer_account_id`、`task_id`、`session_id`、`input_id`、`turn_id`，各身份列为 `uuid` |
| 业务去重 | `request_key text`、`request_fingerprint bytea`。指纹覆盖规范化提示词和画幅，排除价格、凭据和传输请求编号 |
| 受理快照 | `plugin_id uuid`、`plugin_version_id uuid`、`config_version bigint`、`unit_price_units bigint`、`aspect_ratio text`、`quota_date date` |
| 上游执行 | `generation_status text`、`upstream_request_id text`、`error_code text`、`started_at`、`dispatched_at`、`deadline_at`、`finished_at`，时间戳使用 `timestamptz` |
| 固定原图 | `asset_version text`、`output_path text`、`mime_type text`、`size_bytes bigint`、`width integer`、`height integer` |
| 账务与核对 | `image_count smallint`、`billing_status text`、`settled_at timestamptz`、`resolved_by uuid`、`resolution_reason text`、`resolved_at timestamptz` |

关键约束如下。受理事务还须从真实运行关系核验 Task、Session、Input、Turn 及员工归属。

```sql
UNIQUE (workspace_id, task_id, request_key),
FOREIGN KEY (workspace_id, agent_id) REFERENCES platform_agents(workspace_id, id),
FOREIGN KEY (payer_account_id) REFERENCES platform_credit_wallets(account_id),
FOREIGN KEY (plugin_id, plugin_version_id) REFERENCES platform_plugin_versions(plugin_id, id),
FOREIGN KEY (plugin_id, config_version) REFERENCES platform_image_generation_config_versions(plugin_id, version)
```

| 执行状态 | 张数与账务状态 |
| --- | --- |
| `admitted`、`dispatched` | 张数未知，账务为 `pending`。已持久化的发送状态用于决定是否可以安全重试。 |
| `pending_review` | 尚无可信原图，张数未知，暂不扣款。核对后进入 `ready` 或 `failed`。 |
| `ready` | `image_count=1`，须有固定版本、实际大小和尺寸。账务为 `pending` 或 `settled`。已结算须有结算时间及唯一流水。 |
| `failed` | 明确失败或确认无法恢复时，`image_count=0`、账务为 `no_charge`。保留错误或人工处理原因。 |

索引至少覆盖 `(payer_account_id, started_at DESC, id DESC)` 的账单分页、`(workspace_id, quota_date, generation_status)` 的日额度计算，以及待处理记录上 `(deadline_at, id)` 的部分索引。渠道发送状态以现有产物投递回执为准。

### platform_credit_ledger 的扩展

复用 `platform_credit_wallets`、`platform_agent_model_keys` 和钱包版本序号。流水新增可空外键 `image_generation_id uuid REFERENCES platform_image_generations(id)`，并对非空值建唯一索引。既有 `kind` 检查约束需涵盖下表中的资金来源和关联条件。

| 流水类型 | 关联与金额约束 |
| --- | --- |
| 新增 `image_charge` | `image_generation_id`、`agent_id` 非空，`usage_id IS NULL`，`amount_units < 0`。账户、员工和金额须与生成记录相符，唯一 `biz_key` 取 `image:<generation_id>`。 |
| `signup_bonus`、`referral_bonus`、`admin_grant`、`model_charge` | 沿用既有约束，并要求 `image_generation_id IS NULL`。Token 用量仍写入 `platform_model_usage`。 |

现有汇总把 `model_charge` 计为消耗，其余类型归入赠送。发布时同时扩展过滤枚举、查询和 Web 标签，`spent` 汇总 `model_charge` 与 `image_charge` 的消耗金额，`granted` 汇总 `signup_bonus`、`referral_bonus` 和 `admin_grant`。应以同账户含五种流水的账单验证余额、赠送和本月消耗。[现有账本约束](../../event-hub/database/postgres/migrations/000018_platform_model_proxy.sql) · [流水读写与汇总](../../backend/internal/platform/modelproxy/repository/credits.go)

## 上线顺序与计划验收

2026-11-28 在本地隔离数据库中用 `doubao-seedream-5-5-261028` 完成了正方形图片验收。管理员试图与员工任务各产生一次真实供应商调用。测试对话模型驱动 Engine、Runner 和 Pi 调用官方 MCP，重复提交同一请求后仍只有一张图和一笔 5 积分的流水。Chrome 在任务详情中预览并下载了 1024 × 1024 的 PNG，下载内容与归档原图的 SHA-256 一致。[端到端测试](../../backend/internal/platform/repository/image_generation_runtime_test.go) · [浏览器验收](../../web/test/browser-image-delivery.mjs)

先发布兼容历史数据的账本与生成记录迁移，再部署支持新流水、原图恢复和图片展示的 Backend、Engine 和 Web。确认受影响实例兼容后，同步默认插件清单。最后由管理员验证实际供应商、设置单价并启用。以下为实施后的验收条件。

| 场景 | 预期结果 |
| --- | --- |
| 存量员工、自备聊天模型、无岗位能力包，以及新建员工 | 管理员启用一次，员工下一轮可从真实对话交付原图。无付款绑定时明确提示，供应商请求数为零。 |
| 发布第二个默认插件、重复同步或中途失败 | 无需逐员工或逐空间写绑定。已开始轮次工具稳定，新轮次取得完整生效目录。同步失败、重试和回退均保留价格、停用选择与账单。 |
| 真实出图中单价从 5 调到 8 | 旧请求扣 5，新请求扣 8，各自可查受理快照。 |
| 余额 2、单价 5，再重放原请求 | 成功后余额 −3，新生成被拒，获准的原结果仍可读取，仅有一笔扣费流水。 |
| 两个副本同键并发、争最后一个额度、结算时重启 | 同意图一条记录和一笔流水，最后一个日名额只放行一次。上游结果未知时进入核对，已有原图仅补账或恢复文件。 |
| 停用插件、撤销任务权限或付款人退出 | 后续新生成被拒，已受理调用按证据收尾。迟到回执不能重开已关闭为零收费的记录。 |
| Web、办公群及独立 TaskSpace | Web 可预览和下载固定版本，群图片回到正确话题，TaskSpace 文件归属正确。重投同一产物不产生新费用。 |
| 账务与权限隔离 | 五种流水的赠送与消耗汇总正确。付款人可核对单价和张数，其他工作区凭生成 ID 无法读图，平台账务访问权不会扩大私聊产物读取范围。 |
